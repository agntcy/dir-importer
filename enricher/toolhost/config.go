// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package toolhost

import (
	"errors"
	"fmt"
)

// dirMCPServerKey is the only MCP server entry the enricher uses (stdio to dir MCP).
const dirMCPServerKey = "dir-mcp-server"

// defaultMaxSteps is the fallback tool-loop limit applied when callers leave [Config.MaxSteps] unset.
const defaultMaxSteps = 10

// Config is the tool-host configuration (model, MCP servers, tool-loop limits).
type Config struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	Model      string                     `json:"model"`
	MaxSteps   int                        `json:"max-steps"`
}

// MCPServerConfig describes one MCP server (stdio).
type MCPServerConfig struct {
	Command string         `json:"command"`
	Args    []string       `json:"args"`
	Env     map[string]any `json:"env,omitempty"`
}

// Validate checks the configuration and fills in defaults.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("toolhost config: nil")
	}

	if c.Model == "" {
		return errors.New("toolhost config: model is required")
	}

	if _, err := c.dirMCPServer(); err != nil {
		return err
	}

	if c.MaxSteps <= 0 {
		c.MaxSteps = defaultMaxSteps
	}

	return nil
}

// dirMCPServer returns the required dir MCP stdio server; other mcpServers keys are ignored.
func (c *Config) dirMCPServer() (*MCPServerConfig, error) {
	if c == nil {
		return nil, errors.New("toolhost config: nil")
	}

	s, ok := c.MCPServers[dirMCPServerKey]
	if !ok {
		return nil, fmt.Errorf("toolhost config: mcpServers must include %q", dirMCPServerKey)
	}

	if s.Command == "" {
		return nil, fmt.Errorf("toolhost config: mcpServers[%q].command is required", dirMCPServerKey)
	}

	return &s, nil
}

// ExtraEnv returns KEY=value pairs to append to the subprocess environment (merged with os.Environ by mcp-go).
func (m *MCPServerConfig) ExtraEnv() []string {
	if m == nil || len(m.Env) == 0 {
		return nil
	}

	out := make([]string, 0, len(m.Env))
	for k, v := range m.Env {
		s, ok := v.(string)
		if !ok {
			continue
		}

		out = append(out, fmt.Sprintf("%s=%s", k, s))
	}

	return out
}
