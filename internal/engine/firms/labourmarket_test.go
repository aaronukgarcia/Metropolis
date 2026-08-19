package firms

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// roster returns n distinct citizen IDs 1..n (a synthetic Staff roster).
func roster(n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = uint64(i + 1)
	}
	return out
}

// addFirm registers a fixture firm with a controlled stage and staff roster
// directly on the registry (same-package fixture setup; no public growth
// path is needed to exercise the read-only aggregate).
func addFirm(t *testing.T, api *FirmsAPI, id FirmID, stage Stage, staff []uint64) {
	t.Helper()
	api.mu.Lock()
	api.firms[id] = &firmState{firm: Firm{ID: id, Stage: stage, Staff: staff}}
	api.mu.Unlock()
}

// readFirmsJSON returns the committed data/firms.json bytes (the mutation
// fixture source — tests mutate a COPY, never the committed file).
func readFirmsJSON(t *testing.T) []byte {
	t.Helper()
	dir, err := data.ResolveDataDir("firms-labourmarket-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "firms.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return b
}

// configFromBytes writes b to a temp file and loads it through LoadConfig,
// so a mutation test exercises the real data-file → config → aggregate path
// (a hardcoded-band build cannot pass it).
func configFromBytes(t *testing.T, b []byte) config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "firms.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadConfig(path, "firms-labourmarket-test")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// TestTotalVacanciesDataDerivedBands (AC-21): the vacancy headroom is
// computed from the LOADED data/firms.json stage floors, never Go-literal
// 5/25/250 headcounts. A Startup with 1 staff and a Medium with 10 staff
// must yield the headroom the loaded floors imply.
func TestTotalVacanciesDataDerivedBands(t *testing.T) {
	api := newTestAPI(t, 1)
	addFirm(t, api, 1, StageStartup, roster(1))
	addFirm(t, api, 2, StageMedium, roster(10))

	// Expected from the loaded floors (GR#15): Startup ceiling = small floor
	// − 1; Medium ceiling = enterprise floor − 1.
	startupCeiling := api.cfg.Stages[1].MinStaff - 1
	mediumCeiling := api.cfg.Stages[3].MinStaff - 1
	want := (startupCeiling - 1) + (mediumCeiling - 10)

	if got := api.TotalVacancies(); got != want {
		t.Fatalf("TotalVacancies = %d, want %d (startup headroom %d + medium headroom %d)",
			got, want, startupCeiling-1, mediumCeiling-10)
	}
}

// TestTotalVacanciesTracksStageFloorMutation (AC-21 mutation / GR#15): a
// band ceiling raised in a COPY of data/firms.json moves TotalVacancies with
// NO code change — the check a hardcoded Σ(5−len) build fails.
func TestTotalVacanciesTracksStageFloorMutation(t *testing.T) {
	raw := readFirmsJSON(t)

	baselineCfg := configFromBytes(t, raw)
	baseline := newAPIWithConfig(t, baselineCfg, 1)
	addFirm(t, baseline, 1, StageStartup, nil)
	baseVac := baseline.TotalVacancies()
	if baseVac != baselineCfg.Stages[1].MinStaff-1 {
		t.Fatalf("baseline TotalVacancies = %d, want startup ceiling %d", baseVac, baselineCfg.Stages[1].MinStaff-1)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal firms.json: %v", err)
	}
	small := doc["stages"].([]any)[1].(map[string]any)
	small["minStaff"] = float64(7) // raise small floor 6→7 ⇒ startup ceiling 5→6
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal mutated firms.json: %v", err)
	}

	mutatedCfg := configFromBytes(t, mutated)
	mutatedAPI := newAPIWithConfig(t, mutatedCfg, 1)
	addFirm(t, mutatedAPI, 1, StageStartup, nil)
	mutVac := mutatedAPI.TotalVacancies()
	if mutVac != mutatedCfg.Stages[1].MinStaff-1 {
		t.Fatalf("mutated TotalVacancies = %d, want startup ceiling %d", mutVac, mutatedCfg.Stages[1].MinStaff-1)
	}
	if mutVac <= baseVac {
		t.Fatalf("TotalVacancies did not move with the mutated floor: baseline=%d mutated=%d (a hardcoded band build fails here)", baseVac, mutVac)
	}
}

