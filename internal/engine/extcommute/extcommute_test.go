package extcommute

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- test seams (recording stubs; the composition root's real modules are
// consumed through these same interfaces) ---

// workingAgeMonths is the adult boundary (18 years) used to compute the AC-6
// identity's working-age total and the NotInLaborForce adult-never-worked term
// (ICD engine.citizens-offmap.md §12 Open Decision 2).
const workingAgeMonths int64 = 18 * 12

type stubCitizens struct {
	mu         sync.Mutex
	population int
	existing   map[uint64]bool
	states     map[uint64]EmploymentState // per-citizen coarse employment bucket
	ages       map[uint64]int64           // age in months
	applyErr   error                      // optional failure injection for ApplyLifeEventEmployment
}

func newStubCitizens() *stubCitizens {
	return &stubCitizens{
		existing: map[uint64]bool{},
		states:   map[uint64]EmploymentState{},
		ages:     map[uint64]int64{},
	}
}

// add registers an adult unemployed citizen — the default used by the legacy
// tests that only need CitizenExists to return true.
func (s *stubCitizens) add(id uint64) {
	s.addCitizen(id, EmploymentUnemployed, workingAgeMonths)
}

// addCitizen registers a citizen with an explicit coarse employment state and
// age (in months) — the AC-6/AC-7 tests use it to model a synthetic
// working-age population spanning every identity bucket.
func (s *stubCitizens) addCitizen(id uint64, state EmploymentState, ageMonths int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.existing[id] = true
	s.states[id] = state
	s.ages[id] = ageMonths
	s.population++
}

func (s *stubCitizens) TotalPopulation() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.population
}

func (s *stubCitizens) CitizenExists(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.existing[id]
}

// ApplyLifeEventEmployment mirrors the citizens seam's LifeEventEmployment
// write (ICD engine.citizens-offmap.md §4). It records the coarse bucket this
// module drives; the stub does not model the sector (extcommute never sets it).
func (s *stubCitizens) ApplyLifeEventEmployment(id uint64, state EmploymentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applyErr != nil {
		return s.applyErr
	}
	s.states[id] = state
	return nil
}

// workingAgeCount independently counts the working-age residents from the
// stub's own record — the AC-6 identity's "independently-counted working-age
// total", derived from each citizen's state/age, never from the bucket sum.
func (s *stubCitizens) workingAgeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, state := range s.states {
		if bucketFor(state, s.ages[id]) != "child" {
			n++
		}
	}
	return n
}

type stubTraffic struct {
	mu         sync.Mutex
	congestion map[string]float64
	err        error
}

func (s *stubTraffic) Congestion(channel string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	return s.congestion[channel], nil
}

type financeEntry struct {
	citizenID uint64
	poolID    string
	amount    int64
}

type leakEntry struct {
	poolID string
	amount int64
}

type stubFinance struct {
	mu            sync.Mutex
	offMapWage    []financeEntry
	businessRates []financeEntry
	corpShare     []financeEntry
	wageLeakage   []leakEntry
	recordErr     error // optional failure injection for RecordOffMapWage
}

func (s *stubFinance) RecordOffMapWage(citizenID uint64, poolID string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordErr != nil {
		return s.recordErr
	}
	s.offMapWage = append(s.offMapWage, financeEntry{citizenID: citizenID, poolID: poolID, amount: amount})
	return nil
}

// RemoveOffMapWage is the compensating inverse of RecordOffMapWage: it removes
// the most recent matching entry, mirroring the real finance seam's reversal.
func (s *stubFinance) RemoveOffMapWage(citizenID uint64, poolID string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.offMapWage) - 1; i >= 0; i-- {
		e := s.offMapWage[i]
		if e.citizenID == citizenID && e.poolID == poolID && e.amount == amount {
			s.offMapWage = append(s.offMapWage[:i], s.offMapWage[i+1:]...)
			return nil
		}
	}
	return errors.New("stubFinance: no matching off-map wage to remove")
}

func (s *stubFinance) RecordBusinessRates(citizenID uint64, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.businessRates = append(s.businessRates, financeEntry{citizenID: citizenID, amount: amount})
	return nil
}

func (s *stubFinance) RecordCorpShare(citizenID uint64, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.corpShare = append(s.corpShare, financeEntry{citizenID: citizenID, amount: amount})
	return nil
}

func (s *stubFinance) RecordWageLeakage(poolID string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wageLeakage = append(s.wageLeakage, leakEntry{poolID: poolID, amount: amount})
	return nil
}

func (s *stubFinance) offMapWageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.offMapWage)
}

func (s *stubFinance) ratesCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.businessRates)
}

func (s *stubFinance) corpCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.corpShare)
}

func (s *stubFinance) leakageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.wageLeakage)
}

// --- helpers ---

// assertErrCode asserts err carries registry code `code`. data/errors.json is
// owned by the lead and may not yet contain this module's newly-claimed
// codes; until it does, errs.New/Wrap return the documented unregistered-code
// fallback MET-F003 with the requested code preserved in Ctx["code"]. The
// helper accepts both the registered form (errors.Is) and the
// pending-registration form, so the same assertions pass before AND after the
// lead registers the codes — and once registered, the strict errors.Is path
// is what runs.
func assertErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with code %s, got nil", code)
	}
	if errors.Is(err, &errs.E{Code: code}) {
		return
	}
	var e *errs.E
	if errors.As(err, &e) && e.Code == "MET-F003" {
		if got, _ := e.Ctx["code"].(string); got == code {
			t.Logf("code %s not yet registered in data/errors.json (pending lead registration); fallback preserves it", code)
			return
		}
	}
	t.Fatalf("want error code %s, got %T: %v", code, err, err)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const smallExtCommuteJSON = `{"version":1,"transportCapacity":{"motorway":100000,"externalRail":100000}}`

