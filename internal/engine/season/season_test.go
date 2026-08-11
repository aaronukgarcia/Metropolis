package season

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// realAPI loads a *SeasonAPI against the repository's own
// data/seasonal.json (via ResolveDataDir), for tests that check the
// actual spec-transcribed figures (AC-2/AC-3/AC-4) rather than a
// synthetic fixture.
func realAPI(t *testing.T) *SeasonAPI {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load real data/seasonal.json: %v", err)
	}
	return api
}

// --- AC-1: purity -----------------------------------------------------

func TestPureFunctionOfMonthIndex(t *testing.T) {
	api := realAPI(t)

	for _, month := range []int64{0, 5, 8, 11, 25, 240} {
		v1, err1 := api.PowerDemandMultiplier(month)
		v2, err2 := api.PowerDemandMultiplier(month)
		if err1 != nil || err2 != nil {
			t.Fatalf("month %d: unexpected errors %v / %v", month, err1, err2)
		}
		if v1 != v2 {
			t.Errorf("month %d: PowerDemandMultiplier not pure: %v != %v", month, v1, v2)
		}
	}

	// Every other query method is checked the same way, generically.
	type fn func(int64) (any, error)
	wrap := func(name string, f func(int64) (float64, error)) fn {
		return func(m int64) (any, error) { v, err := f(m); return v, err }
	}
	fns := map[string]fn{
		"Water":        wrap("Water", api.WaterDemandMultiplier),
		"Gas":          wrap("Gas", api.GasDemandMultiplier),
		"Harvest":      wrap("Harvest", api.HarvestCalendar),
		"Construction": wrap("Construction", api.ConstructionSpeedMultiplier),
		"HealthWave":   wrap("HealthWave", api.HealthWaveModifier),
	}
	for name, f := range fns {
		a, errA := f(6)
		b, errB := f(6)
		if errA != nil || errB != nil {
			t.Fatalf("%s: unexpected errors %v / %v", name, errA, errB)
		}
		if a != b {
			t.Errorf("%s not pure: %v != %v", name, a, b)
		}
	}

	g1, err := api.IsSchoolIntakeMonth(8)
	g2, err2 := api.IsSchoolIntakeMonth(8)
	if err != nil || err2 != nil {
		t.Fatalf("IsSchoolIntakeMonth: unexpected errors %v / %v", err, err2)
	}
	if g1 != g2 {
		t.Errorf("IsSchoolIntakeMonth not pure: %v != %v", g1, g2)
	}

	l1, err := api.LeisureMix(6)
	l2, err2 := api.LeisureMix(6)
	if err != nil || err2 != nil {
		t.Fatalf("LeisureMix: unexpected errors %v / %v", err, err2)
	}
	if l1 != l2 {
		t.Errorf("LeisureMix not pure: %v != %v", l1, l2)
	}
}

// --- AC-2: power (§17.1 winter +15%) -----------------------------------

func TestPowerSeasonalWinterPeak(t *testing.T) {
	api := realAPI(t)

	// January (monthIndex 0) is a documented winter-peak month.
	winter, err := api.PowerDemandMultiplier(0)
	if err != nil {
		t.Fatalf("PowerDemandMultiplier(0): %v", err)
	}
	// June (monthIndex 5) is a documented non-winter baseline month.
	summer, err := api.PowerDemandMultiplier(5)
	if err != nil {
		t.Fatalf("PowerDemandMultiplier(5): %v", err)
	}
	if winter < 1.15*summer {
		t.Errorf("winter power multiplier %v is not >= 1.15x the baseline %v (§17.1)", winter, summer)
	}
}

// --- AC-3: water (§17.1 summer +25%) -----------------------------------

func TestWaterSeasonalSummerPeak(t *testing.T) {
	api := realAPI(t)

	// July (monthIndex 6) is a documented summer-peak month.
	summer, err := api.WaterDemandMultiplier(6)
	if err != nil {
		t.Fatalf("WaterDemandMultiplier(6): %v", err)
	}
	baseline := 1.0
	if summer < 1.25*baseline {
		t.Errorf("summer water multiplier %v is not >= 1.25x baseline (§17.1)", summer)
	}
}

// --- AC-4: gas (§17.1 x2.2 Jan, x0.2 Jul) --------------------------------

func TestGasSeasonalAnchorMonths(t *testing.T) {
	api := realAPI(t)

	jan, err := api.GasDemandMultiplier(0)
	if err != nil {
		t.Fatalf("GasDemandMultiplier(0): %v", err)
	}
	if jan != 2.2 {
		t.Errorf("January gas multiplier = %v, want 2.2 (§17.1)", jan)
	}

	jul, err := api.GasDemandMultiplier(6)
	if err != nil {
		t.Fatalf("GasDemandMultiplier(6): %v", err)
	}
	if jul != 0.2 {
		t.Errorf("July gas multiplier = %v, want 0.2 (§17.1)", jul)
	}
}

