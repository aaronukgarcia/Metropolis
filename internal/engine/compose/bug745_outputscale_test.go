package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-745 (P2, money circulation): engine.firms' Financial.OutputScale
// (AC-8's market-input-availability scale, set by FirmsAPI.ResolveMonth's
// applyInputScalingLocked) had exactly one reader before this fix —
// ResolveMonth's own credit-failure check — and ResolveMonth itself was
// never called from production compose at all. Forcing a firm's output to
// a fraction of full for 24 months left the treasury, tracked citizen
// wealth, and population hash byte-identical to a run with no shortfall
// whatsoever. The fix (moneycirc.go's resolveFirmsMonth/
// scaleByOutputPerMille, wired from compose.go's financeHook.ApplyEffect)
// finally routes engine.firms' AggregateOutputScale into the household-
// spend revenue firms receive (postConsumptionAndTax; the private wage
// bill is deliberately NOT also scaled — see compose.go's own doc comment
// on that leg for the destructive credit-line/emigration cascade that was
// measured to trigger) — these tests prove it moved, prove it did not
// move anything at the documented neutral (1000), and record the exact
// direction the move takes.

// registerFirmWithInputRequired registers one real firm (via the same
// FirmRegistrar seam engine.freight already uses, firms.go's RegisterFirm)
// on comp's own wired FirmsAPI/MarketAPI, with InputRequired set to
// exactly the given value — RegisterFirm's staff argument IS
// Firm.InputRequired (firms.go's RegisterFirm), so no unexported-field
// reach-in is needed from this package.
func registerFirmWithInputRequired(t *testing.T, comp *Composition, inputRequired int64) {
	t.Helper()
	st := comp.state
	if st.firms == nil {
		t.Fatal("firms not wired — fixture is vacuous")
	}
	if _, err := st.firms.RegisterFirm("bug745-test-firm", inputRequired, "industrial"); err != nil {
		t.Fatalf("RegisterFirm(inputRequired=%d): %v", inputRequired, err)
	}
}

// constructionMaterialsCapacity reads the market's ConstructionMaterials
// capacity ceiling — the same "request far more than the ceiling to learn
// it" pattern firms/integration_test.go's
// TestMarketInputShortfallConstrainsGrowth already uses. market.go's
// Availability is a pure function of the loaded ceiling (never a
// running/depleting balance — see market.go's Availability doc comment),
// so the ceiling this returns is CONSTANT for the life of the run: a
// firm's InputRequired set as a multiple of it produces an OutputScale
// that never drifts month to month, a stable fixture rather than a
// one-off draw.
func constructionMaterialsCapacity(t *testing.T, comp *Composition) int64 {
	t.Helper()
	st := comp.state
	if st.market == nil {
		t.Fatal("market not wired — fixture is vacuous")
	}
	avail, err := st.market.Availability(market.ConstructionMaterials, 1<<40)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if avail.Available <= 0 {
		t.Fatal("expected a positive ConstructionMaterials capacity ceiling")
	}
	return avail.Available
}

// runBUG745Fixture composes a fresh engine at the given seed, registers
// ONE firm sized to the given InputRequired multiple of the real
// ConstructionMaterials capacity, advances 24 months, and returns the
// three observables this bug is about. Every comparison this file draws
// registers EXACTLY ONE firm in both arms (only the multiple differs) —
// merely registering a firm (regardless of its InputRequired) changes
// engine.firms' TotalVacancies (LabourMarket's headroom side, firms.go's
// bandCeiling − len(Staff), and RegisterFirm never populates Staff — see
// firms.go's RegisterFirm), which feeds servicesfirms_wire.go's
// jobAvailabilityTerm into attract's migration arithmetic: a "no firm at
// all" vs "one firm" comparison was tried first and measured to diverge
// hugely in population/wealth from THAT confound alone, long before
// OutputScale's own contribution — a genuinely misleading comparison for
// this bug's own finding. Holding "exactly one firm, same band/stage"
// constant and varying ONLY the InputRequired ratio isolates OutputScale
// as the single remaining variable.
func runBUG745Fixture(t *testing.T, seed uint64, capacityMultiple int64) (treasury, wealth int64, popHash [32]byte) {
	t.Helper()
	e, comp := newTestEngine(t, seed)
	capacity := constructionMaterialsCapacity(t, comp)
	registerFirmWithInputRequired(t, comp, capacity*capacityMultiple)
	for month := 1; month <= 24; month++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", month, err)
		}
	}
	return comp.Treasury(), comp.CitizenWealth(), comp.PopulationHash()
}

