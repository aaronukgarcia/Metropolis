package citizens

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSourceHasNoSharedRNGOrWallClock (AC-15/AC-20, SG-7): a mechanical
// self-scan of this package's own non-test source proves there is no
// shared RNG object (no math/rand, no rand.New) and no wall-clock call
// (no time.Now( / time.Since() on any logic path). This backs the Tester's
// grep with an in-suite proof.
func TestSourceHasNoSharedRNGOrWallClock(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		src := string(b)
		if strings.Contains(src, `"math/rand"`) {
			t.Fatalf("%s imports math/rand (AC-15: no shared RNG object)", name)
		}
		if strings.Contains(src, "rand.New") {
			t.Fatalf("%s uses rand.New (AC-15)", name)
		}
		if strings.Contains(src, "time.Now(") || strings.Contains(src, "time.Since(") {
			t.Fatalf("%s calls the wall clock (AC-20: sim time is the only time)", name)
		}
	}
}

// TestShardCountInvariance (AC-17): the same (worldSeed, command log)
// produces a bit-identical population hash after N months at worker count
// 1 vs 14 — the cold pass merges in shard order, never completion order.
func TestShardCountInvariance(t *testing.T) {
	records := make([]ColdRecord, 3000)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%10))
		records[i].BirthMonth = 0
	}

	run := func(workers int) [32]byte {
		api, err := NewCitizensAPI(42, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = workers
		if err := api.SeedColdRecords(records, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		for m := 0; m < 3; m++ {
			if err := api.AdvanceMonth("corr"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.PopulationHash("corr")
	}

	h1 := run(1)
	h14 := run(14)
	if h1 != h14 {
		t.Fatalf("worker-count invariance violated: hash(1 worker) = %x, hash(14 workers) = %x", h1, h14)
	}
}

// TestPopulationHashDeterministic: the same state hashes identically twice
// (the fingerprint is stable, not order-dependent).
func TestPopulationHashDeterministic(t *testing.T) {
	api, err := NewCitizensAPI(9, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	records := make([]ColdRecord, 200)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%7))
		records[i].BirthMonth = 0
	}
	if err := api.SeedColdRecords(records, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	a := api.PopulationHash("corr")
	b := api.PopulationHash("corr")
	if a != b {
		t.Fatalf("PopulationHash not stable: %x vs %x", a, b)
	}
}
