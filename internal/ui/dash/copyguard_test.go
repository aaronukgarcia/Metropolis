package dash

import (
	"testing"
	"time"
	"unsafe"
)

// mapResolverByteCopy performs SEC-020's attack — a plain MapResolver
// struct copy — via a raw byte-for-byte memcpy through unsafe.Pointer,
// mirroring internal/ui/screens/map/sec020_test.go's mapScreenByteCopy
// (itself mirroring internal/engine/debug/copyguard_test.go's
// stateByteCopy): a literal `m2 := *m` is legal, unsafe-free Go but is
// exactly what `go vet`'s copylocks check flags, and this package's
// VERIFY step requires `go vet ./...` clean. The byte-level copy
// produces IDENTICAL runtime semantics (mu's bytes copied as-is, live's
// map header copied — aliasing the same map — self's pointer bytes copied
// unchanged) without a statically flaggable copy expression.
func mapResolverByteCopy(m *MapResolver) *MapResolver {
	c := new(MapResolver)
	*(*[unsafe.Sizeof(MapResolver{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(MapResolver{})]byte)(unsafe.Pointer(m))
	return c
}

// TestMapResolverCopyguard_RejectsStructCopy is the SEC-020 core case: a
// struct copy of a MapResolver is rejected fail-closed — its Resolve
// reads as "not live" even for a target the original sees as live, and
// its Mark is a no-op that never writes through the aliased map to the
// original.
func TestMapResolverCopyguard_RejectsStructCopy(t *testing.T) {
	live := DrillTarget{ViewName: "f2.ledger", EntityID: "line-1"}
	orig := NewMapResolver()
	orig.Mark(live)

	cp := mapResolverByteCopy(orig)

	if cp.Resolve(live) {
		t.Fatal("copy.Resolve(live) = true, want false (copy must be rejected fail-closed)")
	}
	if !orig.Resolve(live) {
		t.Fatal("original.Resolve(live) = false, want true (sanity: the original must be unaffected by the copy)")
	}

	// The copy's Mark must be rejected BEFORE it can write through the
	// aliased map — otherwise the original would see `other` as live.
	other := DrillTarget{ViewName: "f3.market", EntityID: "x"}
	cp.Mark(other)
	if orig.Resolve(other) {
		t.Fatal("copy.Mark(other) leaked into the original's map — the copy's Mark was not rejected")
	}
}

// TestMapResolverCopyguard_ZeroValue_FailsClosed proves `var m MapResolver`
// (never passed through NewMapResolver, so self was never stored) is
// rejected the same way a copy is.
func TestMapResolverCopyguard_ZeroValue_FailsClosed(t *testing.T) {
	var m MapResolver
	if m.Resolve(DrillTarget{ViewName: "f2.ledger"}) {
		t.Fatal("zero-value MapResolver.Resolve() = true, want false (fail-closed)")
	}
	m.Mark(DrillTarget{ViewName: "f3.market"}) // must not panic
}

// TestMapResolverCopyguard_CopyTakenWhileLockHeld_NoHang is the
// deterministic SEC-016 "copy taken mid-lock" attack: lock mu, take the
// byte copy while it is held (so the copy's mu bytes read as "currently
// locked, no waiters"), unlock the original, then call the copy. The
// copy's calls must return promptly because checkNotCopied is lock-free
// and runs BEFORE mu.Lock()/RLock() — a guard placed after the lock
// would block forever on the copy's own permanently-unrecoverable mu.
// Bounded so a regression hangs the test at 3s rather than Go's
// 10-minute default (same rationale as map's runBoundedSEC020).
func TestMapResolverCopyguard_CopyTakenWhileLockHeld_NoHang(t *testing.T) {
	orig := NewMapResolver()
	orig.mu.Lock()
	cp := mapResolverByteCopy(orig) // cp.mu's bytes now read "locked" — byte-identical to orig.mu at this instant
	orig.mu.Unlock()

	done := make(chan struct{})
	go func() {
		_ = cp.Resolve(DrillTarget{ViewName: "f2.ledger"})
		cp.Mark(DrillTarget{ViewName: "f3.market"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SEC-020 REGRESSION: copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode")
	}

	// The original must still be fully usable afterward.
	if orig.Resolve(DrillTarget{ViewName: "f2.ledger"}) {
		t.Fatal("original.Resolve() on an unmarked target = true, want false (original's state must not be polluted)")
	}
	orig.Mark(DrillTarget{ViewName: "f4.energy"})
	if !orig.Resolve(DrillTarget{ViewName: "f4.energy"}) {
		t.Fatal("original.Resolve() after Mark = false, want true (original must remain usable)")
	}
}
