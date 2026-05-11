// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
