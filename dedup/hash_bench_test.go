// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"fmt"
	"testing"

	"github.com/agntcy/dir-importer/transformer"
	"github.com/agntcy/dir-importer/types"
	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	mcpmodel "github.com/modelcontextprotocol/registry/pkg/model"
	"google.golang.org/protobuf/types/known/structpb"
)

// benchRecord builds a record with moduleCount modules, approximating the
// shape the translator emits for an MCP server.
//
// Module count is the only size dimension varied here, and deliberately so:
// skills and domains are in stripFields, so stripEnrichmentFields removes them
// before anything is marshaled. A "50 skills" case would measure exactly the
// same work as a "0 skills" case, which is a misleading axis to publish. The
// stripped fields are still populated below at a fixed size, so the strip
// itself stays on the measured path.
func benchRecord(b *testing.B, moduleCount int) *structpb.Struct {
	b.Helper()

	const strippedSkills = 10

	skills := make([]any, 0, strippedSkills)
	for i := range strippedSkills {
		skills = append(skills, map[string]any{
			testFieldName: fmt.Sprintf("skill/category/name_%d", i),
			"id":          float64(i),
		})
	}

	modules := make([]any, 0, moduleCount)
	for i := range moduleCount {
		modules = append(modules, map[string]any{
			testFieldName: fmt.Sprintf("integration/mcp_%d", i),
			"data": map[string]any{
				testFieldName:        fmt.Sprintf("server-%d", i),
				testFieldDescription: "a representative description string for the module payload",
				"tools": []any{
					map[string]any{testFieldName: "tool_a", testFieldDescription: "does a thing"},
					map[string]any{testFieldName: "tool_b", testFieldDescription: "does another thing"},
				},
			},
		})
	}

	data, err := structpb.NewStruct(map[string]any{
		"schema_version":     "0.7.0",
		testFieldName:        "example/server",
		testFieldVersion:     "v1.2.3",
		testFieldDescription: "a representative record used to measure hashing cost",
		createdAtField:       "2026-07-30T00:00:00Z",
		"authors":            []any{"someone@example.com"},
		skillsField:          skills,
		"modules":            modules,
	})
	if err != nil {
		b.Fatalf("structpb.NewStruct: %v", err)
	}

	return data
}

// BenchmarkContentHash measures the hash alone, which is the cost buildCache
// pays per directory record it pulls.
//
// Run it with: go test ./dedup/ -run '^$' -bench . -benchmem.
func BenchmarkContentHash(b *testing.B) {
	cases := []struct {
		name    string
		modules int
	}{
		{"1module", 1},
		{"2modules", 2},
		{"5modules", 5},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			data := benchRecord(b, tc.modules)

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				if _, err := contentHash(data); err != nil {
					b.Fatalf("contentHash: %v", err)
				}
			}
		})
	}
}

func benchSourceItem() types.SourceItem {
	return types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{
		Name:        "io.github.acme/widget",
		Version:     "9.9.9",
		Description: "A widget MCP server",
		Remotes: []mcpmodel.Transport{
			{Type: "streamable-http", URL: "https://example.invalid/mcp"},
		},
	}})
}

// BenchmarkTransformOnly and BenchmarkTransformPlusHash together measure what
// the dedup stage actually adds per incoming item.
//
// isDuplicate transforms the item to get hashable content, and the Transform
// stage later transforms it again, so the added cost is a whole duplicated
// TransformRecord plus the hash, not the hash on its own. Benchmarking only
// the hash would understate it by roughly the transform.
func BenchmarkTransformOnly(b *testing.B) {
	tr := transformer.NewTransformer(false, nil, "")
	item := benchSourceItem()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		if _, err := tr.TransformRecord(item); err != nil {
			b.Fatalf("TransformRecord: %v", err)
		}
	}
}

func BenchmarkTransformPlusHash(b *testing.B) {
	tr := transformer.NewTransformer(false, nil, "")
	item := benchSourceItem()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		record, err := tr.TransformRecord(item)
		if err != nil {
			b.Fatalf("TransformRecord: %v", err)
		}

		if _, err := contentHash(record.GetData()); err != nil {
			b.Fatalf("contentHash: %v", err)
		}
	}
}
