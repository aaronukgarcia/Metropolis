package world

// This file is §2.2's off-map connection anchors (AC-9): the motorway's
// edge turnaround loops, the purchasable Sellindge grid-power connection,
// and the dormant sea/port connection — modelled as world-owned entry
// points that later network modules (engine.roads, a future power
// network, engine.logistics) attach to via WorldAPI, per GR#20 (this
// package only anchors WHERE they are and their static facts; it does
// not simulate traffic/power/freight itself — see the acceptance doc's
// "Out of scope").

// OffMapKind names one of the three §2.2 off-map connection types.
type OffMapKind uint8

const (
	OffMapMotorway OffMapKind = iota
	OffMapPower
	OffMapSea
)

// OffMapConnection is one anchor point where the simulated world meets
// "the world" outside it (§2.2).
type OffMapConnection struct {
	Kind OffMapKind
	// Location is the local start-tile cell this connection anchors at.
	Location CellLocal
	// Name is a human-readable label for UI/debug display.
	Name string

	// Power-specific fields (zero for non-power connections).
	PowerTrancheCapacityMW      float64 // capacity added per purchased tranche
	PowerStandingChargePerMonth float64
	PowerImportPricePerMWh      float64

	// Dormant reports whether this connection is inactive until a later
	// milestone unlocks it (§2.2: sea/port is "dormant until the port
	// milestone").
	Dormant bool
}

// OffMapConnections returns the fixed §2.2 anchor set for the start
// tile: two motorway turnaround loops (east/west edges of the M20
// corridor identified by DeriveMotorwayCorridor), one purchasable
// Sellindge grid-power connection, and one dormant sea/port connection
// on the shoreline. Static, deterministic — a pure function of the
// imported heightmap (AC-16, AC-18).
func OffMapConnections(heights [][]float32) []OffMapConnection {
	if len(heights) == 0 {
		return nil
	}
	corridorRow, junction, ok := DeriveMotorwayCorridor(heights)
	conns := make([]OffMapConnection, 0, 4)
	if ok {
		conns = append(conns,
			OffMapConnection{
				Kind:     OffMapMotorway,
				Location: CellLocal{Row: corridorRow, Col: 0},
				Name:     "M20 west turnaround (traffic to/from 'the world')",
			},
			OffMapConnection{
				Kind:     OffMapMotorway,
				Location: CellLocal{Row: corridorRow, Col: TileSizeCells - 1},
				Name:     "M20 east turnaround (traffic to/from 'the world')",
			},
			OffMapConnection{
				Kind:     OffMapMotorway,
				Location: junction,
				Name:     "M20 Junction 13 / Castle Hill Interchange (grade-separated)",
			},
		)
	}

	// Sellindge converter station: real §2.2 anchor, referenced by name
	// (data/georef.json / GDD §2.2). Sited near the tile's north-east
	// corner, matching Sellindge's real position broadly north-east of
	// Folkestone.
	conns = append(conns, OffMapConnection{
		Kind:                        OffMapPower,
		Location:                    CellLocal{Row: 5, Col: TileSizeCells - 5},
		Name:                        "Sellindge grid converter station connection",
		PowerTrancheCapacityMW:      50,
		PowerStandingChargePerMonth: 25000,
		PowerImportPricePerMWh:      85,
	})

	// Dormant sea/port connection: shoreline, south edge (§2.1's south
	// band, §2.2: "dormant until the port milestone").
	conns = append(conns, OffMapConnection{
		Kind:     OffMapSea,
		Location: CellLocal{Row: TileSizeCells - 1, Col: TileSizeCells / 2},
		Name:     "Sea / future port connection",
		Dormant:  true,
	})

	return conns
}

// DeriveMotorwayCorridor heuristically locates the §2.1 "upper third...
// M20/A20 corridor at ~50-60m elevation, running roughly E-W, with one
// grade-separated junction in-tile" band from the imported heightmap: it
// scans the tile's upper third of rows for the row whose cells have the
// lowest elevation VARIANCE (an E-W-running band of roughly consistent
// elevation reads as "corridor-like" against surrounding relief), then
// picks that row's flattest cell as the junction.
//
// # Scope note (ASM — see dispatch report)
//
// This is a heuristic derived from elevation alone — this package has no
// real OS Open Roads vector data for the M20's actual centreline, only
// OS Terrain 50's heightmap (data/georef.json's source.formats). A real
// road-graph import (once OS Open Roads data is available) should
// replace this heuristic with the M20's actual surveyed geometry;
// documented here rather than silently presented as authoritative.
func DeriveMotorwayCorridor(heights [][]float32) (row int, junction CellLocal, ok bool) {
	n := len(heights)
	if n == 0 {
		return 0, CellLocal{}, false
	}
	upperThird := n / 3
	bestRow := -1
	bestVariance := -1.0
	for r := 0; r < upperThird; r++ {
		mean := 0.0
		for _, h := range heights[r] {
			mean += float64(h)
		}
		mean /= float64(len(heights[r]))
		variance := 0.0
		for _, h := range heights[r] {
			d := float64(h) - mean
			variance += d * d
		}
		variance /= float64(len(heights[r]))
		if bestRow == -1 || variance < bestVariance {
			bestRow = r
			bestVariance = variance
		}
	}
	if bestRow == -1 {
		return 0, CellLocal{}, false
	}
	// Junction: the flattest local cell along the corridor row (lowest
	// local grade), a reasonable stand-in for "the one grade-separated
	// junction" until real road-graph data is available.
	bestCol := 0
	bestGrade := -1.0
	for c := range heights[bestRow] {
		g := localGradePercent(heights, bestRow, c)
		if bestGrade < 0 || g < bestGrade {
			bestGrade = g
			bestCol = c
		}
	}
	return bestRow, CellLocal{Row: bestRow, Col: bestCol}, true
}
