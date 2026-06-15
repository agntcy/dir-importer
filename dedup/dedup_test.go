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

func searchQueriesFrom(req *searchv1.SearchCIDsRequest) []searchv1.RecordQueryType {
	types := make([]searchv1.RecordQueryType, 0, len(req.GetQueries()))
	for _, q := range req.GetQueries() {
		types = append(types, q.GetType())
	}

	return types
}

// recordWith returns a corev1.Record whose Data has the given name/version.
// shared.ExtractNameVersion reads only those two fields.
func recordWith(name, version string) *corev1.Record {
	return &corev1.Record{
		Data: &structpb.Struct{
			Fields: map[string]*structpb.Value{
				fieldName:    structpb.NewStringValue(name),
				fieldVersion: structpb.NewStringValue(version),
			},
		},
	}
}

func TestFilterDuplicates_SkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	c := &DuplicateChecker{
		existingRecords: map[string]string{"dup@1.0.0": "bafycid"},
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

	c := &DuplicateChecker{existingRecords: map[string]string{}}

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

func TestFilterDuplicates_A2ASkipsKnownDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	c := &DuplicateChecker{
		importType:      config.ImportTypeA2A,
		existingRecords: map[string]string{"my-agent@v1.0.0": "bafycid123"},
	}

	card, err := structpb.NewStruct(map[string]any{fieldName: "my-agent", fieldVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("failed to create A2A struct: %v", err)
	}

	newCard, err := structpb.NewStruct(map[string]any{fieldName: "other-agent", fieldVersion: "v2.0.0"})
	if err != nil {
		t.Fatalf("failed to create A2A struct: %v", err)
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

	c := &DuplicateChecker{
		importType:      config.ImportTypeAgentSkill,
		existingRecords: map[string]string{"my-skill@v1.0.0": "bafycid456"},
	}

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

func TestNewDuplicateChecker_PopulatesCache(t *testing.T) {
	t.Parallel()

	wantNV := "demo@1.0.0"

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

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCPRegistry, false, false)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	if _, ok := checker.existingRecords[wantNV]; !ok {
		t.Errorf("cache missing %q; got %v", wantNV, checker.existingRecords)
	}
}

func TestNewDuplicateChecker_SkipsRecordsWithoutNameVersion(t *testing.T) {
	t.Parallel()

	client := &stubClient{
		searchFn: func(_ context.Context, _ *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			return newStubStreamResult([]string{cidBafy1, "bafy2"}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{
				{Data: &structpb.Struct{Fields: map[string]*structpb.Value{}}},
				recordWith("kept", "9.9.9"),
			}, nil
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeA2A, false, false)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	if _, ok := checker.existingRecords["kept@9.9.9"]; !ok {
		t.Error("expected kept@9.9.9 in cache")
	}

	if len(checker.existingRecords) != 1 {
		t.Errorf("cache size = %d, want 1 (record without name/version should be skipped)", len(checker.existingRecords))
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

	if _, err := NewDuplicateChecker(context.Background(), client, config.ImportType("unknown-type"), false, false); err != nil {
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

	_, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, false, false)
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

	_, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeAgentSkill, false, false)
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

	_, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, false, false)
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

	_, err := NewDuplicateChecker(ctx, client, config.ImportTypeMCP, false, false)
	if err == nil {
		t.Fatal("expected error when context is cancelled mid-stream")
	}
}

func TestNewDuplicateChecker_TrackUnsigned_AddsTrustedSearchQuery(t *testing.T) {
	t.Parallel()

	var trustedQueries []searchv1.RecordQueryType

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			queries := searchQueriesFrom(req)
			for _, q := range req.GetQueries() {
				if q.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_TRUSTED {
					trustedQueries = queries
				}
			}

			return newStubStreamResult(nil, nil), nil
		},
	}

	if _, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, true, false); err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	want := []searchv1.RecordQueryType{
		searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME,
		searchv1.RecordQueryType_RECORD_QUERY_TYPE_TRUSTED,
	}

	if len(trustedQueries) < len(want) {
		t.Fatalf("trusted search got %d queries, want at least %d: %v", len(trustedQueries), len(want), trustedQueries)
	}

	for i, q := range want {
		if trustedQueries[i] != q {
			t.Errorf("query[%d] = %v, want %v", i, trustedQueries[i], q)
		}
	}
}

func TestNewDuplicateChecker_TrackUnsigned_SeparatesTrustedAndUnsigned(t *testing.T) {
	t.Parallel()

	const unsignedCID = "bafyunsigned"

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			for _, q := range req.GetQueries() {
				if q.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_TRUSTED {
					return newStubStreamResult(nil, nil), nil
				}
			}

			return newStubStreamResult([]string{unsignedCID}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{recordWith("unsigned", "1.0.0")}, nil
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, true, false)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	if len(checker.trustedRecords) != 0 {
		t.Errorf("trusted cache size = %d, want 0", len(checker.trustedRecords))
	}

	wantNV := "unsigned@1.0.0"
	if got, ok := checker.unsignedRecords[wantNV]; !ok || got == "" {
		t.Errorf("unsignedRecords[%q] = %q, ok=%v; want non-empty cid, true", wantNV, got, ok)
	}
}

func TestNewDuplicateChecker_TrackUnsigned_IncludesTrustedRecords(t *testing.T) {
	t.Parallel()

	wantNV := "signed@2.0.0"

	client := &stubClient{
		searchFn: func(_ context.Context, req *searchv1.SearchCIDsRequest) (streaming.StreamResult[searchv1.SearchCIDsResponse], error) {
			for _, q := range req.GetQueries() {
				if q.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_TRUSTED {
					return newStubStreamResult([]string{cidBafy1}, nil), nil
				}
			}

			return newStubStreamResult([]string{cidBafy1}, nil), nil
		},
		pullFn: func(_ context.Context, _ []*corev1.RecordRef) ([]*corev1.Record, error) {
			return []*corev1.Record{recordWith("signed", "2.0.0")}, nil
		},
	}

	checker, err := NewDuplicateChecker(context.Background(), client, config.ImportTypeMCP, true, false)
	if err != nil {
		t.Fatalf("NewDuplicateChecker err = %v", err)
	}

	if got, ok := checker.trustedRecords[wantNV]; !ok || got == "" {
		t.Errorf("trustedRecords[%q] = %q, ok=%v; want non-empty cid, true", wantNV, got, ok)
	}

	if _, ok := checker.unsignedRecords[wantNV]; ok {
		t.Errorf("trusted record should not appear in unsignedRecords")
	}
}

func TestFilterDuplicates_RecordsUnsignedDuplicateCID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result := &types.Result{}

	c := &DuplicateChecker{
		trackUnsigned:   true,
		trustedRecords:  map[string]string{},
		unsignedRecords: map[string]string{"unsigned@1.0.0": "bafyunsigned"},
	}

	in := make(chan types.SourceItem, 1)
	in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "unsigned", Version: "1.0.0"}})

	close(in)

	out := c.FilterDuplicates(ctx, in, result)

	for range out {
		t.Fatal("unsigned duplicate should not pass through pipeline")
	}

	if len(result.UnsignedDuplicateCIDs) != 1 || result.UnsignedDuplicateCIDs[0] != "bafyunsigned" {
		t.Fatalf("UnsignedDuplicateCIDs = %v, want [bafyunsigned]", result.UnsignedDuplicateCIDs)
	}

	if result.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", result.SkippedCount)
	}
}
