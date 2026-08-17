package build

// SEC-020 copy-guard regression: a struct-copied Screen (s2 := *s) with
// no copy-guard gets its own mu but ALIASES the original's subs map and
// per-surface slices/pointers — two lock domains over one referent, the
// shape that crashes under concurrent map/slice mutation. The fixed
// behaviour verified below (checkNotCopied rejects every exported method
// on a copy, fail-closed before mu or aliased state is touched) is what
// ships.

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into its
// own tiny helper (mirrors trade.screenCopy exactly, including the unsafe
// byte-copy): a plain `s2 := *s1` is legal, correct Go that produces the
// identical attack shape, but go vet's copylocks check would flag the
// literal assignment at its own call site. The byte-copy achieves the same
// struct-value copy via a route copylocks does not statically recognise as
// a lock copy.
func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

// assertScreenCopiedCode fails t unless err is a registry error carrying
// ErrScreenCopied (MET-V202).
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

	s2.BindSubscription("sub-2")
	if _, ok := s1.subs["sub-2"]; ok {
		t.Error("s2.BindSubscription on a copy leaked into s1.subs")
	}
	s2.UnbindSubscription("sub-1")
	if _, ok := s1.subs["sub-1"]; !ok {
		t.Error("s2.UnbindSubscription on a copy removed sub-1 from s1.subs")
	}
	s2.ApplyDelta(protocolDelta(t, "sub-1", fullPatch()))
	if _, have := s1.Queue(); have {
		t.Error("s2.ApplyDelta on a copy applied a patch visible via s1.Queue")
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
	s1.BindSubscription("sub-build")
	s2 := screenCopy(s1)

	if zones, have := s2.Zones(); len(zones) != 0 || have {
		t.Errorf("s2.Zones() = %v, %v, want empty/false", zones, have)
	}
	if orders, have := s2.Queue(); len(orders) != 0 || have {
		t.Errorf("s2.Queue() = %v, %v, want empty/false", orders, have)
	}
	if entries, have := s2.Catalogue(); len(entries) != 0 || have {
		t.Errorf("s2.Catalogue() = %v, %v, want empty/false", entries, have)
	}
	if _, have := s2.LandPrice(); have {
		t.Error("s2.LandPrice() reported have=true before any patch applied")
	}
	if _, have := s2.Demolition(); have {
		t.Error("s2.Demolition() reported have=true before any patch applied")
	}
	if got := s2.Stale(); got {
		t.Errorf("s2.Stale() = %v, want false", got)
	}
	if got := s2.HaveData(); got {
		t.Errorf("s2.HaveData() = %v, want false", got)
	}

	if _, have := s1.Queue(); have {
		t.Error("s1.Queue() reports have=true before any patch applied")
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
	queue, have := s1.Queue()
	if !have || len(queue) != 2 {
		t.Fatalf("s1.Queue() = %d/%v, want 2/true (s1's own ApplyDelta calls should have succeeded)", len(queue), have)
	}
}
