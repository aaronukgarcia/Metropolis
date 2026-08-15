package dash

import (
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// dashboardByteCopy performs SEC-020's attack — a byte-for-byte struct
// copy of *Dashboard — via unsafe.Pointer, mirroring mapResolverByteCopy
// in copyguard_test.go: a literal `d2 := *d` is legal Go but is exactly
// what `go vet`'s copylocks check flags, and this package's VERIFY step
// requires `go vet ./...` clean. The byte-level copy produces identical
// runtime semantics (mu's bytes copied as-is, layout's tiles slice header
// copied — aliasing the same backing array — self's pointer bytes copied
// unchanged) without a statically flaggable copy expression.
func dashboardByteCopy(d *Dashboard) *Dashboard {
	c := new(Dashboard)
	*(*[unsafe.Sizeof(Dashboard{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Dashboard{})]byte)(unsafe.Pointer(d))
	return c
}

func threeTileDashboard(t *testing.T) *Dashboard {
	t.Helper()
	l := NewLayout("f1")
	for _, id := range []string{"a", "b", "c"} {
		tile, err := NewBignumTile(id, DrillTarget{ViewName: "f1.viewport"}, BignumSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.AddTile(tile); err != nil {
			t.Fatal(err)
		}
	}
	return NewDashboard(l, nil, nil)
}

func dashboardTileIDs(l Layout) []string {
	ids := make([]string, 0, l.Len())
	for _, t := range l.tiles {
		ids = append(ids, t.id)
	}
	return ids
}

// TestDashboardCopyguard_RejectsStructCopy is the SEC-064 core case: a
// struct copy of a Dashboard is rejected fail-closed — its RemoveTile
// returns the copy-rejection error BEFORE it can mutate the shared tiles
// backing array, and the original's layout is untouched.
func TestDashboardCopyguard_RejectsStructCopy(t *testing.T) {
	d := threeTileDashboard(t)
	cp := dashboardByteCopy(d)

	err := cp.RemoveTile("a")
	if err == nil {
		t.Fatal("copy.RemoveTile returned nil error, want copy-rejection")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != codeDashboardCopied {
		t.Fatalf("copy.RemoveTile error = %v, want %s", err, codeDashboardCopied)
	}

	if got := dashboardTileIDs(d.Layout()); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("original layout = %v, want [a b c] (the copy's mutation leaked into the original)", got)
	}
}

// TestDashboardCopyguard_ZeroValue_FailsClosed proves `var d Dashboard`
// (never passed through NewDashboard, so self was never stored) is
// rejected the same way a copy is, and never panics.
func TestDashboardCopyguard_ZeroValue_FailsClosed(t *testing.T) {
	var d Dashboard
	if err := d.RemoveTile("a"); err == nil {
		t.Fatal("zero-value Dashboard.RemoveTile returned nil error, want copy-rejection")
	}
	if got := d.Layout(); got.Len() != 0 {
		t.Fatalf("zero-value Dashboard.Layout() = %d tiles, want 0 (fail-closed)", got.Len())
	}
	d.SetLayout(NewLayout("f1")) // must not panic
}

// TestDashboardCopyguard_CopyTakenWhileLockHeld_NoHang is the deterministic
// SEC-016 "copy taken mid-lock" attack: lock mu, take the byte copy while
// it is held (so the copy's mu bytes read "currently locked"), unlock the
// original, then call the copy. The copy's calls must return promptly
// because checkNotCopied is lock-free and runs BEFORE mu.Lock()/RLock() —
// a guard placed after the lock would block forever on the copy's own
// permanently-unrecoverable mu. Bounded at 3s so a regression hangs the
// test promptly rather than Go's 10-minute default.
func TestDashboardCopyguard_CopyTakenWhileLockHeld_NoHang(t *testing.T) {
	d := threeTileDashboard(t)
	d.mu.Lock()
	cp := dashboardByteCopy(d) // cp.mu's bytes now read "locked"
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		_ = cp.RemoveTile("a")
		cp.SetLayout(NewLayout("f1"))
		_ = cp.Layout()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SEC-020 REGRESSION: Dashboard copy taken while mu was held did not return within 3s — hung")
	}

	if got := dashboardTileIDs(d.Layout()); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("original layout after mid-lock copy attack = %v, want [a b c]", got)
	}
}
