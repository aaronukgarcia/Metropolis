package unlocks

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// Field-parity drift tests (the "built but not serialized" guard).
//
// TestUnlocksAPIFieldsAllClassified fails the build if any UnlocksAPI field
// is neither serialized (covered) nor explicitly excluded (runtime/config/
// injected/copy-guard). A new mutable field added without a save is exactly
// the class this inc exists to prevent. TestUnlocksMetaWireFieldsMatchScalars
// keeps the meta wire in lock-step with the serialized SCALAR field set.
// ---------------------------------------------------------------------------

func TestUnlocksAPIFieldsAllClassified(t *testing.T) {
	// Excluded: runtime lock / correlation, immutable config loaded from
	// data/unlock_trees.json, injected dependencies, and the copy guard —
	// deliberately NOT part of a save.
	excluded := map[string]string{
		"mu":            "runtime lock, not state",
		"correlationID": "per-instance error correlation, not simulation state",
		"categories":    "immutable config, loaded from data/unlock_trees.json",
		"nodes":         "immutable config, loaded from data/unlock_trees.json",
		"finance":       "injected dependency, re-wired by the composition root on load",
		"debugGate":     "injected dependency, re-wired by the composition root on load",
		"debugTouch":    "injected dependency, re-wired by the composition root on load",
		"self":          "SEC-020 copy-guard pointer, re-armed by Load",
	}
	// Covered: serialized via unlocksMetaWire (scalars) or a per-item record
	// (unlockedNodes -> unlocks.node, capacity -> unlocks.capacity).
	covered := map[string]bool{
		"xp": true, "population": true, "tier": true, "dp": true,
		"dpSpent": true, "unlockedNodes": true, "expansionPermits": true,
		"capacity": true, "debugTouched": true,
	}
	ut := reflect.TypeOf((*UnlocksAPI)(nil)).Elem()
	for i := 0; i < ut.NumField(); i++ {
		name := ut.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("UnlocksAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this inc forbids", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("UnlocksAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestUnlocksMetaWireFieldsMatchScalars asserts the meta wire carries a
// counterpart for exactly the serialized SCALAR fields of UnlocksAPI (the
// non-map covered fields). A new scalar added to the save without a meta
// wire field, or a wire field with no API field, fails here. tier's wire
// counterpart is int32 (the atomic.Int32's value type), so its kind is
// checked against int32 rather than the atomic struct.
func TestUnlocksMetaWireFieldsMatchScalars(t *testing.T) {
	// domainField -> wireField, plus the reflect.Kind the wire field must
	// have. tier is stored as an atomic.Int32 on the API but serialized as a
	// bare int32.
	want := map[string]struct {
		wire string
		kind reflect.Kind
	}{
		"xp":               {"XP", reflect.Int64},
		"population":       {"Population", reflect.Int64},
		"tier":             {"Tier", reflect.Int32},
		"dp":               {"DP", reflect.Int64},
		"dpSpent":          {"DPSpent", reflect.Int64},
		"expansionPermits": {"ExpansionPermits", reflect.Int64},
		"debugTouched":     {"DebugTouched", reflect.Bool},
	}
	mw := reflect.TypeOf((*unlocksMetaWire)(nil)).Elem()
	if mw.NumField() != len(want) {
		t.Fatalf("unlocksMetaWire has %d fields but %d serialized scalars are expected -- meta wire drifted from the scalar set", mw.NumField(), len(want))
	}
	for _, spec := range want {
		f, ok := mw.FieldByName(spec.wire)
		if !ok {
			t.Fatalf("unlocksMetaWire is missing field %q for a serialized scalar", spec.wire)
		}
		if f.Type.Kind() != spec.kind {
			t.Fatalf("unlocksMetaWire.%s has kind %s, want %s", spec.wire, f.Type.Kind(), spec.kind)
		}
	}
}

// ---------------------------------------------------------------------------
// Shared drivers + comparison helpers.
// ---------------------------------------------------------------------------

func ckErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// allowDebugGate is a DebugGateFunc that always authorizes (debug "on").
func allowDebugGate(string) error { return nil }

// noopDebugTouch is a debug-touch callback that always succeeds.
func noopDebugTouch() error { return nil }

// wireAPI wires a fresh finance + an allowing debug gate + a noop
// debug-touch onto u, so every mutation path (milestone cash awards, Buy,
// ForceUnlock) can fire.
func wireAPI(t *testing.T, u *UnlocksAPI) *finance.FinanceAPI {
	t.Helper()
	f := finance.NewFinanceAPI(testCorrelationID())
	ckErr(t, u.SetFinance(f))
	ckErr(t, u.SetDebugGate(allowDebugGate))
	ckErr(t, u.SetDebugTouch(noopDebugTouch))
	return f
}

// sortedUnlockNodeIDs returns every "unlock"-kind node id, sorted — the
// nodes a test may spend/force-unlock. Reads the immutable index directly
// (in-package).
func sortedUnlockNodeIDs(u *UnlocksAPI) []string {
	out := make([]string, 0, len(u.nodes))
	for id, n := range u.nodes {
		if n.Kind == "unlock" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// driveUnlocks runs a fixed, deterministic sequence exercising every
// serialized field: xp (all four award sources), population + tier
// crossings (dp/permits grants + treasury funding), dp spend + unlocked
// nodes, force-unlock (more nodes + debugTouched + a higher tier), and
// off-map capacity buys. No RNG anywhere (unlocks is RNG-free).
func driveUnlocks(t *testing.T, u *UnlocksAPI) {
	t.Helper()
	cid := testCorrelationID()

	ckErr(t, u.AwardConstructionXP(5_000_000, cid)) // 5 XP
	ckErr(t, u.AwardPopulationXP(1000, cid))
	ckErr(t, u.AwardServiceXP(80, cid))
	ckErr(t, u.AwardMilestoneProgressXP(30, cid))

	// Cross tiers 1..8 (600k < 1M so tier 9 not reached): grants dp+=80,
	// permits+=8, and credits treasury 8 x 100k pounds.
	if _, err := u.AdvancePopulation(600_000, cid); err != nil {
		t.Fatalf("AdvancePopulation: %v", err)
	}

	// Spend DP on a handful of low-tier unlock nodes (tier <= current 8).
	ids := sortedUnlockNodeIDs(u)
	spent := 0
	for _, id := range ids {
		n := u.nodes[id]
		if n.PrereqTier <= u.CurrentTier() && !u.IsNodeUnlocked(id) {
			if err := u.SpendDevelopmentPoints(id, cid); err == nil {
				spent++
			}
			if spent >= 5 {
				break
			}
		}
	}
	if spent == 0 {
		t.Fatalf("driver spent no DP -- test would not exercise dpSpent/unlockedNodes")
	}

	// Force-unlock a high-tier node (bypasses tier + cost): sets
	// debugTouched and adds to unlockedNodes.
	ckErr(t, u.ForceUnlock(ForceTarget{NodeID: "roads_arterial_megastructure_roads"}, cid))
	// Force-reach tier 10 (higher-water mark up from 8).
	ckErr(t, u.ForceUnlock(ForceTarget{Tier: 10}, cid))

	// Buy off-map capacity (treasury funded by the milestone awards).
	ckErr(t, u.BuyOffMapCapacity(OffMapGrid, cid))
	ckErr(t, u.BuyOffMapCapacity(OffMapGrid, cid))
	ckErr(t, u.BuyOffMapCapacity(OffMapGas, cid))
	ckErr(t, u.BuyOffMapCapacity(OffMapWater, cid))
}

// seedTreasury funds u's wired finance treasury (external -> treasury), so
// a capacity buy can succeed. Needed because engine.finance state is NOT
// part of the unlocks save: a reloaded UnlocksAPI is wired to a FRESH,
// empty finance, so any post-load buy must fund it explicitly (in-package
// access to u.finance).
func seedTreasury(t *testing.T, u *UnlocksAPI, amount finance.Money) {
	t.Helper()
	_, err := u.finance.Post(finance.Transaction{
		Description: "test treasury seed",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: finance.Category("test.seed")},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount, Category: finance.Category("test.seed")},
		},
	})
	ckErr(t, err)
}

// continueUnlocks applies one more deterministic batch of operations, so a
// post-load state that silently diverged shows up as unequal reads. It uses
// only unlocks-internal effects plus a self-funded capacity buy, so it does
// NOT depend on the (un-saved, per-instance) finance balance: population
// (no crossing, since tier is already 10 and the next threshold is 10M),
// XP, a force-unlock, and one seed-then-buy.
func continueUnlocks(t *testing.T, u *UnlocksAPI) {
	t.Helper()
	cid := testCorrelationID()
	ckErr(t, u.AwardConstructionXP(2_000_000, cid))
	if _, err := u.AdvancePopulation(6_000_000, cid); err != nil { // no crossing (tier 10, next is 10M)
		t.Fatalf("continue AdvancePopulation: %v", err)
	}
	ckErr(t, u.ForceUnlock(ForceTarget{NodeID: "electricity_nuclear"}, cid))
	seedTreasury(t, u, 1_000_000*finance.MicropoundsPerPound)
	ckErr(t, u.BuyOffMapCapacity(OffMapRail, cid))
}

var allOffMapKinds = []OffMapKind{OffMapGrid, OffMapGas, OffMapRail, OffMapPort, OffMapWater}

// compareUnlocks asserts a and b are observably identical across the full
// read surface: every scalar accessor, dpSpent (internal), every node's
// unlocked flag, and every off-map capacity.
func compareUnlocks(t *testing.T, a, b *UnlocksAPI, label string) {
	t.Helper()
	if a.XP() != b.XP() {
		t.Fatalf("%s: XP %d != %d", label, a.XP(), b.XP())
	}
	if a.CurrentPopulation() != b.CurrentPopulation() {
		t.Fatalf("%s: Population %d != %d", label, a.CurrentPopulation(), b.CurrentPopulation())
	}
	if a.CurrentTier() != b.CurrentTier() {
		t.Fatalf("%s: Tier %d != %d", label, a.CurrentTier(), b.CurrentTier())
	}
	if a.DevelopmentPoints() != b.DevelopmentPoints() {
		t.Fatalf("%s: DP %d != %d", label, a.DevelopmentPoints(), b.DevelopmentPoints())
	}
	if a.ExpansionPermits() != b.ExpansionPermits() {
		t.Fatalf("%s: ExpansionPermits %d != %d", label, a.ExpansionPermits(), b.ExpansionPermits())
	}
	if a.DebugTouched() != b.DebugTouched() {
		t.Fatalf("%s: DebugTouched %v != %v", label, a.DebugTouched(), b.DebugTouched())
	}
	if a.dpSpent != b.dpSpent {
		t.Fatalf("%s: dpSpent %d != %d", label, a.dpSpent, b.dpSpent)
	}
	// Every node's unlocked flag (a and b share the same immutable node set).
	for id := range a.nodes {
		if a.IsNodeUnlocked(id) != b.IsNodeUnlocked(id) {
			t.Fatalf("%s: IsNodeUnlocked(%s) %v != %v", label, id, a.IsNodeUnlocked(id), b.IsNodeUnlocked(id))
		}
	}
	// And the reverse: no ghost unlocked node in b absent from a.
	if len(a.unlockedNodes) != len(b.unlockedNodes) {
		t.Fatalf("%s: unlockedNodes size %d != %d", label, len(a.unlockedNodes), len(b.unlockedNodes))
	}
	for _, k := range allOffMapKinds {
		ca, ea := a.OffMapCapacity(k)
		cb, eb := b.OffMapCapacity(k)
		if (ea == nil) != (eb == nil) || ca != cb {
			t.Fatalf("%s: OffMapCapacity(%s) (%d,%v) != (%d,%v)", label, k, ca, ea, cb, eb)
		}
	}
	if len(a.capacity) != len(b.capacity) {
		t.Fatalf("%s: capacity map size %d != %d", label, len(a.capacity), len(b.capacity))
	}
}

// saveInto drives a save of u's participant into a fresh bundle under a
// temp root and returns the bundle root directory.
func saveInto(t *testing.T, u *UnlocksAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(u)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-build"}
	ckErr(t, mgr.SaveManual(ctx, "det"))
	return root
}

// loadInto loads the single manual bundle under root into u.
func loadInto(t *testing.T, root string, u *UnlocksAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(u)}, cid)
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ckErr(t, err)
}

// ---------------------------------------------------------------------------
// Round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestUnlocksParticipant_RoundTrip(t *testing.T) {
	orig := realAPI(t)
	wireAPI(t, orig)
	driveUnlocks(t, orig)

	root := saveInto(t, orig, "orig")

	// Load into a FRESH UnlocksAPI (same data/unlock_trees.json, empty
	// runtime state replaced by the saved one).
	reloaded := realAPI(t)
	wireAPI(t, reloaded)
	loadInto(t, root, reloaded, "reloaded")

	compareUnlocks(t, orig, reloaded, "post-load")

	// Continue identical operations on BOTH and assert they stay equal: a
	// divergent restore would surface the moment new work builds on it.
	continueUnlocks(t, orig)
	continueUnlocks(t, reloaded)
	compareUnlocks(t, orig, reloaded, "post-continue")

	// Prove-can-fail: mutate one reloaded scalar -> divergence from a second
	// pristine load of the SAME saved bytes (orig has since advanced via
	// continueUnlocks, so compare the two reloads, not against orig).
	reloaded2 := realAPI(t)
	wireAPI(t, reloaded2)
	loadInto(t, root, reloaded2, "reloaded2")
	fresh := realAPI(t)
	wireAPI(t, fresh)
	loadInto(t, root, fresh, "fresh")
	reloaded2.dp += 1
	if reloaded2.DevelopmentPoints() == fresh.DevelopmentPoints() {
		t.Fatalf("prove-can-fail: mutating a reloaded dp did not diverge from the saved value")
	}

	// Prove-can-fail: mutate one reloaded unlocked node -> divergence.
	fresh2 := realAPI(t)
	wireAPI(t, fresh2)
	loadInto(t, root, fresh2, "fresh2")
	// Pick any currently-unlocked node and clear it.
	var anyUnlocked string
	for id := range fresh2.unlockedNodes {
		anyUnlocked = id
		break
	}
	if anyUnlocked == "" {
		t.Fatalf("test setup: reloaded state has no unlocked node to mutate")
	}
	delete(fresh2.unlockedNodes, anyUnlocked)
	if fresh2.IsNodeUnlocked(anyUnlocked) == fresh.IsNodeUnlocked(anyUnlocked) {
		t.Fatalf("prove-can-fail: clearing a reloaded unlocked node did not diverge")
	}
}

