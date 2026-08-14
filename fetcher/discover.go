// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// discoverFiles returns the files directly inside root whose base name matches
// pattern (as understood by [filepath.Match], e.g. "*.json").
//
// The search is deliberately NOT recursive: a directory of agent cards is a flat
// batch export, and descending into subdirectories would pick up unrelated JSON.
// This differs from skill.DiscoverSkillDirectories, where a skill legitimately is
// a directory tree.
//
// Entries that cannot contribute a readable file are skipped rather than failing
// the whole call: subdirectories, broken symlinks, and symlinks that resolve
// outside root (a path-escape attempt). Returned paths are rooted at the resolved
// root, so callers report the location the user actually pointed at rather than a
// symlink target elsewhere on disk.
//
// Results are ordered by file name, since [os.ReadDir] returns sorted entries.
// An error is returned when nothing matches, mirroring
// skill.DiscoverSkillDirectories.
func discoverFiles(ctx context.Context, root, pattern string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}

	// Reject a malformed pattern up front; otherwise every entry would silently
	// fail to match and the call would look like an empty directory.
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("invalid file pattern %q: %w", pattern, err)
	}

	resolvedRoot, err := resolveDirRoot(root)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", resolvedRoot, err)
	}

	var files []string

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("discover files: %w", err)
		}

		matched, err := filepath.Match(pattern, entry.Name())
		if err != nil || !matched {
			continue
		}

		path := filepath.Join(resolvedRoot, entry.Name())

		// Stat follows symlinks, so this both resolves link targets and drops
		// broken links and directories that happen to match the pattern.
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}

		within, err := isWithinRoot(resolvedRoot, path)
		if err != nil || !within {
			continue
		}

		files = append(files, path)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files matching %q found in %s", pattern, resolvedRoot)
	}

	return files, nil
}

// resolveDirRoot turns path into an absolute, symlink-free directory path.
// It is the fetcher-local counterpart of skill's search-root resolution.
func resolveDirRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("directory path is empty")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve directory path: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat directory path: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory", abs)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for directory path: %w", err)
	}

	return resolved, nil
}

// isWithinRoot reports whether path, once symlinks are resolved, still lives
// under root. It is what stops a symlink inside the directory from pulling in a
// file from elsewhere on disk.
func isWithinRoot(root, path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve path %q: %w", path, err)
	}

	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false, fmt.Errorf("resolve symlinks for %q: %w", path, err)
	}

	rel, err := filepath.Rel(root, eval)
	if err != nil {
		return false, fmt.Errorf("rel path from root: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}

	return true, nil
}
