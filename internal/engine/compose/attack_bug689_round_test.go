package compose

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// attack_bug689_round_test.go — INDEPENDENT DESTRUCTIVE ROUND against
// BUG-689's engine.deathservices compose wiring (GR#23; attacker is not the
// author). Every test here was written to BREAK the landed wiring, not to
// confirm it. Findings that could not be turned into a passing regression
// (because they pin a CURRENT gap rather than a CURRENT guarantee) are
// marked FINDING in their doc comment and assert the gap explicitly, so
// closing the gap makes the test fail loudly and forces the pin to be
// re-read rather than silently rotting.

// attackSeed is this file's own dedicated world seed, distinct from
// bug689Seed (bug689_deathservices_wire_test.go) and roundTripSeed, so a
// future edit to either of those cannot silently change this file's
// citizen-id or death-count assumptions.
const attackSeed = uint64(68909)

// stripDeathServicesShard rewrites the save bundle's header.json to drop
// the "deathservices" entry from its shardIndex, producing a bundle
// byte-indistinguishable from one written by a PRE-BUG-689 binary (whose
// Participants() list had no deathservices participant at all). The shard
// FILE is deleted too, so nothing about the resulting bundle depends on an
// orphan file being tolerated.
//
// serialize.ValidateBundle validates only the shards the header LISTS (it
// has no orphan-file check), and save.Load iterates header.ShardIndex — so
// a participant with no matching shard is simply never invoked, exactly as
// it would have been for a genuine old save.
func stripDeathServicesShard(t *testing.T, root string) {
	t.Helper()
	summaries, _, err := save.List(root)
	if err != nil {
		t.Fatalf("save.List: %v", err)
	}
	dir := ""
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			dir = s.Path
			break
		}
	}
	if dir == "" {
		t.Fatalf("no composition save named %q under %q", compositionSaveName, root)
	}
	headerPath := filepath.Join(dir, "header.json")
	raw, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatalf("reading header.json: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(raw, &hdr); err != nil {
		t.Fatalf("decoding header.json: %v", err)
	}
	shards, ok := hdr["shardIndex"].([]any)
	if !ok {
		t.Fatalf("header.json shardIndex is %T, want []any", hdr["shardIndex"])
	}
	kept := make([]any, 0, len(shards))
	removed := 0
	for _, s := range shards {
		m, ok := s.(map[string]any)
		if !ok {
			t.Fatalf("shardIndex entry is %T, want map", s)
		}
		if m["Kind"] == deathservices.KindDeathServices {
			removed++
			if name, ok := m["Name"].(string); ok {
				_ = os.Remove(filepath.Join(dir, "shards", name+".ndjson.gz"))
				_ = os.Remove(filepath.Join(dir, "shards", name))
			}
			continue
		}
		kept = append(kept, s)
	}
	if removed != 1 {
		t.Fatalf("stripped %d deathservices shard entries from header.json, want exactly 1 (the save under test must actually contain one)", removed)
	}
	hdr["shardIndex"] = kept
	out, err := json.Marshal(hdr)
	if err != nil {
		t.Fatalf("re-encoding header.json: %v", err)
	}
	if err := os.WriteFile(headerPath, out, 0o600); err != nil {
		t.Fatalf("writing header.json: %v", err)
	}
}

// saveWithDeaths drives a fresh composition far enough to have real
// intaken deaths, saves it to a temp root, and returns that root plus the
// pre-save (cursor, conservation) pair.
func saveWithDeaths(t *testing.T, months int) (root string, cursor int64, cons deathservices.Conservation) {
	t.Helper()
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	for i := 0; i < months; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	ds := comp.DeathServices()
	cursor, err = ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	cons, err = ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cursor == 0 || cons.BodiesReleased == 0 {
		t.Fatalf("fixture produced no deaths (cursor=%d released=%d)", cursor, cons.BodiesReleased)
	}
	root = t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return root, cursor, cons
}