// TestTotalVacanciesEnterpriseCeilingMutation (AC-21 / ICD §12 item 3): the
// Enterprise ceiling (no §45 upper bound) is ALSO data-sourced — mutating
// labourMarket.enterpriseCeiling moves an Enterprise firm's headroom.
func TestTotalVacanciesEnterpriseCeilingMutation(t *testing.T) {
	raw := readFirmsJSON(t)

	baselineCfg := configFromBytes(t, raw)
	baseline := newAPIWithConfig(t, baselineCfg, 1)
	addFirm(t, baseline, 1, StageEnterprise, roster(300))
	baseVac := baseline.TotalVacancies()
	if baseVac != baselineCfg.LabourMarket.EnterpriseCeiling-300 {
		t.Fatalf("baseline TotalVacancies = %d, want enterprise headroom %d", baseVac, baselineCfg.LabourMarket.EnterpriseCeiling-300)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal firms.json: %v", err)
	}
	lm := doc["labourMarket"].(map[string]any)
	lm["enterpriseCeiling"] = float64(600) // 500→600 ⇒ headroom 200→300
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal mutated firms.json: %v", err)
	}

	mutatedCfg := configFromBytes(t, mutated)
	mutatedAPI := newAPIWithConfig(t, mutatedCfg, 1)
	addFirm(t, mutatedAPI, 1, StageEnterprise, roster(300))
	mutVac := mutatedAPI.TotalVacancies()
	if mutVac != mutatedCfg.LabourMarket.EnterpriseCeiling-300 {
		t.Fatalf("mutated TotalVacancies = %d, want enterprise headroom %d", mutVac, mutatedCfg.LabourMarket.EnterpriseCeiling-300)
	}
	if mutVac <= baseVac {
		t.Fatalf("TotalVacancies did not move with the mutated enterprise ceiling: baseline=%d mutated=%d", baseVac, mutVac)
	}
}

// TestLabourMarketWorkforceLiveQuery (AC-22): Workforce is read LIVE from
// CitizensAPI.TotalPopulation — raising the population on the SAME citizens
// API tracks the field (never a value baked in at SetCitizens time).
func TestLabourMarketWorkforceLiveQuery(t *testing.T) {
	api := newTestAPI(t, 1)
	c := seedCitizens(t, 100) // IDs 1..100
	if err := api.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	lm, err := api.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket: %v", err)
	}
	if lm.Workforce != 100 {
		t.Fatalf("Workforce = %d, want 100", lm.Workforce)
	}

	// Raise the population on the SAME citizens API — a live read must see it.
	more := make([]citizens.ColdRecord, 0, 50)
	for i := 101; i <= 150; i++ {
		more = append(more, citizenRecord(uint64(i), 0, citizens.SectorTertiary, 0))
	}
	if err := c.SeedColdRecords(more, "firms-test-citizens"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	lm2, err := api.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket (after growth): %v", err)
	}
	if lm2.Workforce != 150 {
		t.Fatalf("Workforce = %d, want 150 (must track the live population)", lm2.Workforce)
	}
}

// TestVacancyRatePerMilleIntegerArithmetic (AC-23): the ratio is exact
// integer arithmetic with a division-by-zero guard — never NaN/Inf, never
// fractional.
func TestVacancyRatePerMilleIntegerArithmetic(t *testing.T) {
	cases := []struct {
		vacancies, workforce int64
		want                 int64
	}{
		{10, 100, 100},     // 10*1000/100
		{5, 2, 2500},       // vacancies exceed workforce ⇒ above 1000‰, no clamp
		{1, 3, 333},        // truncation toward zero
		{0, 5, 0},          // no vacancies
		{7, 0, 0},          // division-by-zero guard
		{7, -1, 0},         // defensive: negative workforce also guarded
		{2500, 1000, 2500}, // 2500‰
	}
	for _, c := range cases {
		if got := vacancyRatePerMille(c.vacancies, c.workforce); got != c.want {
			t.Errorf("vacancyRatePerMille(%d,%d) = %d, want %d", c.vacancies, c.workforce, got, c.want)
		}
	}
}

