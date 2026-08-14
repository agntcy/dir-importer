// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/types"
)

// Directory input for the file-based fetchers. A directory yields every *.json
// file directly inside it, and one bad file must not stop the others.

func TestA2AFileFetcher_Fetch_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "one.json", `{"name":"alpha","version":"1.0.0"}`)
	writeInDir(t, dir, "two.json", `{"name":"beta","version":"1.0.0"}`)

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (one per file)", len(items))
	}
}

func TestA2AFileFetcher_Fetch_DirectoryWithArraysAcrossFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "batch-a.json", `[{"name":"a1","version":"1.0.0"},{"name":"a2","version":"1.0.0"}]`)
	writeInDir(t, dir, "batch-b.json", `{"name":"b1","version":"1.0.0"}`)

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 3 {
		t.Fatalf("got %d items, want 3 (2 from the array file, 1 from the object file)", len(items))
	}
}

func TestA2AFileFetcher_Fetch_DirectoryContinuesAfterBadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "broken.json", `{ not json`)
	writeInDir(t, dir, "good.json", `{"name":"survivor","version":"1.0.0"}`)

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 — a bad file must not abort the run", len(items))
	}

	if len(errs) == 0 {
		t.Fatal("expected an error for the malformed file")
	}

	// With more than one file in play, errors must say which file failed.
	var named bool

	for _, e := range errs {
		if strings.Contains(e.Error(), "broken.json") {
			named = true
		}
	}

	if !named {
		t.Errorf("errors %v should name the failing file", errs)
	}
}

func TestA2AFileFetcher_Fetch_DirectoryIgnoresNonJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "card.json", `{"name":"alpha","version":"1.0.0"}`)
	writeInDir(t, dir, "README.md", "documentation, not a card")
	writeInDir(t, dir, "notes.txt", "also not a card")

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 — non-JSON files must be ignored", len(items))
	}
}

func TestA2AFileFetcher_Fetch_DirectoryIsNotRecursive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "top.json", `{"name":"top","version":"1.0.0"}`)

	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}

	writeInDir(t, nested, "deep.json", `{"name":"deep","version":"1.0.0"}`)

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 — nested files must not be discovered", len(items))
	}
}

func TestA2AFileFetcher_Fetch_DirectoryWithoutJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "README.md", "no cards here")

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("got %d items, want 0", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "no files matching") {
		t.Fatalf("errs = %v, want an error explaining nothing matched", errs)
	}
}

func TestOASFFileFetcher_Fetch_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "one.json", `{"name":"rec-a","schema_version":"1.0.0","version":"1.0.0"}`)
	writeInDir(t, dir, "two.json", `{"name":"rec-b","schema_version":"1.0.0","version":"1.0.0"}`)

	f, err := NewOASFFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewOASFFileFetcher: %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
}

func TestMCPFileFetcher_Fetch_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "one.json", `{"name":"io.example/one","version":"1.0.0"}`)
	writeInDir(t, dir, "two.json", `{"name":"io.example/two","version":"1.0.0"}`)

	f, err := NewMCPFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher: %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (one per file)", len(items))
	}
}

func TestMCPFileFetcher_Fetch_DirectoryContinuesAfterBadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "broken.json", `{ not json`)
	writeInDir(t, dir, "good.json", `{"name":"io.example/survivor","version":"1.0.0"}`)

	f, err := NewMCPFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher: %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 — a bad file must not abort the run", len(items))
	}

	if len(errs) == 0 {
		t.Fatal("expected an error for the malformed file")
	}

	var named bool

	for _, e := range errs {
		if strings.Contains(e.Error(), "broken.json") {
			named = true
		}
	}

	if !named {
		t.Errorf("errors %v should name the failing file", errs)
	}
}

