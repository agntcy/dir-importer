// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSkillMD = "---\nname: example\ndescription: Example skill for tests.\n---\n\nBody.\n"

func writeSkillAt(t *testing.T, dir, name, contents string) {
	t.Helper()

	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func evalPath(t *testing.T, path string) string {
	t.Helper()

	eval, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}

	return eval
}

func TestDiscoverSkillDirectories_SingleSkill(t *testing.T) {
	t.Parallel()

	root := writeSkillDir(t, validSkillMD)

	got, err := DiscoverSkillDirectories(root)
	if err != nil {
		t.Fatalf("DiscoverSkillDirectories: %v", err)
	}

	if len(got) != 1 || got[0] != evalPath(t, root) {
		t.Fatalf("got %v, want [%s]", got, evalPath(t, root))
	}
}

func TestDiscoverSkillDirectories_NestedSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	writeSkillAt(t, root, "code-review", validSkillMD)
	writeSkillAt(t, root, filepath.Join("nested", "summarize"), validSkillMD)

	got, err := DiscoverSkillDirectories(root)
	if err != nil {
		t.Fatalf("DiscoverSkillDirectories: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d dirs, want 2: %v", len(got), got)
	}

	want := map[string]bool{
		evalPath(t, filepath.Join(root, "code-review")):         true,
		evalPath(t, filepath.Join(root, "nested", "summarize")): true,
	}

	for _, dir := range got {
		if !want[dir] {
			t.Fatalf("unexpected skill dir %q in %v", dir, got)
		}
	}
}

func TestDiscoverSkillDirectories_SkipsSkillInternals(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, "code-review")

	if err := os.MkdirAll(filepath.Join(skillDir, "references", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(validSkillMD), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "references", "nested", "SKILL.md"), []byte(validSkillMD), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverSkillDirectories(root)
	if err != nil {
		t.Fatalf("DiscoverSkillDirectories: %v", err)
	}

	if len(got) != 1 || got[0] != evalPath(t, skillDir) {
		t.Fatalf("got %v, want [%s]", got, evalPath(t, skillDir))
	}
}

func TestDiscoverSkillDirectories_NoSkillsFound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	_, err := DiscoverSkillDirectories(root)
	if err == nil || !strings.Contains(err.Error(), "no agent skills found") {
		t.Fatalf("expected no-skills error, got %v", err)
	}
}

func TestDiscoverSkillDirectories_RootWithSkillDoesNotDescend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(validSkillMD), 0o600); err != nil {
		t.Fatal(err)
	}

	writeSkillAt(t, root, "child-skill", validSkillMD)

	got, err := DiscoverSkillDirectories(root)
	if err != nil {
		t.Fatalf("DiscoverSkillDirectories: %v", err)
	}

	if len(got) != 1 || got[0] != evalPath(t, root) {
		t.Fatalf("got %v, want only root [%s]", got, evalPath(t, root))
	}
}

func TestDiscoverSkillDirectories_Errors(t *testing.T) {
	t.Parallel()

	t.Run("nonexistent directory", func(t *testing.T) {
		t.Parallel()

		_, err := DiscoverSkillDirectories("/nonexistent/skill/path")
		if err == nil || !strings.Contains(err.Error(), "stat skill search root") {
			t.Fatalf("expected stat error, got %v", err)
		}
	})

	t.Run("path is a file", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "not-a-dir.txt")
		if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := DiscoverSkillDirectories(path)
		if err == nil || !strings.Contains(err.Error(), "must be a directory") {
			t.Fatalf("expected must-be-a-directory error, got %v", err)
		}
	})
}
