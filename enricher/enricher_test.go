// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"strings"
	"testing"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	fieldName   = "name"
	testDomainA = "domain-a"
)

func TestParseSkillsJSON_HappyPath(t *testing.T) {
	t.Parallel()

	got, err := parseSkillsJSON(`{"skills":[
		{"name":"a","id":1,"confidence":0.9,"reasoning":"because"},
		{"name":"b","id":2,"confidence":0.4}
	]}`)
	if err != nil {
		t.Fatalf("parseSkillsJSON: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	if got[0].Name != "a" || got[0].ID != 1 || got[0].Confidence != 0.9 || got[0].Reasoning != "because" {
		t.Errorf("got[0] = %+v", got[0])
	}

	if got[1].Reasoning != "" {
		t.Errorf("got[1].Reasoning should default to empty, got %q", got[1].Reasoning)
	}
}

func TestParseSkillsJSON_TrimsWhitespace(t *testing.T) {
	t.Parallel()

	// LLM responses sometimes come back with leading/trailing whitespace; the
	// parser is required to strip it via TrimSpace before unmarshalling.
	got, err := parseSkillsJSON("\n\t  " + `{"skills":[{"name":"x","id":1,"confidence":0.5}]}` + "\n  ")
	if err != nil {
		t.Fatalf("parseSkillsJSON: %v", err)
	}

	if len(got) != 1 || got[0].Name != "x" {
		t.Errorf("got = %+v", got)
	}
}

func TestParseSkillsJSON_EmptyArray(t *testing.T) {
	t.Parallel()

	got, err := parseSkillsJSON(`{"skills":[]}`)
	if err != nil {
		t.Fatalf("parseSkillsJSON: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("want empty slice, got %+v", got)
	}
}

func TestParseSkillsJSON_MissingKey(t *testing.T) {
	t.Parallel()

	// "domains" key with no "skills" key should still unmarshal cleanly into
	// the zero-value Skills slice; Go's encoding/json silently ignores
	// unknown fields by default.
	got, err := parseSkillsJSON(`{"domains":[{"name":"x"}]}`)
	if err != nil {
		t.Fatalf("parseSkillsJSON: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("want empty Skills, got %+v", got)
	}
}

func TestParseSkillsJSON_Malformed(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"not json",
		`{"skills":`,
		`{"skills":"oops"}`,
		`{"skills":[{"name":1}]}`, // Name is a string, not int
	}

	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := parseSkillsJSON(in)
			if err == nil {
				t.Fatal("expected error for malformed JSON")
			}

			if !strings.Contains(err.Error(), "json") {
				t.Errorf("error should reference json, got %q", err.Error())
			}
		})
	}
}

func TestParseDomainsJSON_HappyPath(t *testing.T) {
	t.Parallel()

	got, err := parseDomainsJSON(`{"domains":[
		{"name":"d1","id":10,"confidence":0.7},
		{"name":"d2","id":20,"confidence":0.3,"reasoning":"low"}
	]}`)
	if err != nil {
		t.Fatalf("parseDomainsJSON: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	if got[1].Reasoning != "low" {
		t.Errorf("got[1].Reasoning = %q", got[1].Reasoning)
	}
}

func TestParseDomainsJSON_Malformed(t *testing.T) {
	t.Parallel()

	_, err := parseDomainsJSON("[]") // top-level array, not the expected object
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSetStructSkills_HappyPath(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	skills := []*typesv1.Skill{
		{Name: "skill-1", Id: 100},
		{Name: "skill-2", Id: 200},
	}

	if err := setStructSkills(s, skills); err != nil {
		t.Fatalf("setStructSkills: %v", err)
	}

	got := s.GetFields()["skills"].GetListValue()
	if got == nil {
		t.Fatal("skills field missing or wrong type")
	}

	if len(got.GetValues()) != 2 {
		t.Fatalf("values len = %d, want 2", len(got.GetValues()))
	}

	first := got.GetValues()[0].GetStructValue().GetFields()
	if first["name"].GetStringValue() != "skill-1" || first["id"].GetNumberValue() != 100 {
		t.Errorf("first = %+v", first)
	}
}

func TestSetStructSkills_OmitsEmptyName(t *testing.T) {
	t.Parallel()

	// An empty Name should not be written into the resulting struct entry —
	// otherwise downstream consumers see a zero-value field where they'd
	// expect the key to be absent.
	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}

	if err := setStructSkills(s, []*typesv1.Skill{{Name: "", Id: 7}}); err != nil {
		t.Fatalf("setStructSkills: %v", err)
	}

	entry := s.GetFields()["skills"].GetListValue().GetValues()[0].GetStructValue().GetFields()
	if _, ok := entry["name"]; ok {
		t.Errorf("name key should be absent for empty name, got %+v", entry)
	}

	if entry["id"].GetNumberValue() != 7 {
		t.Errorf("id should still be written, got %+v", entry)
	}
}

func TestSetStructSkills_OmitsZeroID(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}

	if err := setStructSkills(s, []*typesv1.Skill{{Name: "named", Id: 0}}); err != nil {
		t.Fatalf("setStructSkills: %v", err)
	}

	entry := s.GetFields()["skills"].GetListValue().GetValues()[0].GetStructValue().GetFields()
	if _, ok := entry["id"]; ok {
		t.Errorf("id key should be absent for zero id, got %+v", entry)
	}

	if entry["name"].GetStringValue() != "named" {
		t.Errorf("name should still be written, got %+v", entry)
	}
}

