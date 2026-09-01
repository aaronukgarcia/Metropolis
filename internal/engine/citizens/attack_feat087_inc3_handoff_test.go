package citizens

import (
	"math"
	"testing"
	"unsafe"
)

// deathQueueByteCopy performs SEC-020's attack on a DeathQueue — a plain
// struct copy — via a raw memcpy through unsafe.Pointer, mirroring
// citizensByteCopy in copyguard_test.go: a literal `cp := *q` is flagged by
// go vet's copylocks check, which CI runs bare (not only via golangci).
func deathQueueByteCopy(q *DeathQueue) *DeathQueue {
	cp := new(DeathQueue)
	*(*[unsafe.Sizeof(DeathQueue{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(DeathQueue{})]byte)(unsafe.Pointer(q))
	return cp
}

// FEAT-087 inc3 INDEPENDENT DESTRUCTIVE ROUND (Opus r1, GR#23 -- the
// attacker is not the author). These tests exist to FAIL if inc3's
// handoff/drain contract is broken, not to restate the builder's own
// happy paths. Each one was proved able to fail by mutating the
// implementation in a scratch copy and observing RED.

// --- Attack 1 (F8 aliasing lesson): the handoff must never hand out the
// internal backing array. ---
//
// The F8 class: a getter that returns q.handoff directly lets any consumer
// (FEAT-088) corrupt engine.citizens' own realisation record by writing
// through the returned slice, including growing it via append into spare
// capacity the queue still owns.
func TestAttackInc3_HandoffReturnIsNotAliasedToInternalState(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 10, 0)

	mustEnqueue(t, q, 1, 100, "corr")
	mustEnqueue(t, q, 2, 100, "corr")
	mustEnqueue(t, q, 3, 100, "corr")
	released := q.RealiseDrained(cfg, false, 200, "corr")
	if len(released) != 3 {
		t.Fatalf("setup: released %d, want 3", len(released))
	}

	// Vandalise the value RealiseDrained returned.
	released[0] = RealisedDeath{CitizenID: 999999, DeathMonth: -1, EmergencyFlag: true}

	// Vandalise a first read of the stream, then append past its length to
	// try to reach any spare capacity in the queue's own backing array.
	first := q.RealisedDeaths("corr")
	first[1] = RealisedDeath{CitizenID: 888888, DeathMonth: -2, EmergencyFlag: true}
	first = append(first, RealisedDeath{CitizenID: 777777})
	_ = first

	// A second read must be pristine.
	second := q.RealisedDeaths("corr")
	want := []RealisedDeath{
		{CitizenID: 1, DeathMonth: 200},
		{CitizenID: 2, DeathMonth: 200},
		{CitizenID: 3, DeathMonth: 200},
	}
	assertRealisedDeathsEqual(t, "RealisedDeaths after caller vandalism", second, want)
}

// --- Attack 2: cumulative-stream semantics must be stated and stable. ---
//
// AC-9 requires a queryable, deterministically-ordered stream. The chosen
// design is CUMULATIVE (append-only, never drained on read). This test
// pins that: two consecutive reads with no intervening realisation must be
// identical, so a consumer polling the surface cannot silently lose deaths
// to a read that clears. If the design is ever changed to drain-on-read,
// this test must be replaced deliberately, not broken by accident.
func TestAttackInc3_RealisedDeathsIsCumulativeNotDrainedOnRead(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 2, 0)

	for id := uint64(1); id <= 4; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}
	q.RealiseDrained(cfg, false, 200, "corr")

	a := q.RealisedDeaths("corr")
	b := q.RealisedDeaths("corr")
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("read-twice: len(a)=%d len(b)=%d, want 2 and 2 -- a read must not drain the stream", len(a), len(b))
	}
	assertRealisedDeathsEqual(t, "second read", b, a)

	// A later realisation APPENDS to (never replaces) the earlier records.
	q.RealiseDrained(cfg, false, 201, "corr")
	c := q.RealisedDeaths("corr")
	if len(c) != 4 {
		t.Fatalf("after a second realisation the cumulative stream has %d records, want 4", len(c))
	}
	assertRealisedDeathsEqual(t, "prefix stability", c[:2], a)
}

