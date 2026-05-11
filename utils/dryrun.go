// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "github.com/agntcy/dir/api/core/v1"
)

const (
	// Restrictive perms so dry-run artifacts aren't world-readable.
	outputDirPerm  = 0o750
	outputFilePerm = 0o640

	// Bound on each filename segment to keep total path under common FS limits.
	maxSegmentLen = 64

	// Fallback base name when no usable identifier can be derived from a record.
	fallbackBaseName = "record"
)

// WriteRecords writes one JSON file per record into outputDir.
func WriteRecords(outputDir string, recordsCh <-chan *corev1.Record) error {
	if err := os.MkdirAll(outputDir, outputDirPerm); err != nil {
		// Drain to avoid blocking upstream stages.
		for range recordsCh {
		}

		return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}

	var (
		writeErrs []error
		// Disambiguates distinct records that resolve to the same filename.
		seen = make(map[string]int)
	)

	for record := range recordsCh {
		if record == nil {
			continue
		}

		if err := writeRecord(outputDir, record, seen); err != nil {
			writeErrs = append(writeErrs, err)
		}
	}

	if len(writeErrs) > 0 {
		return errors.Join(writeErrs...)
	}

	return nil
}

func writeRecord(outputDir string, record *corev1.Record, seen map[string]int) error {
	payload, err := json.Marshal(record.GetData())
	if err != nil {
		return fmt.Errorf("failed to encode record: %w", err)
	}

	fileName := recordFileName(record, seen)

	// fileName is sanitized (no separators), so Join cannot escape outputDir.
	outputPath := filepath.Join(outputDir, fileName)

	if err := os.WriteFile(outputPath, payload, outputFilePerm); err != nil {
		return fmt.Errorf("failed to write record %q: %w", fileName, err)
	}

	return nil
}

// recordFileName builds `<name>-<version>.json` (or `<name>.json` /
// `record.json` fallbacks), appending `-N` on collisions.
func recordFileName(record *corev1.Record, seen map[string]int) string {
	name, version := extractNameVersion(record)

	var base string

	switch {
	case name != "" && version != "":
		base = sanitizeSegment(name) + "-" + sanitizeSegment(version)
	case name != "":
		base = sanitizeSegment(name)
	default:
		base = fallbackBaseName
	}

	candidate := base + ".json"

	if n, exists := seen[candidate]; exists {
		seen[candidate] = n + 1
		candidate = fmt.Sprintf("%s-%d.json", base, n+1)
	} else {
		seen[candidate] = 0
	}

	return candidate
}

// extractNameVersion returns the "name" and "version" string fields, or
// empty strings if missing.
func extractNameVersion(record *corev1.Record) (string, string) {
	if record == nil || record.GetData() == nil {
		return "", ""
	}

	fields := record.GetData().GetFields()
	if fields == nil {
		return "", ""
	}

	var name, version string
	if v, ok := fields["name"]; ok {
		name = v.GetStringValue()
	}

	if v, ok := fields["version"]; ok {
		version = v.GetStringValue()
	}

	return name, version
}

// sanitizeSegment makes s filesystem-safe: alphanumerics, dash, dot and
// underscore are kept; everything else becomes `_`. Result is trimmed,
// length-bounded, and never empty.
func sanitizeSegment(s string) string {
	var b strings.Builder

	b.Grow(len(s))

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	out := strings.Trim(b.String(), "._-")

	if len(out) > maxSegmentLen {
		out = out[:maxSegmentLen]
	}

	if out == "" {
		out = fallbackBaseName
	}

	return out
}
