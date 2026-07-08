// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package transformer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/types"
	mcpapiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	mcpmodel "github.com/modelcontextprotocol/registry/pkg/model"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	fixtureName        = "io.test/transformer-fixture"
	fixtureVersion     = "1.0.0"
	fieldName          = "name"
	fieldVersion       = "version"
	fieldDescription   = "description"
	fixtureDescription = "transformer test fixture"
)

// --- TransformRecord direct (per-kind) ---

func TestTransformRecord_MCP_Success_DebugOn(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(true, nil, "")
	item := types.MCPSourceItem(validMCP())

	rec, err := tr.TransformRecord(item)
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	if rec == nil || rec.GetData() == nil {
		t.Fatal("returned record has no data")
	}

	// __mcp_debug_source carries the original MCP payload so push-time failures can be debugged.
	if dbg, ok := rec.GetData().GetFields()["__mcp_debug_source"]; !ok || dbg.GetStringValue() == "" {
		t.Errorf("__mcp_debug_source not attached: %+v", rec.GetData().GetFields())
	}

	if !strings.Contains(rec.GetData().GetFields()["__mcp_debug_source"].GetStringValue(), fixtureName) {
		t.Error("debug source should embed the original server name")
	}
}

// Without --debug the transformer must NOT attach the importer-only debug
// field, so produced records stay free of payload bloat and unknown-OASF-field
// risk all the way through the pipeline.
func TestTransformRecord_MCP_Success_DebugOff(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	rec, err := tr.TransformRecord(types.MCPSourceItem(validMCP()))
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	if rec == nil || rec.GetData() == nil {
		t.Fatal("returned record has no data")
	}

	if _, ok := rec.GetData().GetFields()["__mcp_debug_source"]; ok {
		t.Errorf("__mcp_debug_source must NOT be attached when debug=false: %+v", rec.GetData().GetFields())
	}
}

func TestTransformRecord_MCP_TranslationError(t *testing.T) {
	t.Parallel()

	// A server with neither remotes nor packages is structurally invalid as
	// far as the OASF translator is concerned (nothing to bind a runtime to).
	tr := NewTransformer(false, nil, "")
	item := types.MCPSourceItem(mcpapiv0.ServerResponse{
		Server: mcpapiv0.ServerJSON{
			Name:    "io.test/empty",
			Version: "1.0.0",
		},
	})

	_, err := tr.TransformRecord(item)
	if err == nil {
		t.Fatal("expected translation error for server with no packages or remotes")
	}

	if !strings.Contains(err.Error(), "OASF") && !strings.Contains(err.Error(), "convert") {
		t.Errorf("error should mention OASF/convert, got %q", err.Error())
	}
}

func TestTransformRecord_A2A_Success_DebugOn(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(true, nil, "")

	card := validA2ACard(t)

	rec, err := tr.TransformRecord(types.A2ASourceItem(card))
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	if rec == nil || rec.GetData() == nil {
		t.Fatal("returned record has no data")
	}

	dbg, ok := rec.GetData().GetFields()["__a2a_debug_source"]
	if !ok || dbg.GetStringValue() == "" {
		t.Errorf("__a2a_debug_source not attached: %+v", rec.GetData().GetFields())
	}
}

func TestTransformRecord_A2A_Success_DebugOff(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	rec, err := tr.TransformRecord(types.A2ASourceItem(validA2ACard(t)))
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	if rec == nil || rec.GetData() == nil {
		t.Fatal("returned record has no data")
	}

	if _, ok := rec.GetData().GetFields()["__a2a_debug_source"]; ok {
		t.Errorf("__a2a_debug_source must NOT be attached when debug=false: %+v", rec.GetData().GetFields())
	}
}

func TestTransformRecord_A2A_NilCard(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	_, err := tr.TransformRecord(types.SourceItem{Kind: types.SourceKindA2A, A2A: nil})
	if err == nil {
		t.Fatal("expected error for nil A2A card")
	}

	if !strings.Contains(err.Error(), "A2A") {
		t.Errorf("error should mention A2A, got %q", err.Error())
	}
}

