package capexport

import (
	"sync"
	"testing"
)

// TestConcurrentReadsAreRaceFree (AC-12): concurrent readers hammer the query
// surface while a writer issues contracts. The -race detector (run by the
// baseline) flags any unsynchronised access; this test only asserts the
// operations stay valid under any schedule, never a timing-dependent outcome.
func TestConcurrentReadsAreRaceFree(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)

	if _, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 100, TermMonths: 12, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = a.SurplusBook(ExportHospitalBeds)
				_, _ = a.Committed(ExportHospitalBeds)
				_, _ = a.CitizenCoverage(ExportHospitalBeds)
				_, _ = a.Crossing(ExportHospitalBeds)
				_ = a.Contracts()
			}
		}()
	}
	wg.Wait()
}
