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

const errMissingRecordName = "missing non-empty"

func writeOASFFixture(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}

	return nil
}

func TestDecodeOASFRoot(t *testing.T) {
	t.Parallel()

	const validRecord = `{"name":"hello-agent","schema_version":"1.0.0","version":"1.0.0"}`

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
			name:      "single object decodes to one record",
			raw:       validRecord,
			wantCount: 1,
		},
		{
			name:      "JSON array of one decodes to one record",
			raw:       "[" + validRecord + "]",
			wantCount: 1,
		},
		{
			name:      "JSON array of three decodes to three records",
			raw:       "[" + validRecord + "," + validRecord + "," + validRecord + "]",
			wantCount: 3,
		},
		{
			name:      "empty JSON array decodes to zero records (caller layers handle the empty case)",
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
			raw:            `[` + validRecord + `, "not an object", ` + validRecord + `]`,
			wantCount:      2,
			wantPerElemErr: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Buffered enough that decodeOASFRoot never blocks on errCh during the
			// per-element loop; draining happens after the call.
			errCh := make(chan error, 16)

			got, fatalErr := decodeOASFRoot(context.Background(), []byte(tt.raw), errCh)

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
					t.Fatalf("decoded %d records, want %d", len(got), tt.wantCount)
				}

				if len(perElemErrs) != tt.wantPerElemErr {
					t.Fatalf("got %d per-element errors, want %d (errors: %v)", len(perElemErrs), tt.wantPerElemErr, perElemErrs)
				}
			}
		})
	}
}

func TestOASFRecordStructFromMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		record      map[string]any
		wantErrText string
	}{
		{
			name:        "nil record is rejected",
			record:      nil,
			wantErrText: "record is nil",
		},
		{
			name:        "missing name is rejected",
			record:      map[string]any{"schema_version": "1.0.0"},
			wantErrText: errMissingRecordName,
		},
		{
			name:        "empty-string name is rejected",
			record:      map[string]any{"name": "", "schema_version": "1.0.0"},
			wantErrText: errMissingRecordName,
		},
		{
			name:        "whitespace-only name is rejected",
			record:      map[string]any{"name": whitespacePath, "schema_version": "1.0.0"},
			wantErrText: errMissingRecordName,
		},
		{
			name: "valid record with extra fields is accepted (structpb passes them through)",
			record: map[string]any{
				"name":           "hello-agent",
				"schema_version": "1.0.0",
				"version":        "1.0.0",
				"modules":        []any{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, err := oasfRecordStructFromMap(tt.record)

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

func TestNewOASFFileFetcher_RejectsEmptyPath(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", whitespacePath, "\t\n"} {
		t.Run("path="+path, func(t *testing.T) {
			t.Parallel()

			_, err := NewOASFFileFetcher(path)
			if err == nil {
				t.Fatalf("expected error for empty path %q, got nil", path)
			}
		})
	}
}

func drainOASFFetch(outCh <-chan types.SourceItem, errCh <-chan error) ([]types.SourceItem, []error) {
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

func TestOASFFileFetcher_Fetch_SingleObjectHappyPath(t *testing.T) {
	t.Parallel()

	body := `{"name":"hello-agent","schema_version":"1.0.0","version":"1.0.0"}`
	path := filepath.Join(t.TempDir(), "record.json")

	if err := writeOASFFixture(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewOASFFileFetcher(path)
	if err != nil {
		t.Fatalf("NewOASFFileFetcher err = %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	if items[0].Kind != types.SourceKindOASF {
		t.Errorf("Kind = %v, want SourceKindOASF", items[0].Kind)
	}
}

func TestOASFFileFetcher_Fetch_ArrayWithInvalidElement(t *testing.T) {
	t.Parallel()

	body := `[{"name":"good","schema_version":"1.0.0"},{"schema_version":"1.0.0"}]`
	path := filepath.Join(t.TempDir(), "records.json")

	if err := writeOASFFixture(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewOASFFileFetcher(path)
	if err != nil {
		t.Fatalf("NewOASFFileFetcher err = %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (invalid record without name should be skipped)", len(items))
	}

	if len(errs) == 0 {
		t.Fatal("expected at least one per-element error for invalid record")
	}
}

func TestOASFFileFetcher_Fetch_FileMissing(t *testing.T) {
	t.Parallel()

	f, err := NewOASFFileFetcher(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("NewOASFFileFetcher err = %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "read file") {
		t.Fatalf("expected read-file error, got %v", errs)
	}
}

func TestOASFFileFetcher_Fetch_DecodeFatalError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.json")
	if err := writeOASFFixture(path, `{"name":`); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewOASFFileFetcher(path)
	if err != nil {
		t.Fatalf("NewOASFFileFetcher err = %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "decode JSON object") {
		t.Fatalf("expected decode-object fatal error, got %v", errs)
	}
}

func TestOASFFileFetcher_Fetch_EmptyArray(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.json")
	if err := writeOASFFixture(path, `[]`); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewOASFFileFetcher(path)
	if err != nil {
		t.Fatalf("NewOASFFileFetcher err = %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "no OASF records found") {
		t.Fatalf("expected empty-array error, got %v", errs)
	}
}

func TestOASFFileFetcher_Fetch_BOMStripped(t *testing.T) {
	t.Parallel()

	body := "\xef\xbb\xbf" + `{"name":"hello-agent","schema_version":"1.0.0"}`
	path := filepath.Join(t.TempDir(), "bom.json")

	if err := writeOASFFixture(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	f, err := NewOASFFileFetcher(path)
	if err != nil {
		t.Fatalf("NewOASFFileFetcher err = %v", err)
	}

	items, errs := drainOASFFetch(f.Fetch(context.Background()))

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
}
