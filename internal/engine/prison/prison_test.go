package prison

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testConfig returns a config with known, data-file-matching magnitudes so
// tests assert the mechanism (direction, isolation, equality) rather than
// re-asserting a constant equals itself (the banned self-fulfilling test).
func testConfig() config {
	return config{
		Version:    1,
		Categories: []string{"open", "standard", "highSecurity"},
		BaseRates: map[string]map[string]float64{
			"youth": {"minor": 0.22, "serious": 0.42, "violent": 0.62},
			"adult": {"minor": 0.25, "serious": 0.45, "violent": 0.65},
		},
		CategoryMismatchPenalty: 0.10,
		Regime: map[string]regimeLine{
			"education":          {MaxEffect: 0.08, CostForMax: 1000},
			"work":               {MaxEffect: 0.06, CostForMax: 1000},
			"addictionTreatment": {MaxEffect: 0.07, CostForMax: 1000},
		},
		Reentry: map[string]reentryLine{
			"probationCapacity": {MaxEffect: 0.06},
			"employmentUptake":  {MaxEffect: 0.05},
			"housingOnRelease":  {MaxEffect: 0.04},
		},
		Overcrowding:         overcrowdingConfig{DegradeMax: 1.0},
		Youth:                youthConfig{CostMultiplier: 0.5},
		AdultCostPerOffender: map[string]int64{"minor": 100000, "serious": 200000, "violent": 300000},
		FuseYears:            fuseYearsConfig{Min: 5, Max: 15},
	}
}

// newTestPrison constructs a *PrisonAPI with a fixed seed and a
// citizen-existence predicate that accepts every citizen (overridable in
// individual tests via SetCitizenExists).
func newTestPrison(t *testing.T) *PrisonAPI {
	t.Helper()
	p, err := New(12345, testConfig(), "test-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.SetCitizenExists(func(uint64) bool { return true }); err != nil {
		t.Fatalf("SetCitizenExists: %v", err)
	}
	return p
}

// admit is a test helper that admits one citizen and fails the test on error.
func admit(t *testing.T, p *PrisonAPI, id uint64, d crime.DistrictID, month int64, o OffenceClass, c Category, youth bool) {
	t.Helper()
	if err := p.Admit(Admission{CitizenID: id, District: d, Month: month, Offence: o, Category: c, SentenceMonths: 12, Youth: youth, SentencingRef: id}); err != nil {
		t.Fatalf("Admit(%d): %v", id, err)
	}
}

// AC-1: per-prisoner cohort records are queryable individually by citizen ID,
// with independent category/offence fields — never an aggregate headcount.
func TestPerPrisonerCohortRecord(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	admit(t, p, 2, crime.DistrictID(1), 1, OffenceViolent, CategoryHighSecurity, false)

	c1, ok1, err := p.Cohort(1)
	if err != nil {
		t.Fatalf("Cohort(1): %v", err)
	}
	c2, ok2, err := p.Cohort(2)
	if err != nil {
		t.Fatalf("Cohort(2): %v", err)
	}
	if !ok1 || !ok2 {
		t.Fatalf("both prisoners must be individually queryable, got ok1=%v ok2=%v", ok1, ok2)
	}
	if c1.Category != CategoryOpen || c2.Category != CategoryHighSecurity {
		t.Fatalf("independent category fields: %v vs %v", c1.Category, c2.Category)
	}
	if c1.Offence != OffenceMinor || c2.Offence != OffenceViolent {
		t.Fatalf("independent offence fields: %v vs %v", c1.Offence, c2.Offence)
	}
}

