package debug

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func enabledState(t *testing.T) *State {
	t.Helper()
	s := NewState(WithHeader(newTestHeader()))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return s
}

// TestSpeed8xGate is AC-5: SetSpeed(8x) is accepted (gate returns nil)
// when IsOn()==true and rejected when IsOn()==false.
func TestSpeed8xGate(t *testing.T) {
	off := NewState()
	if err := off.AllowSpeed8x("corr-off"); err == nil {
		t.Fatalf("AllowSpeed8x with debug off: got nil error, want rejection")
	}

	on := enabledState(t)
	if err := on.AllowSpeed8x("corr-on"); err != nil {
		t.Fatalf("AllowSpeed8x with debug on: %v, want nil", err)
	}
}

// TestConsoleAndFixtureControlsGate is AC-10: both are unreachable with
// debug off and reachable with it on.
func TestConsoleAndFixtureControlsGate(t *testing.T) {
	off := NewState()
	if err := off.RequireConsole("corr-off-1"); err == nil {
		t.Fatalf("RequireConsole with debug off: got nil error, want rejection")
	}
	if err := off.RequireFixtureControls("corr-off-2"); err == nil {
		t.Fatalf("RequireFixtureControls with debug off: got nil error, want rejection")
	}

	on := enabledState(t)
	if err := on.RequireConsole("corr-on-1"); err != nil {
		t.Fatalf("RequireConsole with debug on: %v, want nil", err)
	}
	if err := on.RequireFixtureControls("corr-on-2"); err != nil {
		t.Fatalf("RequireFixtureControls with debug on: %v, want nil", err)
	}
}

// TestUnlocksRejectedWhenOff is AC-9/AC-11: with debug off, every
// gated unlock (AC-5 through AC-8, AC-10) is rejected with a clear
// registry-sourced errs.E, never a silent no-op, never a panic.
func TestUnlocksRejectedWhenOff(t *testing.T) {
	off := NewState(WithEntityLookup(func(ref string) (any, error) {
		return map[string]string{"ref": ref}, nil
	}), WithFidelityDial(&fakeFidelityDial{}))

	checks := []struct {
		name string
		err  error
	}{
		{"speed-8x", off.AllowSpeed8x("corr-1")},
		{"console", off.RequireConsole("corr-2")},
		{"fixture-controls", off.RequireFixtureControls("corr-3")},
		{"cheat", off.InvokeCheat("corr-4", CheatFreeMoney, nil, func() error { return nil })},
	}

	for _, c := range checks {
		if c.err == nil {
			t.Fatalf("%s with debug off: got nil error, want rejection", c.name)
		}
		var e *errs.E
		if !errors.As(c.err, &e) || e.Code != ErrDebugRequired {
			t.Fatalf("%s with debug off: got %v, want code %s", c.name, c.err, ErrDebugRequired)
		}
	}

	if _, err := off.InspectEntity("corr-5", "citizen:1"); err == nil {
		t.Fatalf("InspectEntity with debug off: got nil error, want rejection")
	} else {
		var e *errs.E
		if !errors.As(err, &e) || e.Code != ErrDebugRequired {
			t.Fatalf("InspectEntity with debug off: got %v, want code %s", err, ErrDebugRequired)
		}
	}

	if _, err := off.FidelityDial("corr-6"); err == nil {
		t.Fatalf("FidelityDial with debug off: got nil error, want rejection")
	} else {
		var e *errs.E
		if !errors.As(err, &e) || e.Code != ErrDebugRequired {
			t.Fatalf("FidelityDial with debug off: got %v, want code %s", err, ErrDebugRequired)
		}
	}
}
