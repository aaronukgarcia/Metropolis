package deathservices

// attack_round4_test.go -- INDEPENDENT DESTRUCTIVE ROUND 4 (GR#23) against
// MOD-083's round-3 rework. Attacks the coupling the AC-8 restoration
// introduced: does consulting engine.logistics under the single lock hold
// break H3's budget bound or H6's determinism, and does the congestion
// bound interact correctly with the plot-saturation skip?

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// R4-1 (AC-8 x H3): congestion is consulted under the SAME lock hold that
// claims the monthly budget. Concurrent callers racing a small budget
// through a CONGESTED logistics channel must still never collectively
// exceed the monthly budget.
func TestAttackR4CongestedConcurrentTransportStaysBudgetBounded(t *testing.T) {
	const budget = 10
	const workers = 20
	const perWorker = 10
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = budget
	})
	d := NewDeathServicesAPI(cfg, "atk")
	if err := d.Wire(nil, congestedLogistics(t, 4), "atk"); err != nil {
		t.Fatalf("Wire: %v", err)
	}
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
		t.Errorf("ATTACK LANDS: %d bodies buried in month 1 against a monthly budget of %d, with "+
			"congestion consulted inside the lock -- the AC-8 restoration reopened H3's TOCTOU",
			snap.BodiesBuried, budget)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Errorf("conservation broke under congestion+contention: %+v", snap)
	}
	t.Logf("congested+contended: %d buried, budget %d", snap.BodiesBuried, budget)
}