// --- AC-5: harvest calendar (§9, a real lump) ---------------------------

func TestHarvestCalendarHasALump(t *testing.T) {
	api := realAPI(t)

	var sum float64
	values := make([]float64, 12)
	for m := int64(0); m < 12; m++ {
		v, err := api.HarvestCalendar(m)
		if err != nil {
			t.Fatalf("HarvestCalendar(%d): %v", m, err)
		}
		values[m] = v
		sum += v
	}
	avg := sum / 12

	foundLump := false
	for _, v := range values {
		if v > avg*1.5 {
			foundLump = true
			break
		}
	}
	if !foundLump {
		t.Errorf("no month materially exceeds the yearly average %v (values=%v) — curve is flat, not lumped", avg, values)
	}
}

// --- AC-6: construction slowdown (§9, winter < 1.0x) --------------------

func TestConstructionSlowdownInWinter(t *testing.T) {
	api := realAPI(t)

	winter, err := api.ConstructionSpeedMultiplier(0) // January
	if err != nil {
		t.Fatalf("ConstructionSpeedMultiplier(0): %v", err)
	}
	summer, err := api.ConstructionSpeedMultiplier(5) // June
	if err != nil {
		t.Fatalf("ConstructionSpeedMultiplier(5): %v", err)
	}

	if winter >= 1.0 {
		t.Errorf("winter construction multiplier %v is not below 1.0", winter)
	}
	if winter >= summer {
		t.Errorf("winter construction multiplier %v is not below the summer value %v", winter, summer)
	}
}

// --- AC-7: September school intake gate ---------------------------------

func TestSchoolIntakeGateOnlySeptember(t *testing.T) {
	api := realAPI(t)

	trueCount := 0
	sawSeptember := false
	for m := int64(0); m < 12; m++ {
		gate, err := api.IsSchoolIntakeMonth(m)
		if err != nil {
			t.Fatalf("IsSchoolIntakeMonth(%d): %v", m, err)
		}
		if gate {
			trueCount++
			if m == 8 { // 0=Jan .. 8=Sep, per data/seasonal.json's meta convention
				sawSeptember = true
			}
		}
	}
	if trueCount != 1 {
		t.Errorf("expected exactly 1 intake month in a full 12-month cycle, got %d", trueCount)
	}
	if !sawSeptember {
		t.Error("the single intake month is not September (calendar index 8)")
	}
}

// --- AC-8: leisure mix (§9 beach summer / indoor winter) -----------------

func TestLeisureMixSeasonalShift(t *testing.T) {
	api := realAPI(t)

	summer, err := api.LeisureMix(6) // July
	if err != nil {
		t.Fatalf("LeisureMix(6): %v", err)
	}
	winter, err := api.LeisureMix(0) // January
	if err != nil {
		t.Fatalf("LeisureMix(0): %v", err)
	}

	if !(summer.Beach > winter.Beach) {
		t.Errorf("beach weight does not peak in summer: summer=%v winter=%v", summer.Beach, winter.Beach)
	}
	if !(winter.Indoor > summer.Indoor) {
		t.Errorf("indoor weight does not peak in winter: summer=%v winter=%v", summer.Indoor, winter.Indoor)
	}
}

// --- AC-9: health wave (§9/§18, winter more negative) --------------------

func TestHealthWaveWorseInWinter(t *testing.T) {
	api := realAPI(t)

	winter, err := api.HealthWaveModifier(0) // January
	if err != nil {
		t.Fatalf("HealthWaveModifier(0): %v", err)
	}
	summer, err := api.HealthWaveModifier(6) // July
	if err != nil {
		t.Fatalf("HealthWaveModifier(6): %v", err)
	}
	if !(winter < summer) {
		t.Errorf("winter health-wave modifier %v is not lower than the summer value %v", winter, summer)
	}
}

// --- AC-10: no hardcoded seasonal literals ------------------------------
//
// This is enforced by inspection (grep, see the report) rather than a Go
// test, since a Go test cannot assert the absence of a literal in its
// own package's source text without reading that source as data. The
// report documents the grep command and its empty result.

// --- AC-11: future-month queries ----------------------------------------

