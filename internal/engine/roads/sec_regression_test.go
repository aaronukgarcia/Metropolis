package roads

import (
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestAddNodeRejectsOutOfDomainCoordinate (SEC-222) asserts that a node
// whose tile is outside the 30x30 expansion extent, or whose local row/col
// is outside the 200x200 tile grid, is rejected with ErrInvalidInput BEFORE
// any footprint work — so a hostile coordinate can never make AddRoad's
// Bresenham stamp run unbounded (Row=500000 would build 500,001 cells; a
// wrapping Tile.X would feed garbage geometry to the obstruction check).
func TestAddNodeRejectsOutOfDomainCoordinate(t *testing.T) {
	a := newTestAPI(t)

	// Local.Row far outside the 200-cell tile domain.
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: 1, Pos: CellRef{
		Tile:  world.TileCoord{X: 0, Y: 0},
		Local: world.CellLocal{Row: 500000, Col: 0},
	}}); !errors.Is(err, &errs.E{Code: ErrInvalidInput}) {
		t.Fatalf("AddNode(Row=500000) = %v, want ErrInvalidInput", err)
	}

	// An int64-wrap-inducing tile coordinate (Tile.X far outside the extent).
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: 2, Pos: CellRef{
		Tile:  world.TileCoord{X: 46116860184273880, Y: 0},
		Local: world.CellLocal{Row: 0, Col: 0},
	}}); !errors.Is(err, &errs.E{Code: ErrInvalidInput}) {
		t.Fatalf("AddNode(wrapping Tile.X) = %v, want ErrInvalidInput", err)
	}

	// The rejected nodes were never registered, so AddRoad cannot reach the
	// footprint path from them — zero footprint work happened.
	if _, err := a.AddRoad(AddRoadCommand{CorrelationID: "test", ID: 1, Start: 1, End: 2, Class: ClassTwoLane}); !errors.Is(err, &errs.E{Code: ErrNodeNotFound}) {
		t.Fatalf("AddRoad(rejected nodes) = %v, want ErrNodeNotFound (nodes must not be registered)", err)
	}

	// Defense-in-depth at the geometry layer: even reached directly,
	// computeFootprint must not build an unbounded stamp from an out-of-domain
	// endpoint (this is unreachable through the API once AddNode validates).
	if fp := computeFootprint(
		CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 500000, Col: 0}},
		CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		1,
	); len(fp) != 0 {
		t.Fatalf("computeFootprint(out-of-domain endpoint) returned %d cells, want 0", len(fp))
	}
}

// TestUpgradeHostilePermilleNeverProducesNegativeCost (SEC-223) asserts that a
// schema-valid-but-hostile rungDistanceCostPermille (MaxInt64, which passes
// buildConfig's ">= 0" check) cannot make a motorway->alley downgrade yield a
// negative cost. The rung-distance numerator multiply is checked: it must be
// rejected loudly (ErrInvalidInput) rather than wrap int64 into a negative
// numerator and pay the player.
func TestUpgradeHostilePermilleNeverProducesNegativeCost(t *testing.T) {
	a := newTestAPI(t)
	if err := a.SetWorld(newTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassMotorway)

	// Inject the hostile value directly (in-package), simulating a config
	// that passed the load-time ">= 0" check. The finding's repro also zeroes
	// the rebuild-disruption term so the negative rung penalty is the whole
	// cost (otherwise the positive disruption term masks it).
	a.cfg.upgrade.RungDistanceCostPermille = math.MaxInt64
	a.cfg.upgrade.RebuildDisruptionPermille = 0

	quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassAlley})
	if err != nil {
		if !errors.Is(err, &errs.E{Code: ErrInvalidInput}) {
			t.Fatalf("hostile permille downgrade = %v, want ErrInvalidInput (overflow rejection)", err)
		}
		return
	}
	// If a cost was produced, it must be finite and non-negative — never a
	// negative number that pays the player.
	if quote.CostMicropounds < 0 {
		t.Fatalf("hostile permille downgrade cost = %d micropounds (negative — the game pays the player)", quote.CostMicropounds)
	}
}

