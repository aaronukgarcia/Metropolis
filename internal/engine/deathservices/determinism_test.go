package deathservices

import (
	"runtime"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// runScenario drives a fixed sequence of deaths and disposal commands
// against a fresh DeathServicesAPI using `workers` goroutines to submit
// the SAME command set (each goroutine handling a disjoint id range,
// mirroring a sharded cold-pass), and returns the final Conservation
// snapshot plus the sorted set of buried/cremated ids -- the state AC-18
// requires be byte-identical across worker counts.
func runScenario(t *testing.T, workers int) (Conservation, []uint64) {
	t.Helper()
	// A large daily throughput avoids the crematorium's own AC-5(c) queuing
	// cap introducing a SEPARATE, deliberate source of run-to-run variance
	// (which id wins one of a scarce number of daily slots under real
	// concurrent contention is a scheduling race, not a determinism defect
	// -- production callers batch-submit one Cremate call per crematorium
	// per day rather than one call per body, which is what makes the release
	// order deterministic in the live composition; this test isolates the
	// PLOT/BODY-STATE determinism question from that separate contention
	// concern by giving every concurrent call room to succeed).
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.CremationDailyThroughputPerBody.Value = 1000
	})
	d := NewDeathServicesAPI(cfg, "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	const n = 60
	deaths := make([]citizens.RealisedDeath, n)
	for i := 0; i < n; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	// Partition [1,n] into `workers` disjoint shards; each shard alternates
	// bury/cremate by parity, exactly as a real sharded cold-pass would
	// process a disjoint id range regardless of worker count.
	var wg sync.WaitGroup
	shardSize := (n + workers - 1) / workers
	for w := 0; w < workers; w++ {
		lo := w*shardSize + 1
		hi := lo + shardSize
		if hi > n+1 {
			hi = n + 1
		}
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for id := lo; id < hi; id++ {
				if id%2 == 0 {
					_ = d.Bury(uint64(id), "cem-1", 1, "corr")
				} else {
					_, _, _ = d.Cremate([]uint64{uint64(id)}, "crem-1", 1, "corr")
				}
			}
		}(lo, hi)
	}
	wg.Wait()

	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	terminal := make([]uint64, 0, n)
	for id := 1; id <= n; id++ {
		b, err := d.Body(uint64(id), "corr")
		if err != nil {
			t.Fatalf("Body(%d): %v", id, err)
		}
		if b.State == BodyBuried || b.State == BodyCremated {
			terminal = append(terminal, uint64(id))
		}
	}
	return snap, terminal
}

// TestDeterministicCremationUnderDailyCapContention (H6, round-2): the
// author's own determinism tests (TestDeterministicAcrossWorkerCounts,
// runScenario) deliberately raise the crematorium's daily throughput to
// 1000 to remove contention from the picture entirely -- masking exactly
// the class of bug attack_round_test.go's TestAttackDeterminismUnderPlotContention
// found in plot admission (fixed by cemetery.go's
// awaitingAheadCountLocked rank rule, crematory.go's Cremate now applying
// the identical rule per id). This test re-runs an analogous scenario for
// CREMATION specifically: many more Awaiting bodies than the crematorium's
// daily cap, disposed of via SEPARATE, disjoint-subset concurrent Cremate
// calls (one per worker, exactly the shape a naive multi-shard composition
// root might use) at worker counts 1, 4, and 20 -- proving the accept/
// reject partition is byte-identical regardless of which goroutine's call
// reaches the crematorium's counter first.
func TestDeterministicCremationUnderDailyCapContention(t *testing.T) {
	runCremationScenario := func(workers int) []BodyState {
		cfg := writeConfigFixture(t, func(c *Config) {
			c.Params.CremationDailyThroughputPerBody.Value = 10
		})
		d := NewDeathServicesAPI(cfg, "corr")
		if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
			t.Fatalf("RegisterCrematorium: %v", err)
		}
		const n = 90
		deaths := make([]citizens.RealisedDeath, n)
		for i := 0; i < n; i++ {
			deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
		}
		if _, err := d.Intake(deaths, "corr"); err != nil {
			t.Fatalf("Intake: %v", err)
		}
		var wg sync.WaitGroup
		shard := (n + workers - 1) / workers
		for w := 0; w < workers; w++ {
			lo := w*shard + 1
			hi := lo + shard
			if hi > n+1 {
				hi = n + 1
			}
			if lo >= hi {
				continue
			}
			wg.Add(1)
			go func(lo, hi int) {
				defer wg.Done()
				ids := make([]uint64, 0, hi-lo)
				for id := lo; id < hi; id++ {
					ids = append(ids, uint64(id))
				}
				// Each worker submits its own shard as ONE Cremate call
				// (a realistic per-shard batch), all racing for the SAME
				// crematorium's shared daily cap.
				_, _, _ = d.Cremate(ids, "crem-1", 1, "corr")
			}(lo, hi)
		}
		wg.Wait()
		out := make([]BodyState, 0, n)
		for id := 1; id <= n; id++ {
			b, err := d.Body(uint64(id), "corr")
			if err != nil {
				t.Fatalf("Body(%d): %v", id, err)
			}
			out = append(out, b.State)
		}
		return out
	}

	base := runCremationScenario(1)
	for _, w := range []int{4, 20} {
		got := runCremationScenario(w)
		for i := range base {
			if base[i] != got[i] {
				t.Fatalf("body %d state at workers=1 is %q but at workers=%d is %q -- cremation admission under daily-cap contention depends on goroutine scheduling", i+1, base[i], w, got[i])
			}
		}
	}
}

