// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"github.com/agntcy/oasf-sdk/pkg/translator"
	"google.golang.org/protobuf/types/known/structpb"
)

const testSkillName = "skill-1"

// fakeExtractor records the last text it received and returns canned results
// (or a canned error). It stands in for the oasf-sdk extractor in tests.
type fakeExtractor struct {
	gotText string
	result  ExtractResult
	err     error
}

func (f *fakeExtractor) Extract(_ context.Context, text string) (ExtractResult, error) {
	f.gotText = text
	if f.err != nil {
		return ExtractResult{}, f.err
	}

	return f.result, nil
}

// mcpRecord builds a minimal MCP/A2A-style record (top-level name+description,
// no agentskills module).
func mcpRecord(name, description string) *corev1.Record {
	return &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{
		"name":        structpb.NewStringValue(name),
		"description": structpb.NewStringValue(description),
	}}}
}

func TestNewExtractorEnricher_NilExtractor(t *testing.T) {
	t.Parallel()

	if _, err := NewExtractorEnricher(nil); err == nil {
		t.Fatal("expected error for nil extractor")
	}
}

func TestExtractorEnricher_WritesSkillsAndDomains(t *testing.T) {
	t.Parallel()

	fake := &fakeExtractor{result: ExtractResult{
		Skills:  []TaxonomyClass{{Name: testSkillName, ID: 100}},
		Domains: []TaxonomyClass{{Name: "domain-1", ID: 200}},
	}}

	ee, err := NewExtractorEnricher(fake)
	if err != nil {
		t.Fatalf("NewExtractorEnricher: %v", err)
	}

	in := make(chan *corev1.Record, 1)
	in <- mcpRecord("agent-1", "does things")

	close(in)

	result := &types.Result{}
	out, errCh := ee.Enrich(context.Background(), in, result)

	got := drainOne(t, out, errCh)

	skills := got.GetData().GetFields()["skills"].GetListValue()
	if len(skills.GetValues()) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills.GetValues()))
	}

	first := skills.GetValues()[0].GetStructValue().GetFields()
	if first["name"].GetStringValue() != testSkillName || first["id"].GetNumberValue() != 100 {
		t.Errorf("unexpected skill: %v", first)
	}

	domains := got.GetData().GetFields()["domains"].GetListValue()
	if len(domains.GetValues()) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains.GetValues()))
	}
}

func TestExtractorEnricher_MCPUsesNameAndDescription(t *testing.T) {
	t.Parallel()

	fake := &fakeExtractor{result: ExtractResult{
		Skills: []TaxonomyClass{{Name: "s", ID: 1}},
	}}

	ee, _ := NewExtractorEnricher(fake)

	in := make(chan *corev1.Record, 1)
	in <- mcpRecord("agent-name", "agent description")

	close(in)

	out, errCh := ee.Enrich(context.Background(), in, &types.Result{})
	drainOne(t, out, errCh)

	if fake.gotText != "agent-name\nagent description" {
		t.Errorf("expected name+description text, got %q", fake.gotText)
	}
}

func TestExtractorEnricher_ExtractError(t *testing.T) {
	t.Parallel()

	fake := &fakeExtractor{err: errors.New("boom")}
	ee, _ := NewExtractorEnricher(fake)

	in := make(chan *corev1.Record, 1)
	in <- mcpRecord("agent-1", "desc")

	close(in)

	result := &types.Result{}
	out, errCh := ee.Enrich(context.Background(), in, result)

	assertFails(t, out, errCh)

	result.Mu.Lock()
	defer result.Mu.Unlock()

	if result.FailedCount != 1 {
		t.Errorf("expected FailedCount 1, got %d", result.FailedCount)
	}
}

func TestExtractorEnricher_EmptyTextFails(t *testing.T) {
	t.Parallel()

	fake := &fakeExtractor{}
	ee, _ := NewExtractorEnricher(fake)

	in := make(chan *corev1.Record, 1)
	in <- mcpRecord("", "") // no text at all

	close(in)

	result := &types.Result{}
	out, errCh := ee.Enrich(context.Background(), in, result)

	assertFails(t, out, errCh)

	result.Mu.Lock()
	defer result.Mu.Unlock()

	if result.FailedCount != 1 {
		t.Errorf("expected FailedCount 1, got %d", result.FailedCount)
	}
}

func TestExtractorEnricher_AgentSkillUsesSkillMarkdown(t *testing.T) {
	t.Parallel()

	skillMD := "---\nname: my-skill\ndescription: greets people\n---\n\n# Body\n\nDetailed instructions."

	data, err := translator.SkillMarkdownToRecord(
		&structpb.Struct{Fields: map[string]*structpb.Value{
			"skillMarkdown": structpb.NewStringValue(skillMD),
		}},
		translator.WithVersion("1.0.0"),
	)
	if err != nil {
		t.Fatalf("SkillMarkdownToRecord: %v", err)
	}

	fake := &fakeExtractor{result: ExtractResult{Skills: []TaxonomyClass{{Name: "s", ID: 1}}}}
	ee, _ := NewExtractorEnricher(fake)

	in := make(chan *corev1.Record, 1)
	in <- &corev1.Record{Data: data}

	close(in)

	out, errCh := ee.Enrich(context.Background(), in, &types.Result{})
	drainOne(t, out, errCh)

	if !strings.Contains(fake.gotText, "greets people") || !strings.Contains(fake.gotText, "Detailed instructions") {
		t.Errorf("expected full SKILL.md text, got %q", fake.gotText)
	}
}

// drainOne waits for a single enriched record, failing on error or timeout.
func drainOne(t *testing.T, out <-chan *corev1.Record, errCh <-chan error) *corev1.Record {
	t.Helper()

	for {
		select {
		case rec, ok := <-out:
			if ok {
				return rec
			}

			out = nil
		case err, ok := <-errCh:
			if ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			errCh = nil
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for enriched record")
		}

		if out == nil && errCh == nil {
			t.Fatal("channels closed without a record")
		}
	}
}

// assertFails expects an error on errCh and no record on out.
func assertFails(t *testing.T, out <-chan *corev1.Record, errCh <-chan error) {
	t.Helper()

	for {
		select {
		case rec, ok := <-out:
			if ok && rec != nil {
				t.Fatalf("expected no record, got one")
			}

			out = nil
		case err, ok := <-errCh:
			if ok && err != nil {
				return // expected
			}

			errCh = nil
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for error")
		}

		if out == nil && errCh == nil {
			t.Fatal("channels closed without an error")
		}
	}
}