// AC-2: the intake ledger is built entry-by-entry (not a shared counter), so
// its own count equals — but is independent of — engine.crime's figure, and a
// discrepancy is detectable through the VerifyPrisonIntake seam.
func TestIntakeLedgerIndependentOfCrimeCount(t *testing.T) {
	p := newTestPrison(t)
	for i := uint64(0); i < 3; i++ {
		admit(t, p, 1000+i, crime.DistrictID(1), 1, OffenceMinor, "", false)
	}
	if got := p.IntakeCount(1, 1); got != 3 {
		t.Fatalf("IntakeCount = %d, want 3 (one ledger entry per admission)", got)
	}
	if len(p.admissions) != 3 {
		t.Fatalf("ledger length = %d, want 3 — the ledger is entry-by-entry, not a bare counter", len(p.admissions))
	}
}

func TestVerifyPrisonIntakeDetectsMismatch(t *testing.T) {
	c, err := crime.New(777, "crime-test")
	if err != nil {
		t.Fatalf("crime.New: %v", err)
	}
	if err := c.RegisterDistrict(7); err != nil {
		t.Fatalf("RegisterDistrict: %v", err)
	}
	if err := c.AdvanceMonth(1, []crime.DistrictInput{{
		District: 7, OwnDeprivation: 1, YouthUnemployment: 1, Blight: 1,
		YouthLeisureDesert: 1, PatrolCoverage: 0, DetectiveCapacity: 20,
		CourthouseThroughput: 1000, EligiblePool: 10000,
	}}, crime.SecurityInput{}); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	n, err := c.OffendersSentencedToPrison(7)
	if err != nil {
		t.Fatalf("OffendersSentencedToPrison: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least one sentenced offender to drive the cross-check, got %d", n)
	}

	p := newTestPrison(t)
	for i := int64(0); i < n-1; i++ {
		admit(t, p, uint64(9000+i), crime.DistrictID(7), 1, OffenceMinor, "", false)
	}
	if err := c.SetPrisonIntake(p); err != nil {
		t.Fatalf("SetPrisonIntake: %v", err)
	}
	ok, err := c.VerifyPrisonIntake(7, 1)
	if err != nil {
		t.Fatalf("VerifyPrisonIntake: %v", err)
	}
	if ok {
		t.Fatalf("a %d-vs-%d mismatch must be detected as false, not swallowed", n-1, n)
	}
	admit(t, p, uint64(9000+n-1), crime.DistrictID(7), 1, OffenceMinor, "", false)
	ok, err = c.VerifyPrisonIntake(7, 1)
	if err != nil {
		t.Fatalf("VerifyPrisonIntake (post-fix): %v", err)
	}
	if !ok {
		t.Fatalf("matching counts must verify true")
	}
}

// AC-3: placing a low-risk offender in high-security RAISES reoffending
// relative to the matched category — the counterintuitive direction.
func TestCategoryMismatchRaisesReoffending(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	admit(t, p, 2, crime.DistrictID(1), 1, OffenceMinor, CategoryHighSecurity, false)
	matched, err := p.ReoffendingRate(1)
	if err != nil {
		t.Fatalf("ReoffendingRate(1): %v", err)
	}
	mismatched, err := p.ReoffendingRate(2)
	if err != nil {
		t.Fatalf("ReoffendingRate(2): %v", err)
	}
	if mismatched <= matched {
		t.Fatalf("high-security-placed minor offender rate %.3f must exceed matched rate %.3f", mismatched, matched)
	}
}

// assertSingleLineIsolation funds exactly one regime line to its cost-for-max
// and asserts that line's effect rises while the other two stay at zero — the
// single-variable isolation AC-4 requires (raising one programme never moves
// the other two programmes' own attributable contribution).
func assertSingleLineIsolation(t *testing.T, line RegimeLine) {
	t.Helper()
	p := newTestPrison(t)
	if e, w, a := p.EducationEffect(), p.WorkEffect(), p.AddictionEffect(); e != 0 || w != 0 || a != 0 {
		t.Fatalf("zero funding must yield zero effect, got e=%.3f w=%.3f a=%.3f", e, w, a)
	}
	if err := p.SetRegimeFunding(line, 1000); err != nil {
		t.Fatalf("SetRegimeFunding(%s): %v", line, err)
	}
	effects := map[RegimeLine]float64{
		RegimeEducation:          p.EducationEffect(),
		RegimeWork:               p.WorkEffect(),
		RegimeAddictionTreatment: p.AddictionEffect(),
	}
	if effects[line] <= 0 {
		t.Fatalf("%s effect must rise when funded alone, got %.3f", line, effects[line])
	}
	for _, other := range []RegimeLine{RegimeEducation, RegimeWork, RegimeAddictionTreatment} {
		if other == line {
			continue
		}
		if effects[other] != 0 {
			t.Fatalf("%s must not move when only %s is funded, got %.3f", other, line, effects[other])
		}
	}
}

