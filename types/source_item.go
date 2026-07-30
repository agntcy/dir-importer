// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"fmt"
	"strings"

	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"google.golang.org/protobuf/types/known/structpb"
)

// SourceKind identifies which payload is set on SourceItem.
type SourceKind int

const (
	// SourceKindMCP is an MCP registry/listing payload ([mcpapiv0.ServerResponse]).
	SourceKindMCP SourceKind = iota
	// SourceKindA2A is an A2A AgentCard as [structpb.Struct] (JSON object).
	SourceKindA2A
	// SourceKindAgentSkill is a parsed Agent Skill as [structpb.Struct] (see importer/skill package contract).
	SourceKindAgentSkill
	// SourceKindOASF is a record already in OASF format as [structpb.Struct] (e.g. --dry-run output re-imported).
	SourceKindOASF
)

// SourceItem is one record from fetch through dedup before OASF transformation.
// SourceItem.Kind selects which field is valid.
type SourceItem struct {
	Kind  SourceKind
	MCP   mcpapiv0.ServerResponse
	A2A   *structpb.Struct
	Skill *structpb.Struct
	OASF  *structpb.Struct
}

// MCPSourceItem wraps an MCP server response for the pipeline.
func MCPSourceItem(s mcpapiv0.ServerResponse) SourceItem {
	return SourceItem{Kind: SourceKindMCP, MCP: s}
}

// A2ASourceItem wraps an AgentCard as structpb.Struct for the pipeline.
func A2ASourceItem(card *structpb.Struct) SourceItem {
	return SourceItem{Kind: SourceKindA2A, A2A: card}
}

// AgentSkillSourceItem wraps a parsed Agent Skill payload for the pipeline.
func AgentSkillSourceItem(skill *structpb.Struct) SourceItem {
	return SourceItem{Kind: SourceKindAgentSkill, Skill: skill}
}

// OASFSourceItem wraps a record already in OASF format as structpb.Struct for the pipeline.
func OASFSourceItem(record *structpb.Struct) SourceItem {
	return SourceItem{Kind: SourceKindOASF, OASF: record}
}

// defaultVersion is used for NameVersion when a source item has a name but no
// version (e.g. an AgentCard, Agent Skill, or OASF record that omits it).
const defaultVersion = "v1.0.0"

// NameVersion returns "name@version" for deduplication, or "" if it cannot be derived.
func (s SourceItem) NameVersion() string {
	switch s.Kind {
	case SourceKindMCP:
		if s.MCP.Server.Name != "" && s.MCP.Server.Version != "" {
			return fmt.Sprintf("%s@%s", s.MCP.Server.Name, s.MCP.Server.Version)
		}
	case SourceKindA2A:
		return nameVersion(topLevelNameVersionFields(s.A2A))
	case SourceKindAgentSkill:
		return nameVersion(agentSkillNameVersionFields(s.Skill))
	case SourceKindOASF:
		return nameVersion(topLevelNameVersionFields(s.OASF))
	}

	return ""
}

// nameVersion formats name/version as "name@version", defaulting an empty
// version to defaultVersion. Returns "" if name is empty.
func nameVersion(name, version string) string {
	if name == "" {
		return ""
	}

	if version == "" {
		version = defaultVersion
	}

	return fmt.Sprintf("%s@%s", name, version)
}

// topLevelNameVersionFields extracts the top-level "name"/"version" string
// fields shared by A2A AgentCard and OASF record payloads.
func topLevelNameVersionFields(card *structpb.Struct) (string, string) {
	if card == nil {
		return "", ""
	}

	var name, version string

	fields := card.GetFields()
	if v, ok := fields["name"]; ok {
		name = strings.TrimSpace(v.GetStringValue())
	}

	if v, ok := fields["version"]; ok {
		version = strings.TrimSpace(v.GetStringValue())
	}

	return name, version
}

func agentSkillNameVersionFields(skill *structpb.Struct) (string, string) {
	if skill == nil {
		return "", ""
	}

	fields := skill.GetFields()

	var name, version string

	if v, ok := fields["name"]; ok {
		name = strings.TrimSpace(v.GetStringValue())
	}

	if metaVal, ok := fields["metadata"]; ok {
		if meta := metaVal.GetStructValue(); meta != nil {
			if v, ok := meta.GetFields()["version"]; ok {
				version = strings.TrimSpace(v.GetStringValue())
			}
		}
	}

	return name, version
}
