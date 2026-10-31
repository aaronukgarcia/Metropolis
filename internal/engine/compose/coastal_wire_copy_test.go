package compose

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1145: coastalWiring is a mutex-bearing candidate type for the astgate
// copy-guard ratchet. A struct copy gets its own independent mu while still
// aliasing api/svc, so each lock-taking entry point must reject a copy BEFORE
// touching mu and fail closed. These tests prove the guard can fail: they take
// a raw byte copy (the same shape as debug's screenByteCopy, sidestepping
// vet's copylocks) and assert the rejection.

func newCopyGuardWiring() *coastalWiring {
	w := &coastalWiring{lastMonth: -1, lastGranted: 5}
	w.self.Store(w)
	return w
}

func coastalByteCopy(w *coastalWiring) *coastalWiring {
	c := new(coastalWiring)
	*(*[unsafe.Sizeof(coastalWiring{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(coastalWiring{})]byte)(unsafe.Pointer(w))
	return c
}

func TestCoastalWiring_StructCopy_FailsClosed(t *testing.T) {
	orig := newCopyGuardWiring()
	if !orig.checkNotCopied() {
		t.Fatal("constructed coastalWiring rejected as a copy")
	}
	// Take the copy while orig.mu is HELD: the copy captures a "locked" mutex,
	// so a guard that reached cp.mu would park forever (SEC-016). The
	// pre-lock guard must return without touching it.
	orig.mu.Lock()
	cp := coastalByteCopy(orig)
	orig.mu.Unlock()
	if cp.checkNotCopied() {
		t.Fatal("struct copy accepted by checkNotCopied")
	}
	err := cp.ensureBaselineServices()
	var e *errs.E
	if err == nil || !errors.As(err, &e) || e.Code != ErrCoastalStateInvalid {
		t.Fatalf("copy.ensureBaselineServices() = %v, want code %s", err, ErrCoastalStateInvalid)
	}
	if err := cp.bind(&simState{}); err == nil {
		t.Fatal("copy.bind() returned nil, want the copy rejection")
	}
	// A bare literal never went through newCoastalWiring: nil self == rejected.
	if (&coastalWiring{}).checkNotCopied() {
		t.Fatal("bare literal accepted by checkNotCopied")
	}
}

func TestCoastalWiring_StructCopy_SimStateEntryPointsFailClosed(t *testing.T) {
	orig := newCopyGuardWiring()
	st := &simState{coastalW: coastalByteCopy(orig)}
	// migrationInflow: the copy's lastGranted (5) must NOT be added.
	if got := st.migrationInflow(7); got != 7 {
		t.Fatalf("copy migrationInflow = %d, want attract's 7 alone (fail closed)", got)
	}
	// The genuine value still adds its coastal term (the guard is not a blanket reject).
	st.coastalW = orig
	if got := st.migrationInflow(7); got != 12 {
		t.Fatalf("original migrationInflow = %d, want 12", got)
	}
}
