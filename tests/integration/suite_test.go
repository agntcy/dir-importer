// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"testing"
	"time"

	"github.com/agntcy/dir-importer/tests/integration"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// importTimeout is the wall-clock budget for a single MCP file import (one record).
// LLM calls dominate this — qwen3:8b on CPU can take 30-60s per chat turn, and the
// enricher does up to MaxSteps turns per record.
const importTimeout = 5 * time.Minute

var harness *integration.Harness

func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration suite needs Docker + a ~5GB Ollama model; skipped under -short")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "Importer Integration Suite")
}

var _ = BeforeSuite(func() {
	h, err := integration.Setup()
	Expect(err).NotTo(HaveOccurred(), "harness bootstrap failed")

	harness = h
})

var _ = AfterSuite(func() {
	integration.Shutdown()
})
