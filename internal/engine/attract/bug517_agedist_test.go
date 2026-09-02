package attract

import "testing"

// BUG-517: before this fix, every migrant birthMigrant minted carried
// BirthMonth == the current admission month (age 0), regardless of when
// they arrived. This file proves the fix at the tightest possible
// granularity: a SINGLE ApplyMigration call (a single admission month) that
// admits many migrants at once must NOT give them all the identical
// BirthMonth — a real founding wave of migrants is age-varied, not a crowd
// of simultaneous newborns.
//
// admitManyMigrants (feat_1972079927_migrantwealth_test.go) is reused
// verbatim: it already builds the harness this needs (AttractAPI +
// CitizensAPI + households + finance, wired for a guaranteed large
// positive migration admit in one ApplyMigration call) and returns the
// admitted ids.

// TestBUG517_MigrantBatch_HasNonDegenerateAgeSpread is the precise,
// single-cohort proof: every migrant in this test was admitted in THE SAME
// month (so a flat age-0 bug would give them all the IDENTICAL
// BirthMonth). It must instead show real variety, with representation in
// more than one age band.
//
// PROOF THIS CAN FAIL: temporarily replacing birthMigrant's drawn age with
// a constant 0 (the pre-fix behaviour) collapses every BirthMonth in this
// batch to the identical value (the admission month) and this test fails
// on the "not all equal" check — verified during development, then
// reverted (see the sibling package's citizens.DrawAgeAtCreationMonths
// RED-proof for the same mechanism at unit level).
func TestBUG517_MigrantBatch_HasNonDegenerateAgeSpread(t *testing.T) {
	const month = int64(1)
	_, ca, ids := admitManyMigrants(t, 42, month)
	if len(ids) < 10 {
		t.Fatalf("only %d migrants admitted, want at least 10 to probe age variance meaningfully", len(ids))
	}

	seenBirthMonth := map[int32]bool{}
	var atLeastOneOlderThanNewborn bool
	for _, id := range ids {
		cit, ok := ca.CitizenAt(id, "corr-wealth")
		if !ok {
			t.Fatalf("CitizenAt(%d): not found", id)
		}
		if int64(cit.BirthMonth) > month {
			t.Fatalf("migrant %d BirthMonth=%d is AFTER its own admission month %d", id, cit.BirthMonth, month)
		}
		seenBirthMonth[cit.BirthMonth] = true
		if cit.BirthMonth < int32(month) {
			atLeastOneOlderThanNewborn = true
		}
	}
	if len(seenBirthMonth) < 2 {
		t.Fatalf("all %d migrants admitted in month %d share the IDENTICAL BirthMonth %v — degenerate (BUG-517's all-age-0 class)", len(ids), month, seenBirthMonth)
	}
	if !atLeastOneOlderThanNewborn {
		t.Fatalf("every migrant in this batch has BirthMonth == the admission month (%d) — none of them arrived as anything other than a newborn", month)
	}
}

// TestBUG517_MigrantAge_Deterministic proves the age draw is reproducible
// (GR#21): two independent admissions at the same world seed and month
// must give the identical migrant the identical BirthMonth.
func TestBUG517_MigrantAge_Deterministic(t *testing.T) {
	_, ca1, ids1 := admitManyMigrants(t, 99, 3)
	_, ca2, ids2 := admitManyMigrants(t, 99, 3)
	if len(ids1) != len(ids2) {
		t.Fatalf("two identical-seed runs admitted different migrant counts: %d vs %d", len(ids1), len(ids2))
	}
	for i, id := range ids1 {
		c1, ok1 := ca1.CitizenAt(id, "corr-wealth")
		c2, ok2 := ca2.CitizenAt(ids2[i], "corr-wealth")
		if !ok1 || !ok2 {
			t.Fatalf("migrant %d: CitizenAt ok1=%v ok2=%v", id, ok1, ok2)
		}
		if c1.BirthMonth != c2.BirthMonth {
			t.Fatalf("migrant %d: run1 BirthMonth=%d, run2 BirthMonth=%d — the age draw is not deterministic", id, c1.BirthMonth, c2.BirthMonth)
		}
	}
}
