package world

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is the code.json-registered inbound contract for engine.world
// (GUID 2d8855b8-f4f0-43a3-a179-67accca83115, "WorldAPI... cell/tile
// queries, ownership mutations via commands only"). Every consumer
// module (engine.build, engine.roads, engine.traffic, ...) reaches this
// package ONLY through *WorldAPI's exported methods — never through
// grid.go's internal SoA storage directly (GR#20).
//
// Query methods (CellAt, TileAt, ...) return plain values. Mutation
// methods take a typed *Command struct and return a protocol.CommandResult
// exactly like engine.core's own command surface (docs/design/protocol.md;
// AC-1, AC-15) — this package does not register new protocol.Kind values
// in internal/protocol (out of this item's owned path), so its commands
// are package-local structs, but the RESULT shape and rejection
// semantics (ErrorRef, never a panic, never a silent no-op) are the
// same protocol.CommandResult/ErrorRef every other engine module uses.

// WorldAPI is the sole entry point into engine.world's state.
type WorldAPI struct {
	w *World
}

// NewWorldAPI constructs a WorldAPI over a fresh World whose start tile
// (§2.1) will live at startCoord once ImportAndPlaceStartTile runs.
func NewWorldAPI(startCoord TileCoord) *WorldAPI {
	return &WorldAPI{w: NewWorld(startCoord)}
}

// ImportAndPlaceStartTile runs the build-time OS Terrain 50 importer
// (terrain_import.go's ImportTerrain, AC-2/AC-3) against src and installs
// the result as the real start tile's terrain. Must be called before any
// query against the start tile if real (not synthesized-placeholder)
// terrain is required; safe to call before or after other tiles have
// been queried.
//
// BUG-062 (ASM-215, logged at fix time): re-import is a PARTIAL reset —
// only the terrain SoA (elevation/slope/surface) is replaced.
// Ownership, sim state, ownerID, geology and prospected status all live
// on the same *tile struct and are DELIBERATELY preserved across
// re-import, never wiped. The prior implementation did
// delete(a.w.tiles, a.w.startCoord) to force regeneration, which
// destroyed the whole *tile struct — silently reverting a purchased,
// prospected, zoned start tile to unowned/unprospected/zero with no
// error returned (live-reproduced by Destructive-2). Chosen contract:
// re-import always succeeds and never discards player state, because a
// re-import is a legitimate, expected operation (a retry, a second
// importer run, a scenario re-running setup per BUG-062's own
// description) — refusing it outright would make a normal operation
// fail, and refusing it ONLY when state would be lost would make
// re-import's behaviour depend on whether anyone has touched the tile
// yet, which is a worse, harder-to-predict contract than "terrain
// refreshes, everything else is untouched, always". If a future
// caller needs the OTHER contract (wipe ownership deliberately), that
// must be its own explicit, named, registry-erroring operation — never
// a side effect of re-importing terrain.
//
// BUG-066 (Destructive-3, logged at fix time): "everything else is
// untouched" has ONE deliberate exception — per-cell landValue (inside
// sim). landValue is not independent player-facing state; it is
// terrain-derived (PurchaseTile seeds it from terrainQualityFactor(tl),
// itself a pure function of the tile's onLand flag and terrain.slope).
// Leaving it at its pre-reimport value would silently strand it against
// the OLD terrain the moment terrain.slope is refreshed below — a
// permanent, silent mismatch between a cell's stored economic value and
// its actual terrain (repro: 1500.00 stale vs 1346.00 fresh against
// genuinely different fixture terrain), with nothing that looks wrong
// (a plausible non-zero landValue, just the wrong one). So for an
// already-owned tile, landValue is recomputed from the NEW terrain in
// the same call that refreshes it, matching "terrain refreshes,
// everything ABOUT terrain refreshes with it" — geology and onLand need
// no such recompute because they are coord-derived, not heightmap-
// derived, so re-import never changes their inputs.
func (a *WorldAPI) ImportAndPlaceStartTile(src *SourceGrid, correlationID string) error {
	// BUG-064 (AC-28): identity check BEFORE a.w.mu is touched at all —
	// see World.checkNotCopied's doc comment (grid.go) for why a copy's
	// mu must never be acquired.
	if err := a.w.checkNotCopied(correlationID, nil); err != nil {
		return err
	}
	heights, err := ImportTerrain(src, correlationID)
	if err != nil {
		return err
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	// Defence-in-depth re-check under the lock (cheap — one more atomic
	// load — mirrors engine.core's RegisterPhaseHook/seal pattern).
	if err := a.w.checkNotCopied(correlationID, nil); err != nil {
		return err
	}
	a.w.startHeight = heights
	if t := a.w.tiles[a.w.startCoord]; t != nil {
		// Tile already generated (e.g. an earlier query or a prior
		// import): refresh terrain in place, preserving sim/owned/
		// ownerID/geology/prospected on the SAME *tile struct.
		populateTerrainFromHeightmap(t, heights)
		// BUG-066: landValue is derived FROM the terrain just refreshed
		// above, not independent state — recompute it for an
		// already-owned tile so it never strands against stale terrain.
		if t.owned && t.sim != nil {
			q := terrainQualityFactor(t)
			for i := range t.sim.landValue {
				t.sim.landValue[i] = float32(q * 1000)
			}
		}
	}
	// If the tile has never been generated yet, leave it absent —
	// ensureTile's next call will build it correctly from
	// w.startHeight (already set above), with no state to lose.
	return nil
}

// CellAt returns the Cell snapshot at a global position (tile + local
// coordinate). Terrain fields are always populated; ownership/zoning/
// structureRef/landValue/overlay-scratch read as zero values for an
// unowned tile (AC-10 — "exposes terrain... but is not fully
// simulated"), which is the correct, honest answer: those fields
// genuinely do not exist yet for land nobody has bought.
func (a *WorldAPI) CellAt(t TileCoord, local CellLocal, correlationID string) (Cell, error) {
	// BUG-064 (AC-28): identity check BEFORE a.w.mu is touched at all.
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": t}); err != nil {
		return Cell{}, err
	}
	if !t.InExtent() {
		return Cell{}, errs.New(ErrTileOutOfBounds, correlationID, map[string]any{"tile": t})
	}
	// BUG-063: validate Col/Row INDIVIDUALLY before ever computing the
	// composite index — a composite-only check lets an out-of-domain
	// Col/Row combination alias back into range and silently read the
	// wrong cell. See CellLocal.InBounds's doc comment (hydrology.go).
	if !local.InBounds() {
		return Cell{}, errs.New(ErrTileOutOfBounds, correlationID, map[string]any{"tile": t, "local": local})
	}
	idx := localIndex(local.Col, local.Row)

	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	// Defence-in-depth re-check under the lock.
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": t}); err != nil {
		return Cell{}, err
	}
	tl, err := a.w.ensureTile(t)
	if err != nil {
		return Cell{}, err
	}

	c := Cell{
		Elevation: tl.terrain.elevation[idx],
		Slope:     tl.terrain.slope[idx],
		Surface:   tl.terrain.surface[idx],
	}
	if tl.sim != nil {
		c.Owner = tl.sim.owner[idx]
		c.Zoning = tl.sim.zoning[idx]
		c.StructureRef = tl.sim.structureRef[idx]
		c.LandValue = tl.sim.landValue[idx]
		c.Overlay = OverlayScratch{
			Traffic: tl.sim.traffic[idx], UtilityCoverage: tl.sim.utility[idx],
			Pollution: tl.sim.pollution[idx], Decay: tl.sim.decay[idx],
		}
	}
	return c, nil
}

