// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"

	"google.golang.org/protobuf/types/known/structpb"
)

// ParseSkillDirectoryForImport parses a skill directory for import. When the directory
// contains only SKILL.md, the payload omits skillArchive (markdown record). When it
// contains additional files, a .gzip bundle is built and skillArchive is set.
func ParseSkillDirectoryForImport(skillDir string) (*structpb.Struct, error) {
	root, err := openSkillRoot(skillDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	return parseSkillRootForImport(root, root.Name())
}

func parseSkillRootForImport(root *os.Root, displayPath string) (*structpb.Struct, error) {
	isBundle, err := isSkillBundleRoot(root)
	if err != nil {
		return nil, err
	}
	if !isBundle {
		return parseSkillRoot(root, displayPath)
	}

	st, err := parseSkillRoot(root, displayPath)
	if err != nil {
		return nil, err
	}
	archive, err := createSkillArchiveFromRoot(root)
	if err != nil {
		return nil, fmt.Errorf("create skill archive: %w", err)
	}
	return withSkillArchive(st, archive)
}

// ParseSkillDirectoryBundle reads skillDir, builds a .gzip archive of its files, and returns
// a structpb payload for oasf-sdk SkillBundleToRecord:
//   - skillMarkdown: full SKILL.md content (required)
//   - skillArchive: base64-encoded .gzip bytes (required)
func ParseSkillDirectoryBundle(skillDir string) (*structpb.Struct, error) {
	root, err := openSkillRoot(skillDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	st, err := parseSkillRoot(root, root.Name())
	if err != nil {
		return nil, err
	}
	archive, err := createSkillArchiveFromRoot(root)
	if err != nil {
		return nil, fmt.Errorf("create skill archive: %w", err)
	}
	return withSkillArchive(st, archive)
}

func withSkillArchive(st *structpb.Struct, archive []byte) (*structpb.Struct, error) {
	if st == nil {
		return nil, fmt.Errorf("skill payload is nil")
	}

	fields := st.AsMap()
	fields["skillArchive"] = base64.StdEncoding.EncodeToString(archive)

	out, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("skill bundle payload struct: %w", err)
	}

	return out, nil
}

func isSkillBundleRoot(root *os.Root) (bool, error) {
	var hasExtra bool
	walkErr := fs.WalkDir(root.FS(), ".", func(walkPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if normalizeArchiveEntryPath(walkPath) != skillFileName {
			hasExtra = true
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, fmt.Errorf("walk skill directory: %w", walkErr)
	}
	return hasExtra, nil
}
