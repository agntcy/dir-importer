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

// importTimeout is the wall-clock budget for a single Importer.Run. With
// Azure OpenAI doing the inference, each record finishes in well under a
// minute; the per-record cap (enricher.enrichmentTimeout) is still the
// authoritative limit, this exists just so a hung spec doesn't block the
// suite forever.
const importTimeout = 10 * time.Minute

var harness *integration.Harness

func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration suite needs Docker and Azure OpenAI credentials; skipped under -short")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "Importer Integration Suite")
}

var _ = BeforeSuite(func() {
	h, err := integration.Setup()
	Expect(err).NotTo(HaveOccurred(), "harness bootstrap failed")

	harness = h
})
