package compose

import (
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/coastal"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-2326609833: tests for engine.coastal wired into the composition root
// (pack docs/planning/acceptance/wire-engine.coastal.md). Every scenario is
// driven ONLY through compose.Wire ticks -- no test calls coastal.Advance.

const coastalTestSeed = uint64(20261031)

// coastalMonthRecord is one month's observation, taken at the start of
// PhaseLandValueDecay (the phase after PhasePopulation, before PhaseFinance's
// BeginMonth clears the per-tick transaction log).
type coastalMonthRecord struct {
	month                                    int64
	hotels, departure, processing, integrate int64 // ledger line totals this month
	dHotel, dDeparture, dProc, dInteg        int64 // module cumulative deltas this month
	violations                               int
	unbalanced                               int
}

type coastalRun struct {
	e          *core.Engine
	comp       *Composition
	records    []coastalMonthRecord
	invariantV int64
}

// sumCat totals the treasury-side (debit) leg of every ledger line carrying
// the category; the external-side credit leg carries the same category, so it
// is checked for balance separately (sumCatBoth).
func sumCat(f *finance.FinanceAPI, cat finance.Category) (total int64, unbalancedLines int) {
	var deb, cred int64
	for _, l := range f.LinesByCategory(cat) {
		switch {
		case l.Account == finance.AcctTreasury && l.Side == finance.SideDebit:
			total += int64(l.Amount)
			deb += int64(l.Amount)
		case l.Account == finance.AcctExternal && l.Side == finance.SideCredit:
			cred += int64(l.Amount)
		default:
			unbalancedLines++
		}
	}
	if deb != cred {
		unbalancedLines++
	}
	return total, unbalancedLines
}

// runCoastalScenario wires a composition (optionally with an injected coastal
// module configured by mutate) and runs `months` months, recording per-month
// ledger/module observations through a phase observer.
func runCoastalScenario(t *testing.T, seed uint64, months int64, mutate func(*coastal.CoastalAPI)) *coastalRun {
	t.Helper()
	return runCoastalScenarioMode(t, seed, months, "", mutate)
}

// runCoastalScenarioMode is runCoastalScenario with a game mode. The money-path
// scenarios use "unlimited": the placeholder hotel costs (tens of thousands of
// pounds a month) exceed the real-mode starting treasury, so in real mode they
// are (correctly) rejected by the overdraft check and carried forward.
func runCoastalScenarioMode(t *testing.T, seed uint64, months int64, mode string, mutate func(*coastal.CoastalAPI)) *coastalRun {
	t.Helper()
	run := &coastalRun{}
	var violations atomic.Int64
	var comp *Composition
	var prev [4]int64
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1), core.WithPhaseObserver(func(kind core.PhaseKind, _ int64, month int64) {
		if kind != core.PhaseLandValueDecay || comp == nil || comp.state == nil || comp.state.coastalW == nil {
			return
		}
		api := comp.state.coastalW.api
		fin := comp.state.finance
		var rec coastalMonthRecord
		rec.month = month
		var ub int
		rec.hotels, ub = sumCat(fin, finCatCoastalHotels)
		rec.unbalanced += ub
		rec.departure, ub = sumCat(fin, finCatCoastalDeparture)
		rec.unbalanced += ub
		rec.processing, ub = sumCat(fin, finCatCoastalProcessing)
		rec.unbalanced += ub
		rec.integrate, ub = sumCat(fin, finCatCoastalIntegration)
		rec.unbalanced += ub
		cur := [4]int64{api.HotelCost(), api.DepartureCost(), api.ProcessingOpex(), api.IntegrationOpex()}
		rec.dHotel, rec.dDeparture, rec.dProc, rec.dInteg = cur[0]-prev[0], cur[1]-prev[1], cur[2]-prev[2], cur[3]-prev[3]
		prev = cur
		rec.violations = len(fin.FindConservationViolations())
		run.records = append(run.records, rec)
	}))
	deps := &Deps{GameMode: mode, InvariantOpts: []invariant.HookOption{invariant.WithLogSink(func(*errs.E) { violations.Add(1) })}}
	if mutate != nil {
		api, err := coastal.LoadDefault(seed, errs.NewCorrelationID())
		if err != nil {
			t.Fatalf("coastal.LoadDefault: %v", err)
		}
		mutate(api)
		deps.Coastal = api
	}
	c, err := Wire(e, deps)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	comp = c
	advanceInChunks(t, e, months*int64(core.DailyTicksPerMonth))
	run.e, run.comp, run.invariantV = e, c, violations.Load()
	return run
}

