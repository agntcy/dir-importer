// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package dedup

import (
	"errors"
	"maps"

	"github.com/agntcy/dir-importer/shared"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// OASF field names written by the enrichment stage, which runs AFTER dedup in
// the pipeline. Exported for reuse in tests.
const (
	skillsField  = "skills"
	domainsField = "domains"
)

// createdAtField is a top-level field the OASF translator (oasf-sdk
// pkg/translator: mcp.go, a2a.go, agentskills.go) stamps onto every record it
// produces, using time.Now().UTC().Format(time.RFC3339) - a fresh wall-clock
// timestamp on every call, for every source kind. It carries no content
// identity (two calls transforming byte-identical input produce different
// created_at values whenever they straddle a second boundary), so it MUST be
// stripped before hashing. Leaving it in would mean the import-side hash
// (stamped at check time) almost never matches the directory-side hash
// (stamped whenever the record was originally imported), silently disabling
// duplicate detection entirely.
const createdAtField = "created_at"

// "schema_version" and "authors" are also top-level, translator-written
// fields, but - unlike createdAtField - they are operator-controlled config
// (--schema-version, --authors), not generated noise, so they are
// deliberately NOT added to stripFields: they are treated as real content.
// This is a conscious trade-off, not an oversight - see PR description for
// the consequence (re-importing the same source with different flags than
// the original import will not be recognized as a duplicate) and the case
// for revisiting it.

// stripFields lists top-level record-data fields that must be excluded from
// content-hash comparison:
//   - skillsField, domainsField: written by the enrichment stage. Stripping
//     them lets the pre-enrichment hash computed on the import side match the
//     hash of an already-enriched record pulled from the directory on the
//     cache-build side.
//   - createdAtField: a fresh timestamp on every translator call - see
//     createdAtField's doc comment.
//   - shared.MCPDebugSourceField, shared.A2ADebugSourceField: importer-only
//     debug fields the transformer attaches when running with --debug. They
//     are never part of OASF content and must not perturb the hash depending
//     on whether debug mode happens to be on.
var stripFields = []string{
	skillsField,
	domainsField,
	createdAtField,
	shared.MCPDebugSourceField,
	shared.A2ADebugSourceField,
}

// stripEnrichmentFields returns a shallow copy of data with the fields in
// stripFields removed. The input struct is not mutated.
func stripEnrichmentFields(data *structpb.Struct) *structpb.Struct {
	if data == nil || data.GetFields() == nil {
		return data
	}

	fields := data.GetFields()
	stripped := &structpb.Struct{Fields: make(map[string]*structpb.Value, len(fields))}

	maps.Copy(stripped.GetFields(), fields)

	for _, f := range stripFields {
		delete(stripped.GetFields(), f)
	}

	return stripped
}

// contentHash computes a stable content-identity hash for record data.
//
// It reuses corev1.Record.GetCid(), the same CIDv1/SHA2-256-over-canonical-
// JSON digest the directory itself uses to content-address records, so the
// hash produced here is directly comparable with the CID of anything already
// pushed to the directory. Fields that are not part of a record's content
// identity - enrichment-only fields (skills, domains), the translator's
// generated created_at timestamp, and importer debug fields - are stripped
// first; see stripFields for the full list and rationale for each.
func contentHash(data *structpb.Struct) (string, error) {
	if data == nil {
		return "", errors.New("record data is nil")
	}

	stripped := stripEnrichmentFields(data)

	hash := (&corev1.Record{Data: stripped}).GetCid()
	if hash == "" {
		return "", errors.New("failed to compute content hash for record")
	}

	return hash, nil
}
