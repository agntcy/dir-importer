// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
)

// Default values for enricher configuration.
const (
	DefaultConfigFile        = "importer/enricher/enricher.json"
	DefaultRequestsPerMinute = 2 // Default maximum LLM API requests per minute (to avoid rate limit errors)
)

//go:embed enricher.skills.prompt.md
var DefaultSkillsPromptTemplate string

//go:embed enricher.domains.prompt.md
var DefaultDomainsPromptTemplate string

// Config contains configuration for the enricher pipeline stage.
type Config struct {
	ConfigFile            string // Path to enricher JSON (model, mcpServers, max-steps)
	SkillsPromptTemplate  string // Path to custom skills prompt template file (empty = embedded default)
	DomainsPromptTemplate string // Path to custom domains prompt template file (empty = embedded default)
	RequestsPerMinute     int    // Maximum LLM API requests per minute (to avoid rate limit errors)

	SkipEnricher bool              // SkipEnricher bypasses LLM-based enrichment entirely.
	Skills       []*typesv1.Skill  // OASF skill taxonomy entries assigned to every record when SkipEnricher is true.
	Domains      []*typesv1.Domain // OASF domain taxonomy entries assigned to every record when SkipEnricher is true.
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.SkipEnricher {
		return c.validateSkipEnricher()
	}

	if c.ConfigFile == "" {
		return errors.New("config file is required")
	}

	if _, err := os.Stat(c.ConfigFile); err != nil {
		return fmt.Errorf("config file not found: %w", err)
	}

	if c.RequestsPerMinute <= 0 {
		return errors.New("requests per minute must be greater than 0")
	}

	if err := validatePromptTemplate("skills", c.SkillsPromptTemplate); err != nil {
		return err
	}

	if err := validatePromptTemplate("domains", c.DomainsPromptTemplate); err != nil {
		return err
	}

	return nil
}

// SkillsPrompt returns the skills prompt template content: the contents of the
// SkillsPromptTemplate file, or the embedded default when no path is set.
func (c *Config) SkillsPrompt() (string, error) {
	return loadPromptTemplate("skills", c.SkillsPromptTemplate, DefaultSkillsPromptTemplate)
}

// DomainsPrompt returns the domains prompt template content: the contents of
// the DomainsPromptTemplate file, or the embedded default when no path is set.
func (c *Config) DomainsPrompt() (string, error) {
	return loadPromptTemplate("domains", c.DomainsPromptTemplate, DefaultDomainsPromptTemplate)
}

// validatePromptTemplate checks a custom prompt template path. An empty path is
// valid (the embedded default is used later); a non-empty path must point to an
// existing, non-blank file.
func validatePromptTemplate(label, path string) error {
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s prompt template file not found: %w", label, err)
	}

	if strings.TrimSpace(string(data)) == "" {
		return fmt.Errorf("%s prompt template is empty", label)
	}

	return nil
}

// validateSkipEnricher enforces that every taxonomy entry carries at least
// one identifier (name or id).
func (c *Config) validateSkipEnricher() error {
	for i, s := range c.Skills {
		if s == nil {
			return fmt.Errorf("skills[%d]: nil entry", i)
		}

		if s.GetName() == "" && s.GetId() == 0 {
			return fmt.Errorf("skills[%d]: at least one of name or id must be set", i)
		}
	}

	for i, d := range c.Domains {
		if d == nil {
			return fmt.Errorf("domains[%d]: nil entry", i)
		}

		if d.GetName() == "" && d.GetId() == 0 {
			return fmt.Errorf("domains[%d]: at least one of name or id must be set", i)
		}
	}

	return nil
}

// loadPromptTemplate returns the contents of the template file at path, or the
// embedded fallback when path is empty.
func loadPromptTemplate(label, path, fallback string) (string, error) {
	if path == "" {
		return fallback, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s prompt template file not found: %w", label, err)
	}

	return string(data), nil
}
