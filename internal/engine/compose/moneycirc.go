package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-1972079927 "money circulation inc1" wires the composition-root half
// of the FEAT-082 money-circulation brief (docs/planning/
// feat-082-money-circulation-brief.md) per Aaron's 2026-08-31 rulings:
//
//   - Q1 households form MONTHLY from resident citizens — real ASM-247
//     wiring via the SAME LifeEventPartner path engine.attract's admitted
//     migrants already use (formResidentHouseholds below), not a
//     hardcoded "all residents -> household 1" stub.
//   - Q2 engine.attract already calls engine.households' real
//     HousingAffordability internally (attract/api.go's snapshotTerms) —
//     it was only ever starved of a non-nil household-id set and a
//     non-zero rent; applyMigration (compose.go) now supplies both.
//   - Q4 consumption spend: PostHouseholdSpend (households -> firms)
//     monthly, paired with commercial/industrial tax on that spend so the
//     loop closes back to the treasury (see financeHook.ApplyEffect).
//   - Q5 migrant wealth: a deterministic (seeded) log-normal draw, see
//     engine/attract/migration.go's migrantWealth. Ongoing per-citizen
//     wealth accrual (distributeWagesToResidents below) uses the same
//     real-world-grounded wage figure, credited via LifeEventWealth —
//     citizen.Wealth is a per-citizen data field, never posted to
//     engine.finance's ledger (see syncMoneyFromLedger's doc comment: the
//     conservation invariant only tracks the AGGREGATE AcctHouseholds
//     ledger balance), so it is safe to credit at full real-world scale
//     regardless of baseline-one's much smaller toy treasury.
//   - Q3 construction is PARKED for inc1: this file never calls
//     finance.SettleConstruction. TODO(FEAT-1972079927 inc2): wire it once
//     firms supply/price construction materials (the firms->build
//     materials-supply+pricing edge, GR#25 — that edge is not registered
//     in code.json today, so inc1 must not add the call).
//
// --- Ledger scale vs real-world scale ---
//
// docs/planning/money-numbers-real-world.md gives real UK-grounded ABSOLUTE
// figures (utility spend, council tax, gross wage). Those figures were
// sized against a REAL reference city (~937 citizens, a treasury on the
// order of millions of pounds) — pre-BUG-452, baseline-one's actual seed
// (seedCitizenCount=64, initialTreasury=£10, initialCitizenWealth=£5) was
// three-plus orders of magnitude smaller, so posting the full real-world
// absolute amounts straight onto that toy ledger would have
// overdraft-rejected every transaction from month 1 (engine.finance's
// RoleMoney accounts never go negative) and the loop would never visibly
// circulate — hence the original ledgerScaleDivisor=1_000 hack (see git
// history for this file pre-2026-09-01).
//
// BUG-452 (2026-09-01, Aaron's ruling: "retire the divisor hack entirely")
// removes ledgerScaleDivisor: initialTreasury is now £1,500,000
// (compose.go) — three-plus orders of magnitude larger than the old £10
// toy seed — so every LEDGER-facing figure below (PostHouseholdSpend/
// PostCouncilTax's monthly amounts) is now posted at its FULL real-world
// value directly, no division. Figures that are NOT ledger-facing — the
// rent figure attract feeds into HousingAffordability's math (a formula
// input, never transferred), and per-citizen Wealth crediting (a data
// field the conservation invariant does not track) — already used the
// full real-world value before this change and are unaffected except for
// the base-unit rebase itself (internal/foundation/det/money.go's
// MicropoundsPerPound doc comment: 1e-6 GBP/unit -> 1e-3 GBP/unit,
// 2026-09-01) applied uniformly to every raw literal in this file.
// TaxRates (basis points) are scale-invariant and always use the real UK
// rate, unaffected by either change.
const (
	// monthlyUtilitySpendPerCapitaMicropounds is the real UK per-capita
	// monthly utility spend (docs/planning/money-numbers-real-world.md §1,
	// ~£55/person/month) — real-world-grounded, balance-pass adjustable.
	// BUG-452 (2026-09-01): rebased 55_000_000 -> 55_000 for the money
	// base-unit change (same real £55), and is now posted AT this full
	// value (ledgerScaleDivisor deleted — see doc comment above).
	monthlyUtilitySpendPerCapitaMicropounds = 55_000

	// monthlyConsumptionSpendMicropounds is the PER-HOUSEHOLD price
	// postConsumptionAndTax multiplies by the current household count
	// (FEAT-083, 2026-09-02) — the real per-capita utility figure above,
	// posted at full scale since BUG-452 retired ledgerScaleDivisor. Was a
	// flat, non-population-scaled quantity=1 post before FEAT-083; see
	// postConsumptionAndTax's doc comment for the household-vs-population
	// scaling call flagged for Aaron.
	monthlyConsumptionSpendMicropounds = monthlyUtilitySpendPerCapitaMicropounds

	// baselineOneMonthlyRentPerHousehold is the real UK Kent/Folkestone
	// average monthly rent per household (docs/planning/
	// money-numbers-real-world.md §2, ~£1,000/household/month) —
	// real-world-grounded, balance-pass adjustable. BUG-452 (2026-09-01):
	// rebased 1_000_000_000 -> 1_000_000 for the money base-unit change
	// (same real £1,000).
	//
	// Pre-BUG-452, posting this REAL absolute figure directly was tried
	// and caused a catastrophic emigration collapse (population 64->4 over
	// 12 months, this package's own regression tests) — documented in this
	// file's git history — because baseline-one's per-household income
	// (attract/api.go's snapshotTerms: WagesPosted()/len(HouseholdIDs), on
	// compose.go's monthlyWages stub) was, at the OLD toy scale
	// (monthlyWages=£1/month), three-plus orders of magnitude smaller than
	// this real rent, so rent >> income for every household every month —
	// RentBurdenOf's ratio pegged above 1.0 unconditionally.
	//
	// BUG-452 (2026-09-01, Aaron's ruling: "re-verify the rent-figure
	// emigration-collapse failure mode empirically at the new
	// treasury+scale"): RE-TRIED against the new £1,500,000 treasury and
	// the ALSO-rescaled monthlyWages (compose.go's finance stub, rescaled
	// 150,000x alongside initialTreasury — see compose.go's doc comment)
	// and the collapse did NOT recur (empirically verified via a temporary
	// probe test driving a real 12-month composed run: population grew
	// 64->96, fluctuating 85-107, HousingAffordability oscillating 0/100
	// month to month rather than pinned, NetMigration staying
	// predominantly positive throughout — see this file's commit history
	// for the probe). The real rent:income ratio at the new scale (£1,000
	// rent vs £150,000 monthlyWages) is ~0.67%, comfortably under
	// RentBurdenOf's 35% threshold for most months, so the flat
	// hand-tuned placeholder this file previously carried
	// (baselineOneMonthlyRentMicropounds, since RETIRED) is no longer
	// needed: this constant is now POSTED DIRECTLY (see
	// monthlyRentForHouseholds below) — one real number, not a
	// hand-tuned proxy for it.
	baselineOneMonthlyRentPerHousehold = 1_000_000

	// councilTaxPerCapitaMicropounds is the real UK Band-D-Folkestone
	// council tax per resident (docs/planning/money-numbers-real-world.md
	// §5, ~£47/person/month) — real-world-grounded, balance-pass
	// adjustable. BUG-452 (2026-09-01): rebased 47_000_000 -> 47_000 for
	// the money base-unit change (same real £47).
	councilTaxPerCapitaMicropounds = 47_000

	// monthlyCouncilTaxMicropounds is the PER-HOUSEHOLD price
	// postConsumptionAndTax multiplies by the current household count
	// (FEAT-083, 2026-09-02 — UK council tax is genuinely billed per
	// dwelling/household, so this leg's household-scaling is the MORE
	// correct of the two, unlike the consumption-spend leg above). Was a
	// flat post before FEAT-083; see postConsumptionAndTax's doc comment.
	monthlyCouncilTaxMicropounds = councilTaxPerCapitaMicropounds

	// commercialTaxRateBp is the "commercial" leg of Aaron's diversify-the-
	// base steer (BUG-391): a UK-standard-VAT-rate-grounded (20%) tax on
	// household consumption spend, paid by firms — basis points are
	// scale-invariant, so this is the real UK rate unscaled.
	commercialTaxRateBp = 2000

	// industrialTaxRateBp is the "industrial" leg: a UK-corporation-tax-
	// rate-grounded (25%, the 2023+ main rate) tax on firm revenue — the
	// same consumption-spend base doubles as the profit proxy (engine.firms
	// has no per-firm P&L tracking yet at baseline-one; a documented
	// placeholder, not a literal profit figure).
	industrialTaxRateBp = 2500

	// monthlyWageGrossPerEmployedMicropounds is the real UK/Kent gross
	// monthly wage per employed adult (docs/planning/
	// money-numbers-real-world.md §4, ~£2,100/month) — real-world-
	// grounded, balance-pass adjustable. Feeds per-citizen Wealth
	// crediting only (never the ledger), so it is used at full scale.
	// BUG-452 (2026-09-01): rebased 2_100_000_000 -> 2_100_000 for the
	// money base-unit change (same real £2,100).
	monthlyWageGrossPerEmployedMicropounds = 2_100_000

	// incomeNITaxRateBp is the UK income-tax + National-Insurance blended
	// rate (docs/planning/money-numbers-real-world.md §4, ~28%) —
	// scale-invariant, real UK rate.
	incomeNITaxRateBp = 2800

	// monthlyWageNetPerCitizenMicropounds is the take-home figure
	// distributeWagesToResidents credits: gross minus the blended
	// income/NI rate above (docs/planning/money-numbers-real-world.md §4's
	// own precomputed net, £1,512/month).
	monthlyWageNetPerCitizenMicropounds = monthlyWageGrossPerEmployedMicropounds -
		(monthlyWageGrossPerEmployedMicropounds * incomeNITaxRateBp / 10_000)
)

