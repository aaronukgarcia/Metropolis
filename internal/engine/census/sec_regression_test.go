package census

// Bounce-fix regression matrix for MOD-078 (engine.census) SEC-126..161.
// Each test below is a member of one of the failure classes the
// Destructive sweep identified; every test fails against the unfixed code
// (panicking or observing the corrupt value) and passes once the class is
// closed at its trust boundary.
//
// Classes:
//   - source-controlled index/float trusted without a domain check (SEC-126/129/130)
//   - GUID/identity string stored unvalidated (SEC-127)
//   - returned slice aliases internal state (SEC-128)
//   - unchecked int64 accumulation (SEC-131)
//   - identity stored/queried in non-canonical form forks one object (SEC-152)
//   - unchecked int64 addition/subtraction wraps negative (SEC-161)

import (
	"math"
	"testing"
)

// --- SEC-126: source-controlled StageKind used as a fixed-array index ---

// TestSnapshotRejectsInvalidStageKind proves the snapshot boundary rejects a
// source-controlled stage outside the [0,7] census stage domain before it can
// index a fixed [numStages]int64 array out of range (SEC-126). Pre-fix,
// Snapshot accepted the stage and Stats panicked with
// "index out of range [200] with length 8".
func TestSnapshotRejectsInvalidStageKind(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	w.education.set(1, EducationView{
		Attainment: 50,
		Stages:     []StageView{{Stage: StageKind(200)}},
	})

	if _, err := c.Snapshot(1, "test"); err == nil {
		t.Fatal("Snapshot accepted an out-of-domain StageKind(200)")
	} else {
		assertCode(t, err, ErrInvalidStageKind)
	}
}

// TestSnapshotAcceptsBoundaryStageKinds proves the domain boundary is [0,7]:
// every legal stage kind (StageNone..StageAdultEd) survives the snapshot, so
// the validation is not off-by-one (SEC-126).
func TestSnapshotAcceptsBoundaryStageKinds(t *testing.T) {
	for k := StageKind(0); k < numStages; k++ {
		c := newTestCensus(t)
		w := wire(t, c)
		w.citizens.set(mkCitizen(1))
		w.education.set(1, EducationView{Attainment: 50, Stages: []StageView{{Stage: k}}})
		if _, err := c.Snapshot(1, "test"); err != nil {
			t.Fatalf("legal stage %d rejected: %v", k, err)
		}
	}
}

// TestEducationTierSeriesBindsHandBuiltStage proves the aggregation itself
// never indexes the fixed array with a raw source-controlled value: a
// hand-built Snapshot (which bypasses Snapshot's validation) carrying an
// out-of-domain stage does not panic and is counted into the StageNone bucket
// (SEC-126 defence-in-depth).
func TestEducationTierSeriesBindsHandBuiltStage(t *testing.T) {
	c := newTestCensus(t)
	snap := &Snapshot{
		Tick:     1,
		Citizens: []CitizenView{{ID: 1}},
		Education: map[uint64]EducationView{
			1: {Stages: []StageView{{Stage: StageKind(200)}}},
		},
		Income: map[uint64]int64{},
	}
	tiers := c.EducationTierSeries(snap) // must not panic
	if tiers[StageNone] != 1 {
		t.Fatalf("out-of-domain stage should bucket to StageNone: %v", tiers)
	}
}

// --- SEC-127: GUID stored unvalidated at TrackObject ---

// TestTrackObjectRejectsMalformedGUID proves TrackObject rejects a GUID that
// cannot round-trip through parseGUID (empty, non-numeric id, unknown prefix)
// instead of storing it and never resolving it (SEC-127).
func TestTrackObjectRejectsMalformedGUID(t *testing.T) {
	cases := []struct {
		name string
		guid GUID
	}{
		{"empty", GUID("")},
		{"citizen-non-numeric", GUID("citizen:notanumber")},
		{"unknown-prefix", GUID("bogus:5")},
		{"missing-separator", GUID("citizen5")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCensus(t)
			wire(t, c)
			err := c.TrackObject(tc.guid, ObjectCar, LifeSpanShortLived)
			assertCode(t, err, ErrInvalidGUID)
			if len(c.TrackedObjects()) != 0 {
				t.Fatalf("malformed GUID was stored in the tracked set")
			}
		})
	}
}

