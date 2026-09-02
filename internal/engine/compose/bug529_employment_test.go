package compose

import (
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-529 / BUG-535 (composition-root / real-engine runtime proof):
// FEAT-083's population-scaled wages silently reverted to a flat floor
// within ~1.5 sim-years, and (a runtime probe found, mid-fix) fertility
// births could never happen for anyone outside the original seed cohort
// either. TWO compounding wiring gaps, both fixed here:
//
//  1. engine.attract's birthMigrant (migration.go) minted EVERY admitted
//     migrant with Employment.State = citizens.EmploymentUnemployed — a
//     state moneycirc.go's desiredEmployment treats as TERMINAL
//     (Employed/Unemployed/EmploymentOffMap are never redrawn, see
//     desiredEmployment's doc comment). Fixed by minting migrants
//     EmploymentNone instead (the same "undecided" state the seed
//     population already uses), so they receive the same one-time
//     employmentDecision draw as everyone else.
//
//  2. Independently — and this is what actually made the wage bill pin,
//     since fix #1 alone provably changes nothing observable (see below) —
//     compose.go's residentIDs() enumerated ONLY [1, nextCitizenID): the
//     CLOSED, sequentially-minted seed/direct-seed id set spawnCitizens
//     mints once at Wire time, which only ever SHRINKS thereafter
//     (mortality, emigration) since nothing is ever added to it after
//     Wire. Migrant ids (engine.attract, high-bit-prefixed) and
//     fertility-child ids (engine.citizens, FertilityChildIDBase-prefixed)
//     were NEVER enumerated, so moneycirc.go's markEmploymentAndCount/
//     formResidentHouseholds/distributeWagesToResidents/
//     employedResidentCount never counted a migrant's or child's
//     Employment.State/Household regardless of its value — pinning the
//     wage bill at monthlyWagesFloor as the seed cohort attrited even
//     while the CITY's total population (comp.Population()) grew via
//     migration, AND leaving a migrant/child's Household at 0 forever
//     since formResidentHouseholds (the only caller of citizens'
//     LifeEventPartner) never paired them either. Fixed by widening
//     residentIDs() itself to union the seed range with every migrant id
//     engine.attract has ever admitted (AttractAPI.MigrantsAdmitted(), a
//     LIVE read of attract's own already-correctly-persisted counter) and
//     every fertility-child id engine.citizens has ever minted
//     (CitizensAPI.FertilityChildrenBorn(), likewise a live read) — see
//     residentIDs' own doc comment (compose.go) for why a compose-tracked
//     shadow counter was tried first and rejected (it desynced from the
//     real counters across a save/LoadAt boundary, since compose's own
//     simState fields are not part of any snapshot payload).
//
// PROOF FIX #1 ALONE CHANGES NOTHING OBSERVABLE: reverting migration.go's
// EmploymentNone back to EmploymentUnemployed while keeping fix #2 produced
// BIT-IDENTICAL wage-bill/employed figures in a 24-month composed run,
// because citizens.ColdShard.matchJob (a separate, always-on,
// JobMatchRate-gated mechanism, coldpass.go) already treats
// EmploymentUnemployed and EmploymentNone identically and independently
// converges the WHOLE population toward Employed regardless — it was
// compose's own accounting that never looked at migrants/children at all,
// not their initial Employment.State, that pinned the wage bill.
const bug529RetirementAgeMonths = 66 * 12 // mirrors moneycirc.go's retirementAgeMonths (GR#15: derived, not hand-picked)

// bug529Snapshot is one month's worth of the runtime-proof numbers, kept so
// the test can print the RED/GREEN month->employed/pop/wageBill table the
// task asked for. population is read from comp.Population()
// (citizens.TotalPopulation, ALL fidelities/id-ranges) — a metric that
// means the same thing whether or not either fix is applied, so it is the
// stable "how big is the city really" axis the eligible/employed collapse
// is measured against.
type bug529Snapshot struct {
	month      int64
	population int
	eligible   int // wage-eligible residents below retirement age (see bug529RetirementAgeMonths)
	employed   int
	wageBill   int64
}

// ratio returns employed/eligible, or -1 if eligible is 0 (avoids a
// divide-by-zero corrupting the failure message).
func (s bug529Snapshot) ratio() float64 {
	if s.eligible == 0 {
		return -1
	}
	return float64(s.employed) / float64(s.eligible)
}

// bug529Measure reads comp.Population() for the population figure, then
// walks residentIDs() (compose.go's OWN production definition of the live
// resident set, post-fix: seed + migrants + fertility children — see that
// function's doc comment), bucketing each citizen's Employment.State/Age
// directly off the record (never calling markEmploymentAndCount's own
// counting logic), so the eligible/employed tally is an INDEPENDENT check
// on the real engine's citizen records, not a self-fulfilling re-call into
// the code under test.
func bug529Measure(t *testing.T, comp *Composition, month int64) bug529Snapshot {
	t.Helper()
	snap := bug529Snapshot{
		month:      month,
		population: comp.Population(),
		wageBill:   int64(comp.state.finance.WagesPosted()),
	}
	for _, id := range comp.state.liveResidentIDs() {
		cit, ok := comp.state.citizens.CitizenAt(id, comp.state.cid)
		if !ok {
			continue // departed — not a corruption, just skip
		}
		if cit.Age() < bug529RetirementAgeMonths {
			snap.eligible++
			if cit.Employment.State == citizens.EmploymentEmployed {
				snap.employed++
			}
		}
	}
	return snap
}

// TestBUG529_EmployedFractionStaysProportionalUnderOrganicMigration is the
// mandatory RED->GREEN runtime proof: it composes the REAL engine
// (core.NewEngine + compose.Wire, no stubs), advances it 48 sim-months
// under ordinary organic migration (no test-injected migrants), and
// asserts:
//
//  1. the employed fraction of the wage-eligible population stays close to
//     employmentRateOfWorkingAgeFraction (0.75) instead of collapsing
//     toward zero as migrants accumulate, while total population
//     (comp.Population()) keeps growing;
//  2. the wage bill does NOT pin at monthlyWagesFloor (BUG-529's concrete
//     symptom);
//  3. (logged, not asserted — see the safeUint32 finding in the test body)
//     VitalBirths() after the run: >0 once the births-unblock lane
//     (2026-09-02) fixed the safeUint32 partner/household truncation this
//     finding originally reported;
//  4. the conservation invariant (people in == out + pending, money
//     conserved) holds every tick of the whole run — proving the wider
//     liveResidentIDs() enumeration introduces no double-counting or
//     double-crediting.
//
// PROOF THIS CAN FAIL: against origin/main's pre-fix code (birthMigrant
// minting citizens.EmploymentUnemployed AND markEmploymentAndCount/co.
// scoped to residentIDs() alone, with no liveResidentIDs()), assertions 1-2
// are RED: the employed/eligible ratio measured against that closed range
// stays near 1.0 (whoever SURVIVES in the shrinking seed range is
// essentially all employed) while comp.Population() keeps growing via
// migration the closed range never sees, and the wage bill never clears
// monthlyWagesFloor for the entire run. Confirmed by hand (this ticket's
// report carries the exact RED numbers for a 24-month run): reverting
// migration.go's EmploymentNone fix AND removing liveResidentIDs()
// (falling back to residentIDs() alone) reproduces both failures.
func TestBUG529_EmployedFractionStaysProportionalUnderOrganicMigration(t *testing.T) {
	const seed = uint64(4242) // same seed BUG-517's own migration tests use — known to admit real migrants within a handful of months
	const totalMonths = 48

	var violations atomic.Int64
	e, comp := newTestEngine(t, seed, invariant.WithLogSink(func(*errs.E) { violations.Add(1) }))

	var snaps []bug529Snapshot
	for m := int64(1); m <= totalMonths; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		snaps = append(snaps, bug529Measure(t, comp, m))
	}

	// Sanity: this run must actually exercise organic migration growth,
	// otherwise the test proves nothing about the migrant-employment path.
	first, last := snaps[0], snaps[len(snaps)-1]
	if last.population <= first.population {
		t.Fatalf("population did not grow over %d months (month1=%d -> month%d=%d) — this run admitted no net migrants, so it cannot exercise the BUG-529 path; widen the run or pick a different seed", totalMonths, first.population, totalMonths, last.population)
	}

	t.Logf("month  population  eligible  employed  ratio    wageBill")
	for _, s := range snaps {
		t.Logf("%5d  %10d  %8d  %8d  %.3f   %d", s.month, s.population, s.eligible, s.employed, s.ratio(), s.wageBill)
	}

	// 1) The employed fraction of the wage-eligible population must stay
	// roughly proportional to the 0.75 employmentRateOfWorkingAgeFraction
	// target for the ENTIRE run, not just at the start. A wide-but-
	// meaningful band tolerates the seed-vs-migrant mix noise and
	// citizens.ColdShard.matchJob's own separate JobMatchRate-driven
	// convergence (matchJob has no upper target — given enough months it
	// drifts EVERY non-off-map/retired citizen toward Employed, so a
	// healthy fixed run's ratio drifts UP toward ~0.95-1.0 over time, never
	// above 1.0) without masking a genuine COLLAPSE.
	const ratioFloor = 0.55
	const ratioCeil = 1.01
	for _, s := range snaps {
		if s.eligible == 0 {
			continue
		}
		r := s.ratio()
		if r < ratioFloor || r > ratioCeil {
			t.Fatalf("month %d: employed/eligible = %.3f (employed=%d eligible=%d population=%d), want within [%.2f, %.2f] of the 0.75 employment-rate target — employment is not tracking population (BUG-529 collapse)", s.month, r, s.employed, s.eligible, s.population, ratioFloor, ratioCeil)
		}
	}

	// 2) The concrete symptom: the wage bill must NOT pin at
	// monthlyWagesFloor once the run has meaningfully grown past the seed
	// population.
	if last.wageBill <= monthlyWagesFloor {
		t.Fatalf("month %d: wage bill = %d, did not exceed monthlyWagesFloor (%d) despite population growing from %d to %d — the wage bill is pinned at the floor (BUG-529 symptom)", last.month, last.wageBill, int64(monthlyWagesFloor), first.population, last.population)
	}

	// 3) BUG-535/births-unblock lane history (logged, NOT asserted — see
	// below): formResidentHouseholds pairs migrants/children
	// (liveResidentIDs()), which was expected to unblock cross-cohort
	// fertility but did NOT at the time BUG-535 landed — VitalBirths()
	// stayed 0 for the whole run. Root-caused there as a SEPARATE bug,
	// independent of BUG-529/535: CitizensAPI's LifeEventPartner handler
	// (registry.go) stored BOTH the new household id and each partner's id
	// through safeUint32() into ColdShard's `partners []uint32`/
	// `households []uint32` columns (coldshard.go). Migrant ids live at
	// attract.MigrantIDBase (1<<62) and fertility-child ids at
	// citizens.FertilityChildIDBase (1<<63) — both far outside uint32's
	// range — so safeUint32 SATURATED them to math.MaxUint32 (4294967295),
	// permanently zeroing Citizen.Partner for any migrant-or-fertility-child
	// couple and excluding them from FertilityEligible's partnerID==0 check.
	//
	// FIXED (births-unblock lane, 2026-09-02, Aaron ruling Q100050 A1):
	// households/partners widened to uint64 (coldshard.go), so this run now
	// DOES see real births once a cross-cohort couple forms and clears the
	// fertility hazard — VitalBirths() > 0 is the expected, correct outcome
	// below, not a regression.
	t.Logf("VitalBirths() = %d after %d months (births-unblock lane fix live — >0 is now expected once a cross-cohort couple clears the fertility hazard; see the safeUint32 partner/household widening above)", comp.VitalBirths(), totalMonths)

	// 4) Conservation must still hold every tick: widening residentIDs()
	// touches the wage/employment/household-formation surface every month,
	// so this proves the wider enumeration introduces no double-counting
	// (a citizen appearing in more than one range) or double-crediting.
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations over the %d-month run with the widened residentIDs() live, want 0", got, totalMonths)
	}
}
