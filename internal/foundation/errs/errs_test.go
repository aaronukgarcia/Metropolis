package errs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func setupTestRegistry(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := writeRegistry(t, dir, validRegistry)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	resetSinkForTest()
	t.Cleanup(func() {
		resetRegistryForTest()
		resetSinkForTest()
	})
}

func TestNew_RegisteredCode(t *testing.T) {
	setupTestRegistry(t)

	e := New("MET-F900", "corr-1", map[string]any{"thing": "widget"})
	if e.Code != "MET-F900" {
		t.Errorf("Code = %q", e.Code)
	}
	if e.CorrelationID != "corr-1" {
		t.Errorf("CorrelationID = %q", e.CorrelationID)
	}
	if e.Module != "foundation.errors" {
		t.Errorf("Module = %q", e.Module)
	}
	if e.Msg != "test message widget" {
		t.Errorf("Msg = %q", e.Msg)
	}
	if e.Wrapped != nil {
		t.Errorf("expected New to leave Wrapped nil, got %v", e.Wrapped)
	}
}

func TestWrap_PreservesCause(t *testing.T) {
	setupTestRegistry(t)

	cause := errors.New("boom")
	e := Wrap("MET-F900", "corr-2", cause, nil)
	if !errors.Is(e, cause) {
		t.Error("expected errors.Is(e, cause) to be true via Unwrap")
	}
	if errors.Unwrap(e) != cause {
		t.Errorf("Unwrap = %v, want %v", errors.Unwrap(e), cause)
	}
}

func TestE_As(t *testing.T) {
	setupTestRegistry(t)

	err := fmt.Errorf("context: %w", New("MET-F900", "corr-3", nil))
	var target *E
	if !errors.As(err, &target) {
		t.Fatal("expected errors.As to find *E")
	}
	if target.Code != "MET-F900" {
		t.Errorf("Code = %q", target.Code)
	}
}

func TestE_Is_MatchesByCode(t *testing.T) {
	setupTestRegistry(t)

	e1 := New("MET-F900", "corr-a", nil)
	e2 := New("MET-F900", "corr-b", nil)
	if !errors.Is(e1, e2) {
		t.Error("expected two *E with the same code to satisfy errors.Is")
	}

	sentinel := &E{Code: "MET-F900"}
	if !errors.Is(e1, sentinel) {
		t.Error("expected errors.Is against a bare-code sentinel *E to match")
	}
}

func TestE_Display(t *testing.T) {
	setupTestRegistry(t)

	e := New("MET-F900", "corr-4", map[string]any{"thing": "gadget"})
	want := "[MET-F900] test message gadget (correlation: corr-4)"
	if got := e.Display(); got != want {
		t.Errorf("Display() = %q, want %q", got, want)
	}
}

func TestNew_UnregisteredCode_NeverPanics(t *testing.T) {
	setupTestRegistry(t)

	e := New("MET-F999", "corr-5", nil)
	if e.Code != "MET-F003" {
		t.Errorf("expected fallback code MET-F003, got %q", e.Code)
	}
	if !strings.Contains(e.Msg, "MET-F999") {
		t.Errorf("expected fallback message to mention the requested code, got %q", e.Msg)
	}
	if !strings.Contains(e.Msg, "New") {
		t.Errorf("expected fallback message to mention the constructor, got %q", e.Msg)
	}
}

func TestWrap_UnregisteredCode_MentionsWrap(t *testing.T) {
	setupTestRegistry(t)

	e := Wrap("MET-F999", "corr-6", errors.New("cause"), nil)
	if e.Code != "MET-F003" {
		t.Errorf("expected fallback code MET-F003, got %q", e.Code)
	}
	if !strings.Contains(e.Msg, "Wrap") {
		t.Errorf("expected fallback message to mention Wrap, got %q", e.Msg)
	}
	if e.Wrapped == nil {
		t.Error("expected fallback error to still preserve the cause")
	}
}

func TestNew_RegistryUnavailable_FallsBackWithoutPanic(t *testing.T) {
	t.Setenv(registryPathEnv, "/definitely/does/not/exist/errors.json")
	resetRegistryForTest()
	resetSinkForTest()
	t.Cleanup(func() {
		resetRegistryForTest()
		resetSinkForTest()
	})

	// BUG-279: a registry that could not be loaded at all is MET-F001 (fatal
	// "failed to load"), NOT the MET-F003 "unregistered code" fallback that a
	// valid-registry typo produces. Before the fix this asserted MET-F003 —
	// the exact collapse BUG-279 reports.
	e := New("MET-F900", "corr-7", nil)
	if e.Code != "MET-F001" {
		t.Errorf("expected MET-F001 when registry could not be loaded, got %q", e.Code)
	}
	if !strings.Contains(e.Msg, "errors.json") {
		t.Errorf("expected fallback message to explain the registry failure, got %q", e.Msg)
	}
}

func TestNew_MissingCorrelationID_LogsWarningAndPlaceholders(t *testing.T) {
	setupTestRegistry(t)

	e := New("MET-F900", "", nil)
	if e.CorrelationID != missingCorrelationPlaceholder {
		t.Errorf("CorrelationID = %q, want placeholder", e.CorrelationID)
	}

	recent := Recent()
	found := false
	for _, entry := range recent {
		if entry.Code == "MET-F004" {
			found = true
		}
	}
	if !found {
		t.Error("expected a MET-F004 warning to have been logged for the missing correlation ID")
	}
}

func TestConstruct_UsesInjectableClock(t *testing.T) {
	setupTestRegistry(t)

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	SetClock(func() time.Time { return fixed })
	t.Cleanup(func() { SetClock(time.Now) })

	e := New("MET-F900", "corr-8", nil)
	if !e.Time.Equal(fixed) {
		t.Errorf("Time = %v, want %v", e.Time, fixed)
	}
}

func TestRenderTemplate_MissingPlaceholderStaysVisible(t *testing.T) {
	got := renderTemplate("hello {name}, code {code}", "MET-F900", "corr-9", nil)
	want := "hello {name}, code MET-F900"
	if got != want {
		t.Errorf("renderTemplate = %q, want %q", got, want)
	}
}
