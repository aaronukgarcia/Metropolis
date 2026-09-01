package converge

// Tier names one of docs/planning/phase3-convergence-plan.md §2's three
// parity bars, from strongest to weakest.
type Tier int

const (
	// TierExact requires ref == candidate, exactly, at every compared
	// tick. The target for integer-modeled, deterministic, non-
	// stochastic quantities (treasury balance, counts) once a units/
	// rounding convention is agreed (BUG-355).
	TierExact Tier = iota

	// TierBounded requires |ref - candidate| <= Tolerance.Epsilon at
	// every compared tick. For continuous/aggregate quantities where a
	// legitimate rounding-order difference is expected and bounded.
	TierBounded

	// TierDistribution requires the trailing-window MEAN of candidate
	// to be within Tolerance.BandPct of the trailing-window mean of ref,
	// evaluated once enough history has accumulated (Tolerance.Window
	// samples). For per-agent stochastic domains (population, births/
	// deaths, migration) that can never tick-match under any seeding —
	// gated on aggregate trajectory, never on a single tick's value.
	TierDistribution
)

// String names the tier, for Report diff text.
func (t Tier) String() string {
	switch t {
	case TierExact:
		return "exact"
	case TierBounded:
		return "bounded"
	case TierDistribution:
		return "distribution"
	default:
		return "unknown"
	}
}

// Tolerance is one field's parity bar. Which sub-fields matter depends
// on Tier: TierExact uses none of them, TierBounded uses only Epsilon,
// TierDistribution uses only Window and BandPct.
type Tolerance struct {
	Tier Tier

	// Epsilon is TierBounded's absolute bound: a diff with
	// |ref-candidate| <= Epsilon passes. Zero-valued Epsilon under
	// TierBounded is equivalent to TierExact for that field — allowed,
	// but TierExact should be used instead for clarity.
	Epsilon int64

	// Window is TierDistribution's trailing-sample count the mean is
	// computed over. Must be >= 1; a tick is only evaluated once at
	// least Window samples (this tick included) have accumulated in
	// BOTH trajectories, so an early, still-warming-up tick is never
	// misreported as a divergence.
	Window int

	// BandPct is TierDistribution's relative band: the trailing-window
	// means must satisfy |refMean - candMean| <= BandPct * |refMean|.
	// When refMean is exactly zero, the band collapses to an absolute
	// check (|refMean-candMean| <= 0, i.e. candMean must also be zero)
	// rather than dividing by zero.
	BandPct float64
}

// Contract names, per field, which Tolerance gates it. A field a
// Trajectory reports but the Contract has no entry for is a fail-closed
// condition in Compare (codeUnknownTolerance) — an unconstrained field
// is never silently treated as passing.
type Contract map[string]Tolerance
