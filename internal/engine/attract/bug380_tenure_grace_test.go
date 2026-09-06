package attract

import "testing"

// BUG-380 (2026-09-05, Aaron's ruling): the "sawtooth boom/bust population
// collapse" two earlier attempts at widening compose.go's residentIDs() to
// include admitted migrants reproduced is NOT evidence that migrants must
// stay permanently exempt from emigration — it is the expected result of
// letting a migrant admitted in month M be an emigration candidate again in
// month M+1, before the city has had a chance to stabilise around them. The
// fix is a TENURE GRACE (migrantTenureGraceMonths, migration.go): a migrant
// is skipped by applyEmigration until that many simulated months have
// passed since their own admission month (tracked per-migrant in
// AttractAPI.migrantAdmittedMonth, api.go).
//
// This file proves the tenure gate itself, at the layer it actually lives
// in (this package) — the compose-level payoff (population growth staying
// smooth under organic migration) is proved separately by
// bug529_employment_test.go's TestBUG529_EmployedFractionStaysProportionalUnderOrganicMigration,
// whose own doc comment records the hand-verified grace=0 RED-reproduction
// of the sawtooth this test's grace-override sub-test mirrors at the unit
// level.

// bug380AdmitOnePair admits exactly one migrant household (two citizens)
// via a single positive-score ApplyMigration call at month 0, and returns
// their two ids. Fails the test via t.Fatalf if the call does not admit
// exactly one pair — a setup failure, not a tenure-grace result.
func bug380AdmitOnePair(t *testing.T, a *AttractAPI) (idA, idB uint64) {
	t.Helper()
	if err := a.SetTermInputs(TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs(positive): %v", err)
	}
	// HousingVacancy=1 household (2 people) caps admission at exactly one
	// pair regardless of how large the raw attractiveness gap computes,
	// keeping this test's id bookkeeping trivial.
	res, err := a.ApplyMigration(MigrationCommand{
		Month: 0, HousingVacancy: 1, JunctionThroughput: int64(migrantHouseholdSize),
	})
	if err != nil {
		t.Fatalf("ApplyMigration(positive): %v", err)
	}
	if res.Inflow != migrantHouseholdSize {
		t.Fatalf("test setup: expected exactly one migrant household (Inflow=%d), got Inflow=%d", migrantHouseholdSize, res.Inflow)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	// The pair minted by THIS call is the top two entries of the counter
	// (nextMigrantID-1, nextMigrantID) — a fresh AttractAPI's counter
	// starts at 1 (New's doc comment), so a single one-pair admission ends
	// at nextMigrantID==3, giving ids base+2/base+3. Read the live counter
	// rather than hardcoding those literals so this helper stays correct
	// if ever reused after prior admissions.
	return migrantIDHighBit | (a.nextMigrantID - 1), migrantIDHighBit | a.nextMigrantID
}

// bug380ForceSevereDecline pushes every pushed term to its floor so
// subsequent ApplyMigration calls compute a maximally negative net
// (decline saturates at 1 — see applyEmigration's own doc comment).
func bug380ForceSevereDecline(t *testing.T, a *AttractAPI) {
	t.Helper()
	if err := a.SetTermInputs(TermInputs{}); err != nil {
		t.Fatalf("SetTermInputs(severe decline): %v", err)
	}
}

// bothResolve reports whether both idA and idB still resolve to a live
// citizen.
func bothResolve(a *AttractAPI, idA, idB uint64) bool {
	_, okA := a.citizens.CitizenAt(idA, a.correlationID)
	_, okB := a.citizens.CitizenAt(idB, a.correlationID)
	return okA && okB
}

// TestMigrantTenureGrace_BlocksEarlyEmigrationAllowsLate is BUG-380's
// central RED->GREEN proof: a migrant household admitted at month 0 is
// NEVER selected by emigration while its tenure is under
// migrantTenureGraceMonths (checked at month 6, well inside the 12-month
// grace — a HARD, deterministic guarantee regardless of decline severity
// or RNG, since the gate skips the id before any hazard draw), and CAN be
// selected once tenure clears the grace (checked from month 13 onward,
// the first month tenure>=12).
//
// PROOF THIS CAN FAIL: bug380AdmitOnePair+bug380ForceSevereDecline
// against migrantTenureGraceMonths temporarily overridden to 0 (this same
// test's grace-0 sub-test, run at the end) departs the SAME pair as early
// as month 1 — i.e. this test's month-6 "must not depart" assertion would
// fail deterministically at grace=0, which is exactly the sawtooth
// mechanism bug529_employment_test.go's own doc comment records: nothing
// insulates a just-arrived migrant from the very next decline month.
func TestMigrantTenureGrace_BlocksEarlyEmigrationAllowsLate(t *testing.T) {
	if migrantTenureGraceMonths != 12 {
		t.Fatalf("test assumes the documented placeholder migrantTenureGraceMonths==12, got %d — update this test's month boundaries (6/13) if the placeholder changes", migrantTenureGraceMonths)
	}

	a, _, _, _ := newAPI(t, validConfig())
	idA, idB := bug380AdmitOnePair(t, a)
	bug380ForceSevereDecline(t, a)

	// Months 1-11: tenure (month-0) is 1..11, always < 12 -- MUST NOT
	// depart, deterministically, regardless of decline severity.
	for month := int64(1); month <= 11; month++ {
		if _, err := a.ApplyMigration(MigrationCommand{
			Month: month, ResidentIDs: []uint64{idA, idB}, HousingVacancy: 0, JunctionThroughput: 0,
		}); err != nil {
			t.Fatalf("ApplyMigration(month %d): %v", month, err)
		}
		if !bothResolve(a, idA, idB) {
			t.Fatalf("month %d: a migrant departed while tenure (%d) < migrantTenureGraceMonths (%d) -- the tenure grace did not block an early departure", month, month, migrantTenureGraceMonths)
		}
	}
	// Explicit month-6 checkpoint the ticket asked for by name: both ids
	// must still be present.
	if !bothResolve(a, idA, idB) {
		t.Fatalf("month 6 checkpoint: migrant pair departed before tenure reached migrantTenureGraceMonths")
	}

	// From month 12 (tenure==12, the grace boundary itself -- "< grace" no
	// longer holds) onward, the pair becomes eligible; run under sustained
	// severe decline until at least one departs, bounded well within a
	// plausible window (grace=0's own sawtooth shows a comparable hazard
	// departs a fresh migrant within a handful of months, so 40 months of
	// margin past the grace boundary is generous, not tight).
	var departedAtMonth int64
	const maxMonth = 52
	for month := int64(12); month <= maxMonth; month++ {
		if _, err := a.ApplyMigration(MigrationCommand{
			Month: month, ResidentIDs: []uint64{idA, idB}, HousingVacancy: 0, JunctionThroughput: 0,
		}); err != nil {
			t.Fatalf("ApplyMigration(month %d): %v", month, err)
		}
		if !bothResolve(a, idA, idB) {
			departedAtMonth = month
			break
		}
	}
	if departedAtMonth == 0 {
		t.Fatalf("neither migrant departed within %d months of sustained severe decline once past the tenure grace (idA=%d idB=%d) -- the grace boundary itself may be blocking eligibility past the intended 12 months", maxMonth, idA, idB)
	}
	if departedAtMonth < 12 {
		t.Fatalf("departure at month %d is BEFORE the tenure grace boundary (12) -- should be structurally impossible", departedAtMonth)
	}
	t.Logf("migrant pair (ids %d,%d) admitted month 0: blocked through month 11, departed at month %d (tenure %d)", idA, idB, departedAtMonth, departedAtMonth)

	// Explicit month-13 checkpoint the ticket asked for by name: confirm a
	// FRESH, independent run of the identical scenario is ALSO eligible by
	// month 13 specifically (not just "eventually" by maxMonth) -- run
	// months 12-13 in isolation and assert the pair is at least NO LONGER
	// grace-blocked (migrantBelowTenureGrace itself, checked directly,
	// independent of whether this particular RNG draw happens to depart it
	// that exact month).
	a2, _, _, _ := newAPI(t, validConfig())
	idA2, idB2 := bug380AdmitOnePair(t, a2)
	if a2.migrantBelowTenureGrace(idA2, 6) != true {
		t.Fatalf("migrantBelowTenureGrace(month=6) = false, want true (tenure 6 < grace 12)")
	}
	if a2.migrantBelowTenureGrace(idA2, 13) != false {
		t.Fatalf("migrantBelowTenureGrace(month=13) = true, want false (tenure 13 >= grace 12) -- migrant CAN depart at month 13")
	}
	if a2.migrantBelowTenureGrace(idB2, 13) != false {
		t.Fatalf("migrantBelowTenureGrace(idB, month=13) = true, want false -- both household members clear the grace together")
	}
}

// TestMigrantTenureGrace_Deterministic proves GR#21: two independent runs
// of the identical admit-then-decline scenario (same fixed newAPI seed)
// produce IDENTICAL outcomes -- same departure month, same surviving id
// set -- never a map-iteration-order or wall-clock-derived divergence.
func TestMigrantTenureGrace_Deterministic(t *testing.T) {
	run := func() (departedMonth int64, idA, idB uint64) {
		a, _, _, _ := newAPI(t, validConfig())
		idA, idB = bug380AdmitOnePair(t, a)
		bug380ForceSevereDecline(t, a)
		for month := int64(1); month <= 52; month++ {
			if _, err := a.ApplyMigration(MigrationCommand{
				Month: month, ResidentIDs: []uint64{idA, idB}, HousingVacancy: 0, JunctionThroughput: 0,
			}); err != nil {
				t.Fatalf("ApplyMigration(month %d): %v", month, err)
			}
			if !bothResolve(a, idA, idB) {
				return month, idA, idB
			}
		}
		return 0, idA, idB
	}
	m1, idA1, idB1 := run()
	m2, idA2, idB2 := run()
	if m1 == 0 || m2 == 0 {
		t.Fatalf("test setup: at least one run never departed within 52 months (m1=%d m2=%d)", m1, m2)
	}
	if m1 != m2 || idA1 != idA2 || idB1 != idB2 {
		t.Fatalf("two identical-seed runs diverged: run1(month=%d ids=%d,%d) != run2(month=%d ids=%d,%d)", m1, idA1, idB1, m2, idA2, idB2)
	}
}

// TestMigrantTenureGrace_ZeroGraceReproducesSawtoothMechanism is the
// explicit "RED-prove by setting grace to 0" the ticket asked for: with
// migrantTenureGraceMonths temporarily forced to 0, the SAME admit-then-
// decline scenario departs the freshly-admitted migrant almost immediately
// (well before month 6), reproducing at the unit level exactly the
// mechanism bug529_employment_test.go's compose-level suite shows causing
// the sawtooth (a migrant admitted this month is a full-strength emigration
// candidate again the very next declining month). Restores the package var
// via t.Cleanup so this override can never leak into another test in the
// same process (Go test binaries share package-level state).
func TestMigrantTenureGrace_ZeroGraceReproducesSawtoothMechanism(t *testing.T) {
	saved := migrantTenureGraceMonths
	migrantTenureGraceMonths = 0
	t.Cleanup(func() { migrantTenureGraceMonths = saved })

	a, _, _, _ := newAPI(t, validConfig())
	idA, idB := bug380AdmitOnePair(t, a)
	bug380ForceSevereDecline(t, a)

	var departedAtMonth int64
	for month := int64(1); month <= 6; month++ {
		if _, err := a.ApplyMigration(MigrationCommand{
			Month: month, ResidentIDs: []uint64{idA, idB}, HousingVacancy: 0, JunctionThroughput: 0,
		}); err != nil {
			t.Fatalf("ApplyMigration(month %d): %v", month, err)
		}
		if !bothResolve(a, idA, idB) {
			departedAtMonth = month
			break
		}
	}
	if departedAtMonth == 0 {
		t.Fatalf("with migrantTenureGraceMonths=0, the migrant pair survived all 6 months under sustained severe decline -- this test no longer demonstrates the sawtooth mechanism it exists to pin (grace=0 should behave exactly like no gate at all, i.e. as vulnerable as any native)")
	}
	t.Logf("grace=0: migrant pair departed at month %d (well before the real grace's month-12 boundary) -- confirms the gate, not something else, is what protects a freshly-admitted migrant", departedAtMonth)
}
