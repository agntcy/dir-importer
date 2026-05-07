// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/types"
)

func writeA2AFixture(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}

	return nil
}

const (
	cardVersionV1      = "1.0.0"
	cardNameKey        = "name"
	cardVersionKey     = "version"
	errMissingCardName = "missing non-empty"
)

func drainErrs(errCh chan error) []error {
	close(errCh)

	var out []error
	for err := range errCh {
		out = append(out, err)
	}

	return out
}

func TestDecodeA2ARoot(t *testing.T) {
	t.Parallel()

	const validCard = `{"name":"hello-agent","version":"1.0.0"}`

	tests := []struct {
		name           string
		raw            string
		wantCount      int
		wantPerElemErr int    // expected count of errors drained from errCh (best-effort, per-element)
		wantFatalText  string // empty = expect no fatal error
	}{
		{
			name:          "empty input is fatal",
			raw:           "",
			wantFatalText: errEmptyFile,
		},
		{
			name:          "whitespace-only input is fatal",
			raw:           "   \n",
			wantFatalText: errEmptyFile,
		},
		{
			name:      "single object decodes to one card",
			raw:       validCard,
			wantCount: 1,
		},
		{
			name:      "JSON array of one decodes to one card",
			raw:       "[" + validCard + "]",
			wantCount: 1,
		},
		{
			name:      "JSON array of three decodes to three cards",
			raw:       "[" + validCard + "," + validCard + "," + validCard + "]",
			wantCount: 3,
		},
		{
			name:      "empty JSON array decodes to zero cards (caller layers handle the empty case)",
			raw:       "[]",
			wantCount: 0,
		},
		{
			name:          "malformed array is fatal",
			raw:           `[{"name":`,
			wantFatalText: "decode JSON array",
		},
		{
			name:          "malformed single object is fatal",
			raw:           `{"name":`,
			wantFatalText: "decode JSON object",
		},
		{
			name:           "array with mixed valid and malformed elements: valids decoded, malformed reported on errCh",
			raw:            `[` + validCard + `, "not an object", ` + validCard + `]`,
			wantCount:      2,
			wantPerElemErr: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Buffered enough that decodeA2ARoot never blocks on errCh during the
			// per-element loop; draining happens after the call.
			errCh := make(chan error, 16)

			got, fatalErr := decodeA2ARoot(context.Background(), []byte(tt.raw), errCh)

			perElemErrs := drainErrs(errCh)

			switch {
			case tt.wantFatalText != "":
				if fatalErr == nil {
					t.Fatalf("expected fatal error containing %q, got nil", tt.wantFatalText)
				}

				if !strings.Contains(fatalErr.Error(), tt.wantFatalText) {
					t.Fatalf("fatal error %q does not contain %q", fatalErr.Error(), tt.wantFatalText)
				}
			default:
				if fatalErr != nil {
					t.Fatalf("unexpected fatal error: %v", fatalErr)
				}

				if len(got) != tt.wantCount {
					t.Fatalf("decoded %d cards, want %d", len(got), tt.wantCount)
				}

				if len(perElemErrs) != tt.wantPerElemErr {
					t.Fatalf("got %d per-element errors, want %d (errors: %v)", len(perElemErrs), tt.wantPerElemErr, perElemErrs)
				}
			}
		})
	}
}

func TestAgentCardStructFromMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		card        map[string]any
		wantErrText string
	}{
		{
			name:        "nil card is rejected",
			card:        nil,
			wantErrText: "card is nil",
		},
		{
			name:        "missing name is rejected",
			card:        map[string]any{cardVersionKey: cardVersionV1},
			wantErrText: errMissingCardName,
		},
		{
			name:        "empty-string name is rejected",
			card:        map[string]any{cardNameKey: "", cardVersionKey: cardVersionV1},
			wantErrText: errMissingCardName,
		},
		{
			name:        "whitespace-only name is rejected",
			card:        map[string]any{cardNameKey: whitespacePath, cardVersionKey: cardVersionV1},
			wantErrText: errMissingCardName,
		},
		{
			name: "valid card with extra unknown fields is accepted (structpb passes them through)",
			card: map[string]any{
				cardNameKey:    "hello-agent",
				cardVersionKey: cardVersionV1,
				"extra":        "ignored-by-validation",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, err := agentCardStructFromMap(tt.card)

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

				if st == nil {
					t.Fatalf("expected non-nil struct on success")
				}
			}
		})
	}
}

func TestNewA2AFileFetcher_RejectsEmptyPath(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", whitespacePath, "\t\n"} {
		t.Run("path="+path, func(t *testing.T) {
			t.Parallel()

			_, err := NewA2AFileFetcher(path)
			if err == nil {
				t.Fatalf("expected error for empty path %q, got nil", path)
			}
		})
	}
}

func drainA2AFetch(outCh <-chan types.SourceItem, errCh <-chan error) ([]types.SourceItem, []error) {
	var (
		items []types.SourceItem
		errs  []error
		done  = make(chan struct{}, 2)
	)

	go func() {
		for it := range outCh {
			items = append(items, it)
		}

		done <- struct{}{}
	}()

	go func() {
		for e := range errCh {
			if e != nil {
				errs = append(errs, e)
			}
		}

		done <- struct{}{}
	}()

	<-done
	<-done

	return items, errs
}

func TestA2AFileFetcher_Fetch_SingleObjectHappyPath(t *testing.T) {
	t.Parallel()

	body := `{"name":"hello-agent","version":"1.0.0"}`
	path := filepath.Join(t.TempDir(), "card.json")

	if err := writeA2AFixture(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewA2AFileFetcher(path)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher err = %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
}

func TestA2AFileFetcher_Fetch_ArrayWithInvalidElement(t *testing.T) {
	t.Parallel()

	body := `[{"name":"good","version":"1.0.0"},{"version":"2.0.0"}]`
	path := filepath.Join(t.TempDir(), "cards.json")

	if err := writeA2AFixture(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewA2AFileFetcher(path)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher err = %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (invalid card without name should be skipped)", len(items))
	}

	if len(errs) == 0 {
		t.Fatal("expected at least one per-element error for invalid card")
	}
}

func TestA2AFileFetcher_Fetch_FileMissing(t *testing.T) {
	t.Parallel()

	f, err := NewA2AFileFetcher(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("NewA2AFileFetcher err = %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "read file") {
		t.Fatalf("expected read-file error, got %v", errs)
	}
}

func TestA2AFileFetcher_Fetch_DecodeFatalError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.json")
	if err := writeA2AFixture(path, `{"name":`); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewA2AFileFetcher(path)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher err = %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "decode JSON object") {
		t.Fatalf("expected decode-object fatal error, got %v", errs)
	}
}

func TestA2AFileFetcher_Fetch_EmptyArray(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.json")
	if err := writeA2AFixture(path, `[]`); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewA2AFileFetcher(path)
	if err != nil {
		t.Fatalf("NewA2AFileFetcher err = %v", err)
	}

	items, errs := drainA2AFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "no A2A agent cards found") {
		t.Fatalf("expected empty-array error, got %v", errs)
	}
}
