package citizens

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// BUG-775 DESTRUCTIVE ROUND (GR#23) — independent attacker "opus-round-bug775".
//
// The fix under attack: CitizensAPI.PinForBatch(ids) prefetches and HOLDS
// every distinct shard an id slice touches for the duration of a
// full-population walk. This file attacks the claim that this is a safe
// paging optimisation rather than a paging DISABLER.

// pagedCity builds `n` citizens on sequential ids (the real engine's id
// shape) with disk paging enabled at `budget` resident shards.
func pagedCity(t *testing.T, n, budget int, dir string) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(42, "bug775-attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		r := mkRecord(uint64(i), uint16(i%64))
		r.BirthMonth = -300 - int64(i%60)
		r.Household = uint64((i + 1) / 2)
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "bug775-attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.SeedHouseholds(recs, "bug775-attack"); err != nil {
		t.Fatalf("SeedHouseholds: %v", err)
	}
	if err := api.EnableDiskPaging(dir, budget, "bug775-attack"); err != nil {
		t.Fatalf("EnableDiskPaging: %v", err)
	}
	return api
}

func idsOf(n int) []uint64 {
	ids := make([]uint64, 0, n)
	for i := 1; i <= n; i++ {
		ids = append(ids, uint64(i))
	}
	return ids
}

// ---------------------------------------------------------------------------
// A1 — THE PIN DEFEATS PAGING (the headline attack)
// ---------------------------------------------------------------------------

// TestAttackBug775PinForBatchBlowsResidencyBudget: at a realistic population
// the id slice of a full-population walk spans EVERY shard, so PinForBatch
// pins all 256 at once. Residency is then the UNPAGED footprint for the whole
// walk — i.e. paging is switched off precisely during the passes it exists to
// bound. Measured against the configured budget.
func TestAttackBug775PinForBatchBlowsResidencyBudget(t *testing.T) {
	for _, budget := range []int{32, 64} {
		t.Run(fmt.Sprintf("budget%d", budget), func(t *testing.T) {
			const n = 20000
			api := pagedCity(t, n, budget, t.TempDir())

			// EnableDiskPaging only shrinks residency lazily, on touch — do
			// one UNPINNED full walk first so the store is genuinely at
			// budget (this is also the pre-fix behaviour of the same walk).
			for _, id := range idsOf(n) {
				api.CitizenAt(id, "bug775-warm")
			}

			_, baseActual, _, basePins := residentSnapshot(api)
			if baseActual > budget {
				t.Fatalf("precondition: after EnableDiskPaging(%d) expected <=%d resident, got %d", budget, budget, baseActual)
			}
			if basePins != 0 {
				t.Fatalf("precondition: %d pins before any walk", basePins)
			}

			var msBefore, msDuring runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&msBefore)

			ids := idsOf(n)
			unpin := api.PinForBatch(ids)

			counted, actual, _, pins := residentSnapshot(api)
			runtime.ReadMemStats(&msDuring)

			t.Logf("budget=%d n=%d  BEFORE resident=%d heapAlloc=%.1fMB | DURING PIN resident=%d(counted %d) pins=%d heapAlloc=%.1fMB",
				budget, n, baseActual, float64(msBefore.HeapAlloc)/1e6,
				actual, counted, pins, float64(msDuring.HeapAlloc)/1e6)

			unpin()
			_, afterActual, _, afterPins := residentSnapshot(api)
			t.Logf("AFTER UNPIN resident=%d pins=%d", afterActual, afterPins)

			if afterPins != 0 {
				t.Errorf("PIN LEAK: %d pins still held after unpin()", afterPins)
			}
			if actual > budget {
				t.Errorf("PAGING DEFEATED: budget=%d but %d/%d shards held resident for the whole walk (%.0f%% of unpaged footprint); "+
					"heap during pin %.1fMB vs %.1fMB before",
					budget, actual, numColdShards, 100*float64(actual)/float64(numColdShards),
					float64(msDuring.HeapAlloc)/1e6, float64(msBefore.HeapAlloc)/1e6)
			}
		})
	}
}

