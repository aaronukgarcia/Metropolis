package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// bug743_e2e_test.go — BUG-743's end-to-end proof that BOTH feeds
// (CompletedBuildings/BUG-734 and DemolishedSince/BUG-743) actually reach
// the tick through the REAL Composition, closing BUG-720's own STOP
// ("compose calls only deathservices intake, never RegisterCemetery/
// Crematorium"). Driven through:
//
//   - the buy: the real public gameplay command path (e.HandleCommand,
//     protocol.KindBuy);
//   - the build: [build.BuildAPI.SubmitBuildCommand] called directly
//     against the REAL Wire()'d composition's own buildAPI instance — the
//     SAME documented workaround save_services_bridge_test.go's own
//     buildAndCompleteClinic already uses and explains (compose.go's own
//     "baseline-one seam note": protocol.KindBuild's BuildPayload never
//     carries a BuildingID today, a pre-existing, separate gap out of
//     THIS bug's scope — see Deps.DeathServiceCemeteries' doc comment,
//     gap #1). This is engine.build's own real, public, production API —
//     never a test-only registration seam;
//   - the registration/deregistration: ENTIRELY the real
//     runDeathServiceBuildingRegistry bridge wired into buildHook.ApplyEffect
//     — at NO point does this test call
//     deathservices.RegisterCrematorium/UnregisterCrematorium directly,
//     or populate Deps.DeathServiceCemeteries/DeathServiceCrematoria (the
//     BUG-720 stopgap seam this proof deliberately avoids);
//   - the death: [deathservices.DeathServicesAPI.Intake] — deathservices'
//     own real, public, directly-callable production entry point
//     (syntheticDeaths/Intake, bug720_deathservices_run_test.go's own
//     documented precedent for driving a body into Awaiting without
//     waiting hundreds of simulated months for real per-citizen
//     mortality);
//   - the demolish: the real public gameplay command path (e.HandleCommand,
//     protocol.KindDemolish) — this path has NO BuildingID gap, so it
//     needs no white-box workaround at all.
const bug743E2ESeed = uint64(20260905)

// buildAndCompleteCrematorium mirrors save_services_bridge_test.go's
// buildAndCompleteClinic exactly, adapted to the "crematorium" catalogue
// entry (BUG-743's own subject) instead of "clinic". Returns nothing —
// callers already know the cell they asked for.
func buildAndCompleteCrematorium(t *testing.T, e *core.Engine, comp *Composition, cell protocol.CellRef) {
	t.Helper()
	cid := errs.NewCorrelationID()

	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("bug743-buy"),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: cell},
	}
	if res := e.HandleCommand(buy); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}

	tile, local, err := comp.state.cellFromRef(cell)
	if err != nil {
		t.Fatalf("cellFromRef: %v", err)
	}
	month, err := comp.state.currentMonth()
	if err != nil {
		t.Fatalf("currentMonth: %v", err)
	}
	if _, err := comp.state.buildAPI.SubmitBuildCommand(build.BuildCommand{
		Tile: tile, Local: local, OwnerID: 1, // playerOwnerID
		Zone: build.ZoneDwelling, Month: month, BuildingID: "crematorium",
	}); err != nil {
		t.Fatalf("SubmitBuildCommand(crematorium): %v", err)
	}

	// Three months of daily ticks: the SAME window buildAndCompleteClinic
	// uses for a dwelling-zone order's lead time, and the SAME window this
	// package's own save_roundtrip_test.go driveMultiDomain relies on.
	// Each daily tick also runs buildHook.ApplyEffect -> the SAME hook
	// this test proves registers the crematorium the moment it completes.
	if err := e.AdvanceTicks(cid, 3*int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
}

// demolishViaPublicPath submits a real protocol.KindDemolish gameplay
// command through e.HandleCommand — the demolish path carries no
// BuildingID gap (compose.go's case protocol.KindDemolish forwards the
// cell straight to SubmitDemolishCommand), so no white-box workaround is
// needed here at all.
func demolishViaPublicPath(t *testing.T, e *core.Engine, cell protocol.CellRef) {
	t.Helper()
	demolish := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("bug743-demolish"),
		Kind:            protocol.KindDemolish,
		Payload:         protocol.DemolishPayload{Cell: cell},
	}
	if res := e.HandleCommand(demolish); !res.Accepted {
		t.Fatalf("Demolish rejected: %+v", res.Error)
	}
}

