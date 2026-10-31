package converge

// Destructive round r1 attack tests for the population domain
// (FEAT-1972079936 Phase 3, attacker Destructive-F1936-pop-r1). Kept in the
// tree because they pin properties the builder's tests did not: the seed
// asymmetry is driven through the REAL Go engine (not a +1 on a sampled
// value), and a plausible-wrong "no births at all" model is caught by the
// births row even though its population row stays inside the 25% band.

import (
	"encoding/json"
	"slices"
	"testing"
)

// populationJournalWithSeed returns a deep copy of the canonical journal
// with the first seed op's args replaced.
func populationJournalWithSeed(t *testing.T, count, ageYears int64) Journal {
	t.Helper()
	j := loadPopulationJournal(t)
	out := Journal{Entries: make([]JournalEntry, len(j.Entries))}
	copy(out.Entries, j.Entries)
	for i := range out.Entries {
		if out.Entries[i].Op != "seed" {
			continue
		}
		b, err := json.Marshal(map[string]int64{"count": count, "ageYears": ageYears})
		if err != nil {
			t.Fatal(err)
		}
		out.Entries[i].Args = b
		return out
	}
	t.Fatal("canonical journal has no seed op")
	return Journal{}
}

// TestAttackPopR1_SeedCohortAsymmetryThroughEngine: a candidate produced by
// the real Go engine from a DIFFERENT seed cohort must fail, never average
// away — both a headcount change (caught exactly) and an age-mix change at
// the same headcount (caught on the natural-change rows).
func TestAttackPopR1_SeedCohortAsymmetryThroughEngine(t *testing.T) {
	ref := mustRunPopulation(t, loadPopulationJournal(t))
	contract := PopulationDomain{}.Contract()

	more := mustRunPopulation(t, populationJournalWithSeed(t, 1300, 28))
	r := Compare("population", ref, more, contract)
	if r.Pass || !slices.Contains(divergedFields(r), "seed_headcount") {
		t.Fatalf("headcount asymmetry (1200 -> 1300) must fail on seed_headcount, got pass=%v fields=%v", r.Pass, divergedFields(r))
	}

	older := mustRunPopulation(t, populationJournalWithSeed(t, 1200, 60))
	r = Compare("population", ref, older, contract)
	if r.Pass {
		t.Fatalf("age-mix asymmetry (the 28y cohort aged to 60y, same headcount) passed: the comparison averaged the cohort away")
	}
	t.Logf("age-mix asymmetry caught on %v", divergedFields(r))
}

// TestAttackPopR1_NoBirthsModelCaughtOnBirths: a self-consistent model with
// zero births (population = seed - deaths) must fail, and the failure must
// name births. Its population row is within the 25% placeholder band (natural
// change over 36 months is a few percent of the headcount), so the births row
// is what carries the gate's teeth; if a future change drops or loosens it,
// this reddens.
func TestAttackPopR1_NoBirthsModelCaughtOnBirths(t *testing.T) {
	ref := mustRunPopulation(t, loadPopulationJournal(t))
	cand := cloneTrajectory(ref)
	pop := ref[0].Values["seed_headcount"]
	for i := range cand {
		pop -= cand[i].Values["deaths"]
		cand[i].Values["births"] = 0
		cand[i].Values["population"] = pop
	}
	r := Compare("population", ref, cand, PopulationDomain{}.Contract())
	if r.Pass || !slices.Contains(divergedFields(r), "births") {
		t.Fatalf("a zero-births model must fail on births, got pass=%v fields=%v", r.Pass, divergedFields(r))
	}
	t.Logf("zero-births model diverged on %v (population row alone does not catch it at BandPct=%v)", divergedFields(r), PopulationBandPct)
}
