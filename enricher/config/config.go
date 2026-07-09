// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	"github.com/agntcy/dir-importer/enricher/toolhost"
)

// DefaultRequestsPerMinute is the fallback maximum LLM API requests per minute (to avoid rate limit errors).
const DefaultRequestsPerMinute = 2

//go:embed enricher.skills.prompt.md
var DefaultSkillsPromptTemplate string

//go:embed enricher.domains.prompt.md
var DefaultDomainsPromptTemplate string

// Config selects the enrichment method for the pipeline stage. The three
// methods are mutually exclusive and share equal standing; exactly one should be
// set. importer.New selects in the order Static, Extractor, LLM, and errors when
// none is configured.
type Config struct {
	Static    *StaticConfig    // Fixed skills/domains stamped on every record.
	Extractor *ExtractorConfig // Deterministic, in-process taxonomy extractor.
	LLM       *LLMConfig       // LLM agent over an MCP tool host.
}

// StaticConfig configures the static enricher: a fixed set of OASF taxonomy
// entries assigned to every record.
type StaticConfig struct {
	Skills  []*typesv1.Skill  // OASF skill taxonomy entries assigned to every record.
	Domains []*typesv1.Domain // OASF domain taxonomy entries assigned to every record.
}

// ExtractorConfig configures the extractor enricher. The caller provisions the
// Extractor and injects it here.
type ExtractorConfig struct {
	Extractor RecordExtractor // Deterministic extractor; must be non-nil.
}

// LLMConfig configures the LLM enricher: an LLM agent talking to an MCP tool host.
type LLMConfig struct {
	ToolHost              toolhost.Config // Tool-host configuration (model, MCP servers, max-steps)
	SkillsPromptTemplate  string          // Path to custom skills prompt template file (empty = embedded default)
	DomainsPromptTemplate string          // Path to custom domains prompt template file (empty = embedded default)
	RequestsPerMinute     int             // Maximum LLM API requests per minute (to avoid rate limit errors)
}

// RecordExtractor deterministically classifies record text into OASF skills and
// domains, without an LLM. Implementations should scope extraction to the latest
// OASF version (enrich-on-import semantics). It is satisfied by a caller-side
// adapter over the oasf-sdk extractor and is trivially faked in tests. Keeping
// this interface dir-importer-local keeps the oasf-sdk extractor package (and its
// ML dependencies) out of dir-importer's build graph. It lives here (rather than
// in the enricher package) so ExtractorConfig can hold it without an import cycle.
type RecordExtractor interface {
	// Extract classifies text into OASF skills and domains.
	Extract(ctx context.Context, text string) (ExtractResult, error)
}

// ExtractResult holds the skills and domains a RecordExtractor recommends.
type ExtractResult struct {
	Skills  []TaxonomyClass
	Domains []TaxonomyClass
}

// TaxonomyClass is a single OASF skill or domain identified by name and/or id.
type TaxonomyClass struct {
	ID   uint32 // OASF numeric id
	Name string // hierarchical OASF name
}

// Validate checks that exactly one enrichment method is configured and that the
// configured method is itself valid. The precedence mirrors importer.New:
// Static, then Extractor, then LLM.
func (c *Config) Validate() error {
	switch {
	case c.Static != nil:
		return c.Static.Validate()
	case c.Extractor != nil:
		return c.Extractor.Validate()
	case c.LLM != nil:
		return c.LLM.Validate()
	default:
		return errors.New("no enrichment method configured (set one of Static, Extractor, LLM)")
	}
}

// Validate enforces that every taxonomy entry carries at least one identifier
// (name or id).
func (c *StaticConfig) Validate() error {
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

// Validate ensures an extractor was injected.
func (c *ExtractorConfig) Validate() error {
	if c.Extractor == nil {
		return errors.New("extractor must not be nil")
	}

	return nil
}

// Validate checks the LLM/MCP configuration.
func (c *LLMConfig) Validate() error {
	if err := c.ToolHost.Validate(); err != nil {
		return fmt.Errorf("tool host: %w", err)
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
func (c *LLMConfig) SkillsPrompt() (string, error) {
	return loadPromptTemplate("skills", c.SkillsPromptTemplate, DefaultSkillsPromptTemplate)
}

// DomainsPrompt returns the domains prompt template content: the contents of
// the DomainsPromptTemplate file, or the embedded default when no path is set.
func (c *LLMConfig) DomainsPrompt() (string, error) {
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
