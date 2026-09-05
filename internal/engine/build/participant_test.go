package build

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// FEAT-1972079941 inc3 — engine.build save.Participant tests.
//
// Mirrors the inc2 engine.unlocks participant test suite exactly, adapted to
// engine.build's mutable state (the construction queue + zone/structure/
// demand maps + the district/nextOrder scalars). The five mandatory shapes:
// field-parity drift, full round-trip (prove-can-fail per field), byte
// determinism (many-order, sorted-emission), load-into-non-empty (replace not
// merge), and copyguard-fires + unknown-record-kind rejection.
// ---------------------------------------------------------------------------

func ckErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Field-parity drift tests (the "built but not serialized" guard).
// ---------------------------------------------------------------------------

// TestBuildAPIFieldsAllClassified fails the build if any BuildAPI field is
// neither serialized (covered) nor explicitly excluded (runtime/config/
// injected/copy-guard). A new mutable field added without a save is exactly
// the class this inc exists to prevent.
func TestBuildAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"mu":               "runtime lock, not state",
		"correlationID":    "per-instance error correlation, not simulation state",
		"catalogue":        "immutable config, loaded from data/buildings.json",
		"labourPerTick":    "immutable config, loaded from data/buildings.json meta",
		"world":            "injected dependency, re-wired by the composition root on load",
		"season":           "injected dependency, re-wired by the composition root on load",
		"logistics":        "injected dependency, re-wired by the composition root on load",
		"self":             "SEC-020 copy-guard pointer, re-armed by Load",
		"catalogueEntries": "immutable config, loaded from data/buildings.json entries (FEAT-build-services-bridge-2026-09-02)",
		"services":         "injected dependency, re-wired by the composition root on load (FEAT-build-services-bridge-2026-09-02)",
		"serviceByOrder": "derived runtime index (order id -> registered ServiceID), rebuildable from queue+catalogueEntries+engine.services state; " +
			"NOT part of the save schema per FEAT-build-services-bridge-2026-09-02's own scope notes (composition-root save wiring for engine.services " +
			"is separate follow-up work) -- resetForLoad clears it to empty so a load never carries a stale order->service reference forward",
		"servicesSweepDirty": "derived runtime flag (BUG-586), not simulation state -- rederived fresh on every resetForLoad/SetServices call, " +
			"never round-tripped through a save; it only gates WHEN the already-persisted registerCompletedServicesLocked sweep runs, not what it does",
	}
	// Covered: serialized via buildMetaWire (scalars) or a per-item record
	// (queue -> build.order, zoneState -> build.zone, structures ->
	// build.structure, demand -> build.demand).
	covered := map[string]bool{
		"district": true, "nextOrder": true, "nextCompletionSeq": true, "queue": true,
		"zoneState": true, "structures": true, "demand": true,
	}
	bt := reflect.TypeOf((*BuildAPI)(nil)).Elem()
	for i := 0; i < bt.NumField(); i++ {
		name := bt.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("BuildAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this inc forbids", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("BuildAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestBuildMetaWireFieldsMatchScalars asserts the meta wire carries a
// counterpart for exactly the serialized SCALAR fields of BuildAPI (the
// non-map, non-slice covered fields). A new scalar added to the save without
// a meta wire field, or a wire field with no API field, fails here.
func TestBuildMetaWireFieldsMatchScalars(t *testing.T) {
	want := map[string]struct {
		wire string
		kind reflect.Kind
	}{
		"district":          {"District", reflect.String},
		"nextOrder":         {"NextOrder", reflect.Uint64},         // BuildOrderID is uint64
		"nextCompletionSeq": {"NextCompletionSeq", reflect.Uint64}, // BuildOrderID is uint64
	}
	mw := reflect.TypeOf((*buildMetaWire)(nil)).Elem()
	if mw.NumField() != len(want) {
		t.Fatalf("buildMetaWire has %d fields but %d serialized scalars are expected -- meta wire drifted from the scalar set", mw.NumField(), len(want))
	}
	for _, spec := range want {
		f, ok := mw.FieldByName(spec.wire)
		if !ok {
			t.Fatalf("buildMetaWire is missing field %q for a serialized scalar", spec.wire)
		}
		if f.Type.Kind() != spec.kind {
			t.Fatalf("buildMetaWire.%s has kind %s, want %s", spec.wire, f.Type.Kind(), spec.kind)
		}
	}
}

// TestBuildOrderWireCoversEveryMutableOrderField asserts the on-wire order
// record carries a counterpart for every mutable field of the internal
// buildOrder -- the queue is the highest-value state (a lost in-progress
// build), so a new order field added without a wire counterpart reddens here.
func TestBuildOrderWireCoversEveryMutableOrderField(t *testing.T) {
	// buildOrder field -> the buildOrderWire field expected to carry it.
	want := map[string]string{
		"id":                 "ID",
		"tile":               "Tile",
		"local":              "Local",
		"zone":               "Zone",
		"buildingID":         "BuildingID",
		"materialsTotal":     "MaterialsTotal",
		"materialsRemaining": "MaterialsRemaining",
		"materialsDrawn":     "MaterialsDrawn",
		"labourRemaining":    "LabourRemaining",
		"leadTimeRemaining":  "LeadTimeRemaining",
		"complete":           "Complete",
		"completionSeq":      "CompletionSeq",
	}
	ot := reflect.TypeOf((*buildOrder)(nil)).Elem()
	if ot.NumField() != len(want) {
		t.Fatalf("buildOrder has %d fields but %d are mapped to the wire -- an order field was added without a wire counterpart (a lost in-progress build)", ot.NumField(), len(want))
	}
	wt := reflect.TypeOf((*buildOrderWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := ot.FieldByName(domain); !ok {
			t.Fatalf("buildOrder is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("buildOrderWire is missing field %q for buildOrder.%s", wire, domain)
		}
	}
}

// ---------------------------------------------------------------------------
// Shared drivers + comparison helpers.
// ---------------------------------------------------------------------------

// driveBuild runs a fixed, deterministic sequence producing a rich queue with
// orders in DIFFERENT states (complete, materials-pending with PARTIAL draw,
// materials-pending untouched), landed structures + zones, standalone zoned
// cells, reported demand, an advanced order-id counter, and a non-default
// district. No RNG anywhere (build is RNG-free).
func driveBuild(t *testing.T, b *BuildAPI, w *world.WorldAPI, l *logistics.LogisticsAPI) {
	t.Helper()
	// Provision exactly 250t: enough for two dwellings (100 each) to fully
	// draw, a third to draw a PARTIAL 50 and stall, and the shop/office to
	// draw nothing -- guaranteeing a spread of order states.
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 250); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	cmds := []BuildCommand{
		{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6},
		{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 1}, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6},
		{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 2}, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6},
		{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 3}, OwnerID: testOwner, Zone: ZoneShop, Month: 0},    // winter lead
		{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 4}, OwnerID: testOwner, Zone: ZoneOffice, Month: 11}, // winter lead
	}
	for _, c := range cmds {
		if _, err := b.SubmitBuildCommand(c); err != nil {
			t.Fatalf("SubmitBuildCommand: %v", err)
		}
	}
	// Advance far past every lead time so the funded orders complete.
	for i := int64(0); i < 200; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}

	// Standalone zoned cells (zoneState entries with no queue order behind
	// them), exercising the map independently of completions.
	ckErr(t, b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 1, Col: 0}, OwnerID: testOwner, Zone: ZoneFarming}))
	ckErr(t, b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 1, Col: 1}, OwnerID: testOwner, Zone: ZoneMining}))

	// Reported demand for a couple of zones (with distinct starved flags).
	ckErr(t, b.ReportDemand(ZoneOffice, DemandInput{Unfilled: 7, LabourStarved: true, PowerStarved: true}))
	ckErr(t, b.ReportDemand(ZoneShop, DemandInput{Unfilled: 3, FreightStarved: true}))

	// A non-default district (must round-trip). Set AFTER the draws so the
	// completions above still resolved against DefaultDistrict's shelf.
	ckErr(t, b.SetDistrict("north"))
}

