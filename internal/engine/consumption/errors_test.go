package consumption

import "testing"

// TestDanglingRefReturnsRegistryError is AC-13: a consumptionRef that fails
// to resolve against data/consumption.json produces a registry-sourced
// error (MET-G301) at reference-resolution time, not a silent zero-demand
// default. The GR#7 assertion (BUG-100) is stated explicitly: the test
// asserts the returned error's registry code AND that no zero-demand
// default was applied — ClassDemand on a dangling ref must ERROR, not
// return a zero Demand.
func TestDanglingRefReturnsRegistryError(t *testing.T) {
	api := realAPI(t)

	_, err := api.ClassCoefficients("thisClassDoesNotExist")
	assertCode(t, err, ErrUnresolvedConsumptionRef)

	// No silent zero-demand default (GR#7/BUG-100): the demand query on the
	// same dangling ref must fail with the same registry code, not return a
	// zero-valued Demand.
	got, err := api.ClassDemand("thisClassDoesNotExist", 100, DemandOptions{MonthIndex: 0, GasNetworkPresent: true})
	if err == nil {
		t.Fatalf("ClassDemand on a dangling ref returned %+v with no error — silent zero-demand default (AC-13)", got)
	}
	assertCode(t, err, ErrUnresolvedConsumptionRef)
}

// TestUnresolvedRefReachesSolve proves the dangling-ref failure also
// surfaces through the solve entry point (SolveDailyTick), not only
// through the direct class queries.
func TestUnresolvedRefReachesSolve(t *testing.T) {
	api := realAPI(t)
	n := NewNetwork(UtilityWater, testCorrelationID())
	n.AddSource(Source{ID: "s", Capacity: 100})

	_, err := api.SolveDailyTick(n, []DemandEntity{
		{EntityRef: "e", ClassRef: "thisClassDoesNotExist", Occupancy: 1},
	}, DemandOptions{MonthIndex: 0, GasNetworkPresent: true})
	assertCode(t, err, ErrUnresolvedConsumptionRef)
}
