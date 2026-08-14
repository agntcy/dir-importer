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

// a2aFileFetcher reads one or more A2A AgentCard JSON objects from a local file.
type a2aFileFetcher struct {
	path string
}

// NewA2AFileFetcher creates a fetcher that reads A2A agent card(s) from a file.
// Supported formats:
//   - A JSON array of AgentCard objects
//   - A single AgentCard object
//
// Each card is emitted as types.SourceItem with Kind SourceKindA2A and the AgentCard as structpb.Struct.
func NewA2AFileFetcher(path string) (*a2aFileFetcher, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is empty")
	}

	return &a2aFileFetcher{path: path}, nil
}

// Fetch reads the file and sends each AgentCard to the output channel.
// Invalid array elements are reported on errCh and skipped. See fetchStructFile
// for the shared read/decode/validate/emit logic (mirrors oasfFileFetcher.Fetch).
func (f *a2aFileFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	return fetchStructFile(ctx, structFileFetcherConfig{
		path:            f.path,
		itemLabel:       "A2A card",
		collectionLabel: "A2A agent cards",
		wrap:            types.A2ASourceItem,
	})
}

// decodeA2ARoot returns agent card maps. For a JSON array, invalid elements are skipped and
// errors are sent to errCh (best-effort; may drop an error if errCh blocks).
func decodeA2ARoot(ctx context.Context, raw []byte, errCh chan<- error) ([]map[string]any, error) {
	return decodeStructRoot(ctx, raw, errCh, "")
}

func agentCardStructFromMap(card map[string]any) (*structpb.Struct, error) {
	return structFromMap(card, "A2A card")
}