// TestUpgradeQuotePhasesDoesNotAliasInternalSchedule (SEC-225) asserts the
// returned UpgradeQuote.Phases is a deep copy, not the slice stored in
// roadState.roadworks — a caller mutating the returned slice must not corrupt
// the internal schedule and bypass the command surface (GR#20).
func TestUpgradeQuotePhasesDoesNotAliasInternalSchedule(t *testing.T) {
	a := newTestAPI(t)
	if err := a.SetWorld(newTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassGravel)

	quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassResidentialStreet})
	if err != nil {
		t.Fatalf("ApplyUpgrade: %v", err)
	}
	if len(quote.Phases) == 0 {
		t.Fatal("no roadworks phases returned")
	}
	internal := a.roads[r.ID].roadworks
	if len(internal) != 1 {
		t.Fatalf("internal roadworks len = %d, want 1", len(internal))
	}
	before := internal[0]

	// Mutate the returned slice every way a hostile caller could: rewrite a
	// field, append a phase, and null the tail.
	quote.Phases[0].OpenLanes = before.OpenLanes + 100
	quote.Phases[0].DurationMonths = 999999
	quote.Phases = append(quote.Phases, RoadworksPhase{StartMonth: 1, DurationMonths: 1, OpenLanes: 0})

	after := a.roads[r.ID].roadworks
	if len(after) != 1 {
		t.Fatalf("internal roadworks len after mutation = %d, want 1 (returned slice aliased internal)", len(after))
	}
	if after[0] != before {
		t.Fatalf("internal phase mutated by caller: before=%+v after=%+v (returned slice aliased internal)", before, after[0])
	}
}

// TestRoadworksEndMonthOverflowRejected (SEC-226) asserts a phase whose end
// month (StartMonth + DurationMonths) overflows int64 is rejected up front
// rather than stored with a wrapped negative end that silently never
// activates.
func TestRoadworksEndMonthOverflowRejected(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	err := a.ScheduleRoadworks(ScheduleRoadworksCommand{
		CorrelationID: "test", RoadID: r.ID,
		Phases: []RoadworksPhase{{StartMonth: math.MaxInt64, DurationMonths: 5, OpenLanes: 1}},
	})
	if !errors.Is(err, &errs.E{Code: ErrInvalidRoadworks}) {
		t.Fatalf("StartMonth=MaxInt64 schedule = %v, want ErrInvalidRoadworks", err)
	}

	// A phase starting just below MaxInt64 whose end wraps must also reject.
	err = a.ScheduleRoadworks(ScheduleRoadworksCommand{
		CorrelationID: "test", RoadID: r.ID,
		Phases: []RoadworksPhase{{StartMonth: math.MaxInt64 - 1, DurationMonths: 2, OpenLanes: 1}},
	})
	if !errors.Is(err, &errs.E{Code: ErrInvalidRoadworks}) {
		t.Fatalf("StartMonth=MaxInt64-1 duration=2 schedule = %v, want ErrInvalidRoadworks", err)
	}
}

// TestRoadworksReadPathSaturatesEndMonth (SEC-226) is the defense-in-depth
// half: even if a near-max phase reaches the read path directly, its end must
// saturate (not wrap), so it activates from its start month onward instead of
// silently never applying.
func TestRoadworksReadPathSaturatesEndMonth(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane) // 2 lanes steady-state

	a.roads[r.ID].roadworks = []RoadworksPhase{{StartMonth: math.MaxInt64 - 1, DurationMonths: 2, OpenLanes: 1}}
	got, err := a.CurrentLaneCount(r.ID, math.MaxInt64-1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("CurrentLaneCount at start month = %d, want 1 (saturated, not wrapped)", got)
	}
}