// TestBUG745_HalfOutput_MovesTreasuryAndCitizenWealth is the core finding's
// regression: a firm running at HALF output (InputRequired == 2x the real
// market capacity, so applyInputScalingLocked's own AC-8 formula —
// capacity*1000/required — computes exactly 500) for 24 months must leave
// the treasury and tracked citizen wealth MEASURABLY DIFFERENT from an
// otherwise-identical run whose ONLY difference is a firm running at FULL,
// unconstrained output (InputRequired == exactly the capacity, scale
// 1000). Pre-fix, OutputScale's only reader was ResolveMonth's own
// credit-failure check, so this pair was byte-identical regardless of how
// starved the firm was.
//
// Direction, derived from the ledger legs this fix touches (moneycirc.go's
// postConsumptionAndTax doc comment): half output halves the scaled
// household-spend price actually posted, which (a) halves the
// commercial+industrial tax computed on that reduced spend — LESS money
// reaches the treasury — and (b) means households are debited LESS for
// that same reduced spend — tracked citizen wealth (AcctHouseholds) is
// HIGHER, not lower, than the full-output run (a real, if unintuitive,
// consequence of "less is transacted", not a defect — flagged the same
// way moneycirc.go's own household-vs-population-scaling finding is
// flagged for Aaron's balance pass). The private-wage-bill leg this fix
// also scales does NOT move this pair's citizen wealth: at baseline-one's
// seed population the nominal private bill sits below monthlyWagesFloor
// regardless of scale (48-ish employed x the real gross wage is already
// below the floor even unscaled), so the unconditional floor backstop
// posts the SAME monthlyWagesFloor total either way — this test isolates
// and proves the consumption/tax leg, which has no such floor.
func TestBUG745_HalfOutput_MovesTreasuryAndCitizenWealth(t *testing.T) {
	fullTreasury, fullWealth, _ := runBUG745Fixture(t, 745, 1)
	halfTreasury, halfWealth, _ := runBUG745Fixture(t, 745, 2)

	if halfTreasury == fullTreasury {
		t.Fatalf("treasury identical (%d) between the full-output run and a 24-month half-output run — OutputScale is still not reaching the money legs compose posts", halfTreasury)
	}
	if halfTreasury >= fullTreasury {
		t.Fatalf("treasury with the firm at half output (%d) is not LOWER than the full-output run (%d) — half output should mean less taxable spend clears, not more", halfTreasury, fullTreasury)
	}
	if halfWealth == fullWealth {
		t.Fatalf("tracked citizen wealth identical (%d) between the full-output run and a 24-month half-output run — OutputScale is still not reaching the money legs compose posts", halfWealth)
	}
	if halfWealth <= fullWealth {
		t.Fatalf("tracked citizen wealth with the firm at half output (%d) is not HIGHER than the full-output run (%d) — households should retain more of what they never got to spend, not less", halfWealth, fullWealth)
	}
	t.Logf("full-output: treasury=%d wealth=%d; half-output (24mo): treasury=%d wealth=%d (delta treasury=%d, delta wealth=%d)",
		fullTreasury, fullWealth, halfTreasury, halfWealth, halfTreasury-fullTreasury, halfWealth-fullWealth)
}

