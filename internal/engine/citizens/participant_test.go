package citizens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc9 — engine.citizens save.Participant tests. The five
// mandatory shapes plus a streaming-laziness assertion (the 100M-citizen
// contract this inc exists to satisfy):
//   1. field-parity drift (ColdRecord / Household / ColdPassParams wires, and
//      the whole-CitizensAPI classification);
//   2. round-trip + continue + prove-can-fail (every ColdRecord field, meta
//      scalar, personality axis and satisfaction component individually);
//   3. byte determinism (many citizens across shards + households + fidelity);
//   4. load-into-non-empty full-replace;
//   5. copyguard + unknown-kind;
//   6. STREAMING — Source does not materialise all 256 shards before the
//      first yield (coldStream is lazy).

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Distinct-value test data — every field distinct and non-zero, including
// each of the 8 personality axes and each of the 5 satisfaction components
// element-wise (the recurring gap the destructive rounds keep catching).
// ---------------------------------------------------------------------------

// distinctRecord builds a valid ColdRecord whose fields are all distinct and
// (bar the deliberately-varied enums) non-zero, so a dropped or aliased field
// is visible in a round-trip comparison. Passes ValidateColdRecord.
func distinctRecord(id uint64) ColdRecord {
	var p [NumPersonalityAxes]int8
	for i := range p {
		p[i] = int8(1 + (int(id)*3+i*11)%100) // distinct per axis, 1..100
	}
	return ColdRecord{
		ID:         id,
		BirthMonth: int64(1 + id%1000),
		Sex:        Sex(id % 2),
		// Household/Partner deliberately exceed uint32's range (births-unblock
		// lane, 2026-09-02): these columns were widened uint32->uint64 to fix
		// the safeUint32 truncation that saturated every migrant/fertility-
		// child partner id to math.MaxUint32, so the round-trip test data must
		// actually exercise the full width, not just re-confirm a narrower
		// range that would pass even with the old, buggy uint32 columns.
		Household:       uint64(1)<<40 + id,
		Partner:         uint64(1)<<41 + id,
		ChildCount:      uint8(1 + id%4),
		Home:            CellRef(1 + id%800000),
		District:        uint16(1 + id%50),
		Workplace:       uint32(300 + id%400),
		School:          uint32(100 + id%150),
		Personality:     p,
		Attainment:      int16(1 + id%90),
		Stage:           Stage(1 + id%7), // 1..7 (<= StageAdultEd)
		Schooling:       int16(1 + id%180),
		HealthBand:      HealthBand(1 + id%5), // 1..5 (<= MaxHealthBand)
		Access:          uint8(1 + id%100),
		Wealth:          int64(1000 + id*13),
		EmploymentState: EmploymentState(id % 6), // 0..5 (incl. OffMap)
		Sector:          Sector(1 + id%4),        // 1..4
		SatHousing:      int32(1 + (id*1)%100),
		SatServices:     int32(1 + (id*3)%100),
		SatEnvironment:  int32(1 + (id*7)%100),
		SatLeisureFit:   int32(1 + (id*11)%100),
		SatCommute:      int32(1 + (id*13)%100),
	}
}

// buildPopulation constructs a rich CitizensAPI: a batch of distinct cold
// citizens spanning many shards, a few real households (via partnering),
// several elevated citizens at DISTINCT fidelity tiers (HOT and WARM), and a
// partially-advanced clock (so month/dayTick/monthParams/monthlyUpdates and
// the vital-event accumulators are all non-trivial in the saved state).
func buildPopulation(t *testing.T, seed uint64, cid string) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(seed, cid)
	must(t, err)

	recs := make([]ColdRecord, 0, 300)
	for id := uint64(1); id <= 300; id++ {
		recs = append(recs, distinctRecord(id))
	}
	must(t, api.SeedColdRecords(recs, cid))

	// Real households via partnering (creates households map entries + rewires
	// cold household/partner columns).
	for pair := 0; pair < 6; pair++ {
		a := uint64(1 + pair*2)
		b := uint64(2 + pair*2)
		must(t, api.ApplyLifeEventCommand(LifeEventCommand{
			CorrelationID: cid, Kind: LifeEventPartner, CitizenID: a, PartnerID: b,
		}))
	}

	// Elevate to DISTINCT fidelity tiers: some HOT, some WARM.
	for id := uint64(20); id <= 26; id++ {
		target := FidelityHot
		if id%2 == 0 {
			target = FidelityWarm
		}
		must(t, api.ApplyFidelityCommand(FidelityCommand{CorrelationID: cid, CitizenID: id, Target: target}))
	}

	// Advance a few day-ticks so monthlyUpdates>0 for the scheduled shards and
	// the clock/params/accumulators are set.
	for d := 0; d < 5; d++ {
		_, _, err := api.AdvanceDayTick(cid)
		must(t, err)
	}

	// FEAT-087 AC-20: put genuine death-queue state into the fixture -- two
	// PENDING (selected-but-unrealised) entries and one already-REALISED
	// entry, spanning two distinct selection months, so every round-trip test
	// below (round-trip, prove-can-fail, byte determinism) actually exercises
	// the new "citizens.deathqueue" record instead of trivially round-tripping
	// an empty queue. IDs are drawn from the 300 already-seeded citizens (the
	// death queue itself has no notion of "does this citizen exist" -- it is
	// pure bookkeeping keyed by citizenID, see deathwave.go's own doc).
	must(t, api.deathQueue.Enqueue(101, api.month, cid))
	must(t, api.deathQueue.Enqueue(102, api.month, cid))
	must(t, api.deathQueue.Enqueue(103, api.month+1, cid))
	must(t, api.deathQueue.RealiseByID(101, api.month, cid))
	return api
}

