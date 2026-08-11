// Package world is engine.world (MOD-017): the build-time OS Terrain 50
// import, per-cell/tile state, slope/geology/hydrology derivation, and
// the §2.3 2x2km tile-purchase expansion economy over the ~60x60km Kent
// extent. It is the "real ground" every other engine module (build,
// roads, traffic, mining, farming, finance, logistics, parking,
// dispatch, spiral, disasters, and ui.screen.map) sits on, consumed
// exclusively through *WorldAPI (worldapi.go) per GR#20 — no other
// package may reach into this one's internal grid.go storage directly.
//
// Module key: engine.world (see code.json; inbound GUID
// 2d8855b8-f4f0-43a3-a179-67accca83115, outbound GUID
// d9ef1d5a-9436-4f25-a1db-a426d407efc1)
// Spec ref: §2 The World (all subsections: §2.1 start tile, §2.2
//
//	off-map connections, §2.3 expansion, §2.4 cells); §32 Mining,
//	Extraction & the Blight Model (the geology layer this package
//	exposes; extraction mechanics themselves are engine.mining's,
//	out of scope here); II.1 (Part II summary); data/georef.json +
//	docs/planning/georef-notes.md (FEAT-009's frozen anchor pin).
//
// # BOW-comment obligations this item closes (Bill, 2026-08-08)
//
// Four obligations were inherited from FEAT-009's closure onto this
// item's BOW comment; here is what each one got, and what did NOT get
// closed (read the cited files for the honest detail — nothing below is
// overstated):
//
//  1. §2.1 artistic-compression mapping (Option (a), Aaron's design
//     call) — IMPLEMENTED: compression.go's compressV, exercised by
//     terrain_import.go's ImportTerrain. Real elevations are sampled
//     verbatim; only the north-south sample position is non-linearly
//     warped. See compression_test.go.
//  2. OSTN15 anchor re-verification against downloaded Terrain 50 data
//     — PARTIALLY DONE. georef_verify.go's VerifyGeorefAnchors runs the
//     internal-consistency check this package CAN run without network
//     access or an OSTN15 transform library (do the documented anchors
//     fall inside the documented tile bounds?), and it CONFIRMS the
//     exact J13 risk georef.json's own openQuestions already flagged
//     (621100,137500 sits past the tile's 137000 north edge). The real
//     geodetic re-verification against an actually-downloaded tile could
//     not be completed — this package has no network access — and is
//     escalated to Bill/Aaron per the acceptance doc's own Escalations
//     section, not silently resolved. See georef_verify.go's doc comment.
//  3. Real land/sea split of the 36 expansion tiles — IMPLEMENTED as a
//     genuine programmatic computation (coastline.go's ComputeLandSea36),
//     replacing georef.json's "approximately 24-28" placeholder with a
//     concrete number. The coastline MODEL it computes against is a
//     hand-authored approximation (no real coastline dataset was
//     available to download) — see coastline.go's doc comment for the
//     honesty note and what a follow-up pass should replace.
//  4. OGL attribution wording confirmed against the actual downloaded
//     licence file — NOT DONE. No licence file was available to verify
//     against; data/georef.json's attribution field still carries its
//     placeholder [YEAR]. This also requires editing data/georef.json,
//     which is outside this item's owned path (internal/engine/world/)
//     — escalated to Bill rather than edited directly.
//
// # Storage model and memory budget (M0-ENG §1.2/§1.3, AC-19)
//
// grid.go stores every tile's per-cell state as struct-of-arrays, not an
// array of Cell structs — Cell (types.go) is assembled on demand only as
// an API return value. Terrain (elevation/slope/surface, 6 bytes/cell) is
// generated for a tile the first time it is queried, for every tile in
// the 900-tile/36M-cell extent; the full-simulation fields (ownership/
// zoning/structureRef/landValue/overlay scratch, 17 bytes/cell) are only
// allocated once a tile is purchased (AC-10's "not fully simulated"
// requirement, enforced structurally: an unowned tile's simGrid pointer
// is nil, so there is nothing for a mutation command to write into, and
// ApplyOwnershipCommand rejects rather than allocating on demand). See
// memory_test.go for the measured total against the full 900-tile,
// fully-owned worst case, and the Sprint-3-time caveat this doc.go's
// caller (the dispatch report) restates from the acceptance doc's own
// "For Bill" escalation: this package's own footprint is proven under
// budget, but the full 20GB-envelope proof is a Sprint-3-exit-gate
// composite claim together with engine.citizens, not this item alone.
//
// # Determinism (AC-16, AC-18)
//
// Every derivation in this package — compression (compression.go),
// slope (slope.go), geology (geology.go), hydrology (hydrology.go),
// synthetic placeholder terrain (synth_terrain.go), tile pricing
// (tile_price.go), coastline classification (coastline.go) — is a pure
// function of its inputs (heightmap, TileCoord, or already-committed
// World state read under lock). None of this package's non-test files
// import the standard library's time package or call its wall-clock-now
// / elapsed-since functions (AC-18; verified by grepping this package's
// non-test files for the standard library time package's clock-reading
// calls, which returns no matches). geology.go's hashCoord
// and synth_terrain.go's noiseCorner are deterministic integer-mixing
// hashes, not math/rand and not foundation/det's Stream (which is keyed
// gameplay RNG for the tick path — this package's placeholder-terrain
// generation is a one-off, build-time content decision, not simulated
// randomness, so it deliberately does not consume a det.Stream draw).
//
// # Route cache (AC-17)
//
// This package implements no route cache. The route-cache-contents-
// independence requirement (M0-ENG §1.3) applies to whichever package
// ends up owning it — engine.roads or engine.traffic, both later-sprint
// consumers of this package's terrain/cell data, not built yet. N/A here.
//
// # Out of scope (see the acceptance doc's "Out of scope" section)
//
// Every consumer module listed in code.json (engine.build, engine.roads,
// engine.traffic, engine.mining, engine.farming, engine.finance,
// engine.logistics, engine.parking, engine.dispatch, engine.spiral,
// feat.disasters, ui.screen.map) — this item only provides the
// query/mutation surface those modules build against. Real mining
// mechanics, real-time rendering, and GPU-accelerated import are all
// explicitly out of scope per that section and not attempted here.
package world
