package worklife

import (
	"sync"
	"testing"
)

// AC-20 (race): the schedule/commute query surface is read concurrently
// with a tick advancing and a policy toggle, with no data race under -race.
func TestConcurrentQueryWhileTickAndToggle(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	p := &fakePolicies{}
	wb := &fakeWellbeing{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}
	if err := api.SetWellbeing(wb); err != nil {
		t.Fatalf("SetWellbeing: %v", err)
	}

	var wg sync.WaitGroup

	// One goroutine toggles the policy between 996 and the default week.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if i%2 == 0 {
				p.set(effect(policy), true)
			} else {
				p.set(WorkingWeekEffect{}, false)
			}
		}
	}()

	// Several readers query at-work, working-hours, wage, and wellbeing
	// while the tick advances.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_, _ = api.AtWork(Worker{ID: uint64(r), Pattern: PatternCoreHours}, int64(i%168))
				_, _ = api.WorkingHours(PatternShift, "corr")
				_, _ = api.WeeklyWage(500, "corr")
				_, _ = api.OverworkWellbeingInput(Worker{ID: uint64(r), Pattern: PatternCoreHours}, "corr")
				_, _ = api.CommuteDemandByHour([]Worker{{ID: uint64(r), Pattern: PatternCoreHours}}, 0, 24)
			}
		}(r)
	}

	wg.Wait()
}
