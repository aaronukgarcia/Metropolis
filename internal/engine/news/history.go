package news

import "sync"

// History is the persisted, append-only event log — the single source of
// truth (§29's "archive searchable" clause, GR#3) that every generation
// layer (bulletin, annual review, epilogue) and the archive read from
// (AC-4, AC-5, AC-9). It is safe for concurrent use: the single write path
// takes a write lock, every read takes a defensive snapshot under a read
// lock (AC-12).
//
// The log is write-once from outside the package: the only mutation is the
// unexported append, reached solely through NewsAPI.Ingest (which validates
// the event and resolves its name first). A caller handed *History via
// NewsAPI.History can only read (Len / Snapshot / SnapshotWhere), so an
// invalid event can never be recorded into the single source of truth
// (SEC-112).
//
// Retention is full-history with no pruning (the BA's logged ASM for this
// module): news text volume is orders of magnitude smaller than raw
// per-citizen state, but the no-culls-ever choice is still an assumption,
// not spec text.
type History struct {
	mu      sync.RWMutex
	records []record
}

// record is one persisted news entry: the source Event plus the §20 name
// resolved at ingest time. Storing the resolved name means a later namer
// change can never retroactively drop or alter an already-accepted story
// (SEC-110): every accepted event keeps the name it was ingested with, and
// AC-9's "every accepted event appears in the archive" holds independent of
// the current namer.
type record struct {
	ev   Event
	name string
}

// NewHistory constructs an empty log.
func NewHistory() *History {
	return &History{}
}

// append records a validated event and its resolved name in ingest order,
// returning the number of events now held. It is unexported and the only
// write path: Ingest is the sole caller, and it has already validated the
// event and resolved the name, so an invalid event cannot reach the log.
func (h *History) append(ev Event, name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record{ev: ev, name: name})
	return len(h.records)
}

// Len returns the number of recorded events.
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.records)
}

// Snapshot returns a defensive copy of the whole log in ingest order. The
// copy is immutable; callers range over it without holding h's lock, so a
// generation pass never blocks (or races with) concurrent ingestion.
func (h *History) Snapshot() []record {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshotLocked(func(record) bool { return true })
}

// SnapshotWhere returns a defensive copy of the records matching keep, in
// ingest order, without materialising the whole log — a month/year query
// allocates only the matching window (SEC-114), not O(total history). keep
// runs under the read lock and must not call back into h.
func (h *History) SnapshotWhere(keep func(record) bool) []record {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshotLocked(keep)
}

func (h *History) snapshotLocked(keep func(record) bool) []record {
	// No len(h.records) preallocation: that sized the backing array to the
	// whole log even when the filter matched a sliver of it, so a window
	// query still allocated O(total) (SEC-150). A nil slice grows with the
	// matching window instead, so the returned copy's capacity is
	// O(window), not O(total history).
	var out []record
	for _, r := range h.records {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}
