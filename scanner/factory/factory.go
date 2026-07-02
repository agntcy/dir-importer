// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package factory

import (
	"fmt"
	"sort"
	"sync"

	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
	dirscanner "github.com/agntcy/dir/utils/scanner"
)

// RunnerFactory creates a Runner from the shared scanner config.
type RunnerFactory func(cfg scannerconfig.Config) dirscanner.Runner

var (
	registry = make(map[string]RunnerFactory)
	mu       sync.RWMutex
)

// Register registers a RunnerFactory under the given name.
// It panics if the same name is registered twice to prevent duplications at compile-time.
// Runner implementations should call this from their init() function.
func Register(name string, factory RunnerFactory) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("scanner: already registered for name: %s", name))
	}

	registry[name] = factory
}

// NewRunners creates Runner instances for all registered runners.
func NewRunners(cfg scannerconfig.Config) ([]dirscanner.Runner, error) {
	mu.RLock()
	defer mu.RUnlock()

	runners := make([]dirscanner.Runner, 0, len(registry))

	for _, name := range sortedRegistryNames() {
		runners = append(runners, registry[name](cfg))
	}

	return runners, nil
}

func sortedRegistryNames() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
