package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-2326609845 -- the build -> citizens housing bridge.
//
// Once per day (piggybacked on the existing daily build hook, like the
// deathservices registry bridge -- NOT a new PhaseHook, so PhaseHookCount and
// the perf baseline are unchanged) the composition root:
//
//  1. compares build.StructureEpoch (O(1)) with the epoch it last synced, and
//     only when it moved (or the citizens ledger is off, e.g. just after a
//     load) pushes the standing residential structures into citizens as
//     (cell, capacity) pairs;
//  2. asks citizens to house the homeless into that capacity.
//
// A LOAD does neither (r2 ruling): citizens restores its last-applied capacity
// snapshot and rebuilds occupancy from the Home columns (RestoreHousing), and
// housingSynced is cleared so the NEXT daily sync pushes the snapshot and
// assigns -- exactly what the uninterrupted run does on that same day.
//
// engine.citizens never sees engine.build (GR#20/GR#25): this file is the one
// place both edges meet, exactly like compose_buildregistry.go.
//
// A structure's identity is its linear cell y*width+x over the world grid
// (the same row-major index citizen_query.go decodes). Cell 0 collides with
// the "no home" sentinel, so citizens ignores a structure there.

// housingCellRef is the linear world-grid cell of a (tile, local) position.
func housingCellRef(tile world.TileCoord, local world.CellLocal) (citizens.CellRef, bool) {
	y := tile.Y*world.TileSizeCells + local.Row
	x := tile.X*world.TileSizeCells + local.Col
	if x < 0 || y < 0 || x >= worldGridWidthCells || y >= worldGridWidthCells {
		return 0, false
	}
	return citizens.CellRef(y*worldGridWidthCells + x), true
}

// housingCapacities converts build's standing-structure list into citizens'
// (cell, capacity) snapshot. Pure and order-preserving.
func housingCapacities(standing []build.StandingCapacity) []citizens.HomeCapacity {
	out := make([]citizens.HomeCapacity, 0, len(standing))
	for _, s := range standing {
		if s.ResidentCapacity <= 0 {
			continue
		}
		ref, ok := housingCellRef(s.Tile, s.Local)
		if !ok {
			continue
		}
		capacity := s.ResidentCapacity
		if capacity > 1<<30 {
			capacity = 1 << 30
		}
		out = append(out, citizens.HomeCapacity{Cell: ref, Capacity: int32(capacity)})
	}
	return out
}

// restoreHousing is the load-time half: materialise the restored ledger (no
// assignment, no eviction, no Home changes) and force the next daily sync to
// re-push the capacity snapshot. Failures are logged, never fatal --
// the same posture as every other daily bridge.
func (st *simState) restoreHousing() {
	st.housingSynced = false
	if st.citizens == nil {
		return
	}
	if err := st.citizens.RestoreHousing(st.cid); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "op": "RestoreHousing", "cause": err.Error()})
	}
}

// syncHousing is the daily bridge. Failures are logged, never fatal -- the
// same posture as every other daily bridge.
func (st *simState) syncHousing() {
	if st.buildAPI == nil || st.citizens == nil {
		return
	}
	epoch := st.buildAPI.StructureEpoch()
	if !st.housingSynced || epoch != st.housingEpoch || !st.citizens.HousingEnabled() {
		caps := housingCapacities(st.buildAPI.StandingCapacities())
		if err := st.citizens.SetHomeCapacities(caps, st.cid); err != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "op": "SetHomeCapacities", "cause": err.Error()})
			return
		}
		st.housingEpoch = epoch
		st.housingSynced = true
	}
	if _, err := st.citizens.AssignHomes(st.cid); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "op": "AssignHomes", "cause": err.Error()})
	}
}