// deathQueueStateOf extracts a *DeathQueue's full DATA snapshot for
// comparison — the FEAT-087 AC-20 counterpart of coldRecordsOf/householdsOf/
// fidelitiesOf above.
func deathQueueStateOf(c *CitizensAPI, cid string) DeathQueueSnapshot {
	return c.deathQueue.Snapshot(cid)
}

// ---------------------------------------------------------------------------
// Same-package extraction helpers — read EVERY durable field directly
// (PopulationHash omits ChildCount/Workplace/School/Attainment/Schooling, so
// a hash-only comparison would miss those five; reflect.DeepEqual over the
// full ColdRecord set does not).
// ---------------------------------------------------------------------------

func coldRecordsOf(c *CitizensAPI) map[uint64]ColdRecord {
	out := make(map[uint64]ColdRecord)
	for _, s := range c.cold {
		for i := 0; i < s.count(); i++ {
			r := s.recordAt(i)
			out[r.ID] = r
		}
	}
	return out
}

func monthlyUpdatesOf(c *CitizensAPI) map[uint64]uint32 {
	out := make(map[uint64]uint32)
	for _, s := range c.cold {
		for i := 0; i < s.count(); i++ {
			out[s.ids[i]] = s.monthlyUpdates[i]
		}
	}
	return out
}

func householdsOf(c *CitizensAPI) map[uint64]Household {
	out := make(map[uint64]Household)
	for id, h := range c.households {
		hh := *h
		hh.Members = append([]uint64(nil), h.Members...)
		out[id] = hh
	}
	return out
}

func fidelitiesOf(c *CitizensAPI) map[uint64]Fidelity {
	out := make(map[uint64]Fidelity)
	for id, cit := range c.hot {
		out[id] = cit.Fidelity
	}
	return out
}

// assertSameState asserts a and b are byte-observably the same population:
// every cold field, the per-row monthlyUpdates, households, the hot fidelity
// set, the clock/counter/accumulator scalars, monthParams, and PopulationHash.
func assertSameState(t *testing.T, a, b *CitizensAPI, label string) {
	t.Helper()
	if !reflect.DeepEqual(coldRecordsOf(a), coldRecordsOf(b)) {
		t.Fatalf("%s: cold records differ", label)
	}
	if !reflect.DeepEqual(monthlyUpdatesOf(a), monthlyUpdatesOf(b)) {
		t.Fatalf("%s: monthlyUpdates differ", label)
	}
	if !reflect.DeepEqual(householdsOf(a), householdsOf(b)) {
		t.Fatalf("%s: households differ", label)
	}
	if !reflect.DeepEqual(fidelitiesOf(a), fidelitiesOf(b)) {
		t.Fatalf("%s: hot fidelity set differs\n a=%v\n b=%v", label, fidelitiesOf(a), fidelitiesOf(b))
	}
	if a.month != b.month || a.dayTick != b.dayTick {
		t.Fatalf("%s: clock differs: month %d/%d dayTick %d/%d", label, a.month, b.month, a.dayTick, b.dayTick)
	}
	if a.nextHouseholdID != b.nextHouseholdID {
		t.Fatalf("%s: nextHouseholdID %d != %d", label, a.nextHouseholdID, b.nextHouseholdID)
	}
	if a.nextFertilityChildID != b.nextFertilityChildID {
		t.Fatalf("%s: nextFertilityChildID %d != %d", label, a.nextFertilityChildID, b.nextFertilityChildID)
	}
	if a.curMonthBirths != b.curMonthBirths || a.curMonthDeaths != b.curMonthDeaths ||
		a.lastMonthBirths != b.lastMonthBirths || a.lastMonthDeaths != b.lastMonthDeaths {
		t.Fatalf("%s: vital accumulators differ: cur(%d,%d)/(%d,%d) last(%d,%d)/(%d,%d)", label,
			a.curMonthBirths, a.curMonthDeaths, b.curMonthBirths, b.curMonthDeaths,
			a.lastMonthBirths, a.lastMonthDeaths, b.lastMonthBirths, b.lastMonthDeaths)
	}
	if a.monthParams != b.monthParams {
		t.Fatalf("%s: monthParams differ:\n a=%+v\n b=%+v", label, a.monthParams, b.monthParams)
	}
	if a.PopulationHash(label) != b.PopulationHash(label) {
		t.Fatalf("%s: PopulationHash differs", label)
	}
	// FEAT-087 AC-20: pending/realisedIDs/realisedAt/handoff must all survive
	// a save/restore boundary byte-identically (BUG-483 F3).
	if !reflect.DeepEqual(deathQueueStateOf(a, label), deathQueueStateOf(b, label)) {
		t.Fatalf("%s: death queue state differs\n a=%+v\n b=%+v", label, deathQueueStateOf(a, label), deathQueueStateOf(b, label))
	}
}