// --- Attack 3 (ASM-580): kill the min(). ---
//
// The mutation this catches: using the budget alone (dropping the drain
// clamp), or using the drain alone (dropping the budget). Both knobs must
// bind independently, and the queue length must bind third.
func TestAttackInc3_MinOfBudgetDrainQueuedBindsAllThreeWays(t *testing.T) {
	cases := []struct {
		name    string
		budget  int
		drain   int
		queued  int
		want    int
		binding string
	}{
		{"budget binds", 3, 100, 50, 3, "budget"},
		{"drain binds", 100, 4, 50, 4, "drain"},
		{"queued binds", 100, 100, 5, 5, "queued"},
		{"drain zero halts entirely", 100, 0, 50, 0, "drain=0"},
		{"budget and drain equal", 7, 7, 50, 7, "tie"},
		{"drain one below budget", 8, 7, 50, 7, "drain"},
		{"budget one below drain", 7, 8, 50, 7, "budget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := NewDeathQueue()
			cfg := mkFixedBudgetCfg(t, tc.budget, 0)
			drain := tc.drain
			if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return drain }), "corr"); err != nil {
				t.Fatalf("SetDrainCapacity: %v", err)
			}
			for id := uint64(1); id <= uint64(tc.queued); id++ {
				mustEnqueue(t, q, id, 100, "corr")
			}
			got := q.RealiseDrained(cfg, false, 200, "corr")
			if len(got) != tc.want {
				t.Fatalf("released %d, want %d (min(budget=%d, drain=%d, queued=%d), %s should bind)",
					len(got), tc.want, tc.budget, tc.drain, tc.queued, tc.binding)
			}
			// The undrained remainder must still be pending -- smoothing
			// defers, never drops (AC-2).
			if pend := q.Len("corr"); pend != tc.queued-tc.want {
				t.Fatalf("pending=%d after releasing %d of %d queued, want %d -- deferral must never drop a selection",
					pend, tc.want, tc.queued, tc.queued-tc.want)
			}
		})
	}
}

// --- Attack 4 (AC-10): kill the emergency flag propagation. ---
//
// Catches a mutation that hardcodes EmergencyFlag to false/true, or that
// tags the whole stream with the LAST call's flag rather than each
// record's own release conditions.
func TestAttackInc3_EmergencyFlagIsPerRecordNotWholeStream(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 2, 2)

	for id := uint64(1); id <= 6; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}
	// Alternate: normal, emergency, normal -- so neither "always false",
	// "always true", nor "last call's flag wins" can pass.
	q.RealiseDrained(cfg, false, 200, "corr")
	q.RealiseDrained(cfg, true, 201, "corr")
	q.RealiseDrained(cfg, false, 202, "corr")

	got := q.RealisedDeaths("corr")
	want := []RealisedDeath{
		{CitizenID: 1, DeathMonth: 200, EmergencyFlag: false},
		{CitizenID: 2, DeathMonth: 200, EmergencyFlag: false},
		{CitizenID: 3, DeathMonth: 201, EmergencyFlag: true},
		{CitizenID: 4, DeathMonth: 201, EmergencyFlag: true},
		{CitizenID: 5, DeathMonth: 202, EmergencyFlag: false},
		{CitizenID: 6, DeathMonth: 202, EmergencyFlag: false},
	}
	assertRealisedDeathsEqual(t, "per-record emergency flags", got, want)
}

// --- Attack 5: the nil-drain equivalence claim, differentially. ---
//
// The builder duplicated EmergencyRealise's budget rule inside
// RealiseDrained rather than calling it, so nothing structurally enforces
// the claimed byte-identical behaviour. This differential test IS that
// enforcement: across an exhaustive grid of (ordinaryBudget,
// emergencyBudget, emergency, queued), a nil-drain RealiseDrained must
// release the identical id sequence EmergencyRealise does.
func TestAttackInc3_NilDrainIsDifferentiallyIdenticalToEmergencyRealise(t *testing.T) {
	for _, ordinary := range []int{1, 3, 25} {
		for _, emergencyBudget := range []int{0, 1, 4, 100} {
			for _, emergency := range []bool{false, true} {
				for _, queued := range []int{0, 1, 5, 40} {
					cfg := mkFixedBudgetCfg(t, ordinary, emergencyBudget)

					ref := NewDeathQueue()
					got := NewDeathQueue()
					for id := uint64(1); id <= uint64(queued); id++ {
						mustEnqueue(t, ref, id, 100, "corr")
						mustEnqueue(t, got, id, 100, "corr")
					}

					wantIDs := EmergencyRealise(ref, cfg, emergency, 200, "corr")
					gotRecords := got.RealiseDrained(cfg, emergency, 200, "corr")

					if len(gotRecords) != len(wantIDs) {
						t.Fatalf("ordinary=%d emergencyBudget=%d emergency=%v queued=%d: RealiseDrained released %d, EmergencyRealise released %d -- the nil-drain path must stay identical",
							ordinary, emergencyBudget, emergency, queued, len(gotRecords), len(wantIDs))
					}
					for i := range wantIDs {
						if gotRecords[i].CitizenID != wantIDs[i] {
							t.Fatalf("ordinary=%d emergencyBudget=%d emergency=%v queued=%d: release order diverges at %d: RealiseDrained=%d EmergencyRealise=%d",
								ordinary, emergencyBudget, emergency, queued, i, gotRecords[i].CitizenID, wantIDs[i])
						}
					}
					// The residual queue must match too, not just the release.
					if a, b := got.Len("corr"), ref.Len("corr"); a != b {
						t.Fatalf("ordinary=%d emergencyBudget=%d emergency=%v queued=%d: pending after release %d vs %d",
							ordinary, emergencyBudget, emergency, queued, a, b)
					}
				}
			}
		}
	}
}

