package citizens

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// mortalityJSON builds a minimal, schema-valid data/mortality.json document
// (with the three numeric params overridden) for the loader tests — no
// dependence on the checked-in data file, so a schema test can mutate a value
// in isolation.
func mortalityJSON(budget, threshold, multiplier string) string {
	return fmt.Sprintf(`{
  "version": 1,
  "meta": {"module": "engine.citizens", "featureKey": "feat.deathwave", "specRefs": ["§5.1"], "balanceRegime": "placeholder"},
  "params": {
    "monthlyDeathBudget": {"value": %s, "unit": "deaths/month", "disclosure": "placeholder"},
    "weatherEmergencyThreshold": {"value": %s, "unit": "probability", "disclosure": "placeholder"},
    "emergencyBudgetMultiplier": {"value": %s, "unit": "multiple", "disclosure": "placeholder"}
  }
}`, budget, threshold, multiplier)
}

// TestMortalityConfigLoadsAndValidates (AC-5, GR#15): the checked-in data file
// is well-formed and every placeholder carries unit + disclosure.
func TestMortalityConfigLoadsAndValidates(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	if cfg.Params.MonthlyDeathBudget.Value <= 0 {
		t.Fatalf("monthlyDeathBudget must be positive, got %g", cfg.Params.MonthlyDeathBudget.Value)
	}
	thr := cfg.Params.WeatherEmergencyThreshold.Value
	if thr < 0 || thr > 1 {
		t.Fatalf("weatherEmergencyThreshold %g out of [0,1]", thr)
	}
	if cfg.Params.EmergencyBudgetMultiplier.Value < 1 {
		t.Fatalf("emergencyBudgetMultiplier %g must be >= 1", cfg.Params.EmergencyBudgetMultiplier.Value)
	}
	if cfg.Params.MonthlyDeathBudget.Unit == "" || cfg.Params.MonthlyDeathBudget.Disclosure == "" {
		t.Fatalf("monthlyDeathBudget missing unit or disclosure")
	}
}

// TestMortalityConfigMalformedRejected (AC-12, GR#7): a missing, malformed,
// or schema-invalid budget data file produces a registry-sourced error at
// load time, never a silent default that would silently re-enable the cliff.
func TestMortalityConfigMalformedRejected(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, FileMortality), []byte(content), 0o644); err != nil {
			t.Fatalf("write mortality.json: %v", err)
		}
	}

	// Missing file → registry "config not found", no invented default.
	if _, err := LoadMortalityConfig(dir, "corr"); err == nil {
		t.Fatal("expected a registry error for a missing budget file, got nil")
	} else {
		assertRegistryCode(t, err, mortalityDataNotFoundCode)
	}

	// Malformed JSON → registry "not well-formed JSON".
	write(`{ this is not json`)
	if _, err := LoadMortalityConfig(dir, "corr"); err == nil {
		t.Fatal("expected a registry error for malformed JSON, got nil")
	} else {
		assertRegistryCode(t, err, mortalityDataMalformedCode)
	}

	// Negative budget → registry "schema validation failed", no silent default.
	write(mortalityJSON("-5", "0.9", "10"))
	if _, err := LoadMortalityConfig(dir, "corr"); err == nil {
		t.Fatal("expected a registry error for a negative budget, got nil")
	} else {
		assertRegistryCode(t, err, mortalityDataInvalidCode)
	}

	// A well-formed file loads (sanity: the rejection above is about the
	// value, not the loader).
	write(mortalityJSON("50", "0.9", "10"))
	cfg, err := LoadMortalityConfig(dir, "corr")
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if int(cfg.Params.MonthlyDeathBudget.Value) != 50 {
		t.Fatalf("budget = %g, want 50", cfg.Params.MonthlyDeathBudget.Value)
	}
}

