package citizens

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// FEAT-2326609842 -- the occupant index: "who lives in this cell" and "who works
// at this workplace", for the citizen dummy-passport's Residents and Staff
// lists.
//
// # Cost justification (BUG-602 / perf)
//
// The cold store has no home/workplace-keyed access path (shards are keyed by
// citizen id), so a listing needs an index. Maintaining one incrementally would
// thread through every row mutation (append, removeAt's swap-delete, load,
// paging decode) and cost memory on EVERY city whether or not anyone ever opens
// a Residents list. So the index is LAZY and QUERY-DRIVEN instead:
//
//   - nothing runs per tick -- there is no per-tick O(N) rebuild;
//   - the first OccupantsPage after a change builds it in ONE O(N) pass over
//     the id/home/workplace columns (no per-record allocation), then every later
//     page of every building is O(page);
//   - it is rebuilt only when occupancyStamp changes (sim month, a seed/load/
//     life-event touch, or a fertility birth), so a scraper hammering the query
//     pays at most one rebuild per such change, never one per request.
//
// Between rebuilds a citizen who dies mid-month may still be in the index;
// OccupantsPage re-verifies every returned id against the live store through
// the indexed rowOf path (O(1) per id) and drops the dead, so a stale entry is
// never presented as a live occupant. A citizen BORN mid-month appears at the
// next rebuild trigger. Home and Workplace are only ever set at creation
// (nothing in the engine moves a citizen), so an index built this month is exact
// for the living.

// OccupantKind selects which relation OccupantsPage lists.
type OccupantKind uint8

const (
	// OccupantsHome lists the citizens whose Home cell is ref.
	OccupantsHome OccupantKind = 1
	// OccupantsWorkplace lists the citizens whose Workplace ref is ref.
	OccupantsWorkplace OccupantKind = 2
)

type occupancyStamp struct {
	month     int64
	fertility uint64
	gen       uint64
}

// occupantIndex maps a key to its occupants' ids, ascending (a stable page
// order). Lookup only: no code ranges over the maps to decide any order.
type occupantIndex struct {
	stamp     occupancyStamp
	home      map[uint32][]uint64
	workplace map[uint32][]uint64
}

func (c *CitizensAPI) currentOccupancyStampLocked() occupancyStamp {
	return occupancyStamp{month: c.month, fertility: c.nextFertilityChildID, gen: c.occGen}
}

// buildOccupantIndexLocked is the one O(N) pass. Caller holds c.mu (read or
// write) and c.occMu.
func (c *CitizensAPI) buildOccupantIndexLocked() *occupantIndex {
	idx := &occupantIndex{
		stamp:     c.currentOccupancyStampLocked(),
		home:      map[uint32][]uint64{},
		workplace: map[uint32][]uint64{},
	}
	for shard := range c.cold {
		s := c.shardAt(shard)
		for i, id := range s.ids {
			if h := s.homeCells[i]; h != 0 {
				idx.home[h] = append(idx.home[h], id)
			}
			if w := s.workplaces[i]; w != 0 {
				idx.workplace[w] = append(idx.workplace[w], id)
			}
		}
	}
	for k := range idx.home {
		ids := idx.home[k]
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	}
	for k := range idx.workplace {
		ids := idx.workplace[k]
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	}
	return idx
}

