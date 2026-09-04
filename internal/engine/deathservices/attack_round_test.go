package deathservices

// attack_round_test.go -- INDEPENDENT DESTRUCTIVE ROUND (GR#23) against
// MOD-083 inc1. Not authored by the estate's author. Each test states the
// invariant it attacks and the AC it derives from.

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

func mkAPI(t *testing.T, mutate func(*Config)) *DeathServicesAPI {
	t.Helper()
	cfg := writeConfigFixture(t, mutate)
	d := NewDeathServicesAPI(cfg, "atk")
	if err := d.RegisterCemetery("cem-1", "atk"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "atk"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	return d
}

func intakeN(t *testing.T, d *DeathServicesAPI, from, n int, month int64, emergency bool) []uint64 {
	t.Helper()
	deaths := make([]citizens.RealisedDeath, 0, n)
	for i := 0; i < n; i++ {
		deaths = append(deaths, citizens.RealisedDeath{CitizenID: uint64(from + i), DeathMonth: month, EmergencyFlag: emergency})
	}
	ids, err := d.Intake(deaths, "atk")
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	return ids
}

// ATTACK 1 (AC-5c / AC-6 / AC-17): Cremate aborts mid-batch on an
// already-terminal id AFTER having already flipped earlier ids to
// BodyCremated -- but returns before cr.cremToday += len(cremated) and
// before the cost posting. The bodies are cremated for FREE and the day's
// throughput counter never sees them, so the 12/d cap can be exceeded.
func TestAttackCremateMidBatchErrorLosesCostAndThroughput(t *testing.T) {
	d := mkAPI(t, func(c *Config) { c.Params.CremationDailyThroughputPerBody.Value = 12 })
	intakeN(t, d, 1, 20, 1, false)

	// Pre-terminate id 5 by burial.
	if err := d.Bury(5, "cem-1", 1, "atk"); err != nil {
		t.Fatalf("Bury: %v", err)
	}

	// Batch [1,2,3,4,5]: 1-4 cremate, 5 is already buried -> error.
	cremated, cost, err := d.Cremate([]uint64{1, 2, 3, 4, 5}, "crem-1", 1, "atk")
	if err == nil {
		t.Fatalf("expected ErrBodyAlreadyHandled")
	}
	t.Logf("cremated=%v cost=%d err=%v", cremated, cost, err)

	// Independently verify how many bodies actually reached BodyCremated.
	snap, _ := d.Snapshot("atk")
	if snap.BodiesCremated == 0 {
		t.Logf("PASS: no partial commit")
		return
	}
	if cost != 0 {
		t.Fatalf("unexpected: cost was returned on the error path")
	}
	t.Errorf("ATTACK LANDS: %d bodies reached BodyCremated on the error path but the call returned cost=%d "+
		"(AC-6 cost never posted, AC-17 'no side-effect record was created' violated)", snap.BodiesCremated, cost)

	// And the day's throughput counter never saw them: a full 12 more can
	// still be cremated the SAME day, exceeding the spec-seed 12/d cap.
	throughput, _ := d.DailyThroughput("atk")
	rest := []uint64{}
	for i := uint64(6); i <= 20; i++ {
		rest = append(rest, i)
	}
	more, _, err := d.Cremate(rest, "crem-1", 1, "atk")
	if err != nil {
		t.Fatalf("second Cremate: %v", err)
	}
	total := snap.BodiesCremated + int64(len(more))
	if total > throughput {
		t.Errorf("ATTACK LANDS: %d bodies cremated on day 1 against a data-sourced daily cap of %d "+
			"(the aborted batch's %d bodies never consumed throughput)", total, throughput, snap.BodiesCremated)
	}
}

// ATTACK 2 (AC-11 / AC-17): the identical partial-commit shape in Dispense
// -- an unknown/terminal id mid-batch returns after earlier ids already
// reached BodyDispensed, without incrementing usedThisMonth. The monthly
// dispensation budget is then bypassable.
func TestAttackDispenseMidBatchErrorSkipsMonthlyBudget(t *testing.T) {
	d := mkAPI(t, nil)
	intakeN(t, d, 1, 30, 1, true)
	if err := d.SetDispensationActive(true, "atk"); err != nil {
		t.Fatalf("SetDispensationActive: %v", err)
	}
	mode, _ := d.Dispensation("atk")

	// vanCap-sized batch whose LAST id is unknown.
	batch := []uint64{}
	for i := int64(0); i < mode.VanBodyCapacity-1; i++ {
		batch = append(batch, uint64(i+1))
	}
	batch = append(batch, 999999) // unknown

	dispensed, err := d.Dispense(batch, 1, "atk")
	if err == nil {
		t.Fatalf("expected ErrUnknownBody")
	}
	snap, _ := d.Snapshot("atk")
	if snap.BodiesHandledByDispensation == 0 {
		t.Logf("PASS: no partial commit (dispensed=%v)", dispensed)
		return
	}
	t.Errorf("ATTACK LANDS: %d bodies reached BodyDispensed on the ErrUnknownBody path; "+
		"usedThisMonth was not incremented, so the monthly dispensation budget of %d is over-spendable "+
		"(AC-17 'no side-effect record was created' violated)",
		snap.BodiesHandledByDispensation, mode.ThroughputMonthly)
}

// ATTACK 3 (AC-7 / AC-9 / AC-20): RunHearseTransport reads the remaining
// monthly budget, RELEASES d.mu, does the work, then re-locks to add its
// consumption. Concurrent callers therefore all see the same "remaining"
// and can collectively transport far more than the data-sourced monthly
// budget -- the one bound AC-7 exists to enforce.
func TestAttackConcurrentHearseTransportExceedsMonthlyBudget(t *testing.T) {
	const budget = 10
	const workers = 20
	const perWorker = 10
	d := mkAPI(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = budget
		c.Params.GraveyardPlotCapacity.Value = 5000
	})
	// Re-register the cemetery so it picks up the big capacity.
	if err := d.RegisterCemeteryWithCapacity("cem-1", 5000, "atk"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	intakeN(t, d, 1, workers*perWorker, 1, false)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w*perWorker + 1
		wg.Add(1)
		go func(lo int) {
			defer wg.Done()
			ids := make([]uint64, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				ids = append(ids, uint64(lo+i))
			}
			_, _, _ = d.RunHearseTransport(ids, "cem-1", 1, "atk")
		}(lo)
	}
	wg.Wait()

	snap, _ := d.Snapshot("atk")
	if snap.BodiesBuried > budget {
		t.Errorf("ATTACK LANDS: %d bodies transported+buried in month 1 against a data-sourced monthly hearse "+
			"budget of %d (AC-7's throughput cap is not concurrency-safe: read-remaining / unlock / work / "+
			"re-lock-and-add TOCTOU in RunHearseTransport)", snap.BodiesBuried, budget)
	} else {
		t.Logf("PASS: %d buried, budget %d", snap.BodiesBuried, budget)
	}
}