// R4-2 (AC-8 x H6): the plot-admission rank rule must stay worker-count-
// invariant when a CONGESTED logistics bound also truncates each call.
// Scarce plots + congestion + workers 1/4/20 must be byte-identical.
func TestAttackR4DeterminismUnderCongestionAndPlotContention(t *testing.T) {
	run := func(workers int) []BodyState {
		cfg := writeConfigFixture(t, func(c *Config) {
			c.Params.PlotReuseHorizonMonths.Value = 1
			c.Params.HearseMonthlyTransportBudget.Value = 40
		})
		d := NewDeathServicesAPI(cfg, "atk")
		if err := d.Wire(nil, congestedLogistics(t, 6), "atk"); err != nil {
			t.Fatalf("Wire: %v", err)
		}
		if err := d.RegisterCemeteryWithCapacity("cem-1", 10, "atk"); err != nil {
			t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
		}
		const n = 120
		deaths := make([]citizens.RealisedDeath, n)
		for i := 0; i < n; i++ {
			deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: int64(i%4 + 1)}
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
					_, _, _ = d.RunHearseTransport([]uint64{uint64(id)}, "cem-1", 1, "atk")
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
	buried := 0
	for _, s := range base {
		if s == BodyBuried {
			buried++
		}
	}
	if buried == 0 || buried > 10 {
		t.Errorf("workers=1 buried %d against a 10-plot cemetery -- expected a throttled, non-zero subset", buried)
	}
	for _, w := range []int{4, 20} {
		got := run(w)
		for i := range base {
			if base[i] != got[i] {
				t.Errorf("ATTACK LANDS (AC-18 x AC-8): body %d state at workers=1 is %q but at workers=%d "+
					"is %q -- congestion coupling broke the rank-based admission's worker-count invariance",
					i+1, base[i], w, got[i])
				return
			}
		}
	}
	t.Logf("congestion + plot contention: %d buried, byte-identical at workers 1/4/20", buried)
}

// R4-3 (N3 exit paths): every RunHearseTransport exit that MADE trips must
// charge the budget. Enumerate the reachable error exits and prove each
// leaves the accounting exactly consistent with the trips actually made.
func TestAttackR4HearseAccountingConsistentOnEveryExit(t *testing.T) {
	const budget = 10
	newAPI := func() *DeathServicesAPI {
		cfg := writeConfigFixture(t, func(c *Config) {
			c.Params.HearseMonthlyTransportBudget.Value = budget
		})
		d := NewDeathServicesAPI(cfg, "atk")
		if err := d.RegisterCemeteryWithCapacity("cem-1", 500, "atk"); err != nil {
			t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
		}
		if err := d.RegisterCrematorium("crem-1", "atk"); err != nil {
			t.Fatalf("RegisterCrematorium: %v", err)
		}
		intakeN(t, d, 1, 60, 1, false)
		return d
	}

	// budgetSpent probes how much of the month's budget the module thinks
	// it has left, by offering a large second batch and seeing how many
	// more trips it grants.
	budgetSpent := func(d *DeathServicesAPI) int64 {
		rest := make([]uint64, 0, 40)
		for i := uint64(21); i <= 60; i++ {
			rest = append(rest, i)
		}
		got, _, err := d.RunHearseTransport(rest, "cem-1", 1, "atk")
		if err != nil {
			t.Fatalf("probe RunHearseTransport: %v", err)
		}
		return budget - int64(len(got))
	}

	cases := []struct {
		name string
		call func(d *DeathServicesAPI) ([]uint64, error)
	}{
		{"unknown body mid-batch", func(d *DeathServicesAPI) ([]uint64, error) {
			got, _, err := d.RunHearseTransport([]uint64{1, 2, 999999, 3}, "cem-1", 1, "atk")
			return got, err
		}},
		{"already-terminal body mid-batch", func(d *DeathServicesAPI) ([]uint64, error) {
			if _, _, err := d.Cremate([]uint64{3}, "crem-1", 1, "atk"); err != nil {
				t.Fatalf("Cremate setup: %v", err)
			}
			got, _, err := d.RunHearseTransport([]uint64{1, 2, 3, 4}, "cem-1", 1, "atk")
			return got, err
		}},
		{"unknown cemetery", func(d *DeathServicesAPI) ([]uint64, error) {
			got, _, err := d.RunHearseTransport([]uint64{1, 2, 3}, "no-such-cemetery", 1, "atk")
			return got, err
		}},
		{"duplicate ids in batch", func(d *DeathServicesAPI) ([]uint64, error) {
			got, _, err := d.RunHearseTransport([]uint64{1, 1, 1, 2, 2}, "cem-1", 1, "atk")
			return got, err
		}},
	}

	for _, tc := range cases {
		d := newAPI()
		got, err := tc.call(d)
		snap, _ := d.Snapshot("atk")
		buriedByCall := snap.BodiesBuried
		if int64(len(got)) != buriedByCall {
			t.Errorf("[%s] returned %d transported ids but %d bodies are Buried -- the return value "+
				"and the committed state disagree", tc.name, len(got), buriedByCall)
		}
		spent := budgetSpent(d)
		if spent != buriedByCall {
			t.Errorf("ATTACK LANDS [%s]: %d bodies were buried by the call but only %d units of the "+
				"monthly budget were charged -- an exit path made trips without charging them (err=%v)",
				tc.name, buriedByCall, spent, err)
		}
		if snap.Sum() != snap.BodiesReleased {
			t.Errorf("[%s] conservation broke: %+v", tc.name, snap)
		}
		t.Logf("[%s] transported=%d charged=%d err=%v", tc.name, len(got), spent, err)
	}
}

// R4-4 (AC-8 boundary): a congested channel must THROTTLE, and the
// throttled remainder must stay a real, queryable backlog that drains in
// a later month -- congestion must not silently destroy bodies.
func TestAttackR4CongestionThrottlesWithoutLosingBodies(t *testing.T) {
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = 30
	})
	d := NewDeathServicesAPI(cfg, "atk")
	if err := d.Wire(nil, congestedLogistics(t, 5), "atk"); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := d.RegisterCemeteryWithCapacity("cem-1", 500, "atk"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	intakeN(t, d, 1, 30, 1, false)

	moved := 0
	for month := int64(1); month <= 12; month++ {
		ids, err := d.AwaitingSorted("atk")
		if err != nil {
			t.Fatalf("AwaitingSorted: %v", err)
		}
		if len(ids) == 0 {
			break
		}
		got, _, err := d.RunHearseTransport(ids, "cem-1", month, "atk")
		if err != nil {
			t.Fatalf("month %d: %v", month, err)
		}
		if len(got) >= 30 {
			t.Errorf("month %d moved %d -- congestion did not throttle", month, len(got))
		}
		moved += len(got)
		snap, _ := d.Snapshot("atk")
		if snap.Sum() != snap.BodiesReleased {
			t.Fatalf("month %d conservation broke: %+v", month, snap)
		}
	}
	snap, _ := d.Snapshot("atk")
	if snap.BodiesBuried+snap.BodiesAwaitingHandling != 30 {
		t.Errorf("ATTACK LANDS: %d buried + %d awaiting != 30 released -- congestion lost bodies (%+v)",
			snap.BodiesBuried, snap.BodiesAwaitingHandling, snap)
	}
	t.Logf("congested drain over 12 months: %d moved, %d still awaiting", moved, snap.BodiesAwaitingHandling)
}
