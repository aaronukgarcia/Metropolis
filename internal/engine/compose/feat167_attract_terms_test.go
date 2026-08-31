package compose

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-167 (docs/planning/icd/engine.attract-terms.md): tests for the real
// Safety/LeisureFit/Environment term wiring in compose.go's
// safetyTerm/leisureFitTerm/environmentTerm/registerLeisureVenues, plus the
// ServiceCoverage/JobAvailability honest-placeholder tripwire and
// determinism/end-to-end causal-chain proofs the ICD's §11 requires.

// --- fixtures ------------------------------------------------------------

// newFeat167TestState builds a bare simState carrying only the fields
// safetyTerm/leisureFitTerm/environmentTerm/registerLeisureVenues touch —
// NOT a full Wire()-composed engine. White-box (package compose), so it can
// reach every unexported field directly; this isolates the FEAT-167 term-
// computation logic from the rest of the composition root, the same way
// TestGameplay_DemolishCreditsCompensation (above, in compose_test.go)
// reaches comp.state directly.
func newFeat167TestState(t *testing.T, seed uint64) *simState {
	t.Helper()
	cid := errs.NewCorrelationID()

	crimeAPI, err := crime.New(seed, cid)
	if err != nil {
		t.Fatalf("crime.New: %v", err)
	}
	leisureAPI, err := leisure.LoadDefault(cid)
	if err != nil {
		t.Fatalf("leisure.LoadDefault: %v", err)
	}
	refuseAPI, err := refuse.LoadDefault(cid)
	if err != nil {
		t.Fatalf("refuse.LoadDefault: %v", err)
	}
	if err := refuseAPI.RegisterCell(citywideRefuseCellID, refuse.LandUseResidential, "citywide"); err != nil {
		t.Fatalf("RegisterCell: %v", err)
	}
	citizensAPI, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("citizens.NewCitizensAPI: %v", err)
	}
	attractTerms, err := loadAttractTermsData(cid)
	if err != nil {
		t.Fatalf("loadAttractTermsData: %v", err)
	}

	return &simState{
		cid:                     cid,
		seed:                    seed,
		citizens:                citizensAPI,
		crime:                   crimeAPI,
		leisure:                 leisureAPI,
		refuse:                  refuseAPI,
		attractTerms:            attractTerms,
		leisureVenuesRegistered: make(map[uint64]bool),
	}
}

// spawnFeat167Citizens births count sequential citizens (ids
// idBase..idBase+count-1) into api directly through the real command
// surface (GR#20) — mirrors simState.spawnCitizens but standalone, so a
// test can control population size without going through the full
// seedCitizenCount=64 Wire path.
func spawnFeat167Citizens(t *testing.T, api *citizens.CitizensAPI, seed uint64, cid string, idBase uint64, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		id := idBase + uint64(i)
		cit := citizens.Citizen{
			ID:          id,
			BirthMonth:  0,
			Personality: citizens.InitPersonality(seed, id, 0, citizens.Personality{}, citizens.Personality{}),
		}
		if err := api.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: cid,
			Kind:          citizens.LifeEventBirth,
			Citizen:       cit,
		}); err != nil {
			t.Fatalf("ApplyLifeEventCommand(%d): %v", id, err)
		}
	}
}

// --- Safety: real-signal-moves-the-term -----------------------------------

