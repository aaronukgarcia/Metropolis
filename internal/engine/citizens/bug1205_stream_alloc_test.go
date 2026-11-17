package citizens

import "testing"

// TestBUG1205_ColdStreamNextNoPerRecordAllocs guards the save hot path: next()
// must not mint a correlation id (or allocate at all) per record when the
// copy-guard passes. 12M allocs at 1M citizens came from exactly this.
func TestBUG1205_ColdStreamNextNoPerRecordAllocs(t *testing.T) {
	api, err := NewCitizensAPI(5, "bug1205")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	recs := make([]ColdRecord, 2000)
	for i := range recs {
		recs[i] = ColdRecord{ID: uint64(1000 + i)}
	}
	if err := api.SeedColdRecords(recs, "bug1205"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	cs := api.newColdStream()
	if _, ok := cs.next(); !ok { // warm: loads the first shard buffer
		t.Fatal("empty stream")
	}
	// Stay within the first shard's buffer so shard loads do not count.
	n := len(cs.buf) - cs.pos - 1
	if n < 2 {
		t.Skip("first shard too small to measure")
	}
	if n > 100 {
		n = 100
	}
	allocs := testing.AllocsPerRun(n, func() { cs.next() })
	if allocs > 0 {
		t.Fatalf("coldStream.next allocates %.2f per record, want 0 (correlation id minted on the hot path?)", allocs)
	}
}

// TestBUG1205_ColdStreamNextCopyGuardStillFails proves the guard still fires.
func TestBUG1205_ColdStreamNextCopyGuardStillFails(t *testing.T) {
	api, err := NewCitizensAPI(5, "bug1205")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	cs := &coldStream{c: citizensByteCopy(api)}
	if _, ok := cs.next(); ok {
		t.Fatal("next() succeeded on a struct-copied CitizensAPI; copy guard lost")
	}
}
