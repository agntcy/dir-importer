// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	errSkillEmpty            = "SKILL.md is empty"
	errSkillFrontmatterClose = "frontmatter must end with ---"
	bodyMarker               = "body"
	skillName                = "name: code-review"
)

func TestSplitSkillFrontmatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		wantFM      string // expected frontmatter content (between the --- delimiters)
		wantBody    string // expected body content (after the closing ---)
		wantErrText string // empty = expect success
	}{
		{
			name:        "empty input",
			raw:         "",
			wantErrText: errSkillEmpty,
		},
		{
			name:        "whitespace-only input",
			raw:         "   \n\t  ",
			wantErrText: errSkillEmpty,
		},
		{
			name:        "no frontmatter delimiters",
			raw:         "just a body, no frontmatter\n",
			wantErrText: "must start with YAML frontmatter",
		},
		{
			name:        "first line is not the delimiter",
			raw:         "# heading\n---\nname: x\n---\n",
			wantErrText: "must start with YAML frontmatter",
		},
		{
			name:        "missing closing delimiter",
			raw:         "---\nname: x\nbody starts here without closing fence",
			wantErrText: errSkillFrontmatterClose,
		},
		{
			// splitSkillFrontmatter calls strings.TrimSpace on the entire input
			// before splitting, so trailing newlines on the body are stripped.
			// That's fine for the downstream consumers; this test just pins the
			// behaviour so a future refactor doesn't accidentally start
			// preserving them (or vice versa).
			name:     "minimal valid frontmatter with body",
			raw:      "---\nname: code-review\n---\nbody text\n",
			wantFM:   skillName,
			wantBody: "body text",
		},
		{
			name:     "valid frontmatter with empty body",
			raw:      "---\nname: code-review\n---\n",
			wantFM:   skillName,
			wantBody: "",
		},
		{
			name:     "BOM is stripped before delimiter check",
			raw:      "\ufeff---\nname: code-review\n---\nbody\n",
			wantFM:   skillName,
			wantBody: bodyMarker,
		},
		{
			name:     "CRLF line endings are normalized",
			raw:      "---\r\nname: code-review\r\n---\r\nbody\r\n",
			wantFM:   skillName,
			wantBody: bodyMarker,
		},
		{
			name:     "leading whitespace before frontmatter is stripped",
			raw:      "\n\n  ---\nname: x\n---\nbody",
			wantFM:   "name: x",
			wantBody: bodyMarker,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fm, body, err := splitSkillFrontmatter(tt.raw)

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

				if fm != tt.wantFM {
					t.Errorf("frontmatter = %q, want %q", fm, tt.wantFM)
				}

				if body != tt.wantBody {
					t.Errorf("body = %q, want %q", body, tt.wantBody)
				}
			}
		})
	}
}

// writeSkillDir is a tiny fixture helper: creates a fresh dir under t.TempDir()
// and writes contents to dir/SKILL.md, returning the directory path.
func writeSkillDir(t *testing.T, contents string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	return dir
}

func TestParseSkillDirectory_DirectoryErrors(t *testing.T) {
	t.Parallel()

	t.Run("nonexistent directory", func(t *testing.T) {
		t.Parallel()

		_, err := ParseSkillDirectory("/nonexistent/skill/path")
		if err == nil || !strings.Contains(err.Error(), "stat skill directory") {
			t.Fatalf("expected stat-skill-directory error, got %v", err)
		}
	})

	t.Run("path is a file, not a directory", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "not-a-dir.txt")
		if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		_, err := ParseSkillDirectory(path)
		if err == nil || !strings.Contains(err.Error(), "must be a directory") {
			t.Fatalf("expected must-be-a-directory error, got %v", err)
		}
	})

	t.Run("directory missing SKILL.md", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		_, err := ParseSkillDirectory(dir)
		if err == nil || !strings.Contains(err.Error(), "missing SKILL.md") {
			t.Fatalf("expected missing-SKILL.md error, got %v", err)
		}
	})
}

