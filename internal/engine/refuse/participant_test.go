package refuse

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
// FEAT-1972079941 inc5 — engine.refuse save.Participant tests.
//
// Mirrors the inc3 engine.build participant test suite exactly, adapted to
// engine.refuse's mutable state (per-cell bin stock, collection rounds,
// depots, disposal sites, strike flags, and the generated/collected/
// contamination/site-target/truck scalars). The five mandatory shapes:
// field-parity drift, full round-trip (prove-can-fail per field, distinct
// non-zero values so a dropped field cannot match by coincidence), byte
// determinism (many-key sorted emission), load-into-non-empty (replace not
// merge), and copyguard-fires + unknown-record-kind rejection.
// ---------------------------------------------------------------------------

func ckErrP(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Field-parity drift tests (the "built but not serialized" guard).
// ---------------------------------------------------------------------------

// TestRefuseAPIFieldsAllClassified fails the build if any RefuseAPI field is
// neither serialized (covered) nor explicitly excluded (runtime/config/
// injected/copy-guard/logistics-coupling). A new mutable field added without a
// save is exactly the class this inc exists to prevent.
func TestRefuseAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"mu":            "runtime lock, not state",
		"correlationID": "per-instance error correlation, not simulation state",
		"cfg":           "immutable config, loaded from data/refuse.json (a save must not pin old rules — FEAT-1972079897)",
		"logistics":     "injected dependency, re-wired by the composition root on load",
		"services":      "injected dependency, re-wired by the composition root on load",
		"wellbeing":     "injected dependency seam, re-wired by the composition root on load",
		"self":          "SEC-020 copy-guard pointer, re-armed by Load",
		"provisioned":   "logistics-shelf-coupling cache re-established by Wire/ensureSiteShelf from the durable disposalSite.used; persisting it would skip the re-provision of a fresh logistics instance's shelf and lose the fill on the logistics side",
	}
	// Covered: serialized via refuseMetaWire (scalars/arrays) or a per-item
	// record (cells -> refuse.cell, rounds -> refuse.round, depots ->
	// refuse.depot, sites -> refuse.site, strike -> refuse.strike).
	covered := map[string]bool{
		"cells": true, "rounds": true, "depots": true, "sites": true,
		"generated": true, "collected": true, "contamination": true,
		"generalSiteID": true, "compostSiteID": true, "trucksAvailable": true,
		"strike": true,
	}
	rt := reflect.TypeOf((*RefuseAPI)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("RefuseAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this inc forbids", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("RefuseAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestRefuseCellStateFieldsCovered asserts the on-wire cell record carries a
// counterpart for every field of the internal cellState.
func TestRefuseCellStateFieldsCovered(t *testing.T) {
	want := map[string]string{
		"landUse":   "LandUse",
		"street":    "Street",
		"capacity":  "Capacity",
		"levels":    "Levels",
		"overflow":  "Overflow",
		"vermin":    "Vermin",
		"missCause": "MissCause",
	}
	ct := reflect.TypeOf((*cellState)(nil)).Elem()
	if ct.NumField() != len(want) {
		t.Fatalf("cellState has %d fields but %d are mapped to the wire -- a cell field was added without a wire counterpart", ct.NumField(), len(want))
	}
	wt := reflect.TypeOf((*refuseCellWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := ct.FieldByName(domain); !ok {
			t.Fatalf("cellState is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("refuseCellWire is missing field %q for cellState.%s", wire, domain)
		}
	}
}

// TestRefuseRoundStateFieldsClassified asserts every roundState field is
// either carried on the wire or explicitly excluded. `active` is the one
// exclusion: it is a transient in-call re-entrancy claim (held only while a
// RunRound is executing), and persisting it would leave the round permanently
// un-runnable after a load.
func TestRefuseRoundStateFieldsClassified(t *testing.T) {
	covered := map[string]string{
		"id":            "ID",
		"depotID":       "DepotID",
		"cells":         "Cells",
		"route":         "Route",
		"overridden":    "Overridden",
		"overrideRoute": "OverrideRoute",
		"completed":     "Completed",
		"inTransit":     "InTransit",
	}
	excluded := map[string]string{
		"active": "transient in-call re-entrancy claim; persisting a mid-call claim would leave the round permanently un-runnable after load",
	}
	rt := reflect.TypeOf((*roundState)(nil)).Elem()
	if rt.NumField() != len(covered)+len(excluded) {
		t.Fatalf("roundState has %d fields but %d are classified -- a round field was added without a wire counterpart or an exclusion", rt.NumField(), len(covered)+len(excluded))
	}
	wt := reflect.TypeOf((*refuseRoundWire)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if wire, ok := covered[name]; ok {
			if _, ok := wt.FieldByName(wire); !ok {
				t.Fatalf("refuseRoundWire is missing field %q for roundState.%s", wire, name)
			}
			continue
		}
		if _, ok := excluded[name]; ok {
			continue
		}
		t.Fatalf("roundState field %q is neither covered nor excluded", name)
	}
}

// TestRefuseDisposalSiteFieldsCovered asserts the on-wire site record carries
// a counterpart for every field of the internal disposalSite -- the durable
// fill (`used`), backlog, and accumulated energy/airshed/compost are the
// highest-value state a lost save would corrupt.
func TestRefuseDisposalSiteFieldsCovered(t *testing.T) {
	want := map[string]string{
		"id":          "ID",
		"kind":        "Kind",
		"capacity":    "Capacity",
		"used":        "Used",
		"backlog":     "Backlog",
		"reclaimed":   "Reclaimed",
		"surrounding": "Surrounding",
		"energy":      "Energy",
		"airshed":     "Airshed",
		"compost":     "Compost",
	}
	st := reflect.TypeOf((*disposalSite)(nil)).Elem()
	if st.NumField() != len(want) {
		t.Fatalf("disposalSite has %d fields but %d are mapped to the wire -- a site field was added without a wire counterpart", st.NumField(), len(want))
	}
	wt := reflect.TypeOf((*refuseSiteWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := st.FieldByName(domain); !ok {
			t.Fatalf("disposalSite is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("refuseSiteWire is missing field %q for disposalSite.%s", wire, domain)
		}
	}
}

// TestRefuseMetaWireFieldsMatchScalars asserts the meta wire carries a
// counterpart for exactly the serialized SCALAR/ARRAY fields (the non-map
// covered fields). A new scalar added to the save without a meta wire field,
// or a wire field with no API field, fails here.
func TestRefuseMetaWireFieldsMatchScalars(t *testing.T) {
	want := map[string]reflect.Kind{
		"Generated":       reflect.Array,
		"Collected":       reflect.Array,
		"Contamination":   reflect.Float64,
		"GeneralSiteID":   reflect.String,
		"CompostSiteID":   reflect.String,
		"TrucksAvailable": reflect.Int64,
	}
	mw := reflect.TypeOf((*refuseMetaWire)(nil)).Elem()
	if mw.NumField() != len(want) {
		t.Fatalf("refuseMetaWire has %d fields but %d serialized scalars/arrays are expected -- meta wire drifted from the scalar set", mw.NumField(), len(want))
	}
	for name, kind := range want {
		f, ok := mw.FieldByName(name)
		if !ok {
			t.Fatalf("refuseMetaWire is missing field %q for a serialized scalar", name)
		}
		if f.Type.Kind() != kind {
			t.Fatalf("refuseMetaWire.%s has kind %s, want %s", name, f.Type.Kind(), kind)
		}
	}
}

// ---------------------------------------------------------------------------
// Rich-state builders (DISTINCT, NON-ZERO values for EVERY field, so a
// dropped field cannot round-trip by coincidence).
// ---------------------------------------------------------------------------

func mc(c MissCause) *MissCause { return &c }

// injectRichRefuse fills r's internal state directly (white-box, same package)
// with distinct, non-zero values across every serialized field: distinct
// per-index generated/collected counters, a distinct fractional contamination,
// distinct active-site targets and truck count, a spread of cells (each with
// distinct levels/overflow/vermin and a MIX of nil and non-nil miss causes), a
// spread of rounds (overridden and not, completed and not, distinct in-transit
// tonnage), several depots, all three disposal-site kinds with distinct
// durable state, and strike flags that are BOTH true and false.
func injectRichRefuse(r *RefuseAPI) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.generated = [3]int64{11, 22, 33}
	r.collected = [3]int64{44, 55, 66}
	r.contamination = 0.375
	r.generalSiteID = "landfill-A"
	r.compostSiteID = "compost-C"
	r.trucksAvailable = 7

	r.cells = map[string]*cellState{
		"cell-01": {landUse: LandUseResidential, street: "Elm Row", capacity: 240,
			levels: [3]int64{100, 50, 20}, overflow: [3]int64{5, 3, 1}, vermin: 0.11, missCause: nil},
		"cell-02": {landUse: LandUseCommercial, street: "Trade Way", capacity: 1100,
			levels: [3]int64{300, 120, 60}, overflow: [3]int64{9, 7, 4}, vermin: 0.42, missCause: mc(MissTruckShortage)},
		"cell-03": {landUse: LandUseIndustrial, street: "Foundry Lane", capacity: 6000,
			levels: [3]int64{900, 400, 150}, overflow: [3]int64{20, 13, 6}, vermin: 0.87, missCause: mc(MissStrike)},
	}

	r.rounds = map[string]*roundState{
		"round-A": {id: "round-A", depotID: "depot-1", cells: []string{"cell-01", "cell-02"},
			route: []string{"cell-01", "cell-02"}, overridden: false, completed: false,
			inTransit: [3]int64{12, 8, 3}},
		"round-B": {id: "round-B", depotID: "depot-2", cells: []string{"cell-03"},
			route: []string{"cell-03"}, overridden: true, overrideRoute: []string{"cell-03"},
			completed: true, inTransit: [3]int64{70, 40, 25}},
	}

	r.depots = map[string]bool{"depot-1": true, "depot-2": true, "depot-3": true}

	r.sites = map[string]*disposalSite{
		"landfill-A": {id: "landfill-A", kind: DisposalLandfill, capacity: 100000, used: 25000,
			backlog: [3]int64{500, 0, 0}, reclaimed: false, surrounding: []string{"cell-01", "cell-03"}},
		"incin-B": {id: "incin-B", kind: DisposalIncinerator, backlog: [3]int64{300, 0, 0},
			energy: 8800, airshed: 1.75},
		"compost-C": {id: "compost-C", kind: DisposalCompost, backlog: [3]int64{0, 0, 210},
			compost: 4200},
		"landfill-D": {id: "landfill-D", kind: DisposalLandfill, capacity: 50000, used: 50000,
			reclaimed: true, surrounding: []string{"cell-02"}},
	}

	r.strike = map[string]bool{"depot-1": true, "depot-2": false, "depot-3": true}
}

// injectManyKeys forces MANY entries into every map-backed collection with
// distinct per-index values, so raw map-iteration order (if any emission were
// unsorted) would differ between two saves -- the sorted emission must survive.
func injectManyKeys(r *RefuseAPI) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.generated = [3]int64{101, 202, 303}
	r.collected = [3]int64{404, 505, 606}
	r.contamination = 0.6125
	r.generalSiteID = "site-000"
	r.compostSiteID = "site-007"
	r.trucksAvailable = 123

	landUses := []LandUse{LandUseResidential, LandUseCommercial, LandUseIndustrial}
	caps := []int64{240, 1100, 6000}
	causes := []*MissCause{nil, mc(MissTruckShortage), mc(MissGridlockDelay), mc(MissDepotUnderfunding)}

	r.cells = make(map[string]*cellState)
	for i := 0; i < 30; i++ {
		id := keyN("cell", i)
		r.cells[id] = &cellState{
			landUse:   landUses[i%3],
			street:    keyN("street", i),
			capacity:  caps[i%3],
			levels:    [3]int64{int64(i*3 + 1), int64(i*5 + 2), int64(i*7 + 3)},
			overflow:  [3]int64{int64(i + 1), int64(i + 2), int64(i + 3)},
			vermin:    float64(i) * 0.017,
			missCause: causes[i%len(causes)],
		}
	}

	r.rounds = make(map[string]*roundState)
	for i := 0; i < 20; i++ {
		id := keyN("round", i)
		r.rounds[id] = &roundState{
			id:         id,
			depotID:    keyN("depot", i%15),
			cells:      []string{keyN("cell", i), keyN("cell", i+1)},
			route:      []string{keyN("cell", i)},
			overridden: i%2 == 0,
			completed:  i%3 == 0,
			inTransit:  [3]int64{int64(i*11 + 1), int64(i*13 + 2), int64(i*17 + 3)},
		}
	}

	r.depots = make(map[string]bool)
	for i := 0; i < 15; i++ {
		r.depots[keyN("depot", i)] = true
	}

	kinds := []DisposalKind{DisposalLandfill, DisposalIncinerator, DisposalCompost}
	r.sites = make(map[string]*disposalSite)
	for i := 0; i < 12; i++ {
		id := keyN("site", i)
		r.sites[id] = &disposalSite{
			id:          id,
			kind:        kinds[i%3],
			capacity:    int64(10000 * (i + 1)),
			used:        int64(100 * (i + 1)),
			backlog:     [3]int64{int64(i*2 + 1), int64(i*4 + 2), int64(i*6 + 3)},
			reclaimed:   i%5 == 0,
			surrounding: []string{keyN("cell", i)},
			energy:      int64(i*1000 + 7),
			airshed:     float64(i) * 0.31,
			compost:     int64(i*500 + 9),
		}
	}

	r.strike = make(map[string]bool)
	for i := 0; i < 10; i++ {
		r.strike[keyN("depot", i)] = i%2 == 0
	}
}

func keyN(prefix string, i int) string {
	// zero-padded so lexical order is stable and non-trivial.
	const digits = "0123456789"
	return prefix + "-" + string([]byte{digits[(i/10)%10], digits[i%10]})
}

// ---------------------------------------------------------------------------
// Comparison + save/load drivers.
// ---------------------------------------------------------------------------

// compareRefuse asserts a and b are observably identical across the FULL
// serialized mutable state. It deliberately does NOT compare `provisioned`
// (excluded logistics-coupling cache) or roundState.active (excluded transient
// claim) -- those legitimately differ after a load.
func compareRefuse(t *testing.T, a, b *RefuseAPI, label string) {
	t.Helper()
	if a.generated != b.generated {
		t.Fatalf("%s: generated %v != %v", label, a.generated, b.generated)
	}
	if a.collected != b.collected {
		t.Fatalf("%s: collected %v != %v", label, a.collected, b.collected)
	}
	if a.contamination != b.contamination {
		t.Fatalf("%s: contamination %v != %v", label, a.contamination, b.contamination)
	}
	if a.generalSiteID != b.generalSiteID {
		t.Fatalf("%s: generalSiteID %q != %q", label, a.generalSiteID, b.generalSiteID)
	}
	if a.compostSiteID != b.compostSiteID {
		t.Fatalf("%s: compostSiteID %q != %q", label, a.compostSiteID, b.compostSiteID)
	}
	if a.trucksAvailable != b.trucksAvailable {
		t.Fatalf("%s: trucksAvailable %d != %d", label, a.trucksAvailable, b.trucksAvailable)
	}
	if !reflect.DeepEqual(a.cells, b.cells) {
		t.Fatalf("%s: cells mismatch:\n a=%+v\n b=%+v", label, dumpCells(a.cells), dumpCells(b.cells))
	}
	if !reflect.DeepEqual(a.rounds, b.rounds) {
		t.Fatalf("%s: rounds mismatch", label)
	}
	if !reflect.DeepEqual(a.depots, b.depots) {
		t.Fatalf("%s: depots mismatch:\n a=%+v\n b=%+v", label, a.depots, b.depots)
	}
	if !reflect.DeepEqual(a.sites, b.sites) {
		t.Fatalf("%s: sites mismatch", label)
	}
	if !reflect.DeepEqual(a.strike, b.strike) {
		t.Fatalf("%s: strike mismatch:\n a=%+v\n b=%+v", label, a.strike, b.strike)
	}
}

func dumpCells(m map[string]*cellState) map[string]cellState {
	out := make(map[string]cellState, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

// saveIntoP drives a save of r's participant into a fresh bundle under a temp
// root and returns the bundle root directory.
func saveIntoP(t *testing.T, r *RefuseAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(r)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-refuse"}
	ckErrP(t, mgr.SaveManual(ctx, "det"))
	return root
}

// loadIntoP loads the single manual bundle under root into r.
func loadIntoP(t *testing.T, root string, r *RefuseAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(r)}, cid)
	_, _, err := mgr.Load(manualBundleDirP(t, root))
	ckErrP(t, err)
}

// ---------------------------------------------------------------------------
// Round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestRefuseParticipant_RoundTrip(t *testing.T) {
	orig, _ := newWiredAPI(t)
	injectRichRefuse(orig)

	root := saveIntoP(t, orig, "orig")

	// Load into a FRESH RefuseAPI (same data/refuse.json, empty runtime state
	// replaced by the saved one). Distinct non-zero values throughout mean any
	// dropped field surfaces here as a mismatch (a zero would not equal orig's
	// distinct value).
	reloaded, _ := newWiredAPI(t)
	loadIntoP(t, root, reloaded, "reloaded")
	compareRefuse(t, orig, reloaded, "post-load")

	// Continue identical, logistics-free operations on BOTH and assert they
	// stay equal: a divergent restore would surface the moment new work builds
	// on it. (No RunRound: that reads the un-saved, per-fixture logistics shelf.)
	continueRefuse(t, orig)
	continueRefuse(t, reloaded)
	compareRefuse(t, orig, reloaded, "post-continue")
}

// continueRefuse applies one more deterministic batch that touches ONLY refuse
// state via cfg-driven public methods (identical on both fixtures, same
// data/refuse.json) and never depends on the un-saved logistics shelf.
func continueRefuse(t *testing.T, r *RefuseAPI) {
	t.Helper()
	ckErrP(t, r.RegisterCell("cell-99", LandUseResidential, "New Street"))
	ckErrP(t, r.Generate("cell-99", 3))
	ckErrP(t, r.SetContamination(0.5))
	ckErrP(t, r.RegisterDepot("depot-9"))
	ckErrP(t, r.SetStrike("depot-9", true))
}

// TestRefuseParticipant_ProveCanFail mutates each serialized field family on
// one pristine reload and asserts it diverges from a second pristine reload of
// the SAME bytes -- proving the comparison is discriminating for every field
// (the round-trip's distinct values prove the wire CARRIES them; this proves a
// difference is DETECTED).
func TestRefuseParticipant_ProveCanFail(t *testing.T) {
	orig, _ := newWiredAPI(t)
	injectRichRefuse(orig)
	root := saveIntoP(t, orig, "orig")

	// Each sub-case: load two pristine copies, mutate one, assert divergence.
	cases := []struct {
		name   string
		mutate func(r *RefuseAPI)
	}{
		{"contamination", func(r *RefuseAPI) { r.contamination += 0.1 }},
		{"trucksAvailable", func(r *RefuseAPI) { r.trucksAvailable++ }},
		{"generated", func(r *RefuseAPI) { r.generated[1]++ }},
		{"collected", func(r *RefuseAPI) { r.collected[2]++ }},
		{"generalSiteID", func(r *RefuseAPI) { r.generalSiteID = "elsewhere" }},
		{"compostSiteID", func(r *RefuseAPI) { r.compostSiteID = "elsewhere" }},
		{"cell.levels", func(r *RefuseAPI) { r.cells["cell-02"].levels[0] = 0 }},
		{"cell.overflow", func(r *RefuseAPI) { r.cells["cell-02"].overflow[1] = 0 }},
		{"cell.vermin", func(r *RefuseAPI) { r.cells["cell-03"].vermin = 0 }},
		{"cell.missCause", func(r *RefuseAPI) { r.cells["cell-02"].missCause = nil }},
		{"cell.capacity", func(r *RefuseAPI) { r.cells["cell-01"].capacity = 0 }},
		{"round.inTransit", func(r *RefuseAPI) { r.rounds["round-A"].inTransit[2] = 0 }},
		{"round.completed", func(r *RefuseAPI) { r.rounds["round-A"].completed = true }},
		{"round.overrideRoute", func(r *RefuseAPI) { r.rounds["round-B"].overrideRoute = nil }},
		{"site.used", func(r *RefuseAPI) { r.sites["landfill-A"].used = 0 }},
		{"site.backlog", func(r *RefuseAPI) { r.sites["incin-B"].backlog[0] = 0 }},
		{"site.energy", func(r *RefuseAPI) { r.sites["incin-B"].energy = 0 }},
		{"site.airshed", func(r *RefuseAPI) { r.sites["incin-B"].airshed = 0 }},
		{"site.compost", func(r *RefuseAPI) { r.sites["compost-C"].compost = 0 }},
		{"site.reclaimed", func(r *RefuseAPI) { r.sites["landfill-D"].reclaimed = false }},
		{"site.surrounding", func(r *RefuseAPI) { r.sites["landfill-A"].surrounding = nil }},
		{"depot", func(r *RefuseAPI) { delete(r.depots, "depot-3") }},
		{"strike", func(r *RefuseAPI) { r.strike["depot-2"] = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated, _ := newWiredAPI(t)
			loadIntoP(t, root, mutated, "mutated")
			pristine, _ := newWiredAPI(t)
			loadIntoP(t, root, pristine, "pristine")
			// Sanity: the two pristine loads are equal before the mutation.
			compareRefuse(t, mutated, pristine, "pre-mutation")
			c.mutate(mutated)
			if refuseStatesEqual(mutated, pristine) {
				t.Fatalf("prove-can-fail: mutating %s did not diverge from a pristine reload -- the field may be dropped or the compare is blind to it", c.name)
			}
		})
	}
}

// refuseStatesEqual is compareRefuse as a boolean (for the prove-can-fail
// divergence assertion, which expects INEQUALITY).
func refuseStatesEqual(a, b *RefuseAPI) bool {
	return a.generated == b.generated &&
		a.collected == b.collected &&
		a.contamination == b.contamination &&
		a.generalSiteID == b.generalSiteID &&
		a.compostSiteID == b.compostSiteID &&
		a.trucksAvailable == b.trucksAvailable &&
		reflect.DeepEqual(a.cells, b.cells) &&
		reflect.DeepEqual(a.rounds, b.rounds) &&
		reflect.DeepEqual(a.depots, b.depots) &&
		reflect.DeepEqual(a.sites, b.sites) &&
		reflect.DeepEqual(a.strike, b.strike)
}

// ---------------------------------------------------------------------------
// Byte determinism.
// ---------------------------------------------------------------------------

func TestRefuseParticipant_ByteDeterminism(t *testing.T) {
	r1, _ := newWiredAPI(t)
	injectRichRefuse(r1)
	root1 := saveIntoP(t, r1, "run1")

	r2, _ := newWiredAPI(t)
	injectRichRefuse(r2)
	root2 := saveIntoP(t, r2, "run2")

	assertBundlesByteIdenticalP(t, root1, root2)
}

// TestRefuseAttack_ManyKeyByteDeterminism forces MANY map keys into every
// collection and asserts two saves of the same state are byte-identical --
// proves SORTED emission of the map-backed collections (cells/rounds/depots/
// sites/strike), not just single-key determinism. Two independently-built maps
// iterate in different orders, so an unsorted emission would differ.
func TestRefuseAttack_ManyKeyByteDeterminism(t *testing.T) {
	r1, _ := newWiredAPI(t)
	injectManyKeys(r1)
	root1 := saveIntoP(t, r1, "run1")

	r2, _ := newWiredAPI(t)
	injectManyKeys(r2)
	root2 := saveIntoP(t, r2, "run2")

	if len(r1.cells) < 20 {
		t.Fatalf("test setup: only %d cells -- too few to force map reorder", len(r1.cells))
	}
	assertBundlesByteIdenticalP(t, root1, root2)
}

// TestRefuseAttack_ManyKeyRoundTrip asserts the many-key state round-trips
// exactly (every cell + round + depot + site + strike + the counters).
func TestRefuseAttack_ManyKeyRoundTrip(t *testing.T) {
	orig, _ := newWiredAPI(t)
	injectManyKeys(orig)
	root := saveIntoP(t, orig, "orig")

	reloaded, _ := newWiredAPI(t)
	loadIntoP(t, root, reloaded, "reloaded")

	compareRefuse(t, orig, reloaded, "many-key-load")
}

// ---------------------------------------------------------------------------
// Load-into-non-empty (full replace, not merge) + copyguard.
// ---------------------------------------------------------------------------

// TestRefuseAttack_LoadIntoNonEmptyFullyReplaces: a Load into a RefuseAPI that
// already holds DIFFERENT runtime state must fully overwrite it (Handler
// resets), never merge.
func TestRefuseAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig, _ := newWiredAPI(t)
	injectRichRefuse(orig)
	root := saveIntoP(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger runtime state (the
	// many-key block), including GHOST entries the saved state never touches.
	target, _ := newWiredAPI(t)
	injectManyKeys(target)
	const ghostCell = "cell-27"
	const ghostSite = "site-11"
	if _, ok := orig.cells[ghostCell]; ok {
		t.Fatalf("test setup: saved state unexpectedly holds the ghost cell")
	}
	if _, ok := target.cells[ghostCell]; !ok {
		t.Fatalf("test setup: ghost cell not present on target pre-load")
	}
	if _, ok := target.sites[ghostSite]; !ok {
		t.Fatalf("test setup: ghost site not present on target pre-load")
	}

	loadIntoP(t, root, target, "target")

	if _, ok := target.cells[ghostCell]; ok {
		t.Fatalf("ghost cell survived load -- Handler merged instead of replacing")
	}
	if _, ok := target.sites[ghostSite]; ok {
		t.Fatalf("ghost site survived load -- Handler merged instead of replacing")
	}
	if len(target.cells) != len(orig.cells) {
		t.Fatalf("cells size %d != saved %d -- merge, not replace", len(target.cells), len(orig.cells))
	}
	if len(target.sites) != len(orig.sites) {
		t.Fatalf("sites size %d != saved %d -- merge, not replace", len(target.sites), len(orig.sites))
	}
	compareRefuse(t, orig, target, "load-into-nonempty")
}

// TestRefuseAttack_CopyguardFiresOnParticipant: a struct-copied RefuseAPI's
// participant must fail closed on Kind/Source/Handler.
func TestRefuseAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig, _ := newWiredAPI(t)
	injectRichRefuse(orig)

	// Reproduce a struct-copied RefuseAPI's guard-visible state (self still
	// points at the ORIGINAL) without a vet-copylocks-tripping value copy of
	// the embedded RWMutex.
	var copied RefuseAPI
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
	if NewSaveParticipant(orig).Kind() != KindRefuse {
		t.Fatalf("original participant Kind() broken")
	}
}

// TestRefuseAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestRefuseAttack_UnknownRecordKindRejected(t *testing.T) {
	r, _ := newWiredAPI(t)
	h := NewSaveParticipant(r).Handler()
	if err := h(serialize.Record{Kind: "refuse.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// TestRefuseAttack_ActiveFlagNotPersisted proves the transient `active`
// re-entrancy claim is NOT persisted: a round saved with active=true reloads
// with active=false, so the round is runnable after a load rather than
// permanently stuck.
func TestRefuseAttack_ActiveFlagNotPersisted(t *testing.T) {
	orig, _ := newWiredAPI(t)
	injectRichRefuse(orig)
	orig.mu.Lock()
	orig.rounds["round-A"].active = true
	orig.mu.Unlock()

	root := saveIntoP(t, orig, "orig")
	reloaded, _ := newWiredAPI(t)
	loadIntoP(t, root, reloaded, "reloaded")

	reloaded.mu.RLock()
	got := reloaded.rounds["round-A"].active
	reloaded.mu.RUnlock()
	if got {
		t.Fatalf("reloaded round-A.active = true -- a mid-call claim was persisted, leaving the round un-runnable")
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the finance/unlocks/build pilots).
// ---------------------------------------------------------------------------

func assertBundlesByteIdenticalP(t *testing.T, root1, root2 string) {
	t.Helper()
	dir1 := manualBundleDirP(t, root1)
	dir2 := manualBundleDirP(t, root2)
	files1 := allFilesP(t, dir1)
	files2 := allFilesP(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ckErrP(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ckErrP(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic refuse state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

// manualBundleDirP locates the single manual-save bundle directory under a save
// root by finding the header.json leaf.
func manualBundleDirP(t *testing.T, root string) string {
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
	ckErrP(t, err)
	if found == "" {
		t.Fatalf("no bundle (header.json) found under %q", root)
	}
	return found
}

// allFilesP returns every file under dir, relative to dir, sorted.
func allFilesP(t *testing.T, dir string) []string {
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
	ckErrP(t, err)
	sort.Strings(out)
	return out
}
