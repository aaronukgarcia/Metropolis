package dispatch

import (
	"math"
	"sync"
	"testing"
)

func TestDispatch_AC2_PriorityQueue(t *testing.T) {
	api := New()

	// Report Severity 1 first, then Severity 3, then Severity 2
	_ = api.ReportIncident("fire", 1)    // ID 1
	_ = api.ReportIncident("medical", 3) // ID 2 (Priority)
	_ = api.ReportIncident("crime", 2)   // ID 3

	// Dispatch next should yield Severity 3 (ID 2)
	inc1, err := api.DispatchNext()
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}
	if inc1.ID != 2 || inc1.Severity != 3 {
		t.Errorf("expected Severity 3 (ID 2) to be dispatched first, got ID %d, Severity %d", inc1.ID, inc1.Severity)
	}

	// Next should be Severity 2 (ID 3)
	inc2, _ := api.DispatchNext()
	if inc2.ID != 3 || inc2.Severity != 2 {
		t.Errorf("expected Severity 2 (ID 3) next, got ID %d", inc2.ID)
	}

	// Last should be Severity 1 (ID 1)
	inc3, _ := api.DispatchNext()
	if inc3.ID != 1 || inc3.Severity != 1 {
		t.Errorf("expected Severity 1 (ID 1) last, got ID %d", inc3.ID)
	}
}

func TestDispatch_AC3_ResolutionStateFlow(t *testing.T) {
	api := New()
	_ = api.ReportIncident("fire", 2)

	// Attempting to resolve a queued incident should fail (AC-11)
	err := api.ResolveIncident(1)
	if err == nil {
		t.Error("expected error resolving non-dispatched incident")
	}

	// Dispatch it
	inc, _ := api.DispatchNext()
	if inc.Status != "Dispatched" {
		t.Errorf("expected status Dispatched, got %s", inc.Status)
	}

	// Resolve it
	err = api.ResolveIncident(1)
	if err != nil {
		t.Fatalf("unexpected error resolving: %v", err)
	}

	if inc.Status != "Resolved" {
		t.Errorf("expected status Resolved, got %s", inc.Status)
	}
}

func TestDispatch_AC5_AC6_AC7_ResponseTimes(t *testing.T) {
	api := New()

	// Base fire response: Severity 1 = 8.0, Severity 3 = 8.0 - 4.0 = 4.0
	t1, _ := api.ResponseTimeMinutes("fire", 1)
	t3, _ := api.ResponseTimeMinutes("fire", 3)

	if t1 != 8.0 {
		t.Errorf("expected Severity 1 fire response 8.0, got %f", t1)
	}
	if t3 != 4.0 {
		t.Errorf("expected Severity 3 fire response 4.0, got %f", t3)
	}

	// Base medical response: Severity 1 = 6.0
	tm1, _ := api.ResponseTimeMinutes("medical", 1)
	if tm1 != 6.0 {
		t.Errorf("expected Severity 1 medical response 6.0, got %f", tm1)
	}

	// Enable autonomy-era travel optimization (30% reduction)
	_ = api.SetAutonomyEra(true)
	tmAutonomy, _ := api.ResponseTimeMinutes("medical", 1)
	expectedTime := 6.0 * 0.7 // 4.2
	if math.Abs(tmAutonomy-expectedTime) > 1e-9 {
		t.Errorf("expected autonomy medical response %f, got %f", expectedTime, tmAutonomy)
	}
}

func TestDispatch_AC10_NoIncidentError(t *testing.T) {
	api := New()
	_, err := api.DispatchNext()
	if err == nil {
		t.Error("expected error dispatching from empty queue")
	}
	expectedCode := "MET-E_DISPATCH_01: dispatch queue is empty (AC-10)"
	if err.Error() != expectedCode {
		t.Errorf("expected error matching empty queue code, got: %v", err)
	}

	_ = api.ReportIncident("fire", 1)
	_, _ = api.DispatchNext()

	// Resolve unknown ID
	err = api.ResolveIncident(999)
	if err == nil {
		t.Error("expected error for unknown incident resolution")
	}
	expectedCode4 := "MET-E_DISPATCH_04: unknown incident ID: 999 (AC-10)"
	if err.Error() != expectedCode4 {
		t.Errorf("expected error matching unknown ID, got: %v", err)
	}
}

func TestDispatch_AC11_ValidationErrors(t *testing.T) {
	api := New()

	// Invalid category
	err := api.ReportIncident("tornado", 3)
	if err == nil {
		t.Error("expected error for invalid category")
	}

	// Invalid severity
	err = api.ReportIncident("fire", 4)
	if err == nil {
		t.Error("expected error for invalid severity")
	}
}

func TestDispatch_AC11_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.ReportIncident("fire", 2)
	_ = api1.ReportIncident("medical", 3)

	_ = api2.ReportIncident("fire", 2)
	_ = api2.ReportIncident("medical", 3)

	inc1, _ := api1.DispatchNext()
	inc2, _ := api2.DispatchNext()

	if inc1.ID != inc2.ID || inc1.Category != inc2.Category || inc1.Severity != inc2.Severity {
		t.Errorf("expected deterministic outcome, got %+v and %+v", inc1, inc2)
	}
}

func TestDispatch_AC13_Concurrency(t *testing.T) {
	api := New()

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = api.ReportIncident("fire", 1)
				_, _ = api.QueueSize()
			}
		}()
	}

	wg.Wait()
}
