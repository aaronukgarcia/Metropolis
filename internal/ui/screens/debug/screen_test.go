package debug_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	debug "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
)

func newTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	stubMod := fakeModule{name: "crime", version: "0.1.0", health: registry.HealthOK}
	if err := reg.Register("crime", nil, stubMod, registry.WithCanToggle(true)); err != nil {
		t.Fatalf("register crime: %v", err)
	}
	noToggleMod := fakeModule{name: "finance", version: "0.2.0", health: registry.HealthOK}
	if err := reg.Register("finance", nil, noToggleMod, registry.WithCanToggle(false)); err != nil {
		t.Fatalf("register finance: %v", err)
	}
	return reg
}

type fakeModule struct {
	name, version string
	health        registry.Health
}

func (f fakeModule) Name() string            { return f.name }
func (f fakeModule) Version() string         { return f.version }
func (f fakeModule) Health() registry.Health { return f.health }

// --- AC-4/AC-5/AC-12: guarded toggle ---

func TestRequestToggle_Success_EmitsWorldEvent(t *testing.T) {
	reg := newTestRegistry(t)
	s := debug.NewScreen(reg, "corr-1")

	if err := s.RequestToggle("crime", registry.StatusStub, "crime"); err != nil {
		t.Fatalf("RequestToggle: %v", err)
	}

	entry, ok := reg.Get("crime")
	if !ok || entry.Status != registry.StatusStub {
		t.Fatalf("crime status = %+v, want StatusStub", entry)
	}

	snap := s.Collect()
	found := false
	for _, e := range snap.Events {
		if e == "crime module -> STUB" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Events = %v, want an entry for the crime toggle", snap.Events)
	}
}

func TestRequestToggle_NonToggleableModule_RejectedAndUnchanged(t *testing.T) {
	reg := newTestRegistry(t)
	s := debug.NewScreen(reg, "corr-1")

	before, _ := reg.Get("finance")

	err := s.RequestToggle("finance", registry.StatusOff, "finance")
	if err == nil {
		t.Fatal("RequestToggle on a non-CanToggle module: want error, got nil")
	}

	after, _ := reg.Get("finance")
	if after != before {
		t.Fatalf("finance entry changed despite CanToggle=false: before=%+v after=%+v", before, after)
	}

	if s.LastToggleError() == nil {
		t.Fatal("LastToggleError() = nil, want the rejection surfaced")
	}

	snap := s.Collect()
	for _, e := range snap.Events {
		if e == "finance module -> OFF" {
			t.Fatalf("Events contains a ticker entry for a rejected toggle: %v", snap.Events)
		}
	}
}

func TestRequestToggle_WrongConfirmToken_RejectedAndUnchanged(t *testing.T) {
	reg := newTestRegistry(t)
	s := debug.NewScreen(reg, "corr-1")

	before, _ := reg.Get("crime")

	err := s.RequestToggle("crime", registry.StatusOff, "not-crime")
	if err == nil {
		t.Fatal("RequestToggle with wrong confirm token: want error, got nil")
	}

	after, _ := reg.Get("crime")
	if after != before {
		t.Fatalf("crime entry changed despite bad confirm token: before=%+v after=%+v", before, after)
	}
}

func TestRequestToggle_CancelledConfirm_EmptyToken_RejectedAndUnchanged(t *testing.T) {
	reg := newTestRegistry(t)
	s := debug.NewScreen(reg, "corr-1")

	before, _ := reg.Get("crime")

	err := s.RequestToggle("crime", registry.StatusOff, "")
	if err == nil {
		t.Fatal("RequestToggle with empty (cancelled) confirm: want error, got nil")
	}
	after, _ := reg.Get("crime")
	if after != before {
		t.Fatalf("crime entry changed despite cancelled confirm: before=%+v after=%+v", before, after)
	}
	if s.LastToggleError() == nil {
		t.Fatal("LastToggleError() = nil after a cancelled confirm, want the reason surfaced")
	}
}

// --- AC-7: full-entry retrieval ---

