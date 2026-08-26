package detgate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// advanceChunkTicks is how many daily ticks each AdvanceTicks command in
// RunGate's command loop asks for. 360 = 12 in-game months, comfortably
// under core.MaxAdvanceTicksPerCall (10 in-game years) — chosen so a
// 120-month gate run issues 10 real commands (exercising the envelope +
// dispatch path repeatedly, per this package's doc comment) rather than
// either one enormous command or hundreds of tiny ones.
const advanceChunkTicks = 360

// RunSpec is one labelled run RunGate performs: the same seed and the
// same command log (months*30 daily ticks via AdvanceTicks), at
// WorkerCount POOL-SIM workers. Label is only for report/diagnostic text
// (AC-7's "identifying which two runs disagreed") — it plays no role in
// hashing.
//
// This shape — fixed seed, N labelled runs, hash compare, worker-count
// variant — is the reference structure AC-10 asks later
// determinism-relevant modules to copy for their own shard-count
// invariance tests, rather than each inventing a bespoke harness.
type RunSpec struct {
	Label       string
	WorkerCount int
}

// RunResult is one RunSpec's outcome: the sha256 hex digest RunGate
// computed for that run's world snapshot (see doc.go's "Hashing" note).
type RunResult struct {
	Label       string
	WorkerCount int
	Hash        string
}

// GateReport is RunGate's verdict: every run's hash, plus whether they
// all agree. A red gate (Verdict == false) is auto-P0 per GR#21 — see
// gate_test.go's TestDeterminismGate for how the CI-facing test turns
// this into a failed build.
type GateReport struct {
	Seed   uint64
	Months int
	Runs   []RunResult

	// Verdict is true iff every run in Runs produced the same hash as
	// Runs[0]. A GateReport with fewer than two Runs is never produced —
	// RunGate rejects that at construction time (ErrTooFewRuns).
	Verdict bool

	// Mismatches names every run whose hash disagreed with the baseline
	// (Runs[0]), one human-readable line per disagreement (AC-7): which
	// two runs, what seed, what month count, both hashes. Empty iff
	// Verdict is true.
	Mismatches []string
}

// RunGate is the determinism-gate reference runner (package doc.go).
// It performs one full run per spec — booting a fresh engine.core.Engine
// at (seed, spec.WorkerCount), wiring the full composed hook set through
// compose.Wire (the single real registration path — BUG-375), driving it
// months*30 daily ticks through the real protocol.Command path, then
// hashing its Snapshot — and reports whether every run's hash agrees.
//
// correlationID is the root correlation ID this gate run is filed under
// (GR#1); RunGate derives a distinct sub-ID per command and per run from
// it so every rejected command traces back to exactly which run/command
// produced it. Pass protocol.NewCorrelationID() if the caller has no
// existing correlation chain to attach to (gate_test.go does this).
//
// RunGate itself never reads the wall clock and never ranges over a Go
// map on any path that feeds a hash or the verdict (AC-9) — see doc.go.
func RunGate(correlationID string, seed uint64, months int, specs []RunSpec) (GateReport, error) {
	if months <= 0 {
		return GateReport{}, errs.New(ErrInvalidMonths, correlationID, map[string]any{"months": months})
	}
	if len(specs) < 2 {
		return GateReport{}, errs.New(ErrTooFewRuns, correlationID, map[string]any{"runs": len(specs)})
	}

	report := GateReport{Seed: seed, Months: months}
	for _, spec := range specs {
		hash, err := runOnce(correlationID, seed, months, spec)
		if err != nil {
			return GateReport{}, err
		}
		report.Runs = append(report.Runs, RunResult{Label: spec.Label, WorkerCount: spec.WorkerCount, Hash: hash})
	}

	report.Verdict, report.Mismatches = evaluate(seed, months, report.Runs)
	return report, nil
}

// evaluate compares every run's hash to the first run's (the baseline)
// and reports the pass/fail verdict plus one diagnostic line per
// disagreement (AC-7). Split out from RunGate so gate_test.go's negative
// control (AC "a gate that can't fail is no gate") can exercise the
// comparison logic directly against deliberately corrupted hashes,
// without needing a second full 120-month engine run just to prove the
// failure path fires.
//
// Walks runs as a plain slice in caller order — never a map — so this
// function's own output is never subject to Go map iteration order
// (AC-9).
func evaluate(seed uint64, months int, runs []RunResult) (verdict bool, mismatches []string) {
	if len(runs) == 0 {
		return true, nil
	}
	baseline := runs[0]
	verdict = true
	for _, r := range runs[1:] {
		if r.Hash != baseline.Hash {
			verdict = false
			mismatches = append(mismatches, fmt.Sprintf(
				"seed %d, %d months: run %q (POOL-SIM=%d) hash=%s != run %q (POOL-SIM=%d) hash=%s",
				seed, months, baseline.Label, baseline.WorkerCount, baseline.Hash, r.Label, r.WorkerCount, r.Hash,
			))
		}
	}
	return verdict, mismatches
}

