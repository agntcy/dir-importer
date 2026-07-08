// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"errors"
	"strings"

	"github.com/agntcy/dir-importer/types"
	"google.golang.org/protobuf/types/known/structpb"
)

// oasfFileFetcher reads one or more OASF record JSON objects from a local file.
type oasfFileFetcher struct {
	path string
}

// NewOASFFileFetcher creates a fetcher that reads record(s) already in OASF
// format from a file. Supported formats:
//   - A JSON array of OASF record objects
//   - A single OASF record object
//
// This is the counterpart to --dry-run: the <cid>.record.json files it writes
// (one OASF record object per file) can be reviewed, edited, and fed back in
// through this fetcher instead of switching to the separate push command.
//
// Each record is emitted as types.SourceItem with Kind SourceKindOASF and the
// record data as structpb.Struct. Only a light shape check (non-empty "name")
// happens here; full OASF schema validation is left to the transformer stage,
// which acts as a passthrough/validator for this import type.
func NewOASFFileFetcher(path string) (*oasfFileFetcher, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is empty")
	}

	return &oasfFileFetcher{path: path}, nil
}

// Fetch reads the file and sends each OASF record to the output channel.
// Invalid array elements are reported on errCh and skipped. See fetchStructFile
// for the shared read/decode/validate/emit logic (mirrors a2aFileFetcher.Fetch).
func (f *oasfFileFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	return fetchStructFile(ctx, structFileFetcherConfig{
		path:            f.path,
		itemLabel:       "OASF record",
		collectionLabel: "OASF records",
		wrap:            types.OASFSourceItem,
	})
}

// decodeOASFRoot returns OASF record maps. For a JSON array, invalid elements
// are skipped and errors are sent to errCh (best-effort; may drop an error if
// errCh blocks).
func decodeOASFRoot(ctx context.Context, raw []byte, errCh chan<- error) ([]map[string]any, error) {
	return decodeStructRoot(ctx, raw, errCh)
}

func oasfRecordStructFromMap(record map[string]any) (*structpb.Struct, error) {
	return structFromMap(record, "OASF record")
}
