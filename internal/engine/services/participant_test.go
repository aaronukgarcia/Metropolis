package services

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
// FEAT-build-services-bridge-2026-09-02 round remedy (root fix) — engine.
// services save.Participant tests. Mirrors the engine.crime participant test
// suite exactly, adapted to engine.services' mutable state (the kind
// registry, the per-instance spec/funding/upgrade/demand/allocated state,
// the caller-pushed per-district demand table, and the per-pool availability
// input). Mandatory shapes: field-parity drift (ServicesAPI + serviceInstance
// + ServiceSpec), full round-trip + continue + prove-can-fail per field, byte
// determinism, load-into-non-empty (replace not merge), copyguard fires,
// unknown-record-kind rejection, GR#16 load-time validation, and the round's
// own named defect: a live-composition rewind must not leave a phantom
// service instance.
// ---------------------------------------------------------------------------

func ckErrS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newServicesAPI(t *testing.T) *ServicesAPI {
	t.Helper()
	return New("services-save-test")
}

// ---------------------------------------------------------------------------
// Field-parity drift tests (the "built but not serialized" guard).
// ---------------------------------------------------------------------------

// TestServicesAPIFieldsAllClassified fails the build if any ServicesAPI field
// is neither serialized (covered) nor explicitly excluded (config/injected-
// dep/runtime/copy-guard). A new mutable field added without a save is
// exactly the class this participant exists to prevent.
func TestServicesAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"mu":                      "runtime lock, not state",
		"correlationID":           "per-instance error correlation, not simulation state",
		"pools":                   "immutable config (data/services.json staffingPools), reloaded by Load -- a save must not pin old balance rules",
		"pie":                     "immutable config (data/services.json pie.benchmarks), reloaded by Load",
		"wagePerStaffMicropounds": "immutable config (data/services.json), reloaded by Load",
		"severityHalfPoint":       "immutable config (data/services.json), reloaded by Load",
		"gate":                    "injected dependency (UnlockGate interface), re-wired by the composition root via SetUnlockGate on load",
		"self":                    "SEC-020 copy-guard pointer, re-armed by New/Load",
	}
	covered := map[string]bool{
		"kinds":          true,
		"instances":      true,
		"districtDemand": true,
		"poolAvailable":  true,
	}
	rt := reflect.TypeOf((*ServicesAPI)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("ServicesAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason)", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("ServicesAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestServiceInstanceFieldsCovered asserts every serviceInstance field
// (including every ServiceSpec field, nested by value) is carried on the
// wire.
func TestServiceInstanceFieldsCovered(t *testing.T) {
	it := reflect.TypeOf((*serviceInstance)(nil)).Elem()
	wantInstance := []string{"spec", "currentUpgrade", "funding", "demand", "demandDist", "allocated"}
	if it.NumField() != len(wantInstance) {
		t.Fatalf("serviceInstance has %d fields but %d are expected -- a field was added without updating this test AND the wire projection", it.NumField(), len(wantInstance))
	}
	for _, name := range wantInstance {
		if _, ok := it.FieldByName(name); !ok {
			t.Fatalf("serviceInstance is missing expected field %q", name)
		}
	}

	st := reflect.TypeOf((*ServiceSpec)(nil)).Elem()
	wantSpec := []string{"ID", "Kind", "CapacityRaw", "CoverageRadius", "X", "Y", "Milestone", "StaffingNeed", "UpgradePath"}
	if st.NumField() != len(wantSpec) {
		t.Fatalf("ServiceSpec has %d fields but %d are expected -- a field was added without updating this test AND the wire projection", st.NumField(), len(wantSpec))
	}

	wt := reflect.TypeOf((*serviceInstanceWire)(nil)).Elem()
	wantWire := []string{"ID", "Kind", "CapacityRaw", "CoverageRadius", "X", "Y", "Milestone", "StaffingNeed", "UpgradePath", "CurrentUpgrade", "Funding", "Demand", "DemandDist", "Allocated"}
	for _, name := range wantWire {
		if _, ok := wt.FieldByName(name); !ok {
			t.Fatalf("serviceInstanceWire is missing field %q", name)
		}
	}
}

// TestUpgradeStepFieldsCovered guards the nested upgrade-step wire against
// drift.
func TestUpgradeStepFieldsCovered(t *testing.T) {
	ut := reflect.TypeOf((*UpgradeStep)(nil)).Elem()
	want := []string{"BuildingID", "Name", "Milestone", "CapacityCeiling"}
	if ut.NumField() != len(want) {
		t.Fatalf("UpgradeStep has %d fields but %d are expected", ut.NumField(), len(want))
	}
	wt := reflect.TypeOf((*upgradeStepWire)(nil)).Elem()
	for _, name := range want {
		if _, ok := wt.FieldByName(name); !ok {
			t.Fatalf("upgradeStepWire is missing field %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Rich-state builders (DISTINCT, NON-ZERO values for EVERY field).
// ---------------------------------------------------------------------------

func richUpgradePath(idx int) []UpgradeStep {
	return []UpgradeStep{
		{BuildingID: "clinic-1", Name: "Small Clinic", Milestone: 1, CapacityCeiling: float64(idx*10 + 50)},
		{BuildingID: "hospital-1", Name: "General Hospital", Milestone: 3, CapacityCeiling: float64(idx*10 + 500)},
	}
}

func richInstance(id ServiceID, kind ServiceKind, idx int) *serviceInstance {
	f := float64(idx)
	return &serviceInstance{
		spec: ServiceSpec{
			ID:             id,
			Kind:           kind,
			CapacityRaw:    "150 visits/d",
			CoverageRadius: f*2 + 3.5,
			X:              f * 11,
			Y:              f*13 + 1,
			Milestone:      1,
			StaffingNeed:   f*4 + 2.5,
			UpgradePath:    richUpgradePath(idx),
		},
		currentUpgrade: 1,
		funding:        0.1 * f,
		demand:         f*7 + 1,
		demandDist:     f*0.5 + 0.25,
		allocated:      f*3 + 0.75,
	}
}

// injectRichServices installs distinct non-zero kinds, instances (with a
// full upgrade path), district demand, and pool-availability state — the
// whole serialized surface, no field zero.
func injectRichServices(a *ServicesAPI) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.kinds = map[ServiceKind]KindDef{
		ServiceHealthcare: {Name: "Healthcare", Benchmark: "nursesGps"},
		ServiceFire:       {Name: "Fire", Benchmark: "firefighters"},
		"custom-kind":     {Name: "Synthetic Kind", Benchmark: "customBenchmark"},
	}

	a.instances = map[ServiceID]*serviceInstance{
		"clinic-1":  richInstance("clinic-1", ServiceHealthcare, 1),
		"station-1": richInstance("station-1", ServiceFire, 2),
	}

	a.districtDemand = map[DistrictID]map[ServiceID]demandRecord{
		"district-a": {
			"clinic-1":  {demand: 30, distance: 1.5},
			"station-1": {demand: 12, distance: 0.5},
		},
		"district-b": {
			"clinic-1": {demand: 40, distance: 2.5},
		},
	}

	a.poolAvailable = map[string]float64{
		"nursing": 12.5,
		"police":  8.25,
	}
}

// injectManyKeysServices forces many entries into every map-backed
// collection so a lexical-vs-insertion-order bug in the emission sort would
// be caught.
func injectManyKeysServices(a *ServicesAPI) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.kinds = make(map[ServiceKind]KindDef)
	kindIDs := []ServiceKind{"k10", "k2", "k1", "k20", "k3", "k100", ServiceHealthcare}
	for i, k := range kindIDs {
		a.kinds[k] = KindDef{Name: string(k), Benchmark: "bench"}
		_ = i
	}

	a.instances = make(map[ServiceID]*serviceInstance)
	svcIDs := []ServiceID{"svc10", "svc2", "svc1", "svc20", "svc3", "svc100"}
	for i, id := range svcIDs {
		a.instances[id] = richInstance(id, ServiceHealthcare, i+1)
	}

	a.districtDemand = make(map[DistrictID]map[ServiceID]demandRecord)
	distIDs := []DistrictID{"d10", "d2", "d1", "d20"}
	for _, d := range distIDs {
		a.districtDemand[d] = map[ServiceID]demandRecord{}
		for i, id := range svcIDs {
			a.districtDemand[d][id] = demandRecord{demand: float64(i + 1), distance: float64(i) * 0.5}
		}
	}

	a.poolAvailable = map[string]float64{"pool10": 1, "pool2": 2, "pool1": 3, "pool20": 4}
}

// ---------------------------------------------------------------------------
// Comparison via the wire projection.
// ---------------------------------------------------------------------------

func servicesWireOf(t *testing.T, a *ServicesAPI) servicesSnapshot {
	t.Helper()
	snap, err := a.snapshotForSave()
	ckErrS(t, err)
	return snap
}

func compareServices(t *testing.T, a, b *ServicesAPI, label string) {
	t.Helper()
	wa, wb := servicesWireOf(t, a), servicesWireOf(t, b)
	if !reflect.DeepEqual(wa.kinds, wb.kinds) {
		t.Fatalf("%s: kinds mismatch:\n a=%+v\n b=%+v", label, wa.kinds, wb.kinds)
	}
	if !reflect.DeepEqual(wa.instances, wb.instances) {
		t.Fatalf("%s: instances mismatch:\n a=%+v\n b=%+v", label, wa.instances, wb.instances)
	}
	if !reflect.DeepEqual(wa.demand, wb.demand) {
		t.Fatalf("%s: district demand mismatch:\n a=%+v\n b=%+v", label, wa.demand, wb.demand)
	}
	if !reflect.DeepEqual(wa.pools, wb.pools) {
		t.Fatalf("%s: pool availability mismatch:\n a=%+v\n b=%+v", label, wa.pools, wb.pools)
	}
}

func servicesStatesEqual(t *testing.T, a, b *ServicesAPI) bool {
	t.Helper()
	wa, wb := servicesWireOf(t, a), servicesWireOf(t, b)
	return reflect.DeepEqual(wa, wb)
}

// ---------------------------------------------------------------------------
// Save/load drivers.
// ---------------------------------------------------------------------------

func saveIntoS(t *testing.T, a *ServicesAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-services"}
	ckErrS(t, mgr.SaveManual(ctx, "det"))
	return root
}

func loadIntoS(t *testing.T, root string, a *ServicesAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	_, _, err := mgr.Load(manualBundleDirS(t, root))
	ckErrS(t, err)
}

// ---------------------------------------------------------------------------
// Round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestServicesParticipant_RoundTrip(t *testing.T) {
	orig := newServicesAPI(t)
	injectRichServices(orig)

	root := saveIntoS(t, orig, "orig")

	reloaded := newServicesAPI(t)
	loadIntoS(t, root, reloaded, "reloaded")
	compareServices(t, orig, reloaded, "post-load")
}

// TestServicesParticipant_ProveCanFail mutates each serialized field family
// on one pristine reload and asserts it diverges from a second pristine
// reload of the SAME bytes -- proving the comparison is discriminating.
func TestServicesParticipant_ProveCanFail(t *testing.T) {
	orig := newServicesAPI(t)
	injectRichServices(orig)
	root := saveIntoS(t, orig, "orig")

	cases := []struct {
		name   string
		mutate func(a *ServicesAPI)
	}{
		{"kind.Name", func(a *ServicesAPI) { a.kinds[ServiceHealthcare] = KindDef{Name: "renamed"} }},
		{"kind.dropped", func(a *ServicesAPI) { delete(a.kinds, "custom-kind") }},
		{"instance.Kind", func(a *ServicesAPI) { a.instances["clinic-1"].spec.Kind = ServiceFire }},
		{"instance.CapacityRaw", func(a *ServicesAPI) { a.instances["clinic-1"].spec.CapacityRaw = "9 different" }},
		{"instance.CoverageRadius", func(a *ServicesAPI) { a.instances["clinic-1"].spec.CoverageRadius = 0 }},
		{"instance.X", func(a *ServicesAPI) { a.instances["clinic-1"].spec.X = 0 }},
		{"instance.Y", func(a *ServicesAPI) { a.instances["clinic-1"].spec.Y = 0 }},
		{"instance.Milestone", func(a *ServicesAPI) { a.instances["clinic-1"].spec.Milestone = 9 }},
		{"instance.StaffingNeed", func(a *ServicesAPI) { a.instances["clinic-1"].spec.StaffingNeed = 0 }},
		{"instance.UpgradePath", func(a *ServicesAPI) { a.instances["clinic-1"].spec.UpgradePath[0].CapacityCeiling = 0 }},
		{"instance.currentUpgrade", func(a *ServicesAPI) { a.instances["clinic-1"].currentUpgrade = 0 }},
		{"instance.funding", func(a *ServicesAPI) { a.instances["clinic-1"].funding = 0 }},
		{"instance.demand", func(a *ServicesAPI) { a.instances["clinic-1"].demand = 0 }},
		{"instance.demandDist", func(a *ServicesAPI) { a.instances["clinic-1"].demandDist = 0 }},
		{"instance.allocated", func(a *ServicesAPI) { a.instances["clinic-1"].allocated = 0 }},
		{"instance.dropped", func(a *ServicesAPI) { delete(a.instances, "station-1") }},
		{"districtDemand.demand", func(a *ServicesAPI) {
			a.districtDemand["district-a"]["clinic-1"] = demandRecord{demand: 0, distance: 1.5}
		}},
		{"districtDemand.distance", func(a *ServicesAPI) {
			a.districtDemand["district-a"]["clinic-1"] = demandRecord{demand: 30, distance: 0}
		}},
		{"districtDemand.dropped", func(a *ServicesAPI) { delete(a.districtDemand["district-b"], "clinic-1") }},
		{"pool.available", func(a *ServicesAPI) { a.poolAvailable["nursing"] = 0 }},
		{"pool.dropped", func(a *ServicesAPI) { delete(a.poolAvailable, "police") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := newServicesAPI(t)
			loadIntoS(t, root, mutated, "mutated")
			pristine := newServicesAPI(t)
			loadIntoS(t, root, pristine, "pristine")
			compareServices(t, mutated, pristine, "pre-mutation")
			mutated.mu.Lock()
			c.mutate(mutated)
			mutated.mu.Unlock()
			if servicesStatesEqual(t, mutated, pristine) {
				t.Fatalf("prove-can-fail: mutating %s did not diverge from a pristine reload -- the field may be dropped or the compare is blind to it", c.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Byte determinism.
// ---------------------------------------------------------------------------

func TestServicesParticipant_ByteDeterminism(t *testing.T) {
	a1 := newServicesAPI(t)
	injectRichServices(a1)
	root1 := saveIntoS(t, a1, "run1")

	a2 := newServicesAPI(t)
	injectRichServices(a2)
	root2 := saveIntoS(t, a2, "run2")

	assertBundlesByteIdenticalS(t, root1, root2)
}

func TestServicesAttack_ManyKeyByteDeterminism(t *testing.T) {
	a1 := newServicesAPI(t)
	injectManyKeysServices(a1)
	root1 := saveIntoS(t, a1, "run1")

	a2 := newServicesAPI(t)
	injectManyKeysServices(a2)
	root2 := saveIntoS(t, a2, "run2")

	assertBundlesByteIdenticalS(t, root1, root2)
}

func TestServicesAttack_ManyKeyRoundTrip(t *testing.T) {
	orig := newServicesAPI(t)
	injectManyKeysServices(orig)
	root := saveIntoS(t, orig, "orig")

	reloaded := newServicesAPI(t)
	loadIntoS(t, root, reloaded, "reloaded")
	compareServices(t, orig, reloaded, "many-key-load")
}

// TestServicesAttack_SortedEmissionOrder asserts every collection is emitted
// in strictly ascending lexical key order — a Go map-iteration-order
// emission (no sort) would very likely (and, run enough times, certainly)
// break this.
func TestServicesAttack_SortedEmissionOrder(t *testing.T) {
	a := newServicesAPI(t)
	injectManyKeysServices(a)
	snap, err := a.snapshotForSave()
	ckErrS(t, err)

	for i := 1; i < len(snap.kinds); i++ {
		if snap.kinds[i-1].Kind >= snap.kinds[i].Kind {
			t.Fatalf("kinds not in ascending order at %d: %q then %q", i, snap.kinds[i-1].Kind, snap.kinds[i].Kind)
		}
	}
	for i := 1; i < len(snap.instances); i++ {
		if snap.instances[i-1].ID >= snap.instances[i].ID {
			t.Fatalf("instances not in ascending order at %d: %q then %q", i, snap.instances[i-1].ID, snap.instances[i].ID)
		}
	}
	for i := 1; i < len(snap.demand); i++ {
		a, b := snap.demand[i-1], snap.demand[i]
		if a.District > b.District || (a.District == b.District && a.Service >= b.Service) {
			t.Fatalf("district demand not in ascending (district, service) order at %d: %+v then %+v", i, a, b)
		}
	}
	for i := 1; i < len(snap.pools); i++ {
		if snap.pools[i-1].PoolID >= snap.pools[i].PoolID {
			t.Fatalf("pools not in ascending order at %d: %q then %q", i, snap.pools[i-1].PoolID, snap.pools[i].PoolID)
		}
	}
}

// ---------------------------------------------------------------------------
// Load-into-non-empty (full replace, not merge) + copyguard + unknown kind.
// ---------------------------------------------------------------------------

func TestServicesAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig := newServicesAPI(t)
	injectRichServices(orig)
	root := saveIntoS(t, orig, "orig")

	target := newServicesAPI(t)
	injectManyKeysServices(target)
	const ghostInstance ServiceID = "svc100"
	if _, ok := orig.instances[ghostInstance]; ok {
		t.Fatalf("test setup: saved state unexpectedly holds the ghost instance")
	}
	if _, ok := target.instances[ghostInstance]; !ok {
		t.Fatalf("test setup: ghost instance not present on target pre-load")
	}

	loadIntoS(t, root, target, "target")

	if _, ok := target.instances[ghostInstance]; ok {
		t.Fatalf("ghost instance survived load -- Handler merged instead of replacing")
	}
	if len(target.instances) != len(orig.instances) {
		t.Fatalf("instances size %d != saved %d -- merge, not replace", len(target.instances), len(orig.instances))
	}
	if len(target.kinds) != len(orig.kinds) {
		t.Fatalf("kinds size %d != saved %d -- merge, not replace", len(target.kinds), len(orig.kinds))
	}
	compareServices(t, orig, target, "load-into-nonempty")
}

func TestServicesAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig := newServicesAPI(t)
	injectRichServices(orig)

	var copied ServicesAPI
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
	if NewSaveParticipant(orig).Kind() != KindServices {
		t.Fatalf("original participant Kind() broken")
	}
}

func TestServicesAttack_UnknownRecordKindRejected(t *testing.T) {
	a := newServicesAPI(t)
	h := NewSaveParticipant(a).Handler()
	if err := h(serialize.Record{Kind: "services.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// ---------------------------------------------------------------------------
// GR#16-style load-time validation: garbage on the wire must be rejected,
// never silently stored where the quality/staffing arithmetic would consume
// it.
// ---------------------------------------------------------------------------

// TestServicesAttack_LoadRejectsOverflowFloat proves the decode boundary
// itself is fail-closed: encoding/json already refuses a float literal that
// overflows float64 (it can never represent a literal NaN/Inf token, so
// nonFiniteInstanceWireField's IsFinite checks are defense-in-depth for a
// value that reaches Go as a finite float that later arithmetic could still
// blow up on — the SAME boundary RegisterService enforces live). This test
// pins the decode-time half of that boundary.
func TestServicesAttack_LoadRejectsOverflowFloat(t *testing.T) {
	a := newServicesAPI(t)
	h := NewSaveParticipant(a).Handler()
	if err := h(serialize.Record{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","coverageRadius":1e400}`)}); err == nil {
		t.Fatalf("Handler accepted an overflowing float literal -- want a loud decode error")
	}
}

func TestServicesAttack_LoadRejectsInvalidFunding(t *testing.T) {
	a := newServicesAPI(t)
	ckErrS(t, a.resetForLoad())
	ckErrS(t, applyKindRecord(a, ServiceHealthcare))
	err := a.applyLoadRecord(serialize.Record{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","funding":1.5}`)})
	if err == nil {
		t.Fatalf("applyLoadRecord accepted funding=1.5 (outside [0,1]) -- want ErrInvalidFunding")
	}
}

func TestServicesAttack_LoadRejectsUnknownKindReference(t *testing.T) {
	a := newServicesAPI(t)
	ckErrS(t, a.resetForLoad())
	err := a.applyLoadRecord(serialize.Record{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"never-registered","funding":0.5}`)})
	if err == nil {
		t.Fatalf("applyLoadRecord accepted an instance naming a kind with no prior services.kind record -- referential integrity not enforced")
	}
}

func TestServicesAttack_LoadRejectsOutOfRangeUpgradeIndex(t *testing.T) {
	a := newServicesAPI(t)
	ckErrS(t, a.resetForLoad())
	ckErrS(t, applyKindRecord(a, ServiceHealthcare))
	err := a.applyLoadRecord(serialize.Record{Kind: recServicesInstance, Data: []byte(`{"id":"x","kind":"healthcare","funding":0.5,"currentUpgrade":5,"upgradePath":[{"buildingID":"b","name":"n","milestone":1,"capacityCeiling":10}]}`)})
	if err == nil {
		t.Fatalf("applyLoadRecord accepted currentUpgrade=5 against a 1-step upgrade path -- want ErrUpgradeUnavailable")
	}
}

// applyKindRecord is a small test helper installing one kind record directly
// (bypassing resetForLoad, which the caller may have already run).
func applyKindRecord(a *ServicesAPI, kind ServiceKind) error {
	return a.applyLoadRecord(serialize.Record{Kind: recServicesKind, Data: []byte(`{"kind":"` + string(kind) + `","name":"n","benchmark":""}`)})
}

// ---------------------------------------------------------------------------
// The round's own named defect: a live-composition rewind must not leave a
// phantom service instance behind. This package-level test proves the
// PARTICIPANT half in isolation (resetForLoad wholesale-replaces the
// instance table); internal/engine/compose has the full build+services
// integration test for the actual rewind scenario.
// ---------------------------------------------------------------------------

func TestServicesAttack_RewindDropsLaterInstance(t *testing.T) {
	// An "earlier" save with just one service.
	early := newServicesAPI(t)
	early.mu.Lock()
	early.kinds = map[ServiceKind]KindDef{ServiceHealthcare: {Name: "Healthcare"}}
	early.instances = map[ServiceID]*serviceInstance{"clinic-1": richInstance("clinic-1", ServiceHealthcare, 1)}
	early.mu.Unlock()
	root := saveIntoS(t, early, "early")

	// The "live" composition has since registered a SECOND service (as if a
	// later build completed after the earlier save was taken).
	live := newServicesAPI(t)
	live.mu.Lock()
	live.kinds = map[ServiceKind]KindDef{ServiceHealthcare: {Name: "Healthcare"}, ServiceFire: {Name: "Fire"}}
	live.instances = map[ServiceID]*serviceInstance{
		"clinic-1":  richInstance("clinic-1", ServiceHealthcare, 1),
		"station-1": richInstance("station-1", ServiceFire, 2), // the "phantom" if not cleared
	}
	live.mu.Unlock()

	// Rewind: load the EARLIER save into the LIVE (non-empty) instance.
	loadIntoS(t, root, live, "live")

	if _, ok := live.instances["station-1"]; ok {
		t.Fatalf("phantom instance survived a rewind load -- resetForLoad did not wholesale-replace the instance table")
	}
	if _, ok := live.instances["clinic-1"]; !ok {
		t.Fatalf("the earlier save's own instance did not survive the rewind load")
	}
	if len(live.instances) != 1 {
		t.Fatalf("instance count after rewind = %d, want 1 (no phantom, the earlier instance only)", len(live.instances))
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers.
// ---------------------------------------------------------------------------

func assertBundlesByteIdenticalS(t *testing.T, root1, root2 string) {
	t.Helper()
	dir1 := manualBundleDirS(t, root1)
	dir2 := manualBundleDirS(t, root2)
	files1 := allFilesS(t, dir1)
	files2 := allFilesS(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ckErrS(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ckErrS(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic services state", rel)
		}
	}
}

func manualBundleDirS(t *testing.T, root string) string {
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
	ckErrS(t, err)
	if found == "" {
		t.Fatalf("no manual save bundle (header.json) found under %q", root)
	}
	return found
}

func allFilesS(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, rel)
		return nil
	})
	ckErrS(t, err)
	sort.Strings(out)
	return out
}
