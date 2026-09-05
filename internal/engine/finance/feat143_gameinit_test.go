package finance

import "testing"

// fakeModeGate is a minimal finance.ModeGate test double — feat.gameinit's
// *gameinit.GameInit satisfies this interface structurally in production;
// this package's own tests use a fake rather than importing gameinit (no
// engine.finance -> feat.gameinit edge is registered).
type fakeModeGate struct{ unlimited bool }

func (g fakeModeGate) Unlimited(string) (bool, error) { return g.unlimited, nil }

// TestModeGate_NilGateIsRealMode proves the documented default: a
// FinanceAPI that never calls SetModeGate behaves exactly like Real mode
// (the pre-FEAT-143 behaviour), so every existing test/caller is
// unaffected.
func TestModeGate_NilGateIsRealMode(t *testing.T) {
	f := NewFinanceAPI("t-nil-gate")
	if f.unlimitedLocked() {
		t.Fatalf("unlimitedLocked() with no gate installed = true, want false (Real mode default)")
	}
}

// TestModeGate_UnlimitedBypassesOverdraft (AC-2, causal not correlational
// per the AC's own false-pass-risk note): toggling ONLY the mode gate,
// with the SAME zero-balance account state, flips the outcome — proving
// the mode is the CAUSE, not merely that a rich city happens not to fail.
func TestModeGate_UnlimitedBypassesOverdraft(t *testing.T) {
	newUnderfundedAPI := func() *FinanceAPI {
		f := NewFinanceAPI("t-overdraft")
		// AcctTreasury starts at zero balance, no credit line — an ordinary
		// placement/OPEX/payroll debit against it would overdraw.
		return f
	}
	debit := Transaction{
		Description: "placement debit against an empty treasury",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: 1000, Category: CatOpex},
			{Account: AcctFirms, Side: SideCredit, Amount: 1000, Category: CatOpex},
		},
	}

	real := newUnderfundedAPI()
	if err := real.SetModeGate(fakeModeGate{unlimited: false}); err != nil {
		t.Fatalf("SetModeGate(real): %v", err)
	}
	if _, err := real.Post(debit); err == nil {
		t.Fatalf("Real mode: Post succeeded against an empty treasury with no credit line, want ErrInsufficientFunds")
	}

	unlimited := newUnderfundedAPI()
	if err := unlimited.SetModeGate(fakeModeGate{unlimited: true}); err != nil {
		t.Fatalf("SetModeGate(unlimited): %v", err)
	}
	if _, err := unlimited.Post(debit); err != nil {
		t.Fatalf("Unlimited mode: Post failed against the IDENTICAL empty-treasury state, want success (AC-2): %v", err)
	}
}

// TestModeGate_UnlimitedStillRejectsStructuralErrors proves the bypass is
// scoped to the overdraft check ONLY — an unbalanced transaction or an
// unknown account is still rejected in Unlimited mode, since those are
// programming errors, not insufficient-funds conditions.
func TestModeGate_UnlimitedStillRejectsStructuralErrors(t *testing.T) {
	f := NewFinanceAPI("t-structural")
	if err := f.SetModeGate(fakeModeGate{unlimited: true}); err != nil {
		t.Fatalf("SetModeGate: %v", err)
	}

	unbalanced := Transaction{
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: 1000, Category: CatOpex},
			{Account: AcctFirms, Side: SideCredit, Amount: 500, Category: CatOpex},
		},
	}
	if _, err := f.Post(unbalanced); err == nil {
		t.Fatalf("Unlimited mode: Post accepted an unbalanced transaction, want ErrUnbalancedTransaction")
	}

	unknownAccount := Transaction{
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: 1000, Category: CatOpex},
			{Account: AccountID("does-not-exist"), Side: SideCredit, Amount: 1000, Category: CatOpex},
		},
	}
	if _, err := f.Post(unknownAccount); err == nil {
		t.Fatalf("Unlimited mode: Post accepted an unknown account, want ErrUnknownAccount")
	}
}

// TestModeGate_UnlimitedInsolvencyInert (AC-2): InsolvencyMonths never
// advances and game-over never fires in Unlimited mode, even across many
// consecutive "obligations not met, no credit" months — the exact
// scenario that would trip Real mode's 3-month game-over trigger.
func TestModeGate_UnlimitedInsolvencyInert(t *testing.T) {
	f := NewFinanceAPI("t-insolvency-unlimited")
	if err := f.SetModeGate(fakeModeGate{unlimited: true}); err != nil {
		t.Fatalf("SetModeGate: %v", err)
	}
	for i := 0; i < 12; i++ {
		res := f.RecordMonthResult(false, false)
		if res.ConsecutiveFailedMonths != 0 {
			t.Fatalf("month %d: ConsecutiveFailedMonths = %d, want 0 (Unlimited mode never advances the counter)", i, res.ConsecutiveFailedMonths)
		}
		if res.GameOver {
			t.Fatalf("month %d: GameOver = true, want false (Unlimited mode never fires game-over)", i)
		}
	}
	if f.IsInsolvent() {
		t.Fatalf("IsInsolvent() = true after 12 failing months in Unlimited mode, want false")
	}
}

// TestModeGate_RealInsolvencyUnchanged proves the SAME 3-consecutive-
// failed-months scenario still fires game-over in Real mode — the mode
// gate changes behaviour, it does not silently disable the failure loop
// for everyone (US-4's "one finance code, mode as a gate" requirement).
func TestModeGate_RealInsolvencyUnchanged(t *testing.T) {
	f := NewFinanceAPI("t-insolvency-real")
	if err := f.SetModeGate(fakeModeGate{unlimited: false}); err != nil {
		t.Fatalf("SetModeGate: %v", err)
	}
	var last MonthResult
	for i := 0; i < 3; i++ {
		last = f.RecordMonthResult(false, false)
	}
	if last.ConsecutiveFailedMonths != 3 {
		t.Fatalf("ConsecutiveFailedMonths = %d, want 3 (Real mode's unchanged failure loop)", last.ConsecutiveFailedMonths)
	}
	if !last.GameOver {
		t.Fatalf("GameOver = false after 3 consecutive failed months in Real mode, want true")
	}
	if !f.IsInsolvent() {
		t.Fatalf("IsInsolvent() = false, want true")
	}
}
