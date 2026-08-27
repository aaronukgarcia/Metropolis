package roads

import (
	"fmt"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the in-place upgrade surface (§51/AC-4/AC-5/AC-7): the
// compatibility rule, the fixed-point cost model (delta + rebuild disruption
// + rung-distance scaling + widening land cost), and the AC-5 footprint/
// widening occupancy check against engine.world. Money is det.Micropounds
// throughout — float64 never touches a cost computation (GR#16).

// ApplyUpgradeCommand converts a road in-place to a target class.
type ApplyUpgradeCommand struct {
	CorrelationID string
	RoadID        RoadID
	TargetClass   RoadClass
}

// ApplyUpgrade approves an in-place upgrade to TargetClass (§51/AC-4).
//
// Compatibility rule (PROVISIONAL, escalated to Aaron — see doc.go): every
// distinct class is a compatible target ("any-to-any"), and the rung
// distance is priced rather than gated (rung-distance cost scaling). The
// alternative Aaron may rule (step-through-adjacent-rungs) is a one-line
// predicate change here. A same-class request is an idempotent no-op (zero
// cost, no error); an invalid class is ErrInvalidClass.
//
// The upgrade does NOT swap the class instantly — it schedules roadworks
// (a default single-phase lane closure starting at the current simulation
// month, AC-6) and returns the quote; the class commits when the phase
// ends (see [RoadsAPI.Advance]). A widening whose target footprint would
// overlap an occupied (zoned or structured) cell the road does not already
// own is rejected with ErrFootprintObstructed until that cell is cleared
// (AC-5) — the purchase/demolition itself is engine.build's operation, out
// of this package's scope.
func (a *RoadsAPI) ApplyUpgrade(cmd ApplyUpgradeCommand) (UpgradeQuote, error) {
	if err := a.checkNotCopied("ApplyUpgrade"); err != nil {
		return UpgradeQuote{}, err
	}
	if !cmd.TargetClass.valid() {
		return UpgradeQuote{}, roadsErr(a.correlationID, ErrInvalidClass, map[string]any{"class": uint8(cmd.TargetClass)})
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	rs, ok := a.roads[cmd.RoadID]
	if !ok {
		return UpgradeQuote{}, roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(cmd.RoadID)})
	}

	// Same class: idempotent no-op (there is nothing to convert).
	if cmd.TargetClass == rs.class {
		return UpgradeQuote{}, nil
	}

	// AC-5: widening must not overlap an occupied cell the road does not own.
	obstructed, err := a.obstructedCellsLocked(rs, cmd.TargetClass)
	if err != nil {
		return UpgradeQuote{}, err
	}
	if len(obstructed) > 0 {
		first := obstructed[0]
		return UpgradeQuote{}, roadsErr(a.correlationID, ErrFootprintObstructed, map[string]any{
			"road":   uint64(rs.id),
			"target": cmd.TargetClass.String(),
			"cell":   cellRefString(first),
			"count":  len(obstructed),
		})
	}

	cost, err := a.upgradeCostLocked(rs, cmd.TargetClass)
	if err != nil {
		return UpgradeQuote{}, err
	}

	// AC-6: approve + schedule roadworks (not an instant swap).
	steady := a.cfg.classes[cmd.TargetClass].Lanes
	phases := defaultUpgradeRoadworks(a.nowMonth, steady, a.cfg.roadworks)
	rs.roadworks = phases
	tc := cmd.TargetClass
	rs.pendingClass = &tc

	// SEC-225: the returned quote must not alias the internal schedule slice —
	// a caller mutating quote.Phases would otherwise rewrite rs.roadworks and
	// bypass the command surface (GR#20). Deep-copy on return.
	return UpgradeQuote{CostMicropounds: int64(cost), Phases: append([]RoadworksPhase(nil), phases...)}, nil
}

// PreviewCapacityDelta returns the before/after lane counts and classes for
// a proposed upgrade (AC-7) — roads-owned figures only, never a journey-time
// estimate (that is a consuming layer's composition with engine.traffic).
// The "after" lane count is the reduced count during the upgrade's default
// roadworks phase, matching what [RoadsAPI.CurrentLaneCount] reports once
// the upgrade is approved (held by the AC-7 test).
func (a *RoadsAPI) PreviewCapacityDelta(id RoadID, target RoadClass) (CapacityDelta, error) {
	if err := a.checkNotCopied("PreviewCapacityDelta"); err != nil {
		return CapacityDelta{}, err
	}
	if !target.valid() {
		return CapacityDelta{}, roadsErr(a.correlationID, ErrInvalidClass, map[string]any{"class": uint8(target)})
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	rs, ok := a.roads[id]
	if !ok {
		return CapacityDelta{}, roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(id)})
	}
	before := a.cfg.classes[rs.class].Lanes
	after := defaultOpenLanes(a.cfg.classes[target].Lanes, a.cfg.roadworks.LaneReductionFraction)
	return CapacityDelta{
		BeforeClass: rs.class,
		AfterClass:  target,
		BeforeLanes: before,
		AfterLanes:  after,
	}, nil
}

// defaultOpenLanes computes the lanes OPEN during a roadworks phase for a
// road of steadyLanes: the steady count minus the (ceil-rounded) closure
// fraction, floored at zero (a single-lane road fully closes).
func defaultOpenLanes(steadyLanes int, fraction float64) int {
	closed := int(math.Ceil(float64(steadyLanes) * fraction))
	open := steadyLanes - closed
	if open < 0 {
		open = 0
	}
	return open
}