// TestBUG745_NeutralScale_PostConsumptionAndTaxMatchesUnscaledFormula is
// the x1.0 requirement, proved at the leg level rather than over a full
// 24-month composed run: a full engine run's Treasury()/CitizenWealth()
// cannot be compared against "no firm registered at all" (see
// runBUG745Fixture's doc comment on the vacancy confound that comparison
// hits), so this instead proves the thing that comparison was trying to
// prove directly — that postConsumptionAndTax(1000) posts EXACTLY the
// pre-BUG-745 unscaled figure, derived here from the same real-world
// constants production uses (GR#15), never a hand-picked literal. Combined
// with TestScaleByOutputPerMille_NeutralIsNoOp (scaleByOutputPerMille(x,
// 1000) == x for every representative amount) and the fact that
// postConsumptionAndTax's ONLY change from its pre-BUG-745 form is that one
// scaleByOutputPerMille call, this is a complete, honest proof that the
// neutral scale changes nothing — reinforced empirically by every
// pre-existing compose test in this package (BUG-548, FEAT-083, etc, none
// of which register a real-staffed firm) passing unmodified against this
// same code.
func TestBUG745_NeutralScale_PostConsumptionAndTaxMatchesUnscaledFormula(t *testing.T) {
	_, comp := newTestEngine(t, 745)
	st := comp.state
	if err := st.formResidentHouseholds(0); err != nil {
		t.Fatalf("formResidentHouseholds: %v", err)
	}
	households := int64(len(st.citizens.HouseholdIDs(st.cid)))
	if households <= 0 {
		t.Fatal("no households formed — fixture is vacuous")
	}

	wantSpend := households * monthlyConsumptionSpendMicropounds
	wantSalesTax := wantSpend * commercialTaxRateBp / 10_000
	wantCorpTax := wantSpend * industrialTaxRateBp / 10_000
	wantCouncil := households * monthlyCouncilTaxMicropounds
	want := wantSpend + wantSalesTax + wantCorpTax + wantCouncil

	got := st.postConsumptionAndTax(1000)
	if got != want {
		t.Fatalf("postConsumptionAndTax(1000) = %d, want %d (households=%d) — the pre-BUG-745 unscaled formula must be reproduced EXACTLY at the neutral scale", got, want, households)
	}
}

// TestScaleByOutputPerMille_NeutralIsNoOp proves, at the unit level and
// independent of any composed run, that scaleByOutputPerMille's documented
// no-op guarantee holds exactly (GR#15 — derive the expected value from
// the same formula production uses, never a hand-picked constant) across
// representative amounts including this file's own real money constants,
// and that its clamp/overflow-fallback edges degrade to the documented
// safe reading rather than corrupting the amount.
func TestScaleByOutputPerMille_NeutralIsNoOp(t *testing.T) {
	for _, amount := range []int64{0, 1, monthlyConsumptionSpendMicropounds, monthlyWagesFloor, 1 << 40} {
		if got := scaleByOutputPerMille(amount, 1000); got != amount {
			t.Fatalf("scaleByOutputPerMille(%d, 1000) = %d, want %d (neutral must be an exact identity)", amount, got, amount)
		}
	}
	if got := scaleByOutputPerMille(1000, 500); got != 500 {
		t.Fatalf("scaleByOutputPerMille(1000, 500) = %d, want 500", got)
	}
	if got := scaleByOutputPerMille(1000, 0); got != 0 {
		t.Fatalf("scaleByOutputPerMille(1000, 0) = %d, want 0", got)
	}
	// Out-of-range scale clamps toward the documented safe bound rather
	// than inverting the sign or overflowing (GR#16).
	if got := scaleByOutputPerMille(1000, -5); got != 0 {
		t.Fatalf("scaleByOutputPerMille(1000, -5) = %d, want 0 (clamped)", got)
	}
	if got := scaleByOutputPerMille(1000, 5000); got != 1000 {
		t.Fatalf("scaleByOutputPerMille(1000, 5000) = %d, want 1000 (clamped)", got)
	}
	// An overflowing product falls back to the unscaled amount rather than
	// a corrupted saturated-then-divided result.
	huge := int64(1) << 62
	if got := scaleByOutputPerMille(huge, 500); got != huge {
		t.Fatalf("scaleByOutputPerMille(%d, 500) = %d, want the overflow fallback %d", huge, got, huge)
	}
}

