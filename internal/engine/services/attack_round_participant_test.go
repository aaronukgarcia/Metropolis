package services

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Independent destructive round (GR#23) against the FEAT-2326609743
// engine.services save-participant estate. NOT authored by the participant's
// author.

// drain pulls every record out of a participant's Source, in order.
func drain(t *testing.T, p *SaveParticipant) []serialize.Record {
	t.Helper()
	src := p.Source()
	var out []serialize.Record
	for {
		rec, ok, err := src()
		if err != nil {
			t.Fatalf("Source: %v", err)
		}
		if !ok {
			return out
		}
		out = append(out, rec)
	}
}

// shardBytes renders a participant's whole stream as raw NDJSON bytes (the
// thing that actually lands on disk), so a determinism check compares BYTES
// not just reconstructed state.
func shardBytes(t *testing.T, p *SaveParticipant) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := (serialize.NDJSONSerializer{}).WriteShard(&buf, serialize.ShardMeta{Name: "services", Kind: "services", Encoding: "ndjson+gzip"}, p.Source()); err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	return buf.Bytes()
}

func replay(t *testing.T, dst *ServicesAPI, recs []serialize.Record) error {
	t.Helper()
	h := NewSaveParticipant(dst).Handler()
	for _, r := range recs {
		if err := h(r); err != nil {
			return err
		}
	}
	return nil
}

// loadedAPI is a *ServicesAPI carrying the data-driven config, as a real
// composition builds it.
func loadedAPI(t *testing.T) *ServicesAPI {
	t.Helper()
	a, err := LoadDefault("attack-round")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return a
}

// populate drives a rich, hostile-but-legal state through the PUBLIC command
// surface only — nothing is poked into unexported fields, so every value the
// round-trip must preserve is one a real caller can actually produce.
func populate(t *testing.T, a *ServicesAPI) {
	t.Helper()
	if err := a.SetUnlockGate(UnlockGateFunc(func(int) bool { return true })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	// A SYNTHETIC kind (AC-2): the class a fresh New()'s built-in reseed
	// cannot reproduce.
	if err := a.RegisterKind("space-elevator", KindDef{Name: "Space Elevator", Benchmark: "police"}); err != nil {
		t.Fatalf("RegisterKind: %v", err)
	}
	// A built-in kind DEFINITION overridden at runtime — also unreproducible
	// from New()'s defaults.
	if err := a.RegisterKind(ServiceFire, KindDef{Name: "Fire (renamed)", Benchmark: "teachers"}); err != nil {
		t.Fatalf("RegisterKind(override): %v", err)
	}

	specs := []ServiceSpec{
		{
			ID: "svc-b", Kind: ServiceHealthcare, CapacityRaw: "150 visits/d",
			CoverageRadius: 12.5, X: -3.25, Y: 7.75, Milestone: 2, StaffingNeed: 40,
			UpgradePath: []UpgradeStep{
				{BuildingID: "clinic", Name: "Clinic", Milestone: 2, CapacityCeiling: 150},
				{BuildingID: "hospital", Name: "Hospital", Milestone: 6, CapacityCeiling: 900},
			},
		},
		{
			ID: "svc-a", Kind: ServiceFire, CapacityRaw: "4 appliances",
			CoverageRadius: 20, X: 1, Y: 2, Milestone: 1, StaffingNeed: 25,
			UpgradePath: []UpgradeStep{{BuildingID: "firestation", Name: "Fire Station", Milestone: 1, CapacityCeiling: 4}},
		},
		{
			// No upgrade path at all — the len(path)==0 branch.
			ID: "svc-c", Kind: "space-elevator", CapacityRaw: "",
			CoverageRadius: 0, X: 0, Y: 0, Milestone: 0, StaffingNeed: 0,
		},
	}
	for _, s := range specs {
		if err := a.RegisterService(s); err != nil {
			t.Fatalf("RegisterService(%s): %v", s.ID, err)
		}
	}
	if err := a.Upgrade("svc-b"); err != nil { // currentUpgrade -> 1
		t.Fatalf("Upgrade: %v", err)
	}
	if err := a.SetFunding("svc-b", 0.625); err != nil {
		t.Fatalf("SetFunding: %v", err)
	}
	if err := a.SetFunding("svc-a", 1); err != nil {
		t.Fatalf("SetFunding: %v", err)
	}
	if err := a.UpdateDemand("svc-b", 321.5, 4.25); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}
	if err := a.UpdateDemand("svc-a", 12, 0.5); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}
	if err := a.UpdateStaffing("svc-a", 33); err != nil {
		t.Fatalf("UpdateStaffing: %v", err)
	}
	if err := a.UpdateDistrictDemand("D-north", "svc-b", 100, 2); err != nil {
		t.Fatalf("UpdateDistrictDemand: %v", err)
	}
	if err := a.UpdateDistrictDemand("D-north", "svc-a", 5, 1); err != nil {
		t.Fatalf("UpdateDistrictDemand: %v", err)
	}
	if err := a.UpdateDistrictDemand("A-south", "svc-b", 7, 9); err != nil {
		t.Fatalf("UpdateDistrictDemand: %v", err)
	}
	// Pool availability + a real allocation, so `allocated` is non-zero.
	pools := a.poolIDsForTest()
	if len(pools) == 0 {
		t.Fatalf("test setup: data/services.json declared no staffing pools")
	}
	for i, p := range pools {
		if err := a.SetPoolStaff(p, float64(10+i)); err != nil {
			t.Fatalf("SetPoolStaff(%s): %v", p, err)
		}
		if _, err := a.AllocateStaffing(p); err != nil {
			t.Fatalf("AllocateStaffing(%s): %v", p, err)
		}
	}
}