// ---------------------------------------------------------------------------
// Byte determinism.
// ---------------------------------------------------------------------------

func TestUnlocksParticipant_ByteDeterminism(t *testing.T) {
	u1 := realAPI(t)
	wireAPI(t, u1)
	driveUnlocks(t, u1)
	root1 := saveInto(t, u1, "run1")

	u2 := realAPI(t)
	wireAPI(t, u2)
	driveUnlocks(t, u2)
	root2 := saveInto(t, u2, "run2")

	assertBundlesByteIdentical(t, root1, root2)
}

// driveManyKeys forces MANY keys into both map-backed collections so raw
// map-iteration order (if any emission were unsorted) would differ between
// two saves -- the attack sorted emission must survive. Force-unlocks EVERY
// "unlock" node (~150) and buys all five off-map capacities.
func driveManyKeys(t *testing.T, u *UnlocksAPI) {
	t.Helper()
	cid := testCorrelationID()
	// Fund treasury for the five capacity buys: cross to tier 9 (9 crossings
	// x 100k pounds = 900k, covers grid+gas+rail+port+water = 450k).
	if _, err := u.AdvancePopulation(1_500_000, cid); err != nil {
		t.Fatalf("AdvancePopulation: %v", err)
	}
	for _, id := range sortedUnlockNodeIDs(u) {
		ckErr(t, u.ForceUnlock(ForceTarget{NodeID: id}, cid))
	}
	for _, k := range allOffMapKinds {
		ckErr(t, u.BuyOffMapCapacity(k, cid))
	}
}

