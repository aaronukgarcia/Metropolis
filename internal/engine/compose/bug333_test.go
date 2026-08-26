package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG333_FinancePatchTreasuryConsistentWithChromeMirror proves
// buildFinanceBalanceSheetPatch sources its Treasury figure from the BUG-324
// publish-only mirror (st.publishedTreasury / treasuryPub) — the SAME single
// published source chrome.topbar reads — and NOT from engine.finance's
// AcctTreasury ledger account directly.
//
// HONEST CLAIM (BUG-333 r2, per the r1 attacker's findings): this is
// consistency-with-chrome hygiene, NOT a race fix. The pre-fix ledger read
// was already lock-safe (FinanceAPI.AccountBalance takes f.mu internally),
// no race reproduced under -race with the old code, and BUG-333's filed
// zeros symptom was already fixed by BUG-355 (which routes wages/tax through
// the FinanceAPI ledger and keeps the mirror synced at every phase boundary
// via syncMoneyFromLedger). What this change guarantees is that F2's balance
// sheet and the chrome top bar can NEVER disagree about the player's money,
// because both render the one published source — even if a future change
// lets the mirror and ledger diverge between phase boundaries.
//
// Because the running engine keeps mirror == ledger at every boundary, an
// engine-driven test cannot distinguish the two read sources (the r1 test's
// fatal flaw — it passed against both old and new code). So this test builds
// a standalone simState, bug308_test.go-style, and FORCES the two sources
// apart: the ledger's AcctTreasury holds one value, the mirror is seeded via
// setTreasury with a different one. Only a mirror read reports the mirror
// value.
//
// PROOF THIS CAN FAIL (RED): reverting finance_publish.go's
// buildFinanceBalanceSheetPatch Treasury read to the pre-fix ledger read
// (`treasury, ok := st.finance.AccountBalance(finance.AcctTreasury)`) makes
// this test fail — the patch then reports the ledger's 7,000,000 instead of
// the mirror's 10,000,000. Verified by scratch-copy revert during r2, then
// restored (see the BOW item's r2 comment for the run evidence).
func TestBUG333_FinancePatchTreasuryConsistentWithChromeMirror(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)

	// Give the ledger's AcctTreasury a nonzero balance DIFFERENT from the
	// mirror seed below, so the two candidate read sources are
	// distinguishable. Funded from the unconstrained AcctExternal source,
	// exactly as bug308_test.go's fixture does.
	const ledgerTreasury = finance.Money(7_000_000)
	if _, err := fin.Post(finance.Transaction{
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: ledgerTreasury, Category: finance.CatOpex},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: ledgerTreasury, Category: finance.CatOpex},
		},
	}); err != nil {
		t.Fatalf("Post(AcctTreasury seed): %v", err)
	}

	st := &simState{cid: cid, finance: fin}

	// Seed the publish mirror independently of the ledger, through the one
	// sanctioned writer (setTreasury, BUG-324) — the same pathway the
	// composed engine uses.
	const mirrorTreasury = int64(10_000_000)
	st.setTreasury(mirrorTreasury)

	// Fixture sanity: the divergence this test depends on must actually
	// exist, or a future refactor could silently make it as unfalsifiable
	// as the r1 test was.
	ledgerBal, ok := fin.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatal("AcctTreasury not found")
	}
	if int64(ledgerBal) == st.publishedTreasury() {
		t.Fatalf("fixture degenerate: ledger AcctTreasury (%d) == published mirror (%d); the test needs them to differ to distinguish the read sources", ledgerBal, st.publishedTreasury())
	}

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
	if len(patch.BalanceSheet.Assets) < 1 || patch.BalanceSheet.Assets[0].Label != "Treasury" {
		t.Fatalf("Assets[0] is not the Treasury line: %+v", patch.BalanceSheet.Assets)
	}

	got := patch.BalanceSheet.Assets[0].ValueMicropounds
	if got != mirrorTreasury {
		t.Errorf("patch Treasury = %d, want the published mirror value %d (BUG-324 single published source, matching chrome.topbar) — got the ledger's %d? then the read source regressed to AcctTreasury", got, mirrorTreasury, ledgerBal)
	}

	// NetWorth must be built from the same mirror figure (reserves and debt
	// are both zero in this fixture), or F2's own lines would disagree with
	// each other.
	if patch.BalanceSheet.NetWorth != mirrorTreasury {
		t.Errorf("NetWorth = %d, want %d (mirror treasury + 0 reserves - 0 debt)", patch.BalanceSheet.NetWorth, mirrorTreasury)
	}
}
