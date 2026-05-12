// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

// Package logging provides a thin wrapper over [log/slog] that configures the
// process-wide default logger from environment variables owned by this module.
//
// It intentionally has no third-party dependencies so the importer can be
// released independently of the wider Directory release train.
package logging

import "os"

const (
	// EnvPrefix is the namespace for the environment variables read by
	// [LoadConfig]. It is intentionally distinct from the upstream
	// `DIRECTORY_LOGGER_*` namespace previously used.
	EnvPrefix = "IMPORTER_LOG"

	// EnvLogLevel is read for the log level (e.g. DEBUG, INFO, WARN, ERROR).
	EnvLogLevel = EnvPrefix + "_LEVEL"
	// EnvLogFormat is read for the log format (text or json).
	EnvLogFormat = EnvPrefix + "_FORMAT"
	// EnvLogFile is read for an optional file path; empty means stdout.
	EnvLogFile = EnvPrefix + "_FILE"

	// DefaultLogLevel is the level used when none is configured or the
	// configured value cannot be parsed.
	DefaultLogLevel = "INFO"
	// DefaultLogFormat is the format used when none is configured or the
	// configured value is unrecognised.
	DefaultLogFormat = "text"
)

// Config captures the runtime knobs of the logging package. All fields are
// optional; zero values fall back to the package defaults.
type Config struct {
	// LogFile is an absolute or relative path to a file. When empty, logs are
	// written to stdout.
	LogFile string
	// LogLevel is one of DEBUG, INFO, WARN, ERROR (case-insensitive).
	LogLevel string
	// LogFormat is text or json (case-insensitive).
	LogFormat string
}

// LoadConfig builds a [Config] from the environment, applying defaults for
// missing values. It never returns an error: invalid values are intentionally
// passed through so [InitLogger] can log a warning and fall back safely.
func LoadConfig() *Config {
	cfg := &Config{
		LogFile:   os.Getenv(EnvLogFile),
		LogLevel:  os.Getenv(EnvLogLevel),
		LogFormat: os.Getenv(EnvLogFormat),
	}

	if cfg.LogLevel == "" {
		cfg.LogLevel = DefaultLogLevel
	}

	if cfg.LogFormat == "" {
		cfg.LogFormat = DefaultLogFormat
	}

	return cfg
}
