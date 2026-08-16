package worklife

import (
	"bytes"
	"testing"
)

// AC-16 (GR#21, core): at-work state, commute-demand distribution,
// coverage, and hours/wage/wellbeing derivation are a deterministic
// function of (tick, pattern, policy state, seed) — two identical runs over
// an identical snapshot and command/policy sequence produce byte-identical
// results. (worklife has no internal parallelism — no POOL-SIM workers —
// so worker-count invariance is structural: every query is a pure function.)
func TestDeterminism_ByteIdenticalAcrossRuns(t *testing.T) {
	f := loadTestFile(t)
	policy := policyDef(t, f, "996")

	run := func() (atWork, commute []byte, hours, wage int64, bal float64) {
		api := newTestAPI(t, f)
		p := &fakePolicies{}
		wb := &fakeWellbeing{}
		if err := api.SetPolicies(p); err != nil {
			t.Fatalf("SetPolicies: %v", err)
		}
		if err := api.SetWellbeing(wb); err != nil {
			t.Fatalf("SetWellbeing: %v", err)
		}
		p.set(effect(policy), true)

		var workers []Worker
		kinds := []PatternKind{PatternCoreHours, PatternShift, PatternAnyTime}
		for i := 0; i < 20; i++ {
			workers = append(workers, Worker{ID: uint64(i), Pattern: kinds[i%len(kinds)]})
		}

		for _, wkr := range workers {
			for tick := int64(0); tick < 168; tick++ {
				at, err := api.AtWork(wkr, tick)
				if err != nil {
					t.Fatalf("AtWork: %v", err)
				}
				if at {
					atWork = append(atWork, 1)
				} else {
					atWork = append(atWork, 0)
				}
			}
		}

		demand, err := api.CommuteDemandByHour(workers, 0, 168)
		if err != nil {
			t.Fatalf("CommuteDemandByHour: %v", err)
		}
		for _, d := range demand {
			commute = append(commute, byte(d.Hour), byte(d.Arrivals), byte(d.Departures))
		}

		hours, _ = api.WorkingHours(PatternCoreHours, "corr")
		wage, _ = api.WeeklyWage(1_000_000, "corr")
		bal, _ = api.OverworkWellbeingInput(Worker{ID: 99, Pattern: PatternCoreHours}, "corr")
		return atWork, commute, hours, wage, bal
	}

	aAtWork, aCommute, aHours, aWage, aBal := run()
	bAtWork, bCommute, bHours, bWage, bBal := run()

	if !bytes.Equal(aAtWork, bAtWork) {
		t.Fatal("at-work profile differs across identical runs")
	}
	if !bytes.Equal(aCommute, bCommute) {
		t.Fatal("commute-demand distribution differs across identical runs")
	}
	if aHours != bHours || aWage != bWage || aBal != bBal {
		t.Fatalf("hours/wage/balance differ across identical runs: (%d,%d,%v) vs (%d,%d,%v)",
			aHours, aWage, aBal, bHours, bWage, bBal)
	}
}

// AC-5 (deterministic rotation assignment): a shift worker's rotation is a
// pure function of (workerID, weekIndex) — the same worker in the same week
// always draws the same rotation, and the draw never depends on map order.
func TestRotationAssignment_Deterministic(t *testing.T) {
	f := loadTestFile(t)
	shift := patternDef(t, f, PatternShift)
	api := newTestAPI(t, f)

	for _, id := range []uint64{1, 2, 3, 1000, 1001} {
		first := api.rotation(id, 0, shift.Rotations)
		for trial := 0; trial < 10; trial++ {
			if got := api.rotation(id, 0, shift.Rotations); got != first {
				t.Fatalf("worker %d week 0: rotation %v then %v (not deterministic)", id, first, got)
			}
		}
	}
}
