package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
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

	// monthlyConsumptionSpendMicropounds is the LEDGER-facing amount
	// PostHouseholdSpend actually posts each month: the real per-capita
	// utility figure above, posted DIRECTLY at full scale since BUG-452
	// retired ledgerScaleDivisor (flat, not population-scaled — the same
	// documented simplification the pre-existing monthlyWages/monthlyTax
	// stub already uses, pending a real population-scaled balance pass
	// once seedCitizenCount is real-derived).
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

	// monthlyCouncilTaxMicropounds is the LEDGER-facing amount
	// PostCouncilTax actually posts each month, posted DIRECTLY at full
	// scale since BUG-452 retired ledgerScaleDivisor (see
	// monthlyConsumptionSpendMicropounds' doc comment — same treatment).
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
// constraint on the immigration side).
func (st *simState) formResidentHouseholds(month int64) error {
	ids := st.residentIDs()
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
// wealth accrual: every resident citizen that still resolves (departed
// citizens are skipped, never a corruption) gains
// monthlyWageNetPerCitizenMicropounds via LifeEventWealth. "Proportional to
// employment" (the brief's Q5 recommendation) collapses to an equal split
// across every resident today because no compose-owned path ever marks a
// citizen Employed yet (engine.attract's admitted migrants are always
// created Unemployed, and spawnCitizens never sets Employment either) — a
// documented inc1 simplification. TODO(FEAT-1972079927 inc2+): once
// staffing/employment is wired to citizens broadly, weight this by each
// citizen's real Employment.State/Sector instead of splitting evenly.
// citizen.Wealth is a per-citizen data field the conservation invariant
// does not track (see this file's ledger-scale doc comment), so crediting
// it every month at the real per-capita wage figure is safe and does not
// affect StockMoney's conservation check.
func (st *simState) distributeWagesToResidents() error {
	for _, id := range st.residentIDs() {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok {
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
// treasury (Aaron's 2026-08-31 diversify-the-base steer, BUG-391): a flat
// monthlyConsumptionSpendMicropounds (households -> firms, the degenerate
// quantity=1/price=spend PostHouseholdSpend pattern the brief recommends),
// then commercial (VAT-like) + industrial (corp-tax-like) tax on whatever
// was ACTUALLY posted (not the nominal target — a rejected spend post
// leaves nothing to tax, matching PostWages/CollectTax's existing
// all-or-nothing pairing pattern in financeHook). Every leg's success or
// failure is logged loudly (GR#1) rather than silently skipped; a failure
// here never blocks the finance hook's other legs (each Post is
// independently validated/atomic).
func (st *simState) postConsumptionAndTax() (flowed int64) {
	spendPosted, err := st.finance.PostHouseholdSpend(1, finance.Money(monthlyConsumptionSpendMicropounds))
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

	councilPosted, err := st.finance.PostCouncilTax(finance.Money(monthlyCouncilTaxMicropounds))
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "PostCouncilTax", "cause": err.Error()})
	} else {
		flowed = num.SatAdd(flowed, int64(councilPosted))
	}

	return flowed
}
