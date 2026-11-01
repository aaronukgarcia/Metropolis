package citizens

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// FEAT-2326609842 -- the occupant index re-verifies every returned id against
// the live store, so a citizen who leaves the store WITHOUT bumping the index
// stamp (a mid-month cold-pass death realisation) is never listed as a live
// occupant. This test removes the row directly, below the occGen bump, to
// exercise exactly that re-verify path.
func TestOccupantsPage_ReverifiesDeadWithoutStampChange(t *testing.T) {
	api, err := NewCitizensAPI(1, "occ-test")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	mk := func(id uint64) ColdRecord {
		r := ColdRecord{ID: id}
		r.Home, r.Workplace = 4242, 17
		return r
	}
	if err := api.SeedColdRecords([]ColdRecord{mk(1000), mk(1001), mk(1002)}, "occ-test"); err != nil {
		t.Fatal(err)
	}
	ids, total, _, err := api.OccupantsPage(OccupantsHome, 4242, 0, 10, "occ-test")
	const wantResidents = 3
	if err != nil || total != wantResidents || len(ids) != wantResidents {
		t.Fatalf("pre: ids=%v total=%d err=%v", ids, total, err)
	}
	// Remove 1001 straight from its shard: the index (and its stamp) is stale.
	s := api.shardAt(det.ShardForEntity(1001))
	row := s.rowOf(1001)
	if row < 0 {
		t.Fatal("1001 not resident")
	}
	api.mu.Lock()
	s.removeAt(row)
	api.mu.Unlock()
	ids, total, _, err = api.OccupantsPage(OccupantsWorkplace, 17, 0, 10, "occ-test")
	if err != nil {
		t.Fatal(err)
	}
	const wantRemaining = 2
	if total != wantRemaining {
		t.Errorf("total after the walk compacts the dead entry out expected 2 (documented: the index's remaining count), got %d", total)
	}
	for _, id := range ids {
		if id == 1001 {
			t.Fatalf("dead citizen 1001 still listed: %v", ids)
		}
	}
	if len(ids) != 2 {
		t.Errorf("want 2 live occupants, got %v", ids)
	}
	if _, _, ok := func() (ColdRecord, int64, bool) { return api.PassportRecord(1001, "occ-test") }(); ok {
		t.Error("PassportRecord answered for a removed citizen")
	}
}

func TestOccupantsPage_RejectsBadArgs(t *testing.T) {
	api, err := NewCitizensAPI(1, "occ-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := api.OccupantsPage(OccupantsHome, 1, 0, 0, "c"); err == nil {
		t.Error("limit 0 accepted")
	}
	if _, _, _, err := api.OccupantsPage(OccupantsHome, 1, 0, -1, "c"); err == nil {
		t.Error("negative limit accepted")
	}
	if _, _, _, err := api.OccupantsPage(OccupantKind(9), 1, 0, 5, "c"); err == nil {
		t.Error("unknown kind accepted")
	}
}
