package synth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/harness/headless"
)

// # Status: MOD-015 (harness.headless) landed mid-dispatch
//
// AC-2/AC-4/AC-6 ask for a generated world to be "consumable by
// harness.headless ... without a translation step" and for RunPerf to
// run "under harness.headless". At the START of this dispatch,
// internal/harness/headless/ had no buildable Go package on disk (only
// its code.json entry and a pre-registered error range existed) — this
// package was first built with a same-shape stand-in (a HeadlessRunner
// contract driving engine.core directly, mirroring engine.detgate's
// RunGate) and the gap was logged as an assumption and escalated to
// Bill. MOD-015 landed later the same day. This file was rewritten to
// call the real package directly — the stand-in is gone, not merely
// deprecated, because keeping two implementations of the same seam
// alive is exactly the class of drift GR#3 exists to prevent.
//
// runHeadless is this package's single call site into
// headless.Run — RunPerf (perf.go) uses it, and
// TestGeneratedWorldFeedsHeadlessRunnerWithoutTranslation
// (headless_seam_test.go) exercises it directly for AC-2.

// runHeadless drives hdr.WorldSeed through the real harness.headless
// package for months simulated months, returning the wall-clock time
// spent inside headless.Run, the ticks it actually advanced, and the
// per-phase timing headless.Run's own -report stream already computes
// (report.go's phaseTimingRecord) — parsed back out here rather than
// re-derived via a second core.WithPhaseObserver of this package's own,
// since headless.Config exposes no separate observer hook and computing
// phase timing twice would be exactly the "two implementations of one
// seam" GR#3 forbids.
//
// hdr.WorldSeed is passed to headless.Config.Seed with NO translation
// step (AC-2): both are the same seed Generate produced, read and
// carried through unchanged — the property AC-2's check asks for.
func runHeadless(correlationID string, hdr serialize.Header, months int) (tickElapsed time.Duration, totalTicks int64, timings []PhaseTiming, err error) {
	if months <= 0 {
		return 0, 0, nil, errs.New(codeInvalidMonths, correlationID, map[string]any{"months": months})
	}

	// headless.Run's CreateBundleDir refuses to write into a directory
	// that already exists (never silently merge into a stale bundle),
	// so OutDir must be a not-yet-existing child of a scratch directory
	// this function owns and cleans up — a perf/measurement run has no
	// use for the -out bundle itself once RunPerf has read
	// Result.TicksAdvanced back.
	scratch, mkErr := os.MkdirTemp("", "synth-perf-*")
	if mkErr != nil {
		return 0, 0, nil, errs.Wrap(codePerfRunFailed, correlationID, mkErr, map[string]any{
			"n": months, "engineErrorCode": "N/A (mkdir failed)",
		})
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	var report bytes.Buffer
	cfg := headless.Config{
		Seed:          uint64(hdr.WorldSeed),
		Months:        int64(months),
		OutDir:        filepath.Join(scratch, "bundle"),
		Report:        &report,
		CorrelationID: correlationID,
	}

	start := time.Now()
	result, runErr := headless.Run(context.Background(), cfg)
	elapsed := time.Since(start)
	if runErr != nil {
		return 0, 0, nil, errs.Wrap(codePerfRunFailed, correlationID, runErr, map[string]any{
			"n": months, "engineErrorCode": runErr.Error(),
		})
	}

	return elapsed, result.TicksAdvanced, parsePhaseTimings(report.Bytes()), nil
}

// reportLine is the subset of headless's phaseTimingRecord (report.go)
// this package reads back — a private, minimal mirror of that JSON
// shape's field names, not an import of an unexported type (harness.
// headless intentionally keeps phaseTimingRecord unexported; NDJSON
// lines on an io.Writer are its public contract for this data, per its
// own doc comment: "-report is best-effort operator telemetry").
type reportLine struct {
	Type      string `json:"type"`
	Phase     string `json:"phase"`
	ElapsedMs int64  `json:"elapsedMs"`
}

// parsePhaseTimings reconstructs per-phase elapsed durations from
// headless.Run's -report NDJSON stream: each phaseTiming line carries
// ElapsedMs = wall-clock milliseconds since Run() began, so the time
// spent IN one phase is the gap between that phase's line and the NEXT
// line's ElapsedMs. Lines are read in stream order (never re-sorted by
// phase name or any other key), matching this package's standing rule
// against feeding observable output through Go map iteration order.
//
// The final phase's own interval is left unclosed (no trailing line
// exists to compute its end from) — headless's own report.go doc
// comment already states this stream is best-effort telemetry, not a
// correctness-bearing artifact, so an intentionally incomplete last
// interval is an acceptable, documented gap rather than a defect.
func parsePhaseTimings(data []byte) []PhaseTiming {
	totals := map[core.PhaseKind]*PhaseTiming{}
	var order []core.PhaseKind

	var lastPhase core.PhaseKind
	var lastMs int64
	haveLast := false

	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var line reportLine
		if decErr := dec.Decode(&line); decErr != nil {
			break
		}
		if line.Type != "phaseTiming" {
			continue
		}
		if haveLast {
			d := time.Duration(line.ElapsedMs-lastMs) * time.Millisecond
			if t, ok := totals[lastPhase]; ok {
				t.Total += d
				t.Calls++
			} else {
				totals[lastPhase] = &PhaseTiming{Phase: lastPhase, Total: d, Calls: 1}
				order = append(order, lastPhase)
			}
		}
		lastPhase = core.PhaseKind(line.Phase)
		lastMs = line.ElapsedMs
		haveLast = true
	}

	var out []PhaseTiming
	for _, k := range order {
		out = append(out, *totals[k])
	}
	return out
}
