package headless

import (
	"encoding/json"
	"io"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// reportKindPhaseTiming/reportKindInvariant name the fixed "type" field
// every NDJSON line this file writes carries (AC-5/AC-6), so a consumer
// of the -report stream dispatches on one fixed string field rather than
// guessing from shape.
const (
	reportKindPhaseTiming = "phaseTiming"
	reportKindInvariant   = "invariant"
)

// checkStub names the placeholder invariant check the reporting hook
// runs (AC-6's "out of scope: the real invariant checker's actual
// conservation assertions — MOD-019, a separate Sprint-3 item; this item
// only needs the reporting hook and a wired-in stub check"). It always
// reports Passed: true — it asserts nothing about simulation content
// today. MOD-019 is expected to replace what runs through this hook,
// not the hook's shape itself.
const checkStub = "stub: reporting hook wired, no real invariant asserted yet (MOD-019)"

// phaseTimingRecord is one line of the -report stream's phase-timing
// half (AC-5). Field order is fixed and part of this type's JSON
// contract (AC-11): encoding/json marshals struct fields in declaration
// order, never map order, so two runs of the same (seed, months,
// scenario) never diverge in FIELD ORDER even though ElapsedMs may
// legitimately differ run to run — see this file's ElapsedMs doc
// comment for why that specific field is exempt from GR#21's
// byte-determinism requirement.
type phaseTimingRecord struct {
	Type  string `json:"type"`
	Tick  int64  `json:"tick"`
	Month int64  `json:"month"`
	Phase string `json:"phase"`

	// ElapsedMs is wall-clock milliseconds since this run's Run() call
	// began (AC-10: operator "elapsed real time so far" progress
	// reporting ONLY). It is never read back into simulation state and
	// never appears in the -out snapshot bundle — only in this optional,
	// human/CI-facing telemetry stream.
	ElapsedMs int64 `json:"elapsedMs"`
}

// invariantRecord is one line of the -report stream's invariant half
// (AC-6). See checkStub's doc comment for why Passed is always true
// today.
type invariantRecord struct {
	Type   string `json:"type"`
	Tick   int64  `json:"tick"`
	Month  int64  `json:"month"`
	Check  string `json:"check"`
	Passed bool   `json:"passed"`
}

// reportWriter streams phaseTimingRecord/invariantRecord lines as NDJSON
// to w. A nil w (the common case — -report was not requested) makes
// every method here a no-op, so callers never need to nil-check before
// wiring this into core.WithPhaseObserver.
type reportWriter struct {
	w     io.Writer
	start time.Time
	enc   *json.Encoder

	// err holds the FIRST encode/write failure this reportWriter observed,
	// if any. -report is best-effort operator telemetry, not part of this
	// package's correctness contract (only -out is, per AC-3/AC-9) — a
	// failing report stream must never abort or corrupt a headless run
	// that is otherwise succeeding (GR#1 "log, don't lose" is served by
	// surfacing err to the caller via Result.ReportWriteErr, so it is
	// still visible, just non-fatal; see run.go). Only the first failure
	// is kept — a flaky writer failing every tick would otherwise need
	// this package to also bound how many errors it accumulates, which is
	// unnecessary complexity for a single "something went wrong, look at
	// the stream" signal.
	err error
}

// newReportWriter constructs a reportWriter over w. Pass nil for "no
// -report requested" — every subsequent method call becomes a no-op.
func newReportWriter(w io.Writer) *reportWriter {
	if w == nil {
		return &reportWriter{}
	}
	return &reportWriter{w: w, start: time.Now(), enc: json.NewEncoder(w)}
}

// phaseObserver adapts this reportWriter into a core.PhaseObserver
// (AC-5/AC-6). core.PhaseObserver is called once per phase, immediately
// before that phase's hooks run, in the fixed pipeline order
// (phase.go's doc comment) — PhaseDailyTick fires exactly once per daily
// tick regardless of whether that tick also completes a calendar month,
// so keying the invariant line off kind == core.PhaseDailyTick gives
// AC-6's "invariant reports are emitted every tick" exactly one
// invariant line per tick, no more and no fewer.
func (r *reportWriter) phaseObserver() core.PhaseObserver {
	return func(kind core.PhaseKind, tick, month int64) {
		if r.enc == nil {
			return
		}
		elapsed := time.Since(r.start).Milliseconds()
		if err := r.enc.Encode(phaseTimingRecord{
			Type: reportKindPhaseTiming, Tick: tick, Month: month,
			Phase: string(kind), ElapsedMs: elapsed,
		}); err != nil && r.err == nil {
			r.err = err
		}
		if kind != core.PhaseDailyTick {
			return
		}
		if err := r.enc.Encode(invariantRecord{
			Type: reportKindInvariant, Tick: tick, Month: month,
			Check: checkStub, Passed: true,
		}); err != nil && r.err == nil {
			r.err = err
		}
	}
}
