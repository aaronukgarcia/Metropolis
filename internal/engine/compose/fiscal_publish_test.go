package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uifinance "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// FEAT-233 (FEAT-1972079848) — the fiscal Sankey + loans sub-views now
// riding the EXISTING "f2.finance" view (REUSE discipline: no new view id;
// ui.screen.finance's wire schema already carried `sankey` and `loans` as
// documented omitempty fast-follows).

// TestFiscalSubViews_EndToEnd_DecodeThroughUIScreen subscribes to
// "f2.finance" against a REAL compose.Wire'd engine and decodes the patch
// through ui.screen.finance's own ApplyDelta/Sankey()/Loans() round trip —
// proving compose's independently-maintained schema copy actually
// round-trips (mirroring TestFinanceView_EndToEnd_DeltaMatchesLiveState's
// decode discipline for the two new fields).
func TestFiscalSubViews_EndToEnd_DecodeThroughUIScreen(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	// Give the month a real flow to publish before subscribing: drive one
	// monthly tick so the finance hook's BeginMonth window exists (the
	// bands may still be zero — baseline-one's stub posts only internal
	// transfers, which ASM-1220 excludes; non-zero band content is proven
	// at patch level below).
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}); err != nil {
		t.Fatalf("SendCommand(AdvanceTicks): %v", err)
	}

	subID, delta := subscribeAndAwaitFirstDelta(t, transport, uifinance.ViewSubscriptionName)

	scr := uifinance.New(errs.NewCorrelationID())
	scr.BindSubscription(subID)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: subID, Patch: delta.Patch})

	// Sankey: present after one advance, bands all anchored to "budget",
	// inflow rows first then outflow rows in FlowMatrix order.
	sk, have := scr.Sankey()
	if !have {
		t.Fatal("ui.screen.finance Sankey() reported have=false after ApplyDelta")
	}
	if len(sk.Bands) == 0 {
		t.Fatal("Sankey() returned zero bands — the view is publishing an empty payload (BUG-323's warning class)")
	}
	for i, b := range sk.Bands {
		if b.Source != financeSankeyBudgetNode && b.Target != financeSankeyBudgetNode {
			t.Fatalf("Bands[%d] = %s->%s: neither endpoint is the budget node", i, b.Source, b.Target)
		}
		if b.Amount < 0 {
			t.Fatalf("Bands[%d].Amount = %d: money is never negative (GR#16)", i, b.Amount)
		}
	}

	// Loans: present (possibly empty — baseline-one borrows nothing), each
	// row carrying a positive principal and term if populated.
	wireLoans, haveLoans := scr.Loans()
	if !haveLoans {
		t.Fatal("ui.screen.finance Loans() reported have=false after ApplyDelta")
	}
	for i, l := range wireLoans {
		if l.ID == "" || l.PrincipalMicropounds <= 0 || l.TermMonths <= 0 {
			t.Fatalf("wireLoans[%d] = %+v: degenerate loan row", i, l)
		}
	}
}

