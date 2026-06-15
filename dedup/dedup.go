// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/agntcy/dir-importer/config"
	"github.com/agntcy/dir-importer/internal/utils/logging"
	"github.com/agntcy/dir-importer/shared"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	searchv1 "github.com/agntcy/dir/api/search/v1"
)

const trustedQueryValue = "true"

var dedupLogger = logging.Logger("importer/pipeline/dedup")

// OASF module names recognised by the deduplication cache. MCP and A2A include
// the legacy runtime/* names so that records imported under the old module
// name are also detected as duplicates.
const (
	moduleMCPCurrent     = "integration/mcp"
	moduleMCPLegacy      = "runtime/mcp"
	moduleA2ACurrent     = "integration/a2a"
	moduleA2ALegacy      = "runtime/a2a"
	moduleAgentSkillName = "core/language_model/agentskills"
)

// modulesByImportType maps each import type to the module names that should be
// queried when building the deduplication cache.
var modulesByImportType = map[config.ImportType][]string{
	config.ImportTypeMCPRegistry: {moduleMCPCurrent, moduleMCPLegacy},
	config.ImportTypeMCP:         {moduleMCPCurrent, moduleMCPLegacy},
	config.ImportTypeA2A:         {moduleA2ACurrent, moduleA2ALegacy},
	config.ImportTypeAgentSkill:  {moduleAgentSkillName},
}

type duplicateKind int

const (
	notDuplicate duplicateKind = iota
	duplicateTrusted
	duplicateUnsigned
)

// DuplicateChecker checks for duplicate records by comparing name@version
// against existing records in the directory. It queries only the modules that
// are relevant for the configured import type.
//
// When trackUnsigned is true, trusted and unsigned records are tracked
// separately: trusted duplicates are skipped; unsigned duplicates are reported
// via result.UnsignedDuplicateCIDs for signing or deferred signing output.
type DuplicateChecker struct {
	client          config.ClientInterface
	importType      config.ImportType
	trackUnsigned   bool
	debug           bool
	existingRecords map[string]string // map[name@version]cid when trackUnsigned is false
	trustedRecords  map[string]string // map[name@version]cid
	unsignedRecords map[string]string // map[name@version]cid
	mu              sync.RWMutex
}

// NewDuplicateChecker creates a new duplicate checker for the given import type.
// It queries the directory for all existing records of the relevant module(s)
// and builds an in-memory cache.
func NewDuplicateChecker(
	ctx context.Context,
	client config.ClientInterface,
	importType config.ImportType,
	trackUnsigned bool,
	debug bool,
) (*DuplicateChecker, error) {
	checker := &DuplicateChecker{
		client:          client,
		importType:      importType,
		trackUnsigned:   trackUnsigned,
		debug:           debug,
		existingRecords: make(map[string]string),
		trustedRecords:  make(map[string]string),
		unsignedRecords: make(map[string]string),
	}

	if err := checker.buildCache(ctx); err != nil {
		return nil, fmt.Errorf("failed to build duplicate cache: %w", err)
	}

	if debug {
		cacheSize := len(checker.existingRecords)
		if trackUnsigned {
			cacheSize = len(checker.trustedRecords) + len(checker.unsignedRecords)
		}

		fmt.Fprintf(os.Stderr, "[DEDUP] Cache built with %d existing %s records\n", cacheSize, importType)
		os.Stderr.Sync()
	}

	return checker, nil
}

// buildCache queries the directory for records belonging to the modules that
// correspond to the configured import type. It uses pagination and builds an
// in-memory cache of name@version combinations.
//
//nolint:gocognit,cyclop // Complexity is acceptable for building cache from multiple modules
func (c *DuplicateChecker) buildCache(ctx context.Context) error {
	if c.trackUnsigned {
		if err := c.buildRecordsCache(ctx, true, c.trustedRecords); err != nil {
			return err
		}

		allRecords := make(map[string]string)
		if err := c.buildRecordsCache(ctx, false, allRecords); err != nil {
			return err
		}

		for nameVersion, cid := range allRecords {
			if _, trusted := c.trustedRecords[nameVersion]; trusted {
				continue
			}

			c.unsignedRecords[nameVersion] = cid
		}

		return nil
	}

	return c.buildRecordsCache(ctx, false, c.existingRecords)
}

