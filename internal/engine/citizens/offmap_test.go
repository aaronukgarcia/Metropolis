package citizens

import "testing"

// Tests for FEAT-198 (docs/planning/icd/engine.citizens-offmap.md): the
// EmploymentOffMap=5 enum extension that lets a citizen's own record
// distinguish "has a job, off-map" from "has a job, locally" so
// engine.extcommute can un-skip-gate its AC-6/AC-7 dormitory-arithmetic
// identity. This file covers ONLY the citizens-side extension (§4/§8/§11 of
// the ICD); the extcommute-side wiring is a separate build item.

// TestEmploymentOffMapRoundTrip (ICD §11 "enum domain / round-trip"): the
// command path accepts EmploymentOffMap, persists it losslessly through the
// cold store's packEmployment/unpackEmployment nibble split, and a
// transition back to Unemployed is equally readable — proving the round
// trip Unemployed -> OffMap -> Unemployed via ApplyLifeEventCommand alone
// (the same path extcommute.Assign/Release will call, ICD §4).
func TestEmploymentOffMapRoundTrip(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seed := mkRecord(1, 0)
	seed.EmploymentState = EmploymentUnemployed
	seed.Sector = SectorNone
	if err := api.SeedColdRecords([]ColdRecord{seed}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	// Unemployed -> OffMap (mirrors extcommute.Assign's write, ICD §4).
	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1,
		Employment: EmploymentOffMap, Sector: SectorNone,
	}); err != nil {
		t.Fatalf("Unemployed -> OffMap: %v", err)
	}
	got, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("citizen 1 not found after OffMap transition")
	}
	if got.Employment.State != EmploymentOffMap {
		t.Fatalf("state after assign = %v, want EmploymentOffMap", got.Employment.State)
	}
	if got.Employment.Sector != SectorNone {
		t.Fatalf("sector after assign = %v, want SectorNone", got.Employment.Sector)
	}

	// OffMap -> Unemployed (mirrors extcommute.Release's write, ICD §4).
	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1,
		Employment: EmploymentUnemployed, Sector: SectorNone,
	}); err != nil {
		t.Fatalf("OffMap -> Unemployed: %v", err)
	}
	got2, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("citizen 1 not found after release transition")
	}
	if got2.Employment.State != EmploymentUnemployed {
		t.Fatalf("state after release = %v, want EmploymentUnemployed", got2.Employment.State)
	}
}

// TestValidateEmploymentStateWidenedDomain (ICD §11, MET-G007 boundary):
// validateEmploymentState accepts EmploymentOffMap (5, the new top of the
// domain) but still rejects EmploymentOffMap+1 (6) and every larger value —
// proving the domain widened by exactly one value, not silently opened
// further. EmploymentState is uint8 (unsigned) so a negative value cannot
// be constructed through the public API; the shared validateFieldRange
// combinator validateEmploymentState delegates to is exercised directly
// with a negative int64 below to prove the underlying range check (the same
// one MET-G007 comes from) still rejects negative regardless.
func TestValidateEmploymentStateWidenedDomain(t *testing.T) {
	if err := validateEmploymentState(1, EmploymentOffMap, "corr"); err != nil {
		t.Fatalf("EmploymentOffMap must be accepted: %v", err)
	}
	if err := validateEmploymentState(1, EmploymentState(6), "corr"); err == nil {
		t.Fatal("expected EmploymentState(6) to be rejected, got nil")
	} else {
		assertRegistryCode(t, err, ErrFieldOutOfRange)
	}
	if err := validateEmploymentState(1, EmploymentState(255), "corr"); err == nil {
		t.Fatal("expected EmploymentState(255) to be rejected, got nil")
	} else {
		assertRegistryCode(t, err, ErrFieldOutOfRange)
	}
	// Negative, via the shared combinator directly (EmploymentState itself
	// cannot represent a negative value — it is uint8).
	if err := validateFieldRange(1, "employmentState", -1, 0, int64(EmploymentOffMap), "corr"); err == nil {
		t.Fatal("expected a negative employmentState to be rejected, got nil")
	} else {
		assertRegistryCode(t, err, ErrFieldOutOfRange)
	}
}

// allKnownEmploymentStates is the exhaustive-switch guard's own manifest of
// every declared EmploymentState constant (ICD §11's "table-driven test ...
// fails if a new constant is ever added without a corresponding case").
// When a future PR adds a new EmploymentState value, TestMatchJobExhaustiveSwitch
// below fails loudly (both the length check and the boundary-probe check)
// until this map AND matchJob's switch (coldpass.go) are both updated.
var allKnownEmploymentStates = map[EmploymentState]string{
	EmploymentNone:       "none",
	EmploymentStudent:    "student",
	EmploymentEmployed:   "employed",
	EmploymentUnemployed: "unemployed",
	EmploymentRetired:    "retired",
	EmploymentOffMap:     "offmap",
}