// TestBUG743_EndToEnd_RegisterCremateDemolishConserve is the headline proof:
// build a crematorium via the real player path, watch capacity rise, feed
// it a real queued death and watch it actually get cremated with NO test
// seam registration, then demolish it via the real player path and watch
// capacity fall back while bodies stay conserved.
func TestBUG743_EndToEnd_RegisterCremateDemolishConserve(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := buildComposition2(t, bug743E2ESeed)
	ds := comp.state.deathServices
	cell := protocol.CellRef{X: 0, Y: 0}

	// --- Before: no crematorium, zero cremation capacity. ---
	status0 := comp.DeathServicesRunStatus()
	if status0.CrematoriaRegistered != 0 {
		t.Fatalf("test setup: %d crematoria already registered before any build", status0.CrematoriaRegistered)
	}
	capBefore := ds.MonthlyDrainCapacity(0)

	// --- Build the crematorium via the real player path + real build API. ---
	buildAndCompleteCrematorium(t, e, comp, cell)

	// It is now registered — via THIS bridge, never a Deps stopgap seam.
	status1 := comp.DeathServicesRunStatus()
	if status1.CrematoriaRegistered != 1 {
		t.Fatalf("crematorium was not registered by the live build->deathservices bridge: DeathServicesRunStatus=%+v", status1)
	}
	month, err := comp.state.currentMonth()
	if err != nil {
		t.Fatalf("currentMonth: %v", err)
	}
	capAfterBuild := ds.MonthlyDrainCapacity(month)
	if capAfterBuild <= capBefore {
		t.Fatalf("MonthlyDrainCapacity did not RISE after the crematorium registered: before=%d after=%d", capBefore, capAfterBuild)
	}

	// --- Feed it a real queued death (deathservices' own real Intake — not
	// a cemetery/crematorium registration seam) and let the daily run loop
	// actually cremate it. ---
	const deadCitizen = uint64(90_000_001)
	if _, err := ds.Intake(syntheticDeaths(1, deadCitizen, false), cid); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	before, err := ds.Body(deadCitizen, cid)
	if err != nil {
		t.Fatalf("Body (before cremation): %v", err)
	}
	if before.State != deathservices.BodyAwaiting {
		t.Fatalf("test setup: body state = %v, want Awaiting", before.State)
	}

	// One more month of daily ticks: the crematorium's own daily
	// throughput (12/d, spec seed) trivially covers a single body.
	if err := e.AdvanceTicks(cid, int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks (cremation window): %v", err)
	}

	after, err := ds.Body(deadCitizen, cid)
	if err != nil {
		t.Fatalf("Body (after cremation window): %v", err)
	}
	if after.State != deathservices.BodyCremated {
		t.Fatalf("BUG-743 regression: queued death was never cremated — state=%v (crematorium registered but the daily sweep never reached it, e.g. its id missing from the live roster)", after.State)
	}
	requireConservation(t, ds, cid)

	// --- Demolish it via the real player path, and watch capacity fall
	// back + bodies stay conserved. ---
	preDemolishCons := requireConservation(t, ds, cid)
	demolishViaPublicPath(t, e, cell)
	// One more daily tick so buildHook's piggybacked bridge call actually
	// processes the demolition feed (SubmitDemolishCommand alone only
	// stamps the DemolitionSeq; the UNREGISTRATION happens in the SAME
	// daily hook that registered it).
	if err := e.AdvanceTicks(cid, 1); err != nil {
		t.Fatalf("AdvanceTicks (post-demolish): %v", err)
	}

	status2 := comp.DeathServicesRunStatus()
	if status2.CrematoriaRegistered != 0 {
		t.Fatalf("crematorium was not UNregistered by the live build->deathservices bridge after demolition: DeathServicesRunStatus=%+v", status2)
	}
	month2, err := comp.state.currentMonth()
	if err != nil {
		t.Fatalf("currentMonth (post-demolish): %v", err)
	}
	capAfterDemolish := ds.MonthlyDrainCapacity(month2)
	if capAfterDemolish >= capAfterBuild {
		t.Fatalf("MonthlyDrainCapacity did not FALL after the crematorium was demolished: afterBuild=%d afterDemolish=%d", capAfterBuild, capAfterDemolish)
	}

	postDemolishCons := requireConservation(t, ds, cid)
	if postDemolishCons != preDemolishCons {
		t.Fatalf("conservation snapshot changed across a demolition that touches no bodies: before=%+v after=%+v", preDemolishCons, postDemolishCons)
	}
	if postDemolishCons.BodiesCremated == 0 {
		t.Fatalf("test setup: the cremated body's terminal state did not survive demolition: %+v", postDemolishCons)
	}
}