// --- FEAT-083 finance de-stub: minimal employment marking ---
//
// The pre-existing wage/tax legs (compose.go's old monthlyWages/monthlyTax
// flat £150,000/month stub) never scaled with population, and
// distributeWagesToResidents even-split the wage across EVERY resident
// because no compose-owned path ever set Employment.State (see this
// file's original doc comment on that function, retired below). This is
// the MINIMAL employment-marking scope Aaron authorized directly
// (2026-08-31/09-01 rulings): NOT the full jobs model (that is the
// deferred FEAT-1972079929) — just enough to give the money loop a real,
// population-scaled employed/unemployed split.
//
// citizens.LifeEventEmployment + CitizensAPI.ApplyLifeEventCommand
// ALREADY EXIST (citizens/registry.go) as the registered command surface
// for mutating Employment.State/Sector — this package uses that surface
// exclusively (SEC-020 copyguard: no reaching into Citizen fields
// directly), the same pattern formResidentHouseholds/
// distributeWagesToResidents already use for LifeEventPartner/
// LifeEventWealth.
//
// citizens.ColdShard.matchJob (coldpass.go) is a SEPARATE, pre-existing,
// citizens-owned employment-transition mechanism gated by
// ColdPassParams.JobMatchRate (workingAge-fraction-of-sample derived).
// It is NOT reused here for two reasons, both DOCUMENTED FINDINGS flagged
// for Aaron rather than silently worked around: (1) it only fires for
// COLD (non-elevated) citizens (applyMonthly's hot-citizen skip), and (2)
// far more significantly, EVERY citizen this engine mints today — seed
// population (compose.go's spawnCitizens) AND admitted migrants
// (attract/migration.go's applyImmigration) — is created with
// BirthMonth = the CURRENT sim month, i.e. every citizen is a literal
// newborn at creation. Gating employment on any realistic UK working-age
// floor (16+ years = 192+ months) would therefore pin EVERY employment
// draw (this package's own, and matchJob's) at "child" for the entire
// observable game/test horizon (baseline-one's tests run 12-400 months =
// 1-33 years) — the money loop would never visibly scale. This is a real,
// separate gap (no citizen is ever minted pre-aged into adulthood) that
// this ticket does not fix; workingAgeMinMonths below is set to 0 as an
// explicit, documented interim placeholder pending that fix, so the wage
// bill is observable NOW rather than dormant for decades of sim-time.
const (
	// workingAgeMinMonths is the floor age (in months) below which a
	// resident is never drawn for employment (EmploymentNone, "child").
	// GROUNDED FIGURE WOULD BE 192 (UK compulsory-education-leaving age,
	// 16 years) but is set to 0 here — see the doc comment above: every
	// citizen in this engine is minted at age 0, so a real 16-year floor
	// would leave employment (and hence wages) at zero for the entire
	// observable baseline-one horizon. FLAGGED for Aaron: the correct fix
	// is minting seed/migrant citizens with a realistic age distribution,
	// not lowering this floor — tracked as a follow-up, not solved here.
	workingAgeMinMonths = 0

	// retirementAgeMonths is the UK State Pension age (~66 years,
	// docs/planning/money-numbers-real-world.md's UK-grounded figures
	// share this basis) — real-world-grounded, balance-pass adjustable.
	// A resident at or beyond this age is marked EmploymentRetired and
	// never redrawn. Given every citizen starts at age 0 (see above), this
	// only bites in very long runs (33+ years / 400+ months), but is kept
	// at its real value since it costs nothing to be correct here.
	retirementAgeMonths = 66 * 12

	// employmentRateOfWorkingAgeFraction is the UK ONS employment rate for
	// the working-age population (~75%, ONS labour-market statistics,
	// 16-64 age band) — real-world-grounded, balance-pass adjustable. A
	// working-age resident not yet decided (EmploymentNone/EmploymentStudent,
	// never EmploymentEmployed/EmploymentUnemployed/EmploymentOffMap —
	// see employmentDecision's doc comment) draws Employed against this
	// fraction, Unemployed otherwise.
	employmentRateOfWorkingAgeFraction = 0.75

	// employedSectorPlaceholder is the sector newly-Employed residents are
	// assigned: UK employment is ~80% services (ONS sector-share
	// statistics) — real-world-grounded, balance-pass adjustable. A single
	// sector for every employed resident is the minimal-scope
	// simplification; per-sector distribution is part of the deferred
	// FEAT-1972079929 full jobs model.
	employedSectorPlaceholder = citizens.SectorTertiary
)