// TestFEAT167_SafetyTermRespondsToPopulation proves Safety is wired to
// engine.crime's real, population-driven generation model (ICD §11): two
// otherwise-identical states differing ONLY in population must diverge —
// higher EligiblePool -> higher crime generation -> lower SafetyTerm. Not a
// test that merely calls the accessor once: it mutates the population
// input and asserts the resulting term actually moves.
//
// PROOF THIS CAN FAIL: temporarily replacing safetyTerm's body with
// `return baselineOneTermValue, nil` (the old flat stub) makes both
// branches equal and this test fails — verified by hand during development,
// then reverted (the working tree must never be left with that change).
func TestFEAT167_SafetyTermRespondsToPopulation(t *testing.T) {
	const seed = 42
	low := newFeat167TestState(t, seed)
	spawnFeat167Citizens(t, low.citizens, seed, low.cid, 1, 10)
	lowSafety, err := low.safetyTerm(1)
	if err != nil {
		t.Fatalf("safetyTerm (low population): %v", err)
	}

	high := newFeat167TestState(t, seed)
	spawnFeat167Citizens(t, high.citizens, seed, high.cid, 1, 20000)
	highSafety, err := high.safetyTerm(1)
	if err != nil {
		t.Fatalf("safetyTerm (high population): %v", err)
	}

	if highSafety >= lowSafety {
		t.Fatalf("Safety with high population (%v) is not lower than with low population (%v) — Safety is not population-driven", highSafety, lowSafety)
	}
	if lowSafety < 0 || lowSafety > 100 || highSafety < 0 || highSafety > 100 {
		t.Fatalf("Safety out of [0,100]: low=%v high=%v", lowSafety, highSafety)
	}
}

// --- LeisureFit: real-signal-moves-the-term -------------------------------

// TestFEAT167_LeisureFitTermRespondsToVenueRegistration proves LeisureFit
// is wired to engine.leisure's real venue-supply model (ICD §11): a state
// with zero registered venues vs an otherwise-identical state with several
// venues covering every going-out category must diverge.
//
// PROOF THIS CAN FAIL: temporarily replacing leisureFitTerm's body with
// `return baselineOneTermValue, nil` makes both branches equal and this
// test fails — verified by hand during development, then reverted.
func TestFEAT167_LeisureFitTermRespondsToVenueRegistration(t *testing.T) {
	noVenues := newFeat167TestState(t, 7)
	noVenuesFit, err := noVenues.leisureFitTerm()
	if err != nil {
		t.Fatalf("leisureFitTerm (no venues): %v", err)
	}

	withVenues := newFeat167TestState(t, 7)
	for c := 0; c < leisure.NumCategories; c++ {
		if c == leisure.CategoryHome {
			continue
		}
		v := leisure.Venue{ID: uint64(c + 1), Category: c, District: 0, Capacity: 10_000}
		if err := withVenues.leisure.OpenVenue(v, withVenues.cid); err != nil {
			t.Fatalf("OpenVenue(%d): %v", c, err)
		}
	}
	withVenuesFit, err := withVenues.leisureFitTerm()
	if err != nil {
		t.Fatalf("leisureFitTerm (with venues): %v", err)
	}

	if withVenuesFit <= noVenuesFit {
		t.Fatalf("LeisureFit with venues (%v) is not higher than with zero venues (%v) — LeisureFit is not venue-driven", withVenuesFit, noVenuesFit)
	}
	if withVenuesFit < 0 || withVenuesFit > 100 || noVenuesFit < 0 || noVenuesFit > 100 {
		t.Fatalf("LeisureFit out of [0,100]: none=%v with=%v", noVenuesFit, withVenuesFit)
	}
}

// TestFEAT167_RegisterLeisureVenues_BridgesCompletedEntertainmentZone
// proves the compose->leisure venue-registration bridge itself (not just
// LeisureFitAggregate's own sensitivity above): a completed
// build.ZoneEntertainment order becomes exactly one leisure venue, and a
// non-entertainment / non-complete order does not.
func TestFEAT167_RegisterLeisureVenues_BridgesCompletedEntertainmentZone(t *testing.T) {
	cid := errs.NewCorrelationID()
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	// Provision generously so the order completes within the Tick loop
	// below, mirroring TestGameplay_DemolishCreditsCompensation's setup.
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(13), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	cell := protocol.CellRef{X: 2, Y: 2}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-buy"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-zone"),
		Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "entertainment"},
	}); !res.Accepted {
		t.Fatalf("Zone rejected: %+v", res.Error)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-build"),
		Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "entertainment"},
	}); !res.Accepted {
		t.Fatalf("Build rejected: %+v", res.Error)
	}

	tile := world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	local := world.CellLocal{Row: cell.Y, Col: cell.X}
	completed := false
	for i := int64(0); i < 300; i++ {
		if err := comp.state.buildAPI.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
		if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatalf("entertainment build order never completed after 300 ticks")
	}

	before := len(comp.state.leisureVenuesRegistered)
	comp.state.registerLeisureVenues()
	after := len(comp.state.leisureVenuesRegistered)
	if after <= before {
		t.Fatalf("registerLeisureVenues did not register a new venue for the completed entertainment zone (before=%d after=%d)", before, after)
	}

	// Idempotency: calling again must not re-register (map size unchanged).
	comp.state.registerLeisureVenues()
	if got := len(comp.state.leisureVenuesRegistered); got != after {
		t.Fatalf("registerLeisureVenues re-registered on a second call: %d -> %d", after, got)
	}
}