// ATTACK 4 (AC-10): a composition root sets dispensation active from
// FEAT-087's live weather signal. The very next ORDINARY Intake batch
// (deaths with EmergencyFlag=false, which is what most deaths are even
// DURING a weather event) silently clears it, because Intake overwrites
// the flag from the batch alone.
func TestAttackOrdinaryIntakeClearsActiveDispensation(t *testing.T) {
	d := mkAPI(t, nil)
	if err := d.SetDispensationActive(true, "atk"); err != nil {
		t.Fatalf("SetDispensationActive: %v", err)
	}
	active, _ := d.DispensationActive("atk")
	if !active {
		t.Fatalf("setup: dispensation not active")
	}
	// Ordinary (non-emergency) deaths arrive while the event is still on.
	intakeN(t, d, 1, 3, 1, false)
	active, _ = d.DispensationActive("atk")
	if !active {
		t.Errorf("ATTACK LANDS: an ordinary (EmergencyFlag=false) Intake batch cleared an ACTIVE dispensation "+
			"that the composition root had set from FEAT-087's live weather signal. AC-10 requires deactivation "+
			"when THE EVENT ends, not when a batch of ordinary deaths arrives. active=%v", active)
	}
}

// ATTACK 5 (AC-7 / AC-11 / AC-12): while dispensation is INACTIVE, Dispense
// with len==1 is permitted AND explicitly unbounded ("remaining =
// len(bodyIDs)"). A caller can therefore terminally dispose of an unlimited
// number of bodies outside any emergency, consuming neither the hearse
// monthly budget nor the dispensation budget -- the AC-7 throughput ceiling
// the whole backlog mechanic depends on has a one-body-at-a-time hole.
func TestAttackInactiveSingleBodyDispenseBypassesAllBudgets(t *testing.T) {
	const n = 500
	d := mkAPI(t, func(c *Config) { c.Params.HearseMonthlyTransportBudget.Value = 5 })
	intakeN(t, d, 1, n, 1, false)
	active, _ := d.DispensationActive("atk")
	if active {
		t.Fatalf("setup: dispensation should be inactive")
	}
	for i := uint64(1); i <= n; i++ {
		if _, err := d.Dispense([]uint64{i}, 1, "atk"); err != nil {
			t.Fatalf("Dispense(%d): %v", i, err)
		}
	}
	snap, _ := d.Snapshot("atk")
	budget, _ := d.HearseMonthlyBudget("atk")
	if snap.BodiesHandledByDispensation > budget {
		t.Errorf("ATTACK LANDS: %d bodies terminally disposed in one month with dispensation INACTIVE, "+
			"against a monthly transport budget of %d -- no budget of any kind bounds the inactive "+
			"single-body Dispense path (AC-7's surge-becomes-backlog mechanic is bypassable)",
			snap.BodiesHandledByDispensation, budget)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Errorf("conservation broke: %+v", snap)
	}
}