// TestAttackBUG689_OldSaveWithoutDeathServicesShardLoadsClean is attack
// surface 6: a save written by a PRE-BUG-689 binary (no deathservices
// shard) must decode without error into a BUG-689-era composition, leaving
// deathservices at its pristine construction state — the FEAT-087
// empty-queue precedent. PASSES.
func TestAttackBUG689_OldSaveWithoutDeathServicesShardLoadsClean(t *testing.T) {
	cid := errs.NewCorrelationID()
	root, _, _ := saveWithDeaths(t, 2)
	stripDeathServicesShard(t, root)

	apiB := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	eB := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, &Deps{Citizens: apiB})
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	if err := compB.Load(root); err != nil {
		t.Fatalf("Load of a pre-BUG-689 save (no deathservices shard) failed: %v — old saves must still decode", err)
	}
	dsB := compB.DeathServices()
	cursorB, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor B: %v", err)
	}
	if cursorB != 0 {
		t.Fatalf("HandoffCursor after loading a shard-less save = %d, want 0 (a fresh module must not invent a cursor)", cursorB)
	}
	consB, err := dsB.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot B: %v", err)
	}
	if consB.BodiesReleased != 0 {
		t.Fatalf("BodiesReleased after loading a shard-less save = %d, want 0", consB.BodiesReleased)
	}
	// And the module must then CATCH UP on the restored handoff stream
	// rather than staying inert: those deaths were never intaken by the
	// old binary, so intaking them now is the correct behaviour.
	advanceInChunks(t, eB, int64(core.DailyTicksPerMonth))
	cursorB2, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor B2: %v", err)
	}
	handoffB, err := compB.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff B: %v", err)
	}
	if int(cursorB2) != len(handoffB) {
		t.Fatalf("post-old-save catch-up left the cursor at %d against a %d-entry handoff stream", cursorB2, len(handoffB))
	}
}

// TestAttackBUG689_ShardlessLoadResetsDeathServices is the FIXED form of
// FINDING F1 (P2, latent; PIN DELETED — the fix landed in the same round
// this file's attack was drafted against). The gap: SaveParticipant.
// Handler() (participant.go) used to call resetForLoad lazily — ON THE
// FIRST RECORD — so a bundle with NO deathservices shard (every
// pre-BUG-689 save) never invoked the Handler at all, and resetForLoad
// never ran, leaving the target module's live state exactly as it was
// while every OTHER module in the composition WAS reset and restored.
//
// Fix (two parts, both load-bearing on their own):
//  1. Handler() now resets EAGERLY on construction, not lazily on the
//     first record — covers any caller that reaches Handler() directly.
//  2. compose.Composition.Load (save_wire.go) checks the bundle's OWN
//     validated header for a deathservices shard AFTER a successful
//     mgr.Load and calls the newly exported DeathServicesAPI.ResetForLoad
//     when it is absent — covers the actual defect, since save.Manager.
//     Load's shard loop ranges over header.ShardIndex and therefore never
//     calls Handler() at all for a participant whose shard is missing;
//     part 1 alone cannot reach this path.
//
// This test drives the exact scenario the deleted pin proved broken —
// composition B runs 4 months (deathservices strictly AHEAD of anything a
// 2-month bundle could restore) then loads a shard-stripped 2-month save —
// and now asserts the CORRECT outcome: deathservices lands at a clean,
// zero cursor, and the citizens handoff stream (also restored) is only 50
// entries long, so the module no longer drops the next N realised deaths
// through the desynced-cursor bug F1 named.
func TestAttackBUG689_ShardlessLoadResetsDeathServices(t *testing.T) {
	cid := errs.NewCorrelationID()
	root, _, _ := saveWithDeaths(t, 2)
	stripDeathServicesShard(t, root)

	// B runs LONGER than the save point, so its deathservices state is
	// strictly ahead of anything the bundle could restore — the shape that
	// makes a stale (unreset) cursor observable.
	apiB := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	eB := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, &Deps{Citizens: apiB})
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	for i := 0; i < 4; i++ {
		advanceInChunks(t, eB, int64(core.DailyTicksPerMonth))
	}
	dsB := compB.DeathServices()
	cursorBefore, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor before: %v", err)
	}
	if cursorBefore == 0 {
		t.Fatal("fixture: B produced no deaths before the load")
	}

	if err := compB.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cursorAfter, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor after: %v", err)
	}
	consAfter, err := dsB.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	handoffAfter, err := compB.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff after: %v", err)
	}

	if cursorAfter != 0 {
		t.Fatalf("F1 regression: shard-less load left HandoffCursor=%d, want 0 (clean module state) — was %d before the load", cursorAfter, cursorBefore)
	}
	if consAfter.BodiesReleased != 0 {
		t.Fatalf("F1 regression: shard-less load left BodiesReleased=%d, want 0", consAfter.BodiesReleased)
	}

	// And the module must then CATCH UP on the restored handoff stream
	// rather than staying inert or double-dropping: those deaths were never
	// intaken relative to this fresh cursor, so intaking them now on the
	// next monthly tick is the correct behaviour, exactly like the
	// existing shard-PRESENT precedent
	// (TestAttackBUG689_OldSaveWithoutDeathServicesShardLoadsClean).
	advanceInChunks(t, eB, int64(core.DailyTicksPerMonth))
	cursorCaughtUp, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor caught-up: %v", err)
	}
	handoffCaughtUp, err := compB.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff caught-up: %v", err)
	}
	if int(cursorCaughtUp) != len(handoffCaughtUp) {
		t.Fatalf("post-shard-less-load catch-up left the cursor at %d against a %d-entry handoff stream (pre-load handoff was %d entries)", cursorCaughtUp, len(handoffCaughtUp), len(handoffAfter))
	}
}

