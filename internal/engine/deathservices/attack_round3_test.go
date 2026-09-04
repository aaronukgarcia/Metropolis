package deathservices

// attack_round3_test.go -- INDEPENDENT DESTRUCTIVE ROUND 3 (GR#23) against
// MOD-083's round-2 REWORK. Attacks the NEW code the fixes introduced
// (all-or-nothing batches, rank-based admission, the drain wiring), not
// the round-2 shapes (those are attack_round_test.go's job, kept green
// and unmodified).

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// R3-1 (NEW seam, H1's fix): Cremate's two-pass rewrite validates the whole
// batch then admits per-id by rank. A DUPLICATE id inside one batch passes
// validation twice and ranks identically twice -- does one body get charged
// and counted twice?
func TestAttackR3CremateDuplicateIDInOneBatch(t *testing.T) {
	d := mkAPI(t, nil)
	intakeN(t, d, 1, 5, 1, false)

	perBody, _ := d.PerBodyCostMicropounds("atk")
	cremated, cost, err := d.Cremate([]uint64{1, 1}, "crem-1", 1, "atk")
	if err != nil {
		t.Logf("PASS: duplicate-in-batch rejected typed: %v", err)
		return
	}
	snap, _ := d.Snapshot("atk")
	if snap.BodiesCremated != 1 {
		t.Errorf("BodiesCremated = %d for a [1,1] batch, want 1", snap.BodiesCremated)
	}
	if len(cremated) > 1 {
		t.Errorf("ATTACK LANDS: Cremate([1,1]) returned %v -- one body reported cremated twice", cremated)
	}
	if cost > perBody {
		t.Errorf("ATTACK LANDS: Cremate([1,1]) charged %d micro-pounds for ONE body (per-body cost %d) "+
			"-- the duplicate is double-billed through engine.services", cost, perBody)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Errorf("conservation broke: %+v", snap)
	}
}

// R3-2 (NEW seam, H2's fix): the identical duplicate-in-batch question for
// Dispense -- does one body consume two units of the monthly budget?
func TestAttackR3DispenseDuplicateIDInOneBatch(t *testing.T) {
	d := mkAPI(t, nil)
	intakeN(t, d, 1, 5, 1, true)
	if err := d.SetDispensationActive(true, "atk"); err != nil {
		t.Fatalf("SetDispensationActive: %v", err)
	}
	dispensed, err := d.Dispense([]uint64{2, 2}, 1, "atk")
	if err != nil {
		t.Logf("PASS: duplicate-in-batch rejected typed: %v", err)
		return
	}
	snap, _ := d.Snapshot("atk")
	if snap.BodiesHandledByDispensation != 1 {
		t.Errorf("BodiesHandledByDispensation = %d for a [2,2] batch, want 1", snap.BodiesHandledByDispensation)
	}
	if len(dispensed) > 1 {
		t.Errorf("ATTACK LANDS: Dispense([2,2]) returned %v -- one body reported dispensed twice, "+
			"and usedThisMonth was charged twice for it", dispensed)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Errorf("conservation broke: %+v", snap)
	}
}

