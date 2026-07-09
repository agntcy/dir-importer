// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"context"
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
// so each LLM-config test can focus on the field under test instead of
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

// validLLM returns an [enricherconfig.LLMConfig] that passes Validate().
func validLLM() enricherconfig.LLMConfig {
	return enricherconfig.LLMConfig{
		ToolHost:          validToolHost(),
		RequestsPerMinute: 1,
	}
}

// stubExtractor implements enricherconfig.RecordExtractor for config tests.
type stubExtractor struct{}

func (stubExtractor) Extract(context.Context, string) (enricherconfig.ExtractResult, error) {
	return enricherconfig.ExtractResult{}, nil
}

// --- Config method selection --------------------------------------------------

// Config.Validate must require exactly one enrichment method: with none set it
// errors, and with any one set it delegates to that method's validation.
func TestConfigValidate_RequiresAConfiguredMethod(t *testing.T) {
	t.Parallel()

	var empty enricherconfig.Config
	if err := empty.Validate(); err == nil {
		t.Fatal("expected error when no enrichment method is configured, got nil")
	}
}

func TestConfigValidate_DelegatesToConfiguredMethod(t *testing.T) {
	t.Parallel()

	llm := validLLM()

	cases := []struct {
		name string
		cfg  enricherconfig.Config
	}{
		{"static", enricherconfig.Config{Static: &enricherconfig.StaticConfig{}}},
		{"extractor", enricherconfig.Config{Extractor: &enricherconfig.ExtractorConfig{Extractor: stubExtractor{}}}},
		{"llm", enricherconfig.Config{LLM: &llm}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.cfg.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

// --- LLM config ---------------------------------------------------------------

// LLMConfig.Validate delegates the tool-host check to toolhost.Config.Validate();
// a broken ToolHost must surface as a validation failure (no silent acceptance,
// no panic deeper in toolhost.New).
func TestLLMValidate_DelegatesToolHostValidation(t *testing.T) {
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

			cfg := enricherconfig.LLMConfig{
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
func TestLLMPrompts_DefaultWhenUnset(t *testing.T) {
	t.Parallel()

	cfg := validLLM()

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
func TestLLMPrompts_LoadCustomFromDisk(t *testing.T) {
	t.Parallel()

	const skillsBody = "custom skills prompt body"

	const domainsBody = "custom domains prompt body"

	skillsPath := writeFile(t, "skills.md", skillsBody)
	domainsPath := writeFile(t, "domains.md", domainsBody)

	cfg := enricherconfig.LLMConfig{
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

func TestLLMValidate_RejectsMissingPromptTemplateFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(*enricherconfig.LLMConfig)
		wantErrText string
	}{
		{
			name: "missing skills template file",
			mutate: func(c *enricherconfig.LLMConfig) {
				c.SkillsPromptTemplate = "/nonexistent/skills.md"
			},
			wantErrText: "skills prompt template file not found",
		},
		{
			name: "missing domains template file",
			mutate: func(c *enricherconfig.LLMConfig) {
				c.DomainsPromptTemplate = "/nonexistent/domains.md"
			},
			wantErrText: "domains prompt template file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validLLM()
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
func TestLLMValidate_RejectsEmptyPromptTemplateContents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(t *testing.T, c *enricherconfig.LLMConfig)
		wantErrText string
	}{
		{
			name: "skills template file contains only whitespace",
			mutate: func(t *testing.T, c *enricherconfig.LLMConfig) {
				t.Helper()
				c.SkillsPromptTemplate = writeFile(t, "skills.md", "   \n\t  ")
			},
			wantErrText: "skills prompt template is empty",
		},
		{
			name: "domains template file contains only whitespace",
			mutate: func(t *testing.T, c *enricherconfig.LLMConfig) {
				t.Helper()
				c.DomainsPromptTemplate = writeFile(t, "domains.md", "\n\n")
			},
			wantErrText: "domains prompt template is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validLLM()
			tt.mutate(t, &cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrText, err)
			}
		})
	}
}

func TestLLMValidate_RequestsPerMinuteMustBePositive(t *testing.T) {
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

			cfg := enricherconfig.LLMConfig{
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

func TestLLMValidate_RequiresToolHost(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.LLMConfig{RequestsPerMinute: 2}
	if err := cfg.Validate(); err == nil {
		t.Fatal("LLM config should fail validation without tool host config")
	}
}

// --- Extractor config ---------------------------------------------------------

func TestExtractorValidate_RequiresNonNilExtractor(t *testing.T) {
	t.Parallel()

	empty := enricherconfig.ExtractorConfig{}
	if err := empty.Validate(); err == nil {
		t.Fatal("expected error for nil extractor, got nil")
	}

	set := enricherconfig.ExtractorConfig{Extractor: stubExtractor{}}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// --- Static config ------------------------------------------------------------

// Empty Skills + Domains are valid on the static path. The static enricher will
// assign empty lists to every record; that's a deterministic and well-defined
// outcome, not a misconfiguration.
func TestStaticValidate_AllowsEmptyLists(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.StaticConfig{}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// An entry with neither Name nor Id would silently produce an empty struct
// in the record, which downstream OASF consumers reject in confusing ways.
// Validate must catch this at config time, including nil pointer entries.
func TestStaticValidate_RejectsEntriesWithoutIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         enricherconfig.StaticConfig
		wantErrText string
	}{
		{
			name: "skill with empty name and zero id",
			cfg: enricherconfig.StaticConfig{
				Skills: []*typesv1.Skill{{Name: "ok", Id: 1}, {}},
			},
			wantErrText: "skills[1]",
		},
		{
			name: "domain with empty name and zero id",
			cfg: enricherconfig.StaticConfig{
				Domains: []*typesv1.Domain{{}, {Name: "ok"}},
			},
			wantErrText: "domains[0]",
		},
		{
			name: "nil skill pointer is rejected",
			cfg: enricherconfig.StaticConfig{
				Skills: []*typesv1.Skill{nil},
			},
			wantErrText: "skills[0]: nil entry",
		},
		{
			name: "nil domain pointer is rejected",
			cfg: enricherconfig.StaticConfig{
				Domains: []*typesv1.Domain{nil},
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
func TestStaticValidate_AcceptsEntriesWithEitherIdentifier(t *testing.T) {
	t.Parallel()

	cfg := enricherconfig.StaticConfig{
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
