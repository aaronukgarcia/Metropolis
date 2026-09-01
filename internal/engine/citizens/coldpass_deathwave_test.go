package citizens

import "testing"

// FEAT-087 (mkey feat.deathwave) inc1.5: LIVE cold-pass wiring tests. inc1's
// deathwave_test.go proves DeathQueue correct in ISOLATION (calling
// Enqueue/Realise directly); these tests drive the ACTUAL monthly tick
// (AdvanceMonth/AdvanceDayTick) end to end, proving the primitive is really
// reached by the running sim -- the false-pass trap AC-1 names explicitly
// (a smooth report sitting on top of a still-immediate population cliff).

// mkGuaranteedDeathRecord builds a cold record whose Gompertz-Makeham
// hazard clamps to 1 (death guaranteed for every possible hash draw,
// GR#21) -- the same "age 1000y, critical health, zero access" fixture
// bug270_test.go/coldpass_dissolution_test.go use, so a selection is never
// left to chance.
func mkGuaranteedDeathRecord(id uint64, month int64) ColdRecord {
	r := mkRecord(id, 0)
	r.BirthMonth = month - 12000
	r.HealthBand = HealthCritical
	r.Access = 0
	r.Household = 0
	r.Partner = 0
	return r
}

// seedGuaranteedDeathCohort seeds n guaranteed-death citizens with ids
// idBase+1..idBase+n and sets the sim clock to month, returning the ids in
// ascending order (the FIFO tiebreak order AC-4 documents).
func seedGuaranteedDeathCohort(t *testing.T, api *CitizensAPI, idBase uint64, n int, month int64) []uint64 {
	t.Helper()
	ids := make([]uint64, 0, n)
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		id := idBase + uint64(i)
		recs = append(recs, mkGuaranteedDeathRecord(id, month))
		ids = append(ids, id)
	}
	if err := api.SeedColdRecords(recs, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()
	return ids
}

// TestLiveColdPassCliffBounded (AC-1, the load-bearing AC, at the LIVE
// cold-pass level -- the check inc1's isolated DeathQueue test could not
// give). A cohort of 2000 same-birthMonth citizens on the steep Gompertz
// slope, aged and health-banded so MortalityDeath selects nearly all of
// them in one month (verified below so the assertion is never vacuous),
// is advanced through the REAL AdvanceMonth path. The LIVING-POPULATION
// delta in that single month must be <= the data-file budget (25,
// data/mortality.json) -- a bare population-count check, not a query of
// internal queue/report state, so an implementation that merely reports a
// smooth count while still removing the whole cohort in one month (the
// stated false-pass risk) cannot pass this.
func TestLiveColdPassCliffBounded(t *testing.T) {
	const seed = uint64(555)
	const month = int64(20000)
	const n = 2000
	const budget = 25 // data/mortality.json params.monthlyDeathBudget.value

	api, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seedGuaranteedDeathCohort(t, api, 100_000, n, month)

	popBefore := api.TotalPopulation("corr")
	if popBefore != n {
		t.Fatalf("fixture broken: popBefore = %d, want %d", popBefore, n)
	}

	// Sanity: this cohort really is a cliff under MortalityDeath -- proves
	// the test setup selects well more than the budget (mirrors deathwave_
	// test.go's mkCliffCohort sanity check, at the live-pass fixture).
	selectable := 0
	for i := 1; i <= n; i++ {
		if MortalityDeath(seed, 100_000+uint64(i), month, 12000, HealthCritical, 0) {
			selectable++
		}
	}
	if selectable < budget*3 {
		t.Fatalf("test setup invalid: only %d of %d citizens are hazard-selectable this month, want >> budget=%d", selectable, n, budget)
	}

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}

	popAfter := api.TotalPopulation("corr")
	delta := popBefore - popAfter
	if delta > budget {
		t.Fatalf("living-population delta this month = %d, want <= budget=%d (cohort cliff NOT smoothed by the live cold pass)", delta, budget)
	}
	if delta <= 0 {
		t.Fatalf("living-population delta = %d, want > 0 (fixture must actually realise some deaths, or this test is vacuous)", delta)
	}

	// The remainder must be RETAINED (queued, not lost) -- AC-2's
	// conservation half of the same guarantee, checked here at the queue
	// level: everything hazard-selected this month that was not realised
	// must still be sitting in the live DeathQueue.
	remaining := api.deathQueue.Len("corr")
	if remaining != selectable-delta {
		t.Fatalf("deathQueue.Len() = %d, want %d (selected=%d, realised=%d)", remaining, selectable-delta, selectable, delta)
	}
}

