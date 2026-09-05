package finance

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
)

// ---------------------------------------------------------------------------
// AC-2 — domain↔wire field-parity drift tests.
//
// Every serialized domain type must have a wire projection covering all of
// its fields; a future field added to a domain type without a wire
// counterpart must FAIL the build here (the participant.go:AC-2 obligation).
// Mirrors save/fixture_test.go's TestWidgetWireFieldsMatchWidget, generalised
// to the several finance domain types.
// ---------------------------------------------------------------------------

// assertFieldParity checks that domain and wire have the same number of
// fields and that every domain field has a wire counterpart of the same
// reflect.Kind. rename maps a domain field name to its wire field name where
// they differ (a serialized-but-unexported domain field whose wire
// counterpart must be exported to be JSON-marshalled).
func assertFieldParity(t *testing.T, domain, wire reflect.Type, rename map[string]string) {
	t.Helper()
	if domain.NumField() != wire.NumField() {
		t.Fatalf("%s has %d fields but wire %s has %d -- every serialized %s field must have a wire counterpart or an explicit, commented exclusion (AC-2)",
			domain.Name(), domain.NumField(), wire.Name(), wire.NumField(), domain.Name())
	}
	for i := 0; i < domain.NumField(); i++ {
		df := domain.Field(i)
		want := df.Name
		if r, ok := rename[df.Name]; ok {
			want = r
		}
		wf, ok := wire.FieldByName(want)
		if !ok {
			t.Fatalf("%s field %q has no counterpart %s.%s", domain.Name(), df.Name, wire.Name(), want)
		}
		if wf.Type.Kind() != df.Type.Kind() {
			t.Fatalf("%s.%s has kind %s, want %s to match %s.%s", wire.Name(), wf.Name, wf.Type.Kind(), df.Type.Kind(), domain.Name(), df.Name)
		}
	}
}

func TestFinanceWireFieldsMatchDomain(t *testing.T) {
	// Use (*T)(nil).Elem() rather than T{} so no value (and no embedded
	// sync.RWMutex) is ever copied for reflection (go vet copylocks).
	assertFieldParity(t, reflect.TypeOf((*Entry)(nil)).Elem(), reflect.TypeOf((*entryWire)(nil)).Elem(), nil)
	assertFieldParity(t, reflect.TypeOf((*Transaction)(nil)).Elem(), reflect.TypeOf((*transactionWire)(nil)).Elem(), nil)
	assertFieldParity(t, reflect.TypeOf((*accountState)(nil)).Elem(), reflect.TypeOf((*accountStateWire)(nil)).Elem(), nil)
	assertFieldParity(t, reflect.TypeOf((*Loan)(nil)).Elem(), reflect.TypeOf((*loanWire)(nil)).Elem(), nil)
	assertFieldParity(t, reflect.TypeOf((*InvestmentProgramme)(nil)).Elem(), reflect.TypeOf((*investmentWire)(nil)).Elem(), nil)
	// SimpleFirm.lossStreak is unexported state that MUST be serialized (it
	// gates AdvanceMonth's close), so its wire counterpart is the exported
	// LossStreak.
	assertFieldParity(t, reflect.TypeOf((*SimpleFirm)(nil)).Elem(), reflect.TypeOf((*simpleFirmWire)(nil)).Elem(), map[string]string{"lossStreak": "LossStreak"})
}

