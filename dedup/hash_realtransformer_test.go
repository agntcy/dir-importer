// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"testing"
	"time"

	"github.com/agntcy/dir-importer/transformer"
	"github.com/agntcy/dir-importer/types"
	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	mcpmodel "github.com/modelcontextprotocol/registry/pkg/model"
)

// waitForNextSecond blocks until the next whole-second boundary (plus a small
// buffer), so a subsequent time.Now().UTC().Format(time.RFC3339) call is
// guaranteed to produce a different value than one made before this returns.
// RFC3339 (as produced by the OASF translator's created_at field) has
// one-second resolution, so a >=1s sleep already guarantees a boundary
// crossing; this additionally anchors the test on that boundary explicitly
// rather than relying on timing margin.
func waitForNextSecond(t *testing.T) {
	t.Helper()

	now := time.Now().UTC()
	next := now.Truncate(time.Second).Add(time.Second)
	time.Sleep(time.Until(next) + 10*time.Millisecond) //nolint:mnd
}

// TestContentHash_StableAcrossCreatedAtTimeBoundary_RealTranslator is a
// regression test for the real OASF translator (not the testTransform stub
// used elsewhere in this package): the translator stamps a fresh
// created_at:time.Now() onto every record it produces (oasf-sdk
// pkg/translator mcp.go/a2a.go/agentskills.go), so hashing its raw output
// would make the content hash change on every call regardless of whether the
// underlying content changed - silently disabling duplicate detection in
// production. It transforms the *same* source item twice, forcing a real
// wall-clock second boundary between the two calls, and asserts the
// resulting content hashes are equal.
func TestContentHash_StableAcrossCreatedAtTimeBoundary_RealTranslator(t *testing.T) {
	t.Parallel()

	tr := transformer.NewTransformer(false, nil, "")

	item := types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{
		Name:        "io.github.acme/widget",
		Version:     testVersion,
		Description: "A widget MCP server",
		Remotes: []mcpmodel.Transport{
			{Type: "streamable-http", URL: "https://example.invalid/mcp"},
		},
	}})

	record1, err := tr.TransformRecord(item)
	if err != nil {
		t.Fatalf("TransformRecord (1st call): %v", err)
	}

	waitForNextSecond(t)

	record2, err := tr.TransformRecord(item)
	if err != nil {
		t.Fatalf("TransformRecord (2nd call): %v", err)
	}

	createdAt1 := record1.GetData().GetFields()[createdAtField].GetStringValue()
	createdAt2 := record2.GetData().GetFields()[createdAtField].GetStringValue()

	if createdAt1 == "" || createdAt2 == "" {
		t.Fatalf("test invalid: translator did not set %q (got %q, %q) - update this test to match the translator's current field name", createdAtField, createdAt1, createdAt2)
	}

	if createdAt1 == createdAt2 {
		t.Fatalf("test invalid: created_at did not change between calls (%q == %q both times); this test only proves anything if it crosses a real second boundary", createdAt1, createdAt2)
	}

	hash1, err := contentHash(record1.GetData())
	if err != nil {
		t.Fatalf("contentHash(record1): %v", err)
	}

	hash2, err := contentHash(record2.GetData())
	if err != nil {
		t.Fatalf("contentHash(record2): %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hash1 = %q (created_at=%s), hash2 = %q (created_at=%s); want equal - content hash of the real translator's output must be stable across created_at, or duplicate detection breaks in production",
			hash1, createdAt1, hash2, createdAt2)
	}
}