// employmentDecision draws ONE resident's Employed/Unemployed verdict,
// deterministically and PERMANENTLY (keyed (seed, id, "employment-marking")
// with a FIXED month argument of 0 — never the calendar month) so the
// decision, once made, never flip-flops from one month to the next the
// way a per-month re-draw would. It is a pure function (no citizens
// lookup), which lets tests predict the exact employed/unemployed split
// for a known seed and id set WITHOUT running any ticks (GR#15 — derive
// expected values from the same formula production uses, never a
// hand-picked hardcoded count).
func employmentDecision(seed, id uint64) (citizens.EmploymentState, citizens.Sector) {
	stream := det.NewStream(seed, id, 0, "employment-marking")
	if stream.Float64() < employmentRateOfWorkingAgeFraction {
		return citizens.EmploymentEmployed, employedSectorPlaceholder
	}
	return citizens.EmploymentUnemployed, citizens.SectorNone
}

// desiredEmployment is the pure age/current-state decision table
// markEmploymentAndCount applies to one resident: EmploymentNone below
// workingAgeMinMonths, EmploymentRetired at/above retirementAgeMonths,
// the current state unchanged if already
// Employed/Unemployed/EmploymentOffMap (never redrawn/flapped/overwritten
// — EmploymentOffMap in particular already has a real off-map job,
// engine.extcommute, see citizens/types.go's EmploymentOffMap doc
// comment), or a fresh employmentDecision draw otherwise. Extracted as a
// pure function (age and current state as plain arguments, no CitizensAPI
// lookup) so it is directly unit-testable against synthetic ages without
// needing to advance CitizensAPI's internal clock hundreds of simulated
// months to observe the retirement/off-map branches.
func desiredEmployment(seed, id uint64, age int64, cur citizens.Employment) (citizens.EmploymentState, citizens.Sector) {
	switch {
	case age < workingAgeMinMonths:
		return citizens.EmploymentNone, cur.Sector
	case age >= retirementAgeMonths:
		return citizens.EmploymentRetired, cur.Sector
	case cur.State == citizens.EmploymentEmployed, cur.State == citizens.EmploymentUnemployed, cur.State == citizens.EmploymentOffMap:
		return cur.State, cur.Sector
	default:
		return employmentDecision(seed, id)
	}
}

