// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agntcy/dir-importer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAICatalogFileFetcher_EmitsEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	body := `{
		"specVersion": "1.0",
		"entries": [
			{
				"displayName": "agent",
				"version": "1.0.0",
				"type": "application/a2a-agent-card+json",
				"data": {"name": "agent"}
			},
			{
				"displayName": "remote",
				"type": "application/a2a-agent-card+json",
				"url": "https://example.invalid/card.json"
			}
		]
	}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	fetch, err := NewAICatalogFileFetcher(path)
	require.NoError(t, err)

	items, errs := collectCatalog(t, fetch)
	require.Empty(t, errs)
	require.Len(t, items, 2)
	assert.Equal(t, types.SourceKindAICatalog, items[0].Kind)
	assert.Equal(t, "agent", items[0].Catalog.GetDisplayName())
	assert.Equal(t, "remote", items[1].Catalog.GetDisplayName())
}

func TestDecodeCatalog_RejectsUnknownSpec(t *testing.T) {
	t.Parallel()

	_, err := decodeCatalog([]byte(`{"specVersion":"2.0","entries":[{"displayName":"a"}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported AI Catalog spec version")
}

func TestDecodeCatalog_RejectsEmpty(t *testing.T) {
	t.Parallel()

	_, err := decodeCatalog([]byte(`{"specVersion":"1.0","entries":[]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entries")
}

func collectCatalog(t *testing.T, fetch *aiCatalogFileFetcher) ([]types.SourceItem, []error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	itemCh, errCh := fetch.Fetch(ctx)

	var (
		items []types.SourceItem
		errs  []error
	)

	for itemCh != nil || errCh != nil {
		select {
		case item, ok := <-itemCh:
			if !ok {
				itemCh = nil

				continue
			}

			items = append(items, item)
		case err, ok := <-errCh:
			if !ok {
				errCh = nil

				continue
			}

			errs = append(errs, err)
		}
	}

	return items, errs
}
