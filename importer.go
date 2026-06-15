// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/agntcy/dir-importer/config"
	"github.com/agntcy/dir-importer/dedup"
	"github.com/agntcy/dir-importer/enricher"
	"github.com/agntcy/dir-importer/fetcher"
	"github.com/agntcy/dir-importer/pusher"
	"github.com/agntcy/dir-importer/scanner"
	"github.com/agntcy/dir-importer/shared"
	"github.com/agntcy/dir-importer/transformer"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
)

// Importer implements the Importer interface for MCP registry using a pipeline architecture.
type Importer struct {
	cfg         config.Config
	client      config.ClientInterface
	fetcher     types.Fetcher
	dedup       types.DuplicateChecker
	transformer types.Transformer
	enricher    types.Enricher
	scanner     types.Scanner
	pusher      types.Pusher
}

// New creates a new importer instance (MCP registry/file, A2A file, or Agent Skill directory tree).
func New(ctx context.Context, client config.ClientInterface, cfg config.Config) (types.Importer, error) {
	var (
		fetch types.Fetcher
		err   error
	)

	switch cfg.Type {
	case config.ImportTypeMCPRegistry:
		fetch, err = fetcher.NewMCPRegistryFetcher(cfg.RegistryURL, cfg.Filters, cfg.Limit)
	case config.ImportTypeMCP:
		fetch, err = fetcher.NewMCPFileFetcher(cfg.FilePath)
	case config.ImportTypeA2A:
		fetch, err = fetcher.NewA2AFileFetcher(cfg.FilePath)
	case config.ImportTypeAgentSkill:
		fetch, err = fetcher.NewAgentSkillDirFetcher(cfg.FilePath)
	default:
		return nil, fmt.Errorf("unsupported import type: %s", cfg.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create fetcher: %w", err)
	}

	trackUnsigned := cfg.SignFunc != nil || cfg.IncludeUnsignedCIDs

	d, err := dedup.NewDuplicateChecker(ctx, client, cfg.Type, trackUnsigned, cfg.Debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create duplicate checker: %w", err)
	}

	var e types.Enricher

	if cfg.Enricher.SkipEnricher {
		e = enricher.NewStaticEnricher(cfg.Enricher.Skills, cfg.Enricher.Domains)
	} else {
		e, err = enricher.New(ctx, cfg.Enricher)
		if err != nil {
			return nil, fmt.Errorf("failed to create enricher: %w", err)
		}
	}

	sc, err := scanner.New(cfg.Scanner)
	if err != nil {
		return nil, fmt.Errorf("failed to create scanner: %w", err)
	}

	return &Importer{
		cfg:         cfg,
		client:      client,
		fetcher:     fetch,
		dedup:       d,
		transformer: transformer.NewTransformer(cfg.Debug, cfg.Authors),
		enricher:    e,
		scanner:     sc,
		pusher:      pusher.NewClientPusher(client, cfg.Debug, cfg.SignFunc),
	}, nil
}

//nolint:gocognit,cyclop // Full pipeline wiring and per-stage error collectors; splitting obscures flow.
func (i *Importer) Run(ctx context.Context) *types.ImportResult {
	result := &types.Result{}

	// Stage 1: Fetch records
	fetchedCh, fetchErrCh := i.fetcher.Fetch(ctx)

	// Stage 2: Filter duplicates (optional - only if duplicate checker is available)
	var filteredCh <-chan types.SourceItem
	if !i.cfg.Force {
		filteredCh = i.dedup.FilterDuplicates(ctx, fetchedCh, result)
	} else {
		filteredCh = fetchedCh
	}

	// Stage 3: Transform records (non-duplicates)
	transformedCh, transformErrCh := i.transformer.Transform(ctx, filteredCh, result)

	// Stage 4: Enrich records
	enrichedCh, enrichErrCh := i.enricher.Enrich(ctx, transformedCh, result)

	// Stage 5: Scanner — may drop records, appends to result.ScannerFindings
	var pushInputCh <-chan *corev1.Record

	var scannerErrCh <-chan error

	if i.cfg.Scanner.Enabled {
		pushInputCh, scannerErrCh = i.scanner.Scan(ctx, enrichedCh, result)
	} else {
		pushInputCh = enrichedCh
		scannerErrCh = scanner.ClosedScannerErrCh
	}

	// Stage 6: Push records
	refCh, pushErrCh := i.pusher.Push(ctx, pushInputCh)

	// Collect errors from all stages
	var wg sync.WaitGroup
	wg.Add(6) //nolint:mnd // fetch, transform, enrich, scanner, push refs, push errors

	// Collect fetch errors
	go func() {
		defer wg.Done()

		for err := range fetchErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("fetch error: %w", err))
				result.Mu.Unlock()
			}
		}
	}()

	// Collect transform errors
	go func() {
		defer wg.Done()

		for err := range transformErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	// Collect enrich errors
	go func() {
		defer wg.Done()

		for err := range enrichErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	// Collect scanner errors
	go func() {
		defer wg.Done()

		for err := range scannerErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	// Track successful pushes
	go func() {
		defer wg.Done()

		for ref := range refCh {
			if ref != nil && ref.GetCid() != "" {
				// Valid CID - record successfully imported
				result.Mu.Lock()
				result.ImportedCount++
				result.ImportedCIDs = append(result.ImportedCIDs, ref.GetCid())
				result.Mu.Unlock()
			}
		}
	}()

	// Track push errors
	go func() {
		defer wg.Done()

		for err := range pushErrCh {
			if err != nil {
				result.Mu.Lock()
				result.FailedCount++
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	wg.Wait()

	i.processUnsignedDuplicates(ctx, result)

	return i.buildImportResult(result, "")
}

func (i *Importer) buildImportResult(result *types.Result, outputDir string) *types.ImportResult {
	return &types.ImportResult{
		TotalRecords:          result.TotalRecords,
		ImportedCount:         result.ImportedCount,
		SkippedCount:          result.SkippedCount,
		FailedCount:           result.FailedCount,
		Errors:                result.Errors,
		OutputDir:             outputDir,
		ImportedCIDs:          result.ImportedCIDs,
		UnsignedDuplicateCIDs: result.UnsignedDuplicateCIDs,
		ScannerFindings:       result.ScannerFindings,
	}
}

// processUnsignedDuplicates signs or records CIDs for unsigned duplicates that
// were skipped during dedup instead of re-importing them through the pipeline.
func (i *Importer) processUnsignedDuplicates(ctx context.Context, result *types.Result) {
	if len(result.UnsignedDuplicateCIDs) == 0 {
		return
	}

	if i.cfg.DryRun {
		if i.cfg.IncludeUnsignedCIDs {
			result.Mu.Lock()
			result.ImportedCIDs = append(result.ImportedCIDs, result.UnsignedDuplicateCIDs...)
			result.Mu.Unlock()
		}

		return
	}

	for _, cid := range result.UnsignedDuplicateCIDs {
		switch {
		case i.cfg.SignFunc != nil:
			if err := i.cfg.SignFunc(ctx, cid); err != nil {
				result.Mu.Lock()
				result.FailedCount++
				result.Errors = append(result.Errors, fmt.Errorf("signing unsigned duplicate CID %s: %w", cid, err))
				result.Mu.Unlock()

				continue
			}

			result.Mu.Lock()
			result.ImportedCount++
			result.ImportedCIDs = append(result.ImportedCIDs, cid)
			result.Mu.Unlock()
		case i.cfg.IncludeUnsignedCIDs:
			result.Mu.Lock()
			result.ImportedCIDs = append(result.ImportedCIDs, cid)
			result.Mu.Unlock()
		}
	}
}

func (i *Importer) DryRun(ctx context.Context) *types.ImportResult {
	outputDir := i.cfg.OutputDir
	if outputDir == "" {
		outputDir = fmt.Sprintf("import-dry-run-%s", time.Now().Format("2006-01-02-150405"))
	}

	result := &types.Result{}

	// Stage 1: Fetch records
	fetchedCh, fetchErrCh := i.fetcher.Fetch(ctx)

	// Stage 2: Filter duplicates (optional - provides accurate preview)
	var filteredCh <-chan types.SourceItem
	if !i.cfg.Force {
		filteredCh = i.dedup.FilterDuplicates(ctx, fetchedCh, result)
	} else {
		filteredCh = fetchedCh
	}

	// Stage 3: Transform records
	transformedCh, transformErrCh := i.transformer.Transform(ctx, filteredCh, result)

	// Stage 4: Enrich records
	enrichedCh, enrichErrCh := i.enricher.Enrich(ctx, transformedCh, result)

	// Stage 5: Scanner — may drop records, writes to stdout (same gating as Run).
	var fileInputCh <-chan *corev1.Record

	var scannerErrCh <-chan error

	if i.cfg.Scanner.Enabled {
		fileInputCh, scannerErrCh = i.scanner.Scan(ctx, enrichedCh, result)
	} else {
		fileInputCh = enrichedCh
		scannerErrCh = scanner.ClosedScannerErrCh
	}

	// Collect errors from all stages
	var wg sync.WaitGroup
	wg.Add(5) //nolint:mnd // fetch, transform, enrich, scanner errors, file writer

	// Collect fetch errors
	go func() {
		defer wg.Done()

		for err := range fetchErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("fetch error: %w", err))
				result.Mu.Unlock()
			}
		}
	}()

	// Collect transform errors
	go func() {
		defer wg.Done()

		for err := range transformErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	// Collect enrich errors
	go func() {
		defer wg.Done()

		for err := range enrichErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	// Collect scanner errors
	go func() {
		defer wg.Done()

		for err := range scannerErrCh {
			if err != nil {
				result.Mu.Lock()
				result.Errors = append(result.Errors, err)
				result.Mu.Unlock()
			}
		}
	}()

	// Collect records - write to directory, one file per record
	go func() {
		defer wg.Done()

		defer func() {
			for range fileInputCh {
			}
		}()

		if err := writeRecords(outputDir, fileInputCh); err != nil {
			result.Mu.Lock()
			result.Errors = append(result.Errors, fmt.Errorf("failed to write records: %w", err))
			result.Mu.Unlock()
		}
	}()

	wg.Wait()

	i.processUnsignedDuplicates(ctx, result)

	return i.buildImportResult(result, outputDir)
}

// writeRecords writes one JSON file per record into outputDir, named by the
// record's CID (`<cid>.record.json`).
func writeRecords(outputDir string, recordsCh <-chan *corev1.Record) error {
	if err := os.MkdirAll(outputDir, 0o750); err != nil { //nolint:mnd
		// Drain to avoid blocking upstream stages.
		for range recordsCh {
		}

		return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}

	var writeErrs []error

	for record := range recordsCh {
		if record == nil {
			continue
		}

		if err := writeRecord(outputDir, record); err != nil {
			writeErrs = append(writeErrs, err)
		}
	}

	if len(writeErrs) > 0 {
		return errors.Join(writeErrs...)
	}

	return nil
}

func writeRecord(outputDir string, record *corev1.Record) error {
	// Remove importer-only debug fields (e.g. __mcp_debug_source)
	_ = shared.StripImportDebugFields(record)

	cid := record.GetCid()
	if cid == "" {
		return errors.New("failed to derive CID for record")
	}

	payload, err := json.Marshal(record.GetData())
	if err != nil {
		return fmt.Errorf("failed to encode record %q: %w", cid, err)
	}

	fileName := cid + ".record.json"

	// CIDs have no path separators, so Join cannot escape outputDir.
	outputPath := filepath.Join(outputDir, fileName)

	if err := os.WriteFile(outputPath, payload, 0o640); err != nil { //nolint:mnd,gosec
		return fmt.Errorf("failed to write record %q: %w", fileName, err)
	}

	return nil
}
