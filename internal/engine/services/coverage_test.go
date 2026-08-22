package services

import (
	"math"
	"reflect"
	"sync"
	"testing"
)

// This file is the coverage/enumeration aggregate suite (AC-18…AC-25,
// docs/planning/icd/engine.services-coverage.md §11). Each test is written
// to be able to FAIL: a constant CoverageRatio fails the mutation test, an
// unsorted result fails the enumeration test, and a zero-value district
// record fails the unknown-district test.

// assertNear asserts got is within eps of want.
func assertNear(t *testing.T, got, want, eps float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("got %.15g, want %.15g (±%.0e)", got, want, eps)
	}
}

// --- AC-18: deterministic enumeration -------------------------------------

// TestEnumerationSortedAndStable registers instances in deliberately
// unsorted insertion order and asserts ServiceIDs()/ServiceKinds() return
// ascending order, stable across repeated calls (GR#21).
func TestEnumerationSortedAndStable(t *testing.T) {
	a := testAPI(t)
	// Deliberately unsorted insertion order.
	for _, id := range []ServiceID{"zeta", "alpha", "mike", "bravo"} {
		registerService(t, a, id, ServiceHealthcare, 10, 10, 0)
	}
	if err := a.RegisterKind("synthetic.zz", KindDef{Name: "Synthetic ZZ"}); err != nil {
		t.Fatalf("RegisterKind: %v", err)
	}

	ids, err := a.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	want := []ServiceID{"alpha", "bravo", "mike", "zeta"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ServiceIDs() = %v, want %v (ascending)", ids, want)
	}

	// Stable across repeated calls.
	for i := 0; i < 3; i++ {
		again, err := a.ServiceIDs()
		if err != nil {
			t.Fatalf("ServiceIDs (repeat %d): %v", i, err)
		}
		if !reflect.DeepEqual(again, want) {
			t.Fatalf("ServiceIDs() repeat %d = %v, want %v", i, again, want)
		}
	}

	kinds, err := a.ServiceKinds()
	if err != nil {
		t.Fatalf("ServiceKinds: %v", err)
	}
	// The whole slice must be ascending and the synthetic kind present.
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] >= kinds[i] {
			t.Fatalf("ServiceKinds() not ascending at %d: %v", i, kinds)
		}
	}
	found := false
	for _, k := range kinds {
		if k == "synthetic.zz" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ServiceKinds() = %v, missing synthetic.zz", kinds)
	}
}

// --- AC-19: city-wide summary formula -------------------------------------

// TestCoverageSummaryFormula is the exact AC-19 fixture: two instances with
// capacities 10 and 40 and demands 20 and 60 ⇒ TotalCapacity 50,
// TotalDemand 80, CoverageRatio 0.625, and the derived MeanQuality.
func TestCoverageSummaryFormula(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "a", ServiceHealthcare, 10, 10, 0)
	registerService(t, a, "b", ServiceHealthcare, 40, 10, 0)
	if err := a.UpdateDemand("a", 20, 1); err != nil {
		t.Fatalf("UpdateDemand(a): %v", err)
	}
	if err := a.UpdateDemand("b", 60, 1); err != nil {
		t.Fatalf("UpdateDemand(b): %v", err)
	}

	s, err := a.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if s.ServiceCount != 2 {
		t.Errorf("ServiceCount = %d, want 2", s.ServiceCount)
	}
	if s.TotalCapacity != 50 {
		t.Errorf("TotalCapacity = %v, want 50", s.TotalCapacity)
	}
	if s.TotalDemand != 80 {
		t.Errorf("TotalDemand = %v, want 80", s.TotalDemand)
	}
	assertNear(t, s.CoverageRatio, 0.625, 1e-9)
	// quality(a) = 1.0 × (10/20) × 1.0 × 1.0 = 0.5
	// quality(b) = 1.0 × (40/60) × 1.0 × 1.0 = 2/3
	// MeanQuality = (0.5 + 2/3)/2.
	assertNear(t, s.MeanQuality, (0.5+40.0/60.0)/2.0, 1e-12)
}

// --- AC-20: coverage responds to capacity/demand, not a constant ----------

