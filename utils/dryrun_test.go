// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	fieldName    = "name"
	fieldVersion = "version"

	testVersion = "1.2.3"
	testRecord  = "record"
)

func mkRecord(fields map[string]string) *corev1.Record {
	pbFields := make(map[string]*structpb.Value, len(fields))
	for k, v := range fields {
		pbFields[k] = structpb.NewStringValue(v)
	}

	return &corev1.Record{Data: &structpb.Struct{Fields: pbFields}}
}

func TestWriteRecords_OneFilePerRecord(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	ch := make(chan *corev1.Record, 2)

	ch <- mkRecord(map[string]string{fieldName: "server1", fieldVersion: "1.0.0"})

	ch <- mkRecord(map[string]string{fieldName: "server2", fieldVersion: "2.0.0"})

	close(ch)

	if err := WriteRecords(dir, ch); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d files, want 2", len(entries))
	}

	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", e.Name(), err)
		}

		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal(%q): %v\n%s", e.Name(), err, string(data))
		}

		if _, ok := decoded[fieldName]; !ok {
			t.Errorf("file %q missing %q field: %v", e.Name(), fieldName, decoded)
		}
	}
}

func TestWriteRecords_DeterministicNaming(t *testing.T) {
	t.Parallel()

	collect := func(dir string, records ...*corev1.Record) []string {
		t.Helper()

		ch := make(chan *corev1.Record, len(records))
		for _, r := range records {
			ch <- r
		}

		close(ch)

		if err := WriteRecords(dir, ch); err != nil {
			t.Fatalf("WriteRecords: %v", err)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}

		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		return names
	}

	rec := mkRecord(map[string]string{fieldName: "acme/server", fieldVersion: testVersion})

	first := collect(filepath.Join(t.TempDir(), "a"), rec)
	second := collect(filepath.Join(t.TempDir(), "b"), rec)

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected one file per run, got %d / %d", len(first), len(second))
	}

	if first[0] != second[0] {
		t.Errorf("filename for identical record changed across runs: %q vs %q", first[0], second[0])
	}

	want := "acme_server-" + testVersion + ".json"
	if first[0] != want {
		t.Errorf("filename = %q, want %q", first[0], want)
	}

	if strings.ContainsAny(first[0], "/\\") {
		t.Errorf("filename %q contains path separators", first[0])
	}
}

func TestWriteRecords_CollisionDisambiguation(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	mk := func(extra string) *corev1.Record {
		return mkRecord(map[string]string{
			fieldName:    "dup",
			fieldVersion: "1.0.0",
			"extra":      extra,
		})
	}

	ch := make(chan *corev1.Record, 2)

	ch <- mk("alpha")

	ch <- mk("beta")

	close(ch)

	if err := WriteRecords(dir, ch); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		got[e.Name()] = true
	}

	for _, want := range []string{"dup-1.0.0.json", "dup-1.0.0-1.json"} {
		if !got[want] {
			t.Errorf("missing expected file %q; got %v", want, got)
		}
	}
}

func TestWriteRecords_SkipsNilRecords(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	ch := make(chan *corev1.Record, 3)

	ch <- nil

	ch <- mkRecord(map[string]string{fieldName: "kept"})

	ch <- nil

	close(ch)

	if err := WriteRecords(dir, ch); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("got %d files, want 1 (nils should be skipped)", len(entries))
	}
}

func TestWriteRecords_FallbackFilenameWhenNameMissing(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "out")

	ch := make(chan *corev1.Record, 1)

	ch <- mkRecord(map[string]string{"unrelated": "value"})

	close(ch)

	if err := WriteRecords(dir, ch); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1", len(entries))
	}

	const want = "record.json"
	if entries[0].Name() != want {
		t.Errorf("fallback filename = %q, want %q", entries[0].Name(), want)
	}
}

func TestWriteRecords_MkdirFails(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	notADir := filepath.Join(parent, "blocker")

	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	bad := filepath.Join(notADir, "out")

	ch := make(chan *corev1.Record)
	close(ch)

	err := WriteRecords(bad, ch)
	if err == nil {
		t.Fatal("expected error creating directory under a regular file")
	}

	if !strings.Contains(err.Error(), "failed to create output directory") {
		t.Errorf("error %q does not match expected prefix", err.Error())
	}
}

func TestSanitizeSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"alphanumeric kept", "abc123", "abc123"},
		{"slash replaced", "acme/server", "acme_server"},
		{"backslash replaced", "acme\\server", "acme_server"},
		{"colon replaced", "scheme:1.0.0", "scheme_1.0.0"},
		{"dots preserved", testVersion, testVersion},
		{"leading separators trimmed", "...abc", "abc"},
		{"trailing separators trimmed", "abc...", "abc"},
		{"empty becomes record", "", testRecord},
		{"only separators becomes record", "...", testRecord},
		{"length bounded", strings.Repeat("a", 200), strings.Repeat("a", 64)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := sanitizeSegment(tc.in); got != tc.want {
				t.Errorf("sanitizeSegment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
