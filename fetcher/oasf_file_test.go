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

const (
	errMissingRecordName = "missing non-empty"
	schemaVersionKey     = "schema_version"
)

func writeOASFFixture(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}

	return nil
}

// TestDecodeOASFRoot is a smoke test confirming decodeOASFRoot delegates to
// decodeStructRoot; see TestDecodeStructRoot for full behavioral coverage.
func TestDecodeOASFRoot(t *testing.T) {
	t.Parallel()

	const validRecord = `{"name":"hello-agent","schema_version":"1.0.0","version":"1.0.0"}`

	errCh := make(chan error, 16)

	got, fatalErr := decodeOASFRoot(context.Background(), []byte(validRecord), errCh)
	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}

	if len(got) != 1 {
		t.Fatalf("decoded %d records, want 1", len(got))
	}

	if _, fatalErr := decodeOASFRoot(context.Background(), nil, errCh); fatalErr == nil {
		t.Fatal("expected fatal error for empty input")
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
			record:      map[string]any{schemaVersionKey: cardVersionV1},
			wantErrText: errMissingRecordName,
		},
		{
			name:        "empty-string name is rejected",
			record:      map[string]any{cardNameKey: "", schemaVersionKey: cardVersionV1},
			wantErrText: errMissingRecordName,
		},
		{
			name:        "whitespace-only name is rejected",
			record:      map[string]any{cardNameKey: whitespacePath, schemaVersionKey: cardVersionV1},
			wantErrText: errMissingRecordName,
		},
		{
			name: "valid record with extra fields is accepted (structpb passes them through)",
			record: map[string]any{
				cardNameKey:      "hello-agent",
				schemaVersionKey: cardVersionV1,
				cardVersionKey:   cardVersionV1,
				"modules":        []any{},
			},
		},
		{
			// A named record whose value graph contains a type structpb cannot
			// represent (here a channel) must surface a conversion error rather
			// than panicking or emitting a partial struct. JSON decoding never
			// produces such a value, so this exercises structFromMap directly.
			name:        "value structpb cannot represent is rejected",
			record:      map[string]any{cardNameKey: "named-record", "bad": make(chan int)},
			wantErrText: "as struct",
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
