// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

// Package integration provides a real-dependency test harness for the importer
// library. It brings up a DIR stack (apiserver + zot + postgres) via docker
// compose plus a local Ollama daemon, then exposes a configured importer client
// and a static enricher config to tests.
//
// The harness deliberately does not mock anything. Every component the importer
// touches in production has a live counterpart here:
//
//   - DIR client (Push/SearchCIDs/PullBatch)  ->  real apiserver + zot + postgres
//   - enricher LLM chat completion            ->  real Ollama (qwen3:8b)
//   - enricher MCP tool host                  ->  real `dirctl mcp serve` subprocess
//
// Binary lookup:
//   - ollama: $OLLAMA_BIN (set by the `deps:ollama` Taskfile task) or PATH.
//   - dirctl: $DIRCTL_BIN (set by the `deps:dirctl` Taskfile task) or PATH. The
//     dirctl directory is prepended to PATH at startup so the static enricher
//     config can refer to the binary as a bare "dirctl" command.
package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	importerclient "github.com/agntcy/dir/client"
)

const (
	apiserverAddr       = "127.0.0.1:8888"
	ollamaAddr          = "127.0.0.1:11434"
	stackComposeProject = "dir-importer-integration"
	ollamaModel         = "qwen3:8b"

	// First-time bootstrap on a fresh CI runner pulls the dir/zot/postgres images
	// (dockerStartTimeout) and the qwen3:8b weights (modelPullTimeout); hence the
	// generous budgets.
	dockerStartTimeout  = 10 * time.Minute
	ollamaStartTimeout  = 30 * time.Second
	ollamaShutdownGrace = 10 * time.Second
	modelPullTimeout    = 15 * time.Minute
)

// Harness owns the lifecycle of the docker compose stack and the Ollama daemon
// used by all integration tests in this package. Callers obtain the singleton
// via Setup().
type Harness struct {
	composeFile string
	enricherCfg string
	dirClient   *importerclient.Client

	// ollamaCmd is non-nil only when this harness started ollama itself. If we found
	// an existing daemon on the default port, ollamaCmd stays nil and Shutdown leaves
	// it alone.
	ollamaCmd *exec.Cmd
}

var (
	sharedHarness     *Harness
	sharedHarnessErr  error //nolint:errname // not a sentinel; cached bootstrap result for sync.Once
	sharedHarnessOnce sync.Once
)

// Setup returns the package-wide Harness, bringing the stack up on first call.
func Setup() (*Harness, error) {
	sharedHarnessOnce.Do(func() {
		sharedHarness, sharedHarnessErr = bootstrap()
	})

	return sharedHarness, sharedHarnessErr
}

// Client returns a DIR client connected to the in-stack apiserver. Tests should treat
// it as a shared resource and not close it.
func (h *Harness) Client() *importerclient.Client { return h.dirClient }

// EnricherConfigPath returns the on-disk path to the static enricher.json shipped
// alongside the suite.
func (h *Harness) EnricherConfigPath() string { return h.enricherCfg }

func bootstrap() (*Harness, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}

	if err := prependDirctlToPath(); err != nil {
		return nil, err
	}

	composeFile := filepath.Join(wd, "docker-compose.yaml")

	if err := composeUp(composeFile); err != nil {
		return nil, err
	}

	ollamaCmd, err := ensureOllama()
	if err != nil {
		return nil, err
	}

	if err := pullOllamaModel(); err != nil {
		return nil, err
	}

	cli, err := importerclient.New(
		context.Background(),
		importerclient.WithConfig(&importerclient.Config{ServerAddress: apiserverAddr}),
	)
	if err != nil {
		return nil, fmt.Errorf("dir client.New: %w", err)
	}

	return &Harness{
		composeFile: composeFile,
		enricherCfg: filepath.Join(wd, "enricher.json"),
		dirClient:   cli,
		ollamaCmd:   ollamaCmd,
	}, nil
}

// prependDirctlToPath puts the directory containing dirctl at the front of PATH so
// the enricher's mcp-go stdio launcher (which does exec.LookPath under the hood)
// resolves the bare "dirctl" command from the static enricher.json to the binary
// installed by `task deps:dirctl`.
func prependDirctlToPath() error {
	bin, err := resolveBinary("DIRCTL_BIN", "dirctl")
	if err != nil {
		return err
	}

	dir := filepath.Dir(bin)

	newPath := dir
	if currentPath := os.Getenv("PATH"); currentPath != "" {
		newPath = dir + string(os.PathListSeparator) + currentPath
	}

	if err := os.Setenv("PATH", newPath); err != nil {
		return fmt.Errorf("set PATH: %w", err)
	}

	return nil
}

