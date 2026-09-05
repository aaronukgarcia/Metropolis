package finance

// FEAT-094 independent destructive round (attacker "opus-round-feat094").
// These tests are adversarial: they hammer the AC-9 conservation identity
// under every posting order/interleaving across month boundaries with
// backlog carryover, try to force a BudgetBalance divergence from the
// pre-FEAT-094 formula, prove partial-post atomicity on every rejected
// path, and prove the maintenance backlog + monotonic efficiency survive a
// mid-accumulation save/restore. They add no production code.

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// testOpexCfg is a deterministic, non-data-file config for the efficiency
// accessor (the accessor fails closed without one; these tests inject a
// known config so monotonicity is checkable without touching the data file).
func testOpexCfg() OpexConfig {
	return OpexConfig{
		CostPerEngineerDay:       gbp(1),
		BacklogEfficiencyDivisor: 1000, // 1 bp lost per 1000 units of backlog
		MinEfficiencyBasisPoints: 2000, // floor 20%
		MajorDrainMinFractionBps: 1000,
	}
}

// assertIdentity is the AC-9 kill target: NetOther - ComposedOpex -
// CapexTotal == TrackedDelta, EXACTLY (integer micropounds, zero residual);
// and the money stock closes at opening + delta.
func assertIdentity(t *testing.T, f *FinanceAPI, label string) {
	t.Helper()
	net := f.NetOther()
	opex := f.ComposedOpex()
	capex := f.CapexTotal()
	lhs := net - opex - capex
	ms := f.MoneyStock()
	if lhs != ms.TrackedDelta {
		t.Fatalf("%s: AC-9 identity BROKEN: NetOther(%d) - ComposedOpex(%d) - CapexTotal(%d) = %d != TrackedDelta %d",
			label, net, opex, capex, lhs, ms.TrackedDelta)
	}
	if ms.Closing != ms.Opening+ms.TrackedDelta {
		t.Fatalf("%s: money stock does not close: Closing %d != Opening %d + Delta %d",
			label, ms.Closing, ms.Opening, ms.TrackedDelta)
	}
	// Independent from-scratch reconciliation of the running total.
	if got := f.RecomputeMoneyStock(); got != ms.Closing {
		t.Fatalf("%s: RecomputeMoneyStock %d != running Closing %d", label, got, ms.Closing)
	}
}

// opStep is one settlement action; every permutation of these is exercised.
type opStep struct {
	name string
	run  func(t *testing.T, f *FinanceAPI)
}

func settlementSteps() []opStep {
	return []opStep{
		{"maintenance", func(t *testing.T, f *FinanceAPI) {
			if _, err := f.PostMaintenance(gbp(50), gbp(30)); err != nil {
				t.Fatalf("PostMaintenance: %v", err)
			}
		}},
		{"materials", func(t *testing.T, f *FinanceAPI) {
			if _, err := f.PostMaterials(gbp(17)); err != nil {
				t.Fatalf("PostMaterials: %v", err)
			}
		}},
		{"staffwages", func(t *testing.T, f *FinanceAPI) {
			if _, err := f.PostStaffWages(gbp(23)); err != nil {
				t.Fatalf("PostStaffWages: %v", err)
			}
		}},
		{"services", func(t *testing.T, f *FinanceAPI) {
			if _, err := f.SettleOpex(gbp(11)); err != nil {
				t.Fatalf("SettleOpex: %v", err)
			}
		}},
		{"debt", func(t *testing.T, f *FinanceAPI) {
			if err := f.ServiceDebt(gbp(7), gbp(13)); err != nil {
				t.Fatalf("ServiceDebt: %v", err)
			}
		}},
		{"capex", func(t *testing.T, f *FinanceAPI) {
			if _, err := f.PostMaintenanceSpend(gbp(19), RepairPolicyRefitRebuild); err != nil {
				t.Fatalf("PostMaintenanceSpend refit: %v", err)
			}
		}},
		{"autorepair", func(t *testing.T, f *FinanceAPI) {
			if _, err := f.PostMaintenanceSpend(gbp(9), RepairPolicyAuto); err != nil {
				t.Fatalf("PostMaintenanceSpend auto: %v", err)
			}
		}},
	}
}

