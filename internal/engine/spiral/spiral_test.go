package spiral

import (
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string { return errs.NewCorrelationID() }

func newTestAPI(t *testing.T) *DecayAPI {
	t.Helper()
	d, err := New(testCorrelationID())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", want)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != want {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, want, err)
	}
}

func cellRef(row, col int) CellRef {
	return CellRef{Tile: world.TileCoord{X: 15, Y: 15}, Local: world.CellLocal{Row: row, Col: col}}
}

// insertDecayed white-box inserts a decayed cell at the given severity so a
// test can drive the spread/recovery/decay paths without running the full
// monthly loop.
func insertDecayed(d *DecayAPI, c CellRef, severity int, month int64) {
	d.decay[c.key()] = &decayState{cell: c, abandonedAt: month, age: 0, severity: severity}
}

// --- AC-1: subscribable metrics surface ----------------------------------

func TestSubscribeMetricsDeliversOnAdvance(t *testing.T) {
	d := newTestAPI(t)
	ch := make(chan SpiralMetric, 8)
	unsub := d.Subscribe(ch)
	defer unsub()

	_, err := d.AdvanceMonth(MonthInput{Month: 0, Population: 60_000})
	if err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	select {
	case m := <-ch:
		if m.Month != 0 || m.Population != 60_000 {
			t.Errorf("metric = %+v, want month 0 population 60000", m)
		}
	default:
		t.Fatal("no metric delivered to subscriber")
	}

	// Unsubscribing stops delivery.
	unsub()
	if _, err := d.AdvanceMonth(MonthInput{Month: 1, Population: 59_000}); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	select {
	case <-ch:
		t.Fatal("metric delivered after unsubscribe")
	default:
	}
}

// --- AC-2: stages are derived from real values (reversible, no counter) ---

func TestStageReversibleOnRecovery(t *testing.T) {
	d := newTestAPI(t)

	// A healthy city is stable.
	s := d.EvaluateStage(StageInputs{Attractiveness: 80, PrevAttractiveness: 80, NetMigration: 100, TaxDelta: 10_000, InsolvencyRisk: false, AbandonedCells: 0, ShockRecorded: false})
	if s != StageStable {
		t.Fatalf("healthy city stage = %v, want StageStable", s)
	}

	// A post-shock declining city reaches the deep stages.
	deep := d.EvaluateStage(StageInputs{Attractiveness: 20, PrevAttractiveness: 30, NetMigration: -1500, TaxDelta: -5000, InsolvencyRisk: true, AbandonedCells: 3, ShockRecorded: true})
	if deep != StageBlightSpread {
		t.Fatalf("declining city stage = %v, want StageBlightSpread", deep)
	}

	// Reversing the driving values (attractiveness/migration recover) retreats
	// the stage — the progression is NOT a ratcheting counter (AC-2's
	// lazy-implementation rejection).
	recovered := d.EvaluateStage(StageInputs{Attractiveness: 80, PrevAttractiveness: 75, NetMigration: 200, TaxDelta: 8000, InsolvencyRisk: false, AbandonedCells: 0, ShockRecorded: true})
	if recovered != StageShock {
		t.Fatalf("recovered city stage = %v, want StageShock (reversible, not latched)", recovered)
	}
}

// --- AC-3: three independent decay effects --------------------------------

func TestLandValueDragIncreasesWithSeverity(t *testing.T) {
	d := newTestAPI(t)
	low := d.LandValueDrag(1)
	high := d.LandValueDrag(6)
	if high <= low {
		t.Fatalf("LandValueDrag(6)=%d not > LandValueDrag(1)=%d", high, low)
	}
	// Independent of the other two effects: this value is a pure function of
	// severity with its own coefficient, not a merged scalar.
	if d.LandValueDrag(0) != 0 {
		t.Errorf("LandValueDrag(0) = %d, want 0", d.LandValueDrag(0))
	}
}

func TestHazardPressureIncreasesWithSeverity(t *testing.T) {
	d := newTestAPI(t)
	low := d.HazardPressure(1)
	high := d.HazardPressure(6)
	if high <= low {
		t.Fatalf("HazardPressure(6)=%d not > HazardPressure(1)=%d", high, low)
	}
	if d.HazardPressure(0) != 0 {
		t.Errorf("HazardPressure(0) = %d, want 0", d.HazardPressure(0))
	}
}