// TestCoverageRespondsToMutation drives demand past capacity and asserts the
// city-wide CoverageRatio strictly falls, then upgrades capacity and asserts
// it strictly rises — a constant ratio (or a count-based approximation)
// fails both arms.
func TestCoverageRespondsToMutation(t *testing.T) {
	a := testAPI(t)
	// A service with an upgrade path so Upgrade can raise its ceiling; both
	// steps carry Milestone 0, so no tier gate is consulted.
	if err := a.RegisterService(ServiceSpec{
		ID:          "svc",
		Kind:        ServiceHealthcare,
		CapacityRaw: "fixture",
		UpgradePath: []UpgradeStep{
			{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100},
			{BuildingID: "hospital", Name: "Hospital", CapacityCeiling: 200},
		},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	mustSummary := func() CoverageSummary {
		s, err := a.CoverageSummary()
		if err != nil {
			t.Fatalf("CoverageSummary: %v", err)
		}
		return s
	}

	if err := a.UpdateDemand("svc", 50, 0); err != nil {
		t.Fatalf("UpdateDemand(50): %v", err)
	}
	under := mustSummary().CoverageRatio // 100/50 → clamped to 1.0

	if err := a.UpdateDemand("svc", 200, 0); err != nil {
		t.Fatalf("UpdateDemand(200): %v", err)
	}
	over := mustSummary().CoverageRatio // 100/200 = 0.5
	if !(over < under) {
		t.Fatalf("CoverageRatio after raising demand past capacity = %v, want < %v", over, under)
	}

	if err := a.Upgrade("svc"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	upgraded := mustSummary().CoverageRatio // 200/200 = 1.0
	if !(upgraded > over) {
		t.Fatalf("CoverageRatio after upgrade = %v, want > %v", upgraded, over)
	}

	// The ratio is the exact Σcapacity/Σdemand figure, not a
	// count(capacity≥demand)/count approximation: at demand 200 / capacity 100
	// the exact figure is 0.5 (a count-based ratio would be 0/1 = 0).
	if over != 0.5 {
		t.Fatalf("CoverageRatio = %v, want exactly 0.5 (Σcapacity/Σdemand)", over)
	}
}

// --- AC-21: per-district coverage (push-input, no spatial read) -----------

// TestDistrictCoverageIsolationAndOrdering pushes demand for two districts
// and asserts each district's ratio is computed from only its own records
// (mutating one leaves the other unchanged), and the result is ordered by
// DistrictID.
func TestDistrictCoverageIsolationAndOrdering(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "svc", ServiceHealthcare, 100, 10, 0)

	// Deliberately unsorted insertion order: "beta" before "alpha".
	if err := a.UpdateDistrictDemand("beta", "svc", 50, 0); err != nil {
		t.Fatalf("UpdateDistrictDemand(beta): %v", err)
	}
	if err := a.UpdateDistrictDemand("alpha", "svc", 200, 0); err != nil {
		t.Fatalf("UpdateDistrictDemand(alpha): %v", err)
	}

	dIDs, err := a.DistrictIDs()
	if err != nil {
		t.Fatalf("DistrictIDs: %v", err)
	}
	if !reflect.DeepEqual(dIDs, []DistrictID{"alpha", "beta"}) {
		t.Fatalf("DistrictIDs() = %v, want [alpha beta] (ascending)", dIDs)
	}

	betaBefore, err := a.CoverageForDistrict("beta")
	if err != nil {
		t.Fatalf("CoverageForDistrict(beta): %v", err)
	}
	if betaBefore.CoverageRatio != 1.0 { // 100/50 clamped to 1.0
		t.Fatalf("beta CoverageRatio = %v, want 1.0", betaBefore.CoverageRatio)
	}

	// Mutate alpha's demand — beta's coverage must not move.
	if err := a.UpdateDistrictDemand("alpha", "svc", 400, 0); err != nil {
		t.Fatalf("UpdateDistrictDemand(alpha, 400): %v", err)
	}
	betaAfter, err := a.CoverageForDistrict("beta")
	if err != nil {
		t.Fatalf("CoverageForDistrict(beta) after alpha mutation: %v", err)
	}
	if betaAfter.CoverageRatio != betaBefore.CoverageRatio {
		t.Fatalf("beta ratio changed after alpha mutation: %v → %v", betaBefore.CoverageRatio, betaAfter.CoverageRatio)
	}

	// The whole slice is ordered by DistrictID.
	all, err := a.CoverageByDistrict()
	if err != nil {
		t.Fatalf("CoverageByDistrict: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(CoverageByDistrict()) = %d, want 2", len(all))
	}
	if all[0].District != "alpha" || all[1].District != "beta" {
		t.Fatalf("CoverageByDistrict() order = [%v %v], want [alpha beta]", all[0].District, all[1].District)
	}
	if alpha := all[0]; alpha.CoverageRatio != 0.25 { // 100/400
		t.Fatalf("alpha CoverageRatio = %v, want 0.25", alpha.CoverageRatio)
	}
}

// --- AC-22: the aggregate is query-only -----------------------------------

// TestAggregateReadOnlyNoMutation snapshots the aggregate, calls every
// accessor repeatedly, and asserts byte-identical results with no change to
// any instance's FundingLevel/Demand afterward.
func TestAggregateReadOnlyNoMutation(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "svc", ServiceHealthcare, 100, 10, 0)
	if err := a.UpdateDemand("svc", 50, 1); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}
	if err := a.UpdateDistrictDemand("north", "svc", 25, 1); err != nil {
		t.Fatalf("UpdateDistrictDemand: %v", err)
	}

	fundingBefore, err := a.FundingLevel("svc")
	if err != nil {
		t.Fatalf("FundingLevel: %v", err)
	}
	demandBefore, err := a.Demand("svc")
	if err != nil {
		t.Fatalf("Demand: %v", err)
	}

	sumBefore, err := a.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	idsBefore, err := a.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	distBefore, err := a.CoverageByDistrict()
	if err != nil {
		t.Fatalf("CoverageByDistrict: %v", err)
	}

	for i := 0; i < 3; i++ {
		if s, err := a.CoverageSummary(); err != nil || !reflect.DeepEqual(s, sumBefore) {
			t.Fatalf("CoverageSummary repeat %d changed: %+v vs %+v (err %v)", i, s, sumBefore, err)
		}
		if ids, err := a.ServiceIDs(); err != nil || !reflect.DeepEqual(ids, idsBefore) {
			t.Fatalf("ServiceIDs repeat %d changed: %v vs %v (err %v)", i, ids, idsBefore, err)
		}
		if d, err := a.CoverageByDistrict(); err != nil || !reflect.DeepEqual(d, distBefore) {
			t.Fatalf("CoverageByDistrict repeat %d changed: %+v vs %+v (err %v)", i, d, distBefore, err)
		}
	}

	fundingAfter, err := a.FundingLevel("svc")
	if err != nil {
		t.Fatalf("FundingLevel (after): %v", err)
	}
	demandAfter, err := a.Demand("svc")
	if err != nil {
		t.Fatalf("Demand (after): %v", err)
	}
	if fundingAfter != fundingBefore || demandAfter != demandBefore {
		t.Fatalf("aggregate mutated state: funding %v→%v, demand %v→%v", fundingBefore, fundingAfter, demandBefore, demandAfter)
	}
}