func TestTailEntry_ReturnsFullFields(t *testing.T) {
	entries := []errs.Entry{
		{Ts: "t0", Level: "warn", Code: "MET-U100", CorrelationID: "c0", Module: "ui.screen.map", Msg: "m0", Ctx: map[string]any{"k": "v"}},
	}
	s := debug.NewScreen(nil, "corr-1", debug.WithErrorTailSource(func() []errs.Entry { return entries }))
	snap := s.Collect()

	got, ok := s.TailEntry(snap, 0)
	if !ok {
		t.Fatal("TailEntry(0) not found")
	}
	if got.CorrelationID != "c0" || got.Ctx["k"] != "v" {
		t.Fatalf("TailEntry(0) = %+v, want full Entry with ctx", got)
	}

	if _, ok := s.TailEntry(snap, 99); ok {
		t.Fatal("TailEntry(99) found, want false for out-of-range index")
	}
}

// --- AC-6: last-50 slicing ---

func TestCollect_ErrorTail_TakesLast50(t *testing.T) {
	entries := make([]errs.Entry, 0, 75)
	for i := 0; i < 75; i++ {
		entries = append(entries, errs.Entry{Code: "MET-U100", Msg: itoa(i)})
	}
	s := debug.NewScreen(nil, "corr-1", debug.WithErrorTailSource(func() []errs.Entry { return entries }))
	snap := s.Collect()

	if len(snap.ErrorTail) != 50 {
		t.Fatalf("len(ErrorTail) = %d, want 50", len(snap.ErrorTail))
	}
	if snap.ErrorTail[0].Msg != itoa(25) {
		t.Fatalf("ErrorTail[0].Msg = %q, want %q (oldest of the last 50)", snap.ErrorTail[0].Msg, itoa(25))
	}
	if snap.ErrorTail[49].Msg != itoa(74) {
		t.Fatalf("ErrorTail[49].Msg = %q, want %q (newest)", snap.ErrorTail[49].Msg, itoa(74))
	}
}

func itoa(i int) string {
	digits := "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

// --- AC-11: unavailable panes never panic, are clearly flagged ---

func TestCollect_NilRegistry_RegistryPaneUnavailable(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1")
	snap := s.Collect()
	if snap.RegistryAvailable {
		t.Fatal("RegistryAvailable = true with a nil Registry")
	}
	if snap.RegistryReason == "" {
		t.Fatal("RegistryReason empty, want a stated cause")
	}
	for _, ps := range snap.PhaseSeries {
		if ps.Available {
			t.Fatalf("phase %q reports Available with a nil Registry", ps.Phase)
		}
	}
}

func TestCollect_NilErrorTailSource_PaneUnavailable(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1", debug.WithErrorTailSource(nil))
	snap := s.Collect()
	if snap.ErrorTailAvailable {
		t.Fatal("ErrorTailAvailable = true with a nil source")
	}
}

type failingBoWSource struct{}

func (failingBoWSource) Summary() (debug.BoWSummary, error) {
	return debug.BoWSummary{}, errors.New("connection refused")
}

func TestCollect_BoWSourceError_PaneUnavailable(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1", debug.WithBoWSource(failingBoWSource{}))
	snap := s.Collect()
	if snap.BoWAvailable {
		t.Fatal("BoWAvailable = true despite Summary() returning an error")
	}
	if snap.BoWReason == "" {
		t.Fatal("BoWReason empty, want the underlying cause surfaced")
	}
}

func TestCollect_NoBoWSourceConfigured_PaneUnavailable(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1")
	snap := s.Collect()
	if snap.BoWAvailable {
		t.Fatal("BoWAvailable = true with no source configured")
	}
}

// --- AC-14: -race clean under concurrent toggle + collect ---

func TestConcurrentToggleAndCollect_NoRace(t *testing.T) {
	reg := newTestRegistry(t)
	s := debug.NewScreen(reg, "corr-1")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = s.RequestToggle("crime", registry.StatusStub, "crime")
			_ = s.RequestToggle("crime", registry.StatusOff, "crime")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = s.Collect()
		}
	}()
	wg.Wait()
}
