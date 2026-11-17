package citizens

import (
	"testing"
)

// FEAT-2326609845 -- housing ledger behaviour.

func housingAPI(t *testing.T, n int) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(7, "housing-test")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		recs = append(recs, ColdRecord{ID: uint64(i)})
	}
	if err := api.SeedColdRecords(recs, "housing-test"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	return api
}

func homeOf(t *testing.T, api *CitizensAPI, id uint64) CellRef {
	t.Helper()
	r, ok := api.coldRecord(id)
	if !ok {
		t.Fatalf("citizen %d not resident", id)
	}
	return r.Home
}

// Lowest ids fill the lowest cell first; the overflow stays homeless.
func TestHousing_AssignsInIdOrderIntoLowestCellFirst(t *testing.T) {
	api := housingAPI(t, 10)
	// Capacity given out of order and with a cell-0 structure that must be ignored.
	caps := []HomeCapacity{{Cell: 900, Capacity: 2}, {Cell: 0, Capacity: 50}, {Cell: 500, Capacity: 3}}
	if err := api.SetHomeCapacities(caps, "c"); err != nil {
		t.Fatal(err)
	}
	n, err := api.AssignHomes("c")
	if err != nil || n != 5 {
		t.Fatalf("housed=%d err=%v, want 5", n, err)
	}
	for id := uint64(1); id <= 3; id++ {
		if h := homeOf(t, api, id); h != 500 {
			t.Errorf("citizen %d home=%d, want 500", id, h)
		}
	}
	for id := uint64(4); id <= 5; id++ {
		if h := homeOf(t, api, id); h != 900 {
			t.Errorf("citizen %d home=%d, want 900", id, h)
		}
	}
	for id := uint64(6); id <= 10; id++ {
		if h := homeOf(t, api, id); h != 0 {
			t.Errorf("citizen %d home=%d, want 0 (no capacity left)", id, h)
		}
	}
	if got := api.HomelessCount(); got != 5 {
		t.Errorf("HomelessCount=%d, want 5", got)
	}
}

// A death frees the slot and the next homeless citizen takes it.
func TestHousing_DeathReleasesSlot(t *testing.T) {
	api := housingAPI(t, 4)
	if err := api.SetHomeCapacities([]HomeCapacity{{Cell: 77, Capacity: 3}}, "c"); err != nil {
		t.Fatal(err)
	}
	if n, _ := api.AssignHomes("c"); n != 3 {
		t.Fatalf("housed=%d, want 3", n)
	}
	if got := api.HomeOccupancy(77); got != 3 {
		t.Fatalf("occupancy=%d, want 3", got)
	}
	api.mu.Lock()
	api.removeColdLocked(2)
	api.mu.Unlock()
	if got := api.HomeOccupancy(77); got != 2 {
		t.Fatalf("occupancy after death=%d, want 2 (slot not released)", got)
	}
	if n, _ := api.AssignHomes("c"); n != 1 {
		t.Fatalf("housed after death=%d, want 1", n)
	}
	if h := homeOf(t, api, 4); h != 77 {
		t.Errorf("citizen 4 home=%d, want 77 (should take the freed slot)", h)
	}
}

// Newborns after the ledger is on queue as pending and are housed; a dead
// pending id is dropped, never housed.
func TestHousing_BirthsQueueAndDeadPendingDropped(t *testing.T) {
	api := housingAPI(t, 1)
	if err := api.SetHomeCapacities([]HomeCapacity{{Cell: 10, Capacity: 5}}, "c"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{50, 51} {
		if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "c", Kind: LifeEventBirth, Citizen: Citizen{ID: id}}); err != nil {
			t.Fatal(err)
		}
	}
	api.mu.Lock()
	api.removeColdLocked(50) // dies while still homeless
	api.mu.Unlock()
	n, _ := api.AssignHomes("c")
	if n != 2 { // citizen 1 and 51
		t.Fatalf("housed=%d, want 2", n)
	}
	if h := homeOf(t, api, 51); h != 10 {
		t.Errorf("citizen 51 home=%d, want 10", h)
	}
	if got := api.HomeOccupancy(10); got != 2 {
		t.Errorf("occupancy=%d, want 2", got)
	}
}