// ATTACK 6 (AC-14/AC-15 seam): plot reuse must free the PLOT, never
// decrement the lifetime BodiesBuried count, and must never let occupancy
// exceed capacity.
func TestAttackPlotReuseNeverDecrementsBuriedCount(t *testing.T) {
	d := mkAPI(t, func(c *Config) { c.Params.PlotReuseHorizonMonths.Value = 3 })
	if err := d.RegisterCemeteryWithCapacity("cem-small", 2, "atk"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	intakeN(t, d, 1, 10, 1, false)
	if err := d.Bury(1, "cem-small", 1, "atk"); err != nil {
		t.Fatalf("Bury(1): %v", err)
	}
	if err := d.Bury(2, "cem-small", 1, "atk"); err != nil {
		t.Fatalf("Bury(2): %v", err)
	}
	// Month 4: horizon 3 elapsed, both plots reusable.
	for _, id := range []uint64{3, 4} {
		if err := d.Bury(id, "cem-small", 4, "atk"); err != nil {
			t.Fatalf("Bury(%d) via reuse: %v", id, err)
		}
	}
	snap, _ := d.Snapshot("atk")
	if snap.BodiesBuried != 4 {
		t.Errorf("BodiesBuried = %d after 2 burials + 2 reuse-burials, want 4 (reuse frees the PLOT, "+
			"it must never decrement the lifetime buried count)", snap.BodiesBuried)
	}
	occ, cap, err := d.CemeteryOccupancy("cem-small", "atk")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occ > cap {
		t.Errorf("occupancy %d exceeds capacity %d", occ, cap)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Errorf("conservation broke across plot reuse: %+v", snap)
	}
	// Evicted occupants 1 and 2 must still read BodyBuried (terminal
	// exclusivity is a lifetime classification), and must not be
	// re-disposable.
	for _, id := range []uint64{1, 2} {
		b, err := d.Body(id, "atk")
		if err != nil {
			t.Fatalf("Body(%d): %v", id, err)
		}
		if b.State != BodyBuried {
			t.Errorf("evicted occupant %d state = %q, want %q", id, b.State, BodyBuried)
		}
		if _, _, err := d.Cremate([]uint64{id}, "crem-1", 1, "atk"); err == nil {
			t.Errorf("ATTACK LANDS: evicted occupant %d was re-disposable by cremation "+
				"(double-count across two terminal buckets)", id)
		}
	}
}

// ATTACK 7 (AC-14, extended soak): 400 months interleaving dispensation
// on/off transitions WITH capacity starvation, checking the six-term
// identity every month. The author's soak never toggles dispensation.
func TestAttackSoakConservationWithDispensationToggling(t *testing.T) {
	d := mkAPI(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = 2
		c.Params.CremationDailyThroughputPerBody.Value = 2
		c.Params.PlotReuseHorizonMonths.Value = 6
	})
	if err := d.RegisterCemeteryWithCapacity("cem-1", 8, "atk"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	var nextID uint64 = 1
	for month := int64(1); month <= 400; month++ {
		emergency := month%5 == 0 || month%11 == 0
		n := int(month%6) + 1
		deaths := make([]citizens.RealisedDeath, 0, n)
		for i := 0; i < n; i++ {
			deaths = append(deaths, citizens.RealisedDeath{CitizenID: nextID, DeathMonth: month, EmergencyFlag: emergency})
			nextID++
		}
		if _, err := d.Intake(deaths, "atk"); err != nil {
			t.Fatalf("month %d Intake: %v", month, err)
		}
		ids, err := d.AwaitingSorted("atk")
		if err != nil {
			t.Fatalf("month %d AwaitingSorted: %v", month, err)
		}
		if len(ids) > 0 {
			switch month % 3 {
			case 0:
				if _, _, err := d.RunHearseTransport(ids, "cem-1", month, "atk"); err != nil {
					t.Fatalf("month %d hearse: %v", month, err)
				}
			case 1:
				if _, _, err := d.Cremate(ids, "crem-1", month, "atk"); err != nil {
					t.Fatalf("month %d cremate: %v", month, err)
				}
			case 2:
				active, _ := d.DispensationActive("atk")
				if active || len(ids) == 1 {
					if _, err := d.Dispense(ids, month, "atk"); err != nil {
						t.Fatalf("month %d dispense: %v", month, err)
					}
				}
			}
		}
		snap, err := d.Snapshot("atk")
		if err != nil {
			t.Fatalf("month %d Snapshot: %v", month, err)
		}
		if snap.Sum() != snap.BodiesReleased {
			t.Fatalf("month %d conservation violated: %+v", month, snap)
		}
		occ, cap, err := d.CemeteryOccupancy("cem-1", "atk")
		if err != nil {
			t.Fatalf("month %d occupancy: %v", month, err)
		}
		if occ > cap {
			t.Fatalf("month %d occupancy %d > capacity %d", month, occ, cap)
		}
	}
}

// ATTACK 8 (AC-18): the author's determinism test deliberately removes all
// contention (disjoint shards, throughput raised to 1000). Re-run it WITH
// contention -- a scarce plot pool concurrent burials compete for -- across
// worker counts 1/4/20, which is what AC-18's "byte-identical across worker
// counts" actually claims.
func TestAttackDeterminismUnderPlotContention(t *testing.T) {
	run := func(workers int) []BodyState {
		cfg := writeConfigFixture(t, func(c *Config) { c.Params.PlotReuseHorizonMonths.Value = 1 })
		d := NewDeathServicesAPI(cfg, "atk")
		if err := d.RegisterCemeteryWithCapacity("cem-1", 10, "atk"); err != nil {
			t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
		}
		const n = 120
		deaths := make([]citizens.RealisedDeath, n)
		for i := 0; i < n; i++ {
			deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
		}
		if _, err := d.Intake(deaths, "atk"); err != nil {
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
				for id := lo; id < hi; id++ {
					_ = d.Bury(uint64(id), "cem-1", 1, "atk")
				}
			}(lo, hi)
		}
		wg.Wait()
		out := make([]BodyState, 0, n)
		for id := 1; id <= n; id++ {
			b, err := d.Body(uint64(id), "atk")
			if err != nil {
				t.Fatalf("Body(%d): %v", id, err)
			}
			out = append(out, b.State)
		}
		return out
	}
	base := run(1)
	for _, w := range []int{4, 20} {
		got := run(w)
		for i := range base {
			if base[i] != got[i] {
				t.Errorf("ATTACK LANDS (AC-18): body %d state at workers=1 is %q but at workers=%d is %q -- "+
					"under real plot contention the buried/awaiting partition depends on goroutine scheduling. "+
					"The estate's own determinism test cannot see this because it removes contention by design "+
					"(disjoint shards + throughput 1000).", i+1, base[i], w, got[i])
				return
			}
		}
	}
}