// AC-4: the three regime programmes are independently isolable — raising one
// moves only its own effect (three single-variable tests, one per programme).
func TestRegimeIsolation(t *testing.T) {
	assertSingleLineIsolation(t, RegimeEducation)
}

func TestWorkProgrammeIsolation(t *testing.T) {
	assertSingleLineIsolation(t, RegimeWork)
}

func TestAddictionTreatmentIsolation(t *testing.T) {
	assertSingleLineIsolation(t, RegimeAddictionTreatment)
}

// AC-5: the reoffending formula's three terms move in the documented
// directions, each sub-input independent of the others.
func TestReoffendingFormulaDirections(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	base, err := p.ReoffendingRate(1)
	if err != nil {
		t.Fatalf("ReoffendingRate: %v", err)
	}

	// Regime: each of the three lines lowers the rate.
	for _, line := range []RegimeLine{RegimeEducation, RegimeWork, RegimeAddictionTreatment} {
		if err := p.SetRegimeFunding(line, 1000); err != nil {
			t.Fatalf("SetRegimeFunding(%s): %v", line, err)
		}
		after, _ := p.ReoffendingRate(1)
		if after >= base {
			t.Fatalf("funding %s must lower reoffending (base %.3f → %.3f)", line, base, after)
		}
		if err := p.SetRegimeFunding(line, 0); err != nil {
			t.Fatalf("reset %s: %v", line, err)
		}
	}

	// Re-entry: each of the three sub-inputs lowers the rate.
	for _, kind := range []ReentryKind{ReentryProbation, ReentryEmployment, ReentryHousing} {
		if err := p.SetReentrySupport(kind, 1.0); err != nil {
			t.Fatalf("SetReentrySupport(%s): %v", kind, err)
		}
		after, _ := p.ReoffendingRate(1)
		if after >= base {
			t.Fatalf("re-entry %s must lower reoffending (base %.3f → %.3f)", kind, base, after)
		}
		if err := p.SetReentrySupport(kind, 0); err != nil {
			t.Fatalf("reset %s: %v", kind, err)
		}
	}
}

// AC-6: the youth pipeline is cheaper than the adult pipeline for every
// offence severity.
func TestYouthPipelineCheaperThanAdult(t *testing.T) {
	p := newTestPrison(t)
	for _, o := range []OffenceClass{OffenceMinor, OffenceSerious, OffenceViolent} {
		youth, err := p.YouthPipelineCost(o)
		if err != nil {
			t.Fatalf("YouthPipelineCost(%s): %v", o, err)
		}
		adult, err := p.AdultPipelineCost(o)
		if err != nil {
			t.Fatalf("AdultPipelineCost(%s): %v", o, err)
		}
		if youth >= adult {
			t.Fatalf("youth cost %d must be < adult %d for %s", youth, adult, o)
		}
	}
}

