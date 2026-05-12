// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package logging

import "testing"

const testLevelDebug = "DEBUG"

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv(EnvLogLevel, "")
	t.Setenv(EnvLogFormat, "")
	t.Setenv(EnvLogFile, "")

	cfg := LoadConfig()

	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("expected LogLevel=%q, got %q", DefaultLogLevel, cfg.LogLevel)
	}

	if cfg.LogFormat != DefaultLogFormat {
		t.Errorf("expected LogFormat=%q, got %q", DefaultLogFormat, cfg.LogFormat)
	}

	if cfg.LogFile != "" {
		t.Errorf("expected empty LogFile, got %q", cfg.LogFile)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv(EnvLogLevel, testLevelDebug)
	t.Setenv(EnvLogFormat, "json")
	t.Setenv(EnvLogFile, "/tmp/importer.log")

	cfg := LoadConfig()

	if cfg.LogLevel != testLevelDebug {
		t.Errorf("expected LogLevel=%s, got %q", testLevelDebug, cfg.LogLevel)
	}

	if cfg.LogFormat != "json" {
		t.Errorf("expected LogFormat=json, got %q", cfg.LogFormat)
	}

	if cfg.LogFile != "/tmp/importer.log" {
		t.Errorf("expected LogFile=/tmp/importer.log, got %q", cfg.LogFile)
	}
}

func TestLoadConfigPreservesInvalidValues(t *testing.T) {
	// Invalid values must survive round-tripping; InitLogger is responsible
	// for the actual fallback behavior so it can also emit a warning.
	t.Setenv(EnvLogLevel, "BOGUS")
	t.Setenv(EnvLogFormat, "yaml")

	cfg := LoadConfig()

	if cfg.LogLevel != "BOGUS" {
		t.Errorf("expected LogLevel=BOGUS, got %q", cfg.LogLevel)
	}

	if cfg.LogFormat != "yaml" {
		t.Errorf("expected LogFormat=yaml, got %q", cfg.LogFormat)
	}
}

func TestEnvVarNamespace(t *testing.T) {
	// The migration is observable: the new env-var prefix must be IMPORTER_LOG.
	if EnvPrefix != "IMPORTER_LOG" {
		t.Errorf("expected EnvPrefix=IMPORTER_LOG, got %q", EnvPrefix)
	}

	if EnvLogLevel != "IMPORTER_LOG_LEVEL" {
		t.Errorf("expected EnvLogLevel=IMPORTER_LOG_LEVEL, got %q", EnvLogLevel)
	}

	if EnvLogFormat != "IMPORTER_LOG_FORMAT" {
		t.Errorf("expected EnvLogFormat=IMPORTER_LOG_FORMAT, got %q", EnvLogFormat)
	}

	if EnvLogFile != "IMPORTER_LOG_FILE" {
		t.Errorf("expected EnvLogFile=IMPORTER_LOG_FILE, got %q", EnvLogFile)
	}
}
