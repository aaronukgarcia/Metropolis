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

	AllocBytes uint64 // runtime.MemStats.TotalAlloc delta across the headless.Run call
	AllocCount uint64 // runtime.MemStats.Mallocs delta across the headless.Run call
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

	genStart := time.Now()
	var buf bytes.Buffer
	header, err := Generate(correlationID, p, &buf)
	if err != nil {
		return PerfResult{}, err
	}
	genElapsed := time.Since(genStart)

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	tickElapsed, totalTicks, timings, err := runHeadless(correlationID, header, months)
	runtime.ReadMemStats(&memAfter)
	if err != nil {
		return PerfResult{}, err
	}

	return PerfResult{
		Preset:         preset,
		CitizenCount:   p.CitizenCount,
		Seed:           p.Seed,
		Months:         months,
		TotalTicks:     totalTicks,
		GenerationTime: genElapsed,
		TickTime:       tickElapsed,
		PerMonthTick:   tickElapsed / time.Duration(months),
		PhaseTimings:   timings,
		AllocBytes:     memAfter.TotalAlloc - memBefore.TotalAlloc,
		AllocCount:     memAfter.Mallocs - memBefore.Mallocs,
	}, nil
}
