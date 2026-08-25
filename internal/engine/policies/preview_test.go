package policies

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ---------------------------------------------------------------------------
// BUG-300 — inverted preview range gets its own dedicated registry code
// ---------------------------------------------------------------------------

// TestPreviewImpactInvertedRangeRejectsWithDedicatedCode (BUG-300): an
// inverted preview range (toMonth < fromMonth) is a temporal-order violation
// and must raise the module's dedicated registry code,
// ErrInvertedPreviewRange (MET-G4015) — never the scope-lookup code
// ErrUnknownScope (MET-G4003) that the pre-fix branch returned
// (registry-semantics drift, GR#7). The rejection must also fire BEFORE
// SetCurrentMonth mutates the projections seam, so a rejected preview never
// advances the seam's current month (GR#12).
func TestPreviewImpactInvertedRangeRejectsWithDedicatedCode(t *testing.T) {
	a := testAPI(t)
	rec := &recordingProjections{horizon: 72}
	a.projections = rec
	a.currentMonth = 10
	addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))

	_, err := a.PreviewImpactRange("cycling", Scope{Kind: ScopeCitywide}, 5)

	// The dedicated temporal-order code (MET-G4015), matching the package's
	// assertCode convention (errors.Is on the *errs.E code).
	assertCode(t, err, ErrInvertedPreviewRange)

	// ... and specifically NOT the scope-lookup code (MET-G4003) the
	// pre-fix branch returned. An inverted range is not a scope failure.
	if errors.Is(err, &errs.E{Code: ErrUnknownScope}) {
		t.Fatalf("inverted preview range must NOT raise ErrUnknownScope (MET-G4003), got %v", err)
	}

	// The branch's metadata is preserved: which months formed the inverted
	// range, so the caller can see the offending bounds.
	var ee *errs.E
	if !errors.As(err, &ee) {
		t.Fatalf("error must be an *errs.E, got %T", err)
	}
	if got, ok := ee.Ctx["fromMonth"].(int64); !ok || got != 10 {
		t.Fatalf("metadata fromMonth = %v (%T), want int64 10", ee.Ctx["fromMonth"], ee.Ctx["fromMonth"])
	}
	if got, ok := ee.Ctx["toMonth"].(int64); !ok || got != 5 {
		t.Fatalf("metadata toMonth = %v (%T), want int64 5", ee.Ctx["toMonth"], ee.Ctx["toMonth"])
	}
	if got := ee.Ctx["scope"]; got != "inverted preview range" {
		t.Fatalf("metadata scope = %v, want %q", got, "inverted preview range")
	}

	// GR#12: the rejection happens before SetCurrentMonth, so the seam's
	// current month never moves on a rejected preview.
	if got := len(rec.setCurrentMonth); got != 0 {
		t.Fatalf("SetCurrentMonth called %d times on a rejected inverted-range preview, want 0", got)
	}
}
