// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/config"
	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"google.golang.org/protobuf/types/known/structpb"
)

const fieldName = "name"

const (
	testCIDUnsigned = "cid-unsigned"
	testCIDOk       = "cid-ok"
	testCIDBad      = "cid-bad"
	testCIDA        = "cid-a"
	testCIDB        = "cid-b"
)

// --- Mocks ---

type mockFetcher struct {
	items []types.SourceItem
	err   error
}

func (m *mockFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	dataCh := make(chan types.SourceItem)
	errCh := make(chan error, 1)

	go func() {
		defer close(dataCh)
		defer close(errCh)

		if m.err != nil {
			errCh <- m.err

			return
		}

		for _, item := range m.items {
			select {
			case dataCh <- item:
			case <-ctx.Done():
				return
			}
		}
	}()

	return dataCh, errCh
}

func mcpSourceItems(servers ...mcpapiv0.ServerResponse) []types.SourceItem {
	out := make([]types.SourceItem, len(servers))
	for i, s := range servers {
		out[i] = types.MCPSourceItem(s)
	}

	return out
}

// passThroughDedup forwards all items (mirrors “no duplicates” dedup).
type passThroughDedup struct{}

func (passThroughDedup) FilterDuplicates(
	ctx context.Context,
	inputCh <-chan types.SourceItem,
	_ *types.Result,
) <-chan types.SourceItem {
	out := make(chan types.SourceItem)

	go func() {
		defer close(out)

		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-inputCh:
				if !ok {
					return
				}

				select {
				case out <- s:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

// denyDedup panics if FilterDuplicates is invoked (used to assert Force bypasses dedup).
type denyDedup struct{}

func (denyDedup) FilterDuplicates(
	context.Context,
	<-chan types.SourceItem,
	*types.Result,
) <-chan types.SourceItem {
	panic("FilterDuplicates must not be called when cfg.Force skips dedup")
}

// mockDedupByName skips items whose Server.Name is in duplicates (mirrors dedup accounting).
type mockDedupByName struct {
	duplicates map[string]struct{}
}

func (m *mockDedupByName) FilterDuplicates(
	ctx context.Context,
	inputCh <-chan types.SourceItem,
	result *types.Result,
) <-chan types.SourceItem {
	out := make(chan types.SourceItem)

	go func() {
		defer close(out)

		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-inputCh:
				if !ok {
					return
				}

				var name string
				if s.Kind == types.SourceKindMCP {
					name = s.MCP.Server.Name
				}

				if _, dup := m.duplicates[name]; dup {
					result.Mu.Lock()
					result.TotalRecords++
					result.SkippedCount++
					result.Mu.Unlock()

					continue
				}

				select {
				case out <- s:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

// mockUnsignedDuplicateDedup skips items present in unsignedDuplicates (name -> cid),
// mirroring dedup accounting for unsigned duplicate recovery.
type mockUnsignedDuplicateDedup struct {
	unsignedDuplicates map[string]string
}

//nolint:gocognit // Test double mirrors dedup unsigned-duplicate accounting.
func (m *mockUnsignedDuplicateDedup) FilterDuplicates(
	ctx context.Context,
	inputCh <-chan types.SourceItem,
	result *types.Result,
) <-chan types.SourceItem {
	out := make(chan types.SourceItem)

	go func() {
		defer close(out)

		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-inputCh:
				if !ok {
					return
				}

				var name string
				if s.Kind == types.SourceKindMCP {
					name = s.MCP.Server.Name
				}

				cid, dup := m.unsignedDuplicates[name]
				if !dup {
					select {
					case out <- s:
					case <-ctx.Done():
						return
					}

					continue
				}

				result.Mu.Lock()
				result.TotalRecords++
				result.SkippedCount++

				if cid != "" && !slices.Contains(result.UnsignedDuplicateCIDs, cid) {
					result.UnsignedDuplicateCIDs = append(result.UnsignedDuplicateCIDs, cid)
				}

				result.Mu.Unlock()
			}
		}
	}()

	return out
}

type mockTransformer struct {
	shouldFail bool
}

func (m *mockTransformer) Transform(
	ctx context.Context,
	inputCh <-chan types.SourceItem,
	result *types.Result,
) (<-chan *corev1.Record, <-chan error) {
	out := make(chan *corev1.Record)
	errCh := make(chan error)

	go func() {
		defer close(out)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return
			case source, ok := <-inputCh:
				if !ok {
					return
				}

				result.Mu.Lock()
				result.TotalRecords++
				result.Mu.Unlock()

				if m.shouldFail {
					result.Mu.Lock()
					result.FailedCount++
					result.Mu.Unlock()

					select {
					case errCh <- errors.New("transform failed"):
					case <-ctx.Done():
						return
					}

					continue
				}

				var name string
				if source.Kind == types.SourceKindMCP {
					name = source.MCP.Server.Name
				}

				rec := &corev1.Record{
					Data: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							fieldName: structpb.NewStringValue(name),
						},
					},
				}

				select {
				case out <- rec:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, errCh
}

type mockEnricher struct{}

func (mockEnricher) Enrich(
	ctx context.Context,
	inputCh <-chan *corev1.Record,
	_ *types.Result,
) (<-chan *corev1.Record, <-chan error) {
	out := make(chan *corev1.Record)
	errCh := make(chan error)

	go func() {
		defer close(out)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return
			case r, ok := <-inputCh:
				if !ok {
					return
				}

				select {
				case out <- r:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, errCh
}

type mockScanner struct {
	err error
}

func (m *mockScanner) Scan(
	ctx context.Context,
	inputCh <-chan *corev1.Record,
	_ *types.Result,
) (<-chan *corev1.Record, <-chan error) {
	out := make(chan *corev1.Record)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		if m.err != nil {
			errCh <- m.err
		}

		for {
			select {
			case <-ctx.Done():
				return
			case r, ok := <-inputCh:
				if !ok {
					return
				}

				select {
				case out <- r:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, errCh
}

type mockPusher struct {
	shouldFail bool
	pushed     []*corev1.Record
}

func (m *mockPusher) Push(ctx context.Context, inputCh <-chan *corev1.Record) (<-chan *corev1.RecordRef, <-chan error) {
	refCh := make(chan *corev1.RecordRef)
	errCh := make(chan error)

	go func() {
		defer close(refCh)
		defer close(errCh)

		for r := range inputCh {
			m.pushed = append(m.pushed, r)

			if m.shouldFail {
				select {
				case errCh <- errors.New("push failed"):
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case refCh <- &corev1.RecordRef{Cid: "bafytestcid"}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return refCh, errCh
}

func testServer(name string) mcpapiv0.ServerResponse {
	return mcpapiv0.ServerResponse{
		Server: mcpapiv0.ServerJSON{Name: name, Version: "1"},
	}
}

func testImporter(
	cfg config.Config,
	fetcher types.Fetcher,
	dedup types.DuplicateChecker,
	tr types.Transformer,
	sc types.Scanner,
	pu types.Pusher,
) *Importer {
	return &Importer{
		cfg:         cfg,
		fetcher:     fetcher,
		dedup:       dedup,
		transformer: tr,
		enricher:    mockEnricher{},
		scanner:     sc,
		pusher:      pu,
	}
}

func baseConfig() config.Config {
	return config.Config{
		Force: false,
		Scanner: scannerconfig.Config{
			Enabled: false,
		},
	}
}

// --- Run tests ---

func TestImporter_Run_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()

	fetcher := &mockFetcher{items: mcpSourceItems(
		testServer("a"),
		testServer("b"),
		testServer("c"),
	)}

	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if res.ImportedCount != 3 {
		t.Errorf("ImportedCount = %d, want 3", res.ImportedCount)
	}

	if res.TotalRecords != 3 {
		t.Errorf("TotalRecords = %d, want 3", res.TotalRecords)
	}

	if len(pusher.pushed) != 3 {
		t.Errorf("pushed = %d, want 3", len(pusher.pushed))
	}
}

func TestProcessUnsignedDuplicates_SignFuncInvokedPerUniqueCID(t *testing.T) {
	t.Parallel()

	var signed []string

	imp := &Importer{cfg: config.Config{
		SignFunc: func(_ context.Context, cid string) error {
			signed = append(signed, cid)

			return nil
		},
	}}

	result := &types.Result{
		UnsignedDuplicateCIDs: []string{testCIDA, testCIDA, testCIDB},
		SkippedCount:          3,
		ImportedCount:         2,
	}

	imp.processUnsignedDuplicates(context.Background(), result)

	if len(signed) != 2 || signed[0] != testCIDA || signed[1] != testCIDB {
		t.Fatalf("SignFunc signed %v, want [%s %s]", signed, testCIDA, testCIDB)
	}

	if result.SignedCount != 2 {
		t.Errorf("SignedCount = %d, want 2", result.SignedCount)
	}

	if result.ImportedCount != 2 {
		t.Errorf("ImportedCount = %d, want 2 (unchanged)", result.ImportedCount)
	}

	if len(result.ImportedCIDs) != 2 {
		t.Fatalf("ImportedCIDs = %v, want two entries", result.ImportedCIDs)
	}
}

func TestProcessUnsignedDuplicates_SignFuncFailure(t *testing.T) {
	t.Parallel()

	imp := &Importer{cfg: config.Config{
		SignFunc: func(_ context.Context, cid string) error {
			if cid == testCIDBad {
				return errors.New("sign failed")
			}

			return nil
		},
	}}

	result := &types.Result{
		UnsignedDuplicateCIDs: []string{testCIDOk, testCIDBad},
	}

	imp.processUnsignedDuplicates(context.Background(), result)

	if result.SignedCount != 1 {
		t.Errorf("SignedCount = %d, want 1", result.SignedCount)
	}

	if result.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", result.FailedCount)
	}

	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %v, want one sign failure", result.Errors)
	}
}

func TestProcessUnsignedDuplicates_IncludeUnsignedCIDs(t *testing.T) {
	t.Parallel()

	imp := &Importer{cfg: config.Config{IncludeUnsignedCIDs: true}}

	result := &types.Result{
		UnsignedDuplicateCIDs: []string{testCIDA, testCIDA},
	}

	imp.processUnsignedDuplicates(context.Background(), result)

	if result.SignedCount != 1 {
		t.Errorf("SignedCount = %d, want 1", result.SignedCount)
	}

	if len(result.ImportedCIDs) != 1 || result.ImportedCIDs[0] != testCIDA {
		t.Fatalf("ImportedCIDs = %v, want [%s]", result.ImportedCIDs, testCIDA)
	}
}

func TestProcessUnsignedDuplicates_DryRunIncludeUnsignedCIDs(t *testing.T) {
	t.Parallel()

	imp := &Importer{cfg: config.Config{DryRun: true, IncludeUnsignedCIDs: true}}

	result := &types.Result{
		UnsignedDuplicateCIDs: []string{testCIDA, testCIDB},
	}

	imp.processUnsignedDuplicates(context.Background(), result)

	if result.SignedCount != 2 {
		t.Errorf("SignedCount = %d, want 2", result.SignedCount)
	}

	if len(result.ImportedCIDs) != 2 {
		t.Fatalf("ImportedCIDs = %v, want two entries", result.ImportedCIDs)
	}
}

func TestProcessUnsignedDuplicates_DryRunWithoutIncludeUnsignedCIDs(t *testing.T) {
	t.Parallel()

	imp := &Importer{cfg: config.Config{DryRun: true}}

	result := &types.Result{
		UnsignedDuplicateCIDs: []string{testCIDA},
		SignedCount:           0,
	}

	imp.processUnsignedDuplicates(context.Background(), result)

	if result.SignedCount != 0 {
		t.Errorf("SignedCount = %d, want 0", result.SignedCount)
	}

	if len(result.ImportedCIDs) != 0 {
		t.Fatalf("ImportedCIDs = %v, want none", result.ImportedCIDs)
	}
}

func TestProcessUnsignedDuplicates_NoSignOrIncludeIsNoOp(t *testing.T) {
	t.Parallel()

	imp := &Importer{cfg: config.Config{}}

	result := &types.Result{
		UnsignedDuplicateCIDs: []string{testCIDA},
	}

	imp.processUnsignedDuplicates(context.Background(), result)

	if result.SignedCount != 0 || len(result.ImportedCIDs) != 0 {
		t.Fatalf("expected no signing/output without SignFunc or IncludeUnsignedCIDs: SignedCount=%d ImportedCIDs=%v",
			result.SignedCount, result.ImportedCIDs)
	}
}

func TestImporter_Run_UnsignedDuplicateSigning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var signed []string

	cfg := baseConfig()
	cfg.SignFunc = func(_ context.Context, cid string) error {
		signed = append(signed, cid)

		return nil
	}

	fetcher := &mockFetcher{items: mcpSourceItems(
		testServer("new"),
		testServer("existing-unsigned"),
	)}

	dedup := &mockUnsignedDuplicateDedup{
		unsignedDuplicates: map[string]string{
			"existing-unsigned": testCIDUnsigned,
		},
	}

	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, dedup, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if res.ImportedCount != 1 {
		t.Errorf("ImportedCount = %d, want 1", res.ImportedCount)
	}

	if res.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", res.SkippedCount)
	}

	if res.SignedCount != 1 {
		t.Errorf("SignedCount = %d, want 1", res.SignedCount)
	}

	if len(signed) != 1 || signed[0] != testCIDUnsigned {
		t.Fatalf("SignFunc signed %v, want [%s]", signed, testCIDUnsigned)
	}

	if len(pusher.pushed) != 1 {
		t.Errorf("pushed = %d, want 1", len(pusher.pushed))
	}

	if res.TotalRecords != res.SkippedCount+res.ImportedCount+res.FailedCount {
		t.Errorf("TotalRecords %d != skipped+imported+failed = %d",
			res.TotalRecords, res.SkippedCount+res.ImportedCount+res.FailedCount)
	}
}

func TestImporter_DryRun_IncludeUnsignedCIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg := baseConfig()
	cfg.DryRun = true
	cfg.IncludeUnsignedCIDs = true

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("existing-unsigned"))}

	dedup := &mockUnsignedDuplicateDedup{
		unsignedDuplicates: map[string]string{
			"existing-unsigned": testCIDUnsigned,
		},
	}

	imp := testImporter(cfg, fetcher, dedup, &mockTransformer{}, nil, &mockPusher{})

	res := imp.DryRun(ctx)

	if res.SignedCount != 1 {
		t.Errorf("SignedCount = %d, want 1", res.SignedCount)
	}

	if len(res.ImportedCIDs) != 1 || res.ImportedCIDs[0] != testCIDUnsigned {
		t.Fatalf("ImportedCIDs = %v, want [%s]", res.ImportedCIDs, testCIDUnsigned)
	}

	if res.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", res.SkippedCount)
	}

	if res.ImportedCount != 0 {
		t.Errorf("ImportedCount = %d, want 0", res.ImportedCount)
	}
}

func TestImporter_Run_FetchError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()

	fetcher := &mockFetcher{err: errors.New("fetch failed")}
	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if len(res.Errors) == 0 {
		t.Fatal("expected fetch error in result.Errors")
	}
}

func TestImporter_Run_TransformError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("a"))}
	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{shouldFail: true}, nil, pusher)

	res := imp.Run(ctx)

	if res.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", res.FailedCount)
	}

	if res.ImportedCount != 0 {
		t.Errorf("ImportedCount = %d, want 0", res.ImportedCount)
	}

	if len(res.Errors) == 0 {
		t.Error("expected transform error")
	}
}

