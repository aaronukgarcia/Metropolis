package citizens

import (
	"math"
	"testing"
)

// BUG-517: before this fix, every citizen minted outside the real-birth
// path (seed population, migrants) was created at age 0 regardless of
// path, degenerating the whole age-based economy. These tests are
// deliberately RED against the pre-fix behaviour (a caller that always
// passed BirthMonth = creationMonth) and prove:
//   - a non-degenerate age spread comes out of the draw (not all age 0);
//   - the draw is fully deterministic (same inputs -> same outputs, run
//     after run);
//   - the sample distribution roughly tracks the documented UK-like band
//     weights, not a uniform or fixed value;
//   - BirthMonthForAge never produces a birth month in the future, and
//     always stays inside the widened storage domain.

// TestDrawAgeAtCreationMonths_ProducesNonDegenerateSpread (BUG-517): drawing
// ages for a population of distinct citizen ids must yield MORE THAN ONE
// distinct age value, with a meaningful fraction landing in each of the
// three age bands (child/working/retired). This FAILS against the old
// all-age-0 behaviour (a single citizen "count of distinct ages == 1" and
// zero citizens ever reaching the working/retired bands).
func TestDrawAgeAtCreationMonths_ProducesNonDegenerateSpread(t *testing.T) {
	const seed = 12345
	const population = 2000
	const month = 0

	distinct := map[int32]struct{}{}
	var children, working, retired int
	for id := uint64(1); id <= population; id++ {
		age := DrawAgeAtCreationMonths(seed, id, month)
		distinct[age] = struct{}{}
		switch {
		case age < workingMinAgeMonths:
			children++
		case age < retiredMinAgeMonths:
			working++
		default:
			retired++
		}
	}

	if len(distinct) < 10 {
		t.Fatalf("only %d distinct ages drawn across %d citizens — the distribution is degenerate (all-age-0 class of bug)", len(distinct), population)
	}
	if children == 0 || working == 0 || retired == 0 {
		t.Fatalf("age bands not all represented: children=%d working=%d retired=%d (want all > 0)", children, working, retired)
	}
	// Working-age must be the largest band (documented weight 64 vs 18/18).
	if working <= children || working <= retired {
		t.Fatalf("working-age band (%d) is not the largest, want it to dominate per the documented 64%% weight (children=%d, retired=%d)", working, children, retired)
	}
}

// TestDrawAgeAtCreationMonths_BandSharesTrackDocumentedWeights (BUG-517): a
// large sample's band shares should be within a generous tolerance of the
// documented ageBand*Weight percentages — proving the draw is actually
// governed by the documented pyramid, not some other undocumented split.
func TestDrawAgeAtCreationMonths_BandSharesTrackDocumentedWeights(t *testing.T) {
	const seed = 999
	const population = 20000
	const month = 0

	var children, working, retired int
	for id := uint64(1); id <= population; id++ {
		age := DrawAgeAtCreationMonths(seed, id, month)
		switch {
		case age < workingMinAgeMonths:
			children++
		case age < retiredMinAgeMonths:
			working++
		default:
			retired++
		}
	}

	pct := func(n int) float64 { return 100 * float64(n) / float64(population) }
	const tolerance = 3.0 // percentage points either way
	if got := pct(children); math.Abs(got-ageBandChildWeight) > tolerance {
		t.Fatalf("children share = %.1f%%, want ~%d%% (+/- %.0f)", got, ageBandChildWeight, tolerance)
	}
	if got := pct(working); math.Abs(got-ageBandWorkingWeight) > tolerance {
		t.Fatalf("working share = %.1f%%, want ~%d%% (+/- %.0f)", got, ageBandWorkingWeight, tolerance)
	}
	if got := pct(retired); math.Abs(got-ageBandRetiredWeight) > tolerance {
		t.Fatalf("retired share = %.1f%%, want ~%d%% (+/- %.0f)", got, ageBandRetiredWeight, tolerance)
	}
}

