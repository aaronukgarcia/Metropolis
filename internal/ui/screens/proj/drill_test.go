package proj

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestDrillTargets_EveryFigureTracesToTheProjectionsView is SF-5's check
// (PRJ's drill-through, consumed not reimplemented): every figure this
// screen displays — one per curve, one per crossing, and the rate-outlook
// figure — produces a canonical dash.DrillTarget whose ViewName is the
// screen's one subscribed view ("f7.projections") and whose EntityID names
// the sub-entity within it, so "Enter on this figure" always resolves to a
// real source, never a fabricated non-view (GR#3: no bespoke parallel
// type).
func TestDrillTargets_EveryFigureTracesToTheProjectionsView(t *testing.T) {
	curves := []Curve{
		{Key: "water.demand"},
		{Key: "school.places"},
	}
	crossings := []Crossing{
		{Key: "refuse.ashford"},
	}

	targets := DrillTargets(curves, crossings)
	want := len(curves) + len(crossings) + 1 // + the rate-outlook figure
	if len(targets) != want {
		t.Fatalf("DrillTargets produced %d targets for %d curves + %d crossings + rate, want %d",
			len(targets), len(curves), len(crossings), want)
	}

	// One target per curve, in order.
	for i, c := range curves {
		dt := targets[i]
		if dt.ViewName != ViewSubscriptionName {
			t.Errorf("curve %q ViewName = %q, want %q", c.Key, dt.ViewName, ViewSubscriptionName)
		}
		if dt.EntityID != "curve."+c.Key {
			t.Errorf("curve %q EntityID = %q, want %q", c.Key, dt.EntityID, "curve."+c.Key)
		}
	}
	// One target per crossing, in order.
	for i, x := range crossings {
		dt := targets[len(curves)+i]
		if dt.ViewName != ViewSubscriptionName {
			t.Errorf("crossing %q ViewName = %q, want %q", x.Key, dt.ViewName, ViewSubscriptionName)
		}
		if dt.EntityID != "crossing."+x.Key {
			t.Errorf("crossing %q EntityID = %q, want %q", x.Key, dt.EntityID, "crossing."+x.Key)
		}
	}
	// The rate-outlook figure.
	rate := targets[len(curves)+len(crossings)]
	if rate.ViewName != ViewSubscriptionName {
		t.Errorf("rate ViewName = %q, want %q", rate.ViewName, ViewSubscriptionName)
	}
	if rate.EntityID != "rate" {
		t.Errorf("rate EntityID = %q, want %q", rate.EntityID, "rate")
	}

	// SF-5's "not a dead end": every target names a non-empty,
	// grammar-valid view (the screen's own subscribed view).
	for i, dt := range targets {
		if dt.ViewName == "" {
			t.Errorf("target %d ViewName is empty (a dead end)", i)
		}
		if err := protocol.ValidateViewName(dt.ViewName); err != nil {
			t.Errorf("target %d ViewName = %q is not grammar-valid: %v", i, dt.ViewName, err)
		}
	}
}
