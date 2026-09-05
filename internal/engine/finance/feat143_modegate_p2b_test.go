package finance

import (
	"errors"
	"testing"
)

// erroringModeGate simulates the real-world P2-B failure shape: a
// composition root that (by mistake) handed FinanceAPI.SetModeGate a
// struct-copied *gameinit.GameInit, whose SEC-020 copy-guard rejects
// every call with an error rather than a silent zero value. This double
// is independent of finance's own test fakes (fakeModeGate/
// attackModeGate) precisely so this test does not accidentally rely on
// any of their behaviour.
type erroringModeGate struct {
	// wouldBeUnlimited records what the underlying (uncopied) session's
	// real mode was -- proving the fix fails toward Real regardless of
	// what the gate WOULD have said had it not errored.
	wouldBeUnlimited bool
}

var errModeGateCopyGuard = errors.New("simulated SEC-020 copy-guard rejection")

func (g erroringModeGate) Unlimited(string) (bool, error) {
	return g.wouldBeUnlimited, errModeGateCopyGuard
}

// TestFEAT143_P2B_ErroringModeGateFailsClosedAndRecords is the round's
// P2-B fix verification: a ModeGate that errors (the copied-*GameInit
// shape) must make FinanceAPI behave as REAL mode -- an ordinary
// zero-balance debit with no credit line is rejected exactly as it would
// be with no gate installed at all -- AND the failure must be recorded
// via ModeGateError() rather than disappearing with no trace, even though
// the underlying session was actually Unlimited.
func TestFEAT143_P2B_ErroringModeGateFailsClosedAndRecords(t *testing.T) {
	f := NewFinanceAPI("t-p2b-erroring-gate")
	gate := erroringModeGate{wouldBeUnlimited: true}
	if err := f.SetModeGate(gate); err != nil {
		t.Fatalf("SetModeGate: %v", err)
	}

	if err := f.ModeGateError(); err != nil {
		t.Fatalf("ModeGateError() before any check ran = %v, want nil", err)
	}

	debit := Transaction{
		Description: "placement debit against an empty treasury",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: 1000, Category: CatOpex},
			{Account: AcctFirms, Side: SideCredit, Amount: 1000, Category: CatOpex},
		},
	}
	if _, err := f.Post(debit); err == nil {
		t.Fatalf("Post succeeded against an empty treasury under an ERRORING mode gate that would have said Unlimited=true -- the failure must fail CLOSED toward Real mode (the stricter mode), never toward whatever the gate would have said")
	}

	gateErr := f.ModeGateError()
	if gateErr == nil {
		t.Fatalf("ModeGateError() = nil after a failing gate check -- the failure disappeared with NO trace on any channel, exactly the silent-downgrade risk P2-B exists to close")
	}
	if !errors.Is(gateErr, gateErr) { // sanity: gateErr is non-nil and comparable to itself
		t.Fatalf("ModeGateError() returned a non-comparable error")
	}
	if got := gateErr.Error(); got == "" {
		t.Fatalf("ModeGateError().Error() is empty")
	}

	// RecordMonthResult must ALSO fail closed toward Real (obligations
	// unmet with no credit advances the insolvency counter exactly as it
	// would with no gate at all).
	res := f.RecordMonthResult(false, false)
	if res.ConsecutiveFailedMonths != 1 {
		t.Fatalf("RecordMonthResult under an erroring gate: ConsecutiveFailedMonths = %d, want 1 (Real-mode behaviour, not Unlimited's inert counter)", res.ConsecutiveFailedMonths)
	}
}

// TestFEAT143_P2B_ModeGateErrorClearsOnRecovery proves ModeGateError is a
// live status surface, not a sticky latch: once the gate starts
// succeeding again, the recorded error clears.
func TestFEAT143_P2B_ModeGateErrorClearsOnRecovery(t *testing.T) {
	f := NewFinanceAPI("t-p2b-recovery")
	if err := f.SetModeGate(erroringModeGate{wouldBeUnlimited: true}); err != nil {
		t.Fatalf("SetModeGate(erroring): %v", err)
	}
	f.RecordMonthResult(false, false)
	if f.ModeGateError() == nil {
		t.Fatalf("precondition: ModeGateError() should be set after a failing check")
	}

	if err := f.SetModeGate(fakeModeGate{unlimited: false}); err != nil {
		t.Fatalf("SetModeGate(healthy): %v", err)
	}
	f.RecordMonthResult(true, false)
	if err := f.ModeGateError(); err != nil {
		t.Fatalf("ModeGateError() = %v after a successful gate check, want nil (the surface must clear, not stay latched on an old failure)", err)
	}
}
