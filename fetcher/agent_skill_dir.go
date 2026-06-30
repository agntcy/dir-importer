// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agntcy/dir-importer/skill"
	"github.com/agntcy/dir-importer/types"
)

// agentSkillDirFetcher reads one or more Agent Skill directories (SKILL.md).
type agentSkillDirFetcher struct {
	path string
}

// NewAgentSkillDirFetcher creates a fetcher that reads skills from a directory path.
// The path may be a single skill directory or a root directory; every nested directory
// that contains SKILL.md is imported (see skill.DiscoverSkillDirectories).
func NewAgentSkillDirFetcher(path string) (*agentSkillDirFetcher, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("skill directory path is empty")
	}

	return &agentSkillDirFetcher{path: path}, nil
}

// Fetch discovers skill directories under the configured path, parses each, and emits
// one SourceItem per skill. Per-skill parse failures are reported on errCh without
// aborting the remaining skills.
func (f *agentSkillDirFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	const chanBuf = 8

	outputCh := make(chan types.SourceItem, chanBuf)
	errCh := make(chan error, chanBuf)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		info, err := os.Stat(f.path)
		if err != nil {
			sendError(ctx, errCh, fmt.Errorf("stat skill path: %w", err))

			return
		}

		if !info.IsDir() {
			sendError(ctx, errCh, errors.New("skill path must be a directory"))

			return
		}

		skillDirs, err := skill.DiscoverSkillDirectories(ctx, f.path)
		if err != nil {
			sendError(ctx, errCh, err)

			return
		}

		var emitted int

		for _, skillDir := range skillDirs {
			st, err := skill.ParseSkillDirectoryForImport(skillDir)
			if err != nil {
				sendError(ctx, errCh, fmt.Errorf("%s: %w", skillDir, err))

				continue
			}

			select {
			case <-ctx.Done():
				return
			case outputCh <- types.AgentSkillSourceItem(st):
				emitted++
			}
		}

		if emitted == 0 {
			sendError(ctx, errCh, errors.New("no agent skills could be parsed (check earlier errors)"))
		}
	}()

	return outputCh, errCh
}

func sendError(ctx context.Context, errCh chan<- error, err error) {
	select {
	case errCh <- err:
	case <-ctx.Done():
		select {
		case errCh <- err:
		default:
		}
	}
}