// defaultUpgradeRoadworks builds the default single-phase roadworks schedule
// an upgrade schedules, starting at startMonth (AC-6).
func defaultUpgradeRoadworks(startMonth int64, steadyLanes int, rw roadworksConfig) []RoadworksPhase {
	return []RoadworksPhase{{
		StartMonth:     startMonth,
		DurationMonths: rw.PhaseDurationMonths,
		OpenLanes:      defaultOpenLanes(steadyLanes, rw.LaneReductionFraction),
	}}
}

// upgradeCostLocked computes the fixed-point upgrade cost (AC-4):
// delta + rebuild disruption + rung-distance scaling + widening land cost.
// The caller holds a.mu.
func (a *RoadsAPI) upgradeCostLocked(rs *roadState, target RoadClass) (det.Micropounds, error) {
	cur := a.cfg.classes[rs.class]
	tgt := a.cfg.classes[target]
	curBase := det.FromPounds(cur.BaseCostPounds)
	tgtBase := det.FromPounds(tgt.BaseCostPounds)

	var total det.Micropounds
	// Delta: an upgrade-up costs the difference; a downgrade carries no
	// negative delta (only the disruption + rung scaling below).
	if tgtBase > curBase {
		d, err := det.Sub(a.correlationID, tgtBase, curBase)
		if err != nil {
			return 0, err
		}
		total = d
	}

	// Rebuild disruption (§51 "cost = delta + rebuild disruption").
	disruption, err := det.MulRat(a.correlationID, tgtBase, a.cfg.upgrade.RebuildDisruptionPermille, 1000)
	if err != nil {
		return 0, err
	}
	// Rung-distance scaling: jumping more rungs costs proportionally more.
	rungDist := int64(int(target) - int(rs.class))
	if rungDist < 0 {
		rungDist = -rungDist
	}
	// The rungDist * RungDistanceCostPermille numerator is checked (num.SafeMul)
	// before it feeds the fixed-point MulRat: buildConfig only validates the
	// permille as ">= 0", so a schema-valid-but-hostile value (e.g. MaxInt64)
	// would otherwise wrap int64 into a negative numerator and make a downgrade
	// pay the player (SEC-223). Reject rather than saturate — a saturating
	// clamp would still overflow tgtBase*num inside MulRat.
	scaled, overflowed := num.SafeMul(rungDist, a.cfg.upgrade.RungDistanceCostPermille)
	if overflowed {
		return 0, invalidInputError(a.correlationID, "upgrade.rungDistanceCostPermille",
			fmt.Sprintf("rung-distance cost overflow (rungDist=%d × permille=%d)",
				rungDist, a.cfg.upgrade.RungDistanceCostPermille))
	}
	rungPenalty, err := det.MulRat(a.correlationID, tgtBase, scaled, 1000)
	if err != nil {
		return 0, err
	}
	landCost, err := a.wideningLandCostLocked(rs, target)
	if err != nil {
		return 0, err
	}

	for _, part := range []det.Micropounds{disruption, rungPenalty, landCost} {
		total, err = det.Add(a.correlationID, total, part)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// wideningLandCostLocked prices the footprint expansion into new cells
// (AC-5's land purchase): the per-cell land cost from data times the number
// of cells the target footprint adds beyond the current one. The caller
// holds a.mu.
func (a *RoadsAPI) wideningLandCostLocked(rs *roadState, target RoadClass) (det.Micropounds, error) {
	curWidth := a.cfg.classes[rs.class].WidthCells
	tgtWidth := a.cfg.classes[target].WidthCells
	if tgtWidth <= curWidth {
		return 0, nil
	}
	start, end := a.nodes[rs.start], a.nodes[rs.end]
	targetFP := computeFootprint(start.Pos, end.Pos, tgtWidth)
	owned := make(map[cellKey]struct{}, len(rs.footprint))
	for _, c := range rs.footprint {
		owned[keyFor(c)] = struct{}{}
	}
	var newCells int64
	for _, c := range targetFP {
		if _, ok := owned[keyFor(c)]; !ok {
			newCells++
		}
	}
	perCell := det.FromPounds(a.cfg.upgrade.LandCostPerCellPounds)
	return det.MulRat(a.correlationID, perCell, newCells, 1)
}

// obstructedCellsLocked returns the target-footprint cells (beyond the
// current footprint) that are occupied — zoned or structured — and thus
// block a widening (AC-5). A nil return with a nil error means the widening
// is clear. It requires engine.world to be wired when the target footprint
// actually widens (ErrWorldNotWired otherwise — fail closed, GR#17).
//
// The caller holds a.mu. The engine.world seam is a pure read (CellAt) that
// cannot re-enter this package, so holding a.mu across it is safe (FEAT-135:
// the lock-order risk would require world → roads, which does not exist).
func (a *RoadsAPI) obstructedCellsLocked(rs *roadState, target RoadClass) ([]CellRef, error) {
	curWidth := a.cfg.classes[rs.class].WidthCells
	tgtWidth := a.cfg.classes[target].WidthCells
	if tgtWidth <= curWidth {
		return nil, nil // no widening, nothing to check
	}
	if a.world == nil {
		return nil, roadsErr(a.correlationID, ErrWorldNotWired, nil)
	}
	start, end := a.nodes[rs.start], a.nodes[rs.end]
	targetFP := computeFootprint(start.Pos, end.Pos, tgtWidth)
	owned := make(map[cellKey]struct{}, len(rs.footprint))
	for _, c := range rs.footprint {
		owned[keyFor(c)] = struct{}{}
	}
	var obstructed []CellRef
	for _, c := range targetFP {
		if _, ok := owned[keyFor(c)]; ok {
			continue
		}
		cell, err := a.world.CellAt(c.Tile, c.Local, a.correlationID)
		if err != nil {
			return nil, err
		}
		if cell.StructureRef != 0 || cell.Zoning != world.ZoningNone {
			obstructed = append(obstructed, c)
		}
	}
	return obstructed, nil
}
