package num

// DefaultLedgerCapacity is the fixed maximum number of events a
// [BoundedLedger] retains — the spec's "max = 1000 events" ceiling for
// NON-conserved event history (SEC-203 / FEAT-135). It is a resource
// ceiling, not a balance number: event history that is not required for a
// conservation identity is bounded so a hostile or buggy input stream cannot
// grow it without limit, while the most recent 1000 events remain inspectable.
// Named constant per GR#15 / FEAT-135 AC-6 — never a bare literal.
const DefaultLedgerCapacity = 1000

// BoundedLedger is a fixed-capacity FIFO ring buffer for event history that is
// NOT conserved (SEC-203 / FEAT-135): once capacity is reached, appending a
// new event evicts the OLDEST retained event, so memory is bounded no matter
// how many events arrive. It is the structural counterpart to
// [SanitizeEventID]'s BoundedString: BoundedString bounds a single input
// value, BoundedLedger bounds the RETAINED HISTORY a stream of inputs would
// otherwise accumulate without limit.
//
// Eviction is deterministic (GR#21): [BoundedLedger.Append] always overwrites
// the oldest live slot in insertion order, and [BoundedLedger.Snapshot]
// returns the retained events in oldest-first order — never map-iteration
// order, never a wall clock or RNG.
//
// BoundedLedger is NOT internally synchronized: the embedding owner guards it
// with its own mutex (exactly as engine.social's conserved a.cases slice is
// guarded by SocialAPI.mu). It is intended for NON-conserved history only — a
// conserved ledger whose identity depends on every entry surviving (e.g.
// engine.social's AC-11 case ledger) must NOT be routed through a structure
// that evicts, or the conservation identity would be silently broken.
//
// The zero value is not usable; construct via [NewBoundedLedger].
type BoundedLedger[T any] struct {
	max  int // fixed capacity (len(buf) == max)
	buf  []T // ring storage
	head int // index of the OLDEST live entry
	size int // number of live entries (0..max)
}

// NewBoundedLedger returns an empty [BoundedLedger] with the spec's default
// capacity ([DefaultLedgerCapacity]).
func NewBoundedLedger[T any]() *BoundedLedger[T] {
	return newBoundedLedger[T](DefaultLedgerCapacity)
}

// newBoundedLedger builds a ledger with an explicit capacity — the test seam,
// and the escape hatch for an owner that legitimately needs a different
// window. A max below 1 is raised to [DefaultLedgerCapacity] so the ring
// arithmetic never divides by zero.
func newBoundedLedger[T any](max int) *BoundedLedger[T] {
	if max < 1 {
		max = DefaultLedgerCapacity
	}
	return &BoundedLedger[T]{max: max, buf: make([]T, max)}
}

// Append records one event. When the ledger is full, the oldest event is
// evicted (overwritten) — bounded, deterministic retention (GR#21).
func (b *BoundedLedger[T]) Append(e T) {
	if b.size < b.max {
		// Not yet full: head is still 0, so the next slot is just size; the
		// general form keeps the arithmetic correct regardless of head.
		b.buf[(b.head+b.size)%b.max] = e
		b.size++
		return
	}
	// Full: overwrite the oldest slot, then advance head so the next-oldest
	// becomes the new oldest.
	b.buf[b.head] = e
	b.head = (b.head + 1) % b.max
}

// Len returns the number of events currently retained (0..max).
func (b *BoundedLedger[T]) Len() int { return b.size }

// Snapshot returns the retained events in oldest-first insertion order, as a
// fresh slice — the caller mutating the returned slice does not affect the
// ledger (the SEC-062 return-a-copy discipline). Deterministic (GR#21).
func (b *BoundedLedger[T]) Snapshot() []T {
	out := make([]T, b.size)
	for i := 0; i < b.size; i++ {
		out[i] = b.buf[(b.head+i)%b.max]
	}
	return out
}