// --- AC-23: unknown district + copy guard --------------------------------

// TestUnknownDistrictRejected asserts a never-pushed district returns the
// new registry code, no zero-value record is created, an empty district id
// is rejected on push, and every aggregate accessor rejects a struct-copied
// *ServicesAPI with ErrCopiedValue.
func TestUnknownDistrictRejected(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "svc", ServiceHealthcare, 100, 10, 0)

	if _, err := a.CoverageForDistrict("never-pushed"); err == nil {
		t.Fatal("CoverageForDistrict(never-pushed) returned nil, want ErrUnknownDistrict")
	} else {
		assertCode(t, err, ErrUnknownDistrict)
	}

	// No zero-value district record was created by the rejected query.
	dIDs, err := a.DistrictIDs()
	if err != nil {
		t.Fatalf("DistrictIDs: %v", err)
	}
	if len(dIDs) != 0 {
		t.Fatalf("DistrictIDs() = %v after a rejected query, want empty (no zero-value record)", dIDs)
	}
	all, err := a.CoverageByDistrict()
	if err != nil {
		t.Fatalf("CoverageByDistrict: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("CoverageByDistrict() = %+v after a rejected query, want empty", all)
	}

	// Pushing to an empty district id is the "never seen" push case.
	if err := a.UpdateDistrictDemand("", "svc", 10, 0); err == nil {
		t.Fatal("UpdateDistrictDemand(\"\") returned nil, want ErrUnknownDistrict")
	} else {
		assertCode(t, err, ErrUnknownDistrict)
	}

	// Struct-copied aggregate accessors return ErrCopiedValue (AC-23).
	copied := servicesCopy(a)
	if _, err := copied.ServiceIDs(); err == nil {
		t.Fatal("ServiceIDs on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
	if _, err := copied.CoverageSummary(); err == nil {
		t.Fatal("CoverageSummary on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
	if _, err := copied.CoverageByDistrict(); err == nil {
		t.Fatal("CoverageByDistrict on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
	if err := copied.UpdateDistrictDemand("x", "svc", 1, 0); err == nil {
		t.Fatal("UpdateDistrictDemand on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
}

// --- AC-24: determinism across constructions ------------------------------

// TestCoverageDeterministicAcrossConstructions builds two identical
// ServicesAPIs (same registrations, same demand pushes) and asserts
// byte-identical CoverageSummary and CoverageByDistrict — coverage is a pure
// function of registration + pushed-demand state, never wall clock or map
// order.
func TestCoverageDeterministicAcrossConstructions(t *testing.T) {
	build := func() *ServicesAPI {
		a := testAPI(t)
		registerService(t, a, "b", ServiceHealthcare, 40, 10, 0)
		registerService(t, a, "a", ServiceHealthcare, 10, 10, 0)
		if err := a.UpdateDemand("a", 20, 1); err != nil {
			t.Fatalf("UpdateDemand(a): %v", err)
		}
		if err := a.UpdateDemand("b", 60, 1); err != nil {
			t.Fatalf("UpdateDemand(b): %v", err)
		}
		if err := a.UpdateDistrictDemand("south", "a", 30, 1); err != nil {
			t.Fatalf("UpdateDistrictDemand(south): %v", err)
		}
		if err := a.UpdateDistrictDemand("north", "b", 40, 1); err != nil {
			t.Fatalf("UpdateDistrictDemand(north): %v", err)
		}
		return a
	}

	a1, a2 := build(), build()
	s1, err := a1.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (1): %v", err)
	}
	s2, err := a2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (2): %v", err)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("CoverageSummary diverged across constructions:\n%+v\nvs\n%+v", s1, s2)
	}
	d1, err := a1.CoverageByDistrict()
	if err != nil {
		t.Fatalf("CoverageByDistrict (1): %v", err)
	}
	d2, err := a2.CoverageByDistrict()
	if err != nil {
		t.Fatalf("CoverageByDistrict (2): %v", err)
	}
	if !reflect.DeepEqual(d1, d2) {
		t.Fatalf("CoverageByDistrict diverged across constructions:\n%+v\nvs\n%+v", d1, d2)
	}
}

// --- race: concurrent aggregate reads + district pushes ------------------

// TestConcurrentAggregateReadsAndDistrictPushesRaceFree hammers the
// aggregate accessors and UpdateDistrictDemand concurrently against one
// *ServicesAPI; -race verifies no data race (all accessors read under
// RLock, pushes under Lock).
func TestConcurrentAggregateReadsAndDistrictPushesRaceFree(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "svc", ServiceHealthcare, 100, 10, 0)
	districts := []DistrictID{"north", "south", "east"}

	var wg sync.WaitGroup
	errCh := make(chan error, 256)
	for i := 0; i < 32; i++ {
		n := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.UpdateDistrictDemand(districts[n%3], "svc", float64(n)+1, 0); err != nil {
				errCh <- err
			}
			if _, err := a.CoverageSummary(); err != nil {
				errCh <- err
			}
			if _, err := a.CoverageByDistrict(); err != nil {
				errCh <- err
			}
			if _, err := a.DistrictIDs(); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}
}