// TestAttackBUG689_LoadDeathServicesFailureIsFailClosed exercises the AC-4
// seam the Deps.LoadDeathServices doc comment explicitly CLAIMS a test for
// ("a caller injects a failing loader and asserts Wire returns
// ErrModuleFailed naming deathservices with zero hooks left behind") — no
// such test existed in the landed estate (FINDING F3, doc-claims-a-test).
// This is that test.
func TestAttackBUG689_LoadDeathServicesFailureIsFailClosed(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	_, err := Wire(e, &Deps{
		LoadDeathServices: func(correlationID string) (*deathservices.DeathServicesAPI, error) {
			return nil, errs.New("MET-G801", correlationID, map[string]any{"module": "deathservices", "cause": "injected failure"})
		},
	})
	if err == nil {
		t.Fatal("Wire returned nil for a failing deathservices loader")
	}
	var e2 *errs.E
	if ok := asErrsE(err, &e2); !ok {
		t.Fatalf("Wire error is %T, want *errs.E", err)
	}
	if e2.Code != ErrModuleFailed {
		t.Fatalf("Wire error code = %q, want %q", e2.Code, ErrModuleFailed)
	}
	if got := e2.Ctx["module"]; got != "deathservices" {
		t.Fatalf("Wire error context module = %v, want \"deathservices\"", got)
	}
	if got := e.HookCount(); got != 0 {
		t.Fatalf("engine left with %d hooks after a failed deathservices Wire, want 0 (no partial wiring)", got)
	}
}

// asErrsE is a local errors.As shim kept explicit so this file does not
// depend on whichever helper the surrounding package happens to use today.
func asErrsE(err error, target **errs.E) bool {
	e, ok := err.(*errs.E)
	if !ok {
		return false
	}
	*target = e
	return true
}

// TestAttackBUG720_FINDING_NoDisposalIsEverDriven_FIXED is FINDING F2 (P1,
// scope)'s FIXED form — the pin's own doc comment named the exact
// condition that would flip it: "The first increment that wires a
// disposal step will break it; that break is the signal to delete the pin."
// BUG-720 is that increment: deathServicesRunHook (compose.go, registered
// daily, right after "build") now drives RunHearseTransport/Bury,
// Cremate, and — while dispensation is active — Dispense every simulated
// day. Renamed from the FINDING/pin form to a FIXED regression proving the
// gap stays closed: WITHOUT at least one registered cemetery or
// crematorium, backlog still equals released (the disposal channels have
// nothing to dispose INTO — that is not the class of gap this bug closes,
// see the compose test's TestAttackBUG720_RunLoop_DrainsAwaitingBacklog
// suite for the actual crematorium/cemetery-registered proof), but with
// one crematorium registered, the SAME driven months now measurably
// dispose of bodies — the answer to Aaron's "when do we see crematoriums
// and hearses?" is no longer "never".
func TestAttackBUG720_FINDING_NoDisposalIsEverDriven_FIXED(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens:               api,
		DeathServiceCrematoria: []string{"crematorium-1"},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	for i := 0; i < 4; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	ds := comp.DeathServices()
	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cons.BodiesReleased == 0 {
		t.Fatal("fixture produced no deaths")
	}
	backlog, err := ds.AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if int64(backlog) == cons.BodiesReleased {
		t.Fatalf("F2 REGRESSED: all %d released bodies are still Awaiting with a crematorium registered — "+
			"the BUG-720 disposal run loop is no longer driving Cremate/Dispense (conservation snapshot %+v).",
			cons.BodiesReleased, cons)
	}
	if cons.BodiesCremated == 0 && cons.BodiesHandledByDispensation == 0 {
		t.Fatalf("F2 REGRESSED: zero bodies cremated AND zero dispensed after 4 composed months with a "+
			"crematorium registered — some other channel drained the backlog instead of the ones this bug wires "+
			"(conservation snapshot %+v).", cons)
	}
	t.Logf("F2 FIXED (BUG-720): after 4 composed months with one crematorium registered, %d of %d released "+
		"bodies were disposed of (cremated=%d, dispensed=%d), only %d left Awaiting (conservation snapshot %+v). "+
		"Crematoriums and hearses now RUN.",
		cons.BodiesReleased-int64(backlog), cons.BodiesReleased, cons.BodiesCremated, cons.BodiesHandledByDispensation, backlog, cons)
}

