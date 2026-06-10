// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const skillFileName = "SKILL.md"

// DiscoverSkillDirectories walks root recursively and returns every directory that
// contains SKILL.md directly. When a skill directory is found, its subdirectories
// are not searched (references/, scripts/, etc. belong to that skill).
// The walk stops promptly when ctx is canceled.
func DiscoverSkillDirectories(ctx context.Context, root string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover skill directories: %w", err)
	}

	resolvedRoot, err := resolveSearchRoot(root)
	if err != nil {
		return nil, err
	}

	var skillDirs []string

	walkErr := filepath.WalkDir(resolvedRoot, discoverSkillWalkFunc(ctx, resolvedRoot, &skillDirs))
	if walkErr != nil {
		return nil, fmt.Errorf("walk skill directories: %w", walkErr)
	}

	if len(skillDirs) == 0 {
		return nil, fmt.Errorf("no agent skills found under %s", resolvedRoot)
	}

	return skillDirs, nil
}

func discoverSkillWalkFunc(ctx context.Context, resolvedRoot string, skillDirs *[]string) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		within, err := isPathWithinRoot(resolvedRoot, path)
		if err != nil {
			return err
		}

		if !within {
			return filepath.SkipDir
		}

		skillPath := filepath.Join(path, skillFileName)

		info, err := os.Stat(skillPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return fmt.Errorf("stat %s: %w", skillPath, err)
		}

		if info.IsDir() {
			return nil
		}

		skillWithinRoot, err := isPathWithinRoot(resolvedRoot, skillPath)
		if err != nil {
			return err
		}

		if !skillWithinRoot {
			// SKILL.md may be a symlink pointing outside the search root; ignore it
			// and keep walking subdirectories.
			return nil
		}

		normalized, normErr := filepath.EvalSymlinks(path)
		if normErr != nil {
			normalized = path
		}

		*skillDirs = append(*skillDirs, normalized)

		return filepath.SkipDir
	}
}

func resolveSearchRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve skill search root: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat skill search root: %w", err)
	}

	if !info.IsDir() {
		return "", errors.New("skill path must be a directory")
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for skill search root: %w", err)
	}

	return resolved, nil
}

func isPathWithinRoot(root, path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve path %q: %w", path, err)
	}

	evalPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return false, fmt.Errorf("resolve symlinks for %q: %w", path, err)
	}

	rel, err := filepath.Rel(root, evalPath)
	if err != nil {
		return false, fmt.Errorf("rel path from root: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}

	return true, nil
}
