// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package toolhost

import "testing"

func TestNewOpenAIClientFromModel_AzureSeparators(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "test-key")
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://example.openai.azure.com")

	for _, in := range []string{"azure:gpt-5", "azure/gpt-5"} {
		c, err := newOpenAIClientFromModel(in)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", in, err)
		}

		if got := c.Model(); got != "gpt-5" {
			t.Errorf("%s: Model() = %q, want %q", in, got, "gpt-5")
		}
	}
}

func TestNewOpenAIClientFromModel_AzureDeploymentEnv(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "test-key")
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://example.openai.azure.com")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "my-deployment")

	c, err := newOpenAIClientFromModel("azure/gpt-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := c.Model(); got != "my-deployment" {
		t.Errorf("Model() = %q, want %q", got, "my-deployment")
	}
}