func smallWorldJSON(capacity int) string {
	return fmt.Sprintf(`{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":%d}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]}`, capacity)
}

// newSmallAPI builds a single-pool API over a temp data dir (capacity at era 1
// = capacity), with free-flow traffic, an empty-but-populable citizens stub,
// and a recording finance stub — all wired.
func newSmallAPI(t *testing.T, capacity int) (*ExtCommuteAPI, *stubCitizens, *stubFinance) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "external_world.json", smallWorldJSON(capacity))
	writeFile(t, dir, "extcommute.json", smallExtCommuteJSON)
	api, err := Load(dir, "test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := newStubCitizens()
	tr := &stubTraffic{congestion: map[string]float64{"motorway": 0, "externalRail": 0}}
	f := &stubFinance{}
	if err := api.SetCitizensSeam(c); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrafficSeam(tr); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFinanceSeam(f); err != nil {
		t.Fatal(err)
	}
	return api, c, f
}

// packageGoFiles returns the non-test .go source files of this package
// (optionally excluding doc.go), used by the source-scan AC checks.
func packageGoFiles(t *testing.T, excludeDoc bool) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if excludeDoc && e.Name() == "doc.go" {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	if len(files) == 0 {
		t.Fatal("no package source files found")
	}
	return files
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- AC-1: ExtCommuteAPI surface (pool queries + capacity methods) ---

func TestPoolQueriesAndCapacitySurface(t *testing.T) {
	api, _, _ := newSmallAPI(t, 10)

	ids := api.PoolIDs()
	if len(ids) != 1 || ids[0] != "london" {
		t.Fatalf("PoolIDs: got %v, want [london]", ids)
	}
	pool, err := api.Pool("london")
	if err != nil {
		t.Fatalf("Pool(london): %v", err)
	}
	if pool.Name != "London" || pool.WageMicropounds != 1000000 {
		t.Fatalf("Pool(london): got %+v", pool)
	}
	cap, err := api.Capacity("london", 1)
	if err != nil || cap != 10 {
		t.Fatalf("Capacity(london,1): got %d, %v; want 10, nil", cap, err)
	}
	if got, _ := api.FilledSlots("london"); got != 0 {
		t.Fatalf("FilledSlots(london) on fresh API: got %d, want 0", got)
	}
	if _, err := api.Pool("nope"); err == nil {
		t.Fatal("Pool(unknown) should error")
	} else {
		assertErrCode(t, err, ErrUnknownPool)
	}
}

// --- AC-2: three named pools with era-scaled capacity, loaded from data ---

func TestThreeNamedPoolsLoadedFromData(t *testing.T) {
	api, err := LoadDefault("test-correlation")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	ids := api.PoolIDs()
	if len(ids) != 3 {
		t.Fatalf("want 3 pools, got %d (%v)", len(ids), ids)
	}
	want := []string{"ashford", "dover", "london"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("PoolIDs[%d] = %q, want %q (deterministic sorted order)", i, ids[i], want[i])
		}
	}
	names := map[string]bool{}
	for _, id := range ids {
		p, err := api.Pool(id)
		if err != nil {
			t.Fatalf("Pool(%s): %v", id, err)
		}
		names[p.Name] = true
		if p.Capacity(1) <= 0 {
			t.Fatalf("pool %s era-1 capacity must be positive, got %d", id, p.Capacity(1))
		}
	}
	for _, n := range []string{"London", "Ashford", "Dover"} {
		if !names[n] {
			t.Fatalf("missing pool %q in loaded data", n)
		}
	}
}

// --- AC-3: the exhaustion identity — FilledSlots never exceeds Capacity ---

func TestPoolCapacityExhaustionInvariant(t *testing.T) {
	api, c, _ := newSmallAPI(t, 3)
	const era = 1

	// Fill the pool to capacity and assert the invariant after each command.
	for i := uint64(1); i <= 3; i++ {
		c.add(i)
		if err := api.Assign(AssignCommand{CitizenID: i, PoolID: "london", Era: era, Month: 1}); err != nil {
			t.Fatalf("assign citizen %d: %v", i, err)
		}
		filled, _ := api.FilledSlots("london")
		cap, _ := api.Capacity("london", era)
		if filled < 0 || filled > cap {
			t.Fatalf("invariant broken at step %d: FilledSlots=%d not in [0,%d]", i, filled, cap)
		}
	}

	// The (Capacity+1)th assignment must be rejected, never silently accepted.
	c.add(4)
	err := api.Assign(AssignCommand{CitizenID: 4, PoolID: "london", Era: era, Month: 1})
	if err == nil {
		t.Fatal("(Capacity+1)th assignment was accepted; pool is not genuinely finite")
	}
	assertErrCode(t, err, ErrPoolFull)

	// Invariant still holds after the rejection.
	filled, _ := api.FilledSlots("london")
	if filled != 3 {
		t.Fatalf("FilledSlots after rejection: got %d, want 3 (rejection must not mutate)", filled)
	}
}

// --- AC-4: pool-full rejection is typed and non-corrupting ---

