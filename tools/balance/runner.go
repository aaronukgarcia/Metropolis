package balance

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/headless"
	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

// headlessCellRunner is the production cellRunner (AC-1): it generates a
// synthetic world via harness.synth and drives it through harness.headless —
// the two ALREADY-REGISTERED outbound edges, never a private reimplementation
// of either. It imports both packages so a grep of tools/balance/*.go shows
// the real dependency (the AC-1 false-pass risk is a CLI that only LOOKS like
// it uses them).
type headlessCellRunner struct{}

// HeadlessRunner returns the production cellRunner — the harness.synth +
// harness.headless-backed implementation — for NewSweepRunner.
func HeadlessRunner() cellRunner { return headlessCellRunner{} }

// runCell synthesizes the cell's world at cfg.CitizenCount/sprawl/shape, then
// runs it headless for cfg.Months simulated months at seed. It mirrors
// harness.synth's own runHeadless seam (headless_seam.go): the synth header's
// WorldSeed is passed to headless.Run with no translation step, and the
// generated world is discarded after the run (the engine advances a real
// composition-root simulation; the synth fixture validates the world-shape
// fan-out and supplies the seed).
func (headlessCellRunner) runCell(ctx context.Context, cfg CellConfig, seed uint64) (Measurement, error) {
	correlationID := errs.NewCorrelationID()

	params := synth.Params{
		CitizenCount: cfg.CitizenCount,
		Seed:         seed,
		Sprawl:       cfg.Sprawl,
		NetworkShape: synth.NetworkShape(cfg.NetworkShape),
	}
	var buf bytes.Buffer
	header, err := synth.Generate(correlationID, params, &buf)
	if err != nil {
		return Measurement{}, &cellError{category: CauseSynthGeneration, err: err}
	}

	// headless.Run's CreateBundleDir refuses to write into an existing
	// directory, so OutDir is a not-yet-existing child of a scratch dir this
	// runner owns and discards — a sweep cell has no use for the -out bundle
	// once the Result is read back (mirrors synth.runHeadless).
	scratch, mkErr := os.MkdirTemp("", "balance-sweep-*")
	if mkErr != nil {
		return Measurement{}, &cellError{
			category: CauseHeadlessExitNonzero,
			err:      wrapErr(codeResultsWriteFailed, mkErr, nil),
		}
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	result, runErr := headless.Run(ctx, headless.Config{
		Seed:          uint64(header.WorldSeed),
		Months:        cfg.Months,
		OutDir:        filepath.Join(scratch, "bundle"),
		CorrelationID: correlationID,
	})
	if runErr != nil {
		return Measurement{}, &cellError{category: CauseHeadlessExitNonzero, err: runErr}
	}

	return Measurement{
		// SimulatedMonths is read out of the headless run's end-state header
		// (the in-world calendar month counter after Months simulated months),
		// never re-derived from a wall clock (ICD §7).
		SimulatedMonths: result.Header.GameMonth,
		TicksAdvanced:   result.TicksAdvanced,
		PhaseHookCount:  result.PhaseHookCount,
	}, nil
}
