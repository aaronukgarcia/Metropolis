package debug

import (
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestInvokeCheatAppliesAndLogs is AC-6: invoking a cheat applies its
// effect and emits a logged event documenting the cheat was used.
func TestInvokeCheatAppliesAndLogs(t *testing.T) {
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	s := NewState(WithHeader(newTestHeader()), WithClock(func() time.Time { return stamp }))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	for _, kind := range []CheatKind{CheatFreeMoney, CheatInstantBuild, CheatForceMilestone} {
		var applied bool
		effect := func() error { applied = true; return nil }

		if err := s.InvokeCheat("corr-"+string(kind), kind, map[string]any{"amount": 100}, effect); err != nil {
			t.Fatalf("InvokeCheat(%s): %v", kind, err)
		}
		if !applied {
			t.Fatalf("InvokeCheat(%s): effect was not invoked", kind)
		}
	}

	log := s.CheatLog()
	if len(log) != 3 {
		t.Fatalf("CheatLog() has %d entries, want 3", len(log))
	}
	for i, kind := range []CheatKind{CheatFreeMoney, CheatInstantBuild, CheatForceMilestone} {
		if log[i].Kind != kind {
			t.Fatalf("CheatLog()[%d].Kind = %s, want %s", i, log[i].Kind, kind)
		}
		if !log[i].At.Equal(stamp) {
			t.Fatalf("CheatLog()[%d].At = %v, want %v (injected clock)", i, log[i].At, stamp)
		}
		if log[i].CorrelationID != "corr-"+string(kind) {
			t.Fatalf("CheatLog()[%d].CorrelationID = %s, want %s", i, log[i].CorrelationID, "corr-"+string(kind))
		}
	}
}

// TestInvokeCheatNilEffectRejected: InvokeCheat refuses a nil effect
// rather than treating it as a silent no-op.
func TestInvokeCheatNilEffectRejected(t *testing.T) {
	s := enabledState(t)
	err := s.InvokeCheat("corr-nil", CheatFreeMoney, nil, nil)
	if err == nil {
		t.Fatalf("InvokeCheat with nil effect: got nil error, want ErrNilCheatEffect")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrNilCheatEffect {
		t.Fatalf("InvokeCheat with nil effect: got %v, want code %s", err, ErrNilCheatEffect)
	}
	if len(s.CheatLog()) != 0 {
		t.Fatalf("CheatLog() has entries after a rejected InvokeCheat, want none")
	}
}

// TestInvokeCheatEffectFailureNotLogged: a failing effect's failure is
// surfaced, and the invocation is not recorded as a successful use.
func TestInvokeCheatEffectFailureNotLogged(t *testing.T) {
	s := enabledState(t)
	effectErr := errors.New("insufficient world state")
	err := s.InvokeCheat("corr-fail", CheatInstantBuild, nil, func() error { return effectErr })
	if err == nil {
		t.Fatalf("InvokeCheat with failing effect: got nil error, want ErrCheatEffectFailed")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrCheatEffectFailed {
		t.Fatalf("InvokeCheat with failing effect: got %v, want code %s", err, ErrCheatEffectFailed)
	}
	if !errors.Is(err, effectErr) {
		t.Fatalf("InvokeCheat with failing effect: wrapped cause not retrievable via errors.Is")
	}
	if len(s.CheatLog()) != 0 {
		t.Fatalf("CheatLog() has entries after a failed effect, want none (a failed effect never happened)")
	}
}
