package education

import (
	"sync"
	"testing"
)

// AC-16: EducationAPI is safe for concurrent use — concurrent enrolment and
// concurrent queries across stages/shards race cleanly under -race.
func TestConcurrentEnrolAndQuery(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 100)
	const workers = 4
	const perWorker = 16
	for id := uint64(1); id <= workers*perWorker; id++ {
		seedCitizen(t, c, id, 8, 0)
	}
	advanceCitizens(t, c, 8)

	// Concurrent enrolment writes.
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base uint64) {
			defer wg.Done()
			for i := uint64(0); i < perWorker; i++ {
				_ = a.Enrol(base*perWorker+i+1, 8)
			}
		}(uint64(w))
	}
	wg.Wait()

	if got, err := a.Enrolment(StageNursery); err != nil || got != workers*perWorker {
		t.Fatalf("enrolled = %d (err %v), want %d", got, err, workers*perWorker)
	}

	// Concurrent reads racing against a concurrent advance.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = a.Enrolment(StageNursery)
				_, _ = a.Enrolment(StagePrimary)
				_ = a.StageLedger(StageNursery)
				_, _ = a.Pupil(1)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = a.AdvanceIntake(20)
		}
	}()
	wg.Wait()
}