// TestLiveColdPassConservationAcrossDrain (AC-2, at the live level): a
// cohort larger than one month's budget is fully drained across enough
// subsequent months with NO new selections (every citizen already
// guaranteed-selected in month 1), and the total population removed must
// equal the total initially selected exactly -- no death lost, none
// duplicated -- with the queue empty at the end.
func TestLiveColdPassConservationAcrossDrain(t *testing.T) {
	const seed = uint64(556)
	const month = int64(20000)
	const n = 100
	const budget = 25

	api, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seedGuaranteedDeathCohort(t, api, 200_000, n, month)
	popBefore := api.TotalPopulation("corr")

	// n/budget rounds up to fully drain a guaranteed-selected cohort of n,
	// plus one extra month of headroom in case any citizen's shard-visit
	// ordering pushed its selection a tick later than expected.
	rounds := (n + budget - 1) / budget
	for i := 0; i < rounds+1; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", i, err)
		}
	}

	popAfter := api.TotalPopulation("corr")
	if popBefore-popAfter != n {
		t.Fatalf("population removed = %d, want %d (every guaranteed death must eventually realise, exactly once)", popBefore-popAfter, n)
	}
	if got := api.deathQueue.Len("corr"); got != 0 {
		t.Fatalf("deathQueue.Len() = %d, want 0 (queue must fully drain)", got)
	}
	if got := api.deathQueue.TotalRealised("corr"); got != n {
		t.Fatalf("deathQueue.TotalRealised() = %d, want %d (conservation: totalRealised == totalSelected)", got, n)
	}
}

// TestLiveColdPassQueuedStaysAliveAgesAndCounts (AC-3/ASM-581, at the live
// level): a cohort of 40 guaranteed-death citizens, budget 25, realises
// only 25 in month 1 -- the remaining 15 must (a) still be present in the
// cold store / TotalPopulation, (b) still resolve via CitizenAt (still a
// queryable, "alive" citizen, not a frozen ghost), (c) still age between
// month 1 and month 2, and (d) never be double-selected (the queue's
// pending count for the survivors must stay exactly 15 across the month
// they wait, never grow).
func TestLiveColdPassQueuedStaysAliveAgesAndCounts(t *testing.T) {
	const seed = uint64(557)
	const month = int64(20000)
	const n = 40
	const budget = 25

	api, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	ids := seedGuaranteedDeathCohort(t, api, 300_000, n, month)

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 1: %v", err)
	}

	popAfterMonth1 := api.TotalPopulation("corr")
	if want := n - budget; popAfterMonth1 != want {
		t.Fatalf("population after month 1 = %d, want %d (exactly the budget realised)", popAfterMonth1, want)
	}
	if got := api.deathQueue.Len("corr"); got != n-budget {
		t.Fatalf("deathQueue.Len() after month 1 = %d, want %d (the remainder must be RETAINED, not lost)", got, n-budget)
	}

	// Find one survivor still resident (a queued-but-unrealised citizen)
	// and prove it is a fully live, queryable, ageing citizen.
	var survivorID uint64
	found := false
	for _, id := range ids {
		if _, ok := api.CitizenAt(id, "corr"); ok {
			survivorID = id
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fixture broken: expected at least one queued survivor after month 1")
	}
	before, _ := api.CitizenAt(survivorID, "corr")
	ageMonth1 := before.Age()

	if _, queued := api.deathQueue.IsQueued(survivorID, "corr"); !queued {
		t.Fatalf("survivor %d is resident but not IsQueued -- it should still be a pending queue entry from month 1's selection", survivorID)
	}

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 2: %v", err)
	}

	// The survivor may itself have been realised in month 2 (FIFO order),
	// but if it is STILL resident, its age must have advanced by exactly
	// one month -- proving it was not frozen while queued.
	if after, ok := api.CitizenAt(survivorID, "corr"); ok {
		if after.Age() != ageMonth1+1 {
			t.Fatalf("survivor %d age = %d after a further month queued, want %d (a queued citizen must still age, ASM-581)", survivorID, after.Age(), ageMonth1+1)
		}
	}

	// Single-terminal-selection check (AC-3(b)): TotalRealised at the end
	// of month 2 must equal exactly min(n, 2*budget) = n (40 <= 50) -- if a
	// queued citizen had been re-drawn and re-enqueued each month it waited,
	// conservation (checked in the sibling drain test) would already have
	// caught a duplicate, but this asserts it directly via the queue's own
	// lifetime realised count never exceeding the population that ever
	// existed.
	if got := api.deathQueue.TotalRealised("corr"); got > n {
		t.Fatalf("deathQueue.TotalRealised() = %d, exceeds the cohort size %d (a citizen was realised more than once)", got, n)
	}
}