func TestImporter_Run_PushError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()

	fetcher := &mockFetcher{items: mcpSourceItems(
		testServer("a"),
		testServer("b"),
	)}

	pusher := &mockPusher{shouldFail: true}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if res.FailedCount != 2 {
		t.Errorf("FailedCount = %d, want 2", res.FailedCount)
	}

	if res.ImportedCount != 0 {
		t.Errorf("ImportedCount = %d, want 0", res.ImportedCount)
	}

	if len(res.Errors) != 2 {
		t.Errorf("len(Errors) = %d, want 2", len(res.Errors))
	}
}

func TestImporter_Run_WithDuplicateChecker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()

	fetcher := &mockFetcher{items: mcpSourceItems(
		testServer("item1"),
		testServer("item2"),
		testServer("item3"),
		testServer("item4"),
		testServer("item5"),
	)}

	dedup := &mockDedupByName{
		duplicates: map[string]struct{}{"item2": {}, "item4": {}},
	}

	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, dedup, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if res.SkippedCount != 2 {
		t.Errorf("SkippedCount = %d, want 2", res.SkippedCount)
	}

	if res.ImportedCount != 3 {
		t.Errorf("ImportedCount = %d, want 3", res.ImportedCount)
	}

	if res.TotalRecords != 5 {
		t.Errorf("TotalRecords = %d, want 5", res.TotalRecords)
	}

	if len(pusher.pushed) != 3 {
		t.Errorf("pushed = %d, want 3", len(pusher.pushed))
	}

	expected := res.SkippedCount + res.ImportedCount + res.FailedCount
	if res.TotalRecords != expected {
		t.Errorf("TotalRecords %d != skipped+imported+failed = %d", res.TotalRecords, expected)
	}
}

