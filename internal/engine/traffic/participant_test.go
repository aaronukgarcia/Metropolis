package traffic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// FEAT-1972079941 inc6 — engine.traffic save.Participant tests.
//
// Mirrors the inc5 engine.refuse participant test suite exactly, adapted to
// engine.traffic's mutable state (the demand map, the node graph, and the link
// graph with its accumulated per-link Volume). The five mandatory shapes:
// field-parity drift (TrafficAPI + Node + Link every field), full round-trip
// (prove-can-fail per field/nested-field/array-element, distinct non-zero
// values so a dropped field cannot match by coincidence), byte determinism
// (many-key numerically-sorted uint64 emission), load-into-non-empty (replace
// not merge), and copyguard-fires + unknown-record-kind rejection.
//
// engine.traffic has NO durable scalar state (cfg/correlationID excluded), so —
// unlike refuse — there is no meta record: every record comes from one of the
// three map-backed collections.
// ---------------------------------------------------------------------------

func ckErrT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Field-parity drift tests (the "built but not serialized" guard).
// ---------------------------------------------------------------------------

// TestTrafficAPIFieldsAllClassified fails the build if any TrafficAPI field is
// neither serialized (covered) nor explicitly excluded (runtime/config/
// injected/copy-guard). A new mutable field added without a save is exactly the
// class this inc exists to prevent.
func TestTrafficAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"mu":            "runtime lock, not state",
		"self":          "SEC-020 copy-guard atomic pointer, re-armed by New/Load",
		"roads":         "injected dependency (engine.roads), re-wired by the composition root via SetRoads on load",
		"cfg":           "immutable config, loaded from data/traffic.json (a save must not pin old rules — FEAT-1972079897)",
		"correlationID": "per-instance error correlation, not simulation state",
	}
	// Covered: serialized via a per-item record (demands -> traffic.demand,
	// nodes -> traffic.node, links -> traffic.link). There is no meta record:
	// no durable scalar state exists.
	covered := map[string]bool{
		"demands": true, "nodes": true, "links": true,
	}
	rt := reflect.TypeOf((*TrafficAPI)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("TrafficAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this inc forbids", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("TrafficAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestTrafficNodeFieldsCovered asserts the on-wire node record carries a
// counterpart for every field of the internal Node.
func TestTrafficNodeFieldsCovered(t *testing.T) {
	want := map[string]string{
		"ID": "ID",
	}
	nt := reflect.TypeOf((*Node)(nil)).Elem()
	if nt.NumField() != len(want) {
		t.Fatalf("Node has %d fields but %d are mapped to the wire -- a node field was added without a wire counterpart", nt.NumField(), len(want))
	}
	wt := reflect.TypeOf((*trafficNodeWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := nt.FieldByName(domain); !ok {
			t.Fatalf("Node is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("trafficNodeWire is missing field %q for Node.%s", wire, domain)
		}
	}
}

// TestTrafficLinkFieldsCovered asserts the on-wire link record carries a
// counterpart for every field of the internal Link -- the accumulated Volume is
// the highest-value state a lost save would corrupt (it drives every
// LinkTravelTime BPR result).
func TestTrafficLinkFieldsCovered(t *testing.T) {
	want := map[string]string{
		"ID":     "ID",
		"Start":  "Start",
		"End":    "End",
		"Length": "Length",
		"Volume": "Volume",
	}
	lt := reflect.TypeOf((*Link)(nil)).Elem()
	if lt.NumField() != len(want) {
		t.Fatalf("Link has %d fields but %d are mapped to the wire -- a link field was added without a wire counterpart", lt.NumField(), len(want))
	}
	wt := reflect.TypeOf((*trafficLinkWire)(nil)).Elem()
	for domain, wire := range want {
		if _, ok := lt.FieldByName(domain); !ok {
			t.Fatalf("Link is missing expected field %q", domain)
		}
		if _, ok := wt.FieldByName(wire); !ok {
			t.Fatalf("trafficLinkWire is missing field %q for Link.%s", wire, domain)
		}
	}
}

// TestTrafficDemandWireFieldsCovered asserts the demand wire carries the ID key
// plus the count value -- both halves of a demands map entry.
func TestTrafficDemandWireFieldsCovered(t *testing.T) {
	wt := reflect.TypeOf((*trafficDemandWire)(nil)).Elem()
	for _, name := range []string{"ID", "Count"} {
		if _, ok := wt.FieldByName(name); !ok {
			t.Fatalf("trafficDemandWire is missing field %q", name)
		}
	}
	if wt.NumField() != 2 {
		t.Fatalf("trafficDemandWire has %d fields, want exactly 2 (ID, Count)", wt.NumField())
	}
}

// ---------------------------------------------------------------------------
// Rich-state builders (DISTINCT, NON-ZERO values for EVERY field, so a dropped
// field cannot round-trip by coincidence). Every demand a distinct non-zero
// count; every node a distinct id; every link a distinct id AND distinct
// Start/End/Length/Volume.
// ---------------------------------------------------------------------------

// injectRichTraffic fills t's internal state directly (white-box, same package)
// with distinct, non-zero values across every serialized field.
func injectRichTraffic(tr *TrafficAPI) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	// Distinct non-zero demand per distinct non-zero destination ID. IDs are
	// deliberately NOT in insertion/sorted coincidence with counts.
	tr.demands = map[uint64]int64{
		7:   111,
		3:   222,
		42:  333,
		100: 444,
		1:   555,
	}

	tr.nodes = map[uint64]*Node{
		9:  {ID: 9},
		2:  {ID: 2},
		77: {ID: 77},
	}

	// Every link distinct id AND distinct Start/End/Length/Volume (all
	// non-zero) so a dropped field surfaces as a mismatch.
	tr.links = map[uint64]*Link{
		5:  {ID: 5, Start: 9, End: 2, Length: 12.5, Volume: 340.0},
		11: {ID: 11, Start: 2, End: 77, Length: 6.25, Volume: 1180.5},
		23: {ID: 23, Start: 77, End: 9, Length: 88.125, Volume: 42.75},
	}
}

// injectManyTraffic forces MANY entries into every map-backed collection with
// distinct per-index values, so raw map-iteration order (if any emission were
// unsorted) would differ between two saves -- the numerically-sorted emission
// must survive. Keys are chosen so a LEXICAL sort would order them differently
// from a NUMERIC one (e.g. 2 vs 10), catching a string-sorted regression.
func injectManyTraffic(tr *TrafficAPI) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	tr.demands = make(map[uint64]int64)
	for i := 0; i < 40; i++ {
		id := uint64(i*7 + 2) // distinct, non-zero, non-contiguous
		tr.demands[id] = int64(i*13 + 5)
	}

	tr.nodes = make(map[uint64]*Node)
	for i := 0; i < 25; i++ {
		id := uint64(i*3 + 1)
		tr.nodes[id] = &Node{ID: id}
	}

	tr.links = make(map[uint64]*Link)
	for i := 0; i < 30; i++ {
		id := uint64(i*5 + 3)
		tr.links[id] = &Link{
			ID:     id,
			Start:  uint64(i*3 + 1),
			End:    uint64(i*3 + 4),
			Length: float64(i)*1.5 + 0.25,
			Volume: float64(i)*11.0 + 7.5,
		}
	}
}

// ---------------------------------------------------------------------------
// Comparison + save/load drivers.
// ---------------------------------------------------------------------------

// compareTraffic asserts a and b are observably identical across the FULL
// serialized mutable state.
func compareTraffic(t *testing.T, a, b *TrafficAPI, label string) {
	t.Helper()
	if !reflect.DeepEqual(a.demands, b.demands) {
		t.Fatalf("%s: demands mismatch:\n a=%+v\n b=%+v", label, a.demands, b.demands)
	}
	if !reflect.DeepEqual(dumpNodes(a.nodes), dumpNodes(b.nodes)) {
		t.Fatalf("%s: nodes mismatch:\n a=%+v\n b=%+v", label, dumpNodes(a.nodes), dumpNodes(b.nodes))
	}
	if !reflect.DeepEqual(dumpLinks(a.links), dumpLinks(b.links)) {
		t.Fatalf("%s: links mismatch:\n a=%+v\n b=%+v", label, dumpLinks(a.links), dumpLinks(b.links))
	}
}

func dumpNodes(m map[uint64]*Node) map[uint64]Node {
	out := make(map[uint64]Node, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

func dumpLinks(m map[uint64]*Link) map[uint64]Link {
	out := make(map[uint64]Link, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

func trafficStatesEqual(a, b *TrafficAPI) bool {
	return reflect.DeepEqual(a.demands, b.demands) &&
		reflect.DeepEqual(dumpNodes(a.nodes), dumpNodes(b.nodes)) &&
		reflect.DeepEqual(dumpLinks(a.links), dumpLinks(b.links))
}

// saveIntoT drives a save of tr's participant into a fresh bundle under a temp
// root and returns the bundle root directory.
func saveIntoT(t *testing.T, tr *TrafficAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(tr)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-traffic"}
	ckErrT(t, mgr.SaveManual(ctx, "det"))
	return root
}

// loadIntoT loads the single manual bundle under root into tr.
func loadIntoT(t *testing.T, root string, tr *TrafficAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(tr)}, cid)
	_, _, err := mgr.Load(manualBundleDirT(t, root))
	ckErrT(t, err)
}

// ---------------------------------------------------------------------------
// Round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestTrafficParticipant_RoundTrip(t *testing.T) {
	orig := New()
	injectRichTraffic(orig)

	root := saveIntoT(t, orig, "orig")

	// Load into a FRESH TrafficAPI (same New() cfg defaults, empty runtime
	// state replaced by the saved one). Distinct non-zero values throughout mean
	// any dropped field surfaces here as a mismatch (a zero would not equal
	// orig's distinct value).
	reloaded := New()
	loadIntoT(t, root, reloaded, "reloaded")
	compareTraffic(t, orig, reloaded, "post-load")

	// Continue identical operations on BOTH and assert they stay equal: a
	// divergent restore would surface the moment new work builds on it.
	continueTraffic(t, orig)
	continueTraffic(t, reloaded)
	compareTraffic(t, orig, reloaded, "post-continue")
}

// continueTraffic applies one more deterministic batch that touches ONLY
// traffic state via public methods (identical on both fixtures).
func continueTraffic(t *testing.T, tr *TrafficAPI) {
	t.Helper()
	ckErrT(t, tr.AddDemand(555, 17))
	ckErrT(t, tr.AddNode(999))
	ckErrT(t, tr.AddLink(31, 999, 5, 4.5))
	ckErrT(t, tr.AddLinkVolume(31, 88.0))
	ckErrT(t, tr.AddLinkVolume(5, 10.0)) // top up an existing link's Volume
}

// TestTrafficParticipant_ProveCanFail mutates each serialized field family on
// one pristine reload and asserts it diverges from a second pristine reload of
// the SAME bytes -- proving the comparison is discriminating for every field
// (the round-trip's distinct values prove the wire CARRIES them; this proves a
// difference is DETECTED).
func TestTrafficParticipant_ProveCanFail(t *testing.T) {
	orig := New()
	injectRichTraffic(orig)
	root := saveIntoT(t, orig, "orig")

	cases := []struct {
		name   string
		mutate func(tr *TrafficAPI)
	}{
		{"demand.count", func(tr *TrafficAPI) { tr.demands[42] = 0 }},
		{"demand.key", func(tr *TrafficAPI) { delete(tr.demands, 1) }},
		{"demand.extra", func(tr *TrafficAPI) { tr.demands[9999] = 1 }},
		{"node.drop", func(tr *TrafficAPI) { delete(tr.nodes, 77) }},
		{"node.extra", func(tr *TrafficAPI) { tr.nodes[8888] = &Node{ID: 8888} }},
		{"link.start", func(tr *TrafficAPI) { tr.links[5].Start = 0 }},
		{"link.end", func(tr *TrafficAPI) { tr.links[11].End = 0 }},
		{"link.length", func(tr *TrafficAPI) { tr.links[23].Length = 0 }},
		{"link.volume", func(tr *TrafficAPI) { tr.links[11].Volume = 0 }},
		{"link.drop", func(tr *TrafficAPI) { delete(tr.links, 5) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := New()
			loadIntoT(t, root, mutated, "mutated")
			pristine := New()
			loadIntoT(t, root, pristine, "pristine")
			// Sanity: the two pristine loads are equal before the mutation.
			compareTraffic(t, mutated, pristine, "pre-mutation")
			c.mutate(mutated)
			if trafficStatesEqual(mutated, pristine) {
				t.Fatalf("prove-can-fail: mutating %s did not diverge from a pristine reload -- the field may be dropped or the compare is blind to it", c.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Byte determinism.
// ---------------------------------------------------------------------------

func TestTrafficParticipant_ByteDeterminism(t *testing.T) {
	r1 := New()
	injectRichTraffic(r1)
	root1 := saveIntoT(t, r1, "run1")

	r2 := New()
	injectRichTraffic(r2)
	root2 := saveIntoT(t, r2, "run2")

	assertBundlesByteIdenticalT(t, root1, root2)
}

// TestTrafficAttack_ManyKeyByteDeterminism forces MANY map keys into every
// collection and asserts two saves of the same state are byte-identical --
// proves NUMERICALLY-SORTED uint64 emission of the map-backed collections
// (demands/nodes/links), not just single-key determinism. Two independently
// built maps iterate in different orders, so an unsorted (or lexically-sorted)
// emission would differ.
func TestTrafficAttack_ManyKeyByteDeterminism(t *testing.T) {
	r1 := New()
	injectManyTraffic(r1)
	root1 := saveIntoT(t, r1, "run1")

	r2 := New()
	injectManyTraffic(r2)
	root2 := saveIntoT(t, r2, "run2")

	if len(r1.demands) < 20 {
		t.Fatalf("test setup: only %d demands -- too few to force map reorder", len(r1.demands))
	}
	assertBundlesByteIdenticalT(t, root1, root2)
}

// TestTrafficAttack_NumericNotLexicalOrder pins the emission order to be
// NUMERIC (2 before 10), not merely deterministic. The ManyKey byte-determinism
// test compares two saves of the same state, so it catches an UNSORTED (raw
// map-range) regression but NOT a lexically-sorted one (a lexical sort is still
// deterministic between two runs, so those bytes stay identical). This test
// reads the actual record stream and asserts the map keys within each record
// family are ascending as uint64 — a lexical string sort would place id 10
// before id 2 within a family and redden here. Each family uses keys that a
// lexical sort orders differently from a numeric one (2,10,100 and 3,30,300).
func TestTrafficAttack_NumericNotLexicalOrder(t *testing.T) {
	tr := New()
	tr.mu.Lock()
	tr.demands = map[uint64]int64{2: 20, 10: 100, 100: 1000, 3: 30}
	tr.nodes = map[uint64]*Node{2: {ID: 2}, 10: {ID: 10}, 100: {ID: 100}, 3: {ID: 3}}
	tr.links = map[uint64]*Link{
		2:   {ID: 2, Start: 1, End: 1, Length: 1, Volume: 1},
		10:  {ID: 10, Start: 1, End: 1, Length: 1, Volume: 1},
		100: {ID: 100, Start: 1, End: 1, Length: 1, Volume: 1},
		3:   {ID: 3, Start: 1, End: 1, Length: 1, Volume: 1},
	}
	tr.mu.Unlock()

	// Drain the Source record stream, collecting the ID per record family.
	src := NewSaveParticipant(tr).Source()
	perKind := map[string][]uint64{}
	for {
		rec, ok, err := src()
		ckErrT(t, err)
		if !ok {
			break
		}
		var w struct {
			ID uint64 `json:"id"`
		}
		ckErrT(t, json.Unmarshal(rec.Data, &w))
		perKind[rec.Kind] = append(perKind[rec.Kind], w.ID)
	}

	for kind, ids := range perKind {
		if len(ids) != 4 {
			t.Fatalf("%s: expected 4 records, got %d (%v)", kind, len(ids), ids)
		}
		for i := 1; i < len(ids); i++ {
			if ids[i-1] >= ids[i] {
				t.Fatalf("%s: record IDs not strictly ascending as uint64: %v -- a lexical (string) sort would order 10 or 100 before 2/3; emission MUST be numeric (GR#21)", kind, ids)
			}
		}
		// Explicitly assert the numeric order (not the lexical "10,100,2,3").
		want := []uint64{2, 3, 10, 100}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("%s: emission order %v != numeric %v (lexical would be [10 100 2 3])", kind, ids, want)
		}
	}
}

// TestTrafficAttack_ManyKeyRoundTrip asserts the many-key state round-trips
// exactly (every demand + node + link with its distinct per-index values).
func TestTrafficAttack_ManyKeyRoundTrip(t *testing.T) {
	orig := New()
	injectManyTraffic(orig)
	root := saveIntoT(t, orig, "orig")

	reloaded := New()
	loadIntoT(t, root, reloaded, "reloaded")

	compareTraffic(t, orig, reloaded, "many-key-load")
}

// ---------------------------------------------------------------------------
// Load-into-non-empty (full replace, not merge) + copyguard.
// ---------------------------------------------------------------------------

// TestTrafficAttack_LoadIntoNonEmptyFullyReplaces: a Load into a TrafficAPI that
// already holds DIFFERENT runtime state must fully overwrite it (Handler
// resets), never merge.
func TestTrafficAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig := New()
	injectRichTraffic(orig)
	root := saveIntoT(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger runtime state (the
	// many-key block), including GHOST entries the saved state never touches.
	target := New()
	injectManyTraffic(target)
	const ghostDemand = uint64(275) // 40 demands go up to i=39 -> 39*7+2=275
	const ghostLink = uint64(148)   // 30 links go up to i=29 -> 29*5+3=148
	if _, ok := orig.demands[ghostDemand]; ok {
		t.Fatalf("test setup: saved state unexpectedly holds the ghost demand")
	}
	if _, ok := target.demands[ghostDemand]; !ok {
		t.Fatalf("test setup: ghost demand not present on target pre-load")
	}
	if _, ok := target.links[ghostLink]; !ok {
		t.Fatalf("test setup: ghost link not present on target pre-load")
	}

	loadIntoT(t, root, target, "target")

	if _, ok := target.demands[ghostDemand]; ok {
		t.Fatalf("ghost demand survived load -- Handler merged instead of replacing")
	}
	if _, ok := target.links[ghostLink]; ok {
		t.Fatalf("ghost link survived load -- Handler merged instead of replacing")
	}
	if len(target.demands) != len(orig.demands) {
		t.Fatalf("demands size %d != saved %d -- merge, not replace", len(target.demands), len(orig.demands))
	}
	if len(target.links) != len(orig.links) {
		t.Fatalf("links size %d != saved %d -- merge, not replace", len(target.links), len(orig.links))
	}
	if len(target.nodes) != len(orig.nodes) {
		t.Fatalf("nodes size %d != saved %d -- merge, not replace", len(target.nodes), len(orig.nodes))
	}
	compareTraffic(t, orig, target, "load-into-nonempty")
}

// TestTrafficAttack_CopyguardFiresOnParticipant: a struct-copied TrafficAPI's
// participant must fail closed on Kind/Source/Handler.
func TestTrafficAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig := New()
	injectRichTraffic(orig)

	// Reproduce a struct-copied TrafficAPI's guard-visible state (self still
	// points at the ORIGINAL) without a vet-copylocks-tripping value copy of the
	// embedded RWMutex.
	var copied TrafficAPI
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
	if NewSaveParticipant(orig).Kind() != KindTraffic {
		t.Fatalf("original participant Kind() broken")
	}
}

// TestTrafficAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestTrafficAttack_UnknownRecordKindRejected(t *testing.T) {
	tr := New()
	h := NewSaveParticipant(tr).Handler()
	if err := h(serialize.Record{Kind: "traffic.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the finance/unlocks/build/refuse
// pilots).
// ---------------------------------------------------------------------------

func assertBundlesByteIdenticalT(t *testing.T, root1, root2 string) {
	t.Helper()
	dir1 := manualBundleDirT(t, root1)
	dir2 := manualBundleDirT(t, root2)
	files1 := allFilesT(t, dir1)
	files2 := allFilesT(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ckErrT(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ckErrT(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic traffic state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

// manualBundleDirT locates the single manual-save bundle directory under a save
// root by finding the header.json leaf.
func manualBundleDirT(t *testing.T, root string) string {
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
	ckErrT(t, err)
	if found == "" {
		t.Fatalf("no bundle (header.json) found under %q", root)
	}
	return found
}

// allFilesT returns every file under dir, relative to dir, sorted.
func allFilesT(t *testing.T, dir string) []string {
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
	ckErrT(t, err)
	sort.Strings(out)
	return out
}