// TestAttackBug775ShardSpanOfAWalk quantifies attack (5): is "all 256 shards
// per walk" inherent, or an artefact of population size?
func TestAttackBug775ShardSpanOfAWalk(t *testing.T) {
	for _, n := range []int{100, 400, 2000, 20000, 100000} {
		var seen [numColdShards]bool
		d := 0
		for i := 1; i <= n; i++ {
			s := det.ShardForEntity(uint64(i))
			if !seen[s] {
				seen[s] = true
				d++
			}
		}
		t.Logf("population %7d -> %3d/%d distinct shards spanned by ONE full-population walk (pin holds all of them)", n, d, numColdShards)
	}
}

// ---------------------------------------------------------------------------
// A2 — CONCURRENCY: pinned walk vs concurrent engine paths
// ---------------------------------------------------------------------------

func TestAttackBug775PinnedWalkConcurrentWithEngine(t *testing.T) {
	const n = 4000
	api := pagedCity(t, n, 8, t.TempDir())
	ids := idsOf(n)

	done := make(chan struct{})
	var wg sync.WaitGroup

	// Goroutine 1: the pinned full-population walk, repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := 0; r < 20; r++ {
			func() {
				unpin := api.PinForBatch(ids)
				defer unpin()
				for _, id := range ids {
					api.CitizenAt(id, "bug775-walk")
				}
			}()
		}
	}()

	// Goroutine 2: concurrent unpinned reads + a mutating tick path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := 0; r < 20; r++ {
			for _, id := range ids[:200] {
				api.CitizenAt(id, "bug775-reader")
			}
			if _, _, err := api.AdvanceDayTick("bug775-tick"); err != nil {
				t.Errorf("AdvanceDayTick: %v", err)
				return
			}
		}
	}()

	// Goroutine 3: household reads (the households.DemandByType shape).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := 0; r < 20; r++ {
			hids := api.HouseholdIDs("bug775-hh")
			for _, hid := range hids {
				api.Household(hid, "bug775-hh")
			}
		}
	}()

	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Minute):
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		t.Fatalf("DEADLOCK: pinned walk + concurrent engine paths did not finish in 4m\n%s", buf)
	}

	_, _, _, pins := residentSnapshot(api)
	if pins != 0 {
		t.Errorf("PIN LEAK after concurrent run: %d pins held", pins)
	}
}

// ---------------------------------------------------------------------------
// A3 — FAIL-CLOSED / PIN HYGIENE ON I/O ERROR
// ---------------------------------------------------------------------------

// TestAttackBug775IOFailureMidWalk makes the page dir unwritable partway
// through a pinned walk and asserts (a) no pin leak, (b) the failure is not
// silently swallowed into corrupt state.
func TestAttackBug775IOFailureMidWalk(t *testing.T) {
	dir := t.TempDir()
	const n = 4000
	api := pagedCity(t, n, 8, dir)
	ids := idsOf(n)

	// Force some page files to exist, then corrupt one so Load fails.
	for _, id := range ids {
		api.CitizenAt(id, "bug775-warm")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	corrupted := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".page" {
			if err := os.WriteFile(filepath.Join(dir, e.Name()), []byte("not a gob stream at all"), 0o644); err != nil {
				t.Fatalf("corrupt write: %v", err)
			}
			corrupted++
			if corrupted >= 4 {
				break
			}
		}
	}
	t.Logf("corrupted %d page files of %d entries", corrupted, len(entries))

	func() {
		unpin := api.PinForBatch(ids)
		defer unpin()
		for _, id := range ids {
			api.CitizenAt(id, "bug775-walk-after-corrupt")
		}
	}()

	_, _, _, pins := residentSnapshot(api)
	if pins != 0 {
		t.Errorf("PIN LEAK after corrupt-page walk: %d pins held", pins)
	}
	if api.pageFault.Load() == nil && corrupted > 0 {
		t.Errorf("SILENT FAILURE: %d page files corrupted but pageFault was never latched", corrupted)
	} else if api.pageFault.Load() != nil {
		t.Logf("pageFault latched as expected: %v", api.pageFault.Load())
	}
}