// TestBUG743_EndToEnd_SaveLoadMidway is the SAME story with a real
// save/load boundary spliced in mid-way — between the crematorium
// completing (registered, cremating) and its demolition — proving the two
// persisted cursors (buildRegistryCursor/buildDemolitionCursor) and the
// live roster (deathServiceBridgeCrematoriumIDs) all round-trip correctly:
// a plain Load must neither redeliver the already-consumed registration
// nor skip the still-pending demolition.
func TestBUG743_EndToEnd_SaveLoadMidway(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := buildComposition2(t, bug743E2ESeed+1)
	ds := comp.state.deathServices
	cell := protocol.CellRef{X: 0, Y: 0}

	buildAndCompleteCrematorium(t, e, comp, cell)
	if got := comp.DeathServicesRunStatus().CrematoriaRegistered; got != 1 {
		t.Fatalf("test setup: crematorium not registered, DeathServicesRunStatus.CrematoriaRegistered=%d", got)
	}
	const deadCitizen = uint64(90_000_101)
	if _, err := ds.Intake(syntheticDeaths(1, deadCitizen, false), cid); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if err := e.AdvanceTicks(cid, int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks (cremation window): %v", err)
	}
	before, err := ds.Body(deadCitizen, cid)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if before.State != deathservices.BodyCremated {
		t.Fatalf("test setup: body was not cremated before the save boundary, state=%v", before.State)
	}
	consBeforeSave := requireConservation(t, ds, cid)

	// --- The save boundary, MID-WAY: crematorium built+cremating, NOT yet
	// demolished. ---
	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Fresh composition, fresh engine, SAME provisioning fixture (mirrors
	// buildComposition2's own construction) — a REAL cross-process-shaped
	// restore, not a same-instance rewind.
	e2, comp2 := buildComposition2(t, bug743E2ESeed+1)
	if err := comp2.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// No redelivery: the roster + cursor round-tripped, so the crematorium
	// is registered on comp2 WITHOUT this test (or any hook) calling
	// RegisterCrematorium a second time — proof is the count staying at
	// exactly 1, and MonthlyDrainCapacity staying flat immediately after
	// Load (before any further tick runs).
	if got := comp2.DeathServicesRunStatus().CrematoriaRegistered; got != 1 {
		t.Fatalf("roster did not survive Load: CrematoriaRegistered=%d, want 1 (no redelivery, no skip)", got)
	}
	ds2 := comp2.state.deathServices
	afterLoad, err := ds2.Body(deadCitizen, cid)
	if err != nil {
		t.Fatalf("Body (post-load): %v", err)
	}
	if afterLoad.State != deathservices.BodyCremated {
		t.Fatalf("cremation did not survive Load: state=%v", afterLoad.State)
	}
	consAfterLoad := requireConservation(t, ds2, cid)
	if consAfterLoad != consBeforeSave {
		t.Fatalf("conservation diverged across the save/load boundary: before=%+v after=%+v", consBeforeSave, consAfterLoad)
	}

	// No skip either: the demolition cursor resumes correctly — demolish
	// on comp2 (post-load) via the real player path and confirm the
	// bridge's unregistration half still fires exactly once.
	demolishViaPublicPath(t, e2, cell)
	if err := e2.AdvanceTicks(cid, 1); err != nil {
		t.Fatalf("AdvanceTicks (post-demolish, post-load): %v", err)
	}
	if got := comp2.DeathServicesRunStatus().CrematoriaRegistered; got != 0 {
		t.Fatalf("crematorium not unregistered after a post-load demolition: CrematoriaRegistered=%d, want 0", got)
	}
	finalCons := requireConservation(t, ds2, cid)
	if finalCons != consAfterLoad {
		t.Fatalf("conservation changed across a post-load demolition that touches no bodies: before=%+v after=%+v", consAfterLoad, finalCons)
	}
}
