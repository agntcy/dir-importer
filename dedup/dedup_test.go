// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/agntcy/dir-importer/config"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	searchv1 "github.com/agntcy/dir/api/search/v1"
	"github.com/agntcy/dir/client/streaming"
	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	fieldName    = "name"
	fieldVersion = "version"
	cidBafy1     = "bafy1"
)

// stubStreamResult is a hand-rolled streaming.StreamResult that emits the
// configured payloads (or an error) and then closes its done channel.
type stubStreamResult struct {
	resCh  chan *searchv1.SearchCIDsResponse
	errCh  chan error
	doneCh chan struct{}
}

func newStubStreamResult(cids []string, streamErr error) *stubStreamResult {
	r := &stubStreamResult{
		resCh:  make(chan *searchv1.SearchCIDsResponse, len(cids)),
		errCh:  make(chan error, 1),
		doneCh: make(chan struct{}),
	}

	go func() {
		defer close(r.doneCh)

		if streamErr != nil {
			r.errCh <- streamErr

			return
		}

		for _, cid := range cids {
			r.resCh <- &searchv1.SearchCIDsResponse{RecordCid: cid}
		}
	}()

	return r
}

func (r *stubStreamResult) ResCh() <-chan *searchv1.SearchCIDsResponse { return r.resCh }
func (r *stubStreamResult) ErrCh() <-chan error                        { return r.errCh }
func (r *stubStreamResult) DoneCh() <-chan struct{}                    { return r.doneCh }

// stubClient implements config.ClientInterface for dedup tests. The
// per-method hooks let each test customise behaviour (success, error, etc.).
type stubClient struct {
	searchFn func(ctx context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error)
	pullFn   func(ctx context.Context, refs []*corev1.RecordRef) ([]*corev1.Record, error)
}

func (s *stubClient) Push(_ context.Context, _ *corev1.Record) (*corev1.RecordRef, error) {
	return nil, errors.New("not implemented")
}

func (s *stubClient) SearchCIDs(ctx context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
	if s.searchFn != nil {
		return s.searchFn(ctx, req)
	}

	return newStubStreamResult(nil, nil), nil
}

func (s *stubClient) PullBatch(ctx context.Context, refs []*corev1.RecordRef) ([]*corev1.Record, error) {
	if s.pullFn != nil {
		return s.pullFn(ctx, refs)
	}

	return nil, nil
}

// recordWith returns a corev1.Record whose Data has the given name/version.
func recordWith(name, version string) *corev1.Record {
	return recordWithDescription(name, version, "")
}

// recordWithDescription returns a corev1.Record whose Data has the given
// name/version and, when non-empty, a "description" field. The description
// lets tests vary content independently of name@version.
func recordWithDescription(name, version, description string) *corev1.Record {
	fields := map[string]*structpb.Value{
		fieldName:    structpb.NewStringValue(name),
		fieldVersion: structpb.NewStringValue(version),
	}

	if description != "" {
		fields["description"] = structpb.NewStringValue(description)
	}

	return &corev1.Record{
		Data: &structpb.Struct{Fields: fields},
	}
}

// testTransform stands in for (*transformer.Transformer).TransformRecord in
// these tests. It builds record data directly from the source item's own
// fields (name, version, and - for MCP - description) so tests can control
// hashed content independently of name@version, without depending on the
// real OASF translator.
func testTransform(item types.SourceItem) (*corev1.Record, error) {
	switch item.Kind {
	case types.SourceKindMCP:
		return recordWithDescription(item.MCP.Server.Name, item.MCP.Server.Version, item.MCP.Server.Description), nil
	case types.SourceKindA2A:
		return &corev1.Record{Data: item.A2A}, nil
	case types.SourceKindAgentSkill:
		return &corev1.Record{Data: item.Skill}, nil
	case types.SourceKindOASF:
		// Already OASF, so the stand-in passes it through the way the real
		// transformer does. Added when #62 introduced this kind.
		return &corev1.Record{Data: item.OASF}, nil
	default:
		return nil, fmt.Errorf("testTransform: unsupported source kind: %v", item.Kind)
	}
}

func mustContentHash(t *testing.T, data *structpb.Struct) string {
	t.Helper()

	hash, err := contentHash(data)
	if err != nil {
		t.Fatalf("contentHash err = %v", err)
	}

	return hash
}