// TestTrackObjectRejectsKindMismatch proves a kind that contradicts the GUID's
// prefix is rejected: the exact corruption the Destructive sweep demonstrated
// (a citizen GUID pre-registered as a car), plus the inverse (a car GUID
// declared a house) (SEC-127).
func TestTrackObjectRejectsKindMismatch(t *testing.T) {
	cases := []struct {
		name string
		guid GUID
		kind ObjectKind
	}{
		{"citizen-as-car", citizenGUID(1), ObjectCar},
		{"car-as-house", carGUID(1), ObjectHouse},
		{"chopper-as-citizen", chopperGUID(1), ObjectCitizen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCensus(t)
			wire(t, c)
			assertCode(t, c.TrackObject(tc.guid, tc.kind, LifeSpanWholeGame), ErrInvalidGUID)
			if len(c.TrackedObjects()) != 0 {
				t.Fatalf("mis-prefixed GUID was stored in the tracked set")
			}
		})
	}
}

// TestTrackObjectRejectsCitizenShortLived proves a citizen GUID cannot be
// registered with a short-lived life span: citizens are whole-game by the
// module's own invariant (checkLocked always creates them so), so the
// life-span contradicts the prefix (SEC-127).
func TestTrackObjectRejectsCitizenShortLived(t *testing.T) {
	c := newTestCensus(t)
	wire(t, c)
	assertCode(t, c.TrackObject(citizenGUID(1), ObjectCitizen, LifeSpanShortLived), ErrInvalidGUID)
}

// TestTrackObjectRejectsOutOfDomainLifeSpan proves an out-of-domain life-span
// enum value is rejected rather than stored unvalidated (GR#16).
func TestTrackObjectRejectsOutOfDomainLifeSpan(t *testing.T) {
	c := newTestCensus(t)
	wire(t, c)
	assertCode(t, c.TrackObject(carGUID(1), ObjectCar, LifeSpan(99)), ErrInvalidGUID)
}

// TestTrackObjectAcceptsValidNonCitizen proves the validation does not
// over-reject: a correctly-prefixed GUID with a matching kind and a valid
// life span is accepted (SEC-127 positive control).
func TestTrackObjectAcceptsValidNonCitizen(t *testing.T) {
	c := newTestCensus(t)
	wire(t, c)
	must(t, c.TrackObject(carGUID(100), ObjectCar, LifeSpanShortLived))
	must(t, c.TrackObject(houseGUID(200), ObjectHouse, LifeSpanWholeGame))
	must(t, c.TrackObject(chopperGUID(300), ObjectChopper, LifeSpanShortLived))
	objs := c.TrackedObjects()
	if len(objs) != 3 {
		t.Fatalf("valid non-citizen objects not tracked: %d (%v)", len(objs), objs)
	}
}

// --- SEC-128: returned slice aliases internal state ---

// TestSnapshotEducationStagesDeepCopied proves the snapshot's immutable view
// does not alias the education source's Stages backing array: mutating the
// source's slice in place after capture must not change the snapshot
// (SEC-128). Pre-fix, the mutation leaked through and two reads of the
// "committed" snapshot disagreed.
func TestSnapshotEducationStagesDeepCopied(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	shared := []StageView{{Stage: StagePrimary}}
	w.education.set(1, EducationView{Attainment: 50, Stages: shared})

	snap, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Mutate the source's backing array in place after capture.
	shared[0].Stage = StageUniversity

	got := snap.Education[1].Stages[0].Stage
	if got != StagePrimary {
		t.Fatalf("snapshot aliases the source slice: stage changed to %d after capture", got)
	}
}

// --- SEC-129: bare int64() coercion of a source float ---

// TestSourceHappinessRejectsNonFinite proves Source("happiness") no longer
// coerces a non-finite float with a bare int64() (which wrapped NaN to
// MinInt64 garbage): it now fails closed through num.SafeInt64 (SEC-129).
func TestSourceHappinessRejectsNonFinite(t *testing.T) {
	c := newTestCensus(t)
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		snap := &Snapshot{Happiness: v}
		if _, err := c.Source(snap, KPIKeyHappiness); err == nil {
			t.Fatalf("Source(happiness=%v) should reject the non-finite float", v)
		} else {
			assertCode(t, err, "MET-F800")
		}
	}
}