func TestDemolitionCostIncreasesWithSeverity(t *testing.T) {
	d := newTestAPI(t)
	young := d.DemolitionCost(1, 1)
	severe := d.DemolitionCost(6, 1)
	old := d.DemolitionCost(1, 40)
	if severe <= young {
		t.Fatalf("DemolitionCost(6,1)=%d not > DemolitionCost(1,1)=%d", severe, young)
	}
	if old <= young {
		t.Fatalf("DemolitionCost(1,40)=%d not > DemolitionCost(1,1)=%d (age must raise cost)", old, young)
	}
	if d.DemolitionCost(0, 0) <= 0 {
		t.Errorf("DemolitionCost(0,0) = %d, want a strictly positive base", d.DemolitionCost(0, 0))
	}
}

// --- AC-4: deterministic blight frontier (forced tie) ---------------------

func TestBlightFrontierTieDeterministic(t *testing.T) {
	// A decayed source cell with four equally-eligible orthogonal neighbours
	// (a forced tie). The chosen first neighbour must be identical every run
	// (the cellLess minimum), never a map-iteration or hash-ordered pick.
	source := cellRef(5, 5)
	wantFirst := cellRef(4, 5) // north: smallest (row,col) in cellLess order

	for run := 0; run < 50; run++ {
		d := newTestAPI(t)
		insertDecayed(d, source, 5, 0)
		got, ok := d.spreadOneStep(0)
		if !ok {
			t.Fatalf("run %d: spreadOneStep reported no frontier", run)
		}
		if got != wantFirst {
			t.Fatalf("run %d: first blighted = %v, want %v (non-deterministic frontier)", run, got, wantFirst)
		}
	}
}

// --- AC-5: three recovery commands reduce decay at positive cost ----------

func TestRecoveryDemolitionReducesDecay(t *testing.T) {
	d := newTestAPI(t)
	c := cellRef(3, 3)
	insertDecayed(d, c, 5, 0)

	res := d.RecoverDemolition(DemolitionCommand{CorrelationID: testCorrelationID(), Cell: c})
	if !res.Result.Accepted {
		t.Fatalf("demolition rejected: %+v", res.Result.Error)
	}
	if res.Cost <= 0 {
		t.Errorf("demolition cost = %d, want > 0", res.Cost)
	}
	if _, still := d.DecayState(c); still {
		t.Errorf("demolition left decay state on %v", c)
	}
}

func TestRecoveryInvestmentReducesDecay(t *testing.T) {
	d := newTestAPI(t)
	c := cellRef(4, 4)
	insertDecayed(d, c, 7, 0)

	res := d.RecoverTargetedInvestment(TargetedInvestmentCommand{CorrelationID: testCorrelationID(), Cell: c})
	if !res.Result.Accepted {
		t.Fatalf("investment rejected: %+v", res.Result.Error)
	}
	if res.Cost <= 0 {
		t.Errorf("investment cost = %d, want > 0", res.Cost)
	}
	st, ok := d.DecayState(c)
	if !ok {
		t.Fatalf("investment removed the cell entirely (should only reduce severity)")
	}
	if st.Severity >= 7 {
		t.Errorf("investment left severity %d, want strictly less than 7", st.Severity)
	}
}

func TestTaxReliefReducesDecay(t *testing.T) {
	d := newTestAPI(t)
	a, b := cellRef(6, 6), cellRef(7, 7)
	insertDecayed(d, a, 5, 0)
	insertDecayed(d, b, 5, 0)

	res := d.RecoverTaxRelief(TaxReliefCommand{CorrelationID: testCorrelationID(), District: []CellRef{a, b}})
	if !res.Result.Accepted {
		t.Fatalf("tax relief rejected: %+v", res.Result.Error)
	}
	if res.Cost <= 0 {
		t.Errorf("tax relief cost = %d, want > 0", res.Cost)
	}
	stA, okA := d.DecayState(a)
	if !okA {
		t.Fatalf("tax relief removed cell A entirely")
	}
	if stA.Severity >= 5 {
		t.Errorf("tax relief left severity %d on A, want strictly less than 5", stA.Severity)
	}
}

// --- AC-6: insolvency death condition consumes engine.finance -------------

