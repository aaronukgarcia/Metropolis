package census

import (
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// screenCopy takes a same-package value copy of *Screen, isolated into its
// own tiny helper (mirrors services.screenCopy/finance.screenCopy exactly,
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
	s2 := screenCopy(s)

	err := s2.Subscribe(func(protocol.Command) error { return nil })
	assertScreenCopiedCode(t, err)
}

// TestScreen_AccessorsRejectCopy is AC-13's copy-guard sweep: every
// exported *Screen method that reads/writes a receiver field must reject
// a struct-copied value rather than operating on an independently-zeroed,
// aliasing copy.
func TestScreen_AccessorsRejectCopy(t *testing.T) {
	s := New("corr-test")
	s2 := screenCopy(s)

	if s2.HaveData() {
		t.Error("HaveData on copy returned true")
	}
	if s2.Stale() {
		t.Error("Stale on copy returned true")
	}
	s2.SetStale(true)
	if s2.Stale() {
		t.Error("SetStale on copy took effect")
	}
	if s2.SelectionRejectedReason() != "" {
		t.Error("SelectionRejectedReason on copy returned non-empty")
	}
	if _, ok := s2.AgeBandSeries(); ok {
		t.Error("AgeBandSeries on copy returned have=true")
	}
	if _, ok := s2.SexSeries(); ok {
		t.Error("SexSeries on copy returned have=true")
	}
	if _, ok := s2.EducationTierSeries(); ok {
		t.Error("EducationTierSeries on copy returned have=true")
	}
	if _, ok := s2.BlueWhiteCollarSplit(); ok {
		t.Error("BlueWhiteCollarSplit on copy returned have=true")
	}
	if _, ok := s2.KPITiles(); ok {
		t.Error("KPITiles on copy returned have=true")
	}
	if _, ok := s2.KPISource(KPIKeyGDP); ok {
		t.Error("KPISource on copy returned ok=true")
	}
	if _, ok := s2.SelectedBio(); ok {
		t.Error("SelectedBio on copy returned have=true")
	}
	if _, ok := s2.EducationCrimeLinkageView(); ok {
		t.Error("EducationCrimeLinkageView on copy returned have=true")
	}
	if err := s2.SelectKPI(func(protocol.Command) error { return nil }, KPIKeyGDP); err == nil {
		t.Error("SelectKPI on copy returned nil error")
	} else {
		assertScreenCopiedCode(t, err)
	}
	if err := s2.SelectCitizen(func(protocol.Command) error { return nil }, "citizen:1"); err == nil {
		t.Error("SelectCitizen on copy returned nil error")
	} else {
		assertScreenCopiedCode(t, err)
	}

	// BindSubscription/UnbindSubscription/ApplyDelta/ApplyResult are void
	// methods -- proving they no-op on a copy means the copy's own (bogus,
	// independently-zeroed) subs map is never mutated and the original's
	// state is untouched.
	s2.BindSubscription("sub-copy")
	s2.UnbindSubscription("sub-copy")
	s2.ApplyDelta(protocol.Delta{SubscriptionID: "sub-copy"})
	s2.ApplyResult(protocol.CommandResult{CorrelationID: "corr-test", Accepted: true})
	if s.HaveData() {
		t.Error("operating on the copy somehow mutated the original's state")
	}
}