// TestSourceHappinessFinite proves a finite happiness still resolves to its
// whole-pound figure through the safe coercion (SEC-129 positive control).
func TestSourceHappinessFinite(t *testing.T) {
	c := newTestCensus(t)
	res, err := c.Source(&Snapshot{Happiness: 88}, KPIKeyHappiness)
	if err != nil {
		t.Fatalf("Source(finite happiness): %v", err)
	}
	if res.LineValue != 88 {
		t.Fatalf("finite happiness coerced wrong: got %d want 88", res.LineValue)
	}
}

// --- SEC-130: non-finite source floats silently disable the regulator ---

// TestNonFiniteSourceFloatFailsClosed proves each of the four source floats
// (crime/happiness/unfed/policy) is finiteness-checked at the snapshot
// boundary: a NaN source value makes RunObservers fail closed with
// ErrNonFiniteSource instead of returning a clean run with zero findings
// (which is what happened pre-fix, because NaN > threshold is false — the
// watchdog silently disabled, GR#17) (SEC-130).
func TestNonFiniteSourceFloatFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		poison func(w wired)
	}{
		{"crime", func(w wired) { w.crime.setRate(math.NaN()) }},
		{"happiness", func(w wired) { w.wellbeing.happiness = math.NaN() }},
		{"unfed", func(w wired) { w.wellbeing.unfed = math.NaN() }},
		{"policy", func(w wired) { w.policies.coef = math.NaN() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCensus(t)
			w := wire(t, c)
			w.citizens.set(mkCitizen(1))
			tc.poison(w)
			assertCode(t, c.RunObservers(1, "test"), ErrNonFiniteSource)
		})
	}
}

// TestNonFiniteSourceFloatAllFormsRejected proves the finiteness check catches
// ±Inf as well as NaN — the class is "non-finite", not "NaN" (SEC-130).
func TestNonFiniteSourceFloatAllFormsRejected(t *testing.T) {
	for name, v := range map[string]float64{
		"nan":     math.NaN(),
		"pos-inf": math.Inf(1),
		"neg-inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestCensus(t)
			w := wire(t, c)
			w.citizens.set(mkCitizen(1))
			w.crime.setRate(v)
			assertCode(t, c.RunObservers(1, "test"), ErrNonFiniteSource)
		})
	}
}

// --- SEC-131: unchecked int64 accumulation wraps negative ---

// TestStatsIncomeSaturatesNotWraps proves the income aggregate sum saturates
// instead of wrapping: MaxInt64 + 1 must not flip TotalIncome/MeanIncome
// negative (SEC-131). Pre-fix the unchecked += wrapped both negative.
func TestStatsIncomeSaturatesNotWraps(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	w.citizens.set(mkCitizen(2))
	w.finance.setIncome(1, math.MaxInt64)
	w.finance.setIncome(2, 1)

	snap, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	agg := c.Stats(snap)
	if agg.TotalIncome != math.MaxInt64 {
		t.Fatalf("TotalIncome wrapped: got %d want MaxInt64", agg.TotalIncome)
	}
	if agg.MeanIncome <= 0 {
		t.Fatalf("MeanIncome wrapped negative: %d", agg.MeanIncome)
	}
}

// TestStatsAttainmentSaturatesNotWraps proves the attainment aggregate sum
// saturates instead of wrapping (SEC-131, the stats.go:128 site).
func TestStatsAttainmentSaturatesNotWraps(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	w.citizens.set(mkCitizen(2))
	w.education.set(1, EducationView{Attainment: math.MaxInt64})
	w.education.set(2, EducationView{Attainment: 1})

	snap, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	agg := c.Stats(snap)
	if agg.MeanAttainment <= 0 {
		t.Fatalf("MeanAttainment wrapped negative: %v", agg.MeanAttainment)
	}
}

// TestEducationCrimeLinkageAttainmentSaturates proves the linkage's own
// attainment accumulation (demographics.go) is the same class and is fixed
// the same way (SEC-131 class coverage).
func TestEducationCrimeLinkageAttainmentSaturates(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	w.citizens.set(mkCitizen(2))
	w.education.set(1, EducationView{Attainment: math.MaxInt64})
	w.education.set(2, EducationView{Attainment: 1})

	snap, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	link := c.EducationCrimeLinkage(snap)
	if link.MeanAttainment <= 0 {
		t.Fatalf("MeanAttainment wrapped negative: %v", link.MeanAttainment)
	}
}