// AC-7: overcrowding degrades ALL three regime effects in the same tick.
func TestOvercrowdingDegradesAllThree(t *testing.T) {
	p := newTestPrison(t)
	if err := p.SetCapacity(10); err != nil {
		t.Fatalf("SetCapacity: %v", err)
	}
	for _, line := range []RegimeLine{RegimeEducation, RegimeWork, RegimeAddictionTreatment} {
		if err := p.SetRegimeFunding(line, 1000); err != nil {
			t.Fatalf("SetRegimeFunding(%s): %v", line, err)
		}
	}
	baseE, baseW, baseA := p.EducationEffect(), p.WorkEffect(), p.AddictionEffect()
	if err := p.SetSoldPlaces(15); err != nil { // 0 domestic + 15 sold > 10 capacity
		t.Fatalf("SetSoldPlaces: %v", err)
	}
	if e := p.EducationEffect(); e >= baseE {
		t.Fatalf("education must degrade under overcrowding (base %.3f → %.3f)", baseE, e)
	}
	if w := p.WorkEffect(); w >= baseW {
		t.Fatalf("work must degrade under overcrowding (base %.3f → %.3f)", baseW, w)
	}
	if a := p.AddictionEffect(); a >= baseA {
		t.Fatalf("addiction must degrade under overcrowding (base %.3f → %.3f)", baseA, a)
	}
}

// AC-8: sold places are a distinct commitment from domestic population but
// degrade capacity identically to an equivalent domestic increase.
func TestSoldPlacesCountIdenticallyToDomestic(t *testing.T) {
	effect := func(domestic, sold int64) float64 {
		p := newTestPrison(t)
		_ = p.SetCapacity(10)
		_ = p.SetRegimeFunding(RegimeEducation, 1000)
		for i := int64(0); i < domestic; i++ {
			admit(t, p, uint64(5000+i), crime.DistrictID(1), 1, OffenceMinor, "", false)
		}
		_ = p.SetSoldPlaces(sold)
		return p.EducationEffect()
	}
	if got := effect(15, 0); got >= 0.08 {
		t.Fatalf("15 domestic over 10 capacity should degrade, got %.3f", got)
	}
	if a, b := effect(5, 10), effect(15, 0); a != b {
		t.Fatalf("5 domestic + 10 sold must degrade identically to 15 domestic: %.6f vs %.6f", a, b)
	}
}

// AC-10: an unknown citizen or an unregistered category is rejected with a
// registry-sourced error and no placeholder record.
func TestUnknownCitizenRejected(t *testing.T) {
	p := newTestPrison(t)
	_ = p.SetCitizenExists(func(id uint64) bool { return id != 999 })
	err := p.Admit(Admission{CitizenID: 999, District: 1, Month: 1, Offence: OffenceMinor, SentenceMonths: 12})
	if err == nil {
		t.Fatal("Admit of an unknown citizen must error")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrUnknownCitizen {
		t.Fatalf("want ErrUnknownCitizen (MET-G4301), got %v", err)
	}
	if _, ok, _ := p.Cohort(999); ok {
		t.Fatal("no placeholder inmate record may be created")
	}
}

func TestUnregisteredCategoryRejected(t *testing.T) {
	p := newTestPrison(t)
	err := p.Admit(Admission{CitizenID: 1, District: 1, Month: 1, Offence: OffenceMinor, Category: "supermax", SentenceMonths: 12})
	if err == nil {
		t.Fatal("Admit with an unregistered category must error")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrUnregisteredCategory {
		t.Fatalf("want ErrUnregisteredCategory (MET-G4302), got %v", err)
	}
}

// AC-11: a double release or an out-of-range funding command is rejected with
// a typed error, never silently no-op'd or clamped.
func TestDoubleReleaseRejected(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, "", false)
	if err := p.Release(1); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	var e *errs.E
	if err := p.Release(1); !errors.As(err, &e) || e.Code != ErrAlreadyReleased {
		t.Fatalf("second Release must be ErrAlreadyReleased (MET-G4303), got %v", err)
	}
}

// Re-admitting a citizen whose cohort is still unreleased is rejected with
// ErrInvalidAdmission (reason "already admitted") and mutates nothing: the
// population, cohort record, and intake ledger stay exactly as the first
// admission left them — never a double increment, never a silently
// overwritten first-admission record.
func TestReAdmitUnreleasedRejected(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)

	before, ok, err := p.Cohort(1)
	if err != nil || !ok {
		t.Fatalf("Cohort(1) after first admit: ok=%v err=%v", ok, err)
	}

	err = p.Admit(Admission{CitizenID: 1, District: 1, Month: 1, Offence: OffenceViolent, Category: CategoryHighSecurity, SentenceMonths: 60})
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrInvalidAdmission {
		t.Fatalf("re-admit must be ErrInvalidAdmission (MET-G4306), got %v", err)
	}
	if e.Ctx["reason"] != "already admitted" {
		t.Fatalf("re-admit error must carry reason=already admitted, got %v", e.Ctx)
	}

	if got := p.DomesticPopulation(); got != 1 {
		t.Fatalf("population = %d, want 1 (re-admit must not double-increment)", got)
	}
	if got := p.IntakeCount(1, 1); got != 1 {
		t.Fatalf("IntakeCount = %d, want 1 (re-admit must not double-count)", got)
	}
	if len(p.admissions) != 1 {
		t.Fatalf("ledger length = %d, want 1 (re-admit must not append)", len(p.admissions))
	}

	after, ok, err := p.Cohort(1)
	if err != nil || !ok {
		t.Fatalf("Cohort(1) after re-admit: ok=%v err=%v", ok, err)
	}
	if after.Offence != before.Offence || after.Category != before.Category || after.SentenceMonths != before.SentenceMonths {
		t.Fatalf("cohort record must not be overwritten by re-admit: before=%+v after=%+v", before, after)
	}
}

