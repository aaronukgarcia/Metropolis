package demo

// SEC-020 copy-guard regression (BOW FEAT-018, Widowmaker's Destructive
// REJECT): a struct-copied Screen (s2 := *s) with no copy-guard let two
// goroutines each correctly lock their OWN copy's mu while mutating
// applyHousing's shared typologies map — under go test -race this
// crashed the process outright with "fatal error: concurrent map read
// and map write". The fixed behaviour verified below is what ships.

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into
// its own tiny helper (mirrors internal/foundation/data's storeCopy /
// internal/engine/world's w2Copy / internal/engine/core's e2Copy
// convention exactly, including the unsafe byte-copy): a plain
// `s2 := *s1` is legal, correct Go that produces the identical attack
// shape (the exact hazard Widowmaker's Destructive review reproduced on
// FEAT-018), but go vet's copylocks check would flag the LITERAL
// assignment at its own call site, which would make this test file
// itself fail `go vet ./internal/ui/screens/demo/...`, one of this
// package's own baseline gates. The byte-copy achieves the same
// struct-value copy (same mu bytes, same aliased subs/stale/typologies
// maps and ageMonths/personality/hoursByActivity/leisureTaste/
// typologyOrder slices) via a route copylocks does not statically
// recognise as a lock copy.
func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

// assertScreenCopiedCode fails t unless err is a registry error carrying
// ErrScreenCopied (MET-U503).
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
// exported methods (Subscribe/SubscribeAll) on a struct-copied Screen
// and confirms each is rejected fail-closed with MET-U503, then exercises
// the void/bool-returning methods and confirms a copy's calls are a
// silent no-op that never mutates or is visible via the original.
func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewHousing, "sub-1")

	// The legal-but-hazardous copy: the real-world attack (`s2 := *s1`)
	// is a plain dereference-and-reassign with no unsafe/reflect at all
	// — screenCopy reaches the identical struct-value copy via unsafe
	// only so THIS TEST FILE doesn't trip go vet's copylocks check on
	// its own literal assignment (see screenCopy's doc comment).
	s2 := screenCopy(s1)

	send := func(protocol.Command) error { return nil }

	assertScreenCopiedCode(t, s2.Subscribe(ViewPopulation, send))
	assertScreenCopiedCode(t, s2.SubscribeAll(send))

	// BindSubscription/UnbindSubscription/ApplyDelta/SetStale return no
	// error — a copy call must be a silent no-op, never observably
	// mutating anything s1 can see (s2's subs/typologies/stale maps are
	// the SAME aliased maps as s1's, pre-guard; the guard must fire
	// before the map is ever touched).
	s2.BindSubscription(ViewCommute, "sub-2")
	if _, ok := s1.subs["sub-2"]; ok {
		t.Error("s2.BindSubscription on a copy leaked into s1.subs")
	}

	s2.UnbindSubscription("sub-1")
	if _, ok := s1.subs["sub-1"]; !ok {
		t.Error("s2.UnbindSubscription on a copy removed sub-1 from s1.subs")
	}

	s2.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion":1,"full":false,"typologies":[{"typology":"terrace","demand":10,"stock":8}]}`),
	})
	if _, haveHousing := s1.Typologies(); haveHousing {
		t.Error("s2.ApplyDelta on a copy applied a housing patch visible via s1.Typologies")
	}

	s2.SetStale(ViewHousing, true)
	if s1.Stale(ViewHousing) {
		t.Error("s2.SetStale on a copy set staleness visible via s1.Stale")
	}
}

// TestScreen_AccessorsRejectCopy directly exercises every accessor
// method's guard on a struct-copied Screen — this is the one-to-one
// enumeration astgate's ratchet expects to see resolved (all 19
// FEAT-018 findings): every exported method calls checkNotCopied before
// touching aliased state, so a copy always reads back its documented
// zero-value/false result rather than aliasing the original's data.
func TestScreen_AccessorsRejectCopy(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewPopulation, "sub-pop")
	s2 := screenCopy(s1)

	if ages, have := s2.Population(); len(ages) != 0 || have {
		t.Errorf("s2.Population() = %v, %v, want empty/false", ages, have)
	}
	if traits, have := s2.Personality(); len(traits) != 0 || have {
		t.Errorf("s2.Personality() = %v, %v, want empty/false", traits, have)
	}
	if hours, have := s2.HoursByActivity(); len(hours) != 0 || have {
		t.Errorf("s2.HoursByActivity() = %v, %v, want empty/false", hours, have)
	}
	if taste, have := s2.LeisureTaste(); len(taste) != 0 || have {
		t.Errorf("s2.LeisureTaste() = %v, %v, want empty/false", taste, have)
	}
	if rows, have := s2.Typologies(); len(rows) != 0 || have {
		t.Errorf("s2.Typologies() = %v, %v, want empty/false", rows, have)
	}
	if fig, have := s2.Commute(); fig != (CommuteFigures{}) || have {
		t.Errorf("s2.Commute() = %v, %v, want zero/false", fig, have)
	}
	if got := s2.Stale(ViewPopulation); got {
		t.Errorf("s2.Stale() = %v, want false", got)
	}

	// The original s1 must be completely unaffected by s2's existence —
	// normal single-instance usage still works exactly as before through
	// the real *Screen.
	if _, have := s1.Population(); have {
		t.Error("s1.Population() reports havePopulation=true before any patch applied")
	}
}

// TestScreen_CopyRaceNoLongerReproducible re-runs Widowmaker's exact
// concurrency shape from the Destructive REJECT (concurrent ApplyDelta
// calls through the original and a copy, both mutating what used to be
// the shared applyHousing typologies map) under -race. Pre-fix this
// reproduced a genuine crash/data race reliably (2/5 manual runs during
// this fix round). Post-fix, the copy's ApplyDelta call is rejected by
// checkNotCopied before it ever reaches mu/typologies, so there is no
// write for -race to catch — this test proves the shape stays clean
// under sustained concurrent load, not that the crash magically stopped
// happening.
func TestScreen_CopyRaceNoLongerReproducible(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewHousing, "sub-1")
	s2 := screenCopy(s1)

	patch := []byte(`{"schemaVersion":1,"full":false,"typologies":[{"typology":"terrace","demand":10,"stock":8}]}`)

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
			// typologies is ever touched. This loop is load for -race to
			// try to catch a race in, not itself an assertion site — the
			// real assertions are below and in
			// TestScreen_AccessorsRejectCopy.
			s2.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: patch})
			atomic.AddInt64(&s2Iterations, 1)
		}
	}()
	wg.Wait()

	if got := atomic.LoadInt64(&s2Iterations); got != 500 {
		t.Errorf("s2 ApplyDelta iterations = %d, want 500 (all should have run, all silently rejected)", got)
	}

	rows, haveHousing := s1.Typologies()
	if !haveHousing {
		t.Fatal("s1.Typologies(): haveHousing = false, want true (s1's own ApplyDelta calls should have succeeded)")
	}
	if len(rows) != 1 || rows[0].Typology != "terrace" || rows[0].Demand != 10 || rows[0].Stock != 8 {
		t.Errorf("s1.Typologies() = %+v, want exactly [{terrace 10 8 false}] (s2's rejected calls must never have touched s1's data)", rows)
	}
}