func TestFilterDuplicates_SkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	dupHash := mustContentHash(t, recordWith("dup", "1.0.0").GetData())

	c := &DuplicateChecker{
		transform:       testTransform,
		existingRecords: map[string]cacheEntry{dupHash: {cid: "bafycid", nameVersion: "dup@1.0.0"}},
	}

	in := make(chan types.SourceItem, 2)
	in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "dup", Version: "1.0.0"}})

	in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "new", Version: "2.0.0"}})

	close(in)

	out := c.FilterDuplicates(ctx, in, result)

	var passed int

	for range out {
		passed++
	}

	if passed != 1 {
		t.Errorf("passed through = %d, want 1", passed)
	}

	if result.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", result.SkippedCount)
	}

	// Dedup increments TotalRecords for duplicates; transform would add for non-dupes (not run here).
	if result.TotalRecords != 1 {
		t.Errorf("TotalRecords after dedup = %d, want 1 (duplicate only)", result.TotalRecords)
	}
}

func TestFilterDuplicates_PassThroughWhenUnknown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	c := &DuplicateChecker{transform: testTransform, existingRecords: map[string]cacheEntry{}}

	in := make(chan types.SourceItem, 1)
	in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "only", Version: "1"}})

	close(in)

	out := c.FilterDuplicates(ctx, in, result)

	n := 0

	for range out {
		n++
	}

	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	if result.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d", result.SkippedCount)
	}
}

// TestFilterDuplicates_HashCoversFullContent exercises the two directions
// called out in issue #54's motivation, and one deliberate design decision:
//
//   - "content changed, same name@version": the false-negative case - a
//     record keeps the same name@version as one already in the directory,
//     but its content (here, description) changed. Must NOT be a duplicate.
//   - "different name, otherwise identical": a design decision - the hash
//     covers the full translator output, including "name" and "version"
//     (genuine OASF fields, unlike enrichment artifacts skills/domains). So
//     two records differing only by name are never duplicates, even if every
//     other field agrees.
//   - "identical content": the base case - a record whose full content
//     (including name/version) matches one already cached IS a duplicate.
func TestFilterDuplicates_HashCoversFullContent(t *testing.T) {
	t.Parallel()

	const (
		existingName        = "dup"
		existingVersion     = "1.0.0"
		existingDescription = "original description"
	)

	existingHash := mustContentHash(t, recordWithDescription(existingName, existingVersion, existingDescription).GetData())

	tests := []struct {
		name           string
		item           mcpapiv0.ServerJSON
		wantDuplicate  bool
		wantAssertNote string
	}{
		{
			name: "content changed, same name@version -> not a duplicate",
			item: mcpapiv0.ServerJSON{
				Name: existingName, Version: existingVersion, Description: "updated description",
			},
			wantDuplicate:  false,
			wantAssertNote: "content differs; must not be treated as a duplicate",
		},
		{
			name: "different name, identical version/description -> not a duplicate",
			item: mcpapiv0.ServerJSON{
				Name: "not-" + existingName, Version: existingVersion, Description: existingDescription,
			},
			wantDuplicate:  false,
			wantAssertNote: "different name is different content, not a duplicate",
		},
		{
			name: "identical content (including name@version) -> a duplicate",
			item: mcpapiv0.ServerJSON{
				Name: existingName, Version: existingVersion, Description: existingDescription,
			},
			wantDuplicate:  true,
			wantAssertNote: "identical content must be treated as a duplicate",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			result := &types.Result{}

			c := &DuplicateChecker{
				transform:       testTransform,
				existingRecords: map[string]cacheEntry{existingHash: {cid: "bafyold", nameVersion: existingName + "@" + existingVersion}},
			}

			in := make(chan types.SourceItem, 1)

			in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: tc.item})

			close(in)

			out := c.FilterDuplicates(ctx, in, result)

			var passed int

			for range out {
				passed++
			}

			wantPassed := 1
			if tc.wantDuplicate {
				wantPassed = 0
			}

			if passed != wantPassed {
				t.Errorf("passed through = %d, want %d (%s)", passed, wantPassed, tc.wantAssertNote)
			}

			wantSkipped := 0
			if tc.wantDuplicate {
				wantSkipped = 1
			}

			if result.SkippedCount != wantSkipped {
				t.Errorf("SkippedCount = %d, want %d", result.SkippedCount, wantSkipped)
			}
		})
	}
}

