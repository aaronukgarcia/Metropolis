package crime

import (
	"math"
	"testing"
)

// Destructive round r1 (FEAT-167 independent attack) findings F1/F3
// regression tests. The ORIGINAL bug: AdvanceMonth seeded
// districtState.eligiblePool from DistrictInput.EligiblePool only on first
// sight (`if st.eligiblePool == 0`), so every live monthly push after
// month 1 was silently ignored, and the pool could only ever fall (via gang
// recruitment) — never track a real population signal, and a pool that
// happened to land on exactly zero (fully recruited) wrongly re-triggered
// the same accidental "unseeded" reseed. The fix: eligiblePool is
// recomputed every month, unconditionally, as
// max(0, pushedEligiblePool - recruitedCumulative) — recruitedCumulative
// being the new persistent per-district running total gang recruitment has
// ever drawn off (districtState/gangs.go).

// TestF1_PoolRespondsToLiveMonthlyPush proves the live monthly push itself
// moves the queryable EligiblePool after month 1 — the direct regression
// for the ORIGINAL `if st.eligiblePool == 0` gate.
//
// PROOF THIS CAN FAIL: reverting AdvanceMonth's pool recompute to the old
// `if st.eligiblePool == 0 { st.eligiblePool = d.EligiblePool }` gate makes
// pool2 report 100 (frozen at month 1's value) instead of 50000, and this
// test fails — verified by hand during development, then reverted.
func TestF1_PoolRespondsToLiveMonthlyPush(t *testing.T) {
	a := testAPI(t)
	din := func(pool int64) []DistrictInput { return []DistrictInput{{District: 1, EligiblePool: pool}} }

	if err := a.AdvanceMonth(1, din(100), SecurityInput{}); err != nil {
		t.Fatalf("AdvanceMonth(1): %v", err)
	}
	pool1, err := a.EligiblePool(1)
	if err != nil {
		t.Fatalf("EligiblePool: %v", err)
	}
	if pool1 != 100 {
		t.Fatalf("EligiblePool after month 1 = %d, want 100", pool1)
	}

	if err := a.AdvanceMonth(2, din(50000), SecurityInput{}); err != nil {
		t.Fatalf("AdvanceMonth(2): %v", err)
	}
	pool2, err := a.EligiblePool(1)
	if err != nil {
		t.Fatalf("EligiblePool: %v", err)
	}
	if pool2 != 50000 {
		t.Fatalf("EligiblePool after month 2 push of 50000 = %d, want 50000 — the live monthly push is being ignored (F1)", pool2)
	}
}

