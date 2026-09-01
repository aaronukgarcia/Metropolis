package citizens

import "testing"

// FEAT-087 (mkey feat.deathwave) inc3 acceptance tests: the FEAT-088
// handoff surface (AC-9/AC-10) and the injected drain capacity (AC-11,
// ASM-580's min(budget, drain, queued)). AC-1..8/AC-12..19 are inc1/inc1.5/
// inc2 and are covered in deathwave_test.go/weatheremergency_test.go.

// --- AC-9: the ordered handoff surface ---

// TestRealiseDrained_HandoffFIFOOrderAndFields (AC-9, the load-bearing
// handoff check). Realising a mixed month (some via the emergency path,
// some not, across two calls) must enumerate on RealisedDeaths in the
// AC-4 FIFO order (selectionMonth then citizenID), each record carrying
// the correct citizenId/deathMonth/emergencyFlag.
func TestRealiseDrained_HandoffFIFOOrderAndFields(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 3, 0)

	// Two selection months, several citizens, deliberately enqueued
	// out-of-FIFO-order so a passing test cannot be an accident of
	// insertion order (AC-4's determinism guarantee under test here too).
	mustEnqueue(t, q, 30, 100, "corr")
	mustEnqueue(t, q, 10, 100, "corr")
	mustEnqueue(t, q, 20, 100, "corr")

	// Non-emergency release: budget=3 releases exactly the 3 pending,
	// FIFO by (selectionMonth, citizenID) => 10, 20, 30 (month tie, id
	// ascending).
	got := q.RealiseDrained(cfg, false, 200, "corr")
	want := []RealisedDeath{
		{CitizenID: 10, DeathMonth: 200, EmergencyFlag: false},
		{CitizenID: 20, DeathMonth: 200, EmergencyFlag: false},
		{CitizenID: 30, DeathMonth: 200, EmergencyFlag: false},
	}
	assertRealisedDeathsEqual(t, "RealiseDrained return value (non-emergency)", got, want)

	// A second, emergency month realising a fresh entry: the handoff
	// stream must APPEND (not replace), and this record's flag must be
	// true while the earlier three stay false.
	mustEnqueue(t, q, 40, 300, "corr")
	got2 := q.RealiseDrained(cfg, true, 300, "corr")
	want2 := []RealisedDeath{{CitizenID: 40, DeathMonth: 300, EmergencyFlag: true}}
	assertRealisedDeathsEqual(t, "RealiseDrained return value (emergency)", got2, want2)

	full := q.RealisedDeaths("corr")
	wantFull := append(append([]RealisedDeath{}, want...), want2...)
	assertRealisedDeathsEqual(t, "RealisedDeaths() full stream", full, wantFull)
}

// --- AC-10: the emergency flag rides the handoff ---

// TestRealiseDrained_EmergencyFlagDiffersCorrectly (AC-10). Realising one
// month WITHOUT a declared emergency and a second month WITH one must tag
// the two records' EmergencyFlag differently, correctly.
func TestRealiseDrained_EmergencyFlagDiffersCorrectly(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 10, 0)

	mustEnqueue(t, q, 1, 50, "corr")
	nonEmergency := q.RealiseDrained(cfg, false, 60, "corr")
	if len(nonEmergency) != 1 || nonEmergency[0].EmergencyFlag {
		t.Fatalf("non-emergency release = %+v, want exactly one record with EmergencyFlag=false", nonEmergency)
	}

	mustEnqueue(t, q, 2, 50, "corr")
	emergency := q.RealiseDrained(cfg, true, 70, "corr")
	if len(emergency) != 1 || !emergency[0].EmergencyFlag {
		t.Fatalf("emergency release = %+v, want exactly one record with EmergencyFlag=true", emergency)
	}
}

// --- AC-11: min(budget, drain, queued) — the two independent knobs ---

// TestRealiseDrained_DefaultUnlimitedDrainMatchesEmergencyRealise (AC-11's
// backward-compatibility guarantee: a DeathQueue with no injected drain —
// the state of every world today, no FEAT-088 consumer wired — must
// release the IDENTICAL set/order EmergencyRealise would for the same
// inputs, proving inc3's wiring into registry.go is a behavioural no-op
// until a consumer calls SetDrainCapacity.)
func TestRealiseDrained_DefaultUnlimitedDrainMatchesEmergencyRealise(t *testing.T) {
	cfg := mkFixedBudgetCfg(t, 5, 0)

	mk := func() *DeathQueue {
		q := NewDeathQueue()
		for id := uint64(1); id <= 20; id++ {
			mustEnqueue(t, q, id, 100, "corr")
		}
		return q
	}

	qRef := mk()
	refIDs := EmergencyRealise(qRef, cfg, false, 200, "corr")

	qNew := mk()
	got := qNew.RealiseDrained(cfg, false, 200, "corr")
	if len(got) != len(refIDs) {
		t.Fatalf("RealiseDrained released %d, EmergencyRealise released %d, want equal (default drain must be unlimited)", len(got), len(refIDs))
	}
	for i, rd := range got {
		if rd.CitizenID != refIDs[i] {
			t.Fatalf("RealiseDrained[%d].CitizenID = %d, EmergencyRealise[%d] = %d, want identical release order", i, rd.CitizenID, i, refIDs[i])
		}
	}
}