// TestAttackBUG689_ComposeUsesLiveModuleDrainCapacity is the FIXED form of
// FINDING F4 (P2, GR#3 SSOT; PIN DELETED — G2 landed in the same round this
// attack was drafted against).
//
// The gap: MOD-083 shipped its OWN composition-root wiring call,
// deathservices.DeathServicesAPI.WireDrainCapacity, whose doc comment
// states in terms that it "is the call a FUTURE composition-root wiring
// pass would make". compose.go used to bypass it entirely and hand-roll a
// SECOND, DIFFERENT drain instead:
//
//	old compose: citizens.DrainCapacityFunc(... HearseMonthlyBudget() ...)
//	             == the STATIC data-file constant, forever.
//	module:      MonthlyDrainCapacity
//	             == live plot capacity + cremation headroom + REMAINING
//	                hearse budget, recomputed every call.
//
// Two implementations of one concept, with the module's own registered
// adapter left dead — the same built-but-not-wired class BUG-689 exists to
// close. Fix (G2): compose.go's Wire now calls
// deathServicesAPI.WireDrainCapacity(c, cid) directly (compose.go, the
// FEAT-087 inc3/BUG-689 comment block) instead of hand-rolling a second
// closure — deleting the old bypass rather than leaving it to bit-rot
// alongside the real adapter.
//
// This is checked here as a SOURCE-LEVEL assertion (grepping compose.go's
// text, the same technique F2's own doc comment already uses to establish
// "grepping the whole composition root for deathservices call sites finds
// exactly two") rather than by driving population numbers through it,
// because — as this finding's own doc explained — the two implementations
// are numerically INDISTINGUISHABLE through any full-engine run for
// today's data (TestBUG689_DrainCapacityNeverBindsForDefaultData: the
// ordinary mortality smoothing budget of 25/month binds before either
// candidate drain figure, 300 or 660+, ever could) — the only way to prove
// which call compose actually makes is to read the call it makes.
// TestBUG689_G1_WiredDrainCapacityBindsDeathQueueRelease (this package)
// separately proves the underlying WireDrainCapacity mechanism is not a
// no-op at the DeathQueue level.
func TestAttackBUG689_ComposeUsesLiveModuleDrainCapacity(t *testing.T) {
	src, err := os.ReadFile("compose.go")
	if err != nil {
		t.Fatalf("reading compose.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "deathServicesAPI.WireDrainCapacity(c, cid)") {
		t.Fatal("F4 regression: compose.go no longer calls deathServicesAPI.WireDrainCapacity(c, cid) — " +
			"the composition root must wire MOD-083's own live-capacity adapter, not a hand-rolled substitute")
	}
	if strings.Contains(text, "citizens.DrainCapacityFunc(func(int64) int {") {
		t.Fatal("F4 regression: compose.go reintroduced a hand-rolled citizens.DrainCapacityFunc closure — " +
			"this is the exact bypass G2 removed; use deathServicesAPI.WireDrainCapacity instead")
	}

	// Independently confirm WireDrainCapacity itself is what feeds through
	// to citizens: the SAME numeric divergence F4 originally pinned (a
	// registered crematorium changes MonthlyDrainCapacity but never
	// HearseMonthlyBudget alone) still exists as a live property of the
	// module, proving there is a real, non-trivial capacity for compose's
	// call to actually be picking up.
	cid := errs.NewCorrelationID()
	ds, err := deathservices.LoadDefault(cid)
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	hearseOnly := func() int {
		budget, err := ds.HearseMonthlyBudget(cid)
		if err != nil || budget < 0 {
			return 0
		}
		return int(budget)
	}
	if got, want := hearseOnly(), ds.MonthlyDrainCapacity(0); got != want {
		t.Fatalf("baseline mismatch before any capacity exists: hearse-only=%d module=%d", got, want)
	}
	// Register one crematorium — something a real city obviously builds.
	if err := ds.RegisterCrematorium("crem-1", cid); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	hearseOnlyVal := hearseOnly()
	moduleVal := ds.MonthlyDrainCapacity(0)
	if hearseOnlyVal == moduleVal {
		t.Fatalf("test setup invalid: hearse-only=%d module=%d after registering a crematorium — "+
			"these must still diverge for the source-level assertion above to mean anything", hearseOnlyVal, moduleVal)
	}
	t.Logf("with one crematorium registered, the module's own live DrainCapacity reports %d while "+
		"HearseMonthlyBudget alone still reports the static %d — compose.go now calls WireDrainCapacity, "+
		"which feeds citizens the live %d figure, not the static one.", moduleVal, hearseOnlyVal, moduleVal)
}

// TestAttackBUG689_InjectedInstanceIsTheOneDriven proves the Deps
// test seam is not decorative: an injected DeathServicesAPI must be the
// exact instance the monthly hook feeds and Participants() serializes.
func TestAttackBUG689_InjectedInstanceIsTheOneDriven(t *testing.T) {
	cid := errs.NewCorrelationID()
	injected, err := deathservices.LoadDefault(cid)
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	api := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServices: injected})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp.DeathServices() != injected {
		t.Fatal("Composition.DeathServices() is not the injected instance")
	}
	for i := 0; i < 3; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	cursor, err := injected.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	if cursor == 0 {
		t.Fatal("the injected instance never received an Intake — Deps.DeathServices is wired but not driven")
	}
}