// --- Attack 6: hostile DrainCapacity implementations. ---
//
// A FEAT-088 consumer is foreign code. Negative, MinInt, and MaxInt
// returns must neither panic, nor overflow, nor invert into "unlimited".
func TestAttackInc3_HostileDrainCapacityValues(t *testing.T) {
	cases := []struct {
		name     string
		drain    int
		queued   int
		budget   int
		wantRel  int
		wantPend int
	}{
		{"negative clamps to zero, never unlimited", -1, 20, 5, 0, 20},
		{"MinInt clamps to zero, no overflow", math.MinInt, 20, 5, 0, 20},
		{"MaxInt does not overflow, budget still binds", math.MaxInt, 20, 5, 5, 15},
		{"exactly zero halts", 0, 20, 5, 0, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := NewDeathQueue()
			cfg := mkFixedBudgetCfg(t, tc.budget, 0)
			d := tc.drain
			if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return d }), "corr"); err != nil {
				t.Fatalf("SetDrainCapacity: %v", err)
			}
			for id := uint64(1); id <= uint64(tc.queued); id++ {
				mustEnqueue(t, q, id, 100, "corr")
			}
			got := q.RealiseDrained(cfg, false, 200, "corr")
			if len(got) != tc.wantRel {
				t.Fatalf("drain=%d released %d, want %d", tc.drain, len(got), tc.wantRel)
			}
			if p := q.Len("corr"); p != tc.wantPend {
				t.Fatalf("drain=%d pending %d, want %d -- a hostile capacity must never drop a queued selection", tc.drain, p, tc.wantPend)
			}
			// Nothing bogus reached the handoff.
			for _, rd := range q.RealisedDeaths("corr") {
				if rd.CitizenID == 0 || rd.DeathMonth != 200 {
					t.Fatalf("drain=%d produced a malformed handoff record %+v", tc.drain, rd)
				}
			}
		})
	}
}

// A drain implementation that panics must not leave the queue's mutex
// held or its state half-mutated (the "panic between drain and consume"
// case in the round brief).
func TestAttackInc3_PanickingDrainLeavesQueueUsableAndConsistent(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { panic("hostile FEAT-088 consumer") }), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= 10; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected the hostile drain's panic to propagate")
			}
		}()
		q.RealiseDrained(cfg, false, 200, "corr")
	}()

	// The mutex must have been released by the deferred unlock, and no
	// partial realisation may have occurred.
	if err := q.SetDrainCapacity(nil, "corr"); err != nil {
		t.Fatalf("queue unusable after a panicking drain (mutex likely still held): %v", err)
	}
	if n := q.Len("corr"); n != 10 {
		t.Fatalf("pending=%d after a panicking drain, want 10 -- no partial realisation", n)
	}
	if n := len(q.RealisedDeaths("corr")); n != 0 {
		t.Fatalf("handoff has %d records after a panicking drain, want 0", n)
	}
	if n := q.TotalRealised("corr"); n != 0 {
		t.Fatalf("TotalRealised=%d after a panicking drain, want 0", n)
	}
}

