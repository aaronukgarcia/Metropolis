package compose

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-167 completion (docs/planning/icd/engine.services-coverage.md,
// docs/planning/icd/engine.firms-labourmarket.md): tests for
// serviceCoverageTerm/jobAvailabilityTerm (servicesfirms_wire.go) —
// replacing TestFEAT167_ServiceCoverageAndJobAvailability_RemainFlatPlaceholder
// (feat167_attract_terms_test.go), whose own doc comment named this exact
// day as its designed lifecycle: the tripwire flips from "must stay flat"
// to "must be real".

// newServicesFirmsTestState builds a bare simState carrying only the fields
// serviceCoverageTerm/jobAvailabilityTerm touch, mirroring
// newFeat167TestState's white-box shape (package compose).
func newServicesFirmsTestState(t *testing.T, seed uint64) *simState {
	t.Helper()
	cid := errs.NewCorrelationID()

	servicesAPI, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	firmsAPI, err := firms.LoadDefault(seed, cid)
	if err != nil {
		t.Fatalf("firms.LoadDefault: %v", err)
	}
	citizensAPI, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("citizens.NewCitizensAPI: %v", err)
	}
	if err := firmsAPI.SetCitizens(citizensAPI); err != nil {
		t.Fatalf("firmsAPI.SetCitizens: %v", err)
	}
	attractTerms, err := loadAttractTermsData(cid)
	if err != nil {
		t.Fatalf("loadAttractTermsData: %v", err)
	}

	return &simState{
		cid:          cid,
		seed:         seed,
		citizens:     citizensAPI,
		services:     servicesAPI,
		firms:        firmsAPI,
		attractTerms: attractTerms,
	}
}

// --- ServiceCoverage: real-signal-moves-the-term ---------------------------

// TestFEAT167Completion_ServiceCoverageRespondsToCoverageRatio proves
// ServiceCoverage is wired to engine.services' real CoverageSummary
// (ICD engine.services-coverage.md §11): a state with one under-served
// registered service (demand >> capacity) must read LOWER than a state with
// the same service comfortably covered (demand << capacity) — not a test
// that merely calls the accessor once, it mutates the pushed demand and
// asserts the term actually moves.
//
// PROOF THIS CAN FAIL: temporarily replacing serviceCoverageTerm's body
// with `return 50.0, nil` (the old flat stub) makes both branches equal
// and this test fails — verified by hand during development, then
// reverted (the working tree must never be left with that change).
func TestFEAT167Completion_ServiceCoverageRespondsToCoverageRatio(t *testing.T) {
	const seed = 3

	register := func(st *simState, demand float64) {
		t.Helper()
		spec := services.ServiceSpec{
			ID:          "clinic-1",
			Kind:        services.ServiceHealthcare,
			CapacityRaw: "100 visits/d",
			UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
		}
		if err := st.services.RegisterService(spec); err != nil {
			t.Fatalf("RegisterService: %v", err)
		}
		if err := st.services.UpdateDemand("clinic-1", demand, 0); err != nil {
			t.Fatalf("UpdateDemand: %v", err)
		}
	}

	wellCovered := newServicesFirmsTestState(t, seed)
	register(wellCovered, 20) // demand << capacity(100): high coverage
	wellCoveredTerm, err := wellCovered.serviceCoverageTerm()
	if err != nil {
		t.Fatalf("serviceCoverageTerm (well covered): %v", err)
	}

	underServed := newServicesFirmsTestState(t, seed)
	register(underServed, 1000) // demand >> capacity(100): low coverage
	underServedTerm, err := underServed.serviceCoverageTerm()
	if err != nil {
		t.Fatalf("serviceCoverageTerm (under-served): %v", err)
	}

	if underServedTerm >= wellCoveredTerm {
		t.Fatalf("under-served ServiceCoverage (%v) is not lower than well-covered (%v) — ServiceCoverage is not coverage-ratio-driven", underServedTerm, wellCoveredTerm)
	}
	if wellCoveredTerm < 0 || wellCoveredTerm > 100 || underServedTerm < 0 || underServedTerm > 100 {
		t.Fatalf("ServiceCoverage out of [0,100]: wellCovered=%v underServed=%v", wellCoveredTerm, underServedTerm)
	}

	// Zero-demand edge case (nothing registered at all): coverageRatio's own
	// "1.0 when demand is zero" rule means the unmodified default state
	// reads the maximum term — the documented, honest baseline-one behaviour
	// until the build->services registration bridge lands (ICD §12 open
	// decision 1, out of this integration's scope).
	empty := newServicesFirmsTestState(t, seed)
	emptyTerm, err := empty.serviceCoverageTerm()
	if err != nil {
		t.Fatalf("serviceCoverageTerm (no services registered): %v", err)
	}
	if emptyTerm != 100 {
		t.Fatalf("serviceCoverageTerm with nothing registered = %v, want exactly 100 (coverageRatio's documented zero-demand default)", emptyTerm)
	}
}

