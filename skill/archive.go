// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxArchiveFiles           = 1000
	maxArchiveUncompressedLen = 50 * 1024 * 1024 // 50 MiB, aligned with oasf-sdk bundle limits
	archiveFileMode           = 0o600            // Unix-style permission (read/write by owner)
)

type skillArchive struct {
	root              string
	files             []skillArchiveFile
	uncompressedTotal int64
}

type skillArchiveFile struct {
	relPath string
	absPath string
	size    int64
	modTime time.Time
}

// CreateSkillArchiveFromDirectory builds a .gzip of all regular files under skillDir.
// Paths inside the archive are relative to skillDir using forward slashes.
func CreateSkillArchiveFromDirectory(skillDir string) ([]byte, error) {
	root, err := resolveSearchRoot(skillDir)
	if err != nil {
		return nil, err
	}

	files, err := collectArchiveFiles(root)
	if err != nil {
		return nil, err
	}

	return encodeSkillArchive(files)
}

func collectArchiveFiles(root string) ([]skillArchiveFile, error) {
	collector := &skillArchive{root: root}

	walkErr := filepath.WalkDir(root, collector.visit)
	if walkErr != nil {
		return nil, fmt.Errorf("walk skill directory: %w", walkErr)
	}

	if len(collector.files) == 0 {
		return nil, errors.New("skill directory contains no files")
	}

	sort.Slice(collector.files, func(i, j int) bool {
		return collector.files[i].relPath < collector.files[j].relPath
	})

	return collector.files, nil
}

func (c *skillArchive) visit(walkPath string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}

	if d.IsDir() || !d.Type().IsRegular() {
		return nil
	}

	within, err := isPathWithinRoot(c.root, walkPath)
	if err != nil {
		return err
	}

	if !within {
		return nil
	}

	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("stat %s: %w", walkPath, err)
	}

	if info.Size() < 0 {
		return fmt.Errorf("invalid size for %s", walkPath)
	}

	c.uncompressedTotal += info.Size()
	if c.uncompressedTotal > maxArchiveUncompressedLen {
		return fmt.Errorf("skill directory exceeds %d byte uncompressed limit", maxArchiveUncompressedLen)
	}

	rel, err := filepath.Rel(c.root, walkPath)
	if err != nil {
		return fmt.Errorf("rel path for %s: %w", walkPath, err)
	}

	rel = normalizeArchiveEntryPath(rel)
	if rel == "" {
		return nil
	}

	c.files = append(c.files, skillArchiveFile{
		relPath: rel,
		absPath: walkPath,
		size:    info.Size(),
		modTime: info.ModTime(),
	})

	if len(c.files) > maxArchiveFiles {
		return fmt.Errorf("skill directory exceeds %d file limit", maxArchiveFiles)
	}

	return nil
}

func encodeSkillArchive(files []skillArchiveFile) ([]byte, error) {
	var buf bytes.Buffer

	gzw := gzip.NewWriter(&buf)

	tw := tar.NewWriter(gzw)

	for _, f := range files {
		header := &tar.Header{
			Name:    f.relPath,
			Mode:    archiveFileMode,
			Size:    f.size,
			ModTime: f.modTime,
		}

		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write tar header for %q: %w", f.relPath, err)
		}

		payload, err := os.ReadFile(f.absPath)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", f.relPath, err)
		}

		if int64(len(payload)) != f.size {
			return nil, fmt.Errorf("size mismatch for %q", f.relPath)
		}

		if _, err := tw.Write(payload); err != nil {
			return nil, fmt.Errorf("write tar payload for %q: %w", f.relPath, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}

	if err := gzw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

func normalizeArchiveEntryPath(name string) string {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	clean = strings.TrimPrefix(clean, "./")

	return clean
}
