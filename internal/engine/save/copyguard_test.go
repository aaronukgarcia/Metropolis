package save

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// managerByteCopy performs the SEC-020-class attack — a plain Manager
// struct copy — via a raw byte-for-byte memcpy through unsafe.Pointer,
// mirroring registryByteCopy (internal/foundation/registry/sec020_test.go)
// and stateByteCopy (internal/engine/debug/copyguard_test.go): a literal
// `m2 := *m` is legal, unsafe-free Go from outside this package too, but
// `go vet`'s copylocks check statically flags it, which VERIFY requires
// this package to pass. The byte-level copy produces identical runtime
// semantics (mu's bytes copied as-is, participants' slice header copied
// — aliasing the same backing array — self's pointer bytes copied
// unchanged) without a statically-flaggable copy expression.
func managerByteCopy(m *Manager) *Manager {
	c := new(Manager)
	*(*[unsafe.Sizeof(Manager{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Manager{})]byte)(unsafe.Pointer(m))
	return c
}

// wantManagerCopied asserts err is exactly ErrManagerCopied.
func wantManagerCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: ErrManagerCopied}) {
		t.Fatalf("%s on a struct-copied Manager: err = %v, want ErrManagerCopied (%s)", method, err, ErrManagerCopied)
	}
}

// TestCopyGuard_EveryGuardedMethod_RejectsStructCopy is this package's
// astgate finding fix: Manager had zero checkNotCopied coverage despite
// being a mutex-hazard candidate (mu sync.Mutex + aliasable participants
// slice). Every exported method, plus the specific unexported method
// astgate's live-tree scan flagged (writeBundleLocked), must reject a
// struct-copied receiver before touching any field — asserted here by
// name so a stripped guard on any ONE site identifies which one
// regressed.
func TestCopyGuard_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	root := t.TempDir()
	orig := NewManager(root, nil, "test-correlation")

	cp := managerByteCopy(orig)

	ctx := Context{WorldSeed: 1, CreatedAtTick: 1, GameMonth: 1, AppVersion: "test"}

	wantManagerCopied(t, "SaveManual", cp.SaveManual(ctx, "slot1"))
	wantManagerCopied(t, "Autosave", cp.Autosave(ctx))
	wantManagerCopied(t, "Milestone", cp.Milestone(ctx, Tier{Number: 1, Name: "t1"}))
	wantManagerCopied(t, "writeBundle", cp.writeBundle(ctx, root+"/x", Meta{}))
	wantManagerCopied(t, "writeBundleLocked", cp.writeBundleLocked(ctx, root+"/x", Meta{}))
	wantManagerCopied(t, "pruneAutosaves", cp.pruneAutosaves())

	_, _, loadErr := cp.Load(root)
	wantManagerCopied(t, "Load", loadErr)

	_, _, _, loadLatestErr := cp.LoadLatest()
	wantManagerCopied(t, "LoadLatest", loadLatestErr)

	if r := cp.Root(); r != "" {
		t.Fatalf("Root on a struct-copied Manager = %q, want \"\"", r)
	}

	// SetMaxDecodedBytes has no error return — prove it silently no-ops
	// rather than mutating the copy's field (which would be harmless in
	// isolation, but proves the guard actually ran).
	cp.SetMaxDecodedBytes(999)
	if cp.maxDecodedBytes == 999 {
		t.Fatalf("SetMaxDecodedBytes on a struct-copied Manager mutated maxDecodedBytes — guard did not run")
	}

	// The ORIGINAL must be completely unaffected and still fully usable
	// after every rejected call above.
	if err := orig.SaveManual(ctx, "slot1"); err != nil {
		t.Fatalf("original SaveManual after copy-attack calls: %v", err)
	}
	if orig.Root() != root {
		t.Fatalf("original Root() = %q, want %q", orig.Root(), root)
	}
}

// TestCopyGuard_ZeroValue_FailsClosed proves a bare `Manager{}` (never
// passed through NewManager, so self was never stored) is rejected the
// same way a copy is — every documented construction path is NewManager.
func TestCopyGuard_ZeroValue_FailsClosed(t *testing.T) {
	var m Manager
	ctx := Context{AppVersion: "test"}
	wantManagerCopied(t, "SaveManual", m.SaveManual(ctx, "x"))
	if r := m.Root(); r != "" {
		t.Fatalf("zero-value Manager.Root() = %q, want \"\"", r)
	}
}

// TestCopyGuard_CopyTakenWhileLockHeld_RejectedNotHung is the SEC-016
// pre-lock-ordering attack: a copy taken while the original's mu was
// held has mu bytes that read as permanently "locked" — nobody will
// ever Unlock() that specific copy's address. Because checkNotCopied is
// lock-free and runs BEFORE mu.TryLock() in every guarded method, the
// copy must be rejected promptly rather than deadlocking.
func TestCopyGuard_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	root := t.TempDir()
	orig := NewManager(root, nil, "test-correlation")

	orig.mu.Lock()
	cp := managerByteCopy(orig) // cp.mu bytes now read "locked"
	orig.mu.Unlock()

	ctx := Context{AppVersion: "test"}

	done := make(chan error, 1)
	go func() { done <- cp.Autosave(ctx) }()
	select {
	case err := <-done:
		wantManagerCopied(t, "Autosave", err)
	case <-time.After(3 * time.Second):
		t.Fatalf("REGRESSION: Autosave on a copy taken while mu was held did not return promptly — hung, the pre-fix failure mode")
	}

	// Original must still be fully usable.
	if err := orig.Autosave(ctx); err != nil {
		t.Fatalf("original Autosave after copy-during-lock attack: %v", err)
	}
}
