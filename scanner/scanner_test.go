// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
	scannertypes "github.com/agntcy/dir-importer/scanner/types"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// The mock-pipeline tests in importer_test.go use a mockScanner and never
// exercise the production scanner stage at this package's wiring layer.
// These tests target Scanner.Scan directly, plus the mergeScanResults helper.

const errorMessage = "boom"

// fakeScanner is a controllable scannertypes.Scanner for unit testing.
type fakeScanner struct {
	name   string
	result *scannertypes.ScanResult
	err    error
}

func (f *fakeScanner) Name() string { return f.name }
func (f *fakeScanner) Scan(_ context.Context, _ *corev1.Record) (*scannertypes.ScanResult, error) {
	return f.result, f.err
}

func newScannerStage(scanners []scannertypes.Scanner, cfg scannerconfig.Config) *Scanner {
	return &Scanner{cfg: cfg, scanners: scanners}
}

func newRecord(t *testing.T, name string) *corev1.Record {
	t.Helper()

	st, err := structpb.NewStruct(map[string]any{
		"name":    name,
		"version": "1.0.0",
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	return &corev1.Record{Data: st}
}

// --- mergeScanResults ---

func TestMergeScanResults_Empty(t *testing.T) {
	t.Parallel()

	got := mergeScanResults(nil)
	if got == nil || !got.Skipped || got.SkippedReason == "" {
		t.Errorf("empty input must produce a skipped result: %+v", got)
	}
}

func TestMergeScanResults_SinglePassThrough(t *testing.T) {
	t.Parallel()

	in := &scannertypes.ScanResult{Safe: true}
	got := mergeScanResults([]*scannertypes.ScanResult{in})

	if got != in {
		t.Error("single result should be returned as-is")
	}
}

func TestMergeScanResults_AllSafe(t *testing.T) {
	t.Parallel()

	got := mergeScanResults([]*scannertypes.ScanResult{
		{Safe: true},
		{Safe: true},
	})

	if !got.Safe || got.Skipped {
		t.Errorf("all-safe should be Safe and not Skipped: %+v", got)
	}
}

func TestMergeScanResults_OneNotSafeWinsOverall(t *testing.T) {
	t.Parallel()

	got := mergeScanResults([]*scannertypes.ScanResult{
		{Safe: true},
		{Safe: false, Findings: []scannertypes.Finding{{Severity: scannertypes.SeverityError, Message: errorMessage}}},
	})

	if got.Safe {
		t.Error("any non-safe result should make the merged result non-safe")
	}

	if len(got.Findings) != 1 {
		t.Errorf("findings should be merged, got %d", len(got.Findings))
	}
}

func TestMergeScanResults_AllSkipped(t *testing.T) {
	t.Parallel()

	got := mergeScanResults([]*scannertypes.ScanResult{
		{Skipped: true, SkippedReason: "no rules"},
		{Skipped: true, SkippedReason: "no scanners"},
	})

	if !got.Skipped {
		t.Error("all skipped → merged should be skipped")
	}

	if got.Safe {
		t.Error("skipped merged result must not be marked safe")
	}

	if got.SkippedReason == "" {
		t.Error("merged skip reason should be populated")
	}
}

func TestMergeScanResults_FindingsAlwaysMakeSafeFalse(t *testing.T) {
	t.Parallel()

	// A scanner can report Safe=true while still emitting an info-severity
	// finding. The merge logic conservatively flips Safe to false whenever
	// findings are present so the handler always processes them. The
	// single-result fast path returns the input as-is, so we test via the
	// multi-input branch where mergeScanResults actually rebuilds Safe.
	got := mergeScanResults([]*scannertypes.ScanResult{
		{Safe: true, Findings: []scannertypes.Finding{{Severity: scannertypes.SeverityInfo, Message: "fyi"}}},
		{Safe: true},
	})

	if got.Safe {
		t.Error("merged result with any finding should be unsafe")
	}
}

// --- Scan stage ---

func TestScan_NoScanners_PassesAllRecords(t *testing.T) {
	t.Parallel()

	stage := newScannerStage(nil, scannerconfig.Config{})

	in := make(chan *corev1.Record, 2)
	in <- newRecord(t, "rec-1")

	in <- newRecord(t, "rec-2")

	close(in)

	result := &types.Result{}

	out, errCh := stage.Scan(context.Background(), in, result)
	go drainErrors(errCh)

	var passed int
	for range out {
		passed++
	}

	if passed != 2 {
		t.Errorf("passed = %d, want 2 (no scanners → skipped → records pass through)", passed)
	}

	if result.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", result.FailedCount)
	}
}

func TestScan_AllSafe_PassesAllRecords(t *testing.T) {
	t.Parallel()

	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "ok-1", result: &scannertypes.ScanResult{Safe: true}},
		&fakeScanner{name: "ok-2", result: &scannertypes.ScanResult{Safe: true}},
	}, scannerconfig.Config{})

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "safe-record")

	close(in)

	result := &types.Result{}
	out, errCh := stage.Scan(context.Background(), in, result)

	go drainErrors(errCh)

	count := 0
	for range out {
		count++
	}

	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	if result.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", result.FailedCount)
	}
}

