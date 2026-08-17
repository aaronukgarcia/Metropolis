package census

import (
	"testing"
)

// TestLeastCheckInAcrossFacets proves the "least" reading (ASM-645): with
// every facet refreshed the least-check-in equals the current tick, and
// with one facet stale the least-check-in equals that stale facet's
// timestamp — the minimum across facets, not the latest refresh (AC-5).
func TestLeastCheckInAcrossFacets(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	w.finance.setIncome(1, 100_000_000)

	if err := c.RunObservers(5, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}
	rec, err := c.CheckIn(citizenGUID(1))
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if rec.LeastCheckIn != 5 {
		t.Fatalf("all facets refreshed: least=%d want 5", rec.LeastCheckIn)
	}

	// Stop refreshing only the income facet (finance no longer tracks it).
	w.finance.removeIncome(1)
	if err := c.RunObservers(6, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}
	rec, err = c.CheckIn(citizenGUID(1))
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if rec.LeastCheckIn != 5 {
		t.Fatalf("income facet stale: least=%d want 5 (the stale income timestamp)", rec.LeastCheckIn)
	}
	if rec.Facets[FacetJob] != 6 {
		t.Fatalf("job facet should refresh to 6, got %d", rec.Facets[FacetJob])
	}
	if rec.Facets[FacetIncome] != 5 {
		t.Fatalf("income facet should stay at 5, got %d", rec.Facets[FacetIncome])
	}
}

// TestLostObjectRetainedInTrackedSet proves the CC never silently forgets an
// object: a removed citizen is flagged lost once its least check-in lags
// past the threshold, AND its GUID stays in the tracked set (AC-6).
func TestLostObjectRetainedInTrackedSet(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	if err := c.RunObservers(0, "test"); err != nil {
		t.Fatalf("RunObservers(0): %v", err)
	}

	// Remove the citizen from the owning module and advance past the
	// threshold (lag > 2 ticks).
	w.citizens.remove(1)
	if err := c.RunObservers(3, "test"); err != nil {
		t.Fatalf("RunObservers(3): %v", err)
	}

	lost := c.LostObjects()
	if len(lost) != 1 {
		t.Fatalf("want 1 lost object, got %d: %+v", len(lost), lost)
	}
	if lost[0].GUID != citizenGUID(1) {
		t.Fatalf("lost object GUID = %s, want %s", lost[0].GUID, citizenGUID(1))
	}

	// The tracked set still retains the GUID — never silently forgotten.
	found := false
	for _, g := range c.TrackedObjects() {
		if g == citizenGUID(1) {
			found = true
		}
	}
	if !found {
		t.Fatalf("tracked set dropped the removed citizen's GUID")
	}
}