func TestInsolvencyDeathCondition(t *testing.T) {
	d := newTestAPI(t)

	// A solvent finance API: no insolvency.
	solvent := finance.NewFinanceAPI(testCorrelationID())
	solvent.RecordMonthResult(true, false)
	if v := d.EvaluateInsolvency(solvent); v != DeathNone {
		t.Fatalf("solvent city insolvency verdict = %v, want DeathNone", v)
	}

	// Drive the real finance API to insolvency: three consecutive
	// failed months with no available credit (finance AC-7's own rule).
	insolvent := finance.NewFinanceAPI(testCorrelationID())
	for i := 0; i < 3; i++ {
		insolvent.RecordMonthResult(false, false)
	}
	if !insolvent.IsInsolvent() {
		t.Fatal("finance API did not reach IsInsolvent after 3 failed months")
	}
	if v := d.EvaluateInsolvency(insolvent); v != DeathInsolvency {
		t.Fatalf("insolvent city verdict = %v, want DeathInsolvency", v)
	}

	// And the full death path fires it through AdvanceMonth.
	if err := d.SetFinance(insolvent); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	res, err := d.AdvanceMonth(MonthInput{Month: 0, Population: 10_000})
	if err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	if res.Death != DeathInsolvency {
		t.Fatalf("AdvanceMonth death = %v, want DeathInsolvency", res.Death)
	}
}

// --- AC-7: ghost-city dual threshold, both clauses independently ----------

func TestGhostCityHistoricPeakConditions(t *testing.T) {
	d := newTestAPI(t)

	t.Run("peak never exceeded 50k", func(t *testing.T) {
		// Current population near zero, but the historic peak never exceeded
		// 50,000 — the condition is structurally undefined, never a trigger.
		if d.GhostCityConditionMet(10, 40_000) {
			t.Fatal("trigger fired with a historic peak below 50,000")
		}
	})
	t.Run("peak exceeded 50k but population at 10pct plus one", func(t *testing.T) {
		// Peak 60,000 -> threshold 6,000. Current 6,001 is still above it.
		if d.GhostCityConditionMet(6_001, 60_000) {
			t.Fatal("trigger fired at exactly 10%+1 of peak")
		}
	})
	t.Run("peak exceeded 50k and population below 10pct", func(t *testing.T) {
		// A peak OTHER than 50,000 (the lazy-implementation trap: a hardcoded
		// "population < 5,000" check would mis-fire here for a 60,000 peak).
		if !d.GhostCityConditionMet(5_999, 60_000) {
			t.Fatal("trigger did NOT fire below 10% of a 60,000 peak")
		}
	})
}

// --- AC-8: epilogue generated from the actual history log -----------------

func TestEpilogueDiffersAcrossScenarios(t *testing.T) {
	d := newTestAPI(t)

	events1 := []Event{
		{Month: 5, Kind: EventShock},
		{Month: 9, Kind: EventStageTransition, Stage: StageEmigrationOnset},
	}
	history1 := []HistoryEntry{
		{Month: 0, Population: 60_000},
		{Month: 9, Population: 40_000},
		{Month: 20, Population: 4_000},
	}
	ep1 := d.GenerateEpilogue(history1, events1)

	events2 := []Event{
		{Month: 12, Kind: EventShock},
		{Month: 18, Kind: EventStageTransition, Stage: StageEmigrationOnset},
	}
	history2 := []HistoryEntry{
		{Month: 0, Population: 90_000},
		{Month: 12, Population: 70_000},
		{Month: 33, Population: 3_000},
	}
	ep2 := d.GenerateEpilogue(history2, events2)

	r1, r2 := ep1.Render(), ep2.Render()
	if r1 == r2 {
		t.Fatal("two different histories produced identical epilogues")
	}
	// The difference must be in the referenced events/dates, not cosmetic.
	if !strings.Contains(r1, "month 5") || !strings.Contains(r2, "month 12") {
		t.Errorf("epilogues do not reference their own shock dates:\n%s\n---\n%s", r1, r2)
	}
	if !strings.Contains(r1, "60000") || !strings.Contains(r2, "90000") {
		t.Errorf("epilogues do not reference their own historic peaks:\n%s\n---\n%s", r1, r2)
	}
}

