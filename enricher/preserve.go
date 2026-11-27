// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"sync"

	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// taxonomyPreserver forwards records that already have skills or domains and
// sends the rest through next. The LLM and static enrichers replace taxonomy,
// which would drop skills and domains restored from AI Catalog tags.
type taxonomyPreserver struct {
	next types.Enricher
}

// PreserveTaxonomy wraps next so records that already carry skills or domains
// skip enrichment.
func PreserveTaxonomy(next types.Enricher) types.Enricher {
	return &taxonomyPreserver{next: next}
}

func (p *taxonomyPreserver) Enrich(ctx context.Context, inputCh <-chan *corev1.Record, result *types.Result) (<-chan *corev1.Record, <-chan error) {
	needs := make(chan *corev1.Record)
	enriched, enrichErr := p.next.Enrich(ctx, needs, result)

	out := make(chan *corev1.Record)
	errCh := make(chan error)
	stop := make(chan struct{})

	var splitDone sync.WaitGroup
	splitDone.Add(1)

	go func() {
		defer splitDone.Done()

		splitTaxonomy(ctx, inputCh, needs, out, stop)
	}()

	go func() {
		defer close(out)
		defer close(errCh)

		forwardEnriched(ctx, enriched, enrichErr, out, errCh, stop)
		// splitTaxonomy can still be sending a record that already has taxonomy.
		// Wait for it to observe stop before closing out.
		splitDone.Wait()
	}()

	return out, errCh
}

func splitTaxonomy(ctx context.Context, inputCh <-chan *corev1.Record, needs chan<- *corev1.Record, out chan<- *corev1.Record, stop <-chan struct{}) {
	defer close(needs)

	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case record, ok := <-inputCh:
			if !ok {
				return
			}

			dest := needs
			if hasTaxonomy(record) {
				dest = out
			}

			select {
			case dest <- record:
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
		}
	}
}

func forwardEnriched(ctx context.Context, enriched <-chan *corev1.Record, enrichErr <-chan error, out chan<- *corev1.Record, errCh chan<- error, stop chan struct{}) {
	defer close(stop)

	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-enrichErr:
			if !ok {
				enrichErr = nil

				continue
			}

			if !sendErr(ctx, errCh, err) {
				return
			}
		case record, ok := <-enriched:
			if !ok {
				drainErrs(ctx, enrichErr, errCh)

				return
			}

			select {
			case out <- record:
			case <-ctx.Done():
				return
			}
		}
	}
}

func sendErr(ctx context.Context, errCh chan<- error, err error) bool {
	if err == nil {
		return true
	}

	select {
	case errCh <- err:
		return true
	case <-ctx.Done():
		return false
	}
}

func drainErrs(ctx context.Context, enrichErr <-chan error, errCh chan<- error) {
	if enrichErr == nil {
		return
	}

	for err := range enrichErr {
		if !sendErr(ctx, errCh, err) {
			return
		}
	}
}

func hasTaxonomy(record *corev1.Record) bool {
	data := record.GetData()
	if data == nil {
		return false
	}

	fields := data.GetFields()

	return listNonEmpty(fields["skills"]) || listNonEmpty(fields["domains"])
}

func listNonEmpty(value *structpb.Value) bool {
	if value == nil {
		return false
	}

	list := value.GetListValue()

	return list != nil && len(list.GetValues()) > 0
}