// permute yields every permutation of indices [0..n).
func permute(n int) [][]int {
	var out [][]int
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			cp := make([]int, n)
			copy(cp, idx)
			out = append(out, cp)
			return
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			rec(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	rec(0)
	return out
}

// TestAttackConservationIdentityEveryOrder posts all seven settlement
// actions in EVERY permutation, across a fresh month each time, and asserts
// the AC-9 identity holds exactly after every single post and at month end.
// 7! = 5040 orderings — if any interleaving breaks conservation, this finds
// it.
func TestAttackConservationIdentityEveryOrder(t *testing.T) {
	steps := settlementSteps()
	perms := permute(len(steps))
	for pi, perm := range perms {
		f := NewFinanceAPI("attack-order")
		ck(t, f.SetMilestoneGate(allowAllGate{}))
		// Fund the treasury generously so no overdraft rejects mid-run.
		ck(t, f.BeginMonth(int64(pi+1)))
		seedTreasury(t, f, gbp(100000))
		for _, si := range perm {
			steps[si].run(t, f)
			assertIdentity(t, f, "mid-run")
		}
		assertIdentity(t, f, "month-end")
	}
}

// TestAttackConservationAcrossMonthsWithBacklog runs many months, each with
// a random-but-deterministic funded<demand shortfall so the backlog carries
// over, and asserts the per-tick identity holds every month AND the backlog
// grows monotonically while underfunded.
func TestAttackConservationAcrossMonthsWithBacklog(t *testing.T) {
	f := NewFinanceAPI("attack-months")
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.SetOpexConfig(testOpexCfg()))

	var prevBacklog Money = -1
	for m := int64(1); m <= 24; m++ {
		ck(t, f.BeginMonth(m))
		seedTreasury(t, f, gbp(100000))
		// Vary amounts per month deterministically (month-driven, no RNG).
		demand := gbp(100 + m*3)
		funded := gbp(40 + m) // always < demand → backlog grows
		if _, err := f.PostMaintenance(demand, funded); err != nil {
			t.Fatalf("m%d PostMaintenance: %v", m, err)
		}
		if _, err := f.PostMaterials(gbp(m * 2)); err != nil {
			t.Fatalf("m%d PostMaterials: %v", m, err)
		}
		if _, err := f.PostStaffWages(gbp(m * 3)); err != nil {
			t.Fatalf("m%d PostStaffWages: %v", m, err)
		}
		if _, err := f.SettleOpex(gbp(m)); err != nil {
			t.Fatalf("m%d SettleOpex: %v", m, err)
		}
		if err := f.ServiceDebt(gbp(m), gbp(m)); err != nil {
			t.Fatalf("m%d ServiceDebt: %v", m, err)
		}
		if _, err := f.PostMaintenanceSpend(gbp(m), RepairPolicyRefitRebuild); err != nil {
			t.Fatalf("m%d capex: %v", m, err)
		}
		assertIdentity(t, f, "months")

		bl := f.MaintenanceBacklog()
		if bl <= prevBacklog {
			t.Fatalf("m%d: backlog not monotonic while underfunded: %d <= prev %d", m, bl, prevBacklog)
		}
		prevBacklog = bl

		eff, err := f.MaintenanceEfficiency()
		if err != nil {
			t.Fatalf("m%d efficiency: %v", m, err)
		}
		if eff < 2000 || eff > 10000 {
			t.Fatalf("m%d efficiency %d out of [2000,10000]", m, eff)
		}
	}
}