// --- AC-9: reproducible scripted shock scenario ---------------------------

func runCanonical(t *testing.T, workers int) ScenarioOutcome {
	t.Helper()
	d := newTestAPI(t)
	if err := d.SetProjections(projections.NewProjectionsAPI(projections.WithCorrelationID(testCorrelationID()))); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}
	out, err := d.RunScenarioWorkers(CanonicalScenario(), workers)
	if err != nil {
		t.Fatalf("RunScenarioWorkers(%d): %v", workers, err)
	}
	return out
}

func TestSpiralReproducibilitySameWorkers(t *testing.T) {
	a := runCanonical(t, 2)
	b := runCanonical(t, 2)

	if a.StateHash != b.StateHash {
		t.Fatalf("state hash differs across two identical runs:\n%s\n%s", a.StateHash, b.StateHash)
	}
	if len(a.Events) != len(b.Events) {
		t.Fatalf("event count differs: %d vs %d", len(a.Events), len(b.Events))
	}
	for i := range a.Events {
		if a.Events[i].String() != b.Events[i].String() {
			t.Fatalf("event %d differs:\n  %s\n  %s", i, a.Events[i], b.Events[i])
		}
	}
	if a.Death != b.Death {
		t.Fatalf("death verdict differs: %v vs %v", a.Death, b.Death)
	}
	if a.Death != DeathGhostCity {
		t.Fatalf("canonical scenario death = %v, want DeathGhostCity (the ASM-240 fixture collapses to ghost-city)", a.Death)
	}
}

func TestSpiralReproducibilityAcrossWorkerCounts(t *testing.T) {
	one := runCanonical(t, 1)
	many := runCanonical(t, 4)

	if one.StateHash != many.StateHash {
		t.Fatalf("state hash differs across worker counts:\n%s\n%s", one.StateHash, many.StateHash)
	}
	if len(one.Events) != len(many.Events) {
		t.Fatalf("event count differs across worker counts: %d vs %d", len(one.Events), len(many.Events))
	}
	for i := range one.Events {
		if one.Events[i].String() != many.Events[i].String() {
			t.Fatalf("event %d differs across worker counts:\n  %s\n  %s", i, one.Events[i], many.Events[i])
		}
	}
}

// --- AC-10: concurrent access is race-free (shard worker pool) ------------

func TestConcurrentAccessRaceFree(t *testing.T) {
	d := newTestAPI(t)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		n := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.ReportAbandonment([]CellRef{cellRef(n, n)}, int64(n))
			_ = d.Stage()
			_ = d.StateHash()
			_ = d.DecayedCellCount()
			_, _ = d.DecayState(cellRef(n, n))
		}()
	}
	wg.Wait()
}

// --- AC-11: malformed scenario is a registry error, nothing applied -------

func TestInvalidShockTypeRejected(t *testing.T) {
	d := newTestAPI(t)
	sc := CanonicalScenario()
	sc.ShockType = ShockType("serviceCollapse")

	_, err := d.RunScenario(sc)
	assertCode(t, err, ErrInvalidScenario)

	// No silently-ignored or silently-substituted shock was applied.
	if d.Stage() != StageStable {
		t.Errorf("rejected scenario left stage %v, want StageStable", d.Stage())
	}
	if ev := d.Events(); len(ev) != 0 {
		t.Errorf("rejected scenario recorded events: %v", ev)
	}
	if d.DecayedCellCount() != 0 {
		t.Errorf("rejected scenario applied decay to %d cells", d.DecayedCellCount())
	}
}

func TestUnknownTargetRejected(t *testing.T) {
	d := newTestAPI(t)
	sc := CanonicalScenario()
	sc.ShockTarget = &CellRef{Tile: world.TileCoord{X: -1, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}}

	_, err := d.RunScenario(sc)
	assertCode(t, err, ErrInvalidScenario)
	if ev := d.Events(); len(ev) != 0 {
		t.Errorf("rejected scenario recorded events: %v", ev)
	}
}

// --- AC-12: recovery on a healthy cell is a typed rejection ---------------