// --- SEC-152: identity stored/queried in non-canonical form forks one object ---

// TestTrackObjectCanonicalisesPaddedGUID proves two spellings of the same
// non-citizen identity collapse to one tracked record (SEC-152). Pre-fix,
// TrackObject stored the raw GUID string as the map key, so "car:007" and
// "car:7" forked one car into two records and the cross-form lookup missed
// with ErrUnknownObject (MET-G2701).
func TestTrackObjectCanonicalisesPaddedGUID(t *testing.T) {
	cases := []struct {
		name      string
		padded    GUID
		canonical GUID
		kind      ObjectKind
		lifeSpan  LifeSpan
	}{
		{"car", GUID("car:007"), carGUID(7), ObjectCar, LifeSpanShortLived},
		{"house", GUID("house:042"), houseGUID(42), ObjectHouse, LifeSpanWholeGame},
		{"chopper", GUID("chopper:003"), chopperGUID(3), ObjectChopper, LifeSpanShortLived},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCensus(t)
			wire(t, c)
			must(t, c.TrackObject(tc.padded, tc.kind, tc.lifeSpan))
			must(t, c.TrackObject(tc.canonical, tc.kind, tc.lifeSpan))
			objs := c.TrackedObjects()
			if len(objs) != 1 {
				t.Fatalf("two spellings forked %s into %d records: %v", tc.name, len(objs), objs)
			}
			if objs[0] != tc.canonical {
				t.Fatalf("tracked GUID = %s, want canonical %s", objs[0], tc.canonical)
			}
			if _, err := c.CheckIn(tc.padded); err != nil {
				t.Fatalf("CheckIn(padded %s): %v", tc.padded, err)
			}
			if _, err := c.ObjectBio(tc.canonical, "test"); err != nil {
				t.Fatalf("ObjectBio(canonical %s): %v", tc.canonical, err)
			}
		})
	}
}

// TestRecordLifeEventCanonicalisesPaddedGUID proves the life-history lookup
// surface canonicalises too: an event recorded under "car:007" lands on the
// canonical "car:7" record and is visible to the canonical spelling (SEC-152).
func TestRecordLifeEventCanonicalisesPaddedGUID(t *testing.T) {
	c := newTestCensus(t)
	wire(t, c)
	must(t, c.TrackObject(GUID("car:007"), ObjectCar, LifeSpanShortLived))
	must(t, c.RecordLifeEvent(GUID("car:007"), 5, "mileage: 12000"))

	bio, err := c.ObjectBio(GUID("car:7"), "test")
	if err != nil {
		t.Fatalf("ObjectBio(car:7): %v", err)
	}
	if len(bio.LifeHistory) != 1 || bio.LifeHistory[0].Description != "mileage: 12000" {
		t.Fatalf("cross-form RecordLifeEvent not visible: %+v", bio.LifeHistory)
	}
}

// TestPreRegisteredPaddedCitizenSingleRecord proves a pre-registered padded
// citizen GUID ("citizen:001") and the consistency checker's own mint
// ("citizen:1") are one record, not two (SEC-152). Pre-fix, TrackObject
// stored "citizen:001" and checkLocked then minted "citizen:1" as a second
// record for the same citizen, duplicating identity in the check-in report.
func TestPreRegisteredPaddedCitizenSingleRecord(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	must(t, c.TrackObject(GUID("citizen:001"), ObjectCitizen, LifeSpanWholeGame))
	if err := c.RunObservers(0, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}

	objs := c.TrackedObjects()
	if len(objs) != 1 {
		t.Fatalf("padded pre-registration forked one citizen into %d records: %v", len(objs), objs)
	}
	if objs[0] != citizenGUID(1) {
		t.Fatalf("tracked GUID = %s, want canonical %s", objs[0], citizenGUID(1))
	}
	if _, err := c.CheckIn(citizenGUID(1)); err != nil {
		t.Fatalf("CheckIn(citizen:1): %v", err)
	}
}

