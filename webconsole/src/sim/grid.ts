// grid.ts — FEAT-2326609790 (Aaron, 2026-09-05, verbatim: "double the land
// mass we need more room now"). SSOT for the map grid dimensions.
//
// Extracted out of data.ts (2026-09-05) so consolidator.ts and
// consolidatorGlide.ts can import the REAL constants instead of maintaining
// hand-duplicated local mirrors (a GR#3 SSOT violation: MAP_W/MAP_H were
// previously declared THREE times — data.ts, consolidator.ts, and a "Local
// mirror" const in consolidatorGlide.ts — with a comment on each site
// promising the values would "stay in sync"). This file is a deliberate
// LEAF: zero imports, so every other sim module can import it with no
// import-cycle risk at all (data.ts <-> consolidator.ts already has a
// real call-time cycle — see data.ts's own header comment — so routing the
// grid constants through a dependency-free leaf sidesteps that entirely
// rather than betting on TDZ ordering across the cycle).
//
// SIZE HISTORY (GR#2 version discipline / save-compat, see replay.ts's
// Savepoint.gridW/gridH doc comment for the load-time guard):
//   - original: 440 x 260 tiles
//   - 2026-09-05 (FEAT-2326609790): doubled to 624 x 368 (2.01x area,
//     aspect preserved to 2 sig figs: 440/260 = 1.692, 624/368 = 1.696).
//     Grown EAST and SOUTH only (x in [0,440) and y in [0,260) are an
//     unchanged sub-rectangle of the new grid) so every pre-existing
//     coordinate — including every building in a savepoint captured under
//     the old size — stays valid with no coordinate translation needed.
//     Never shrink these without reading replay.ts's gridW/gridH gate: a
//     save stamped with a LARGER grid than this build defines must be
//     refused (MET-V873), never silently truncated.
export const MAP_W = 624;
export const MAP_H = 368;

/**
 * Tile grid reference size in metres (data.ts:122 "Tile grid = 50 m").
 * BUG-1051 (2026-09-11): hosted HERE, in the dependency-free leaf, because
 * consolidator.ts previously owned it and sectorPartition.ts read it at
 * module top level while consolidator.ts -> data.ts -> sectorPartition.ts
 * formed a cycle: any suite whose entry point was consolidator.ts hit
 * "Cannot access 'TILE_METRES' before initialization" (CI run 34570495115).
 * consolidator.ts re-exports this symbol so every existing consumer is
 * unchanged; sectorPartition.ts imports the leaf directly.
 */
export const TILE_METRES = 50;
