package proj

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func applyPatch(t *testing.T, s *Screen, sub protocol.SubscriptionID, p wirePatch) {
	t.Helper()
	s.ApplyDelta(protocol.Delta{SubscriptionID: sub, Patch: mustJSON(t, p)})
}

func bind(t *testing.T, s *Screen) protocol.SubscriptionID {
	t.Helper()
	const id = protocol.SubscriptionID("sub-proj")
	s.BindSubscription(id)
	return id
}

func fullPatch() wirePatch {
	return wirePatch{
		SchemaVersion: 1,
		HorizonMonths: 72,
		Curves: []wireCurve{
			{
				Key:        "water.demand",
				Label:      "Water demand",
				Status:     "available",
				History:    []float64{10, 11, 12},
				Projection: []float64{13, 14, 15},
			},
			{
				Key:        "power.demand",
				Label:      "Power demand",
				Status:     "available",
				History:    []float64{20, 21},
				Projection: []float64{22, 23, 24},
			},
		},
		Crossings: []wireCrossing{
			{
				Key:                "refuse.ashford",
				Label:              "Refuse — Ashford",
				Status:             "available",
				InternalDemand:     []float64{100, 110, 120},
				ContractedCapacity: []float64{115, 115, 115},
				CrossingMonth:      2,
			},
		},
		RateOutlook: &wireRateOutlook{
			Status:     "available",
			History:    []float64{2.0, 2.1},
			Projection: []float64{2.2, 2.4, 2.6},
		},
	}
}

// TestApplyDelta_AppliesCurvesCrossingsRateAndHorizon drives one full
// patch through ApplyDelta and asserts every accessor reflects it — the
// single-view round-trip that SF-2's field-traceability table and PRJ-1..
// PRJ-4 rest on.
func TestApplyDelta_AppliesCurvesCrossingsRateAndHorizon(t *testing.T) {
	s := New("corr-apply")
	id := bind(t, s)
	applyPatch(t, s, id, fullPatch())

	curves, have := s.Curves()
	if !have || len(curves) != 2 {
		t.Fatalf("Curves() = %d curves, have=%v, want 2/true", len(curves), have)
	}
	if curves[0].Key != "water.demand" || curves[0].Status != StatusAvailable {
		t.Errorf("curves[0] = %+v, want key water.demand / available", curves[0])
	}
	if len(curves[0].History) != 3 || len(curves[0].Projection) != 3 {
		t.Errorf("curves[0] history/projection = %d/%d, want 3/3", len(curves[0].History), len(curves[0].Projection))
	}

	crossings, _ := s.Crossings()
	if len(crossings) != 1 || crossings[0].Key != "refuse.ashford" || crossings[0].CrossingMonth != 2 {
		t.Errorf("Crossings() = %+v, want one refuse.ashford crossing at month 2", crossings)
	}

	rate, ok, _ := s.RateOutlook()
	if !ok || len(rate.Projection) != 3 {
		t.Errorf("RateOutlook() = %+v ok=%v, want a 3-point projection", rate, ok)
	}

	months, ok := s.HorizonMonths()
	if !ok || months != 72 {
		t.Errorf("HorizonMonths() = %d/%v, want 72/true", months, ok)
	}
}

// TestHorizonMonths_ReadFromViewNotHardcoded is PRJ-2's GR#15 check: the
// horizon is read from the view's horizonMonths field, so two different
// producer values surface two different results — a screen that hardcoded
// a literal N would fail the second assertion.
func TestHorizonMonths_ReadFromViewNotHardcoded(t *testing.T) {
	s := New("corr-horizon")
	id := bind(t, s)

	p := fullPatch()
	p.HorizonMonths = 72
	applyPatch(t, s, id, p)
	if months, _ := s.HorizonMonths(); months != 72 {
		t.Fatalf("HorizonMonths() = %d, want 72", months)
	}

	p.HorizonMonths = 120
	applyPatch(t, s, id, p)
	if months, _ := s.HorizonMonths(); months != 120 {
		t.Fatalf("HorizonMonths() = %d after second patch, want 120 (N must track the view, not a constant)", months)
	}
}

