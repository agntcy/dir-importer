// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateSkillArchiveFromDirectory_IncludesSupportingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillDir := filepath.Join(dir, "demo-skill")

	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: demo-skill\ndescription: Demo skill for archive tests.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(refDir, "extra.md"), []byte("# Extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archive, err := CreateSkillArchiveFromDirectory(skillDir)
	if err != nil {
		t.Fatalf("CreateSkillArchiveFromDirectory: %v", err)
	}

	paths := listArchivePaths(t, archive)
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want 2 entries", paths)
	}

	if !containsPath(paths, "SKILL.md") || !containsPath(paths, "references/extra.md") {
		t.Fatalf("unexpected archive paths: %v", paths)
	}
}

func TestCreateSkillArchiveFromDirectory_DeterministicAcrossMtimes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillDir := filepath.Join(dir, "mtime-skill")

	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: mtime-skill\ndescription: Deterministic archive test.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(refDir, "extra.md"), []byte("# Extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := CreateSkillArchiveFromDirectory(skillDir)
	if err != nil {
		t.Fatalf("first CreateSkillArchiveFromDirectory: %v", err)
	}

	oldTime := time.Date(2019, 3, 14, 15, 9, 26, 0, time.UTC)
	newTime := time.Date(2024, 7, 4, 12, 0, 0, 0, time.UTC)

	for _, path := range []string{
		filepath.Join(skillDir, "SKILL.md"),
		filepath.Join(refDir, "extra.md"),
	} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("Chtimes old %q: %v", path, err)
		}
	}

	second, err := CreateSkillArchiveFromDirectory(skillDir)
	if err != nil {
		t.Fatalf("second CreateSkillArchiveFromDirectory: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatal("archive bytes changed after setting old mtimes")
	}

	for _, path := range []string{
		filepath.Join(skillDir, "SKILL.md"),
		filepath.Join(refDir, "extra.md"),
	} {
		if err := os.Chtimes(path, newTime, newTime); err != nil {
			t.Fatalf("Chtimes new %q: %v", path, err)
		}
	}

	third, err := CreateSkillArchiveFromDirectory(skillDir)
	if err != nil {
		t.Fatalf("third CreateSkillArchiveFromDirectory: %v", err)
	}

	if !bytes.Equal(first, third) {
		t.Fatal("archive bytes changed after setting new mtimes")
	}
}

func TestParseSkillDirectoryForImport_MarkdownOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	skillDir := filepath.Join(dir, "markdown-only")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: markdown-only\ndescription: Markdown-only import test.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := ParseSkillDirectoryForImport(skillDir)
	if err != nil {
		t.Fatalf("ParseSkillDirectoryForImport: %v", err)
	}

	if _, ok := st.GetFields()["skillArchive"]; ok {
		t.Fatal("expected no skillArchive for markdown-only skill")
	}

	if st.GetFields()["skillMarkdown"].GetStringValue() != md {
		t.Fatal("skillMarkdown mismatch")
	}
}

func TestParseSkillDirectoryForImport_BundledWhenSupportingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	skillDir := filepath.Join(dir, "with-files")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: with-files\ndescription: Bundled import test.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("# Guide\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := ParseSkillDirectoryForImport(skillDir)
	if err != nil {
		t.Fatalf("ParseSkillDirectoryForImport: %v", err)
	}

	if st.GetFields()["skillArchive"].GetStringValue() == "" {
		t.Fatal("expected skillArchive when supporting files are present")
	}
}

func TestParseSkillDirectoryBundle_SetsArchiveField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	skillDir := filepath.Join(dir, "bundle-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}

	md := "---\nname: bundle-skill\ndescription: Bundle parse test.\nmetadata:\n  version: 1.0.0\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := ParseSkillDirectoryBundle(skillDir)
	if err != nil {
		t.Fatalf("ParseSkillDirectoryBundle: %v", err)
	}

	if st.GetFields()["skillArchive"].GetStringValue() == "" {
		t.Fatal("expected non-empty skillArchive")
	}

	if st.GetFields()["skillMarkdown"].GetStringValue() != md {
		t.Fatal("skillMarkdown mismatch")
	}
}

func listArchivePaths(t *testing.T, archive []byte) []string {
	t.Helper()

	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var paths []string

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("read tar entry: %v", err)
		}

		paths = append(paths, hdr.Name)
	}

	return paths
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if strings.ReplaceAll(p, "\\", "/") == want {
			return true
		}
	}

	return false
}
