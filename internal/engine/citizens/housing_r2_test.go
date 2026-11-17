package citizens

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
)

// FEAT-2326609845 r2 -- housing ledger: save/restore of the capacity snapshot,
// the deathwave release pin, pending-heap compaction, ASM-1538.

// housedAt counts living citizens whose Home column is cell.
func housedAt(api *CitizensAPI, cell CellRef) int {
	api.mu.RLock()
	defer api.mu.RUnlock()
	n := 0
	for shard := range api.cold {
		s := api.shardAt(shard)
		for i := range s.ids {
			if s.homeCells[i] == uint32(cell) {
				n++
			}
		}
	}
	return n
}

func housingSaveLoad(t *testing.T, api *CitizensAPI) *CitizensAPI {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(api)}, "r2")
	must(t, mgr.SaveManual(save.Context{WorldSeed: 7, CreatedAtTick: 1, GameMonth: 1, AppVersion: "t"}, "r2"))
	out, err := NewCitizensAPI(7, "r2-load")
	must(t, err)
	lm := save.NewManager(root, []save.Participant{NewSaveParticipant(out)}, "r2")
	_, _, err = lm.Load(bundleDir(t, root))
	must(t, err)
	return out
}

// F4: the DEATHWAVE realisation path (AdvanceDayTick on the last day of a
// month) must release the dead citizen's dwelling slot.
func TestHousing_DeathwaveRealisationReleasesSlot(t *testing.T) {
	api := housingAPI(t, 4)
	must(t, api.SetHomeCapacities([]HomeCapacity{{Cell: 77, Capacity: 4}}, "c"))
	if n, _ := api.AssignHomes("c"); n != 4 {
		t.Fatalf("housed=%d, want 4", n)
	}
	must(t, api.deathQueue.Enqueue(2, 0, "c"))
	api.mu.Lock()
	api.dayTick = DaysPerMonth - 1
	api.mu.Unlock()
	if _, _, err := api.AdvanceDayTick("c"); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.coldRecord(2); ok {
		t.Fatal("citizen 2 should have been realised dead by the deathwave")
	}
	alive := housedAt(api, 77)
	if got := api.HomeOccupancy(77); got != alive {
		t.Fatalf("ledger occupancy=%d but %d live residents: deathwave death did not release the slot", got, alive)
	}
}

// F6: dead homeless ids do not pile up in the pending heap.
func TestHousing_DeadHomelessDoNotPileUpInPending(t *testing.T) {
	const n = 2000
	api := housingAPI(t, n)
	must(t, api.SetHomeCapacities(nil, "c")) // ledger on, no capacity: everyone pending
	for id := uint64(1); id <= n-100; id++ {
		api.mu.Lock()
		api.housingReleaseLocked(id, 0)
		api.removeColdLocked(id)
		api.mu.Unlock()
	}
	api.mu.RLock()
	got := len(api.housing.pending)
	api.mu.RUnlock()
	if got > 2*100+200 {
		t.Fatalf("pending heap holds %d entries for 100 live homeless citizens: dead ids piled up", got)
	}
}

// F1: the saved capacity snapshot survives a save/load, so a dwelling
// demolished AFTER the last sync is still seen as a cut by the next sync.
func TestHousing_SavedSnapshotDetectsCutAfterLoad(t *testing.T) {
	api := housingAPI(t, 4)
	must(t, api.SetHomeCapacities([]HomeCapacity{{Cell: 5, Capacity: 4}}, "c"))
	if n, _ := api.AssignHomes("c"); n != 4 {
		t.Fatalf("housed=%d", n)
	}
	loaded := housingSaveLoad(t, api)
	if !loaded.HousingEnabled() {
		t.Fatal("ledger flag not restored")
	}
	if api.PopulationHash("c") != loaded.PopulationHash("c") {
		t.Fatal("Load changed the population")
	}
	must(t, loaded.RestoreHousing("c"))
	if got := loaded.HomeOccupancy(5); got != 4 {
		t.Fatalf("restored occupancy=%d, want 4", got)
	}
	// The dwelling is gone at the next sync: the restored snapshot makes the
	// cut visible and all four are evicted.
	must(t, loaded.SetHomeCapacities(nil, "c"))
	if got := loaded.HomelessCount(); got != 4 {
		t.Fatalf("homeless after cut=%d, want 4 (snapshot lost across the save)", got)
	}
}