// continueBuild applies one more deterministic batch that touches ONLY build
// state via world+season (both present on a reloaded fixture) and never
// depends on the un-saved logistics shelf -- so a divergent restore surfaces
// as unequal reads. It submits a new order (advancing nextOrder + the queue),
// zones a fresh cell, and reports demand. No ticking (that would read the
// un-saved logistics stock).
func continueBuild(t *testing.T, b *BuildAPI) {
	t.Helper()
	if _, err := b.SubmitBuildCommand(BuildCommand{Tile: tile00(), Local: world.CellLocal{Row: 2, Col: 0}, OwnerID: testOwner, Zone: ZoneShop, Month: 6}); err != nil {
		t.Fatalf("continue SubmitBuildCommand: %v", err)
	}
	ckErr(t, b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 2, Col: 1}, OwnerID: testOwner, Zone: ZoneEntertainment}))
	ckErr(t, b.ReportDemand(ZoneDwelling, DemandInput{Unfilled: 11, LabourStarved: true}))
}

// compareBuild asserts a and b are observably identical across the FULL
// mutable state: district + nextOrder (in-package), the entire queue (every
// order's coordinates/zone/materials-ledger/labour/lead/status via Queue's
// derived snapshot), and the zoneState/structures/demand maps.
func compareBuild(t *testing.T, a, b *BuildAPI, label string) {
	t.Helper()
	if a.district != b.district {
		t.Fatalf("%s: district %q != %q", label, a.district, b.district)
	}
	if a.nextOrder != b.nextOrder {
		t.Fatalf("%s: nextOrder %d != %d", label, a.nextOrder, b.nextOrder)
	}
	if a.nextCompletionSeq != b.nextCompletionSeq {
		t.Fatalf("%s: nextCompletionSeq %d != %d", label, a.nextCompletionSeq, b.nextCompletionSeq)
	}
	qa, qb := a.Queue(), b.Queue()
	if !reflect.DeepEqual(qa, qb) {
		t.Fatalf("%s: queue mismatch:\n a=%+v\n b=%+v", label, qa, qb)
	}
	if !reflect.DeepEqual(a.zoneState, b.zoneState) {
		t.Fatalf("%s: zoneState mismatch:\n a=%+v\n b=%+v", label, a.zoneState, b.zoneState)
	}
	if !reflect.DeepEqual(a.structures, b.structures) {
		t.Fatalf("%s: structures mismatch:\n a=%+v\n b=%+v", label, a.structures, b.structures)
	}
	if !reflect.DeepEqual(a.demand, b.demand) {
		t.Fatalf("%s: demand mismatch:\n a=%+v\n b=%+v", label, a.demand, b.demand)
	}
}