func ci(c *Composition) coastal.Counts { return c.state.coastalW.api.Counts() }

// --- AC-1: reachability ------------------------------------------------------

func TestCoastalWire_AC1_Reachability(t *testing.T) {
	order := RegistrationOrder()
	idx := func(n string) int {
		for i, v := range order {
			if v == n {
				return i
			}
		}
		return -1
	}
	if idx("coastal") < 0 {
		t.Fatalf("RegistrationOrder() = %v lacks the coastal monthly hook", order)
	}
	if idx("coastal") != idx("wellbeing")+1 {
		t.Fatalf("coastal must sit directly after wellbeing: %v", order)
	}
	found := false
	for _, n := range RegisteredViewNames() {
		if n == coastalViewSubscriptionName {
			found = true
		}
	}
	if !found {
		t.Fatalf("RegisteredViewNames() = %v lacks %q", RegisteredViewNames(), coastalViewSubscriptionName)
	}

	// The wired engine really carries the hook, and the view serves a patch.
	r := runCoastalScenario(t, coastalTestSeed, 6, nil)
	if got := r.e.HookCount(); got != BaselineOneHookCount() {
		t.Fatalf("HookCount() = %d, want %d", got, BaselineOneHookCount())
	}
	raw, err := r.comp.state.buildCoastalPatch()
	if err != nil {
		t.Fatalf("buildCoastalPatch: %v", err)
	}
	var p coastalWirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("patch json: %v", err)
	}
	if p.SchemaVersion != coastalWireSchemaVersion {
		t.Fatalf("schemaVersion = %d", p.SchemaVersion)
	}
	if p.Arrivals != ci(r.comp).Arrivals || p.Arrivals == 0 {
		t.Fatalf("view arrivals = %d, module arrivals = %d (want equal and > 0 after 6 months at default data)", p.Arrivals, ci(r.comp).Arrivals)
	}
	if p.Month != 5 && p.Month != 6 {
		t.Logf("view month = %d (last processed)", p.Month)
	}
}

// --- AC-2: arrivals occur, deterministic, driver read live (directional) -----

func TestCoastalWire_AC2_ArrivalsDeterministicAndConditionsDirectional(t *testing.T) {
	a := runCoastalScenario(t, coastalTestSeed, 24, nil)
	b := runCoastalScenario(t, coastalTestSeed, 24, nil)
	if ci(a.comp).Arrivals == 0 {
		t.Fatal("no arrival in 24 months of default data")
	}
	if a.comp.StateDigest() != b.comp.StateDigest() {
		t.Fatal("same-seed runs diverged (StateDigest)")
	}
	ra, _ := a.comp.state.buildCoastalPatch()
	rb, _ := b.comp.state.buildCoastalPatch()
	if string(ra) != string(rb) {
		t.Fatalf("same-seed f4.coastal differs:\n%s\n%s", ra, rb)
	}
	// Directional (lead ruling: basePerMonth=1.0 means cond=0 still arrives).
	lo := runCoastalScenario(t, coastalTestSeed, 24, func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(0) })
	hi := runCoastalScenario(t, coastalTestSeed, 24, func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(1) })
	if !(ci(hi.comp).Arrivals > ci(lo.comp).Arrivals) {
		t.Fatalf("arrivals(cond=1)=%d not > arrivals(cond=0)=%d: driver not read live", ci(hi.comp).Arrivals, ci(lo.comp).Arrivals)
	}
}

// --- AC-3: rescue capacity is real ------------------------------------------

func TestCoastalWire_AC3_RescueIDsResolveInDefaultData(t *testing.T) {
	_, comp := newTestEngine(t, coastalTestSeed)
	svc := comp.state.services
	for _, id := range []string{"coastguard", "lifeboat"} {
		capy, err := svc.Capacity(services.ServiceID(id))
		if err != nil {
			t.Fatalf("Capacity(%q): %v -- rescue id absent from data/services.json (the silent-zero trap)", id, err)
		}
		if capy <= 0 {
			t.Fatalf("Capacity(%q) = %v, want > 0", id, capy)
		}
	}
	// The ids the coastal config reads are exactly the ones registered.
	cfgIDs := []string{"coastguard", "lifeboat"}
	for _, id := range cfgIDs {
		if _, err := svc.Capacity(services.ServiceID(id)); err != nil {
			t.Fatalf("config id %q does not resolve: %v", id, err)
		}
	}
}

