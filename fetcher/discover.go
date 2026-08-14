// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// jsonPattern matches the files picked up when a file-based fetcher is pointed
// at a directory instead of a single file.
const jsonPattern = "*.json"

// inputSet is the set of files one fetch should read, together with a safe way
// to open them.
//
// When the configured path is a directory it is held open as an [os.Root], and
// every read goes through that root. The kernel then enforces containment at
// open time, so an entry cannot be swapped for a symlink pointing outside the
// directory between being listed and being read.
type inputSet struct {
	// root is non-nil when the configured path was a directory.
	root *os.Root
	// names are entry names within root, or the single configured path when
	// root is nil.
	names []string
}

// openInputs resolves the path a file-based fetcher was configured with into
// the files it should read.
//
// A file yields itself, preserving single-file behaviour exactly. A directory
// yields its top-level *.json entries. A path that cannot be stat'ed is
// reported as a read failure, matching the error callers produced before
// directories were supported.
//
// The caller must call close on the returned set.
func openInputs(ctx context.Context, path string) (*inputSet, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is empty")
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	if !info.IsDir() {
		return &inputSet{names: []string{path}}, nil
	}

	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open directory: %w", err)
	}

	names, err := discoverFiles(ctx, root, jsonPattern)
	if err != nil {
		_ = root.Close()

		return nil, err
	}

	return &inputSet{root: root, names: names}, nil
}

// close releases the directory handle, if one is held.
func (s *inputSet) close() {
	if s != nil && s.root != nil {
		_ = s.root.Close()
	}
}

// multi reports whether more than one file is being read.
func (s *inputSet) multi() bool {
	return len(s.names) > 1
}

// label qualifies an error message with the file it came from, but only when
// more than one file is being read, so single-file messages are unchanged.
func (s *inputSet) label(name string) string {
	if !s.multi() {
		return ""
	}

	return filepath.Base(name) + ": "
}

// read returns the contents of one entry.
//
// In directory mode the file is opened through the root, which refuses to
// follow a symlink out of it. Containment is enforced by the open itself rather
// than by an earlier check against a path string, so there is no window in which
// the entry can be replaced by an escaping symlink.
// Errors are returned unwrapped so callers keep sole ownership of the message
// they present, as they did when this was a direct os.ReadFile call.
func (s *inputSet) read(name string) ([]byte, error) {
	if s.root == nil {
		return os.ReadFile(name) //nolint:wrapcheck // caller wraps with its own context
	}

	f, err := s.root.Open(name)
	if err != nil {
		return nil, err //nolint:wrapcheck // caller wraps with its own context
	}
	defer func() { _ = f.Close() }()

	return io.ReadAll(f) //nolint:wrapcheck // caller wraps with its own context
}

// discoverFiles returns the names of entries directly inside root whose name
// matches pattern (as understood by [filepath.Match], e.g. "*.json").
//
// The search is deliberately NOT recursive: a directory of agent cards is a flat
// batch export, and descending into subdirectories would pick up unrelated JSON.
// This differs from skill.DiscoverSkillDirectories, where a skill legitimately is
// a directory tree.
//
// Entries that cannot yield a readable file are skipped rather than failing the
// whole call: subdirectories, dangling symlinks, and symlinks resolving outside
// root, all of which the root itself rejects or reports.
//
// Results follow [fs.ReadDir] order, which is sorted by name. An error is
// returned when nothing matches, mirroring skill.DiscoverSkillDirectories.
func discoverFiles(ctx context.Context, root *os.Root, pattern string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}

	// Reject a malformed pattern up front; otherwise every entry would silently
	// fail to match and the call would look like an empty directory.
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("invalid file pattern %q: %w", pattern, err)
	}

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", root.Name(), err)
	}

	var names []string

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("discover files: %w", err)
		}

		matched, err := filepath.Match(pattern, entry.Name())
		if err != nil || !matched {
			continue
		}

		// Lstat identifies a symlink without following it, so that an entry is
		// only resolved when it actually is one. Note this deliberately does
		// not test readability: an unreadable regular file stays in the list so
		// that reading it reports the permission error rather than the file
		// disappearing silently.
		info, err := root.Lstat(entry.Name())
		if err != nil {
			continue
		}

		if info.Mode()&fs.ModeSymlink != 0 {
			// Keep a link only when it resolves to a file inside the root. The
			// root rejects links that escape it, dangle, or are absolute.
			target, terr := root.Stat(entry.Name())
			if terr != nil || target.IsDir() {
				continue
			}
		} else if info.IsDir() {
			continue
		}

		names = append(names, entry.Name())
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("no files matching %q found in %s", pattern, root.Name())
	}

	return names, nil
}
