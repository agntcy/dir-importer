// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	filePermission = 0o600
	dirPermission  = 0o700

	formatJSON = "json"
	formatText = "text"
)

// initOnce is a pointer so tests can swap in a fresh guard without copying a
// sync.Once value (which the runtime forbids).
var initOnce = &sync.Once{}

// getLogOutput resolves the writer used by the configured handler. An empty
// path or any failure to open the requested file falls back to stdout so the
// process keeps logging.
func getLogOutput(logFilePath string) io.Writer {
	if logFilePath == "" {
		return os.Stdout
	}

	cleanPath := filepath.Clean(logFilePath)

	// Best-effort: ensure the parent directory exists with restrictive perms.
	if dir := filepath.Dir(cleanPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, dirPermission); err != nil {
			slog.Warn("Failed to create log file directory, defaulting to stdout",
				"path", cleanPath, "error", err)

			return os.Stdout
		}
	}

	file, err := os.OpenFile(cleanPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePermission)
	if err != nil {
		slog.Warn("Failed to open log file, defaulting to stdout",
			"path", cleanPath, "error", err)

		return os.Stdout
	}

	return file
}

// parseLevel parses a log level string and reports whether the input was
// recognised. The boolean lets callers warn on bad input while still returning
// a usable default.
func parseLevel(s string) (slog.Level, bool) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(s)))); err != nil {
		return slog.LevelInfo, false
	}

	return lvl, true
}

// newHandler builds the slog handler for the given format. Unknown formats
// fall back to text so configuration mistakes never silence the process.
func newHandler(out io.Writer, format string, level slog.Level) (slog.Handler, bool) {
	opts := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(strings.TrimSpace(format)) {
	case formatJSON:
		return slog.NewJSONHandler(out, opts), true
	case formatText, "":
		return slog.NewTextHandler(out, opts), true
	default:
		return slog.NewTextHandler(out, opts), false
	}
}

// InitLogger installs the configured handler as the process-wide default
// [slog.Logger]. It is idempotent and safe for concurrent calls; only the
// first invocation has any effect, mirroring the previous `dir/utils/logging`
// behavior so importers do not accidentally clobber a host-set logger.
func InitLogger(cfg *Config) {
	if cfg == nil {
		cfg = LoadConfig()
	}

	initOnce.Do(func() {
		out := getLogOutput(cfg.LogFile)

		level, levelOK := parseLevel(cfg.LogLevel)
		handler, formatOK := newHandler(out, cfg.LogFormat, level)

		slog.SetDefault(slog.New(handler))

		// Emit warnings *after* the default is wired so they go through the
		// new handler and are visible to operators.
		if !levelOK {
			slog.Warn("Invalid log level, defaulting to INFO",
				"value", cfg.LogLevel, "env", EnvLogLevel)
		}

		if !formatOK {
			slog.Warn("Invalid log format, defaulting to text",
				"value", cfg.LogFormat, "env", EnvLogFormat)
		}
	})
}

// Logger returns a child of the default [slog.Logger] tagged with the given
// component attribute. The signature matches the previous helper so the
// migration from `github.com/agntcy/dir/utils/logging` is import-path-only.
func Logger(component string) *slog.Logger {
	return slog.Default().With("component", component)
}

func init() {
	InitLogger(LoadConfig())
}