// TestRealiseDrained_VaryDrainBudgetFixed (AC-11/ASM-580, first half): with
// the data-file budget held fixed (10) and the injected drain varied, the
// realised count each month must be min(budget, drain, queued) — proving
// drain, not budget, binds when it is the smaller of the two.
func TestRealiseDrained_VaryDrainBudgetFixed(t *testing.T) {
	cfg := mkFixedBudgetCfg(t, 10, 0)

	cases := []struct {
		drain int
		queue int
		want  int
	}{
		{drain: 3, queue: 50, want: 3},   // drain binds (drain < budget < queue)
		{drain: 25, queue: 50, want: 10}, // budget binds (budget < drain < queue)
		{drain: 100, queue: 4, want: 4},  // queue binds (queue < budget < drain)
	}
	for _, c := range cases {
		q := NewDeathQueue()
		for id := uint64(1); id <= uint64(c.queue); id++ {
			mustEnqueue(t, q, id, 100, "corr")
		}
		if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return c.drain }), "corr"); err != nil {
			t.Fatalf("SetDrainCapacity: %v", err)
		}
		got := q.RealiseDrained(cfg, false, 200, "corr")
		if len(got) != c.want {
			t.Fatalf("drain=%d queue=%d budget=10: realised %d, want min(budget,drain,queued)=%d", c.drain, c.queue, len(got), c.want)
		}
	}
}

// TestRealiseDrained_VaryBudgetDrainFixed (AC-11/ASM-580, second half): with
// the injected drain held fixed (8) and the data-file budget varied via
// MonthlyEmergencyBudget-style fixtures, realised must again equal
// min(budget, drain, queued) — proving budget, not drain, binds when it is
// the smaller of the two, and that the two knobs are genuinely independent
// (neither test in this file derives one from the other).
func TestRealiseDrained_VaryBudgetDrainFixed(t *testing.T) {
	const fixedDrain = 8
	const queue = 50

	cases := []struct {
		budget int
		want   int
	}{
		{budget: 3, want: 3},  // budget binds
		{budget: 20, want: 8}, // drain binds
	}
	for _, c := range cases {
		cfg := mkFixedBudgetCfg(t, c.budget, 0)
		q := NewDeathQueue()
		for id := uint64(1); id <= uint64(queue); id++ {
			mustEnqueue(t, q, id, 100, "corr")
		}
		if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return fixedDrain }), "corr"); err != nil {
			t.Fatalf("SetDrainCapacity: %v", err)
		}
		got := q.RealiseDrained(cfg, false, 200, "corr")
		if len(got) != c.want {
			t.Fatalf("budget=%d drain=%d: realised %d, want min(budget,drain,queued)=%d", c.budget, fixedDrain, len(got), c.want)
		}
	}

	// grep -nE "[0-9]{2,}" internal/engine/citizens/*.go (excluding
	// _test.go) must find no bare funeral/hearse-rate literal — this test
	// exercises the injected DrainCapacity interface, never a package
	// constant, so that grep stays clean by construction.
}

// TestRealiseDrained_NilDrainAfterUnsetIsUnlimitedAgain: SetDrainCapacity(nil)
// restores the unlimited default — a consumer that unwires itself must not
// leave the queue permanently bounded by its last-reported capacity.
func TestRealiseDrained_NilDrainAfterUnsetIsUnlimitedAgain(t *testing.T) {
	cfg := mkFixedBudgetCfg(t, 50, 0)
	q := NewDeathQueue()
	for id := uint64(1); id <= 20; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return 2 }), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	if got := q.RealiseDrained(cfg, false, 200, "corr"); len(got) != 2 {
		t.Fatalf("with drain=2: realised %d, want 2", len(got))
	}
	if err := q.SetDrainCapacity(nil, "corr"); err != nil {
		t.Fatalf("SetDrainCapacity(nil): %v", err)
	}
	if got := q.RealiseDrained(cfg, false, 200, "corr"); len(got) != 18 {
		t.Fatalf("after SetDrainCapacity(nil): realised %d, want the remaining 18 (unlimited again, budget=50)", len(got))
	}
}

// --- helpers ---

// mkFixedBudgetCfg builds a MortalityConfig with a fixed monthlyDeathBudget
// and monthlyEmergencyBudget, otherwise using the on-disk defaults'
// (already-validated) weather-emergency thresholds, via the same
// writeMortalityConfig/LoadMortalityConfig fixture path
// weatheremergency_test.go's own tests use.
func mkFixedBudgetCfg(t *testing.T, monthlyBudget, emergencyBudget int) MortalityConfig {
	t.Helper()
	dir := t.TempDir()
	writeMortalityConfig(t, dir, mortalityConfigFixture{
		monthlyDeathBudget:          float64(monthlyBudget),
		monthlyEmergencyBudget:      float64(emergencyBudget),
		winterHealthWaveThreshold:   0.04,
		droughtWaterDemandThreshold: 1.2,
	})
	cfg, err := LoadMortalityConfig(dir, "corr")
	if err != nil {
		t.Fatalf("LoadMortalityConfig: %v", err)
	}
	return cfg
}

func mustEnqueue(t *testing.T, q *DeathQueue, citizenID uint64, selectionMonth int64, correlationID string) {
	t.Helper()
	if err := q.Enqueue(citizenID, selectionMonth, correlationID); err != nil {
		t.Fatalf("Enqueue(%d, %d): %v", citizenID, selectionMonth, err)
	}
}

func assertRealisedDeathsEqual(t *testing.T, label string, got, want []RealisedDeath) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len=%d, want %d (got=%+v want=%+v)", label, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %+v, want %+v", label, i, got[i], want[i])
		}
	}
}