// TileInfo is TileAt's read-only summary of one tile.
type TileInfo struct {
	Coord   TileCoord
	Owned   bool
	OwnerID uint32
	OnLand  bool
	Price   float64 // meaningful only when !Owned (AC-10/AC-11)
}

// TileAt returns tile c's summary — terrain existence, ownership, and
// (for an unowned tile) its purchase price (AC-10).
func (a *WorldAPI) TileAt(c TileCoord, correlationID string) (TileInfo, error) {
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return TileInfo{}, err
	}
	if !c.InExtent() {
		return TileInfo{}, errs.New(ErrTileOutOfBounds, correlationID, map[string]any{"tile": c})
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return TileInfo{}, err
	}
	tl, err := a.w.ensureTile(c)
	if err != nil {
		return TileInfo{}, err
	}
	info := TileInfo{Coord: c, Owned: tl.owned, OwnerID: tl.ownerID, OnLand: tl.onLand}
	if !tl.owned {
		price, err := a.w.tilePrice(tl)
		if err != nil {
			return TileInfo{}, err
		}
		info.Price = price
	}
	return info, nil
}

// OwnershipCommand mutates one cell's ownership-governed fields
// (zoning/owner) on a tile the caller already owns — AC-1's "ownership
// mutations expressed only as commands (never a direct field-set)".
type OwnershipCommand struct {
	CorrelationID string
	Tile          TileCoord
	Local         CellLocal
	NewOwner      uint32
	NewZoning     Zoning
}

