package debug

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// BuildInfo is AC-1's build & code pane: every field sourced from
// internal/foundation/buildinfo's -ldflags-injected package vars (plus
// GoVersion, read from the running toolchain via runtime.Version() —
// dynamic, not hand-maintained, so it satisfies AC-1's "never hand-
// maintained" the same way the ldflags vars do) — see build.go's
// collectBuildInfo, the single place these fields are read.
type BuildInfo struct {
	Version      string
	Commit       string
	Branch       string
	BuildTimeUTC string
	GoVersion    string

	// BuildHost mirrors M0-ENG §3's "build host" field. Neither
	// internal/foundation/buildinfo nor build.ps1 currently inject a
	// build-host value (confirmed by reading both — build.ps1 only
	// passes Version/Commit/Branch/BuildTime); this is an upstream gap in
	// foundation.repo, not something ui.screen.debug can fabricate
	// without violating AC-1's "never hand-maintained" rule. BuildHost is
	// therefore always "" and BuildHostAvailable is always false until
	// buildinfo grows a BuildHost var — flagged to the lead as a known
	// gap rather than silently working around it with a literal.
	BuildHost          string
	BuildHostAvailable bool
}

// MemoryBudgetRegion is one row of the §1.3 memory budget table
// (docs/METROPOLIS-MASTER-v2.1.md lines 828-838) — spec-sourced data
// (GR#15), never invented. See MemoryBudgetTable.
type MemoryBudgetRegion struct {
	Name        string
	BudgetBytes uint64
	Notes       string
}

// gb is the decimal-GB byte multiplier the §1.3 table's figures use
// (e.g. "8 GB", "0.15 GB") — not GiB.
const gb = 1_000_000_000

// MemoryBudgetTable is the complete §1.3 memory budget (20 GB envelope),
// transcribed verbatim from the spec table so every figure here is
// traceable back to docs/METROPOLIS-MASTER-v2.1.md rather than
// hardcoded independently (GR#15). uiProcessDomainBudget below is the
// specific row F12's own process-memory fields (heap in-use, sys, arena
// occupancy) are measured against, since F12 runs inside the UI process
// domain, not the sim.
var MemoryBudgetTable = []MemoryBudgetRegion{
	{Name: "Citizen shards (hot+warm resident)", BudgetBytes: 8 * gb, Notes: "~250 B/citizen ⇒ ~32 M resident; beyond that cold shards page to disk (mmap), LRU"},
	{Name: "World cells + networks + route cache", BudgetBytes: 4 * gb, Notes: "route cache is the big line; capped LRU, deterministic contents-independent (cache only affects speed, never results)"},
	{Name: "Firms, markets, logistics state", BudgetBytes: 1_500_000_000, Notes: ""},
	{Name: "Scratch arenas (phase-local)", BudgetBytes: 2 * gb, Notes: "pre-sized at world-load from world dimensions"},
	{Name: "UI process domain", BudgetBytes: 150_000_000, Notes: "UI-SPEC §5, holds views never world"},
	{Name: "Snapshot COW headroom", BudgetBytes: 2 * gb, Notes: "T-PERSIST marshals while sim continues"},
	{Name: "OS + slack", BudgetBytes: 2 * gb, Notes: ""},
}

// uiProcessDomainBudget is the MemoryBudgetTable row F12's own heap/sys/
// arena fields render against (RuntimeMetrics is this process's own
// memory, not the sim's) — resolved once at package init rather than
// re-scanning MemoryBudgetTable on every render.
var uiProcessDomainBudget = mustFindBudget("UI process domain")

func mustFindBudget(name string) MemoryBudgetRegion {
	for _, r := range MemoryBudgetTable {
		if r.Name == name {
			return r
		}
	}
	panic("debug: MemoryBudgetTable missing region " + name)
}

