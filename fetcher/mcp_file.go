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
	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
)

// mcpFileFetcher reads MCP registry-style server definitions from a local JSON file.
type mcpFileFetcher struct {
	path string
}

// NewMCPFileFetcher creates a fetcher that reads one or more MCP servers from a
// file, or from every *.json file directly inside a directory.
// Supported formats:
//   - A JSON array of ServerResponse
//   - A single bare ServerJSON object (wrapped as ServerResponse)
func NewMCPFileFetcher(path string) (*mcpFileFetcher, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is empty")
	}

	return &mcpFileFetcher{path: path}, nil
}

// Fetch reads the configured path and sends each decoded server to the output
// channel. When the path is a directory, a failure in one file is reported on
// errCh without aborting the remaining files.
func (f *mcpFileFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	const chanBuf = 8

	outputCh := make(chan types.SourceItem, chanBuf)
	errCh := make(chan error, chanBuf)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		paths, err := inputPaths(ctx, f.path)
		if err != nil {
			sendStructErr(ctx, errCh, err)

			return
		}

		// Only qualify messages with a file name when there is more than one
		// file, so single-file errors stay exactly as they were.
		multi := len(paths) > 1

		for _, path := range paths {
			if canceled := emitServersFromFile(ctx, path, multi, outputCh, errCh); canceled {
				return
			}
		}
	}()

	return outputCh, errCh
}

// emitServersFromFile reads one file and emits every server defined in it.
// All failures are reported on errCh; it returns true only when ctx was
// canceled, which tells the caller to stop processing further files.
func emitServersFromFile(
	ctx context.Context,
	path string,
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

	servers, err := decodeServerResponses(raw)
	if err != nil {
		sendStructErr(ctx, errCh, fmt.Errorf("%s%w", where, err))

		return ctx.Err() != nil
	}

	if len(servers) == 0 {
		sendStructErr(ctx, errCh, fmt.Errorf("%sno MCP servers found in file", where))

		return ctx.Err() != nil
	}

	for _, srv := range servers {
		select {
		case <-ctx.Done():
			return true
		case outputCh <- types.MCPSourceItem(srv):
		}
	}

	return false
}

func decodeServerResponses(raw []byte) ([]mcpapiv0.ServerResponse, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("file is empty")
	}

	// JSON array of ServerResponse
	if raw[0] == '[' {
		var batch []mcpapiv0.ServerResponse
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("decode JSON array: %w", err)
		}

		out := make([]mcpapiv0.ServerResponse, 0, len(batch))
		for _, item := range batch {
			if item.Server.Name == "" {
				continue
			}

			out = append(out, item)
		}

		if len(out) == 0 {
			return nil, errors.New("no valid servers in JSON array (missing server.name)")
		}

		return out, nil
	}

	// Single JSON object: bare ServerJSON only (registry server.json shape)
	var bare mcpapiv0.ServerJSON
	if err := json.Unmarshal(raw, &bare); err == nil && bare.Name != "" {
		return []mcpapiv0.ServerResponse{{Server: bare}}, nil
	}

	return nil, errors.New("could not parse file as JSON array of servers or bare server.json")
}