// TestFilterDuplicates_FailsSafeWhenCannotHash covers the fail-safe branches
// of isDuplicate: whenever an item cannot be reduced to a content hash it must
// be treated as NOT a duplicate and passed through (so the transform stage
// processes and reports it normally) rather than silently dropped. The three
// ways hashing can be skipped are exercised here.
func TestFilterDuplicates_FailsSafeWhenCannotHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		transform RecordTransformer
	}{
		{
			name:      "nil transform",
			transform: nil,
		},
		{
			name:      "transform returns error",
			transform: func(types.SourceItem) (*corev1.Record, error) { return nil, errors.New("boom") },
		},
		{
			name:      "transform returns record with nil data",
			transform: func(types.SourceItem) (*corev1.Record, error) { return &corev1.Record{}, nil },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			result := &types.Result{}

			// A populated cache proves the pass-through is due to the
			// fail-safe branch, not an empty cache.
			c := &DuplicateChecker{
				transform:       tc.transform,
				existingRecords: map[string]cacheEntry{"somehash": {cid: "bafyx", nameVersion: "x@1"}},
			}

			in := make(chan types.SourceItem, 1)
			in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "x", Version: "1"}})

			close(in)

			out := c.FilterDuplicates(ctx, in, result)

			var passed int

			for range out {
				passed++
			}

			if passed != 1 {
				t.Errorf("passed through = %d, want 1 (un-hashable item must fail safe and pass through)", passed)
			}

			if result.SkippedCount != 0 {
				t.Errorf("SkippedCount = %d, want 0 (un-hashable item must not be skipped as a duplicate)", result.SkippedCount)
			}
		})
	}
}

func TestModulesByImportType_MCPTypes(t *testing.T) {
	t.Parallel()

	for _, importType := range []config.ImportType{config.ImportTypeMCPRegistry, config.ImportTypeMCP} {
		modules, ok := modulesByImportType[importType]
		if !ok {
			t.Errorf("import type %q has no entry in modulesByImportType", importType)

			continue
		}

		if len(modules) == 0 {
			t.Errorf("import type %q has empty modules list", importType)
		}

		for _, m := range modules {
			if m != moduleMCPCurrent && m != moduleMCPLegacy {
				t.Errorf("import type %q has unexpected module %q", importType, m)
			}
		}
	}
}

func TestModulesByImportType_A2AType(t *testing.T) {
	t.Parallel()

	modules, ok := modulesByImportType[config.ImportTypeA2A]
	if !ok {
		t.Fatal("ImportTypeA2A has no entry in modulesByImportType")
	}

	expected := []string{"integration/a2a", "runtime/a2a"}
	if len(modules) != len(expected) {
		t.Errorf("ImportTypeA2A modules = %v, want %v", modules, expected)
	} else {
		for i, m := range expected {
			if modules[i] != m {
				t.Errorf("ImportTypeA2A modules[%d] = %q, want %q", i, modules[i], m)
			}
		}
	}
}

func TestModulesByImportType_AgentSkillType(t *testing.T) {
	t.Parallel()

	modules, ok := modulesByImportType[config.ImportTypeAgentSkill]
	if !ok {
		t.Fatal("ImportTypeAgentSkill has no entry in modulesByImportType")
	}

	if len(modules) != 1 || modules[0] != "core/language_model/agentskills" {
		t.Errorf("ImportTypeAgentSkill modules = %v, want [core/language_model/agentskills]", modules)
	}
}

// ImportTypeOASF deliberately has no entry in modulesByImportType: an OASF
// record may carry any module, so buildCache's "unknown import type" fallback
// (query every known module) is the intended behavior, not an oversight.
func TestModulesByImportType_OASFTypeHasNoEntry(t *testing.T) {
	t.Parallel()

	if _, ok := modulesByImportType[config.ImportTypeOASF]; ok {
		t.Error("ImportTypeOASF should have no entry in modulesByImportType (relies on the all-modules fallback in buildCache)")
	}
}