// TestAttack_ManyKeyByteDeterminism forces MANY keys and asserts two saves
// of the same state are byte-identical -- proves sorted emission, not just
// single-key trivial determinism.
func TestAttack_ManyKeyByteDeterminism(t *testing.T) {
	u1 := realAPI(t)
	wireAPI(t, u1)
	driveManyKeys(t, u1)
	root1 := saveInto(t, u1, "run1")

	u2 := realAPI(t)
	wireAPI(t, u2)
	driveManyKeys(t, u2)
	root2 := saveInto(t, u2, "run2")

	// Sanity: the driver actually produced many unlocked nodes.
	if len(u1.unlockedNodes) < 50 {
		t.Fatalf("test setup: only %d unlocked nodes -- too few to force map reorder", len(u1.unlockedNodes))
	}
	assertBundlesByteIdentical(t, root1, root2)
}

// TestAttack_ManyKeyRoundTrip asserts the many-key state round-trips
// exactly (the scalar counters + every node + every capacity).
func TestAttack_ManyKeyRoundTrip(t *testing.T) {
	orig := realAPI(t)
	wireAPI(t, orig)
	driveManyKeys(t, orig)
	root := saveInto(t, orig, "orig")

	reloaded := realAPI(t)
	wireAPI(t, reloaded)
	loadInto(t, root, reloaded, "reloaded")

	compareUnlocks(t, orig, reloaded, "many-key-load")
}