// markEmploymentAndCount is FEAT-083's minimal employment-marking pass:
// for every resident, it decides EmploymentNone (child, age <
// workingAgeMinMonths), EmploymentRetired (age >= retirementAgeMonths), or
// — for a working-age resident not yet decided — a one-time
// employmentDecision draw, applied through CitizensAPI's registered
// LifeEventEmployment command (never a direct field write, SEC-020). A
// resident already EmploymentEmployed/EmploymentUnemployed/EmploymentOffMap
// is left untouched (no re-draw, no flapping); EmploymentOffMap in
// particular already has a real off-map job (engine.extcommute, see
// citizens/types.go's EmploymentOffMap doc comment) and must never be
// overwritten by this on-map-only marking. Returns the resulting Employed
// count AND, of those, the count already sitting in citizens.SectorPublic
// (BUG-548's public/private wage split — see financeHook.ApplyEffect),
// counted in the SAME pass so the wage bill and the marking can never
// observe two different snapshots of the same month.
//
// employedSectorPlaceholder (this file's const block) always assigns
// SectorTertiary to a resident freshly decided BY THIS PASS, so
// markEmploymentAndCount itself never mints a new SectorPublic assignment
// — but employedPublic is not always 0: citizens.ColdShard's separate,
// pre-existing matchJob (coldpass.go) independently draws a sector
// (uniformly SectorPrimary..SectorPublic) for cold citizens well before
// this pass ever runs, so a real, if usually small, public headcount can
// already exist on a resident this pass merely leaves untouched (the
// "already decided" branch below). A DEDICATED public-sector assignment
// path (e.g. engine.staffing, not yet wired into compose — see
// staffing/api.go's SectorPublic assignment) would make the split more
// load-bearing still, once wired. `sector` here is desiredEmployment's
// returned sector for EVERY branch (including the "already decided,
// unchanged" branch, which returns cur.Sector), so it always reflects the
// resident's CURRENT actual sector, never the employmentDecision draw
// alone.
//
// BUG-529: iterates liveResidentIDs() (compose.go), which enumerates the
// FULL live population (seed + migrants + fertility children), not just the
// closed seed cohort residentIDs() alone stays scoped to — see
// liveResidentIDs' doc comment for why a migrant/child was previously
// invisible to this pass regardless of its Employment.State.
func (st *simState) markEmploymentAndCount(month int64) (employed int, employedPublic int, err error) {
	for _, id := range st.liveResidentIDs() {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok {
			continue // departed — not a corruption, just skip
		}
		desired, sector := desiredEmployment(st.seed, id, cit.Age(), cit.Employment)
		cur := cit.Employment.State
		if desired != cur {
			if applyErr := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
				CorrelationID: st.cid,
				Kind:          citizens.LifeEventEmployment,
				CitizenID:     id,
				Employment:    desired,
				Sector:        sector,
			}); applyErr != nil {
				return employed, employedPublic, errs.Wrap(ErrModuleFailed, st.cid, applyErr, map[string]any{"module": "citizens", "op": "markEmploymentAndCount", "id": id, "month": month})
			}
		}
		if desired == citizens.EmploymentEmployed {
			employed++
			if sector == citizens.SectorPublic {
				employedPublic++
			}
		}
	}
	return employed, employedPublic, nil
}

