package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-build-services-bridge-2026-09-02 round remedy (root fix) — the
// compose-level integration proofs for engine.services becoming its own
// save.Participant: the round's OWN named defect (a live-composition
// rewind leaves a phantom service instance) end-to-end through the real
// composed engine + the real data-driven build catalogue, plus the
// migration path for a save taken before this participant existed.

const servicesBridgeSeed = uint64(20260902)

// buildAndCompleteClinic drives the composed engine's REAL build catalogue
// (data/buildings.json's "clinic" entry, serviceKind "healthcare") to
// completion via the white-box BuildAPI (the protocol command layer's
// BuildPayload.BuildingType maps onto build's ZONE catalogue today, not the
// named-building catalogue -- see compose.go's KindBuild handler's own
// "baseline-one seam note" -- so exercising the services bridge requires
// the same white-box BuildAPI.SubmitBuildCommand call the build package's
// own attack tests use, now against the REAL Wire()'d instance). Returns
// the completed order's cell so a caller can demolish it if needed.
func buildAndCompleteClinic(t *testing.T, e *core.Engine, comp *Composition, cell protocol.CellRef) {
	t.Helper()
	cid := errs.NewCorrelationID()

	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("svc-buy"),
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
		Zone: build.ZoneDwelling, Month: month, BuildingID: "clinic",
	}); err != nil {
		t.Fatalf("SubmitBuildCommand(clinic): %v", err)
	}

	// Three months of daily ticks -- proven sufficient for a dwelling-zone
	// order's baseLeadTimeDays to complete elsewhere in this package
	// (save_roundtrip_test.go's driveMultiDomain uses the same window for
	// the same zone-level lead time).
	if err := e.AdvanceTicks(cid, 3*int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
}

// TestServicesBridge_RewindDropsPhantomInLiveComposition is the round's own
// named defect, reproduced end-to-end: a save taken BEFORE a service
// building completes, loaded back into the SAME live composition AFTER that
// building has since completed and registered with engine.services, must
// leave NO phantom service instance behind. Before this participant
// existed, engine.services was untouched by Load entirely (see
// services/participant.go's doc comment), so the clinic registered after
// the early save would have silently survived the rewind.
func TestServicesBridge_RewindDropsPhantomInLiveComposition(t *testing.T) {
	e, comp := buildComposition2(t, servicesBridgeSeed)

	// The "early" save: nothing built yet.
	earlyDir := t.TempDir()
	if err := comp.Save(earlyDir); err != nil {
		t.Fatalf("early Save: %v", err)
	}
	if cs, err := comp.state.services.CoverageSummary(); err != nil || cs.ServiceCount != 0 {
		t.Fatalf("test setup: expected zero services before build, got %+v err=%v", cs, err)
	}

	// Complete a clinic AFTER the early save -- this is the service that
	// must NOT survive a rewind to the early save.
	buildAndCompleteClinic(t, e, comp, protocol.CellRef{X: 0, Y: 0})
	if cs, err := comp.state.services.CoverageSummary(); err != nil || cs.ServiceCount != 1 {
		t.Fatalf("test setup: expected exactly one service after the clinic completes, got %+v err=%v", cs, err)
	}

	// Rewind: Load the EARLY save into the SAME live composition.
	if err := comp.Load(earlyDir); err != nil {
		t.Fatalf("rewind Load: %v", err)
	}

	cs, err := comp.state.services.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary after rewind: %v", err)
	}
	if cs.ServiceCount != 0 {
		t.Fatalf("PHANTOM INSTANCE: %d service(s) survived a rewind to a save taken before they were built -- CoverageSummary=%+v", cs.ServiceCount, cs)
	}
	if cs.TotalCapacity != 0 {
		t.Fatalf("PHANTOM CAPACITY: %v capacity survived a rewind -- CoverageSummary=%+v", cs.TotalCapacity, cs)
	}
}

// TestServicesBridge_OldSaveWithoutServicesShard_SweepRebuilds proves the
// documented migration path: a save bundle with no "services" shard at all
// (as every save taken before this participant existed would be) must still
// restore a completed service building's capacity, via the UNCHANGED
// BuildAPI.RegisterCompletedServices sweep compose.Load already calls
// unconditionally. This is the behaviour this round's fix must NOT regress.
func TestServicesBridge_OldSaveWithoutServicesShard_SweepRebuilds(t *testing.T) {
	e, comp := buildComposition2(t, servicesBridgeSeed+1)
	buildAndCompleteClinic(t, e, comp, protocol.CellRef{X: 0, Y: 0})
	if cs, err := comp.state.services.CoverageSummary(); err != nil || cs.ServiceCount != 1 {
		t.Fatalf("test setup: expected exactly one service after the clinic completes, got %+v err=%v", cs, err)
	}

	// Build an "OLD-FORMAT" bundle by hand: every participant EXCEPT
	// services (simulating a save taken before this participant existed).
	oldParticipants := make([]save.Participant, 0, len(comp.Participants())-1)
	for _, p := range comp.Participants() {
		if p.Kind() == "services" {
			continue
		}
		oldParticipants = append(oldParticipants, p)
	}
	clock, err := comp.state.e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	dir := t.TempDir()
	mgr := save.NewManager(dir, oldParticipants, comp.state.cid)
	ctx := save.Context{WorldSeed: int64(comp.state.seed), CreatedAtTick: clock.Tick(), GameMonth: clock.Month(), AppVersion: "old-format-test"}
	if err := mgr.SaveManual(ctx, "composition"); err != nil {
		t.Fatalf("old-format SaveManual: %v", err)
	}

	// Load this OLD-FORMAT bundle into a FRESH composition (the normal
	// Composition.Load, which registers services as a participant like
	// every other real caller) — engine.services' Handler never runs (no
	// "services" shard in the header), so the sweep is the sole rebuild
	// path.
	_, freshComp := buildComposition2(t, servicesBridgeSeed+1)
	if err := freshComp.Load(dir); err != nil {
		t.Fatalf("Load(old-format bundle): %v", err)
	}

	cs, err := freshComp.state.services.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary after old-format load: %v", err)
	}
	if cs.ServiceCount != 1 {
		t.Fatalf("migration path broken: old-format save (no services shard) did not have its clinic rebuilt by the sweep -- CoverageSummary=%+v", cs)
	}
}

// buildComposition2 is buildComposition (save_roundtrip_test.go) with an
// explicit seed parameter, so this file's two tests can each use their own
// deterministic seed without colliding with save_roundtrip_test.go's fixed
// roundTripSeed composition. It pre-provisions the build module's
// construction-materials draw generously (mirroring
// TestGameplay_DemolishCreditsCompensation, compose_test.go) so a submitted
// build order completes within a few months of ticks rather than sitting
// materials-pending forever against an empty default logistics stock.
func buildComposition2(t *testing.T, seed uint64) (*core.Engine, *Composition) {
	t.Helper()
	cid := errs.NewCorrelationID()
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}
