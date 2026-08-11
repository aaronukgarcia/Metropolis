package world

// This file computes the real (not placeholder) land/sea split of the
// expansion extent programmatically against a coastline model (AC-12,
// BOW comment obligation 3 on MOD-017) — data/georef.json's own notes
// flag its "approximately 24-28 of 36" figure as "a geography-knowledge
// estimate... NOT coastline-verified" and asks the terrain importer to
// replace it with a real computation. classifyLandSea/ComputeLandSea36
// are that computation.
//
// # Data source honesty (ASM — see dispatch report)
//
// This package has no network access to a real Ordnance Survey / OSM
// coastline shapefile, so coastlineBoundary below is a HAND-AUTHORED
// piecewise-linear approximation of the real Kent coast, built from
// data/georef.json's own landmark eastings/northings (Dungeness spit,
// Dover, Deal/Sandwich/Thanet's eastward bulge, the Folkestone start
// tile's shoreline) rather than a surveyed polygon. It is a genuine
// programmatic computation — not the "approximately 24-28" placeholder
// string — and is deterministic and reviewable, but it is NOT
// survey-accurate; a real coastline dataset should replace
// coastlineBoundary's control points when one is downloaded. Flagged for
// Bill/Aaron per the acceptance doc's own Escalations section.

// coastlinePoint is one (easting, northing) control point on the
// hand-authored approximate coastline boundary curve.
type coastlinePoint struct {
	easting, northing float64
}

// southBoundary approximates the coastline's northing as a function of
// easting across the expansion extent (590000-650000 E): everything
// south of this curve is sea. Control points follow georef.json's own
// landmark notes: the Dungeness spit dips the coast south around
// E=600000-615000; the Folkestone tile anchors the coast at
// N~134000-135000 around E=620000-622000; the coast rises northward
// past Dover/Deal toward Sandwich/Thanet in the box's north-east.
var southBoundary = []coastlinePoint{
	{590000, 128000},
	{600000, 117000}, // Dungeness spit (real landmark: 609000,117000)
	{609000, 116000},
	{618000, 128000},
	{620000, 134500}, // Folkestone start-tile shoreline
	{631500, 138000}, // Dover
	{637000, 150000}, // Deal/Sandwich, coast curling north
	{645000, 163000}, // toward Thanet
	{650000, 168000},
}

// eastBoundary approximates the eastward extent of land as a function of
// northing: beyond this easting is open Channel/North Sea, EXCEPT the
// Deal/Sandwich/Thanet bulge in the box's north (northing > 150000),
// where land pushes further east, matching georef-notes.md's "east edge
// sits well offshore past Dover... with Deal/Sandwich/Ramsgate land
// clipped by the eastern portion of the box".
func eastBoundary(northing float64) float64 {
	switch {
	case northing < 145000:
		return 634000 // just past Dover's real coastline
	case northing < 155000:
		return 640000
	default:
		return 649000 // Thanet bulge, still inside the 650000 box edge
	}
}

func interpSouthBoundary(easting float64) float64 {
	if easting <= southBoundary[0].easting {
		return southBoundary[0].northing
	}
	last := len(southBoundary) - 1
	if easting >= southBoundary[last].easting {
		return southBoundary[last].northing
	}
	for i := 0; i < last; i++ {
		a, b := southBoundary[i], southBoundary[i+1]
		if easting >= a.easting && easting <= b.easting {
			t := (easting - a.easting) / (b.easting - a.easting)
			return a.northing + t*(b.northing-a.northing)
		}
	}
	return southBoundary[last].northing
}

// isLand reports whether a real-world (easting, northing) position is
// land under the approximate coastline model above.
func isLand(easting, northing float64) bool {
	if northing < interpSouthBoundary(easting) {
		return false
	}
	if easting > eastBoundary(northing) {
		return false
	}
	return true
}

// tileEastingNorthing converts a TileCoord to its centre's real-world
// (easting, northing), given the expansion box's SW corner
// (data/georef.json: 590000, 110000).
func tileEastingNorthing(c TileCoord) (float64, float64) {
	e := 590000.0 + (float64(c.X)+0.5)*TileSizeM
	n := 110000.0 + (float64(c.Y)+0.5)*TileSizeM
	return e, n
}

// classifyLandSea reports whether tile c's centre falls on land under
// the coastline model — used to seed synthetic terrain (synth_terrain.go)
// and per-tile purchase pricing (tile_price.go).
func classifyLandSea(c TileCoord) bool {
	e, n := tileEastingNorthing(c)
	return isLand(e, n)
}

// LandSeaSplit36 is the result of ComputeLandSea36: the real (computed,
// not placeholder) on-land count among the 36 10km OS grid squares
// data/georef.json's expansion.tiles10k lists.
type LandSeaSplit36 struct {
	OnLand int
	Total  int
}

// ComputeLandSea36 computes the on-land count of the 36 10km OS squares
// covering the expansion extent (AC-12), by sampling each square's 25
// constituent 2km sub-tile centres (10km / 2km = 5 per side) against the
// coastline model and calling the square "on land" if a majority of its
// samples are land. This replaces georef.json's "approximately 24-28 of
// 36" placeholder with a concrete, reproducible number.
func ComputeLandSea36() LandSeaSplit36 {
	const subTilesPerSide = 5                             // 10km / 2km
	const squaresPerSide = TilesPerSide / subTilesPerSide // 30/5 = 6

	result := LandSeaSplit36{Total: squaresPerSide * squaresPerSide}
	for sy := 0; sy < squaresPerSide; sy++ {
		for sx := 0; sx < squaresPerSide; sx++ {
			landSamples := 0
			totalSamples := 0
			for dy := 0; dy < subTilesPerSide; dy++ {
				for dx := 0; dx < subTilesPerSide; dx++ {
					c := TileCoord{X: sx*subTilesPerSide + dx, Y: sy*subTilesPerSide + dy}
					totalSamples++
					if classifyLandSea(c) {
						landSamples++
					}
				}
			}
			if landSamples*2 >= totalSamples { // majority-land rule
				result.OnLand++
			}
		}
	}
	return result
}