// TestAttackBUG689_ConcurrentReadersDuringTicks hammers the exported
// deathservices surface from many goroutines while the engine ticks, which
// is the real webconsole/HUD shape (a UI polling backlog/conservation while
// the sim runs). Under -race this is the module-boundary race probe for the
// new HandoffCursor/IntakeFromHandoff pair.
func TestAttackBUG689_ConcurrentReadersDuringTicks(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(4))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.DeathServices()

	var stop atomic.Bool
	var wg sync.WaitGroup
	var readErr atomic.Value
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := ds.HandoffCursor(cid); err != nil {
					readErr.Store(err)
					return
				}
				if _, err := ds.Snapshot(cid); err != nil {
					readErr.Store(err)
					return
				}
				if _, err := ds.AwaitingBacklog(cid); err != nil {
					readErr.Store(err)
					return
				}
				if _, err := ds.AwaitingSorted(cid); err != nil {
					readErr.Store(err)
					return
				}
				// The drain path citizens itself calls every month.
				_ = ds.MonthlyDrainCapacity(0)
			}
		}()
	}
	for i := 0; i < 3; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	stop.Store(true)
	wg.Wait()
	if v := readErr.Load(); v != nil {
		t.Fatalf("concurrent reader failed: %v", v)
	}

	// The exactly-once identity must still hold after the hammering.
	cursor, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	handoff, err := api.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if int(cursor) != len(handoff) {
		t.Fatalf("exactly-once broken under concurrent readers: cursor=%d handoff=%d", cursor, len(handoff))
	}
	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cons.Sum() != cons.BodiesReleased || cons.BodiesReleased != int64(len(handoff)) {
		t.Fatalf("conservation broken under concurrent readers: %+v vs handoff %d", cons, len(handoff))
	}
}