// composeUp brings up the stack and blocks until every healthcheck is green
// (`--wait`), so we don't need separate readiness probes from Go.
func composeUp(composeFile string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerStartTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, //nolint:gosec // composeFile is repo-controlled
		"docker", "compose", "-p", stackComposeProject, "-f", composeFile,
		"up", "-d", "--wait",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}

	return nil
}

// Shutdown is invoked by the Ginkgo AfterSuite hook. It tears down compose and,
// if the harness started ollama itself, stops that too.
func Shutdown() {
	if sharedHarness == nil {
		return
	}

	stopOllama(sharedHarness.ollamaCmd)
	composeDown(sharedHarness.composeFile)
}

func composeDown(composeFile string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) //nolint:mnd
	defer cancel()

	cmd := exec.CommandContext(ctx, //nolint:gosec // composeFile is repo-controlled
		"docker", "compose", "-p", stackComposeProject, "-f", composeFile,
		"down", "-v", "--remove-orphans",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// ensureOllama makes sure an Ollama daemon is reachable at ollamaAddr. If one is
// already serving (e.g. dev's existing brew install), it reuses it and returns
// a nil *exec.Cmd. Otherwise it locates the binary (via $OLLAMA_BIN or PATH) and
// starts it as a subprocess. When non-nil, the returned *exec.Cmd owns the child
// and must be passed to stopOllama on teardown.
func ensureOllama() (*exec.Cmd, error) {
	if ollamaReachable() {
		return nil, nil //nolint:nilnil // nil cmd here means "we did not start it; nothing to clean up"
	}

	bin, err := resolveBinary("OLLAMA_BIN", "ollama")
	if err != nil {
		return nil, err
	}

	// context.Background is intentional: the daemon outlives any single test request and
	// is shut down explicitly via stopOllama in the AfterSuite hook.
	cmd := exec.CommandContext(context.Background(), bin, "serve") //nolint:gosec // bin resolved from $OLLAMA_BIN or PATH
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run in its own process group so a SIGINT to the test binary doesn't kill the
	// daemon mid-shutdown -- we want the deferred stopOllama to be the only path
	// that brings it down, so logs stay coherent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ollama serve: %w", err)
	}

	if err := waitForOllama(); err != nil {
		_ = stopProcess(cmd)

		return nil, err
	}

	return cmd, nil
}

// resolveBinary returns the path to a binary, preferring the value of envVar
// (set by Taskfile to the BIN_DIR install) then falling back to PATH lookup.
func resolveBinary(envVar, name string) (string, error) {
	if envBin := os.Getenv(envVar); envBin != "" {
		// envBin is set by Taskfile to BIN_DIR/<binary>; we only stat it to confirm
		// the install ran, never open or exec the path-as-data, so taint is benign.
		if _, err := os.Stat(envBin); err == nil { //nolint:gosec // env-var path validated by stat-only check
			return envBin, nil
		}
	}

	if pathBin, err := exec.LookPath(name); err == nil {
		return pathBin, nil
	}

	return "", fmt.Errorf(
		"%s binary not found: set $%s, install via `task deps:%s`, or place %s on PATH",
		name, envVar, name, name,
	)
}

// ollamaReachable returns true if something is already serving on ollamaAddr.
// We use a TCP probe because /api/tags requires the model index to be loaded,
// which adds a few hundred ms; a TCP dial is sufficient for "is the daemon up".
func ollamaReachable() bool {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond} //nolint:mnd

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond) //nolint:mnd
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", ollamaAddr)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// waitForOllama polls /api/tags until it responds 200 or the budget is exhausted.
// /api/tags is the cheapest endpoint that requires full daemon initialization.
func waitForOllama() error {
	deadline := time.Now().Add(ollamaStartTimeout)
	url := "http://" + ollamaAddr + "/api/tags"

	client := &http.Client{Timeout: 2 * time.Second} //nolint:mnd

	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx // bounded by client.Timeout
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}

			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		time.Sleep(250 * time.Millisecond) //nolint:mnd
	}

	return fmt.Errorf("ollama did not become ready within %s: %w", ollamaStartTimeout, lastErr)
}

func stopOllama(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = stopProcess(cmd)
}

// stopProcess sends SIGTERM, waits up to ollamaShutdownGrace for a clean exit,
// then escalates to SIGKILL.
func stopProcess(cmd *exec.Cmd) error {
	_ = cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(ollamaShutdownGrace):
		_ = cmd.Process.Kill()

		return <-done
	}
}

// pullOllamaModel asks the daemon to pull the model. Equivalent to running
// `ollama pull qwen3:8b` against the running daemon at ollamaAddr.
func pullOllamaModel() error {
	bin, err := resolveBinary("OLLAMA_BIN", "ollama")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelPullTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "pull", ollamaModel) //nolint:gosec // bin resolved above

	cmd.Env = append(os.Environ(), "OLLAMA_HOST="+ollamaAddr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ollama pull %s: %w", ollamaModel, err)
	}

	return nil
}