// employedResidentCount is a read-only re-count of the current Employed
// resident set (used by observability/tests; markEmploymentAndCount
// already returns this count for the production hot path, so this helper
// avoids a second full pass there — it exists for callers that only need
// the count, not the marking side-effect, e.g. tests reading state AFTER
// a tick has already run markEmploymentAndCount for that month).
func (st *simState) employedResidentCount() int {
	n := 0
	for _, id := range st.liveResidentIDs() {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok {
			continue
		}
		if cit.Employment.State == citizens.EmploymentEmployed {
			n++
		}
	}
	return n
}

// monthlyRentForHouseholds is FEAT-1972079927 Q2's rent figure. BUG-452
// (2026-09-01) retired the old flat hand-tuned placeholder
// (baselineOneMonthlyRentMicropounds) once empirical re-verification (see
// baselineOneMonthlyRentPerHousehold's doc comment) proved the real
// absolute rent no longer collapses emigration at the new treasury/wage
// scale — this now posts the REAL figure directly, still a flat constant
// (not recomputed from income) rather than a proportion, per Q2's
// original rejected-alternative-#2 finding (a fixed proportion of income
// makes RentBurdenOf's verdict constant BY CONSTRUCTION, exactly as frozen
// as the bug Q2 exists to fix). Zero households (nothing formed yet)
// still yields a rent figure, but households.HousingAffordability's own
// total==0 branch reads a zero-household city as fully affordable
// regardless, so this is safe.
func (st *simState) monthlyRentForHouseholds(householdIDs []uint64) int64 {
	return baselineOneMonthlyRentPerHousehold
}