func TestFutureMonthQueries(t *testing.T) {
	api := realAPI(t)

	future := int64(37) // > 12 months ahead of any "now"
	if _, err := api.PowerDemandMultiplier(future); err != nil {
		t.Errorf("PowerDemandMultiplier(%d): %v", future, err)
	}
	if _, err := api.WaterDemandMultiplier(future); err != nil {
		t.Errorf("WaterDemandMultiplier(%d): %v", future, err)
	}
	if _, err := api.GasDemandMultiplier(future); err != nil {
		t.Errorf("GasDemandMultiplier(%d): %v", future, err)
	}
	if _, err := api.HarvestCalendar(future); err != nil {
		t.Errorf("HarvestCalendar(%d): %v", future, err)
	}
	if _, err := api.ConstructionSpeedMultiplier(future); err != nil {
		t.Errorf("ConstructionSpeedMultiplier(%d): %v", future, err)
	}
	if _, err := api.IsSchoolIntakeMonth(future); err != nil {
		t.Errorf("IsSchoolIntakeMonth(%d): %v", future, err)
	}
	if _, err := api.LeisureMix(future); err != nil {
		t.Errorf("LeisureMix(%d): %v", future, err)
	}
	if _, err := api.HealthWaveModifier(future); err != nil {
		t.Errorf("HealthWaveModifier(%d): %v", future, err)
	}

	// A projection 240 months (20 years) ahead must also work — this
	// package has no notion of "too far ahead".
	if _, err := api.PowerDemandMultiplier(240); err != nil {
		t.Errorf("PowerDemandMultiplier(240): %v", err)
	}
}

// --- AC-12: malformed/incomplete seasonal.json ---------------------------

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func fullValidCurvesJSON() string {
	return `{
		"electricityWinterPeak": {"multipliers": [1.15,1.15,1,1,1,1,1,1,1,1,1,1.15]},
		"waterSummerPeak": {"multipliers": [1,1,1,1,1,1.25,1.25,1.25,1,1,1,1]},
		"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
		"harvestCalendar": {"multipliers": [0.1,0.1,0.1,0.15,0.2,0.3,0.5,1,1,0.8,0.2,0.1]},
		"constructionSpeedMultiplier": {"multipliers": [0.8,0.8,0.9,1,1,1,1,1,1,1,0.95,0.8]},
		"schoolIntakeGate": {"multipliers": [0,0,0,0,0,0,0,0,1,0,0,0]},
		"leisureBeachWeight": {"multipliers": [0.1,0.1,0.15,0.2,0.3,0.6,0.8,0.8,0.5,0.2,0.1,0.1]},
		"leisureIndoorWeight": {"multipliers": [0.8,0.8,0.7,0.6,0.4,0.2,0.15,0.15,0.3,0.6,0.8,0.85]},
		"healthWaveModifier": {"multipliers": [0.05,0.05,0.02,0,0,0,0,0,0,0,0.02,0.05]}
	}`
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, data.FileSeasonal, `{ not valid json`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrSeasonalDataInvalid)
}

func TestLoad_MissingCurve(t *testing.T) {
	dir := t.TempDir()
	// Valid schema, but missing "healthWaveModifier" entirely.
	writeFixture(t, dir, data.FileSeasonal, `{
		"version": 1,
		"curves": {
			"electricityWinterPeak": {"multipliers": [1.15,1.15,1,1,1,1,1,1,1,1,1,1.15]},
			"waterSummerPeak": {"multipliers": [1,1,1,1,1,1.25,1.25,1.25,1,1,1,1]},
			"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
			"harvestCalendar": {"multipliers": [0.1,0.1,0.1,0.15,0.2,0.3,0.5,1,1,0.8,0.2,0.1]},
			"constructionSpeedMultiplier": {"multipliers": [0.8,0.8,0.9,1,1,1,1,1,1,1,0.95,0.8]},
			"schoolIntakeGate": {"multipliers": [0,0,0,0,0,0,0,0,1,0,0,0]},
			"leisureBeachWeight": {"multipliers": [0.1,0.1,0.15,0.2,0.3,0.6,0.8,0.8,0.5,0.2,0.1,0.1]},
			"leisureIndoorWeight": {"multipliers": [0.8,0.8,0.7,0.6,0.4,0.2,0.15,0.15,0.3,0.6,0.8,0.85]}
		}
	}`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMissingCurve)
	if !strings.Contains(err.Error(), "healthWaveModifier") {
		t.Errorf("err = %v, want it to name the missing curve", err)
	}
}

func TestLoad_FewerThan12MonthPoints(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, data.FileSeasonal, `{
		"version": 1,
		"curves": {"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1]}}
	}`)

	_, err := Load(dir, testCorrelationID())
	// foundation.data's own schema validation catches this; engine.season
	// wraps it under its own registry code (AC-12).
	assertCode(t, err, ErrSeasonalDataInvalid)
}

func TestLoad_NegativeMultiplierRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, data.FileSeasonal, `{
		"version": 1,
		"curves": {"constructionSpeedMultiplier": {"multipliers": [-0.1,1,1,1,1,1,1,1,1,1,1,1]}}
	}`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrSeasonalDataInvalid)
}