// saveInto drives a save of b's participant into a fresh bundle under a temp
// root and returns the bundle root directory.
func saveInto(t *testing.T, b *BuildAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(b)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-build"}
	ckErr(t, mgr.SaveManual(ctx, "det"))
	return root
}

// loadInto loads the single manual bundle under root into b.
func loadInto(t *testing.T, root string, b *BuildAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(b)}, cid)
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ckErr(t, err)
}

// ---------------------------------------------------------------------------
// Round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestBuildParticipant_RoundTrip(t *testing.T) {
	orig, w, l := newBuildFixture(t)
	driveBuild(t, orig, w, l)

	root := saveInto(t, orig, "orig")

	// Load into a FRESH BuildAPI (same data/buildings.json, empty runtime
	// state replaced by the saved one).
	reloaded, _, _ := newBuildFixture(t)
	loadInto(t, root, reloaded, "reloaded")

	compareBuild(t, orig, reloaded, "post-load")

	// Continue identical operations on BOTH and assert they stay equal: a
	// divergent restore would surface the moment new work builds on it.
	continueBuild(t, orig)
	continueBuild(t, reloaded)
	compareBuild(t, orig, reloaded, "post-continue")

	// Prove-can-fail (district): mutate a reloaded scalar -> divergence from a
	// second pristine load of the SAME bytes.
	r2, _, _ := newBuildFixture(t)
	loadInto(t, root, r2, "r2")
	fresh, _, _ := newBuildFixture(t)
	loadInto(t, root, fresh, "fresh")
	r2.district = "somewhere-else"
	if r2.district == fresh.district {
		t.Fatalf("prove-can-fail: mutating a reloaded district did not diverge")
	}

	// Prove-can-fail (nextOrder counter).
	fresh.nextOrder += 1
	r3, _, _ := newBuildFixture(t)
	loadInto(t, root, r3, "r3")
	if fresh.nextOrder == r3.nextOrder {
		t.Fatalf("prove-can-fail: mutating a reloaded nextOrder did not diverge")
	}

	// Prove-can-fail (an in-progress order's materialsDrawn -- the highest
	// value field: a lost partial build). Drop it on one load, keep it on
	// another, assert the queues diverge.
	rA, _, _ := newBuildFixture(t)
	loadInto(t, root, rA, "rA")
	rB, _, _ := newBuildFixture(t)
	loadInto(t, root, rB, "rB")
	if len(rA.queue) == 0 {
		t.Fatalf("test setup: reloaded queue is empty")
	}
	// Find the partially-drawn, still-pending order and corrupt its drawn ledger.
	var mutated bool
	for _, o := range rA.queue {
		if !o.complete && o.materialsDrawn > 0 {
			o.materialsDrawn = 0
			mutated = true
			break
		}
	}
	if !mutated {
		t.Fatalf("test setup: no partially-drawn in-progress order to corrupt (driver did not produce the expected spread)")
	}
	if reflect.DeepEqual(rA.Queue(), rB.Queue()) {
		t.Fatalf("prove-can-fail: corrupting a reloaded order's materialsDrawn did not diverge the queue")
	}

	// Prove-can-fail (a landed structure entry): drop one -> divergence.
	rC, _, _ := newBuildFixture(t)
	loadInto(t, root, rC, "rC")
	rD, _, _ := newBuildFixture(t)
	loadInto(t, root, rD, "rD")
	if len(rC.structures) == 0 {
		t.Fatalf("test setup: reloaded structures map is empty")
	}
	for k := range rC.structures {
		delete(rC.structures, k)
		break
	}
	if reflect.DeepEqual(rC.structures, rD.structures) {
		t.Fatalf("prove-can-fail: dropping a reloaded structure did not diverge")
	}
}