// --- Attack 7 (SEC-020): the copyguard on every new surface. ---
func TestAttackInc3_CopyguardOnNewSurfaces(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	for id := uint64(1); id <= 5; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}
	cp := deathQueueByteCopy(q) // SEC-020 attack via byte-copy (a literal `*q` trips go vet copylocks, which CI runs)

	if err := cp.SetDrainCapacity(nil, "corr"); err == nil {
		t.Fatalf("SetDrainCapacity on a struct copy returned nil error -- SEC-020 guard did not fire")
	}
	if got := cp.RealiseDrained(cfg, false, 200, "corr"); got != nil {
		t.Fatalf("RealiseDrained on a struct copy released %d records -- SEC-020 guard did not fire", len(got))
	}
	// The copy must not have mutated the original.
	if n := q.Len("corr"); n != 5 {
		t.Fatalf("a struct-copy RealiseDrained mutated the original queue: pending=%d, want 5", n)
	}

	// The CitizensAPI-level surfaces must return the registry error.
	api, err := NewCitizensAPI(7, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	apiCopy := citizensByteCopy(api) // shared helper from copyguard_test.go; see its doc comment
	if err := apiCopy.SetDeathDrainCapacity(nil, "corr"); err == nil {
		t.Fatalf("CitizensAPI.SetDeathDrainCapacity on a struct copy returned nil error")
	}
	if _, err := apiCopy.DeathHandoff("corr"); err == nil {
		t.Fatalf("CitizensAPI.DeathHandoff on a struct copy returned nil error")
	}
}

// --- Attack 8: determinism of the handoff stream. ---
//
// Same inputs, same drain sequence, two independent runs -> identical
// records. Guards against any map-range creeping into the release path.
func TestAttackInc3_HandoffIsDeterministicAcrossRuns(t *testing.T) {
	run := func() []RealisedDeath {
		q := NewDeathQueue()
		cfg := mkFixedBudgetCfg(t, 7, 3)
		call := 0
		if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int {
			call++
			return call % 5
		}), "corr"); err != nil {
			t.Fatalf("SetDrainCapacity: %v", err)
		}
		// Enqueue in a deliberately scrambled order across three months.
		for i := 0; i < 90; i++ {
			id := uint64((i*37)%90) + 1
			month := int64(100 + (i % 3))
			mustEnqueue(t, q, id, month, "corr")
		}
		for m := int64(200); m < 240; m++ {
			q.RealiseDrained(cfg, m%7 == 0, m, "corr")
		}
		return q.RealisedDeaths("corr")
	}
	a, b := run(), run()
	if len(a) == 0 {
		t.Fatalf("test setup invalid: nothing realised")
	}
	assertRealisedDeathsEqual(t, "second identical run", b, a)
}

// --- Attack 9: conservation under a long randomised drain schedule. ---
//
// Every selected citizen is exactly one of {still pending, realised in the
// handoff exactly once}. No duplicate CitizenID may EVER be emitted, and
// in == out + pending must hold after every single call.
func TestAttackInc3_ConservationUnderRandomisedDrainSchedule(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 9, 4)

	// A deterministic pseudo-random capacity sequence spanning 0, small,
	// huge and negative.
	caps := []int{0, 3, math.MaxInt, 1, -5, 12, 0, 7, 2, math.MaxInt, 0, 4}
	idx := 0
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int {
		v := caps[idx%len(caps)]
		idx++
		return v
	}), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}

	enqueued := 0
	seen := make(map[uint64]int)
	var nextID uint64 = 1

	for month := int64(0); month < 400; month++ {
		// A scrambled but deterministic number of new selections.
		n := int((month*61 + 17) % 13)
		for i := 0; i < n; i++ {
			mustEnqueue(t, q, nextID, month, "corr")
			nextID++
			enqueued++
		}

		released := q.RealiseDrained(cfg, month%11 == 0, month, "corr")
		for _, rd := range released {
			seen[rd.CitizenID]++
			if seen[rd.CitizenID] > 1 {
				t.Fatalf("month %d: citizen %d realised %d times -- a duplicate corpse", month, rd.CitizenID, seen[rd.CitizenID])
			}
			if rd.DeathMonth != month {
				t.Fatalf("month %d: record %+v carries the wrong DeathMonth", month, rd)
			}
		}

		// Conservation after EVERY call: in == out + pending.
		stream := q.RealisedDeaths("corr")
		if len(stream)+q.Len("corr") != enqueued {
			t.Fatalf("month %d: conservation broken -- handoff=%d pending=%d enqueued=%d",
				month, len(stream), q.Len("corr"), enqueued)
		}
		// The cumulative stream must equal the realisation sequence
		// exactly, in order (nothing realised without being handed off).
		seq := q.RealisedSequence("corr")
		if len(seq) != len(stream) {
			t.Fatalf("month %d: RealisedSequence=%d but handoff=%d -- a death was realised without reaching FEAT-088", month, len(seq), len(stream))
		}
		for i := range seq {
			if seq[i] != stream[i].CitizenID {
				t.Fatalf("month %d: handoff order diverges from the realisation sequence at %d", month, i)
			}
		}
	}

	if len(seen) == 0 || enqueued == 0 {
		t.Fatalf("test setup invalid: enqueued=%d realised=%d", enqueued, len(seen))
	}
	// Something must have remained pending at least once, or the drain
	// clamp never actually bound and this test is vacuous.
	if q.Len("corr") == 0 && enqueued == len(seen) {
		t.Logf("note: queue fully drained by the end (enqueued=%d)", enqueued)
	}
}