// --- Environment: real-signal-moves-the-term ------------------------------

// TestFEAT167_EnvironmentTermRespondsToUncollectedWaste proves Environment
// is wired to engine.refuse's real generation/accounting model (ICD §11).
// Baseline one never wires a refuse collection round into this integration
// (environmentTerm's own doc comment: no engine.logistics/engine.services
// dependency is injected into refuse here), so the honest way to mutate
// "the uncollected-tonnage input" is the same population lever safetyTerm
// uses: a higher population drives more monthly generation (refuse.
// Generate's driver), which accumulates as more uncollected tonnage with
// nothing ever collecting it — proving the real dependency chain, not a
// literal collection-on/off toggle this integration does not build.
//
// PROOF THIS CAN FAIL: temporarily replacing environmentTerm's body with
// `return baselineOneTermValue, nil` makes both branches equal and this
// test fails — verified by hand during development, then reverted.
func TestFEAT167_EnvironmentTermRespondsToUncollectedWaste(t *testing.T) {
	const seed = 9
	low := newFeat167TestState(t, seed)
	spawnFeat167Citizens(t, low.citizens, seed, low.cid, 1, 5)
	lowEnv, err := low.environmentTerm()
	if err != nil {
		t.Fatalf("environmentTerm (low population): %v", err)
	}

	high := newFeat167TestState(t, seed)
	spawnFeat167Citizens(t, high.citizens, seed, high.cid, 1, 50000)
	highEnv, err := high.environmentTerm()
	if err != nil {
		t.Fatalf("environmentTerm (high population): %v", err)
	}

	if highEnv >= lowEnv {
		t.Fatalf("Environment with high population (%v) is not lower than with low population (%v) — Environment is not uncollected-waste-driven", highEnv, lowEnv)
	}
	if lowEnv < 0 || lowEnv > 100 || highEnv < 0 || highEnv > 100 {
		t.Fatalf("Environment out of [0,100]: low=%v high=%v", lowEnv, highEnv)
	}
}

// --- placeholder tripwire lifecycle -----------------------------------------
//
// TestFEAT167_ServiceCoverageAndJobAvailability_RemainFlatPlaceholder used to
// live here: it asserted ServiceCoverage/JobAvailability stayed EXACTLY the
// flat baselineOneTermValue=50.0 constant, and was designed (its own doc
// comment said so) to fail loudly the day a follow-up wired either term for
// real. That follow-up is THIS wave (FEAT-167 completion,
// docs/planning/icd/engine.services-coverage.md /
// engine.firms-labourmarket.md) — flipping the tripwire from "must stay
// flat" to "must be real" IS its designed lifecycle, not a deletion of
// coverage. The replacement real-signal tests
// (TestFEAT167Completion_ServiceCoverageRespondsToCoverageRatio,
// TestFEAT167Completion_JobAvailabilityRespondsToVacancyRate, and the
// all-seven-terms-real proof) live in servicesfirms_wire_test.go, next to
// the term functions they exercise. baselineOneTermValue itself is gone
// from compose.go (nothing in production references it any more).

// --- determinism -----------------------------------------------------------