// ---------------------------------------------------------------------------
// Save/load plumbing.
// ---------------------------------------------------------------------------

func citizensSaveInto(t *testing.T, c *CitizensAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(c)}, cid)
	ctx := save.Context{WorldSeed: 7, CreatedAtTick: 100, GameMonth: 1, AppVersion: "test-build"}
	must(t, mgr.SaveManual(ctx, "det"))
	return root
}

func reloadFrom(t *testing.T, root string, seed uint64, cid string) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(seed, cid)
	must(t, err)
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(api)}, cid)
	_, _, err = mgr.Load(bundleDir(t, root))
	must(t, err)
	return api
}

func bundleDir(t *testing.T, root string) string {
	t.Helper()
	var found string
	must(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "header.json" {
			found = filepath.Dir(path)
		}
		return nil
	}))
	if found == "" {
		t.Fatalf("no bundle (header.json) under %q", root)
	}
	return found
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	must(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	}))
	sort.Strings(out)
	return out
}

// findIDInShard scans for the smallest positive id that hashes to target.
func findIDInShard(target int) uint64 {
	for id := uint64(1); id < 1<<20; id++ {
		if det.ShardForEntity(id) == target {
			return id
		}
	}
	panic("no id found for shard")
}

// ===========================================================================
// 1. Field-parity drift tests (AC-2 obligation).
// ===========================================================================

// assertFieldParity: every domain field has a wire counterpart of the same
// reflect.Kind, and (unless extraWire names are listed) the field counts
// match. extraWire lists wire fields with NO domain counterpart (documented
// shard-level extras), and rename maps a domain field to a differently-named
// wire field.
func assertFieldParity(t *testing.T, domain, wire reflect.Type, rename map[string]string, extraWire ...string) {
	t.Helper()
	if wire.NumField() != domain.NumField()+len(extraWire) {
		t.Fatalf("%s has %d fields, wire %s has %d (expected %d = domain + %d documented extras)",
			domain.Name(), domain.NumField(), wire.Name(), wire.NumField(), domain.NumField()+len(extraWire), len(extraWire))
	}
	for i := 0; i < domain.NumField(); i++ {
		df := domain.Field(i)
		want := df.Name
		if r, ok := rename[df.Name]; ok {
			want = r
		}
		wf, ok := wire.FieldByName(want)
		if !ok {
			t.Fatalf("%s field %q has no counterpart %s.%s", domain.Name(), df.Name, wire.Name(), want)
		}
		if wf.Type.Kind() != df.Type.Kind() {
			t.Fatalf("%s.%s has kind %s, want %s to match %s.%s", wire.Name(), wf.Name, wf.Type.Kind(), df.Type.Kind(), domain.Name(), df.Name)
		}
	}
	// Every listed extra must actually exist on the wire (so a stale allowlist
	// entry cannot mask a real drift).
	for _, e := range extraWire {
		if _, ok := wire.FieldByName(e); !ok {
			t.Fatalf("documented extra wire field %s.%s does not exist", wire.Name(), e)
		}
	}
}