// --- Attack 10: the LIVE seam -- population removal must reconcile with
// the handoff, with a real drain capacity wired. ---
//
// This is the check the builder's isolated unit tests cannot give: with a
// FEAT-088 drain wired into a real CitizensAPI, does every citizen that
// disappears from the population appear in DeathHandoff exactly once, and
// does the drain actually gate the live realisation rate?
func TestAttackInc3_LiveAdvanceDayTickHandoffReconcilesWithPopulation(t *testing.T) {
	const seed = uint64(561)
	const n = 200

	run := func(drainPerMonth int) (pop0, popEnd, handoff, pending int, ids []uint64) {
		api, err := NewCitizensAPI(seed, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		if drainPerMonth >= 0 {
			d := drainPerMonth
			if err := api.SetDeathDrainCapacity(DrainCapacityFunc(func(int64) int { return d }), "corr"); err != nil {
				t.Fatalf("SetDeathDrainCapacity: %v", err)
			}
		}
		seedGuaranteedDeathCohort(t, api, 500_000, n, monthJanuary)
		pop0 = api.TotalPopulation("corr")

		for m := 0; m < 3; m++ {
			for d := 0; d < DaysPerMonth; d++ {
				if _, _, err := api.AdvanceDayTick("corr"); err != nil {
					t.Fatalf("AdvanceDayTick: %v", err)
				}
			}
		}

		stream, err := api.DeathHandoff("corr")
		if err != nil {
			t.Fatalf("DeathHandoff: %v", err)
		}
		popEnd = api.TotalPopulation("corr")
		pending = api.deathQueue.Len("corr")
		for _, rd := range stream {
			ids = append(ids, rd.CitizenID)
		}
		return pop0, popEnd, len(stream), pending, ids
	}

	// (a) No drain wired: behaviour is the unwired baseline.
	pop0, popEnd, handoff, _, ids := run(-1)
	if handoff == 0 {
		t.Fatalf("test setup invalid: no deaths realised over three months")
	}
	// Every handoff record must correspond to exactly one citizen who left
	// the population.
	if pop0-popEnd != handoff {
		t.Fatalf("population fell by %d but the handoff reported %d deaths -- a citizen was removed without reaching FEAT-088, or a phantom death was handed off", pop0-popEnd, handoff)
	}
	dup := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		if dup[id] {
			t.Fatalf("citizen %d appears twice in the live DeathHandoff stream", id)
		}
		dup[id] = true
	}

	// (b) A drain of 1/month must strictly gate the live realisation rate
	// below the unwired baseline -- proving the injected knob is actually
	// reaching AdvanceDayTick and is not decorative.
	_, _, handoffDrained, pendingDrained, _ := run(1)
	if handoffDrained >= handoff {
		t.Fatalf("with a drain capacity of 1/month the live path realised %d deaths, but the unwired baseline realised %d -- the injected drain is not gating AdvanceDayTick", handoffDrained, handoff)
	}
	if pendingDrained <= 0 {
		t.Fatalf("a drain of 1/month left %d deaths pending -- expected the throttled selections to be deferred, not dropped", pendingDrained)
	}

	// (c) A drain of 0 must halt realisation entirely while the queue
	// keeps every selection.
	pop0Zero, popEndZero, handoffZero, pendingZero, _ := run(0)
	if handoffZero != 0 {
		t.Fatalf("a drain capacity of 0 still realised %d deaths", handoffZero)
	}
	if pop0Zero != popEndZero {
		t.Fatalf("a drain capacity of 0 still removed %d citizens from the population", pop0Zero-popEndZero)
	}
	if pendingZero == 0 {
		t.Fatalf("a drain capacity of 0 realised nothing AND queued nothing -- the selections were silently dropped")
	}
}