// TestBUG745_ProductivityModifier_EndToEnd proves BUG-745's ACTUAL
// regression criterion (the coordinator's round brief, GR#26 merge of
// 57fc437): not "does forcing OutputScale move money" (the direct-lever
// test above), but "does wellbeing's ProductivityModifier — the REAL
// composition-root chain 57fc437 landed
// (wellbeingHook -> FirmsAPI.SetProductivityModifier ->
// applyInputScalingLocked folding it into OutputScale -> this ticket's
// AggregateOutputScale -> postConsumptionAndTax) — actually move
// treasury/citizen wealth end to end, driven from the wellbeing seam
// itself, never by poking OutputScale or the market directly.
//
// 57fc437's own attack_mod034_apply_round_test.go
// (TestAttackMOD034_ModifiersBindAtWorstCase/productivity) measured this
// chain byte-identical to neutral PRE-BUG-745 ("productivity is INERT in
// the composed loop... applying ProductivityModifier changes nothing a
// player can observe") and deliberately left it unasserted, carving it out
// as "BUG-745, own lane". This test is that carve-out's closing proof.
//
// A firm must exist and be market-input-bound for applyInputScalingLocked
// to fold the modifier into anything (a zero-firm city has no OutputScale
// for the modifier to scale — aggregateOutputScaleLocked's own "no firms
// -> neutral 1000" default is unrelated to the wellbeing seam), so this
// registers one firm at FULL input availability (InputRequired == the
// real market capacity, scale 1000 pre-modifier) — isolating
// ProductivityModifier as the ONLY scale source, mirroring
// TestBUG745_HalfOutput_MovesTreasuryAndCitizenWealth's own
// "hold everything else constant" discipline but varying the modifier
// instead of the market shortfall.
func TestBUG745_ProductivityModifier_EndToEnd(t *testing.T) {
	measure := func(t *testing.T, wireModifier bool, productivity float64) (treasury, wealth int64, popHash [32]byte) {
		t.Helper()
		e, comp := newTestEngine(t, 745)
		if wireModifier {
			if err := comp.state.firms.SetProductivityModifier(func() float64 { return productivity }); err != nil {
				t.Fatalf("SetProductivityModifier: %v", err)
			}
		}
		capacity := constructionMaterialsCapacity(t, comp)
		registerFirmWithInputRequired(t, comp, capacity)
		runMonths(t, e, 24)
		return comp.Treasury(), comp.CitizenWealth(), comp.PopulationHash()
	}

	t.Run("x0.5_moves_treasury_and_citizen_wealth", func(t *testing.T) {
		neutralTreasury, neutralWealth, _ := measure(t, true, 1.0)
		halfTreasury, halfWealth, _ := measure(t, true, 0.5)

		if halfTreasury == neutralTreasury {
			t.Fatalf("treasury identical (%d) between ProductivityModifier=1.0 and =0.5 over 24 months — the wellbeing->firms->compose chain is still not reaching the money legs compose posts", halfTreasury)
		}
		if halfTreasury >= neutralTreasury {
			t.Fatalf("treasury with ProductivityModifier=0.5 (%d) is not LOWER than ProductivityModifier=1.0 (%d) — half productivity should mean less taxable spend clears, not more (same direction as the direct market-shortfall lever, moneycirc.go's postConsumptionAndTax doc comment)", halfTreasury, neutralTreasury)
		}
		if halfWealth == neutralWealth {
			t.Fatalf("tracked citizen wealth identical (%d) between ProductivityModifier=1.0 and =0.5 over 24 months — the wellbeing->firms->compose chain is still not reaching the money legs compose posts", halfWealth)
		}
		if halfWealth <= neutralWealth {
			t.Fatalf("tracked citizen wealth with ProductivityModifier=0.5 (%d) is not HIGHER than ProductivityModifier=1.0 (%d) — households should retain more of what they never got to spend, not less (same direction as the direct market-shortfall lever)", halfWealth, neutralWealth)
		}
		t.Logf("productivity=1.0: treasury=%d wealth=%d; productivity=0.5 (24mo): treasury=%d wealth=%d (delta treasury=%d, delta wealth=%d)",
			neutralTreasury, neutralWealth, halfTreasury, halfWealth, halfTreasury-neutralTreasury, halfWealth-neutralWealth)
	})

	// x1.0_byte_identical_to_unwired_baseline was tried FIRST as "a city
	// that never calls SetProductivityModifier itself (relying on Wire's
	// own 57fc437-installed default getter, func() float64 { return
	// st.wellbeingStatus.ProductivityModifier }) must match a city that
	// pins the modifier at an explicit fixed 1.0" — and it REDS: measured
	// treasury 2756680189 (default live getter) vs 2760899500 (pinned
	// 1.0), a real, if small (~0.15%), divergence. Root cause (not a
	// BUG-745 defect): Wire's default getter tracks the REAL, live
	// wellbeing cohort reconstruction every month (compose_wellbeing.go's
	// wellbeingHook), which — even at data/wellbeing.json's placeholder
	// 0.001 slope — is essentially never pinned at EXACTLY 1.0 for all 24
	// months (the cohort's real physical/mental means drift). Comparing
	// "live wellbeing" against "hard-pinned 1.0" is therefore not a valid
	// neutral-noop test AT ALL — the two scenarios are legitimately
	// different by construction, regardless of BUG-745. The correct x1.0
	// proof isolates the ARITHMETIC identity from wellbeing's own live
	// drift, below.

	// TestBUG745_ProductivityModifier_EndToEnd's x1.0 requirement: proves
	// the modifier-fold arithmetic (firms/lifecycle.go's
	// applyInputScalingLocked, "scale = int64(float64(scale) * mod)") is
	// an EXACT identity at productivity=1.0 through the REAL
	// SetProductivityModifier seam — deterministically, at month 0, before
	// any tick can let wellbeing's own live drift (see above) become a
	// confound. Combined with TestScaleByOutputPerMille_NeutralIsNoOp
	// (compose's own multiply is an exact identity at scale=1000) and
	// TestBUG745_NeutralScale_PostConsumptionAndTaxMatchesUnscaledFormula
	// (postConsumptionAndTax(1000) matches the pre-BUG-745 unscaled
	// formula exactly), this closes the full chain's "no behaviour change
	// at neutral" proof end to end: wellbeing seam -> firms' OutputScale
	// fold -> compose's consumption/tax leg.
	t.Run("x1.0_modifier_fold_is_exact_identity", func(t *testing.T) {
		_, comp := newTestEngine(t, 745)
		capacity := constructionMaterialsCapacity(t, comp)
		registerFirmWithInputRequired(t, comp, capacity)
		if err := comp.state.firms.SetProductivityModifier(func() float64 { return 1.0 }); err != nil {
			t.Fatalf("SetProductivityModifier: %v", err)
		}
		if err := comp.state.firms.ResolveMonth(0); err != nil {
			t.Fatalf("ResolveMonth: %v", err)
		}
		scale, err := comp.state.firms.AggregateOutputScale()
		if err != nil {
			t.Fatalf("AggregateOutputScale: %v", err)
		}
		if scale != 1000 {
			t.Fatalf("AggregateOutputScale with ProductivityModifier pinned at exactly 1.0 (full market availability, InputRequired==capacity) = %d, want exactly 1000 (neutral) — the modifier fold is not an exact identity at 1.0", scale)
		}
	})
}
