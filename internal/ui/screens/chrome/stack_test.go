package chrome

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// drill builds a whole-view dash.DrillTarget from a bare view name — the
// test shorthand for "navigate to this screen, no entity row".
func drill(view string) dash.DrillTarget { return dash.DrillTarget{ViewName: view} }

// mustAlert builds a valid Alert via NewAlert, failing the test on error —
// so ordering tests exercise the real constructor, not a hand-built value.
// view is the alert's whole-view navigation target (no entity row).
func mustAlert(t *testing.T, id string, tier Tier, crisis bool, view string, tick protocol.Tick) Alert {
	t.Helper()
	a, err := NewAlert(id, "alert "+id, tier, crisis, drill(view), tick)
	if err != nil {
		t.Fatalf("NewAlert(%q): %v", id, err)
	}
	return a
}

// ids returns the IDs of alerts in order, for compact assertions.
func ids(alerts []Alert) []string {
	out := make([]string, len(alerts))
	for i, a := range alerts {
		out[i] = a.ID
	}
	return out
}

// TestStackPrioritisesByTier is AC-5's adversarial check: a low-tier alert,
// then a mid-tier alert, then a HIGH-tier alert are inserted in that order,
// and the top of the stack must be the high-tier alert DESPITE arriving
// last. A stack that only *looks* prioritised because fixtures happen to
// insert in priority order (i.e. an insertion-order stack) fails this.
func TestStackPrioritisesByTier(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})
	// nil palette is fine here: ordering never touches colour.

	low := mustAlert(t, "low", TierInfo, false, "f1", protocol.Tick(1))
	mid := mustAlert(t, "mid", TierWarning, false, "f2", protocol.Tick(2))
	high := mustAlert(t, "high", TierCritical, false, "f3", protocol.Tick(3))

	if err := c.AddAlert(low); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAlert(mid); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAlert(high); err != nil {
		t.Fatal(err)
	}

	want := []string{"high", "mid", "low"}
	if got := ids(c.Alerts()); !reflect.DeepEqual(got, want) {
		t.Fatalf("stack order = %v, want %v (tier must dominate arrival order)", got, want)
	}
	if top, ok := c.Top(); !ok || top.ID != "high" {
		t.Fatalf("Top() = (%q, %v), want (\"high\", true)", top.ID, ok)
	}
}

// TestStackTieBreakOldestFirst is AC-5's second check: two same-tier alerts
// are ordered by the documented tie-break (oldest — lowest Tick — first),
// NOT by arrival order. The older alert is inserted SECOND here, so an
// insertion-order tie-break would put the newer one first and fail.
func TestStackTieBreakOldestFirst(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	newer := mustAlert(t, "newer", TierWarning, false, "f2", protocol.Tick(9))
	older := mustAlert(t, "older", TierWarning, false, "f2", protocol.Tick(4))

	if err := c.AddAlert(newer); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAlert(older); err != nil {
		t.Fatal(err)
	}

	want := []string{"older", "newer"}
	if got := ids(c.Alerts()); !reflect.DeepEqual(got, want) {
		t.Fatalf("same-tier order = %v, want %v (oldest-first, not arrival order)", got, want)
	}
}

// TestStackTieBreakEqualTickByID is the tertiary tie-break: two same-tier
// alerts raised on the SAME tick are ordered by ascending ID, so the order
// is fully determined by the alerts' own data — never map iteration or
// arrival-order-by-accident.
func TestStackTieBreakEqualTickByID(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	b := mustAlert(t, "b", TierInfo, false, "f2", protocol.Tick(7))
	a := mustAlert(t, "a", TierInfo, false, "f2", protocol.Tick(7))

	if err := c.AddAlert(b); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAlert(a); err != nil {
		t.Fatal(err)
	}

	want := []string{"a", "b"}
	if got := ids(c.Alerts()); !reflect.DeepEqual(got, want) {
		t.Fatalf("equal-tick order = %v, want %v (ascending ID tie-break)", got, want)
	}
}

// TestOrderingIndependentOfInsertionOrder is AC-14's determinism check: the
// SAME set of alerts, fed in two different orders, yields the SAME stack
// order — the ordering is a pure function of (Tier, Tick, ID), never of
// insertion order. A map-iteration- or arrival-order-dependent ordering
// would produce different results across the two insertions and fail.
func TestOrderingIndependentOfInsertionOrder(t *testing.T) {
	set := []Alert{
		mustAlert(t, "low1", TierInfo, false, "f1", protocol.Tick(1)),
		mustAlert(t, "warn2", TierWarning, false, "f2", protocol.Tick(2)),
		mustAlert(t, "crit3", TierCritical, false, "f3", protocol.Tick(3)),
		mustAlert(t, "warn1", TierWarning, false, "f2", protocol.Tick(1)),
		mustAlert(t, "low2", TierInfo, false, "f1", protocol.Tick(2)),
	}

	// Two different arrival orders over the same set.
	forward := append([]Alert(nil), set...)
	reverse := append([]Alert(nil), set[4], set[3], set[2], set[1], set[0])

	orderFor := func(in []Alert) []string {
		c := NewChrome("test", widgets.DefaultPalette, Effects{})
		for _, a := range in {
			if err := c.AddAlert(a); err != nil {
				t.Fatal(err)
			}
		}
		return ids(c.Alerts())
	}

	gotForward := orderFor(forward)
	gotReverse := orderFor(reverse)

	if !reflect.DeepEqual(gotForward, gotReverse) {
		t.Fatalf("stack order differs by arrival order:\n forward: %v\n reverse: %v", gotForward, gotReverse)
	}

	want := []string{"crit3", "warn1", "warn2", "low1", "low2"}
	if !reflect.DeepEqual(gotForward, want) {
		t.Fatalf("stack order = %v, want %v", gotForward, want)
	}
}

// TestSortAlertsIsPureAndStable re-checks the sort helper directly: it
// returns a sorted copy and never mutates its input — the invariant Render's
// lock-free snapshot relies on.
func TestSortAlertsIsPureAndStable(t *testing.T) {
	in := []Alert{
		mustAlert(t, "z", TierInfo, false, "f1", protocol.Tick(1)),
		mustAlert(t, "a", TierCritical, false, "f3", protocol.Tick(1)),
	}
	before := append([]Alert(nil), in...)
	out := sortAlerts(in)

	if !reflect.DeepEqual(in, before) {
		t.Fatal("sortAlerts mutated its input slice")
	}
	if got := ids(out); !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("sortAlerts = %v, want [a z]", got)
	}
}