// --- Attack 11: an emigrating (otherwise-removed) queued citizen must not
// be handed to FEAT-088 as a corpse. ---
//
// The inc1.5 ghost fix force-closes the queue entry via RealiseByID on the
// removal path. That closes it in the queue's own bookkeeping but must NOT
// append to the FEAT-088 handoff: nobody buries a citizen who left town.
func TestAttackInc3_DepartedQueuedCitizenIsNotHandedOffAsACorpse(t *testing.T) {
	api, err := NewCitizensAPI(561, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	// Halt realisation so the whole selected cohort stays pending and can
	// be removed out from under the queue.
	if err := api.SetDeathDrainCapacity(DrainCapacityFunc(func(int64) int { return 0 }), "corr"); err != nil {
		t.Fatalf("SetDeathDrainCapacity: %v", err)
	}
	seedGuaranteedDeathCohort(t, api, 500_000, 200, monthJanuary)

	for d := 0; d < DaysPerMonth; d++ {
		if _, _, err := api.AdvanceDayTick("corr"); err != nil {
			t.Fatalf("AdvanceDayTick: %v", err)
		}
	}
	pending := api.deathQueue.Len("corr")
	if pending == 0 {
		t.Fatalf("test setup invalid: nothing pending to remove")
	}

	// Find a queued citizen and remove them by the ordinary departure path.
	var victim uint64
	for id := uint64(500_001); id <= 500_200; id++ {
		if _, queued := api.deathQueue.IsQueued(id, "corr"); queued {
			victim = id
			break
		}
	}
	if victim == 0 {
		t.Fatalf("test setup invalid: no queued citizen found")
	}
	popBefore := api.TotalPopulation("corr")
	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		Kind: LifeEventDeath, CitizenID: victim, CorrelationID: "corr",
	}); err != nil {
		t.Fatalf("HandleLifeEvent(departure) for the queued citizen: %v", err)
	}

	if _, queued := api.deathQueue.IsQueued(victim, "corr"); queued {
		t.Fatalf("citizen %d was removed from the population but is STILL pending in the death queue -- a stale entry occupying a budget slot forever", victim)
	}
	if got := api.TotalPopulation("corr"); got != popBefore-1 {
		t.Fatalf("population %d after removing one citizen, want %d", got, popBefore-1)
	}

	stream, err := api.DeathHandoff("corr")
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	for _, rd := range stream {
		if rd.CitizenID == victim {
			t.Fatalf("citizen %d departed by another path but was handed to FEAT-088 as a corpse -- a phantom funeral", victim)
		}
	}

	// And re-draining must never resurrect them into the handoff either.
	if err := api.SetDeathDrainCapacity(nil, "corr"); err != nil {
		t.Fatalf("SetDeathDrainCapacity(nil): %v", err)
	}
	for m := 0; m < 2; m++ {
		for d := 0; d < DaysPerMonth; d++ {
			if _, _, err := api.AdvanceDayTick("corr"); err != nil {
				t.Fatalf("AdvanceDayTick: %v", err)
			}
		}
	}
	stream2, err := api.DeathHandoff("corr")
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	seenIDs := make(map[uint64]bool, len(stream2))
	for _, rd := range stream2 {
		if rd.CitizenID == victim {
			t.Fatalf("citizen %d was resurrected into the handoff by a later drain", victim)
		}
		if seenIDs[rd.CitizenID] {
			t.Fatalf("citizen %d appears twice in the handoff after the departure reconciliation", rd.CitizenID)
		}
		seenIDs[rd.CitizenID] = true
	}
}

// --- Attack 12: SetDrainCapacity concurrent with realisation. ---
//
// A FEAT-088 consumer re-wiring its capacity while the cold pass realises
// must not race (AC-17 extended to the new surfaces).
func TestAttackInc3_ConcurrentSetDrainAndRealiseIsRaceFree(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	for id := uint64(1); id <= 5000; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			v := i % 7
			_ = q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return v }), "corr")
		}
	}()
	total := 0
	for i := 0; i < 500; i++ {
		total += len(q.RealiseDrained(cfg, false, int64(200+i), "corr"))
		_ = q.RealisedDeaths("corr")
	}
	<-done

	if got := len(q.RealisedDeaths("corr")); got != total {
		t.Fatalf("handoff has %d records but %d were released -- a concurrent capacity swap lost or duplicated a death", got, total)
	}
	if total+q.Len("corr") != 5000 {
		t.Fatalf("conservation broken under concurrency: realised=%d pending=%d, want 5000 total", total, q.Len("corr"))
	}
}
