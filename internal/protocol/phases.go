package protocol

// The monthly phase-name vocabulary, shared by BOTH sides of the
// Engine<->UI seam at compile time (BUG-382).
//
// internal/engine/core owns the phase PIPELINE — the barrier semantics,
// hook registration, and execution order (§3, AC-3, AC-16). But the
// phase NAMES are wire-surface vocabulary: the F12 debug screen labels
// its per-phase series with them, and GR#20 forbids internal/ui from
// importing internal/engine in non-test code. Before BUG-382 the UI kept
// a hand-copied literal of the six names, so a rename compiled cleanly
// everywhere and only failed later as a red CI drift test.
//
// These constants are that shared vocabulary's single source. Both
// consumers derive from them AT COMPILE TIME:
//
//   - engine/core aliases its typed PhaseKind constants to these strings,
//     so renaming one here breaks engine.core's build immediately.
//   - ui/screens/debug builds its series keys from
//     MonthlyPhaseOrderNames(), so any name/order change here breaks the
//     UI's build immediately.
//
// ADDING, REMOVING, REORDERING or RENAMING a phase therefore starts
// here, and every dependent package fails to compile until it is
// updated — drift is a compiler error, not a CI run. A belt-and-braces
// test in engine/core additionally asserts this package's ordering
// matches core's own pipeline array, so the two can never silently
// diverge in sequence either.

const (
	// PhaseDailyTick is the single daily-tick phase (§8's logistics
	// resolution), run once per AdvanceTicks-driven day.
	PhaseDailyTick = "daily-tick"

	// PhaseProduction through PhaseFinance are the six fixed monthly
	// phases, listed in pipeline order (§3): production -> logistics
	// settlement -> consumption & shortfall -> population -> land value
	// & decay -> finance.
	PhaseProduction           = "production"
	PhaseLogisticsSettlement  = "logistics-settlement"
	PhaseConsumptionShortfall = "consumption-shortfall"
	PhasePopulation           = "population"
	PhaseLandValueDecay       = "land-value-decay"
	PhaseFinance              = "finance"
)

// monthlyPhaseOrderNames is the fixed, documented monthly execution
// order, expressed over the constants above (§3, AC-3, AC-16).
var monthlyPhaseOrderNames = [...]string{
	PhaseProduction,
	PhaseLogisticsSettlement,
	PhaseConsumptionShortfall,
	PhasePopulation,
	PhaseLandValueDecay,
	PhaseFinance,
}

// MonthlyPhaseOrderNames returns a fresh copy of the fixed monthly
// phase order. Callers may freely mutate the returned slice without
// touching this package's state (same defensive-copy pattern as
// engine/core.MonthlyPhaseOrder). Not for per-tick hot paths — call at
// boot/snapshot time.
func MonthlyPhaseOrderNames() []string {
	out := make([]string, len(monthlyPhaseOrderNames))
	copy(out, monthlyPhaseOrderNames[:])
	return out
}
