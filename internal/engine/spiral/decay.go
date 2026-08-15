package spiral

import (
	"sort"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file implements the AC-3 decay effects (three independent,
// independently-queryable consequences of abandonment) and AC-4's
// cell-by-cell blight spread over a documented, deterministic frontier.

// LandValueDrag returns the micro-pounds of neighbour land-value drag a
// decayed cell of the given severity imposes on each adjacent cell (AC-3's
// first effect, §12 "drag neighbouring land value"). It is a pure, linear
// function of severity with the data-sourced per-severity coefficient, and
// is deliberately independent of the hazard and demolition-cost effects —
// raising severity moves this figure and nothing else.
func (d *DecayAPI) LandValueDrag(severity int) int64 {
	if err := d.checkNotCopied("LandValueDrag"); err != nil {
		return 0
	}
	s := clampSeverity(severity)
	v, _ := num.SafeMul(int64(s), d.cfg.Decay.LandValueDragPerSeverityMicropounds)
	return v
}

// HazardPressure returns the 0..100 hazard/fire/crime pressure a decayed
// cell of the given severity produces (AC-3's second effect, §12 "small
// ongoing hazard/fire/crime pressure") — the signal engine.crime consumes.
// Capped at 100 (a pressure is a [0,100] scale, matching the pushed-term
// convention); independent of the drag and demolition-cost effects.
func (d *DecayAPI) HazardPressure(severity int) int {
	if err := d.checkNotCopied("HazardPressure"); err != nil {
		return 0
	}
	s := clampSeverity(severity)
	p := s * d.cfg.Decay.HazardPressurePerSeverity
	if p > 100 {
		return 100
	}
	return p
}

// DemolitionCost returns the micro-pounds cost to demolish a decayed cell
// of the given severity and age (AC-3's third effect, §12 "cost money to
// demolish"). It grows with BOTH severity and age — a longer-abandoned,
// more-severely-decayed cell is dearer to clear — via two independent
// data-sourced rates, and is independent of the drag and hazard effects.
func (d *DecayAPI) DemolitionCost(severity int, age int64) int64 {
	if err := d.checkNotCopied("DemolitionCost"); err != nil {
		return 0
	}
	s := clampSeverity(severity)
	if age < 0 {
		age = 0
	}
	sevCost, _ := num.SafeMul(int64(s), d.cfg.Decay.DemolitionCostPerSeverityMicropounds)
	base := num.SatAdd(d.cfg.Decay.DemolitionCostBaseMicropounds, sevCost)
	monthly, _ := num.SafeMul(age, d.cfg.Decay.DemolitionCostPerMonthMicropounds)
	return num.SatAdd(base, monthly)
}

// clampSeverity coerces a severity into a sane non-negative domain before
// any effect arithmetic touches it — a caller-supplied out-of-range value
// must not drive the cost/drag figures off their documented scale. It clamps
// to a generous fixed ceiling rather than the config's maxSeverity so the
// pure effect helpers are total functions even for a hypothetical
// severity-beyond-max input (GR#16: never let a bad input produce a
// wrapped/negative figure).
func clampSeverity(s int) int {
	if s < 0 {
		return 0
	}
	if s > 1_000_000 {
		return 1_000_000
	}
	return s
}

// decayState is the internal, mutable decay record (the exported DecayState
// is its snapshot).
type decayState struct {
	cell        CellRef
	abandonedAt int64
	age         int64
	severity    int
}

// snapshot builds the exported, read-only DecayState view, recomputing the
// three AC-3 effects from the current severity/age via the API's
// data-sourced rates.
func (d *DecayAPI) snapshot(st *decayState) DecayState {
	if err := d.checkNotCopied("snapshot"); err != nil {
		return DecayState{}
	}
	return DecayState{
		Cell:           st.cell,
		AbandonedAt:    st.abandonedAt,
		Age:            st.age,
		Severity:       st.severity,
		LandValueDrag:  d.LandValueDrag(st.severity),
		HazardPressure: d.HazardPressure(st.severity),
		DemolitionCost: d.DemolitionCost(st.severity, st.age),
	}
}

// neighbours returns c's four orthogonal neighbours (north, east, south,
// west) in a DOCUMENTED, deterministic order — sorted ascending by
// (tile.X, tile.Y, row, col), never Go map-iteration order. AC-4's
// frontier-selection order is exactly this sort: the lowest coordinate in
// this order is always the first-eligible neighbour, so a forced tie is
// broken identically every run (GR#21). Neighbours are cell-local within a
// tile (blight does not cross tile boundaries in v1 — a tile is the
// natural district container, and the local grid's edge cells have no
// cross-tile neighbour here).
func neighbours(c CellRef) []CellRef {
	row, col := c.Local.Row, c.Local.Col
	out := make([]CellRef, 0, 4)
	candidates := [4]CellRef{
		{Tile: c.Tile, Local: world.CellLocal{Row: row - 1, Col: col}}, // north
		{Tile: c.Tile, Local: world.CellLocal{Row: row, Col: col + 1}}, // east
		{Tile: c.Tile, Local: world.CellLocal{Row: row + 1, Col: col}}, // south
		{Tile: c.Tile, Local: world.CellLocal{Row: row, Col: col - 1}}, // west
	}
	for _, n := range candidates {
		if !n.Local.InBounds() {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return cellLess(out[i], out[j]) })
	return out
}

// cellLess is the total, deterministic order over CellRef used by the
// frontier selection (AC-4): tile X, then tile Y, then row, then col.
func cellLess(a, b CellRef) bool {
	if a.Tile.X != b.Tile.X {
		return a.Tile.X < b.Tile.X
	}
	if a.Tile.Y != b.Tile.Y {
		return a.Tile.Y < b.Tile.Y
	}
	if a.Local.Row != b.Local.Row {
		return a.Local.Row < b.Local.Row
	}
	return a.Local.Col < b.Local.Col
}

// sortedCellRefs returns refs in the deterministic cellLess order (never map
// iteration — GR#21).
func sortedCellRefs(refs []CellRef) []CellRef {
	out := append([]CellRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool { return cellLess(out[i], out[j]) })
	return out
}

// spreadFrontier returns every eligible blight target: a non-decayed,
// in-bounds neighbour of a decayed cell whose severity has reached the
// spread threshold. The result is deduplicated and sorted in cellLess order,
// so the first element is ALWAYS the next cell to blight regardless of how
// the source cells were inserted (AC-4's forced-tie determinism).
func (d *DecayAPI) spreadFrontier() []CellRef {
	if err := d.checkNotCopied("spreadFrontier"); err != nil {
		return nil
	}
	seen := make(map[cellKey]bool)
	var out []CellRef
	for k, st := range d.decay {
		if st.severity < d.cfg.Blight.SpreadSeverityThreshold {
			continue
		}
		for _, n := range neighbours(st.cell) {
			key := n.key()
			if seen[key] {
				continue
			}
			if _, alreadyDecayed := d.decay[key]; alreadyDecayed {
				continue
			}
			seen[key] = true
			out = append(out, n)
		}
		_ = k
	}
	return sortedCellRefs(out)
}

// spreadOneStep spreads blight to exactly one cell (AC-4: cell-by-cell, never
// all-at-once across a district). It returns the newly-blighted cell, or
// (CellRef{}, false) if the frontier is empty. The chosen cell is the
// deterministic minimum of the frontier (cellLess order), so a forced tie
// between equally-eligible neighbours resolves identically every run.
func (d *DecayAPI) spreadOneStep(month int64) (CellRef, bool) {
	if err := d.checkNotCopied("spreadOneStep"); err != nil {
		return CellRef{}, false
	}
	frontier := d.spreadFrontier()
	if len(frontier) == 0 {
		return CellRef{}, false
	}
	next := frontier[0]
	d.decay[next.key()] = &decayState{
		cell:        next,
		abandonedAt: month,
		age:         0,
		severity:    d.cfg.Decay.AbandonSeverityStart,
	}
	return next, true
}

// ageCells ages every decayed cell by one month and grows its severity,
// sharded across workers for the AC-9/AC-10 shard-worker-pool path. The
// result is deterministic regardless of worker count: each worker mutates a
// DISJOINT slice of the cell keys (those with index ≡ workerID mod workers),
// so no two workers ever touch the same record, and the caller merges the
// per-worker views in worker order afterward (GR#21).
func (d *DecayAPI) ageCells(workers int) [][]cellKey {
	if err := d.checkNotCopied("ageCells"); err != nil {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	keys := d.sortedDecayKeys()
	shards := make([][]cellKey, workers)
	for i := range shards {
		shards[i] = make([]cellKey, 0, (len(keys)+workers-1)/workers)
	}
	for i, k := range keys {
		shards[i%workers] = append(shards[i%workers], k)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		shard := shards[w]
		wg.Add(1)
		go func(keys []cellKey) {
			defer wg.Done()
			for _, k := range keys {
				st := d.decay[k]
				if st == nil {
					continue
				}
				st.age = num.SatAdd(st.age, 1)
				st.severity += d.cfg.Decay.SeverityGrowthPerMonth
				if st.severity > d.cfg.Blight.MaxSeverity {
					st.severity = d.cfg.Blight.MaxSeverity
				}
			}
		}(shard)
	}
	wg.Wait()
	return shards
}

// sortedDecayKeys returns the decay map's keys in deterministic cellLess
// order (never map iteration — GR#21).
func (d *DecayAPI) sortedDecayKeys() []cellKey {
	if err := d.checkNotCopied("sortedDecayKeys"); err != nil {
		return nil
	}
	out := make([]cellKey, 0, len(d.decay))
	for k := range d.decay {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return cellLess(CellRef{Tile: out[i].tile, Local: out[i].local},
			CellRef{Tile: out[j].tile, Local: out[j].local})
	})
	return out
}
