// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"context"
	"strings"
	"testing"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	importerconfig "github.com/agntcy/dir-importer/config"
	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
)

const (
	errFilePathRequired = "file path is required"
	testFilePath        = "/some/path.json"
)

// staticEnricher is the minimum [enricherconfig.Config] that passes Validate()
// without an LLM: the static method needs no tool-host / template / rate-limit
// config, letting these tests focus on the importer-level fields under test
// (Type, FilePath, RegistryURL) instead of reconstructing a full LLM config
// per case.
func staticEnricher() enricherconfig.Config {
	return enricherconfig.Config{Static: &enricherconfig.StaticConfig{}}
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
			cfg:         importerconfig.Config{Enricher: staticEnricher()},
			wantErrText: "import type is required",
		},
		{
			name: "unsupported type is rejected",
			cfg: importerconfig.Config{
				Type:     "made-up",
				Enricher: staticEnricher(),
			},
			wantErrText: "unsupported import type",
		},
		{
			name: "mcp-registry without RegistryURL is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeMCPRegistry,
				Enricher: staticEnricher(),
			},
			wantErrText: "registry URL is required",
		},
		{
			name: "mcp-registry with RegistryURL is accepted",
			cfg: importerconfig.Config{
				Type:        importerconfig.ImportTypeMCPRegistry,
				RegistryURL: "https://example.invalid/v0",
				Enricher:    staticEnricher(),
			},
		},
		{
			name: "mcp file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeMCP,
				Enricher: staticEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "a2a file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeA2A,
				Enricher: staticEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "agent-skill import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeAgentSkill,
				Enricher: staticEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "oasf file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeOASF,
				Enricher: staticEnricher(),
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "mcp file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeMCP,
				FilePath: testFilePath,
				Enricher: staticEnricher(),
			},
		},
		{
			name: "a2a file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeA2A,
				FilePath: testFilePath,
				Enricher: staticEnricher(),
			},
		},
		{
			name: "agent-skill import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeAgentSkill,
				FilePath: testFilePath,
				Enricher: staticEnricher(),
			},
		},
		{
			name: "oasf file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:     importerconfig.ImportTypeOASF,
				FilePath: testFilePath,
				Enricher: staticEnricher(),
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

// The static method must let the importer-level Validate succeed without an LLM
// configuration; it doesn't use a tool host. A config with no enrichment method
// set must fail, proving Validate requires exactly one method.
func TestValidate_StaticMethodSkipsLLMConfig(t *testing.T) {
	t.Parallel()

	cfgNoMethod := importerconfig.Config{
		Type:     importerconfig.ImportTypeMCP,
		FilePath: testFilePath,
	}
	if err := cfgNoMethod.Validate(); err == nil {
		t.Fatal("expected validation to fail when no enrichment method is configured, got nil")
	}

	cfgStatic := importerconfig.Config{
		Type:     importerconfig.ImportTypeMCP,
		FilePath: testFilePath,
		Enricher: enricherconfig.Config{
			Static: &enricherconfig.StaticConfig{
				Skills: []*typesv1.Skill{{Name: "skill-a", Id: 1}},
			},
		},
	}
	if err := cfgStatic.Validate(); err != nil {
		t.Fatalf("expected static method to bypass LLM validation, got %v", err)
	}
}

// stubExtractor implements enricherconfig.RecordExtractor for validation tests.
type stubExtractor struct{}

func (stubExtractor) Extract(_ context.Context, _ string) (enricherconfig.ExtractResult, error) {
	return enricherconfig.ExtractResult{}, nil
}

// TestValidate_ExtractorMethodSkipsLLMConfig verifies that a configured
// Extractor method bypasses the LLM/MCP validation (no tool-host config
// required).
func TestValidate_ExtractorMethodSkipsLLMConfig(t *testing.T) {
	t.Parallel()

	cfg := importerconfig.Config{
		Type:     importerconfig.ImportTypeMCP,
		FilePath: testFilePath,
		Enricher: enricherconfig.Config{
			Extractor: &enricherconfig.ExtractorConfig{Extractor: stubExtractor{}},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected Extractor method to bypass LLM validation, got %v", err)
	}
}