func TestScan_FailOnError_DropsRecord(t *testing.T) {
	t.Parallel()

	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "broken", result: &scannertypes.ScanResult{
			Safe: false,
			Findings: []scannertypes.Finding{
				{Severity: scannertypes.SeverityError, Message: errorMessage},
			},
		}},
	}, scannerconfig.Config{FailOnError: true})

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "should-drop")

	close(in)

	result := &types.Result{}
	out, errCh := stage.Scan(context.Background(), in, result)

	go drainErrors(errCh)

	count := 0
	for range out {
		count++
	}

	if count != 0 {
		t.Errorf("got %d records out, want 0 (fail-on-error should drop)", count)
	}

	if result.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", result.FailedCount)
	}
}

func TestScan_FailOnWarning_DropsRecord(t *testing.T) {
	t.Parallel()

	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "noisy", result: &scannertypes.ScanResult{
			Safe: false,
			Findings: []scannertypes.Finding{
				{Severity: scannertypes.SeverityWarning, Message: "watch out"},
			},
		}},
	}, scannerconfig.Config{FailOnWarning: true})

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "warn-record")

	close(in)

	result := &types.Result{}
	out, errCh := stage.Scan(context.Background(), in, result)

	go drainErrors(errCh)

	count := 0
	for range out {
		count++
	}

	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	if result.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", result.FailedCount)
	}
}

func TestScan_FailOnErrorWithOnlyWarnings_PassesRecord(t *testing.T) {
	t.Parallel()

	// FailOnError must only drop records that have ERROR-level findings;
	// warning-only records should still be reported on the result but pass
	// through unless FailOnWarning is also set.
	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "warnings-only", result: &scannertypes.ScanResult{
			Safe: false,
			Findings: []scannertypes.Finding{
				{Severity: scannertypes.SeverityWarning, Message: "iffy"},
			},
		}},
	}, scannerconfig.Config{FailOnError: true})

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "warn-only")

	close(in)

	result := &types.Result{}
	out, errCh := stage.Scan(context.Background(), in, result)

	go drainErrors(errCh)

	count := 0
	for range out {
		count++
	}

	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	if result.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0 (warnings should not drop under FailOnError)", result.FailedCount)
	}
}

