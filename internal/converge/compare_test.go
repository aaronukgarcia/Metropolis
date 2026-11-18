package converge

import (
	"strings"
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

// TestCompare_Distribution_WarmupNeverMisreported proves ticks before the
// window has filled are skipped (never misreported as a divergence even when
// the raw values differ wildly) while the filled ticks are evaluated.
func TestCompare_Distribution_WarmupNeverMisreported(t *testing.T) {
	ref := Trajectory{
		{Tick: 1, Values: map[string]int64{"population": 1000}},
		{Tick: 2, Values: map[string]int64{"population": 1000}},
		{Tick: 3, Values: map[string]int64{"population": 1000}},
	}
	cand := Trajectory{
		{Tick: 1, Values: map[string]int64{"population": 999_999}},
		{Tick: 2, Values: map[string]int64{"population": 1000}},
		{Tick: 3, Values: map[string]int64{"population": 1000}},
	}
	contract := Contract{"population": {Tier: TierDistribution, Window: 2, BandPct: 0.01}}
	rep := Compare("demographics", ref, cand, contract)
	if rep.Evaluated != 2 || rep.FieldEvaluated["population"] != 2 {
		t.Fatalf("expected the two filled windows evaluated, got %d (%v)", rep.Evaluated, rep.FieldEvaluated)
	}
	// tick 2's window still contains the wild tick-1 value, so it diverges;
	// tick 3's window is clean. Warm-up tick 1 itself is never reported.
	for _, d := range rep.Diffs {
		if d.Tick == 1 {
			t.Fatalf("warm-up tick misreported: %v", d)
		}
	}
}

func reasonCites(rep *Report, code, field string) bool {
	for _, d := range rep.Diffs {
		if strings.Contains(d.Reason, code) && (field == "" || d.Field == field) {
			return true
		}
	}
	return false
}

// TestCompare_ReviewerProbe_UnfilledDistributionPlusExactFails is the BUG-1140
// r1 round's probe: a never-filled distribution field beside an evaluated
// exact field must FAIL for the distribution field.
func TestCompare_ReviewerProbe_UnfilledDistributionPlusExactFails(t *testing.T) {
	ref := Trajectory{{Tick: 1, Values: map[string]int64{"population": 1000, "tick": 1}}}
	cand := Trajectory{{Tick: 1, Values: map[string]int64{"population": 999_999, "tick": 1}}}
	contract := Contract{
		"population": {Tier: TierDistribution, Window: 5, BandPct: 0.01},
		"tick":       {Tier: TierExact},
	}
	rep := Compare("demographics", ref, cand, contract)
	if rep.Pass {
		t.Fatalf("unfilled distribution window passed: %+v", rep)
	}
	if !reasonCites(rep, codeFieldNeverEvaluated, "population") {
		t.Fatalf("expected %s naming population, got %v", codeFieldNeverEvaluated, rep.Diffs)
	}
	if rep.FieldEvaluated["tick"] != 1 || rep.FieldEvaluated["population"] != 0 {
		t.Fatalf("unexpected per-field counts: %v", rep.FieldEvaluated)
	}
}

func TestCompare_DeclaredFieldNeverReportedFails(t *testing.T) {
	tr := Trajectory{{Tick: 1, Values: map[string]int64{"a": 1}}}
	rep := Compare("d", tr, tr, Contract{"a": {Tier: TierExact}, "b": {Tier: TierExact}})
	if rep.Pass || !reasonCites(rep, codeFieldMissingFromReference, "b") || !reasonCites(rep, codeFieldNeverEvaluated, "b") {
		t.Fatalf("declared-but-never-reported field must fail with H511+H512 naming b: %v", rep.Diffs)
	}
}

func TestCompare_SparseReferenceFails(t *testing.T) {
	ref := Trajectory{
		{Tick: 1, Values: map[string]int64{"a": 1, "b": 1}},
		{Tick: 2, Values: map[string]int64{"a": 2}},
	}
	rep := Compare("d", ref, ref, Contract{"a": {Tier: TierExact}, "b": {Tier: TierExact}})
	if rep.Pass || !reasonCites(rep, codeFieldMissingFromReference, "b") {
		t.Fatalf("sparse reference must fail naming b: %v", rep.Diffs)
	}
	for _, d := range rep.Diffs {
		if d.Field == "b" && strings.Contains(d.Reason, codeFieldMissingFromReference) && d.Tick != 2 {
			t.Fatalf("sparse diff should name tick 2, got %v", d)
		}
	}
}

func TestCompare_DuplicateTicksFail(t *testing.T) {
	dup := Trajectory{
		{Tick: 1, Values: map[string]int64{"a": 1}},
		{Tick: 1, Values: map[string]int64{"a": 1}},
	}
	one := Trajectory{{Tick: 1, Values: map[string]int64{"a": 1}}}
	c := Contract{"a": {Tier: TierExact}}
	if rep := Compare("d", dup, one, c); rep.Pass || !reasonCites(rep, codeDuplicateTick, "") {
		t.Fatalf("duplicate reference tick must fail: %v", rep.Diffs)
	}
	if rep := Compare("d", one, dup, c); rep.Pass || !reasonCites(rep, codeDuplicateTick, "") {
		t.Fatalf("duplicate candidate tick must fail: %v", rep.Diffs)
	}
}

func TestCompare_ExtraCandidateTicksAndFieldsHarmless(t *testing.T) {
	ref := Trajectory{{Tick: 1, Values: map[string]int64{"a": 1}}}
	cand := Trajectory{
		{Tick: 1, Values: map[string]int64{"a": 1, "extra": 9}},
		{Tick: 2, Values: map[string]int64{"a": 7}},
	}
	if rep := Compare("d", ref, cand, Contract{"a": {Tier: TierExact}}); !rep.Pass {
		t.Fatalf("extras must stay harmless: %v", rep.Diffs)
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

// TestCompare_ZeroEvaluated_NeverPasses is BUG-1140: every route to zero
// evaluated comparisons is its own registry-coded failure, never a pass.
func TestCompare_ZeroEvaluated_NeverPasses(t *testing.T) {
	exact := Contract{"treasury": {Tier: TierExact}}
	one := Trajectory{{Tick: 1, Values: map[string]int64{"treasury": 5}}}
	cases := []struct {
		name      string
		ref, cand Trajectory
		contract  Contract
		code      string
	}{
		{"nil reference", nil, one, exact, codeEmptyReference},
		{"empty reference and candidate", Trajectory{}, Trajectory{}, exact, codeEmptyReference},
		{"reference samples without values", Trajectory{{Tick: 1}}, one, exact, codeNoFieldsReported},
		{"empty candidate", one, nil, exact, codeZeroEvaluated},
		{"no tick overlap", one, Trajectory{{Tick: 2, Values: map[string]int64{"treasury": 5}}}, exact, codeZeroEvaluated},
		{"candidate lacks the field", one, Trajectory{{Tick: 1, Values: map[string]int64{"other": 1}}}, exact, codeZeroEvaluated},
		{"distribution window never fills", one, one, Contract{"treasury": {Tier: TierDistribution, Window: 5, BandPct: 0.01}}, codeZeroEvaluated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Compare("d", c.ref, c.cand, c.contract)
			if rep.Pass {
				t.Fatalf("zero-evaluation comparison passed: %+v", rep)
			}
			if rep.Evaluated != 0 {
				t.Fatalf("expected Evaluated=0, got %d", rep.Evaluated)
			}
			found := false
			for _, d := range rep.Diffs {
				if strings.Contains(d.Reason, c.code) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a diff citing %s, got %v", c.code, rep.Diffs)
			}
		})
	}
}

// TestCompare_NormalComparison_ReportsEvaluatedCount proves a real
// comparison still passes and carries its evaluated count.
func TestCompare_NormalComparison_ReportsEvaluatedCount(t *testing.T) {
	tr := Trajectory{
		{Tick: 1, Values: map[string]int64{"treasury": 5}},
		{Tick: 2, Values: map[string]int64{"treasury": 6}},
	}
	rep := Compare("d", tr, tr, Contract{"treasury": {Tier: TierExact}})
	if !rep.Pass || rep.Evaluated != 2 {
		t.Fatalf("expected pass with Evaluated=2, got pass=%t evaluated=%d diffs=%v", rep.Pass, rep.Evaluated, rep.Diffs)
	}
	if rep.FieldEvaluated["treasury"] != 2 || !strings.Contains(rep.Text(), "fieldEvaluated: treasury=2") {
		t.Fatalf("per-field counts missing: %v %q", rep.FieldEvaluated, rep.Text())
	}
	if !strings.Contains(rep.Text(), "evaluated=2") {
		t.Fatalf("Text() omits the evaluated count: %q", rep.Text())
	}
}
