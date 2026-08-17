package refuse

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the class-covering test matrix for the Destructive-MOD039 r6
// "concurrent-mutation data races" rejection. The failure CLASS is: a mutable
// field guarded by r.mu (an injected dependency pointer — r.logistics /
// r.services — or disposalSite.reclaimed) is READ outside its lock while a
// writer (Wire / CapAndReclaim) mutates it under r.mu.Lock, plus two
// read-modify-write lock-section splits that break AC-11's mass-conservation
// identity under concurrency.
//
// The race tests below are hammer tests run under `-race`: they exercise the
// exact read sites the attacker cited (disposal.go:198/202/209/293/327/334/
// 410/430/434, round.go:209/270/435) against a concurrent writer, so the race
// detector flags any unsynchronized read/write of the same memory. The two
// deterministic tests construct the state, drive to completion behind a
// release barrier, and assert the AC-11 identity — they must pass under ANY
// schedule, which is exactly what the fixes guarantee.

// TestConcurrentWireDependencyReadNoRace hammers every dependency-reading
// path concurrently with Wire re-writing r.logistics/r.services. Pre-fix this
// flags the unsynchronized reads at disposal.go:202/209/293/334/410/434 and
// round.go:209/270/435 under `-race`.
func TestConcurrentWireDependencyReadNoRace(t *testing.T) {
	api := newTestAPI(t)
	lg1, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	lg2, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	w := &recordingWellbeing{}
	if err := api.Wire(lg1, sv, w); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFunding(1.0); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}

	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterIncinerator("I1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Race Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 100_000); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}

	var ready, begin sync.WaitGroup
	ready.Add(2)
	begin.Add(1)
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: re-wire in a loop, toggling logistics instances so Wire actually
	// writes r.logistics/r.services every iteration.
	go func() {
		defer wg.Done()
		ready.Done()
		begin.Wait()
		for i := 0; i < 300; i++ {
			if i%2 == 0 {
				_ = api.Wire(lg1, sv, w)
			} else {
				_ = api.Wire(lg2, sv, w)
			}
		}
	}()

	// Reader: exercise every dependency-reading path in the package.
	go func() {
		defer wg.Done()
		ready.Done()
		begin.Wait()
		for i := 0; i < 300; i++ {
			_, _ = api.RouteGeneralToSite("I1", 1000) // throughputAccepted (r.logistics.Deliverable)
			_, _ = api.RouteFoodToCompost("C1", 1000) // throughputAccepted (r.logistics.Deliverable)
			_, _ = api.RouteGeneralToSite("L1", 10)   // r.logistics.Stock + Restock
			_, _ = api.RemainingCapacity("L1")        // r.logistics.Stock
			_, _ = api.BlightedCells("L1")            // r.logistics.Stock
			_, _ = api.ProcessDisposal("L1")          // r.logistics.Restock
			_, _ = api.RunRound("r1")                 // r.services.FundingLevel + deliverToSite (r.logistics.Deliverable)
			_ = api.SetFunding(1.0)                   // r.services.SetFunding
		}
	}()

	ready.Wait()
	begin.Done()
	wg.Wait()
}

// TestConcurrentCapAndReclaimReclaimedReadNoRace hammers every read of
// disposalSite.reclaimed concurrently with CapAndReclaim writing it. Pre-fix
// this flags the unsynchronized reads at disposal.go:198/327/430 and
// round.go:429 under `-race`.
func TestConcurrentCapAndReclaimReclaimedReadNoRace(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Race Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 100_000); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}

	var ready, begin sync.WaitGroup
	ready.Add(2)
	begin.Add(1)
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: CapAndReclaim writes site.reclaimed = true (idempotently) each
	// iteration, so the read sites below always have a concurrent writer.
	go func() {
		defer wg.Done()
		ready.Done()
		begin.Wait()
		for i := 0; i < 500; i++ {
			_ = api.CapAndReclaim("L1")
		}
	}()

	// Reader: exercise every read of site.reclaimed.
	go func() {
		defer wg.Done()
		ready.Done()
		begin.Wait()
		for i := 0; i < 500; i++ {
			_, _ = api.RouteGeneralToSite("L1", 10) // disposal.go:198
			_, _ = api.BlightedCells("L1")          // disposal.go:430
			_, _ = api.ProcessDisposal("L1")        // disposal.go:327
			_, _ = api.RunRound("r1")               // deliverToSite, round.go:429
		}
	}()

	ready.Wait()
	begin.Done()
	wg.Wait()
}