func TestCitizensWireFieldsMatchDomain(t *testing.T) {
	// ColdRecord ↔ coldCitizenWire, plus the one documented shard-level extra
	// (MonthlyUpdates — ColdShard.monthlyUpdates, not a ColdRecord field).
	assertFieldParity(t,
		reflect.TypeOf((*ColdRecord)(nil)).Elem(),
		reflect.TypeOf((*coldCitizenWire)(nil)).Elem(), nil, "MonthlyUpdates")
	// Household ↔ householdWire.
	assertFieldParity(t,
		reflect.TypeOf((*Household)(nil)).Elem(),
		reflect.TypeOf((*householdWire)(nil)).Elem(), nil)
	// ColdPassParams ↔ coldPassParamsWire.
	assertFieldParity(t,
		reflect.TypeOf((*ColdPassParams)(nil)).Elem(),
		reflect.TypeOf((*coldPassParamsWire)(nil)).Elem(), nil)
	// FEAT-087 AC-20: DeathQueueEntrySnapshot ↔ deathQueueEntryWire,
	// DeathQueueSnapshot ↔ deathQueueWire, RealisedDeath ↔ realisedDeathWire.
	assertFieldParity(t,
		reflect.TypeOf((*DeathQueueEntrySnapshot)(nil)).Elem(),
		reflect.TypeOf((*deathQueueEntryWire)(nil)).Elem(), nil)
	assertFieldParity(t,
		reflect.TypeOf((*DeathQueueSnapshot)(nil)).Elem(),
		reflect.TypeOf((*deathQueueWire)(nil)).Elem(), nil)
	assertFieldParity(t,
		reflect.TypeOf((*RealisedDeath)(nil)).Elem(),
		reflect.TypeOf((*realisedDeathWire)(nil)).Elem(), nil)
}

// TestCitizensAPIFieldsAllClassified is the highest-teeth AC-2 test: every
// field of CitizensAPI must be either EXCLUDED (runtime/config, never saved)
// or COVERED (serialized via a wire record). A new durable field added to
// CitizensAPI that is neither fails the build here — the "built but not
// serialized" class.
func TestCitizensAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"seed":         "worldSeed construction/header input, not participant state",
		"workers":      "perf pool size (AC-17: affects wall-clock only, never results)",
		"fertilityCfg": "immutable data/fertility.json config, reloaded by NewCitizensAPI",
		"mortalityCfg": "immutable data/mortality.json config, reloaded by NewCitizensAPI",
		"mu":           "runtime lock, not state",
		"self":         "SEC-020 copy-guard pointer, re-armed by NewCitizensAPI",
		"season": "FEAT-087 (mkey feat.deathwave) inc2 — an INJECTED DEPENDENCY " +
			"(*season.SeasonAPI), re-wired by the composition root on load via SetSeason, " +
			"not simulation state this module owns (mirrors engine.consumption's own " +
			"season field, participant.go precedent). engine.season is itself pure " +
			"month-index curves read from data/seasonal.json -- there is nothing here to " +
			"serialize, only a pointer to re-wire.",
	}
	covered := map[string]bool{
		"month": true, "dayTick": true, "cold": true, "hot": true,
		"households": true, "nextHouseholdID": true, "nextFertilityChildID": true,
		"curMonthBirths": true, "curMonthDeaths": true,
		"lastMonthBirths": true, "lastMonthDeaths": true, "monthParams": true,
		// FEAT-087 AC-20 (BUG-483 F3): the death queue's pending/realisedIDs/
		// realisedAt/handoff DATA now round-trips via the "citizens.deathqueue"
		// record (participant.go's toDeathQueueWire/DeathQueue.Snapshot) — see
		// DeathQueueSnapshot's own doc comment in deathwave.go for the
		// durable-vs-derived breakdown, and RestoreSnapshot's doc for the
		// mandatory shardIndex rebuild that closes the inc1.5-era KNOWN GAP
		// this field used to be excluded under.
		"deathQueue": true,
	}
	ct := reflect.TypeOf((*CitizensAPI)(nil)).Elem()
	for i := 0; i < ct.NumField(); i++ {
		name := ct.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("CitizensAPI field %q is neither serialized nor explicitly excluded — AC-2 forbids a silently-unsaved field", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("CitizensAPI field %q is listed as BOTH excluded and covered", name)
		}
	}
}

// ===========================================================================
// 2. Round-trip + continue + prove-can-fail.
// ===========================================================================

func TestCitizensParticipant_RoundTrip(t *testing.T) {
	const seed = uint64(12345)
	orig := buildPopulation(t, seed, "orig")
	root := citizensSaveInto(t, orig, "orig")

	reloaded := reloadFrom(t, root, seed, "reloaded")
	assertSameState(t, orig, reloaded, "post-load")

	// Continue IDENTICAL advancement on both and assert they stay equal — a
	// divergent restore (e.g. a lost monthlyUpdates or monthParams) surfaces
	// the moment new work builds on it.
	for d := 0; d < DaysPerMonth+3; d++ {
		_, _, err := orig.AdvanceDayTick("orig")
		must(t, err)
		_, _, err = reloaded.AdvanceDayTick("reloaded")
		must(t, err)
	}
	assertSameState(t, orig, reloaded, "post-continue")
}

