package capexport

import "testing"

// TestContractCrossingOccurs (AC-2, the headline): with a fixed-capacity
// contract held constant and internal demand projected to grow, the
// contracted-vs-internal-demand curves registered with ProjectionsAPI actually
// cross within the observation window — a month exists where the queried
// internal demand exceeds (capacity − committed), which did not hold at the
// start. Read back from ProjectionsAPI.Curve, not merely asserted on the
// module's own accessors.
func TestContractCrossingOccurs(t *testing.T) {
	a, svc, _, proj := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 10) // start demand 10, far below headroom
	bindLine(t, a, ExportHospitalBeds, id)

	if err := a.SetDemandGrowth(0.05); err != nil {
		t.Fatalf("SetDemandGrowth: %v", err)
	}
	if _, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 30, TermMonths: 120, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract: %v", err)
	}
	if err := a.RegisterContractCurves(); err != nil {
		t.Fatalf("RegisterContractCurves: %v", err)
	}

	demand, err := proj.Curve(DemandCurveKey(ExportHospitalBeds), 0, 60)
	if err != nil {
		t.Fatalf("Curve(demand): %v", err)
	}
	headroom, err := proj.Curve(HeadroomCurveKey(ExportHospitalBeds), 0, 60)
	if err != nil {
		t.Fatalf("Curve(headroom): %v", err)
	}
	if len(demand) != 61 || len(headroom) != 61 {
		t.Fatalf("curve length = %d/%d, want 61 each", len(demand), len(headroom))
	}

	// The start must not already be crossed — the crossing is a future event.
	if demand[0].Value > headroom[0].Value {
		t.Fatalf("month 0: demand %v > headroom %v, want no crossing at the start", demand[0].Value, headroom[0].Value)
	}

	// The crossing must be reachable within the window.
	crossed := false
	for i := range demand {
		if demand[i].Value > headroom[i].Value {
			crossed = true
			break
		}
	}
	if !crossed {
		t.Fatalf("internal demand never exceeded headroom across months 0..60 — the crossing is structurally unreachable (AC-2 rejects a capped-demand model)")
	}
}

// TestContractCrossingControlFlatNeverCrosses (AC-2 control): the reverse is
// constructible — with flat (zero-growth) demand the curves never cross, so the
// crossing is a real growth phenomenon, not a vacuous "always crossing" model.
func TestContractCrossingControlFlatNeverCrosses(t *testing.T) {
	a, svc, _, proj := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 10)
	bindLine(t, a, ExportHospitalBeds, id)

	if err := a.SetDemandGrowth(0); err != nil {
		t.Fatalf("SetDemandGrowth: %v", err)
	}
	if _, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 30, TermMonths: 120, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract: %v", err)
	}
	if err := a.RegisterContractCurves(); err != nil {
		t.Fatalf("RegisterContractCurves: %v", err)
	}

	demand, err := proj.Curve(DemandCurveKey(ExportHospitalBeds), 0, 60)
	if err != nil {
		t.Fatalf("Curve(demand): %v", err)
	}
	headroom, err := proj.Curve(HeadroomCurveKey(ExportHospitalBeds), 0, 60)
	if err != nil {
		t.Fatalf("Curve(headroom): %v", err)
	}
	for i := range demand {
		if demand[i].Value > headroom[i].Value {
			t.Fatalf("flat demand crossed headroom at month %d (demand %v > headroom %v) — a flat line must never cross", i, demand[i].Value, headroom[i].Value)
		}
	}
}
