// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"strings"
	"testing"
)

// Hoisted to constants for the same reason as in mcp_file_test.go: the version
// and name keys appear in many test rows, and the missing-name error text
// must match the production error string in agentCardStructFromMap.
const (
	cardVersionV1      = "1.0.0"
	cardNameKey        = "name"
	cardVersionKey     = "version"
	errMissingCardName = "missing non-empty"
)

// drainErrs collects any errors decodeA2ARoot drained into errCh during the
// per-element loop. This mirrors what the real Fetch goroutine consumes;
// reading them after the call lets tests assert that malformed array elements
// are reported individually instead of failing the whole decode.
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

// agentCardStructFromMap is the per-card validation gate: missing or
// whitespace-only "name" is fatal. Everything else flows through structpb.
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