// TestCitizensParticipant_ProveCanFail proves assertSameState has teeth on
// EVERY ColdRecord field, personality axis, satisfaction component, the
// monthlyUpdates column, and each meta scalar — so a passing round-trip is
// meaningful. For each, a fresh reload is mutated in exactly one place and the
// comparison MUST then diverge.
func TestCitizensParticipant_ProveCanFail(t *testing.T) {
	const seed = uint64(999)
	orig := buildPopulation(t, seed, "orig")
	root := citizensSaveInto(t, orig, "orig")

	// A citizen guaranteed present (id 300 is never partnered/elevated/likely
	// removed) and its shard/row for direct column mutation.
	const victim = uint64(300)

	mutateCold := func(t *testing.T, r *CitizensAPI, fn func(s *ColdShard, row int)) {
		shard := det.ShardForEntity(victim)
		s := r.cold[shard]
		row := s.rowOf(victim)
		if row < 0 {
			t.Fatalf("victim %d not present in reload", victim)
		}
		fn(s, row)
	}

	// (field-name, mutation) table — one entry per ColdRecord field, each
	// personality axis, and each satisfaction component, individually.
	coldMutators := map[string]func(s *ColdShard, row int){
		"BirthMonth":      func(s *ColdShard, i int) { s.birthDelta[i]++ },
		"Sex":             func(s *ColdShard, i int) { s.sexes[i] ^= 1 },
		"Household":       func(s *ColdShard, i int) { s.households[i]++ },
		"Partner":         func(s *ColdShard, i int) { s.partners[i]++ },
		"ChildCount":      func(s *ColdShard, i int) { s.childCount[i]++ },
		"Home":            func(s *ColdShard, i int) { s.homeCells[i]++ },
		"District":        func(s *ColdShard, i int) { s.districts[i]++ },
		"Workplace":       func(s *ColdShard, i int) { s.workplaces[i]++ },
		"School":          func(s *ColdShard, i int) { s.schools[i]++ },
		"Attainment":      func(s *ColdShard, i int) { s.attainment[i]++ },
		"Stage":           func(s *ColdShard, i int) { s.stages[i] = 0 },
		"Schooling":       func(s *ColdShard, i int) { s.schooling[i]++ },
		"HealthBand":      func(s *ColdShard, i int) { s.healthBands[i] = 0 },
		"Access":          func(s *ColdShard, i int) { s.access[i]++ },
		"Wealth":          func(s *ColdShard, i int) { s.wealth[i]++ },
		"Employment":      func(s *ColdShard, i int) { s.employment[i] ^= 0x0f },
		"MonthlyUpdates":  func(s *ColdShard, i int) { s.monthlyUpdates[i]++ },
		"Sat.Housing":     func(s *ColdShard, i int) { s.satHousing[i] ^= 1 },
		"Sat.Services":    func(s *ColdShard, i int) { s.satServices[i] ^= 1 },
		"Sat.Environment": func(s *ColdShard, i int) { s.satEnvironment[i] ^= 1 },
		"Sat.LeisureFit":  func(s *ColdShard, i int) { s.satLeisureFit[i] ^= 1 },
		"Sat.Commute":     func(s *ColdShard, i int) { s.satCommute[i] ^= 1 },
	}
	// Each personality axis individually.
	pcols := func(s *ColdShard) []([]int8) {
		return [][]int8{s.pSociability, s.pAmbition, s.pConscientious, s.pNovelty,
			s.pPhysicality, s.pCommunity, s.pPatience, s.pAesthetic}
	}
	for axis := 0; axis < NumPersonalityAxes; axis++ {
		a := axis
		coldMutators["Personality["+string(rune('0'+a))+"]"] = func(s *ColdShard, i int) { pcols(s)[a][i] ^= 1 }
	}

	for name, fn := range coldMutators {
		reloaded := reloadFrom(t, root, seed, "r")
		mutateCold(t, reloaded, fn)
		if reflect.DeepEqual(coldRecordsOf(orig), coldRecordsOf(reloaded)) &&
			reflect.DeepEqual(monthlyUpdatesOf(orig), monthlyUpdatesOf(reloaded)) {
			t.Fatalf("prove-can-fail: mutating cold field %q did not diverge — that field does not round-trip / is not compared", name)
		}
	}

	// Meta scalars — mutate each on a fresh reload; the comparison must catch it.
	metaMutators := map[string]func(c *CitizensAPI){
		"month":                func(c *CitizensAPI) { c.month++ },
		"dayTick":              func(c *CitizensAPI) { c.dayTick++ },
		"nextHouseholdID":      func(c *CitizensAPI) { c.nextHouseholdID++ },
		"nextFertilityChildID": func(c *CitizensAPI) { c.nextFertilityChildID++ },
		"curMonthBirths":       func(c *CitizensAPI) { c.curMonthBirths++ },
		"curMonthDeaths":       func(c *CitizensAPI) { c.curMonthDeaths++ },
		"lastMonthBirths":      func(c *CitizensAPI) { c.lastMonthBirths++ },
		"lastMonthDeaths":      func(c *CitizensAPI) { c.lastMonthDeaths++ },
		"monthParams":          func(c *CitizensAPI) { c.monthParams.MortalityMultiplier += 0.5 },
	}
	for name, fn := range metaMutators {
		reloaded := reloadFrom(t, root, seed, "r")
		fn(reloaded)
		// Compare only the scalar surface (cold records unchanged here).
		same := reloaded.month == orig.month && reloaded.dayTick == orig.dayTick &&
			reloaded.nextHouseholdID == orig.nextHouseholdID &&
			reloaded.nextFertilityChildID == orig.nextFertilityChildID &&
			reloaded.curMonthBirths == orig.curMonthBirths && reloaded.curMonthDeaths == orig.curMonthDeaths &&
			reloaded.lastMonthBirths == orig.lastMonthBirths && reloaded.lastMonthDeaths == orig.lastMonthDeaths &&
			reloaded.monthParams == orig.monthParams
		if same {
			t.Fatalf("prove-can-fail: mutating meta scalar %q did not diverge — it does not round-trip", name)
		}
	}

	// Fidelity: dropping an elevated citizen's tier must diverge.
	reloaded := reloadFrom(t, root, seed, "r")
	if len(reloaded.hot) == 0 {
		t.Fatalf("test setup: no elevated citizens in reload")
	}
	for id := range reloaded.hot {
		delete(reloaded.hot, id)
		break
	}
	if reflect.DeepEqual(fidelitiesOf(orig), fidelitiesOf(reloaded)) {
		t.Fatalf("prove-can-fail: dropping a hot fidelity entry did not diverge")
	}

	// FEAT-087 AC-20: dropping a pending death-queue entry (the exact BUG-483
	// F3 defect this AC closes) must diverge too.
	deathQueueMutators := map[string]func(c *CitizensAPI){
		"drop a pending entry": func(c *CitizensAPI) {
			c.deathQueue.mu.Lock()
			c.deathQueue.pending = c.deathQueue.pending[:len(c.deathQueue.pending)-1]
			c.deathQueue.mu.Unlock()
		},
		"drop a realisedID": func(c *CitizensAPI) {
			c.deathQueue.mu.Lock()
			c.deathQueue.realisedIDs = nil
			c.deathQueue.mu.Unlock()
		},
		"drop realisedAt": func(c *CitizensAPI) {
			c.deathQueue.mu.Lock()
			c.deathQueue.realisedAt = map[uint64]int64{}
			c.deathQueue.mu.Unlock()
		},
	}
	for name, fn := range deathQueueMutators {
		r := reloadFrom(t, root, seed, "r")
		fn(r)
		if reflect.DeepEqual(deathQueueStateOf(orig, "cmp"), deathQueueStateOf(r, "cmp")) {
			t.Fatalf("prove-can-fail: death queue mutation %q did not diverge — that field does not round-trip / is not compared", name)
		}
	}
}