// An unreadable file must be reported without stopping the files around it.
// A missing path is caught earlier by inputPaths, so this is what exercises the
// read failure inside the per-file helpers.
func TestFileFetchers_DirectoryUnreadableFile(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not prevent reads")
	}

	tests := []struct {
		name     string
		good     string
		locked   string
		newFetch func(string) (types.Fetcher, error)
		drain    func(<-chan types.SourceItem, <-chan error) ([]types.SourceItem, []error)
	}{
		{
			name:     "a2a",
			good:     `{"name":"readable","version":"1.0.0"}`,
			locked:   `{"name":"unreadable","version":"1.0.0"}`,
			newFetch: func(p string) (types.Fetcher, error) { return NewA2AFileFetcher(p) },
			drain:    drainA2AFetch,
		},
		{
			name:     "mcp",
			good:     `{"name":"io.example/readable","version":"1.0.0"}`,
			locked:   `{"name":"io.example/unreadable","version":"1.0.0"}`,
			newFetch: func(p string) (types.Fetcher, error) { return NewMCPFileFetcher(p) },
			drain:    drainMCPFetch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeInDir(t, dir, "good.json", tt.good)

			locked := writeInDir(t, dir, "locked.json", tt.locked)
			if err := os.Chmod(locked, 0o000); err != nil {
				t.Skipf("chmod unavailable: %v", err)
			}

			t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

			f, err := tt.newFetch(dir)
			if err != nil {
				t.Fatalf("new fetcher: %v", err)
			}

			items, errs := tt.drain(f.Fetch(context.Background()))

			if len(items) != 1 {
				t.Fatalf("got %d items, want 1 — the readable file must still be imported", len(items))
			}

			var reported bool

			for _, e := range errs {
				if strings.Contains(e.Error(), "locked.json") && strings.Contains(e.Error(), "read file") {
					reported = true
				}
			}

			if !reported {
				t.Errorf("errors %v should report the unreadable file", errs)
			}
		})
	}
}

// decodeServerResponses never returns an empty slice with a nil error, so a
// file with no usable servers surfaces as a decode error rather than the
// "no MCP servers found" guard.
func TestMCPFileFetcher_Fetch_DirectoryEmptyArrayFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "empty.json", `[]`)
	writeInDir(t, dir, "good.json", `{"name":"io.example/ok","version":"1.0.0"}`)

	f, err := NewMCPFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher: %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 — the valid file must still be imported", len(items))
	}

	var reported bool

	for _, e := range errs {
		if strings.Contains(e.Error(), "empty.json") && strings.Contains(e.Error(), "no valid servers") {
			reported = true
		}
	}

	if !reported {
		t.Errorf("errors %v should report the file containing no servers", errs)
	}
}

func TestA2AFileFetcher_Fetch_DirectoryEmptyArrayFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "empty.json", `[]`)
	writeInDir(t, dir, "good.json", `{"name":"ok","version":"1.0.0"}`)

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	var reported bool

	for _, e := range errs {
		if strings.Contains(e.Error(), "empty.json") && strings.Contains(e.Error(), "no A2A agent cards found") {
			reported = true
		}
	}

	if !reported {
		t.Errorf("errors %v should report the file containing no cards", errs)
	}
}

// A canceled context must stop the run rather than working through the
// remaining files.
func TestFileFetchers_DirectoryStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"a.json", "b.json", "c.json"} {
		writeInDir(t, dir, name, `{"name":"card-`+name+`","version":"1.0.0"}`)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f, err := NewA2AFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	items, _ := drainA2AFetch(f.Fetch(ctx))

	if len(items) == 3 {
		t.Errorf("got all %d items despite a canceled context", len(items))
	}

	mf, err := NewMCPFileFetcher(dir)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher: %v", err)
	}

	mctx, mcancel := context.WithCancel(context.Background())
	mcancel()

	mitems, _ := drainMCPFetch(mf.Fetch(mctx))

	if len(mitems) == 3 {
		t.Errorf("got all %d MCP items despite a canceled context", len(mitems))
	}
}

// Single-file behaviour must be untouched: no file name is prefixed onto the
// error when only one file is in play.
func TestFileFetchers_SingleFileErrorsAreUnqualified(t *testing.T) {
	t.Parallel()

	path := writeInDir(t, t.TempDir(), "cards.json", `{ not json`)

	f, err := NewA2AFileFetcher(path)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher: %v", err)
	}

	_, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(errs) == 0 {
		t.Fatal("expected a decode error")
	}

	if strings.Contains(errs[0].Error(), "cards.json:") {
		t.Errorf("single-file error %q must not be prefixed with the file name", errs[0])
	}
}