// TestReaddRenamedRoadPreservesRename (SEC-227) asserts that re-registering a
// renamed road via AddRoad keeps the player's rename, so RoadInfo and NameRoad
// never diverge (GR#3). The auto-name must not be re-derived over the rename.
func TestReaddRenamedRoadPreservesRename(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	if err := a.Rename(RenameCommand{CorrelationID: "test", Kind: KindRoad, Seed: 42, ID: uint64(r.ID), NewName: "My Favourite Road"}); err != nil {
		t.Fatal(err)
	}

	again, err := a.AddRoad(AddRoadCommand{CorrelationID: "test", ID: r.ID, Start: r.Start, End: r.End, Class: ClassTwoLane})
	if err != nil {
		t.Fatal(err)
	}
	named, err := a.NameRoad(42, uint64(r.ID), ClassTwoLane)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "My Favourite Road" {
		t.Fatalf("RoadInfo.Name after re-add = %q, want %q", again.Name, "My Favourite Road")
	}
	if named != "My Favourite Road" {
		t.Fatalf("NameRoad after re-add = %q, want %q", named, "My Favourite Road")
	}
	if again.Name != named {
		t.Fatalf("RoadInfo.Name=%q vs NameRoad=%q diverge after re-add (GR#3)", again.Name, named)
	}
	info, err := a.RoadInfo(r.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "My Favourite Road" {
		t.Fatalf("RoadInfo.Name = %q, want %q", info.Name, "My Favourite Road")
	}
	if !info.Renamed {
		t.Errorf("renamed flag not set on the re-added road view")
	}
}

// TestHostileCostPoundsRejected (SEC-230) asserts that a schema-valid-but-
// hostile baseCostPounds / landCostPerCellPounds above the Micropounds scale
// bound (MaxInt64/1e6 ≈ 9.2e12) is rejected at load time, rather than passing
// the ">= 0" check and later wrapping negative inside det.FromPounds's ×1e6
// multiply — which would price a same-width upgrade at a negative micropound
// amount and pay the player.
func TestHostileCostPoundsRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*rawRoadsData)
	}{
		{
			name: "baseCostPounds-overflow",
			mutate: func(r *rawRoadsData) {
				r.Classes[0].BaseCostPounds = math.MaxInt64
			},
		},
		{
			name: "baseCostPounds-just-over-bound",
			mutate: func(r *rawRoadsData) {
				r.Classes[0].BaseCostPounds = maxPoundsPerMoney + 1
			},
		},
		{
			name: "landCostPerCellPounds-overflow",
			mutate: func(r *rawRoadsData) {
				r.Upgrade.LandCostPerCellPounds = math.MaxInt64
			},
		},
		{
			name: "landCostPerCellPounds-just-over-bound",
			mutate: func(r *rawRoadsData) {
				r.Upgrade.LandCostPerCellPounds = maxPoundsPerMoney + 1
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := validRawRoads()
			tc.mutate(&raw)
			if _, err := buildConfig(raw, "test", "test"); !errors.Is(err, &errs.E{Code: ErrRoadsDataInvalid}) {
				t.Fatalf("hostile cost pounds = %v, want ErrRoadsDataInvalid", err)
			}
		})
	}

	// The bound itself must still be accepted (exact, not over-restrictive):
	// a cost at maxPoundsPerMoney fits in Micropounds exactly after ×1e6.
	raw := validRawRoads()
	raw.Classes[0].BaseCostPounds = maxPoundsPerMoney
	raw.Upgrade.LandCostPerCellPounds = maxPoundsPerMoney
	if _, err := buildConfig(raw, "test", "test"); err != nil {
		t.Fatalf("cost at the Micropounds bound rejected: %v", err)
	}
}

// TestHostileWidthCellsRejected (SEC-231) asserts a schema-valid-but-hostile
// widthCells — which passes the old "> 0" check but would make computeFootprint
// stamp O(widthCells²) cells per Bresenham step — is rejected at load time with
// ErrRoadsDataInvalid, before it can reach AddRoad/widening and exhaust memory.
// The cap is maxWidthCells (the realistic road-width bound, see config.go).
func TestHostileWidthCellsRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		{name: "hostile-1e6", width: 1000000},
		{name: "just-over-cap", width: maxWidthCells + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := validRawRoads()
			raw.Classes[0].WidthCells = tc.width
			if _, err := buildConfig(raw, "test", "test"); !errors.Is(err, &errs.E{Code: ErrRoadsDataInvalid}) {
				t.Fatalf("widthCells=%d = %v, want ErrRoadsDataInvalid", tc.width, err)
			}
		})
	}

	// The cap itself must still be accepted (exact, not over-restrictive).
	raw := validRawRoads()
	raw.Classes[0].WidthCells = maxWidthCells
	if _, err := buildConfig(raw, "test", "test"); err != nil {
		t.Fatalf("widthCells at the cap rejected: %v", err)
	}
}