// formResidentHouseholds is FEAT-1972079927 Q1's monthly household
// formation: every resident citizen with no household yet (Household==0)
// is paired, sequentially in ascending id order (GR#21 — never a map
// range), into a real 2-member household via CitizensAPI's own
// LifeEventPartner command — the EXACT registered path
// attract.applyImmigration already uses for admitted migrants (household.go's
// FormHousehold), so this is real ASM-247 wiring, not a synthetic
// "household 1" stand-in. A citizen who has already partnered (from a
// prior month, or as an admitted migrant) is skipped. An odd resident left
// over this month is simply unpaired until a new resident joins them next
// month — a documented consequence of pairing being the only
// household-formation primitive engine.citizens exposes (see
// attract/migration.go's migrantHouseholdSize doc comment for the same
// constraint on the immigration side). Iterates the FULL liveResidentIDs()
// (BUG-529/BUG-535: seed + migrants + fertility children, see that
// function's doc comment) rather than residentIDs()'s seed-only range; a
// migrant already has a household from admission (applyImmigration's own
// LifeEventPartner call) so this is a no-op for them in practice, but a
// fertility-born child (previously never in this loop at all) can now be
// paired once one exists without a household — this is what unblocks
// BUG-535's "births never happen because Partner stays 0" finding.
func (st *simState) formResidentHouseholds(month int64) error {
	ids := st.liveResidentIDs()
	unpaired := make([]uint64, 0, len(ids))
	for _, id := range ids {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok {
			continue // departed (death/emigration) — not a corruption, just skip
		}
		if cit.Household == 0 {
			unpaired = append(unpaired, id)
		}
	}
	for i := 0; i+1 < len(unpaired); i += 2 {
		if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: st.cid,
			Kind:          citizens.LifeEventPartner,
			CitizenID:     unpaired[i],
			PartnerID:     unpaired[i+1],
		}); err != nil {
			return errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "citizens", "op": "formResidentHouseholds", "month": month})
		}
	}
	return nil
}

// distributeWagesToResidents is FEAT-1972079927 Q5's ongoing per-citizen
// wealth accrual: every resident citizen currently EmploymentEmployed
// (departed citizens are skipped, never a corruption) gains
// monthlyWageNetPerCitizenMicropounds via LifeEventWealth. FEAT-083
// (2026-09-02) closes the Q5 TODO this doc comment used to carry — "weight
// this by each citizen's real Employment.State" — now that
// markEmploymentAndCount (financeHook.ApplyEffect, called earlier the same
// month) actually marks residents Employed/Unemployed: an unemployed
// resident receives nothing, matching real-world wage income. citizen.Wealth
// is a per-citizen data field the conservation invariant does not track
// (see this file's ledger-scale doc comment), so crediting it every month
// at the real per-capita wage figure is safe and does not affect
// StockMoney's conservation check.
//
// creditPrivateSector (BUG-548 fix #4, 2026-09-05) couples this per-citizen
// accounting to the LEDGER: the round's independent Destructive attack
// (TestBUG548Attack_CreditLineExhaustion_FailureModeIsBoundedButSilent)
// found firms posting ZERO wages to the ledger while every employed
// citizen still had Wealth credited here regardless — a payroll failure
// invisible to the citizen view. When false (financeHook.ApplyEffect's
// PostWagesFromFirms rejected this month's private-sector bill), every
// resident NOT in citizens.SectorPublic is skipped entirely — a citizen
// must never receive Wealth for a wage the ledger did not actually pay.
// Public-sector residents are unaffected by this gate: their wage is paid
// via PostWages (treasury), a separate leg this ticket does not attack.
func (st *simState) distributeWagesToResidents(creditPrivateSector bool) error {
	for _, id := range st.liveResidentIDs() {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok {
			continue
		}
		if cit.Employment.State != citizens.EmploymentEmployed {
			continue
		}
		if !creditPrivateSector && cit.Employment.Sector != citizens.SectorPublic {
			// The ledger did not actually pay this citizen's wage this
			// month (firms' working-capital line rejected the post) —
			// never credit Wealth for money that was never posted.
			continue
		}
		newWealth := num.SatAdd(cit.Wealth, monthlyWageNetPerCitizenMicropounds)
		if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: st.cid,
			Kind:          citizens.LifeEventWealth,
			CitizenID:     id,
			Wealth:        newWealth,
		}); err != nil {
			return errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "citizens", "op": "distributeWagesToResidents"})
		}
	}
	return nil
}