// TestWeatherSeverityDeterministic (AC-14, GR#21): severity is a pure
// function of (seed, month) in [0,1), identical on every call — no wall
// clock, no shared RNG.
func TestWeatherSeverityDeterministic(t *testing.T) {
	for _, seed := range []uint64{0, 1, 42, 1 << 62} {
		for month := int64(0); month < 50; month++ {
			s := WeatherSeverity(seed, month)
			if s < 0 || s >= 1 {
				t.Fatalf("WeatherSeverity(%d,%d) = %g out of [0,1)", seed, month, s)
			}
			if WeatherSeverity(seed, month) != s {
				t.Fatalf("WeatherSeverity not deterministic at seed=%d month=%d", seed, month)
			}
		}
	}
}

// TestWeatherEmergencyBudgetExceedsNormal (AC-6/AC-7, direction only): over a
// wide deterministic month range both emergency and non-emergency months
// occur, a non-emergency month uses exactly the data budget, and an emergency
// month's budget exceeds it.
func TestWeatherEmergencyBudgetExceedsNormal(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	const seed = 123
	normalBudget := int(cfg.Params.MonthlyDeathBudget.Value)

	var foundEmergency, foundNormal bool
	emergencyBudget := 0
	for month := int64(0); month < 2000; month++ {
		b := MonthlyDeathBudget(seed, month, cfg)
		if b < 0 {
			t.Fatalf("budget %d < 0 at month %d", b, month)
		}
		if IsWeatherEmergency(seed, month, cfg) {
			foundEmergency = true
			if emergencyBudget == 0 {
				emergencyBudget = b
			}
		} else {
			foundNormal = true
			if b != normalBudget {
				t.Fatalf("non-emergency month %d budget %d != data budget %d", month, b, normalBudget)
			}
		}
	}
	if !foundEmergency || !foundNormal {
		t.Fatalf("expected both emergency and normal months over 2000 months, got emergency=%v normal=%v", foundEmergency, foundNormal)
	}
	if emergencyBudget <= normalBudget {
		t.Fatalf("emergency budget %d must exceed normal budget %d (suspension of smoothing)", emergencyBudget, normalBudget)
	}
}

// TestMonthlyDeathBudgetAlwaysBounded (AC-1's "no single month kills
// everyone"): every month's budget is finite, non-negative, and never exceeds
// the data budget times the emergency multiplier — even an emergency month is
// a bounded throughput, never unbounded.
func TestMonthlyDeathBudgetAlwaysBounded(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	normal := int(cfg.Params.MonthlyDeathBudget.Value)
	maxAllowed := int(float64(normal) * cfg.Params.EmergencyBudgetMultiplier.Value)
	for month := int64(0); month < 500; month++ {
		b := MonthlyDeathBudget(0, month, cfg)
		if b < 0 || b > maxAllowed {
			t.Fatalf("budget %d out of [0, %d] at month %d (must stay bounded)", b, maxAllowed, month)
		}
	}
}

// TestDeathQueueSingleTerminalSelection (AC-3): a queued citizen is selected
// exactly once — a duplicate enqueue is a no-op.
func TestDeathQueueSingleTerminalSelection(t *testing.T) {
	q := NewDeathQueue()
	q.Enqueue(42, 0)
	q.Enqueue(42, 1) // same citizen re-selected in a later month: must be ignored
	if q.Len() != 1 {
		t.Fatalf("duplicate enqueue re-selected: len=%d, want 1", q.Len())
	}
	if !q.Queued(42) {
		t.Fatalf("Queued(42) = false after enqueue")
	}
	if q.Queued(7) {
		t.Fatalf("Queued(7) = true for an unselected citizen")
	}
}

// TestDeathQueueBoundedRealisation (AC-1): Realise releases at most the
// budget, in FIFO (selection month, citizen id) order, and the remainder
// stays queued — never lost.
func TestDeathQueueBoundedRealisation(t *testing.T) {
	q := NewDeathQueue()
	const total = 100
	for id := uint64(1); id <= total; id++ {
		q.Enqueue(id, 0)
	}
	realised := q.Realise(7, 1, false)
	if len(realised) != 7 {
		t.Fatalf("Realise(7) released %d, want 7 (budget bound)", len(realised))
	}
	if q.Len() != total-7 {
		t.Fatalf("queue len %d after realise, want %d", q.Len(), total-7)
	}
	for i, d := range realised {
		if d.CitizenID != uint64(i+1) {
			t.Fatalf("FIFO order wrong at %d: got id %d, want %d", i, d.CitizenID, i+1)
		}
		if d.DeathMonth != 1 || d.Emergency {
			t.Fatalf("realised record wrong: %+v", d)
		}
	}
}

