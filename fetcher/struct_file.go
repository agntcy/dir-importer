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
	"path/filepath"
	"strings"

	"github.com/agntcy/dir-importer/types"
	"google.golang.org/protobuf/types/known/structpb"
)

// structFileFetcherConfig configures fetchStructFile for one source kind that
// reads bare JSON object(s) from a file. The A2A AgentCard and OASF record file
// fetchers differ only in these labels and in how a validated struct is wrapped
// into a types.SourceItem, so the read/decode/validate/emit logic lives here once.
type structFileFetcherConfig struct {
	path string

	// itemLabel names one item for per-item error messages, e.g. "A2A card" or "OASF record".
	itemLabel string
	// collectionLabel names the collection for the "none found" error, e.g. "A2A agent cards".
	collectionLabel string

	wrap func(*structpb.Struct) types.SourceItem
}

// fetchStructFile reads cfg.path, decodes one or more bare JSON objects (a JSON
// array or a single object), validates each has a non-empty top-level "name",
// and emits each as a types.SourceItem via cfg.wrap. Invalid array elements are
// reported on errCh and skipped.
//
// cfg.path may be either a single file or a directory. When it is a directory,
// every *.json file directly inside it is read, and a failure in one file is
// reported on errCh without aborting the remaining files.
func fetchStructFile(ctx context.Context, cfg structFileFetcherConfig) (<-chan types.SourceItem, <-chan error) {
	const chanBuf = 8

	outputCh := make(chan types.SourceItem, chanBuf)
	errCh := make(chan error, chanBuf)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		paths, err := inputPaths(ctx, cfg.path)
		if err != nil {
			sendStructErr(ctx, errCh, err)

			return
		}

		// Only qualify messages with a file name when there is more than one
		// file, so single-file errors stay exactly as they were.
		multi := len(paths) > 1

		for _, path := range paths {
			if canceled := emitStructsFromFile(ctx, path, cfg, multi, outputCh, errCh); canceled {
				return
			}
		}
	}()

	return outputCh, errCh
}

// emitStructsFromFile reads one file and emits every valid object in it.
// All failures are reported on errCh; it returns true only when ctx was
// canceled, which tells the caller to stop processing further files.
//
//nolint:cyclop // linear control flow with many ctx/errCh branches
func emitStructsFromFile(
	ctx context.Context,
	path string,
	cfg structFileFetcherConfig,
	multi bool,
	outputCh chan<- types.SourceItem,
	errCh chan<- error,
) bool {
	where := ""
	if multi {
		where = filepath.Base(path) + ": "
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		sendStructErr(ctx, errCh, fmt.Errorf("%sread file: %w", where, err))

		return ctx.Err() != nil
	}

	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")) // UTF-8 BOM

	items, fatalErr := decodeStructRoot(ctx, raw, errCh)
	if fatalErr != nil {
		sendStructErr(ctx, errCh, fmt.Errorf("%s%w", where, fatalErr))

		return ctx.Err() != nil
	}

	if len(items) == 0 {
		sendStructErr(ctx, errCh,
			fmt.Errorf("%sno %s found in file (check earlier errors if this was a JSON array)", where, cfg.collectionLabel))

		return ctx.Err() != nil
	}

	for i, m := range items {
		st, err := structFromMap(m, cfg.itemLabel)
		if err != nil {
			select {
			case errCh <- fmt.Errorf("%s%s index %d: %w", where, cfg.itemLabel, i, err):
			case <-ctx.Done():
				return true
			}

			continue
		}

		select {
		case <-ctx.Done():
			return true
		case outputCh <- cfg.wrap(st):
		}
	}

	return false
}

// structFromMap validates that m has a non-empty top-level "name" and converts
// it to a structpb.Struct. itemLabel names the item kind for error messages.
func structFromMap(m map[string]any, itemLabel string) (*structpb.Struct, error) {
	if m == nil {
		return nil, fmt.Errorf("%s is nil", itemLabel)
	}

	name, _ := m["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%s missing non-empty %q", itemLabel, "name")
	}

	st, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("%s as struct: %w", itemLabel, err)
	}

	return st, nil
}

// decodeStructRoot returns bare JSON object maps from raw. For a JSON array,
// invalid elements are skipped and errors are sent to errCh (best-effort; may
// drop an error if errCh blocks).
func decodeStructRoot(ctx context.Context, raw []byte, errCh chan<- error) ([]map[string]any, error) {
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
				sendStructErr(ctx, errCh, fmt.Errorf("array index %d: %w", i, err))

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

func sendStructErr(ctx context.Context, errCh chan<- error, err error) {
	select {
	case errCh <- err:
	case <-ctx.Done():
	}
}