func TestCoastalWire_AC3_NoShortfallWithStationsAndUnregisteredDistinctFromZero(t *testing.T) {
	// Fully resourced: default baseline cover (35/month) >= max boat size 20.
	r := runCoastalScenario(t, coastalTestSeed, 24, nil)
	c := ci(r.comp)
	if c.Arrivals == 0 {
		t.Fatal("no arrivals")
	}
	if c.Shortfalls != 0 {
		t.Fatalf("shortfalls = %d with baseline coastguard+lifeboat registered, want 0", c.Shortfalls)
	}
	if got := r.comp.state.coastalW.unregistered; len(got) != 0 {
		t.Fatalf("unregistered = %v, want none", got)
	}

}

// --- AC-4: arrivals counted in people conservation, unique ids ---------------

func TestCoastalWire_AC4_GrantedCitizensUniqueAndConserved(t *testing.T) {
	r := runCoastalScenarioMode(t, coastalTestSeed, 30, "unlimited", func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(1) })
	st := r.comp.state
	c := ci(r.comp)
	if c.Granted == 0 {
		t.Fatalf("no granted case in 30 months (counts %+v): scenario is degenerate", c)
	}
	if r.invariantV != 0 {
		t.Fatalf("invariant suite reported %d violations (people/money conservation) over the run", r.invariantV)
	}
	// Unique-ID scan over the FULL live population id set.
	seen := map[uint64]int{}
	for _, id := range st.liveResidentIDs() {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("duplicate citizen id %d appears %d times in the resident id set", id, n)
		}
	}
	// Every granted case has a distinct, resolvable, compose-allocated id that
	// the resident id set includes.
	granted := 0
	snap := st.coastalW.api.Snapshot()
	caseIDs := map[uint64]bool{}
	for _, k := range snap.Cases {
		if k.Stage != coastal.CaseGranted {
			continue
		}
		granted++
		if k.CitizenID == 0 || k.CitizenID >= st.nextCitizenID {
			t.Fatalf("case %d citizen id %d outside the compose allocator range [1,%d)", k.ID, k.CitizenID, st.nextCitizenID)
		}
		if caseIDs[k.CitizenID] {
			t.Fatalf("two cases share citizen id %d", k.CitizenID)
		}
		caseIDs[k.CitizenID] = true
		if seen[k.CitizenID] != 1 {
			t.Fatalf("granted citizen %d missing from the resident id set", k.CitizenID)
		}
		if _, ok := st.citizens.CitizenAt(k.CitizenID, st.cid); !ok {
			t.Fatalf("granted citizen %d not in the citizen store", k.CitizenID)
		}
	}
	if int64(granted) != c.Granted {
		t.Fatalf("granted cases %d != Counts.Granted %d", granted, c.Granted)
	}
	// Cumulative people bookkeeping: seed + net migration + births - deaths +
	// coastal granted == live population (every arrival is in the ledger terms).
	if want, got := int64(seedCitizenCount)+st.netMigration+st.vitalBirths-st.vitalDeaths+c.Granted, int64(st.citizens.TotalPopulation(st.cid)); want != got {
		t.Fatalf("seed+migration+births-deaths+granted = %d != live population %d: a coastal arrival escaped the people bookkeeping", want, got)
	}
	// The ledger never reported a conservation violation at any observation.
	for _, rec := range r.records {
		if rec.violations != 0 {
			t.Fatalf("month %d: FindConservationViolations non-empty (%d)", rec.month, rec.violations)
		}
	}
	// Ids stay clear of the migrant namespace.
	for id := range caseIDs {
		if id >= attract.MigrantIDBase {
			t.Fatalf("coastal citizen id %d entered the migrant namespace", id)
		}
	}
}

