package compose

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-308 (Bro audit M5+lows, LEAD estate — compose wire money-safety):
// three independent proof-of-failure tests, one per fix. Each test's own
// comment documents how it was verified to fail against the pre-fix code
// (RED), matching this package's existing "PROOF THIS CAN FAIL" idiom
// (see extcommute_wire_test.go/servicesfirms_wire_test.go).

// --- fix 1: finance_publish.go NetWorth saturation --------------------------

// TestBUG308_NetWorth_SaturatesInsteadOfWrapping proves
// buildFinanceBalanceSheetPatch's NetWorth calculation does not wrap
// negative when Treasury+Reserves would overflow int64. Posts two
// near-MaxInt64/2 credits from the unconstrained AcctExternal source into
// AcctTreasury and AcctReserves (both RoleMoney, no overdraft check on a
// credit), so int64(treasury)+int64(reserves) overflows int64's positive
// range on raw addition.
//
// PROOF THIS CAN FAIL (RED): reverting finance_publish.go's
// buildFinanceBalanceSheetPatch to `netWorth := int64(treasury) +
// int64(reserves) - int64(debt)` (plain int64 arithmetic, the pre-fix
// code) makes this test fail — the sum wraps to a large NEGATIVE value
// (verified by hand: treasury=reserves=MaxInt64/2+1,000,000 sums to
// MaxInt64+2,000,001, which wraps to math.MinInt64+2,000,000), which is
// exactly the money-integrity bug this test guards against. With the
// num.SatAdd/SatSub fix, the same inputs saturate at math.MaxInt64
// instead.
func TestBUG308_NetWorth_SaturatesInsteadOfWrapping(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)

	// Each half is just over MaxInt64/2 so the SUM overflows int64, but
	// neither individual posting does (finance.Post's own validateLocked
	// already saturates a single account's projected balance via
	// satAddMoney — this test is about the SEPARATE two-account SUM
	// compose performs afterwards, not finance's internal bookkeeping).
	half := finance.Money(math.MaxInt64/2 + 1_000_000)
	post := func(acct finance.AccountID) {
		t.Helper()
		if _, err := fin.Post(finance.Transaction{
			Entries: []finance.Entry{
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: half, Category: finance.CatOpex},
				{Account: acct, Side: finance.SideCredit, Amount: half, Category: finance.CatOpex},
			},
		}); err != nil {
			t.Fatalf("Post(%s): %v", acct, err)
		}
	}
	post(finance.AcctTreasury)
	post(finance.AcctReserves)

	treasury, ok := fin.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatal("AcctTreasury not found")
	}
	reserves, ok := fin.AccountBalance(finance.AcctReserves)
	if !ok {
		t.Fatal("AcctReserves not found")
	}
	if int64(treasury) <= 0 || int64(reserves) <= 0 {
		t.Fatalf("fixture degenerate: treasury=%d reserves=%d, want both positive and large", treasury, reserves)
	}

	st := &simState{cid: cid, finance: fin}
	raw, err := st.buildFinanceBalanceSheetPatch()
	if err != nil {
		t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
	}
	var patch financeBalanceSheetWirePatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if patch.BalanceSheet == nil {
		t.Fatal("balanceSheet absent from patch")
	}
	if patch.BalanceSheet.NetWorth < 0 {
		t.Fatalf("NetWorth = %d, want a non-negative (saturated) value — treasury=%d + reserves=%d must not wrap negative on overflow", patch.BalanceSheet.NetWorth, treasury, reserves)
	}
	if patch.BalanceSheet.NetWorth != math.MaxInt64 {
		t.Fatalf("NetWorth = %d, want exactly math.MaxInt64 (%d) — treasury+reserves overflow should saturate at the int64 ceiling", patch.BalanceSheet.NetWorth, int64(math.MaxInt64))
	}
}

// --- fix 2: extcommute_wire.go negative-amount rejection --------------------

// TestBUG308_ExtCommuteFinanceSeam_RejectsNegativeAmount proves
// extCommuteFinanceSeam.post rejects a negative amountMicropounds loudly
// (registry error MET-G807) and leaves the ledger completely untouched —
// a negative amount posted through the debit/credit pair would otherwise
// REVERSE the credit flow (money conservation, GR#16).
//
// PROOF THIS CAN FAIL (RED): removing the `if amount < 0` guard from
// extcommute_wire.go's post method makes this test fail two ways: (a) the
// call would not return an error (RecordOffMapWage would "succeed"), and
// (b) AcctHouseholds' balance would move by the negative amount (a DEBIT
// disguised as the credit call) instead of staying exactly where it
// started — both asserted below, verified by hand during development by
// temporarily deleting the guard, then reverted.
func TestBUG308_ExtCommuteFinanceSeam_RejectsNegativeAmount(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)
	seam := &extCommuteFinanceSeam{api: fin, cid: cid, monthFn: func() int64 { return 1 }}

	before, ok := fin.AccountBalance(finance.AcctHouseholds)
	if !ok {
		t.Fatal("AcctHouseholds not found")
	}

	err := seam.RecordOffMapWage(1, extcommuteTestPool, -1_000_000)
	if err == nil {
		t.Fatal("RecordOffMapWage accepted a negative amount — must be rejected loudly at the seam")
	}
	var appErr *errs.E
	if errors.As(err, &appErr) {
		if appErr.Code != ErrInvalidWireAmount {
			t.Fatalf("error code = %q, want %q", appErr.Code, ErrInvalidWireAmount)
		}
	} else {
		t.Fatalf("error is not an *errs.E: %T %v", err, err)
	}

	after, ok := fin.AccountBalance(finance.AcctHouseholds)
	if !ok {
		t.Fatal("AcctHouseholds not found after rejected post")
	}
	if after != before {
		t.Fatalf("AcctHouseholds balance = %d after a REJECTED negative post, want unchanged %d — the ledger must never move on a rejected seam call", after, before)
	}
	if lines := fin.LinesByCategory(finCatExtCommuteOffMapWage); len(lines) != 0 {
		t.Fatalf("LinesByCategory(offmap wage) = %d entries after a rejected negative post, want exactly 0", len(lines))
	}
}