// A citizen whose prior cohort has already been released may be re-admitted
// normally — the re-admit guard targets only a still-live cohort.
func TestReAdmitAfterReleaseAllowed(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	if err := p.Release(1); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := p.Admit(Admission{CitizenID: 1, District: 1, Month: 2, Offence: OffenceViolent, Category: CategoryHighSecurity, SentenceMonths: 60}); err != nil {
		t.Fatalf("re-admit after release must be accepted, got %v", err)
	}
	if got := p.DomesticPopulation(); got != 1 {
		t.Fatalf("population = %d, want 1 after release + re-admit", got)
	}
	if got := p.IntakeCount(1, 2); got != 1 {
		t.Fatalf("IntakeCount(1,2) = %d, want 1", got)
	}
}

func TestInvalidRegimeFundingRejected(t *testing.T) {
	p := newTestPrison(t)
	var e *errs.E
	if err := p.SetRegimeFunding(RegimeEducation, -5); !errors.As(err, &e) || e.Code != ErrInvalidRegimeFunding {
		t.Fatalf("negative funding must be ErrInvalidRegimeFunding (MET-G4304), got %v", err)
	}
	if err := p.SetRegimeFunding("notALine", 5); !errors.As(err, &e) || e.Code != ErrInvalidRegimeFunding {
		t.Fatalf("unknown line must be ErrInvalidRegimeFunding, got %v", err)
	}
}