func TestFilterDuplicates_A2ASkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	card, err := structpb.NewStruct(map[string]any{fieldName: "my-agent", fieldVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("failed to create A2A struct: %v", err)
	}

	newCard, err := structpb.NewStruct(map[string]any{fieldName: "other-agent", fieldVersion: "v2.0.0"})
	if err != nil {
		t.Fatalf("failed to create A2A struct: %v", err)
	}

	cardHash := mustContentHash(t, card)

	c := &DuplicateChecker{
		importType:      config.ImportTypeA2A,
		transform:       testTransform,
		existingRecords: map[string]cacheEntry{cardHash: {cid: "bafycid123", nameVersion: "my-agent@v1.0.0"}},
	}

	in := make(chan types.SourceItem, 2)
	in <- types.A2ASourceItem(card)

	in <- types.A2ASourceItem(newCard)

	close(in)

	out := c.FilterDuplicates(ctx, in, result)

	var passed int

	for range out {
		passed++
	}

	if passed != 1 {
		t.Errorf("passed through = %d, want 1", passed)
	}

	if result.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", result.SkippedCount)
	}
}

func TestFilterDuplicates_AgentSkillSkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	skill, err := structpb.NewStruct(map[string]any{
		fieldName:  "my-skill",
		"metadata": map[string]any{fieldVersion: "v1.0.0"},
	})
	if err != nil {
		t.Fatalf("failed to create skill struct: %v", err)
	}

	newSkill, err := structpb.NewStruct(map[string]any{
		fieldName:  "other-skill",
		"metadata": map[string]any{fieldVersion: "v2.0.0"},
	})
	if err != nil {
		t.Fatalf("failed to create skill struct: %v", err)
	}

	skillHash := mustContentHash(t, skill)

	c := &DuplicateChecker{
		importType:      config.ImportTypeAgentSkill,
		transform:       testTransform,
		existingRecords: map[string]cacheEntry{skillHash: {cid: "bafycid456", nameVersion: "my-skill@v1.0.0"}},
	}

	in := make(chan types.SourceItem, 2)
	in <- types.AgentSkillSourceItem(skill)

	in <- types.AgentSkillSourceItem(newSkill)

	close(in)

	out := c.FilterDuplicates(ctx, in, result)

	var passed int

	for range out {
		passed++
	}

	if passed != 1 {
		t.Errorf("passed through = %d, want 1", passed)
	}

	if result.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", result.SkippedCount)
	}
}

// TestNewDuplicateChecker_StripsEnrichmentFromDirectoryRecords covers the
// asymmetry the whole change rests on: records pulled from the directory are
// post-enrichment (they carry skills and domains), while items hashed on the
// import side are pre-enrichment. buildCache therefore has to hash the
// STRIPPED record, not its raw CID, or no import item can ever match a
// directory record that has been enriched.
//
// The other buildCache tests use fixtures with no skills or domains, where
// contentHash and GetCid happen to agree, so they pass either way. This one
// gives the pulled record enrichment fields, which makes the two diverge, and
// fails if buildCache stops stripping.
func TestNewDuplicateChecker_StripsEnrichmentFromDirectoryRecords(t *testing.T) {
	t.Parallel()

	enriched := recordWith("demo", "1.0.0")
	enriched.GetData().GetFields()[skillsField] = structpb.NewListValue(&structpb.ListValue{
		Values: []*structpb.Value{structpb.NewStringValue("natural_language_processing")},
	})
	enriched.GetData().GetFields()[domainsField] = structpb.NewListValue(&structpb.ListValue{
		Values: []*structpb.Value{structpb.NewStringValue("technology")},
	})

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult([]string{cidBafy1}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{enriched}, nil
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCPRegistry, false, false, nil)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	// The hash an import-side item would produce: same content, no enrichment.
	wantHash := mustContentHash(t, recordWith("demo", "1.0.0").GetData())

	if _, ok := checker.existingRecords[wantHash]; !ok {
		t.Fatalf("cache is keyed on the unstripped record, so a pre-enrichment item can never match it.\nwant key %q\ngot keys %v", wantHash, checker.existingRecords)
	}

	// And the raw CID of the enriched record must NOT be what got cached,
	// which is what distinguishes stripping from not stripping.
	if rawCID := enriched.GetCid(); rawCID != wantHash {
		if _, ok := checker.existingRecords[rawCID]; ok {
			t.Errorf("cache contains the unstripped CID %q; buildCache is not stripping enrichment fields", rawCID)
		}
	}
}

