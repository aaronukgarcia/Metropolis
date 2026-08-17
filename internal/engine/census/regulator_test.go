package census

import (
	"testing"
)

// TestRegulatorCrimeThreshold proves the regulator emits a queryable
// finding (never an intervention) when crime exceeds its data-defined
// threshold, with the offending value and threshold populated (AC-7).
func TestRegulatorCrimeThreshold(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	w.crime.setRate(0.10) // above the 0.05 threshold
	if err := c.RunObservers(1, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}
	found := false
	for _, f := range c.Findings() {
		if f.Condition == FindingCrimeTooHigh {
			found = true
			if f.Value != 0.10 || f.Threshold != 0.05 {
				t.Fatalf("finding value/threshold wrong: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("crime-too-high finding did not fire: %+v", c.Findings())
	}
}

// TestRegulatorBoundaryDoesNotFire proves the documented inclusive/exclusive
// rule: a value at exactly the threshold does not fire (AC-7).
func TestRegulatorBoundaryDoesNotFire(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	w.crime.setRate(0.05) // exactly the threshold
	if err := c.RunObservers(1, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}
	for _, f := range c.Findings() {
		if f.Condition == FindingCrimeTooHigh {
			t.Fatalf("boundary value fired: %+v", f)
		}
	}
}
