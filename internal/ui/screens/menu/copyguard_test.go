package menu

// SEC-020 copy-guard regression: a struct-copied Screen (s2 := *s) with no
// copy-guard would let two goroutines each correctly lock their OWN copy's
// mu while mutating shared aliased state — the "two locks, one referent"
// shape. The guard (copyguard.go's checkNotCopied) rejects every exported
// method call on a copy before mu is ever touched. This test proves the
// rejection is live, not merely documented.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// screenCopy takes a same-package value copy of *Screen via unsafe byte
// copy (mirrors ui.screen.demo's screenCopy): a plain `s2 := *s1` is legal
// Go producing the identical attack shape, but go vet's copylocks check
// would flag the literal assignment at its own call site, failing this
// package's `go vet` baseline gate.
func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

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

func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewSession, "sub-1")

	s2 := screenCopy(s1)

	send := func(protocol.Command) error { return nil }

	// Error-returning methods reject with ErrScreenCopied.
	assertScreenCopiedCode(t, s2.Subscribe(ViewSession, send))
	assertScreenCopiedCode(t, s2.SubscribeSession(send))
	assertScreenCopiedCode(t, s2.Refresh())
	assertScreenCopiedCode(t, s2.Load("/s/x", send))
	assertScreenCopiedCode(t, s2.Save("n", send))
	assertScreenCopiedCode(t, s2.Delete("/s/x", send))
	assertScreenCopiedCode(t, s2.NewGame("1", false, send))
	assertScreenCopiedCode(t, s2.OpenLayoutEditor())

	// Void methods must be silent no-ops that never leak into s1.
	s2.BindSubscription(ViewSession, "sub-2")
	if _, ok := s1.subs["sub-2"]; ok {
		t.Error("s2.BindSubscription on a copy leaked into s1.subs")
	}
	s2.UnbindSubscription("sub-1")
	if _, ok := s1.subs["sub-1"]; !ok {
		t.Error("s2.UnbindSubscription on a copy removed sub-1 from s1.subs")
	}
	s2.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          mustJSON(t, wireSessionPatch{SchemaVersion: 1, WorldSeed: 1}),
	})
	if _, have := s1.Session(); have {
		t.Error("s2.ApplyDelta on a copy applied a session patch visible via s1.Session")
	}
	s2.SetStale(ViewSession, true)
	if s1.Stale(ViewSession) {
		t.Error("s2.SetStale on a copy set staleness visible via s1.Stale")
	}
	s2.SetSettingsSchema([]SettingSpec{{Key: "k", Label: "K", Kind: SettingBool}})
	if _, have := s1.SettingsSchema(); have {
		t.Error("s2.SetSettingsSchema on a copy set the schema visible via s1.SettingsSchema")
	}
	s2.SetSettingValue("k", "v")
	if len(s1.settingValues) != 0 {
		t.Error("s2.SetSettingValue on a copy wrote into s1.settingValues")
	}
}

func TestScreen_AccessorsRejectCopy(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewSession, "sub-pop")
	s2 := screenCopy(s1)

	if _, have := s2.Session(); have {
		t.Errorf("s2.Session() have = true, want false")
	}
	if entries := s2.SaveEntries(); len(entries) != 0 {
		t.Errorf("s2.SaveEntries() = %v, want empty", entries)
	}
	if errs := s2.SaveListErrors(); len(errs) != 0 {
		t.Errorf("s2.SaveListErrors() = %v, want empty", errs)
	}
	if got := s2.SaveListUnavailable(); got != "unavailable" {
		t.Errorf("s2.SaveListUnavailable() = %q, want %q", got, "unavailable")
	}
	if _, have := s2.SettingsSchema(); have {
		t.Errorf("s2.SettingsSchema() have = true, want false")
	}
	if vals := s2.SettingValues(); len(vals) != 0 {
		t.Errorf("s2.SettingValues() = %v, want empty", vals)
	}
	if got := s2.SettingsUnavailable(); got != "unavailable" {
		t.Errorf("s2.SettingsUnavailable() = %q, want %q", got, "unavailable")
	}
	if _, have := s2.SelectedKeymap(); have {
		t.Errorf("s2.SelectedKeymap() have = true, want false")
	}
	if _, have := s2.SelectedLayoutProfile(); have {
		t.Errorf("s2.SelectedLayoutProfile() have = true, want false")
	}
	if _, have := s2.LastNewGameRequest(); have {
		t.Errorf("s2.LastNewGameRequest() have = true, want false")
	}
	if got := s2.Stale(ViewSession); got {
		t.Errorf("s2.Stale() = %v, want false", got)
	}
}

// TestScreen_SelectKeymapOnCopyIsNoop proves the keymap select path is
// guarded too — a copy's SelectKeymap must not apply the keymap to g.
func TestScreen_SelectKeymapOnCopyIsNoop(t *testing.T) {
	s1 := New("corr-original")
	s2 := screenCopy(s1)
	g := keys.NewKeyGrammar(keys.ClockFunc(func() time.Time { return time.Time{} }), time.Minute, 3, "corr-g")
	km, _ := keys.ParseKeymap([]byte(`{"version":1,"bindings":{"b":"b"}}`))
	rejected, err := s2.SelectKeymap(km, g)
	assertScreenCopiedCode(t, err)
	if len(rejected) != 0 {
		t.Errorf("s2.SelectKeymap() rejected = %+v, want empty (copy rejected fail-closed)", rejected)
	}
	if _, have := s1.SelectedKeymap(); have {
		t.Error("s2.SelectKeymap on a copy set s1's selected keymap")
	}
}

// TestScreen_CopyRaceNoLongerReproducible re-runs the concurrent
// apply-via-original-and-copy shape under -race: the copy's calls are
// rejected by checkNotCopied before mu/state is touched, so there is no
// write for -race to catch.
func TestScreen_CopyRaceNoLongerReproducible(t *testing.T) {
	s1 := New("corr-original")
	s1.BindSubscription(ViewSession, "sub-1")
	s2 := screenCopy(s1)

	patch := mustJSON(t, wireSessionPatch{SchemaVersion: 1, WorldSeed: 1})

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
		t.Errorf("s2 ApplyDelta iterations = %d, want 500", got)
	}
	if _, have := s1.Session(); !have {
		t.Error("s1.Session() have = false, want true (s1's own ApplyDelta calls should have succeeded)")
	}
}