// postConsumptionAndTax is FEAT-1972079927 Q4's monthly household spend and
// the commercial/industrial tax legs that close the loop back to the
// treasury (Aaron's 2026-08-31 diversify-the-base steer, BUG-391): FEAT-083
// (2026-09-02) replaced the flat quantity=1 stub with household-count
// scaling — quantity=len(HouseholdIDs), price=the real per-capita figure
// (households -> firms) — then commercial (VAT-like) + industrial
// (corp-tax-like) tax on whatever was ACTUALLY posted (not the nominal
// target — a rejected spend post leaves nothing to tax, matching
// PostWages/CollectTax's existing all-or-nothing pairing pattern in
// financeHook). Every leg's success or failure is logged loudly (GR#1)
// rather than silently skipped; a failure here never blocks the finance
// hook's other legs (each Post is independently validated/atomic).
//
// FLAGGED FOR AARON (a genuine balance/design call, not solved here):
// scaling a PER-CAPITA figure (monthlyConsumptionSpendMicropounds/
// councilTaxPerCapitaMicropounds are both documented "per resident") by
// HOUSEHOLD count rather than population count under-states the true
// city-wide total by roughly the average household size (~2x, formed
// households pair 2 residents — moneycirc.go's formResidentHouseholds).
// UK council tax is genuinely billed per DWELLING (so household-count
// scaling is arguably MORE correct for that leg specifically), but
// household-scaling a per-capita utility-spend figure is a coarser
// approximation; population-count scaling was the alternative considered.
// Following this ticket's brief (household-count scaling for both legs)
// as the documented placeholder pending Aaron's balance-pass call.
//
// outputScalePerMille is BUG-745's fix (compose.go's financeHook.ApplyEffect
// doc comment, resolveFirmsMonth below): engine.firms' city-wide
// AggregateOutputScale, read fresh THIS month before this leg runs. It
// scales the household-spend price actually posted — a productivity/input
// shortfall means firms have less to sell, so less spend clears — which
// then flows straight through into the commercial/industrial tax legs
// below (both computed on the ACTUAL spendPosted, never the nominal
// target, exactly as they already were pre-BUG-745). Council tax is a
// PER-DWELLING civic levy, not a firm-output-funded transaction, so it is
// deliberately left UNSCALED. 1000 (full output) is a no-op
// (scaleByOutputPerMille's doc comment), so an unmodified/no-firms city
// posts byte-identical to the pre-BUG-745 figure.
func (st *simState) postConsumptionAndTax(outputScalePerMille int64) (flowed int64) {
	households := int64(len(st.citizens.HouseholdIDs(st.cid)))
	if households <= 0 {
		// No households formed yet this month (should not happen once
		// formResidentHouseholds has run — PhasePopulation precedes
		// PhaseFinance — but degenerate-city defense-in-depth keeps the
		// loop from freezing at a permanent zero rather than a transient
		// one): fall back to a single household-equivalent.
		households = 1
	}

	scaledSpendPrice := scaleByOutputPerMille(monthlyConsumptionSpendMicropounds, outputScalePerMille)
	spendPosted, err := st.finance.PostHouseholdSpend(households, finance.Money(scaledSpendPrice))
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "PostHouseholdSpend", "cause": err.Error()})
		spendPosted = 0
	} else {
		flowed = num.SatAdd(flowed, int64(spendPosted))
	}

	if spendPosted > 0 {
		receipts, err := st.finance.CollectTax(finance.TaxRates{
			SalesRate: commercialTaxRateBp,
			CorpRate:  industrialTaxRateBp,
		}, 0, spendPosted, spendPosted)
		if err != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "CollectTax.commercial+industrial", "cause": err.Error()})
		} else {
			flowed = num.SatAdd(flowed, int64(receipts.Sales))
			flowed = num.SatAdd(flowed, int64(receipts.Corp))
		}
	}

	councilPosted, err := st.finance.PostCouncilTax(finance.Money(households * monthlyCouncilTaxMicropounds))
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "PostCouncilTax", "cause": err.Error()})
	} else {
		flowed = num.SatAdd(flowed, int64(councilPosted))
	}

	return flowed
}