// TestCitizenBioReturnsCanonicalGUID proves CitizenBio emits the canonical
// GUID, not the caller's raw padded spelling, so every surface agrees on one
// identity string (SEC-152 class coverage).
func TestCitizenBioReturnsCanonicalGUID(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	bio, err := c.CitizenBio(GUID("citizen:001"), 0, "test")
	if err != nil {
		t.Fatalf("CitizenBio(citizen:001): %v", err)
	}
	if bio.GUID != citizenGUID(1) {
		t.Fatalf("CitizenBio returned non-canonical GUID %s, want %s", bio.GUID, citizenGUID(1))
	}
}

// --- SEC-161: unchecked int64 addition/subtraction wraps negative ---

// TestRetirementMonthsSaturatesNotWraps proves RetirementMonths saturates
// instead of wrapping: a config retirement age of 7.5e17 years (9.0e18
// months, accepted by safeInt64Months) plus a source-controlled birth month
// of 3e17 must not wrap the sum into a negative retirement month (SEC-161).
// Pre-fix, the unchecked birthMonth + months returned -9146744073709551616
// with err == nil.
func TestRetirementMonthsSaturatesNotWraps(t *testing.T) {
	cfg := testConfig()
	cfg.BellCurves.RetirementAgeYears.Value = 7.5e17
	retire, err := cfg.RetirementMonths(3e17, "test")
	if err != nil {
		t.Fatalf("RetirementMonths: %v", err)
	}
	if retire != math.MaxInt64 {
		t.Fatalf("RetirementMonths wrapped: got %d want MaxInt64", retire)
	}
}

// TestRetirementMonthsNormalValue is the positive control for the saturation
// fix: a sane birth month plus the shipped retirement age still produces the
// exact expected month (SEC-161 must not break the normal path).
func TestRetirementMonthsNormalValue(t *testing.T) {
	cfg := testConfig() // retirement age 68 years = 816 months
	retire, err := cfg.RetirementMonths(100, "test")
	if err != nil {
		t.Fatalf("RetirementMonths: %v", err)
	}
	if retire != 100+68*12 {
		t.Fatalf("RetirementMonths normal value wrong: got %d want %d", retire, 100+68*12)
	}
}

// TestAgeBandSeriesSaturatesOnSubtractionOverflow proves the age computation
// (snap.Tick - cv.BirthMonth) saturates instead of wrapping: a snapshot tick
// of MaxInt64 against a citizen born at MinInt64 must not wrap the age
// negative and mis-bucket the citizen into the youngest band (SEC-161 class,
// the stats.go subtraction sibling). Pre-fix, the wrap produced age -1 and
// band 0; post-fix the saturated age lands in the eldest band.
func TestAgeBandSeriesSaturatesOnSubtractionOverflow(t *testing.T) {
	c := newTestCensus(t)
	snap := &Snapshot{
		Tick:     math.MaxInt64,
		Citizens: []CitizenView{{ID: 1, BirthMonth: math.MinInt64}},
	}
	bands := c.AgeBandSeries(snap) // must not panic
	if bands[4] != 1 {
		t.Fatalf("overflowing age wrapped negative and mis-bucketed: %v", bands)
	}
}

// TestConsistencyLagSaturatesOnSubtractionOverflow proves the consistency
// checker's lag computation (snap.Tick - least) saturates instead of wrapping:
// a citizen whose check-in froze at a positive tick, observed again at a
// MinInt64 tick, must not wrap the lag into a huge positive value and emit a
// spurious lost-object flag (SEC-161 class, the consistency.go subtraction
// sibling). Pre-fix, MinInt64 - 5 wrapped to a large positive lag and the
// object was wrongly flagged lost; post-fix the saturated lag is MinInt64 and
// the object is correctly not flagged.
func TestConsistencyLagSaturatesOnSubtractionOverflow(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	w.finance.setIncome(1, 1000)

	// First observation at a positive tick: every facet (incl. income) = 5.
	if err := c.RunObservers(5, "test"); err != nil {
		t.Fatalf("first RunObservers: %v", err)
	}
	// The citizen disappears from the source; its facets freeze at 5.
	w.citizens.remove(1)
	// Second observation at MinInt64: lag = MinInt64 - 5 overflows pre-fix.
	if err := c.RunObservers(math.MinInt64, "test"); err != nil {
		t.Fatalf("second RunObservers: %v", err)
	}

	if lost := c.LostObjects(); len(lost) != 0 {
		t.Fatalf("overflowing lag wrapped and produced a spurious lost flag: %+v", lost)
	}
}
