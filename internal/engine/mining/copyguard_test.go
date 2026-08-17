package mining

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is Finding 1's copyguard regression suite (SEC-020 family,
// modelled on internal/engine/world/copyguard_test.go). A same-package
// value copy `cp := *m` gets its OWN independently-zeroed mu but ALIASES
// m.placed (a map, a reference type) — the exact "two locks, one referent"
// shape. Every lock-touching *DepositMap method must reject the copy with
// ErrDepositMapCopied before its own mu (or the aliased map) is touched.

// TestDepositMapValueCopyRejected proves a struct copy is rejected by all
// four guarded entry points, and that none of the rejected calls corrupts
// the original's map through the aliased reference.
func TestDepositMapValueCopyRejected(t *testing.T) {
	m := mustNewDepositMap(t, 1, newWorld(t), realParams(t))
	// Seed one entry directly (same package may touch the unexported map)
	// so the copy's aliasing is observable, without paying a full 40000-cell
	// shuffle.
	m.placed[depositKey{0, 0, 5, 5}] = LocatedDeposit{
		Tile:    world.TileCoord{X: 0, Y: 0},
		Local:   world.CellLocal{Row: 5, Col: 5},
		Deposit: Deposit{Type: DepositCoal},
	}

	// The attack: a plain struct copy. Legal Go, no unsafe, no reflect —
	// every field of DepositMap is unexported, but that does not stop a
	// same-package copy (and the shuffle's own callers hold a *DepositMap,
	// so this is the realistic shape any future helper that forgot a
	// pointer receiver could introduce). The byte-copy route mirrors
	// world's w2Copy: a literal `cp := *m` would trip go vet copylocks.
	cp := depositMapCopy(m)

	if err := cp.ShuffleTile(world.TileCoord{X: 0, Y: 0}, cid()); !errors.Is(err, &errs.E{Code: ErrDepositMapCopied}) {
		t.Fatalf("cp.ShuffleTile on a value-copied DepositMap: err = %v, want ErrDepositMapCopied", err)
	}
	if _, _, err := cp.DepositAt(world.TileCoord{X: 0, Y: 0}, world.CellLocal{Row: 0, Col: 0}); !errors.Is(err, &errs.E{Code: ErrDepositMapCopied}) {
		t.Fatalf("cp.DepositAt on a value-copied DepositMap: err = %v, want ErrDepositMapCopied", err)
	}
	// TileDeposits/AllDeposits have no error channel (AC-1's plain-read
	// surface); the guard rejects by returning nil before the lock is touched.
	if got := cp.TileDeposits(world.TileCoord{X: 0, Y: 0}); got != nil {
		t.Fatalf("cp.TileDeposits on a value-copied DepositMap returned %d deposits, want nil (rejected)", len(got))
	}
	if got := cp.AllDeposits(); got != nil {
		t.Fatalf("cp.AllDeposits on a value-copied DepositMap returned %d deposits, want nil (rejected)", len(got))
	}

	// Confirm the original still sees exactly the one seeded entry — none of
	// the rejected calls above wrote into the ALIASED map.
	if got := m.AllDeposits(); len(got) != 1 {
		t.Fatalf("original DepositMap has %d deposits after rejected copy calls, want 1 (aliased map was corrupted)", len(got))
	}
}

// TestDepositMapCopyWhileLockedRejectedNotHung proves the SEC-016 ordering:
// a copy taken while the original holds its write lock carries mu's bytes
// as "currently locked", so a guard that ran AFTER the lock would block
// forever (nothing will ever Unlock that specific copy's address). The
// guard must run BEFORE the lock — the copy is rejected promptly, never
// hung. The timeout turns a regression into a fast failure rather than a
// 10-minute test-timeout hang.
func TestDepositMapCopyWhileLockedRejectedNotHung(t *testing.T) {
	m := mustNewDepositMap(t, 1, newWorld(t), realParams(t))

	m.mu.Lock()
	cp := depositMapCopy(m) // copy taken while the original holds its write lock
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- cp.ShuffleTile(world.TileCoord{X: 0, Y: 0}, cid())
	}()

	select {
	case err := <-done:
		if !errors.Is(err, &errs.E{Code: ErrDepositMapCopied}) {
			t.Fatalf("cp.ShuffleTile on a copy-taken-while-locked DepositMap: err = %v, want ErrDepositMapCopied", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copy-taken-while-locked DepositMap HUNG: checkNotCopied did not reject before the copy's lock was acquired")
	}
}

// depositMapCopy takes a same-package value copy of *DepositMap, isolated
// into its own helper so both tests document the attack shape identically
// (mirrors world's w2Copy and engine.core's e2Copy conventions, including
// the unsafe byte-copy — a plain `cp := *m` is legal, correct Go that
// produces the identical attack shape, but go vet's copylocks check would
// flag the LITERAL assignment at its own call site, which would make this
// test file itself fail `go vet ./internal/engine/mining/...`. The
// byte-copy achieves the same struct-value copy via a route copylocks does
// not statically recognise as a lock copy).
func depositMapCopy(m *DepositMap) *DepositMap {
	c := new(DepositMap)
	*(*[unsafe.Sizeof(DepositMap{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(DepositMap{})]byte)(unsafe.Pointer(m))
	return c
}
