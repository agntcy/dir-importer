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
//
// config.ImportTypeOASF has no entry here by design: a record already in OASF
// format may carry any module (MCP, A2A, Agent Skill, or none), so buildCache's
// "unknown import type" fallback — query every known module — is the correct
// behavior for it, not an oversight.
var modulesByImportType = map[config.ImportType][]string{
	config.ImportTypeMCPRegistry: {moduleMCPCurrent, moduleMCPLegacy},
	config.ImportTypeMCP:         {moduleMCPCurrent, moduleMCPLegacy},
	config.ImportTypeA2A:         {moduleA2ACurrent, moduleA2ALegacy},
	config.ImportTypeAgentSkill:  {moduleAgentSkillName},
}

// RecordTransformer converts a fetched source item into its OASF record
// representation, the same conversion the pipeline's transform stage
// performs, but run here (pre-enrichment) purely to obtain content to hash
// for duplicate detection. Implemented by (*transformer.Transformer).TransformRecord.
type RecordTransformer func(types.SourceItem) (*corev1.Record, error)

// cacheEntry pairs a cached record's real CID with its name@version. The
// name@version is kept only for debug logging - shared.ExtractNameVersion is
// preserved for that purpose even though it is no longer part of the
// duplicate-detection key.
type cacheEntry struct {
	cid         string
	nameVersion string // best-effort; "" if the record had no name/version fields
}

// DuplicateChecker checks for duplicate records by comparing content hashes
// against existing records in the directory, instead of name@version. It
// queries only the modules that are relevant for the configured import type.
type DuplicateChecker struct {
	client          config.ClientInterface
	importType      config.ImportType
	debug           bool
	transform       RecordTransformer
	existingRecords map[string]cacheEntry // map[content hash]cacheEntry
	mu              sync.RWMutex
}

// NewDuplicateChecker creates a new duplicate checker for the given import type.
// It queries the directory for all existing records of the relevant module(s)
// and builds an in-memory cache keyed by content hash. transform is used to
// convert each fetched source item to its pre-enrichment OASF representation
// so it can be hashed the same way the directory-side cache is; pass the
// pipeline's transformer.TransformRecord.
func NewDuplicateChecker(ctx context.Context, client config.ClientInterface, importType config.ImportType, debug bool, transform RecordTransformer) (*DuplicateChecker, error) {
	checker := &DuplicateChecker{
		client:          client,
		importType:      importType,
		debug:           debug,
		transform:       transform,
		existingRecords: make(map[string]cacheEntry),
	}

	if err := checker.buildCache(ctx); err != nil {
		return nil, fmt.Errorf("failed to build duplicate cache: %w", err)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEDUP] Cache built with %d existing %s records\n", len(checker.existingRecords), importType)
		os.Stderr.Sync()
	}

	return checker, nil
}