// TestFEAT167_TermsDeterministicAcrossRuns extends AC-17's determinism
// discipline to the three new derived term values (ICD §11's determinism-
// equivalence requirement): two identical-seed runs must produce
// byte-identical Safety/LeisureFit/Environment every month.
func TestFEAT167_TermsDeterministicAcrossRuns(t *testing.T) {
	run := func() (safety, leisureFit, environment float64) {
		e, comp := newTestEngine(t, 55)
		advanceInChunks(t, e, testTicks)
		return comp.state.attract.Safety(), comp.state.attract.LeisureFit(), comp.state.attract.Environment()
	}
	s1, l1, en1 := run()
	s2, l2, en2 := run()
	if s1 != s2 {
		t.Fatalf("Safety differs across identical-seed runs: %v vs %v", s1, s2)
	}
	if l1 != l2 {
		t.Fatalf("LeisureFit differs across identical-seed runs: %v vs %v", l1, l2)
	}
	if en1 != en2 {
		t.Fatalf("Environment differs across identical-seed runs: %v vs %v", en1, en2)
	}
}

// --- end-to-end causal chain -----------------------------------------------

// feat167WorsenedPopulationExtra/feat167WorsenedRunMonths bound the
// end-to-end test below. The extra population is large relative to
// baseline one's organic seed+growth (seedCitizenCount=64 plus a handful
// of months of fertility) so the Safety divergence is unmistakable within
// a short run, keeping the test's wall-clock cost bounded.
//
// feat167WorsenedRunMonths was 6 before FEAT-1972079927 (money circulation
// inc1) wired real household formation + HousingAffordability into the
// same monthly migration step this test exercises: by month 6 EITHER run
// (worsened or not) had already formed enough households to cross
// HousingAffordability's binary rent-burden threshold to 0 — baseline-one
// forms every household at a fixed 2-members/2-rooms capacity, so there is
// no per-household variance, only a citywide step function — and once
// BOTH runs saturate to affordability=0, that term dominates NetMigration
// identically in both branches and swamps the Safety differential this
// test is actually about. 2 months keeps both runs inside the
// affordability=100 "vacant/comfortable" window (proven by this file's own
// development-time instrumentation) so Safety's divergence is what drives
// the NetMigration difference, exactly as the test's own doc comment
// intends.
const (
	feat167WorsenedPopulationExtra = 4000
	feat167WorsenedRunMonths       = 2
)

// TestFEAT167_WorsenPopulation_LowersSafety_LowersNetMigration is the ICD
// §11 "migration responds end-to-end" test: worsen a real crime-relevant
// input (population growth with no offsetting change) -> assert Safety
// falls -> assert NetMigration's cumulative total is measurably lower than
// an otherwise-identical run without the population growth — a genuine
// causal chain, not two independent unit tests asserting unrelated facts.
func TestFEAT167_WorsenPopulation_LowersSafety_LowersNetMigration(t *testing.T) {
	const seed = 42
	run := func(extraPopulation int) (netMigration int64, safety float64) {
		cid := errs.NewCorrelationID()
		citizensAPI, err := citizens.NewCitizensAPI(seed, cid)
		if err != nil {
			t.Fatalf("citizens.NewCitizensAPI: %v", err)
		}
		if extraPopulation > 0 {
			// Pre-seed extra citizens at a disjoint high id range —
			// well clear of compose's own seedCitizenCount=64 sequential
			// mint (which starts at id 1 regardless of what an injected
			// Citizens API already holds) and of attract's migrant /
			// citizens' fertility id ranges.
			spawnFeat167Citizens(t, citizensAPI, seed, cid, 10_000_000, extraPopulation)
		}
		e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
		comp, err := Wire(e, &Deps{CorrelationID: cid, Citizens: citizensAPI})
		if err != nil {
			t.Fatalf("Wire (extra=%d): %v", extraPopulation, err)
		}
		advanceInChunks(t, e, feat167WorsenedRunMonths*int64(core.DailyTicksPerMonth))
		return comp.NetMigration(), comp.state.attract.Safety()
	}

	baseMigration, baseSafety := run(0)
	worsenedMigration, worsenedSafety := run(feat167WorsenedPopulationExtra)

	if worsenedSafety >= baseSafety {
		t.Fatalf("worsened-population Safety (%v) is not lower than the baseline (%v) — population growth did not worsen Safety", worsenedSafety, baseSafety)
	}
	if worsenedMigration >= baseMigration {
		t.Fatalf("worsened-population NetMigration (%d) is not lower than the baseline (%d) — a Safety fall did not measurably reduce net migration", worsenedMigration, baseMigration)
	}
}