// ---------------------------------------------------------------------------
// Byte determinism.
// ---------------------------------------------------------------------------

func TestBuildParticipant_ByteDeterminism(t *testing.T) {
	b1, w1, l1 := newBuildFixture(t)
	driveBuild(t, b1, w1, l1)
	root1 := saveInto(t, b1, "run1")

	b2, w2, l2 := newBuildFixture(t)
	driveBuild(t, b2, w2, l2)
	root2 := saveInto(t, b2, "run2")

	assertBundlesByteIdentical(t, root1, root2)
}

// driveManyOrders forces MANY entries into every map-backed collection AND a
// long queue, so raw map-iteration order (if any emission were unsorted)
// would differ between two saves -- the sorted emission must survive. Zones
// and structures are map-backed; the queue is slice-backed (its own order).
func driveManyOrders(t *testing.T, b *BuildAPI, w *world.WorldAPI, l *logistics.LogisticsAPI) {
	t.Helper()
	// Ample materials so every order completes and lands a structure.
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// A 6x6 block of owned cells in tile (0,0): 36 build orders across several
	// zone types, all completing -> 36 zoneState + 36 structure entries.
	zones := []ZoneType{ZoneDwelling, ZoneShop, ZoneOffice, ZoneEntertainment, ZoneFarming, ZoneManufacturing}
	n := 0
	for row := 0; row < 6; row++ {
		for col := 0; col < 6; col++ {
			z := zones[(row*6+col)%len(zones)]
			if _, err := b.SubmitBuildCommand(BuildCommand{
				Tile: tile00(), Local: world.CellLocal{Row: row, Col: col},
				OwnerID: testOwner, Zone: z, Month: int64((row + col) % 12),
			}); err != nil {
				t.Fatalf("SubmitBuildCommand(%d,%d): %v", row, col, err)
			}
			n++
		}
	}
	for i := int64(0); i < 400; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}
	// Demand for every one of the eight zone types.
	for _, z := range []ZoneType{ZoneDwelling, ZoneShop, ZoneOffice, ZoneEntertainment, ZoneFarming, ZoneManufacturing, ZoneHeavyIndustry, ZoneMining} {
		ckErr(t, b.ReportDemand(z, DemandInput{Unfilled: 1, LabourStarved: true}))
	}
	_ = n
}

