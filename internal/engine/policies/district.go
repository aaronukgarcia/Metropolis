package policies

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DistrictInfo is the district query surface (AC-8/AC-12): the district's
// ID, its queryable Name, and its cell set (ASM-285's cell-tagged model).
type DistrictInfo struct {
	ID    DistrictID
	Name  string
	Cells []CellRef
}

// CreateDistrict creates a named district from a set of cell references
// (AC-8, ASM-285 — cell-tagged, never vector polygons) and returns its
// assigned DistrictID. The ID is assigned deterministically by this package
// (a monotonic counter) so renaming can never change scoping (AC-8).
// Cells are sorted and de-duplicated on entry so a district's cell set is
// a deterministic, canonical value (GR#21). A district with no cells is
// rejected rather than silently stored empty (a scope that resolves to
// nothing would be a resolved-to-empty-set false success, AC-13).
func (a *PoliciesAPI) CreateDistrict(name string, cells []CellRef) (DistrictID, error) {
	if err := a.checkNotCopied("CreateDistrict"); err != nil {
		return "", err
	}
	if name == "" {
		return "", errs.New(ErrEmptyDistrictName, a.correlationID, nil)
	}
	if len(cells) == 0 {
		return "", errs.New(ErrEmptyDistrictCells, a.correlationID, nil)
	}
	canonical := dedupeCells(cells)

	a.mu.Lock()
	defer a.mu.Unlock()
	id := DistrictID(districtIDPrefix + itoaU64(a.nextDistrictID))
	a.nextDistrictID++
	a.districts[id] = &district{name: name, cells: canonical}
	return id, nil
}

// RenameDistrict renames a district, preserving its DistrictID and its
// existing policy/tax scoping (AC-8: renaming never re-keys scoping).
func (a *PoliciesAPI) RenameDistrict(id DistrictID, name string) error {
	if err := a.checkNotCopied("RenameDistrict"); err != nil {
		return err
	}
	if name == "" {
		return errs.New(ErrEmptyRenameName, a.correlationID, map[string]any{"district": string(id)})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d, ok := a.districts[id]
	if !ok {
		return errs.New(ErrUnknownDistrict, a.correlationID, map[string]any{"district": string(id)})
	}
	d.name = name
	return nil
}

// District returns one district's query info (AC-12).
func (a *PoliciesAPI) District(id DistrictID) (DistrictInfo, error) {
	if err := a.checkNotCopied("District"); err != nil {
		return DistrictInfo{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	d, ok := a.districts[id]
	if !ok {
		return DistrictInfo{}, errs.New(ErrUnknownDistrict, a.correlationID, map[string]any{"district": string(id)})
	}
	return DistrictInfo{ID: id, Name: d.name, Cells: copyCells(d.cells)}, nil
}

// Districts lists every district in sorted ID order (GR#21).
func (a *PoliciesAPI) Districts() []DistrictInfo {
	if err := a.checkNotCopied("Districts"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := a.sortedDistrictIDsLocked()
	out := make([]DistrictInfo, 0, len(ids))
	for _, id := range ids {
		d := a.districts[id]
		out = append(out, DistrictInfo{ID: id, Name: d.name, Cells: copyCells(d.cells)})
	}
	return out
}

// RegisterRoad registers a named road's edge set as a scope reference
// (AC-9). Edges are sorted and de-duplicated on entry for deterministic
// resolution. A duplicate RoadID is rejected, never silently overwritten
// (matching every Register-shaped surface in this codebase).
func (a *PoliciesAPI) RegisterRoad(id RoadID, edges []EdgeRef) error {
	if err := a.checkNotCopied("RegisterRoad"); err != nil {
		return err
	}
	if id == "" {
		return errs.New(ErrEmptyRoadID, a.correlationID, nil)
	}
	if len(edges) == 0 {
		return errs.New(ErrEmptyRoadEdges, a.correlationID, map[string]any{"road": string(id)})
	}
	canonical := dedupeEdges(edges)

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.roads[id]; ok {
		return errs.New(ErrRoadAlreadyRegistered, a.correlationID, map[string]any{"road": string(id)})
	}
	a.roads[id] = roadDef{edges: canonical}
	return nil
}

const districtIDPrefix = "district-"

func itoaU64(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// cellRefLess orders cell references by tile X, tile Y, row, column.
func cellRefLess(x, y CellRef) bool {
	if x.Tile.X != y.Tile.X {
		return x.Tile.X < y.Tile.X
	}
	if x.Tile.Y != y.Tile.Y {
		return x.Tile.Y < y.Tile.Y
	}
	if x.Local.Row != y.Local.Row {
		return x.Local.Row < y.Local.Row
	}
	return x.Local.Col < y.Local.Col
}

// edgeRefLess orders edges by their from-cell then to-cell.
func edgeRefLess(x, y EdgeRef) bool {
	if cellRefLess(x.From, y.From) {
		return true
	}
	if cellRefLess(y.From, x.From) {
		return false
	}
	return cellRefLess(x.To, y.To)
}

// dedupeCells returns a sorted, de-duplicated copy of cells.
func dedupeCells(cells []CellRef) []CellRef {
	sorted := copyCells(cells)
	sort.Slice(sorted, func(i, j int) bool { return cellRefLess(sorted[i], sorted[j]) })
	return compactCells(sorted)
}

// compactCells removes adjacent duplicates from a sorted slice.
func compactCells(sorted []CellRef) []CellRef {
	if len(sorted) == 0 {
		return sorted
	}
	out := sorted[:1]
	for _, c := range sorted[1:] {
		if cellRefEqual(out[len(out)-1], c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// cellRefEqual reports reference equality (coordinate identity).
func cellRefEqual(x, y CellRef) bool {
	return x.Tile == y.Tile && x.Local == y.Local
}

// copyCells returns a fresh copy of cells (never an alias of the stored
// backing array).
func copyCells(cells []CellRef) []CellRef {
	out := make([]CellRef, len(cells))
	copy(out, cells)
	return out
}

// dedupeEdges returns a sorted, de-duplicated copy of edges.
func dedupeEdges(edges []EdgeRef) []EdgeRef {
	sorted := make([]EdgeRef, len(edges))
	copy(sorted, edges)
	sort.Slice(sorted, func(i, j int) bool { return edgeRefLess(sorted[i], sorted[j]) })
	if len(sorted) == 0 {
		return sorted
	}
	out := sorted[:1]
	for _, e := range sorted[1:] {
		if edgeRefEqual(out[len(out)-1], e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// edgeRefEqual reports edge reference equality.
func edgeRefEqual(x, y EdgeRef) bool {
	return cellRefEqual(x.From, y.From) && cellRefEqual(x.To, y.To)
}

// copyEdges returns a fresh copy of edges.
func copyEdges(edges []EdgeRef) []EdgeRef {
	out := make([]EdgeRef, len(edges))
	copy(out, edges)
	return out
}