// TestMatchJobExhaustiveSwitch (ICD §11 "exhaustive-switch guard test"):
// proves matchJob (coldpass.go) has a verified, documented outcome for
// every declared EmploymentState value — most importantly that
// EmploymentOffMap citizens are left completely untouched (they already
// hold a real job; the statistical job-matching draw must not fire for
// them, ICD §11 first bullet). Also guards that the manifest above tracks
// validateEmploymentState's own upper bound, so a future enum addition
// cannot silently widen the domain without this test noticing.
func TestMatchJobExhaustiveSwitch(t *testing.T) {
	if err := validateEmploymentState(1, EmploymentOffMap, "corr"); err != nil {
		t.Fatalf("EmploymentOffMap must be a valid state: %v", err)
	}
	if err := validateEmploymentState(1, EmploymentOffMap+1, "corr"); err == nil {
		t.Fatal("a state above EmploymentOffMap was accepted — allKnownEmploymentStates and matchJob's switch are now stale relative to validateEmploymentState's domain; update all three together")
	}
	if len(allKnownEmploymentStates) != int(EmploymentOffMap)+1 {
		t.Fatalf("allKnownEmploymentStates has %d entries, want %d (0..EmploymentOffMap inclusive) — a new EmploymentState constant exists that this manifest (and matchJob's switch) has not been updated for",
			len(allKnownEmploymentStates), int(EmploymentOffMap)+1)
	}

	for state, label := range allKnownEmploymentStates {
		s := newColdShard(0)
		rec := mkRecord(1, 0)
		rec.EmploymentState = state
		rec.Sector = SectorNone
		s.append(rec)

		s.matchJob(42, 1, 0)
		got, _ := unpackEmployment(s.employment[0])

		switch state {
		case EmploymentUnemployed, EmploymentNone:
			if got != EmploymentEmployed {
				t.Fatalf("%s: matchJob did not match an unemployed/never-worked citizen to Employed, got %v", label, got)
			}
		case EmploymentOffMap:
			if got != EmploymentOffMap {
				t.Fatalf("%s: matchJob must leave an off-map-employed citizen's state untouched, got %v (silent-drop/mismatch regression)", label, got)
			}
		case EmploymentEmployed:
			if got != EmploymentEmployed {
				t.Fatalf("%s: matchJob must never change an already-employed citizen's state (only sector may drift), got %v", label, got)
			}
		default: // EmploymentStudent, EmploymentRetired: no case matches, must be untouched
			if got != state {
				t.Fatalf("%s: matchJob unexpectedly mutated state to %v", label, got)
			}
		}
	}
}

// TestOffMapTransitionConservesPopulation (ICD §11 "conservation-unchanged
// test" / ICD §4's "no conservation-accumulator effect" ruling): an
// off-map employment transition is a coarse-state relabelling only — the
// resident count must be exactly unchanged before/after, while the
// population hash DOES change (proving the state actually moved, not that
// the test is vacuous).
func TestOffMapTransitionConservesPopulation(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seed := mkRecord(1, 0)
	seed.EmploymentState = EmploymentUnemployed
	seed.Sector = SectorNone
	if err := api.SeedColdRecords([]ColdRecord{seed}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	popBefore := api.TotalPopulation("corr")
	hashBefore := api.PopulationHash("corr")

	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1,
		Employment: EmploymentOffMap, Sector: SectorNone,
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	popAfter := api.TotalPopulation("corr")
	hashAfter := api.PopulationHash("corr")

	if popAfter != popBefore {
		t.Fatalf("population changed on a coarse relabelling: %d -> %d", popBefore, popAfter)
	}
	if hashAfter == hashBefore {
		t.Fatal("PopulationHash did not change — the employment state transition was not actually recorded (test would be vacuous)")
	}

	// Release: back to Unemployed. Population still unchanged, and the
	// hash returns to its original value (proving the state genuinely
	// round-trips, not merely "changed somehow").
	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1,
		Employment: EmploymentUnemployed, Sector: SectorNone,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	popFinal := api.TotalPopulation("corr")
	hashFinal := api.PopulationHash("corr")
	if popFinal != popBefore {
		t.Fatalf("population changed across the full round trip: %d -> %d", popBefore, popFinal)
	}
	if hashFinal != hashBefore {
		t.Fatal("PopulationHash did not return to its original value after the full Unemployed->OffMap->Unemployed round trip")
	}
}
