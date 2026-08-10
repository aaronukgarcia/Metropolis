package debug

import (
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

func newTestHeader() *serialize.Header {
	h := serialize.NewHeader(1, 0, 0, "test")
	return &h
}

// TestDefaultOff is AC-2: a freshly constructed State (no flag, no
// config, no palette command) reports IsOn() == false.
func TestDefaultOff(t *testing.T) {
	s := NewState()
	if s.IsOn() {
		t.Fatalf("NewState().IsOn() = true, want false")
	}
}

// TestEnableSourcesConverge is AC-1: all three enable paths converge on
// the same IsOn() read.
func TestEnableSourcesConverge(t *testing.T) {
	for _, src := range []EnableSource{SourceFlag, SourceConfig, SourcePalette} {
		s := NewState(WithHeader(newTestHeader()))
		if err := s.Enable(src, "corr-1"); err != nil {
			t.Fatalf("Enable(%s): %v", src, err)
		}
		if !s.IsOn() {
			t.Fatalf("Enable(%s) then IsOn() = false, want true", src)
		}
	}
}

// TestEnableTouchesHeaderStickily is AC-3: enabling calls TouchDebug on
// the active header, and the flag remains true across a subsequent
// disable-then-save (the sticky invariant).
func TestEnableTouchesHeaderStickily(t *testing.T) {
	h := newTestHeader()
	s := NewState(WithHeader(h))

	if h.DebugTouched() {
		t.Fatalf("fresh header DebugTouched = true before Enable, want false")
	}
	if err := s.Enable(SourcePalette, "corr-2"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !h.DebugTouched() {
		t.Fatalf("header.DebugTouched = false after Enable, want true")
	}

	// Disable, then simulate a subsequent save-over of the same header
	// (nothing should ever be able to clear DebugTouched).
	s.Disable()
	if !h.DebugTouched() {
		t.Fatalf("header.DebugTouched = false after Disable, want still true (sticky)")
	}
}

// TestDisableDoesNotClearDebugTouched is AC-4: toggling debug on then
// off leaves the header's DebugTouched flag true.
func TestDisableDoesNotClearDebugTouched(t *testing.T) {
	h := newTestHeader()
	s := NewState(WithHeader(h))

	if err := s.Enable(SourceFlag, "corr-3"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	s.Disable()

	if s.IsOn() {
		t.Fatalf("IsOn() = true after Disable, want false")
	}
	if !h.DebugTouched() {
		t.Fatalf("header.DebugTouched = false after Disable, want true (AC-4 sticky invariant)")
	}
}

// TestEnableWithoutHeaderRejected: Enable refuses to report success
// with no header wired to touch.
func TestEnableWithoutHeaderRejected(t *testing.T) {
	s := NewState()
	err := s.Enable(SourceFlag, "corr-4")
	if err == nil {
		t.Fatalf("Enable with no header configured: got nil error, want ErrNoHeaderConfigured")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrNoHeaderConfigured {
		t.Fatalf("Enable with no header: got %v, want code %s", err, ErrNoHeaderConfigured)
	}
	if s.IsOn() {
		t.Fatalf("IsOn() = true after a failed Enable, want false")
	}
}

// TestEnableUnknownSourceRejected: Enable rejects an EnableSource
// outside the three documented constants.
func TestEnableUnknownSourceRejected(t *testing.T) {
	s := NewState(WithHeader(newTestHeader()))
	err := s.Enable(EnableSource("bogus"), "corr-5")
	if err == nil {
		t.Fatalf("Enable with unknown source: got nil error, want ErrUnknownEnableSource")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrUnknownEnableSource {
		t.Fatalf("Enable with unknown source: got %v, want code %s", err, ErrUnknownEnableSource)
	}
	if s.IsOn() {
		t.Fatalf("IsOn() = true after a rejected Enable, want false")
	}
}

// TestEnablePersistFailureSurfacesError is AC-12: if the sticky flag
// fails to persist, Enable does not report success — IsOn() must stay
// false and the returned error must be registry-sourced.
func TestEnablePersistFailureSurfacesError(t *testing.T) {
	h := newTestHeader()
	persistErr := errors.New("disk full")
	s := NewState(WithHeader(h), WithPersist(func() error { return persistErr }))

	err := s.Enable(SourceConfig, "corr-6")
	if err == nil {
		t.Fatalf("Enable with failing persist: got nil error, want ErrEnablePersistFailed")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrEnablePersistFailed {
		t.Fatalf("Enable with failing persist: got %v, want code %s", err, ErrEnablePersistFailed)
	}
	if !errors.Is(err, persistErr) {
		t.Fatalf("Enable with failing persist: wrapped cause not retrievable via errors.Is")
	}
	if s.IsOn() {
		t.Fatalf("IsOn() = true after a persist-failed Enable, want false (AC-12: must not report success)")
	}
}

// TestEnableIdempotentOnceOn: re-enabling via a different source while
// already on is a harmless re-touch, not an error.
func TestEnableIdempotentOnceOn(t *testing.T) {
	h := newTestHeader()
	s := NewState(WithHeader(h))
	if err := s.Enable(SourceFlag, "corr-7a"); err != nil {
		t.Fatalf("first Enable: %v", err)
	}
	if err := s.Enable(SourcePalette, "corr-7b"); err != nil {
		t.Fatalf("second Enable (different source): %v", err)
	}
	if !s.IsOn() {
		t.Fatalf("IsOn() = false after two Enable calls, want true")
	}
}

// TestClockNeverDefaultsToWallClock is a behavioural guard for AC-14: a
// State with no WithClock stamps events with the zero time.Time, never
// the current wall-clock time.
func TestClockNeverDefaultsToWallClock(t *testing.T) {
	s := NewState()
	got := s.nowFunc()
	if !got.IsZero() {
		t.Fatalf("nowFunc() with no WithClock = %v, want zero time.Time", got)
	}
}

// TestWithClockOverride confirms the injected Clock is actually used.
func TestWithClockOverride(t *testing.T) {
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := NewState(WithClock(func() time.Time { return want }))
	if got := s.nowFunc(); !got.Equal(want) {
		t.Fatalf("nowFunc() = %v, want %v", got, want)
	}
}
