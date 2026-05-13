// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

//nolint:goconst
package shared

import (
	"strings"
	"testing"

	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestStripImportDebugFields_NoFields(t *testing.T) {
	t.Parallel()

	r := &corev1.Record{
		Data: &structpb.Struct{
			Fields: map[string]*structpb.Value{
				"name":    structpb.NewStringValue("srv"),
				"version": structpb.NewStringValue("1.0.0"),
			},
		},
	}

	got := StripImportDebugFields(r)
	if got != "" {
		t.Errorf("expected empty summary, got %q", got)
	}

	// Real fields must remain untouched.
	if _, ok := r.GetData().GetFields()["name"]; !ok {
		t.Error("non-debug field 'name' was unexpectedly removed")
	}
}

func TestStripImportDebugFields_MCPOnly(t *testing.T) {
	t.Parallel()

	r := &corev1.Record{
		Data: &structpb.Struct{
			Fields: map[string]*structpb.Value{
				"name":              structpb.NewStringValue("srv"),
				MCPDebugSourceField: structpb.NewStringValue(`{"name":"srv"}`),
			},
		},
	}

	got := StripImportDebugFields(r)

	if !strings.Contains(got, "MCP server JSON") {
		t.Errorf("summary should mention MCP server JSON; got %q", got)
	}

	if _, ok := r.GetData().GetFields()[MCPDebugSourceField]; ok {
		t.Errorf("%s was not removed", MCPDebugSourceField)
	}

	if _, ok := r.GetData().GetFields()["name"]; !ok {
		t.Error("non-debug field 'name' was unexpectedly removed")
	}
}

func TestStripImportDebugFields_A2AOnly(t *testing.T) {
	t.Parallel()

	r := &corev1.Record{
		Data: &structpb.Struct{
			Fields: map[string]*structpb.Value{
				"name":              structpb.NewStringValue("agent"),
				A2ADebugSourceField: structpb.NewStringValue(`{"name":"agent"}`),
			},
		},
	}

	got := StripImportDebugFields(r)

	if !strings.Contains(got, "A2A AgentCard JSON") {
		t.Errorf("summary should mention A2A AgentCard JSON; got %q", got)
	}

	if _, ok := r.GetData().GetFields()[A2ADebugSourceField]; ok {
		t.Errorf("%s was not removed", A2ADebugSourceField)
	}
}

func TestStripImportDebugFields_BothFields(t *testing.T) {
	t.Parallel()

	r := &corev1.Record{
		Data: &structpb.Struct{
			Fields: map[string]*structpb.Value{
				MCPDebugSourceField: structpb.NewStringValue(`{"kind":"mcp"}`),
				A2ADebugSourceField: structpb.NewStringValue(`{"kind":"a2a"}`),
			},
		},
	}

	got := StripImportDebugFields(r)
	if !strings.Contains(got, "MCP server JSON") || !strings.Contains(got, "A2A AgentCard JSON") {
		t.Errorf("summary should mention both sources; got %q", got)
	}

	if len(r.GetData().GetFields()) != 0 {
		t.Errorf("expected all debug fields removed, fields left: %v", r.GetData().GetFields())
	}
}

func TestStripImportDebugFields_NilSafe(t *testing.T) {
	t.Parallel()

	if got := StripImportDebugFields(&corev1.Record{}); got != "" {
		t.Errorf("expected empty summary for record with no data, got %q", got)
	}

	if got := StripImportDebugFields(&corev1.Record{Data: &structpb.Struct{}}); got != "" {
		t.Errorf("expected empty summary for record with no fields, got %q", got)
	}
}

func TestExtractNameVersion(t *testing.T) {
	t.Parallel()

	r := &corev1.Record{
		Data: &structpb.Struct{
			Fields: map[string]*structpb.Value{
				"name":    structpb.NewStringValue("srv"),
				"version": structpb.NewStringValue("1.2.3"),
			},
		},
	}

	s, err := ExtractNameVersion(r)
	if err != nil {
		t.Fatal(err)
	}

	if s != "srv@1.2.3" {
		t.Errorf("got %q", s)
	}

	_, err = ExtractNameVersion(&corev1.Record{})
	if err == nil {
		t.Fatal("expected error")
	}
}