func TestNoDecayRecoveryRejected(t *testing.T) {
	d := newTestAPI(t)
	c := cellRef(9, 9)

	dem := d.RecoverDemolition(DemolitionCommand{CorrelationID: testCorrelationID(), Cell: c})
	if dem.Result.Accepted {
		t.Fatal("demolition of a healthy cell accepted (silent free recovery)")
	}
	if dem.Result.Error == nil || dem.Result.Error.Code != ErrNoDecayToRecover {
		t.Errorf("demolition rejection = %+v, want ErrorRef code %s", dem.Result.Error, ErrNoDecayToRecover)
	}

	inv := d.RecoverTargetedInvestment(TargetedInvestmentCommand{CorrelationID: testCorrelationID(), Cell: c})
	if inv.Result.Accepted || inv.Result.Error == nil || inv.Result.Error.Code != ErrNoDecayToRecover {
		t.Errorf("investment rejection = %+v, want ErrorRef code %s", inv.Result.Error, ErrNoDecayToRecover)
	}

	tax := d.RecoverTaxRelief(TaxReliefCommand{CorrelationID: testCorrelationID(), District: []CellRef{c}})
	if tax.Result.Accepted || tax.Result.Error == nil || tax.Result.Error.Code != ErrNoDecayToRecover {
		t.Errorf("tax relief rejection = %+v, want ErrorRef code %s", tax.Result.Error, ErrNoDecayToRecover)
	}
}

// --- AC-15(a)/AC-17: ghost-city gated on a prior warning ------------------

func TestGhostCityWarningLeadTime(t *testing.T) {
	d := newTestAPI(t)
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID(testCorrelationID()))
	if err := d.SetProjections(proj); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}

	out, err := d.RunScenario(CanonicalScenario())
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if out.Death != DeathGhostCity {
		t.Fatalf("canonical scenario death = %v, want DeathGhostCity", out.Death)
	}

	// The trigger month is read from the run's own death event, not invented.
	ledger, err := proj.WarningLedger()
	if err != nil {
		t.Fatalf("WarningLedger: %v", err)
	}
	entries := ledger.Query(projections.MetricMarginToGhostCity, 0, out.Month)
	if len(entries) == 0 {
		t.Fatal("no MarginToGhostCity warning recorded at all")
	}
	warningMonth := entries[0].Month

	var triggerMonth int64
	for _, ev := range d.Events() {
		if ev.Kind == EventDeath && ev.Death == DeathGhostCity {
			triggerMonth = ev.Month
		}
	}
	if triggerMonth == 0 {
		t.Fatal("no ghost-city death event recorded")
	}

	// AC-29(a)'s inequality, using the run's own real timestamps on both sides.
	lead := int64(d.cfg.GhostCity.MinWarningLeadMonths)
	if warningMonth+lead > triggerMonth {
		t.Fatalf("warning lead too short: warning month %d + %d > trigger month %d",
			warningMonth, lead, triggerMonth)
	}
}

func TestGhostCityNoWarningRejectsWithRegistryError(t *testing.T) {
	d := newTestAPI(t)
	// Projections is wired and the provider registered, but the normal monthly
	// processing that feeds MarginToGhostCity (AdvanceMonth) is deliberately
	// BYPASSED — so the WarningLedger is empty when the trigger is queried.
	if err := d.SetProjections(projections.NewProjectionsAPI(projections.WithCorrelationID(testCorrelationID()))); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}

	triggered, err := d.GhostCityTrigger(5_000, 60_000, 100)
	if triggered {
		t.Fatal("ghost-city trigger fired with no warning on record")
	}
	assertCode(t, err, ErrGhostCityNoWarning)

	// No death signal was emitted alongside the rejection (AC-17).
	if d.Death() != DeathNone {
		t.Fatalf("death = %v, want DeathNone (rejection must not emit game-over)", d.Death())
	}
}

// --- AC-16: the warning tracks recovery --------------------------------

