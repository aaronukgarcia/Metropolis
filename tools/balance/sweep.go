package balance

import (
	"context"
	"errors"
	"io"
	"runtime"
	"sync"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Measurement is what a completed headless run reports back to the harness
// (ICD §3: "metric deltas, invariants" read out of the headless end-state).
type Measurement struct {
	SimulatedMonths int64
	TicksAdvanced   int64
	PhaseHookCount  int
}

// cellRunner runs one cell (generate synthetic world via harness.synth, drive
// it through harness.headless, read back the measurement). The production
// implementation is headlessCellRunner (runner.go), which imports both
// registered outbound edges (AC-1); tests inject a fake to exercise
// classification/determinism without booting a real engine.
type cellRunner interface {
	runCell(ctx context.Context, cfg CellConfig, seed uint64) (Measurement, error)
}

// cellError carries both a failure category (AC-3's closed taxonomy) and the
// underlying (usually registry-sourced) error, so the harness classifies
// without string-matching error codes.
type cellError struct {
	category CauseCategory
	err      error
}

func (e *cellError) Error() string { return e.err.Error() }
func (e *cellError) Unwrap() error { return e.err }

// Options are the non-scenario inputs to a sweep run.
type Options struct {
	WorkerCount int    // 0 = default (runtime.NumCPU(), min 1)
	CommitHash  string // provenance override (AC-12); "" = buildinfo.Commit
	Version     string // provenance override; "" = buildinfo.Version
}

// SweepRunner fans a scenario's (config, seed) grid out through its
// cellRunner and produces a deterministic, fully-accounted result set.
type SweepRunner struct {
	runner  cellRunner
	workers int
	commit  string
	version string
}

// NewSweepRunner constructs a SweepRunner over r.
func NewSweepRunner(r cellRunner, opts Options) *SweepRunner {
	workers := opts.WorkerCount
	if workers <= 0 {
		workers = runtime.NumCPU()
		if workers < 1 {
			workers = 1
		}
	}
	commit := opts.CommitHash
	if commit == "" {
		commit = buildinfo.Commit
	}
	version := opts.Version
	if version == "" {
		version = buildinfo.Version
	}
	return &SweepRunner{runner: r, workers: workers, commit: commit, version: version}
}

// Run executes the sweep for scn and streams the result set to out as NDJSON
// (meta line + sorted records). It returns the SweepResult for callers that
// want the proposal in-process. The returned error is nil iff the result set
// was fully written; a per-cell failure is never a sweep failure — it is
// recorded as that cell's terminal classification (AC-2).
func (sr *SweepRunner) Run(ctx context.Context, scn *Scenario, out io.Writer) (SweepResult, error) {
	// Defensive re-validation: Run never trusts a caller to have validated
	// the scenario first (AC-7 — a malformed scenario must never produce a
	// partial/empty grid that runs anyway).
	if err := scn.Validate(); err != nil {
		return SweepResult{}, err
	}
	cells, err := scn.cells()
	if err != nil {
		return SweepResult{}, err
	}

	records := sr.fanOut(ctx, scn, cells)
	sortRecords(records)

	res := SweepResult{
		ScenarioHash: scn.hash(),
		CommitHash:   sr.commit,
		Version:      sr.version,
		WorkerCount:  sr.workers,
		TotalCells:   len(cells),
		Records:      records,
	}
	if err := writeResults(out, res); err != nil {
		return SweepResult{}, wrapErr(codeResultsWriteFailed, err, nil)
	}
	return res, nil
}

// fanOut runs every cell through the worker pool and collects every attempt's
// record. The output slice is unsorted here — Run sorts it — because results
// merge in ascending (sweep-point, seed) order, never completion order (ICD
// §7).
func (sr *SweepRunner) fanOut(ctx context.Context, scn *Scenario, cells []cell) []CellResult {
	type job struct{ c cell }

	jobs := make(chan job)
	results := make(chan []CellResult)

	var wg sync.WaitGroup
	for w := 0; w < sr.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- sr.runCellAttempts(ctx, scn, j.c)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, c := range cells {
			select {
			case jobs <- job{c}:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var records []CellResult
	for rs := range results {
		records = append(records, rs...)
	}
	return records
}

// runCellAttempts runs one cell through its (possibly retried) attempts,
// returning one record per attempt (AC-4: additive, never substitutive).
func (sr *SweepRunner) runCellAttempts(ctx context.Context, scn *Scenario, c cell) []CellResult {
	if err := validateCellDomain(c.Config); err != nil {
		return []CellResult{{
			Cell:      Cell{Config: c.Config.Config, Seed: c.Seed},
			Status:    StatusRejected,
			Attempt:   0,
			Cause:     CauseInvalidParameter,
			ErrorCode: codeOf(err),
		}}
	}

	maxAttempts := scn.Retries + 1
	out := make([]CellResult, 0, maxAttempts)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		rec := sr.runOnce(ctx, scn, c, attempt)
		out = append(out, rec)
		if rec.Status == StatusCompleted {
			break
		}
	}
	return out
}

// runOnce executes one attempt of one cell and classifies its outcome.
func (sr *SweepRunner) runOnce(ctx context.Context, scn *Scenario, c cell, attempt int) CellResult {
	cellCtx := ctx
	var cancel context.CancelFunc
	if scn.TimeoutMS > 0 {
		cellCtx, cancel = context.WithTimeout(ctx, time.Duration(scn.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	m, err := sr.runner.runCell(cellCtx, c.Config, c.Seed)
	if err != nil {
		var ce *cellError
		if errors.As(err, &ce) {
			switch ce.category {
			case CauseTimeout:
				return CellResult{
					Cell: Cell{Config: c.Config.Config, Seed: c.Seed}, Status: StatusTimedOut,
					Attempt: attempt, Cause: CauseTimeout,
				}
			case CauseSynthGeneration:
				return CellResult{
					Cell: Cell{Config: c.Config.Config, Seed: c.Seed}, Status: StatusCrashed,
					Attempt: attempt, Cause: CauseSynthGeneration, ErrorCode: codeOf(ce.err),
				}
			default: // CauseHeadlessExitNonzero, or any other category
				return CellResult{
					Cell: Cell{Config: c.Config.Config, Seed: c.Seed}, Status: StatusCrashed,
					Attempt: attempt, Cause: CauseHeadlessExitNonzero, ErrorCode: codeOf(ce.err),
				}
			}
		}
		// No explicit category: fall back to the context signal, else crashed.
		if cellCtx.Err() == context.DeadlineExceeded {
			return CellResult{
				Cell: Cell{Config: c.Config.Config, Seed: c.Seed}, Status: StatusTimedOut,
				Attempt: attempt, Cause: CauseTimeout,
			}
		}
		return CellResult{
			Cell: Cell{Config: c.Config.Config, Seed: c.Seed}, Status: StatusCrashed,
			Attempt: attempt, Cause: CauseHeadlessExitNonzero, ErrorCode: codeOf(err),
		}
	}

	rh := realHours(m.SimulatedMonths, c.Config.SecondsPerMonthAt1x)
	return CellResult{
		Cell:                Cell{Config: c.Config.Config, Seed: c.Seed},
		Status:              StatusCompleted,
		Attempt:             attempt,
		SimulatedMonths:     m.SimulatedMonths,
		SecondsPerMonthAt1x: c.Config.SecondsPerMonthAt1x,
		RealHours:           &rh,
	}
}

// codeOf extracts the intended registry code from an error: the error's own
// code, or — while balance.harness's T-range is unregistered (errors.go) and
// errs.New degrades it to MET-F003 — the requested code preserved in
// Ctx["code"]. This lets tests assert the CONTRACT (which code was requested)
// rather than the transient registration state.
func codeOf(err error) string {
	var e *errs.E
	if !errors.As(err, &e) {
		return ""
	}
	if e.Code == "MET-F003" {
		if c, ok := e.Ctx["code"].(string); ok {
			return c
		}
	}
	return e.Code
}