// AC-12 (determinism): the reoffend draw is a pure function of (seed, id,
// month, purpose) — same seed, same outcome.
func TestReoffendedDeterministic(t *testing.T) {
	p1 := newTestPrison(t)
	p2 := newTestPrison(t)
	admit(t, p1, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	admit(t, p2, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	o1, err := p1.Reoffended(1)
	if err != nil {
		t.Fatalf("Reoffended(p1): %v", err)
	}
	o2, err := p2.Reoffended(1)
	if err != nil {
		t.Fatalf("Reoffended(p2): %v", err)
	}
	if o1 != o2 {
		t.Fatalf("same seed/id/month/purpose must yield the same outcome, got %v vs %v", o1, o2)
	}
}

// AC-14: concurrent admission/release across categories is safe (run with
// -race).
func TestConcurrentAdmitRelease(t *testing.T) {
	p := newTestPrison(t)
	_ = p.SetCapacity(100000)
	const goroutines = 8
	const per = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id := uint64(base*1000 + i)
				cat := CategoryOpen
				if i%3 == 0 {
					cat = CategoryStandard
				}
				if err := p.Admit(Admission{CitizenID: id, District: 1, Month: 1, Offence: OffenceSerious, Category: cat, SentenceMonths: 12}); err != nil {
					t.Errorf("Admit(%d): %v", id, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if got := p.DomesticPopulation(); got != goroutines*per {
		t.Fatalf("population = %d, want %d", got, goroutines*per)
	}

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := p.Release(uint64(base*1000 + i)); err != nil {
					t.Errorf("Release(%d): %v", base*1000+i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if got := p.DomesticPopulation(); got != 0 {
		t.Fatalf("population after release = %d, want 0", got)
	}
}

// AC-9 interim (BUG-058): a rehab-spend increase without a FuseYears tag in
// [5,15] or without a local projected-consequence value is rejected.
func TestSlowFuseRehabPreSubmissionCheck(t *testing.T) {
	p := newTestPrison(t)
	// missing local projection → rejected
	err := p.RehabSpend(RehabSpendRequest{Line: RegimeEducation, Increase: 100, FuseYears: 10})
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrSlowFuseRejected {
		t.Fatalf("missing projection must be ErrSlowFuseRejected (MET-G4307), got %v", err)
	}
	// out-of-range FuseYears → rejected
	err = p.RehabSpend(RehabSpendRequest{Line: RegimeEducation, Increase: 100, FuseYears: 20, ProjectedConsequence: f64p(0.5)})
	if !errors.As(err, &e) || e.Code != ErrSlowFuseRejected {
		t.Fatalf("out-of-range FuseYears must be ErrSlowFuseRejected, got %v", err)
	}
	// valid → applied
	if err := p.RehabSpend(RehabSpendRequest{Line: RegimeEducation, Increase: 100, FuseYears: 10, ProjectedConsequence: f64p(0.5)}); err != nil {
		t.Fatalf("valid rehab-spend must be accepted, got %v", err)
	}
	if got, _ := p.RegimeFunding(RegimeEducation); got != 100 {
		t.Fatalf("education funding after rehab-spend = %d, want 100", got)
	}
}

// f64p returns a pointer to v. RehabSpendRequest.ProjectedConsequence is a
// *float64 so a missing projection (nil) is distinguishable from a supplied
// zero; this helper builds the pointer literal the valid cases need.
func f64p(v float64) *float64 { return &v }

// US-2/AC-1: the reoffend-or-not outcome is persisted back onto the cohort
// record, so Cohort(id).Reoffended reflects the same outcome Reoffended(id)
// returned — the field is a real, queryable result, not a dead slot.
func TestReoffendedPersistsToCohort(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)

	outcome, err := p.Reoffended(1)
	if err != nil {
		t.Fatalf("Reoffended: %v", err)
	}
	rec, ok, err := p.Cohort(1)
	if err != nil {
		t.Fatalf("Cohort: %v", err)
	}
	if !ok {
		t.Fatal("cohort record must exist after Reoffended")
	}
	if rec.Reoffended != outcome {
		t.Fatalf("Cohort(1).Reoffended = %v, want the returned outcome %v", rec.Reoffended, outcome)
	}

	// A second draw is deterministic and keeps the stored value in sync.
	outcome2, err := p.Reoffended(1)
	if err != nil {
		t.Fatalf("Reoffended (2nd): %v", err)
	}
	if outcome2 != outcome {
		t.Fatalf("deterministic draw changed across calls: %v vs %v", outcome, outcome2)
	}
	rec, _, _ = p.Cohort(1)
	if rec.Reoffended != outcome2 {
		t.Fatalf("Cohort(1).Reoffended = %v after second draw, want %v", rec.Reoffended, outcome2)
	}
}

// AC-5 Base term: the reoffending base rate moves in the documented direction
// — rising with offence severity (minor → serious → violent) at a fixed age
// band. Matched categories and zero regime/re-entry funding isolate the Base
// term so the assertion targets exactly the data-loaded offence table.
func TestReoffendingBaseRisesWithSeverity(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)
	admit(t, p, 2, crime.DistrictID(1), 1, OffenceSerious, CategoryStandard, false)
	admit(t, p, 3, crime.DistrictID(1), 1, OffenceViolent, CategoryHighSecurity, false)

	minor, err := p.ReoffendingRate(1)
	if err != nil {
		t.Fatalf("ReoffendingRate(minor): %v", err)
	}
	serious, err := p.ReoffendingRate(2)
	if err != nil {
		t.Fatalf("ReoffendingRate(serious): %v", err)
	}
	violent, err := p.ReoffendingRate(3)
	if err != nil {
		t.Fatalf("ReoffendingRate(violent): %v", err)
	}
	if !(minor < serious && serious < violent) {
		t.Fatalf("base rate must rise with severity: minor %.3f, serious %.3f, violent %.3f", minor, serious, violent)
	}
}

// AC-5 Base term, age band: the youth base is lower than the adult base at
// equal offence severity (the prevention synergy §43/§28 names, held as data).
func TestReoffendingBaseYouthBelowAdult(t *testing.T) {
	p := newTestPrison(t)
	admit(t, p, 1, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, true)
	admit(t, p, 2, crime.DistrictID(1), 1, OffenceMinor, CategoryOpen, false)

	youth, err := p.ReoffendingRate(1)
	if err != nil {
		t.Fatalf("ReoffendingRate(youth): %v", err)
	}
	adult, err := p.ReoffendingRate(2)
	if err != nil {
		t.Fatalf("ReoffendingRate(adult): %v", err)
	}
	if youth >= adult {
		t.Fatalf("youth base must be below adult base at equal severity: youth %.3f, adult %.3f", youth, adult)
	}
}

// assertWorkingAPI constructs and exercises a working API from a real config
// (not the inline testConfig): wire the existence predicate, admit a
// prisoner, and read back the cohort record and reoffending outcome.
func assertWorkingAPI(t *testing.T, p *PrisonAPI) {
	t.Helper()
	if err := p.SetCitizenExists(func(uint64) bool { return true }); err != nil {
		t.Fatalf("SetCitizenExists: %v", err)
	}
	if err := p.Admit(Admission{CitizenID: 7, District: crime.DistrictID(1), Month: 1, Offence: OffenceMinor, Category: CategoryOpen, SentenceMonths: 12}); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	rec, ok, err := p.Cohort(7)
	if err != nil || !ok {
		t.Fatalf("Cohort(7): ok=%v err=%v", ok, err)
	}
	if rec.Category != CategoryOpen || rec.Offence != OffenceMinor {
		t.Fatalf("cohort record from real data mismatch: %+v", rec)
	}
	if _, err := p.ReoffendingRate(7); err != nil {
		t.Fatalf("ReoffendingRate(7): %v", err)
	}
	if _, err := p.Reoffended(7); err != nil {
		t.Fatalf("Reoffended(7): %v", err)
	}
}

// AC-1/GR#15: Load reads the real data/prison.json from a directory resolved
// via foundation/data.ResolveDataDir and constructs a working API — not
// merely the inline testConfig path.
func TestLoadLoadsRealPrisonJSON(t *testing.T) {
	dir, err := data.ResolveDataDir("load-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	p, err := Load(dir, "load-test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertWorkingAPI(t, p)
}

// AC-1/GR#15: LoadDefault resolves the data directory itself and loads the
// real data/prison.json into a working API.
func TestLoadDefaultLoadsRealPrisonJSON(t *testing.T) {
	p, err := LoadDefault("load-default-test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	assertWorkingAPI(t, p)
}