func TestTransformRecord_AgentSkill_Success(t *testing.T) {
	t.Parallel()

	// Agent-skill records must never carry a debug source field (server-side
	// OASF validation rejects unknown fields on this type), regardless of
	// whether --debug is enabled.
	for _, debug := range []bool{false, true} {
		tr := NewTransformer(debug, nil, "")

		skill := validAgentSkill(t)

		rec, err := tr.TransformRecord(types.AgentSkillSourceItem(skill))
		if err != nil {
			t.Fatalf("TransformRecord(debug=%v): %v", debug, err)
		}

		if rec == nil || rec.GetData() == nil {
			t.Fatalf("returned record has no data (debug=%v)", debug)
		}

		if _, ok := rec.GetData().GetFields()["__agentskill_debug_source"]; ok {
			t.Errorf("agent-skill records must not carry __agentskill_debug_source (debug=%v)", debug)
		}
	}
}

func TestTransformRecord_AgentSkill_WithAuthors(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, []string{"ACME Corp", "Example Team"}, "")

	rec, err := tr.TransformRecord(types.AgentSkillSourceItem(validAgentSkill(t)))
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	authorVals := rec.GetData().GetFields()["authors"].GetListValue().GetValues()
	if len(authorVals) != 2 {
		t.Fatalf("got %d authors, want 2", len(authorVals))
	}

	if authorVals[0].GetStringValue() != "ACME Corp" || authorVals[1].GetStringValue() != "Example Team" {
		t.Fatalf("unexpected authors: %v", authorVals)
	}
}

func TestTransformRecord_AgentSkill_Bundle(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	rec, err := tr.TransformRecord(types.AgentSkillSourceItem(validAgentSkillBundle(t)))
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	if rec == nil || rec.GetData() == nil {
		t.Fatal("returned record has no data")
	}

	foundModule := false

	for _, modVal := range rec.GetData().GetFields()["modules"].GetListValue().GetValues() {
		mod := modVal.GetStructValue()
		if mod.GetFields()["name"].GetStringValue() != "core/language_model/agentskills" {
			continue
		}

		foundModule = true

		artifact := mod.GetFields()["artifact"].GetStructValue()
		if artifact.GetFields()["media_type"].GetStringValue() != "application/agent-skills+gzip" {
			t.Fatalf("media_type = %q", artifact.GetFields()["media_type"].GetStringValue())
		}

		artifacts := mod.GetFields()["data"].GetStructValue().GetFields()["artifacts"].GetListValue().GetValues()
		if len(artifacts) != 1 {
			t.Fatalf("expected 1 indexed artifact, got %d", len(artifacts))
		}
	}

	if !foundModule {
		t.Fatal("agentskills module not found")
	}
}

func TestTransformRecord_AgentSkill_NilSkill(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	_, err := tr.TransformRecord(types.SourceItem{Kind: types.SourceKindAgentSkill, Skill: nil})
	if err == nil {
		t.Fatal("expected error for nil skill")
	}

	if !strings.Contains(err.Error(), "skill") {
		t.Errorf("error should mention skill, got %q", err.Error())
	}
}

