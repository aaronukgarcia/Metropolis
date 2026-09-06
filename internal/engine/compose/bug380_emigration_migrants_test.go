package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// BUG-380: compose.go's residentIDs() — the citizen-id set applyMigration
// (compose.go) hands to engine.attract as emigration-eligible — used to
// enumerate ONLY the sequentially-minted seed/direct-seed range
// [1, nextCitizenID), excluding the entire admitted-migrant id range
// (engine.attract's migrantIDHighBit-prefixed ids, minted by
// mintMigrantID, migration.go). Immigration kept admitting migrants every
// month attractiveness exceeded the world baseline; nothing could ever
// remove one again, no matter how severe or how long a subsequent decline
// ran, because attract's applyEmigration (migration.go) only ever
// evaluates the ids it is HANDED in MigrationCommand.ResidentIDs — an id
// simply never enumerated can never be drawn, regardless of the hazard
// function under it.
//
// CLOSED (2026-09-05, Aaron's ruling), on the THIRD attempt at widening
// residentIDs() to include the admitted-migrant range. The first two
// attempts (a 2026-09-02 BUG-529/BUG-535 "first cut", and this ticket's
// own earlier same-day attempt) both reverted after reproducing a
// "sawtooth boom/bust population collapse" and read that collapse as
// evidence migrants must stay PERMANENTLY exempt from emigration. Aaron's
// ruling: that reading was wrong — the sawtooth is the EXPECTED result of
// letting a migrant admitted in month M be an emigration candidate again
// in month M+1, before the city has had a chance to stabilise around them,
// not evidence that eligibility itself is unsafe. The real fix is a TENURE
// GRACE: attract.migrantTenureGraceMonths (migration.go) now gates
// applyEmigration so a migrant is skipped until that many simulated months
// have passed since their own admission (tracked per-migrant in
// AttractAPI.migrantAdmittedMonth, persisted via participant.go). See
// residentIDs()' own doc comment (compose.go) for the full history, and
// internal/engine/attract/bug380_tenure_grace_test.go for the tenure-gate
// proof itself (M+6 blocked, M+13 eligible, grace=0 RED-reproduces the
// sawtooth mechanism at the unit level) — bug529_employment_test.go's
// TestBUG529_EmployedFractionStaysProportionalUnderOrganicMigration is the
// compose-level payoff (population growth stays smooth under organic
// migration with the grace live).
//
// migrantIDsFromCount() (compose.go) — the helper both residentIDs() and
// liveResidentIDs() share the migrant range from — also had its OWN,
// independent, pre-existing off-by-one, fixed alongside this ticket
// (TestMigrantIDsFromCount_MatchesRealMintedRange below): migration.go's
// mintMigrantID pre-increments a counter AttractAPI.New initialises to 1
// (api.go), so the true minted range for a MigrantsAdmitted() count of M
// is [MigrantIDBase+2, MigrantIDBase+M] (M-1 ids), not
// [MigrantIDBase+1, MigrantIDBase+M] (M ids) as the pre-existing
// liveResidentIDs() enumerated inline. That fix stands independently of
// residentIDs()'s eligibility policy.