// TestLiveColdPassHouseholdDissolutionAtRealisation (BUG-270/BUG-369
// parity through the death queue): dissolution must fire when a citizen is
// actually REALISED (removed), not when merely selected/queued. 25 filler
// guaranteed-deaths with LOWER ids than the elder saturate month 1's
// budget (FIFO ties break by ascending citizenID, AC-4), so the elder is
// selected in month 1 but held in the queue -- the household must stay
// fully intact (widow still partnered, still co-housed) while the elder is
// queued-but-unrealised. Only once the elder is actually realised (month
// 2, queue now holds only the elder) must the pairing dissolve and the
// household prune, exactly as the immediate cold-pass path (BUG-369) and
// the elevated hot path (BUG-270) both do.
func TestLiveColdPassHouseholdDissolutionAtRealisation(t *testing.T) {
	const seed = uint64(558)
	const month = int64(20000)
	const budget = 25
	const elderID uint64 = 900_000
	const widowID uint64 = 900_001
	const childID uint64 = 900_002

	api, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}

	// 25 lower-id filler guaranteed deaths -- FIFO-prioritised ahead of the
	// elder (same selectionMonth, lower citizenID, AC-4).
	fillers := make([]ColdRecord, 0, budget)
	for i := 1; i <= budget; i++ {
		fillers = append(fillers, mkGuaranteedDeathRecord(uint64(i), month))
	}
	if err := api.SeedColdRecords(fillers, "corr"); err != nil {
		t.Fatalf("SeedColdRecords fillers: %v", err)
	}

	elder := mkGuaranteedDeathRecord(elderID, month)
	widow := mkRecord(widowID, 0)
	widow.BirthMonth = month - 1100 // above the fertility window, low hazard
	widow.HealthBand = HealthGood
	widow.Access = 100
	widow.Household = 0
	widow.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{elder, widow}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords elder/widow: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: elderID, PartnerID: widowID}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	hh, ok := api.HouseholdOf(elderID, "corr")
	if !ok {
		t.Fatal("household not formed")
	}
	child := mkRecord(childID, 0)
	child.BirthMonth = month - 1
	child.HealthBand = HealthGood
	child.Access = 100
	child.Household = safeUint32(hh.ID)
	child.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{child}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords child: %v", err)
	}
	api.mu.Lock()
	api.households[hh.ID].AddMember(child.ID)
	api.month = month
	api.mu.Unlock()

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 1: %v", err)
	}

	// Month 1: the 25 fillers must be gone; the elder must be QUEUED, not
	// realised -- the household must therefore be fully intact still.
	for i := 1; i <= budget; i++ {
		if _, ok := api.coldRecord(uint64(i)); ok {
			t.Fatalf("filler %d survived month 1 (budget saturation fixture broken)", i)
		}
	}
	if _, ok := api.coldRecord(elderID); !ok {
		t.Fatal("elder was realised in month 1 -- FIFO/budget fixture broken, elder should still be queued")
	}
	if _, queued := api.deathQueue.IsQueued(elderID, "corr"); !queued {
		t.Fatal("elder is resident but not queued after month 1 -- selection did not happen")
	}
	widowMonth1, ok := api.CitizenAt(widowID, "corr")
	if !ok {
		t.Fatal("widow vanished during month 1 (over-removal)")
	}
	if widowMonth1.Partner != elderID {
		t.Fatalf("widow.Partner = %d after month 1, want %d (elder still queued, dissolution must NOT have fired yet)", widowMonth1.Partner, elderID)
	}
	hMonth1, ok := api.Household(hh.ID, "corr")
	if !ok {
		t.Fatal("household deleted while the elder is only QUEUED, not realised")
	}
	foundElder := false
	for _, m := range hMonth1.Members {
		if m == elderID {
			foundElder = true
		}
	}
	if !foundElder {
		t.Fatal("elder pruned from household Members while only queued -- dissolution fired at selection, not realisation")
	}

	// Month 2: the queue now holds only the elder; realisation must fire
	// and dissolution must run exactly as the immediate/hot paths do.
	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 2: %v", err)
	}
	if _, ok := api.coldRecord(elderID); ok {
		t.Fatal("elder survived month 2 -- should have been realised (queue held only the elder)")
	}
	widowMonth2, ok := api.CitizenAt(widowID, "corr")
	if !ok {
		t.Fatal("widow vanished alongside the elder's realisation (over-removal)")
	}
	if widowMonth2.Partner != 0 {
		t.Fatalf("widow.Partner = %d after the elder's realisation, want 0 (pairing must dissolve NOW)", widowMonth2.Partner)
	}
	if widowMonth2.Household != hh.ID {
		t.Fatalf("widow.Household = %d, want %d (survivor keeps the household)", widowMonth2.Household, hh.ID)
	}
	hMonth2, ok := api.Household(hh.ID, "corr")
	if !ok {
		t.Fatal("household deleted despite surviving members (widow + child)")
	}
	for _, m := range hMonth2.Members {
		if m == elderID {
			t.Fatal("household Members still lists the realised elder (leaked member)")
		}
	}
	foundWidow, foundChild := false, false
	for _, m := range hMonth2.Members {
		switch m {
		case widowID:
			foundWidow = true
		case childID:
			foundChild = true
		}
	}
	if !foundWidow || !foundChild {
		t.Fatalf("household Members = %v, want exactly [%d %d]", hMonth2.Members, widowID, childID)
	}
}