// RuntimeMetrics is AC-2's runtime stats pane input. Callers supply a
// RuntimeMetricsFunc (WithRuntimeSource) that produces one of these per
// Collect call; DefaultRuntimeMetricsProvider (runtime.go) is the
// default, filling what it can read directly from the Go runtime.
//
// Fields with no live source wired yet in Sprint 1 (SimDate, Speed,
// TickNumber, the three queue depths, InputEchoLatencyP99Micros) are
// legitimately zero-valued rather than invented — Render still labels
// them, it does not hide them, so a zero reads as "not yet wired," not
// as a silently wrong measurement.
type RuntimeMetrics struct {
	UptimeSeconds      float64
	RealElapsedSeconds float64
	SimDate            string
	Speed              float64
	TickNumber         int64

	HeapInUseBytes      uint64
	SysBytes            uint64
	GCPauseP99Micros    uint64
	ArenaOccupancyBytes uint64

	GoroutineCount int

	InputQueueDepth   int
	DeltaQueueDepth   int
	PersistQueueDepth int

	InputEchoLatencyP99Micros uint64
}

// RuntimeMetricsFunc produces one RuntimeMetrics snapshot. See
// DefaultRuntimeMetricsProvider for the built-in implementation.
type RuntimeMetricsFunc func() RuntimeMetrics

// ErrorTailFunc produces the current error-tail source entries
// (oldest-first, any length — Collect takes the last 50 itself, per
// AC-6). Defaults to errs.Recent. Pass nil to WithErrorTailSource to
// deliberately mark the pane unavailable (AC-11).
type ErrorTailFunc func() []errs.Entry

// DebugFlagFunc reports whether the feat.debugmode runtime switch is
// currently on (AC-10). debug.Screen never decides this itself — it
// only reads whatever this func reports.
type DebugFlagFunc func() bool

// PhaseSeries is one phase's row in the AC-8 sparkline pane: up to the
// last 60 RecordTickCost samples for that phase (oldest-first), sourced
// from Registry.TickCostHistory(key) where key is the phase's name — see
// phase.go's monthlyPhaseOrder for the fixed key list and the GR#20 note
// on why it's a local mirror rather than an internal/engine import.
type PhaseSeries struct {
	Phase     string
	Micros    []uint64
	Available bool
	// Reason is set (AC-11-style) when Available is false — e.g. the
	// registry has no entry registered under this phase's key yet.
	Reason string
}

// microsAsFloat64 converts s.Micros to []float64 for widgets.Sparkline,
// which operates on float64 series. A nil/empty Micros renders as an
// all-blank sparkline (Sparkline's own documented degenerate case).
func (s PhaseSeries) microsAsFloat64() []float64 {
	out := make([]float64, len(s.Micros))
	for i, v := range s.Micros {
		out[i] = float64(v)
	}
	return out
}

// BoWItem is one open/in-progress row in the AC-9 BoW tab.
type BoWItem struct {
	Code     string
	Title    string
	Priority string
}

// BoWSummary is AC-9's read-only BoW tab content: open-item counts by
// priority and what's in_progress — matching the checkin startup
// summary's shape (CLAUDE.md).
type BoWSummary struct {
	// OpenByPriority maps a priority label ("P0".."P3") to its open
	// count. Rendered in a fixed P0->P3 order (render.go), never Go map
	// iteration order.
	OpenByPriority map[string]int
	InProgress     []BoWItem
}

// BoWSource is a read-only query against the metro BOW (production) or a
// mock (tests) — AC-9's "sourced from a read-only query ... or a mocked
// equivalent for the test." debug.Screen never mutates the BOW; there is
// deliberately no write method on this interface.
type BoWSource interface {
	Summary() (BoWSummary, error)
}

// registryRows is a small local alias so screen.go/render.go read a bit
// more clearly; it is exactly registry.ModuleEntry, reused (GR#3) rather
// than re-declared with duplicate fields.
type registryRow = registry.ModuleEntry