func TestSetStructSkills_NilFields(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: nil}

	err := setStructSkills(s, []*typesv1.Skill{{Name: "x", Id: 1}})
	if err == nil {
		t.Fatal("expected error for nil Fields")
	}
}

func TestSetStructSkills_EmptySlice(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: map[string]*structpb.Value{"existing": structpb.NewStringValue("keep")}}

	if err := setStructSkills(s, nil); err != nil {
		t.Fatalf("setStructSkills: %v", err)
	}

	if _, ok := s.GetFields()["skills"]; !ok {
		t.Error("skills key should be set even for empty input")
	}

	if s.GetFields()["skills"].GetListValue() == nil {
		t.Error("skills should be a list value")
	}

	if len(s.GetFields()["skills"].GetListValue().GetValues()) != 0 {
		t.Errorf("skills list should be empty, got %d values", len(s.GetFields()["skills"].GetListValue().GetValues()))
	}

	if s.GetFields()["existing"].GetStringValue() != "keep" {
		t.Error("existing fields should be preserved")
	}
}

func TestSetStructDomains_HappyPath(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	domains := []*typesv1.Domain{
		{Name: testDomainA, Id: 11},
		{Name: "domain-b", Id: 22},
	}

	if err := setStructDomains(s, domains); err != nil {
		t.Fatalf("setStructDomains: %v", err)
	}

	got := s.GetFields()["domains"].GetListValue()
	if got == nil || len(got.GetValues()) != 2 {
		t.Fatalf("unexpected domains: %+v", got)
	}

	if got.GetValues()[0].GetStructValue().GetFields()["name"].GetStringValue() != testDomainA {
		t.Errorf("first domain name = %+v", got.GetValues()[0])
	}
}

func TestSetStructDomains_NilFields(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: nil}

	err := setStructDomains(s, []*typesv1.Domain{{Name: "x", Id: 1}})
	if err == nil {
		t.Fatal("expected error for nil Fields")
	}
}

func TestSetStructDomains_OmitsZeroes(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}

	if err := setStructDomains(s, []*typesv1.Domain{{Name: "", Id: 0}}); err != nil {
		t.Fatalf("setStructDomains: %v", err)
	}

	entry := s.GetFields()["domains"].GetListValue().GetValues()[0].GetStructValue().GetFields()
	if len(entry) != 0 {
		t.Errorf("entry with zero name+id should be empty, got %+v", entry)
	}
}

func TestEnrichedField_JSONShape(t *testing.T) {
	t.Parallel()

	// EnrichedField JSON tags are the contract between the prompt schema and the
	// parser; renaming any tag silently breaks LLM-driven enrichment.
	got, err := parseSkillsJSON(`{"skills":[{"name":"n","id":42,"confidence":0.99,"reasoning":"r"}]}`)
	if err != nil {
		t.Fatalf("parseSkillsJSON: %v", err)
	}

	want := EnrichedField{Name: "n", ID: 42, Confidence: 0.99, Reasoning: "r"}
	if got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}
