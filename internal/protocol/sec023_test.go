package protocol

// SEC-023 (SEC-020 wave 1 sibling hunt): SubscriptionAllocator
// copy-rejection guard tests.
//
// ENUMERATION METHOD (Weakness pattern #3 — state the method, not just
// the count). SubscriptionAllocator has exactly one exported method
// besides the constructor:
//
//	grep -n '^func (a \*SubscriptionAllocator)' internal/protocol/subscription.go
//
// which returns exactly one hit: Allocate. Allocate is the sole place
// a.next is ever read or written, so it is also the sole guarded site —
// cross-checked by hand against subscription.go's full text (no other
// method, no exported field write, touches a.next). One method, one
// guard, zero remaining ungated a.next access.
//
// REPRODUCED COLLISION (dispatch brief: "reproduce that collision
// yourself before fixing it"). Before the checkNotCopied guard existed,
// copyAllocatorBytes(a) after two real Allocate() calls on the original
// produced a copy whose OWN next.Add(1) call returns 3 — the SAME
// "sub-3" the original's next real call also returns — because next is
// a plain atomic.Uint64 VALUE (unlike InProcTransport/SubscriptionServer,
// nothing here is a reference type, so the byte-copy does not alias
// anything; it duplicates the counter's current state instead).
// TestSEC023_DeterministicCopy_CollidesOnNextID below proves this
// directly by temporarily bypassing the guard (constructing the copy
// with self deliberately left pointing at itself would defeat the whole
// point of the test — instead it asserts on the RAW counter arithmetic,
// which is the actual defect, independent of whether the guard catches
// it) and separately proves the guard rejects the real Allocate() path.

import (
	"testing"
	"unsafe"
)

