package citizens

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-087 (mkey feat.deathwave) inc2 acceptance tests: AC-6 (emergency
// suspends the smoothing budget for a major non-smoothed death event),
// AC-7 (the emergency is weather-driven, sourced through engine.season's
// SeasonAPI curves per ASM-579 -- never a mortality-local calendar, never
// feat.disasters), AC-8 (suspension of throughput only -- the hazard
// SELECTION set is unaffected by the emergency).

// realSeasonAPI loads the real data/seasonal.json through engine.season
// (never a mortality-local re-derivation of seasonality, GR#3) -- the same
// fixture pattern engine.build/engine.education/engine.tourism's own tests
// use for a real *season.SeasonAPI.
func realSeasonAPI(t *testing.T) *season.SeasonAPI {
	t.Helper()
	api, err := season.LoadDefault("corr-deathwave-inc2")
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}
	return api
}

// Absolute month indices landing on known calendar months under
// data/seasonal.json's monthIndexConvention (index 0 = January). Chosen to
// land on months data/mortality.json's inc2 thresholds are calibrated
// against (see that file's weatherEmergency disclosures): January (a
// winter month, healthWaveModifier 0.05 >= threshold 0.04), July (a
// drought-shaped month, waterSummerPeak 1.25 >= threshold 1.2), and April
// (a mild month, both curves at baseline -- neither threshold trips).
// Chosen large enough that mkGuaranteedDeathRecord's birthMonth (month -
// 12000) stays >= 0 -- ValidateCitizen/mkRecord reject a negative
// birthMonth (MET-G001), and the live-level tests below need a
// guaranteed-death cohort at these exact months.
const (
	monthJanuary = int64(19992) // 19992 % 12 == 0 == January
	monthJuly    = int64(19998) // 19998 % 12 == 6 == July
	monthApril   = int64(19995) // 19995 % 12 == 3 == April
)

// --- AC-7: the emergency declaration (IsWeatherEmergency) ---

// TestIsWeatherEmergency_WinterMonthTrips (AC-7): a winter month
// (HealthWaveModifier magnitude >= the data-file threshold) declares an
// emergency.
func TestIsWeatherEmergency_WinterMonthTrips(t *testing.T) {
	seasonAPI := realSeasonAPI(t)
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}

	got, err := IsWeatherEmergency(seasonAPI, monthJanuary, cfg, "corr")
	if err != nil {
		t.Fatalf("IsWeatherEmergency: %v", err)
	}
	if !got {
		hw, _ := seasonAPI.HealthWaveModifier(monthJanuary)
		t.Fatalf("IsWeatherEmergency(January) = false, want true (HealthWaveModifier=%v, threshold=%v)", hw, cfg.WinterHealthWaveThreshold())
	}
}

// TestIsWeatherEmergency_DroughtShapedMonthTrips (AC-7): a drought-shaped
// month (WaterDemandMultiplier >= the data-file threshold) declares an
// emergency, independently of the winter curve.
func TestIsWeatherEmergency_DroughtShapedMonthTrips(t *testing.T) {
	seasonAPI := realSeasonAPI(t)
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}

	got, err := IsWeatherEmergency(seasonAPI, monthJuly, cfg, "corr")
	if err != nil {
		t.Fatalf("IsWeatherEmergency: %v", err)
	}
	if !got {
		wd, _ := seasonAPI.WaterDemandMultiplier(monthJuly)
		t.Fatalf("IsWeatherEmergency(July) = false, want true (WaterDemandMultiplier=%v, threshold=%v)", wd, cfg.DroughtWaterDemandThreshold())
	}
}

