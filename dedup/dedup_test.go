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
	testVersion  = "1.0.0"
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

// queryNameValue returns the value of the NAME query in req, or "" if none is
// present. Lets tests key stub responses off what dedup actually asked for
// (e.g. "only return a candidate when the NAME query is 'dup'") instead of
// ignoring the request entirely.
func queryNameValue(req *searchv1.SearchCIDsRequest) string {
	for _, q := range req.GetQueries() {
		if q.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_NAME {
			return q.GetValue()
		}
	}

	return ""
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

func mustStruct(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()

	st, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("structpb.NewStruct err = %v", err)
	}

	return st
}

func TestFilterDuplicates_SkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			if queryNameValue(req) == "dup" {
				return newStubStreamResult([]string{cidBafy1}, nil), nil
			}

			return newStubStreamResult(nil, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{recordWith("dup", testVersion)}, nil
		},
	}

	c := &DuplicateChecker{client: client, transform: testTransform}

	in := make(chan types.SourceItem, 2)
	in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "dup", Version: testVersion}})

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

	c := &DuplicateChecker{client: &stubClient{}, transform: testTransform}

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
//   - "different name, otherwise identical": a design decision - the lookup
//     is scoped by name, so a differently-named item finds no candidates at
//     all and is therefore never a duplicate, even if every other field
//     agrees.
//   - "identical content": the base case - a record whose full content
//     (including name/version) matches the existing candidate IS a duplicate.
func TestFilterDuplicates_HashCoversFullContent(t *testing.T) {
	t.Parallel()

	const (
		existingName        = "dup"
		existingVersion     = testVersion
		existingDescription = "original description"
	)

	existingRecord := recordWithDescription(existingName, existingVersion, existingDescription)

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			if queryNameValue(req) == existingName {
				return newStubStreamResult([]string{cidBafy1}, nil), nil
			}

			return newStubStreamResult(nil, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{existingRecord}, nil
		},
	}

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
			wantAssertNote: "different name finds no candidates, so it's not a duplicate",
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

			c := &DuplicateChecker{client: client, transform: testTransform}

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

			c := &DuplicateChecker{client: &stubClient{}, transform: tc.transform}

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
// record may carry any module, so findCandidates' "unknown import type"
// fallback (match every known module) is the intended behavior, not an
// oversight.
func TestModulesByImportType_OASFTypeHasNoEntry(t *testing.T) {
	t.Parallel()

	if _, ok := modulesByImportType[config.ImportTypeOASF]; ok {
		t.Error("ImportTypeOASF should have no entry in modulesByImportType (relies on findCandidates skipping the module filter)")
	}
}

// TestIsDuplicate_OASFFindsModuleLessCandidate: a bare OASF record with no
// module must still be found as a duplicate.
func TestIsDuplicate_OASFFindsModuleLessCandidate(t *testing.T) {
	t.Parallel()

	existing := recordWith("bare-agent", testVersion)

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			for _, q := range req.GetQueries() {
				if q.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME {
					t.Errorf("unexpected module query %q for ImportTypeOASF", q.GetValue())
				}
			}

			return newStubStreamResult([]string{cidBafy1}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{existing}, nil
		},
	}

	c := NewDuplicateChecker(client, config.ImportTypeOASF, false, testTransform)

	item := types.OASFSourceItem(mustStruct(t, map[string]any{fieldName: "bare-agent", fieldVersion: testVersion}))

	if !c.isDuplicate(context.Background(), item) {
		t.Fatal("expected a module-less OASF record to still be found as a duplicate")
	}
}

func TestFilterDuplicates_A2ASkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	card := mustStruct(t, map[string]any{fieldName: "my-agent", fieldVersion: "v1.0.0"})
	newCard := mustStruct(t, map[string]any{fieldName: "other-agent", fieldVersion: "v2.0.0"})

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			if queryNameValue(req) == "my-agent" {
				return newStubStreamResult([]string{cidBafy1}, nil), nil
			}

			return newStubStreamResult(nil, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{{Data: card}}, nil
		},
	}

	c := &DuplicateChecker{client: client, importType: config.ImportTypeA2A, transform: testTransform}

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

	skill := mustStruct(t, map[string]any{
		fieldName:  "my-skill",
		"metadata": map[string]any{fieldVersion: "v1.0.0"},
	})
	newSkill := mustStruct(t, map[string]any{
		fieldName:  "other-skill",
		"metadata": map[string]any{fieldVersion: "v2.0.0"},
	})

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			if queryNameValue(req) == "my-skill" {
				return newStubStreamResult([]string{cidBafy1}, nil), nil
			}

			return newStubStreamResult(nil, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{{Data: skill}}, nil
		},
	}

	c := &DuplicateChecker{client: client, importType: config.ImportTypeAgentSkill, transform: testTransform}

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