// Demolition (capacity vanishes) evicts the highest ids to homeless and they
// rehouse when other capacity exists.
func TestHousing_CapacityCutEvictsHighestIdsAndRehouses(t *testing.T) {
	api := housingAPI(t, 4)
	_ = api.SetHomeCapacities([]HomeCapacity{{Cell: 5, Capacity: 4}}, "c")
	if n, _ := api.AssignHomes("c"); n != 4 {
		t.Fatalf("housed=%d, want 4", n)
	}
	_ = api.SetHomeCapacities([]HomeCapacity{{Cell: 5, Capacity: 2}, {Cell: 9, Capacity: 1}}, "c")
	if h := homeOf(t, api, 3); h != 0 {
		t.Errorf("citizen 3 home=%d, want 0 after cut", h)
	}
	if h := homeOf(t, api, 1); h != 5 {
		t.Errorf("citizen 1 home=%d, want 5 (lowest ids stay)", h)
	}
	if n, _ := api.AssignHomes("c"); n != 1 {
		t.Fatalf("rehoused=%d, want 1", n)
	}
	if h := homeOf(t, api, 3); h != 9 {
		t.Errorf("citizen 3 home=%d, want 9", h)
	}
	if h := homeOf(t, api, 4); h != 0 {
		t.Errorf("citizen 4 home=%d, want 0", h)
	}
	// Whole building demolished.
	_ = api.SetHomeCapacities(nil, "c")
	if got := api.HomelessCount(); got != 4 {
		t.Errorf("HomelessCount=%d, want 4 after all capacity removed", got)
	}
}

// The ledger rebuilds from the columns: dropping it (what a restore does) and
// re-enabling reproduces occupancy exactly and does not re-house anyone.
func TestHousing_RebuildFromColumnsAfterDrop(t *testing.T) {
	api := housingAPI(t, 6)
	caps := []HomeCapacity{{Cell: 3, Capacity: 2}, {Cell: 8, Capacity: 2}}
	_ = api.SetHomeCapacities(caps, "c")
	_, _ = api.AssignHomes("c")
	before := api.PopulationHash("c")
	api.mu.Lock()
	api.housing = nil
	api.mu.Unlock()
	if api.HousingEnabled() {
		t.Fatal("ledger should be off")
	}
	_ = api.SetHomeCapacities(caps, "c")
	if got := api.HomeOccupancy(3); got != 2 {
		t.Errorf("occupancy(3)=%d, want 2", got)
	}
	if n, _ := api.AssignHomes("c"); n != 0 {
		t.Errorf("rebuild re-housed %d citizens, want 0", n)
	}
	if after := api.PopulationHash("c"); after != before {
		t.Error("population hash changed across a ledger rebuild")
	}
}

// Same inputs, same homes (byte-identical hash).
func TestHousing_Deterministic(t *testing.T) {
	run := func() [32]byte {
		api := housingAPI(t, 40)
		_ = api.SetHomeCapacities([]HomeCapacity{{Cell: 70, Capacity: 7}, {Cell: 20, Capacity: 9}, {Cell: 55, Capacity: 11}}, "c")
		_, _ = api.AssignHomes("c")
		return api.PopulationHash("c")
	}
	first, second := run(), run()
	if first != second {
		t.Fatal("housing is not deterministic")
	}
}

// PopulationHash must cover Workplace (it previously hashed Home only).
func TestPopulationHash_CoversWorkplace(t *testing.T) {
	mk := func(w uint32) [32]byte {
		api, _ := NewCitizensAPI(1, "h")
		r := ColdRecord{ID: 1}
		r.Workplace = w
		_ = api.SeedColdRecords([]ColdRecord{r}, "h")
		return api.PopulationHash("h")
	}
	if mk(0) == mk(9) {
		t.Fatal("PopulationHash ignores Workplace")
	}
}

// Fertility children inherit a parent's home while the ledger is on.
func TestHousing_ChildInheritsParentHome(t *testing.T) {
	api := housingAPI(t, 2)
	_ = api.SetHomeCapacities([]HomeCapacity{{Cell: 40, Capacity: 2}}, "c")
	_, _ = api.AssignHomes("c")
	api.mu.Lock()
	ok := api.birthChildLocked(1, 2, 0, 10, "c")
	api.mu.Unlock()
	if !ok {
		t.Fatal("birthChildLocked refused")
	}
	child := fertilityChildIDBase
	if h := homeOf(t, api, uint64(child)); h != 40 {
		t.Errorf("child home=%d, want 40", h)
	}
	if got := api.HomeOccupancy(40); got != 3 {
		t.Errorf("occupancy=%d, want 3", got)
	}
}
