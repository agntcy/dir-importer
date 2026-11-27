// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package transformer

import (
	"fmt"
	"strings"
	"time"

	catalogv1 "github.com/agntcy/dir/api/catalog/v1"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

const catalogIdentifierAnnotation = "org.agntcy.dir.catalog.identifier"

// convertAICatalogToOASF builds an OASF record from one catalog entry.
// Leaf data is copied onto the matching module. A container entry becomes one
// record with one module per nested leaf. Entry URLs are not fetched.
func (t *Transformer) convertAICatalogToOASF(entry *catalogv1.CatalogEntry) (*corev1.Record, error) {
	if entry == nil {
		return nil, fmt.Errorf("AI Catalog source item missing entry")
	}

	name := strings.TrimSpace(entry.GetDisplayName())
	if name == "" {
		return nil, fmt.Errorf("catalog entry %q missing display name", entry.GetIdentifier())
	}

	modules, parsed, err := t.catalogModules(entry)
	if err != nil {
		return nil, fmt.Errorf("catalog entry %q: %w", name, err)
	}

	record, err := t.catalogRecord(name, entry, modules, parsed)
	if err != nil {
		return nil, fmt.Errorf("catalog entry %q: %w", name, err)
	}

	if _, err := record.Decode(); err != nil {
		return nil, fmt.Errorf("invalid OASF record %s: %w", recordLabel(name, entry.GetVersion()), err)
	}

	return record, nil
}

func (t *Transformer) catalogModules(entry *catalogv1.CatalogEntry) ([]any, parsedTags, error) {
	if entry.GetType() == catalogv1.CatalogMediaType {
		return t.containerModules(entry)
	}

	module, parsed, err := moduleFromLeaf(entry)
	if err != nil {
		return nil, parsedTags{}, err
	}

	return []any{module}, parsed, nil
}

func (t *Transformer) containerModules(entry *catalogv1.CatalogEntry) ([]any, parsedTags, error) {
	nested, err := nestedCatalog(entry)
	if err != nil {
		return nil, parsedTags{}, err
	}

	parsed := parseCatalogTags(entry.GetTags())
	modules := make([]any, 0, len(nested.GetEntries()))

	for _, child := range nested.GetEntries() {
		if child.GetType() == catalogv1.CatalogMediaType {
			return nil, parsedTags{}, fmt.Errorf("nested catalog entry %q is not a leaf", child.GetIdentifier())
		}

		module, childTags, err := moduleFromLeaf(child)
		if err != nil {
			return nil, parsedTags{}, err
		}

		parsed.skills = append(parsed.skills, childTags.skills...)
		parsed.domains = append(parsed.domains, childTags.domains...)

		if len(childTags.annotations) > 0 {
			module["annotations"] = stringMap(childTags.annotations)
		}

		modules = append(modules, module)
	}

	if len(modules) == 0 {
		return nil, parsedTags{}, fmt.Errorf("container entry has no modules")
	}

	return modules, parsed, nil
}

func moduleFromLeaf(entry *catalogv1.CatalogEntry) (map[string]any, parsedTags, error) {
	if strings.TrimSpace(entry.GetUrl()) != "" || entry.GetData() == nil {
		return nil, parsedTags{}, fmt.Errorf("entry %q has no inline data", entryLabel(entry))
	}

	moduleName, withArtifact, err := moduleForMediaType(entry.GetType())
	if err != nil {
		return nil, parsedTags{}, err
	}

	data := entry.GetData().GetStructValue()
	if data == nil {
		return nil, parsedTags{}, fmt.Errorf("entry %q data is not an object", entryLabel(entry))
	}

	module := map[string]any{
		"name": moduleName,
		"data": data.AsMap(),
	}
	if withArtifact {
		module["artifact"] = map[string]any{"media_type": entry.GetType()}
	}

	return module, parseCatalogTags(entry.GetTags()), nil
}

func moduleForMediaType(mediaType string) (string, bool, error) {
	switch mediaType {
	case catalogv1.ProtocolA2ACardJsonMediaType:
		return "integration/a2a", false, nil
	case catalogv1.ProtocolMCPCardJsonMediaType:
		return "integration/mcp", false, nil
	case catalogv1.ProtocolAgentSkillsMdMediaType, catalogv1.ProtocolAgentSkillsBundleMediaType:
		return catalogv1.AgentSkillsModuleName, true, nil
	default:
		return "", false, fmt.Errorf("unsupported catalog type %q", mediaType)
	}
}

func nestedCatalog(entry *catalogv1.CatalogEntry) (*catalogv1.AICatalog, error) {
	data := entry.GetData().GetStructValue()
	if data == nil {
		if strings.TrimSpace(entry.GetUrl()) != "" {
			return nil, fmt.Errorf("entry %q has no inline data", entryLabel(entry))
		}

		return nil, fmt.Errorf("entry %q data is not an object", entryLabel(entry))
	}

	raw, err := protojson.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("read nested catalog: %w", err)
	}

	var nested catalogv1.AICatalog
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &nested); err != nil {
		return nil, fmt.Errorf("decode nested catalog: %w", err)
	}

	if len(nested.GetEntries()) == 0 {
		return nil, fmt.Errorf("entry %q nested catalog has no entries", entryLabel(entry))
	}

	return &nested, nil
}