// TestAttackBUG689_RepeatedSaveLoadCyclesAreStable drives save/restore
// repeatedly, alternating months, to shake out any cursor drift that a
// single round trip would hide (the MOD-083 rounds' own partial-commit
// class). Every cycle must land on the same exactly-once identity.
func TestAttackBUG689_RepeatedSaveLoadCyclesAreStable(t *testing.T) {
	cid := errs.NewCorrelationID()
	root := t.TempDir()

	api := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	current := comp
	currentAPI := api
	for cycle := 0; cycle < 4; cycle++ {
		advanceInChunks(t, current.state.e, int64(core.DailyTicksPerMonth))
		if err := current.Save(root); err != nil {
			t.Fatalf("cycle %d Save: %v", cycle, err)
		}
		nextAPI := buildGuaranteedDeathCitizensAPI(t, attackSeed)
		nextE := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
		next, err := Wire(nextE, &Deps{Citizens: nextAPI})
		if err != nil {
			t.Fatalf("cycle %d Wire: %v", cycle, err)
		}
		if err := next.Load(root); err != nil {
			t.Fatalf("cycle %d Load: %v", cycle, err)
		}
		cursor, err := next.DeathServices().HandoffCursor(cid)
		if err != nil {
			t.Fatalf("cycle %d HandoffCursor: %v", cycle, err)
		}
		handoff, err := next.state.citizens.DeathHandoff(cid)
		if err != nil {
			t.Fatalf("cycle %d DeathHandoff: %v", cycle, err)
		}
		if int(cursor) != len(handoff) {
			t.Fatalf("cycle %d: restored cursor=%d != restored handoff length=%d", cycle, cursor, len(handoff))
		}
		since, err := next.state.citizens.DeathHandoffSince(int(cursor), cid)
		if err != nil {
			t.Fatalf("cycle %d DeathHandoffSince: %v", cycle, err)
		}
		if len(since) != 0 {
			t.Fatalf("cycle %d: %d records would be re-delivered after restore", cycle, len(since))
		}
		cons, err := next.DeathServices().Snapshot(cid)
		if err != nil {
			t.Fatalf("cycle %d Snapshot: %v", cycle, err)
		}
		if cons.Sum() != cons.BodiesReleased {
			t.Fatalf("cycle %d: conservation broken after restore: %+v", cycle, cons)
		}
		current = next
		currentAPI = nextAPI
	}
	_ = currentAPI
}

// TestAttackBUG689_DuplicateHandoffPageIsNonFatalAndDoesNotDoubleCount
// attacks the ErrDuplicateDeath path the hook deliberately swallows: an
// operator-style double delivery of the same page must not double-count
// BodiesReleased, must not advance the cursor twice for the same records
// in a way that loses later ones, and must not fail the hook.
func TestAttackBUG689_DuplicateHandoffPageIsNonFatalAndDoesNotDoubleCount(t *testing.T) {
	cid := errs.NewCorrelationID()
	ds, err := deathservices.LoadDefault(cid)
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	page := []citizens.RealisedDeath{
		{CitizenID: 1, DeathMonth: 3},
		{CitizenID: 2, DeathMonth: 3},
	}
	if _, err := ds.IntakeFromHandoff(page, cid); err != nil {
		t.Fatalf("first IntakeFromHandoff: %v", err)
	}
	applied, err := ds.IntakeFromHandoff(page, cid)
	if err == nil {
		t.Fatal("re-delivering the same page returned nil error, want ErrDuplicateDeath (the documented warning)")
	}
	if !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("re-delivery error is not ErrDuplicateDeath: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("re-delivery applied %d bodies, want 0", len(applied))
	}
	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cons.BodiesReleased != 2 {
		t.Fatalf("BodiesReleased=%d after a duplicate page, want 2 (no double count)", cons.BodiesReleased)
	}
	// FINDING-adjacent, deliberately asserted: the cursor DID advance twice
	// (by len(deaths), per IntakeFromHandoff's documented contract). That is
	// intentional and safe only because the caller always pages from the
	// module's OWN cursor; a caller that pages from anywhere else can skip
	// records. Pinning it so the contract cannot drift unnoticed.
	cursor, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	if cursor != 4 {
		t.Fatalf("HandoffCursor=%d after two 2-record pages, want 4 (advance-by-received-count contract)", cursor)
	}
}