// TestAttack_ManyOrderByteDeterminism forces MANY map keys and a long queue
// and asserts two saves of the same state are byte-identical -- proves sorted
// emission of the map-backed collections, not just single-key determinism.
func TestAttack_ManyOrderByteDeterminism(t *testing.T) {
	b1, w1, l1 := newBuildFixture(t)
	driveManyOrders(t, b1, w1, l1)
	root1 := saveInto(t, b1, "run1")

	b2, w2, l2 := newBuildFixture(t)
	driveManyOrders(t, b2, w2, l2)
	root2 := saveInto(t, b2, "run2")

	// Sanity: the driver actually produced many structure entries.
	if len(b1.structures) < 20 {
		t.Fatalf("test setup: only %d structures -- too few to force map reorder", len(b1.structures))
	}
	assertBundlesByteIdentical(t, root1, root2)
}

// TestAttack_ManyOrderRoundTrip asserts the many-order state round-trips
// exactly (every order + every zone + every structure + every demand + the
// counters).
func TestAttack_ManyOrderRoundTrip(t *testing.T) {
	orig, w, l := newBuildFixture(t)
	driveManyOrders(t, orig, w, l)
	root := saveInto(t, orig, "orig")

	reloaded, _, _ := newBuildFixture(t)
	loadInto(t, root, reloaded, "reloaded")

	compareBuild(t, orig, reloaded, "many-order-load")
}

// ---------------------------------------------------------------------------
// Load-into-non-empty (full replace, not merge) + copyguard.
// ---------------------------------------------------------------------------

// TestAttack_LoadIntoNonEmptyFullyReplaces: a Load into a BuildAPI that
// already holds DIFFERENT runtime state must fully overwrite it (Handler
// resets), never merge.
func TestAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig, w, l := newBuildFixture(t)
	driveBuild(t, orig, w, l)
	root := saveInto(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger runtime state (the
	// 36-order block), including a GHOST cell the saved state never touches.
	target, tw, tl := newBuildFixture(t)
	driveManyOrders(t, target, tw, tl)
	ghost := cellKey{tile: tile00(), local: world.CellLocal{Row: 5, Col: 5}}
	if _, ok := orig.structures[ghost]; ok {
		t.Fatalf("test setup: saved state unexpectedly holds the ghost cell")
	}
	if _, ok := target.structures[ghost]; !ok {
		t.Fatalf("test setup: ghost structure not present on target pre-load")
	}

	loadInto(t, root, target, "target")

	if _, ok := target.structures[ghost]; ok {
		t.Fatalf("ghost structure survived load -- Handler merged instead of replacing")
	}
	if len(target.queue) != len(orig.queue) {
		t.Fatalf("queue length %d != saved %d -- merge, not replace", len(target.queue), len(orig.queue))
	}
	if len(target.structures) != len(orig.structures) {
		t.Fatalf("structures size %d != saved %d -- merge, not replace", len(target.structures), len(orig.structures))
	}
	compareBuild(t, orig, target, "load-into-nonempty")
}

// TestAttack_CopyguardFiresOnParticipant: a struct-copied BuildAPI's
// participant must fail closed on Kind/Source/Handler.
func TestAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig, w, l := newBuildFixture(t)
	driveBuild(t, orig, w, l)

	// Reproduce a struct-copied BuildAPI's guard-visible state (self still
	// points at the ORIGINAL) without a vet-copylocks-tripping value copy of
	// the embedded RWMutex.
	var copied BuildAPI
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
	if NewSaveParticipant(orig).Kind() != KindBuild {
		t.Fatalf("original participant Kind() broken")
	}
}

// TestAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestAttack_UnknownRecordKindRejected(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	h := NewSaveParticipant(b).Handler()
	if err := h(serialize.Record{Kind: "build.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// TestAttack_InProgressOrderRoundTrips is the sharpest teeth for this inc:
// the whole point is that a build caught mid-construction survives a save.
// It drives a single order to a KNOWN partial state (materials partly drawn,
// labour partly applied, lead time partly elapsed, NOT complete), saves,
// reloads into a fresh API, and asserts every one of those in-flight numbers
// came back verbatim. Mutation-proven: drop any one of them in the wire and
// this reddens.
func TestAttack_InProgressOrderRoundTrips(t *testing.T) {
	orig, w, l := newBuildFixture(t)
	// Fund only 60t: the dwelling needs 100, so it draws 60 across ticks and
	// stalls materials-pending with a NON-zero drawn ledger and remaining bill.
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 60); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	_ = w
	id, err := orig.SubmitBuildCommand(BuildCommand{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	// A handful of ticks: enough to draw the 60 and burn some labour+lead, but
	// far short of completion (dwelling lead is 45, labour 40).
	for i := int64(0); i < 10; i++ {
		if err := orig.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}
	before := orderByID(t, orig.Queue(), id)
	if before.Status != OrderPendingMaterials {
		t.Fatalf("setup: order status = %s, want materials-pending (a genuine in-progress build)", before.Status)
	}
	if before.MaterialsDrawn != 60 || before.MaterialsRemaining != 40 {
		t.Fatalf("setup: drawn=%d remaining=%d, want 60/40 (partial draw)", before.MaterialsDrawn, before.MaterialsRemaining)
	}
	if before.LeadTimeRemaining <= 0 || before.LabourRemaining <= 0 {
		t.Fatalf("setup: lead=%d labour=%d, want both > 0 (mid-flight)", before.LeadTimeRemaining, before.LabourRemaining)
	}

	root := saveInto(t, orig, "orig")
	reloaded, _, _ := newBuildFixture(t)
	loadInto(t, root, reloaded, "reloaded")

	after := orderByID(t, reloaded.Queue(), id)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("in-progress order did not round-trip verbatim:\n before=%+v\n after =%+v", before, after)
	}
}

// TestAttack_DistinctTileCoordsRoundTrip closes the world-coordinate teeth
// gap left by every other round-trip test: the driver fixtures only ever
// build in tile00 (X=0, Y=0), so a load that DROPPED the tile.X or tile.Y of
// an order/structure/zone (teleporting the build to a different map tile) was
// invisible — before and after both read (0,0). This test injects state at
// DISTINCT, non-zero tile X and Y across an order, a structure and a zone,
// saves, reloads into a fresh API, and asserts the full state round-trips.
// Mutation-proven: zeroing the tile restore in applyLoadRecord (for orders,
// structures OR zones) reddens this — a slice order's tile diverges in the
// Queue() compare, and a map entry's tile is part of its cellKey so the map
// keys diverge. The local Row/Col are already proven by the driver tests
// (they vary); this one guards X and Y specifically.
func TestAttack_DistinctTileCoordsRoundTrip(t *testing.T) {
	orig, _, _ := newBuildFixture(t)

	// Inject internal state directly (white-box, same package) at tiles with
	// pairwise-distinct X and pairwise-distinct Y, so dropping either X or Y on
	// load changes the observed state. Load does no world-ownership check on the
	// restored records, so arbitrary tiles are legal on the wire.
	orig.district = "coords"
	orig.nextOrder = 99
	orig.queue = []*buildOrder{
		{id: 10, tile: world.TileCoord{X: 3, Y: 5}, local: world.CellLocal{Row: 1, Col: 2},
			zone: ZoneDwelling, materialsTotal: 100, materialsRemaining: 40, materialsDrawn: 60,
			labourRemaining: 30, leadTimeRemaining: 35, complete: false},
		{id: 11, tile: world.TileCoord{X: 7, Y: 2}, local: world.CellLocal{Row: 0, Col: 4},
			zone: ZoneShop, materialsTotal: 80, materialsRemaining: 0, materialsDrawn: 80,
			labourRemaining: 0, leadTimeRemaining: 0, complete: true},
	}
	orig.structures = map[cellKey]BuildOrderID{
		{tile: world.TileCoord{X: 4, Y: 9}, local: world.CellLocal{Row: 2, Col: 3}}: 11,
		{tile: world.TileCoord{X: 8, Y: 1}, local: world.CellLocal{Row: 5, Col: 6}}: 12,
	}
	orig.zoneState = map[cellKey]ZoneType{
		{tile: world.TileCoord{X: 2, Y: 6}, local: world.CellLocal{Row: 3, Col: 1}}: ZoneFarming,
		{tile: world.TileCoord{X: 6, Y: 3}, local: world.CellLocal{Row: 4, Col: 0}}: ZoneMining,
	}
	orig.demand = map[ZoneType]DemandInput{
		ZoneOffice: {Unfilled: 5, LabourStarved: true},
	}

	root := saveInto(t, orig, "orig")
	reloaded, _, _ := newBuildFixture(t)
	loadInto(t, root, reloaded, "reloaded")

	compareBuild(t, orig, reloaded, "distinct-tile-coords")

	// Belt-and-braces: assert the reloaded tiles are the exact non-zero values,
	// so this fails even if compareBuild were ever weakened.
	rq := reloaded.Queue()
	if len(rq) != 2 {
		t.Fatalf("reloaded queue length %d, want 2", len(rq))
	}
	if rq[0].Tile != (world.TileCoord{X: 3, Y: 5}) || rq[1].Tile != (world.TileCoord{X: 7, Y: 2}) {
		t.Fatalf("reloaded order tiles teleported: got %+v and %+v", rq[0].Tile, rq[1].Tile)
	}
	wantStruct := map[world.TileCoord]bool{{X: 4, Y: 9}: true, {X: 8, Y: 1}: true}
	for k := range reloaded.structures {
		if !wantStruct[k.tile] {
			t.Fatalf("reloaded structure at unexpected tile %+v (X or Y dropped on load)", k.tile)
		}
	}
	wantZone := map[world.TileCoord]bool{{X: 2, Y: 6}: true, {X: 6, Y: 3}: true}
	for k := range reloaded.zoneState {
		if !wantZone[k.tile] {
			t.Fatalf("reloaded zone at unexpected tile %+v (X or Y dropped on load)", k.tile)
		}
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the finance/unlocks pilots).
// ---------------------------------------------------------------------------

func assertBundlesByteIdentical(t *testing.T, root1, root2 string) {
	t.Helper()
	dir1 := manualBundleDir(t, root1)
	dir2 := manualBundleDir(t, root2)
	files1 := allFiles(t, dir1)
	files2 := allFiles(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ckErr(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ckErr(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic build state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

// manualBundleDir locates the single manual-save bundle directory under a
// save root by finding the header.json leaf.
func manualBundleDir(t *testing.T, root string) string {
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
	ckErr(t, err)
	if found == "" {
		t.Fatalf("no bundle (header.json) found under %q", root)
	}
	return found
}

// allFiles returns every file under dir, relative to dir, sorted.
func allFiles(t *testing.T, dir string) []string {
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
	ckErr(t, err)
	sort.Strings(out)
	return out
}