// TestBUG308_ExtCommuteFinanceSeam_AcceptsNonNegativeAmount is the
// companion "the guard doesn't over-reject" proof: a genuine zero or
// positive amount must still post normally through every FinanceSeam
// verb's shared post() choke point.
func TestBUG308_ExtCommuteFinanceSeam_AcceptsNonNegativeAmount(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)
	seam := &extCommuteFinanceSeam{api: fin, cid: cid, monthFn: func() int64 { return 1 }}

	if err := seam.RecordOffMapWage(1, extcommuteTestPool, 0); err != nil {
		t.Fatalf("RecordOffMapWage(amount=0): %v", err)
	}
	if err := seam.RecordOffMapWage(1, extcommuteTestPool, 500_000); err != nil {
		t.Fatalf("RecordOffMapWage(amount=500000): %v", err)
	}
}

// --- fix 3: servicesfirms_wire.go half<=0 divisor guard --------------------

// TestBUG308_JobAvailabilityTerm_GuardsNonPositiveHalfSaturation proves
// jobAvailabilityTerm returns the documented guarded floor (0), not an
// accidental +Inf-clamped-to-100, when the half-saturation constant is
// non-positive.
//
// PROOF THIS CAN FAIL (RED): removing the `if half <= 0 { return 0, nil
// }` guard makes rate=5/half=-5 compute 100*5/(5+(-5)) = 100*5/0 = +Inf,
// which clampTerm's v>100 branch then clamps to 100 (not caught by the
// NaN guard, since +Inf is not NaN) — this test asserts exactly 0, so it
// fails with got=100 against the pre-fix code. Verified by hand during
// development, then reverted.
func TestBUG308_JobAvailabilityTerm_GuardsNonPositiveHalfSaturation(t *testing.T) {
	st := newServicesFirmsTestState(t, 11)

	// Force a firm with vacancies so rate > 0 (a zero rate would make the
	// pre-fix bug produce NaN, not +Inf — clampTerm's existing NaN guard
	// would mask that case and this test would not distinguish the two
	// bugs). A non-zero workforce (spawned citizens) is required first —
	// LabourMarket's VacancyRatePerMille is workforce-relative, mirroring
	// TestFEAT167Completion_JobAvailabilityRespondsToVacancyRate's fixture.
	spawnFeat167Citizens(t, st.citizens, 11, st.cid, 1, 50)
	if _, err := st.firms.RegisterFirm("startup", 0, "industrial"); err != nil {
		t.Fatalf("RegisterFirm: %v", err)
	}
	lm, err := st.firms.LabourMarket()
	if err != nil {
		t.Fatalf("LabourMarket: %v", err)
	}
	if lm.VacancyRatePerMille <= 0 {
		t.Fatalf("fixture degenerate: VacancyRatePerMille = %d, want > 0", lm.VacancyRatePerMille)
	}

	st.attractTerms.JobAvailability.VacancyRateHalfSaturationPerMille = -float64(lm.VacancyRatePerMille)

	term, err := st.jobAvailabilityTerm()
	if err != nil {
		t.Fatalf("jobAvailabilityTerm: %v", err)
	}
	if term != 0 {
		t.Fatalf("jobAvailabilityTerm with half=%v (non-positive) = %v, want exactly 0 (the guarded floor, not an accidental +Inf-clamped-to-100)", st.attractTerms.JobAvailability.VacancyRateHalfSaturationPerMille, term)
	}

	// half == 0 exactly: rate>0 makes this rate/rate == 1 pre-guard (not
	// actually +Inf), but the guard must still fire uniformly for every
	// half<=0 input rather than special-casing strictly-negative.
	st.attractTerms.JobAvailability.VacancyRateHalfSaturationPerMille = 0
	term, err = st.jobAvailabilityTerm()
	if err != nil {
		t.Fatalf("jobAvailabilityTerm (half=0): %v", err)
	}
	if term != 0 {
		t.Fatalf("jobAvailabilityTerm with half=0 = %v, want exactly 0", term)
	}
}