func TestImporter_Run_Force_BypassesDedup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()
	cfg.Force = true

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("x"))}
	pusher := &mockPusher{}

	// If Run incorrectly called FilterDuplicates, denyDedup would panic.
	imp := testImporter(cfg, fetcher, denyDedup{}, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if res.ImportedCount != 1 {
		t.Errorf("ImportedCount = %d, want 1", res.ImportedCount)
	}
}

func TestImporter_Run_ScannerDisabled_Completes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()
	cfg.Scanner.Enabled = false

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("a"))}
	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, nil, pusher)

	res := imp.Run(ctx)

	if res.ImportedCount != 1 {
		t.Errorf("ImportedCount = %d, want 1", res.ImportedCount)
	}
}

func TestImporter_Run_ScannerEnabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()
	cfg.Scanner.Enabled = true

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("a"))}
	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, &mockScanner{}, pusher)

	res := imp.Run(ctx)

	if res.ImportedCount != 1 {
		t.Errorf("ImportedCount = %d, want 1", res.ImportedCount)
	}
}

func TestImporter_Run_ScannerErrorRecorded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := baseConfig()
	cfg.Scanner.Enabled = true

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("a"))}
	pusher := &mockPusher{}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, &mockScanner{err: errors.New("scan setup failed")}, pusher)

	res := imp.Run(ctx)

	found := false

	for _, e := range res.Errors {
		if e != nil && strings.Contains(e.Error(), "scan setup failed") {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("expected scanner error in Errors, got %#v", res.Errors)
	}
}