// resolveFirmsMonth is BUG-745's seam. Before this fix, engine.firms'
// FirmsAPI.ResolveMonth — firms' own "monthly failure/churn resolution"
// (lifecycle.go's doc comment) — was NEVER called from production compose
// (only from firms' own package tests): Financial.OutputScale (AC-8's
// market-input-availability scale) was computed nowhere in a real run, so
// it sat pinned at its founding default (1000) forever, and even where a
// test drove it directly, its only reader was ResolveMonth's own
// credit-failure check. This runs ResolveMonth once per month from the
// EXISTING PhaseFinance financeHook tick (no new phase hook — see
// compose.go's financeHook.ApplyEffect call site) using the same clock
// month already threaded through markEmploymentAndCount, then reads the
// resulting city-wide FirmsAPI.AggregateOutputScale so
// postConsumptionAndTax can finally scale the household-spend revenue
// firms receive by it (compose.go's financeHook.ApplyEffect deliberately
// does NOT also scale the private wage bill by this — see that call
// site's own doc comment for the destructive credit-line/emigration
// cascade scaling BOTH legs was measured to trigger).
//
// No new cross-module edge: engine.compose already consumes engine.firms
// (code.json's feat.compositionroot -> engine.firms outbound edge,
// exercised today via RegisterFirm/LabourMarket/SetCitizens etc.) —
// ResolveMonth and AggregateOutputScale are new METHODS called over that
// existing edge, not a new edge.
//
// Blast-radius note (verified against every firm compose registers today,
// 2026-09-05): ResolveMonth's credit-failure branch only fires for a
// Startup/Small firm with CreditOutstanding > 0, and CreditOutstanding is
// set exclusively by firms' own credit.go ApproveCredit path — compose
// never calls it for the builders'-merchant or freight stage firms it
// registers, so CreditOutstanding is always 0 for them and the failure
// branch never fires from this call. applyInputScalingLocked (the
// OutputScale side) is a no-op (pins 1000) whenever InputRequired <= 0,
// which every RegisterFirm(..., staff, ...) test call in this package uses
// staff=0 for — so this call is a genuine no-op against the existing test
// estate; only a firm with real staff and a real market wired (the
// builders'-merchant auto-placement) can ever see a non-1000 scale, and
// only if its InputCommodity's market capacity is actually short.
//
// A missing firms handle (a stub-engine test path with no FirmsAPI wired)
// or either call failing both degrade to the documented NEUTRAL scale 1000
// (GR#17 — a failed read must never fabricate a productivity collapse the
// city never actually suffered), logged loudly (GR#1) rather than silently
// swallowed.
func (st *simState) resolveFirmsMonth(month int64) int64 {
	if st.firms == nil {
		return 1000
	}
	if err := st.firms.ResolveMonth(month); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "firms", "op": "ResolveMonth", "cause": err.Error()})
		return 1000
	}
	scale, err := st.firms.AggregateOutputScale()
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "firms", "op": "AggregateOutputScale", "cause": err.Error()})
		return 1000
	}
	return scale
}

// scaleByOutputPerMille applies a per-mille output-scale factor (BUG-745:
// engine.firms' AggregateOutputScale) to a money amount that funds FROM
// firm output — the household-spend revenue firms receive
// (postConsumptionAndTax) and the private-sector wage bill firms pay
// (compose.go's financeHook). scalePerMille is clamped to [0,1000]
// defensively (an out-of-range value from a future caller must degrade
// toward the documented neutral bound, never overflow or invert the sign,
// GR#16) — 1000 (full output) is a strict no-op: amount*1000/1000 ==
// amount exactly, so a neutral scale never perturbs the pre-BUG-745 figure
// by even one micropound (the x1.0 regression's byte-identical
// requirement). The intermediate product is a saturating multiply (GR#16),
// guarded before the division; an overflow falls back to the unscaled
// amount rather than a corrupted saturated product divided by 1000 — the
// same "degrade to the neutral/no-op reading, never a silently wrong
// number" discipline as resolveFirmsMonth's own error paths.
func scaleByOutputPerMille(amount, scalePerMille int64) int64 {
	if scalePerMille < 0 {
		scalePerMille = 0
	}
	if scalePerMille > 1000 {
		scalePerMille = 1000
	}
	product, overflowed := num.SafeMul(amount, scalePerMille)
	if overflowed {
		return amount
	}
	return product / 1000
}
