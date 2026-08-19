package finance

import (
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into its
// own tiny helper (mirrors proj.screenCopy / demo.screenCopy exactly,
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
	if _, ok := s2.PL(); ok {
		t.Error("PL on copy returned true")
	}
	if _, ok := s2.BalanceSheet(); ok {
		t.Error("BalanceSheet on copy returned true")
	}
	if _, ok := s2.Loans(); ok {
		t.Error("Loans on copy returned true")
	}
	if _, ok := s2.TaxSliders(); ok {
		t.Error("TaxSliders on copy returned true")
	}
	if _, ok := s2.PublicPayroll(); ok {
		t.Error("PublicPayroll on copy returned true")
	}
	if _, ok := s2.Sankey(); ok {
		t.Error("Sankey on copy returned true")
	}
}