// ---------------------------------------------------------------------------
// Load-into-non-empty (full replace, not merge) + copyguard.
// ---------------------------------------------------------------------------

// TestAttack_LoadIntoNonEmptyFullyReplaces: a Load into an UnlocksAPI that
// already holds DIFFERENT runtime state must fully overwrite it (Handler
// resets), never merge.
func TestAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig := realAPI(t)
	wireAPI(t, orig)
	driveUnlocks(t, orig)
	root := saveInto(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger runtime state.
	target := realAPI(t)
	wireAPI(t, target)
	driveManyKeys(t, target) // many more unlocked nodes + all capacities + higher tier
	// A ghost unlocked node the SAVED state does NOT hold (driveUnlocks does
	// not touch electricity_nuclear); driveManyKeys force-unlocks it on the
	// target, so its survival after load would be a merge signal.
	ghost := "electricity_nuclear"
	if orig.IsNodeUnlocked(ghost) {
		t.Fatalf("test setup: saved state unexpectedly holds the ghost node %q", ghost)
	}
	if !target.IsNodeUnlocked(ghost) {
		t.Fatalf("test setup: ghost node not unlocked on target pre-load")
	}

	loadInto(t, root, target, "target")

	if target.IsNodeUnlocked(ghost) {
		t.Fatalf("ghost node %q survived load -- Handler merged instead of replacing", ghost)
	}
	// Capacity count must equal the SAVED count, not saved+target (driveMany
	// bought all 5; driveUnlocks bought 3 distinct kinds).
	if len(target.capacity) != len(orig.capacity) {
		t.Fatalf("capacity map size %d != saved %d -- merge, not replace", len(target.capacity), len(orig.capacity))
	}
	compareUnlocks(t, orig, target, "load-into-nonempty")
}

