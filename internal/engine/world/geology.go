package world

// This file is the §32 geology layer: chalk everywhere as a baseline,
// plus one secondary "pocket" per tile — clay, sand/gravel, or (in the
// real Betteshanger/Snowdown coalfield's eastern tiles only) deep coal.
// Modelled per-tile (2km, coarser than the 10m cell) rather than
// per-cell — AC-6 explicitly allows either grain "if geology is
// coarser-grained than the 10m cell, documented either way"; per-tile
// keeps the ~30-byte cell core untouched (AC-4) and matches how a real
// geological survey would actually be granular (formations don't
// meaningfully vary cell-to-cell at 10m).
//
// AC-7's prospecting gate: GeologyKind (the pocket) is not queryable for
// an unprospected tile — Geology()'s Pocket field reads GeologyUnknown
// and PocketGeology returns ErrGeologyNotProspected until Prospect(tile)
// has run. Chalk baseline is always visible (§32: "chalk everywhere" is
// common knowledge, not a prospecting reveal).

// GeologyKind is one formation in the §32 geology layer.
type GeologyKind uint8

const (
	GeologyChalk GeologyKind = iota
	GeologyClay
	GeologyGravel
	GeologyDeepCoal
	GeologyNone    // baseline chalk only, no secondary pocket in this tile
	GeologyUnknown // pocket exists but this tile has not been prospected
)

func (g GeologyKind) String() string {
	switch g {
	case GeologyChalk:
		return "chalk"
	case GeologyClay:
		return "clay"
	case GeologyGravel:
		return "gravel"
	case GeologyDeepCoal:
		return "deep_coal"
	case GeologyNone:
		return "none"
	case GeologyUnknown:
		return "unknown"
	default:
		return "unrecognised"
	}
}

// geologyRegion is the per-tile geology state: the always-present chalk
// baseline plus the secondary pocket revealed by prospecting.
type geologyRegion struct {
	baseline GeologyKind // always GeologyChalk today, per §32
	pocket   GeologyKind // GeologyClay/Gravel/DeepCoal/None — the real value
}

// eastCoalfieldMinX is the first TileCoord.X column real §32 "East Kent
// deep coal seams" (the real Betteshanger/Snowdown coalfield) may
// appear in — the eastern third of the 30-wide expansion grid, roughly
// easting 630000+ (data/georef.json expansion.swEasting=590000, 2km
// tiles: (630000-590000)/2000 = 20).
const eastCoalfieldMinX = 20

// deriveGeology assigns tile c's baseline+pocket geology deterministically
// from its coordinate — no randomness, no wall clock (AC-16, AC-18): the
// same TileCoord always yields the same geology.
func deriveGeology(c TileCoord) geologyRegion {
	h := hashCoord(c.X, c.Y, 0xE0106)
	pocket := GeologyNone
	switch {
	case c.X >= eastCoalfieldMinX && h%5 == 0:
		pocket = GeologyDeepCoal
	case h%7 == 0:
		pocket = GeologyClay
	case h%11 == 0:
		pocket = GeologyGravel
	}
	return geologyRegion{baseline: GeologyChalk, pocket: pocket}
}

// hashCoord is a small deterministic integer mixing hash (splitmix64-
// style) used for build-time procedural placement decisions in this
// package (geology pockets, synthetic terrain — see terrain_import.go).
// It is NOT gameplay RNG — anything on the simulated tick path uses
// foundation/det's seeded Stream instead; this is purely a pure
// function of (x, y, salt) for one-off, deterministic-by-construction
// build-time content generation.
func hashCoord(x, y int, salt uint64) uint64 {
	v := uint64(uint32(x))<<32 ^ uint64(uint32(y)) ^ salt
	v ^= v >> 33
	v *= 0xff51afd7ed558ccd
	v ^= v >> 33
	v *= 0xc4ceb9fe1a85ec53
	v ^= v >> 33
	return v
}