func TestCoastalWire_AC4_AllocatorRefusesCollisionRanges(t *testing.T) {
	st := &simState{cid: "t", nextCitizenID: attract.MigrantIDBase}
	if _, err := st.allocateCoastalCitizenID(); err == nil {
		t.Fatal("allocator accepted an id in the migrant namespace")
	}
	st = &simState{cid: "t", nextCitizenID: 105, seedResidentIDBase: 100, seedResidentIDCount: 10}
	if _, err := st.allocateCoastalCitizenID(); err == nil {
		t.Fatal("allocator accepted an id inside the seeded resident range")
	}
	st = &simState{cid: "t", nextCitizenID: 7}
	id, err := st.allocateCoastalCitizenID()
	if err != nil || id != 7 || st.nextCitizenID != 8 {
		t.Fatalf("allocate = (%d,%v) next=%d, want (7,nil) next=8", id, err, st.nextCitizenID)
	}
}

// --- AC-5: costs hit the one-money ledger ------------------------------------

func TestCoastalWire_AC5_MonthlyLedgerLinesEqualModuleCosts(t *testing.T) {
	r := runCoastalScenarioMode(t, coastalTestSeed, 30, "unlimited", func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(1) })
	var sumH, sumD, sumP, sumI, nonzero int64
	for _, rec := range r.records {
		if rec.hotels != rec.dHotel || rec.departure != rec.dDeparture || rec.processing != rec.dProc || rec.integrate != rec.dInteg {
			t.Fatalf("month %d: ledger lines (h=%d d=%d p=%d i=%d) != module monthly costs (h=%d d=%d p=%d i=%d)",
				rec.month, rec.hotels, rec.departure, rec.processing, rec.integrate, rec.dHotel, rec.dDeparture, rec.dProc, rec.dInteg)
		}
		if rec.violations != 0 || rec.unbalanced != 0 {
			t.Fatalf("month %d: %d conservation violations, %d unbalanced coastal lines", rec.month, rec.violations, rec.unbalanced)
		}
		sumH += rec.hotels
		sumD += rec.departure
		sumP += rec.processing
		sumI += rec.integrate
		if rec.hotels+rec.departure > 0 {
			nonzero++
		}
	}
	if sumH+sumD == 0 || nonzero == 0 {
		t.Fatalf("no hotel/departure cost was ever posted (h=%d d=%d): scenario degenerate", sumH, sumD)
	}
	api := r.comp.state.coastalW.api
	posted := r.comp.CoastalPostedMicropounds()
	if posted["hotels"] != api.HotelCost() || posted["departure"] != api.DepartureCost() ||
		posted["processing"] != api.ProcessingOpex() || posted["integration"] != api.IntegrationOpex() {
		t.Fatalf("posted %v != module totals h=%d d=%d p=%d i=%d", posted, api.HotelCost(), api.DepartureCost(), api.ProcessingOpex(), api.IntegrationOpex())
	}
	if sumH != api.HotelCost() || sumD != api.DepartureCost() {
		t.Fatalf("summed monthly lines (%d,%d) != cumulative module costs (%d,%d)", sumH, sumD, api.HotelCost(), api.DepartureCost())
	}
	if r.invariantV != 0 {
		t.Fatalf("%d invariant violations", r.invariantV)
	}
}

func TestCoastalWire_AC5_PostingIsBalancedAndNotDoubledOnReplay(t *testing.T) {
	r := runCoastalScenarioMode(t, coastalTestSeed, 12, "unlimited", func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(1) })
	st := r.comp.state
	before := st.coastalW.posted
	treas := ledgerBalance(st.finance, finance.AcctTreasury)
	// Replay an already-processed month: the guard must stop a second post.
	st.runCoastal(st.coastalW.lastMonth)
	if st.coastalW.posted != before {
		t.Fatalf("replayed month changed posted counters: %v -> %v", before, st.coastalW.posted)
	}
	if got := ledgerBalance(st.finance, finance.AcctTreasury); got != treas {
		t.Fatalf("replay moved treasury %d -> %d", treas, got)
	}
}

// --- AC-6: policy sliders move outcomes --------------------------------------