func (t *Transformer) catalogRecord(name string, entry *catalogv1.CatalogEntry, modules []any, parsed parsedTags) (*corev1.Record, error) {
	version := strings.TrimSpace(entry.GetVersion())
	if version == "" {
		version = defaultCatalogVersion
	}

	schemaVersion := parsed.schemaVersion
	if schemaVersion == "" {
		schemaVersion = t.schemaVersion
	}

	annotations := parsed.annotations
	if annotations == nil {
		annotations = map[string]string{}
	}

	if id := strings.TrimSpace(entry.GetIdentifier()); id != "" {
		if _, exists := annotations[catalogIdentifierAnnotation]; !exists {
			annotations[catalogIdentifierAnnotation] = id
		}
	}

	recordMap := map[string]any{
		"name":           name,
		"version":        version,
		"schema_version": schemaVersion,
		"modules":        modules,
	}

	if description := strings.TrimSpace(entry.GetDescription()); description != "" {
		recordMap["description"] = description
	}

	if createdAt := strings.TrimSpace(entry.GetUpdatedAt()); createdAt != "" {
		if _, err := time.Parse(time.RFC3339, createdAt); err == nil {
			recordMap["created_at"] = createdAt
		}
	}

	if len(t.authors) > 0 {
		recordMap["authors"] = stringSliceAny(t.authors)
	}

	if skills := taxonomyEntries(parsed.skills); len(skills) > 0 {
		recordMap["skills"] = skills
	}

	if domains := taxonomyEntries(parsed.domains); len(domains) > 0 {
		recordMap["domains"] = domains
	}

	if len(annotations) > 0 {
		recordMap["annotations"] = stringMap(annotations)
	}

	data, err := structpb.NewStruct(recordMap)
	if err != nil {
		return nil, fmt.Errorf("build OASF record: %w", err)
	}

	return &corev1.Record{Data: data}, nil
}

const defaultCatalogVersion = "v1.0.0"

type parsedTags struct {
	schemaVersion string
	skills        []string
	domains       []string
	annotations   map[string]string
}

func parseCatalogTags(tags []string) parsedTags {
	parsed := parsedTags{annotations: map[string]string{}}

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}

		if schema, kind, name, ok := parseOASFTag(tag); ok {
			if parsed.schemaVersion == "" {
				parsed.schemaVersion = schema
			}

			switch kind {
			case "skills":
				parsed.skills = append(parsed.skills, name)
			case "domains":
				parsed.domains = append(parsed.domains, name)
			}

			continue
		}

		key, value := splitAnnotation(tag)
		if key == "" {
			continue
		}

		if _, exists := parsed.annotations[key]; !exists {
			parsed.annotations[key] = value
		}
	}

	return parsed
}

func parseOASFTag(tag string) (string, string, string, bool) {
	parts := strings.SplitN(tag, ":", 4)
	if len(parts) != 4 || parts[0] != "oasf" || parts[3] == "" {
		return "", "", "", false
	}

	if parts[2] != "skills" && parts[2] != "domains" {
		return "", "", "", false
	}

	return parts[1], parts[2], parts[3], true
}

func splitAnnotation(tag string) (string, string) {
	key, value, _ := strings.Cut(tag, "=")

	return strings.TrimSpace(key), value
}

func taxonomyEntries(names []string) []any {
	out := make([]any, 0, len(names))
	seen := make(map[string]struct{}, len(names))

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}
		out = append(out, map[string]any{"name": name})
	}

	return out
}

func stringSliceAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}

	return out
}

func stringMap(values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}

	return out
}

func entryLabel(entry *catalogv1.CatalogEntry) string {
	if entry == nil {
		return ""
	}

	if id := strings.TrimSpace(entry.GetIdentifier()); id != "" {
		return id
	}

	return strings.TrimSpace(entry.GetDisplayName())
}

func recordLabel(name, version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		version = defaultCatalogVersion
	}

	return name + "@" + version
}
