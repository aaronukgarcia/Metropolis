package dash

// Gap is one drill-through dead end found by AuditDrillCoverage: a
// specific element of a Layout that carries a displayed value but has no
// resolvable DrillTarget.
type Gap struct {
	// TileID is the owning tile's ID.
	TileID string
	// Kind is the owning tile's type.
	Kind TileKind
	// ElementID identifies the specific element within an aggregate tile:
	// "row:N" for a table row, "hit:N" for a diagram hit-test entry, or
	// "" when the gap is the tile itself (a scalar tile whose own target
	// is missing).
	ElementID string
}

// AuditDrillCoverage walks every element of l that carries a displayed
// value and returns one Gap per element with no resolvable DrillTarget.
//
// It is exhaustive by construction, not a sample (AC-5): it enumerates
// the closed set of drillable element kinds —
//
//   - the tile itself, for every tile (scalar tiles carry their value on
//     the tile; table/diagram tiles carry a whole-view target on the
//     tile and per-element targets below);
//   - every row of every table tile (not just the table tile as one
//     unit);
//   - every hit-test entry of every embedded diagram tile (per
//     ui.diagrams' AC-5 hit-test source mapping).
//
// Because AC-4 makes a zero DrillTarget unconstructible through the
// public API, a Gap on scalar/table/diagram elements is expected to be
// structurally impossible in a layout built through the constructors —
// this function's job is to prove that, and to catch anything that slips
// in through a hand-edited/corrupt profile or a future bug, without
// silently skipping a tile type. Future screens (F2/F4/F8) call this
// over their own shipped layouts instead of writing bespoke checks.
//
// Walk order is slice order — deterministic (AC-12); no map iteration
// feeds the result.
func AuditDrillCoverage(l Layout) []Gap {
	var gaps []Gap
	for _, t := range l.tiles {
		if !t.drill.Valid() {
			gaps = append(gaps, Gap{TileID: t.id, Kind: t.kind})
		}
		switch t.kind {
		case KindTable:
			if t.table != nil {
				for i := range t.table.Rows {
					if !t.table.Rows[i].Drill.Valid() {
						gaps = append(gaps, Gap{TileID: t.id, Kind: t.kind, ElementID: elementID(t.kind, i)})
					}
				}
			}
		case KindDiagram:
			if t.diagram != nil {
				for i := range t.diagram.Hits {
					if !t.diagram.Hits[i].Drill.Valid() {
						gaps = append(gaps, Gap{TileID: t.id, Kind: t.kind, ElementID: elementID(t.kind, i)})
					}
				}
			}
		}
	}
	return gaps
}
