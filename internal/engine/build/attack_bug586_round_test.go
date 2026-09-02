package build

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// GR#23 INDEPENDENT DESTRUCTIVE ROUND — BUG-586 servicesSweepDirty flag.
// The attacker's tests, not the author's. They probe the two flag-set sites
// for INDEPENDENT regression pinning, and hunt for a third gap-creating path
// the field doc's "exhaustive enumeration" does not cover.
// ---------------------------------------------------------------------------

// newBuildFixtureNoServices is newBuildServicesFixtureIn minus the
// SetServices call, so an attack can control WHEN engine.services is wired
// relative to a load and a tick.
func newBuildFixtureNoServices(t *testing.T, w *world.WorldAPI) (*BuildAPI, *logistics.LogisticsAPI) {
	t.Helper()
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSONWithEntries(100, 5))
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := b.SetWorld(w); err != nil {
		t.Fatalf("SetWorld: %v", err)
	}
	s, err := season.LoadDefault(testCorr())
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}
	if err := b.SetSeason(s); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	l, err := logistics.LoadDefault(testCorr())
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if err := b.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return b, l
}

// --- ATTACK A: the SetServices flag site, in isolation ---------------------
//
// Restore a completed clinic into a BuildAPI that has NOT yet had
// engine.services wired, tick once (the sweep runs, finds services nil,
// skips, and clears the dirty flag), THEN wire services and tick again.
// Only the SetServices flag-set site can save this. Mutating that ONE line
// to `false` leaves every other test in the package (and in compose) green,
// so without this test half the fix is unpinned.
func TestAttackBUG586_LoadThenTickThenWireServices(t *testing.T) {
	svc1 := services.New(testCorr())
	orig, _ := newBuildServicesFixtureIn(t, svc1)
	tile, local := tile00(), local00()
	id, err := orig.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, orig, id, 50)
	root := saveInto(t, orig, "orig")

	// Restore into a BuildAPI with NO services wired yet.
	b2, _ := newBuildFixtureNoServices(t, newOwnedWorld(t))
	loadInto(t, root, b2, "reloaded")

	// A tick BEFORE the dependency is wired: the sweep runs and legitimately
	// finds nothing to do (services nil), clearing the dirty flag.
	if err := b2.Tick(0); err != nil {
		t.Fatalf("Tick before SetServices: %v", err)
	}

	// Now the composition root wires engine.services.
	svc2 := services.New(testCorr())
	if err := b2.SetServices(svc2); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := b2.Tick(1); err != nil {
		t.Fatalf("Tick after SetServices: %v", err)
	}

	cs, err := svc2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if cs.ServiceCount != 1 {
		t.Fatalf("DURABILITY DEFECT (SetServices flag site): a restored, standing clinic "+
			"never re-registered after engine.services was wired post-load: %+v", cs)
	}
}

// --- ATTACK B: a THIRD gap path the enumeration misses ---------------------
//
// The field doc asserts Tick's own completion block "is never a source of
// this gap: it calls registerServiceLocked synchronously ... BEFORE setting
// order.complete = true — so an order Tick itself completes always already
// has its serviceByOrder record ... by the time Tick's loop moves on."
//
// That is FALSE. Tick's completion block is:
//
//	registerServiceLocked(...)          // service now LIVE in engine.services
//	order.complete = true
//	b.structures[key] = order.id        // order is now STANDING
//	b.world.SetStructure(...)  -> err   // RETURNS, aborting the tick
//	b.serviceByOrder[order.id] = ...    // NEVER REACHED
//
// A SetStructure failure (world.ErrTileNotOwned / ErrTileOutOfBounds) leaves
// a complete, standing order whose service IS registered in engine.services
// but is absent from serviceByOrder — with servicesSweepDirty CLEAR. Before
// BUG-586 the unconditional per-tick sweep healed this on the very next
// tick. With the flag, it never heals: the service is permanently
// undemolishable ghost capacity.
func TestAttackBUG586_TickCompletionAbortLeavesUnhealableGap(t *testing.T) {
	svc := services.New(testCorr())
	owned := newOwnedWorld(t)
	b, _ := newBuildFixtureNoServices(t, owned)
	if err := b.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	tile, local := tile00(), local00()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}

	// The composition root re-wires the world (a fresh/rehydrated world that
	// does not (yet) own the tile) -- SetWorld is a public, supported call.
	// Nothing in Tick's per-order loop touches world until the completion
	// step, so every in-flight tick still succeeds.
	if err := b.SetWorld(world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})); err != nil {
		t.Fatalf("SetWorld swap: %v", err)
	}

	// Tick on. Registration succeeds, complete+structures land, SetStructure
	// fails, and the tick aborts BEFORE serviceByOrder is written.
	var ticks int64
	var aborted bool
	for ; ticks < 60; ticks++ {
		if err := b.Tick(ticks); err != nil {
			aborted = true
			break
		}
	}
	if !aborted {
		t.Fatalf("setup: expected the completing Tick to fail on SetStructure, none did")
	}

	// The service IS live in engine.services.
	cs, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if cs.ServiceCount != 1 {
		t.Skipf("setup did not produce the gap (ServiceCount=%d); attack inapplicable", cs.ServiceCount)
	}

	// Put a working world back and let the sim run on. The pre-BUG-586 code
	// re-swept every tick and healed the index here.
	if err := b.SetWorld(owned); err != nil {
		t.Fatalf("SetWorld restore: %v", err)
	}
	for i := int64(0); i < 5; i++ {
		if err := b.Tick(ticks + 1 + i); err != nil {
			t.Fatalf("recovery Tick: %v", err)
		}
	}

	b.mu.Lock()
	_, tracked := b.serviceByOrder[id]
	b.mu.Unlock()
	if !tracked {
		t.Fatalf("THIRD GAP PATH: order %d is complete and standing with a LIVE registered "+
			"service, but serviceByOrder has no record and servicesSweepDirty is clear — "+
			"five ticks did not heal it. The field doc's enumeration claims Tick can never "+
			"create this gap; the SetStructure error path does.", id)
	}

	// Demonstrate the user-visible consequence: demolition cannot deregister.
	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}
	cs, err = svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary after demolish: %v", err)
	}
	if cs.ServiceCount != 0 {
		t.Fatalf("GHOST SERVICE: demolished clinic still contributes capacity: %+v", cs)
	}
}
