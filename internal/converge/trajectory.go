package converge

// Sample is one tick's worth of a domain's reported scalars — a flat,
// named map so a Domain adapter can report as many or as few fields as
// its model actually has (finance's Treasury/Reserves/Debt/NetWorth
// today; nothing stops a later domain from reporting a dozen fields).
// Every value is an int64: money is already int64 micropounds
// (engine.finance's Money type), and every other domain this harness is
// expected to gate (population counts, building counts, tick indices)
// is naturally integer too — a float field would reintroduce exactly
// the rounding-order ambiguity the tiered Tolerance model exists to
// bound explicitly instead.
type Sample struct {
	// Tick is the sample's position in the journal's own timeline (a
	// simulation month for finance; whatever cadence a later domain
	// uses). Trajectories are compared tick-for-tick by this value, not
	// by slice index, so a Domain adapter that samples less often than
	// every tick is still comparable against one that samples every
	// tick, as long as both name the same tick numbers.
	Tick int64 `json:"tick"`

	// Values is the sample's named scalars, e.g.
	// {"treasury": 1000000, "netWorth": 950000}.
	Values map[string]int64 `json:"values"`
}

// Trajectory is an ordered series of Samples — a Domain adapter's
// deterministic output for one Journal run, or a fixture's captured
// series for the same Journal from the other side.
type Trajectory []Sample

// indexByTick returns t's samples keyed by Tick, for Compare's
// tick-aligned lookups. A duplicate Tick in t keeps the LAST sample for
// that tick (a Domain adapter is expected never to emit one, but this
// keeps the behaviour defined rather than a map-iteration-order
// accident if it ever does).
func (t Trajectory) indexByTick() map[int64]Sample {
	idx := make(map[int64]Sample, len(t))
	for _, s := range t {
		idx[s.Tick] = s
	}
	return idx
}

// ticksInOrder returns t's distinct Tick values in the order they first
// appear in t (never map-iteration order, GR#21) — the iteration order
// Compare drives its per-tick comparison loop from.
func (t Trajectory) ticksInOrder() []int64 {
	seen := make(map[int64]bool, len(t))
	out := make([]int64, 0, len(t))
	for _, s := range t {
		if seen[s.Tick] {
			continue
		}
		seen[s.Tick] = true
		out = append(out, s.Tick)
	}
	return out
}
