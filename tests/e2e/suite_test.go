// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e_test

import (
	"testing"
	"time"

	"github.com/agntcy/dir-importer/tests/e2e"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const importTimeout = 10 * time.Minute

var harness *e2e.Harness

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Importer E2E Suite")
}

var _ = BeforeSuite(func() {
	h, err := e2e.Setup()
	Expect(err).NotTo(HaveOccurred(), "harness bootstrap failed")

	harness = h
})