func TestPoolFullRejectsWithoutPhantomJob(t *testing.T) {
	api, c, _ := newSmallAPI(t, 1)
	c.add(1)
	if err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1}); err != nil {
		t.Fatalf("first assign: %v", err)
	}
	c.add(2)
	err := api.Assign(AssignCommand{CitizenID: 2, PoolID: "london", Era: 1, Month: 1})
	if err == nil {
		t.Fatal("second assign into a full pool should fail")
	}
	assertErrCode(t, err, ErrPoolFull)

	// The rejected citizen holds no phantom off-map job.
	if _, ok, err := api.Assignment(2); err != nil || ok {
		t.Fatalf("rejected citizen: got ok=%v err=%v, want not-assigned", ok, err)
	}
	// The pool state is unchanged.
	if filled, _ := api.FilledSlots("london"); filled != 1 {
		t.Fatalf("FilledSlots: got %d, want 1", filled)
	}
}

// --- AC-5: era scaling is non-decreasing and data-sourced ---

func TestEraScalingNonDecreasing(t *testing.T) {
	api, err := LoadDefault("test-correlation")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	for _, id := range api.PoolIDs() {
		prev := -1
		for era := 1; era <= 13; era++ {
			cap, err := api.Capacity(id, era)
			if err != nil {
				t.Fatalf("Capacity(%s,%d): %v", id, era, err)
			}
			if prev >= 0 && cap < prev {
				t.Fatalf("pool %s: Capacity(era=%d)=%d < Capacity(era-1)=%d (non-decreasing violated)", id, era, cap, prev)
			}
			prev = cap
		}
	}
	if _, err := api.Capacity("london", 0); err == nil {
		t.Fatal("Capacity(era=0) should be rejected")
	} else {
		assertErrCode(t, err, ErrInvalidEra)
	}
}

// --- AC-8: the second cap — transport capacity rejects despite free slots ---

func TestTransportCapRejectsDespiteFreePoolSlots(t *testing.T) {
	api, c, _ := newSmallAPI(t, 100) // pool has plenty of slots
	// Gridlock: the motorway leg is saturated.
	api.mu.Lock()
	api.traffic = &stubTraffic{congestion: map[string]float64{"motorway": 1.0}}
	api.mu.Unlock()

	c.add(1)
	err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1})
	if err == nil {
		t.Fatal("assignment into a pool with free slots but a saturated transport leg was accepted")
	}
	assertErrCode(t, err, ErrTransportCapacity)

	// Distinct from the pool-full reason: the pool is nowhere near full.
	if filled, _ := api.FilledSlots("london"); filled != 0 {
		t.Fatalf("FilledSlots: got %d, want 0", filled)
	}
}

// --- AC-9: in-commuting fills a shortage without creating residents ---

func TestInCommutingDoesNotGainResidents(t *testing.T) {
	api, c, f := newSmallAPI(t, 100)
	c.add(1)
	c.add(2)
	popBefore := c.TotalPopulation()

	res, err := api.InCommute(InCommuteCommand{PoolID: "london", Vacancies: 3})
	if err != nil {
		t.Fatalf("InCommute: %v", err)
	}
	if res.FilledVacancies != 3 {
		t.Fatalf("FilledVacancies: got %d, want 3", res.FilledVacancies)
	}
	if popAfter := c.TotalPopulation(); popAfter != popBefore {
		t.Fatalf("resident population changed: before=%d after=%d (in-commuters must not be residents)", popBefore, popAfter)
	}
	// No citizen assignment was created either (in-commuters are off-map workers).
	if got, _ := api.FilledSlots("london"); got != 0 {
		t.Fatalf("FilledSlots after in-commute: got %d, want 0 (in-commuters are not out-commuters)", got)
	}
	_ = f
}

// --- AC-10: wage leakage is a distinct, visible ledger entry ---

func TestWageLeakageRecordedDistinctly(t *testing.T) {
	api, _, f := newSmallAPI(t, 100)
	if _, err := api.InCommute(InCommuteCommand{PoolID: "london", Vacancies: 4}); err != nil {
		t.Fatalf("InCommute: %v", err)
	}
	if n := f.leakageCount(); n != 1 {
		t.Fatalf("wage-leakage entries: got %d, want 1", n)
	}
	f.mu.Lock()
	leak := f.wageLeakage[0]
	f.mu.Unlock()
	if leak.poolID != "london" || leak.amount != 4*1000000 {
		t.Fatalf("wage-leakage entry: got %+v, want pool=london amount=4000000", leak)
	}
	// In-commuting records ONLY the leakage category — no off-map wage, no
	// rates, no corp share (distinct from local-wage / out-commuter paths).
	if f.offMapWageCount() != 0 || f.ratesCount() != 0 || f.corpCount() != 0 {
		t.Fatalf("in-commuting must record only wageLeakage, got offMapWage=%d rates=%d corp=%d",
			f.offMapWageCount(), f.ratesCount(), f.corpCount())
	}
}

