package fiscal

import (
	"testing"
)

// TestChildcareNetLine asserts the §54 childcare subsidy is exposed as three
// distinct values whose net line is exactly grossSpend − taxYield (AC-6), and
// that the tax yield is a real, rate-driven figure (positive at a positive
// income-tax rate), not a narration.
func TestChildcareNetLine(t *testing.T) {
	f, _, taxAPI := newTestFiscal(t)
	incomeID := incomeInstrumentID(t, taxAPI)

	if err := taxAPI.SetRate(incomeID, 20.0); err != nil {
		t.Fatalf("SetRate(20%%): %v", err)
	}
	if err := f.SetChildcarePlaces(10); err != nil {
		t.Fatalf("SetChildcarePlaces: %v", err)
	}

	line, err := f.ChildcareNetLine()
	if err != nil {
		t.Fatalf("ChildcareNetLine: %v", err)
	}
	if line.GrossSpend <= 0 {
		t.Errorf("GrossSpend = %d, want > 0", int64(line.GrossSpend))
	}
	if line.TaxYield <= 0 {
		t.Errorf("TaxYield = %d, want > 0 at a 20%% income-tax rate", int64(line.TaxYield))
	}
	// The net line is exactly grossSpend − taxYield (never a separately
	// narrated number the player has to trust).
	if line.Net != line.GrossSpend-line.TaxYield {
		t.Errorf("Net = %d, want grossSpend − taxYield = %d", int64(line.Net), int64(line.GrossSpend-line.TaxYield))
	}
	if line.Net >= line.GrossSpend {
		t.Errorf("Net = %d, want < grossSpend %d (subsidy is only partially self-funding)", int64(line.Net), int64(line.GrossSpend))
	}
}

// TestChildcareZeroPlaces asserts the degenerate zero-places case returns a
// zero net line with no error (AC-6's "synthetic scenario" boundary).
func TestChildcareZeroPlaces(t *testing.T) {
	f, _, _ := newTestFiscal(t)
	if err := f.SetChildcarePlaces(0); err != nil {
		t.Fatalf("SetChildcarePlaces(0): %v", err)
	}
	line, err := f.ChildcareNetLine()
	if err != nil {
		t.Fatalf("ChildcareNetLine: %v", err)
	}
	if line.GrossSpend != 0 || line.TaxYield != 0 || line.Net != 0 {
		t.Errorf("zero places should yield a zero net line, got %+v", line)
	}
}

// TestChildcareNetLineNeverNegative asserts Net is clamped at 0 across the
// full income-tax rate range, including the adversarial repro from SEC-149
// (60% rate, 10 places) where TaxYield exceeds GrossSpend: the subsidy is
// documented as only partially self-funding, never a negative money figure
// (GR#16 money-is-never-negative).
func TestChildcareNetLineNeverNegative(t *testing.T) {
	f, _, taxAPI := newTestFiscal(t)
	incomeID := incomeInstrumentID(t, taxAPI)

	if err := f.SetChildcarePlaces(10); err != nil {
		t.Fatalf("SetChildcarePlaces: %v", err)
	}

	for rate := 0.0; rate <= 60.0; rate += 5.0 {
		if err := taxAPI.SetRate(incomeID, rate); err != nil {
			t.Fatalf("SetRate(%v%%): %v", rate, err)
		}
		line, err := f.ChildcareNetLine()
		if err != nil {
			t.Fatalf("ChildcareNetLine at rate %v%%: %v", rate, err)
		}
		if line.Net < 0 {
			t.Errorf("rate %v%%: Net = %d, want >= 0 (SEC-149)", rate, int64(line.Net))
		}
	}

	// The exact SEC-149 repro: at the instrument's own max 60% rate, TaxYield
	// exceeds GrossSpend, so Net must be clamped to exactly 0, not a
	// negative saturated difference.
	if err := taxAPI.SetRate(incomeID, 60.0); err != nil {
		t.Fatalf("SetRate(60%%): %v", err)
	}
	line, err := f.ChildcareNetLine()
	if err != nil {
		t.Fatalf("ChildcareNetLine at 60%%: %v", err)
	}
	if line.TaxYield <= line.GrossSpend {
		t.Fatalf("test precondition failed: TaxYield %d not > GrossSpend %d at 60%% rate", int64(line.TaxYield), int64(line.GrossSpend))
	}
	if line.Net != 0 {
		t.Errorf("Net = %d, want exactly 0 when TaxYield exceeds GrossSpend", int64(line.Net))
	}
}

// TestChildcareNegativePlacesRejected asserts the input boundary (GR#16).
func TestChildcareNegativePlacesRejected(t *testing.T) {
	f, _, _ := newTestFiscal(t)
	if err := f.SetChildcarePlaces(-1); err == nil {
		t.Fatal("SetChildcarePlaces(-1) returned nil error, want ErrInvalidInput")
	}
}