// rewriteDeathServicesCursor rewrites the deathservices shard in the save
// bundle under root so its meta record carries the given handoffCursor,
// then repairs header.json's ByteSize/SHA256 so the bundle still validates.
// This models a CORRUPT or HAND-EDITED save (or a future format skew), not
// a save this codebase would ever write — the point is what the load path
// does with an impossible value it has no reason to trust.
func rewriteDeathServicesCursor(t *testing.T, root string, cursor int64) {
	t.Helper()
	summaries, _, err := save.List(root)
	if err != nil {
		t.Fatalf("save.List: %v", err)
	}
	dir := ""
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			dir = s.Path
			break
		}
	}
	if dir == "" {
		t.Fatalf("no composition save under %q", root)
	}
	hdr, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	idx := -1
	for i, sm := range hdr.ShardIndex {
		if sm.Kind == deathservices.KindDeathServices {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no deathservices shard in bundle")
	}
	path, err := serialize.ShardPath(dir, hdr.ShardIndex[idx])
	if err != nil {
		t.Fatalf("ShardPath: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	_ = zr.Close()
	re := regexp.MustCompile(`"handoffCursor":\s*-?\d+`)
	if !re.Match(plain) {
		t.Fatalf("no handoffCursor field found in the deathservices shard")
	}
	plain = re.ReplaceAll(plain, []byte(fmt.Sprintf(`"handoffCursor":%d`, cursor)))

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	out := buf.Bytes()
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("writing shard: %v", err)
	}
	sum := sha256.Sum256(out)
	hdr.ShardIndex[idx].ByteSize = int64(len(out))
	hdr.ShardIndex[idx].SHA256 = hex.EncodeToString(sum[:])
	if err := serialize.WriteHeader(dir, hdr); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
}

// TestAttackBUG689_HostileNegativeCursorRecoversAtDecode is the FIXED form
// of FINDING F6 (P3, decode-time sanitisation; PIN DELETED — the fix
// landed in the same round this file's attack was drafted against).
//
// The gap: applyLoadRecord (participant.go) used to install handoffCursor
// VERBATIM from the wire — no range check, no clamp, no reconciliation.
// compose's intakeDeathServices clamped a negative cursor to 0 for the
// READ, but the clamp was LOCAL to that call and never written back, so
// d.handoffCursor stayed negative and IntakeFromHandoff's
// advance-by-received-count arithmetic kept starting from the negative
// base — the module re-read the ENTIRE handoff stream every month
// forever, throwing every record away as a swallowed ErrDuplicateDeath.
//
// Fix: applyLoadRecord now clamps a decoded negative handoffCursor to 0 at
// the point of install (participant.go, ErrCorruptHandoffCursor, MET-G5452
// — data/errors.json), logging once as a diagnosability WARNING (GR#17),
// never fatal. This test drives the exact hostile-shard scenario the
// deleted pin proved broken and asserts the CORRECT outcome: the decoded
// cursor is 0 (never negative), and driving one month afterwards lands the
// cursor exactly on the handoff stream length — self-correcting in a
// single month rather than staying permanently behind.
func TestAttackBUG689_HostileNegativeCursorRecoversAtDecode(t *testing.T) {
	cid := errs.NewCorrelationID()
	root, _, _ := saveWithDeaths(t, 2)
	rewriteDeathServicesCursor(t, root, -5)

	api := buildGuaranteedDeathCitizensAPI(t, attackSeed)
	e := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := comp.Load(root); err != nil {
		t.Fatalf("Load of a bundle with a negative handoffCursor failed: %v — decode must not hard-fail here, it must clamp", err)
	}
	ds := comp.DeathServices()
	got, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	if got != 0 {
		t.Fatalf("F6 regression: negative wire cursor decoded to %d, want 0 (clamped at decode)", got)
	}

	// Drive a month: the clamped cursor must self-correct to exactly the
	// handoff stream length — no permanent lag, no re-read-forever.
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	after, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor after: %v", err)
	}
	handoff, err := comp.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if int(after) != len(handoff) {
		t.Fatalf("F6 regression: cursor is %d after one driven month, want exactly the handoff stream length %d (self-correction failed)", after, len(handoff))
	}
}