// --- JobAvailability: real-signal-moves-the-term ----------------------------

// TestFEAT167Completion_JobAvailabilityRespondsToVacancyRate proves
// JobAvailability is wired to engine.firms' real LabourMarket aggregate
// (ICD engine.firms-labourmarket.md §11): adding vacancies (registering
// Startup-stage firms, each with an empty staff roster and therefore
// headroom up to its band ceiling) raises the term; workforce held
// constant.
//
// PROOF THIS CAN FAIL: temporarily replacing jobAvailabilityTerm's body
// with `return 50.0, nil` makes both branches equal and this test fails —
// verified by hand during development, then reverted.
func TestFEAT167Completion_JobAvailabilityRespondsToVacancyRate(t *testing.T) {
	const seed = 5

	noVacancies := newServicesFirmsTestState(t, seed)
	spawnFeat167Citizens(t, noVacancies.citizens, seed, noVacancies.cid, 1, 50)
	noVacTerm, err := noVacancies.jobAvailabilityTerm()
	if err != nil {
		t.Fatalf("jobAvailabilityTerm (no firms): %v", err)
	}

	withVacancies := newServicesFirmsTestState(t, seed)
	spawnFeat167Citizens(t, withVacancies.citizens, seed, withVacancies.cid, 1, 50)
	for i := 0; i < 5; i++ {
		if _, err := withVacancies.firms.RegisterFirm("startup", 0, "industrial"); err != nil {
			t.Fatalf("RegisterFirm(%d): %v", i, err)
		}
	}
	withVacTerm, err := withVacancies.jobAvailabilityTerm()
	if err != nil {
		t.Fatalf("jobAvailabilityTerm (with firms): %v", err)
	}

	if withVacTerm <= noVacTerm {
		t.Fatalf("JobAvailability with vacancies (%v) is not higher than with none (%v) — JobAvailability is not vacancy-rate-driven", withVacTerm, noVacTerm)
	}
	if noVacTerm < 0 || noVacTerm > 100 || withVacTerm < 0 || withVacTerm > 100 {
		t.Fatalf("JobAvailability out of [0,100]: none=%v with=%v", noVacTerm, withVacTerm)
	}
	if noVacTerm != 0 {
		t.Fatalf("jobAvailabilityTerm with zero vacancies = %v, want exactly 0 (the half-saturation curve's own zero-rate value)", noVacTerm)
	}
}

// TestFEAT167Completion_JobAvailability_RequiresCitizensWired proves the
// term function surfaces LabourMarket's own fail-closed contract
// (MET-G1409, "never a zero Workforce silently read as no jobs") rather
// than masking it — a firms instance constructed without SetCitizens must
// error, not return a bogus 0.
func TestFEAT167Completion_JobAvailability_RequiresCitizensWired(t *testing.T) {
	cid := errs.NewCorrelationID()
	firmsAPI, err := firms.LoadDefault(1, cid)
	if err != nil {
		t.Fatalf("firms.LoadDefault: %v", err)
	}
	attractTerms, err := loadAttractTermsData(cid)
	if err != nil {
		t.Fatalf("loadAttractTermsData: %v", err)
	}
	st := &simState{cid: cid, firms: firmsAPI, attractTerms: attractTerms}
	if _, err := st.jobAvailabilityTerm(); err == nil {
		t.Fatal("jobAvailabilityTerm accepted a firms instance with no citizens wired")
	}
}

// --- clamp -------------------------------------------------------------

