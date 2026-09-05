package finance

import "testing"

// testOpexConfig is a fixed, non-data-file config for tests that don't
// need to exercise the loader itself (opexdata_test.go covers that).
func testOpexConfig() OpexConfig {
	return OpexConfig{
		CostPerEngineerDay:       gbp(250),
		BacklogEfficiencyDivisor: 50,
		MinEfficiencyBasisPoints: 2000,
		MajorDrainMinFractionBps: 500,
	}
}

// TestOpexBreakdownComponentsIndependent proves AC-1's substance: the
// five components are independently retrievable, and maintenance is
// NOT folded into (or equal to) services for a fixture that posts both.
func TestOpexBreakdownComponentsIndependent(t *testing.T) {
	f := NewFinanceAPI("opex-breakdown")
	seedTreasury(t, f, gbp(10_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	if _, err := f.PostMaintenance(gbp(80), gbp(80)); err != nil {
		t.Fatalf("PostMaintenance: %v", err)
	}
	if _, err := f.SettleOpex(gbp(30)); err != nil {
		t.Fatalf("SettleOpex (services): %v", err)
	}
	if _, err := f.PostMaterials(gbp(20)); err != nil {
		t.Fatalf("PostMaterials: %v", err)
	}
	if _, err := f.PostStaffWages(gbp(40)); err != nil {
		t.Fatalf("PostStaffWages: %v", err)
	}
	if err := f.ServiceDebt(gbp(10), 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}

	b := f.OpexBreakdown()
	if b.Maintenance != gbp(80) {
		t.Fatalf("Maintenance = %d, want %d", b.Maintenance, gbp(80))
	}
	if b.Services != gbp(30) {
		t.Fatalf("Services = %d, want %d", b.Services, gbp(30))
	}
	if b.Materials != gbp(20) {
		t.Fatalf("Materials = %d, want %d", b.Materials, gbp(20))
	}
	if b.StaffWages != gbp(40) {
		t.Fatalf("StaffWages = %d, want %d", b.StaffWages, gbp(40))
	}
	if b.DebtService != gbp(10) {
		t.Fatalf("DebtService = %d, want %d", b.DebtService, gbp(10))
	}
	// The false-pass this AC exists to catch: maintenance must not equal
	// (or be subsumed by) services.
	if b.Maintenance == b.Services {
		t.Fatalf("maintenance (%d) must not equal services (%d) — they must be independently retrievable, distinct components", b.Maintenance, b.Services)
	}
}

// TestComposedOpexEqualsSumAndDrivesBudget (AC-2) proves ComposedOpex
// is exactly the sum of its five parts, and that BudgetBalance
// subtracts the COMPOSED total — not just the pre-FEAT-094 service-opex
// figure — so every component is a real, felt budget drain.
func TestComposedOpexEqualsSumAndDrivesBudget(t *testing.T) {
	f := NewFinanceAPI("opex-composed")
	seedTreasury(t, f, gbp(100_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	wages, spend := gbp(1000), gbp(600)
	if _, err := f.PostWages(wages); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if _, err := f.PostHouseholdSpend(300, gbp(2)); err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	receipts, err := f.CollectTax(TaxRates{IncomeRate: 2000, SalesRate: 2000, CorpRate: 2000}, wages, spend, 0)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}

	maintenance, materials, staff, services := gbp(80), gbp(20), gbp(40), gbp(30)
	if _, err := f.PostMaintenance(maintenance, maintenance); err != nil {
		t.Fatalf("PostMaintenance: %v", err)
	}
	if _, err := f.PostMaterials(materials); err != nil {
		t.Fatalf("PostMaterials: %v", err)
	}
	if _, err := f.PostStaffWages(staff); err != nil {
		t.Fatalf("PostStaffWages: %v", err)
	}
	if _, err := f.SettleOpex(services); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	debtInterest := gbp(10)
	if err := f.ServiceDebt(debtInterest, 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	construction := gbp(50)
	if _, err := f.SettleConstruction(construction); err != nil {
		t.Fatalf("SettleConstruction: %v", err)
	}
	imports := gbp(15)
	if _, err := f.SettleImports(imports); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}

	wantComposed := maintenance + materials + staff + services + debtInterest
	if got := f.ComposedOpex(); got != wantComposed {
		t.Fatalf("ComposedOpex = %d, want %d", got, wantComposed)
	}
	if got := f.OpexBreakdown().Total(); got != wantComposed {
		t.Fatalf("OpexBreakdown().Total() = %d, want %d", got, wantComposed)
	}

	wantBudget := receipts.Total() - wantComposed - construction - imports
	if got := f.BudgetBalance(); got != wantBudget {
		t.Fatalf("BudgetBalance = %d, want %d (recomputed from ComposedOpex, not the old service-opex-only figure)", got, wantBudget)
	}

	// The false-pass this AC exists to catch: a build that posts
	// maintenance but never folds it into the budget's subtraction. Prove
	// removing maintenance from the expectation would NOT match — i.e.
	// maintenance really is felt.
	wantBudgetWithoutMaintenance := receipts.Total() - (wantComposed - maintenance) - construction - imports
	if got := f.BudgetBalance(); got == wantBudgetWithoutMaintenance {
		t.Fatalf("BudgetBalance (%d) must differ from the maintenance-excluded figure (%d) — maintenance must be a real drain", got, wantBudgetWithoutMaintenance)
	}
}

// TestComponentLinesDrillThrough (AC-3) proves each component posts via
// its own category so LinesByCategory isolates it from every other
// component.
func TestComponentLinesDrillThrough(t *testing.T) {
	f := NewFinanceAPI("opex-lines")
	seedTreasury(t, f, gbp(10_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	if _, err := f.PostMaintenance(gbp(80), gbp(80)); err != nil {
		t.Fatalf("PostMaintenance: %v", err)
	}
	if _, err := f.PostMaterials(gbp(20)); err != nil {
		t.Fatalf("PostMaterials: %v", err)
	}
	if _, err := f.PostStaffWages(gbp(40)); err != nil {
		t.Fatalf("PostStaffWages: %v", err)
	}

	maint := f.LinesByCategory(CatMaintenance)
	if len(maint) == 0 {
		t.Fatal("expected maintenance lines")
	}
	for _, e := range maint {
		if e.Category != CatMaintenance {
			t.Fatalf("maintenance line carries category %q, want CatMaintenance", e.Category)
		}
	}
	mat := f.LinesByCategory(CatMaterials)
	staff := f.LinesByCategory(CatStaffWages)
	if len(mat) == 0 || len(staff) == 0 {
		t.Fatal("expected materials and staff-wage lines")
	}
	// No cross-contamination: materials lines never carry the maintenance
	// category and vice versa.
	for _, e := range mat {
		if e.Category == CatMaintenance || e.Category == CatStaffWages {
			t.Fatalf("materials line leaked into another component's category: %q", e.Category)
		}
	}
}

// TestSettlementStagesIndependentlyCallable (AC-4) runs the full
// five-component settlement for one synthetic month and asserts each
// stage's own posted amount against hand-computed expected values.
func TestSettlementStagesIndependentlyCallable(t *testing.T) {
	f := NewFinanceAPI("opex-settlement")
	seedTreasury(t, f, gbp(10_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	demand, funded := gbp(100), gbp(70)
	gotMaint, err := f.PostMaintenance(demand, funded)
	if err != nil {
		t.Fatalf("PostMaintenance: %v", err)
	}
	if gotMaint != funded {
		t.Fatalf("PostMaintenance returned %d, want funded %d", gotMaint, funded)
	}
	gotMat, err := f.PostMaterials(gbp(25))
	if err != nil {
		t.Fatalf("PostMaterials: %v", err)
	}
	if gotMat != gbp(25) {
		t.Fatalf("PostMaterials returned %d, want %d", gotMat, gbp(25))
	}
	gotStaff, err := f.PostStaffWages(gbp(45))
	if err != nil {
		t.Fatalf("PostStaffWages: %v", err)
	}
	if gotStaff != gbp(45) {
		t.Fatalf("PostStaffWages returned %d, want %d", gotStaff, gbp(45))
	}
	gotSvc, err := f.SettleOpex(gbp(35))
	if err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	if gotSvc != gbp(35) {
		t.Fatalf("SettleOpex returned %d, want %d", gotSvc, gbp(35))
	}
	if err := f.ServiceDebt(gbp(15), gbp(5)); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}

	want := funded + gbp(25) + gbp(45) + gbp(35) + gbp(15)
	if got := f.ComposedOpex(); got != want {
		t.Fatalf("ComposedOpex = %d, want %d", got, want)
	}
}

// TestMaintenanceBacklogGrowsAndEfficiencyDegrades (AC-5) exercises the
// backlog->efficiency monotonic relationship: three months of
// underfunding grow the backlog and degrade efficiency monotonically;
// a fourth, overfunded month shrinks the backlog and recovers
// efficiency.
func TestMaintenanceBacklogGrowsAndEfficiencyDegrades(t *testing.T) {
	f := NewFinanceAPI("opex-backlog")
	seedTreasury(t, f, gbp(100_000))
	if err := f.SetOpexConfig(testOpexConfig()); err != nil {
		t.Fatalf("SetOpexConfig: %v", err)
	}

	demand, funded := gbp(100), gbp(60) // 40 shortfall/month
	var lastBacklog Money = -1
	var lastEff BasisPoints = 10001 // above the max, so month 0's check always passes
	for m := int64(1); m <= 3; m++ {
		if err := f.BeginMonth(m); err != nil {
			t.Fatalf("BeginMonth(%d): %v", m, err)
		}
		if _, err := f.PostMaintenance(demand, funded); err != nil {
			t.Fatalf("PostMaintenance(%d): %v", m, err)
		}
		backlog := f.MaintenanceBacklog()
		if backlog <= lastBacklog {
			t.Fatalf("month %d: backlog %d did not grow past previous %d", m, backlog, lastBacklog)
		}
		eff, err := f.MaintenanceEfficiency()
		if err != nil {
			t.Fatalf("MaintenanceEfficiency(%d): %v", m, err)
		}
		if eff >= lastEff {
			t.Fatalf("month %d: efficiency %d did not decrease from previous %d", m, eff, lastEff)
		}
		lastBacklog, lastEff = backlog, eff
	}

	// Month 4: overfund heavily — backlog shrinks, efficiency recovers.
	if err := f.BeginMonth(4); err != nil {
		t.Fatalf("BeginMonth(4): %v", err)
	}
	if _, err := f.PostMaintenance(gbp(10), gbp(200)); err != nil {
		t.Fatalf("PostMaintenance(4): %v", err)
	}
	newBacklog := f.MaintenanceBacklog()
	if newBacklog >= lastBacklog {
		t.Fatalf("overfunded month: backlog %d did not shrink from %d", newBacklog, lastBacklog)
	}
	newEff, err := f.MaintenanceEfficiency()
	if err != nil {
		t.Fatalf("MaintenanceEfficiency(4): %v", err)
	}
	if newEff <= lastEff {
		t.Fatalf("overfunded month: efficiency %d did not recover from %d", newEff, lastEff)
	}
}

// TestMaintenanceIsMajorDrain (AC-6) asserts maintenance is comparable
// in order-of-magnitude to the composed total under a realistic
// fixture, via the documented data-sourced fraction threshold rather
// than a hardcoded constant.
func TestMaintenanceIsMajorDrain(t *testing.T) {
	f := NewFinanceAPI("opex-major-drain")
	seedTreasury(t, f, gbp(1_000_000))
	cfg := testOpexConfig()
	if err := f.SetOpexConfig(cfg); err != nil {
		t.Fatalf("SetOpexConfig: %v", err)
	}
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	// A genuine engineering-scaled demand: engineer-days x the
	// data-sourced cost-per-engineer-day (GR#15), fully funded.
	const engineerDays = 400
	maintDemand := Money(engineerDays) * cfg.CostPerEngineerDay
	if _, err := f.PostMaintenance(maintDemand, maintDemand); err != nil {
		t.Fatalf("PostMaintenance: %v", err)
	}
	if _, err := f.PostMaterials(gbp(500)); err != nil {
		t.Fatalf("PostMaterials: %v", err)
	}
	if _, err := f.PostStaffWages(gbp(500)); err != nil {
		t.Fatalf("PostStaffWages: %v", err)
	}
	if _, err := f.SettleOpex(gbp(500)); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	if err := f.ServiceDebt(gbp(500), 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}

	composed := f.ComposedOpex()
	maint := f.OpexBreakdown().Maintenance
	if maint == 0 {
		t.Fatal("expected a non-zero maintenance component")
	}
	// maint/composed >= majorDrainMinFractionBps/10000, tested without
	// float division: maint*10000 >= composed*fraction.
	lhs := int64(maint) * 10000
	rhs := int64(composed) * int64(cfg.MajorDrainMinFractionBps)
	if lhs < rhs {
		t.Fatalf("maintenance (%d) is below the data-sourced major-drain threshold: need maint*10000 >= composed*%d, got %d < %d", maint, cfg.MajorDrainMinFractionBps, lhs, rhs)
	}
}

// TestCapexOpexSplitByPolicy (AC-7) proves the same underlying
// obligation amount lands in the OPEX component under an auto-repair
// policy and in the capital total under a refit/rebuild policy.
func TestCapexOpexSplitByPolicy(t *testing.T) {
	f := NewFinanceAPI("opex-capex-split")
	seedTreasury(t, f, gbp(10_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	obligation := gbp(200)
	if _, err := f.PostMaintenanceSpend(obligation, RepairPolicyAuto); err != nil {
		t.Fatalf("PostMaintenanceSpend(auto): %v", err)
	}
	if got := f.OpexBreakdown().Maintenance; got != obligation {
		t.Fatalf("auto-repair: OPEX maintenance = %d, want %d", got, obligation)
	}
	if got := f.CapexTotal(); got != 0 {
		t.Fatalf("auto-repair: CapexTotal = %d, want 0 (must not land in capital)", got)
	}

	if err := f.BeginMonth(2); err != nil {
		t.Fatalf("BeginMonth(2): %v", err)
	}
	if _, err := f.PostMaintenanceSpend(obligation, RepairPolicyRefitRebuild); err != nil {
		t.Fatalf("PostMaintenanceSpend(refit): %v", err)
	}
	if got := f.OpexBreakdown().Maintenance; got != 0 {
		t.Fatalf("refit/rebuild: OPEX maintenance = %d, want 0 (must not land in the operating bucket)", got)
	}
	if got := f.CapexTotal(); got != obligation {
		t.Fatalf("refit/rebuild: CapexTotal = %d, want %d", got, obligation)
	}
}

// TestPolicySplitPreservesTotal (AC-8) drives the same obligation
// through both policy values and asserts the OPEX+CAPEX total is
// unchanged while the proportions differ.
func TestPolicySplitPreservesTotal(t *testing.T) {
	obligation := gbp(300)

	run := func(policy RepairPolicy) (opex, capex Money) {
		f := NewFinanceAPI("opex-policy-invariant")
		seedTreasury(t, f, gbp(10_000))
		if err := f.BeginMonth(1); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
		if _, err := f.PostMaintenanceSpend(obligation, policy); err != nil {
			t.Fatalf("PostMaintenanceSpend: %v", err)
		}
		return f.OpexBreakdown().Maintenance, f.CapexTotal()
	}

	autoOpex, autoCapex := run(RepairPolicyAuto)
	refitOpex, refitCapex := run(RepairPolicyRefitRebuild)

	autoTotal := autoOpex + autoCapex
	refitTotal := refitOpex + refitCapex
	if autoTotal != refitTotal {
		t.Fatalf("total obligation changed across policies: auto=%d refit=%d", autoTotal, refitTotal)
	}
	if autoOpex == refitOpex && autoCapex == refitCapex {
		t.Fatal("expected the OPEX/CAPEX proportions to differ between policies")
	}
	if autoOpex != obligation || autoCapex != 0 {
		t.Fatalf("auto-repair split wrong: opex=%d capex=%d", autoOpex, autoCapex)
	}
	if refitCapex != obligation || refitOpex != 0 {
		t.Fatalf("refit/rebuild split wrong: opex=%d capex=%d", refitOpex, refitCapex)
	}
}

// TestOpexCapexConservationIdentity (AC-9) runs a full monthly
// settlement — wages -> spend -> tax -> composed OPEX -> capex -> debt
// — and asserts NetOther() - ComposedOpex() - CapexTotal() ==
// TrackedDelta exactly (the documented sign convention, see opex.go's
// NetOther doc comment), and that TotalMoneyInCirculation's closing
// value equals opening + that delta.
func TestOpexCapexConservationIdentity(t *testing.T) {
	f := NewFinanceAPI("opex-conservation")
	seedTreasury(t, f, gbp(1_000_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	opening := f.TotalMoneyInCirculation()

	wages, spend := gbp(1000), gbp(600)
	if _, err := f.PostWages(wages); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if _, err := f.PostHouseholdSpend(300, gbp(2)); err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	if _, err := f.CollectTax(TaxRates{IncomeRate: 2000, SalesRate: 2000, CorpRate: 2000}, wages, spend, 0); err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if _, err := f.PostMaintenance(gbp(80), gbp(80)); err != nil {
		t.Fatalf("PostMaintenance: %v", err)
	}
	if _, err := f.PostMaterials(gbp(20)); err != nil {
		t.Fatalf("PostMaterials: %v", err)
	}
	if _, err := f.PostStaffWages(gbp(40)); err != nil {
		t.Fatalf("PostStaffWages: %v", err)
	}
	if _, err := f.SettleOpex(gbp(30)); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	if err := f.ServiceDebt(gbp(10), gbp(5)); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	if _, err := f.PostCapexSpend(gbp(150)); err != nil {
		t.Fatalf("PostCapexSpend: %v", err)
	}

	stock := f.MoneyStock()
	got := f.NetOther() - f.ComposedOpex() - f.CapexTotal()
	if got != stock.TrackedDelta {
		t.Fatalf("NetOther()-ComposedOpex()-CapexTotal() = %d, want TrackedDelta %d", got, stock.TrackedDelta)
	}

	closing := f.TotalMoneyInCirculation()
	if closing != opening+stock.TrackedDelta {
		t.Fatalf("TotalMoneyInCirculation closing %d != opening %d + delta %d", closing, opening, stock.TrackedDelta)
	}
}

// TestUnbalancedMaintenancePostLocalisable (AC-10) posts a deliberately
// unbalanced CatMaintenance transaction via the low-level path and
// asserts FindConservationViolations reports it.
func TestUnbalancedMaintenancePostLocalisable(t *testing.T) {
	f := NewFinanceAPI("opex-conservation-violation")
	seedTreasury(t, f, gbp(1_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	txID := f.postRaw(Transaction{
		Description: "unbalanced maintenance post (simulated bug)",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: gbp(50), Category: CatMaintenance},
		},
	})

	violations := f.FindConservationViolations()
	if len(violations) == 0 {
		t.Fatal("expected FindConservationViolations to report the unbalanced maintenance post")
	}
	found := false
	for _, v := range violations {
		if v.TransactionID == txID {
			found = true
			if len(v.AccountIDs) == 0 {
				t.Fatal("expected the violation to name the accounts it touched")
			}
		}
	}
	if !found {
		t.Fatalf("violation for transaction %d not found in %+v", txID, violations)
	}
}

// TestMalformedOpexInputsRejected (AC-11/GR#7) proves negative demand,
// negative funded, and a zero-cost "capital event" are rejected with
// their named registry codes, with no partial post.
func TestMalformedOpexInputsRejected(t *testing.T) {
	f := NewFinanceAPI("opex-malformed")
	seedTreasury(t, f, gbp(1_000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	if _, err := f.PostMaintenance(-1, gbp(10)); !hasCode(err, ErrMaintenanceDemandNegative) {
		t.Fatalf("negative demand: got %v, want %s", err, ErrMaintenanceDemandNegative)
	}
	if lines := f.LinesByCategory(CatMaintenance); len(lines) != 0 {
		t.Fatalf("negative demand must not partially post, got %d lines", len(lines))
	}

	if _, err := f.PostMaintenance(gbp(10), -1); !hasCode(err, ErrMaintenanceFundedNegative) {
		t.Fatalf("negative funded: got %v, want %s", err, ErrMaintenanceFundedNegative)
	}
	if lines := f.LinesByCategory(CatMaintenance); len(lines) != 0 {
		t.Fatalf("negative funded must not partially post, got %d lines", len(lines))
	}

	if _, err := f.PostCapexSpend(0); !hasCode(err, ErrCapexUnclassified) {
		t.Fatalf("zero-cost capex: got %v, want %s", err, ErrCapexUnclassified)
	}
	if lines := f.LinesByCategory(CatCapex); len(lines) != 0 {
		t.Fatalf("zero-cost capex must not post, got %d lines", len(lines))
	}
}

// TestMaintenanceEfficiencyRequiresConfig (GR#15) proves the efficiency
// accessor fails closed — never a silently-substituted default — when
// no balance data has been installed.
func TestMaintenanceEfficiencyRequiresConfig(t *testing.T) {
	f := NewFinanceAPI("opex-no-config")
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	if _, err := f.MaintenanceEfficiency(); !hasCode(err, ErrOpexConfigNotSet) {
		t.Fatalf("got %v, want %s", err, ErrOpexConfigNotSet)
	}
}

// TestOpexDeterministicAcrossOrder (AC-12) asserts the composed totals
// are identical regardless of the order components are posted in —
// since these are sums over a fixed slice (tickTxns), never map
// iteration, order of POSTING (not of summation) is the only thing that
// could vary; the totals must not depend on it.
func TestOpexDeterministicAcrossOrder(t *testing.T) {
	build := func(order []func(*FinanceAPI) error) Money {
		f := NewFinanceAPI("opex-determinism")
		seedTreasury(t, f, gbp(10_000))
		if err := f.BeginMonth(1); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
		for _, step := range order {
			if err := step(f); err != nil {
				t.Fatalf("step: %v", err)
			}
		}
		return f.ComposedOpex()
	}

	maint := func(f *FinanceAPI) error { _, err := f.PostMaintenance(gbp(80), gbp(80)); return err }
	mat := func(f *FinanceAPI) error { _, err := f.PostMaterials(gbp(20)); return err }
	staff := func(f *FinanceAPI) error { _, err := f.PostStaffWages(gbp(40)); return err }
	svc := func(f *FinanceAPI) error { _, err := f.SettleOpex(gbp(30)); return err }

	a := build([]func(*FinanceAPI) error{maint, mat, staff, svc})
	b := build([]func(*FinanceAPI) error{svc, staff, mat, maint})
	if a != b {
		t.Fatalf("ComposedOpex depends on posting order: %d vs %d", a, b)
	}
}