// TestMigrantIDsFromCount_MatchesRealMintedRange is the RED->GREEN unit
// proof for the off-by-one fix: for a range of migrant counts, the ids
// migrantIDsFromCount() returns must be EXACTLY the ids a real
// AttractAPI/CitizensAPI pair actually minted for that many admitted
// migrants (checked independently via CitizenAt against a real
// engine.attract admission, never by re-deriving the same formula).
//
// PROOF THIS CAN FAIL (corrected, opus-round-bug380 — this comment
// previously also claimed the pre-fix range excluded the true top id;
// wrong, see migrantIDsFromCount's own corrected doc comment):
// migrantIDsFromCount()'s pre-fix formula ([MigrantIDBase+1,
// MigrantIDBase+migrants] inclusive) was a strict SUPERSET of the true
// range, off by exactly one id at the BOTTOM — it always included one
// never-minted phantom (MigrantIDBase+1). That fails THIS test on the
// "every returned id resolves to a real admitted citizen via CitizenAt"
// check below (the phantom never resolves) and on the length check
// (migrants ids returned, not the correct migrants-1) — never on a missing
// top id, which the pre-fix range always contained.
func TestMigrantIDsFromCount_MatchesRealMintedRange(t *testing.T) {
	for _, housingVacancy := range []int64{2, 10, 50, 250} {
		_, comp := newTestEngine(t, 5001+uint64(housingVacancy))
		st := comp.state
		if err := st.attract.SetTermInputs(attract.TermInputs{
			JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
		}); err != nil {
			t.Fatalf("SetTermInputs: %v", err)
		}
		res, err := st.attract.ApplyMigration(attract.MigrationCommand{
			Month:              0,
			ResidentIDs:        st.residentIDs(),
			HousingVacancy:     housingVacancy,
			JunctionThroughput: housingVacancy,
		})
		if err != nil {
			t.Fatalf("ApplyMigration: %v", err)
		}
		if res.Inflow <= 0 {
			t.Fatalf("test setup: HousingVacancy=%d admitted no migrants (Inflow=%d)", housingVacancy, res.Inflow)
		}

		migrants := st.attract.MigrantsAdmitted()
		got := migrantIDsFromCount(migrants)

		// Every id migrantIDsFromCount returns must resolve to a real,
		// admitted citizen.
		for _, id := range got {
			if _, ok := st.citizens.CitizenAt(id, st.cid); !ok {
				t.Fatalf("HousingVacancy=%d: migrantIDsFromCount(%d) returned id %d, which does not resolve to a real admitted migrant", housingVacancy, migrants, id)
			}
		}
		// Every real admitted migrant id (independently walked via the
		// documented true range base+2..base+Inflow+1) must be present.
		want := int(res.Inflow)
		if len(got) != want {
			t.Fatalf("HousingVacancy=%d: migrantIDsFromCount(%d) returned %d ids, want %d (== Inflow)", housingVacancy, migrants, len(got), want)
		}
		for i := uint64(2); i <= uint64(res.Inflow)+1; i++ {
			id := attract.MigrantIDBase + i
			found := false
			for _, g := range got {
				if g == id {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("HousingVacancy=%d: migrantIDsFromCount(%d) is missing real admitted-migrant id %d", housingVacancy, migrants, id)
			}
		}
	}
}

// TestBUG380_EmigrationMechanismDepartsMigrant proves attract's emigration
// mechanism (migration.go's applyEmigration/EmigrationHazard) is unbiased
// with respect to id range: fed a ResidentIDs slice containing REAL
// admitted-migrant ids well past the tenure grace, a severe, sustained
// decline departs one of them exactly as it would a native, via the SAME
// per-resident hazard function (attract.EmigrationHazard) — "same terms",
// not a separate rate.
func TestBUG380_EmigrationMechanismDepartsMigrant(t *testing.T) {
	_, comp := newTestEngine(t, 9002)
	st := comp.state

	// Admit migrants (positive-score month), independently deriving the
	// real minted range from this call's own Inflow (never from
	// migrantIDsFromCount/MigrantsAdmitted — this test proves the
	// mechanism, so it must not rely on the production enumeration it is
	// separately testing above).
	if err := st.attract.SetTermInputs(attract.TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs(positive): %v", err)
	}
	admitRes, err := st.attract.ApplyMigration(attract.MigrationCommand{
		Month:              0,
		ResidentIDs:        st.residentIDs(),
		HousingVacancy:     500,
		JunctionThroughput: 500,
	})
	if err != nil {
		t.Fatalf("ApplyMigration(positive): %v", err)
	}
	if admitRes.Inflow <= 0 {
		t.Fatalf("test setup: expected a positive-score month to admit migrants, got Inflow=%d", admitRes.Inflow)
	}
	migrantIDs := make([]uint64, 0, admitRes.Inflow)
	for i := uint64(2); i <= uint64(admitRes.Inflow)+1; i++ {
		migrantIDs = append(migrantIDs, attract.MigrantIDBase+i)
	}

	// Force a maximally severe decline: every term at its floor, so decline
	// saturates at 1 and every resident's hazard sits at its per-ambition
	// ceiling (EmigrationHazard's base floor is 0.2, so even the
	// least-ambitious migrant has a non-trivial per-month chance) — run
	// starting from month 13 so every fed id is already past the 12-month
	// tenure grace (admitted at month 0), isolating the MECHANISM proof
	// from the grace gate (that gate has its own dedicated proof,
	// internal/engine/attract/bug380_tenure_grace_test.go).
	if err := st.attract.SetTermInputs(attract.TermInputs{}); err != nil {
		t.Fatalf("SetTermInputs(severe decline): %v", err)
	}

	beforePop := st.citizens.TotalPopulation(st.cid)

	var departedID uint64
	const startMonth = 13
	const maxMonths = 48
	var month int64
	for month = startMonth; month <= maxMonths; month++ {
		res, err := st.attract.ApplyMigration(attract.MigrationCommand{
			Month:              month,
			ResidentIDs:        migrantIDs, // hand-built: ONLY the real migrant ids, proving the mechanism in isolation
			HousingVacancy:     0,
			JunctionThroughput: 0,
		})
		if err != nil {
			t.Fatalf("ApplyMigration(month %d): %v", month, err)
		}
		if res.Net >= 0 {
			t.Fatalf("month %d: expected a negative net under a fully-floored term scenario, got %v (res=%+v)", month, res.Net, res)
		}
		for _, id := range migrantIDs {
			if _, ok := st.citizens.CitizenAt(id, st.cid); !ok {
				departedID = id
				break
			}
		}
		if departedID != 0 {
			break
		}
	}

	if departedID == 0 {
		t.Fatalf("no migrant departed via emigration within months %d-%d of maximal decline (started with %d migrants past tenure grace, fed directly as ResidentIDs) — the emigration MECHANISM itself failed to depart a migrant even when explicitly given eligible migrant ids", startMonth, maxMonths, len(migrantIDs))
	}
	t.Logf("migrant id %d departed via emigration at month %d (of %d fed as eligible, all past the tenure grace)", departedID, month, len(migrantIDs))

	afterPop := st.citizens.TotalPopulation(st.cid)
	if afterPop >= beforePop {
		t.Fatalf("TotalPopulation did not decrease across the emigration run: before=%d after=%d", beforePop, afterPop)
	}
	if afterPop < 0 {
		t.Fatalf("TotalPopulation went negative: %d", afterPop)
	}
}

// TestBUG380_ResidentIDs_IncludesAdmittedMigrants is the closing structural
// proof: residentIDs() (the set applyMigration hands to attract as
// emigration-eligible) now includes EVERY admitted-migrant id, not just
// the seed/direct-seed range. Pre-fix (and during this ticket's own first
// two reverted attempts at the widening alone, without the tenure grade)
// this was the missing half of the story — see the package doc comment
// above for why the widening alone was not sufficient to ship safely.
func TestBUG380_ResidentIDs_IncludesAdmittedMigrants(t *testing.T) {
	_, comp := newTestEngine(t, 9001)
	st := comp.state

	if err := st.attract.SetTermInputs(attract.TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	res, err := st.attract.ApplyMigration(attract.MigrationCommand{
		Month:              0,
		ResidentIDs:        st.residentIDs(),
		HousingVacancy:     500,
		JunctionThroughput: 500,
	})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if res.Inflow <= 0 {
		t.Fatalf("test setup: expected a positive-score month to admit migrants, got Inflow=%d", res.Inflow)
	}

	migrants := st.attract.MigrantsAdmitted()
	idSet := make(map[uint64]bool, len(st.residentIDs()))
	for _, id := range st.residentIDs() {
		idSet[id] = true
	}
	for i := uint64(2); i <= migrants; i++ {
		want := attract.MigrantIDBase + i
		if !idSet[want] {
			t.Fatalf("residentIDs() is missing admitted-migrant id %d (of %d admitted) — emigration eligibility still structurally excludes migrants (BUG-380)", want, migrants)
		}
	}
}

// TestBUG380_PopulationNeverCollapsesMoreThanCap is the third re-round's
// (opus-reround3-bug380) closing proof: applyEmigration's own doc comment
// claims "removes up to |net| residents", and a HARD backstop
// (emigrationMaxMonthlyShare, migration.go) additionally bounds any single
// month's emigration-driven outflow at ~2% of the resident pool regardless
// of net's magnitude. This test runs the exact scenario that broke the
// contract before the fix (seed 4242, 200 months) and asserts NO month's
// population drop exceeds ceil(2% of the PRIOR month's population) plus
// that month's own natural-mortality deaths (VitalDeaths() delta) — the
// two population-reducing channels this composition has, both accounted
// for, neither able to compound into the kind of collapse the round found:
// population 905->292 in ONE month at month 191 (a >65% single-month
// drop), with organic admissions flatlined at 1087 since ~month 160 (every
// arriving cohort immediately eligible for the same uncapped 20%+/month
// cull once past the tenure grace — see applyEmigration's own doc comment
// for the full root-cause history).
//
// PROOF THIS CAN FAIL: reverting migration.go's applyEmigration to skip
// the outflowCap entirely (restoring the pre-fix unconditional loop over
// every eligible id) reproduces the exact month-191 collapse on this same
// seed — verified by hand this session (temporarily reverted, re-run,
// confirmed the >60%-in-one-month drop recurs and this test reds on it,
// then restored).
func TestBUG380_PopulationNeverCollapsesMoreThanCap(t *testing.T) {
	const months = 200
	const maxMonthlySharePct = 2 // mirrors migration.go's emigrationMaxMonthlyShare (0.02), expressed as a whole percent for integer arithmetic

	e, comp := newTestEngine(t, b380Seed)
	prevPop := comp.Population()
	prevDeaths := comp.VitalDeaths()
	var worstDrop, worstMonth, worstAllowed int

	for m := int64(1); m <= months; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		pop := comp.Population()
		deaths := comp.VitalDeaths()
		monthDeaths := int(deaths - prevDeaths)

		drop := prevPop - pop
		if drop > 0 {
			// Integer ceil(prevPop * 2 / 100) — avoids a float import for
			// a simple percentage-of-int ceiling.
			capAllowance := (prevPop*maxMonthlySharePct + 99) / 100
			maxAllowed := capAllowance + monthDeaths
			if drop > worstDrop {
				worstDrop, worstMonth, worstAllowed = drop, int(m), maxAllowed
			}
			if drop > maxAllowed {
				t.Fatalf("month %d: population dropped by %d (from %d to %d) — exceeds the cap+deaths bound of %d (2%% of %d = %d, plus %d natural deaths this month). This is the BUG-380 third-re-round mass-emigration collapse (uncapped applyEmigration removing >=20%% of the eligible pool in one month) reproducing.",
					m, drop, prevPop, pop, maxAllowed, prevPop, capAllowance, monthDeaths)
			}
		}
		prevPop, prevDeaths = pop, deaths
	}
	t.Logf("worst single-month drop over %d months: %d (month %d), within allowed bound %d — no month-191-style collapse (final population=%d, VitalDeaths()=%d)", months, worstDrop, worstMonth, worstAllowed, prevPop, prevDeaths)
}

// TestBUG380_SurvivalIsFairAcrossIDBands is the fourth re-round's
// (opus-reround4-bug380) closing survival-fairness proof: the third
// re-round's cap fix selected departures by walking cmd.ResidentIDs in
// its existing ascending-by-id order and taking the first N whose hazard
// draw cleared, stopping once the cap was reached — under a BINDING cap,
// this deterministically emptied the LOWEST ids first every month,
// regardless of ambition (measured: native citizens, ids 1-64, fell to
// 1/64 alive by month 200 while the newest migrant cohort stayed 97%
// intact — pure scan-position bias, inverting AC-6). Fixed by selecting
// departures by draw/hazard margin instead of scan position (migration.go's
// applyEmigration). Since every resident in this organic run is subject to
// the SAME hazard formula (EmigrationHazard is a pure function of ambition
// and decline, and this composed run's citizens are drawn from the same
// personality distribution regardless of native/migrant status or
// admission order), survival should now be roughly UNIFORM across id
// bands — no band should be structurally favoured or starved purely by
// where its ids happen to sit in the enumeration.
//
// PROOF THIS CAN FAIL: reverting migration.go's applyEmigration to the
// third re-round's scan-order selection reproduces the exact band skew
// this test guards against — verified by hand this session (temporarily
// reverted, re-run, confirmed a >15-point spread, then restored,
// diff-verified byte-identical).
//
// NATIVES ARE LOGGED, NOT ASSERTED (found while writing this test, worth
// recording): birthMigrant (migration.go) mints every migrant with a
// NEUTRAL personality (neutralMigrantPersonality: every axis, including
// ambition, at the midpoint 50 — a documented v1 placeholder), so under
// sustained decline every migrant shares the SAME hazard exactly. Native/
// seed citizens are minted with a REAL, varied personality distribution
// (compose.go's spawnCitizens), so their ambition — and therefore their
// hazard — varies individually and is not comparable to the migrants'
// flat 50 as an "identical hazard" band. Measured this run: natives
// survived 76.6% vs ~57% for every migrant third — a real, EXPECTED
// consequence of AC-6 (natives' average ambition here is evidently lower
// than the migrants' fixed 50, so their average hazard is genuinely
// lower), not a residual scan-position bug. The THREE MIGRANT bands
// (oldest/middle/newest-admitted thirds) are the actual scan-position-bias
// test: they share the identical ambition=50 hazard by construction, so
// any survival gap AMONG THEM is attributable only to admission-order
// (id) position, exactly what BUG-380's fourth re-round found broken.
func TestBUG380_SurvivalIsFairAcrossIDBands(t *testing.T) {
	const months = 200
	const maxSpreadPoints = 15.0 // applies to the three MIGRANT bands only — see doc comment for why natives are excluded from this assertion

	e, comp := newTestEngine(t, b380Seed)
	for m := int64(1); m <= months; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	st := comp.state

	admitted := st.attract.MigrantsAdmitted()
	if admitted < 12 {
		t.Fatalf("test setup: too few migrants admitted (%d) to bucket into thirds meaningfully", admitted)
	}
	nativeIDs := st.baseResidentIDs()
	if len(nativeIDs) == 0 {
		t.Fatalf("test setup: no native/seed ids to compare against")
	}
	migrantIDs := migrantIDsFromCount(admitted) // true admission order: oldest first, newest last
	third := len(migrantIDs) / 3
	oldest := migrantIDs[:third]
	middle := migrantIDs[third : 2*third]
	newest := migrantIDs[2*third:]

	survivalPct := func(ids []uint64) float64 {
		if len(ids) == 0 {
			return -1
		}
		alive := 0
		for _, id := range ids {
			if _, ok := st.citizens.CitizenAt(id, st.cid); ok {
				alive++
			}
		}
		return 100 * float64(alive) / float64(len(ids))
	}

	type band struct {
		name string
		pct  float64
		n    int
	}
	// natives is LOGGED for context (see doc comment: different, varied
	// ambition distribution, not a fair "identical hazard" comparison
	// point) but excluded from the asserted spread below.
	nativeBand := band{"natives", survivalPct(nativeIDs), len(nativeIDs)}
	migrantBands := []band{
		{"oldest-third", survivalPct(oldest), len(oldest)},
		{"middle-third", survivalPct(middle), len(middle)},
		{"newest-third", survivalPct(newest), len(newest)},
	}

	t.Logf("survival by id band after %d months (seed %d, %d cumulative admissions):", months, b380Seed, admitted)
	t.Logf("  %-14s n=%4d survival=%.1f%% (logged only — varied native ambition distribution, see doc comment)", nativeBand.name, nativeBand.n, nativeBand.pct)
	minPct, maxPct := 101.0, -1.0
	for _, b := range migrantBands {
		t.Logf("  %-14s n=%4d survival=%.1f%%", b.name, b.n, b.pct)
		if b.pct < minPct {
			minPct = b.pct
		}
		if b.pct > maxPct {
			maxPct = b.pct
		}
	}
	spread := maxPct - minPct
	t.Logf("migrant-band spread (max-min) = %.1f points, want < %.1f", spread, maxSpreadPoints)
	if spread >= maxSpreadPoints {
		t.Fatalf("survival spread across the three MIGRANT id bands (identical ambition=50 hazard by construction) is %.1f points (>= %.1f) — bands: %+v. This is the BUG-380 fourth-re-round scan-position bias (lowest ids emptied first under a binding cap) reproducing.", spread, maxSpreadPoints, migrantBands)
	}
}
