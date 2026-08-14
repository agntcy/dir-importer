// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"strings"
	"testing"
)

// TestDecodeStructRoot exercises decodeStructRoot directly, the format-agnostic
// decode logic shared by decodeA2ARoot and decodeOASFRoot. The two thin
// per-kind wrappers only need a small smoke test each (see
// TestDecodeA2ARoot / TestDecodeOASFRoot) confirming they delegate here.
func TestDecodeStructRoot(t *testing.T) {
	t.Parallel()

	const validObj = `{"name":"hello-agent","version":"1.0.0"}`

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
			name:      "single object decodes to one item",
			raw:       validObj,
			wantCount: 1,
		},
		{
			name:      "JSON array of one decodes to one item",
			raw:       "[" + validObj + "]",
			wantCount: 1,
		},
		{
			name:      "JSON array of three decodes to three items",
			raw:       "[" + validObj + "," + validObj + "," + validObj + "]",
			wantCount: 3,
		},
		{
			name:      "empty JSON array decodes to zero items (caller layers handle the empty case)",
			raw:       "[]",
			wantCount: 0,
		},
		{
			name:          "malformed array is fatal",
			raw:           `[{"name":`,
			wantFatalText: errDecodeJSONArray,
		},
		{
			name:          "malformed single object is fatal",
			raw:           `{"name":`,
			wantFatalText: "decode JSON object",
		},
		{
			name:           "array with mixed valid and malformed elements: valids decoded, malformed reported on errCh",
			raw:            `[` + validObj + `, "not an object", ` + validObj + `]`,
			wantCount:      2,
			wantPerElemErr: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Buffered enough that decodeStructRoot never blocks on errCh during the
			// per-element loop; draining happens after the call.
			errCh := make(chan error, 16)

			got, fatalErr := decodeStructRoot(context.Background(), []byte(tt.raw), errCh, "")

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
					t.Fatalf("decoded %d items, want %d", len(got), tt.wantCount)
				}

				if len(perElemErrs) != tt.wantPerElemErr {
					t.Fatalf("got %d per-element errors, want %d (errors: %v)", len(perElemErrs), tt.wantPerElemErr, perElemErrs)
				}
			}
		})
	}
}