// TestInCommuteWageLeakageSaturatesNotWrap is the GR#16 overflow regression: a
// huge vacancy count times a large wage must saturate at math.MaxInt64, never
// wrap negative (which would turn a leakage figure into a bogus negative
// ledger entry). Uses a near-ceiling wage so Vacancies=2 overflows a raw
// int64 multiply (2 * MaxInt64 wraps to -2).
func TestInCommuteWageLeakageSaturatesNotWrap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "external_world.json", `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":100}],"wageMicropounds":9223372036854775807,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]}`)
	writeFile(t, dir, "extcommute.json", smallExtCommuteJSON)
	api, err := Load(dir, "test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := &stubFinance{}
	if err := api.SetFinanceSeam(f); err != nil {
		t.Fatal(err)
	}

	res, err := api.InCommute(InCommuteCommand{PoolID: "london", Vacancies: 2})
	if err != nil {
		t.Fatalf("InCommute: %v", err)
	}
	if res.WageLeakageMicropounds < 0 {
		t.Fatalf("wage leakage wrapped negative: got %d", res.WageLeakageMicropounds)
	}
	if res.WageLeakageMicropounds != math.MaxInt64 {
		t.Fatalf("wage leakage = %d, want math.MaxInt64 (saturated, not wrapped)", res.WageLeakageMicropounds)
	}
	// The saturated figure is what finance recorded too.
	f.mu.Lock()
	leak := f.wageLeakage[0]
	f.mu.Unlock()
	if leak.amount != math.MaxInt64 {
		t.Fatalf("recorded wage leakage = %d, want math.MaxInt64", leak.amount)
	}
}

// --- AC-11: double-assignment rejected, never overwritten ---

func TestDoubleAssignmentRejectedWithoutOverwrite(t *testing.T) {
	api, c, _ := newSmallAPI(t, 100)
	c.add(1)
	if err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1}); err != nil {
		t.Fatalf("first assign: %v", err)
	}
	err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 2})
	if err == nil {
		t.Fatal("second assignment of the same citizen should fail")
	}
	assertErrCode(t, err, ErrAlreadyOffMap)

	// The first assignment is intact (no overwrite).
	as, ok, err := api.Assignment(1)
	if err != nil || !ok {
		t.Fatalf("Assignment(1): ok=%v err=%v, want intact", ok, err)
	}
	if as.PoolID != "london" || as.SinceMonth != 1 {
		t.Fatalf("first assignment overwritten: got %+v", as)
	}
	if filled, _ := api.FilledSlots("london"); filled != 1 {
		t.Fatalf("FilledSlots: got %d, want 1 (double-assign must not double-count)", filled)
	}
}

// --- AC-12: fiscal thinness — income-tax only, exactly zero rates/corp ---

func TestDormitoryFiscalThinnessZeroRatesCorp(t *testing.T) {
	api, c, f := newSmallAPI(t, 100)
	c.add(1)
	if err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Income-tax-eligible wage base is nonzero.
	if n := f.offMapWageCount(); n != 1 {
		t.Fatalf("off-map wage entries: got %d, want 1", n)
	}
	f.mu.Lock()
	wage := f.offMapWage[0]
	f.mu.Unlock()
	if wage.amount <= 0 {
		t.Fatalf("off-map wage must be nonzero (income-tax base), got %d", wage.amount)
	}
	// Business rates and corporate share are EXACTLY zero (not merely small).
	if f.ratesCount() != 0 {
		t.Fatalf("business-rates entries: got %d, want exactly 0", f.ratesCount())
	}
	if f.corpCount() != 0 {
		t.Fatalf("corporate-share entries: got %d, want exactly 0", f.corpCount())
	}
}

// --- AC-15: malformed data -> registry-sourced error, no silent default ---

func TestMalformedExternalWorldRejectedAtLoad(t *testing.T) {
	cases := []struct {
		name string
		json string
		code string
	}{
		{
			name: "capacity-curve-does-not-cover-era-1",
			json: `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":3,"capacity":10}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]}`,
			code: ErrExternalWorldDataInvalid,
		},
		{
			name: "negative-capacity",
			json: `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":-5}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]}`,
			code: ErrExternalWorldDataInvalid,
		},
		{
			name: "transport-channel-missing-capacity",
			json: `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":10}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"monorail","availableFromTier":1}]}]}`,
			code: ErrExternalWorldDataInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "external_world.json", tc.json)
			writeFile(t, dir, "extcommute.json", smallExtCommuteJSON)
			api, err := Load(dir, "test-correlation")
			if err == nil {
				t.Fatal("malformed external_world.json was accepted (silent default)")
			}
			assertErrCode(t, err, tc.code)
			if api != nil {
				t.Fatal("a failed Load must not return a usable API (no silent default)")
			}
		})
	}
}

func TestMalformedExtCommuteConfigRejectedAtLoad(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "external_world.json", smallWorldJSON(10))
	writeFile(t, dir, "extcommute.json", `{"version":1,"transportCapacity":{"motorway":-5}}`)
	api, err := Load(dir, "test-correlation")
	if err == nil {
		t.Fatal("negative transport capacity was accepted")
	}
	assertErrCode(t, err, ErrExtCommuteDataInvalid)
	if api != nil {
		t.Fatal("a failed Load must not return a usable API")
	}
}

// --- AC-16: determinism (seed + sorted iteration, no map-range) ---

const multiPoolWorldJSON = `{"version":1,"profiles":[
  {"id":"ashford","name":"Ashford","capacityByEra":[{"era":1,"capacity":2}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]},
  {"id":"dover","name":"Dover","capacityByEra":[{"era":1,"capacity":2}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]},
  {"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":2}],"wageMicropounds":1000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}
]}`

