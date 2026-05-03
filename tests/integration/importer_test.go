// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

// Specs in this file exercise the importer library against real downstream
// services (DIR apiserver + zot + postgres + Azure OpenAI + dirctl mcp serve).
// Run the suite via `task test:integration`, which manages the docker stack and
// requires AZURE_OPENAI_* env vars to be exported.
package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/agntcy/dir-importer/config"
	enricherconfig "github.com/agntcy/dir-importer/enricher/config"
	"github.com/agntcy/dir-importer/factory"
	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Importer", func() {
	// newConfig assembles a full importer config for the given import type and source path.
	// Each spec gets a fresh Config so they can mutate Force/DryRun/etc. without bleeding
	// into neighbours.
	newConfig := func(importType config.ImportType, source string) config.Config {
		return config.Config{
			Type:     importType,
			FilePath: source,
			Enricher: enricherconfig.Config{
				ConfigFile:        harness.EnricherConfigPath(),
				RequestsPerMinute: 600,
			},
			Scanner: scannerconfig.Config{Enabled: false},
		}
	}

	mcpConfig := func(fixture string) config.Config {
		return newConfig(config.ImportTypeMCP, fixture)
	}

	fixturePath := func(name string) string {
		wd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		return filepath.Join(wd, "testdata", name)
	}

	// Smoke test for the full pipeline. One well-formed MCP server -> one CID in DIR.
	It("imports a single well-formed MCP record end-to-end", func() {
		cfg := mcpConfig(fixturePath("mcp_basic.json"))

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.Run(ctx)

		Expect(res.FailedCount).To(BeZero(), "errors: %v", res.Errors)
		Expect(res.ImportedCount).To(Equal(1), "errors: %v", res.Errors)
		Expect(res.ImportedCIDs).To(HaveLen(1))
		Expect(res.ImportedCIDs[0]).NotTo(BeEmpty())
	})

	// Re-import: dedup must filter previously-pushed records on the second run. This
	// exercises the SearchCIDs + PullBatch path in dedup.go against the live
	// postgres-backed search index.
	It("filters duplicates on re-import via the dedup stage", func() {
		fixture := fixturePath("mcp_pair.json")

		By("first import: both records new")
		{
			cfg := mcpConfig(fixture)

			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()

			imp, err := factory.Create(ctx, harness.Client(), cfg)
			Expect(err).NotTo(HaveOccurred())

			res := imp.Run(ctx)
			Expect(res.ImportedCount).To(Equal(2), "first run errors: %v", res.Errors)
		}

		By("second import of the same fixture: dedup must filter both")

		cfg := mcpConfig(fixture)

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.Run(ctx)

		Expect(res.ImportedCount).To(BeZero(), "imported CIDs on re-import: %v", res.ImportedCIDs)
		Expect(res.SkippedCount).To(Equal(2))
	})

	// cfg.Force=true bypasses the dedup stage. The DIR server is content-addressable, so
	// re-pushing returns the same CID without erroring.
	It("re-pushes records when Force=true bypasses dedup", func() {
		fixture := fixturePath("mcp_basic.json")

		By("seed run to ensure the record exists")
		{
			cfg := mcpConfig(fixture)

			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()

			imp, err := factory.Create(ctx, harness.Client(), cfg)
			Expect(err).NotTo(HaveOccurred())

			_ = imp.Run(ctx)
		}

		By("forced re-import bypasses dedup")

		cfg := mcpConfig(fixture)
		cfg.Force = true

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.Run(ctx)
		Expect(res.ImportedCount).To(Equal(1), "errors: %v", res.Errors)
		Expect(res.SkippedCount).To(BeZero(), "Force should bypass dedup, no skips expected")
	})

	// Regression test for the historical "first error returns from goroutine" class of
	// bugs across pipeline stages. With one invalid record next to one valid record, the
	// pipeline must continue and import the valid one.
	It("does not abort the run when a single record fails to transform", func() {
		fixture := writeMixedFixture()

		cfg := mcpConfig(fixture)

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.Run(ctx)

		Expect(res.ImportedCount).To(Equal(1), "expected the valid record to import; errors: %v", res.Errors)
		Expect(errorsContain(res.Errors, "no packages or remotes")).
			To(BeTrue(), "expected a transform error mentioning 'no packages or remotes', got: %v", res.Errors)
	})

	// DryRun writes records to a JSONL file and never pushes. ImportedCount stays at 0
	// (DryRun never increments it) and the OutputFile must exist + be non-empty.
	// Force=true bypasses dedup so this spec is order-independent: prior specs in the
	// suite seed mcp_basic.json into the live apiserver, and without Force the dedup
	// stage would silently filter the only record before it reaches the file writer.
	It("writes records to a JSONL file and skips push on DryRun", func() {
		cfg := mcpConfig(fixturePath("mcp_basic.json"))
		cfg.DryRun = true
		cfg.Force = true

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.DryRun(ctx)

		DeferCleanup(func() {
			if res.OutputFile != "" {
				_ = os.Remove(res.OutputFile)
			}
		})

		Expect(res.OutputFile).NotTo(BeEmpty())

		info, err := os.Stat(res.OutputFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Size()).NotTo(BeZero(), "DryRun output file is empty")
	})

	// Single A2A AgentCard end-to-end. Exercises the a2a fetcher + transformer's A2AToRecord
	// path against the live OASF translator.
	It("imports a single A2A AgentCard end-to-end", func() {
		cfg := newConfig(config.ImportTypeA2A, fixturePath("a2a_basic.json"))

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.Run(ctx)

		Expect(res.FailedCount).To(BeZero(), "errors: %v", res.Errors)
		Expect(res.ImportedCount).To(Equal(1), "errors: %v", res.Errors)
		Expect(res.ImportedCIDs).To(HaveLen(1))
		Expect(res.ImportedCIDs[0]).NotTo(BeEmpty())
	})

	// Agent Skill directory end-to-end. Exercises skill.ParseSkillDirectory and the
	// transformer's SkillMarkdownToRecord path. The fixture is a directory, not a file.
	It("imports an Agent Skill directory end-to-end", func() {
		cfg := newConfig(config.ImportTypeAgentSkill, fixturePath("agent_skill_hello"))

		ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
		defer cancel()

		imp, err := factory.Create(ctx, harness.Client(), cfg)
		Expect(err).NotTo(HaveOccurred())

		res := imp.Run(ctx)

		Expect(res.FailedCount).To(BeZero(), "errors: %v", res.Errors)
		Expect(res.ImportedCount).To(Equal(1), "errors: %v", res.Errors)
		Expect(res.ImportedCIDs).To(HaveLen(1))
		Expect(res.ImportedCIDs[0]).NotTo(BeEmpty())
	})
})

// writeMixedFixture composes a fixture with one invalid + one valid record on the fly.
// Done in the spec rather than as a static file so the test stays self-documenting.
func writeMixedFixture() string {
	GinkgoHelper()

	const body = `[
  {
    "server": {
      "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
      "name": "io.github.agntcy/integration-test-mixed-invalid",
      "description": "Has neither packages nor remotes; the transformer must reject this record.",
      "title": "Mixed Invalid",
      "version": "1.0.0"
    }
  },
  {
    "server": {
      "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
      "name": "io.github.agntcy/integration-test-mixed-valid",
      "description": "Valid record that must still be imported despite its sibling failing.",
      "title": "Mixed Valid",
      "version": "1.0.0",
      "remotes": [{"type": "streamable-http", "url": "https://example.invalid/mixed"}]
    }
  }
]`

	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "mixed.json")

	Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

	return path
}

func errorsContain(errs []error, substr string) bool {
	for _, err := range errs {
		if err == nil {
			continue
		}

		if strings.Contains(err.Error(), substr) {
			return true
		}
	}

	return false
}