// ATTACK 9 (AC-12): dispensation ends between a caller building its
// multi-body batch and submitting it. The submission must be rejected
// typed, with no state change.
func TestAttackDispensationEndsBeforeSubmission(t *testing.T) {
	d := mkAPI(t, nil)
	intakeN(t, d, 1, 10, 1, true)
	if err := d.SetDispensationActive(true, "atk"); err != nil {
		t.Fatalf("SetDispensationActive: %v", err)
	}
	batch := []uint64{1, 2, 3}
	// Event ends.
	if err := d.SetDispensationActive(false, "atk"); err != nil {
		t.Fatalf("SetDispensationActive: %v", err)
	}
	before, _ := d.Snapshot("atk")
	got, err := d.Dispense(batch, 1, "atk")
	if err == nil {
		t.Fatalf("multi-body Dispense succeeded after the event ended: %v", got)
	}
	assertRegistryCode(t, err, ErrMultiBodyOutsideDispensation)
	after, _ := d.Snapshot("atk")
	if before != after {
		t.Errorf("post-event multi-body attempt changed state: %+v -> %+v", before, after)
	}
}

// ATTACK 10 (AC-1/AC-17): Intake partially commits before rejecting a
// duplicate mid-stream. A caller that treats a non-nil error as "the batch
// did not apply" and retries the whole batch gets ErrDuplicateDeath on the
// FIRST id forever -- the stream deadlocks.
func TestAttackIntakePartialCommitOnDuplicate(t *testing.T) {
	d := mkAPI(t, nil)
	batch := []citizens.RealisedDeath{
		{CitizenID: 1, DeathMonth: 1},
		{CitizenID: 2, DeathMonth: 1},
		{CitizenID: 1, DeathMonth: 1}, // duplicate mid-stream
		{CitizenID: 3, DeathMonth: 1},
	}
	out, err := d.Intake(batch, "atk")
	if err == nil {
		t.Fatalf("expected ErrDuplicateDeath")
	}
	snap, _ := d.Snapshot("atk")
	t.Logf("out=%v released=%d", out, snap.BodiesReleased)
	if snap.BodiesReleased != 0 {
		t.Logf("NOTE: Intake partially committed %d bodies before erroring; body 3 was DROPPED "+
			"(never intaken, never in any bucket) and a whole-batch retry now fails on id 1", snap.BodiesReleased)
	}
	// Conservation still holds over what WAS released...
	if snap.Sum() != snap.BodiesReleased {
		t.Errorf("conservation broke: %+v", snap)
	}
	// ...but the third death (id 3) is silently absent from the module.
	if _, err := d.Body(3, "atk"); err == nil {
		t.Logf("PASS: id 3 was intaken despite the mid-stream duplicate")
	} else {
		t.Errorf("ATTACK LANDS: RealisedDeath{CitizenID:3} was DROPPED by Intake -- AC-1 requires the body " +
			"set have identical cardinality and IDs to the input, 'never dropping a citizen'. " +
			"A whole-batch retry is impossible (id 1 now duplicates), so the death is lost permanently.")
	}
}