// ===========================================================================
// 3. Byte determinism (many citizens/households/fidelity across shards).
// ===========================================================================

func TestCitizensParticipant_ByteDeterminism(t *testing.T) {
	root1 := citizensSaveInto(t, buildPopulation(t, 42, "run1"), "run1")
	root2 := citizensSaveInto(t, buildPopulation(t, 42, "run2"), "run2")

	dir1, dir2 := bundleDir(t, root1), bundleDir(t, root2)
	files1, files2 := filesUnder(t, dir1), filesUnder(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		must(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		must(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic population (raw-shard/raw-map emission?)", rel)
		}
	}
}

// ===========================================================================
// 4. Load-into-non-empty full-replace.
// ===========================================================================

func TestCitizensParticipant_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	const seed = uint64(2024)
	orig := buildPopulation(t, seed, "orig")
	root := citizensSaveInto(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger population + ghost data.
	target, err := NewCitizensAPI(seed, "target")
	must(t, err)
	ghost := make([]ColdRecord, 0, 500)
	for id := uint64(10000); id < 10500; id++ {
		ghost = append(ghost, distinctRecord(id))
	}
	must(t, target.SeedColdRecords(ghost, "target"))
	must(t, target.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "target", Kind: LifeEventPartner, CitizenID: 10000, PartnerID: 10001}))
	must(t, target.ApplyFidelityCommand(FidelityCommand{CorrelationID: "target", CitizenID: 10002, Target: FidelityHot}))

	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(target)}, "target")
	_, _, err = mgr.Load(bundleDir(t, root))
	must(t, err)

	// The ghost citizens must be GONE (full replace, not merge).
	if _, ok := target.coldRecord(10000); ok {
		t.Fatalf("ghost citizen 10000 survived load — Handler merged instead of replacing")
	}
	if target.TotalPopulation("target") != orig.TotalPopulation("orig") {
		t.Fatalf("population %d != saved %d — merge, not replace", target.TotalPopulation("target"), orig.TotalPopulation("orig"))
	}
	assertSameState(t, orig, target, "load-into-nonempty")
}

