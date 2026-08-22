package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-208 increment 2 (docs/planning — the FEAT-208 publish-path design
// proposal's §6 fast-follow list: "f2.finance ... the ApplyDelta side
// already exists"): the second real UI delta-publishing vertical slice,
// f2.finance's balanceSheet sub-view ONLY (PL/loans/creditRating/
// taxSliders/publicPayroll/sankey remain documented fast-follows,
// strictly additive — every field on ui.screen.finance's wirePatch is
// already `omitempty`, so no schemaVersion bump is needed when they
// land).
//
// This file mirrors services_publish.go's exact one-file-per-integration
// convention and, per the design's §3.3, builds compose's OWN copy of
// the wire schema — the same JSON tags as ui.screen.finance's wire.go's
// wireBalanceSheetView/wireBalanceItem, duplicated independently, NEVER
// importing internal/ui/screens/finance (GR#20's engine-never-imports-ui
// half of the seam, preserved here exactly as services_publish.go
// preserves it).
//
// balanceSheet was chosen over PL/loans/etc as the smallest coherent
// first slice: it needs only two already-composed FinanceAPI read
// accessors (AccountBalance, OutstandingDebt), no per-tick aggregation,
// and no new engine dependency (engine.finance is already composed —
// st.finance, compose.go's Wire).

// financeWireSchemaVersion mirrors ui.screen.finance/wire.go's
// wireSchemaVersion constant (kept as a separate, independently
// maintained value per the same GR#20/SF-1 discipline
// services_publish.go's identical constant follows).
const financeWireSchemaVersion = 1

// financeBalanceItem mirrors ui.screen.finance/wire.go's
// wireBalanceItem field-for-field.
type financeBalanceItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

// financeBalanceSheetView mirrors ui.screen.finance/wire.go's
// wireBalanceSheetView field-for-field.
type financeBalanceSheetView struct {
	Assets      []financeBalanceItem `json:"assets"`
	Liabilities []financeBalanceItem `json:"liabilities"`
	NetWorth    int64                `json:"netWorth"`
}

// financeBalanceSheetWirePatch is compose's own copy of
// ui.screen.finance/wire.go's wirePatch — only the BalanceSheet field is
// ever populated this increment; every other field is deliberately left
// nil (and therefore omitted, via wire.go's own `omitempty` tags on the
// UI side) rather than sent as an empty/zero value, so a future
// fast-follow sub-view (PL, loans, taxSliders, publicPayroll, sankey)
// can start sending its own field without this one changing shape.
type financeBalanceSheetWirePatch struct {
	SchemaVersion int                      `json:"schemaVersion"`
	BalanceSheet  *financeBalanceSheetView `json:"balanceSheet,omitempty"`
}

// buildFinanceBalanceSheetPatch reads st.finance's own synchronization
// (FinanceAPI.mu, an internal sync.RWMutex — see AccountBalance/
// OutstandingDebt's own doc comments) and returns the "f2.finance"
// balanceSheet-only patch (the design's §6 fast-follow, mirroring
// buildServicesCapacityDemandPatch's shape exactly).
//
// The city balance sheet this slice publishes is deliberately narrow:
// Assets = {Treasury, Reserves} (finance.AcctTreasury/AcctReserves, the
// city's own RoleMoney accounts), Liabilities = {Outstanding Debt}
// (FinanceAPI.OutstandingDebt, the maintained loan-principal running
// total), NetWorth = Assets - Liabilities. Households/Firms cash
// accounts (AcctHouseholds/AcctFirms) are deliberately EXCLUDED — they
// are the city's citizens'/firms' own money, not the city's assets; a
// city balance sheet that included them would overstate net worth by
// counting money the city does not own. Per the r1 addendum's corrected
// contract (subscribe.go's ViewPatchFunc doc comment): this function
// runs on the subscription pump goroutine, concurrently with
// tick-phase writes to simState — safe here only because every read
// goes through FinanceAPI's own accessor methods, each of which takes
// f.mu internally, never a plain simState field read.
func (st *simState) buildFinanceBalanceSheetPatch() (json.RawMessage, error) {
	treasury, ok := st.finance.AccountBalance(finance.AcctTreasury)
	if !ok {
		return nil, errs.New(ErrModuleFailed, st.cid, map[string]any{
			"module": "finance", "accessor": "AccountBalance", "account": string(finance.AcctTreasury),
		})
	}
	reserves, ok := st.finance.AccountBalance(finance.AcctReserves)
	if !ok {
		return nil, errs.New(ErrModuleFailed, st.cid, map[string]any{
			"module": "finance", "accessor": "AccountBalance", "account": string(finance.AcctReserves),
		})
	}
	debt := st.finance.OutstandingDebt()

	// BUG-308 fix 1: raw int64 +/- on saturated finance.Money values can
	// wrap negative (two near-MaxInt64 assets summing past the int64 top
	// wraps around to a large negative NetWorth rather than saturating,
	// which would render as a nonsensical city-bankrupt reading on the
	// wire). GR#3 check performed first: engine.finance's FinanceAPI
	// exposes no exported NetWorth/total accessor this could delegate to
	// instead — TotalMoneyInCirculation/TotalFirmProfit/BudgetBalance all
	// answer a DIFFERENT question (money stock across ALL accounts incl.
	// Households/Firms, firm profit, opex-vs-tax), and this view's own
	// Assets/Liabilities definition (§ doc comment above) deliberately
	// EXCLUDES Households/Firms, so there is no existing total to reuse —
	// this file keeps its own narrow Treasury+Reserves-Debt arithmetic,
	// just made saturating. Mirrors finance/money.go's own
	// satAddMoney/satSubMoney idiom (both of which are themselves thin
	// Money-typed adapters over these exact num functions — money.go's
	// helpers are unexported so compose calls foundation/num directly
	// rather than duplicating the adapter).
	netWorth := num.SatSub(num.SatAdd(int64(treasury), int64(reserves)), int64(debt))

	patch := financeBalanceSheetWirePatch{
		SchemaVersion: financeWireSchemaVersion,
		BalanceSheet: &financeBalanceSheetView{
			Assets: []financeBalanceItem{
				{Label: "Treasury", ValueMicropounds: int64(treasury)},
				{Label: "Reserves", ValueMicropounds: int64(reserves)},
			},
			Liabilities: []financeBalanceItem{
				{Label: "Outstanding Debt", ValueMicropounds: int64(debt)},
			},
			NetWorth: netWorth,
		},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of strings/int64s cannot fail;
		// unreachable in practice — mirrored on
		// buildServicesCapacityDemandPatch's identical "cannot fail"
		// branch. Per GR#1, degrade loudly rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "finance", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// financeViewSubscriptionName mirrors
// internal/ui/screens/finance/wire.go's ViewSubscriptionName constant
// VALUE ("f2.finance") — duplicated independently as compose's own
// string literal, never imported from internal/ui/screens/finance
// (GR#20's engine-never-imports-ui half of the seam; this file's own
// doc comment). Kept as its own named constant for the same reason
// servicesViewSubscriptionName is (a symbol a compose test can
// reference for the registered view-name set).
const financeViewSubscriptionName = "f2.finance"
