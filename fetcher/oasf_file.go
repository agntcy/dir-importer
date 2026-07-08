// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
// Invalid array elements are reported on errCh and skipped.
//
//nolint:cyclop // linear control flow with many ctx/errCh branches (mirrors a2aFileFetcher.Fetch)
func (f *oasfFileFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	const chanBuf = 8

	outputCh := make(chan types.SourceItem, chanBuf)
	errCh := make(chan error, chanBuf)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		raw, err := os.ReadFile(f.path)
		if err != nil {
			select {
			case errCh <- fmt.Errorf("read file: %w", err):
			case <-ctx.Done():
			}

			return
		}

		raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")) // UTF-8 BOM

		items, fatalErr := decodeOASFRoot(ctx, raw, errCh)
		if fatalErr != nil {
			select {
			case errCh <- fatalErr:
			case <-ctx.Done():
			}

			return
		}

		if len(items) == 0 {
			select {
			case errCh <- errors.New("no OASF records found in file (check earlier errors if this was a JSON array)"):
			case <-ctx.Done():
			}

			return
		}

		for i, recordMap := range items {
			rec, err := oasfRecordStructFromMap(recordMap)
			if err != nil {
				select {
				case errCh <- fmt.Errorf("OASF record index %d: %w", i, err):
				case <-ctx.Done():
					return
				}

				continue
			}

			select {
			case <-ctx.Done():
				return
			case outputCh <- types.OASFSourceItem(rec):
			}
		}
	}()

	return outputCh, errCh
}

func oasfRecordStructFromMap(record map[string]any) (*structpb.Struct, error) {
	if record == nil {
		return nil, errors.New("record is nil")
	}

	name, _ := record["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("OASF record missing non-empty \"name\"")
	}

	st, err := structpb.NewStruct(record)
	if err != nil {
		return nil, fmt.Errorf("OASF record as struct: %w", err)
	}

	return st, nil
}

// decodeOASFRoot returns OASF record maps. For a JSON array, invalid elements
// are skipped and errors are sent to errCh (best-effort; may drop an error if
// errCh blocks).
func decodeOASFRoot(ctx context.Context, raw []byte, errCh chan<- error) ([]map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("file is empty")
	}

	if raw[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, fmt.Errorf("decode JSON array: %w", err)
		}

		out := make([]map[string]any, 0, len(arr))

		for i, elt := range arr {
			var m map[string]any
			if err := json.Unmarshal(elt, &m); err != nil {
				sendOASFErr(ctx, errCh, fmt.Errorf("array index %d: %w", i, err))

				continue
			}

			out = append(out, m)
		}

		return out, nil
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}

	return []map[string]any{obj}, nil
}

func sendOASFErr(ctx context.Context, errCh chan<- error, err error) {
	select {
	case errCh <- err:
	case <-ctx.Done():
	}
}
