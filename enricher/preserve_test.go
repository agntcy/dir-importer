// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"testing"

	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type rewriteEnricher struct{}

func (rewriteEnricher) Enrich(ctx context.Context, inputCh <-chan *corev1.Record, _ *types.Result) (<-chan *corev1.Record, <-chan error) {
	out := make(chan *corev1.Record)
	errCh := make(chan error)

	go func() {
		defer close(out)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return
			case record, ok := <-inputCh:
				if !ok {
					return
				}

				record.GetData().GetFields()["name"] = structpb.NewStringValue("rewritten")

				select {
				case out <- record:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, errCh
}

func TestPreserveTaxonomy_KeepsExistingSkills(t *testing.T) {
	t.Parallel()

	withSkills := recordWithFields(t, map[string]any{
		"name":   "kept",
		"skills": []any{map[string]any{"name": "/example/skill"}},
	})
	without := recordWithFields(t, map[string]any{"name": "plain"})

	in := make(chan *corev1.Record, 2)
	in <- withSkills
	in <- without
	close(in)

	out, errCh := PreserveTaxonomy(rewriteEnricher{}).Enrich(context.Background(), in, &types.Result{})

	var names []string

	for out != nil || errCh != nil {
		select {
		case record, ok := <-out:
			if !ok {
				out = nil

				continue
			}

			names = append(names, record.GetData().GetFields()["name"].GetStringValue())
		case err, ok := <-errCh:
			if !ok {
				errCh = nil

				continue
			}

			require.NoError(t, err)
		}
	}

	assert.ElementsMatch(t, []string{"kept", "rewritten"}, names)
}

func recordWithFields(t *testing.T, fields map[string]any) *corev1.Record {
	t.Helper()

	data, err := structpb.NewStruct(fields)
	require.NoError(t, err)

	return &corev1.Record{Data: data}
}
