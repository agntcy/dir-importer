// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/agntcy/dir-importer/types"
	catalogv1 "github.com/agntcy/dir/api/catalog/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	// maxCatalogFileBytes caps one catalog file. Larger files are rejected
	// before they are parsed into records.
	maxCatalogFileBytes = 32 << 20
	// maxCatalogEntries caps how many entries one file may contribute.
	maxCatalogEntries = 10000
)

// aiCatalogFileFetcher reads AI Catalog documents from a local JSON file or a
// directory of such files. It does not follow entry URLs.
type aiCatalogFileFetcher struct {
	path string
}

// NewAICatalogFileFetcher creates a fetcher that reads AI Catalog JSON.
// path may be one file or a directory of *.json files (not recursive).
func NewAICatalogFileFetcher(path string) (*aiCatalogFileFetcher, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is empty")
	}

	return &aiCatalogFileFetcher{path: path}, nil
}

// Fetch reads catalog documents and emits one source item per top-level entry.
func (f *aiCatalogFileFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	const chanBuf = 8

	outputCh := make(chan types.SourceItem, chanBuf)
	errCh := make(chan error, chanBuf)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		inputs, err := openInputs(ctx, f.path)
		if err != nil {
			sendStructErr(ctx, errCh, err)

			return
		}
		defer inputs.close()

		for _, name := range inputs.names {
			if emitCatalogFile(ctx, inputs, name, outputCh, errCh) {
				return
			}
		}
	}()

	return outputCh, errCh
}

// emitCatalogFile reports file-level failures on errCh and returns true when
// ctx is canceled.
func emitCatalogFile(
	ctx context.Context,
	inputs *inputSet,
	name string,
	outputCh chan<- types.SourceItem,
	errCh chan<- error,
) bool {
	where := inputs.label(name)

	tooLarge, err := catalogFileTooLarge(inputs, name)
	if err != nil {
		sendStructErr(ctx, errCh, fmt.Errorf("%sread file: %w", where, err))

		return ctx.Err() != nil
	}

	if tooLarge {
		sendStructErr(ctx, errCh, fmt.Errorf("%scatalog file exceeds %d bytes", where, maxCatalogFileBytes))

		return ctx.Err() != nil
	}

	raw, err := inputs.read(name)
	if err != nil {
		sendStructErr(ctx, errCh, fmt.Errorf("%sread file: %w", where, err))

		return ctx.Err() != nil
	}

	if len(raw) > maxCatalogFileBytes {
		sendStructErr(ctx, errCh, fmt.Errorf("%scatalog file exceeds %d bytes", where, maxCatalogFileBytes))

		return ctx.Err() != nil
	}

	doc, err := decodeCatalog(raw)
	if err != nil {
		sendStructErr(ctx, errCh, fmt.Errorf("%s%w", where, err))

		return ctx.Err() != nil
	}

	for _, entry := range doc.GetEntries() {
		select {
		case <-ctx.Done():
			return true
		case outputCh <- types.AICatalogSourceItem(entry):
		}
	}

	return false
}

func catalogFileTooLarge(inputs *inputSet, name string) (bool, error) {
	var (
		info fs.FileInfo
		err  error
	)

	if inputs.root == nil {
		info, err = os.Stat(name)
	} else {
		info, err = inputs.root.Stat(name)
	}

	if err != nil {
		return false, err
	}

	return info.Size() > maxCatalogFileBytes, nil
}

func decodeCatalog(raw []byte) (*catalogv1.AICatalog, error) {
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")))
	if len(raw) == 0 {
		return nil, errors.New("file is empty")
	}

	var doc catalogv1.AICatalog
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode AI Catalog: %w", err)
	}

	version := strings.TrimSpace(doc.GetSpecVersion())
	if version != "" && !strings.HasPrefix(version, "1.") {
		return nil, fmt.Errorf("unsupported AI Catalog spec version %q", version)
	}

	if len(doc.GetEntries()) == 0 {
		return nil, errors.New("catalog has no entries")
	}

	if len(doc.GetEntries()) > maxCatalogEntries {
		return nil, fmt.Errorf("catalog has %d entries, exceeding the limit of %d", len(doc.GetEntries()), maxCatalogEntries)
	}

	return &doc, nil
}