func newMultiPoolAPI(t *testing.T) (*ExtCommuteAPI, *stubCitizens) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "external_world.json", multiPoolWorldJSON)
	writeFile(t, dir, "extcommute.json", smallExtCommuteJSON)
	api, err := Load(dir, "test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := newStubCitizens()
	tr := &stubTraffic{congestion: map[string]float64{"motorway": 0, "externalRail": 0}}
	if err := api.SetCitizensSeam(c); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrafficSeam(tr); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFinanceSeam(&stubFinance{}); err != nil {
		t.Fatal(err)
	}
	return api, c
}

func TestSelectPoolDeterministic(t *testing.T) {
	a1, _ := newMultiPoolAPI(t)
	a2, _ := newMultiPoolAPI(t)
	if err := a1.SetSeed(42); err != nil {
		t.Fatal(err)
	}
	if err := a2.SetSeed(42); err != nil {
		t.Fatal(err)
	}

	// Same seed + same inputs -> same selection, across instances and across
	// repeated calls (no map-range nondeterminism, no wall clock).
	for month := int64(1); month <= 20; month++ {
		p1, err := a1.SelectPool(1, month)
		if err != nil {
			t.Fatalf("SelectPool(month=%d): %v", month, err)
		}
		p2, err := a2.SelectPool(1, month)
		if err != nil {
			t.Fatalf("SelectPool(month=%d): %v", month, err)
		}
		if p1 != p2 {
			t.Fatalf("SelectPool diverged across identical instances at month %d: %q vs %q", month, p1, p2)
		}
		// Repeated calls on the same instance are stable.
		p3, err := a1.SelectPool(1, month)
		if err != nil {
			t.Fatalf("SelectPool(month=%d) repeat: %v", month, err)
		}
		if p1 != p3 {
			t.Fatalf("SelectPool not repeatable at month %d: %q vs %q", month, p1, p3)
		}
	}
}

// TestSetSeedConcurrentSelectPoolNoDataRace hammers SetSeed (a locked write
// to a.seed) against SelectPool (which reads the seed for the tie-breaking
// draw). SelectPool must capture the seed under the same RLock as the rest of
// its read-only snapshot — a bare a.seed read after RUnlock would trip the
// race detector here.
func TestSetSeedConcurrentSelectPoolNoDataRace(t *testing.T) {
	api, _ := newMultiPoolAPI(t)

	const workers = 4
	const iters = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if err := api.SetSeed(uint64(w*iters + i)); err != nil {
					t.Errorf("SetSeed: %v", err)
					return
				}
				if _, err := api.SelectPool(1, int64(i)); err != nil {
					t.Errorf("SelectPool: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestSelectPoolNoEligiblePool(t *testing.T) {
	api, c := newMultiPoolAPI(t)
	// Fill every pool to capacity (each has capacity 2).
	id := uint64(1)
	for _, pool := range []string{"ashford", "dover", "london"} {
		for j := 0; j < 2; j++ {
			c.add(id)
			if err := api.Assign(AssignCommand{CitizenID: id, PoolID: pool, Era: 1, Month: 1}); err != nil {
				t.Fatalf("assign %d -> %s: %v", id, pool, err)
			}
			id++
		}
	}
	_, err := api.SelectPool(1, 1)
	if err == nil {
		t.Fatal("SelectPool with every pool full should fail")
	}
	assertErrCode(t, err, ErrNoEligiblePool)
}

func TestPoolIDsDeterministicSortedOrder(t *testing.T) {
	api, err := LoadDefault("test-correlation")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	for i := 0; i < 20; i++ {
		ids := api.PoolIDs()
		if len(ids) != 3 || ids[0] != "ashford" || ids[1] != "dover" || ids[2] != "london" {
			t.Fatalf("PoolIDs order not stable/sorted: %v", ids)
		}
	}
}

// --- AC-18: concurrency — no data race and the capacity invariant holds ---

func TestConcurrentAssignReleaseCapacityInvariant(t *testing.T) {
	const capacity = 20
	const workers = 50
	const perWorker = 10
	api, c, _ := newSmallAPI(t, capacity)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				id := uint64(w*perWorker + j + 1)
				c.add(id)
				_ = api.Assign(AssignCommand{CitizenID: id, PoolID: "london", Era: 1, Month: 1})
			}
		}(w)
	}
	wg.Wait()

	filled, err := api.FilledSlots("london")
	if err != nil {
		t.Fatalf("FilledSlots: %v", err)
	}
	cap, _ := api.Capacity("london", 1)
	if filled < 0 || filled > cap {
		t.Fatalf("capacity invariant violated after concurrent assigns: FilledSlots=%d not in [0,%d]", filled, cap)
	}
	if filled != capacity {
		t.Fatalf("FilledSlots: got %d, want exactly capacity %d (only %d slots exist)", filled, capacity, capacity)
	}
}

// --- AC-13/AC-14/AC-17: negative source-scan checks ---

func TestNoMentalHealthPenaltyLogic(t *testing.T) {
	for _, f := range packageGoFiles(t, true) { // exclude doc.go (explanatory only)
		src := readAll(t, f)
		for _, banned := range []string{"wellbeing", "mentalHealth", "CommuteTime"} {
			if strings.Contains(src, banned) {
				t.Fatalf("%s references %q — this module must not compute a separate mental-health penalty (AC-13)", f, banned)
			}
		}
	}
}

func TestNoInventedTenureCap(t *testing.T) {
	for _, f := range packageGoFiles(t, false) {
		src := readAll(t, f)
		for _, banned := range []string{"maxTenure", "forceLocal", "yearsLimit"} {
			if strings.Contains(src, banned) {
				t.Fatalf("%s references %q — no invented hard cap (AC-14)", f, banned)
			}
		}
	}
}