// OccupantsPage returns one page of the occupants of ref (a Home CellRef or a
// Workplace ref, per kind), ascending by id, as a KEYSET page: the verified-live
// ids strictly greater than afterID (0 = from the start), at most limit of them.
//
// The cursor is an ID, not a position, so a client stepping afterID = the last
// id it received can neither skip nor repeat a citizen however the index is
// compacted or rebuilt between its pages: a death anywhere only removes that one
// citizen from the ascending sequence and a birth only inserts one, and "first
// id > afterID" is unaffected by either (round r2 F2).
//
// hasMore is true iff at least one further verified-live id exists after the
// last returned id (one extra live id is probed, so it is exact at the moment
// of the read, not a "maybe"); when no ids are returned it is false. total is
// the index's remaining count for the key after stale entries met by the walk
// are compacted out: exact for what has been visited, a documented UPPER BOUND
// otherwise (an exact live-now count would cost O(occupants) per request, which
// the scrape bound AC-7 forbids). limit must be positive; callers enforce their
// own cap (the protocol layer's OccupantsPageCap). An unknown key, or an
// afterID at or past the last id, is an empty page, not an error.
func (c *CitizensAPI) OccupantsPage(kind OccupantKind, ref uint32, afterID uint64, limit int, correlationID string) (ids []uint64, total int, hasMore bool, err error) {
	if err := c.checkNotCopied(correlationID, "OccupantsPage"); err != nil {
		return nil, 0, false, err
	}
	if err := validateFieldRange(0, "occupantKind", int64(kind), int64(OccupantsHome), int64(OccupantsWorkplace), correlationID); err != nil {
		return nil, 0, false, err
	}
	if err := validateFieldRange(0, "occupantsLimit", int64(limit), 1, 1<<31-1, correlationID); err != nil {
		return nil, 0, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.occMu.Lock()
	defer c.occMu.Unlock()
	if c.occ == nil || c.occ.stamp != c.currentOccupancyStampLocked() {
		c.occ = c.buildOccupantIndexLocked()
	}
	var list []uint64
	if kind == OccupantsHome {
		list = c.occ.home[ref]
	} else {
		list = c.occ.workplace[ref]
	}
	// First position holding an id > afterID (the list is ascending, unique).
	pos := sort.Search(len(list), func(i int) bool { return list[i] > afterID })
	// Walk forward collecting up to limit verified-live ids, then probe for ONE
	// more live id (hasMore). A stale entry (mid-month death, or a row whose
	// key no longer matches) is compacted out in place and the walk continues
	// at the same position. Each verification is the indexed rowOf path (O(1));
	// a removal is O(len(list)) but happens at most once per death.
	out := make([]uint64, 0, limit)
	for pos < len(list) && len(out) <= limit {
		id := list[pos]
		s := c.shardAt(det.ShardForEntity(id))
		row := s.rowOf(id)
		live := row >= 0
		if live && kind == OccupantsHome && s.homeCells[row] != ref {
			live = false
		}
		if live && kind == OccupantsWorkplace && s.workplaces[row] != ref {
			live = false
		}
		if !live {
			list = append(list[:pos], list[pos+1:]...)
			continue
		}
		out = append(out, id)
		pos++
	}
	if len(out) > limit {
		hasMore = true
		out = out[:limit]
	}
	if kind == OccupantsHome {
		c.occ.home[ref] = list
	} else {
		c.occ.workplace[ref] = list
	}
	return out, len(list), hasMore, nil
}

// PassportRecord returns the citizen's current record in the uniform cold
// shape plus the sim month it was read at -- the single read the citizen
// dummy-passport (FEAT-2326609842) renders. The citizen is found through the
// indexed rowOf path (O(1), pages the owning shard in transparently), never a
// scan; an elevated (HOT/WARM) citizen's richer hot record wins so the
// passport never shows a stale cold copy. A dead, never-minted or unknown id
// is ok=false (the caller refuses with a registry code, never zeros).
func (c *CitizensAPI) PassportRecord(id uint64, correlationID string) (rec ColdRecord, month int64, ok bool) {
	if err := c.checkNotCopied(correlationID, "PassportRecord"); err != nil {
		return ColdRecord{}, 0, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	cold, inCold := c.coldRecord(id)
	if hot, isHot := c.hot[id]; isHot {
		district := districtOf(hot.Home)
		if inCold {
			district = cold.District
		}
		return hotToColdRecord(cloneCitizen(*hot), district), c.month, true
	}
	if !inCold {
		return ColdRecord{}, c.month, false
	}
	return cold, c.month, true
}