// TestVacancyRateDirectionality (AC-23): adding a firm with vacancies
// (workforce held) strictly raises the rate; raising workforce (vacancies
// held) strictly lowers it.
func TestVacancyRateDirectionality(t *testing.T) {
	api := newTestAPI(t, 1)
	c := seedCitizens(t, 100)
	if err := api.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	addFirm(t, api, 1, StageStartup, nil) // 5 vacancies
	before, err := api.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket: %v", err)
	}

	// More vacancies, workforce held: rate must strictly rise.
	addFirm(t, api, 2, StageStartup, nil) // +5 vacancies ⇒ 10
	moreVacancies, err := api.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket: %v", err)
	}
	if moreVacancies.VacancyRatePerMille <= before.VacancyRatePerMille {
		t.Fatalf("adding vacancies did not raise the rate: %d → %d",
			before.VacancyRatePerMille, moreVacancies.VacancyRatePerMille)
	}

	// Workforce raised (vacancies held at 10): rate must strictly fall.
	more := make([]citizens.ColdRecord, 0, 100)
	for i := 101; i <= 200; i++ {
		more = append(more, citizenRecord(uint64(i), 0, citizens.SectorTertiary, 0))
	}
	if err := c.SeedColdRecords(more, "firms-test-citizens"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	moreWorkforce, err := api.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket: %v", err)
	}
	if moreWorkforce.VacancyRatePerMille >= moreVacancies.VacancyRatePerMille {
		t.Fatalf("raising workforce did not lower the rate: %d → %d",
			moreVacancies.VacancyRatePerMille, moreWorkforce.VacancyRatePerMille)
	}
}

// TestLabourMarketDivisionByZeroGuard (AC-23): citizens WIRED but empty
// (Workforce == 0) yields a 0 rate with NO error — the dependency is
// present, so this is not the AC-24 unwired case.
func TestLabourMarketDivisionByZeroGuard(t *testing.T) {
	api := newTestAPI(t, 1)
	if err := api.SetCitizens(seedCitizens(t, 0)); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	addFirm(t, api, 1, StageStartup, nil) // 5 vacancies

	lm, err := api.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket with wired-but-empty citizens must not error: %v", err)
	}
	if lm.Workforce != 0 {
		t.Fatalf("Workforce = %d, want 0", lm.Workforce)
	}
	if lm.TotalVacancies != 5 {
		t.Fatalf("TotalVacancies = %d, want 5", lm.TotalVacancies)
	}
	if lm.VacancyRatePerMille != 0 {
		t.Fatalf("VacancyRatePerMille = %d, want 0 (no NaN/Inf when workforce==0)", lm.VacancyRatePerMille)
	}
}

// TestLabourMarketUnwiredDependencyFailsClosed (AC-24/GR#17): LabourMarket
// before SetCitizens returns ErrDependencyMissing (MET-G1409), never a zero
// Workforce that silently reads "no jobs".
func TestLabourMarketUnwiredDependencyFailsClosed(t *testing.T) {
	api := newTestAPI(t, 1) // citizens NOT wired

	lm, err := api.LabourMarket()
	if !hasCode(err, ErrDependencyMissing) {
		t.Fatalf("LabourMarket() err = %v, want ErrDependencyMissing (MET-G1409)", err)
	}
	if lm != (LabourMarket{}) {
		t.Fatalf("LabourMarket() before wiring must not return a usable value: %+v", lm)
	}
}