// TestApplyDelta_MalformedPatchKeepsLastKnownGood is SF-7's "malformed
// patch is logged and dropped" check: a bad-JSON and a wrong-schemaVersion
// patch must not disturb the previously applied state.
func TestApplyDelta_MalformedPatchKeepsLastKnownGood(t *testing.T) {
	s := New("corr-malformed")
	id := bind(t, s)
	applyPatch(t, s, id, fullPatch())

	s.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: []byte(`{"schemaVersion":1,"horizonMonths":999,"curves":`)}) // bad JSON
	s.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: []byte(`{"schemaVersion":9,"horizonMonths":1}`)})            // wrong version

	curves, have := s.Curves()
	if !have || len(curves) != 2 {
		t.Fatalf("Curves() after malformed patches = %d/%v, want the original 2/true (last-known-good preserved)", len(curves), have)
	}
	months, _ := s.HorizonMonths()
	if months != 72 {
		t.Errorf("HorizonMonths() = %d, want 72 (malformed patches must not overwrite state)", months)
	}
}

// TestApplyDelta_UnknownSubscriptionDropped is SF-7's "delta for an
// unknown/stale subscription is dropped" check: a Delta whose
// SubscriptionID was never bound must not be applied.
func TestApplyDelta_UnknownSubscriptionDropped(t *testing.T) {
	s := New("corr-unknown")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "never-bound", Patch: mustJSON(t, fullPatch())})
	if _, have := s.Curves(); have {
		t.Fatal("Curves() reports haveData=true after an unbound-SubscriptionID delta, want false (dropped)")
	}
}

// TestUnavailableCurveGetsDefaultReason is PRJ-6's reason-always-shown
// half: an unavailable/not-unlocked curve with no producer-supplied reason
// still renders a reason, never a blank.
func TestUnavailableCurveGetsDefaultReason(t *testing.T) {
	s := New("corr-reason")
	id := bind(t, s)
	applyPatch(t, s, id, wirePatch{
		SchemaVersion: 1,
		HorizonMonths: 72,
		Curves: []wireCurve{
			{Key: "capexport.demand", Status: "unavailable"},
			{Key: "rate.cycle", Status: "not-unlocked"},
		},
	})

	curves, _ := s.Curves()
	if curves[0].Status != StatusUnavailable || curves[0].UnavailableReason == "" {
		t.Errorf("curves[0] = %+v, want StatusUnavailable with a non-empty default reason", curves[0])
	}
	if curves[1].Status != StatusNotUnlocked || curves[1].UnavailableReason == "" {
		t.Errorf("curves[1] = %+v, want StatusNotUnlocked with a non-empty default reason", curves[1])
	}
}

// TestRateOutlook_NilUntilDelivered checks the rate figure is absent (not
// a zero value) until the view carries it.
func TestRateOutlook_NilUntilDelivered(t *testing.T) {
	s := New("corr-rate-nil")
	if _, ok, _ := s.RateOutlook(); ok {
		t.Fatal("RateOutlook() ok=true before any patch, want false")
	}
}

// TestSubscribe_SendsProjViewCommand confirms Subscribe emits a Subscribe
// command for ViewSubscriptionName and nothing else.
func TestSubscribe_SendsProjViewCommand(t *testing.T) {
	s := New("corr-subscribe")
	var got protocol.Command
	err := s.Subscribe(func(c protocol.Command) error {
		got = c
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() = %v, want nil", err)
	}
	if got.Kind != protocol.KindSubscribe {
		t.Errorf("Subscribe kind = %q, want Subscribe", got.Kind)
	}
	p, ok := got.Payload.(protocol.SubscribePayload)
	if !ok {
		t.Fatalf("Subscribe payload = %T, want SubscribePayload", got.Payload)
	}
	if p.ViewName != ViewSubscriptionName {
		t.Errorf("Subscribe view = %q, want %q", p.ViewName, ViewSubscriptionName)
	}
}

// TestCurveLabelLine_IncludesMarkers pins the marker-summary formatting on
// the label row (PRJ-1's "queued-decision markers" are readable, not just
// a few chart dots).
func TestCurveLabelLine_IncludesMarkers(t *testing.T) {
	c := Curve{
		Key:   "education.capacity",
		Label: "Education capacity",
		Markers: []DecisionMarker{
			{MonthOffset: 18, Label: "school build"},
		},
	}
	line := curveLabelLine(c, 80)
	for _, want := range []string{"education.capacity", "Education capacity", "[m+18 school build]"} {
		if !strings.Contains(line, want) {
			t.Errorf("curveLabelLine() = %q, want it to contain %q", line, want)
		}
	}
}