// TestFiscalSubViews_PatchMatchesFlowMatrix_ASM1220 pins the ASM-1220
// semantics at the patch level: with a hand-seeded ledger window, the
// published sankey bands are exactly the tax inflows plus the external
// outflows, wages/opex/construction never become bands, and the band sums
// equal FlowMatrix's totals queried live.
func TestFiscalSubViews_PatchMatchesFlowMatrix_ASM1220(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)

	// Seed and open one month-close window (mirrors the engine-side test's
	// shape but through exported calls only, since simState is unexported
	// here — buildFinanceBalanceSheetPatch needs a full st, so drive the
	// assertions through the wire JSON of a seeded st instead). We reuse
	// bug308_test.go's pattern of constructing a minimal simState.
	st := &simState{cid: cid, finance: fin}
	if _, err := fin.Post(finance.Transaction{
		Description: "seed treasury",
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: 1_000_000_000, Category: finance.Category("seed")},
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: 1_000_000_000, Category: finance.Category("seed")},
		},
	}); err != nil {
		t.Fatalf("seed treasury: %v", err)
	}
	if err := fin.BeginMonth(3); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	// Fund payers, collect every tax category, settle imports + interest,
	// AND post internal redistribution that must NOT appear.
	post := func(entries []finance.Entry) {
		t.Helper()
		if _, err := fin.Post(finance.Transaction{Description: "feat233", Entries: entries}); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	post([]finance.Entry{
		{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: 2_000_000, Category: finance.CatWages},
		{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: 2_000_000, Category: finance.CatWages},
	})
	post([]finance.Entry{
		{Account: finance.AcctHouseholds, Side: finance.SideDebit, Amount: 400_000, Category: finance.CatTaxIncome},
		{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: 400_000, Category: finance.CatTaxIncome},
	})
	post([]finance.Entry{
		{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: 150_000, Category: finance.CatImports},
		{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: 150_000, Category: finance.CatImports},
	})
	post([]finance.Entry{
		{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: 50_000, Category: finance.CatOpex},
		{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: 50_000, Category: finance.CatOpex},
	})

	raw, err := st.buildFinanceBalanceSheetPatch()
	if err != nil {
		t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
	}

	var patch struct {
		SchemaVersion int `json:"schemaVersion"`
		Sankey        *struct {
			Bands []struct {
				Source string `json:"source"`
				Target string `json:"target"`
				Amount int64  `json:"amount"`
			} `json:"bands"`
		} `json:"sankey"`
	}
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if patch.Sankey == nil {
		t.Fatal("sankey absent from the f2.finance patch — FEAT-233 must always send it")
	}

	wantOut := map[string]int64{string(finance.CatImports): 150_000}
	gotIn := map[string]int64{}
	gotOut := map[string]int64{}
	internal := map[string]bool{}
	for _, b := range patch.Sankey.Bands {
		switch {
		case b.Source != financeSankeyBudgetNode && b.Target == financeSankeyBudgetNode:
			gotIn[b.Source] += b.Amount
		case b.Source == financeSankeyBudgetNode && b.Target != financeSankeyBudgetNode:
			gotOut[b.Target] += b.Amount
		default:
			internal[b.Source+"->"+b.Target] = true
		}
	}
	if len(internal) != 0 {
		t.Fatalf("non-budget-anchored bands published: %v (ASM-1220: every band anchors to the budget)", internal)
	}
	// Inflow rows are exactly finance's tax categories (honest zero rows
	// for the un-collected ones — never absent rows, never fabricated
	// figures); wages/spend are internal redistribution and must not be
	// among them.
	if gotIn[string(finance.CatTaxIncome)] != 400_000 {
		t.Errorf("inflow band %s = %d, want 400000", finance.CatTaxIncome, gotIn[string(finance.CatTaxIncome)])
	}
	for _, banned := range []finance.Category{finance.CatWages, finance.CatSpend, finance.CatOpex, finance.CatConstruction} {
		if _, ok := gotIn[string(banned)]; ok {
			t.Errorf("inflow band %q published — internal redistribution is never a band (ASM-1220)", banned)
		}
		if _, ok := gotOut[string(banned)]; ok {
			t.Errorf("outflow band %q published — internal redistribution is never a band (ASM-1220)", banned)
		}
	}
	for cat, amt := range wantOut {
		if gotOut[cat] != amt {
			t.Errorf("outflow band %s = %d, want %d", cat, gotOut[cat], amt)
		}
	}
	if _, ok := gotOut[string(finance.CatOpex)]; ok {
		t.Errorf("opex published as an outflow band — opex is internal redistribution per ASM-1220")
	}

	fm := fin.FlowMatrix()
	if fm.TotalIn != finance.Money(400_000) || fm.TotalOut != finance.Money(150_000) {
		t.Fatalf("FlowMatrix totals drifted from the seeded window: in=%d out=%d", fm.TotalIn, fm.TotalOut)
	}
}
