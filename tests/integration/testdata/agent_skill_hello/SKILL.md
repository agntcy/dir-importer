---
name: hello-world
description: >-
  Integration test fixture for the agent-skill importer path. Mirrors the
  layout from the Agent Skills spec (https://agentskills.io/specification):
  a directory whose name matches `name` in the YAML frontmatter, plus this
  SKILL.md file.
license: Apache-2.0
compatibility: Local development; no special runtime requirements for this fixture.
metadata:
  version: "0.1.0"
  author: agntcy-integration-tests
allowed-tools: Read Bash(echo:*)
---

## What this skill does

Fixture content only. The integration suite imports this directory via
`config.ImportTypeAgentSkill` to exercise the SKILL.md parser, the
`translator.SkillMarkdownToRecord` transform, and the push path against a
live DIR stack.

## Steps

1. Fetcher reads `SKILL.md`, splits frontmatter, and emits a structpb payload.
2. Transformer hands it to oasf-sdk's `SkillMarkdownToRecord`.
3. Enricher annotates with skills/domains.
4. Pusher writes the resulting record to DIR and returns the CID.
