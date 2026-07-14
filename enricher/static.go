// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"errors"
	"fmt"

	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// StaticEnricher assigns a fixed set of OASF skills and domains to every
// record it forwards.
type StaticEnricher struct {
	skills  *structpb.ListValue
	domains *structpb.ListValue
}

// NewStaticEnricher returns a StaticEnricher that assigns cfg's OASF skills and
// domains to every record. It errors on a nil or invalid config.
func NewStaticEnricher(cfg *enricherconfig.StaticConfig) (*StaticEnricher, error) {
	if cfg == nil {
		return nil, errors.New("static enricher: config must not be nil")
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("static enricher config: %w", err)
	}

	return &StaticEnricher{
		skills:  skillsToListValue(cfg.Skills),
		domains: domainsToListValue(cfg.Domains),
	}, nil
}

// Enrich reads records from inputCh, injects the configured skills and domains, and forwards them.
func (se *StaticEnricher) Enrich(ctx context.Context, inputCh <-chan *corev1.Record, result *types.Result) (<-chan *corev1.Record, <-chan error) {
	outputCh := make(chan *corev1.Record)
	errCh := make(chan error)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return
			case record, ok := <-inputCh:
				if !ok {
					return
				}

				data := record.GetData()
				if data == nil || data.Fields == nil {
					result.Mu.Lock()
					result.FailedCount++
					result.Mu.Unlock()

					errCh <- errors.New("static enricher: record has nil data")

					return
				}

				data.Fields["skills"] = structpb.NewListValue(se.skills)
				data.Fields["domains"] = structpb.NewListValue(se.domains)

				select {
				case outputCh <- record:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return outputCh, errCh
}