// ApplyOwnershipCommand is the ONLY way a caller may change a cell's
// owner/zoning — there is no exported setter on Cell or on this
// package's internal grid (AC-1). Rejects (never panics, never silently
// no-ops) a command against an out-of-bounds cell or an unowned tile
// (AC-15).
func (a *WorldAPI) ApplyOwnershipCommand(cmd OwnershipCommand) protocol.CommandResult {
	result := func(accepted bool, err error) protocol.CommandResult {
		r := protocol.CommandResult{CorrelationID: protocol.CorrelationID(cmd.CorrelationID), Accepted: accepted}
		if !accepted {
			r.Error = toWorldErrorRef(err)
		}
		return r
	}

	if err := a.w.checkNotCopied(cmd.CorrelationID, map[string]any{"tile": cmd.Tile}); err != nil {
		return result(false, err)
	}
	if !cmd.Tile.InExtent() {
		return result(false, errs.New(ErrTileOutOfBounds, cmd.CorrelationID, map[string]any{"tile": cmd.Tile}))
	}

	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(cmd.CorrelationID, map[string]any{"tile": cmd.Tile}); err != nil {
		return result(false, err)
	}
	tl, err := a.w.ensureTile(cmd.Tile)
	if err != nil {
		return result(false, err)
	}
	if !tl.owned || tl.sim == nil {
		return result(false, errs.New(ErrTileNotOwned, cmd.CorrelationID, map[string]any{"tile": cmd.Tile}))
	}
	// BUG-063: same individual Col/Row validation as CellAt — reject
	// BEFORE computing the composite index, never after.
	if !cmd.Local.InBounds() {
		return result(false, errs.New(ErrTileOutOfBounds, cmd.CorrelationID, map[string]any{"tile": cmd.Tile, "local": cmd.Local}))
	}
	idx := localIndex(cmd.Local.Col, cmd.Local.Row)
	tl.sim.owner[idx] = cmd.NewOwner
	tl.sim.zoning[idx] = cmd.NewZoning
	return result(true, nil)
}

// PurchaseCommand buys an unowned tile in full (§2.3).
type PurchaseCommand struct {
	CorrelationID string
	Tile          TileCoord
	BuyerID       uint32
}

// PurchaseTile allocates a tile's full-simulation storage (simGrid) and
// marks it owned — the moment a tile stops being "terrain + price only"
// (AC-10). Rejects an already-owned tile or an out-of-extent coordinate
// (AC-10, never silently re-charges or no-ops).
func (a *WorldAPI) PurchaseTile(cmd PurchaseCommand) protocol.CommandResult {
	result := func(accepted bool, err error) protocol.CommandResult {
		r := protocol.CommandResult{CorrelationID: protocol.CorrelationID(cmd.CorrelationID), Accepted: accepted}
		if !accepted {
			r.Error = toWorldErrorRef(err)
		}
		return r
	}

	if err := a.w.checkNotCopied(cmd.CorrelationID, map[string]any{"tile": cmd.Tile}); err != nil {
		return result(false, err)
	}
	if !cmd.Tile.InExtent() {
		return result(false, errs.New(ErrPurchaseRejected, cmd.CorrelationID, map[string]any{"tile": cmd.Tile, "cause": "out of expansion extent"}))
	}

	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(cmd.CorrelationID, map[string]any{"tile": cmd.Tile}); err != nil {
		return result(false, err)
	}
	tl, err := a.w.ensureTile(cmd.Tile)
	if err != nil {
		return result(false, err)
	}
	if tl.owned {
		return result(false, errs.New(ErrPurchaseRejected, cmd.CorrelationID, map[string]any{"tile": cmd.Tile, "cause": "already owned"}))
	}
	tl.owned = true
	tl.ownerID = cmd.BuyerID
	tl.sim = newSimGrid()
	// Seed every owned cell's initial land value from terrain quality so
	// engine.build/engine.finance (later consumers) have something
	// non-zero to read from cell 1 of ownership.
	q := terrainQualityFactor(tl)
	for i := range tl.sim.landValue {
		tl.sim.landValue[i] = float32(q * 1000)
	}
	return result(true, nil)
}

// TilePrice returns tile c's current purchase price (AC-11). Meaningless
// (but not an error) for an already-owned tile — callers should check
// TileAt's Owned flag first.
func (a *WorldAPI) TilePrice(c TileCoord, correlationID string) (float64, error) {
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return 0, err
	}
	if !c.InExtent() {
		return 0, errs.New(ErrTileOutOfBounds, correlationID, map[string]any{"tile": c})
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return 0, err
	}
	tl, err := a.w.ensureTile(c)
	if err != nil {
		return 0, err
	}
	price, err := a.w.tilePrice(tl)
	if err != nil {
		return 0, err
	}
	return price, nil
}

// Prospect runs a cheap survey over tile c (§32), revealing its geology
// pocket to subsequent PocketGeology queries. Idempotent — prospecting
// an already-prospected tile is a harmless no-op accept.
func (a *WorldAPI) Prospect(c TileCoord, correlationID string) error {
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return err
	}
	if !c.InExtent() {
		return errs.New(ErrTileOutOfBounds, correlationID, map[string]any{"tile": c})
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return err
	}
	tl, err := a.w.ensureTile(c)
	if err != nil {
		return err
	}
	tl.prospected = true
	return nil
}

