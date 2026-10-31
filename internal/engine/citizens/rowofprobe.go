package citizens

import "sync/atomic"

// FEAT-2326609830 (BUG-666 residue): rowOf complexity-evidence seam.
//
// rowOf is the cold-store id->row lookup. BUG-666 made it a map hit; this
// seam COUNTS (never times) rowOf calls so a test can record the call-count
// scaling shape. It records ONE probe per call from the production path
// (rowOf notes noteRowOf(1) at its entry), so it does NOT detect a
// regression to a linear scan: a scan inside rowOf would still note 1.
// The regression detectors are the index-only oracle
// TestAttackFEAT830RowOfIsIndexOnly and the index-vs-linear oracle in
// attack_bug666_round_test.go; the counter measures operation counts and
// scaling shape only. No clock is read (BUG-602).
//
// Cost on the production hot path: one atomic.Bool load per rowOf call
// (a single load of a cache-resident word, ~1 ns, against a map lookup of
// tens of ns). Counting itself only happens while a test has enabled the
// seam. It is lookup-only state and never feeds simulation state (GR#21).
var (
	rowOfProbeOn    atomic.Bool
	rowOfCalls      atomic.Uint64
	rowOfProbes     atomic.Uint64
	rowOfMaxPerCall atomic.Uint64
)

// RowOfProbeSnapshot is the counter state at one instant.
type RowOfProbeSnapshot struct {
	Calls      uint64 // rowOf invocations
	Probes     uint64 // total probe steps across all invocations
	MaxPerCall uint64 // largest probe count of any single invocation
}

// EnableRowOfProbe turns the seam on or off, resetting the counters. Test
// seam only: production code never calls it.
func EnableRowOfProbe(on bool) {
	ResetRowOfProbe()
	rowOfProbeOn.Store(on)
}

// ResetRowOfProbe zeroes the counters without changing the enabled state.
func ResetRowOfProbe() {
	rowOfCalls.Store(0)
	rowOfProbes.Store(0)
	rowOfMaxPerCall.Store(0)
}

// ReadRowOfProbe returns the current counters.
func ReadRowOfProbe() RowOfProbeSnapshot {
	return RowOfProbeSnapshot{Calls: rowOfCalls.Load(), Probes: rowOfProbes.Load(), MaxPerCall: rowOfMaxPerCall.Load()}
}

// noteRowOf records one rowOf call that took `probes` steps.
func noteRowOf(probes uint64) {
	rowOfCalls.Add(1)
	rowOfProbes.Add(probes)
	for {
		cur := rowOfMaxPerCall.Load()
		if probes <= cur || rowOfMaxPerCall.CompareAndSwap(cur, probes) {
			return
		}
	}
}
