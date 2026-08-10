package protocol

// SEC-020 wave 1: SeqTracker copy-rejection guard tests.
//
// ENUMERATION METHOD (Weakness pattern #3 — state the method, not just
// the count). Every exported *SeqTracker method:
//
//	grep -n '^func (t \*SeqTracker)' internal/protocol/subscription.go
//
// finds exactly two: Observe and Reset. Both touch t.last (the aliased
// reference field) and both are now guarded pre-lock and post-lock — no
// remaining exported method reaches t.last unguarded. (checkNotCopied
// itself is deliberately lock-free and touches neither t.mu nor t.last.)
//
// The failure mode this guard prevents is a FATAL concurrent map access
// on t.last, not a hang or a collision (same class as SEC-003/SEC-019,
// distinct from SEC-023's SubscriptionAllocator, which has no aliased
// reference field and so fails differently) — see subscription.go's
// SeqTracker doc comment for the full mechanism.

import (
	"sync"
	"testing"
	"time"
	"unsafe"
)

// copySeqTrackerBytes performs a raw byte-for-byte memcpy of a
// SeqTracker — identical in effect to the illegal-but-compilable
// `c := *t` (both alias last; both give the copy its own, independent
// mu) — same technique as sec020_test.go's copyTransportBytes /
// sec023_test.go's copyAllocatorBytes, for the identical reason: this
// package cannot contain a literal `*t` and still pass `go vet ./...`
// (copylocks), which this fix's VERIFY step requires.
func copySeqTrackerBytes(t *SeqTracker) *SeqTracker {
	c := new(SeqTracker)
	*(*[unsafe.Sizeof(SeqTracker{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(SeqTracker{})]byte)(unsafe.Pointer(t))
	return c
}

// TestSEC020_SeqTracker_ZeroValue_Rejected exercises a bare
// SeqTracker{}/new(SeqTracker), never touched by NewSeqTracker, so self
// was never stored (and last is nil). Both Observe and Reset must reject
// via the copy-shaped (0, false) / no-op outcomes rather than panicking
// on the nil map or racing anything.
func TestSEC020_SeqTracker_ZeroValue_Rejected(t *testing.T) {
	zero := new(SeqTracker)

	if gap, ok := zero.Observe("sub-1", 1); ok || gap != 0 {
		t.Fatalf("zero.Observe = (%d, %v), want (0, false)", gap, ok)
	}
	// Must not panic — Reset has no return value to assert on beyond that.
	zero.Reset("sub-1")
}

// TestSEC020_SeqTracker_HandBuiltLiteral_Rejected exercises a hand-built
// literal with a real, populated last map (so a naive implementation
// might "work") but self left at its zero value. Must be rejected
// exactly like the zero-value case.
func TestSEC020_SeqTracker_HandBuiltLiteral_Rejected(t *testing.T) {
	partial := &SeqTracker{last: map[SubscriptionID]uint64{"sub-1": 10}}

	if gap, ok := partial.Observe("sub-1", 11); ok || gap != 0 {
		t.Fatalf("partial.Observe = (%d, %v), want (0, false)", gap, ok)
	}
	partial.Reset("sub-1")
	// The hand-built map must be untouched by the rejected Reset —
	// there is no legitimate reason a rejected call should mutate
	// anything, even state that was never supposed to exist.
	if got, ok := partial.last["sub-1"]; !ok || got != 10 {
		t.Fatalf("partial.last[\"sub-1\"] after rejected Reset = (%d, %v), want (10, true) unchanged", got, ok)
	}
}

// TestSEC020_SeqTracker_DeterministicStructCopy_Rejected builds the
// attack STATE deterministically (dispatch brief: "attack state, not
// timing, so it runs under -race") rather than relying on scheduler
// luck: the original's mu is held (locked, simulating "mid-Observe")
// across the byte-copy, exactly the SEC-016 shape. Every call on the
// copy must reject OUTRIGHT — not merely "eventually" — proving the
// pre-lock check runs before the copy's own (poisoned) mu is ever
// touched; if the check ran after the lock instead, every call below
// would hang forever on this specific copy, since nothing will ever
// unlock ITS mu.
func TestSEC020_SeqTracker_DeterministicStructCopy_Rejected(t *testing.T) {
	tr := NewSeqTracker()
	tr.Observe("sub-1", 1)

	tr.mu.Lock()
	cp := copySeqTrackerBytes(tr)
	tr.mu.Unlock()

	const attempts = 50
	for i := 0; i < attempts; i++ {
		if gap, ok := cp.Observe("sub-1", uint64(i+2)); ok || gap != 0 {
			t.Fatalf("cp.Observe attempt %d = (%d, %v), want (0, false)", i, gap, ok)
		}
		cp.Reset("sub-1") // must not hang or panic
	}

	// The ORIGINAL must be completely unaffected: still tracks sub-1
	// normally, in-order continuation from seq 1.
	if gap, ok := tr.Observe("sub-1", 2); !ok || gap != 0 {
		t.Fatalf("original tr.Observe(2) after copy attack = (%d, %v), want (0, true)", gap, ok)
	}
}

// TestSEC020_SeqTracker_ConcurrentDeterministicCopyHammer_NoHangNoRace
// hammers a deterministically-constructed copy (mu captured mid-lock,
// same construction as the test above) from many goroutines
// concurrently, alongside the ORIGINAL being driven normally — the
// concurrent-hammer shape SEC-014/SEC-018/SEC-020 all use. Every call on
// the copy must resolve to the rejected outcome — never hang, never
// panic (a fatal concurrent map write on the aliased last map is exactly
// what an unguarded copy would produce here).
func TestSEC020_SeqTracker_ConcurrentDeterministicCopyHammer_NoHangNoRace(t *testing.T) {
	tr := NewSeqTracker()

	tr.mu.Lock()
	cp := copySeqTrackerBytes(tr)
	tr.mu.Unlock()

	const hammerCalls = 3000
	var wg sync.WaitGroup
	var badMu sync.Mutex
	var bad int

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < hammerCalls; i++ {
			if gap, ok := cp.Observe(SubscriptionID("sub-hammer"), uint64(i+1)); ok || gap != 0 {
				badMu.Lock()
				bad++
				badMu.Unlock()
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < hammerCalls; i++ {
			cp.Reset(SubscriptionID("sub-hammer"))
		}
	}()

	// The original must keep working normally throughout, unaffected —
	// its own independent subscription stream, unrelated key.
	for i := 0; i < 2000; i++ {
		if gap, ok := tr.Observe(SubscriptionID("sub-orig"), uint64(i+1)); !ok || gap != 0 {
			t.Fatalf("original tr.Observe call %d during copy hammer: got (%d, %v), want (0, true)", i, gap, ok)
		}
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("copy hammer did not complete within deadline — a guard site is hanging (pre-lock ordering regressed)")
	}

	if bad != 0 {
		t.Fatalf("%d of the copy's Observe calls returned something other than the rejected outcome", bad)
	}
}

// TestSEC020_SeqTracker_FreshTrackersNeverCollide is the non-attack-path
// sanity check: two independently constructed SeqTrackers, and two
// distinct subscriptions on the same tracker, must never falsely reject
// each other.
func TestSEC020_SeqTracker_FreshTrackersNeverCollide(t *testing.T) {
	t1 := NewSeqTracker()
	t2 := NewSeqTracker()

	if gap, ok := t1.Observe("sub-a", 1); !ok || gap != 0 {
		t.Fatalf("t1.Observe = (%d, %v), want (0, true)", gap, ok)
	}
	if gap, ok := t2.Observe("sub-a", 1); !ok || gap != 0 {
		t.Fatalf("t2.Observe = (%d, %v), want (0, true)", gap, ok)
	}
	t1.Reset("sub-a")
	if gap, ok := t1.Observe("sub-a", 1); !ok || gap != 0 {
		t.Fatalf("t1.Observe after Reset = (%d, %v), want (0, true)", gap, ok)
	}
}