// TestDeathQueueConservation (AC-2): smoothing defers, never destroys — a
// full drain releases every selected death exactly once, and the queue ends
// empty.
func TestDeathQueueConservation(t *testing.T) {
	q := NewDeathQueue()
	const total = 500
	for id := uint64(1); id <= total; id++ {
		q.Enqueue(id, 0)
	}
	var realised []RealisedDeath
	for q.Len() > 0 {
		realised = append(realised, q.Realise(13, 1, false)...)
	}
	if len(realised) != total {
		t.Fatalf("drained %d deaths, want %d (none lost, none duplicated)", len(realised), total)
	}
	if q.Len() != 0 {
		t.Fatalf("queue not empty after drain: %d", q.Len())
	}
	seen := make(map[uint64]bool, total)
	for _, d := range realised {
		if seen[d.CitizenID] {
			t.Fatalf("duplicate realised death %d", d.CitizenID)
		}
		seen[d.CitizenID] = true
	}
	if len(seen) != total {
		t.Fatalf("expected %d distinct realised ids, got %d", total, len(seen))
	}
}

// TestDeathQueueFIFODeterministic (AC-4): realisation order is the documented
// total order — (selection month, then citizen id) — independent of enqueue
// order, and byte-identical across identical runs.
func TestDeathQueueFIFODeterministic(t *testing.T) {
	build := func() []RealisedDeath {
		q := NewDeathQueue()
		// Deliberately out of order, across selection months.
		q.Enqueue(10, 0)
		q.Enqueue(2, 2)
		q.Enqueue(5, 1)
		q.Enqueue(1, 0)
		q.Enqueue(9, 2)
		q.Enqueue(4, 1)
		return q.Realise(100, 3, true)
	}
	a := build()
	b := build()
	want := []uint64{1, 10, 4, 5, 2, 9} // (0,1),(0,10),(1,4),(1,5),(2,2),(2,9)
	if len(a) != len(want) || len(b) != len(want) {
		t.Fatalf("realise lengths %d/%d, want %d", len(a), len(b), len(want))
	}
	for i := range a {
		if a[i].CitizenID != want[i] {
			t.Fatalf("FIFO order wrong at %d: got %d, want %d", i, a[i].CitizenID, want[i])
		}
		if a[i] != b[i] {
			t.Fatalf("realisation sequence not byte-identical at %d: %+v vs %+v", i, a[i], b[i])
		}
		if !a[i].Emergency {
			t.Fatalf("emergency flag not carried on the handoff record: %+v", a[i])
		}
	}
}

// TestApplyMonthlyDefersDeath (AC-1's false-pass guard): a hazard-selected
// death is DEFERRED, not removed inline — the citizen stays in the shard
// (still alive, still aging) until the month boundary realises them.
func TestApplyMonthlyDefersDeath(t *testing.T) {
	s := newColdShard(0)
	rec := mkRecord(1, 0)
	rec.BirthMonth = 0
	rec.HealthBand = HealthExcellent
	rec.Access = 100
	s.append(rec)

	// Age 200 years at month 2400 ⇒ saturated Gompertz hazard (~1.0), so the
	// draw is a guaranteed selection.
	tot := s.applyMonthly(7, 2400, ColdPassParams{MortalityMultiplier: 1.0}, nil, nil)

	if len(tot.selectedDeaths) != 1 || tot.selectedDeaths[0] != 1 {
		t.Fatalf("expected one selected (deferred) death, got %v", tot.selectedDeaths)
	}
	if s.count() != 1 {
		t.Fatalf("selected citizen was removed inline: shard count = %d, want 1", s.count())
	}
	if s.monthlyUpdates[0] != 1 {
		t.Fatalf("selected citizen did not receive its monthly update: %d, want 1", s.monthlyUpdates[0])
	}
}

