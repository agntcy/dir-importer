// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package toolhost_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/enricher/toolhost"
)

const (
	testModel         = "azure:gpt-4o"
	testCommand       = "dirctl"
	testMCPSubcommand = "mcp"
	testServeArg      = "serve"
	dirMCPServerKey   = "dir-mcp-server"
)

// Sample JSON document used to exercise the documented migration path:
// callers unmarshal the configuration themselves and hand the struct to the
// library.
const sampleConfigJSON = `{
  "mcpServers": {
    "dir-mcp-server": {
      "command": "dirctl",
      "args": ["mcp", "serve"]
    }
  },
  "model": "azure:gpt-4o",
  "max-steps": 7
}`

func TestConfig_UnmarshalFromJSON(t *testing.T) {
	t.Parallel()

	var cfg toolhost.Config
	if err := json.Unmarshal([]byte(sampleConfigJSON), &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if cfg.Model != testModel {
		t.Errorf("model: got %q, want %q", cfg.Model, testModel)
	}

	if cfg.MaxSteps != 7 {
		t.Errorf("max-steps: got %d, want 7", cfg.MaxSteps)
	}

	srv, ok := cfg.MCPServers[dirMCPServerKey]
	if !ok {
		t.Fatalf("mcpServers[%q] missing after unmarshal", dirMCPServerKey)
	}

	if srv.Command != testCommand {
		t.Errorf("command: got %q, want %q", srv.Command, testCommand)
	}

	if len(srv.Args) != 2 || srv.Args[0] != testMCPSubcommand || srv.Args[1] != testServeArg {
		t.Errorf("args: got %#v", srv.Args)
	}
}

func TestConfig_Validate_HappyPath(t *testing.T) {
	t.Parallel()

	cfg := toolhost.Config{
		Model: testModel,
		MCPServers: map[string]toolhost.MCPServerConfig{
			dirMCPServerKey: {
				Command: testCommand,
				Args:    []string{testMCPSubcommand, testServeArg},
			},
		},
		MaxSteps: 7,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if cfg.MaxSteps != 7 {
		t.Errorf("MaxSteps should remain 7, got %d", cfg.MaxSteps)
	}
}

// MaxSteps <= 0 must not be rejected; instead it is defaulted so callers can
// omit the field from a freshly-decoded JSON document. This preserves the
// prior LoadFileConfig behaviour and keeps the migration source-compatible
// for users who never set max-steps.
func TestConfig_Validate_DefaultsMaxSteps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
	}{
		{name: "zero defaults to 10", in: 0},
		{name: "negative defaults to 10", in: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := toolhost.Config{
				Model: testModel,
				MCPServers: map[string]toolhost.MCPServerConfig{
					dirMCPServerKey: {Command: testCommand},
				},
				MaxSteps: tt.in,
			}

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}

			if cfg.MaxSteps != 10 {
				t.Errorf("MaxSteps = %d, want default 10", cfg.MaxSteps)
			}
		})
	}
}

func TestConfig_Validate_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         toolhost.Config
		wantErrText string
	}{
		{
			name: "missing model",
			cfg: toolhost.Config{
				MCPServers: map[string]toolhost.MCPServerConfig{
					dirMCPServerKey: {Command: testCommand},
				},
			},
			wantErrText: "model is required",
		},
		{
			name: "missing dir-mcp-server entry",
			cfg: toolhost.Config{
				Model: testModel,
				MCPServers: map[string]toolhost.MCPServerConfig{
					"other-server": {Command: testCommand},
				},
			},
			wantErrText: `mcpServers must include "dir-mcp-server"`,
		},
		{
			name: "dir-mcp-server entry without command",
			cfg: toolhost.Config{
				Model: testModel,
				MCPServers: map[string]toolhost.MCPServerConfig{
					dirMCPServerKey: {Args: []string{testMCPSubcommand, testServeArg}},
				},
			},
			wantErrText: ".command is required",
		},
		{
			name:        "nil mcpServers map",
			cfg:         toolhost.Config{Model: testModel},
			wantErrText: `mcpServers must include "dir-mcp-server"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrText)
			}

			if !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrText)
			}
		})
	}
}

func TestMCPServerConfig_ExtraEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   *toolhost.MCPServerConfig
		want []string
	}{
		{name: "nil receiver", in: nil, want: nil},
		{name: "empty env", in: &toolhost.MCPServerConfig{}, want: nil},
		{
			name: "single string entry",
			in: &toolhost.MCPServerConfig{Env: map[string]any{
				"OASF_API_VALIDATION_SCHEMA_URL": "https://schema.example",
			}},
			want: []string{"OASF_API_VALIDATION_SCHEMA_URL=https://schema.example"},
		},
		{
			name: "non-string values are skipped",
			in: &toolhost.MCPServerConfig{Env: map[string]any{
				"NUMERIC":  42,
				"STR_ONLY": "ok",
			}},
			want: []string{"STR_ONLY=ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.in.ExtraEnv()
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d (%v), want %d (%v)", len(got), got, len(tt.want), tt.want)
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