// TestAttack_CopyguardFiresOnParticipant: a struct-copied UnlocksAPI's
// participant must fail closed on Kind/Source/Handler.
func TestAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig := realAPI(t)
	wireAPI(t, orig)
	driveUnlocks(t, orig)

	// Reproduce a struct-copied UnlocksAPI's guard-visible state (self still
	// points at the ORIGINAL) without a vet-copylocks-tripping value copy of
	// the embedded RWMutex.
	var copied UnlocksAPI
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
	if NewSaveParticipant(orig).Kind() != KindUnlocks {
		t.Fatalf("original participant Kind() broken")
	}
}

// TestAttack_FalseValuedNodeRoundTrips proves a map[string]bool entry whose
// VALUE is false round-trips as false -- not dropped, not coerced to true.
// The live runtime only ever stores true today (dp.go/force.go), so every
// other round-trip test carries exclusively true-valued nodes and would
// pass even if the node handler assumed `true`. This guards the wire's
// "carry the value, don't assume true" contract with teeth: a present-but-
// false key must survive save+load verbatim. Mutation-proven: change
// applyLoadRecord's recNode case to `= true` and this reddens (every other
// test stays green). GR#23 destructive-round net (FEAT-1972079941 inc2).
func TestAttack_FalseValuedNodeRoundTrips(t *testing.T) {
	orig := realAPI(t)
	wireAPI(t, orig)
	driveUnlocks(t, orig)

	// Plant an explicit present-but-false entry (in-package) the save must
	// preserve verbatim. electricity_nuclear is a real node id and is NOT
	// touched by driveUnlocks, so the only value it can hold is the one we set.
	const falseNode = "electricity_nuclear"
	if orig.IsNodeUnlocked(falseNode) {
		t.Fatalf("test setup: %q unexpectedly already unlocked", falseNode)
	}
	orig.mu.Lock()
	orig.unlockedNodes[falseNode] = false
	orig.mu.Unlock()

	root := saveInto(t, orig, "orig")

	reloaded := realAPI(t)
	wireAPI(t, reloaded)
	loadInto(t, root, reloaded, "reloaded")

	reloaded.mu.RLock()
	v, present := reloaded.unlockedNodes[falseNode]
	reloaded.mu.RUnlock()
	if !present {
		t.Fatalf("false-valued node %q vanished on round-trip -- handler dropped the entry", falseNode)
	}
	if v {
		t.Fatalf("false-valued node %q came back true -- handler assumed true instead of carrying the wire value", falseNode)
	}
}

// TestAttack_ConcurrentReadDuringSave stresses a concurrent reader against a
// Source snapshot: readers hammer the lock-free tier accessor and the
// mu-guarded scalar/map accessors while the participant is snapshotted for
// save. Under -race this flushes any unsynchronised read of the atomic tier
// or the maps during snapshotForSave's locked pass.
func TestAttack_ConcurrentReadDuringSave(t *testing.T) {
	u := realAPI(t)
	wireAPI(t, u)
	driveManyKeys(t, u)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			_ = u.CurrentTier() // lock-free atomic read
			_ = u.XP()
			_ = u.DevelopmentPoints()
			for _, k := range allOffMapKinds {
				_, _ = u.OffMapCapacity(k)
			}
		}
		close(done)
	}()
	for i := 0; i < 50; i++ {
		_ = saveInto(t, u, "concurrent")
	}
	<-done
}

// TestAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestAttack_UnknownRecordKindRejected(t *testing.T) {
	u := realAPI(t)
	wireAPI(t, u)
	h := NewSaveParticipant(u).Handler()
	if err := h(serialize.Record{Kind: "unlocks.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the finance pilot).
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
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic unlocks state (correlation ID differs by design and is NOT persisted)", rel)
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