// TestDrawAgeAtCreationMonths_Deterministic (GR#21): the SAME
// (seed, id, month) must draw the SAME age every time, in any order, any
// number of times — the counter-based stream property, never math/rand or
// wall-clock time. This FAILS if the implementation ever switches to
// math/rand or time.Now.
func TestDrawAgeAtCreationMonths_Deterministic(t *testing.T) {
	const seed = 777
	const month = 42

	for id := uint64(1); id <= 500; id++ {
		first := DrawAgeAtCreationMonths(seed, id, month)
		for attempt := 0; attempt < 5; attempt++ {
			if got := DrawAgeAtCreationMonths(seed, id, month); got != first {
				t.Fatalf("citizen %d: age draw not deterministic: first=%d, attempt %d=%d", id, first, attempt, got)
			}
		}
	}
}

// TestDrawAgeAtCreationMonths_DifferentSeedsDiverge (sanity check that the
// draw is actually seed-sensitive, not a hardcoded table): two different
// world seeds must NOT produce the identical age for every single citizen
// in a reasonably sized population (an astronomically unlikely coincidence
// otherwise).
func TestDrawAgeAtCreationMonths_DifferentSeedsDiverge(t *testing.T) {
	const population = 200
	const month = 0
	allSame := true
	for id := uint64(1); id <= population; id++ {
		a := DrawAgeAtCreationMonths(1, id, month)
		b := DrawAgeAtCreationMonths(2, id, month)
		if a != b {
			allSame = false
			break
		}
	}
	if allSame {
		t.Fatal("two different world seeds produced identical ages for every citizen — the draw does not appear to depend on worldSeed")
	}
}

// TestBirthMonthForAge_NeverInTheFuture (BUG-517): BirthMonth must never
// exceed the creation month — a birth month in the future implies a
// negative age at the moment of creation, nonsensical for every path this
// function serves.
func TestBirthMonthForAge_NeverInTheFuture(t *testing.T) {
	cases := []struct {
		month int64
		age   int32
	}{
		{0, 0}, {0, 500}, {100, 0}, {100, 1200}, {5, -3},
	}
	for _, c := range cases {
		bm := BirthMonthForAge(c.month, c.age)
		if int64(bm) > c.month {
			t.Fatalf("BirthMonthForAge(%d, %d) = %d, which is AFTER the creation month", c.month, c.age, bm)
		}
	}
}

// TestBirthMonthForAge_StaysInsideWidenedDomain (BUG-517): the result must
// always sit inside the storage domain ValidateCitizen now accepts
// ([MinInt16, MaxInt16]), even for an age pushing the birth month deep
// into pre-genesis (negative) territory.
func TestBirthMonthForAge_StaysInsideWidenedDomain(t *testing.T) {
	bm := BirthMonthForAge(0, retiredMaxAgeMonths)
	if int64(bm) < math.MinInt16 || int64(bm) > math.MaxInt16 {
		t.Fatalf("BirthMonthForAge(0, %d) = %d, outside [%d, %d]", retiredMaxAgeMonths, bm, math.MinInt16, math.MaxInt16)
	}
	c := testCitizen()
	c.BirthMonth = bm
	if err := ValidateCitizen(c, func(uint64) bool { return true }, "corr"); err != nil {
		t.Fatalf("ValidateCitizen rejected a BirthMonthForAge result: %v (bm=%d)", err, bm)
	}
}

// TestBirthMonthForAge_RoundTripsToTheDrawnAge (BUG-517): a citizen created
// at creationMonth with BirthMonth = BirthMonthForAge(creationMonth, age)
// must report Age() == age at that same month — the whole point of the
// mechanism.
func TestBirthMonthForAge_RoundTripsToTheDrawnAge(t *testing.T) {
	const creationMonth = int64(50)
	for _, age := range []int32{0, 1, 200, 780, 1199} {
		bm := BirthMonthForAge(creationMonth, age)
		c := Citizen{BirthMonth: bm, Month: creationMonth}
		if got := c.Age(); got != int64(age) {
			t.Fatalf("age=%d: BirthMonthForAge round-trip gave Age()=%d, want %d (bm=%d)", age, got, age, bm)
		}
	}
}
