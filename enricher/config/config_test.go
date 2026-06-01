// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
)

func writeFile(t *testing.T, name, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writeFile(%q): %v", name, err)
	}

	return path
}

func TestValidate_ConfigFile(t *testing.T) {
	t.Parallel()

	validConfig := writeFile(t, "enricher.json", `{}`)

	tests := []struct {
		name        string
		configFile  string
		wantErrText string
	}{
		{
			name:        "empty path is rejected",
			configFile:  "",
			wantErrText: "config file is required",
		},
		{
			name:        "missing path is rejected",
			configFile:  "/nonexistent/enricher.json",
			wantErrText: "config file not found",
		},
		{
			name:       "existing path passes the file-existence check",
			configFile: validConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := enricherconfig.Config{
				ConfigFile:        tt.configFile,
				RequestsPerMinute: 1,
			}

			err := cfg.Validate()

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

// When no template path is set, the prompts resolve to the embedded defaults.
// This is load-bearing: an empty prompt silently sends no instructions to the
// LLM, which surfaces as cryptic downstream JSON-parse failures.
func TestPrompts_DefaultWhenUnset(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.Config{
		ConfigFile:        writeFile(t, "enricher.json", `{}`),
		RequestsPerMinute: 1,
	}

	skills, err := cfg.SkillsPrompt()
	if err != nil {
		t.Fatalf("SkillsPrompt: %v", err)
	}

	if skills != enricherconfig.DefaultSkillsPromptTemplate {
		t.Errorf("SkillsPrompt not the embedded default")
	}

	domains, err := cfg.DomainsPrompt()
	if err != nil {
		t.Fatalf("DomainsPrompt: %v", err)
	}

	if domains != enricherconfig.DefaultDomainsPromptTemplate {
		t.Errorf("DomainsPrompt not the embedded default")
	}
}

// When a custom template path is supplied, the prompts resolve to its contents.
// Resolution must not mutate the input path fields.
func TestPrompts_LoadCustomFromDisk(t *testing.T) {
	t.Parallel()

	const skillsBody = "custom skills prompt body"

	const domainsBody = "custom domains prompt body"

	skillsPath := writeFile(t, "skills.md", skillsBody)
	domainsPath := writeFile(t, "domains.md", domainsBody)

	cfg := enricherconfig.Config{
		ConfigFile:            writeFile(t, "enricher.json", `{}`),
		SkillsPromptTemplate:  skillsPath,
		DomainsPromptTemplate: domainsPath,
		RequestsPerMinute:     1,
	}

	skills, err := cfg.SkillsPrompt()
	if err != nil {
		t.Fatalf("SkillsPrompt: %v", err)
	}

	if skills != skillsBody {
		t.Errorf("SkillsPrompt = %q, want %q", skills, skillsBody)
	}

	domains, err := cfg.DomainsPrompt()
	if err != nil {
		t.Fatalf("DomainsPrompt: %v", err)
	}

	if domains != domainsBody {
		t.Errorf("DomainsPrompt = %q, want %q", domains, domainsBody)
	}

	if cfg.SkillsPromptTemplate != skillsPath || cfg.DomainsPromptTemplate != domainsPath {
		t.Errorf("resolution mutated input path fields: skills=%q domains=%q", cfg.SkillsPromptTemplate, cfg.DomainsPromptTemplate)
	}
}

func TestValidate_RejectsMissingPromptTemplateFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(*enricherconfig.Config)
		wantErrText string
	}{
		{
			name: "missing skills template file",
			mutate: func(c *enricherconfig.Config) {
				c.SkillsPromptTemplate = "/nonexistent/skills.md"
			},
			wantErrText: "skills prompt template file not found",
		},
		{
			name: "missing domains template file",
			mutate: func(c *enricherconfig.Config) {
				c.DomainsPromptTemplate = "/nonexistent/domains.md"
			},
			wantErrText: "domains prompt template file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := enricherconfig.Config{
				ConfigFile:        writeFile(t, "enricher.json", `{}`),
				RequestsPerMinute: 1,
			}
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrText, err)
			}
		})
	}
}

// Custom-template files that exist but are blank must be rejected. Without this
// guard the LLM was sent a record with no instructions and replied in prose,
// which then failed JSON parsing several layers downstream. This regression
// test pins the explicit non-empty check at the end of Validate.
func TestValidate_RejectsEmptyPromptTemplateContents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(t *testing.T, c *enricherconfig.Config)
		wantErrText string
	}{
		{
			name: "skills template file contains only whitespace",
			mutate: func(t *testing.T, c *enricherconfig.Config) {
				t.Helper()
				c.SkillsPromptTemplate = writeFile(t, "skills.md", "   \n\t  ")
			},
			wantErrText: "skills prompt template is empty",
		},
		{
			name: "domains template file contains only whitespace",
			mutate: func(t *testing.T, c *enricherconfig.Config) {
				t.Helper()
				c.DomainsPromptTemplate = writeFile(t, "domains.md", "\n\n")
			},
			wantErrText: "domains prompt template is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := enricherconfig.Config{
				ConfigFile:        writeFile(t, "enricher.json", `{}`),
				RequestsPerMinute: 1,
			}
			tt.mutate(t, &cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrText, err)
			}
		})
	}
}

func TestValidate_RequestsPerMinuteMustBePositive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		requestsPerMinute int
		wantErr           bool
	}{
		{name: "zero is rejected", requestsPerMinute: 0, wantErr: true},
		{name: "negative is rejected", requestsPerMinute: -5, wantErr: true},
		{name: "positive is accepted", requestsPerMinute: 1, wantErr: false},
		{name: "large positive is accepted", requestsPerMinute: 1000, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := enricherconfig.Config{
				ConfigFile:        writeFile(t, "enricher.json", `{}`),
				RequestsPerMinute: tt.requestsPerMinute,
			}

			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "requests per minute") {
					t.Fatalf("expected requests-per-minute error, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