func TestParseSkillDirectory_FrontmatterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contents    string
		wantErrText string
	}{
		{
			name:        "empty SKILL.md",
			contents:    "",
			wantErrText: errSkillEmpty,
		},
		{
			name:        "missing closing fence",
			contents:    "---\nname: x\nno closing fence",
			wantErrText: errSkillFrontmatterClose,
		},
		{
			name:        "malformed YAML inside frontmatter",
			contents:    "---\nname: [unclosed\n---\nbody",
			wantErrText: "parse SKILL.md frontmatter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeSkillDir(t, tt.contents)

			_, err := ParseSkillDirectory(dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrText, err)
			}
		})
	}
}

// Happy path: every frontmatter field maps to the expected struct field, and
// the populated payload is suitable for downstream OASF translation. We assert
// on the resulting structpb's AsMap to keep the test free of structpb internals.
func TestParseSkillDirectory_PopulatesPayload(t *testing.T) {
	t.Parallel()

	const contents = `---
name: code-review
description: Review pull requests
license: Apache-2.0
compatibility: cli>=1.0
allowed-tools: bash python
metadata:
  owner: platform
  tier: gold
---
# Body

Some markdown body content.
`

	dir := writeSkillDir(t, contents)

	got, err := ParseSkillDirectory(dir)
	if err != nil {
		t.Fatalf("ParseSkillDirectory: %v", err)
	}

	fields := got.AsMap()

	assertStringField(t, fields, "name", "code-review")
	assertStringField(t, fields, "description", "Review pull requests")
	assertStringField(t, fields, "license", "Apache-2.0")
	assertStringField(t, fields, "compatibility", "cli>=1.0")

	skillMarkdown, ok := fields["skillMarkdown"].(string)
	if !ok || !strings.Contains(skillMarkdown, skillName) {
		t.Errorf("skillMarkdown should preserve the original SKILL.md, got %v", fields["skillMarkdown"])
	}

	bodyText, ok := fields["body"].(string)
	if !ok || !strings.Contains(bodyText, "Some markdown body content.") {
		t.Errorf("body should contain markdown content after frontmatter, got %v", fields["body"])
	}

	tools, ok := fields["allowed_tools"].([]any)
	if !ok || len(tools) != 2 || tools[0] != "bash" || tools[1] != "python" {
		t.Errorf("allowed_tools = %v, want [bash python]", fields["allowed_tools"])
	}

	meta, ok := fields["metadata"].(map[string]any)
	if !ok || meta["owner"] != "platform" || meta["tier"] != "gold" {
		t.Errorf("metadata = %v, want owner=platform tier=gold", fields["metadata"])
	}

	if _, ok := fields["skill_root"].(string); !ok {
		t.Errorf("skill_root should be a non-empty string, got %v", fields["skill_root"])
	}
}

// Optional fields that are absent from frontmatter must not appear in the
// payload at all (rather than appear as empty strings) — downstream OASF
// validation rejects unknown empty fields.
func TestParseSkillDirectory_OmitsAbsentOptionalFields(t *testing.T) {
	t.Parallel()

	const contents = `---
name: minimal
description: just the basics
---
body
`

	dir := writeSkillDir(t, contents)

	got, err := ParseSkillDirectory(dir)
	if err != nil {
		t.Fatalf("ParseSkillDirectory: %v", err)
	}

	fields := got.AsMap()

	for _, omitted := range []string{"license", "compatibility", "allowed_tools", "metadata"} {
		if _, present := fields[omitted]; present {
			t.Errorf("optional field %q should be absent when not in frontmatter, got %v", omitted, fields[omitted])
		}
	}
}

func assertStringField(t *testing.T, fields map[string]any, key, want string) {
	t.Helper()

	got, ok := fields[key].(string)
	if !ok {
		t.Errorf("field %q is not a string: %T", key, fields[key])

		return
	}

	if got != want {
		t.Errorf("field %q = %q, want %q", key, got, want)
	}
}
