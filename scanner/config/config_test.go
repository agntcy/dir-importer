// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"strings"
	"testing"
	"time"

	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
)

// scannerBin is the placeholder CLI path used across rows that exercise the
// "enabled with valid CLIPath" combination. The value never matters — only
// non-emptiness — but reusing the same string keeps rows uniform.
const scannerBin = "mcp-scanner"

// Validate is dominated by the !Enabled short-circuit: the entire pipeline
// runs without a scanner by default, so a zero-value Config must validate
// even though it has no Timeout or CLIPath set. Once Enabled flips, both
// fields become required.
func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         scannerconfig.Config
		wantErrText string // empty = expect success
	}{
		{
			name: "disabled scanner ignores all other fields",
			cfg:  scannerconfig.Config{Enabled: false},
		},
		{
			name: "disabled scanner with junk fields still validates",
			cfg: scannerconfig.Config{
				Enabled: false,
				Timeout: -42,
				CLIPath: "",
			},
		},
		{
			name: "enabled with zero timeout is rejected",
			cfg: scannerconfig.Config{
				Enabled: true,
				Timeout: 0,
				CLIPath: scannerBin,
			},
			wantErrText: "timeout must be greater than 0",
		},
		{
			name: "enabled with negative timeout is rejected",
			cfg: scannerconfig.Config{
				Enabled: true,
				Timeout: -1 * time.Second,
				CLIPath: scannerBin,
			},
			wantErrText: "timeout must be greater than 0",
		},
		{
			name: "enabled with empty CLIPath is rejected",
			cfg: scannerconfig.Config{
				Enabled: true,
				Timeout: 5 * time.Minute,
				CLIPath: "",
			},
			wantErrText: "mcp-scanner binary path is required",
		},
		{
			name: "enabled with positive timeout and CLIPath is accepted",
			cfg: scannerconfig.Config{
				Enabled: true,
				Timeout: 5 * time.Minute,
				CLIPath: scannerBin,
			},
		},
		{
			name: "FailOn flags are independent of validation outcome",
			cfg: scannerconfig.Config{
				Enabled:       true,
				Timeout:       1 * time.Second,
				CLIPath:       scannerBin,
				FailOnError:   true,
				FailOnWarning: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()

			switch {
			case tt.wantErrText != "":
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrText)
				}

				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrText)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