// TestAttackBug775DoubleUnpin: calling the returned unpin twice must not
// underflow the refcount into "never pinned" (the BUG-664 P0 shape).
func TestAttackBug775DoubleUnpin(t *testing.T) {
	api := pagedCity(t, 500, 8, t.TempDir())
	ids := idsOf(500)
	unpin := api.PinForBatch(ids)
	unpin()
	unpin() // double release
	_, _, _, pins := residentSnapshot(api)
	if pins != 0 {
		t.Errorf("pins=%d after double unpin (expected 0, and no underflow)", pins)
	}
	// Still usable afterwards.
	if _, ok := api.CitizenAt(1, "bug775-post"); !ok {
		t.Error("CitizenAt(1) failed after double unpin")
	}
}

// TestAttackBug775PinDisabledPaging: no paging -> no-op, no allocation of pins.
func TestAttackBug775PinDisabledPaging(t *testing.T) {
	api, err := NewCitizensAPI(7, "bug775-nopage")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	recs := make([]ColdRecord, 0, 100)
	for i := 1; i <= 100; i++ {
		recs = append(recs, mkRecord(uint64(i), uint16(i%64)))
	}
	if err := api.SeedColdRecords(recs, "bug775-nopage"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	unpin := api.PinForBatch(idsOf(100))
	if unpin == nil {
		t.Fatal("PinForBatch returned nil unpin")
	}
	unpin()
	unpin()
}

// ---------------------------------------------------------------------------
// A4 — DETERMINISM: pin ON/OFF and across residency budgets
// ---------------------------------------------------------------------------

// TestAttackBug775DeterminismAcrossPinAndBudget drives the same city through
// the same tick sequence with (a) no paging, (b) paging at 32 with pinned
// walks, (c) paging at 256 with pinned walks, (d) paging at 32 with UNPINNED
// walks — the PopulationHash must be byte-identical in all four.
func TestAttackBug775DeterminismAcrossPinAndBudget(t *testing.T) {
	const n = 2000
	run := func(budget int, pin bool) [32]byte {
		var api *CitizensAPI
		if budget < 0 {
			var err error
			api, err = NewCitizensAPI(42, "bug775-det")
			if err != nil {
				t.Fatalf("NewCitizensAPI: %v", err)
			}
			recs := make([]ColdRecord, 0, n)
			for i := 1; i <= n; i++ {
				r := mkRecord(uint64(i), uint16(i%64))
				r.BirthMonth = -300 - int64(i%60)
				r.Household = uint64((i + 1) / 2)
				recs = append(recs, r)
			}
			if err := api.SeedColdRecords(recs, "bug775-det"); err != nil {
				t.Fatalf("SeedColdRecords: %v", err)
			}
			if err := api.SeedHouseholds(recs, "bug775-det"); err != nil {
				t.Fatalf("SeedHouseholds: %v", err)
			}
		} else {
			api = pagedCity(t, n, budget, t.TempDir())
		}
		ids := idsOf(n)
		for m := 0; m < 24; m++ {
			func() {
				if pin {
					unpin := api.PinForBatch(ids)
					defer unpin()
				}
				for _, id := range ids {
					api.CitizenAt(id, "bug775-det")
				}
			}()
			for d := 0; d < 30; d++ {
				if _, _, err := api.AdvanceDayTick("bug775-det"); err != nil {
					t.Fatalf("AdvanceDayTick: %v", err)
				}
			}
			if err := api.AdvanceMonth("bug775-det"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.PopulationHash("bug775-det")
	}
	base := run(-1, false)
	cases := []struct {
		name   string
		budget int
		pin    bool
	}{
		{"paged32-pinned", 32, true},
		{"paged256-pinned", 256, true},
		{"paged32-unpinned", 32, false},
	}
	for _, c := range cases {
		got := run(c.budget, c.pin)
		if got != base {
			t.Errorf("DETERMINISM DIVERGENCE %s: %x != unpaged %x", c.name, got, base)
		} else {
			t.Logf("%s hash matches unpaged baseline %x", c.name, base[:8])
		}
	}
}