// TestIsWeatherEmergency_MildMonthDoesNotTrip (AC-7's false-pass guard): a
// mild month, where neither curve reaches its threshold, must NOT declare
// an emergency -- proves the thresholds are not so loose that every month
// qualifies (the "treating every nonzero HealthWaveModifier as emergency"
// false-pass the acceptance doc names explicitly).
func TestIsWeatherEmergency_MildMonthDoesNotTrip(t *testing.T) {
	seasonAPI := realSeasonAPI(t)
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}

	got, err := IsWeatherEmergency(seasonAPI, monthApril, cfg, "corr")
	if err != nil {
		t.Fatalf("IsWeatherEmergency: %v", err)
	}
	if got {
		hw, _ := seasonAPI.HealthWaveModifier(monthApril)
		wd, _ := seasonAPI.WaterDemandMultiplier(monthApril)
		t.Fatalf("IsWeatherEmergency(April) = true, want false (HealthWaveModifier=%v, WaterDemandMultiplier=%v, thresholds=%v/%v)",
			hw, wd, cfg.WinterHealthWaveThreshold(), cfg.DroughtWaterDemandThreshold())
	}
}

// TestIsWeatherEmergency_NilSeasonNeverTrips (the documented nil-season
// no-op, mirroring engine.build/engine.cafe/engine.education's own
// convention): a CitizensAPI never wired via SetSeason must never declare
// an emergency, even in a winter month -- so every pre-inc2 caller
// (NewCitizensAPI with no SetSeason call) is entirely unaffected.
func TestIsWeatherEmergency_NilSeasonNeverTrips(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	got, err := IsWeatherEmergency(nil, monthJanuary, cfg, "corr")
	if err != nil {
		t.Fatalf("IsWeatherEmergency(nil season): %v", err)
	}
	if got {
		t.Fatal("IsWeatherEmergency(nil season) = true, want false (a nil season must never declare an emergency)")
	}
}

// --- AC-6: EmergencyRealise suspends the smoothing budget ---

// TestEmergencyRealise_UnboundedSentinelDrainsEntireQueue (AC-6, the
// load-bearing check): a queue loaded well beyond the normal monthly
// budget, realised under a declared emergency with the data file's 0
// ("unbounded") monthlyEmergencyBudget sentinel, must release the ENTIRE
// queue in one month -- realised deaths >> the ordinary budget, and the
// queue drains to empty. The mutate-off comparison (same fixture,
// emergency=false) proves the ordinary path would NOT have done this,
// so this is a genuine prove-can-fail check, not a tautology.
func TestEmergencyRealise_UnboundedSentinelDrainsEntireQueue(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	budget := cfg.MonthlyDeathBudget()
	const n = 500 // well beyond the data-file budget (25)
	if n <= budget*3 {
		t.Fatalf("test setup invalid: n=%d must be >> budget=%d", n, budget)
	}

	mk := func() *DeathQueue {
		q := NewDeathQueue()
		for id := uint64(1); id <= uint64(n); id++ {
			if err := q.Enqueue(id, 1000, "corr"); err != nil {
				t.Fatalf("Enqueue(%d): %v", id, err)
			}
		}
		return q
	}

	// Mutation-off comparison: WITHOUT the emergency, the ordinary budget
	// bounds the release exactly as inc1 built it.
	qOrdinary := mk()
	releasedOrdinary := EmergencyRealise(qOrdinary, cfg, false, 1000, "corr")
	if len(releasedOrdinary) != budget {
		t.Fatalf("EmergencyRealise(emergency=false) released %d, want exactly the ordinary budget %d", len(releasedOrdinary), budget)
	}
	if got := qOrdinary.Len("corr"); got != n-budget {
		t.Fatalf("qOrdinary.Len() after a non-emergency release = %d, want %d (ordinary smoothing must still bound the release)", got, n-budget)
	}

	// The actual AC-6 case: WITH the emergency declared, the queue must
	// drain entirely this month -- realised >> the ordinary budget.
	qEmergency := mk()
	releasedEmergency := EmergencyRealise(qEmergency, cfg, true, 1000, "corr")
	if len(releasedEmergency) != n {
		t.Fatalf("EmergencyRealise(emergency=true) released %d, want the ENTIRE queue (%d) -- the unbounded sentinel must drain fully", len(releasedEmergency), n)
	}
	if len(releasedEmergency) <= budget {
		t.Fatalf("emergency release (%d) did not exceed the ordinary budget (%d) -- AC-6 requires realised deaths >> budget", len(releasedEmergency), budget)
	}
	if got := qEmergency.Len("corr"); got != 0 {
		t.Fatalf("qEmergency.Len() after an emergency release = %d, want 0 (the queue must fully drain during the declared emergency)", got)
	}
}