// TestRenameMismatchedSeedDoesNotOverwriteRoadRecord (SEC-232) asserts a
// rename carrying a DIFFERENT seed than the API's own (a.seed) records its
// registry entry under that seed but does NOT overwrite the in-graph road
// record — so RoadInfo.Name and NameRoad(seed) cannot diverge (GR#3).
func TestRenameMismatchedSeedDoesNotOverwriteRoadRecord(t *testing.T) {
	a := newTestAPI(t) // seed 42
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	original := r.Name

	if err := a.Rename(RenameCommand{CorrelationID: "test", Kind: KindRoad, Seed: 999, ID: uint64(r.ID), NewName: "Wrong-Seed Rename"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	info, err := a.RoadInfo(r.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != original {
		t.Fatalf("RoadInfo.Name = %q after mismatched-seed rename, want unchanged %q", info.Name, original)
	}
	if info.Renamed {
		t.Errorf("Renamed flag set after a mismatched-seed rename")
	}

	named, err := a.NameRoad(42, uint64(r.ID), ClassTwoLane)
	if err != nil {
		t.Fatal(err)
	}
	if named != original {
		t.Fatalf("NameRoad(42) = %q, want %q", named, original)
	}
	if info.Name != named {
		t.Fatalf("RoadInfo.Name=%q vs NameRoad(42)=%q diverge after mismatched-seed rename (GR#3)", info.Name, named)
	}

	// The rename itself is still recorded under the seed it was given.
	got, err := a.NameRoad(999, uint64(r.ID), ClassTwoLane)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Wrong-Seed Rename" {
		t.Fatalf("NameRoad(999) = %q, want %q", got, "Wrong-Seed Rename")
	}
}

// TestWidthCapBoundsFootprintCPU (SEC-235) asserts the widthCells cap is a
// realistic road-width bound, not one whole world tile. The r4 attack
// exploited the old cap (world.TileSizeCells = 200): a schema-valid width=200
// class plus a full-extent diagonal road computed a ~2.4M-cell footprint in
// ~26s (~240M stamp ops) in one AddRoad — OOM was bounded, CPU was not. A
// class declaring the old cap's width must now be rejected at load time.
func TestWidthCapBoundsFootprintCPU(t *testing.T) {
	if maxWidthCells >= world.TileSizeCells {
		t.Fatalf("maxWidthCells = %d, must be strictly below world.TileSizeCells = %d (a whole-tile-wide stamp is a CPU DoS)", maxWidthCells, world.TileSizeCells)
	}

	// The old cap value must now be rejected at load time, before it can reach
	// computeFootprint.
	raw := validRawRoads()
	raw.Classes[0].WidthCells = world.TileSizeCells
	if _, err := buildConfig(raw, "test", "test"); !errors.Is(err, &errs.E{Code: ErrRoadsDataInvalid}) {
		t.Fatalf("widthCells=%d (the old cap) = %v, want ErrRoadsDataInvalid", world.TileSizeCells, err)
	}

	// The realistic cap itself is still accepted (exact, not over-restrictive).
	raw = validRawRoads()
	raw.Classes[0].WidthCells = maxWidthCells
	if _, err := buildConfig(raw, "test", "test"); err != nil {
		t.Fatalf("widthCells at the realistic cap rejected: %v", err)
	}
}

// TestAddNodeRejectsMovingReferencedNode (SEC-236) asserts re-registering a
// node that a road already references at a DIFFERENT position is rejected with
// ErrNodeReferenced, so the road's stored footprint cannot desync from its
// endpoints (the r4 probe: moving one endpoint left 11 stored vs 951
// recomputed cells, corrupting widening land-cost and obstruction checks).
// Re-registering at the SAME position stays idempotent.
func TestAddNodeRejectsMovingReferencedNode(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	startID := r.Start
	start := a.nodes[startID]

	// Same-position re-add is idempotent, not an error.
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: startID, Pos: start.Pos}); err != nil {
		t.Fatalf("re-add at the same position: %v", err)
	}

	// Moving the referenced node must be rejected with ErrNodeReferenced.
	moved := start.Pos
	moved.Local.Row += 5
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: startID, Pos: moved}); !errors.Is(err, &errs.E{Code: ErrNodeReferenced}) {
		t.Fatalf("move referenced node = %v, want ErrNodeReferenced", err)
	}

	// The node's position is unchanged and the stored footprint still matches
	// a fresh recompute from the endpoints (no desync — GR#3).
	if got := a.nodes[startID].Pos; got != start.Pos {
		t.Fatalf("node position changed to %v after rejected move, want %v", got, start.Pos)
	}
	rs := a.roads[r.ID]
	recomputed := computeFootprint(a.nodes[rs.start].Pos, a.nodes[rs.end].Pos, a.cfg.classes[rs.class].WidthCells)
	if !footprintsEqual(rs.footprint, recomputed) {
		t.Fatalf("stored footprint desynced from endpoints: %d stored vs %d recomputed", len(rs.footprint), len(recomputed))
	}

	// A node NOT referenced by any road can still be moved (the guard is
	// scoped to referenced nodes only).
	free := NodeID(999)
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: free, Pos: CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 10, Col: 10}}}); err != nil {
		t.Fatalf("AddNode(free): %v", err)
	}
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: free, Pos: CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 20, Col: 20}}}); err != nil {
		t.Fatalf("move unreferenced node: %v", err)
	}
}

