// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
	"github.com/agntcy/dir-importer/enricher/toolhost"
)

const (
	testModel       = "azure:gpt-4o"
	testCommand     = "dirctl"
	dirMCPServerKey = "dir-mcp-server"
)

func writeFile(t *testing.T, name, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writeFile(%q): %v", name, err)
	}

	return path
}

// validToolHost returns a [toolhost.Config] that passes its own Validate(),
// so each enricher-config test can focus on the field under test instead of
// reconstructing a full tool-host fixture per case.
func validToolHost() toolhost.Config {
	return toolhost.Config{
		Model: testModel,
		MCPServers: map[string]toolhost.MCPServerConfig{
			dirMCPServerKey: {
				Command: testCommand,
				Args:    []string{"mcp", "serve"},
			},
		},
		MaxSteps: 10,
	}
}

// Validate delegates the tool-host check to toolhost.Config.Validate(); a
// broken ToolHost must surface as an enricher-config validation failure
// (no silent acceptance, no panic deeper in toolhost.New).
func TestValidate_DelegatesToolHostValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		toolHost    toolhost.Config
		wantErrText string
	}{
		{
			name:        "valid tool host passes",
			toolHost:    validToolHost(),
			wantErrText: "",
		},
		{
			name: "missing model is rejected by tool host",
			toolHost: toolhost.Config{
				MCPServers: map[string]toolhost.MCPServerConfig{
					dirMCPServerKey: {Command: testCommand},
				},
			},
			wantErrText: "model is required",
		},
		{
			name: "missing dir-mcp-server entry is rejected by tool host",
			toolHost: toolhost.Config{
				Model: testModel,
				MCPServers: map[string]toolhost.MCPServerConfig{
					"other-server": {Command: testCommand},
				},
			},
			wantErrText: `mcpServers must include "dir-mcp-server"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := enricherconfig.Config{
				ToolHost:          tt.toolHost,
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
		ToolHost:          validToolHost(),
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
		ToolHost:              validToolHost(),
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
				ToolHost:          validToolHost(),
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
				ToolHost:          validToolHost(),
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

// SkipEnricher must bypass the tool-host / prompt-template / rate-limit
// checks: those fields are unused on the static-enrichment path, and
// requiring them would force callers to configure an LLM they never call.
func TestValidate_SkipEnricher_BypassesLLMConfig(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.Config{
		SkipEnricher: true,
		Skills:       []*typesv1.Skill{{Name: "skill-a", Id: 1}},
		Domains:      []*typesv1.Domain{{Name: "domain-a", Id: 2}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if cfg.SkillsPromptTemplate != "" {
		t.Errorf("prompt templates should not be populated on the skip path, got %q", cfg.SkillsPromptTemplate)
	}
}

// Empty Skills + Domains are valid on the skip path. The static enricher
// will assign empty lists to every record; that's a deterministic and
// well-defined outcome, not a misconfiguration.
func TestValidate_SkipEnricher_AllowsEmptyLists(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.Config{SkipEnricher: true}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// An entry with neither Name nor Id would silently produce an empty struct
// in the record, which downstream OASF consumers reject in confusing ways.
// Validate must catch this at config time, including nil pointer entries.
func TestValidate_SkipEnricher_RejectsEntriesWithoutIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         enricherconfig.Config
		wantErrText string
	}{
		{
			name: "skill with empty name and zero id",
			cfg: enricherconfig.Config{
				SkipEnricher: true,
				Skills:       []*typesv1.Skill{{Name: "ok", Id: 1}, {}},
			},
			wantErrText: "skills[1]",
		},
		{
			name: "domain with empty name and zero id",
			cfg: enricherconfig.Config{
				SkipEnricher: true,
				Domains:      []*typesv1.Domain{{}, {Name: "ok"}},
			},
			wantErrText: "domains[0]",
		},
		{
			name: "nil skill pointer is rejected",
			cfg: enricherconfig.Config{
				SkipEnricher: true,
				Skills:       []*typesv1.Skill{nil},
			},
			wantErrText: "skills[0]: nil entry",
		},
		{
			name: "nil domain pointer is rejected",
			cfg: enricherconfig.Config{
				SkipEnricher: true,
				Domains:      []*typesv1.Domain{nil},
			},
			wantErrText: "domains[0]: nil entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrText, err)
			}
		})
	}
}

// Name-only or Id-only entries are valid: the CLI's --skill <name|id> flag
// is documented to accept either, so the library must accept both.
func TestValidate_SkipEnricher_AcceptsEntriesWithEitherIdentifier(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.Config{
		SkipEnricher: true,
		Skills: []*typesv1.Skill{
			{Name: "name-only"},
			{Id: 42},
		},
		Domains: []*typesv1.Domain{
			{Name: "domain-name-only"},
			{Id: 7},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
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
				ToolHost:          validToolHost(),
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

func TestValidate_LLMMode_StillRequiresToolHost(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.Config{RequestsPerMinute: 2}
	if err := cfg.Validate(); err == nil {
		t.Fatal("LLM mode should fail validation without tool host config")
	}
}
