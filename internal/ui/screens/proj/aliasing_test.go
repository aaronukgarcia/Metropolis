package proj

// Destructive regression (SEC-062): the exported accessors Curves()/
// Crossings()/RateOutlook() must return deep copies — the outer slice AND
// every nested slice (History, Projection, ConfidenceUpper, ConfidenceLower,
// Thresholds, Markers, InternalDemand, ContractedCapacity) — so a caller
// mutating, sorting, truncating or rewriting a returned series owns only the
// copy and cannot corrupt the Screen's stored state. The pre-fix accessors
// copied the outer slice but aliased the nested backing arrays (mutating
// curves[0].History[0] through the returned handle changed the screen).
//
// These tests assert that defensive-copy invariant; they now PASS against the
// SEC-062 deep-copy fix (cloneCurve/cloneCrossing/cloneRateOutlook in
// screen.go) and are this package's regression guard for it.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func aliasPatch() wirePatch {
	return wirePatch{
		SchemaVersion: 1,
		HorizonMonths: 72,
		Curves: []wireCurve{
			{
				Key: "water.demand", Status: "available",
				History:         []float64{10, 11, 12},
				Projection:      []float64{13, 14, 15},
				ConfidenceUpper: []float64{16, 17, 18},
				ConfidenceLower: []float64{8, 9, 10},
				Thresholds:      []wireThreshold{{Value: 14, Label: "ceiling"}},
				Markers:         []wireMarker{{MonthOffset: 1, Label: "school build"}},
			},
		},
		Crossings: []wireCrossing{
			{
				Key: "refuse.ashford", Status: "available",
				InternalDemand:     []float64{100, 110, 120},
				ContractedCapacity: []float64{115, 115, 115},
				CrossingMonth:      2,
			},
		},
		RateOutlook: &wireRateOutlook{
			Status: "available", History: []float64{2.0, 2.1}, Projection: []float64{2.2, 2.4},
		},
	}
}

func applyAliasPatch(t *testing.T, s *Screen) {
	t.Helper()
	const id = protocol.SubscriptionID("sub-alias")
	s.BindSubscription(id)
	s.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: mustJSON(t, aliasPatch())})
}

// TestCurves_DoesNotAliasInternalState mutates every slice field on the
// Curves() result and asserts the next Curves() call is unaffected.
func TestCurves_DoesNotAliasInternalState(t *testing.T) {
	s := New("corr-alias-curves")
	applyAliasPatch(t, s)

	c1, _ := s.Curves()
	c1[0].History[0] = 99999
	c1[0].Projection[0] = -88888
	c1[0].ConfidenceUpper[0] = 77777
	c1[0].ConfidenceLower[0] = -66666
	c1[0].Thresholds[0].Value = 55555
	c1[0].Markers[0].MonthOffset = 4242

	c2, _ := s.Curves()
	got := c2[0]
	switch {
	case got.History[0] == 99999:
		t.Errorf("Curves() aliases internal state: mutating returned History[0] changed the next Curves() result")
	case got.Projection[0] == -88888:
		t.Errorf("Curves() aliases internal state: mutating returned Projection[0] changed the next Curves() result")
	case got.ConfidenceUpper[0] == 77777:
		t.Errorf("Curves() aliases internal state: mutating returned ConfidenceUpper[0] changed the next Curves() result")
	case got.ConfidenceLower[0] == -66666:
		t.Errorf("Curves() aliases internal state: mutating returned ConfidenceLower[0] changed the next Curves() result")
	case got.Thresholds[0].Value == 55555:
		t.Errorf("Curves() aliases internal state: mutating returned Thresholds[0] changed the next Curves() result")
	case got.Markers[0].MonthOffset == 4242:
		t.Errorf("Curves() aliases internal state: mutating returned Markers[0] changed the next Curves() result")
	}
}

// TestCrossings_DoesNotAliasInternalState mutates the two series on the
// Crossings() result and asserts the next Crossings() call is unaffected.
func TestCrossings_DoesNotAliasInternalState(t *testing.T) {
	s := New("corr-alias-crossings")
	applyAliasPatch(t, s)

	x1, _ := s.Crossings()
	x1[0].InternalDemand[0] = 111111
	x1[0].ContractedCapacity[0] = 222222

	x2, _ := s.Crossings()
	if x2[0].InternalDemand[0] == 111111 {
		t.Errorf("Crossings() aliases internal state: mutating returned InternalDemand[0] changed the next Crossings() result")
	}
	if x2[0].ContractedCapacity[0] == 222222 {
		t.Errorf("Crossings() aliases internal state: mutating returned ContractedCapacity[0] changed the next Crossings() result")
	}
}

// TestRateOutlook_DoesNotAliasInternalState mutates the two series on the
// RateOutlook() result and asserts the next RateOutlook() call is
// unaffected.
func TestRateOutlook_DoesNotAliasInternalState(t *testing.T) {
	s := New("corr-alias-rate")
	applyAliasPatch(t, s)

	r1, _, _ := s.RateOutlook()
	r1.History[0] = 333333
	r1.Projection[0] = 444444

	r2, _, _ := s.RateOutlook()
	if r2.History[0] == 333333 {
		t.Errorf("RateOutlook() aliases internal state: mutating returned History[0] changed the next RateOutlook() result")
	}
	if r2.Projection[0] == 444444 {
		t.Errorf("RateOutlook() aliases internal state: mutating returned Projection[0] changed the next RateOutlook() result")
	}
}