// TestLiveColdPassDeterministicAcrossWorkerCounts (AC-15, at the live
// wiring level): a cohort spanning both guaranteed-death and mild-hazard
// citizens, run through the death-queue-wired AdvanceMonth path at worker
// counts 1 and 14, must produce a byte-identical PopulationHash and the
// same TotalPopulation. The queue's FIFO order is a pure function of
// queue CONTENTS (selectionMonth, citizenID), never of Enqueue call order,
// so parallel shards racing to enqueue at any worker count must still
// realise the identical sequence.
func TestLiveColdPassDeterministicAcrossWorkerCounts(t *testing.T) {
	const seed = uint64(559)
	const month = int64(20000)

	records := make([]ColdRecord, 0, 300)
	for i := 1; i <= 200; i++ {
		records = append(records, mkGuaranteedDeathRecord(uint64(i), month))
	}
	for i := 201; i <= 300; i++ {
		r := mkRecord(uint64(i), 0)
		r.BirthMonth = month - int64((i%80)*12) // ordinary ages, tiny hazard
		r.Household = 0
		r.Partner = 0
		records = append(records, r)
	}

	run := func(workers int) ([32]byte, int) {
		api, err := NewCitizensAPI(seed, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = workers
		if err := api.SeedColdRecords(records, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = month
		api.mu.Unlock()
		for m := 0; m < 6; m++ {
			if err := api.AdvanceMonth("corr"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.PopulationHash("corr"), api.TotalPopulation("corr")
	}

	hash1, pop1 := run(1)
	hash14, pop14 := run(14)
	if pop1 != pop14 {
		t.Fatalf("TotalPopulation differs by worker count: 1 worker=%d, 14 workers=%d", pop1, pop14)
	}
	if hash1 != hash14 {
		t.Fatalf("PopulationHash differs by worker count: 1 worker=%x, 14 workers=%x (death-queue realisation must be worker-count invariant)", hash1, hash14)
	}
	if pop1 == 300 {
		t.Fatal("fixture broken: expected at least some of the 200 guaranteed deaths to have realised across 6 months")
	}
}

// TestQueuedCitizenDepartingViaOtherLifeEventReconcilesQueue (integration
// fix found 2026-09-01 by an independent compose-level audit): a citizen
// can now be QUEUED (hazard-selected, not yet realised -- still an
// ordinary resident, ASM-581) for months while the smoothing budget works
// through a backlog. LifeEventDeath is a GENERIC departure command that
// predates the death queue (engine.attract's emigration path issues it
// directly -- migration.go's applyEmigration, unrelated to the cold-pass
// mortality hazard draw). If a citizen departs via THAT path while still
// queued, and the queue is not told, two things break: (a) the queue
// entry is a permanent GHOST that never drains (an id no longer resident,
// occupying a budget slot forever), and (b) the eventual Realise() that
// releases it reports a "death" for someone already gone, corrupting
// AC-2's totalRealised==totalSelected conservation invariant. This test
// proves the registry.go LifeEventDeath handler reconciles the queue via
// RealiseByID BEFORE removing the citizen.
func TestQueuedCitizenDepartingViaOtherLifeEventReconcilesQueue(t *testing.T) {
	const seed = uint64(560)
	const month = int64(20000)
	const n = 40
	const budget = 25 // exceeds n's budget-1-month drain, guaranteeing survivors to depart early

	api, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	ids := seedGuaranteedDeathCohort(t, api, 400_000, n, month)

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 1: %v", err)
	}
	if got := api.deathQueue.Len("corr"); got != n-budget {
		t.Fatalf("deathQueue.Len() after month 1 = %d, want %d (fixture broken: need a queued survivor to depart early)", got, n-budget)
	}

	// Find a still-queued survivor (selected in month 1, not yet realised).
	var survivorID uint64
	found := false
	for _, id := range ids {
		if _, queued := api.deathQueue.IsQueued(id, "corr"); queued {
			survivorID = id
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fixture broken: expected at least one queued survivor after month 1")
	}
	totalRealisedBefore := api.deathQueue.TotalRealised("corr")

	// Simulate the survivor departing via a DIFFERENT life event (the
	// exact shape engine.attract's emigration uses) WHILE still queued.
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: survivorID}); err != nil {
		t.Fatalf("ApplyLifeEventCommand(LifeEventDeath) for queued survivor %d: %v", survivorID, err)
	}

	// The citizen must be gone from the cold store...
	if _, ok := api.coldRecord(survivorID); ok {
		t.Fatalf("survivor %d still resident after LifeEventDeath", survivorID)
	}
	// ...and the queue must have been reconciled: no longer queued, and
	// TotalRealised incremented by exactly one (the RealiseByID close-out),
	// never left as a permanent ghost entry.
	if _, queued := api.deathQueue.IsQueued(survivorID, "corr"); queued {
		t.Fatalf("survivor %d still IsQueued after departing via LifeEventDeath -- the queue entry is now a permanent, un-drainable ghost", survivorID)
	}
	if got := api.deathQueue.TotalRealised("corr"); got != totalRealisedBefore+1 {
		t.Fatalf("deathQueue.TotalRealised() = %d after the reconciling LifeEventDeath, want %d (exactly one close-out, no double count)", got, totalRealisedBefore+1)
	}

	// This reconciliation must NOT feed compose's mortality death tally:
	// draining the rest of the queue over subsequent months must realise
	// exactly n-1 MORE citizens through the budgeted path (the survivor
	// above already left via the OTHER path, not this one), and the
	// lifetime TotalRealised must land at exactly n -- never n+1 (a
	// double-count) and never stuck below n (a lost/ghost entry).
	for i := 0; i < 4; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth drain %d: %v", i, err)
		}
	}
	if got := api.deathQueue.Len("corr"); got != 0 {
		t.Fatalf("deathQueue.Len() after the full drain = %d, want 0", got)
	}
	if got := api.deathQueue.TotalRealised("corr"); got != n {
		t.Fatalf("deathQueue.TotalRealised() after the full drain = %d, want exactly %d (conservation: the reconciled departure counts once, never twice, never lost)", got, n)
	}
}
