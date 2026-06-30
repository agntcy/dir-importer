// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/types"
)

func TestAgentSkillDirFetcher_Fetch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	skillDir := filepath.Join(dir, "code-review")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: code-review\ndescription: Review code for bugs and style. Use when the user asks for a code review.\n---\n\nBody here.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := NewAgentSkillDirFetcher(skillDir)
	if err != nil {
		t.Fatal(err)
	}

	outCh, errCh := f.Fetch(context.Background())

	var n int

	for item := range outCh {
		if item.Kind != types.SourceKindAgentSkill {
			t.Fatalf("Kind = %v, want AgentSkill", item.Kind)
		}

		if item.Skill == nil || item.Skill.GetFields()["name"].GetStringValue() != "code-review" {
			t.Fatal("unexpected skill payload")
		}

		if item.Skill.GetFields()["skillMarkdown"].GetStringValue() == "" {
			t.Fatal("expected wrapped skillMarkdown payload")
		}

		if _, ok := item.Skill.GetFields()["skillArchive"]; ok {
			t.Fatal("expected no skillArchive for markdown-only skill directory")
		}

		n++
	}

	for e := range errCh {
		if e != nil {
			t.Fatalf("unexpected err: %v", e)
		}
	}

	if n != 1 {
		t.Fatalf("got %d items, want 1", n)
	}
}

func TestAgentSkillDirFetcher_Fetch_Recursive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeNestedSkill := func(parts ...string) {
		t.Helper()

		skillDir := filepath.Join(append([]string{dir}, parts...)...)
		if err := os.MkdirAll(skillDir, 0o700); err != nil {
			t.Fatal(err)
		}

		name := parts[len(parts)-1]

		md := "---\nname: " + name + "\ndescription: Skill " + name + " for recursive import tests.\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeNestedSkill("code-review")
	writeNestedSkill("nested", "summarize")

	f, err := NewAgentSkillDirFetcher(dir)
	if err != nil {
		t.Fatal(err)
	}

	outCh, errCh := f.Fetch(context.Background())

	names := make(map[string]bool)

	for item := range outCh {
		if item.Kind != types.SourceKindAgentSkill {
			t.Fatalf("Kind = %v, want AgentSkill", item.Kind)
		}

		name := item.Skill.GetFields()["name"].GetStringValue()
		names[name] = true
	}

	for e := range errCh {
		if e != nil {
			t.Fatalf("unexpected err: %v", e)
		}
	}

	if len(names) != 2 || !names["code-review"] || !names["summarize"] {
		t.Fatalf("got names %v, want code-review and summarize", names)
	}
}

func TestAgentSkillDirFetcher_Fetch_PartialParseFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	goodDir := filepath.Join(dir, "good-skill")
	if err := os.MkdirAll(goodDir, 0o700); err != nil {
		t.Fatal(err)
	}

	goodMD := "---\nname: good-skill\ndescription: Valid skill for partial failure test.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(goodDir, "SKILL.md"), []byte(goodMD), 0o600); err != nil {
		t.Fatal(err)
	}

	badDir := filepath.Join(dir, "bad-skill")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(badDir, "SKILL.md"), []byte("not frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := NewAgentSkillDirFetcher(dir)
	if err != nil {
		t.Fatal(err)
	}

	outCh, errCh := f.Fetch(context.Background())

	var n int

	for item := range outCh {
		if item.Skill.GetFields()["name"].GetStringValue() != "good-skill" {
			t.Fatalf("unexpected skill: %v", item.Skill.GetFields()["name"].GetStringValue())
		}

		n++
	}

	var parseErr bool

	for e := range errCh {
		if e != nil {
			if strings.Contains(e.Error(), "bad-skill") {
				parseErr = true
			} else {
				t.Fatalf("unexpected err: %v", e)
			}
		}
	}

	if n != 1 {
		t.Fatalf("got %d items, want 1", n)
	}

	if !parseErr {
		t.Fatal("expected parse error for bad-skill")
	}
}

func TestAgentSkillDirFetcher_Fetch_ContextCanceledDuringDiscovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillDir := filepath.Join(dir, "code-review")

	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: code-review\ndescription: Skill for cancellation test.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := NewAgentSkillDirFetcher(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outCh, errCh := f.Fetch(ctx)

	for range outCh {
		t.Fatal("expected no items when discovery is canceled")
	}

	var canceled bool

	for e := range errCh {
		if e != nil && errors.Is(e, context.Canceled) {
			canceled = true
		}
	}

	if !canceled {
		t.Fatal("expected context.Canceled on errCh")
	}
}

func TestAgentSkillDirFetcher_Fetch_BundledWhenSupportingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	skillDir := filepath.Join(dir, "with-refs")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: with-refs\ndescription: Skill with supporting files.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("# Guide\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := NewAgentSkillDirFetcher(skillDir)
	if err != nil {
		t.Fatal(err)
	}

	outCh, errCh := f.Fetch(context.Background())

	var n int

	for item := range outCh {
		if item.Skill.GetFields()["skillArchive"].GetStringValue() == "" {
			t.Fatal("expected skillArchive for skill with supporting files")
		}

		n++
	}

	for e := range errCh {
		if e != nil {
			t.Fatalf("unexpected err: %v", e)
		}
	}

	if n != 1 {
		t.Fatalf("got %d items, want 1", n)
	}
}