// --- Dry run & directory output ---

func TestImporter_DryRun_WritesDefaultOutputDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := baseConfig()

	fetcher := &mockFetcher{items: mcpSourceItems(
		testServer("a"),
		testServer("b"),
	)}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, nil, &mockPusher{})

	res := imp.DryRun(ctx)

	if res.TotalRecords != 2 {
		t.Errorf("TotalRecords = %d, want 2", res.TotalRecords)
	}

	if res.OutputDir == "" {
		t.Fatal("OutputDir is empty; expected a default to be generated")
	}

	if !strings.HasPrefix(res.OutputDir, "import-dry-run-") {
		t.Errorf("OutputDir = %q, want prefix %q", res.OutputDir, "import-dry-run-")
	}

	// The default directory is created relative to the current working
	// directory; ensure it actually exists and contains one file per record.
	resolvedDir := filepath.Join(dir, res.OutputDir)

	entries, err := os.ReadDir(resolvedDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", resolvedDir, err)
	}

	if len(entries) != 2 {
		t.Errorf("ReadDir(%q) returned %d entries, want 2", resolvedDir, len(entries))
	}

	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("unexpected sub-directory %q in output dir", e.Name())
		}

		if !strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("file %q does not have .json suffix", e.Name())
		}
	}
}

func TestImporter_DryRun_UsesConfiguredOutputDir(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	configured := filepath.Join(root, "nested", "out")

	cfg := baseConfig()
	cfg.OutputDir = configured

	fetcher := &mockFetcher{items: mcpSourceItems(testServer("only"))}

	imp := testImporter(cfg, fetcher, passThroughDedup{}, &mockTransformer{}, nil, &mockPusher{})

	res := imp.DryRun(ctx)

	if res.OutputDir != configured {
		t.Errorf("OutputDir = %q, want %q", res.OutputDir, configured)
	}

	entries, err := os.ReadDir(configured)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", configured, err)
	}

	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func TestImporter_DryRun_ForceBypassesDedup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := baseConfig()
	cfg.Force = true

	imp := testImporter(
		cfg,
		&mockFetcher{items: mcpSourceItems(testServer("only"))},
		denyDedup{},
		&mockTransformer{},
		nil,
		&mockPusher{},
	)

	res := imp.DryRun(ctx)

	if res.TotalRecords != 1 {
		t.Errorf("TotalRecords = %d, want 1", res.TotalRecords)
	}
}