func TestScan_AllScannersFail_RecordPassesAndErrorEmitted(t *testing.T) {
	t.Parallel()

	// When every scanner errors out, runAll returns an error. The Scan
	// goroutine documents the contract: on a runAll error, the record is
	// NOT dropped (we don't want a transient scanner outage to silently
	// reject records) — we emit an error and pass the record through.
	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "down-1", err: errors.New("scanner offline")},
		&fakeScanner{name: "down-2", err: errors.New("scanner offline")},
	}, scannerconfig.Config{FailOnError: true})

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "rec")

	close(in)

	result := &types.Result{}
	out, errCh := stage.Scan(context.Background(), in, result)

	type counts struct{ recs, errs int }

	done := make(chan counts, 1)

	go func() {
		var c counts

		for {
			select {
			case _, ok := <-out:
				if !ok {
					out = nil
				} else {
					c.recs++
				}
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				} else {
					c.errs++
				}
			}

			if out == nil && errCh == nil {
				break
			}
		}

		done <- c
	}()

	got := <-done
	if got.recs != 1 {
		t.Errorf("records out = %d, want 1", got.recs)
	}

	if got.errs != 1 {
		t.Errorf("errors out = %d, want 1", got.errs)
	}

	if result.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", result.FailedCount)
	}
}

func TestScan_OneScannerFailsOthersSucceed_NoStageError(t *testing.T) {
	t.Parallel()

	// runAll returns an error only when ALL scanners fail. If at least one
	// succeeds, the merged result should drive the decision — no stage
	// error should be emitted.
	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "up", result: &scannertypes.ScanResult{Safe: true}},
		&fakeScanner{name: "down", err: errors.New("scanner offline")},
	}, scannerconfig.Config{})

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "rec")

	close(in)

	out, errCh := stage.Scan(context.Background(), in, &types.Result{})

	errs := 0

	go func() {
		for range errCh {
			errs++
		}
	}()

	count := 0
	for range out {
		count++
	}

	// Give the err-drain goroutine a moment to settle.
	time.Sleep(50 * time.Millisecond)

	if count != 1 {
		t.Errorf("records out = %d, want 1", count)
	}

	if errs != 0 {
		t.Errorf("got %d stage errors; one-scanner-failure should not emit stage error", errs)
	}
}

func TestScan_RecordsFindings(t *testing.T) {
	t.Parallel()

	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "noisy", result: &scannertypes.ScanResult{
			Safe: false,
			Findings: []scannertypes.Finding{
				{Severity: scannertypes.SeverityWarning, Message: "watch"},
				{Severity: scannertypes.SeverityError, Message: errorMessage},
			},
		}},
	}, scannerconfig.Config{}) // no fail-on-* → record still passes

	in := make(chan *corev1.Record, 1)
	in <- newRecord(t, "rec")

	close(in)

	result := &types.Result{}
	out, errCh := stage.Scan(context.Background(), in, result)

	go drainErrors(errCh)

	for range out {
	}

	// Each finding should be added to the result.ScannerFindings list.
	if got := len(result.ScannerFindings); got != 2 {
		t.Errorf("ScannerFindings = %d, want 2", got)
	}
}

func TestScan_ContextCancellation(t *testing.T) {
	t.Parallel()

	stage := newScannerStage([]scannertypes.Scanner{
		&fakeScanner{name: "noop", result: &scannertypes.ScanResult{Safe: true}},
	}, scannerconfig.Config{})

	in := make(chan *corev1.Record) // unbuffered: no producer feeds

	ctx, cancel := context.WithCancel(context.Background())
	out, errCh := stage.Scan(ctx, in, &types.Result{})

	cancel()

	deadline := time.After(time.Second)

	for out != nil || errCh != nil {
		select {
		case _, ok := <-out:
			if !ok {
				out = nil
			}
		case _, ok := <-errCh:
			if !ok {
				errCh = nil
			}
		case <-deadline:
			t.Fatal("Scan did not return after context cancellation")
		}
	}

	close(in)
}

// --- ClosedScannerErrCh ---

func TestClosedScannerErrCh(t *testing.T) {
	t.Parallel()

	// The pipeline relies on ClosedScannerErrCh being a pre-closed channel
	// so that orchestration code can range over it as an empty stream when
	// the scanner stage is disabled.
	count := 0
	for range ClosedScannerErrCh {
		count++
	}

	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func drainErrors(errCh <-chan error) {
	for range errCh {
	}
}
