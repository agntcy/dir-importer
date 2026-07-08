// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/types"
)

const (
	errEmptyFile       = "file is empty"
	whitespacePath     = "   "
	errDecodeJSONArray = "decode JSON array"
)

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
			wantErrText: errDecodeJSONArray,
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

// drainMCPFetch reads outCh and errCh concurrently to avoid deadlocking the
// producer goroutine when it sends to errCh before closing outCh.
func drainMCPFetch(outCh <-chan types.SourceItem, errCh <-chan error) ([]types.SourceItem, []error) {
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

func writeFile(t *testing.T, name, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	return path
}

func TestMCPFileFetcher_Fetch_HappyPath(t *testing.T) {
	t.Parallel()

	body := `{"name":"io.example/server","version":"1.0.0"}`
	path := writeFile(t, "server.json", body)

	f, err := NewMCPFileFetcher(path)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher err = %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
}

func TestMCPFileFetcher_Fetch_StripsBOM(t *testing.T) {
	t.Parallel()

	body := "\xef\xbb\xbf" + `{"name":"io.example/server","version":"1.0.0"}`
	path := writeFile(t, "bom.json", body)

	f, err := NewMCPFileFetcher(path)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher err = %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
}

func TestMCPFileFetcher_Fetch_FileMissing(t *testing.T) {
	t.Parallel()

	f, err := NewMCPFileFetcher(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("NewMCPFileFetcher err = %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "read file") {
		t.Fatalf("expected read-file error, got %v", errs)
	}
}

func TestMCPFileFetcher_Fetch_DecodeError(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "bad.json", `not json at all`)

	f, err := NewMCPFileFetcher(path)
	if err != nil {
		t.Fatalf("NewMCPFileFetcher err = %v", err)
	}

	items, errs := drainMCPFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "could not parse file") {
		t.Fatalf("expected decode error, got %v", errs)
	}
}
