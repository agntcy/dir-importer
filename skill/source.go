// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/types/known/structpb"
)

// SkillDirectorySet holds the search directory open while discovered skills are parsed.
// Names are relative to the held root, so later opens remain confined even if a
// directory entry is replaced concurrently.
type SkillDirectorySet struct {
	root  *os.Root
	base  string
	names []string
}

// OpenSkillDirectories opens root once and discovers skill directories beneath it.
func OpenSkillDirectories(ctx context.Context, path string) (*SkillDirectorySet, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover skill directories: %w", err)
	}

	resolved, err := resolveSearchRoot(path)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, fmt.Errorf("open skill search root: %w", err)
	}

	set := &SkillDirectorySet{root: root, base: resolved}
	if err := fs.WalkDir(root.FS(), ".", set.walkFunc(ctx)); err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("walk skill directories: %w", err)
	}
	if len(set.names) == 0 {
		_ = root.Close()
		return nil, fmt.Errorf("no agent skills found under %s", resolved)
	}
	return set, nil
}

func (s *SkillDirectorySet) walkFunc(ctx context.Context) fs.WalkDirFunc {
	return func(name string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		skillName := filepath.Join(name, skillFileName)
		info, err := s.root.Stat(skillName)
		if err != nil {
			// A missing, dangling, absolute, or escaping SKILL.md is not a
			// skill. Continue below it because a valid nested skill may exist.
			return nil
		}
		if info.IsDir() {
			return nil
		}

		s.names = append(s.names, name)
		return fs.SkipDir
	}
}

// Close releases the search-root handle.
func (s *SkillDirectorySet) Close() error { return s.root.Close() }

// Paths returns display paths for the discovered skills.
func (s *SkillDirectorySet) Paths() []string {
	out := make([]string, len(s.names))
	for i, name := range s.names {
		if name == "." {
			out[i] = s.base
		} else {
			out[i] = filepath.Join(s.base, name)
		}
	}
	return out
}

// ParseForImport parses one discovered skill through the held search root.
func (s *SkillDirectorySet) ParseForImport(index int) (*structpb.Struct, error) {
	if index < 0 || index >= len(s.names) {
		return nil, fmt.Errorf("skill index %d out of range", index)
	}
	root, err := s.root.OpenRoot(s.names[index])
	if err != nil {
		return nil, fmt.Errorf("open skill directory: %w", err)
	}
	defer root.Close()
	return parseSkillRootForImport(root, s.Paths()[index])
}