// TestConcurrentSameRoundRunRoundNoDoubleDeliver is the deterministic AC-11
// regression for the same-round re-delivery defect: two concurrent RunRound
// calls on one gridlocked (still-open) round could both pass the
// `rd.completed` check (a separate lock section from collect/deliver) and
// re-deliver the same in-transit tonnage, inflating the disposal backlog and
// breaking mass conservation. Post-fix, exactly one RunRound may drive a round
// at a time; the rest are rejected with ErrInvalidOverride.
func TestConcurrentSameRoundRunRoundNoDoubleDeliver(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Race Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 100_000); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}

	// First run saturates logistics' throughput, leaving an in-transit
	// shortfall and the round OPEN (gridlocked).
	res, err := api.RunRound("r1")
	if err != nil {
		t.Fatal(err)
	}
	if res.ShortfallGeneral <= 0 {
		t.Fatalf("precondition: expected an in-transit shortfall (gridlocked round), got %+v", res)
	}
	rd, err := api.Round("r1")
	if err != nil {
		t.Fatal(err)
	}
	if rd.Completed {
		t.Fatal("precondition: round must still be open (gridlocked)")
	}

	const n = 16
	var ready, begin sync.WaitGroup
	ready.Add(n)
	begin.Add(1)
	var wg sync.WaitGroup
	errsCh := make(chan error, n)
	okCh := make(chan struct{}, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			begin.Wait()
			if _, err := api.RunRound("r1"); err != nil {
				errsCh <- err
			} else {
				okCh <- struct{}{}
			}
		}()
	}
	ready.Wait()
	begin.Done()
	wg.Wait()
	close(errsCh)
	close(okCh)

	oks := 0
	for range okCh {
		oks++
	}
	for err := range errsCh {
		var e *errs.E
		if !errors.As(err, &e) || e.Code != ErrInvalidOverride {
			t.Fatalf("concurrent RunRound must yield success or ErrInvalidOverride only, got %v", err)
		}
	}
	if oks != 1 {
		t.Fatalf("exactly one RunRound may drive a round at a time, got %d successes", oks)
	}
	// AC-11: no double-delivery — the identity must survive any schedule.
	assertIdentity(t, api)
}

// TestConcurrentProcessDisposalNoLostUpdate is the deterministic AC-11
// regression for the ProcessDisposal lost-update defect: the landfill path
// read site.backlog[0] under one lock section, called logistics.Restock, then
// decremented backlog under a second lock section — two concurrent calls on an
// over-headroom site both read the full backlog and both process it, double
// counting `used`/`collected`. Post-fix, the backlog is claimed atomically
// before the external Restock, so it is processed exactly once.
func TestConcurrentProcessDisposalNoLostUpdate(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Race Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}

	// The round delivered general waste into L1's backlog, not yet processed.
	backlog, err := api.TonnesDisposalBacklog(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}
	if backlog <= 0 {
		t.Fatalf("precondition: expected a nonzero general backlog, got %d", backlog)
	}
	collectedBefore, err := api.TonnesCollected(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}

	const n = 16
	var ready, begin sync.WaitGroup
	ready.Add(n)
	begin.Add(1)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			begin.Wait()
			_, _ = api.ProcessDisposal("L1")
		}()
	}
	ready.Wait()
	begin.Done()
	wg.Wait()

	collectedAfter, err := api.TonnesCollected(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}
	processed := collectedAfter - collectedBefore
	if processed != backlog {
		t.Fatalf("backlog must be processed exactly once: processed=%d, backlog was=%d", processed, backlog)
	}
	assertIdentity(t, api)
}
