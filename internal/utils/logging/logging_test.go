// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// installDefault wires a captured handler into slog and returns a restore
// function. It also resets the package-level once-guard so InitLogger can be
// re-exercised in dedicated tests.
func installDefault(t *testing.T, handler slog.Handler) {
	t.Helper()

	prev := slog.Default()

	slog.SetDefault(slog.New(handler))

	t.Cleanup(func() { slog.SetDefault(prev) })
}

func resetInitOnce(t *testing.T) {
	t.Helper()

	prev := initOnce
	initOnce = &sync.Once{}

	t.Cleanup(func() { initOnce = prev })
}

func TestLoggerAddsComponentAttribute(t *testing.T) {
	var buf bytes.Buffer

	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	installDefault(t, handler)

	Logger("importer/scanner").Info("hello", "extra", "data")

	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if got := parsed["component"]; got != "importer/scanner" {
		t.Errorf("expected component=importer/scanner, got %v", got)
	}

	if got := parsed["extra"]; got != "data" {
		t.Errorf("expected extra=data, got %v", got)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
		ok    bool
	}{
		{testLevelDebug, slog.LevelDebug, true},
		{"info", slog.LevelInfo, true},
		{"  WARN  ", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"", slog.LevelInfo, false},
		{"verbose", slog.LevelInfo, false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := parseLevel(tc.input)
			if ok != tc.ok {
				t.Errorf("parseLevel(%q) ok=%v, want %v", tc.input, ok, tc.ok)
			}

			if got != tc.want {
				t.Errorf("parseLevel(%q) level=%v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestNewHandlerSelectsFormat(t *testing.T) {
	var buf bytes.Buffer

	jsonHandler, ok := newHandler(&buf, "JSON", slog.LevelInfo)
	if !ok {
		t.Fatalf("expected json to be recognised")
	}

	slog.New(jsonHandler).Info("hi")

	if !json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("expected JSON output, got: %s", buf.String())
	}

	buf.Reset()

	textHandler, ok := newHandler(&buf, "text", slog.LevelInfo)
	if !ok {
		t.Fatalf("expected text to be recognised")
	}

	slog.New(textHandler).Info("hi")

	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("expected text output, got JSON: %s", buf.String())
	}

	buf.Reset()

	fallbackHandler, ok := newHandler(&buf, "yaml", slog.LevelInfo)
	if ok {
		t.Errorf("expected yaml to be unrecognised")
	}

	slog.New(fallbackHandler).Info("hi")

	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("expected text fallback, got JSON: %s", buf.String())
	}
}

func TestGetLogOutputDefaultsToStdout(t *testing.T) {
	if got := getLogOutput(""); got != os.Stdout {
		t.Errorf("expected stdout for empty path, got %T", got)
	}
}

func TestGetLogOutputFallsBackOnInvalidPath(t *testing.T) {
	// Use the existing config.go file as a "directory" prefix so MkdirAll
	// fails predictably across platforms.
	bogus := filepath.Join("config.go", "nested", "log.txt")

	if got := getLogOutput(bogus); got != os.Stdout {
		t.Errorf("expected stdout fallback for invalid path %q, got %T", bogus, got)
	}
}

func TestGetLogOutputCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "importer.log")

	out := getLogOutput(path)
	if out == os.Stdout {
		t.Fatal("expected file writer, got stdout")
	}

	file, ok := out.(*os.File)
	if !ok {
		t.Fatalf("expected *os.File, got %T", out)
	}

	t.Cleanup(func() { _ = file.Close() })

	if _, err := file.WriteString("hello\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if perm := info.Mode().Perm(); perm != filePermission {
		t.Errorf("expected file perm %#o, got %#o", filePermission, perm)
	}
}

func TestInitLoggerIdempotent(t *testing.T) {
	resetInitOnce(t)

	// First call wires a JSON handler.
	InitLogger(&Config{LogLevel: testLevelDebug, LogFormat: "json"})

	first := slog.Default()

	// A second call with different config must be a no-op.
	InitLogger(&Config{LogLevel: "ERROR", LogFormat: "text"})

	if slog.Default() != first {
		t.Error("InitLogger should be idempotent: second call must not replace default logger")
	}
}

func TestInitLoggerInvalidValuesFallBackSafely(t *testing.T) {
	resetInitOnce(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "out.log")

	InitLogger(&Config{LogLevel: "BOGUS", LogFormat: "yaml", LogFile: path})

	// Logging must still work and reach the configured file.
	Logger("importer/test").Info("after-init")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if !strings.Contains(string(data), "after-init") {
		t.Errorf("expected log line in %q, got: %s", path, data)
	}

	// The fallback warning emitted during init must mention the bad values.
	if !strings.Contains(string(data), "Invalid log level") {
		t.Errorf("expected level fallback warning in output: %s", data)
	}

	if !strings.Contains(string(data), "Invalid log format") {
		t.Errorf("expected format fallback warning in output: %s", data)
	}
}

func TestInitLoggerNilConfigFallsBackToEnv(t *testing.T) {
	resetInitOnce(t)

	t.Setenv(EnvLogLevel, testLevelDebug)
	t.Setenv(EnvLogFormat, "json")
	t.Setenv(EnvLogFile, "")

	InitLogger(nil)

	if slog.Default() == nil {
		t.Fatal("slog.Default() must not be nil after InitLogger(nil)")
	}
}
