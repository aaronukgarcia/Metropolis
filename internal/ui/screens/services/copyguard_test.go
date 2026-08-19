package services

import (
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into its
// own tiny helper (mirrors finance.screenCopy/trade.screenCopy exactly,
// including the unsafe byte-copy).
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
		t.Fatalf("error code = %q, want %q", e.Code, ErrScreenCopied)
	}
}

func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s := New("corr-test")

	// Create a copy via the helper
	s2 := screenCopy(s)

	err := s2.Subscribe(func(protocol.Command) error { return nil })
	assertScreenCopiedCode(t, err)
}

func TestScreen_AccessorsRejectCopy(t *testing.T) {
	s := New("corr-test")
	s2 := screenCopy(s)

	if s2.HaveData() {
		t.Error("HaveData on copy returned true")
	}
	if s2.Stale() {
		t.Error("Stale on copy returned true")
	}
	if _, ok := s2.Sliders(); ok {
		t.Error("Sliders on copy returned true")
	}
	if _, ok := s2.CapacityDemand(); ok {
		t.Error("CapacityDemand on copy returned true")
	}
	if _, ok := s2.ResponseTimes(); ok {
		t.Error("ResponseTimes on copy returned true")
	}
	if _, ok := s2.WaitingLists(); ok {
		t.Error("WaitingLists on copy returned true")
	}
	if _, ok := s2.PublicServicePie(); ok {
		t.Error("PublicServicePie on copy returned true")
	}
	if s2.FundingRejectedReason() != "" {
		t.Error("FundingRejectedReason on copy returned non-empty")
	}
	if err := s2.SetFunding(func(protocol.Command) error { return nil }, ServiceSlider{ID: "police", Min: 0, Max: 1000}, 100); err == nil {
		t.Error("SetFunding on copy returned nil error")
	} else {
		assertScreenCopiedCode(t, err)
	}
}