//nolint:gocognit,cyclop // Pagination loop over search/pull batches; splitting obscures flow.
func (c *DuplicateChecker) buildRecordsCache(ctx context.Context, trustedOnly bool, dest map[string]string) error {
	const (
		batchSize  = 100   // Process 100 records at a time
		maxRecords = 50000 // Safety limit to prevent unbounded memory growth
	)

	modules, ok := modulesByImportType[c.importType]
	if !ok {
		// Unknown import type: fall back to querying all known modules so that
		// deduplication is still best-effort rather than silently disabled.
		for _, m := range modulesByImportType {
			modules = append(modules, m...)
		}
	}

	totalProcessed := 0

	for _, module := range modules {
		offset := uint32(0)

		for {
			limit := uint32(batchSize)
			searchReq := &searchv1.SearchCIDsRequest{
				Queries: c.searchQueries(module, trustedOnly),
				Limit:   &limit,
				Offset:  &offset,
			}

			result, err := c.client.SearchCIDs(ctx, searchReq)
			if err != nil {
				return fmt.Errorf("search for existing %s records failed: %w", module, err)
			}

			cids := make([]string, 0, batchSize)

		L:
			for {
				select {
				case resp := <-result.ResCh():
					cid := resp.GetRecordCid()
					if cid != "" {
						cids = append(cids, cid)
					}
				case err := <-result.ErrCh():
					return fmt.Errorf("search stream error for %s: %w", module, err)
				case <-result.DoneCh():
					break L
				case <-ctx.Done():
					return fmt.Errorf("context cancelled: %w", ctx.Err())
				}
			}

			if len(cids) == 0 {
				break
			}

			refs := make([]*corev1.RecordRef, 0, len(cids))
			for _, cid := range cids {
				refs = append(refs, &corev1.RecordRef{Cid: cid})
			}

			records, err := c.client.PullBatch(ctx, refs)
			if err != nil {
				return fmt.Errorf("failed to pull existing %s records: %w", module, err)
			}

			c.mu.Lock()

			for _, record := range records {
				nameVersion, err := shared.ExtractNameVersion(record)
				if err != nil {
					continue
				}

				cid := record.GetCid()
				if cid == "" {
					continue
				}

				dest[nameVersion] = cid
			}

			c.mu.Unlock()

			totalProcessed += len(cids)

			if c.debug {
				fmt.Fprintf(os.Stderr, "[DEDUP] Processed %s batch: %d records (total: %d, trustedOnly=%t)\n",
					module, len(cids), totalProcessed, trustedOnly)
				os.Stderr.Sync()
			}

			if totalProcessed >= maxRecords {
				dedupLogger.Warn("Deduplication cache limit reached",
					"max_records", maxRecords,
					"message", "Some existing records may not be cached. Consider using --force to reimport.")

				return nil
			}

			if len(cids) < batchSize {
				break
			}

			offset += uint32(batchSize)
		}
	}

	return nil
}

// FilterDuplicates implements the DuplicateChecker interface.
// It filters out duplicate records from the input channel and returns a channel
// with only non-duplicate records. It tracks only the skipped (duplicate) count.
// The transform stage will track the total records that are actually processed.
func (c *DuplicateChecker) FilterDuplicates(ctx context.Context, inputCh <-chan types.SourceItem, result *types.Result) <-chan types.SourceItem {
	outputCh := make(chan types.SourceItem)

	go func() {
		defer close(outputCh)

		for {
			select {
			case <-ctx.Done():
				return
			case source, ok := <-inputCh:
				if !ok {
					return
				}

				kind, cid := c.classify(source)
				switch kind {
				case duplicateTrusted:
					result.Mu.Lock()
					result.TotalRecords++
					result.SkippedCount++
					result.Mu.Unlock()

					if c.debug {
						fmt.Fprintf(os.Stderr, "[DEDUP] %s is a trusted duplicate\n", source.NameVersion())
						os.Stderr.Sync()
					}

					continue
				case duplicateUnsigned:
					result.Mu.Lock()
					result.TotalRecords++
					result.SkippedCount++
					result.UnsignedDuplicateCIDs = append(result.UnsignedDuplicateCIDs, cid)
					result.Mu.Unlock()

					if c.debug {
						fmt.Fprintf(os.Stderr, "[DEDUP] %s is an unsigned duplicate (cid=%s)\n", source.NameVersion(), cid)
						os.Stderr.Sync()
					}

					continue
				case notDuplicate:
					select {
					case outputCh <- source:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return outputCh
}

func (c *DuplicateChecker) classify(source types.SourceItem) (duplicateKind, string) {
	nameVersion := source.NameVersion()
	if nameVersion == "" {
		return notDuplicate, ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.trackUnsigned {
		if _, exists := c.existingRecords[nameVersion]; exists {
			return duplicateTrusted, ""
		}

		return notDuplicate, ""
	}

	if _, exists := c.trustedRecords[nameVersion]; exists {
		return duplicateTrusted, ""
	}

	if cid, exists := c.unsignedRecords[nameVersion]; exists {
		return duplicateUnsigned, cid
	}

	return notDuplicate, ""
}

func (c *DuplicateChecker) searchQueries(module string, trustedOnly bool) []*searchv1.RecordQuery {
	queries := []*searchv1.RecordQuery{
		{
			Type:  searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME,
			Value: module,
		},
	}

	if trustedOnly {
		queries = append(queries, &searchv1.RecordQuery{
			Type:  searchv1.RecordQueryType_RECORD_QUERY_TYPE_TRUSTED,
			Value: trustedQueryValue,
		})
	}

	return queries
}