func TestImporter_DryRun_WithScannerEnabled(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := baseConfig()
	cfg.Scanner.Enabled = true

	imp := testImporter(
		cfg,
		&mockFetcher{items: mcpSourceItems(testServer("a"))},
		passThroughDedup{},
		&mockTransformer{},
		&mockScanner{},
		&mockPusher{},
	)

	res := imp.DryRun(ctx)

	if res.TotalRecords != 1 {
		t.Errorf("TotalRecords = %d, want 1", res.TotalRecords)
	}
}

func TestImporter_DryRun_PropagatesScannerError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := baseConfig()
	cfg.Scanner.Enabled = true

	imp := testImporter(
		cfg,
		&mockFetcher{items: mcpSourceItems(testServer("a"))},
		passThroughDedup{},
		&mockTransformer{},
		&mockScanner{err: errors.New("scan setup failed")},
		&mockPusher{},
	)

	res := imp.DryRun(ctx)

	found := false

	for _, e := range res.Errors {
		if e != nil && strings.Contains(e.Error(), "scan setup failed") {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("expected scanner error in Errors, got %#v", res.Errors)
	}
}

func TestImporter_DryRun_WriteFailureSurfacedAsError(t *testing.T) {
	ctx := context.Background()

	// Point the importer at a configured output directory whose parent is a
	// regular file, which makes os.MkdirAll inside writeRecordsToDir fail.
	parent := t.TempDir()

	notADir := filepath.Join(parent, "blocker")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	cfg := baseConfig()
	cfg.OutputDir = filepath.Join(notADir, "out")

	imp := testImporter(
		cfg,
		&mockFetcher{items: mcpSourceItems(testServer("a"))},
		passThroughDedup{},
		&mockTransformer{},
		nil,
		&mockPusher{},
	)

	res := imp.DryRun(ctx)

	found := false

	for _, e := range res.Errors {
		if e != nil && strings.Contains(e.Error(), "failed to write records") {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("expected write-records error, got %#v", res.Errors)
	}
}

// --- writeRecords helpers ---

func recordWithFields(fields map[string]string) *corev1.Record {
	pbFields := make(map[string]*structpb.Value, len(fields))
	for k, v := range fields {
		pbFields[k] = structpb.NewStringValue(v)
	}

	return &corev1.Record{Data: &structpb.Struct{Fields: pbFields}}
}

//nolint:goconst
func TestWriteRecords_OneFilePerRecord(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	r1 := recordWithFields(map[string]string{fieldName: "server1", "version": "1.0.0"})
	r2 := recordWithFields(map[string]string{fieldName: "server2", "version": "2.0.0"})

	ch := make(chan *corev1.Record, 2)

	ch <- r1

	ch <- r2

	close(ch)

	if err := writeRecords(dir, ch); err != nil {
		t.Fatalf("writeRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d files, want 2", len(entries))
	}

	wantNames := map[string]bool{
		r1.GetCid() + ".record.json": true,
		r2.GetCid() + ".record.json": true,
	}

	for _, e := range entries {
		if !wantNames[e.Name()] {
			t.Errorf("unexpected filename %q (not a CID-derived name)", e.Name())
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", e.Name(), err)
		}

		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal(%q): %v\n%s", e.Name(), err, string(data))
		}

		if _, ok := decoded[fieldName]; !ok {
			t.Errorf("file %q missing %q field: %v", e.Name(), fieldName, decoded)
		}
	}
}

