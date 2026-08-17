// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"context"
	"fmt"
	"os"

	"github.com/agntcy/dir-importer/config"
	"github.com/agntcy/dir-importer/internal/utils/logging"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	searchv1 "github.com/agntcy/dir/api/search/v1"
	"github.com/agntcy/dir/client/streaming"
)

var dedupLogger = logging.Logger("importer/pipeline/dedup")

// OASF module names recognised by the deduplication lookup. MCP and A2A
// include the legacy runtime/* names so that records imported under the old
// module name are also detected as duplicates.
const (
	moduleMCPCurrent     = "integration/mcp"
	moduleMCPLegacy      = "runtime/mcp"
	moduleA2ACurrent     = "integration/a2a"
	moduleA2ALegacy      = "runtime/a2a"
	moduleAgentSkillName = "core/language_model/agentskills"
)

// modulesByImportType maps each import type to the module names that scope
// the per-item duplicate lookup.
//
// config.ImportTypeOASF has no entry here by design: a record already in
// OASF format may carry any module (MCP, A2A, Agent Skill, or none), so
// findCandidates' "unknown import type" fallback - match any known module -
// is the correct behavior for it, not an oversight.
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

// candidateLimit bounds how many existing records a single name+version
// lookup can return.
const candidateLimit = 20

// DuplicateChecker checks for duplicate records by comparing content hashes
// against existing records in the directory.
type DuplicateChecker struct {
	client     config.ClientInterface
	importType config.ImportType
	debug      bool
	transform  RecordTransformer
}

// NewDuplicateChecker creates a new duplicate checker for the given import
// type. It performs no directory queries itself - all lookups happen lazily,
// per item, in FilterDuplicates. transform is used to convert each fetched
// source item to its pre-enrichment OASF representation so it can be hashed
// the same way the directory-side record is; pass the pipeline's
// transformer.TransformRecord.
func NewDuplicateChecker(client config.ClientInterface, importType config.ImportType, debug bool, transform RecordTransformer) *DuplicateChecker {
	return &DuplicateChecker{
		client:     client,
		importType: importType,
		debug:      debug,
		transform:  transform,
	}
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
				if c.isDuplicate(ctx, source) {
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

// isDuplicate checks whether source already exists in the directory. It
// transforms the source item to its pre-enrichment OASF representation (the
// same conversion the transform stage performs), looks up existing records
// sharing that item's name and version, and compares content hashes against
// just those candidates.
func (c *DuplicateChecker) isDuplicate(ctx context.Context, source types.SourceItem) bool {
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

	name, version := recordNameVersion(record)
	if name == "" {
		// No name to scope a lookup by - can't find candidates.
		return false
	}

	refs, err := c.findCandidates(ctx, name, version)
	if err != nil {
		dedupLogger.Warn("Duplicate lookup failed; importing without a duplicate check for this item",
			"name", name, "version", version, "error", err)

		return false
	}

	if len(refs) == 0 {
		return false
	}

	existing, err := c.client.PullBatch(ctx, refs)
	if err != nil {
		dedupLogger.Warn("Failed to pull candidate records for duplicate check; importing without a duplicate check for this item",
			"name", name, "version", version, "error", err)

		return false
	}

	for _, e := range existing {
		existingHash, err := contentHash(e.GetData())
		if err != nil {
			// No hashable content on this candidate - skip it, not a match.
			continue
		}

		if existingHash == hash {
			if c.debug {
				fmt.Fprintf(os.Stderr, "[DEDUP] %s@%s is a duplicate of existing record (cid=%s, hash=%s)\n", name, version, e.GetCid(), hash)
				os.Stderr.Sync()
			}

			return true
		}
	}

	return false
}

// findCandidates queries the directory for CIDs of records sharing name and
// version, scoped to the modules relevant to c.importType. Results are
// converted to RecordRefs for PullBatch.
func (c *DuplicateChecker) findCandidates(ctx context.Context, name, version string) ([]*corev1.RecordRef, error) {
	queries := []*searchv1.RecordQuery{
		{Type: searchv1.RecordQueryType_RECORD_QUERY_TYPE_NAME, Value: name},
	}

	if version != "" {
		queries = append(queries, &searchv1.RecordQuery{Type: searchv1.RecordQueryType_RECORD_QUERY_TYPE_VERSION, Value: version})
	}

	modules, ok := modulesByImportType[c.importType]
	if !ok {
		// Unknown import type: fall back to matching any known module so that
		// deduplication is still best-effort rather than silently disabled.
		for _, m := range modulesByImportType {
			modules = append(modules, m...)
		}
	}

	for _, m := range modules {
		queries = append(queries, &searchv1.RecordQuery{Type: searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME, Value: m})
	}

	limit := uint32(candidateLimit)

	result, err := c.client.SearchCIDs(ctx, &searchv1.SearchCIDsRequest{
		Queries: queries,
		Limit:   &limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search for existing %s@%s records failed: %w", name, version, err)
	}

	cids, err := collectCIDs(ctx, result)
	if err != nil {
		return nil, fmt.Errorf("search stream error for %s@%s: %w", name, version, err)
	}

	refs := make([]*corev1.RecordRef, 0, len(cids))
	for _, cid := range cids {
		refs = append(refs, &corev1.RecordRef{Cid: cid})
	}

	return refs, nil
}

// collectCIDs drains a SearchCIDs stream into a slice of record CIDs. It
// returns as soon as the stream signals completion, reports an error, or ctx
// is cancelled - whichever comes first.
func collectCIDs(ctx context.Context, result streaming.StreamResult[searchv1.SearchCIDsResponse]) ([]string, error) {
	var cids []string

	for {
		select {
		case resp := <-result.ResCh():
			if cid := resp.GetRecordCid(); cid != "" {
				cids = append(cids, cid)
			}
		case err := <-result.ErrCh():
			return nil, err
		case <-result.DoneCh():
			return cids, nil
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		}
	}
}

// recordNameVersion extracts the top-level "name"/"version" string fields
// from a record's data, defaulting either to "" if absent. Unlike
// shared.ExtractNameVersion (which errors when name or version is missing,
// for push-time error reporting), this returns partial results so a lookup
// can still be scoped by name alone when version is unset.
func recordNameVersion(record *corev1.Record) (string, string) {
	fields := record.GetData().GetFields()
	if fields == nil {
		return "", ""
	}

	return fields["name"].GetStringValue(), fields["version"].GetStringValue()
}