// TestF1_ConvergenceAttack is the destructive round r1's own convergence
// attack: two states reaching the SAME pushed population via different
// growth paths (one seeded there directly, one grown from a much smaller
// starting population one month earlier) must produce Safety values that
// are close immediately after the population converges (nowhere near the
// tens-of-points gap the ORIGINAL stale-pool bug produced — its own
// reported numbers were Safety 99.72 vs 63.78, a ~36-point gap, because
// "grown"'s pool stayed frozen at its month-1 value forever) and shrink
// monotonically toward equality over subsequent months at the same
// population. The tiny residual gap right after the differing month is
// legitimate active-crime persistence (AC-5's recurrence mechanic, a real
// per-month carry-forward stock) — not itself a bug; what F1 fixes is the
// pool tracking the live push at all.
//
// PROOF THIS CAN FAIL: reverting AdvanceMonth's pool recompute to the old
// `if st.eligiblePool == 0` gate reproduces the destructive round's own
// ~36-point gap (verified by hand during development against this exact
// scenario, then reverted) — the assertion below (gap < 5.0) catches it.
func TestF1_ConvergenceAttack(t *testing.T) {
	din := func(pool int64) []DistrictInput { return []DistrictInput{{District: 1, EligiblePool: pool}} }

	grown := testAPI(t)
	if err := grown.AdvanceMonth(1, din(100), SecurityInput{}); err != nil {
		t.Fatalf("grown AdvanceMonth(1): %v", err)
	}
	seeded := testAPI(t)

	const convergedPool = 20100
	var gaps []float64
	for m := int64(2); m < 8; m++ {
		if err := grown.AdvanceMonth(m, din(convergedPool), SecurityInput{}); err != nil {
			t.Fatalf("grown AdvanceMonth(%d): %v", m, err)
		}
		if err := seeded.AdvanceMonth(m, din(convergedPool), SecurityInput{}); err != nil {
			t.Fatalf("seeded AdvanceMonth(%d): %v", m, err)
		}
		gs, err := grown.SafetyTerm(1)
		if err != nil {
			t.Fatalf("grown SafetyTerm: %v", err)
		}
		ss, err := seeded.SafetyTerm(1)
		if err != nil {
			t.Fatalf("seeded SafetyTerm: %v", err)
		}
		gaps = append(gaps, math.Abs(gs-ss))
	}

	if gaps[0] >= 5.0 {
		t.Fatalf("Safety gap immediately after convergence = %v, want < 5.0 (the pool is not tracking the live push — F1 regression)", gaps[0])
	}
	for i := 1; i < len(gaps); i++ {
		if gaps[i] > gaps[i-1] {
			t.Fatalf("Safety gap grew between converged month %d and %d (%v -> %v), want monotonic convergence toward equality", i, i+1, gaps[i-1], gaps[i])
		}
	}
	if gaps[len(gaps)-1] >= gaps[0] {
		t.Fatalf("Safety gap did not shrink over %d converged months: first=%v last=%v", len(gaps), gaps[0], gaps[len(gaps)-1])
	}
}

// TestF3_PoolAtZeroDoesNotAccidentallyReseed is destructive round r1's F3
// regression: draining a district's pool to exactly zero via sustained gang
// recruitment, then pushing the SAME small pool value again the following
// month, must NOT silently reappear at the pushed value — eligiblePool
// stays proportional to the real recruitment history
// (max(0, pushed-recruitedCumulative)), not reseeded just because it
// happened to read exactly zero.
//
// PROOF THIS CAN FAIL: reintroducing the old `if st.eligiblePool == 0 {
// st.eligiblePool = d.EligiblePool }` gate alongside the new
// recruitedCumulative tracking makes poolAfter report 1 (the pushed value)
// instead of 0, and this test fails — verified by hand during development,
// then reverted.
func TestF3_PoolAtZeroDoesNotAccidentallyReseed(t *testing.T) {
	a, _ := formGang(t, 1)

	small := formationDistrict(1)
	small.EligiblePool = 1
	const drainMonths = 30
	for m := int64(24); m < 24+drainMonths; m++ {
		if err := a.AdvanceMonth(m, []DistrictInput{small}, SecurityInput{}); err != nil {
			t.Fatalf("AdvanceMonth(%d): %v", m, err)
		}
	}
	pool, err := a.EligiblePool(1)
	if err != nil {
		t.Fatalf("EligiblePool: %v", err)
	}
	if pool != 0 {
		t.Fatalf("pool after %d recruiting months against a pushed pool of 1 = %d, want exactly 0 (fully recruited)", drainMonths, pool)
	}

	// Push the SAME tiny pool again next month. Under the old `==0` reseed
	// gate this would silently jump back to 1 regardless of the
	// accumulated recruitment history.
	if err := a.AdvanceMonth(24+drainMonths, []DistrictInput{small}, SecurityInput{}); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	poolAfter, err := a.EligiblePool(1)
	if err != nil {
		t.Fatalf("EligiblePool: %v", err)
	}
	if poolAfter != 0 {
		t.Fatalf("pool after re-pushing 1 against a recruitedCumulative >= 1 = %d, want 0 (max(0, pushed-recruited)) — the pool==0 case is accidentally re-seeding (F3 regression)", poolAfter)
	}
}
