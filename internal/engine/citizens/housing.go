package citizens

import (
	"container/heap"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// detShard maps an entity id to its cold shard (the same function every other
// citizens path uses).
func detShard(id uint64) int { return det.ShardForEntity(id) }

// FEAT-2326609845 -- the housing ledger: gives citizens a real Home cell from
// BUILT residential capacity.
//
// The composition root (the only caller that sees engine.build) pushes the
// standing residential structures in with [CitizensAPI.SetHomeCapacities] and
// then calls [CitizensAPI.AssignHomes] once per day. Nothing here imports
// engine.build (GR#20/GR#25): a capacity is just (cell, count).
//
// # Determinism (GR#21)
//
// Homeless citizens are served in ascending id order (a min-heap of ids) into
// buildings in ascending cell order (a min-heap of cells with a free slot).
// Neither order depends on map iteration, goroutine scheduling or shard count,
// so the same seed and the same build history give byte-identical homes.
//
// # Cost
//
// The ledger is OPT-IN: c.housing is nil until the composition root first
// calls SetHomeCapacities, so the bulk-seed / synth paths pay nothing. Enabling
// it costs ONE O(N) pass over the home column; after that every operation is
// O(log) per citizen moved. A demolition or capacity cut is the only event that
// scans the columns again (to evict the displaced), and it is rare.
//
// # Persistence (r2 ruling)
//
// Occupancy is DERIVED: Home already persists as a cold column, and a restore
// rebuilds occupancy and the pending set from it. The ONE thing that is real
// state is the ledger's last-applied capacity snapshot (cell -> capacity):
// the daily sync diffs the next snapshot against it to find shrunk or
// vanished dwellings. Without it a demolition made between the last sync and
// a save would never be seen after a load (the residents would stay on the
// demolished cell forever). So the snapshot IS saved: citizensMetaWire.HousingOn
// marks "ledger was on" and one "citizens.housingcap" record per cell (sorted
// ascending) carries it. A restore stages it (housingRestore) and the ledger is
// materialised by [CitizensAPI.RestoreHousing] (or lazily by the first ledger
// call): occupancy from the Home columns, capacity from the saved snapshot,
// NO AssignHomes. Load therefore never changes a Home; the next daily sync
// (SetHomeCapacities then AssignHomes) does the work, identically in a
// saved-and-loaded run and in the uninterrupted run.
//
// OLD SAVES carry no HousingOn flag, so they restore with the ledger off. The
// next sync then enables it from the CURRENT build state: the one-time effect
// is that capacity changes made before the save cannot be diffed (there is no
// prior snapshot), so any citizen whose Home is a cell with no standing
// capacity keeps it until the first later capacity cut. Pre-feature saves hold
// no ledger-assigned homes, so in practice this only touches seeded homes.
//
// # ASM-1538 (overcrowding, stated precisely)
//
// A newborn inherits a parent's Home (parent A's, else parent B's) without
// consulting capacity, so births MAY overcrowd a dwelling, and across
// generations that overcrowding is not bounded by the ledger. Residents are
// trimmed back to capacity ONLY when that dwelling's capacity is CUT (a smaller
// figure, a rezone or a demolition), and then the HIGHEST ids are evicted
// first. An overcrowded dwelling whose capacity is unchanged is never trimmed.
// Placeholder numbers: AARON BALANCE PASS.
//
// Home 0 is the "no home" sentinel, so a structure on linear cell 0 can never
// be a home: SetHomeCapacities ignores it (documented, ASM logged).

// HomeCapacity is one residential structure: its linear cell reference
// (y*width+x, row-major over the world grid) and how many citizens it houses.
type HomeCapacity struct {
	Cell     CellRef
	Capacity int32
}

type cellHeap []uint32

func (h cellHeap) Len() int           { return len(h) }
func (h cellHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h cellHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *cellHeap) Push(x any)        { *h = append(*h, x.(uint32)) }
func (h *cellHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

type idHeap []uint64

func (h idHeap) Len() int           { return len(h) }
func (h idHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x any)        { *h = append(*h, x.(uint64)) }
func (h *idHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// housingLedger is the derived occupancy state. Guarded by CitizensAPI.mu.
type housingLedger struct {
	capacity map[uint32]int32 // building cell -> slots
	occ      map[uint32]int32 // cell -> residents (may exceed capacity for seeded/inherited homes)
	free     cellHeap         // cells with occ < capacity (lazily validated)
	inFree   map[uint32]bool
	pending  idHeap // homeless ids (lazily validated: dead or already housed ids are dropped on pop)
	// stale counts homeless citizens that died while still in pending; once
	// they outnumber the live entries the heap is compacted (amortised O(1)
	// per death), so dead ids never pile up.
	stale int
}

func newHousingLedger() *housingLedger {
	return &housingLedger{
		capacity: map[uint32]int32{},
		occ:      map[uint32]int32{},
		inFree:   map[uint32]bool{},
	}
}

func (l *housingLedger) pushFreeIfRoom(cell uint32) {
	if l.inFree[cell] || l.occ[cell] >= l.capacity[cell] {
		return
	}
	l.inFree[cell] = true
	heap.Push(&l.free, cell)
}

// HousingEnabled reports whether the housing ledger has been switched on.
func (c *CitizensAPI) HousingEnabled() bool {
	if err := c.checkNotCopied("", "HousingEnabled"); err != nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.housing != nil || c.housingRestore != nil
}

// housingLedgerLocked returns the live ledger (nil when off), materialising a
// restored-but-not-yet-built one first. Caller holds c.mu for WRITE.
func (c *CitizensAPI) housingLedgerLocked() *housingLedger {
	if c.housing == nil && c.housingRestore != nil {
		c.materializeHousingLocked()
	}
	return c.housing
}

// materializeHousingLocked builds the ledger a restore staged: occupancy and
// pending from the Home columns, capacity from the saved snapshot, free heap
// from both. It assigns NOBODY. Caller holds c.mu for write.
func (c *CitizensAPI) materializeHousingLocked() {
	snap := c.housingRestore
	c.housingRestore = nil
	c.housing = newHousingLedger()
	c.rebuildHousingFromColumnsLocked()
	l := c.housing
	l.capacity = snap
	cells := make([]uint32, 0, len(snap))
	for cell := range snap {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i] < cells[j] })
	for _, cell := range cells {
		l.pushFreeIfRoom(cell)
	}
	c.occGen++
}

// RestoreHousing materialises the ledger a load staged (no-op when the save
// carried none, or it is already built). It never assigns or evicts a Home, so
// a Load leaves the population hash exactly as saved. The composition root
// calls it once at the end of Load.
func (c *CitizensAPI) RestoreHousing(correlationID string) error {
	if err := c.checkNotCopied(correlationID, "RestoreHousing"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.housingLedgerLocked()
	return nil
}

// housingSnapshotLocked returns the ledger's last-applied capacity snapshot as
// wire entries sorted by cell, and whether the ledger is on (a staged restore
// counts: it re-saves identically). Caller holds c.mu (read is enough).
func (c *CitizensAPI) housingSnapshotLocked() ([]housingCapWire, bool) {
	var src map[uint32]int32
	switch {
	case c.housing != nil:
		src = c.housing.capacity
	case c.housingRestore != nil:
		src = c.housingRestore
	default:
		return nil, false
	}
	out := make([]housingCapWire, 0, len(src))
	for cell, cp := range src {
		out = append(out, housingCapWire{Cell: cell, Capacity: cp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cell < out[j].Cell })
	return out, true
}

// HomeOccupancy returns how many citizens the ledger records at cell (0 when
// the ledger is off). Diagnostic/test accessor.
func (c *CitizensAPI) HomeOccupancy(cell CellRef) int {
	if err := c.checkNotCopied("", "HomeOccupancy"); err != nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.housingLedgerLocked()
	if l == nil {
		return 0
	}
	return int(l.occ[uint32(cell)])
}

// HomelessCount returns the number of living citizens with no Home. It walks
// the home column (O(N)); diagnostic/test accessor, never called per tick.
func (c *CitizensAPI) HomelessCount() int {
	if err := c.checkNotCopied("", "HomelessCount"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for shard := range c.cold {
		s := c.shardAt(shard)
		for i := range s.ids {
			if s.homeCells[i] == 0 {
				n++
			}
		}
	}
	return n
}

// SetHomeCapacities installs the FULL current set of residential structures
// (a snapshot, not a delta). The first call switches the ledger on and builds
// it from the home column; later calls diff against the previous snapshot:
// a structure that disappeared or shrank below its occupancy evicts the
// highest-id surplus residents back to the homeless pool. A cell of 0 (the
// no-home sentinel) and non-positive capacities are ignored; duplicates of a
// cell keep the largest figure.
func (c *CitizensAPI) SetHomeCapacities(caps []HomeCapacity, correlationID string) error {
	if err := c.checkNotCopied(correlationID, "SetHomeCapacities"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	next := make(map[uint32]int32, len(caps))
	for _, hc := range caps {
		if hc.Cell == 0 || hc.Capacity <= 0 {
			continue
		}
		if hc.Capacity > next[uint32(hc.Cell)] {
			next[uint32(hc.Cell)] = hc.Capacity
		}
	}

	if c.housingLedgerLocked() == nil {
		c.housing = newHousingLedger()
		c.rebuildHousingFromColumnsLocked()
	}
	l := c.housing

	// Cells whose capacity fell (or vanished) below their occupancy need an
	// eviction scan. Cell list sorted, so the walk is order-independent.
	var shrunk []uint32
	for cell, old := range l.capacity {
		if next[cell] < old && l.occ[cell] > next[cell] {
			shrunk = append(shrunk, cell)
		}
	}
	sort.Slice(shrunk, func(i, j int) bool { return shrunk[i] < shrunk[j] })
	if len(shrunk) > 0 {
		c.evictHomesLocked(shrunk, next)
	}

	l.capacity = next
	// Rebuild the free heap from scratch: cells in ascending order, so the
	// heap content depends only on (capacity, occ), never on history.
	l.free = l.free[:0]
	l.inFree = map[uint32]bool{}
	cells := make([]uint32, 0, len(next))
	for cell := range next {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i] < cells[j] })
	for _, cell := range cells {
		l.pushFreeIfRoom(cell)
	}
	c.occGen++
	return nil
}

// rebuildHousingFromColumnsLocked is the one O(N) pass: occupancy from the
// home column, pending from the homeless. Caller holds c.mu (write).
func (c *CitizensAPI) rebuildHousingFromColumnsLocked() {
	l := c.housing
	l.occ = map[uint32]int32{}
	l.pending = l.pending[:0]
	l.stale = 0
	for shard := range c.cold {
		s := c.shardAt(shard)
		for i, id := range s.ids {
			if h := s.homeCells[i]; h != 0 {
				l.occ[h]++
			} else {
				l.pending = append(l.pending, id)
			}
		}
	}
	heap.Init(&l.pending)
}

// evictHomesLocked returns the surplus residents of every cell in cells
// (sorted ascending) to the homeless pool. next is the new capacity map. The
// evicted are the HIGHEST ids of each cell (a pure function of the data).
func (c *CitizensAPI) evictHomesLocked(cells []uint32, next map[uint32]int32) {
	l := c.housing
	want := make(map[uint32][]uint64, len(cells))
	for _, cell := range cells {
		want[cell] = nil
	}
	for shard := range c.cold {
		s := c.shardAt(shard)
		for i, id := range s.ids {
			if h := s.homeCells[i]; h != 0 {
				if _, ok := want[h]; ok {
					want[h] = append(want[h], id)
				}
			}
		}
	}
	for _, cell := range cells {
		ids := want[cell]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		keep := int(next[cell])
		if keep < 0 {
			keep = 0
		}
		if keep > len(ids) {
			keep = len(ids)
		}
		for _, id := range ids[keep:] {
			shard := detShard(id)
			s := c.shardAt(shard)
			row := s.rowOf(id)
			if row < 0 {
				continue
			}
			s.homeCells[row] = 0
			s.districts[row] = districtOf(0)
			if hc, ok := c.hot[id]; ok {
				hc.Home = 0
			}
			l.occ[cell]--
			heap.Push(&l.pending, id)
		}
	}
}

// AssignHomes houses homeless citizens into free residential capacity: lowest
// homeless id into the lowest-numbered building with a free slot, repeated
// until either side runs out. Returns how many citizens were housed. Call from
// the single-threaded barrier (the composition root's daily hook).
func (c *CitizensAPI) AssignHomes(correlationID string) (int, error) {
	if err := c.checkNotCopied(correlationID, "AssignHomes"); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.housingLedgerLocked()
	if l == nil {
		return 0, nil
	}
	c.compactPendingLocked(l, 0)
	housed := 0
	for l.pending.Len() > 0 {
		// Lowest cell that really has room (lazy validation: capacity may
		// have changed since it was pushed).
		cell, ok := uint32(0), false
		for l.free.Len() > 0 {
			top := l.free[0]
			if l.occ[top] < l.capacity[top] {
				cell, ok = top, true
				break
			}
			heap.Pop(&l.free)
			delete(l.inFree, top)
		}
		if !ok {
			break
		}
		id := heap.Pop(&l.pending).(uint64)
		s := c.shardAt(detShard(id))
		row := s.rowOf(id)
		if row < 0 || s.homeCells[row] != 0 {
			continue // died, or already housed: drop the stale entry
		}
		s.homeCells[row] = cell
		s.districts[row] = districtOf(CellRef(cell))
		if hc, found := c.hot[id]; found {
			hc.Home = CellRef(cell)
		}
		l.occ[cell]++
		housed++
	}
	if housed > 0 {
		c.occGen++
	}
	return housed, nil
}

// housingAppendedLocked records a freshly appended citizen. No-op while the
// ledger is off. Caller holds c.mu (write).
func (c *CitizensAPI) housingAppendedLocked(id uint64, home uint32) {
	l := c.housingLedgerLocked()
	if l == nil {
		return
	}
	if home == 0 {
		heap.Push(&l.pending, id)
		return
	}
	l.occ[home]++
}

// housingReleaseLocked frees what a citizen about to be removed (death or
// departure) holds: its dwelling slot, or, if it was homeless, its entry in
// the pending heap (counted stale and compacted away once stale entries
// outnumber live ones, so dead ids never pile up). Call BEFORE the row is
// removed. No-op while the ledger is off. Caller holds c.mu (write).
func (c *CitizensAPI) housingReleaseLocked(id uint64, home uint32) {
	l := c.housingLedgerLocked()
	if l == nil {
		return
	}
	if home == 0 {
		l.stale++
		c.compactPendingLocked(l, id)
		return
	}
	if l.occ[home] > 0 {
		l.occ[home]--
	}
	l.pushFreeIfRoom(home)
}

// compactPendingLocked drops dead, already-housed and excluded ids from the
// pending heap once the stale count passes half of it (and a small floor so
// tiny heaps are not rebuilt per death). exclude is an id about to die (0 for
// none). Caller holds c.mu (write).
func (c *CitizensAPI) compactPendingLocked(l *housingLedger, exclude uint64) {
	if l.stale <= 64 || l.stale*2 <= len(l.pending) {
		return
	}
	kept := l.pending[:0]
	for _, id := range l.pending {
		if id == exclude {
			continue
		}
		s := c.shardAt(detShard(id))
		if row := s.rowOf(id); row >= 0 && s.homeCells[row] == 0 {
			kept = append(kept, id)
		}
	}
	l.pending = kept
	heap.Init(&l.pending)
	l.stale = 0
}

// homeOfLocked returns a living citizen's Home (0 when unknown/none).
func (c *CitizensAPI) homeOfLocked(id uint64) uint32 {
	s := c.shardAt(detShard(id))
	if row := s.rowOf(id); row >= 0 {
		return s.homeCells[row]
	}
	return 0
}