func TestNoWallClockReads(t *testing.T) {
	for _, f := range packageGoFiles(t, false) {
		src := readAll(t, f)
		for _, banned := range []string{"time.Now", "time.Since"} {
			if strings.Contains(src, banned) {
				t.Fatalf("%s references %q — no wall-clock dependency (AC-17)", f, banned)
			}
		}
	}
}

// --- AC-19: doc.go states the module key, spec refs, pools, two-cap model ---

func TestDocGoStatesRequiredFacts(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	doc := readAll(t, filepath.Join(filepath.Dir(thisFile), "doc.go"))
	for _, want := range []string{"engine.extcommute", "§21", "A6", "London", "Ashford", "Dover", "two-cap", "income tax"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("doc.go missing required fact %q", want)
		}
	}
}

// --- AC-6/AC-7: the dormitory arithmetic identity (un-blocked via the
// citizens seam's ApplyLifeEventEmployment, ICD engine.citizens-offmap.md) ---

// bucketFor classifies a citizen's coarse employment state into one of the
// four AC-6 identity buckets ("locallyEmployed", "offMap", "unemployed",
// "notInLaborForce"), or "child" for a working-age-ineligible resident, or ""
// for a state outside the known domain (the guard the tests use to detect a
// future enum extension the switch was not updated for).
func bucketFor(state EmploymentState, ageMonths int64) string {
	switch state {
	case EmploymentEmployed:
		return "locallyEmployed"
	case EmploymentOffMap:
		return "offMap"
	case EmploymentUnemployed:
		return "unemployed"
	case EmploymentStudent, EmploymentRetired:
		return "notInLaborForce"
	case EmploymentNone:
		if ageMonths >= workingAgeMonths {
			return "notInLaborForce" // adult never worked
		}
		return "child"
	default:
		return ""
	}
}

// workingAgeBuckets sums the four AC-6 buckets from the stub's own record,
// independently of workingAgeCount (each citizen lands in exactly one bucket).
func workingAgeBuckets(c *stubCitizens) (locallyEmployed, offMap, unemployed, notInLaborForce int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, state := range c.states {
		switch bucketFor(state, c.ages[id]) {
		case "locallyEmployed":
			locallyEmployed++
		case "offMap":
			offMap++
		case "unemployed":
			unemployed++
		case "notInLaborForce":
			notInLaborForce++
		case "child":
			// not a working-age resident
		case "":
			// unknown state — workingAgeCount still counts it as working-age,
			// so the bucket-sum vs total check below surfaces the never-zero
			// violation.
		}
	}
	return
}

// assertDormitoryArithmetic asserts the AC-6 identity holds EXACTLY (never an
// epsilon): the independently-counted working-age total equals the sum of the
// four buckets (never two, never zero), and citizens' own EmploymentOffMap
// count cross-checks against this module's Σ_pool FilledSlots(pool) — the
// cross-module consistency check of ICD §10/§11.
func assertDormitoryArithmetic(t *testing.T, api *ExtCommuteAPI, c *stubCitizens, pools []string) {
	t.Helper()

	locallyEmployed, offMap, unemployed, notInLaborForce := workingAgeBuckets(c)
	total := c.workingAgeCount()
	if sum := locallyEmployed + offMap + unemployed + notInLaborForce; sum != total {
		t.Fatalf("AC-6 identity broken: buckets sum %d != working-age total %d (locally=%d offMap=%d unemployed=%d nilf=%d)",
			sum, total, locallyEmployed, offMap, unemployed, notInLaborForce)
	}

	// Cross-module consistency: citizens' EmploymentOffMap count must equal the
	// per-pool FilledSlots summed over every pool (proves Assign/Release wired
	// the citizen's own record, not just this module's map).
	filled := 0
	for _, p := range pools {
		n, err := api.FilledSlots(p)
		if err != nil {
			t.Fatalf("FilledSlots(%s): %v", p, err)
		}
		filled += n
	}
	if offMap != filled {
		t.Fatalf("cross-module off-map check broken: citizens EmploymentOffMap=%d != Σ_pool FilledSlots=%d", offMap, filled)
	}
}

// TestDormitoryArithmeticIdentity is AC-6: across a synthetic working-age
// population the four buckets sum EXACTLY to the independently-counted
// working-age total, with citizens' EmploymentOffMap records cross-checked
// against FilledSlots per pool.
func TestDormitoryArithmeticIdentity(t *testing.T) {
	api, c := newMultiPoolAPI(t)
	pools := api.PoolIDs()

	// A synthetic population spanning every bucket plus one child (who must be
	// excluded from the working-age total).
	c.addCitizen(101, EmploymentEmployed, 30*12)
	c.addCitizen(102, EmploymentEmployed, 40*12)
	c.addCitizen(103, EmploymentUnemployed, 25*12)
	c.addCitizen(104, EmploymentRetired, 70*12)
	c.addCitizen(105, EmploymentStudent, 20*12)
	c.addCitizen(106, EmploymentNone, 22*12) // adult never worked
	c.addCitizen(107, EmploymentNone, 10*12) // child — outside the identity

	// Drive off-map assignments across two pools.
	if err := api.Assign(AssignCommand{CitizenID: 103, PoolID: "london", Era: 1, Month: 1}); err != nil {
		t.Fatalf("assign 103: %v", err)
	}
	if err := api.Assign(AssignCommand{CitizenID: 106, PoolID: "dover", Era: 1, Month: 1}); err != nil {
		t.Fatalf("assign 106: %v", err)
	}

	assertDormitoryArithmetic(t, api, c, pools)
}

