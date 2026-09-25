// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const skillFileName = "SKILL.md"

// DiscoverSkillDirectories walks root recursively and returns every directory that
// contains SKILL.md directly. When a skill directory is found, its subdirectories
// are not searched (references/, scripts/, etc. belong to that skill).
// The walk stops promptly when ctx is canceled.
func DiscoverSkillDirectories(ctx context.Context, root string) ([]string, error) {
	set, err := OpenSkillDirectories(ctx, root)
	if err != nil {
		return nil, err
	}
	defer set.Close()

	return set.Paths(), nil
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