// Identical records share a CID and collapse into a single file (natural
// dedup via content addressing).
func TestWriteRecords_IdenticalRecordsDeduplicate(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	r := recordWithFields(map[string]string{fieldName: "same", "version": "1.0.0"})

	ch := make(chan *corev1.Record, 2)

	ch <- r

	ch <- r

	close(ch)

	if err := writeRecords(dir, ch); err != nil {
		t.Fatalf("writeRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1 (identical records dedup by CID)", len(entries))
	}

	want := r.GetCid() + ".record.json"
	if entries[0].Name() != want {
		t.Errorf("filename = %q, want %q", entries[0].Name(), want)
	}
}

// Distinct records produce distinct CIDs and therefore distinct filenames
// without any collision-counter machinery.
func TestWriteRecords_DistinctRecordsDistinctFilenames(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	r1 := recordWithFields(map[string]string{fieldName: "dup", "version": "1.0.0", "extra": "alpha"})
	r2 := recordWithFields(map[string]string{fieldName: "dup", "version": "1.0.0", "extra": "beta"})

	if r1.GetCid() == r2.GetCid() {
		t.Fatalf("test setup: distinct records produced identical CIDs (%q)", r1.GetCid())
	}

	ch := make(chan *corev1.Record, 2)

	ch <- r1

	ch <- r2

	close(ch)

	if err := writeRecords(dir, ch); err != nil {
		t.Fatalf("writeRecords: %v", err)
	}

	got := map[string]bool{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	for _, e := range entries {
		got[e.Name()] = true
	}

	for _, want := range []string{
		r1.GetCid() + ".record.json",
		r2.GetCid() + ".record.json",
	} {
		if !got[want] {
			t.Errorf("missing expected file %q; got %v", want, got)
		}
	}
}

func TestWriteRecords_SkipsNilRecords(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	ch := make(chan *corev1.Record, 3)

	ch <- nil

	ch <- recordWithFields(map[string]string{fieldName: "kept"})

	ch <- nil

	close(ch)

	if err := writeRecords(dir, ch); err != nil {
		t.Fatalf("writeRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("got %d files, want 1 (nils should be skipped)", len(entries))
	}
}

// A record with no Data has no derivable CID and must surface a per-record
// error rather than silently writing an unnamed file.
func TestWriteRecords_RecordWithoutDataErrors(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	ch := make(chan *corev1.Record, 1)

	ch <- &corev1.Record{}

	close(ch)

	err := writeRecords(dir, ch)
	if err == nil {
		t.Fatal("expected error for record without derivable CID")
	}

	if !strings.Contains(err.Error(), "CID") {
		t.Errorf("error %q does not mention CID", err.Error())
	}
}

func TestWriteRecords_MkdirFails(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	notADir := filepath.Join(parent, "blocker")

	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	bad := filepath.Join(notADir, "out")

	ch := make(chan *corev1.Record)
	close(ch)

	err := writeRecords(bad, ch)
	if err == nil {
		t.Fatal("expected error creating directory under a regular file")
	}

	if !strings.Contains(err.Error(), "failed to create output directory") {
		t.Errorf("error %q does not match expected prefix", err.Error())
	}
}

// writeRecord must remove importer-only debug fields before computing the
// CID and marshaling, so dry-run output is consumable by the server (which
// rejects unknown OASF fields) and the filename CID matches the CID of the
// stripped record.
func TestWriteRecords_StripsImportDebugFields(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	// Record that mimics what the transformer emits for MCP and A2A sources:
	// real OASF fields plus the importer-only __*_debug_source side-channels.
	r := recordWithFields(map[string]string{
		fieldName:            "srv",
		"version":            "1.0.0",
		"__mcp_debug_source": `{"name":"srv","version":"1.0.0"}`,
		"__a2a_debug_source": `{"name":"srv","version":"1.0.0"}`,
	})

	// Snapshot the CID we expect on disk: the CID computed AFTER stripping
	// the debug fields. We compute it here on a separate record with the
	// same surviving fields so that the assertion documents the contract
	// rather than tautologically re-deriving from the mutated record.
	expected := recordWithFields(map[string]string{
		fieldName: "srv",
		"version": "1.0.0",
	})
	expectedCID := expected.GetCid()

	if expectedCID == "" {
		t.Fatal("test setup: expected CID is empty")
	}

	ch := make(chan *corev1.Record, 1)
	ch <- r

	close(ch)

	if err := writeRecords(dir, ch); err != nil {
		t.Fatalf("writeRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1", len(entries))
	}

	wantName := expectedCID + ".record.json"
	if entries[0].Name() != wantName {
		t.Errorf("filename = %q, want %q (CID must be computed AFTER stripping debug fields)", entries[0].Name(), wantName)
	}

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, string(data))
	}

	for _, banned := range []string{"__mcp_debug_source", "__a2a_debug_source"} {
		if _, ok := decoded[banned]; ok {
			t.Errorf("written file contains importer-only field %q: %v", banned, decoded)
		}
	}

	// Real fields must survive stripping.
	for _, want := range []string{fieldName, "version"} {
		if _, ok := decoded[want]; !ok {
			t.Errorf("written file is missing OASF field %q: %v", want, decoded)
		}
	}
}