// TestDormitoryArithmeticSequence is AC-7: the AC-6 identity holds immediately
// after EVERY transition this module drives — assignment, release (job loss /
// local job found), pool-full rejection, and double-assignment rejection — not
// only in a static snapshot.
func TestDormitoryArithmeticSequence(t *testing.T) {
	api, c := newMultiPoolAPI(t)
	pools := api.PoolIDs()

	c.addCitizen(1, EmploymentEmployed, 30*12)
	c.addCitizen(2, EmploymentUnemployed, 25*12)
	c.addCitizen(3, EmploymentUnemployed, 26*12)
	c.addCitizen(4, EmploymentRetired, 68*12)
	c.addCitizen(5, EmploymentStudent, 20*12)
	c.addCitizen(6, EmploymentNone, 19*12) // adult never worked
	c.addCitizen(7, EmploymentNone, 8*12)  // child — outside the identity

	check := func(step string) {
		t.Helper()
		assertDormitoryArithmetic(t, api, c, pools)
		t.Logf("identity holds after %s", step)
	}

	check("initial population")

	// Assignment transitions.
	if err := api.Assign(AssignCommand{CitizenID: 2, PoolID: "london", Era: 1, Month: 1}); err != nil {
		t.Fatalf("assign 2: %v", err)
	}
	check("assign 2 -> london")

	if err := api.Assign(AssignCommand{CitizenID: 3, PoolID: "dover", Era: 1, Month: 1}); err != nil {
		t.Fatalf("assign 3: %v", err)
	}
	check("assign 3 -> dover")

	// Release (job loss / local job found).
	if err := api.Release(ReleaseCommand{CitizenID: 2, Month: 2}); err != nil {
		t.Fatalf("release 2: %v", err)
	}
	check("release 2")

	// Fill dover to capacity, then reject the next assignment (pool-full).
	c.addCitizen(8, EmploymentUnemployed, 27*12)
	c.addCitizen(9, EmploymentUnemployed, 28*12)
	if err := api.Assign(AssignCommand{CitizenID: 8, PoolID: "dover", Era: 1, Month: 2}); err != nil {
		t.Fatalf("assign 8: %v", err)
	}
	check("assign 8 -> dover")

	err := api.Assign(AssignCommand{CitizenID: 9, PoolID: "dover", Era: 1, Month: 2})
	if err == nil {
		t.Fatal("assign 9 -> dover should hit the pool-full cap")
	}
	assertErrCode(t, err, ErrPoolFull)
	check("pool-full rejection of 9")

	// Double-assignment rejection (never two).
	err = api.Assign(AssignCommand{CitizenID: 3, PoolID: "london", Era: 1, Month: 3})
	if err == nil {
		t.Fatal("re-assigning citizen 3 should be rejected")
	}
	assertErrCode(t, err, ErrAlreadyOffMap)
	check("double-assign rejection of 3")
}

// TestAssignSeamWriteFailureNonCorrupting: if the citizens seam's write fails,
// Assign must leave BOTH this module's map and the citizen's own record
// unchanged (AC-4's non-corrupting guarantee extends to the seam-write path).
func TestAssignSeamWriteFailureNonCorrupting(t *testing.T) {
	api, c, _ := newSmallAPI(t, 10)
	c.add(1)
	c.mu.Lock()
	c.applyErr = errors.New("citizens seam write failed")
	c.mu.Unlock()

	err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1})
	if err == nil {
		t.Fatal("Assign with a failing citizens seam should fail closed")
	}
	assertErrCode(t, err, ErrDependencyNotWired)

	if _, ok, err := api.Assignment(1); err != nil || ok {
		t.Fatalf("Assignment(1): ok=%v err=%v, want not-assigned after seam failure", ok, err)
	}
	if filled, _ := api.FilledSlots("london"); filled != 0 {
		t.Fatalf("FilledSlots after failed Assign: got %d, want 0", filled)
	}
	c.mu.Lock()
	state := c.states[1]
	c.mu.Unlock()
	if state != EmploymentUnemployed {
		t.Fatalf("citizen state after failed Assign: got %v, want EmploymentUnemployed (unchanged)", state)
	}
}

// TestAssignCitizensFailureLeavesFinanceUntouched is the three-store atomicity
// regression (M-H): the pre-fix Assign posted the finance wage BEFORE flipping
// the citizens seam, so a citizens-side failure orphaned a phantom wage record
// in finance. The fix compensates the wage back, so on a citizens-side failure
// NONE of the three stores (finance, citizens, assignments) is left changed.
func TestAssignCitizensFailureLeavesFinanceUntouched(t *testing.T) {
	api, c, f := newSmallAPI(t, 10)
	c.add(1)
	c.mu.Lock()
	c.applyErr = errors.New("citizens seam write failed")
	c.mu.Unlock()

	err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1})
	if err == nil {
		t.Fatal("Assign with a failing citizens seam should fail closed")
	}
	assertErrCode(t, err, ErrDependencyNotWired)

	// assignments store untouched.
	if _, ok, err := api.Assignment(1); err != nil || ok {
		t.Fatalf("Assignment(1): ok=%v err=%v, want not-assigned after seam failure", ok, err)
	}
	if filled, _ := api.FilledSlots("london"); filled != 0 {
		t.Fatalf("FilledSlots after failed Assign: got %d, want 0", filled)
	}
	// citizens store untouched.
	c.mu.Lock()
	state := c.states[1]
	c.mu.Unlock()
	if state != EmploymentUnemployed {
		t.Fatalf("citizen state after failed Assign: got %v, want EmploymentUnemployed (unchanged)", state)
	}
	// finance store untouched — the store the pre-fix code orphaned. The wage
	// was posted, then compensated back, so the net off-map wage record is zero.
	if n := f.offMapWageCount(); n != 0 {
		t.Fatalf("finance off-map wage entries after citizens-side failure: got %d, want 0 (wage must be compensated back)", n)
	}
}

