// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
	"github.com/agntcy/dir-importer/enricher/toolhost"
	"github.com/agntcy/dir-importer/internal/utils/logging"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/structpb"
)

var logger = logging.Logger("importer/enricher")

const (
	defaultConfidenceThreshold = 0.5
	enrichmentTimeout          = 5 * time.Minute
)

// LLMEnricher fills OASF skills and domains on agent records using an LLM agent
// talking to an MCP tool host, driven by prompt templates.
type LLMEnricher struct {
	toolHost       *toolhost.Host
	skillsPrompt   string
	domainsPrompt  string
	requestLimiter *rate.Limiter
}

// EnrichedField is one model-predicted taxonomy entry with optional reasoning.
type EnrichedField struct {
	Name       string  `json:"name"`
	ID         uint32  `json:"id"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// NewLLMEnricher builds an LLMEnricher from cfg. cfg.Validate is invoked
// explicitly here so callers cannot construct a half-configured enricher (e.g.
// via direct struct literal) — the alternative was a runtime panic deeper in the
// toolhost setup.
func NewLLMEnricher(ctx context.Context, cfg *enricherconfig.LLMConfig) (*LLMEnricher, error) {
	if cfg == nil {
		return nil, errors.New("llm enricher: config must not be nil")
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("enricher config: %w", err)
	}

	skillsPrompt, err := cfg.SkillsPrompt()
	if err != nil {
		return nil, fmt.Errorf("enricher config: %w", err)
	}

	domainsPrompt, err := cfg.DomainsPrompt()
	if err != nil {
		return nil, fmt.Errorf("enricher config: %w", err)
	}

	th, err := toolhost.New(ctx, &cfg.ToolHost)
	if err != nil {
		return nil, fmt.Errorf("enricher tool host: %w", err)
	}

	return &LLMEnricher{
		toolHost:       th,
		skillsPrompt:   skillsPrompt,
		domainsPrompt:  domainsPrompt,
		requestLimiter: rate.NewLimiter(rate.Limit(float64(cfg.RequestsPerMinute)/60.0), 1), //nolint:mnd
	}, nil
}

// Enrich reads records from inputCh, enriches each, and sends them on the returned channel.
//
//nolint:dupl // Enrich loop mirrors ExtractorEnricher.Enrich — same goroutine scaffold, different enrichRecord implementation.
func (e *LLMEnricher) Enrich(ctx context.Context, inputCh <-chan *corev1.Record, result *types.Result) (<-chan *corev1.Record, <-chan error) {
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

				if err := e.enrichRecord(ctx, rec.GetData()); err != nil {
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

func (e *LLMEnricher) enrichRecord(ctx context.Context, s *structpb.Struct) error {
	wrapped := &corev1.Record{Data: s}

	decoded, err := wrapped.Decode()
	if err != nil {
		return fmt.Errorf("decode OASF record: %w", err)
	}

	if !decoded.HasV1() {
		return errors.New("enricher supports OASF v1 records only")
	}

	rec := decoded.GetV1()
	rec.Skills, rec.Domains = nil, nil

	ctx, cancel := context.WithTimeout(ctx, enrichmentTimeout)
	defer cancel()

	rec, err = e.enrichSkills(ctx, rec)
	if err != nil {
		return fmt.Errorf("skills: %w", err)
	}

	rec, err = e.enrichDomains(ctx, rec)
	if err != nil {
		return fmt.Errorf("domains: %w", err)
	}

	if err := setStructSkills(s, rec.GetSkills()); err != nil {
		return fmt.Errorf("write skills: %w", err)
	}

	if err := setStructDomains(s, rec.GetDomains()); err != nil {
		return fmt.Errorf("write domains: %w", err)
	}

	return nil
}

//nolint:dupl // Parallel skills vs domains enrichment; a shared helper would blur separate flows.
func (e *LLMEnricher) enrichSkills(ctx context.Context, rec *typesv1.Record) (*typesv1.Record, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("marshal record: %w", err)
	}

	text, err := e.complete(ctx, e.skillsPrompt, payload)
	if err != nil {
		return nil, err
	}

	fields, err := parseSkillsJSON(text)
	if err != nil {
		return nil, fmt.Errorf("parse skills json: %w", err)
	}

	for _, f := range fields {
		if f.Confidence < defaultConfidenceThreshold {
			logger.Debug("skipped low-confidence skill", "name", f.Name, "confidence", f.Confidence)

			continue
		}

		rec.Skills = append(rec.Skills, &typesv1.Skill{Name: f.Name, Id: f.ID})
		logger.Debug("added skill", "name", f.Name, "id", f.ID)
	}

	return rec, nil
}

//nolint:dupl // Parallel skills vs domains enrichment; a shared helper would blur separate flows.
func (e *LLMEnricher) enrichDomains(ctx context.Context, rec *typesv1.Record) (*typesv1.Record, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("marshal record: %w", err)
	}

	text, err := e.complete(ctx, e.domainsPrompt, payload)
	if err != nil {
		return nil, err
	}

	fields, err := parseDomainsJSON(text)
	if err != nil {
		return nil, fmt.Errorf("parse domains json: %w", err)
	}

	for _, f := range fields {
		if f.Confidence < defaultConfidenceThreshold {
			logger.Debug("skipped low-confidence domain", "name", f.Name, "confidence", f.Confidence)

			continue
		}

		rec.Domains = append(rec.Domains, &typesv1.Domain{Name: f.Name, Id: f.ID})
		logger.Debug("added domain", "name", f.Name, "id", f.ID)
	}

	return rec, nil
}

// complete sends promptTemplate + JSON record to the LLM (rate-limited).
func (e *LLMEnricher) complete(ctx context.Context, promptTemplate string, recordJSON []byte) (string, error) {
	prompt := promptTemplate + string(recordJSON)

	if err := e.requestLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit: %w", err)
	}

	text, err := e.toolHost.Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}

	e.toolHost.ClearSession()

	return text, nil
}

func parseSkillsJSON(response string) ([]EnrichedField, error) {
	var parsed struct {
		Skills []EnrichedField `json:"skills"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &parsed); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return parsed.Skills, nil
}

func parseDomainsJSON(response string) ([]EnrichedField, error) {
	var parsed struct {
		Domains []EnrichedField `json:"domains"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &parsed); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return parsed.Domains, nil
}
