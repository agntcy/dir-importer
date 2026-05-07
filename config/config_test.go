// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"context"
	"strings"
	"testing"

	importerconfig "github.com/agntcy/dir-importer/config"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
)

const (
	errFilePathRequired = "file path is required"
	testFilePath        = "/some/path.json"
)

type stubEnricher struct{}

func (stubEnricher) Enrich(_ context.Context, _ <-chan *corev1.Record, _ *types.Result) (<-chan *corev1.Record, <-chan error) {
	return nil, nil
}

func TestValidate_TypeDispatch(t *testing.T) {
	t.Parallel()

	override := stubEnricher{}

	tests := []struct {
		name        string
		cfg         importerconfig.Config
		wantErrText string // empty = expect success
	}{
		{
			name:        "empty type is rejected",
			cfg:         importerconfig.Config{EnricherOverride: override},
			wantErrText: "import type is required",
		},
		{
			name: "unsupported type is rejected",
			cfg: importerconfig.Config{
				Type:             "made-up",
				EnricherOverride: override,
			},
			wantErrText: "unsupported import type",
		},
		{
			name: "mcp-registry without RegistryURL is rejected",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeMCPRegistry,
				EnricherOverride: override,
			},
			wantErrText: "registry URL is required",
		},
		{
			name: "mcp-registry with RegistryURL is accepted",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeMCPRegistry,
				RegistryURL:      "https://example.invalid/v0",
				EnricherOverride: override,
			},
		},
		{
			name: "mcp file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeMCP,
				EnricherOverride: override,
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "a2a file import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeA2A,
				EnricherOverride: override,
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "agent-skill import without FilePath is rejected",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeAgentSkill,
				EnricherOverride: override,
			},
			wantErrText: errFilePathRequired,
		},
		{
			name: "mcp file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeMCP,
				FilePath:         testFilePath,
				EnricherOverride: override,
			},
		},
		{
			name: "a2a file import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeA2A,
				FilePath:         testFilePath,
				EnricherOverride: override,
			},
		},
		{
			name: "agent-skill import with FilePath is accepted",
			cfg: importerconfig.Config{
				Type:             importerconfig.ImportTypeAgentSkill,
				FilePath:         testFilePath,
				EnricherOverride: override,
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

// EnricherOverride must short-circuit Enricher.Validate(). Without the override,
// the zero-value enricher config would fail validation (no ConfigFile set),
// proving the short-circuit when Validate succeeds.
func TestValidate_EnricherOverrideShortCircuitsEnricherValidation(t *testing.T) {
	t.Parallel()

	cfgWithoutOverride := importerconfig.Config{
		Type:     importerconfig.ImportTypeMCP,
		FilePath: testFilePath,
	}
	if err := cfgWithoutOverride.Validate(); err == nil {
		t.Fatal("expected enricher config validation to fail without EnricherOverride, got nil")
	}

	cfgWithOverride := importerconfig.Config{
		Type:             importerconfig.ImportTypeMCP,
		FilePath:         testFilePath,
		EnricherOverride: stubEnricher{},
	}
	if err := cfgWithOverride.Validate(); err != nil {
		t.Fatalf("expected EnricherOverride to bypass enricher validation, got %v", err)
	}
}