// Load must not assign: a freed slot with a pending citizen is still pending
// (and the hash unchanged) straight after Load; the next AssignHomes houses it.
func TestHousing_LoadDoesNotAssignHomes(t *testing.T) {
	api := housingAPI(t, 4)
	must(t, api.SetHomeCapacities([]HomeCapacity{{Cell: 5, Capacity: 3}}, "c"))
	if n, _ := api.AssignHomes("c"); n != 3 {
		t.Fatalf("housed=%d", n)
	}
	api.mu.Lock()
	api.housingReleaseLocked(2, uint32(5))
	api.removeColdLocked(2)
	api.mu.Unlock()
	before := api.PopulationHash("c")
	loaded := housingSaveLoad(t, api)
	must(t, loaded.RestoreHousing("c"))
	if loaded.PopulationHash("c") != before {
		t.Fatal("Load changed the population hash")
	}
	if got := loaded.HomelessCount(); got != 1 {
		t.Fatalf("homeless after load=%d, want 1 (Load must not assign)", got)
	}
	if n, _ := loaded.AssignHomes("c"); n != 1 {
		t.Fatalf("next sync housed %d, want 1", n)
	}
}

// Old saves (no housingOn flag) restore with the ledger off.
func TestHousing_OldSaveRestoresLedgerOff(t *testing.T) {
	api := housingAPI(t, 3) // ledger never enabled: the save looks like a pre-feature one
	loaded := housingSaveLoad(t, api)
	if loaded.HousingEnabled() {
		t.Fatal("a save with no housing flag must restore with the ledger off")
	}
	must(t, loaded.SetHomeCapacities([]HomeCapacity{{Cell: 9, Capacity: 3}}, "c"))
	if n, _ := loaded.AssignHomes("c"); n != 3 {
		t.Fatalf("housed=%d, want 3 (rebuilt from the current build state)", n)
	}
}

// ASM-1538: births may overcrowd; a capacity that is unchanged never trims,
// a cut trims the HIGHEST ids first.
func TestHousing_ASM1538_OvercrowdingTrimsOnlyOnCut(t *testing.T) {
	api := housingAPI(t, 2)
	must(t, api.SetHomeCapacities([]HomeCapacity{{Cell: 40, Capacity: 2}}, "c"))
	_, _ = api.AssignHomes("c")
	api.mu.Lock()
	ok := api.birthChildLocked(1, 2, 0, 10, "c")
	api.mu.Unlock()
	if !ok {
		t.Fatal("birth refused")
	}
	if got := api.HomeOccupancy(40); got != 3 {
		t.Fatalf("occupancy=%d, want 3 (birth overcrowds)", got)
	}
	must(t, api.SetHomeCapacities([]HomeCapacity{{Cell: 40, Capacity: 2}}, "c")) // unchanged
	if got := api.HomeOccupancy(40); got != 3 {
		t.Fatalf("unchanged capacity trimmed the dwelling: occupancy=%d, want 3", got)
	}
	must(t, api.SetHomeCapacities([]HomeCapacity{{Cell: 40, Capacity: 1}}, "c")) // a cut
	if got := api.HomeOccupancy(40); got != 1 {
		t.Fatalf("occupancy after cut=%d, want 1", got)
	}
	if h := homeOf(t, api, 1); h != 40 {
		t.Errorf("lowest id evicted: home=%d, want 40", h)
	}
}