// TestAssignFinanceFailureLeavesNothingChanged is the other failure order of
// the same three-store atomicity invariant: when the finance post itself fails,
// Assign must not flip the citizens seam or commit the assignments map.
func TestAssignFinanceFailureLeavesNothingChanged(t *testing.T) {
	api, c, f := newSmallAPI(t, 10)
	c.add(1)
	f.mu.Lock()
	f.recordErr = errors.New("finance seam write failed")
	f.mu.Unlock()

	err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1})
	if err == nil {
		t.Fatal("Assign with a failing finance seam should fail closed")
	}
	assertErrCode(t, err, ErrDependencyNotWired)

	if _, ok, err := api.Assignment(1); err != nil || ok {
		t.Fatalf("Assignment(1): ok=%v err=%v, want not-assigned after finance failure", ok, err)
	}
	if filled, _ := api.FilledSlots("london"); filled != 0 {
		t.Fatalf("FilledSlots after finance failure: got %d, want 0", filled)
	}
	c.mu.Lock()
	state := c.states[1]
	c.mu.Unlock()
	if state != EmploymentUnemployed {
		t.Fatalf("citizen state after finance failure: got %v, want EmploymentUnemployed (unchanged)", state)
	}
	if n := f.offMapWageCount(); n != 0 {
		t.Fatalf("finance off-map wage entries after finance failure: got %d, want 0", n)
	}
}

// TestReleaseSeamWriteFailureNonCorrupting: a failing citizens seam write on
// Release must leave the assignment intact (no half-release).
func TestReleaseSeamWriteFailureNonCorrupting(t *testing.T) {
	api, c, _ := newSmallAPI(t, 10)
	c.add(1)
	if err := api.Assign(AssignCommand{CitizenID: 1, PoolID: "london", Era: 1, Month: 1}); err != nil {
		t.Fatalf("assign 1: %v", err)
	}
	c.mu.Lock()
	c.applyErr = errors.New("citizens seam write failed")
	c.mu.Unlock()

	err := api.Release(ReleaseCommand{CitizenID: 1, Month: 2})
	if err == nil {
		t.Fatal("Release with a failing citizens seam should fail closed")
	}
	assertErrCode(t, err, ErrDependencyNotWired)

	as, ok, err := api.Assignment(1)
	if err != nil || !ok || as.PoolID != "london" {
		t.Fatalf("Assignment(1) after failed Release: %+v ok=%v err=%v, want intact", as, ok, err)
	}
	c.mu.Lock()
	state := c.states[1]
	c.mu.Unlock()
	if state != EmploymentOffMap {
		t.Fatalf("citizen state after failed Release: got %v, want EmploymentOffMap (unchanged)", state)
	}
}

// TestOffMapAccountingConsistent asserts the expressible half of the AC-6/AC-7
// identity: this module's own off-map accounting never double-counts a citizen
// across pools and FilledSlots always equals the per-pool assignment count.
func TestOffMapAccountingConsistent(t *testing.T) {
	api, c, _ := newSmallAPI(t, 100)
	for i := uint64(1); i <= 5; i++ {
		c.add(i)
		if err := api.Assign(AssignCommand{CitizenID: i, PoolID: "london", Era: 1, Month: int64(i)}); err != nil {
			t.Fatalf("assign %d: %v", i, err)
		}
	}
	filled, _ := api.FilledSlots("london")
	if filled != 5 {
		t.Fatalf("FilledSlots: got %d, want 5", filled)
	}
	// Every assignment is a distinct citizen (no cross-pool double-count).
	seen := map[uint64]string{}
	for i := uint64(1); i <= 5; i++ {
		as, ok, err := api.Assignment(i)
		if err != nil || !ok {
			t.Fatalf("Assignment(%d): ok=%v err=%v, want present", i, ok, err)
		}
		if prev, dup := seen[i]; dup {
			t.Fatalf("citizen %d double-counted at %s and %s", i, prev, as.PoolID)
		}
		seen[i] = as.PoolID
	}

	// Release transitions keep the accounting consistent (AC-7's expressible
	// half).
	if err := api.Release(ReleaseCommand{CitizenID: 3, Month: 9}); err != nil {
		t.Fatalf("Release(3): %v", err)
	}
	if filled, _ := api.FilledSlots("london"); filled != 4 {
		t.Fatalf("FilledSlots after release: got %d, want 4", filled)
	}
	if err := api.Release(ReleaseCommand{CitizenID: 3, Month: 9}); err == nil {
		t.Fatal("releasing a non-assigned citizen should be rejected")
	} else {
		assertErrCode(t, err, ErrNotOffMapAssigned)
	}
}