// TestApplyMonthlySkipsQueuedCitizen (AC-3): a citizen already in the death
// queue does not draw mortality again (single terminal selection), but still
// ages/updates.
func TestApplyMonthlySkipsQueuedCitizen(t *testing.T) {
	s := newColdShard(0)
	rec := mkRecord(1, 0)
	rec.BirthMonth = 0
	rec.HealthBand = HealthExcellent
	rec.Access = 100
	s.append(rec)

	queued := func(id uint64) bool { return id == 1 }
	tot := s.applyMonthly(7, 2400, ColdPassParams{MortalityMultiplier: 1.0}, nil, queued)

	if len(tot.selectedDeaths) != 0 {
		t.Fatalf("queued citizen drew mortality again: %v", tot.selectedDeaths)
	}
	if s.monthlyUpdates[0] != 1 {
		t.Fatalf("queued citizen did not age: monthlyUpdates=%d", s.monthlyUpdates[0])
	}
}

// TestCohortCliffSmoothedThroughAPI (AC-1, the load-bearing check): a large
// same-birthMonth cohort aged onto the steep Gompertz slope selects en masse,
// yet the living-population delta in the cliff month is bounded by the data
// budget and the un-realised remainder is retained in the queue — a genuine
// one-month population cliff is structurally impossible while smoothing holds.
func TestCohortCliffSmoothedThroughAPI(t *testing.T) {
	api, err := NewCitizensAPI(7, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	// The cliff month's actual budget (normal, or emergency×multiplier if the
	// deterministic weather draw declares an emergency for this seed/month) —
	// smoothing must bound the realised delta to this figure either way.
	budget := MonthlyDeathBudget(7, 2400, cfg)
	if budget <= 0 {
		t.Fatalf("monthly death budget must be positive, got %d", budget)
	}

	const cohort = 2000
	records := make([]ColdRecord, cohort)
	for i := range records {
		r := mkRecord(uint64(i+1), 0)
		r.BirthMonth = 0
		r.HealthBand = HealthExcellent
		r.Access = 100
		r.Household = 0
		r.Partner = 0
		records[i] = r
	}
	if err := api.SeedColdRecords(records, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = 2400 // age the cohort to 200 years: saturated Gompertz hazard
	api.mu.Unlock()

	before := api.TotalPopulation("corr")
	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	after := api.TotalPopulation("corr")
	_, deaths := api.VitalEvents("corr")
	pending := api.PendingDeaths("corr")

	if delta := before - after; delta > budget {
		t.Fatalf("cliff month removed %d citizens, exceeding the %d-death budget (smoothing must bound the population delta)", delta, budget)
	}
	if deaths > budget {
		t.Fatalf("realised %d deaths in one month, exceeding the %d budget", deaths, budget)
	}
	if deaths != before-after {
		t.Fatalf("VitalEvents deaths %d != population drop %d", deaths, before-after)
	}
	if pending == 0 {
		t.Fatalf("expected a non-empty pending queue after the cliff month (selections must exceed the budget)")
	}
	// The scenario's premise holds: selections far exceed the budget (the
	// cohort did hit the cliff), yet only the budget was realised.
	if deaths+pending <= budget {
		t.Fatalf("expected selections well above the budget, got selected=%d", deaths+pending)
	}
}

// TestDeathQueueRealisationInvariantAcrossWorkers (AC-15): the same seed and
// command log produce byte-identical realised-death sequences at worker count
// 1 vs 14 — the queue order is a pure function of (seed, commands), never
// shard-completion order.
func TestDeathQueueRealisationInvariantAcrossWorkers(t *testing.T) {
	run := func(workers int) []RealisedDeath {
		api, err := NewCitizensAPI(42, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = workers
		records := make([]ColdRecord, 300)
		for i := range records {
			r := mkRecord(uint64(i+1), uint16(i%10))
			r.BirthMonth = 0
			r.HealthBand = HealthExcellent
			r.Access = 100
			r.Household = 0
			r.Partner = 0
			records[i] = r
		}
		if err := api.SeedColdRecords(records, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = 2400
		api.mu.Unlock()
		for m := 0; m < 2; m++ {
			if err := api.AdvanceMonth("corr"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.RealisedDeaths("corr")
	}

	a := run(1)
	b := run(14)
	if len(a) == 0 {
		t.Fatal("expected non-empty realised-death ledger")
	}
	if len(a) != len(b) {
		t.Fatalf("realised counts differ across workers: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("realised sequence differs at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestDeathQueueRemove (AC-13 dequeue primitive): Remove cancels exactly the
// named pending death — the citizen is dequeued, surviving entries are
// untouched, and a citizen with no pending entry is a no-op.
func TestDeathQueueRemove(t *testing.T) {
	q := NewDeathQueue()
	q.Enqueue(1, 0)
	q.Enqueue(2, 1)
	q.Enqueue(3, 0)

	q.Remove(2)
	if q.Len() != 2 {
		t.Fatalf("Len = %d after Remove(2), want 2", q.Len())
	}
	if q.Queued(2) {
		t.Fatalf("Queued(2) = true after Remove(2)")
	}
	if !q.Queued(1) || !q.Queued(3) {
		t.Fatalf("Remove(2) disturbed surviving entries: Queued(1)=%v Queued(3)=%v", q.Queued(1), q.Queued(3))
	}

	q.Remove(999) // no pending entry: must be a no-op
	if q.Len() != 2 {
		t.Fatalf("Len = %d after Remove(999), want 2", q.Len())
	}

	// Removing the head does not disturb the FIFO realisation order of the rest.
	q.Remove(1)
	q.Remove(3)
	if q.Len() != 0 {
		t.Fatalf("Len = %d after removing all, want 0", q.Len())
	}
}

// TestEmigrationCancelsQueuedDeath (AC-13 phantom death, FEAT-087 r1 REJECT):
// a citizen hazard-selected for death who EMIGRATES before the month boundary
// must be dequeued by the LifeEventDeath departure path — realiseDeathsLocked
// must never drain the emigrant and emit a RealisedDeath for a citizen who did
// NOT die. Without the fix, the queue still holds the emigrant at the month
// boundary, so Realise pops them and departCitizenLocked records a phantom
// RealisedDeath. attract.applyEmigration issues exactly the LifeEventDeath
// command used below.
func TestEmigrationCancelsQueuedDeath(t *testing.T) {
	api, err := NewCitizensAPI(7, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	// Two residents, neither householded (the departure path unwires household
	// membership; unpaired citizens keep this a pure population-delta check).
	records := []ColdRecord{mkRecord(1, 0), mkRecord(2, 0)}
	for i := range records {
		records[i].Household = 0
		records[i].Partner = 0
	}
	if err := api.SeedColdRecords(records, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	// Citizen 1 is hazard-selected this month and sits in the smoothing queue
	// awaiting realisation (the single, terminal selection event, AC-3).
	api.mu.Lock()
	api.deathQueue.Enqueue(1, api.month)
	api.mu.Unlock()
	if got := api.PendingDeaths("corr"); got != 1 {
		t.Fatalf("PendingDeaths = %d before emigration, want 1", got)
	}

	// Citizen 1 emigrates before the month boundary (the emigration mechanism).
	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		CorrelationID: "corr",
		Kind:          LifeEventDeath,
		CitizenID:     1,
	}); err != nil {
		t.Fatalf("emigrate citizen 1 via LifeEventDeath: %v", err)
	}

	// The ALIVE departure must cancel the pending death immediately — a queued
	// emigrant is the phantom death.
	if got := api.PendingDeaths("corr"); got != 0 {
		t.Fatalf("PendingDeaths = %d after emigration, want 0 (emigrant must be dequeued)", got)
	}

	// Complete the month: the boundary realises the queue. Whatever else is
	// realised, the emigrant must never appear as a death.
	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	for _, d := range api.RealisedDeaths("corr") {
		if d.CitizenID == 1 {
			t.Fatalf("phantom death: emigrated citizen 1 realised as dead: %+v", d)
		}
	}

	// And the emigrant is gone exactly once — no record to realise again.
	if _, ok := api.CitizenAt(1, "corr"); ok {
		t.Fatalf("emigrated citizen 1 still resolves after departure")
	}
}