// TestFinanceAPIFieldsAllClassified is the highest-teeth AC-2 test: every
// field of FinanceAPI itself must be either explicitly EXCLUDED (runtime/
// config, never persisted) or COVERED by the save (a wire field/record). A
// new ledger field added to FinanceAPI that is neither serialized nor
// consciously excluded FAILS the build here -- the "built but not
// serialized" class this pilot exists to prevent.
func TestFinanceAPIFieldsAllClassified(t *testing.T) {
	// Excluded: runtime/config, deliberately NOT part of a save.
	excluded := map[string]string{
		"mu":                        "runtime lock, not state",
		"correlationID":             "per-instance error correlation, not simulation state",
		"gate":                      "injected MilestoneGate config, wired by the composition root on load",
		"modeGate":                  "FEAT-143 injected Real-vs-Unlimited-Money ModeGate config (SetModeGate), wired by the composition root on load like gate -- a session's locked mode itself is feat.gameinit/feat.saveux's own save.Meta.GameMode concern (AC-4), never re-derived from this transient injected policy pointer",
		"self":                      "SEC-020 copy-guard pointer, re-armed by NewFinanceAPI",
		"opexCfg":                   "FEAT-094 injected OPEX balance config (SetOpexConfig/LoadOpexConfig), wired by the composition root on load like gate",
		"lastPayrollShortfall":      "BUG-548 GR#17 status surface, transient this-month observability only (PayrollShortfall()) -- not conservation-relevant ledger state; a reload starting fresh (0, no shortfall) until financeHook next runs is an acceptable, self-correcting gap, unlike the ledger/loan/firm state this test otherwise guards",
		"lastPayrollShortfallMonth": "BUG-548 GR#17 status surface, transient this-month observability only (PayrollShortfall()) -- see lastPayrollShortfall's exclusion reason",
		"lastModeGateErr":           "FEAT-143 round P2-B GR#17 status surface, transient observability only (ModeGateError()) -- mirrors lastPayrollShortfall's exclusion reason exactly: a reload starting fresh (nil, no recorded failure) until the next unlimitedLocked() check re-derives it is an acceptable, self-correcting gap, not conservation-relevant ledger state",
	}
	// Covered: serialized via financeMetaWire or a per-item record.
	covered := map[string]bool{
		"accounts": true, "role": true, "txns": true, "tickTxns": true,
		"nextTxID": true, "moneyStock": true, "openingStock": true, "trackedDelta": true,
		"month": true, "creditLines": true, "totalCreditLine": true, "loans": true,
		"nextLoanID": true, "totalDebt": true, "missedPayments": true,
		"insolvencyMonths": true, "gameOver": true, "firms": true, "nextFirmID": true,
		"investments": true, "nextInvestID": true,
		"backlog":            true,
		"cremationShortfall": true, "lastCremationShortfallMonth": true,
	}
	ft := reflect.TypeOf((*FinanceAPI)(nil)).Elem()
	for i := 0; i < ft.NumField(); i++ {
		name := ft.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("FinanceAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- AC-2 forbids a silently-unsaved ledger field", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("FinanceAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Shared driver + comparison helpers.
// ---------------------------------------------------------------------------

func ck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// driveFinance runs a fixed, deterministic multi-month sequence exercising
// every serialized collection: accounts (balances move), txns/tickTxns,
// credit lines, loans + debt, firms, investments, insolvency counter, id
// counters, the money-stock triple. No RNG anywhere (finance is RNG-free).
func driveFinance(t *testing.T, f *FinanceAPI) {
	t.Helper()
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.SetCreditLine(AcctTreasury, gbp(1000)))

	firm, err := NewSimpleFirm("acme", gbp(100), gbp(20), gbp(10), gbp(5))
	ck(t, err)
	_, err = f.RegisterFirm(firm)
	ck(t, err)

	for m := int64(1); m <= 3; m++ {
		ck(t, f.BeginMonth(m))
		seedTreasury(t, f, gbp(500))
		_, err := f.PostWages(gbp(100))
		ck(t, err)
		_, err = f.PostHouseholdSpend(10, gbp(2)) // spend = gbp(20)
		ck(t, err)
		_, err = f.CollectTax(TaxRates{IncomeRate: 1000, SalesRate: 500, CorpRate: 2000},
			f.WagesPosted(), f.SpendPosted(), f.TotalFirmProfit())
		ck(t, err)
		_, err = f.SettleOpex(gbp(30))
		ck(t, err)
		_, err = f.SettleConstruction(gbp(10))
		ck(t, err)
		_, err = f.AllocateToReserves(gbp(50))
		ck(t, err)
		_, err = f.AccrueReserveInterest(100)
		ck(t, err)
		if m == 1 {
			_, err = f.Borrow(LoanRequest{Tier: 0, Principal: gbp(200), TermMonths: 12})
			ck(t, err)
		}
		ck(t, f.ServiceDebt(gbp(2), gbp(8)))
		ck(t, f.MissPayment(1))
		_, err = f.StartInvestment("capex", gbp(40), gbp(5), 24)
		ck(t, err)
		// Alternate the solvency outcome so both RecordMonthResult branches
		// (increment vs reset) and the resulting counter are exercised.
		f.RecordMonthResult(m == 2, false)
		// BUG-733: exercise the accruing cremation-shortfall balance so the
		// round-trip test proves it survives save/load like every other
		// running total here (never the PayrollShortfall-style transient
		// exclusion — see cremationShortfall's field doc).
		f.RecordCremationShortfall(m, gbp(1))
	}
	// One partial repayment mid-drive, so the persisted balance is not a
	// simple running sum of RecordCremationShortfall calls alone.
	f.RepayCremationShortfall(gbp(1))
}

// allAccountIDs is the fixed, sorted set of accounts driveFinance touches.
func allAccountIDs() []AccountID {
	ids := []AccountID{AcctTreasury, AcctHouseholds, AcctFirms, AcctReserves, AcctDebt, AcctExternal}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// compareFinance asserts a and b are observably identical through finance's
// own public accessors (AC-5's comparison surface), and that b (the reloaded
// instance) internally RECONCILES: RecomputeMoneyStock (a from-scratch walk
// of the restored txns log) equals the restored running MoneyStock().Closing
// -- the proof the append-only txns log round-tripped.
func compareFinance(t *testing.T, a, b *FinanceAPI, label string) {
	t.Helper()

	if a.Month() != b.Month() {
		t.Fatalf("%s: Month %d != %d", label, a.Month(), b.Month())
	}
	if a.MoneyStock() != b.MoneyStock() {
		t.Fatalf("%s: MoneyStock %+v != %+v", label, a.MoneyStock(), b.MoneyStock())
	}
	if a.TotalMoneyInCirculation() != b.TotalMoneyInCirculation() {
		t.Fatalf("%s: TotalMoneyInCirculation %d != %d", label, a.TotalMoneyInCirculation(), b.TotalMoneyInCirculation())
	}
	if a.RecomputeMoneyStock() != b.RecomputeMoneyStock() {
		t.Fatalf("%s: RecomputeMoneyStock %d != %d", label, a.RecomputeMoneyStock(), b.RecomputeMoneyStock())
	}
	// Reconciliation of the RELOADED instance: the from-scratch ledger walk
	// must equal the restored running total. If the txns log did not
	// round-trip, these diverge.
	if got, want := b.RecomputeMoneyStock(), b.MoneyStock().Closing; got != want {
		t.Fatalf("%s: reloaded RecomputeMoneyStock (from-scratch txns walk) %d != running MoneyStock().Closing %d -- txns log did not round-trip", label, got, want)
	}
	if a.OutstandingDebt() != b.OutstandingDebt() {
		t.Fatalf("%s: OutstandingDebt %d != %d", label, a.OutstandingDebt(), b.OutstandingDebt())
	}
	if a.AvailableCredit() != b.AvailableCredit() {
		t.Fatalf("%s: AvailableCredit %d != %d", label, a.AvailableCredit(), b.AvailableCredit())
	}
	if a.InsolvencyMonths() != b.InsolvencyMonths() {
		t.Fatalf("%s: InsolvencyMonths %d != %d", label, a.InsolvencyMonths(), b.InsolvencyMonths())
	}
	if a.IsInsolvent() != b.IsInsolvent() {
		t.Fatalf("%s: IsInsolvent %v != %v", label, a.IsInsolvent(), b.IsInsolvent())
	}
	if a.CreditRatingNow() != b.CreditRatingNow() {
		t.Fatalf("%s: CreditRatingNow %d != %d", label, a.CreditRatingNow(), b.CreditRatingNow())
	}
	if a.CurrentInterestRate() != b.CurrentInterestRate() {
		t.Fatalf("%s: CurrentInterestRate %d != %d", label, a.CurrentInterestRate(), b.CurrentInterestRate())
	}
	if a.TotalFirmProfit() != b.TotalFirmProfit() {
		t.Fatalf("%s: TotalFirmProfit %d != %d", label, a.TotalFirmProfit(), b.TotalFirmProfit())
	}
	// Tick figures depend on the restored tickTxns log.
	if a.TaxRevenue() != b.TaxRevenue() {
		t.Fatalf("%s: TaxRevenue %d != %d", label, a.TaxRevenue(), b.TaxRevenue())
	}
	if a.OpexTotal() != b.OpexTotal() {
		t.Fatalf("%s: OpexTotal %d != %d", label, a.OpexTotal(), b.OpexTotal())
	}
	if a.ConstructionTotal() != b.ConstructionTotal() {
		t.Fatalf("%s: ConstructionTotal %d != %d", label, a.ConstructionTotal(), b.ConstructionTotal())
	}
	if a.WagesPosted() != b.WagesPosted() {
		t.Fatalf("%s: WagesPosted %d != %d", label, a.WagesPosted(), b.WagesPosted())
	}
	if a.SpendPosted() != b.SpendPosted() {
		t.Fatalf("%s: SpendPosted %d != %d", label, a.SpendPosted(), b.SpendPosted())
	}
	if a.BudgetBalance() != b.BudgetBalance() {
		t.Fatalf("%s: BudgetBalance %d != %d", label, a.BudgetBalance(), b.BudgetBalance())
	}
	if a.CremationShortfallOwed() != b.CremationShortfallOwed() {
		t.Fatalf("%s: CremationShortfallOwed %d != %d", label, a.CremationShortfallOwed(), b.CremationShortfallOwed())
	}
	{
		am, ao := a.CremationShortfall()
		bm, bo := b.CremationShortfall()
		if am != bm || ao != bo {
			t.Fatalf("%s: CremationShortfall (month=%d,owed=%d) != (month=%d,owed=%d)", label, am, ao, bm, bo)
		}
	}
	for _, id := range allAccountIDs() {
		ba, oka := a.AccountBalance(id)
		bb, okb := b.AccountBalance(id)
		if oka != okb || ba != bb {
			t.Fatalf("%s: AccountBalance(%s) (%d,%v) != (%d,%v)", label, id, ba, oka, bb, okb)
		}
		if !reflect.DeepEqual(a.Lines(id), b.Lines(id)) {
			t.Fatalf("%s: Lines(%s) differ:\n a=%+v\n b=%+v", label, id, a.Lines(id), b.Lines(id))
		}
	}
}

// saveInto drives a save of f's participant into a fresh bundle under a temp
// root and returns the bundle directory.
func saveInto(t *testing.T, f *FinanceAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(f)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-build"}
	ck(t, mgr.SaveManual(ctx, "det"))
	return root
}

// ---------------------------------------------------------------------------
// AC-5 — round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestFinanceParticipant_RoundTrip(t *testing.T) {
	orig := NewFinanceAPI("orig")
	driveFinance(t, orig)

	root := saveInto(t, orig, "orig")

	// Load into a FRESH FinanceAPI (its pre-opened well-known accounts are
	// replaced by the saved ledger).
	reloaded := NewFinanceAPI("reloaded")
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded)}, "reloaded")
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ck(t, err)

	compareFinance(t, orig, reloaded, "post-load")

	// Continue identical postings on BOTH and assert they stay equal (AC-5e):
	// a divergent restore would surface the moment new work builds on it.
	continueFinance(t, orig)
	continueFinance(t, reloaded)
	compareFinance(t, orig, reloaded, "post-continue")

	// Prove-can-fail #1: mutate one reloaded account balance -> divergence.
	reloaded2 := NewFinanceAPI("reloaded2")
	mgr2 := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded2)}, "reloaded2")
	_, _, err = mgr2.Load(manualBundleDir(t, root))
	ck(t, err)
	reloaded2.accounts[AcctTreasury].Balance += gbp(1)
	if ba, _ := orig.AccountBalance(AcctTreasury); func() bool { b, _ := reloaded2.AccountBalance(AcctTreasury); return b == ba }() {
		t.Fatalf("prove-can-fail: mutating a reloaded account balance did not diverge from the original")
	}

	// Prove-can-fail #2: mutate one reloaded txn amount -> RecomputeMoneyStock
	// no longer reconciles with the running total.
	reloaded3 := NewFinanceAPI("reloaded3")
	mgr3 := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded3)}, "reloaded3")
	_, _, err = mgr3.Load(manualBundleDir(t, root))
	ck(t, err)
	if len(reloaded3.txns) == 0 {
		t.Fatalf("test setup: reloaded ledger has no txns to mutate")
	}
	reloaded3.txns[0].Entries[0].Amount += gbp(1)
	if reloaded3.RecomputeMoneyStock() == reloaded3.MoneyStock().Closing {
		t.Fatalf("prove-can-fail: mutating a reloaded txn amount did not break RecomputeMoneyStock reconciliation")
	}
}