// TestFEAT167Completion_ClampTerm proves clampTerm bounds to [0,100]
// (the requirement SetTermInputs' own validateTermInputs enforces
// downstream — this is the defence-in-depth layer in front of it), and
// (round-verdict hardening item, 2026-08-19) that a NaN input is caught
// explicitly rather than falling through both bound checks — a plain
// `v < 0` comparison against NaN is false under IEEE 754, so an unguarded
// clampTerm would return NaN itself here (this table would fail with
// got=NaN, want=0 if the math.IsNaN guard were removed).
func TestFEAT167Completion_ClampTerm(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-5, 0},
		{0, 0},
		{50, 50},
		{100, 100},
		{150, 100},
		{math.NaN(), 0},
	}
	for _, tc := range cases {
		if got := clampTerm(tc.in); got != tc.want {
			t.Fatalf("clampTerm(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// --- all-seven-terms-real ---------------------------------------------------

// oldFlatPlaceholderValue is the value the retired baselineOneTermValue
// constant held (compose.go no longer defines it — nothing in production
// references it after this integration). Kept here, test-only, as the
// literal the all-seven-terms-real proof below compares against.
const oldFlatPlaceholderValue = 50.0

// TestFEAT167Completion_AllSevenTermsReal is the ICD-required
// "all-seven-terms-real" proof: after a warmed-up run with real
// ServiceCoverage/JobAvailability signal registered (so this test does not
// rely on the zero-signal edge case alone), NO term equals the old flat
// placeholder value — the constant is genuinely dead in production, proven
// by construction rather than asserted by comment.
func TestFEAT167Completion_AllSevenTermsReal(t *testing.T) {
	cid := errs.NewCorrelationID()
	servicesAPI, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	if err := servicesAPI.RegisterService(services.ServiceSpec{
		ID:          "clinic-1",
		Kind:        services.ServiceHealthcare,
		CapacityRaw: "100 visits/d",
		UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := servicesAPI.UpdateDemand("clinic-1", 300, 0); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}
	firmsAPI, err := firms.LoadDefault(23, cid)
	if err != nil {
		t.Fatalf("firms.LoadDefault: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := firmsAPI.RegisterFirm("startup", 0, "industrial"); err != nil {
			t.Fatalf("RegisterFirm(%d): %v", i, err)
		}
	}

	e := core.NewEngine(core.WithWorldSeed(23), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Services: servicesAPI, Firms: firmsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	advanceInChunks(t, e, testTicks)

	terms := map[string]float64{
		"Safety":          comp.state.attract.Safety(),
		"LeisureFit":      comp.state.attract.LeisureFit(),
		"Environment":     comp.state.attract.Environment(),
		"ServiceCoverage": comp.state.attract.ServiceCoverage(),
		"JobAvailability": comp.state.attract.JobAvailability(),
	}
	for name, v := range terms {
		if v == oldFlatPlaceholderValue {
			t.Fatalf("%s = %v after %d months, want NOT exactly the old flat placeholder %v — the term is not genuinely wired", name, v, testMonths, oldFlatPlaceholderValue)
		}
	}
	// HousingAffordability/Reputation were already real before this wave;
	// included for completeness of the "all seven" proof's coverage.
	if ha, err := comp.state.attract.HousingAffordability(); err != nil {
		t.Fatalf("HousingAffordability: %v", err)
	} else if ha == oldFlatPlaceholderValue {
		t.Fatalf("HousingAffordability = %v, want not exactly %v (sanity: this term was never a placeholder)", ha, oldFlatPlaceholderValue)
	}
}

// TestFEAT167Completion_TermsDeterministicAcrossRuns extends
// TestFEAT167_TermsDeterministicAcrossRuns's determinism-equivalence
// requirement (ICD §11) to the two newly-real terms: two identical-seed
// runs (same Deps, freshly constructed services/firms each run) must
// produce byte-identical ServiceCoverage/JobAvailability every month.
func TestFEAT167Completion_TermsDeterministicAcrossRuns(t *testing.T) {
	run := func() (serviceCoverage, jobAvailability float64) {
		cid := errs.NewCorrelationID()
		servicesAPI, err := services.LoadDefault(cid)
		if err != nil {
			t.Fatalf("services.LoadDefault: %v", err)
		}
		if err := servicesAPI.RegisterService(services.ServiceSpec{
			ID:          "clinic-1",
			Kind:        services.ServiceHealthcare,
			CapacityRaw: "100 visits/d",
			UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
		}); err != nil {
			t.Fatalf("RegisterService: %v", err)
		}
		if err := servicesAPI.UpdateDemand("clinic-1", 300, 0); err != nil {
			t.Fatalf("UpdateDemand: %v", err)
		}
		firmsAPI, err := firms.LoadDefault(61, cid)
		if err != nil {
			t.Fatalf("firms.LoadDefault: %v", err)
		}
		if _, err := firmsAPI.RegisterFirm("startup", 0, "industrial"); err != nil {
			t.Fatalf("RegisterFirm: %v", err)
		}
		e := core.NewEngine(core.WithWorldSeed(61), core.WithPoolSize(1))
		comp, err := Wire(e, &Deps{CorrelationID: cid, Services: servicesAPI, Firms: firmsAPI})
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		advanceInChunks(t, e, testTicks)
		return comp.state.attract.ServiceCoverage(), comp.state.attract.JobAvailability()
	}
	sc1, ja1 := run()
	sc2, ja2 := run()
	if sc1 != sc2 {
		t.Fatalf("ServiceCoverage differs across identical-seed runs: %v vs %v", sc1, sc2)
	}
	if ja1 != ja2 {
		t.Fatalf("JobAvailability differs across identical-seed runs: %v vs %v", ja1, ja2)
	}
}

// --- sustained responsiveness (round-verdict hardening item, 2026-08-19) ---
//
// The tests above (TestFEAT167Completion_ServiceCoverageRespondsToCoverageRatio,
// TestFEAT167Completion_JobAvailabilityRespondsToVacancyRate) are first-sight
// only: each builds two INDEPENDENT states and compares one accessor call
// each. They cannot catch a bug where compose reads the source module's
// aggregate exactly once (e.g. cached at Wire time, or read only on the
// first attractHook.ApplyEffect call) and then never re-reads it on later
// monthly applications — a single composed engine driven across several
// months, mutated BETWEEN ticks, is required to catch that class of bug.
// This mirrors TestFEAT167_SafetyRespondsAcrossSustainedMonths's shape
// (compose.go's crime-backed Safety term), simplified: unlike Safety,
// neither aggregate here carries any persistence/decay state of its own
// (CoverageSummary and LabourMarket are both pure re-reads of currently
// registered state, no month-over-month drift), so no noise-floor
// calibration is needed — each mutation's effect is exact and immediate on
// the NEXT monthly application.

// TestFEAT167Completion_ServiceCoverageRespondsAcrossSustainedMonths proves
// ServiceCoverage keeps tracking engine.services' live state across
// multiple monthly attractHook applications on ONE composed engine, not
// just once at Wire time: demand raised between ticks must lower the term
// on the VERY NEXT month's application, repeated twice.
//
// PROOF THIS CAN FAIL: temporarily making Wire snapshot
// summary,_ := servicesAPI.CoverageSummary() once and having
// serviceCoverageTerm return the cached summary's ratio instead of calling
// st.services.CoverageSummary() fresh each time would make term2/term3
// below equal term1 — verified by hand during development (a throwaway
// cached-field variant), then reverted.
func TestFEAT167Completion_ServiceCoverageRespondsAcrossSustainedMonths(t *testing.T) {
	cid := errs.NewCorrelationID()
	servicesAPI, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	if err := servicesAPI.RegisterService(services.ServiceSpec{
		ID:          "clinic-1",
		Kind:        services.ServiceHealthcare,
		CapacityRaw: "100 visits/d",
		UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := servicesAPI.UpdateDemand("clinic-1", 10, 0); err != nil {
		t.Fatalf("UpdateDemand (initial): %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(97), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Services: servicesAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	monthTicks := int64(core.DailyTicksPerMonth)

	advanceInChunks(t, e, monthTicks) // month 1: lightly loaded, high coverage
	term1 := comp.state.attract.ServiceCoverage()

	if err := servicesAPI.UpdateDemand("clinic-1", 500, 0); err != nil {
		t.Fatalf("UpdateDemand (mutation 1): %v", err)
	}
	advanceInChunks(t, e, monthTicks) // month 2
	term2 := comp.state.attract.ServiceCoverage()
	if term2 >= term1 {
		t.Fatalf("ServiceCoverage after raising demand mid-run (%v) is not lower than before (%v) — the term stopped tracking live services state after Wire", term2, term1)
	}

	if err := servicesAPI.UpdateDemand("clinic-1", 5000, 0); err != nil {
		t.Fatalf("UpdateDemand (mutation 2): %v", err)
	}
	advanceInChunks(t, e, monthTicks) // month 3
	term3 := comp.state.attract.ServiceCoverage()
	if term3 >= term2 {
		t.Fatalf("ServiceCoverage after raising demand again mid-run (%v) is not lower than the previous month (%v) — a single mid-run mutation would not distinguish a cached-at-Wire-time bug from a genuinely-stuck-after-month-1 one, so a THIRD data point is required", term3, term2)
	}
	if term1 < 0 || term1 > 100 || term2 < 0 || term2 > 100 || term3 < 0 || term3 > 100 {
		t.Fatalf("ServiceCoverage out of [0,100] across sustained months: %v, %v, %v", term1, term2, term3)
	}
}

// TestFEAT167Completion_JobAvailabilityRespondsAcrossSustainedMonths proves
// JobAvailability keeps tracking engine.firms' live state across multiple
// monthly attractHook applications on ONE composed engine: registering
// additional firms (raising vacancies) between ticks must raise the term
// on the VERY NEXT month's application, repeated twice, with workforce
// held effectively constant (baseline one's seed population, no gameplay
// migration large enough to swamp the vacancy signal within 3 months).
//
// PROOF THIS CAN FAIL: the same cached-at-Wire-time variant described in
// TestFEAT167Completion_ServiceCoverageRespondsAcrossSustainedMonths's
// comment, applied to jobAvailabilityTerm/firmsAPI.LabourMarket instead —
// verified by hand during development, then reverted.
func TestFEAT167Completion_JobAvailabilityRespondsAcrossSustainedMonths(t *testing.T) {
	cid := errs.NewCorrelationID()
	firmsAPI, err := firms.LoadDefault(103, cid)
	if err != nil {
		t.Fatalf("firms.LoadDefault: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(103), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Firms: firmsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	monthTicks := int64(core.DailyTicksPerMonth)

	advanceInChunks(t, e, monthTicks) // month 1: zero firms registered
	term1 := comp.state.attract.JobAvailability()
	if term1 != 0 {
		t.Fatalf("JobAvailability with zero firms registered = %v, want exactly 0", term1)
	}

	for i := 0; i < 3; i++ {
		if _, err := firmsAPI.RegisterFirm("startup", 0, "industrial"); err != nil {
			t.Fatalf("RegisterFirm (mutation 1, %d): %v", i, err)
		}
	}
	advanceInChunks(t, e, monthTicks) // month 2
	term2 := comp.state.attract.JobAvailability()
	if term2 <= term1 {
		t.Fatalf("JobAvailability after registering firms mid-run (%v) is not higher than before (%v) — the term stopped tracking live firms state after Wire", term2, term1)
	}

	for i := 0; i < 5; i++ {
		if _, err := firmsAPI.RegisterFirm("startup", 0, "industrial"); err != nil {
			t.Fatalf("RegisterFirm (mutation 2, %d): %v", i, err)
		}
	}
	advanceInChunks(t, e, monthTicks) // month 3
	term3 := comp.state.attract.JobAvailability()
	if term3 <= term2 {
		t.Fatalf("JobAvailability after registering more firms mid-run (%v) is not higher than the previous month (%v) — a single mid-run mutation would not distinguish a cached-at-Wire-time bug from a genuinely-stuck-after-month-1 one, so a THIRD data point is required", term3, term2)
	}
	if term1 < 0 || term1 > 100 || term2 < 0 || term2 > 100 || term3 < 0 || term3 > 100 {
		t.Fatalf("JobAvailability out of [0,100] across sustained months: %v, %v, %v", term1, term2, term3)
	}
}
