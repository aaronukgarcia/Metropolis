package ticker

// SEC-020 copy-guard regression: a struct-copied Screen (s2 := *s) with
// no copy-guard lets two goroutines each correctly lock their OWN copy's
// mu while mutating shared maps/slices — a fatal "concurrent map read and
// map write" under go test -race. The fixed behaviour verified below is
// what ships: checkNotCopied rejects every exported method call on a
// copy before mu or any aliased state is touched (mirrors
// ui.screen.demo's copyguard_test.go).

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen via unsafe
// byte-copy: a plain `s2 := *s1` is legal Go producing the identical
// attack shape, but go vet's copylocks check would flag the LITERAL
// assignment in this test file and fail `go vet` (one of this package's
// own gates). The byte-copy reaches the same struct-value copy through a
// route copylocks does not statically recognise (mirrors ui.screen.demo's
// screenCopy).
func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewArchive, "sub-1")
	s2 := screenCopy(s1)

	send := func(protocol.Command) error { return nil }

	// Error-returning methods reject a copy with MET-U704.
	if err := s2.Subscribe(ViewTicker, send); err == nil {
		t.Fatal("s2.Subscribe on a copy returned nil, want MET-U704")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrScreenCopied {
		t.Errorf("s2.Subscribe err = %v, want *errs.E with code %s", err, ErrScreenCopied)
	}
	if err := s2.SubscribeAll(send); err == nil {
		t.Fatal("s2.SubscribeAll on a copy returned nil, want MET-U704")
	}

	// BindSubscription (whose returned error a caller may discard) and the
	// void methods below are a silent no-op on a copy that never mutates
	// state visible through s1 (s2's subs/stale maps are the SAME aliased
	// maps as s1's, pre-guard).
	s2.BindSubscription(ViewTicker, "sub-2")
	if _, ok := s1.subs["sub-2"]; ok {
		t.Error("s2.BindSubscription on a copy leaked into s1.subs")
	}

	s2.UnbindSubscription("sub-1")
	if _, ok := s1.subs["sub-1"]; !ok {
		t.Error("s2.UnbindSubscription on a copy removed sub-1 from s1.subs")
	}

	s2.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion":1,"stories":[{"eventId":"evt-1","text":"a"}]}`),
	})
	if _, have := s1.Archive(); have {
		t.Error("s2.ApplyDelta on a copy applied an archive patch visible via s1.Archive")
	}

	s2.SetStale(ViewArchive, true)
	if s1.Stale(ViewArchive) {
		t.Error("s2.SetStale on a copy set staleness visible via s1.Stale")
	}

	// Accessors read back their documented zero value on a copy.
	if _, have := s2.Ticker(); have {
		t.Error("s2.Ticker() have=true on a copy")
	}
	if _, _, have := s2.Bulletin(); have {
		t.Error("s2.Bulletin() have=true on a copy")
	}
	if _, have := s2.Annual(); have {
		t.Error("s2.Annual() have=true on a copy")
	}
	if _, have := s2.Archive(); have {
		t.Error("s2.Archive() have=true on a copy")
	}
	if got := s2.Stale(ViewArchive); got {
		t.Error("s2.Stale() = true on a copy")
	}
	if s2.SearchActive() {
		t.Error("s2.SearchActive() = true on a copy")
	}
}

func TestScreen_CopyRaceNoLongerReproducible(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewArchive, "sub-1")
	s2 := screenCopy(s1)

	patch := []byte(`{"schemaVersion":1,"stories":[{"eventId":"evt-1","text":"a"}]}`)

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
			// A no-op on a copy: rejected by checkNotCopied before mu or
			// archive is ever touched. Load for -race, not an assertion
			// site — the assertions are below.
			s2.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: patch})
			atomic.AddInt64(&s2Iterations, 1)
		}
	}()
	wg.Wait()

	if got := atomic.LoadInt64(&s2Iterations); got != 500 {
		t.Errorf("s2 ApplyDelta iterations = %d, want 500 (all should run, all silently rejected)", got)
	}

	archive, have := s1.Archive()
	if !have || len(archive) != 1 || archive[0].EventID != "evt-1" {
		t.Errorf("s1.Archive() = %+v (have=%v), want exactly [{evt-1}] (s2's rejected calls must never have touched s1's data)", archive, have)
	}
}
