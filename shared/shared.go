// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"fmt"

	corev1 "github.com/agntcy/dir/api/core/v1"
)

// Importer-only field names that the transformer attaches to records for
// debugging push-time failures. They are NOT part of the OASF schema and
// must be stripped before a record is persisted (push or dry-run write).
const (
	MCPDebugSourceField = "__mcp_debug_source"
	A2ADebugSourceField = "__a2a_debug_source"
)

// StripImportDebugFields removes importer-only debug fields from the record's
// data and returns a human-readable, multi-line summary of what was stripped
// (suitable for stderr debug output on push failure). It returns an empty
// string if no debug fields were present.
func StripImportDebugFields(record *corev1.Record) string {
	data := record.GetData()
	if data == nil || data.GetFields() == nil {
		return ""
	}

	fields := data.GetFields()

	var parts []string

	if v, ok := fields[MCPDebugSourceField]; ok {
		parts = append(parts, "MCP server JSON:\n"+v.GetStringValue())

		delete(fields, MCPDebugSourceField)
	}

	if v, ok := fields[A2ADebugSourceField]; ok {
		parts = append(parts, "A2A AgentCard JSON:\n"+v.GetStringValue())

		delete(fields, A2ADebugSourceField)
	}

	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return parts[0] + "\n\n" + parts[1]
	}
}

// ExtractNameVersion extracts "name@version" from a record.
func ExtractNameVersion(record *corev1.Record) (string, error) {
	if record == nil || record.GetData() == nil {
		return "", errors.New("record or record data is nil")
	}

	fields := record.GetData().GetFields()
	if fields == nil {
		return "", errors.New("record data fields are nil")
	}

	// Extract name
	nameVal, ok := fields["name"]
	if !ok {
		return "", errors.New("record missing 'name' field")
	}

	name := nameVal.GetStringValue()
	if name == "" {
		return "", errors.New("record 'name' field is empty")
	}

	// Extract version
	versionVal, ok := fields["version"]
	if !ok {
		return "", errors.New("record missing 'version' field")
	}

	version := versionVal.GetStringValue()
	if version == "" {
		return "", errors.New("record 'version' field is empty")
	}

	return fmt.Sprintf("%s@%s", name, version), nil
}