// TestAttackBUG689_MidMonthSaveRestoreBoundary attacks the save/restore
// boundary AWAY from a month boundary, which every other test in this
// estate takes. The monthly PhasePopulation hook fires on one specific tick
// of the month; a save taken on any OTHER tick must still round-trip the
// cursor consistently with the citizens handoff stream, and the very next
// tick after the restore must neither re-deliver nor drop.
//
// Offsets are swept across the month (including the tick immediately
// before and immediately after the monthly slot) so a boundary-off-by-one
// in the hook's month detection cannot hide behind a single lucky offset.
func TestAttackBUG689_MidMonthSaveRestoreBoundary(t *testing.T) {
	cid := errs.NewCorrelationID()
	perMonth := int64(core.DailyTicksPerMonth)
	for _, offset := range []int64{1, perMonth / 3, perMonth / 2, perMonth - 1} {
		t.Run(fmt.Sprintf("offset_%d", offset), func(t *testing.T) {
			apiA := buildGuaranteedDeathCitizensAPI(t, attackSeed)
			eA := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
			compA, err := Wire(eA, &Deps{Citizens: apiA})
			if err != nil {
				t.Fatalf("Wire A: %v", err)
			}
			// One full month, then a partial one — the save lands mid-month.
			advanceInChunks(t, eA, perMonth)
			advanceInChunks(t, eA, offset)

			cursorA, err := compA.DeathServices().HandoffCursor(cid)
			if err != nil {
				t.Fatalf("HandoffCursor A: %v", err)
			}
			handoffA, err := apiA.DeathHandoff(cid)
			if err != nil {
				t.Fatalf("DeathHandoff A: %v", err)
			}
			if cursorA == 0 {
				t.Fatal("fixture: no deaths intaken before the mid-month save")
			}

			root := t.TempDir()
			if err := compA.Save(root); err != nil {
				t.Fatalf("Save: %v", err)
			}

			apiB := buildGuaranteedDeathCitizensAPI(t, attackSeed)
			eB := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
			compB, err := Wire(eB, &Deps{Citizens: apiB})
			if err != nil {
				t.Fatalf("Wire B: %v", err)
			}
			if err := compB.Load(root); err != nil {
				t.Fatalf("Load: %v", err)
			}
			dsB := compB.DeathServices()
			cursorB, err := dsB.HandoffCursor(cid)
			if err != nil {
				t.Fatalf("HandoffCursor B: %v", err)
			}
			if cursorB != cursorA {
				t.Fatalf("mid-month cursor did not round-trip: A=%d B=%d", cursorA, cursorB)
			}
			handoffB, err := compB.state.citizens.DeathHandoff(cid)
			if err != nil {
				t.Fatalf("DeathHandoff B: %v", err)
			}
			if len(handoffB) != len(handoffA) {
				t.Fatalf("mid-month handoff stream did not round-trip: A=%d B=%d", len(handoffA), len(handoffB))
			}
			since, err := compB.state.citizens.DeathHandoffSince(int(cursorB), cid)
			if err != nil {
				t.Fatalf("DeathHandoffSince: %v", err)
			}
			if len(since) != 0 {
				t.Fatalf("mid-month restore would re-deliver %d records", len(since))
			}

			// Immediate single tick after the restore: the intake hook is
			// MONTHLY, so at this point the cursor may legitimately lag the
			// handoff stream by whatever was realised since the last monthly
			// firing. What must never happen is the cursor running AHEAD of
			// the stream (that would mean records were skipped) or any
			// already-applied record being handed back.
			advanceInChunks(t, eB, 1)
			cursorMid, err := dsB.HandoffCursor(cid)
			if err != nil {
				t.Fatalf("HandoffCursor mid: %v", err)
			}
			handoffMid, err := compB.state.citizens.DeathHandoff(cid)
			if err != nil {
				t.Fatalf("DeathHandoff mid: %v", err)
			}
			if int(cursorMid) > len(handoffMid) {
				t.Fatalf("cursor ran AHEAD of the handoff stream one tick after a mid-month restore: cursor=%d handoff=%d (records skipped)", cursorMid, len(handoffMid))
			}
			for _, rd := range handoffMid[:cursorMid] {
				if _, err := dsB.Body(rd.CitizenID, cid); err != nil {
					t.Fatalf("citizen %d is below the cursor (%d) but has no body record: %v — a paged death was dropped", rd.CitizenID, cursorMid, err)
				}
			}

			// Carry on to a whole-month boundary (total = 2 * perMonth from
			// this engine's tick 0, the same shape every passing test uses),
			// where the monthly hook has run strictly after the last
			// realisation and the identity must hold exactly.
			advanceInChunks(t, eB, 2*perMonth-1)
			cursorB2, err := dsB.HandoffCursor(cid)
			if err != nil {
				t.Fatalf("HandoffCursor B2: %v", err)
			}
			handoffB2, err := compB.state.citizens.DeathHandoff(cid)
			if err != nil {
				t.Fatalf("DeathHandoff B2: %v", err)
			}
			if int(cursorB2) != len(handoffB2) {
				t.Fatalf("exactly-once broken after a mid-month restore: cursor=%d handoff=%d", cursorB2, len(handoffB2))
			}
			cons, err := dsB.Snapshot(cid)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if cons.Sum() != cons.BodiesReleased || cons.BodiesReleased != int64(len(handoffB2)) {
				t.Fatalf("conservation broken after a mid-month restore: %+v vs handoff %d", cons, len(handoffB2))
			}
		})
	}
}