// TestIsDuplicate_StripsEnrichmentFromDirectoryRecords covers the asymmetry
// the whole comparison rests on: a record pulled from the directory is
// post-enrichment (it carries skills and domains), while the item hashed on
// the import side is pre-enrichment. isDuplicate therefore has to hash the
// STRIPPED candidate, not its raw CID, or no import item can ever match a
// directory record that has been enriched.
func TestIsDuplicate_StripsEnrichmentFromDirectoryRecords(t *testing.T) {
	t.Parallel()

	enriched := recordWith("demo", testVersion)
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

	c := NewDuplicateChecker(client, config.ImportTypeMCPRegistry, false, testTransform)

	item := types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "demo", Version: testVersion}})

	if !c.isDuplicate(context.Background(), item) {
		t.Fatal("expected the pre-enrichment item to match the enriched directory record after stripping")
	}
}

// TestIsDuplicate_SkipsCandidatesWithoutData verifies that a candidate with no
// Data at all (so no content to hash) is skipped during comparison rather
// than treated as a match, while a later, hashable candidate is still found.
func TestIsDuplicate_SkipsCandidatesWithoutData(t *testing.T) {
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

	c := NewDuplicateChecker(client, config.ImportTypeA2A, false, testTransform)

	item := types.A2ASourceItem(mustStruct(t, map[string]any{fieldName: "kept", fieldVersion: "9.9.9"}))

	if !c.isDuplicate(context.Background(), item) {
		t.Fatal("expected a match against the second (hashable) candidate")
	}
}

func TestFindCandidates_ScopesQueryToImportTypeModules(t *testing.T) {
	t.Parallel()

	var gotQueries []*searchv1.RecordQuery

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			gotQueries = req.GetQueries()

			return newStubStreamResult(nil, nil), nil
		},
	}

	c := NewDuplicateChecker(client, config.ImportTypeMCPRegistry, false, nil)

	if _, err := c.findCandidates(context.Background(), "demo", testVersion); err != nil {
		t.Fatalf("findCandidates err = %v", err)
	}

	var (
		gotModules          []string
		gotName, gotVersion string
	)

	for _, q := range gotQueries {
		switch q.GetType() { //nolint:exhaustive // only the three query types dedup can emit are relevant here
		case searchv1.RecordQueryType_RECORD_QUERY_TYPE_NAME:
			gotName = q.GetValue()
		case searchv1.RecordQueryType_RECORD_QUERY_TYPE_VERSION:
			gotVersion = q.GetValue()
		case searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME:
			gotModules = append(gotModules, q.GetValue())
		default:
			t.Errorf("unexpected query type %v in per-item lookup", q.GetType())
		}
	}

	if gotName != "demo" || gotVersion != testVersion {
		t.Errorf("name/version = %q/%q, want demo/%s", gotName, gotVersion, testVersion)
	}

	wantModules := map[string]bool{moduleMCPCurrent: false, moduleMCPLegacy: false}

	for _, m := range gotModules {
		if _, ok := wantModules[m]; !ok {
			t.Errorf("unexpected module %q queried for MCP import type", m)
		}

		wantModules[m] = true
	}

	for m, seen := range wantModules {
		if !seen {
			t.Errorf("module %q was not queried", m)
		}
	}
}

// TestFindCandidates_UnknownImportTypeSkipsModuleFilter: an import type with
// no modulesByImportType entry must not filter by module at all.
func TestFindCandidates_UnknownImportTypeSkipsModuleFilter(t *testing.T) {
	t.Parallel()

	var gotQueries []*searchv1.RecordQuery

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			gotQueries = req.GetQueries()

			return newStubStreamResult(nil, nil), nil
		},
	}

	c := NewDuplicateChecker(client, config.ImportType("unknown-type"), false, nil)

	if _, err := c.findCandidates(context.Background(), "x", "1"); err != nil {
		t.Fatalf("findCandidates err = %v", err)
	}

	for _, q := range gotQueries {
		if q.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME {
			t.Errorf("unexpected module query %q for an import type with no modulesByImportType entry", q.GetValue())
		}
	}
}

func TestIsDuplicate_FailsSafeOnSearchError(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return nil, errors.New("dial failed")
		},
	}

	c := NewDuplicateChecker(client, config.ImportTypeMCP, false, testTransform)

	item := types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "x", Version: "1"}})

	if c.isDuplicate(context.Background(), item) {
		t.Fatal("expected fail-safe (not a duplicate) when the directory search errors")
	}
}

func TestIsDuplicate_FailsSafeOnStreamError(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult(nil, errors.New("stream broke")), nil
		},
	}

	c := NewDuplicateChecker(client, config.ImportTypeAgentSkill, false, testTransform)

	item := types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "x", Version: "1"}})

	if c.isDuplicate(context.Background(), item) {
		t.Fatal("expected fail-safe (not a duplicate) when the search stream errors")
	}
}

func TestIsDuplicate_FailsSafeOnPullBatchError(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult([]string{cidBafy1}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return nil, errors.New("pull failed")
		},
	}

	c := NewDuplicateChecker(client, config.ImportTypeMCP, false, testTransform)

	item := types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "x", Version: "1"}})

	if c.isDuplicate(context.Background(), item) {
		t.Fatal("expected fail-safe (not a duplicate) when PullBatch errors")
	}
}

func TestFindCandidates_ContextCancelledDuringStream(t *testing.T) {
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

	c := NewDuplicateChecker(client, config.ImportTypeMCP, false, nil)

	if _, err := c.findCandidates(ctx, "x", "1"); err == nil {
		t.Fatal("expected error when context is cancelled mid-stream")
	}
}