func TestGhostCityWarningRecoveryClearsWarning(t *testing.T) {
	d := newTestAPI(t)
	if err := d.SetProjections(projections.NewProjectionsAPI(projections.WithCorrelationID(testCorrelationID()))); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}

	// Drive a real declining population trend (above the threshold, but
	// worsening toward it) — the normal processing feeds MarginToGhostCity.
	pop := int64(60_000)
	for month := int64(0); month < 12; month++ {
		pop -= 3_000
		if _, err := d.AdvanceMonth(MonthInput{
			Month: month, Population: pop, NetMigration: -3_000,
			Attractiveness: 30, TaxDelta: -1_000,
		}); err != nil {
			t.Fatalf("AdvanceMonth(%d): %v", month, err)
		}
	}
	if !d.ActiveGhostCityWarning(11) {
		t.Fatal("expected an active ghost-city warning during the decline")
	}

	// Recover: population rises for several months — the warning must clear.
	for month := int64(12); month < 16; month++ {
		pop += 3_000
		if _, err := d.AdvanceMonth(MonthInput{
			Month: month, Population: pop, NetMigration: 3_000,
			Attractiveness: 55, TaxDelta: 1_000,
		}); err != nil {
			t.Fatalf("AdvanceMonth(%d): %v", month, err)
		}
	}
	if d.ActiveGhostCityWarning(15) {
		t.Fatal("warning still active after recovery (stale latched warning)")
	}
}

// --- SEC-087: negative population must be rejected at the boundary -------

// TestAdvanceMonthRejectsNegativePopulation is SEC-087's regression test.
// A negative population is a caller bug that must be rejected at the
// AdvanceMonth boundary BEFORE it reaches the death evaluator, where
// float64(-5) < 10%-of-historic-peak reads as "below threshold" and — with a
// prior qualifying warning on record — fires a spurious ghost-city game-over.
func TestAdvanceMonthRejectsNegativePopulation(t *testing.T) {
	d := newTestAPI(t)
	if err := d.SetProjections(projections.NewProjectionsAPI(projections.WithCorrelationID(testCorrelationID()))); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}

	// Drive a declining population (all positive) to build a historic peak
	// above 50,000 and record a qualifying MarginToGhostCity warning — the
	// precondition the SEC-087 attack needs (without it, the ghost-city gate
	// would refuse on its own and mask the bug under test).
	pop := int64(60_000)
	for month := int64(0); month < 12; month++ {
		pop -= 3_000
		if _, err := d.AdvanceMonth(MonthInput{
			Month: month, Population: pop, NetMigration: -3_000,
			Attractiveness: 30, TaxDelta: -1_000,
		}); err != nil {
			t.Fatalf("AdvanceMonth(%d): %v", month, err)
		}
	}

	res, err := d.AdvanceMonth(MonthInput{Month: 12, Population: -5, NetMigration: -3_000})
	assertCode(t, err, ErrNegativePopulation)
	if res.Death != DeathNone {
		t.Fatalf("death = %v, want DeathNone (negative population must not fire ghost-city)", res.Death)
	}
	if d.Death() != DeathNone {
		t.Fatalf("d.Death() = %v, want DeathNone", d.Death())
	}
}

// --- SEC-088: the runner must surface a death-condition gate rejection ---

// TestRunScenarioSurfacesGhostCityGateRejection is SEC-088's regression test.
// A decline far faster than the 6-month warning lead reaches the ghost-city
// threshold but the FEAT-068 gate correctly refuses to fire. The runner must
// surface that rejection (DeathErr) rather than report a silent DeathNone —
// which would be indistinguishable from "no death condition was reached".
func TestRunScenarioSurfacesGhostCityGateRejection(t *testing.T) {
	d := newTestAPI(t)
	if err := d.SetProjections(projections.NewProjectionsAPI(projections.WithCorrelationID(testCorrelationID()))); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}

	sc := CanonicalScenario()
	// A decline far faster than the 6-month warning lead: the warning is
	// recorded only ~2 months before the population crosses the ghost-city
	// threshold, so the gate refuses to fire at the crossing. Cap the run
	// before that lead can mature (warning month + 6), so the outcome is
	// DeathNone with the gate's rejection surfaced — not a silent success.
	sc.EmigrationPerMonth = 20_000
	sc.MonthCap = 10

	out, err := d.RunScenarioWorkers(sc, 1)
	if err != nil {
		t.Fatalf("RunScenarioWorkers: %v", err)
	}
	if out.Death != DeathNone {
		t.Fatalf("death = %v, want DeathNone (the gate must refuse to fire)", out.Death)
	}
	if out.DeathErr == nil {
		t.Fatal("DeathErr not surfaced — silent false negative (SEC-088)")
	}
	assertCode(t, out.DeathErr, ErrGhostCityNoWarning)
}
