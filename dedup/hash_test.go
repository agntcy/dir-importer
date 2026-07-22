// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"testing"

	"github.com/agntcy/dir-importer/shared"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	testFieldName        = "name"
	testFieldVersion     = "version"
	testFieldDescription = "description"
)

func TestContentHash_NilData(t *testing.T) {
	t.Parallel()

	if _, err := contentHash(nil); err == nil {
		t.Fatal("expected error for nil data")
	}
}

func TestContentHash_DeterministicRegardlessOfFieldInsertionOrder(t *testing.T) {
	t.Parallel()

	a, err := structpb.NewStruct(map[string]any{testFieldName: "n", testFieldVersion: "v", testFieldDescription: "d"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	// structpb.Struct is backed by a Go map, so field order is already
	// randomized across runs; build a second struct via a differently
	// ordered literal to double-check that doesn't leak into the hash.
	b, err := structpb.NewStruct(map[string]any{testFieldDescription: "d", testFieldVersion: "v", testFieldName: "n"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	hashA, err := contentHash(a)
	if err != nil {
		t.Fatalf("contentHash(a): %v", err)
	}

	hashB, err := contentHash(b)
	if err != nil {
		t.Fatalf("contentHash(b): %v", err)
	}

	if hashA != hashB {
		t.Errorf("hashA = %q, hashB = %q; want equal (hash must not depend on field insertion order)", hashA, hashB)
	}
}

func TestContentHash_DiffersOnContentChange(t *testing.T) {
	t.Parallel()

	a, err := structpb.NewStruct(map[string]any{testFieldName: "n", testFieldVersion: "v", testFieldDescription: "original"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	b, err := structpb.NewStruct(map[string]any{testFieldName: "n", testFieldVersion: "v", testFieldDescription: "changed"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	hashA, err := contentHash(a)
	if err != nil {
		t.Fatalf("contentHash(a): %v", err)
	}

	hashB, err := contentHash(b)
	if err != nil {
		t.Fatalf("contentHash(b): %v", err)
	}

	if hashA == hashB {
		t.Error("expected different hashes for differing content, got equal")
	}
}

func TestContentHash_StripsSkillsAndDomains(t *testing.T) {
	t.Parallel()

	preEnrichment, err := structpb.NewStruct(map[string]any{testFieldName: "n", testFieldVersion: "v"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	postEnrichment, err := structpb.NewStruct(map[string]any{
		testFieldName:    "n",
		testFieldVersion: "v",
		skillsField:      []any{map[string]any{testFieldName: "skill-a"}},
		domainsField:     []any{map[string]any{testFieldName: "domain-a"}},
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	preHash, err := contentHash(preEnrichment)
	if err != nil {
		t.Fatalf("contentHash(preEnrichment): %v", err)
	}

	postHash, err := contentHash(postEnrichment)
	if err != nil {
		t.Fatalf("contentHash(postEnrichment): %v", err)
	}

	if preHash != postHash {
		t.Errorf("preHash = %q, postHash = %q; want equal (skills/domains must be stripped before hashing)", preHash, postHash)
	}
}

func TestContentHash_StripsImporterDebugFields(t *testing.T) {
	t.Parallel()

	clean, err := structpb.NewStruct(map[string]any{testFieldName: "n", testFieldVersion: "v"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	withDebug, err := structpb.NewStruct(map[string]any{
		testFieldName:              "n",
		testFieldVersion:           "v",
		shared.MCPDebugSourceField: `{"name":"n"}`,
		shared.A2ADebugSourceField: `{"name":"n"}`,
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	cleanHash, err := contentHash(clean)
	if err != nil {
		t.Fatalf("contentHash(clean): %v", err)
	}

	debugHash, err := contentHash(withDebug)
	if err != nil {
		t.Fatalf("contentHash(withDebug): %v", err)
	}

	if cleanHash != debugHash {
		t.Errorf("cleanHash = %q, debugHash = %q; want equal (importer debug fields must not affect the hash, e.g. across --debug runs)", cleanHash, debugHash)
	}
}

func TestStripEnrichmentFields_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	original, err := structpb.NewStruct(map[string]any{
		testFieldName: "n",
		skillsField:   []any{"x"},
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	_ = stripEnrichmentFields(original)

	if _, ok := original.GetFields()[skillsField]; !ok {
		t.Error("stripEnrichmentFields must not mutate its input, but \"skills\" was removed from the original struct")
	}
}

func TestStripEnrichmentFields_NilData(t *testing.T) {
	t.Parallel()

	if got := stripEnrichmentFields(nil); got != nil {
		t.Errorf("stripEnrichmentFields(nil) = %v, want nil", got)
	}
}