// IsProspected reports whether tile c has been prospected.
//
// BUG-064 (AC-28): signature grew an error return (mirroring
// engine.core's Clock(), the established precedent for a mu-guarded
// getter that must reject a copied receiver — a bool-only return had no
// way to signal ErrWorldCopied). false, err is returned on a copy or an
// out-of-extent coordinate; false, nil is a genuine "not yet prospected"
// answer.
func (a *WorldAPI) IsProspected(c TileCoord) (bool, error) {
	if err := a.w.checkNotCopied(errs.NewCorrelationID(), map[string]any{"tile": c}); err != nil {
		return false, err
	}
	if !c.InExtent() {
		return false, nil
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(errs.NewCorrelationID(), map[string]any{"tile": c}); err != nil {
		return false, err
	}
	tl, err := a.w.ensureTile(c)
	if err != nil {
		return false, err
	}
	return tl.prospected, nil
}

// PocketGeology returns tile c's secondary geology pocket (clay/gravel/
// deep coal/none) — the mining-relevant part of the §32 layer. Rejects
// with ErrGeologyNotProspected until Prospect(c) has run (AC-7); the
// chalk baseline (always GeologyChalk today) is common knowledge and
// available via Geology regardless of prospecting.
func (a *WorldAPI) PocketGeology(c TileCoord, correlationID string) (GeologyKind, error) {
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		return GeologyUnknown, err
	}
	if !c.InExtent() {
		return GeologyUnknown, errs.New(ErrTileOutOfBounds, correlationID, map[string]any{"tile": c})
	}
	a.w.mu.Lock()
	if err := a.w.checkNotCopied(correlationID, map[string]any{"tile": c}); err != nil {
		a.w.mu.Unlock()
		return GeologyUnknown, err
	}
	tl, err := a.w.ensureTile(c)
	if err != nil {
		a.w.mu.Unlock()
		return GeologyUnknown, err
	}
	prospected := tl.prospected
	pocket := tl.geology.pocket
	a.w.mu.Unlock()
	if !prospected {
		return GeologyUnknown, errs.New(ErrGeologyNotProspected, correlationID, map[string]any{"tile": c})
	}
	return pocket, nil
}

// GeologyBaseline returns tile c's always-visible baseline formation
// (§32: "chalk everywhere") — never gated by prospecting.
//
// BUG-064 (AC-28): signature grew an error return, same rationale as
// IsProspected's doc comment above.
func (a *WorldAPI) GeologyBaseline(c TileCoord) (GeologyKind, error) {
	if err := a.w.checkNotCopied(errs.NewCorrelationID(), map[string]any{"tile": c}); err != nil {
		return GeologyUnknown, err
	}
	if !c.InExtent() {
		return GeologyUnknown, nil
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if err := a.w.checkNotCopied(errs.NewCorrelationID(), map[string]any{"tile": c}); err != nil {
		return GeologyUnknown, err
	}
	tl, err := a.w.ensureTile(c)
	if err != nil {
		return GeologyUnknown, err
	}
	return tl.geology.baseline, nil
}

// OffMapConnections returns the §2.2 off-map anchor set derived from the
// real start-tile heightmap (offmap.go), or nil if ImportAndPlaceStartTile
// has not run yet.
//
// BUG-064 (AC-28): signature grew an error return, same rationale as
// IsProspected's doc comment above — this method touches a.w.mu-guarded
// state (startHeight) directly, with no ensureTile call to lean on for
// the defence-in-depth check, so both the pre-lock and post-lock checks
// below are load-bearing here, not merely redundant with a helper's own.
func (a *WorldAPI) OffMapConnections() ([]OffMapConnection, error) {
	if err := a.w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil, err
	}
	a.w.mu.Lock()
	if err := a.w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		a.w.mu.Unlock()
		return nil, err
	}
	heights := a.w.startHeight
	a.w.mu.Unlock()
	if heights == nil {
		return nil, nil
	}
	return OffMapConnections(heights), nil
}

// toWorldErrorRef converts an error (always a *errs.E from this
// package's own construction sites) into a protocol.ErrorRef, mirroring
// engine.core's commands.go toErrorRef exactly (GR#1/GR#7: every
// rejection carries a registry code, never a panic, never an untyped
// string).
func toWorldErrorRef(err error) *protocol.ErrorRef {
	if e, ok := err.(*errs.E); ok {
		return &protocol.ErrorRef{Code: e.Code, Display: e.Display()}
	}
	wrapped := errs.Wrap(ErrTileOutOfBounds, "", err, map[string]any{"cause": err.Error()})
	return &protocol.ErrorRef{Code: wrapped.Code, Display: wrapped.Display()}
}