// TestAggregateDeterminism (AC-25): identical construction yields
// byte-identical TotalVacancies and LabourMarket — firms inserted in
// NON-ascending order to prove sorted iteration, not map order.
func TestAggregateDeterminism(t *testing.T) {
	build := func(seed uint64) *FirmsAPI {
		api := newTestAPI(t, seed)
		if err := api.SetCitizens(seedCitizens(t, 50)); err != nil {
			t.Fatalf("SetCitizens: %v", err)
		}
		addFirm(t, api, 30, StageStartup, roster(2))
		addFirm(t, api, 10, StageMedium, roster(5))
		addFirm(t, api, 20, StageEnterprise, roster(300))
		return api
	}

	a := build(7)
	b := build(7)
	if av, bv := a.TotalVacancies(), b.TotalVacancies(); av != bv {
		t.Fatalf("TotalVacancies divergence: %d vs %d", av, bv)
	}
	la, errA := a.LabourMarket()
	lb, errB := b.LabourMarket()
	if errA != nil || errB != nil {
		t.Fatalf("LabourMarket errors: %v / %v", errA, errB)
	}
	if la != lb {
		t.Fatalf("LabourMarket divergence: %+v vs %+v", la, lb)
	}
}

// TestAggregateConcurrentReadsDeterministic (AC-25/-race): concurrent reads
// from multiple workers return byte-identical results and are data-race-free.
func TestAggregateConcurrentReadsDeterministic(t *testing.T) {
	api := newTestAPI(t, 9)
	if err := api.SetCitizens(seedCitizens(t, 50)); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	addFirm(t, api, 30, StageStartup, roster(2))
	addFirm(t, api, 10, StageMedium, roster(5))
	addFirm(t, api, 20, StageEnterprise, roster(300))

	const workers = 16
	var wg sync.WaitGroup
	results := make([]LabourMarket, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lm, err := api.LabourMarket()
			if err != nil {
				t.Errorf("worker %d LabourMarket: %v", i, err)
				return
			}
			results[i] = lm
		}(i)
	}
	wg.Wait()
	for i := 1; i < workers; i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent LabourMarket divergence: %+v vs %+v", results[i], results[0])
		}
	}
}

// TestAggregateCopyGuard (SEC-020-class): the read-only aggregate methods
// reject a struct-copied *FirmsAPI.
func TestAggregateCopyGuard(t *testing.T) {
	api := newTestAPI(t, 1)
	copied := firmsCopy(api)
	if got := copied.TotalVacancies(); got != 0 {
		t.Fatalf("copied.TotalVacancies() = %d, want 0 (copy-guard)", got)
	}
	if _, err := copied.LabourMarket(); !hasCode(err, ErrCopiedValue) {
		t.Fatalf("copied.LabourMarket() = %v, want ErrCopiedValue", err)
	}
}

// TestTotalVacanciesHugeStaffClampsNoOverflow (attack pass): a roster far
// past its band ceiling must contribute ZERO headroom, never a negative
// (wrapped) vacancy.
func TestTotalVacanciesHugeStaffClampsNoOverflow(t *testing.T) {
	api := newTestAPI(t, 1)
	addFirm(t, api, 1, StageStartup, roster(100000))    // ceiling 5
	addFirm(t, api, 2, StageEnterprise, roster(100000)) // ceiling 500
	if got := api.TotalVacancies(); got != 0 {
		t.Fatalf("TotalVacancies = %d, want 0 (over-ceiling rosters clamp to zero headroom)", got)
	}
}

// TestVacancyRatePerMilleNumeratorOverflowSaturates (attack pass): the
// ×1000 numerator saturates rather than wrapping negative at int64 extremes.
func TestVacancyRatePerMilleNumeratorOverflowSaturates(t *testing.T) {
	if got := vacancyRatePerMille(math.MaxInt64, 1); got < 0 {
		t.Fatalf("vacancyRatePerMille(MaxInt64,1) = %d wrapped negative", got)
	}
	if got := vacancyRatePerMille(math.MaxInt64, 0); got != 0 {
		t.Fatalf("vacancyRatePerMille(MaxInt64,0) = %d, want 0 (division-by-zero guard wins)", got)
	}
}
