// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evalDir resolves symlinks in a directory path. Temp dirs on macOS live under
// /var, which is itself a symlink to /private/var, so expected paths must be
// resolved the same way discoverFiles resolves its root.
func evalDir(t *testing.T, path string) string {
	t.Helper()

	eval, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}

	return eval
}

// writeInDir writes body to dir/name and returns the full path.
func writeInDir(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}

func TestDiscoverFiles_MatchingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "alpha.json", "{}")
	writeInDir(t, dir, "beta.json", "{}")

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	want := []string{
		filepath.Join(evalDir(t, dir), "alpha.json"),
		filepath.Join(evalDir(t, dir), "beta.json"),
	}

	if len(got) != len(want) {
		t.Fatalf("got %d files (%v), want %d", len(got), got, len(want))
	}

	// os.ReadDir returns entries sorted by name, so order is deterministic.
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDiscoverFiles_MixedFilesOnlyMatchingReturned(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "card.json", "{}")
	writeInDir(t, dir, "README.md", "not a card")
	writeInDir(t, dir, "notes.txt", "also not a card")
	writeInDir(t, dir, "archive.json.bak", "near miss")

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	want := filepath.Join(evalDir(t, dir), "card.json")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestDiscoverFiles_IgnoresSymlinkEscapingRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()

	// A real card inside the directory, plus a symlink pointing at a card that
	// lives outside it. Only the former may be returned.
	writeInDir(t, dir, "inside.json", "{}")
	target := writeInDir(t, outside, "outside.json", "{}")

	link := filepath.Join(dir, "escape.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	want := filepath.Join(evalDir(t, dir), "inside.json")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s] — escaping symlink must be ignored", got, want)
	}
}

func TestDiscoverFiles_IgnoresBrokenSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "real.json", "{}")

	link := filepath.Join(dir, "dangling.json")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.json"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	want := filepath.Join(evalDir(t, dir), "real.json")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s] — broken symlink must be ignored", got, want)
	}
}

func TestDiscoverFiles_EmptyDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err == nil {
		t.Fatalf("expected an error for an empty directory, got %v", got)
	}

	if !strings.Contains(err.Error(), "no files matching") {
		t.Errorf("error %q should explain that nothing matched", err)
	}
}

func TestDiscoverFiles_DirectoryWithNoMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "README.md", "docs only")

	if _, err := discoverFiles(context.Background(), dir, "*.json"); err == nil {
		t.Fatal("expected an error when no entry matches the pattern")
	}
}

func TestDiscoverFiles_IsNotRecursive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "top.json", "{}")

	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}

	writeInDir(t, nested, "deep.json", "{}")

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	want := filepath.Join(evalDir(t, dir), "top.json")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s] — nested files must not be discovered", got, want)
	}
}

func TestDiscoverFiles_SkipsDirectoryMatchingPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "real.json", "{}")

	// A directory whose name matches the glob must not be returned as a file.
	if err := os.MkdirAll(filepath.Join(dir, "decoy.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := discoverFiles(context.Background(), dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	want := filepath.Join(evalDir(t, dir), "real.json")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s] — a matching directory must be skipped", got, want)
	}
}

func TestDiscoverFiles_Errors(t *testing.T) {
	t.Parallel()

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()

		if _, err := discoverFiles(context.Background(), "", "*.json"); err == nil {
			t.Fatal("expected an error for an empty path")
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "nope")
		if _, err := discoverFiles(context.Background(), missing, "*.json"); err == nil {
			t.Fatal("expected an error for a nonexistent directory")
		}
	})

	t.Run("path is a file", func(t *testing.T) {
		t.Parallel()

		file := writeInDir(t, t.TempDir(), "single.json", "{}")

		_, err := discoverFiles(context.Background(), file, "*.json")
		if err == nil {
			t.Fatal("expected an error when the path is a file")
		}

		if !strings.Contains(err.Error(), "must be a directory") {
			t.Errorf("error %q should say the path must be a directory", err)
		}
	})

	t.Run("invalid pattern", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeInDir(t, dir, "card.json", "{}")

		_, err := discoverFiles(context.Background(), dir, "[")
		if err == nil {
			t.Fatal("expected an error for a malformed pattern")
		}

		if !strings.Contains(err.Error(), "invalid file pattern") {
			t.Errorf("error %q should name the bad pattern", err)
		}
	})
}

func TestDiscoverFiles_ContextCanceled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "card.json", "{}")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := discoverFiles(ctx, dir, "*.json"); err == nil {
		t.Fatal("expected an error when the context is already canceled")
	}
}