// poolIDsForTest exposes the loaded pool ids to this file's tests.
func (a *ServicesAPI) poolIDsForTest() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.pools))
	for _, p := range a.pools {
		out = append(out, p.ID)
	}
	return out
}

// stateFingerprint reads back EVERY observable piece of mutable state through
// the public accessors plus the unexported instance fields, so a dropped wire
// field shows up as a mismatch rather than passing silently.
func stateFingerprint(t *testing.T, a *ServicesAPI) map[string]any {
	t.Helper()
	out := map[string]any{}
	kinds, err := a.ServiceKinds()
	if err != nil {
		t.Fatalf("ServiceKinds: %v", err)
	}
	for _, k := range kinds {
		def, ok := a.KindDef(k)
		out["kind:"+string(k)] = [3]any{ok, def.Name, def.Benchmark}
	}
	out["kindCount"] = len(kinds)

	ids, err := a.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	out["ids"] = ids
	a.mu.RLock()
	for _, id := range ids {
		inst := a.instances[id]
		out["inst:"+string(id)] = [6]any{inst.spec, inst.currentUpgrade, inst.funding, inst.demand, inst.demandDist, inst.allocated}
	}
	a.mu.RUnlock()

	districts, err := a.DistrictIDs()
	if err != nil {
		t.Fatalf("DistrictIDs: %v", err)
	}
	out["districts"] = districts
	cov, err := a.CoverageByDistrict()
	if err != nil {
		t.Fatalf("CoverageByDistrict: %v", err)
	}
	out["coverageByDistrict"] = cov
	summary, err := a.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	out["summary"] = summary
	for _, p := range a.poolIDsForTest() {
		al, err := a.StaffingAllocations(p)
		if err != nil {
			t.Fatalf("StaffingAllocations(%s): %v", p, err)
		}
		out["alloc:"+p] = al
	}
	return out
}

// TestAttack_RoundTripPreservesEveryObservable is the completeness attack: a
// rich state produced entirely through the public command surface must come
// back byte-for-byte identical in every observable.
func TestAttack_RoundTripPreservesEveryObservable(t *testing.T) {
	src := loadedAPI(t)
	populate(t, src)
	before := stateFingerprint(t, src)

	dst := loadedAPI(t)
	if err := replay(t, dst, drain(t, NewSaveParticipant(src))); err != nil {
		t.Fatalf("replay: %v", err)
	}
	after := stateFingerprint(t, dst)

	for k, want := range before {
		got, ok := after[k]
		if !ok {
			t.Errorf("ROUND-TRIP LOSS: key %q absent after load", k)
			continue
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("ROUND-TRIP DIVERGENCE %q:\n before=%#v\n  after=%#v", k, want, got)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("ROUND-TRIP FABRICATION: key %q appeared after load", k)
		}
	}
}

