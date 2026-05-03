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
