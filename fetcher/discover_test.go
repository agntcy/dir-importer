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

// writeInDir writes body to dir/name and returns the full path.
func writeInDir(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}

// openTestRoot opens dir as an os.Root and closes it when the test ends.
func openTestRoot(t *testing.T, dir string) *os.Root {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot(%q): %v", dir, err)
	}

	t.Cleanup(func() { _ = root.Close() })

	return root
}

// discoverIn is the common shape of these tests: open dir as a root and list
// the entries matching pattern.
func discoverIn(t *testing.T, dir, pattern string) ([]string, error) {
	t.Helper()

	return discoverFiles(context.Background(), openTestRoot(t, dir), pattern)
}

func TestDiscoverFiles_MatchingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "alpha.json", "{}")
	writeInDir(t, dir, "beta.json", "{}")

	got, err := discoverIn(t, dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	// fs.ReadDir returns entries sorted by name, so order is deterministic.
	want := []string{"alpha.json", "beta.json"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
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

	got, err := discoverIn(t, dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	if len(got) != 1 || got[0] != "card.json" {
		t.Fatalf("got %v, want [card.json]", got)
	}
}

func TestDiscoverFiles_IgnoresSymlinkEscapingRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()

	writeInDir(t, dir, "inside.json", "{}")
	target := writeInDir(t, outside, "outside.json", "{}")

	if err := os.Symlink(target, filepath.Join(dir, "escape.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := discoverIn(t, dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	if len(got) != 1 || got[0] != "inside.json" {
		t.Fatalf("got %v, want [inside.json] — escaping symlink must be ignored", got)
	}
}

func TestDiscoverFiles_IgnoresBrokenSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "real.json", "{}")

	if err := os.Symlink(filepath.Join(dir, "does-not-exist.json"), filepath.Join(dir, "dangling.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := discoverIn(t, dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	if len(got) != 1 || got[0] != "real.json" {
		t.Fatalf("got %v, want [real.json] — broken symlink must be ignored", got)
	}
}

func TestDiscoverFiles_EmptyDirectory(t *testing.T) {
	t.Parallel()

	got, err := discoverIn(t, t.TempDir(), "*.json")
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

	if _, err := discoverIn(t, dir, "*.json"); err == nil {
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

	got, err := discoverIn(t, dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	if len(got) != 1 || got[0] != "top.json" {
		t.Fatalf("got %v, want [top.json] — nested files must not be discovered", got)
	}
}

func TestDiscoverFiles_SkipsDirectoryMatchingPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "real.json", "{}")

	if err := os.MkdirAll(filepath.Join(dir, "decoy.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := discoverIn(t, dir, "*.json")
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}

	if len(got) != 1 || got[0] != "real.json" {
		t.Fatalf("got %v, want [real.json] — a matching directory must be skipped", got)
	}
}

func TestDiscoverFiles_InvalidPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "card.json", "{}")

	_, err := discoverIn(t, dir, "[")
	if err == nil {
		t.Fatal("expected an error for a malformed pattern")
	}

	if !strings.Contains(err.Error(), "invalid file pattern") {
		t.Errorf("error %q should name the bad pattern", err)
	}
}

func TestDiscoverFiles_ContextCanceled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "card.json", "{}")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := discoverFiles(ctx, openTestRoot(t, dir), "*.json"); err == nil {
		t.Fatal("expected an error when the context is already canceled")
	}
}

func TestOpenInputs_SingleFile(t *testing.T) {
	t.Parallel()

	path := writeInDir(t, t.TempDir(), "card.json", "{}")

	inputs, err := openInputs(context.Background(), path)
	if err != nil {
		t.Fatalf("openInputs: %v", err)
	}

	defer inputs.close()

	if inputs.root != nil {
		t.Error("a single file must not hold a directory root")
	}

	if len(inputs.names) != 1 || inputs.names[0] != path {
		t.Fatalf("names = %v, want [%s]", inputs.names, path)
	}

	if inputs.multi() {
		t.Error("one file must not count as multi")
	}

	if got := inputs.label(path); got != "" {
		t.Errorf("label = %q, want empty so single-file errors stay unqualified", got)
	}
}

func TestOpenInputs_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInDir(t, dir, "one.json", "{}")
	writeInDir(t, dir, "two.json", "{}")

	inputs, err := openInputs(context.Background(), dir)
	if err != nil {
		t.Fatalf("openInputs: %v", err)
	}

	defer inputs.close()

	if inputs.root == nil {
		t.Fatal("a directory must be held open as a root")
	}

	if len(inputs.names) != 2 {
		t.Fatalf("names = %v, want 2 entries", inputs.names)
	}

	if !inputs.multi() {
		t.Error("two files must count as multi")
	}

	if got := inputs.label("one.json"); got != "one.json: " {
		t.Errorf("label = %q, want %q", got, "one.json: ")
	}
}

func TestOpenInputs_Errors(t *testing.T) {
	t.Parallel()

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()

		if _, err := openInputs(context.Background(), ""); err == nil {
			t.Fatal("expected an error for an empty path")
		}
	})

	t.Run("nonexistent path", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "nope")

		_, err := openInputs(context.Background(), missing)
		if err == nil {
			t.Fatal("expected an error for a nonexistent path")
		}

		// Callers assert on this wording for a missing single file.
		if !strings.Contains(err.Error(), "read file") {
			t.Errorf("error %q should read as a read failure", err)
		}
	})
}

// A path accepted during discovery must still be safe at read time. Replacing
// an entry with a symlink pointing outside the directory after it was listed
// must not let the fetcher read the external file: the root enforces
// containment when the file is opened, not when it was listed.
func TestInputSet_ReadRejectsSymlinkSwappedAfterDiscovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()

	secret := writeInDir(t, outside, "secret.json", `{"secret":"must not be read"}`)
	writeInDir(t, dir, "card.json", `{"name":"legit","version":"1.0.0"}`)

	inputs, err := openInputs(context.Background(), dir)
	if err != nil {
		t.Fatalf("openInputs: %v", err)
	}

	defer inputs.close()

	if len(inputs.names) != 1 || inputs.names[0] != "card.json" {
		t.Fatalf("names = %v, want [card.json]", inputs.names)
	}

	// Swap the accepted entry for a symlink out of the directory, exactly the
	// window between listing and reading.
	swapped := filepath.Join(dir, "card.json")
	if err := os.Remove(swapped); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(secret, swapped); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	raw, err := inputs.read("card.json")
	if err == nil {
		t.Fatalf("read succeeded and returned %q; the escaping symlink must be rejected", raw)
	}

	if strings.Contains(string(raw), "must not be read") {
		t.Fatal("read returned the contents of a file outside the directory")
	}
}