func TestTransformRecord_AgentSkill_ConversionError_UnknownName(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	// A non-nil but empty skill struct passes the nil guard, then fails
	// conversion (no skillMarkdown). With no derivable name the error must fall
	// back to the "(unknown)" placeholder rather than emitting "@v1.0.0".
	empty, err := structpb.NewStruct(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tr.TransformRecord(types.AgentSkillSourceItem(empty))
	if err == nil {
		t.Fatal("expected conversion error for empty agent skill struct")
	}

	if !strings.Contains(err.Error(), "agent skill") {
		t.Errorf("error should mention agent skill, got %q", err.Error())
	}

	if !strings.Contains(err.Error(), unknownNameVersion) {
		t.Errorf("error should use %q placeholder for underivable name, got %q", unknownNameVersion, err.Error())
	}
}

func TestTransformRecord_OASF_Success_Passthrough(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	// Produce a genuinely valid, decodable OASF record via the existing A2A
	// conversion path, then feed its Data back in as a SourceKindOASF item —
	// this is exactly the --dry-run-output re-import scenario the OASF import
	// type exists for.
	seed, err := tr.TransformRecord(types.A2ASourceItem(validA2ACard(t)))
	if err != nil {
		t.Fatalf("seed TransformRecord: %v", err)
	}

	rec, err := tr.TransformRecord(types.OASFSourceItem(seed.GetData()))
	if err != nil {
		t.Fatalf("TransformRecord: %v", err)
	}

	if rec == nil || rec.GetData() == nil {
		t.Fatal("returned record has no data")
	}

	// Passthrough means the Data struct itself is carried through unchanged,
	// not rebuilt.
	if rec.GetData() != seed.GetData() {
		t.Error("OASF passthrough should carry the input struct through unchanged")
	}
}

func TestTransformRecord_OASF_NilRecord(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	_, err := tr.TransformRecord(types.SourceItem{Kind: types.SourceKindOASF, OASF: nil})
	if err == nil {
		t.Fatal("expected error for nil OASF record")
	}

	if !strings.Contains(err.Error(), "OASF") {
		t.Errorf("error should mention OASF, got %q", err.Error())
	}
}

func TestTransformRecord_OASF_InvalidRecord_FailsValidation(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	// No schema_version, no recognizable OASF shape: Decode() must reject it
	// rather than silently passing malformed data downstream.
	garbage, err := structpb.NewStruct(map[string]any{"not_a_real_field": "x"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tr.TransformRecord(types.OASFSourceItem(garbage))
	if err == nil {
		t.Fatal("expected validation error for malformed OASF record")
	}

	if !strings.Contains(err.Error(), "invalid OASF record") {
		t.Errorf("error should mention invalid OASF record, got %q", err.Error())
	}
}

func TestTransformRecord_UnknownKind(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	// A SourceKind beyond the defined enum range should be rejected with a
	// clear error rather than silently producing nil records.
	_, err := tr.TransformRecord(types.SourceItem{Kind: types.SourceKind(999)})
	if err == nil {
		t.Fatal("expected error for unknown source kind")
	}

	if !strings.Contains(err.Error(), "unknown source kind") {
		t.Errorf("error should mention unknown source kind, got %q", err.Error())
	}
}

// --- Transform pipeline-stage ---

func TestTransform_PipelineHappyPath(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	in := make(chan types.SourceItem, 3)
	in <- types.MCPSourceItem(validMCP())

	in <- types.A2ASourceItem(validA2ACard(t))

	in <- types.AgentSkillSourceItem(validAgentSkill(t))

	close(in)

	result := &types.Result{}
	out, errCh := tr.Transform(context.Background(), in, result)

	go func() {
		for err := range errCh {
			t.Errorf("unexpected pipeline error: %v", err)
		}
	}()

	var count int
	for range out {
		count++
	}

	if count != 3 {
		t.Errorf("got %d output records, want 3", count)
	}

	if result.TotalRecords != 3 {
		t.Errorf("result.TotalRecords = %d, want 3", result.TotalRecords)
	}

	if result.FailedCount != 0 {
		t.Errorf("result.FailedCount = %d, want 0", result.FailedCount)
	}
}

func TestTransform_PipelineMixedSuccessAndFailure(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	in := make(chan types.SourceItem, 2)
	in <- types.MCPSourceItem(validMCP()) // ok

	in <- types.MCPSourceItem(mcpapiv0.ServerResponse{Server: mcpapiv0.ServerJSON{Name: "bad"}}) // bad

	close(in)

	result := &types.Result{}
	out, errCh := tr.Transform(context.Background(), in, result)

	type counts struct {
		recs int
		errs int
	}

	done := make(chan counts, 1)

	go func() {
		var c counts

		for {
			select {
			case _, ok := <-out:
				if !ok {
					out = nil
				} else {
					c.recs++
				}
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				} else {
					c.errs++
				}
			}

			if out == nil && errCh == nil {
				break
			}
		}

		done <- c
	}()

	got := <-done
	if got.recs != 1 {
		t.Errorf("recs = %d, want 1", got.recs)
	}

	if got.errs != 1 {
		t.Errorf("errs = %d, want 1", got.errs)
	}

	if result.TotalRecords != 2 {
		t.Errorf("result.TotalRecords = %d, want 2", result.TotalRecords)
	}

	if result.FailedCount != 1 {
		t.Errorf("result.FailedCount = %d, want 1", result.FailedCount)
	}
}

func TestTransform_ContextCancellation(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(false, nil, "")

	in := make(chan types.SourceItem) // unbuffered: producer blocks until cancellation
	ctx, cancel := context.WithCancel(context.Background())

	out, errCh := tr.Transform(ctx, in, &types.Result{})

	cancel()

	for out != nil || errCh != nil {
		select {
		case _, ok := <-out:
			if !ok {
				out = nil
			}
		case _, ok := <-errCh:
			if !ok {
				errCh = nil
			}
		}
	}
}

// --- Fixtures ---

func validMCP() mcpapiv0.ServerResponse {
	return mcpapiv0.ServerResponse{
		Server: mcpapiv0.ServerJSON{
			Schema:      "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
			Name:        fixtureName,
			Version:     fixtureVersion,
			Description: "transformer test fixture",
			Title:       "Transformer Test",
			Remotes: []mcpmodel.Transport{
				{Type: "streamable-http", URL: "https://example.invalid/mcp"},
			},
		},
	}
}

func validA2ACard(t *testing.T) *structpb.Struct {
	t.Helper()

	card, err := structpb.NewStruct(map[string]any{
		fieldName:          fixtureName,
		fieldVersion:       fixtureVersion,
		fieldDescription:   fixtureDescription,
		"documentationUrl": "https://example.invalid/agents",
		"capabilities": map[string]any{
			"streaming":         true,
			"pushNotifications": false,
			"extendedAgentCard": false,
		},
		"defaultInputModes":  []any{"text/plain"},
		"defaultOutputModes": []any{"text/plain"},
		"skills": []any{
			map[string]any{
				"id":             "test.skill",
				fieldName:        "Test skill",
				fieldDescription: "fixture skill",
				"tags":           []any{"test"},
			},
		},
		"provider": map[string]any{
			"organization": "Test Org",
			"url":          "https://example.invalid",
		},
		"supportedInterfaces": []any{
			map[string]any{
				"url":             "https://example.invalid/a2a",
				"protocolBinding": "HTTP+JSON",
				"protocolVersion": "0.3",
			},
		},
	})
	if err != nil {
		t.Fatalf("validA2ACard: %v", err)
	}

	return card
}

func validAgentSkill(t *testing.T) *structpb.Struct {
	t.Helper()

	// SkillMarkdownToRecord requires the raw markdown source as the
	// skillMarkdown field; everything else is normalisation metadata.
	const md = "---\nname: test-skill\ndescription: transformer fixture skill\nversion: 0.1.0\n---\n\n## Steps\n\n1. Do nothing.\n"

	skill, err := structpb.NewStruct(map[string]any{
		"skillMarkdown":  md,
		fieldName:        "test-skill",
		fieldDescription: "transformer fixture skill",
		"metadata":       map[string]any{fieldVersion: "0.1.0"},
	})
	if err != nil {
		t.Fatalf("validAgentSkill: %v", err)
	}

	return skill
}

func validAgentSkillBundle(t *testing.T) *structpb.Struct {
	t.Helper()

	const md = "---\nname: bundled-skill\ndescription: Bundled transformer fixture.\nmetadata:\n  author: example-org\n  version: \"1.0\"\n---\n\n# Bundled\n"

	archive := buildTestSkillBundleArchive(t, map[string]string{
		"SKILL.md":            md,
		"references/extra.md": "# Extra\n",
	})

	skill, err := structpb.NewStruct(map[string]any{
		"skillMarkdown":  md,
		"skillArchive":   base64.StdEncoding.EncodeToString(archive),
		fieldName:        "bundled-skill",
		fieldDescription: "Bundled transformer fixture.",
		"metadata":       map[string]any{fieldVersion: "1.0", "author": "example-org"},
	})
	if err != nil {
		t.Fatalf("validAgentSkillBundle: %v", err)
	}

	return skill
}

func buildTestSkillBundleArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(content)),
		}

		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header for %q: %v", name, err)
		}

		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar payload for %q: %v", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	return buf.Bytes()
}