// buildCache queries the directory for records belonging to the modules that
// correspond to the configured import type. It uses pagination and builds an
// in-memory cache keyed by content hash.
//
//nolint:gocognit,cyclop // Complexity is acceptable for building cache from multiple modules
func (c *DuplicateChecker) buildCache(ctx context.Context) error {
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
			// Search for records with this module with pagination
			limit := uint32(batchSize)
			searchReq := &searchv1.SearchCIDsRequest{
				Queries: []*searchv1.RecordQuery{
					{
						Type:  searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME,
						Value: module,
					},
				},
				Limit:  &limit,
				Offset: &offset,
			}

			result, err := c.client.SearchCIDs(ctx, searchReq)
			if err != nil {
				return fmt.Errorf("search for existing %s records failed: %w", module, err)
			}

			// Collect CIDs from this batch
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

			// No more results for this module
			if len(cids) == 0 {
				break
			}

			// Convert CIDs to RecordRefs
			refs := make([]*corev1.RecordRef, 0, len(cids))
			for _, cid := range cids {
				refs = append(refs, &corev1.RecordRef{Cid: cid})
			}

			// Batch pull records from this batch
			records, err := c.client.PullBatch(ctx, refs)
			if err != nil {
				return fmt.Errorf("failed to pull existing %s records: %w", module, err)
			}

			// Build the cache: content hash -> cacheEntry(cid, name@version).
			// Pulled records have already been through enrichment, so their
			// enrichment-added fields (skills, domains) are stripped before
			// hashing to make the hash comparable with the pre-enrichment
			// hash computed on the import side.
			c.mu.Lock()

			for _, record := range records {
				hash, err := contentHash(record.GetData())
				if err != nil {
					// No hashable content (e.g. record has no data) - skip.
					continue
				}

				// Preserved for debug logging only; a missing name/version no
				// longer excludes a record from the cache.
				nameVersion, _ := shared.ExtractNameVersion(record)

				c.existingRecords[hash] = cacheEntry{cid: record.GetCid(), nameVersion: nameVersion}
			}

			c.mu.Unlock()

			totalProcessed += len(cids)

			// Debug logging for batch progress
			if c.debug {
				fmt.Fprintf(os.Stderr, "[DEDUP] Processed %s batch: %d records (total: %d)\n", module, len(cids), totalProcessed)
				os.Stderr.Sync()
			}

			// Safety check: prevent unbounded memory growth
			if totalProcessed >= maxRecords {
				dedupLogger.Warn("Deduplication cache limit reached",
					"max_records", maxRecords,
					"message", "Some existing records may not be cached. Consider using --force to reimport.")

				return nil
			}

			// If we got fewer results than requested, we've reached the end
			if len(cids) < batchSize {
				break
			}

			// Move to next batch
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

				// Check if duplicate
				if c.isDuplicate(source) {
					result.Mu.Lock()
					result.TotalRecords++
					result.SkippedCount++
					result.Mu.Unlock()

					continue
				}

				// Not a duplicate - pass it through (transform stage will count it)
				select {
				case outputCh <- source:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return outputCh
}

// isDuplicate checks if a source item is a duplicate of an existing directory
// record by comparing content hashes. It transforms the source item to its
// pre-enrichment OASF representation (the same conversion the transform stage
// performs) and hashes that.
//
// Name and version are part of the hashed content, not stripped from it. Two
// records are duplicates when their whole pre-enrichment content matches,
// which includes matching on name@version. The change from the previous
// name@version-only comparison is that content is now checked too: records
// sharing a name@version but differing in content are no longer treated as
// duplicates, and an updated record under an unchanged name@version is
// correctly seen as new.
//
// c.transform must be non-nil for FilterDuplicates to detect anything;
// NewDuplicateChecker's contract is to always set it. It is nil-guarded here
// only because tests build DuplicateChecker literals directly (some legitimately
// don't exercise FilterDuplicates and pass nil), so a nil transform fails
// safe - "not a duplicate" - instead of panicking.
func (c *DuplicateChecker) isDuplicate(source types.SourceItem) bool {
	if c.transform == nil {
		return false
	}

	record, err := c.transform(source)
	if err != nil {
		// Can't transform, so can't hash - not a duplicate. The transform
		// stage will process (and report) this item's error normally.
		return false
	}

	hash, err := contentHash(record.GetData())
	if err != nil {
		// Can't determine - not a duplicate (will be processed)
		return false
	}

	// Check if a record with this content hash already exists
	c.mu.RLock()
	entry, exists := c.existingRecords[hash]
	c.mu.RUnlock()

	if exists && c.debug {
		nv := source.NameVersion()
		if nv == "" {
			nv = "(unknown)"
		}

		existingNV := entry.nameVersion
		if existingNV == "" {
			existingNV = "(unknown)"
		}

		fmt.Fprintf(os.Stderr, "[DEDUP] %s is a duplicate of existing record %s (cid=%s, hash=%s)\n", nv, existingNV, entry.cid, hash)
		os.Stderr.Sync()
	}

	return exists
}
