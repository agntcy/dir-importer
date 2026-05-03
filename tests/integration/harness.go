// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

// Package integration exposes a shared dir client to every spec in this
// directory. All process lifecycle (docker compose, Ollama daemon, Ollama
// model) is owned by the `test:integration` Taskfile target; the test binary
// only runs once the stack is healthy and the model is on disk.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	importerclient "github.com/agntcy/dir/client"
)

const apiserverAddr = "127.0.0.1:8888"

// Harness exposes the shared dir client and the path to the static enricher
// config consumed by every spec.
type Harness struct {
	enricherCfg string
	dirClient   *importerclient.Client
}

var (
	sharedHarness     *Harness
	sharedHarnessErr  error //nolint:errname // not a sentinel; cached bootstrap result for sync.Once
	sharedHarnessOnce sync.Once
)

// Setup returns the package-wide Harness, building the dir client on first call.
// It assumes the docker compose stack is already up (Taskfile's responsibility).
func Setup() (*Harness, error) {
	sharedHarnessOnce.Do(func() {
		sharedHarness, sharedHarnessErr = bootstrap()
	})

	return sharedHarness, sharedHarnessErr
}

// Client returns the shared dir client; do not close it.
func (h *Harness) Client() *importerclient.Client { return h.dirClient }

// EnricherConfigPath returns the absolute path to the static enricher.json.
func (h *Harness) EnricherConfigPath() string { return h.enricherCfg }

func bootstrap() (*Harness, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}

	cli, err := importerclient.New(
		context.Background(),
		importerclient.WithConfig(&importerclient.Config{ServerAddress: apiserverAddr}),
	)
	if err != nil {
		return nil, fmt.Errorf("dir client.New: %w", err)
	}

	return &Harness{
		enricherCfg: filepath.Join(wd, "enricher.json"),
		dirClient:   cli,
	}, nil
}
