// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"errors"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// This file holds the taxonomy-to-structpb encoding shared by all three
// enrichers (LLM, static, extractor) so their output is wire-identical.

func setStructSkills(s *structpb.Struct, skills []*typesv1.Skill) error {
	if s.Fields == nil {
		return errors.New("record struct has no fields")
	}

	s.Fields["skills"] = structpb.NewListValue(skillsToListValue(skills))

	return nil
}

func setStructDomains(s *structpb.Struct, domains []*typesv1.Domain) error {
	if s.Fields == nil {
		return errors.New("record struct has no fields")
	}

	s.Fields["domains"] = structpb.NewListValue(domainsToListValue(domains))

	return nil
}

// skillsToListValue / domainsToListValue centralize the name/id omission
// rules so every enrichment path produces wire-identical output. Name is
// omitted when empty, Id when zero; nil entries are tolerated via proto's
// nil-safe getters.
func skillsToListValue(skills []*typesv1.Skill) *structpb.ListValue {
	lv := &structpb.ListValue{Values: make([]*structpb.Value, 0, len(skills))}

	for _, sk := range skills {
		st := &structpb.Struct{Fields: map[string]*structpb.Value{}}
		if sk.GetName() != "" {
			st.Fields["name"] = structpb.NewStringValue(sk.GetName())
		}

		if sk.GetId() != 0 {
			st.Fields["id"] = structpb.NewNumberValue(float64(sk.GetId()))
		}

		lv.Values = append(lv.Values, structpb.NewStructValue(st))
	}

	return lv
}

func domainsToListValue(domains []*typesv1.Domain) *structpb.ListValue {
	lv := &structpb.ListValue{Values: make([]*structpb.Value, 0, len(domains))}

	for _, d := range domains {
		st := &structpb.Struct{Fields: map[string]*structpb.Value{}}
		if d.GetName() != "" {
			st.Fields["name"] = structpb.NewStringValue(d.GetName())
		}

		if d.GetId() != 0 {
			st.Fields["id"] = structpb.NewNumberValue(float64(d.GetId()))
		}

		lv.Values = append(lv.Values, structpb.NewStructValue(st))
	}

	return lv
}