// --- data-file failure paths (loadAttractTermsData) -------------------------

// TestFEAT167_LoadAttractTermsData_RejectsInvalidEnvironment proves the
// data-file validation failure path fails loudly with a registry-sourced
// error (GR#7) rather than silently defaulting — the closest available
// analogue to "Wire failure paths still fail loudly with zero hooks" for
// the three new source modules: crime.New/leisure.LoadDefault/
// refuse.LoadDefault have no fallible-loader test seam (unlike market's
// LoadMarket), so this exercises the one genuinely fallible new
// construction step FEAT-167 adds to Wire.
func TestFEAT167_LoadAttractTermsData_RejectsInvalidEnvironment(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"zero half-saturation", `{"version":1,"environment":{"pollutionHalfSaturationKg":0},"leisure":{"bridgeVenueCapacityUnits":500}}`},
		{"negative half-saturation", `{"version":1,"environment":{"pollutionHalfSaturationKg":-1},"leisure":{"bridgeVenueCapacityUnits":500}}`},
		{"zero venue capacity", `{"version":1,"environment":{"pollutionHalfSaturationKg":50000},"leisure":{"bridgeVenueCapacityUnits":0}}`},
		{"malformed JSON", `{not valid json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, attractTermsFile), []byte(tc.body), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			t.Setenv("METROPOLIS_DATA_DIR", dir)

			_, err := loadAttractTermsData(errs.NewCorrelationID())
			if err == nil {
				t.Fatalf("loadAttractTermsData accepted invalid data (%s)", tc.name)
			}
			var e *errs.E
			if !errors.As(err, &e) {
				t.Fatalf("error is not a registry-sourced *errs.E: %v", err)
			}
			if e.Code != ErrModuleFailed {
				t.Fatalf("error code = %q, want %q (ErrModuleFailed)", e.Code, ErrModuleFailed)
			}
		})
	}
}

// TestFEAT167_LoadAttractTermsData_MissingFile proves a missing data file
// fails loudly (GR#7) rather than silently defaulting.
func TestFEAT167_LoadAttractTermsData_MissingFile(t *testing.T) {
	dir := t.TempDir() // deliberately empty — no attract_terms.json
	t.Setenv("METROPOLIS_DATA_DIR", dir)

	_, err := loadAttractTermsData(errs.NewCorrelationID())
	if err == nil {
		t.Fatal("loadAttractTermsData accepted a missing data file")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrModuleFailed {
		t.Fatalf("error = %v, want ErrModuleFailed", err)
	}
}

// --- destructive round r1 (F1/F2/F3) regression tests ----------------------

// TestFEAT167_SafetyRespondsAcrossSustainedMonths is destructive round r1's
// F1 "sustained responsiveness" requirement: ONE running state, population
// growing across 3+ months, must show Safety strictly falling every month.
// The ORIGINAL bug (crime/api.go's AdvanceMonth seeding eligiblePool only
// `if st.eligiblePool == 0`) froze the pool at its month-1 value forever, so
// every push after month 1 was silently ignored — this would have made
// Safety flat (or worse, only reactive to gang recruitment) from month 2
// onward. PROOF THIS CAN FAIL: reverting crime/api.go's AdvanceMonth to the
// old `if st.eligiblePool == 0 { st.eligiblePool = d.EligiblePool }` gate
// (instead of the unconditional max(0, pushed-recruited) recompute) makes
// every month after the first report the SAME Safety value and this test
// fails.
//
// A naive "just check 4 consecutive months strictly fall" version of this
// test does NOT discriminate the bug reliably: even with the pool frozen at
// its month-1 value, the active-crime STOCK itself keeps drifting toward a
// steady state for a while under AC-5's persistence mechanic (persisted =
// active*(1-clearance) + gen), which alone produces several months of
// falling Safety with zero population signal at all — verified by hand
// against the reverted (buggy) code, which still passed a naive 4-month
// check. This version first WARMS UP the state at a constant, modest
// population (500) until Safety's month-over-month movement (the "noise
// floor" — pure persistence decay, not a population signal) is small, then
// asserts each growth-phase delta is at least 10x that floor — a threshold
// calibrated by hand against BOTH branches: the reverted (buggy) code's
// growth-phase deltas measured within 1-2% of the noise floor (ratio ~0.98,
// pure coincidental drift, never above ~1x) at this exact warm-up/growth
// schedule, while the fixed code's measured 36x-83x — comfortably separated
// either side of 10x — then reverted.
func TestFEAT167_SafetyRespondsAcrossSustainedMonths(t *testing.T) {
	const seed = 21
	st := newFeat167TestState(t, seed)

	nextID := uint64(1)
	spawnFeat167Citizens(t, st.citizens, seed, st.cid, nextID, 500)
	nextID += 500

	// Warm-up: constant population, enough months for the active-crime
	// stock's persistence-driven drift to settle near its steady state for
	// this pool size.
	var prev, noiseFloor float64
	month := int64(1)
	for i := 0; i < 200; i++ {
		safety, err := st.safetyTerm(month)
		if err != nil {
			t.Fatalf("safetyTerm warm-up (month %d): %v", month, err)
		}
		month++
		if i > 0 {
			noiseFloor = math.Abs(safety - prev)
		}
		prev = safety
	}
	if noiseFloor <= 0 {
		t.Fatalf("warm-up noise floor = %v, want > 0 (the test's own calibration signal is degenerate)", noiseFloor)
	}

	// Growth phase: population jumps substantially across 4 more months.
	growth := []int{20000, 40000, 80000, 160000}
	last := prev
	var safeties []float64
	for _, g := range growth {
		spawnFeat167Citizens(t, st.citizens, seed, st.cid, nextID, g)
		nextID += uint64(g)
		safety, err := st.safetyTerm(month)
		if err != nil {
			t.Fatalf("safetyTerm growth (month %d): %v", month, err)
		}
		month++

		if safety >= last {
			t.Fatalf("Safety did not strictly fall this growth month (%v -> %v) — the live monthly population push stopped moving Safety after month 1 (F1 regression)", last, safety)
		}
		delta := last - safety
		if delta < noiseFloor*10 {
			t.Fatalf("growth-phase Safety delta %v is not >>noise floor %v (>=10x expected) — the pool is not tracking the live monthly push, only residual persistence drift (F1 regression)", delta, noiseFloor)
		}
		safeties = append(safeties, safety)
		last = safety
	}
	t.Logf("noise floor = %v; post-warm-up growth-phase Safety: %v", noiseFloor, safeties)
}

// TestFEAT167_DemolishRemovesVenue_ReRegisterIdempotent is destructive round
// r1's F2 regression: build an entertainment zone (LeisureFit rises),
// demolish it (LeisureFit MUST fall back and the venue MUST leave
// leisureVenuesRegistered — the ORIGINAL bug left the queue entry reporting
// complete+ZoneEntertainment forever, so registerLeisureVenues' add-only
// logic never noticed the demolition), then rebuild on the same cell
// (LeisureFit rises again, exactly one venue registered — not two, not
// zero), and finally prove a repeat registerLeisureVenues call with no
// build/demolish in between changes nothing (idempotency both ways).
func TestFEAT167_DemolishRemovesVenue_ReRegisterIdempotent(t *testing.T) {
	cid := errs.NewCorrelationID()
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(17), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	cell := protocol.CellRef{X: 4, Y: 4}
	tile := world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	local := world.CellLocal{Row: cell.Y, Col: cell.X}

	buildEntertainment := func(tag string) {
		t.Helper()
		if res := e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-f2-zone-" + tag),
			Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "entertainment"},
		}); !res.Accepted {
			t.Fatalf("Zone (%s) rejected: %+v", tag, res.Error)
		}
		if res := e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-f2-build-" + tag),
			Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "entertainment"},
		}); !res.Accepted {
			t.Fatalf("Build (%s) rejected: %+v", tag, res.Error)
		}
		completed := false
		for i := 0; i < 300; i++ {
			if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
				t.Fatalf("AdvanceTicks (%s): %v", tag, err)
			}
			if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
				completed = true
				break
			}
		}
		if !completed {
			t.Fatalf("entertainment build (%s) never completed after 300 ticks", tag)
		}
	}

	fitBefore, err := comp.state.leisureFitTerm()
	if err != nil {
		t.Fatalf("leisureFitTerm (before): %v", err)
	}

	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-f2-buy"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	buildEntertainment("first")

	fitAfterBuild, err := comp.state.leisureFitTerm()
	if err != nil {
		t.Fatalf("leisureFitTerm (after build): %v", err)
	}
	if fitAfterBuild <= fitBefore {
		t.Fatalf("LeisureFit after building an entertainment zone (%v) is not higher than before (%v)", fitAfterBuild, fitBefore)
	}
	if got := len(comp.state.leisureVenuesRegistered); got != 1 {
		t.Fatalf("registered-venue count after first build = %d, want exactly 1", got)
	}

	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("feat167-f2-demolish"),
		Kind: protocol.KindDemolish, Payload: protocol.DemolishPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Demolish rejected: %+v", res.Error)
	}
	// One more day-tick so buildHook's daily registerLeisureVenues
	// reconciliation (buildHook.ApplyEffect, compose.go) observes the
	// demolition via BuildAPI.Structure no longer standing.
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks (post-demolish): %v", err)
	}

	fitAfterDemolish, err := comp.state.leisureFitTerm()
	if err != nil {
		t.Fatalf("leisureFitTerm (after demolish): %v", err)
	}
	if fitAfterDemolish >= fitAfterBuild {
		t.Fatalf("LeisureFit after demolishing the entertainment zone (%v) is not lower than while it stood (%v) — the venue leak (F2 regression)", fitAfterDemolish, fitAfterBuild)
	}
	if got := len(comp.state.leisureVenuesRegistered); got != 0 {
		t.Fatalf("registered-venue count after demolish = %d, want exactly 0 — demolition did not unregister the venue (F2 regression)", got)
	}

	// Idempotency across ticks with nothing changed: repeat reconciliation
	// calls (buildHook already runs one every day-tick) must not flap.
	comp.state.registerLeisureVenues()
	comp.state.registerLeisureVenues()
	if got := len(comp.state.leisureVenuesRegistered); got != 0 {
		t.Fatalf("repeat registerLeisureVenues calls after demolish, with nothing rebuilt, changed the registered count to %d, want 0", got)
	}

	// Rebuild on the same cell (zoneState was cleared by demolish, so a
	// fresh Zone+Build is required) and prove the bridge re-registers.
	buildEntertainment("second")

	fitAfterRebuild, err := comp.state.leisureFitTerm()
	if err != nil {
		t.Fatalf("leisureFitTerm (after rebuild): %v", err)
	}
	if fitAfterRebuild <= fitAfterDemolish {
		t.Fatalf("LeisureFit after rebuilding (%v) is not higher than after demolish (%v)", fitAfterRebuild, fitAfterDemolish)
	}
	if got := len(comp.state.leisureVenuesRegistered); got != 1 {
		t.Fatalf("registered-venue count after rebuild = %d, want exactly 1 (not accumulating stale entries)", got)
	}

	// Idempotency once more, now with a live venue: a repeat call must not
	// double-register or otherwise change the count.
	before := len(comp.state.leisureVenuesRegistered)
	comp.state.registerLeisureVenues()
	if got := len(comp.state.leisureVenuesRegistered); got != before {
		t.Fatalf("registerLeisureVenues changed the registered count on a repeat call with nothing built/demolished in between: %d -> %d", before, got)
	}
}
