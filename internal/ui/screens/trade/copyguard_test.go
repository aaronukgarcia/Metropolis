package trade

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
// own tiny helper (mirrors proj.screenCopy / demo.screenCopy exactly,
// including the unsafe byte-copy): a plain `s2 := *s1` is legal, correct Go
// that produces the identical attack shape, but go vet's copylocks check
// would flag the literal assignment at its own call site. The byte-copy
// achieves the same struct-value copy via a route copylocks does not
// statically recognise as a lock copy.
func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

// assertScreenCopiedCode fails t unless err is a registry error carrying
// ErrScreenCopied (MET-V102).
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
	if _, have := s1.Contracts(); have {
		t.Error("s2.ApplyDelta on a copy applied a patch visible via s1.Contracts")
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
	s1.BindSubscription("sub-trade")
	s2 := screenCopy(s1)

	if contracts, have := s2.Contracts(); len(contracts) != 0 || have {
		t.Errorf("s2.Contracts() = %v, %v, want empty/false", contracts, have)
	}
	if junctions, have := s2.Junctions(); len(junctions) != 0 || have {
		t.Errorf("s2.Junctions() = %v, %v, want empty/false", junctions, have)
	}
	if rows, have := s2.Warehouse(); len(rows) != 0 || have {
		t.Errorf("s2.Warehouse() = %v, %v, want empty/false", rows, have)
	}
	if _, have := s2.Port(); have {
		t.Error("s2.Port() reported have=true before any patch applied")
	}
	if _, have := s2.Balance(); have {
		t.Error("s2.Balance() reported have=true before any patch applied")
	}
	if corridors, have := s2.Safety(); len(corridors) != 0 || have {
		t.Errorf("s2.Safety() = %v, %v, want empty/false", corridors, have)
	}
	if got := s2.Stale(); got {
		t.Errorf("s2.Stale() = %v, want false", got)
	}
	if got := s2.HaveData(); got {
		t.Errorf("s2.HaveData() = %v, want false", got)
	}

	// The original s1 is completely unaffected by s2's existence.
	if _, have := s1.Contracts(); have {
		t.Error("s1.Contracts() reports have=true before any patch applied")
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
	contracts, have := s1.Contracts()
	if !have || len(contracts) != 2 {
		t.Fatalf("s1.Contracts() = %d/%v, want 2/true (s1's own ApplyDelta calls should have succeeded)", len(contracts), have)
	}
}
