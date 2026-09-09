// FEAT-2326609792 inc1 (AC-4): the thin production wiring module that
// statically imports the real data/traffic/scale_ladder.json (inc0, owned
// by a sibling lane) and runs it through scaleLadder.ts's schema-agnostic
// loadScaleLadder. Kept separate from scaleLadder.ts itself so that file's
// tests can exercise loadScaleLadder/ladderAt against small hand-built
// fixtures without ever touching the sibling lane's live data file (which
// may not exist, or may change shape, while inc1 is being built -- Aaron's
// explicit "build now, do not wait" sequencing order, 2026-09-09).
//
// AC-10: nothing imports this module yet. inc2 is its first consumer --
// importing it here, at module-load time, is itself the "validate at
// import time, never a silent partial load" behaviour AC-4 asks for; no
// engine/UI code calls scaleLadderOf/ladderAt through it in inc1.
import rawScaleLadder from '../../../data/traffic/scale_ladder.json' with { type: 'json' };
import { loadScaleLadder, type ScaleLadder } from './scaleLadder.ts';

/** The real scale ladder, validated once at module-load time (AC-4). Throws
 * (module-load failure) if data/traffic/scale_ladder.json is missing,
 * malformed, or fails validation -- never a silent partial load. */
export const scaleLadder: ScaleLadder = loadScaleLadder(rawScaleLadder);