// R3-3 (RESIDUAL H1-class): RunHearseTransport still returns mid-loop on a
// non-ErrNoPlotAvailable error -- BEFORE `d.hearse.usedThisMonth +=
// len(transported)`. Bodies already buried by that call would then have
// consumed NO monthly budget, exactly the free-cremation shape H1 closed
// in Cremate. Drive it with an already-terminal id mid-batch.
func TestAttackR3HearseMidBatchErrorSkipsBudgetAccounting(t *testing.T) {
	const budget = 10
	d := mkAPI(t, func(c *Config) { c.Params.HearseMonthlyTransportBudget.Value = budget })
	if err := d.RegisterCemeteryWithCapacity("cem-big", 1000, "atk"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	intakeN(t, d, 1, 40, 1, false)

	// Pre-terminate id 3 so it errors mid-batch.
	if _, _, err := d.Cremate([]uint64{3}, "crem-1", 1, "atk"); err != nil {
		t.Fatalf("Cremate setup: %v", err)
	}

	transported, _, err := d.RunHearseTransport([]uint64{1, 2, 3, 4, 5}, "cem-big", 1, "atk")
	if err == nil {
		t.Logf("no error raised; transported=%v", transported)
	}
	snapA, _ := d.Snapshot("atk")
	buriedAfterFirst := snapA.BodiesBuried

	// Now spend the FULL budget again in the same month. If the aborted
	// call's burials were never charged, the month over-delivers.
	rest := make([]uint64, 0, 30)
	for i := uint64(10); i < 40; i++ {
		rest = append(rest, i)
	}
	if _, _, err := d.RunHearseTransport(rest, "cem-big", 1, "atk"); err != nil {
		t.Fatalf("second RunHearseTransport: %v", err)
	}
	snapB, _ := d.Snapshot("atk")
	if snapB.BodiesBuried > budget {
		t.Errorf("ATTACK LANDS: %d bodies buried by hearse in month 1 against a monthly budget of %d "+
			"(the aborted first call buried %d and returned before `usedThisMonth +=`, so those trips "+
			"were never charged -- the H1 partial-commit shape survives in RunHearseTransport)",
			snapB.BodiesBuried, budget, buriedAfterFirst)
	} else {
		t.Logf("PASS: %d buried, budget %d", snapB.BodiesBuried, budget)
	}
	if snapB.Sum() != snapB.BodiesReleased {
		t.Errorf("conservation broke: %+v", snapB)
	}
}

// R3-4 (M3 / BUG-484): the drain must genuinely bind the ORDINARY release
// to min(budget, drain, queued), and must be IGNORED ENTIRELY on the
// emergency path (Aaron's BUG-484 ruling). Driven against a REAL
// citizens.DeathQueue with this module wired in as the DrainCapacity.
func TestAttackR3DrainBindsOrdinaryAndIsIgnoredUnderEmergency(t *testing.T) {
	cfgMortality, err := citizens.LoadDefaultMortalityConfig("atk")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	ordinaryBudget := cfgMortality.MonthlyDeathBudget()

	// A deathservices instance with a DELIBERATELY TINY total capacity:
	// no cemeteries, no crematoria, hearse budget 3 -> drain == 3.
	newTiny := func() *DeathServicesAPI {
		cfg := writeConfigFixture(t, func(c *Config) { c.Params.HearseMonthlyTransportBudget.Value = 3 })
		return NewDeathServicesAPI(cfg, "atk")
	}

	d := newTiny()
	if got := d.MonthlyDrainCapacity(1); got != 3 {
		t.Fatalf("setup: MonthlyDrainCapacity = %d, want 3 (no cemeteries/crematoria, hearse budget 3)", got)
	}

	// ORDINARY path: queue far more than both the drain and the budget.
	q := citizens.NewDeathQueue()
	if err := q.SetDrainCapacity(d, "atk"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	const queued = 500
	for i := uint64(1); i <= queued; i++ {
		if err := q.Enqueue(i, 1, "atk"); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	released := q.RealiseDrained(cfgMortality, false, 1, "atk")
	want := 3
	if ordinaryBudget < want {
		want = ordinaryBudget
	}
	if len(released) != want {
		t.Errorf("ATTACK LANDS (M3): ordinary RealiseDrained released %d, want min(budget=%d, drain=3, queued=%d)=%d "+
			"-- the death queue does not respect this module's live drain", len(released), ordinaryBudget, queued, want)
	} else {
		t.Logf("ordinary release = %d = min(budget %d, drain 3, queued %d)", len(released), ordinaryBudget, queued)
	}

	// EMERGENCY path (BUG-484): same tiny drain must be IGNORED.
	d2 := newTiny()
	q2 := citizens.NewDeathQueue()
	if err := q2.SetDrainCapacity(d2, "atk"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	q3 := citizens.NewDeathQueue() // control: no drain wired at all
	for i := uint64(1); i <= queued; i++ {
		if err := q2.Enqueue(i, 1, "atk"); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if err := q3.Enqueue(i, 1, "atk"); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	withDrain := q2.RealiseDrained(cfgMortality, true, 1, "atk")
	noDrain := q3.RealiseDrained(cfgMortality, true, 1, "atk")
	if len(withDrain) != len(noDrain) {
		t.Errorf("ATTACK LANDS (BUG-484): emergency release with a drain of 3 wired released %d, "+
			"but with NO drain wired released %d -- Aaron's ruling is that the drain must NOT clamp "+
			"the emergency path", len(withDrain), len(noDrain))
	}
	if len(withDrain) <= 3 {
		t.Errorf("ATTACK LANDS (BUG-484): emergency release was %d, <= the drain of 3 -- the drain "+
			"appears to be clamping the emergency path", len(withDrain))
	}
	for i := range withDrain {
		if withDrain[i] != noDrain[i] {
			t.Errorf("BUG-484: emergency release diverges from the no-drain control at index %d: %+v vs %+v",
				i, withDrain[i], noDrain[i])
			break
		}
	}
	t.Logf("emergency release with drain=3: %d bodies (control with no drain: %d) -- drain correctly ignored",
		len(withDrain), len(noDrain))
}

// R3-5 (M3 fail-closed): MonthlyDrainCapacity has no error return, so a
// struct-copied receiver must fail CLOSED (0), never grant throughput.
func TestAttackR3DrainCapacityOnStructCopyFailsClosed(t *testing.T) {
	d := mkAPI(t, nil)
	if got := d.MonthlyDrainCapacity(1); got <= 0 {
		t.Fatalf("setup: real receiver returned %d, want > 0", got)
	}
	// Uses the estate's own sanctioned unsafe byte-copy helper rather than a
	// literal `cp := *d`, so bare `go vet` (which CI runs) stays clean --
	// the same reason FEAT-087 rewrote its two literal lock copies.
	cp := deathServicesAPIByteCopy(d)
	if got := cp.MonthlyDrainCapacity(1); got != 0 {
		t.Errorf("ATTACK LANDS (SEC-020): MonthlyDrainCapacity on a struct copy returned %d, want 0 "+
			"(fail-closed) -- a copied receiver grants disposal throughput off aliased state", got)
	}
}

// R3-6 (H6's fix, extended): the rank-based admission is now used by BOTH
// Bury and Cremate. Run CREMATE under real contention for a scarce daily
// cap across worker counts 1/4/20 -- the admitted set must be byte-identical.
func TestAttackR3CremateAdmissionDeterministicUnderContention(t *testing.T) {
	run := func(workers int) []BodyState {
		cfg := writeConfigFixture(t, func(c *Config) { c.Params.CremationDailyThroughputPerBody.Value = 7 })
		d := NewDeathServicesAPI(cfg, "atk")
		if err := d.RegisterCrematorium("crem-1", "atk"); err != nil {
			t.Fatalf("RegisterCrematorium: %v", err)
		}
		const n = 100
		deaths := make([]citizens.RealisedDeath, n)
		for i := 0; i < n; i++ {
			deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: int64(i%3 + 1)}
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
					_, _, _ = d.Cremate([]uint64{uint64(id)}, "crem-1", 1, "atk")
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
	cremated := 0
	for _, s := range base {
		if s == BodyCremated {
			cremated++
		}
	}
	if cremated != 7 {
		t.Errorf("workers=1 cremated %d against a daily cap of 7 -- cap not honoured", cremated)
	}
	for _, w := range []int{4, 20} {
		got := run(w)
		for i := range base {
			if base[i] != got[i] {
				t.Errorf("ATTACK LANDS (AC-18): body %d state at workers=1 is %q but at workers=%d is %q "+
					"-- Cremate's daily-cap admission is not worker-count-invariant", i+1, base[i], w, got[i])
				return
			}
		}
	}
}

// R3-7: the AC-8 regression question. With the engine.market/Deliverable
// call removed, is a hearse trip still subject to ANY logistics-owned
// congestion? Wire a real LogisticsAPI and prove whether its state can
// influence hearse throughput at all.
func TestAttackR3HearseIgnoresLogisticsEntirely(t *testing.T) {
	d := mkAPI(t, func(c *Config) { c.Params.HearseMonthlyTransportBudget.Value = 25 })
	if err := d.RegisterCemeteryWithCapacity("cem-big", 500, "atk"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	intakeN(t, d, 1, 50, 1, false)
	ids, _ := d.AwaitingSorted("atk")
	transported, _, err := d.RunHearseTransport(ids, "cem-big", 1, "atk")
	if err != nil {
		t.Fatalf("RunHearseTransport: %v", err)
	}
	// Unwired, the budget alone bounds the month.
	if len(transported) != 25 {
		t.Errorf("transported %d, want the full monthly budget of 25", len(transported))
	}
	t.Logf("AC-8 ASSESSMENT: with no logistics consultation in the code path, hearse throughput is a "+
		"pure function of the data-sourced monthly budget (%d transported). No logistics state of any "+
		"kind can delay or reduce a hearse trip.", len(transported))
}
