package projections

import "testing"

// --- AC-1: registration surface + query surface -------------------------

func TestRegisterCurveProviderAndQuery(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{values: map[int64]float64{0: 10, 1: 20}}

	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	points, err := api.Curve("test.curve", 0, 1)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}
	if len(points) != 2 || points[0].Value != 10 || points[1].Value != 20 {
		t.Errorf("Curve returned %+v, want values [10, 20]", points)
	}
}

func TestRegisterCurveProviderRejectsNil(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.RegisterCurveProvider("test.curve", nil)
	assertCode(t, err, ErrNilCurveProvider)
}

func TestRegisterCurveProviderRejectsDuplicate(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{def: 1}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("first RegisterCurveProvider: %v", err)
	}
	err := api.RegisterCurveProvider("test.curve", provider)
	assertCode(t, err, ErrDuplicateCurveProvider)
}

// --- AC-7: history vs projection distinction -----------------------------

func TestCurveDistinguishesHistoricalFromProjected(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{def: 5}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := api.SetCurrentMonth(3); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}

	points, err := api.Curve("test.curve", 0, 6)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}
	for _, p := range points {
		wantHistorical := p.Month <= 3
		if p.Historical != wantHistorical {
			t.Errorf("month %d: Historical = %v, want %v", p.Month, p.Historical, wantHistorical)
		}
	}
}

// --- AC-8: threshold registration/query, independent of the value series ---

func TestThresholdRegistrationIndependentOfCurve(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterThreshold("test.capacity", 100); err != nil {
		t.Fatalf("RegisterThreshold: %v", err)
	}
	v, err := api.Threshold("test.capacity")
	if err != nil {
		t.Fatalf("Threshold: %v", err)
	}
	if v != 100 {
		t.Errorf("Threshold = %v, want 100", v)
	}

	// No curve provider was ever registered for this key — proving the
	// threshold surface is genuinely independent of RegisterCurveProvider.
	if _, err := api.Curve("test.capacity", 0, 0); err == nil {
		t.Error("Curve unexpectedly succeeded against a key with only a threshold, no provider")
	}
}

func TestThresholdUnknownKeyRejected(t *testing.T) {
	api := NewProjectionsAPI()
	_, err := api.Threshold("nonexistent")
	assertCode(t, err, ErrUnknownCurveKey)
}
