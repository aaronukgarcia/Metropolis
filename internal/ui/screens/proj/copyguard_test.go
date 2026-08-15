package proj

// SEC-020 copy-guard regression: a struct-copied Screen (s2 := *s) with
// no copy-guard gets its own mu but ALIASES the original's subs map and
// curves/crossings slices — two lock domains over one referent, the shape
// that crashes under concurrent map/slice mutation. The fixed behaviour
// verified below (checkNotCopied rejects every exported method on a copy,
// fail-closed before mu or aliased state is touched) is what ships.

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into its
// own tiny helper (mirrors demo.screenCopy / world.w2Copy / core.e2Copy
// exactly, including the unsafe byte-copy): a plain `s2 := *s1` is legal,
// correct Go that produces the identical attack shape, but go vet's
// copylocks check would flag the literal assignment at its own call site,
// which would fail `go vet ./internal/ui/screens/proj/...`. The byte-copy
// achieves the same struct-value copy (same mu bytes, same aliased subs
// map and curves/crossings slices) via a route copylocks does not
// statically recognise as a lock copy.
func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

// assertScreenCopiedCode fails t unless err is a registry error carrying
// ErrScreenCopied (MET-V003).
func assertScreenCopiedCode(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != ErrScreenCopied {
		t.Errorf("e.Code = %s, want %s", e.Code, ErrScreenCopied)
	}
}

// TestScreen_CopyDetectedAndRejected exercises the error-returning
// exported method (Subscribe) and the void/bool-returning methods on a
// struct-copied Screen, confirming each is rejected fail-closed without
// observably mutating anything the original can see.
func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription("sub-1")
	s2 := screenCopy(s1)

	assertScreenCopiedCode(t, s2.Subscribe(func(protocol.Command) error { return nil }))

	// BindSubscription/UnbindSubscription/ApplyDelta/SetStale return no
	// error — a copy call must be a silent no-op, never observably
	// mutating anything s1 can see.
	s2.BindSubscription("sub-2")
	if _, ok := s1.subs["sub-2"]; ok {
		t.Error("s2.BindSubscription on a copy leaked into s1.subs")
	}
	s2.UnbindSubscription("sub-1")
	if _, ok := s1.subs["sub-1"]; !ok {
		t.Error("s2.UnbindSubscription on a copy removed sub-1 from s1.subs")
	}
	s2.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, fullPatch())})
	if _, have := s1.Curves(); have {
		t.Error("s2.ApplyDelta on a copy applied a patch visible via s1.Curves")
	}
	s2.SetStale(true)
	if s1.Stale() {
		t.Error("s2.SetStale on a copy set staleness visible via s1.Stale")
	}
}

// TestScreen_AccessorsRejectCopy directly exercises every accessor
// method's guard on a struct-copied Screen: a copy always reads back its
// documented zero-value/false result rather than aliasing the original's
// data.
func TestScreen_AccessorsRejectCopy(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription("sub-proj")
	s2 := screenCopy(s1)

	if curves, have := s2.Curves(); len(curves) != 0 || have {
		t.Errorf("s2.Curves() = %v, %v, want empty/false", curves, have)
	}
	if crossings, have := s2.Crossings(); len(crossings) != 0 || have {
		t.Errorf("s2.Crossings() = %v, %v, want empty/false", crossings, have)
	}
	if rate, ok, have := s2.RateOutlook(); len(rate.History) != 0 || len(rate.Projection) != 0 || ok || have {
		t.Errorf("s2.RateOutlook() = %v, %v, %v, want empty/false/false", rate, ok, have)
	}
	if months, ok := s2.HorizonMonths(); months != 0 || ok {
		t.Errorf("s2.HorizonMonths() = %d, %v, want 0/false", months, ok)
	}
	if got := s2.Stale(); got {
		t.Errorf("s2.Stale() = %v, want false", got)
	}

	// The original s1 is completely unaffected by s2's existence.
	if _, have := s1.Curves(); have {
		t.Error("s1.Curves() reports haveData=true before any patch applied")
	}
}

// TestScreen_CopyRaceNoLongerReproducible re-runs the SEC-020 concurrent
// shape (ApplyDelta through the original and a copy, both mutating what
// used to be shared state) under -race. Post-fix, the copy's calls are
// rejected before mu or the shared slices are touched.
func TestScreen_CopyRaceNoLongerReproducible(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription("sub-1")
	s2 := screenCopy(s1)

	patch := mustJSON(t, fullPatch())

	var wg sync.WaitGroup
	var s2Iterations int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			s1.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: patch})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			s2.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: patch})
			atomic.AddInt64(&s2Iterations, 1)
		}
	}()
	wg.Wait()

	if got := atomic.LoadInt64(&s2Iterations); got != 500 {
		t.Errorf("s2 ApplyDelta iterations = %d, want 500 (all should have run, all silently rejected)", got)
	}
	curves, have := s1.Curves()
	if !have || len(curves) != 2 {
		t.Fatalf("s1.Curves() = %d/%v, want 2/true (s1's own ApplyDelta calls should have succeeded)", len(curves), have)
	}
}
