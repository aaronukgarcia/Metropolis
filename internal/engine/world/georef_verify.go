package world

// This file is the AC-13 anchor re-verification pass for MOD-017's
// inherited FEAT-009 obligation (BOW comment obligation 2 on MOD-017):
// "OSTN15-verify all georef.json anchor coordinates against downloaded
// OS Terrain 50 data (J13 position was estimated ±200m by ad-hoc
// interpolation)".
//
// # What this package CAN and CANNOT verify (read before trusting the
// result — this is a partial pass, not the full obligation closed)
//
// This build has no network access to download the real OS Terrain 50
// TR23 tile, no OSTN15/Helmert transform library, and no OS Open
// Roads/Names dataset — the three things a genuine anchor re-verification
// needs. What GeorefVerificationReport below DOES check, using only data
// already in data/georef.json (which this package may read but, per its
// dispatch brief, may NOT edit — internal/engine/world/ is the only path
// this item owns):
//
//  1. Internal consistency: does each documented anchor's own coordinate
//     fall inside the documented start-tile bounds it is claimed to sit
//     in? This is arithmetic on numbers already in the file, not a
//     geodetic re-verification, but it DOES surface the exact discrepancy
//     georef.json's own openQuestions already flags for J13 — see below.
//  2. Structural extent checks against a REAL downloaded/imported source
//     grid, via AnchorExtentCheck (terrain_import.go) — this is the real
//     OSTN15-class check, but it only runs when ImportTerrain is given
//     real OS Terrain 50 bytes, which this build never has (see
//     terrain_import.go's doc comment on the fixture-only test strategy).
//
// # Result for J13 (escalated, not silently resolved)
//
// Per the check above: georef.json's own documented J13 position
// (621100, 137500 ± 200m) sits AT/PAST the documented start tile's north
// edge (maxN: 137000) — 137500 > 137000, by more than the tile's own
// stated uncertainty band. This CONFIRMS the exact risk georef.json's
// openQuestions and this item's BOW comment already flagged; it does
// NOT resolve whether J13 is REALLY inside a genuinely re-surveyed tile,
// because that needs the real OSTN15 transform this package cannot run.
// Per the acceptance doc's own Escalations section ("this is Aaron's
// design call... not something this BA or the junior building this item
// should silently resolve"), this result is escalated to Bill/Aaron in
// the dispatch report, not silently patched into georef.json (which is
// outside this item's owned path in any case).
type GeorefVerificationReport struct {
	AnchorName        string
	Easting, Northing float64
	UncertaintyM      float64
	InsideStartTile   bool
	Note              string
}

// startTileBounds mirrors data/georef.json's startTile.bounds
// (620000-622000 E, 135000-137000 N) — duplicated here as a small,
// clearly-sourced constant rather than parsed from the JSON at runtime,
// since this package's own tests need it and the file is small/frozen
// (designDecision.date 2026-08-08). If georef.json's bounds ever change,
// this constant and the anchor table below must be updated together —
// codejson-audit / the next /update pass is the intended place to catch
// drift, same as any other frozen-spec mirror in this codebase.
var startTileBounds = struct{ minE, maxE, minN, maxN float64 }{620000, 622000, 135000, 137000}

// documentedAnchors mirrors the anchors in data/georef.json's
// startTile.landmarksConsidered that carry an explicit easting/northing
// (Sandgate/Seabrook are informational-only place-name anchors without a
// single point in that file and are excluded here).
var documentedAnchors = []GeorefVerificationReport{
	{AnchorName: "Sandgate seafront / promenade", Easting: 620500, Northing: 135140, UncertaintyM: 0},
	{AnchorName: "Seabrook", Easting: 618595, Northing: 134983, UncertaintyM: 0},
	{AnchorName: "Folkestone West railway station", Easting: 620900, Northing: 136400, UncertaintyM: 100},
	{AnchorName: "M20 Junction 13 / Castle Hill Interchange", Easting: 621100, Northing: 137500, UncertaintyM: 200},
	{AnchorName: "Cheriton Hill (North Downs escarpment)", Easting: 619700, Northing: 139600, UncertaintyM: 0},
}

// VerifyGeorefAnchors runs the internal-consistency check described in
// this file's doc comment against every documented anchor and returns
// one report row per anchor.
func VerifyGeorefAnchors() []GeorefVerificationReport {
	out := make([]GeorefVerificationReport, 0, len(documentedAnchors))
	for _, a := range documentedAnchors {
		inside := a.Easting >= startTileBounds.minE && a.Easting <= startTileBounds.maxE &&
			a.Northing >= startTileBounds.minN && a.Northing <= startTileBounds.maxN
		note := "inside documented start-tile bounds"
		if !inside {
			note = "OUTSIDE documented start-tile bounds — matches georef.json's own openQuestions flag for this anchor; escalate, do not silently shift"
		}
		out = append(out, GeorefVerificationReport{
			AnchorName: a.AnchorName, Easting: a.Easting, Northing: a.Northing,
			UncertaintyM: a.UncertaintyM, InsideStartTile: inside, Note: note,
		})
	}
	return out
}