// TestAttackBudgetBalanceIdenticalWhenNoNewComponents proves the doc's
// "arithmetically identical" claim: with only pre-FEAT-094 categories
// posted, BudgetBalance equals the OLD formula tax - OpexTotal -
// DebtServiceTotal - Construction - Imports, and ComposedOpex == OpexTotal +
// DebtServiceTotal exactly (no double-count, no dropped debt service).
func TestAttackBudgetBalanceIdenticalWhenNoNewComponents(t *testing.T) {
	f := NewFinanceAPI("attack-budget-old")
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.SetCreditLine(AcctHouseholds, gbp(100000)))
	ck(t, f.SetCreditLine(AcctFirms, gbp(100000)))
	ck(t, f.BeginMonth(1))
	seedTreasury(t, f, gbp(100000))

	if _, err := f.PostWages(gbp(1000)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CollectTax(TaxRates{IncomeRate: 2000, SalesRate: 1000, CorpRate: 1500}, gbp(1000), gbp(400), gbp(200)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SettleOpex(gbp(120)); err != nil {
		t.Fatal(err)
	}
	if err := f.ServiceDebt(gbp(30), gbp(40)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SettleConstruction(gbp(60)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SettleImports(gbp(25)); err != nil {
		t.Fatal(err)
	}

	// ComposedOpex must fold in debt service and equal old opex+debt exactly.
	if got, want := f.ComposedOpex(), f.OpexTotal()+f.DebtServiceTotal(); got != want {
		t.Fatalf("ComposedOpex %d != OpexTotal+DebtServiceTotal %d (drops or double-counts debt service)", got, want)
	}
	// CapexTotal must be zero with no refit posted.
	if f.CapexTotal() != 0 {
		t.Fatalf("CapexTotal %d != 0 with no capex posted", f.CapexTotal())
	}
	// The OLD budget formula, recomputed independently.
	oldBudget := f.TaxRevenue()
	oldBudget = satSubMoney(oldBudget, f.OpexTotal())
	oldBudget = satSubMoney(oldBudget, f.DebtServiceTotal())
	oldBudget = satSubMoney(oldBudget, f.ConstructionTotal())
	oldBudget = satSubMoney(oldBudget, f.ImportsTotal())
	if got := f.BudgetBalance(); got != oldBudget {
		t.Fatalf("BudgetBalance DIVERGED from pre-FEAT-094 formula: new %d != old %d", got, oldBudget)
	}
}

// TestAttackBudgetBalanceEqualsCompositionWithNewComponents proves, with the
// new components ALSO posted, that BudgetBalance == tax - ComposedOpex -
// Capex - Construction - Imports and ComposedOpex == sum of the five named
// parts exactly.
func TestAttackBudgetBalanceEqualsCompositionWithNewComponents(t *testing.T) {
	f := NewFinanceAPI("attack-budget-new")
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.SetCreditLine(AcctHouseholds, gbp(1000000)))
	ck(t, f.SetCreditLine(AcctFirms, gbp(1000000)))
	ck(t, f.BeginMonth(1))
	seedTreasury(t, f, gbp(100000))

	if _, err := f.CollectTax(TaxRates{IncomeRate: 2000}, gbp(5000), 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.PostMaintenance(gbp(80), gbp(50)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.PostMaterials(gbp(33)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.PostStaffWages(gbp(44)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SettleOpex(gbp(22)); err != nil {
		t.Fatal(err)
	}
	if err := f.ServiceDebt(gbp(15), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.PostMaintenanceSpend(gbp(70), RepairPolicyRefitRebuild); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SettleConstruction(gbp(60)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SettleImports(gbp(25)); err != nil {
		t.Fatal(err)
	}

	b := f.OpexBreakdown()
	sumParts := b.Maintenance + b.StaffWages + b.Materials + b.Services + b.DebtService
	if f.ComposedOpex() != sumParts {
		t.Fatalf("ComposedOpex %d != sum of five parts %d", f.ComposedOpex(), sumParts)
	}
	// The five components must be independently the amounts posted.
	if b.Maintenance != gbp(50) || b.Materials != gbp(33) || b.StaffWages != gbp(44) || b.Services != gbp(22) || b.DebtService != gbp(15) {
		t.Fatalf("components wrong: %+v", b)
	}
	// Maintenance component is its OWN value, not folded into services (AC-1).
	if b.Maintenance == b.Services {
		t.Fatalf("maintenance %d indistinguishable from services %d", b.Maintenance, b.Services)
	}
	want := f.TaxRevenue()
	want = satSubMoney(want, f.ComposedOpex())
	want = satSubMoney(want, f.CapexTotal())
	want = satSubMoney(want, f.ConstructionTotal())
	want = satSubMoney(want, f.ImportsTotal())
	if f.BudgetBalance() != want {
		t.Fatalf("BudgetBalance %d != recomputed %d", f.BudgetBalance(), want)
	}
	// Capex must be the refit amount, and NOT be in any OPEX component.
	if f.CapexTotal() != gbp(70) {
		t.Fatalf("CapexTotal %d != 70", f.CapexTotal())
	}
	assertIdentity(t, f, "budget-new")
}

// ledgerFingerprint captures every observable that a partial post could
// corrupt: txn count, money stock, backlog, and every account balance.
func ledgerFingerprint(t *testing.T, f *FinanceAPI) string {
	t.Helper()
	fp := ""
	ms := f.MoneyStock()
	fp += "stock=" + itoa(int64(ms.Closing)) + ",delta=" + itoa(int64(ms.TrackedDelta))
	fp += ",backlog=" + itoa(int64(f.MaintenanceBacklog()))
	for _, id := range allAccountIDs() {
		bal, _ := f.AccountBalance(id)
		fp += "," + string(id) + "=" + itoa(int64(bal))
	}
	fp += ",lines=" + itoa(int64(len(f.Lines(AcctTreasury))))
	return fp
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	s := string(buf[i:])
	if neg {
		s = "-" + s
	}
	return s
}

// TestAttackPartialPostAtomicity proves AC-11: every rejected settlement
// input leaves the ledger byte-identical (no partial post, no silent clamp).
func TestAttackPartialPostAtomicity(t *testing.T) {
	f := NewFinanceAPI("attack-atomic")
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.SetOpexConfig(testOpexCfg()))
	ck(t, f.BeginMonth(1))
	seedTreasury(t, f, gbp(1000))
	// Establish some backlog so a rejected call that touches it would show.
	if _, err := f.PostMaintenance(gbp(100), gbp(20)); err != nil {
		t.Fatal(err)
	}

	before := ledgerFingerprint(t, f)

	type badCase struct {
		name string
		call func() error
		code string
	}
	cases := []badCase{
		{"negative demand", func() error { _, e := f.PostMaintenance(-1, gbp(10)); return e }, ErrMaintenanceDemandNegative},
		{"negative funded", func() error { _, e := f.PostMaintenance(gbp(10), -1); return e }, ErrMaintenanceFundedNegative},
		{"negative materials", func() error { _, e := f.PostMaterials(-1); return e }, ErrNegativeAmount},
		{"negative staffwages", func() error { _, e := f.PostStaffWages(-1); return e }, ErrNegativeAmount},
		{"zero-cost capex", func() error { _, e := f.PostCapexSpend(0); return e }, ErrCapexUnclassified},
		{"negative capex", func() error { _, e := f.PostCapexSpend(-1); return e }, ErrNegativeAmount},
		{"negative maintenance spend", func() error { _, e := f.PostMaintenanceSpend(-1, RepairPolicyAuto); return e }, ErrNegativeAmount},
		// Overdraft: fund far beyond treasury balance — must reject with no post.
		{"maintenance funded > treasury", func() error { _, e := f.PostMaintenance(gbp(999999), gbp(999999)); return e }, ErrInsufficientFunds},
		{"capex > treasury", func() error { _, e := f.PostCapexSpend(gbp(999999)); return e }, ErrInsufficientFunds},
	}
	for _, c := range cases {
		err := c.call()
		if err == nil {
			t.Fatalf("%s: expected rejection, got nil", c.name)
		}
		if !hasCode(err, c.code) {
			t.Fatalf("%s: wrong code: %v (want %s)", c.name, err, c.code)
		}
		if after := ledgerFingerprint(t, f); after != before {
			t.Fatalf("%s: PARTIAL POST — ledger changed:\n before=%s\n after =%s", c.name, before, after)
		}
	}
}

// TestAttackEfficiencyMonotonicAndRecovery proves the efficiency factor is a
// monotone-decreasing function of a growing backlog and recovers (increases)
// only when the backlog is paid down — never spuriously.
func TestAttackEfficiencyMonotonicAndRecovery(t *testing.T) {
	f := NewFinanceAPI("attack-eff")
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.SetOpexConfig(testOpexCfg()))

	var prevEff BasisPoints = 10001
	// Phase 1: underfund every month, efficiency must never increase.
	for m := int64(1); m <= 20; m++ {
		ck(t, f.BeginMonth(m))
		seedTreasury(t, f, gbp(100000))
		if _, err := f.PostMaintenance(gbp(1000), gbp(10)); err != nil {
			t.Fatal(err)
		}
		eff, err := f.MaintenanceEfficiency()
		if err != nil {
			t.Fatal(err)
		}
		if eff > prevEff {
			t.Fatalf("m%d: efficiency INCREASED while underfunded: %d > %d", m, eff, prevEff)
		}
		prevEff = eff
	}
	underfundedEff := prevEff
	backlogPeak := f.MaintenanceBacklog()

	// Phase 2: massively overfund — backlog pays down, efficiency recovers.
	ck(t, f.BeginMonth(100))
	seedTreasury(t, f, gbp(100000))
	if _, err := f.PostMaintenance(gbp(0), gbp(0)); err != nil {
		t.Fatal(err)
	}
	// Overfund: funded > demand pays down backlog.
	if _, err := f.PostMaintenance(0, backlogPeak); err != nil {
		t.Fatal(err)
	}
	if f.MaintenanceBacklog() != 0 {
		t.Fatalf("backlog not paid down: %d", f.MaintenanceBacklog())
	}
	recoveredEff, err := f.MaintenanceEfficiency()
	if err != nil {
		t.Fatal(err)
	}
	if recoveredEff <= underfundedEff {
		t.Fatalf("efficiency did not recover: %d <= underfunded %d", recoveredEff, underfundedEff)
	}
	if recoveredEff != 10000 {
		t.Fatalf("full recovery expected 10000, got %d", recoveredEff)
	}
}

// TestAttackBacklogSurvivesMidAccumulationSaveRestore proves the backlog and
// the derived identity survive a save taken MID-accumulation (non-zero
// backlog, a populated tickTxns log), byte-for-byte.
func TestAttackBacklogSurvivesMidAccumulationSaveRestore(t *testing.T) {
	orig := NewFinanceAPI("orig-bl")
	ck(t, orig.SetMilestoneGate(allowAllGate{}))
	ck(t, orig.SetOpexConfig(testOpexCfg()))

	// Two months to grow a persistent backlog, then a third mid-tick with
	// live tickTxns not yet closed.
	for m := int64(1); m <= 2; m++ {
		ck(t, orig.BeginMonth(m))
		seedTreasury(t, orig, gbp(100000))
		if _, err := orig.PostMaintenance(gbp(200), gbp(30)); err != nil {
			t.Fatal(err)
		}
	}
	ck(t, orig.BeginMonth(3))
	seedTreasury(t, orig, gbp(100000))
	if _, err := orig.PostMaintenance(gbp(200), gbp(30)); err != nil {
		t.Fatal(err)
	}
	if _, err := orig.PostMaterials(gbp(15)); err != nil {
		t.Fatal(err)
	}
	if _, err := orig.PostMaintenanceSpend(gbp(40), RepairPolicyRefitRebuild); err != nil {
		t.Fatal(err)
	}
	if orig.MaintenanceBacklog() == 0 {
		t.Fatal("precondition: backlog should be non-zero mid-accumulation")
	}
	assertIdentity(t, orig, "orig-mid")

	root := saveInto(t, orig, "orig-bl")
	reloaded := NewFinanceAPI("reloaded-bl")
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded)}, "reloaded-bl")
	if _, _, err := mgr.Load(manualBundleDir(t, root)); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if orig.MaintenanceBacklog() != reloaded.MaintenanceBacklog() {
		t.Fatalf("backlog did not round-trip: %d != %d", orig.MaintenanceBacklog(), reloaded.MaintenanceBacklog())
	}
	// Efficiency config is composition-root-injected (not saved) — inject the
	// same config and prove the efficiency matches.
	ck(t, reloaded.SetOpexConfig(testOpexCfg()))
	oe, err := orig.MaintenanceEfficiency()
	ck(t, err)
	re, err := reloaded.MaintenanceEfficiency()
	ck(t, err)
	if oe != re {
		t.Fatalf("efficiency diverged after restore: %d != %d", oe, re)
	}
	// The tick-scoped opex/capex figures and the identity must all round-trip.
	if orig.ComposedOpex() != reloaded.ComposedOpex() {
		t.Fatalf("ComposedOpex diverged: %d != %d", orig.ComposedOpex(), reloaded.ComposedOpex())
	}
	if orig.CapexTotal() != reloaded.CapexTotal() {
		t.Fatalf("CapexTotal diverged: %d != %d", orig.CapexTotal(), reloaded.CapexTotal())
	}
	if orig.NetOther() != reloaded.NetOther() {
		t.Fatalf("NetOther diverged: %d != %d", orig.NetOther(), reloaded.NetOther())
	}
	assertIdentity(t, reloaded, "reloaded-mid")
	if !reflect.DeepEqual(orig.OpexBreakdown(), reloaded.OpexBreakdown()) {
		t.Fatalf("OpexBreakdown diverged:\n a=%+v\n b=%+v", orig.OpexBreakdown(), reloaded.OpexBreakdown())
	}
}

// TestAttackOldSaveWithoutBacklogDecodesToZero simulates an old save that
// predates the backlog field (JSON with no "backlog" key) and proves it
// decodes to the sane zero default, not garbage.
func TestAttackOldSaveWithoutBacklogDecodesToZero(t *testing.T) {
	// A fresh API's backlog is zero; after resetForLoad and a meta record
	// lacking Backlog, it must stay zero. We exercise the decode path
	// directly by applying a meta record with no backlog field.
	f := NewFinanceAPI("old-save")
	ck(t, f.resetForLoad())
	rec := serialize.Record{Kind: recMeta, Data: []byte(`{"nextTxID":1,"month":5,"moneyStock":0}`)}
	if err := f.applyLoadRecord(rec); err != nil {
		t.Fatalf("applyLoadRecord: %v", err)
	}
	if f.backlog != 0 {
		t.Fatalf("old save without backlog decoded to %d, want 0", f.backlog)
	}
	if f.Month() != 5 {
		t.Fatalf("month field failed to decode: %d", f.Month())
	}
}