// copyAllocatorBytes performs a raw byte-for-byte memcpy of a
// SubscriptionAllocator — identical in effect to the illegal-but-
// compilable `c := *a` — same technique as sec020_test.go's
// copyTransportBytes / engine/core's sec014_poc_test.go's e2Copy, used
// here for the identical reason: this package cannot contain a literal
// `*a` and still pass `go vet ./...` (copylocks, since atomic.Uint64
// carries a noCopy sentinel), which this fix's VERIFY step requires.
func copyAllocatorBytes(a *SubscriptionAllocator) *SubscriptionAllocator {
	c := new(SubscriptionAllocator)
	*(*[unsafe.Sizeof(SubscriptionAllocator{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(SubscriptionAllocator{})]byte)(unsafe.Pointer(a))
	return c
}

// TestSEC023_RawCounter_CopyCollidesOnNextID reproduces the finding's
// core claim directly against the counter arithmetic, independent of the
// guard: take an original that has allocated twice (next == 2), byte-copy
// it, and show the copy's raw atomic.Uint64.Add(1) returns 3 — the exact
// same value the original's next real Add(1) would also return — then
// that both go on to diverge (copy's second call returns 4, same as the
// original's next call would). This is the collision itself, asserted on
// directly per the dispatch brief ("the collision IS the finding").
func TestSEC023_RawCounter_CopyCollidesOnNextID(t *testing.T) {
	a := NewSubscriptionAllocator()
	if id := a.Allocate(); id != "sub-1" {
		t.Fatalf("seed Allocate() = %q, want sub-1", id)
	}
	if id := a.Allocate(); id != "sub-2" {
		t.Fatalf("seed Allocate() = %q, want sub-2", id)
	}

	// Byte-copy at next==2. Read the copy's raw counter directly (NOT
	// through Allocate(), which the fix now guards) to prove the
	// collision is in the counter's own state, not merely observable
	// through the guarded accessor.
	cp := copyAllocatorBytes(a)
	copyNext1 := cp.next.Add(1)
	origNext1 := a.next.Add(1)
	if copyNext1 != origNext1 {
		t.Fatalf("copy's first raw Add(1) = %d, original's next raw Add(1) = %d, want EQUAL (that equality IS the collision)", copyNext1, origNext1)
	}
	if copyNext1 != 3 {
		t.Fatalf("copy's first raw Add(1) = %d, want 3 (sub-3, colliding with the original's next allocation)", copyNext1)
	}

	// The two counters are now independent memory, not a single shared
	// sequence — calling one more than the other (as any real divergent
	// usage would) breaks lockstep immediately. Two more calls on the
	// copy, one more on the original:
	cp.next.Add(1) // copy's third call -> 5
	copyNext3 := cp.next.Add(1)
	origNext3 := a.next.Add(1) // original's third call -> 4
	if copyNext3 == origNext3 {
		t.Fatalf("copy's raw counter (%d) unexpectedly still equals original's (%d) after an uneven call pattern — the two counters are supposed to be fully independent memory once copied, not a coordinated shared sequence", copyNext3, origNext3)
	}
}

// TestSEC023_GuardedAllocate_ZeroValue_Rejected exercises the fixed
// guard on a bare SubscriptionAllocator{}/new(SubscriptionAllocator),
// never touched by NewSubscriptionAllocator, so self was never stored.
// Allocate must return SentinelCopiedSubscriptionID, never a
// "sub-<N>"-shaped value and never panic on the nil self.
func TestSEC023_GuardedAllocate_ZeroValue_Rejected(t *testing.T) {
	zero := new(SubscriptionAllocator)
	if id := zero.Allocate(); id != SentinelCopiedSubscriptionID {
		t.Fatalf("zero.Allocate() = %q, want SentinelCopiedSubscriptionID (%q)", id, SentinelCopiedSubscriptionID)
	}
	// Repeat calls must be stable, not merely "wrong once".
	if id := zero.Allocate(); id != SentinelCopiedSubscriptionID {
		t.Fatalf("zero.Allocate() second call = %q, want SentinelCopiedSubscriptionID (%q)", id, SentinelCopiedSubscriptionID)
	}
}

// TestSEC023_GuardedAllocate_DeterministicCopy_NeverCollidesWithOriginal
// is the fix's real regression test: a deterministically-constructed
// copy (built via the same byte-copy technique the collision-reproduction
// test above uses) must NEVER produce a colliding — or any — real-looking
// SubscriptionID through the guarded Allocate() path, however many times
// it is called, while the original keeps allocating normally and without
// interference.
func TestSEC023_GuardedAllocate_DeterministicCopy_NeverCollidesWithOriginal(t *testing.T) {
	a := NewSubscriptionAllocator()
	if id := a.Allocate(); id != "sub-1" {
		t.Fatalf("seed Allocate() = %q, want sub-1", id)
	}

	cp := copyAllocatorBytes(a)

	seen := make(map[SubscriptionID]bool)
	const attempts = 50
	for i := 0; i < attempts; i++ {
		id := cp.Allocate()
		if id != SentinelCopiedSubscriptionID {
			t.Fatalf("cp.Allocate() attempt %d = %q, want SentinelCopiedSubscriptionID (%q)", i, id, SentinelCopiedSubscriptionID)
		}
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("copy produced %d distinct values across %d calls, want exactly 1 (the sentinel, every time)", len(seen), attempts)
	}

	// The original is completely unaffected: it must still allocate
	// "sub-2" next, not "sub-3" or anything the copy's (rejected, never
	// advanced) calls could have polluted.
	if id := a.Allocate(); id != "sub-2" {
		t.Fatalf("original a.Allocate() after copy-attack = %q, want sub-2 (unaffected by the rejected copy)", id)
	}

	// The sentinel itself never collides with a real ID, by construction
	// (no digits after the prefix) — assert this explicitly so a future
	// change to either format is caught here rather than downstream.
	if seen[SubscriptionID("sub-2")] {
		t.Fatal("sentinel value equals a real-looking SubscriptionID — collision-avoidance property broken")
	}
}

// TestSEC023_GuardedAllocate_HandBuiltLiteral_Rejected exercises the
// third construction-misuse form: a hand-built literal with next
// deliberately pre-seeded to a plausible value, but self left unset.
// Must be rejected exactly like the zero-value case — self being unset
// is the misuse, independent of what next contains.
func TestSEC023_GuardedAllocate_HandBuiltLiteral_Rejected(t *testing.T) {
	partial := &SubscriptionAllocator{}
	partial.next.Store(41) // looks like a live, well-used allocator
	if id := partial.Allocate(); id != SentinelCopiedSubscriptionID {
		t.Fatalf("partial.Allocate() = %q, want SentinelCopiedSubscriptionID (%q)", id, SentinelCopiedSubscriptionID)
	}
	if got := partial.next.Load(); got != 41 {
		t.Fatalf("partial.next after rejected Allocate() = %d, want unchanged 41 (a rejected copy must never advance its own counter)", got)
	}
}

// TestSEC023_ConcurrentGuardedHammer_NoRaceNoCollision hammers a
// deterministically-constructed copy from many goroutines concurrently
// with the original being driven normally — the concurrent-hammer shape
// SEC-020's InProcTransport tests use. Every call on the copy must
// resolve to SentinelCopiedSubscriptionID; the original's IDs must
// remain unique and sequential throughout.
func TestSEC023_ConcurrentGuardedHammer_NoRaceNoCollision(t *testing.T) {
	a := NewSubscriptionAllocator()
	cp := copyAllocatorBytes(a)

	const hammerCalls = 2000
	badCh := make(chan SubscriptionID, hammerCalls)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < hammerCalls; i++ {
			if id := cp.Allocate(); id != SentinelCopiedSubscriptionID {
				badCh <- id
			}
		}
	}()

	origSeen := make(map[SubscriptionID]bool)
	for i := 0; i < hammerCalls; i++ {
		id := a.Allocate()
		if origSeen[id] {
			t.Fatalf("original produced a duplicate ID %q while a rejected copy was hammered concurrently", id)
		}
		origSeen[id] = true
	}

	<-done
	close(badCh)
	for id := range badCh {
		t.Fatalf("copy call returned %q, want SentinelCopiedSubscriptionID (%q) on every call", id, SentinelCopiedSubscriptionID)
	}
	if len(origSeen) != hammerCalls {
		t.Fatalf("original produced %d unique IDs, want %d", len(origSeen), hammerCalls)
	}
}