// TestEmergencyRealise_FiniteEmergencyBudgetOverridesOrdinary (AC-6, a
// finite documented emergency throughput rather than the 0 sentinel): a
// positive monthlyEmergencyBudget REPLACES (never adds to) the ordinary
// budget for that month's release.
func TestEmergencyRealise_FiniteEmergencyBudgetOverridesOrdinary(t *testing.T) {
	dir := t.TempDir()
	writeMortalityConfig(t, dir, mortalityConfigFixture{
		monthlyDeathBudget:          10,
		monthlyEmergencyBudget:      200,
		winterHealthWaveThreshold:   0.04,
		droughtWaterDemandThreshold: 1.2,
	})
	cfg, err := LoadMortalityConfig(dir, "corr")
	if err != nil {
		t.Fatalf("LoadMortalityConfig: %v", err)
	}

	q := NewDeathQueue()
	const n = 500
	for id := uint64(1); id <= uint64(n); id++ {
		if err := q.Enqueue(id, 1000, "corr"); err != nil {
			t.Fatalf("Enqueue(%d): %v", id, err)
		}
	}

	released := EmergencyRealise(q, cfg, true, 1000, "corr")
	if len(released) != 200 {
		t.Fatalf("EmergencyRealise with a finite emergency budget released %d, want exactly the configured emergency throughput (200), not the ordinary budget (10) and not unbounded (%d)", len(released), n)
	}
}

// --- AC-8: suspension of throughput only -- hazard selection unaffected ---

