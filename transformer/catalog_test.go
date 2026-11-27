// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package transformer

import (
	"testing"

	"github.com/agntcy/dir-importer/types"
	catalogv1 "github.com/agntcy/dir/api/catalog/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestConvertAICatalogToOASF_Leaf(t *testing.T) {
	t.Parallel()

	entry := catalogEntry(t, `{
		"identifier": "urn:air:org.agntcy:cid:abc",
		"displayName": "test-a2a-agent",
		"version": "1.0.0",
		"description": "A test A2A agent",
		"updatedAt": "2024-01-01T00:00:00Z",
		"type": "application/a2a-agent-card+json",
		"tags": [
			"oasf:1.0.0:skills:/example/skill",
			"oasf:1.0.0:domains:/example/domain",
			"team=platform"
		],
		"data": {"card_data": {"name": "test-a2a-agent"}}
	}`)

	record, err := NewTransformer(false, []string{"Ada"}, "").convertAICatalogToOASF(entry)
	require.NoError(t, err)

	decoded, err := record.Decode()
	require.NoError(t, err)
	assert.Equal(t, "test-a2a-agent", decoded.GetName())
	assert.Equal(t, "1.0.0", decoded.GetVersion())
	assert.Equal(t, "1.0.0", decoded.GetSchemaVersion())
	assert.Equal(t, "A test A2A agent", decoded.GetDescription())
	assert.Equal(t, "2024-01-01T00:00:00Z", decoded.GetCreatedAt())
	assert.Equal(t, []string{"Ada"}, decoded.GetAuthors())
	require.Len(t, decoded.GetSkills(), 1)
	assert.Equal(t, "/example/skill", decoded.GetSkills()[0].GetName())
	require.Len(t, decoded.GetDomains(), 1)
	assert.Equal(t, "/example/domain", decoded.GetDomains()[0].GetName())
	assert.Equal(t, "platform", decoded.GetAnnotations()["team"])
	assert.Equal(t, "urn:air:org.agntcy:cid:abc", decoded.GetAnnotations()[catalogIdentifierAnnotation])

	require.Len(t, decoded.GetModules(), 1)
	assert.Equal(t, "integration/a2a", decoded.GetModules()[0].GetName())
	assert.Equal(t, "test-a2a-agent", decoded.GetModules()[0].GetData()["card_data"].(map[string]any)["name"])
}

func TestConvertAICatalogToOASF_Container(t *testing.T) {
	t.Parallel()

	entry := catalogEntry(t, `{
		"displayName": "combo",
		"version": "2.0.0",
		"type": "application/ai-catalog+json",
		"data": {
			"specVersion": "1.0",
			"entries": [
				{
					"identifier": "urn:air:org.agntcy:cid:abc:a2a-0",
					"displayName": "combo - A2A",
					"type": "application/a2a-agent-card+json",
					"data": {"card_data": {"name": "combo"}}
				},
				{
					"identifier": "urn:air:org.agntcy:cid:abc:mcp-0",
					"displayName": "combo - MCP",
					"type": "application/mcp-server-card+json",
					"tags": ["module=mcp"],
					"data": {"name": "combo"}
				}
			]
		}
	}`)

	record, err := NewTransformer(false, nil, "1.0.0").convertAICatalogToOASF(entry)
	require.NoError(t, err)

	decoded, err := record.Decode()
	require.NoError(t, err)
	require.Len(t, decoded.GetModules(), 2)

	names := []string{decoded.GetModules()[0].GetName(), decoded.GetModules()[1].GetName()}
	assert.ElementsMatch(t, []string{"integration/a2a", "integration/mcp"}, names)
	assert.Equal(t, "mcp", decoded.GetModules()[1].GetAnnotations()["module"])
}

func TestConvertAICatalogToOASF_SkillMediaType(t *testing.T) {
	t.Parallel()

	entry := catalogEntry(t, `{
		"displayName": "summarize-text",
		"version": "1.0.0",
		"type": "application/agent-skills+md",
		"data": {"skill_file": "SKILL.md"}
	}`)

	record, err := NewTransformer(false, nil, "1.0.0").convertAICatalogToOASF(entry)
	require.NoError(t, err)

	decoded, err := record.Decode()
	require.NoError(t, err)
	require.Len(t, decoded.GetModules(), 1)
	assert.Equal(t, catalogv1.AgentSkillsModuleName, decoded.GetModules()[0].GetName())
	assert.Equal(t, catalogv1.ProtocolAgentSkillsMdMediaType, decoded.GetModules()[0].GetArtifactMediaType())
}

func TestConvertAICatalogToOASF_RejectsURL(t *testing.T) {
	t.Parallel()

	entry := catalogEntry(t, `{
		"displayName": "remote",
		"type": "application/a2a-agent-card+json",
		"url": "https://example.invalid/card.json"
	}`)

	_, err := NewTransformer(false, nil, "1.0.0").convertAICatalogToOASF(entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no inline data")
}

func TestAICatalogSourceItemNameVersion(t *testing.T) {
	t.Parallel()

	entry := catalogEntry(t, `{"displayName": "agent", "version": "1.2.3", "type": "application/a2a-agent-card+json", "data": {}}`)
	assert.Equal(t, "agent@1.2.3", types.AICatalogSourceItem(entry).NameVersion())
}

func catalogEntry(t *testing.T, raw string) *catalogv1.CatalogEntry {
	t.Helper()

	var entry catalogv1.CatalogEntry
	require.NoError(t, protojson.Unmarshal([]byte(raw), &entry))

	return &entry
}