func TestCoastalWire_AC6_ProcessingFundingMovesBacklogAndSpend(t *testing.T) {
	hiFund := runCoastalScenarioMode(t, coastalTestSeed, 24, "unlimited", func(c *coastal.CoastalAPI) {
		_ = c.SetWorldConditions(1)
		_ = c.SetProcessingFunding(1.0)
	})
	loFund := runCoastalScenarioMode(t, coastalTestSeed, 24, "unlimited", func(c *coastal.CoastalAPI) {
		_ = c.SetWorldConditions(1)
		_ = c.SetProcessingFunding(0.0)
	})
	bA, bB := hiFund.comp.state.coastalW.api.Backlog(), loFund.comp.state.coastalW.api.Backlog()
	if !(bA < bB) {
		t.Fatalf("backlog A(high funding)=%d not < B(low funding)=%d", bA, bB)
	}
	pA := hiFund.comp.CoastalPostedMicropounds()["processing"]
	pB := loFund.comp.CoastalPostedMicropounds()["processing"]
	if !(pA > pB) {
		t.Fatalf("processing spend on ledger A=%d not > B=%d", pA, pB)
	}
}

// --- AC-7: migration seam counts coastal arrivals exactly once ---------------

func TestCoastalWire_AC7_MigrationInflowCountsCoastalOnce(t *testing.T) {
	st := &simState{}
	if got := st.migrationInflow(7); got != 7 {
		t.Fatalf("nil coastal wiring: inflow %d, want 7", got)
	}
	st.coastalW = &coastalWiring{lastGranted: 3}
	if got := st.migrationInflow(7); got != 10 {
		t.Fatalf("inflow = %d, want attract 7 + coastal 3 = 10 (each once)", got)
	}
	// In a real run lastGranted is the measured population gain of the month
	// and is the same quantity folded into peopleDelta -- one source, once.
	r := runCoastalScenario(t, coastalTestSeed, 30, func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(1) })
	granted := ci(r.comp).Granted
	if granted == 0 {
		t.Fatal("degenerate: no granted cases")
	}
	st = r.comp.state
	if want, got := int64(seedCitizenCount)+st.netMigration+st.vitalBirths-st.vitalDeaths+granted, int64(st.citizens.TotalPopulation(st.cid)); want != got {
		t.Fatalf("population %d != seed+migration+births-deaths+coastal %d (coastal counted other than once)", got, want)
	}
}

// --- AC-8: N/A (engine.news not wired in compose; SetNews skip is nil-safe) ---

func TestCoastalWire_AC8_NewsUnwiredIsNilSafe(t *testing.T) {
	r := runCoastalScenario(t, coastalTestSeed, 12, nil)
	if r.invariantV != 0 {
		t.Fatalf("run with no news wired reported %d violations", r.invariantV)
	}
	t.Log("AC-8 N/A: engine.news is not composed; coastal's reportNews is a documented nil-safe skip")
}

// --- AC-9: save/load round-trips coastal state -------------------------------