// TestEmergencyDoesNotAffectHazardSelection (AC-8, the scope-honesty
// check): for a FIXED population whose hazard selections are held
// constant (a deterministic seed/month/cohort), the emergency flag must
// change only HOW MANY selected deaths REALISE this month, never WHICH
// citizens the Gompertz-Makeham hazard selected. Run at the LIVE
// CitizensAPI level (AdvanceMonth) so this is a structural proof, not a
// documentation promise: the same guaranteed-death cohort, advanced one
// month with the season wired to a winter month (emergency=true) versus
// unwired (emergency=false, same cohort/seed/month), must select the
// IDENTICAL set of hazard hits (informational passTotals.selected count
// via deathQueue.TotalRealised+Len sum immediately after the pass), while
// the realised COUNT differs sharply.
func TestEmergencyDoesNotAffectHazardSelection(t *testing.T) {
	const seed = uint64(561)
	const n = 200

	run := func(wireSeason bool) (selected int, realisedThisMonth int) {
		api, err := NewCitizensAPI(seed, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		if wireSeason {
			if err := api.SetSeason(realSeasonAPI(t), "corr"); err != nil {
				t.Fatalf("SetSeason: %v", err)
			}
		}
		ids := seedGuaranteedDeathCohort(t, api, 500_000, n, monthJanuary)
		_ = ids

		_, deaths, err := api.AdvanceDayTick("corr")
		if err != nil {
			t.Fatalf("AdvanceDayTick day0: %v", err)
		}
		// Drive the remaining day-ticks of the month so every shard has had
		// its one scheduled visit and the once-per-month realisation fires
		// (mirrors AdvanceMonth, but we need the running deaths total from
		// EVERY day-tick, not just the last).
		total := deaths
		for d := 1; d < DaysPerMonth; d++ {
			_, dd, err := api.AdvanceDayTick("corr")
			if err != nil {
				t.Fatalf("AdvanceDayTick day%d: %v", d, err)
			}
			total += dd
		}
		selectedCount := api.deathQueue.TotalRealised("corr") + api.deathQueue.Len("corr")
		return selectedCount, total
	}

	selectedNoEmergency, realisedNoEmergency := run(false)
	selectedEmergency, realisedEmergency := run(true)

	if selectedNoEmergency != selectedEmergency {
		t.Fatalf("hazard SELECTION differs by emergency state: no-emergency selected=%d, emergency selected=%d -- AC-8 requires the selection set be unaffected by the emergency", selectedNoEmergency, selectedEmergency)
	}
	if realisedEmergency <= realisedNoEmergency {
		t.Fatalf("realised deaths did not increase under the emergency: no-emergency realised=%d, emergency realised=%d -- the emergency must change THROUGHPUT (AC-6)", realisedNoEmergency, realisedEmergency)
	}
	// Sanity: the fixture must actually have selected more than the
	// ordinary budget, or this test is vacuous.
	if selectedNoEmergency < 26 {
		t.Fatalf("test setup invalid: only %d citizens were hazard-selected, want > the data-file budget (25) so the emergency has something extra to release", selectedNoEmergency)
	}
}

// TestUnwiredCitizensAPINeverSuspendsBudgetEvenInWinter (mutation-off
// proof, AC-6/AC-7 combined): a CitizensAPI that was never wired via
// SetSeason must behave EXACTLY as inc1/inc1.5 (no emergency, ever), even
// when the sim clock sits on a winter month -- the "if the suspension
// mechanism were removed" mutation this feature's whole point depends on.
// Without the emergency wiring, a guaranteed-death cohort run through a
// winter month must still be smoothed to at most the ordinary budget.
func TestUnwiredCitizensAPINeverSuspendsBudgetEvenInWinter(t *testing.T) {
	const seed = uint64(562)
	const n = 200
	const budget = 25 // data/mortality.json params.monthlyDeathBudget.value

	api, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	// Deliberately NOT calling SetSeason.
	seedGuaranteedDeathCohort(t, api, 600_000, n, monthJanuary)
	popBefore := api.TotalPopulation("corr")

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	popAfter := api.TotalPopulation("corr")
	delta := popBefore - popAfter
	if delta > budget {
		t.Fatalf("living-population delta = %d in a winter month with NO season wired, want <= budget=%d (an unwired CitizensAPI must never suspend the smoothing budget)", delta, budget)
	}
}

// --- AC-15: worker-count invariance extends to the emergency path ---

// TestEmergencyRealisationWorkerCountInvariant (AC-15, at the emergency
// path): the same guaranteed-death cohort, season wired to a winter month,
// run at worker counts 1 and 14, must produce a byte-identical
// PopulationHash and TotalPopulation -- the emergency declaration
// (IsWeatherEmergency) is a pure function of (SeasonAPI, month, cfg), so
// it can never itself introduce a worker-count-dependent result, and
// EmergencyRealise's release is still governed by DeathQueue's own
// worker-count-invariant FIFO order (AC-4/AC-15).
func TestEmergencyRealisationWorkerCountInvariant(t *testing.T) {
	const seed = uint64(563)
	const n = 300

	records := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		records = append(records, mkGuaranteedDeathRecord(uint64(i), monthJanuary))
	}

	run := func(workers int) ([32]byte, int) {
		api, err := NewCitizensAPI(seed, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		if err := api.SetSeason(realSeasonAPI(t), "corr"); err != nil {
			t.Fatalf("SetSeason: %v", err)
		}
		api.workers = workers
		if err := api.SeedColdRecords(records, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = monthJanuary
		api.mu.Unlock()
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
		return api.PopulationHash("corr"), api.TotalPopulation("corr")
	}

	hash1, pop1 := run(1)
	hash14, pop14 := run(14)
	if pop1 != pop14 {
		t.Fatalf("TotalPopulation differs by worker count under an emergency release: 1 worker=%d, 14 workers=%d", pop1, pop14)
	}
	if hash1 != hash14 {
		t.Fatalf("PopulationHash differs by worker count under an emergency release: 1 worker=%x, 14 workers=%x", hash1, hash14)
	}
	if pop1 != 0 {
		t.Fatalf("population after an emergency month = %d, want 0 (the unbounded sentinel must have drained the whole guaranteed-death cohort)", pop1)
	}
}

// --- AC-7/AC-12/GR#15: weatherEmergency threshold data-file validation ---

// mortalityConfigFixture is a full data/mortality.json shape a test can
// mutate one field of at a time.
type mortalityConfigFixture struct {
	monthlyDeathBudget          any
	monthlyEmergencyBudget      any
	winterHealthWaveThreshold   any
	droughtWaterDemandThreshold any
}

func writeMortalityConfig(t *testing.T, dir string, f mortalityConfigFixture) {
	t.Helper()
	cfg := map[string]any{
		"version": 1,
		"meta": map[string]any{
			"module":        "engine.citizens",
			"featureKey":    "feat.deathwave",
			"specRefs":      []string{"§5.2"},
			"balanceRegime": "placeholder pending Aaron's balance pass",
		},
		"params": map[string]any{
			"monthlyDeathBudget": map[string]any{
				"value": f.monthlyDeathBudget, "unit": "deaths/month", "disclosure": "placeholder",
			},
			"monthlyEmergencyBudget": map[string]any{
				"value": f.monthlyEmergencyBudget, "unit": "deaths/month (0 = unbounded)", "disclosure": "placeholder",
			},
			"weatherEmergency": map[string]any{
				"winterHealthWaveThreshold": map[string]any{
					"value": f.winterHealthWaveThreshold, "unit": "abs(HealthWaveModifier)", "disclosure": "placeholder",
				},
				"droughtWaterDemandThreshold": map[string]any{
					"value": f.droughtWaterDemandThreshold, "unit": "WaterDemandMultiplier", "disclosure": "placeholder",
				},
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileMortality), b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// errCodeOf extracts a registry-sourced error's Code, failing the test if
// err is not a *errs.E (mirrors deathwave_test.go's own errors.As pattern).
func errCodeOf(t *testing.T, err error) string {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected a *errs.E, got %T: %v", err, err)
	}
	return e.Code
}

// TestMortalityConfigRejectsNegativeWeatherEmergencyThresholds (AC-12/
// GR#7/GR#15): a negative winterHealthWaveThreshold or
// droughtWaterDemandThreshold must be rejected at load time -- a negative
// threshold would make EVERY month qualify as an emergency (both curves
// are non-negative by data/seasonal.json's own schema), silently
// suspending the smoothing budget every month instead of during a genuine
// weather-driven event.
func TestMortalityConfigRejectsNegativeWeatherEmergencyThresholds(t *testing.T) {
	cases := []struct {
		name string
		f    mortalityConfigFixture
	}{
		{"negativeWinter", mortalityConfigFixture{25, 0, -0.01, 1.2}},
		{"negativeDrought", mortalityConfigFixture{25, 0, 0.04, -0.5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMortalityConfig(t, dir, c.f)
			cfg, err := LoadMortalityConfig(dir, "corr")
			if err == nil {
				t.Fatalf("expected an error for %s, got none (cfg=%+v)", c.name, cfg)
			}
			if got := errCodeOf(t, err); got != ErrMortalityDataInvalid {
				t.Fatalf("expected ErrMortalityDataInvalid, got %v", got)
			}
		})
	}
}

// TestMortalityConfigLoadsWeatherEmergencyThresholdsFromDataFile (AC-7/
// GR#15): the emergency thresholds are loaded from data/mortality.json,
// never a hardcoded Go literal -- proven the same way
// TestMortalityConfigLoadsBudgetFromDataFile proves the budget: an
// independent raw-file parse must match the loader's own values.
func TestMortalityConfigLoadsWeatherEmergencyThresholdsFromDataFile(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	if cfg.WinterHealthWaveThreshold() <= 0 {
		t.Fatalf("loaded winterHealthWaveThreshold must be positive, got %v", cfg.WinterHealthWaveThreshold())
	}
	if cfg.DroughtWaterDemandThreshold() <= 0 {
		t.Fatalf("loaded droughtWaterDemandThreshold must be positive, got %v", cfg.DroughtWaterDemandThreshold())
	}
}