// TestAttack_SaveLoadSaveByteIdentical is the GR#21 determinism attack at the
// level that matters: the SHARD BYTES, not the reconstructed state.
func TestAttack_SaveLoadSaveByteIdentical(t *testing.T) {
	src := loadedAPI(t)
	populate(t, src)
	first := shardBytes(t, NewSaveParticipant(src))

	dst := loadedAPI(t)
	if err := replay(t, dst, drain(t, NewSaveParticipant(src))); err != nil {
		t.Fatalf("replay: %v", err)
	}
	second := shardBytes(t, NewSaveParticipant(dst))
	if !bytes.Equal(first, second) {
		t.Fatalf("SHARD BYTES DIVERGED across save/load/save\nfirst=%d bytes\nsecond=%d bytes", len(first), len(second))
	}
	// And a repeat save of the SAME instance must be byte-stable too (map
	// iteration order varies per range in Go).
	for i := 0; i < 25; i++ {
		if again := shardBytes(t, NewSaveParticipant(src)); !bytes.Equal(first, again) {
			t.Fatalf("SHARD BYTES NONDETERMINISTIC on repeat save (iteration %d)", i)
		}
	}
}

// TestAttack_InsertionOrderIndependence: two APIs reaching the same logical
// state by different insertion orders must emit identical bytes.
func TestAttack_InsertionOrderIndependence(t *testing.T) {
	a := loadedAPI(t)
	b := loadedAPI(t)
	gate := UnlockGateFunc(func(int) bool { return true })
	if err := a.SetUnlockGate(gate); err != nil {
		t.Fatal(err)
	}
	if err := b.SetUnlockGate(gate); err != nil {
		t.Fatal(err)
	}
	mk := func(id ServiceID) ServiceSpec {
		return ServiceSpec{ID: id, Kind: ServiceHealthcare, CoverageRadius: 5, StaffingNeed: 1,
			UpgradePath: []UpgradeStep{{BuildingID: "clinic", CapacityCeiling: 10}}}
	}
	fwd := []ServiceID{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, id := range fwd {
		if err := a.RegisterService(mk(id)); err != nil {
			t.Fatal(err)
		}
		if err := a.UpdateDistrictDemand(DistrictID("d-"+id), id, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(fwd) - 1; i >= 0; i-- {
		id := fwd[i]
		if err := b.RegisterService(mk(id)); err != nil {
			t.Fatal(err)
		}
		if err := b.UpdateDistrictDemand(DistrictID("d-"+id), id, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(shardBytes(t, NewSaveParticipant(a)), shardBytes(t, NewSaveParticipant(b))) {
		t.Fatal("INSERTION-ORDER DEPENDENCE: identical logical states emitted different shard bytes")
	}
}

// TestAttack_EmptyDistrictKeySurvivesLiveButNotLoad is BUG-594, found by the
// independent round on this participant and CHARACTERISED here rather than
// silently tolerated: UnregisterService (build's SubmitDemolishCommand path)
// deletes a demolished service's record from every district's inner map but
// leaves the DISTRICT KEY behind with an empty map. Live, that district is
// still enumerated by DistrictIDs and still resolves through
// CoverageForDistrict. The participant emits one record per (district,
// service) PAIR, so an empty inner map emits nothing and the key does NOT
// survive a load — an observable behaviour change across a restore, with no
// hand-corrupted save involved.
//
// It is filed P3 (BUG-594), not a blocker: nothing in the repo drives
// UpdateDistrictDemand today, and the divergence direction is benign (the
// post-load state is the cleaner one). The root fix belongs in
// UnregisterService, not here. This test asserts the CURRENT behaviour so the
// divergence cannot widen unnoticed — INVERT it when BUG-594 lands.
func TestAttack_EmptyDistrictKeySurvivesLiveButNotLoad(t *testing.T) {
	src := loadedAPI(t)
	if err := src.RegisterService(ServiceSpec{ID: "clinic-1", Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{{BuildingID: "clinic", CapacityCeiling: 10}}}); err != nil {
		t.Fatal(err)
	}
	if err := src.UpdateDistrictDemand("D1", "clinic-1", 50, 1); err != nil {
		t.Fatal(err)
	}
	// Demolition: exactly what build's SubmitDemolishCommand drives.
	if err := src.UnregisterService("clinic-1"); err != nil {
		t.Fatal(err)
	}

	liveIDs, err := src.DistrictIDs()
	if err != nil {
		t.Fatal(err)
	}
	_, liveErr := src.CoverageForDistrict("D1")

	dst := loadedAPI(t)
	if err := replay(t, dst, drain(t, NewSaveParticipant(src))); err != nil {
		t.Fatalf("replay: %v", err)
	}
	loadedIDs, err := dst.DistrictIDs()
	if err != nil {
		t.Fatal(err)
	}
	_, loadedErr := dst.CoverageForDistrict("D1")

	t.Logf("BUG-594 live: DistrictIDs=%v CoverageForDistrict(D1) err=%v", liveIDs, liveErr)
	t.Logf("BUG-594 loaded: DistrictIDs=%v CoverageForDistrict(D1) err=%v", loadedIDs, loadedErr)

	// Current behaviour, characterised (invert both halves when BUG-594 is
	// fixed in UnregisterService).
	if !reflect.DeepEqual(liveIDs, []DistrictID{"D1"}) || liveErr != nil {
		t.Fatalf("BUG-594 characterisation stale (live side changed): DistrictIDs=%v err=%v — re-audit and update or invert this test", liveIDs, liveErr)
	}
	if len(loadedIDs) != 0 || loadedErr == nil {
		t.Fatalf("BUG-594 characterisation stale (load side changed): DistrictIDs=%v err=%v — if the empty district key now round-trips, BUG-594 is fixed: invert this test", loadedIDs, loadedErr)
	}
}

// TestAttack_CorruptedRecordsRejected: every hostile record shape must fail
// LOUD, never install silently.
func TestAttack_CorruptedRecordsRejected(t *testing.T) {
	cases := []struct {
		name string
		recs []serialize.Record
	}{
		{"out-of-range-coverage-radius", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
			{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","coverageRadius":1e999}`)},
		}},
		{"funding-out-of-range", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
			{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","funding":2}`)},
		}},
		{"negative-funding", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
			{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","funding":-0.0001}`)},
		}},
		{"unknown-kind-reference", []serialize.Record{
			{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare"}`)},
		}},
		{"empty-kind", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{"kind":"","name":"H"}`)},
		}},
		{"upgrade-index-past-end", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
			{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","upgradePath":[{"buildingID":"c","capacityCeiling":1}],"currentUpgrade":1}`)},
		}},
		{"upgrade-index-negative", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
			{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","currentUpgrade":-1}`)},
		}},
		{"unknown-record-kind", []serialize.Record{
			{Kind: "services.whatever", Data: []byte(`{}`)},
		}},
		{"malformed-json", []serialize.Record{
			{Kind: recServicesKind, Data: []byte(`{`)},
		}},
		{"nan-pool", []serialize.Record{
			{Kind: recServicesPool, Data: []byte(`{"poolID":"p","available":1e999}`)},
		}},
		{"nan-district-demand", []serialize.Record{
			{Kind: recServicesDistrictDemand, Data: []byte(`{"district":"d","service":"s","demand":1e999}`)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := loadedAPI(t)
			if err := replay(t, dst, tc.recs); err == nil {
				t.Fatalf("SILENT ACCEPT of corrupted record set %q", tc.name)
			}
		})
	}
}

// TestAttack_NonFiniteNeverSurvivesToArithmetic: even the paths the load-time
// guard does not name explicitly must not land NaN in the state.
func TestAttack_NonFiniteInSpecFieldsRejected(t *testing.T) {
	for _, field := range []string{"x", "y", "staffingNeed", "demand", "demandDist", "allocated"} {
		t.Run(field, func(t *testing.T) {
			dst := loadedAPI(t)
			recs := []serialize.Record{
				{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
				{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","` + field + `":1e999}`)},
			}
			if err := replay(t, dst, recs); err == nil {
				t.Fatalf("SILENT ACCEPT of non-finite %s", field)
			}
		})
	}
	// Upgrade-step capacity ceiling.
	dst := loadedAPI(t)
	recs := []serialize.Record{
		{Kind: recServicesKind, Data: []byte(`{"kind":"healthcare","name":"H"}`)},
		{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","upgradePath":[{"buildingID":"c","capacityCeiling":1e999}]}`)},
	}
	if err := replay(t, dst, recs); err == nil {
		t.Fatal("SILENT ACCEPT of non-finite upgradePath.capacityCeiling")
	}
}

// TestAttack_HandlerWipesLiveStateWholesale is the anti-merge attack: a load
// into a POPULATED api must REPLACE, never merge.
func TestAttack_HandlerWipesLiveStateWholesale(t *testing.T) {
	src := loadedAPI(t)
	if err := src.RegisterService(ServiceSpec{ID: "kept", Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{{BuildingID: "c", CapacityCeiling: 1}}}); err != nil {
		t.Fatal(err)
	}
	recs := drain(t, NewSaveParticipant(src))

	dst := loadedAPI(t)
	if err := dst.RegisterKind("phantom-kind", KindDef{Name: "Phantom"}); err != nil {
		t.Fatal(err)
	}
	if err := dst.RegisterService(ServiceSpec{ID: "phantom", Kind: "phantom-kind",
		UpgradePath: []UpgradeStep{{BuildingID: "p", CapacityCeiling: 999}}}); err != nil {
		t.Fatal(err)
	}
	if err := dst.UpdateDistrictDemand("phantom-district", "phantom", 10, 1); err != nil {
		t.Fatal(err)
	}
	for _, p := range dst.poolIDsForTest() {
		if err := dst.SetPoolStaff(p, 999); err != nil {
			t.Fatal(err)
		}
	}
	if err := replay(t, dst, recs); err != nil {
		t.Fatalf("replay: %v", err)
	}
	ids, _ := dst.ServiceIDs()
	if len(ids) != 1 || ids[0] != "kept" {
		t.Fatalf("PHANTOM SURVIVED a load: ServiceIDs=%v", ids)
	}
	if _, ok := dst.KindDef("phantom-kind"); ok {
		t.Fatal("PHANTOM KIND survived a load")
	}
	ds, _ := dst.DistrictIDs()
	if len(ds) != 0 {
		t.Fatalf("PHANTOM DISTRICT survived a load: %v", ds)
	}
	for _, p := range dst.poolIDsForTest() {
		if got := dst.poolAvailableForTest(p); got != 0 {
			t.Fatalf("PHANTOM POOL AVAILABILITY survived a load: %s=%v", p, got)
		}
	}
}

func (a *ServicesAPI) poolAvailableForTest(id string) float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.poolAvailable[id]
}

// TestAttack_MutableFieldParity is a drift canary: if anyone adds a field to
// ServicesAPI / serviceInstance / ServiceSpec / UpgradeStep / KindDef /
// demandRecord and does not extend the wire, this fails.
func TestAttack_MutableFieldParity(t *testing.T) {
	cases := []struct {
		name    string
		typ     reflect.Type
		fields  []string
		comment string
	}{
		{"ServicesAPI", reflect.TypeOf(ServicesAPI{}), []string{
			"mu", "correlationID", "kinds", "instances", "districtDemand",
			"pools", "poolAvailable", "pie", "wagePerStaffMicropounds",
			"severityHalfPoint", "gate", "self",
		}, "kinds/instances/districtDemand/poolAvailable are wired; the rest are config/injected/runtime"},
		{"serviceInstance", reflect.TypeOf(serviceInstance{}), []string{
			"spec", "currentUpgrade", "funding", "demand", "demandDist", "allocated",
		}, "all wired"},
		{"ServiceSpec", reflect.TypeOf(ServiceSpec{}), []string{
			"ID", "Kind", "CapacityRaw", "CoverageRadius", "X", "Y", "Milestone", "StaffingNeed", "UpgradePath",
		}, "all wired"},
		{"UpgradeStep", reflect.TypeOf(UpgradeStep{}), []string{
			"BuildingID", "Name", "Milestone", "CapacityCeiling",
		}, "all wired"},
		{"KindDef", reflect.TypeOf(KindDef{}), []string{"Name", "Benchmark"}, "all wired"},
		{"demandRecord", reflect.TypeOf(demandRecord{}), []string{"demand", "distance"}, "all wired"},
	}
	for _, tc := range cases {
		got := make([]string, 0, tc.typ.NumField())
		for i := 0; i < tc.typ.NumField(); i++ {
			got = append(got, tc.typ.Field(i).Name)
		}
		if !reflect.DeepEqual(got, tc.fields) {
			t.Errorf("FIELD DRIFT on %s: %v (expected %v) — re-audit participant.go's wire (%s)",
				tc.name, got, tc.fields, tc.comment)
		}
	}
	// UpgradeStep <-> upgradeStepWire conversion depends on an identical
	// field SEQUENCE; prove it rather than trusting the compiler's silence
	// on a same-shape reorder.
	dom, wire := reflect.TypeOf(UpgradeStep{}), reflect.TypeOf(upgradeStepWire{})
	if dom.NumField() != wire.NumField() {
		t.Fatalf("UpgradeStep/upgradeStepWire arity drift")
	}
	for i := 0; i < dom.NumField(); i++ {
		if dom.Field(i).Name != wire.Field(i).Name || dom.Field(i).Type != wire.Field(i).Type {
			t.Errorf("UpgradeStep/upgradeStepWire field %d drift: %v vs %v", i, dom.Field(i), wire.Field(i))
		}
	}
}

// TestAttack_ZeroRecordShardLeavesLiveStateIntact documents the reset-on-
// first-record design: a shard with NO records never fires resetForLoad.
func TestAttack_ZeroRecordShardLeavesLiveStateIntact(t *testing.T) {
	dst := loadedAPI(t)
	if err := dst.RegisterService(ServiceSpec{ID: "survivor", Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{{BuildingID: "c", CapacityCeiling: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := replay(t, dst, nil); err != nil {
		t.Fatal(err)
	}
	ids, _ := dst.ServiceIDs()
	if len(ids) != 1 {
		t.Fatalf("unexpected: %v", ids)
	}
	// Guard the unreachability argument: a real Source can never be empty,
	// because kinds is never empty.
	fresh := New("x")
	if n := len(drain(t, NewSaveParticipant(fresh))); n == 0 {
		t.Fatal("REACHABLE ZERO-RECORD SHARD: a fresh ServicesAPI emits no records, so a load of it would NOT reset the target's live state")
	}
}

// TestAttack_CopiedValueFailsClosedThroughParticipant: SEC-020 through the
// new surface.
func TestAttack_CopiedValueFailsClosedThroughParticipant(t *testing.T) {
	orig := loadedAPI(t)
	if err := orig.RegisterService(ServiceSpec{ID: "s", Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{{BuildingID: "c", CapacityCeiling: 1}}}); err != nil {
		t.Fatal(err)
	}
	cp := &ServicesAPI{}
	cp.kinds = orig.kinds
	cp.instances = orig.instances
	cp.districtDemand = orig.districtDemand
	cp.poolAvailable = orig.poolAvailable
	// self is NOT armed for cp => every guarded method must refuse.
	p := NewSaveParticipant(cp)
	if k := p.Kind(); k != "" {
		t.Fatalf("copied api yielded a live shard kind %q", k)
	}
	if _, _, err := p.Source()(); err == nil {
		t.Fatal("copied api streamed records instead of failing closed")
	}
	if err := p.Handler()(serialize.Record{Kind: recServicesPool, Data: []byte(`{"poolID":"p","available":1}`)}); err == nil {
		t.Fatal("copied api accepted a load record instead of failing closed")
	}
}