// continueFinance runs one more deterministic month, driving new postings so
// a post-load state that silently diverged would show up as unequal totals.
func continueFinance(t *testing.T, f *FinanceAPI) {
	t.Helper()
	ck(t, f.BeginMonth(4))
	seedTreasury(t, f, gbp(500))
	_, err := f.PostWages(gbp(120))
	ck(t, err)
	_, err = f.PostHouseholdSpend(15, gbp(2))
	ck(t, err)
	_, err = f.CollectTax(TaxRates{IncomeRate: 1000, SalesRate: 500, CorpRate: 2000},
		f.WagesPosted(), f.SpendPosted(), f.TotalFirmProfit())
	ck(t, err)
	_, err = f.SettleOpex(gbp(25))
	ck(t, err)
	ck(t, f.ServiceDebt(gbp(2), gbp(8)))
	f.RecordMonthResult(true, false)
	f.RecordCremationShortfall(4, gbp(1))
}

// ---------------------------------------------------------------------------
// AC-3 — byte determinism.
// ---------------------------------------------------------------------------

func TestFinanceParticipant_ByteDeterminism(t *testing.T) {
	f1 := NewFinanceAPI("run1")
	driveFinance(t, f1)
	root1 := saveInto(t, f1, "run1")

	f2 := NewFinanceAPI("run2")
	driveFinance(t, f2)
	root2 := saveInto(t, f2, "run2")

	dir1 := manualBundleDir(t, root1)
	dir2 := manualBundleDir(t, root2)

	files1 := allFiles(t, dir1)
	files2 := allFiles(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ck(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ck(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic finance state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

// manualBundleDir locates the single manual-save bundle directory under a
// save root (save's own manualDir path layout is unexported, so the test
// discovers the leaf bundle directory -- the one directly containing the
// header file -- by walking).
func manualBundleDir(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "header.json" {
			found = filepath.Dir(path)
		}
		return nil
	})
	ck(t, err)
	if found == "" {
		t.Fatalf("no bundle (header.json) found under %q", root)
	}
	return found
}

// allFiles returns every file under dir, relative to dir, sorted.
func allFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	ck(t, err)
	sort.Strings(out)
	return out
}