// TestUpgradeCommitKeepsNameConsistent (SEC-237) asserts that after a
// class-changing upgrade commits, the road's name — and NameRoad for the same
// (seed, id) at the road's CURRENT class — still agree (GR#3). The r4 probe:
// RoadInfo.Name stayed the creation-class auto-name (e.g. "Pent Road") while
// NameRoad(seed, id, currentClass) re-derived from the current class (e.g.
// "M1608"), giving the same (seed, id) object two names on its own read surface.
func TestUpgradeCommitKeepsNameConsistent(t *testing.T) {
	a := newTestAPI(t) // seed 42
	if err := a.SetWorld(newTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	creation := r.Name

	// Upgrade to a numbered class (a different naming scheme: M-numbering).
	quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassMotorway})
	if err != nil {
		t.Fatalf("ApplyUpgrade: %v", err)
	}
	endMonth := quote.Phases[0].StartMonth + quote.Phases[0].DurationMonths
	if err := a.Advance(endMonth); err != nil {
		t.Fatal(err)
	}

	info, err := a.RoadInfo(r.ID, endMonth)
	if err != nil {
		t.Fatal(err)
	}
	if info.Class != ClassMotorway {
		t.Fatalf("class after commit = %s, want motorway", info.Class.String())
	}
	// The road's name is its creation name, stable across the class change.
	if info.Name != creation {
		t.Fatalf("RoadInfo.Name = %q after upgrade, want stable creation name %q", info.Name, creation)
	}

	// NameRoad at the road's CURRENT class must agree with RoadInfo.Name.
	named, err := a.NameRoad(42, uint64(r.ID), ClassMotorway)
	if err != nil {
		t.Fatal(err)
	}
	if named != info.Name {
		t.Fatalf("NameRoad(seed,id,motorway) = %q vs RoadInfo.Name = %q — same (seed,id) object has two names (GR#3)", named, info.Name)
	}
	if named != creation {
		t.Fatalf("NameRoad = %q, want stable creation name %q", named, creation)
	}
}

// footprintsEqual reports whether two sorted footprints hold the identical
// cell sequence.
func footprintsEqual(a, b []CellRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validRawRoads builds a rawRoadsData that passes buildConfig, with all
// eleven §51 rungs in canonical order and in-domain attributes, so a single
// field can be mutated to reproduce a hostile-input scenario.
func validRawRoads() rawRoadsData {
	classes := make([]rawClass, len(classSlugs))
	for i, slug := range classSlugs {
		classes[i] = rawClass{
			ID:             slug,
			Name:           "Rung " + itoa(i),
			Lanes:          1,
			SpeedLimit:     50,
			SpeedMin:       30,
			SpeedMax:       70,
			Parking:        true,
			TreeVerge:      false,
			WidthCells:     1,
			BaseCostPounds: 1000,
		}
	}
	return rawRoadsData{
		Version: 1,
		Classes: classes,
		Maintenance: rawMaintenance{
			ConditionDecayPerMonth:          0.02,
			SpeedPenaltyPerConditionBelow:   0.4,
			CostMultiplierPerConditionBelow: 1.5,
			RepairConditionPerPound:         0.001,
		},
		Upgrade: rawUpgrade{
			RungDistanceCostPermille:  150,
			RebuildDisruptionPermille: 250,
			LandCostPerCellPounds:     1000,
		},
		Roadworks: rawRoadworks{
			PhaseDurationMonths:   2,
			LaneReductionFraction: 0.5,
		},
		Naming: rawNaming{
			CivicTypes:          []string{"School", "Library"},
			InfrastructureTypes: []string{"Substation", "Depot"},
			TransitColours:      []string{"Red", "Blue"},
		},
	}
}