func TestNewDuplicateChecker_PopulatesCache(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult([]string{cidBafy1}, nil), nil
		},
		pullFn: func(_ context.Context, refs []*corev1.RecordRef) ([]*corev1.Record, error) {
			if len(refs) != 1 || refs[0].GetCid() != cidBafy1 {
				return nil, fmt.Errorf("unexpected refs: %v", refs)
			}

			return []*corev1.Record{recordWith("demo", "1.0.0")}, nil
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCPRegistry, false, false, nil)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	wantHash := mustContentHash(t, recordWith("demo", "1.0.0").GetData())

	entry, ok := checker.existingRecords[wantHash]
	if !ok {
		t.Fatalf("cache missing content hash %q; got %v", wantHash, checker.existingRecords)
	}

	if entry.nameVersion != "demo@1.0.0" {
		t.Errorf("cache entry nameVersion = %q, want %q (ExtractNameVersion should still be used for logging)", entry.nameVersion, "demo@1.0.0")
	}
}

// TestNewDuplicateChecker_SkipsRecordsWithoutData verifies that records with
// no Data at all (so no content to hash) are excluded from the cache. Unlike
// the old name@version scheme, a record with empty-but-present Data (no
// name/version fields) is still hashable and IS cached.
func TestNewDuplicateChecker_SkipsRecordsWithoutData(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult([]string{cidBafy1, "bafy2"}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{
				{}, // no Data - cannot be hashed, must be skipped
				recordWith("kept", "9.9.9"),
			}, nil
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeA2A, false, false, nil)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	wantHash := mustContentHash(t, recordWith("kept", "9.9.9").GetData())

	if _, ok := checker.existingRecords[wantHash]; !ok {
		t.Error("expected kept@9.9.9 content hash in cache")
	}

	if len(checker.existingRecords) != 1 {
		t.Errorf("cache size = %d, want 1 (record without data should be skipped)", len(checker.existingRecords))
	}
}

func TestNewDuplicateChecker_UnknownImportTypeFallsBackToAllModules(t *testing.T) {
	t.Parallel()

	var queriedModules []string

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			for _, q := range req.GetQueries() {
				queriedModules = append(queriedModules, q.GetValue())
			}

			return newStubStreamResult(nil, nil), nil
		},
	}

	if _, err := NewDuplicateChecker(context.Background(), client, config.ImportType("unknown-type"), false, false, nil); err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	wantModules := map[string]bool{
		moduleMCPCurrent: false, moduleMCPLegacy: false,
		moduleA2ACurrent: false, moduleA2ALegacy: false,
		moduleAgentSkillName: false,
	}

	for _, m := range queriedModules {
		if _, ok := wantModules[m]; ok {
			wantModules[m] = true
		}
	}

	for m, seen := range wantModules {
		if !seen {
			t.Errorf("module %q was not queried in fallback", m)
		}
	}
}

func TestNewDuplicateChecker_SearchError(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return nil, errors.New("dial failed")
		},
	}

	_, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, false, false, nil)
	if err == nil {
		t.Fatal("expected error from SearchCIDs failure")
	}
}

func TestNewDuplicateChecker_StreamError(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult(nil, errors.New("stream broke")), nil
		},
	}

	_, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeAgentSkill, false, false, nil)
	if err == nil {
		t.Fatal("expected error from stream ErrCh")
	}
}

func TestNewDuplicateChecker_PullBatchError(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult([]string{cidBafy1}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return nil, errors.New("pull failed")
		},
	}

	_, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, false, false, nil)
	if err == nil {
		t.Fatal("expected error from PullBatch failure")
	}
}

func TestNewDuplicateChecker_ContextCancelledDuringStream(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			r := &stubStreamResult{
				resCh:  make(chan *searchv1.SearchCIDsResponse),
				errCh:  make(chan error),
				doneCh: make(chan struct{}),
			}

			cancel()

			return r, nil
		},
	}

	_, err := NewDuplicateChecker(ctx, client, config.ImportTypeMCP, false, false, nil)
	if err == nil {
		t.Fatal("expected error when context is cancelled mid-stream")
	}
}

func TestNewDuplicateChecker_ForceSkipsCacheBuild(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			t.Fatal("SearchCIDs must not be called when force is true")

			return nil, errors.New("unreachable")
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			t.Fatal("PullBatch must not be called when force is true")

			return nil, errors.New("unreachable")
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCPRegistry, false, true, nil)
	if err != nil {
		t.Fatalf("NewDuplicateChecker(force) err = %v", err)
	}

	if len(checker.existingRecords) != 0 {
		t.Errorf("cache size = %d, want 0 when force skips buildCache", len(checker.existingRecords))
	}
}
