// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"context"
	"strings"
	"testing"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	importerconfig "github.com/agntcy/dir-importer/config"
	"github.com/agntcy/dir-importer/enricher"
	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
)

const (
	errFilePathRequired = "file path is required"
	testFilePath        = "/some/path.json"
)

// skipEnricher is the minimum [enricherconfig.Config] that passes Validate()
// without an LLM: it short-circuits the tool-host / template / rate-limit
// checks, letting these tests focus on the importer-level fields under test
// (Type, FilePath, RegistryURL) instead of reconstructing a full LLM config
// per case.
func skipEnricher() enricherconfig.Config {
	return enricherconfig.Config{SkipEnricher: true}
}

func TestValidate_TypeDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         importerconfig.Config
		wantErrText string // empty = expect success
	}{
		{
			name:        "empty type is rejected",
			cfg:         importerconfig.Config{Enricher: skipEnricher()},
			wantErrText: "import type is required",
		},
		{
			name: "unsupported type is rejected",
			cfg: importerconfig.Config{
				Type:     "made-up",
				Enricher: skipEnricher(),
			},
			wantErrText: "unsupported import type",
		},
		{
			name: "mcp-registry without RegistryURL is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeMCPRegistry,
				Enricher: skipEnricher(),
			},
			wantErrText: "registry URL is required",
		},
		{
			name: "mcp-registry with RegistryURL is accepted",
			cfg: importerconfig.Config{
				Type:        importerconfig.ImportTypeMCPRegistry,
				RegistryURL: "https://example.invalid/v0",
				Enricher:    skipEnricher(),
			},
		},
		{
			name: "mcp file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeMCP,
				Enricher: skipEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "a2a file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeA2A,
				Enricher: skipEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "agent-skill import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeAgentSkill,
				Enricher: skipEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "mcp file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeMCP,
				FilePath: testFilePath,
				Enricher: skipEnricher(),
			},
		},
		{
			name: "a2a file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeA2A,
				FilePath: testFilePath,
				Enricher: skipEnricher(),
			},
		},
		{
			name: "agent-skill import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeAgentSkill,
				FilePath: testFilePath,
				Enricher: skipEnricher(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()

			switch {
			case tt.wantErrText != "":
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrText)
				}

				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrText)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// SkipEnricher must let the importer-level Validate succeed without an LLM
// configuration; the static-enrichment path doesn't use a tool host. The
// matching un-skipped config (with the same empty ToolHost) must fail, to
// prove the short-circuit is doing the work.
func TestValidate_SkipEnricherShortCircuitsLLMConfig(t *testing.T) {
	t.Parallel()

	cfgWithoutSkip := importerconfig.Config{
		Type:     importerconfig.ImportTypeMCP,
		FilePath: testFilePath,
	}
	if err := cfgWithoutSkip.Validate(); err == nil {
		t.Fatal("expected enricher config validation to fail without SkipEnricher, got nil")
	}

	cfgWithSkip := importerconfig.Config{
		Type:     importerconfig.ImportTypeMCP,
		FilePath: testFilePath,
		Enricher: enricherconfig.Config{
			SkipEnricher: true,
			Skills:       []*typesv1.Skill{{Name: "skill-a", Id: 1}},
		},
	}
	if err := cfgWithSkip.Validate(); err != nil {
		t.Fatalf("expected SkipEnricher to bypass LLM validation, got %v", err)
	}
}

// stubExtractor implements enricher.RecordExtractor for validation tests.
type stubExtractor struct{}

func (stubExtractor) Extract(_ context.Context, _ string) (enricher.ExtractResult, error) {
	return enricher.ExtractResult{}, nil
}

// TestValidate_ExtractorMode_SkipsLLMValidation verifies that injecting an
// Extractor bypasses the LLM/MCP enricher validation (no tool-host config
// required), while SkipEnricher is not set.
func TestValidate_ExtractorMode_SkipsLLMValidation(t *testing.T) {
	t.Parallel()

	cfg := importerconfig.Config{
		Type:      importerconfig.ImportTypeMCP,
		FilePath:  testFilePath,
		Extractor: stubExtractor{},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected Extractor mode to bypass LLM validation, got %v", err)
	}
}
