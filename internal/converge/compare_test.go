package converge

import (
	"testing"
)

func sampleTrajectory() Trajectory {
	return Trajectory{
		{Tick: 1, Values: map[string]int64{"treasury": 1_000_000, "netWorth": 950_000}},
		{Tick: 2, Values: map[string]int64{"treasury": 1_050_000, "netWorth": 990_000}},
		{Tick: 3, Values: map[string]int64{"treasury": 1_100_000, "netWorth": 1_030_000}},
	}
}

// TestCompare_IdenticalTrajectories_Passes proves the trivial case: two
// identical trajectories under an exact contract produce a passing
// Report with no diffs.
func TestCompare_IdenticalTrajectories_Passes(t *testing.T) {
	ref := sampleTrajectory()
	cand := sampleTrajectory()
	contract := Contract{
		"treasury": {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
	rep := Compare("finance", ref, cand, contract)
	if !rep.Pass {
		t.Fatalf("expected Pass, got diffs: %v", rep.Diffs)
	}
	if len(rep.Diffs) != 0 {
		t.Fatalf("expected zero diffs, got %d: %v", len(rep.Diffs), rep.Diffs)
	}
}

// TestCompare_MutatedField_Fails proves the comparator has teeth: a
// single mutated value in the candidate trajectory under TierExact must
// surface as exactly one diff naming the right field, tick, and values
// — not silently pass.
func TestCompare_MutatedField_Fails(t *testing.T) {
	ref := sampleTrajectory()
	cand := sampleTrajectory()
	cand[1].Values["treasury"] = 1_050_001 // mutate tick 2's treasury by +1

	contract := Contract{
		"treasury": {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
	rep := Compare("finance", ref, cand, contract)
	if rep.Pass {
		t.Fatalf("expected Pass=false after mutating a field, got Pass=true")
	}
	if len(rep.Diffs) != 1 {
		t.Fatalf("expected exactly 1 diff, got %d: %v", len(rep.Diffs), rep.Diffs)
	}
	d := rep.Diffs[0]
	if d.Field != "treasury" || d.Tick != 2 || d.Ref != 1_050_000 || d.Got != 1_050_001 {
		t.Fatalf("unexpected diff shape: %+v", d)
	}
}

// TestCompare_ToleranceBoundary_ExactlyAtEpsilonPasses proves the
// TierBounded boundary is inclusive: a delta exactly equal to Epsilon
// passes, and Epsilon+1 fails — the gate must have a real edge, not an
// off-by-one that silently widens or narrows the contract.
func TestCompare_ToleranceBoundary_ExactlyAtEpsilonPasses(t *testing.T) {
	ref := Trajectory{{Tick: 1, Values: map[string]int64{"flow": 1000}}}

	atEpsilon := Trajectory{{Tick: 1, Values: map[string]int64{"flow": 1005}}}
	overEpsilon := Trajectory{{Tick: 1, Values: map[string]int64{"flow": 1006}}}
	contract := Contract{"flow": {Tier: TierBounded, Epsilon: 5}}

	repAt := Compare("finance", ref, atEpsilon, contract)
	if !repAt.Pass {
		t.Fatalf("expected delta==epsilon to pass, got diffs: %v", repAt.Diffs)
	}

	repOver := Compare("finance", ref, overEpsilon, contract)
	if repOver.Pass {
		t.Fatalf("expected delta==epsilon+1 to fail, got Pass=true")
	}
	if len(repOver.Diffs) != 1 || repOver.Diffs[0].Field != "flow" {
		t.Fatalf("unexpected diffs for over-epsilon case: %v", repOver.Diffs)
	}
}

// TestCompare_UnknownTolerance_FailsClosed proves GR#15's fail-closed
// spirit: a field the reference trajectory reports but the contract
// never names must fail the comparison rather than silently pass as
// "not checked".
func TestCompare_UnknownTolerance_FailsClosed(t *testing.T) {
	ref := sampleTrajectory()
	cand := sampleTrajectory()
	contract := Contract{
		"treasury": {Tier: TierExact},
		// "netWorth" deliberately omitted.
	}
	rep := Compare("finance", ref, cand, contract)
	if rep.Pass {
		t.Fatalf("expected an unregistered field to fail the comparison, got Pass=true")
	}
	found := false
	for _, d := range rep.Diffs {
		if d.Field == "netWorth" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a diff naming the unregistered field netWorth, got: %v", rep.Diffs)
	}
}

// TestCompare_Distribution_WithinBandPasses proves TierDistribution
// gates on a trailing-window MEAN, not a single tick: two series that
// diverge tick-by-tick but agree on their running mean within band
// still pass.
func TestCompare_Distribution_WithinBandPasses(t *testing.T) {
	ref := Trajectory{
		{Tick: 1, Values: map[string]int64{"population": 1000}},
		{Tick: 2, Values: map[string]int64{"population": 1010}},
		{Tick: 3, Values: map[string]int64{"population": 990}},
	}
	// Candidate ticks individually differ from ref but the trailing
	// 3-sample mean (1000) matches ref's trailing 3-sample mean (1000)
	// exactly.
	cand := Trajectory{
		{Tick: 1, Values: map[string]int64{"population": 995}},
		{Tick: 2, Values: map[string]int64{"population": 1015}},
		{Tick: 3, Values: map[string]int64{"population": 990}},
	}
	contract := Contract{"population": {Tier: TierDistribution, Window: 3, BandPct: 0.01}}
	rep := Compare("demographics", ref, cand, contract)
	if !rep.Pass {
		t.Fatalf("expected within-band trailing mean to pass, got diffs: %v", rep.Diffs)
	}
}

// TestCompare_Distribution_OutsideBandFails proves the distribution
// gate still fails when the trailing mean genuinely diverges beyond
// BandPct — the gate has teeth on both sides.
func TestCompare_Distribution_OutsideBandFails(t *testing.T) {
	ref := Trajectory{
		{Tick: 1, Values: map[string]int64{"population": 1000}},
		{Tick: 2, Values: map[string]int64{"population": 1000}},
		{Tick: 3, Values: map[string]int64{"population": 1000}},
	}
	cand := Trajectory{
		{Tick: 1, Values: map[string]int64{"population": 1000}},
		{Tick: 2, Values: map[string]int64{"population": 1000}},
		{Tick: 3, Values: map[string]int64{"population": 2000}}, // way off
	}
	contract := Contract{"population": {Tier: TierDistribution, Window: 3, BandPct: 0.01}}
	rep := Compare("demographics", ref, cand, contract)
	if rep.Pass {
		t.Fatalf("expected an outside-band trailing mean to fail, got Pass=true")
	}
}

// TestCompare_Distribution_WarmupNeverMisreported proves a tick before
// the window has filled is never evaluated (and therefore never
// misreported as a divergence) even when the raw values differ wildly.
func TestCompare_Distribution_WarmupNeverMisreported(t *testing.T) {
	ref := Trajectory{{Tick: 1, Values: map[string]int64{"population": 1000}}}
	cand := Trajectory{{Tick: 1, Values: map[string]int64{"population": 999_999}}}
	contract := Contract{"population": {Tier: TierDistribution, Window: 5, BandPct: 0.01}}
	rep := Compare("demographics", ref, cand, contract)
	if !rep.Pass {
		t.Fatalf("expected a not-yet-full window to be skipped, got diffs: %v", rep.Diffs)
	}
}

// TestCompare_MissingCandidateSample_Fails proves a candidate that
// simply never reports a tick the reference has is treated as a
// divergence, not silently ignored.
func TestCompare_MissingCandidateSample_Fails(t *testing.T) {
	ref := sampleTrajectory()
	cand := Trajectory{ref[0], ref[2]} // tick 2 missing entirely
	contract := Contract{
		"treasury": {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
	rep := Compare("finance", ref, cand, contract)
	if rep.Pass {
		t.Fatalf("expected a missing candidate tick to fail, got Pass=true")
	}
}

// TestCompare_Deterministic proves Compare is a pure function of its
// inputs: two calls over the same trajectories/contract produce an
// identical Report (GR#21) — in particular the same Diffs order, since
// iteration is driven by sorted field names and ref's own tick order,
// never Go's randomised map-range order.
func TestCompare_Deterministic(t *testing.T) {
	ref := sampleTrajectory()
	cand := sampleTrajectory()
	cand[0].Values["treasury"] += 1
	cand[2].Values["netWorth"] += 1
	contract := Contract{
		"treasury": {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
	first := Compare("finance", ref, cand, contract)
	for i := 0; i < 20; i++ {
		got := Compare("finance", ref, cand, contract)
		if len(got.Diffs) != len(first.Diffs) {
			t.Fatalf("iteration %d: diff count changed: %d vs %d", i, len(got.Diffs), len(first.Diffs))
		}
		for j := range got.Diffs {
			if got.Diffs[j] != first.Diffs[j] {
				t.Fatalf("iteration %d: diff[%d] changed: %+v vs %+v", i, j, got.Diffs[j], first.Diffs[j])
			}
		}
	}
}
