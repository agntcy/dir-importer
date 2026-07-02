// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package scanner

import (
	"context"
	"fmt"

	"github.com/agntcy/dir-importer/internal/utils/logging"
	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
	"github.com/agntcy/dir-importer/scanner/factory"
	"github.com/agntcy/dir-importer/shared"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	dirscanner "github.com/agntcy/dir/utils/scanner"
)

var logger = logging.Logger("importer/scanner")

// Scanner is the pipeline stage that runs registered runners per record.
type Scanner struct {
	cfg     scannerconfig.Config
	runners []dirscanner.Runner
}

// New creates a Scanner that runs the configured runners for each record.
func New(cfg scannerconfig.Config) (*Scanner, error) {
	runners, err := factory.NewRunners(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create runners: %w", err)
	}

	return &Scanner{cfg: cfg, runners: runners}, nil
}

// Scan implements pipeline.Scanner. For each record it runs all configured runners,
// merges their results, and applies fail-on-error/warning drop logic.
func (s *Scanner) Scan(ctx context.Context, inputCh <-chan *corev1.Record, result *types.Result) (<-chan *corev1.Record, <-chan error) {
	outputCh := make(chan *corev1.Record)
	errCh := make(chan error)

	go func() {
		defer close(outputCh)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return
			case record, ok := <-inputCh:
				if !ok {
					return
				}

				recordName, _ := shared.ExtractNameVersion(record)

				scanResult, err := dirscanner.RunAll(ctx, s.runners, record, logger)
				if err != nil {
					logger.Warn("Scan error", "record", recordName, "error", err)

					select {
					case errCh <- fmt.Errorf("scan %s: %w", recordName, err):
					case <-ctx.Done():
						return
					}

					select {
					case outputCh <- record:
					case <-ctx.Done():
						return
					}

					continue
				}

				s.handleResult(ctx, record, recordName, scanResult, result, outputCh, errCh)
			}
		}
	}()

	return outputCh, errCh
}

// handleResult processes the merged scan result: logs, records findings, and decides
// whether to pass or drop the record.
func (s *Scanner) handleResult(
	ctx context.Context,
	record *corev1.Record,
	recordName string,
	scanResult *dirscanner.ScanResult,
	result *types.Result,
	outputCh chan<- *corev1.Record,
	_ chan<- error,
) {
	if scanResult.Skipped {
		logger.Info("Scan skipped", "record", recordName, "reason", scanResult.SkippedReason)

		select {
		case outputCh <- record:
		case <-ctx.Done():
		}

		return
	}

	if scanResult.Safe {
		logger.Info("Scan passed", "record", recordName)

		select {
		case outputCh <- record:
		case <-ctx.Done():
		}

		return
	}

	logger.Warn("Scan found issues", "record", recordName, "findings", len(scanResult.Findings))

	for _, f := range scanResult.Findings {
		line := string(f.Severity) + ": " + f.Message
		logger.Warn("Finding", "record", recordName, "severity", string(f.Severity), "message", f.Message)
		result.RecordScannerFinding(recordName + ": " + line)
	}

	drop := (s.cfg.FailOnError && scanResult.HasError()) || (s.cfg.FailOnWarning && scanResult.HasWarning())
	if drop {
		logger.Warn("Record dropped", "record", recordName)
		result.IncrementFailedCount()
	} else {
		select {
		case outputCh <- record:
		case <-ctx.Done():
		}
	}
}

// ClosedScannerErrCh is closed with no values; ranging over it exits immediately when the scanner stage is skipped.
var ClosedScannerErrCh = func() <-chan error {
	ch := make(chan error)
	close(ch)

	return ch
}()