// ===========================================================================
// 5. Copyguard + unknown-kind.
// ===========================================================================

func citizensByteCopyForParticipant(c *CitizensAPI) *CitizensAPI {
	cp := new(CitizensAPI)
	*(*[unsafe.Sizeof(CitizensAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(CitizensAPI{})]byte)(unsafe.Pointer(c))
	return cp
}

func TestCitizensParticipant_CopyguardFires(t *testing.T) {
	orig, err := NewCitizensAPI(1, "orig")
	must(t, err)
	cp := citizensByteCopyForParticipant(orig)
	sp := NewSaveParticipant(cp)

	if sp.Kind() != "" {
		t.Fatalf("copied participant Kind() = %q, want empty", sp.Kind())
	}
	if _, _, err := sp.Source()(); err == nil {
		t.Fatalf("copied participant Source() first pull returned nil error — guard did not fire")
	}
	if err := sp.Handler()(serialize.Record{Kind: recCitizensMeta}); err == nil {
		t.Fatalf("copied participant Handler() returned nil error — guard did not fire")
	}
	// The original still works.
	if NewSaveParticipant(orig).Kind() != KindCitizens {
		t.Fatalf("original participant Kind() broken")
	}
}

func TestCitizensParticipant_UnknownKind(t *testing.T) {
	api, err := NewCitizensAPI(1, "c")
	must(t, err)
	h := NewSaveParticipant(api).Handler()
	// First record triggers reset; a meta record is fine.
	must(t, h(mustMeta(t)))
	if err := h(serialize.Record{Kind: "citizens.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("expected an error for an unknown record kind, got nil")
	}
}

// mustMeta builds a minimal valid citizens.meta record (correct EpochMonths
// length) so a Handler test can get past the reset without a real save.
func mustMeta(t *testing.T) serialize.Record {
	t.Helper()
	sp := NewSaveParticipant(mustAPI(t))
	src := sp.Source()
	rec, ok, err := src()
	must(t, err)
	if !ok || rec.Kind != recCitizensMeta {
		t.Fatalf("first source record is not %s (ok=%v kind=%q)", recCitizensMeta, ok, rec.Kind)
	}
	return rec
}

func mustAPI(t *testing.T) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(1, "c")
	must(t, err)
	return api
}

// ===========================================================================
// 6. STREAMING — the 100M-citizen contract. Source must not materialise all
// 256 shards before the first yield.
// ===========================================================================

// TestCitizensParticipant_ColdStreamIsLazy proves coldStream snapshots shards
// on demand, not all at once: with citizens ONLY in shard 0 and shard 255,
// the first pull must have snapshotted exactly ONE shard (shard 0) — a
// buffer-everything implementation would have snapshotted all 256 up front.
func TestCitizensParticipant_ColdStreamIsLazy(t *testing.T) {
	api, err := NewCitizensAPI(1, "lazy")
	must(t, err)
	loID := findIDInShard(0)
	hiID := findIDInShard(numColdShards - 1)
	must(t, api.SeedColdRecords([]ColdRecord{distinctRecord(loID), distinctRecord(hiID)}, "lazy"))

	cs := api.newColdStream()
	if _, ok := cs.next(); !ok {
		t.Fatalf("expected a first cold record")
	}
	if cs.snapshots != 1 {
		t.Fatalf("after the first pull coldStream snapshotted %d shards, want 1 — Source is not lazy (it buffered ahead)", cs.snapshots)
	}

	// Draining the rest walks every shard and yields both citizens exactly once.
	wantRecords := 2 // the two citizens seeded above (shard 0 + shard 255)
	count := 1
	for {
		if _, ok := cs.next(); !ok {
			break
		}
		count++
	}
	if count != wantRecords {
		t.Fatalf("coldStream yielded %d cold records, want %d", count, wantRecords)
	}
	if cs.snapshots != numColdShards {
		t.Fatalf("after draining coldStream snapshotted %d shards, want %d", cs.snapshots, numColdShards)
	}
}

// ===========================================================================
// 7. Fidelity fail-closed guards + hot-body faithful rebuild (attack 4). These
// guards exist in applyLoadRecord but had no regression coverage before the
// FEAT-1972079941 inc9 destructive round — a dropped guard would have shipped
// a phantom-citizen or out-of-range fidelity silently.
// ===========================================================================

// fidelityRecord marshals a fidelityWire into a citizens.fidelity save record.
func fidelityRecord(t *testing.T, id uint64, f Fidelity) serialize.Record {
	t.Helper()
	data, err := json.Marshal(fidelityWire{ID: id, Fidelity: f})
	must(t, err)
	return serialize.Record{Kind: recCitizensFidelity, Data: data}
}

// TestCitizensParticipant_FidelityNoColdBackingFailsClosed: a fidelity record
// referencing a citizen with NO cold record must be rejected loud-and-closed,
// never installing a hot entry aliasing a citizen that does not exist.
func TestCitizensParticipant_FidelityNoColdBackingFailsClosed(t *testing.T) {
	api := mustAPI(t)
	h := NewSaveParticipant(api).Handler()
	must(t, h(mustMeta(t))) // reset + clock
	const ghostID = uint64(918273645)
	err := h(fidelityRecord(t, ghostID, FidelityHot))
	if err == nil {
		t.Fatalf("fidelity record for a citizen with no cold backing was accepted — phantom citizen installed")
	}
	if _, ok := api.hot[ghostID]; ok {
		t.Fatalf("phantom hot entry %d was installed despite the fail-closed guard", ghostID)
	}
}

// TestCitizensParticipant_FidelityOutOfRangeRejected: a fidelity tier above the
// FidelityHot ceiling is corrupt input and must be rejected.
func TestCitizensParticipant_FidelityOutOfRangeRejected(t *testing.T) {
	api := mustAPI(t)
	h := NewSaveParticipant(api).Handler()
	must(t, h(mustMeta(t)))
	if err := h(fidelityRecord(t, 1, FidelityHot+1)); err == nil {
		t.Fatalf("out-of-range fidelity %d was accepted", FidelityHot+1)
	}
}

// TestCitizensParticipant_HotBodyRebuiltFromCold proves attack-4's second half:
// an elevated citizen's hot BODY (not just its tier) is faithfully rebuilt on
// load via coldRecordToHot of the restored cold record, with the saved tier
// stamped — exactly the ApplyFidelityCommand COLD→HOT path. A restore that kept
// only the tier but left a stale/blank body would diverge here.
func TestCitizensParticipant_HotBodyRebuiltFromCold(t *testing.T) {
	const seed = uint64(77)
	orig := buildPopulation(t, seed, "orig")
	root := citizensSaveInto(t, orig, "orig")
	reloaded := reloadFrom(t, root, seed, "reloaded")

	if len(reloaded.hot) == 0 {
		t.Fatalf("test setup: no elevated citizens after reload")
	}
	for id, cit := range reloaded.hot {
		cold, ok := reloaded.coldRecord(id)
		if !ok {
			t.Fatalf("elevated citizen %d has no cold record after load", id)
		}
		want := coldRecordToHot(cold, reloaded.month)
		want.Fidelity = cit.Fidelity
		if !reflect.DeepEqual(*cit, want) {
			t.Fatalf("hot body for %d not faithfully rebuilt from cold:\n got=%+v\nwant=%+v", id, *cit, want)
		}
	}
}

// TestCitizensParticipant_SourceStreamsRecordShape sanity-checks the emission
// order and that the meta record carries a full EpochMonths vector.
func TestCitizensParticipant_SourceStreamsRecordShape(t *testing.T) {
	api := buildPopulation(t, 3, "shape")
	src := NewSaveParticipant(api).Source()

	rec, ok, err := src()
	must(t, err)
	if !ok || rec.Kind != recCitizensMeta {
		t.Fatalf("first record is not %s", recCitizensMeta)
	}
	// Kinds must appear in the order meta, deathqueue, cold*, household*,
	// fidelity* (FEAT-087 AC-20 adds the single deathqueue record right
	// after meta).
	order := map[string]int{recCitizensMeta: 0, recCitizensDeathQueue: 1, recCitizensCold: 2, recCitizensHousehold: 3, recCitizensFidelity: 4}
	last := 0
	counts := map[string]int{recCitizensMeta: 1}
	for {
		rec, ok, err = src()
		must(t, err)
		if !ok {
			break
		}
		phase, known := order[rec.Kind]
		if !known {
			t.Fatalf("unexpected record kind %q", rec.Kind)
		}
		if phase < last {
			t.Fatalf("record kind %q (phase %d) appeared after phase %d — out of order", rec.Kind, phase, last)
		}
		last = phase
		counts[rec.Kind]++
	}
	if counts[recCitizensDeathQueue] == 0 || counts[recCitizensCold] == 0 || counts[recCitizensHousehold] == 0 || counts[recCitizensFidelity] == 0 {
		t.Fatalf("missing record kinds: %+v", counts)
	}
}
