package crime

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// FEAT-1972079943 — engine.crime save.Participant tests.
//
// Mirrors the engine.refuse participant test suite exactly, adapted to
// engine.crime's mutable state (per-district generation/stock/policing/
// justice, the gang entities, and the meta scalars: nextGangID / HQ gate /
// strategy mix / threat dial / last-pushed security). The mandatory shapes:
// field-parity drift (CrimeAPI + districtState + justiceState + Gang +
// threatState + StrategyMix + SecurityInput), full round-trip + continue +
// prove-can-fail per field, byte determinism (many-key with NUMERIC key
// sort), a dedicated NUMERIC-order test (a lexical sort of the uint64 keys
// must redden), load-into-non-empty (replace not merge), and copyguard-fires
// + unknown-record-kind rejection.
// ---------------------------------------------------------------------------

func ckErrC(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// newCrimeAPI constructs a fresh CrimeAPI for save tests (seed is excluded
// from the participant, so the fixed value only feeds the — unexercised here —
// draw streams).
func newCrimeAPI(t *testing.T) *CrimeAPI {
	t.Helper()
	a, err := New(42, "crime-save-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// ---------------------------------------------------------------------------
// Field-parity drift tests (the "built but not serialized" guard).
// ---------------------------------------------------------------------------

// TestCrimeAPIFieldsAllClassified fails the build if any CrimeAPI field is
// neither serialized (covered) nor explicitly excluded (config/worldSeed/
// injected-dep/runtime/copy-guard). A new mutable field added without a save is
// exactly the class this participant exists to prevent.
func TestCrimeAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"correlationID": "per-instance error correlation, not simulation state",
		"seed":          "worldSeed construction/header input (a load target is constructed with the same seed)",
		"cfg":           "immutable config, loaded from crime.json (a save must not pin old rules — FEAT-1972079897)",
		"mu":            "runtime lock, not state",
		"prison":        "injected dependency (PrisonIntake interface), re-wired by the composition root via SetPrisonIntake on load",
		"self":          "SEC-020 copy-guard pointer, re-armed by New",
	}
	// Covered: serialized via crimeMetaWire (scalars/aggregates) or a per-item
	// record (districts -> crime.district, gangs -> crime.gang).
	covered := map[string]bool{
		"districts":           true,
		"gangs":               true,
		"nextGangID":          true,
		"constabularyHQBuilt": true,
		"mix":                 true,
		"threat":              true,
		"security":            true,
	}
	rt := reflect.TypeOf((*CrimeAPI)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("CrimeAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this participant forbids", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("CrimeAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestCrimeDistrictStateFieldsClassified asserts every districtState field is
// either carried on the wire or explicitly excluded. `inputs` is the one
// exclusion: it is the last-pushed DistrictInput, write-only (read by nothing)
// and re-supplied on every AdvanceMonth, so persisting it would only carry a
// dead input cache.
func TestCrimeDistrictStateFieldsClassified(t *testing.T) {
	covered := map[string]string{
		"id":                  "ID",
		"generation":          "Generation",
		"rawGen":              "RawGen",
		"persisted":           "Persisted",
		"active":              "Active",
		"deterrence":          "Deterrence",
		"clearance":           "Clearance",
		"prevention":          "Prevention",
		"effectiveClearance":  "EffectiveClearance",
		"sustainedMonths":     "SustainedMonths",
		"eligiblePool":        "EligiblePool",
		"recruitedCumulative": "RecruitedCumulative",
		"justice":             "Justice",
	}
	excluded := map[string]string{
		"inputs": "last-pushed DistrictInput; write-only (read by nothing) and re-supplied on every AdvanceMonth from the composition root, so it carries no observable state",
	}
	rt := reflect.TypeOf((*districtState)(nil)).Elem()
	if rt.NumField() != len(covered)+len(excluded) {
		t.Fatalf("districtState has %d fields but %d are classified -- a field was added without a wire counterpart or an exclusion", rt.NumField(), len(covered)+len(excluded))
	}
	wt := reflect.TypeOf((*crimeDistrictWire)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if wire, ok := covered[name]; ok {
			if _, ok := wt.FieldByName(wire); !ok {
				t.Fatalf("crimeDistrictWire is missing field %q for districtState.%s", wire, name)
			}
			continue
		}
		if _, ok := excluded[name]; ok {
			continue
		}
		t.Fatalf("districtState field %q is neither covered nor excluded", name)
	}
}

// TestCrimeJusticeStateFieldsCovered asserts the on-wire justice record carries
// a counterpart for EVERY field of the internal justiceState (the backlog stock
// and all nine per-month stage logs).
func TestCrimeJusticeStateFieldsCovered(t *testing.T) {
	want := map[string]string{
		"backlog":               "Backlog",
		"arrested":              "Arrested",
		"charged":               "Charged",
		"releasedNoCharge":      "ReleasedNoCharge",
		"convicted":             "Convicted",
		"acquitted":             "Acquitted",
		"awaitingTrial":         "AwaitingTrial",
		"sentencedToPrison":     "SentencedToPrison",
		"sentencedNonCustodial": "SentencedNonCustodial",
		"releasedOnBacklog":     "ReleasedOnBacklog",
	}
	jt := reflect.TypeOf((*justiceState)(nil)).Elem()
	if jt.NumField() != len(want) {
		t.Fatalf("justiceState has %d fields but %d are mapped to the wire -- a justice field was added without a wire counterpart", jt.NumField(), len(want))
	}
	wt := reflect.TypeOf((*justiceStateWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := jt.FieldByName(domain); !ok {
			t.Fatalf("justiceState is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("justiceStateWire is missing field %q for justiceState.%s", wire, domain)
		}
	}
}

// TestCrimeGangFieldsCovered asserts the on-wire gang record carries a
// counterpart for every field of the internal Gang entity.
func TestCrimeGangFieldsCovered(t *testing.T) {
	want := map[string]string{
		"ID":                 "ID",
		"Name":               "Name",
		"District":           "District",
		"FormedAt":           "FormedAt",
		"Strength":           "Strength",
		"Territory":          "Territory",
		"TaxLevyMicroPounds": "TaxLevyMicroPounds",
		"BusinessClosures":   "BusinessClosures",
		"Recruited":          "Recruited",
	}
	gt := reflect.TypeOf((*Gang)(nil)).Elem()
	if gt.NumField() != len(want) {
		t.Fatalf("Gang has %d fields but %d are mapped to the wire -- a gang field was added without a wire counterpart", gt.NumField(), len(want))
	}
	wt := reflect.TypeOf((*crimeGangWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := gt.FieldByName(domain); !ok {
			t.Fatalf("Gang is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("crimeGangWire is missing field %q for Gang.%s", wire, domain)
		}
	}
}

// TestCrimeThreatStateFieldsCovered asserts the threat wire carries every
// field of the internal threatState dial.
func TestCrimeThreatStateFieldsCovered(t *testing.T) {
	want := map[string]string{
		"level":          "Level",
		"elevatedMonths": "ElevatedMonths",
		"lastRiseMonth":  "LastRiseMonth",
		"lastEventMonth": "LastEventMonth",
	}
	tt := reflect.TypeOf((*threatState)(nil)).Elem()
	if tt.NumField() != len(want) {
		t.Fatalf("threatState has %d fields but %d are mapped to the wire", tt.NumField(), len(want))
	}
	wt := reflect.TypeOf((*threatStateWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := tt.FieldByName(domain); !ok {
			t.Fatalf("threatState is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("threatStateWire is missing field %q for threatState.%s", wire, domain)
		}
	}
}

// TestCrimeMetaWireFieldsMatchScalars asserts the meta wire carries a
// counterpart for exactly the serialized non-map CrimeAPI fields.
func TestCrimeMetaWireFieldsMatchScalars(t *testing.T) {
	want := map[string]reflect.Kind{
		"NextGangID":          reflect.Uint64,
		"ConstabularyHQBuilt": reflect.Bool,
		"Mix":                 reflect.Struct,
		"Threat":              reflect.Struct,
		"Security":            reflect.Struct,
	}
	mw := reflect.TypeOf((*crimeMetaWire)(nil)).Elem()
	if mw.NumField() != len(want) {
		t.Fatalf("crimeMetaWire has %d fields but %d serialized scalars/aggregates are expected -- meta wire drifted", mw.NumField(), len(want))
	}
	for name, kind := range want {
		f, ok := mw.FieldByName(name)
		if !ok {
			t.Fatalf("crimeMetaWire is missing field %q", name)
		}
		if f.Type.Kind() != kind {
			t.Fatalf("crimeMetaWire.%s has kind %s, want %s", name, f.Type.Kind(), kind)
		}
	}
}

// TestCrimeStrategyMixWireFieldsCovered / TestCrimeSecurityWireFieldsCovered
// guard the two nested meta aggregates against drift.
func TestCrimeStrategyMixWireFieldsCovered(t *testing.T) {
	want := map[string]string{"Patrol": "Patrol", "Detective": "Detective", "Community": "Community"}
	st := reflect.TypeOf((*StrategyMix)(nil)).Elem()
	if st.NumField() != len(want) {
		t.Fatalf("StrategyMix has %d fields but %d are mapped to the wire", st.NumField(), len(want))
	}
	wt := reflect.TypeOf((*strategyMixWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := st.FieldByName(domain); !ok {
			t.Fatalf("StrategyMix is missing %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("strategyMixWire is missing %q", wire)
		}
	}
}

func TestCrimeSecurityWireFieldsCovered(t *testing.T) {
	want := map[string]string{"Exposure": "Exposure", "Funding": "Funding", "Liaison": "Liaison"}
	st := reflect.TypeOf((*SecurityInput)(nil)).Elem()
	if st.NumField() != len(want) {
		t.Fatalf("SecurityInput has %d fields but %d are mapped to the wire", st.NumField(), len(want))
	}
	wt := reflect.TypeOf((*securityWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := st.FieldByName(domain); !ok {
			t.Fatalf("SecurityInput is missing %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("securityWire is missing %q", wire)
		}
	}
}

// ---------------------------------------------------------------------------
// Rich-state builders (DISTINCT, NON-ZERO values for EVERY field, so a dropped
// field cannot round-trip by coincidence).
// ---------------------------------------------------------------------------

// richJustice returns a justiceState with EVERY slice populated with distinct,
// non-empty, distinct-element contents (so no nil/empty ambiguity and every
// field is discriminating). base offsets each district's ids.
func richJustice(base uint64) justiceState {
	seq := func(start, n uint64) []uint64 {
		out := make([]uint64, 0, n)
		for i := uint64(0); i < n; i++ {
			out = append(out, base+start+i)
		}
		return out
	}
	return justiceState{
		backlog:               seq(1000, 5),
		arrested:              seq(2000, 4),
		charged:               seq(3000, 3),
		releasedNoCharge:      seq(4000, 2),
		convicted:             seq(5000, 3),
		acquitted:             seq(6000, 2),
		awaitingTrial:         seq(7000, 2),
		sentencedToPrison:     seq(8000, 2),
		sentencedNonCustodial: seq(9000, 1),
		releasedOnBacklog:     seq(9500, 1),
	}
}

// richDistrict fills a districtState directly (white-box) with distinct,
// non-zero values across every serialized field. idx scales the values so two
// districts never coincide.
func richDistrict(id DistrictID, idx int) *districtState {
	f := float64(idx)
	var gen, raw, per, act [numCrimeTypes]float64
	for t := 0; t < int(numCrimeTypes); t++ {
		gen[t] = f*100 + float64(t) + 0.11
		raw[t] = f*200 + float64(t) + 0.22
		per[t] = f*300 + float64(t) + 0.33
		act[t] = f*400 + float64(t) + 0.44
	}
	return &districtState{
		id:                  id,
		generation:          gen,
		rawGen:              raw,
		persisted:           per,
		active:              act,
		deterrence:          f*0.01 + 0.05,
		clearance:           f*0.02 + 0.06,
		prevention:          f*0.03 + 0.07,
		effectiveClearance:  f*0.04 + 0.08,
		sustainedMonths:     idx + 1,
		eligiblePool:        int64(idx*1000 + 123),
		recruitedCumulative: int64(idx*10 + 7),
		justice:             richJustice(uint64(id) * 100000),
		// inputs left zero: excluded from the save (write-only re-supplied input).
	}
}

// richGang fills a Gang with distinct, non-zero values across every field.
func richGang(id GangID, idx int) *Gang {
	return &Gang{
		ID:                 id,
		Name:               "gang-" + string(rune('A'+idx)),
		District:           DistrictID(idx*7 + 1),
		FormedAt:           int64(idx*3 + 2),
		Strength:           float64(idx)*0.1 + 0.25,
		Territory:          []uint64{uint64(idx*11 + 1), uint64(idx*13 + 2), uint64(idx*17 + 3)},
		TaxLevyMicroPounds: int64(idx*1000 + 500),
		BusinessClosures:   int64(idx + 4),
		Recruited:          int64(idx*100 + 9),
	}
}

// injectRichCrime installs distinct non-zero meta scalars, a spread of
// districts (each with distinct field values and a fully-populated justice
// ledger), and a spread of gangs — the whole serialized surface, no field
// zero.
func injectRichCrime(a *CrimeAPI) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.nextGangID = 42
	a.constabularyHQBuilt = true
	a.mix = StrategyMix{Patrol: 0.5, Detective: 0.3, Community: 0.2}
	a.threat = threatState{level: 3.5, elevatedMonths: 4, lastRiseMonth: 11, lastEventMonth: 7}
	a.security = SecurityInput{Exposure: 0.6, Funding: 0.4, Liaison: 0.25}

	a.districts = map[DistrictID]*districtState{
		1: richDistrict(1, 1),
		2: richDistrict(2, 2),
		3: richDistrict(3, 3),
	}
	a.gangs = map[GangID]*Gang{
		1: richGang(1, 1),
		2: richGang(2, 2),
	}
}

// injectManyKeysCrime forces MANY entries into both maps with keys whose
// NUMERIC order differs from their lexical order (2 < 10 numerically but "10" <
// "2" lexically), so an unsorted OR lexically-sorted emission would differ from
// the numeric-sorted one.
func injectManyKeysCrime(a *CrimeAPI) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.nextGangID = 999
	a.constabularyHQBuilt = true
	a.mix = StrategyMix{Patrol: 0.4, Detective: 0.4, Community: 0.2}
	a.threat = threatState{level: 1.25, elevatedMonths: 2, lastRiseMonth: 5, lastEventMonth: 3}
	a.security = SecurityInput{Exposure: 0.3, Funding: 0.2, Liaison: 0.1}

	a.districts = make(map[DistrictID]*districtState)
	// Deliberately non-contiguous, numeric-vs-lexical-divergent id set.
	dids := []DistrictID{1, 2, 3, 10, 11, 20, 21, 100, 101, 102, 200, 3000, 30001, 5, 7, 9, 12, 15, 18, 25, 33, 44, 55, 99, 123}
	for i, id := range dids {
		a.districts[id] = richDistrict(id, i+1)
	}

	a.gangs = make(map[GangID]*Gang)
	gids := []GangID{2, 10, 3, 30, 1, 100, 21, 200, 5, 50}
	for i, id := range gids {
		a.gangs[id] = richGang(id, i+1)
	}
}

// ---------------------------------------------------------------------------
// Comparison via the wire projection (compares EXACTLY the serialized surface,
// so excluded fields — districtState.inputs — legitimately differ after load).
// ---------------------------------------------------------------------------

// crimeWireOf projects a CrimeAPI's full serialized surface (meta + sorted
// districts + sorted gangs) into a comparable value under the read lock.
type crimeWire struct {
	meta      crimeMetaWire
	districts []crimeDistrictWire
	gangs     []crimeGangWire
}

func crimeWireOf(a *CrimeAPI) crimeWire {
	a.mu.RLock()
	defer a.mu.RUnlock()
	w := crimeWire{
		meta: crimeMetaWire{
			NextGangID:          a.nextGangID,
			ConstabularyHQBuilt: a.constabularyHQBuilt,
			Mix:                 strategyMixWire{a.mix.Patrol, a.mix.Detective, a.mix.Community},
			Threat: threatStateWire{
				Level: a.threat.level, ElevatedMonths: a.threat.elevatedMonths,
				LastRiseMonth: a.threat.lastRiseMonth, LastEventMonth: a.threat.lastEventMonth,
			},
			Security: securityWire{a.security.Exposure, a.security.Funding, a.security.Liaison},
		},
	}
	dids := make([]DistrictID, 0, len(a.districts))
	for id := range a.districts {
		dids = append(dids, id)
	}
	sort.Slice(dids, func(i, j int) bool { return dids[i] < dids[j] })
	for _, id := range dids {
		w.districts = append(w.districts, districtToWire(a.districts[id]))
	}
	gids := make([]GangID, 0, len(a.gangs))
	for id := range a.gangs {
		gids = append(gids, id)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
	for _, id := range gids {
		w.gangs = append(w.gangs, gangToWire(a.gangs[id]))
	}
	return w
}

func crimeStatesEqual(a, b *CrimeAPI) bool {
	return reflect.DeepEqual(crimeWireOf(a), crimeWireOf(b))
}

func compareCrime(t *testing.T, a, b *CrimeAPI, label string) {
	t.Helper()
	wa, wb := crimeWireOf(a), crimeWireOf(b)
	if !reflect.DeepEqual(wa.meta, wb.meta) {
		t.Fatalf("%s: meta mismatch:\n a=%+v\n b=%+v", label, wa.meta, wb.meta)
	}
	if !reflect.DeepEqual(wa.districts, wb.districts) {
		t.Fatalf("%s: districts mismatch:\n a=%+v\n b=%+v", label, wa.districts, wb.districts)
	}
	if !reflect.DeepEqual(wa.gangs, wb.gangs) {
		t.Fatalf("%s: gangs mismatch:\n a=%+v\n b=%+v", label, wa.gangs, wb.gangs)
	}
}

// ---------------------------------------------------------------------------
// Save/load drivers.
// ---------------------------------------------------------------------------

func saveIntoC(t *testing.T, a *CrimeAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-crime"}
	ckErrC(t, mgr.SaveManual(ctx, "det"))
	return root
}

func loadIntoC(t *testing.T, root string, a *CrimeAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	_, _, err := mgr.Load(manualBundleDirC(t, root))
	ckErrC(t, err)
}

// ---------------------------------------------------------------------------
// Round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestCrimeParticipant_RoundTrip(t *testing.T) {
	orig := newCrimeAPI(t)
	injectRichCrime(orig)

	root := saveIntoC(t, orig, "orig")

	reloaded := newCrimeAPI(t)
	loadIntoC(t, root, reloaded, "reloaded")
	compareCrime(t, orig, reloaded, "post-load")

	// Continue identical operations on BOTH and assert they stay equal: a
	// divergent restore would surface the moment new work builds on it.
	continueCrime(t, orig)
	continueCrime(t, reloaded)
	compareCrime(t, orig, reloaded, "post-continue")
}

// continueCrime applies one more deterministic batch through public methods,
// identical on both fixtures (same crime.json).
func continueCrime(t *testing.T, a *CrimeAPI) {
	t.Helper()
	ckErrC(t, a.RegisterDistrict(7))
	ckErrC(t, a.AdvanceMonth(4, []DistrictInput{defaultDistrict(1), defaultDistrict(7)}, SecurityInput{Exposure: 0.2, Funding: 0.1, Liaison: 0.05}))
	ckErrC(t, a.SetStrategyMix(StrategyMix{Patrol: 0.6, Detective: 0.2, Community: 0.2}))
}

// TestCrimeParticipant_ProveCanFail mutates each serialized field family on one
// pristine reload and asserts it diverges from a second pristine reload of the
// SAME bytes -- proving the comparison is discriminating for every field.
func TestCrimeParticipant_ProveCanFail(t *testing.T) {
	orig := newCrimeAPI(t)
	injectRichCrime(orig)
	root := saveIntoC(t, orig, "orig")

	cases := []struct {
		name   string
		mutate func(a *CrimeAPI)
	}{
		{"nextGangID", func(a *CrimeAPI) { a.nextGangID++ }},
		{"constabularyHQBuilt", func(a *CrimeAPI) { a.constabularyHQBuilt = false }},
		{"mix.Patrol", func(a *CrimeAPI) { a.mix.Patrol += 0.1 }},
		{"mix.Community", func(a *CrimeAPI) { a.mix.Community += 0.1 }},
		{"threat.level", func(a *CrimeAPI) { a.threat.level += 1 }},
		{"threat.elevatedMonths", func(a *CrimeAPI) { a.threat.elevatedMonths++ }},
		{"threat.lastRiseMonth", func(a *CrimeAPI) { a.threat.lastRiseMonth++ }},
		{"threat.lastEventMonth", func(a *CrimeAPI) { a.threat.lastEventMonth++ }},
		{"security.Exposure", func(a *CrimeAPI) { a.security.Exposure += 0.1 }},
		{"security.Funding", func(a *CrimeAPI) { a.security.Funding += 0.1 }},
		{"security.Liaison", func(a *CrimeAPI) { a.security.Liaison += 0.1 }},
		{"district.generation", func(a *CrimeAPI) { a.districts[2].generation[3] = 0 }},
		{"district.rawGen", func(a *CrimeAPI) { a.districts[2].rawGen[4] = 0 }},
		{"district.persisted", func(a *CrimeAPI) { a.districts[1].persisted[5] = 0 }},
		{"district.active", func(a *CrimeAPI) { a.districts[3].active[6] = 0 }},
		{"district.deterrence", func(a *CrimeAPI) { a.districts[1].deterrence = 0 }},
		{"district.clearance", func(a *CrimeAPI) { a.districts[1].clearance = 0 }},
		{"district.prevention", func(a *CrimeAPI) { a.districts[2].prevention = 0 }},
		{"district.effectiveClearance", func(a *CrimeAPI) { a.districts[3].effectiveClearance = 0 }},
		{"district.sustainedMonths", func(a *CrimeAPI) { a.districts[2].sustainedMonths = 0 }},
		{"district.eligiblePool", func(a *CrimeAPI) { a.districts[1].eligiblePool = 0 }},
		{"district.recruitedCumulative", func(a *CrimeAPI) { a.districts[3].recruitedCumulative = 0 }},
		{"justice.backlog", func(a *CrimeAPI) { a.districts[1].justice.backlog[0] = 0 }},
		{"justice.arrested", func(a *CrimeAPI) { a.districts[2].justice.arrested = nil }},
		{"justice.charged", func(a *CrimeAPI) { a.districts[2].justice.charged[0] = 0 }},
		{"justice.releasedNoCharge", func(a *CrimeAPI) { a.districts[1].justice.releasedNoCharge = nil }},
		{"justice.convicted", func(a *CrimeAPI) { a.districts[3].justice.convicted[1] = 0 }},
		{"justice.acquitted", func(a *CrimeAPI) { a.districts[1].justice.acquitted[0] = 0 }},
		{"justice.awaitingTrial", func(a *CrimeAPI) { a.districts[2].justice.awaitingTrial = nil }},
		{"justice.sentencedToPrison", func(a *CrimeAPI) { a.districts[3].justice.sentencedToPrison[0] = 0 }},
		{"justice.sentencedNonCustodial", func(a *CrimeAPI) { a.districts[1].justice.sentencedNonCustodial = nil }},
		{"justice.releasedOnBacklog", func(a *CrimeAPI) { a.districts[2].justice.releasedOnBacklog = nil }},
		{"gang.Name", func(a *CrimeAPI) { a.gangs[1].Name = "renamed" }},
		{"gang.District", func(a *CrimeAPI) { a.gangs[1].District = 999 }},
		{"gang.FormedAt", func(a *CrimeAPI) { a.gangs[2].FormedAt++ }},
		{"gang.Strength", func(a *CrimeAPI) { a.gangs[1].Strength = 0 }},
		{"gang.Territory", func(a *CrimeAPI) { a.gangs[2].Territory[1] = 0 }},
		{"gang.TaxLevyMicroPounds", func(a *CrimeAPI) { a.gangs[1].TaxLevyMicroPounds = 0 }},
		{"gang.BusinessClosures", func(a *CrimeAPI) { a.gangs[2].BusinessClosures = 0 }},
		{"gang.Recruited", func(a *CrimeAPI) { a.gangs[1].Recruited = 0 }},
		{"district.dropped", func(a *CrimeAPI) { delete(a.districts, 2) }},
		{"gang.dropped", func(a *CrimeAPI) { delete(a.gangs, 1) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := newCrimeAPI(t)
			loadIntoC(t, root, mutated, "mutated")
			pristine := newCrimeAPI(t)
			loadIntoC(t, root, pristine, "pristine")
			// Sanity: two pristine loads are equal before the mutation.
			compareCrime(t, mutated, pristine, "pre-mutation")
			c.mutate(mutated)
			if crimeStatesEqual(mutated, pristine) {
				t.Fatalf("prove-can-fail: mutating %s did not diverge from a pristine reload -- the field may be dropped or the compare is blind to it", c.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Byte determinism.
// ---------------------------------------------------------------------------

func TestCrimeParticipant_ByteDeterminism(t *testing.T) {
	a1 := newCrimeAPI(t)
	injectRichCrime(a1)
	root1 := saveIntoC(t, a1, "run1")

	a2 := newCrimeAPI(t)
	injectRichCrime(a2)
	root2 := saveIntoC(t, a2, "run2")

	assertBundlesByteIdenticalC(t, root1, root2)
}

// TestCrimeAttack_ManyKeyByteDeterminism forces MANY map keys (numeric-vs-
// lexical-divergent) into both collections and asserts two saves are
// byte-identical -- proves SORTED emission survives independent map-iteration
// orders.
func TestCrimeAttack_ManyKeyByteDeterminism(t *testing.T) {
	a1 := newCrimeAPI(t)
	injectManyKeysCrime(a1)
	root1 := saveIntoC(t, a1, "run1")

	a2 := newCrimeAPI(t)
	injectManyKeysCrime(a2)
	root2 := saveIntoC(t, a2, "run2")

	if len(a1.districts) < 20 {
		t.Fatalf("test setup: only %d districts -- too few to force map reorder", len(a1.districts))
	}
	assertBundlesByteIdenticalC(t, root1, root2)
}

// TestCrimeAttack_ManyKeyRoundTrip asserts the many-key state round-trips
// exactly.
func TestCrimeAttack_ManyKeyRoundTrip(t *testing.T) {
	orig := newCrimeAPI(t)
	injectManyKeysCrime(orig)
	root := saveIntoC(t, orig, "orig")

	reloaded := newCrimeAPI(t)
	loadIntoC(t, root, reloaded, "reloaded")

	compareCrime(t, orig, reloaded, "many-key-load")
}

// TestCrimeAttack_NumericKeyOrder asserts the snapshot emits districts and
// gangs in strictly ascending NUMERIC id order -- a lexical sort of the
// stringified uint64 keys would place 10 before 2 and would redden this. The
// key set is chosen so numeric and lexical orders genuinely diverge.
func TestCrimeAttack_NumericKeyOrder(t *testing.T) {
	a := newCrimeAPI(t)
	injectManyKeysCrime(a)

	snap, err := a.snapshotForSave()
	ckErrC(t, err)

	if len(snap.districts) < 3 {
		t.Fatalf("test setup: need several districts, got %d", len(snap.districts))
	}
	for i := 1; i < len(snap.districts); i++ {
		if snap.districts[i-1].ID >= snap.districts[i].ID {
			t.Fatalf("districts not in ascending numeric id order at %d: %d then %d", i, snap.districts[i-1].ID, snap.districts[i].ID)
		}
	}
	for i := 1; i < len(snap.gangs); i++ {
		if snap.gangs[i-1].ID >= snap.gangs[i].ID {
			t.Fatalf("gangs not in ascending numeric id order at %d: %d then %d", i, snap.gangs[i-1].ID, snap.gangs[i].ID)
		}
	}
	// Prove numeric != lexical for this key set: the emitted (numeric-ordered)
	// sequence of stringified ids must NOT itself be in lexical order, so a
	// lexical sort would genuinely have produced a different sequence and this
	// test could catch it. (e.g. numeric ...9,10... stringifies to "9","10"
	// which is descending lexically.)
	diverges := false
	for i := 1; i < len(snap.districts); i++ {
		if itoa(snap.districts[i].ID) < itoa(snap.districts[i-1].ID) {
			diverges = true
			break
		}
	}
	if !diverges {
		t.Fatalf("test setup weak: the numeric-ordered id sequence is also lexically sorted -- numeric and lexical orders do not diverge, so this test could not catch a lexical sort")
	}
}

func itoa(id DistrictID) string {
	if id == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for id > 0 {
		i--
		b[i] = byte('0' + id%10)
		id /= 10
	}
	return string(b[i:])
}

// ---------------------------------------------------------------------------
// Load-into-non-empty (full replace, not merge) + copyguard + unknown kind.
// ---------------------------------------------------------------------------

// TestCrimeAttack_LoadIntoNonEmptyFullyReplaces: a Load into a CrimeAPI that
// already holds DIFFERENT runtime state must fully overwrite it (Handler
// resets), never merge.
func TestCrimeAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig := newCrimeAPI(t)
	injectRichCrime(orig)
	root := saveIntoC(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger state (the many-key
	// block), including GHOST entries the saved state never touches.
	target := newCrimeAPI(t)
	injectManyKeysCrime(target)
	const ghostDistrict DistrictID = 30001
	const ghostGang GangID = 200
	if _, ok := orig.districts[ghostDistrict]; ok {
		t.Fatalf("test setup: saved state unexpectedly holds the ghost district")
	}
	if _, ok := target.districts[ghostDistrict]; !ok {
		t.Fatalf("test setup: ghost district not present on target pre-load")
	}
	if _, ok := target.gangs[ghostGang]; !ok {
		t.Fatalf("test setup: ghost gang not present on target pre-load")
	}

	loadIntoC(t, root, target, "target")

	if _, ok := target.districts[ghostDistrict]; ok {
		t.Fatalf("ghost district survived load -- Handler merged instead of replacing")
	}
	if _, ok := target.gangs[ghostGang]; ok {
		t.Fatalf("ghost gang survived load -- Handler merged instead of replacing")
	}
	if len(target.districts) != len(orig.districts) {
		t.Fatalf("districts size %d != saved %d -- merge, not replace", len(target.districts), len(orig.districts))
	}
	if len(target.gangs) != len(orig.gangs) {
		t.Fatalf("gangs size %d != saved %d -- merge, not replace", len(target.gangs), len(orig.gangs))
	}
	compareCrime(t, orig, target, "load-into-nonempty")
}

// TestCrimeAttack_CopyguardFiresOnParticipant: a struct-copied CrimeAPI's
// participant must fail closed on Kind/Source/Handler.
func TestCrimeAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig := newCrimeAPI(t)
	injectRichCrime(orig)

	// Reproduce a struct-copied CrimeAPI's guard-visible state (self still
	// points at the ORIGINAL) without a vet-copylocks-tripping value copy of
	// the embedded RWMutex.
	var copied CrimeAPI
	copied.self.Store(orig)
	sp := NewSaveParticipant(&copied)

	if sp.Kind() != "" {
		t.Fatalf("copied participant Kind() = %q, want empty (guard should fire)", sp.Kind())
	}
	src := sp.Source()
	if _, _, err := src(); err == nil {
		t.Fatalf("copied participant Source() first pull returned nil error -- guard did not fire")
	}
	h := sp.Handler()
	if err := h(serialize.Record{}); err == nil {
		t.Fatalf("copied participant Handler() returned nil error -- guard did not fire")
	}
	// And the ORIGINAL still works.
	if NewSaveParticipant(orig).Kind() != KindCrime {
		t.Fatalf("original participant Kind() broken")
	}
}

// TestCrimeAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestCrimeAttack_UnknownRecordKindRejected(t *testing.T) {
	a := newCrimeAPI(t)
	h := NewSaveParticipant(a).Handler()
	if err := h(serialize.Record{Kind: "crime.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the refuse/citizens pilots).
// ---------------------------------------------------------------------------

func assertBundlesByteIdenticalC(t *testing.T, root1, root2 string) {
	t.Helper()
	dir1 := manualBundleDirC(t, root1)
	dir2 := manualBundleDirC(t, root2)
	files1 := allFilesC(t, dir1)
	files2 := allFilesC(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ckErrC(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ckErrC(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic crime state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

func manualBundleDirC(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "header.json" {
			found = filepath.Dir(path)
		}
		return nil
	})
	ckErrC(t, err)
	if found == "" {
		t.Fatalf("no bundle (header.json) found under %q", root)
	}
	return found
}

func allFilesC(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
	})
	ckErrC(t, err)
	sort.Strings(out)
	return out
}
