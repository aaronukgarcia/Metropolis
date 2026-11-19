package build

import (
	"errors"
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-9995385628 (roads lane 1a, rule version 2, rules.LayRoad): a player
// road is a PROPERTY OF A CELL kept here, beside structures, not a building.
//
//   - It is ordered through the existing build queue (so cost, lead time, the
//     committed cost, the F3 queue and saves all work unchanged) as an order
//     whose zone is [ZoneRoad]; ZoneRoad is NOT in the eight-zone catalogue.
//   - On completion Tick writes roadCells and nothing else: no zoneState, no
//     structure, no world.SetStructure, no resident or job capacity.
//   - A road never holds a cell: [cellInUse] ignores road orders, so a home can
//     be ordered on a road cell and a road on a home's cell.
//   - Nothing in the engine reads a road yet except the map (the viewport
//     publisher) through [BuildAPI.RoadCells]; the transport lever, later,
//     reads [BuildAPI.HasRoad].
//
// Cost and lead time are DATA (data/roadlay.json, GR#15), loaded in [Load].

// ZoneRoad is the zone string of a road order. It marks a build order only;
// it is not a catalogue zone and never appears in zoneState or structures.
const ZoneRoad ZoneType = "road"

// roadEconomics is the loaded economics of one road cell (data/roadlay.json).
type roadEconomics struct {
	materials int64 // tonnes of construction materials
	labour    int64 // worker-days
	leadDays  int64 // base lead time in simulation days
}

// loadRoadLay reads data/roadlay.json. A missing file is not fatal at Load
// (a synthetic data directory built by a test need not carry one): the error
// is kept and returned by the first road order, loudly. Any other failure
// (malformed, a negative number, an unknown class) fails Load.
func loadRoadLay(dir, correlationID string) (lay *roadEconomics, deferred, fatal error) {
	rl, err := data.LoadRoadLay(dir, correlationID)
	if err != nil {
		var e *errs.E
		if errors.As(err, &e) && e.Code == data.CodeFileNotFound {
			return nil, err, nil
		}
		return nil, nil, err
	}
	return &roadEconomics{materials: *rl.MaterialsTonnes, labour: *rl.LabourDays, leadDays: *rl.LeadDays}, nil, nil
}

// validMultiplier reports whether a seasonal construction-speed multiplier
// is usable (finite and positive), as SubmitBuildCommand requires.
func validMultiplier(m float64) bool {
	return m > 0 && !math.IsNaN(m) && !math.IsInf(m, 0)
}

// SubmitRoadCommand orders one road cell on an owned cell (rule version 2).
// cmd.Zone is ignored (a road). Below rules.LayRoad the command is refused
// exactly as it was before the rule existed: an unknown zone type
// (MET-G501), checked first, before anything else.
//
// A second order on a cell that already carries a road, built or queued, is
// refused with MET-G511 before an order number is taken: nothing is charged,
// reserved or journaled. A home on the cell does not matter.
func (b *BuildAPI) SubmitRoadCommand(cmd BuildCommand) (BuildOrderID, error) {
	if err := b.checkNotCopied("SubmitRoadCommand"); err != nil {
		return 0, err
	}
	b.mu.RLock()
	enabled := rules.Has(b.activeRules(), rules.LayRoad)
	lay, layErr := b.roadLay, b.roadLayErr
	b.mu.RUnlock()
	if !enabled {
		return 0, errs.New(ErrUnknownZoneType, b.correlationID, map[string]any{"zone": string(ZoneRoad)})
	}
	if lay == nil {
		if layErr != nil {
			return 0, layErr
		}
		return 0, errs.New(ErrDependencyMissing, b.correlationID, map[string]any{"dependency": "roadlay.json", "operation": "SubmitRoadCommand"})
	}
	if cmd.Month < 0 {
		return 0, errs.New(ErrInvalidMonth, b.correlationID, map[string]any{"month": cmd.Month})
	}
	if err := b.requireOwned(cmd.Tile, cmd.Local, cmd.OwnerID, "SubmitRoadCommand"); err != nil {
		return 0, err
	}
	if b.season == nil {
		return 0, errs.New(ErrDependencyMissing, b.correlationID, map[string]any{"dependency": "season", "operation": "SubmitRoadCommand"})
	}
	mult, err := b.season.ConstructionSpeedMultiplier(cmd.Month)
	if err != nil {
		return 0, err
	}
	if !validMultiplier(mult) {
		return 0, errs.New(ErrInvalidSeasonalMultiplier, b.correlationID, map[string]any{"multiplier": mult, "month": cmd.Month})
	}
	leadTime := effectiveLeadTime(lay.leadDays, mult)

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkNotCopied("SubmitRoadCommand"); err != nil {
		return 0, err
	}
	key := cellKey{tile: cmd.Tile, local: cmd.Local}
	if roadOnCell(b.roadCells, b.queue, key) {
		return 0, errs.New(ErrRoadAlreadyLaid, b.correlationID, map[string]any{"tile": cmd.Tile, "local": cmd.Local})
	}
	b.nextOrder++
	order := &buildOrder{
		id:                 b.nextOrder,
		tile:               cmd.Tile,
		local:              cmd.Local,
		zone:               ZoneRoad,
		materialsTotal:     lay.materials,
		materialsRemaining: lay.materials,
		labourRemaining:    lay.labour,
		leadTimeRemaining:  leadTime,
	}
	b.queue = append(b.queue, order)
	return order.id, nil
}

// roadOnCell reports whether the cell carries a completed road or holds a
// queued road order that has not completed. The queue is walked in slice
// order, never a map range. Caller holds b.mu.
func roadOnCell(roads map[cellKey]struct{}, queue []*buildOrder, key cellKey) bool {
	if _, built := roads[key]; built {
		return true
	}
	for _, o := range queue {
		if !o.complete && o.zone == ZoneRoad && o.tile == key.tile && o.local == key.local {
			return true
		}
	}
	return false
}

// HasRoad reports whether the cell carries a COMPLETED road. A queued road
// does not count. It is the read the transport lever will use.
func (b *BuildAPI) HasRoad(tile world.TileCoord, local world.CellLocal) bool {
	if err := b.checkNotCopied("HasRoad"); err != nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.roadCells[cellKey{tile: tile, local: local}]
	return ok
}

// RoadCells returns the completed road cells of one tile in a fixed order
// (ascending row, then column), under one lock.
func (b *BuildAPI) RoadCells(tile world.TileCoord) []world.CellLocal {
	if err := b.checkNotCopied("RoadCells"); err != nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]world.CellLocal, 0, len(b.roadCells))
	for k := range b.roadCells {
		if k.tile == tile {
			out = append(out, k.local)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Row != out[j].Row {
			return out[i].Row < out[j].Row
		}
		return out[i].Col < out[j].Col
	})
	return out
}
