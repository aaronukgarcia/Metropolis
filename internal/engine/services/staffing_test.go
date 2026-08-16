package services

import (
	"testing"
)

// --- AC-4: shared staffing pools (§26) -----------------------------------

// TestSharedStaffingPoolShortageDegradesMultipleServices (AC-4): two distinct
// services drawing from the same "nursing" pool (healthcare + elder-care,
// per §26's explicit example) both see their quality degrade when the
// shared pool is short, in the same tick.
func TestSharedStaffingPoolShortageDegradesMultipleServices(t *testing.T) {
	a := testLoadedAPI(t) // pools come from data/services.json, not hardcoded

	// Both services draw from the "nursing" pool (declared in
	// data/services.json staffingPools[].members), each needing 10 staff.
	registerService(t, a, "hospital-1", ServiceHealthcare, 100, 10, 10)
	registerService(t, a, "elder-1", ServiceElderCare, 100, 10, 10)

	// Fully staffed: both services are at full quality.
	if err := a.SetPoolStaff("nursing", 20); err != nil {
		t.Fatalf("SetPoolStaff: %v", err)
	}
	if _, err := a.AllocateStaffing("nursing"); err != nil {
		t.Fatalf("AllocateStaffing: %v", err)
	}
	fullH, err := a.Quality("hospital-1")
	if err != nil {
		t.Fatalf("Quality(hospital-1): %v", err)
	}
	fullE, err := a.Quality("elder-1")
	if err != nil {
		t.Fatalf("Quality(elder-1): %v", err)
	}
	if fullH != 1 || fullE != 1 {
		t.Fatalf("full-staff quality = (%v, %v), want (1, 1)", fullH, fullE)
	}

	// The shared pool is short (only 10 staff for a combined need of 20):
	// BOTH services degrade simultaneously.
	if err := a.SetPoolStaff("nursing", 10); err != nil {
		t.Fatalf("SetPoolStaff(short): %v", err)
	}
	if _, err := a.AllocateStaffing("nursing"); err != nil {
		t.Fatalf("AllocateStaffing(short): %v", err)
	}
	shortH, err := a.Quality("hospital-1")
	if err != nil {
		t.Fatalf("Quality(hospital-1) short: %v", err)
	}
	shortE, err := a.Quality("elder-1")
	if err != nil {
		t.Fatalf("Quality(elder-1) short: %v", err)
	}
	if shortH >= fullH {
		t.Errorf("hospital quality under shared shortage = %v, want < %v", shortH, fullH)
	}
	if shortE >= fullE {
		t.Errorf("elder-care quality under shared shortage = %v, want < %v", shortE, fullE)
	}
}

// TestSharedStaffingPoolAllocationReportsShortfall verifies the allocation outcome
// itself: with 10 staff across needs of 10 and 10, the pool is at half
// strength, so each member receives half its need (a uniform proportional
// shortfall — every member degrades, not a first-served full allocation).
func TestSharedStaffingPoolAllocationReportsShortfall(t *testing.T) {
	a := testLoadedAPI(t)
	registerService(t, a, "a-hospital", ServiceHealthcare, 100, 10, 10)
	registerService(t, a, "b-elder", ServiceElderCare, 100, 10, 10)

	if err := a.SetPoolStaff("nursing", 10); err != nil {
		t.Fatalf("SetPoolStaff: %v", err)
	}
	alloc, err := a.AllocateStaffing("nursing")
	if err != nil {
		t.Fatalf("AllocateStaffing: %v", err)
	}
	if len(alloc) != 2 {
		t.Fatalf("AllocateStaffing returned %d allocations, want 2", len(alloc))
	}
	// Ascending ServiceID: a-hospital first, b-elder second.
	if alloc[0].ServiceID != "a-hospital" || alloc[1].ServiceID != "b-elder" {
		t.Fatalf("allocation order = %v, %v; want ascending ServiceID", alloc[0].ServiceID, alloc[1].ServiceID)
	}
	// Uniform 50% shortfall: each of the two 10-need members gets 5.
	for _, al := range alloc {
		if al.Allocated != 5 || al.Shortfall != 5 {
			t.Errorf("allocation = %+v, want allocated 5, shortfall 5 (uniform proportional shortfall)", al)
		}
	}
}

// --- AC-13: deterministic allocation (GR#21) -----------------------------

// TestAllocationDeterministicAcrossRuns runs the SAME short allocation 60
// times and asserts byte-identical outcomes every run — Go map iteration
// order is randomised per range, so a map-iteration-order allocation would
// produce different per-service splits across runs (AC-13).
func TestAllocationDeterministicAcrossRuns(t *testing.T) {
	a := testLoadedAPI(t)
	// Many members so an unsorted map range would visibly scramble the
	// allocation order with high probability.
	registerService(t, a, "svc-a", ServiceHealthcare, 100, 10, 5)
	registerService(t, a, "svc-b", ServiceHealthcare, 100, 10, 5)
	registerService(t, a, "svc-c", ServiceHealthcare, 100, 10, 5)
	registerService(t, a, "svc-d", ServiceHealthcare, 100, 10, 5)
	if err := a.SetPoolStaff("nursing", 7); err != nil {
		t.Fatalf("SetPoolStaff: %v", err)
	}

	first, err := a.AllocateStaffing("nursing")
	if err != nil {
		t.Fatalf("AllocateStaffing: %v", err)
	}
	for i := 0; i < 60; i++ {
		got, err := a.AllocateStaffing("nursing")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if len(got) != len(first) {
			t.Fatalf("run %d: %d allocations, want %d", i, len(got), len(first))
		}
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("run %d: allocation[%d] = %+v, want %+v (non-deterministic — GR#21)", i, j, got[j], first[j])
			}
		}
	}
}

// TestUnknownPoolRejected: an undeclared pool id is a registry error, never
// a silently-returned empty allocation (AC-4's structural half).
func TestUnknownPoolRejected(t *testing.T) {
	a := testLoadedAPI(t)
	if err := a.SetPoolStaff("no-such-pool", 10); err == nil {
		t.Fatal("SetPoolStaff(unknown) returned nil, want ErrUnknownStaffingPool")
	} else {
		assertCode(t, err, ErrUnknownStaffingPool)
	}
	if _, err := a.AllocateStaffing("no-such-pool"); err == nil {
		t.Fatal("AllocateStaffing(unknown) returned nil, want ErrUnknownStaffingPool")
	} else {
		assertCode(t, err, ErrUnknownStaffingPool)
	}
}
