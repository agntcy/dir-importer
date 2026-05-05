// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"strings"
	"testing"
)

// errEmptyFile is the production error string for empty MCP and A2A inputs.
// Hoisted because it appears in several rows across the fetcher unit tests
// and mirrors what the production code emits (so a wording drift breaks the test).
const errEmptyFile = "file is empty"

// whitespacePath is a non-empty whitespace string used to verify that fetchers
// reject paths that are *only* whitespace, not just literal-empty ones.
const whitespacePath = "   "

// decodeServerResponses is the parsing kernel for MCP file imports. We test it
// directly (rather than driving Fetch end-to-end) because every interesting
// branch lives here: empty input, JSON array, bare ServerJSON, and the
// missing-server-name pruning rules. Driving Fetch instead would force us to
// touch the filesystem and the channel plumbing for what is fundamentally a
// pure-function test.
func TestDecodeServerResponses(t *testing.T) {
	t.Parallel()

	const validBareServer = `{"name":"io.example/server","version":"1.0.0"}`

	const validArrayElement = `{"server":{"name":"io.example/server","version":"1.0.0"}}`

	tests := []struct {
		name        string
		raw         string
		wantCount   int    // expected number of decoded servers (0 means an error is expected)
		wantErrText string // empty = expect success
	}{
		{
			name:        "empty input",
			raw:         "",
			wantErrText: errEmptyFile,
		},
		{
			name:        "whitespace-only input",
			raw:         "   \n\t  ",
			wantErrText: errEmptyFile,
		},
		{
			name:      "single bare ServerJSON",
			raw:       validBareServer,
			wantCount: 1,
		},
		{
			name:      "JSON array with one element",
			raw:       "[" + validArrayElement + "]",
			wantCount: 1,
		},
		{
			name:      "JSON array with multiple elements",
			raw:       "[" + validArrayElement + "," + validArrayElement + "]",
			wantCount: 2,
		},
		{
			name:        "JSON array of zero elements is an error",
			raw:         "[]",
			wantErrText: "no valid servers in JSON array",
		},
		{
			name:        "JSON array of all-empty-name elements is an error",
			raw:         `[{"server":{"name":"","version":"1.0.0"}}]`,
			wantErrText: "no valid servers",
		},
		{
			name:      "JSON array with one valid and one nameless element keeps only the valid one",
			raw:       `[{"server":{"name":"","version":"1.0.0"}},` + validArrayElement + `]`,
			wantCount: 1,
		},
		{
			name:        "malformed JSON array",
			raw:         `[{"server":{"name":`,
			wantErrText: "decode JSON array",
		},
		{
			name:        "single object that is neither an array nor a valid bare ServerJSON",
			raw:         `{"foo":"bar"}`,
			wantErrText: "could not parse file",
		},
		{
			name:        "garbage input",
			raw:         "not json at all",
			wantErrText: "could not parse file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeServerResponses([]byte(tt.raw))

			switch {
			case tt.wantErrText != "":
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (decoded %d servers)", tt.wantErrText, len(got))
				}

				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrText)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if len(got) != tt.wantCount {
					t.Fatalf("got %d servers, want %d", len(got), tt.wantCount)
				}
			}
		})
	}
}

// NewMCPFileFetcher rejects empty/whitespace-only paths up front so callers
// see a clear error before the goroutine inside Fetch tries to ReadFile("").
func TestNewMCPFileFetcher_RejectsEmptyPath(t *testing.T) {
	t.Parallel()

	tests := []string{"", whitespacePath, "\t\n"}
	for _, path := range tests {
		t.Run("path="+path, func(t *testing.T) {
			t.Parallel()

			_, err := NewMCPFileFetcher(path)
			if err == nil {
				t.Fatalf("expected error for empty path %q, got nil", path)
			}
		})
	}
}
