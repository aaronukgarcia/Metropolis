package districts

import (
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into its
// own tiny helper (mirrors services.screenCopy/finance.screenCopy/
// trade.screenCopy exactly, including the unsafe byte-copy).
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
	if _, ok := s2.Districts(); ok {
		t.Error("Districts on copy returned true")
	}
	if _, ok := s2.TaxSettings(); ok {
		t.Error("TaxSettings on copy returned true")
	}
	if s2.TaxRejectedReason() != "" {
		t.Error("TaxRejectedReason on copy returned non-empty")
	}
	if s2.SelectedDistrict() != "" {
		t.Error("SelectedDistrict on copy returned non-empty")
	}
	if err := s2.SetDistrictMultiplier(func(protocol.Command) error { return nil }, "harbour", "councilTax", 1.0); err == nil {
		t.Error("SetDistrictMultiplier on copy returned nil error")
	} else {
		assertScreenCopiedCode(t, err)
	}
}
