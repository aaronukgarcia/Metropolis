package world

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// This file is §2.3's tile pricing model (AC-11): "Tile price scales with
// terrain quality, adjacency to your city, and milestone tier" — a
// function of all three, never a flat per-tile constant.

// basePricePerTile is the Sprint-3-era placeholder base price (in-game
// currency units) before terrain/adjacency/tier adjustment. The real
// economic tuning pass belongs to a later finance-balancing item; this
// value only needs to be non-zero and consistently applied so the three
// documented factors each visibly move the result (ASM — see dispatch
// report).
const basePricePerTile = 10000.0

// terrainQualityFactor scores a tile's terrain quality in (0, 1.5]: 1.0
// for an average buildable tile, higher for a tile that is mostly flat
// buildable land, lower for steep/unbuildable or sea-heavy terrain.
func terrainQualityFactor(t *tile) float64 {
	if !t.onLand {
		return 0.1 // sea tiles are cheap — nothing to build until reclamation (§2.1)
	}
	buildable := 0
	for _, s := range t.terrain.slope {
		if s == SlopeFlat || s == SlopeGentle {
			buildable++
		}
	}
	frac := float64(buildable) / float64(CellsPerTile)
	// Map [0,1] buildable fraction to a [0.4, 1.5] quality factor —
	// even a mostly-steep tile retains some value (views, later
	// engineering), a fully flat tile is worth the most.
	return 0.4 + frac*1.1
}

// adjacencyFactor scales price up the more of a tile's four orthogonal
// neighbours are already owned — buying land that extends your
// contiguous city costs more than an isolated speculative purchase.
func adjacencyFactor(ownedNeighbors int) float64 {
	return 1.0 + float64(ownedNeighbors)*0.25
}

// milestoneTierFactor scales price with the player's milestone
// progression (later-game land costs more, matching the late-game
// density unlocks §2.3 describes).
func milestoneTierFactor(tier int) float64 {
	if tier < 1 {
		tier = 1
	}
	return 1.0 + float64(tier-1)*0.5
}

// tilePrice computes tile c's purchase price: base * terrain quality *
// adjacency * milestone tier (AC-11). Caller must hold at least a read
// lock on w (or be inside a write-locked section already, e.g.
// PurchaseTile) since it reads neighbour ownership state directly off
// w.tiles without its own locking.
//
// BUG-064 (AC-27): astgate's live-tree scan names this exact function as
// an unguarded reachable function for the World candidate type — see
// ensureTile's doc comment (grid.go) for why this defence-in-depth call
// is correct even though every *WorldAPI caller already checks before
// taking the lock.
func (w *World) tilePrice(t *tile) (float64, error) {
	if err := w.checkNotCopied(errs.NewCorrelationID(), map[string]any{"tile": t.coord}); err != nil {
		return 0, err
	}
	ownedNeighbors := 0
	for _, off := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nc := TileCoord{X: t.coord.X + off[0], Y: t.coord.Y + off[1]}
		if !nc.InExtent() {
			continue
		}
		if nt, ok := w.tiles[nc]; ok && nt.owned {
			ownedNeighbors++
		}
	}
	return basePricePerTile * terrainQualityFactor(t) * adjacencyFactor(ownedNeighbors) * milestoneTierFactor(w.milestoneTier), nil
}