// curvesJSONWithSchoolIntake returns fullValidCurvesJSON with
// "schoolIntakeGate"'s multipliers swapped for the given literal array
// (e.g. "[0,0,1,0,1,0,0,0,0,0,0,0]" for a two-qualifying-month fixture) —
// BUG-059's repro shape.
func curvesJSONWithSchoolIntake(multipliersLiteral string) string {
	full := fullValidCurvesJSON()
	oldEntry := `"schoolIntakeGate": {"multipliers": [0,0,0,0,0,0,0,0,1,0,0,0]}`
	newEntry := `"schoolIntakeGate": {"multipliers": ` + multipliersLiteral + `}`
	return strings.Replace(full, oldEntry, newEntry, 1)
}

// TestLoad_SchoolIntakeGateTwoQualifyingMonths is BUG-059's first repro
// direction: a hand-authored schoolIntakeGate curve with TWO months at
// or above schoolIntakeGateThreshold must be rejected at Load — the §9/
// US-4 "exactly one intake month per 12-month cycle" contract
// (education's single-fire-per-year stage-transition gate) is a real
// structural invariant, not a balance number, so a data-authoring slip
// here must fail loudly rather than silently double-fire.
func TestLoad_SchoolIntakeGateTwoQualifyingMonths(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, data.FileSeasonal, `{"version": 1, "curves": `+
		curvesJSONWithSchoolIntake("[0,0,0,0,0,0,0,0,1,0,1,0]")+`}`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrIntakeGateShapeInvalid)
}

// TestLoad_SchoolIntakeGateZeroQualifyingMonths is BUG-059's symmetric
// repro direction: a curve where no month reaches the threshold must
// also be rejected — the gate silently never firing (intake never
// happens) is just as much a violation of the "exactly one" contract as
// firing twice.
func TestLoad_SchoolIntakeGateZeroQualifyingMonths(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, data.FileSeasonal, `{"version": 1, "curves": `+
		curvesJSONWithSchoolIntake("[0,0,0,0,0,0,0,0,0,0,0,0]")+`}`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrIntakeGateShapeInvalid)
}

func TestLoad_HappyPathAllCurves(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, data.FileSeasonal, `{"version": 1, "curves": `+fullValidCurvesJSON()+`}`)

	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v, err := api.GasDemandMultiplier(0); err != nil || v != 2.2 {
		t.Errorf("GasDemandMultiplier(0) = %v, %v; want 2.2, nil", v, err)
	}
}

// --- AC-13: out-of-domain month index -------------------------------

func TestNegativeMonthIndexRejected(t *testing.T) {
	api := realAPI(t)

	if _, err := api.PowerDemandMultiplier(-1); err == nil {
		assertCodeFatal(t, "PowerDemandMultiplier(-1) returned nil error, want ErrNegativeMonthIndex")
	} else {
		assertCode(t, err, ErrNegativeMonthIndex)
	}

	if _, err := api.IsSchoolIntakeMonth(-12); err == nil {
		assertCodeFatal(t, "IsSchoolIntakeMonth(-12) returned nil error, want ErrNegativeMonthIndex")
	} else {
		assertCode(t, err, ErrNegativeMonthIndex)
	}

	if _, err := api.LeisureMix(-1); err == nil {
		assertCodeFatal(t, "LeisureMix(-1) returned nil error, want ErrNegativeMonthIndex")
	} else {
		assertCode(t, err, ErrNegativeMonthIndex)
	}
}

func assertCodeFatal(t *testing.T, msg string) {
	t.Helper()
	t.Fatal(msg)
}

func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

// --- AC-14: determinism ---------------------------------------------

func TestDeterministicAcrossRepeatedCalls(t *testing.T) {
	api := realAPI(t)

	var first [12]float64
	for m := int64(0); m < 12; m++ {
		v, err := api.GasDemandMultiplier(m)
		if err != nil {
			t.Fatalf("GasDemandMultiplier(%d): %v", m, err)
		}
		first[m] = v
	}

	for iter := 0; iter < 5; iter++ {
		for m := int64(0); m < 12; m++ {
			v, err := api.GasDemandMultiplier(m)
			if err != nil {
				t.Fatalf("GasDemandMultiplier(%d): %v", m, err)
			}
			if v != first[m] {
				t.Errorf("iteration %d, month %d: got %v, want %v (non-deterministic)", iter, m, v, first[m])
			}
		}
	}
}

// --- AC-16: concurrent reads are safe (go test -race) ------------------

func TestConcurrentQueriesAreRaceFree(t *testing.T) {
	api := realAPI(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(month int64) {
			defer wg.Done()
			if _, err := api.PowerDemandMultiplier(month); err != nil {
				errCh <- err
			}
			if _, err := api.HarvestCalendar(month); err != nil {
				errCh <- err
			}
			if _, err := api.LeisureMix(month); err != nil {
				errCh <- err
			}
		}(int64(i))
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent query error: %v", err)
	}
}
