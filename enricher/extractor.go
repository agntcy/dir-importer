// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"errors"
	"fmt"
	"strings"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"github.com/agntcy/oasf-sdk/pkg/translator"
	"google.golang.org/protobuf/types/known/structpb"
)

// RecordExtractor deterministically classifies record text into OASF skills and
// domains, without an LLM. Implementations should scope extraction to the latest
// OASF version (enrich-on-import semantics). It is satisfied by a caller-side
// adapter over the oasf-sdk extractor and is trivially faked in tests. Keeping
// this interface dir-importer-local keeps the oasf-sdk extractor package (and its
// ML dependencies) out of dir-importer's build graph.
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

// ExtractorEnricher enriches records with OASF skills and domains using a
// deterministic RecordExtractor instead of an LLM. The extractor is provided by
// the caller (already provisioned) and injected behind the RecordExtractor
// interface so it can be faked in tests.
type ExtractorEnricher struct {
	ext RecordExtractor
}

// NewExtractorEnricher returns an ExtractorEnricher backed by ext. It errors if
// ext is nil, so callers cannot construct a non-functional enricher.
func NewExtractorEnricher(ext RecordExtractor) (*ExtractorEnricher, error) {
	if ext == nil {
		return nil, errors.New("extractor enricher: extractor must not be nil")
	}

	return &ExtractorEnricher{ext: ext}, nil
}

// Enrich reads records from inputCh, classifies each via the extractor, writes
// the resulting skills and domains, and forwards them.
//
//nolint:dupl // Enrich loop mirrors Enricher.Enrich — same goroutine scaffold, different enrichRecord implementation.
func (ee *ExtractorEnricher) Enrich(ctx context.Context, inputCh <-chan *corev1.Record, result *types.Result) (<-chan *corev1.Record, <-chan error) {
	out := make(chan *corev1.Record)
	errCh := make(chan error)

	go func() {
		defer close(out)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return
			case rec, ok := <-inputCh:
				if !ok {
					return
				}

				if err := ee.enrichRecord(ctx, rec.GetData()); err != nil {
					result.Mu.Lock()
					result.FailedCount++
					result.Mu.Unlock()

					errCh <- fmt.Errorf("enrich record: %w", err)

					return
				}

				select {
				case out <- rec:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, errCh
}

func (ee *ExtractorEnricher) enrichRecord(ctx context.Context, data *structpb.Struct) error {
	if data == nil || data.Fields == nil {
		return errors.New("record has nil data")
	}

	text := classificationText(data)
	if text == "" {
		return errors.New("record has no text to classify")
	}

	res, err := ee.ext.Extract(ctx, text)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	skills := make([]*typesv1.Skill, 0, len(res.Skills))
	for _, s := range res.Skills {
		skills = append(skills, &typesv1.Skill{Name: s.Name, Id: s.ID})
	}

	domains := make([]*typesv1.Domain, 0, len(res.Domains))
	for _, d := range res.Domains {
		domains = append(domains, &typesv1.Domain{Name: d.Name, Id: d.ID})
	}

	if err := setStructSkills(data, skills); err != nil {
		return fmt.Errorf("write skills: %w", err)
	}

	if err := setStructDomains(data, domains); err != nil {
		return fmt.Errorf("write domains: %w", err)
	}

	return nil
}

// classificationText builds the text fed to the extractor. Agent Skill records
// yield the full SKILL.md via the oasf-sdk translator; MCP/A2A records (and skill
// bundles, where RecordToSkillMarkdown errors) fall back to name + description.
func classificationText(data *structpb.Struct) string {
	if md, err := translator.RecordToSkillMarkdown(data); err == nil {
		if md = strings.TrimSpace(md); md != "" {
			return md
		}
		// An empty/whitespace-only SKILL.md deliberately falls through to the
		// name+description fallback below rather than being used as-is.
	}

	name := data.GetFields()["name"].GetStringValue()
	description := data.GetFields()["description"].GetStringValue()

	return strings.TrimSpace(name + "\n" + description)
}
