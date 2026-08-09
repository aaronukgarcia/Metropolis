package debug

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fakeFidelityDial is a minimal test double for FidelityDial — the real
// implementation belongs to whichever module ends up owning adaptive
// fidelity (see fidelity.go's doc comment); this package only needs
// something satisfying the interface to prove its own gating.
type fakeFidelityDial struct {
	radius int
}

func (f *fakeFidelityDial) Range() (int, int) { return 1, 10 }
func (f *fakeFidelityDial) Current() int      { return f.radius }
func (f *fakeFidelityDial) Cost() float64     { return float64(f.radius) * float64(f.radius) }
func (f *fakeFidelityDial) SetRadius(r int) error {
	f.radius = r
	return nil
}

// TestFidelityDialGate is AC-8: the dial's exposed range/current-value
// API is only reachable when IsOn()==true.
func TestFidelityDialGate(t *testing.T) {
	dial := &fakeFidelityDial{radius: 3}

	off := NewState(WithFidelityDial(dial))
	if _, err := off.FidelityDial("corr-off"); err == nil {
		t.Fatalf("FidelityDial with debug off: got nil error, want rejection")
	}

	on := NewState(WithHeader(newTestHeader()), WithFidelityDial(dial))
	if err := on.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	got, err := on.FidelityDial("corr-on")
	if err != nil {
		t.Fatalf("FidelityDial with debug on: %v", err)
	}
	minR, maxR := got.Range()
	if minR != 1 || maxR != 10 {
		t.Fatalf("FidelityDial.Range() = (%d, %d), want (1, 10)", minR, maxR)
	}
	if got.Current() != 3 {
		t.Fatalf("FidelityDial.Current() = %d, want 3", got.Current())
	}
	if got.Cost() != 9 {
		t.Fatalf("FidelityDial.Cost() = %v, want 9", got.Cost())
	}
}

// TestFidelityDialNotConfigured: FidelityDial refuses rather than
// returning a nil interface silently when debug is on but no dial was
// wired.
func TestFidelityDialNotConfigured(t *testing.T) {
	s := enabledState(t)
	_, err := s.FidelityDial("corr-noconf")
	if err == nil {
		t.Fatalf("FidelityDial with none configured: got nil error, want ErrFidelityDialNotConfigured")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrFidelityDialNotConfigured {
		t.Fatalf("FidelityDial with none configured: got %v, want code %s", err, ErrFidelityDialNotConfigured)
	}
}