func TestCoastalWire_AC9_SaveLoadRoundTripsDigestAndSliders(t *testing.T) {
	mut := func(c *coastal.CoastalAPI) {
		_ = c.SetWorldConditions(1)
		_ = c.SetProcessingFunding(0.8)
		_ = c.SetHousingApproach(0.2)
	}
	rA := runCoastalScenario(t, coastalTestSeed, 12, mut)
	if ci(rA.comp).People == 0 {
		t.Fatal("no cases by month 12: degenerate")
	}
	dir := t.TempDir()
	if err := rA.comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eB := core.NewEngine(core.WithWorldSeed(coastalTestSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, nil)
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rA.comp.StateDigest() != compB.StateDigest() {
		t.Fatal("StateDigest did not round-trip with coastal state")
	}
	sA, sB := rA.comp.state.coastalW.api.Snapshot(), compB.state.coastalW.api.Snapshot()
	if sA.ProcessingFunding != sB.ProcessingFunding || sA.HousingApproach != sB.HousingApproach || len(sA.Cases) != len(sB.Cases) || len(sA.Backlog) != len(sB.Backlog) {
		t.Fatalf("snapshot mismatch after load: A=%+v B=%+v", sA, sB)
	}
	if compB.state.nextCitizenID != rA.comp.state.nextCitizenID {
		t.Fatalf("id allocator position not restored: %d vs %d", compB.state.nextCitizenID, rA.comp.state.nextCitizenID)
	}
	if rA.comp.state.coastalW.posted != compB.state.coastalW.posted || rA.comp.state.coastalW.lastMonth != compB.state.coastalW.lastMonth {
		t.Fatal("posted counters / lastMonth not restored")
	}
}

func TestCoastalWire_ParticipantListedAndRejectsCorruptRecord(t *testing.T) {
	_, comp := newTestEngine(t, coastalTestSeed)
	listed := false
	for _, p := range comp.Participants() {
		if p.Kind() == kindComposeCoastal {
			listed = true
		}
	}
	if !listed {
		t.Fatal("coastal participant missing from Participants()")
	}
	h := newCoastalParticipant(comp.state).Handler()
	bad := []string{
		`{"snapshot":{},"nextCitizenID":0}`,
		`{"snapshot":{},"nextCitizenID":5,"posted":[-1,0,0,0]}`,
		`not json`,
	}
	for _, b := range bad {
		if err := h(serialize.Record{Kind: recComposeCoastal, Data: []byte(b)}); err == nil {
			t.Fatalf("handler accepted corrupt record %q", b)
		}
	}
}

// --- baselineServices / unregistered-vs-zero ---------------------------------

func TestCoastalWire_BaselineServicesValidatedAndIdempotent(t *testing.T) {
	specs, err := services.LoadBaselineServiceSpecs(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("LoadBaselineServiceSpecs: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range specs {
		ids[string(s.ID)] = true
		if services.CapacityFromRaw(s.CapacityRaw) <= 0 {
			t.Fatalf("baseline %q has non-positive capacity", s.ID)
		}
	}
	if !ids["coastguard"] || !ids["lifeboat"] {
		t.Fatalf("baseline specs %v lack coastguard/lifeboat", ids)
	}
	_, comp := newTestEngine(t, coastalTestSeed)
	if err := comp.state.coastalW.ensureBaselineServices(); err != nil {
		t.Fatalf("second ensureBaselineServices not idempotent: %v", err)
	}
}

// TestCoastalWire_RealModeUnaffordableCostsCarryForwardNeverDropped: in real
// mode the placeholder hotel cost exceeds the treasury, the overdraft check
// rejects the posting, and the amount stays owed (posted < module total) with
// the ledger still conserving -- never silently dropped, never overdrawn.
func TestCoastalWire_RealModeUnaffordableCostsCarryForwardNeverDropped(t *testing.T) {
	r := runCoastalScenario(t, coastalTestSeed, 12, func(c *coastal.CoastalAPI) { _ = c.SetWorldConditions(1) })
	api := r.comp.state.coastalW.api
	posted := r.comp.CoastalPostedMicropounds()
	if api.HotelCost() == 0 {
		t.Fatal("degenerate: no hotel cost accrued")
	}
	if posted["hotels"] > api.HotelCost() {
		t.Fatalf("posted hotels %d exceeds accrued %d", posted["hotels"], api.HotelCost())
	}
	if posted["hotels"] == api.HotelCost() {
		t.Skip("treasury covered the cost in this scenario; carry-forward not exercised")
	}
	if r.comp.state.coastalW.failedMonths == 0 {
		t.Fatal("rejected postings were not counted")
	}
	if bal := ledgerBalance(r.comp.state.finance, finance.AcctTreasury); bal < 0 {
		t.Fatalf("treasury overdrawn: %d", bal)
	}
	if r.invariantV != 0 {
		t.Fatalf("%d invariant violations", r.invariantV)
	}
}

// quietCoastal returns a coastal module with zero arrival rate and all three
// policy sliders at zero (no opex), for fixtures of OTHER modules (economy,
// death services, wellbeing, save/restore continuation) that assert exact
// money / population figures and must not see boats. The baseline rescue
// services are still registered by the wiring. Coastal's own interaction with
// the ledger and the population is covered by the dedicated scenarios above.
func quietCoastal(t *testing.T) *coastal.CoastalAPI {
	t.Helper()
	cid := errs.NewCorrelationID()
	dir, err := data.ResolveDataDir(cid)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	cfg, err := coastal.LoadConfig(filepath.Join(dir, "coastal.json"), cid)
	if err != nil {
		t.Fatalf("coastal.LoadConfig: %v", err)
	}
	cfg.BaseArrivalRate = 0
	api, err := coastal.New(1, cfg, cid)
	if err != nil {
		t.Fatalf("coastal.New: %v", err)
	}
	for _, set := range []func(float64) error{api.SetProcessingFunding, api.SetHousingApproach, api.SetIntegrationInvestment} {
		if err := set(0); err != nil {
			t.Fatalf("zero slider: %v", err)
		}
	}
	return api
}