// TestDeterministicAcrossWorkerCounts (AC-18): the same command sequence
// run at worker counts 1 and 14 (or GOMAXPROCS-bounded, whichever is
// smaller) produces byte-identical Conservation state and the identical
// set of terminally-disposed ids -- the assignment of WHICH id goes to
// which disposal method is a pure function of id parity here (never of
// goroutine scheduling order), so the two runs must agree exactly.
func TestDeterministicAcrossWorkerCounts(t *testing.T) {
	workerCounts := []int{1, 14}
	if runtime.GOMAXPROCS(0) < 14 {
		workerCounts = []int{1, runtime.GOMAXPROCS(0)}
	}

	snapA, terminalA := runScenario(t, workerCounts[0])
	snapB, terminalB := runScenario(t, workerCounts[1])

	if snapA != snapB {
		t.Fatalf("Conservation snapshot differs across worker counts %v vs %v: %+v vs %+v", workerCounts[0], workerCounts[1], snapA, snapB)
	}
	if len(terminalA) != len(terminalB) {
		t.Fatalf("terminal id count differs: %d vs %d", len(terminalA), len(terminalB))
	}
	for i := range terminalA {
		if terminalA[i] != terminalB[i] {
			t.Fatalf("terminal id sequence diverges at index %d: %d vs %d", i, terminalA[i], terminalB[i])
		}
	}
}

// TestPlotAllocationTieBreakIsDeterministic (AC-18/GR#21): the
// findAllocatablePlotLocked tie-break (buriedMonth, then bodyID) always
// picks the SAME plot given the same occupancy state, regardless of
// iteration order -- proven by running the same reuse-eligible-selection
// scenario repeatedly and checking the chosen plot's occupant is always
// the same id.
func TestPlotAllocationTieBreakIsDeterministic(t *testing.T) {
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.PlotReuseHorizonMonths.Value = 1
	})

	pick := func() uint64 {
		d := NewDeathServicesAPI(cfg, "corr")
		if err := d.RegisterCemeteryWithCapacity("cem-1", 2, "corr"); err != nil {
			t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
		}
		deaths := []citizens.RealisedDeath{{CitizenID: 10, DeathMonth: 1}, {CitizenID: 20, DeathMonth: 1}}
		if _, err := d.Intake(deaths, "corr"); err != nil {
			t.Fatalf("Intake: %v", err)
		}
		// Both plots occupied at the same buriedMonth -- tie-break must
		// fall to the lower bodyID (10) as the reuse candidate.
		if err := d.Bury(10, "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury(10): %v", err)
		}
		if err := d.Bury(20, "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury(20): %v", err)
		}
		if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 30, DeathMonth: 2}}, "corr"); err != nil {
			t.Fatalf("Intake: %v", err)
		}
		if err := d.Bury(30, "cem-1", 2, "corr"); err != nil {
			t.Fatalf("Bury(30) via reuse: %v", err)
		}
		// The reused plot now holds 30; the OTHER plot's original occupant
		// (10 or 20) tells us which one was recycled.
		if elig, _ := d.PlotEligibleForReuse("cem-1", 20, 2, "corr"); elig {
			// 20's plot is still eligible (not reused) => 10 was recycled.
			return 10
		}
		return 20
	}

	first := pick()
	for i := 0; i < 5; i++ {
		if got := pick(); got != first {
			t.Fatalf("plot tie-break picked %d on run %d, want %d (deterministic)", got, i, first)
		}
	}
}