// runOnce boots one fresh Engine at (seed, spec.WorkerCount), advances it
// months*30 daily ticks entirely through the real protocol.Command path
// (never Engine.AdvanceTicks directly — see doc.go), then hashes its
// Snapshot plus the BROAD composed-state digest (BUG-375 r3:
// Composition.StateDigest observes EVERY composed module observable a hook
// can mutate — finance ledger per-account balances, crime, refuse, and
// compose's conservation ledgers — not the citizen population alone). The
// gate MUST include this digest to detect when a hook is skipped/disabled
// AND when any hook contains nondeterminism: Snapshot alone only captures
// Tick/Month/Seed (deterministic regardless of whether hooks ran), and the
// earlier r2 PopulationHash-only probe observed population-class state
// only, letting a finance/crime/refuse ordering bug ship green.
func runOnce(rootCorrelationID string, seed uint64, months int, spec RunSpec) (string, error) {
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(spec.WorkerCount))

	// BUG-375: the gate hashes the COMPOSED engine — compose.Wire is the
	// single wiring path (feat.compositionroot AC-1/AC-13) every runnable
	// top reaches real hooks through, so a bare-core boot here would hash
	// zero hooks while production runs compose's full registration order
	// and one nondeterministic ordering bug in any hook would ship green.
	// Wiring failure aborts the run with compose's registry-sourced error,
	// never a silent stub gate.
	comp, err := compose.Wire(e, nil)
	if err != nil {
		return "", err
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// RunCommandLoop now returns an error (engine.headless.md AC-4/AC-7,
	// [MOD-015]) distinguishing a clean ctx-cancelled shutdown from a
	// transport that closed prematurely. RunGate is the one documented
	// exception to "observe this return" (AC-7's "Out of scope" note): it
	// already controls cancel()/transport.Close() ordering deterministically
	// below (cancel(); transport.Close(), no other goroutine can close
	// Commands() first), so the premature-close case this return value
	// exists to catch is structurally unreachable here — see
	// engine.headless.md AC-7 and RunCommandLoop's own "Exit contract" doc
	// comment (internal/engine/core/commands.go) for the full argument.
	go func() { _ = e.RunCommandLoop(ctx, transport) }()

	totalTicks := int64(months) * core.DailyTicksPerMonth
	remaining := totalTicks
	seq := 0
	for remaining > 0 {
		n := int64(advanceChunkTicks)
		if remaining < n {
			n = remaining
		}
		seq++
		corr := protocol.CorrelationID(fmt.Sprintf("%s-%s-advance-%d", rootCorrelationID, spec.Label, seq))

		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   corr,
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: n},
		}
		if err := transport.SendCommand(cmd); err != nil {
			return "", errs.Wrap(ErrCommandRejected, string(corr), err, map[string]any{"run": spec.Label, "n": n})
		}

		result := <-transport.Results()
		if !result.Accepted {
			code := ""
			if result.Error != nil {
				code = result.Error.Code
			}
			return "", errs.New(ErrCommandRejected, string(corr), map[string]any{"run": spec.Label, "n": n, "engineErrorCode": code})
		}
		remaining -= n
	}

	// Stop the command loop before Snapshot: Snapshot is engine.core's
	// own T-PERSIST hook, called directly rather than routed through the
	// protocol command path (see doc.go) — there is no reason to keep
	// the loop goroutine alive once every AdvanceTicks command for this
	// run has been accepted.
	cancel()
	_ = transport.Close()

	snapCorr := fmt.Sprintf("%s-%s-snapshot", rootCorrelationID, spec.Label)
	var buf bytes.Buffer
	header, err := e.Snapshot(&buf, snapCorr)
	if err != nil {
		return "", err // already registry-sourced by Engine.Snapshot
	}

	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", errs.Wrap(ErrSnapshotEncodeFailed, snapCorr, err, map[string]any{"run": spec.Label})
	}

	h := sha256.New()
	h.Write(headerBytes)
	h.Write(buf.Bytes())
	// BUG-375 r3: hash the BROAD composed-state digest, not PopulationHash
	// alone. Snapshot alone captures only Tick/Month/Seed (deterministic
	// regardless of whether any hook ran); PopulationHash (r2) added the
	// citizen-store fingerprint but observes ONLY population-class state —
	// an independent round proved conserving map-order nondeterminism in
	// financeHook diverged treasury ~54,000 micropounds between two
	// same-seed runs while PopulationHash stayed byte-identical and the gate
	// PASSED. Composition.StateDigest observes EVERY composed module
	// observable a hook can mutate — the finance ledger's per-account
	// balances, crime threat/safety/per-type figures, refuse per-stream
	// tonnage, and compose's own conservation ledgers — so a
	// nondeterministic ordering bug in ANY hook (not only a population hook)
	// changes this hash and reddens the gate. See
	// compose.Composition.StateDigest for exactly what it covers and its
	// known limits.
	digest := comp.StateDigest()
	h.Write(digest[:])
	return hex.EncodeToString(h.Sum(nil)), nil
}
