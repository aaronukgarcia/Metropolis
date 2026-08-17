package wellbeing

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// This file defines wellbeing's local view of each registered outbound
// module's inbound contract (code.json engine.wellbeing.outbound.calls) —
// the GR#20 "consume via registered interfaces" shape. Each seam is
// satisfied by the real module (or a thin adapter) at the composition root,
// and by a fake in this package's tests. Modules that are unbuilt at this
// module's first pass (engine.traffic, engine.shopping) are consumed purely
// through these seams; built modules are consumed through the seam too, with
// a compile-time assertion proving the real type satisfies it.

// SeasonSource is the seam over engine.season's §9/§18 HealthWaveModifier.
// The real *season.SeasonAPI satisfies it structurally (asserted below).
type SeasonSource interface {
	HealthWaveModifier(monthIndex int64) (float64, error)
}

// ShoppingSource is the seam over engine.shopping's fresh-food share (§37,
// the Diet driver's input). ok=false means "no record for this citizen yet"
// — the gather path degrades the driver to a neutral delta + low confidence
// (AC-14) rather than propagating a NaN or divide-by-zero.
type ShoppingSource interface {
	FreshFoodShare(citizenID uint64, correlationID string) (float64, bool, error)
}

// TrafficSource is the seam over engine.traffic's door-to-door commute
// figure (§19.3, the CommuteTime driver) and active-travel mode share (the
// ActiveTravel driver).
type TrafficSource interface {
	CommuteMinutes(citizenID uint64, correlationID string) (float64, bool, error)
	ActiveTravelShare(citizenID uint64, correlationID string) (float64, bool, error)
}

// HealthcareSource is the seam over engine.services' healthcare
// access/quality (the HealthcareAccess driver). ok=false means "no coverage
// record for this citizen yet" (AC-14).
type HealthcareSource interface {
	HealthcareAccess(citizenID uint64, correlationID string) (float64, bool, error)
}

// NeighbourhoodSource is the seam over engine.world's neighbourhood
// overlays not yet carried as OverlayScratch bytes — green space within
// 400m (§18) and noise exposure.
type NeighbourhoodSource interface {
	GreenSpace400m(citizenID uint64, correlationID string) (float64, bool, error)
	Noise(citizenID uint64, correlationID string) (float64, bool, error)
}

// PollutionSource is the seam over engine.world's home-cell pollution
// overlay (OverlayScratch.Pollution, §2.4). The real *world.WorldAPI is
// adapted by [WorldPollution]; a fake satisfies it in tests (AC-12b).
type PollutionSource interface {
	Pollution(home uint32, correlationID string) (float64, bool, error)
}

// Compile-time proof that the real engine.season API satisfies this
// package's SeasonSource seam (GR#20: consume the real registered interface,
// never a reimplementation of the seasonal-health curve — AC-10).
var _ SeasonSource = (*season.SeasonAPI)(nil)

// WorldPollution adapts *world.WorldAPI to PollutionSource, reading the home
// cell's pollution overlay byte and normalising the 0-255 byte to [0,1]
// (AC-12b). It is the composition root's concrete bridge from the real
// engine.world to this package's seam.
type WorldPollution struct {
	World *world.WorldAPI
}

// Pollution implements PollutionSource. An unowned home tile (or a nil
// world) reports ok=false — the tile has no simulated overlay yet, which the
// gather path degrades to a neutral delta + low confidence (AC-14). Tile
// ownership (TileAt.Owned) is the "is this cell simulated" signal; the
// per-cell Cell.Owner byte is a separate per-cell ownership assignment and
// reads 0 until a later ApplyOwnershipCommand writes it.
func (p WorldPollution) Pollution(home uint32, correlationID string) (float64, bool, error) {
	if p.World == nil {
		return 0, false, nil
	}
	tile, local := homeToWorld(home)
	info, err := p.World.TileAt(tile, correlationID)
	if err != nil {
		return 0, false, err
	}
	if !info.Owned {
		return 0, false, nil
	}
	cell, err := p.World.CellAt(tile, local, correlationID)
	if err != nil {
		return 0, false, err
	}
	return float64(cell.Overlay.Pollution) / 255.0, true, nil
}

// homeToWorld maps a packed citizens.CellRef (the linear index of a cell in
// the 30×30-tile × 200×200-cell world grid) to a world TileCoord + CellLocal.
// This is a documented placeholder mapping (ASM-2) until engine.citizens and
// engine.world converge on an explicit shared coordinate key — it exists so
// the real-world pollution read is already wired and testable today.
func homeToWorld(home uint32) (world.TileCoord, world.CellLocal) {
	const (
		cellsPerTile = 200 * 200
		tilesPerSide = 30
	)
	tileIdx := home / cellsPerTile
	localIdx := home % cellsPerTile
	return world.TileCoord{
			X: int(tileIdx) % tilesPerSide,
			Y: int(tileIdx) / tilesPerSide,
		}, world.CellLocal{
			Col: int(localIdx) % 200,
			Row: int(localIdx) / 200,
		}
}
