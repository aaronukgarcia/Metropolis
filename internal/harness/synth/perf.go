package synth

import (
	"bytes"
	"runtime"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// PhaseTiming is one phase's cumulative wall-clock cost across every
// tick RunPerf drove (AC-4: "per-phase ... timing"), reconstructed from
// harness.headless's own -report stream (headless_seam.go's
// parsePhaseTimings).
type PhaseTiming struct {
	Phase core.PhaseKind
	Total time.Duration
	Calls int64
}

// PerfResult is RunPerf's measurement output (AC-4, AC-5): per-phase and
// total timing for a fixed-months run of a generated synthetic city,
// plus a work-based counter (TotalTicks) that travels alongside the
// wall-clock figures — see baseline.go's CompareToBaseline doc comment
// for why a work counter matters as much as the timing does.
type PerfResult struct {
	Preset       string // caller-supplied label, e.g. "1M" / "10M"
	CitizenCount int64
	Seed         uint64
	Months       int
	TotalTicks   int64

	GenerationTime time.Duration // cost of Generate itself (AC-1b's O(citizenCount) work)
	TickTime       time.Duration // total headless.Run wall time across all months
	PerMonthTick   time.Duration // TickTime / Months — the figure AC-6/AC-10 gate on
	PhaseTimings   []PhaseTiming

	// PhaseHookCount is BUG-034's specific, named defence: "nothing
	// currently stops someone citing a '10M-citizen tick cost' from
	// today's runs, which would be pure walking-skeleton overhead
	// wearing a simulation label." Every PerfResult carries this figure
	// so a reader of TickTime/PerMonthTick never has to trust a claim
	// about what the run actually measured — the context travels WITH
	// the number. See phasehooks.go's PhaseHookCountInHeadlessPath doc
	// comment for exactly what this is and is not (a manually-asserted,
	// grep-guarded fact about harness.headless's engine construction,
	// not a live introspection).
	PhaseHookCount int

	// AllocBytes/AllocCount measure ONLY the tick-driving call
	// (runHeadless/headless.Run) — this is the figure that becomes
	// meaningful once citizens land and PhaseHook hooks start doing
	// real per-tick work (Sprint 3+). GenerationAllocBytes/
	// GenerationAllocCount measure ONLY Generate's own cost, which is
	// the dominant, O(citizenCount) allocator load TODAY (see doc.go's
	// "Generated content is a Sprint-1-skeleton stand-in" section) —
	// kept as a genuinely separate figure, not folded into the tick
	// counters, for the identical reason GenerationTime and TickTime are
	// already kept separate: so a generation regression and a tick
	// regression can never be mistaken for each other, and so the tick
	// figure stays a fair basis for comparison once real simulation
	// content exists without any re-plumbing of this struct.
	AllocBytes uint64 // runtime.MemStats.TotalAlloc delta across the headless.Run call
	AllocCount uint64 // runtime.MemStats.Mallocs delta across the headless.Run call

	GenerationAllocBytes uint64 // runtime.MemStats.TotalAlloc delta across the Generate call
	GenerationAllocCount uint64 // runtime.MemStats.Mallocs delta across the Generate call
}

// RunPerf generates params' synthetic city, then drives it through the
// REAL harness.headless package (headless_seam.go's runHeadless, which
// calls headless.Run) for months simulated months (AC-4), recording
// per-phase and total monthly-tick timing plus the tick-count work
// counter. See headless_seam.go's package-level "Status" note for the
// history: this package was first built against a same-shape stand-in
// because MOD-015 was not yet buildable, then rewritten to call the real
// package once it landed the same day.
func RunPerf(correlationID string, p Params, preset string, months int) (PerfResult, error) {
	if months <= 0 {
		return PerfResult{}, errs.New(codeInvalidMonths, correlationID, map[string]any{"months": months})
	}
	if err := ValidateParams(correlationID, p); err != nil {
		return PerfResult{}, err
	}

	var genMemBefore, genMemAfter runtime.MemStats
	runtime.ReadMemStats(&genMemBefore)
	genStart := time.Now()
	var buf bytes.Buffer
	header, err := Generate(correlationID, p, &buf)
	if err != nil {
		return PerfResult{}, err
	}
	genElapsed := time.Since(genStart)
	runtime.ReadMemStats(&genMemAfter)

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	tickElapsed, totalTicks, timings, err := runHeadless(correlationID, header, months)
	runtime.ReadMemStats(&memAfter)
	if err != nil {
		return PerfResult{}, err
	}

	return PerfResult{
		Preset:               preset,
		CitizenCount:         p.CitizenCount,
		Seed:                 p.Seed,
		Months:               months,
		TotalTicks:           totalTicks,
		GenerationTime:       genElapsed,
		TickTime:             tickElapsed,
		PerMonthTick:         tickElapsed / time.Duration(months),
		PhaseTimings:         timings,
		PhaseHookCount:       PhaseHookCountInHeadlessPath(),
		AllocBytes:           memAfter.TotalAlloc - memBefore.TotalAlloc,
		AllocCount:           memAfter.Mallocs - memBefore.Mallocs,
		GenerationAllocBytes: genMemAfter.TotalAlloc - genMemBefore.TotalAlloc,
		GenerationAllocCount: genMemAfter.Mallocs - genMemBefore.Mallocs,
	}, nil
}
