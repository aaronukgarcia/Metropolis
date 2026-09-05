package compose

import (
	"encoding/json"
	"fmt"

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

	// UnlimitedMoney is BUG-737's FEAT-143 AC-7 wiring: mirrors
	// internal/ui/screens/finance/wire.go's wirePatch.UnlimitedMoney
	// field-for-field (same JSON tag, same *bool pointer semantics — GR#20's
	// engine-never-imports-ui half of the seam, this file's own doc comment
	// above). A non-nil pointer is the real gi.Unlimited() value read every
	// publish tick; nil (never sent, via the UI side's own omitempty) means
	// "not yet known" and is only reachable if gameinit were somehow unwired
	// (never true after a successful Wire — wireGameInit either constructs
	// gi or Wire itself fails).
	UnlimitedMoney *bool `json:"unlimitedMoney,omitempty"`

	// PayrollShortfall is BUG-723's fix: the built-but-not-wired gap
	// where FinanceAPI.RecordPayrollShortfall (BUG-548) was set/cleared
	// every month but nothing outside a test ever called
	// FinanceAPI.PayrollShortfall()/PayrollShortfallMonths() to surface
	// it. Mirrors the TS wire.ts FinancePayrollShortfallView copy (same
	// GR#20-adjacent independent-duplication discipline every field on
	// this patch follows). This function already assumes st.finance is
	// non-nil (see AccountBalance/OutstandingDebt above, which would
	// already have panicked otherwise), so this is populated every
	// publish tick in production — the `omitempty` tag exists purely
	// for wire-shape symmetry with the other optional sections.
	PayrollShortfall *financePayrollShortfallView `json:"payrollShortfall,omitempty"`

	// CreditRating is BUG-759's wiring of a REAL, already-consumed gap:
	// internal/ui/screens/finance's own wirePatch/Screen (wire.go/screen.go)
	// have carried a CreditRating field and a Screen.CreditRating()
	// accessor (read by cmd/metropolis/boot.go) since before this fix, but
	// compose's independent wire-patch copy (this struct, per this file's
	// own doc comment on why it duplicates rather than imports
	// internal/ui/screens/finance, GR#20) never populated or sent the
	// field — the UI's ApplyDelta `if p.CreditRating != nil` branch could
	// never fire. Same *int JSON shape as the UI side field-for-field;
	// value is FinanceAPI.CreditRatingNow() read live every publish tick
	// (never cached — mirrors UnlimitedMoney's own live-read rationale
	// above), nil ONLY on FinanceAPI's SEC-020 copy-guard violation
	// (st.finance.Valid() — see buildFinanceBalanceSheetPatch, unreachable
	// in production; st.finance itself is never nil after Wire, unlike
	// st.gameInit above). CreditRatingHistory is a documented separate
	// fast-follow (trend tracking is a distinct feature from publishing
	// the current score) and stays unpopulated here.
	CreditRating *int `json:"creditRating,omitempty"`

	// InsolvencyMonths/Insolvent are BUG-769's wiring of the OTHER real,
	// already-consumed BUG-759 gap: FinanceAPI.InsolvencyMonths()/
	// IsInsolvent() (insolvency.go) have advanced for real in production
	// since BUG-759's financeHook.ApplyEffect call site landed, but
	// nothing on the wire ever surfaced either figure — a player-facing
	// city could sit at IsInsolvent()==true forever with no way to see it
	// (engine.spiral.EvaluateInsolvency, the only Go consumer, is itself
	// not reachable from compose — feat.compositionroot has no registered
	// outbound edge to engine.spiral in code.json, GR#25 — so this publish
	// leg is the ONLY production surface for the signal right now).
	// Same live-read + Valid()-nil discipline as CreditRating immediately
	// above: both fields are read from the SAME st.finance.Valid() check
	// (one guard, two accessors — InsolvencyMonths/IsInsolvent are cheap
	// atomic-mutex reads, not worth a second Valid() call), nil ONLY on
	// the SEC-020 copy-guard violation, unreachable in production.
	InsolvencyMonths *int  `json:"insolvencyMonths,omitempty"`
	Insolvent        *bool `json:"insolvent,omitempty"`

	// InsolvencyVerdict is BUG-769's second increment: engine.spiral's
	// DecayAPI.EvaluateInsolvency verdict (spiral/death.go — DeathVerdict.
	// String(): "insolvency" | "none"; "ghost-city" is structurally
	// unreachable via this call site, since GhostCityTrigger is never
	// called here). BUG-769 round REJECT (opus-round-bug769, F1): this
	// used to be read from an atomic mirror written only at a month
	// boundary, which went stale across a Save->starve->Load sequence
	// (finance's own participant reset zeroes insolvencyMonths/gameOver
	// on Load; nothing reset the mirror) — DELETED per the round's own
	// recommendation. EvaluateInsolvency is now called LIVE, every
	// publish tick, inside buildFinanceBalanceSheetPatch, on the exact
	// same finance handle InsolvencyStatus() reads for Insolvent/
	// InsolvencyMonths above — so all three fields on this patch can never
	// disagree with each other the way the mirror could. This is spiral's
	// OWN read of finance's signal, distinct in PROVENANCE from (but now
	// always in sync with) the plain Insolvent bool above, which
	// finance_publish.go derives directly from FinanceAPI.IsInsolvent()
	// without going through spiral at all. Honest disclosure (unchanged by
	// this round): EvaluateInsolvency is a pure reader with NO enforcement
	// of its own — there is no game-over/halt state anywhere in the
	// composed engine yet for a DeathInsolvency verdict to trigger;
	// building that halt is a separate policy item. Also unchanged (round
	// finding F3, honest disclosure): FinanceAPI.gameOver LATCHES once set
	// (RecordMonthResult never clears it) while InsolvencyMonths resets to
	// 0 on the next met month — so a reachable, real production state is
	// InsolvencyMonths=0 with Insolvent=true and InsolvencyVerdict=
	// "insolvency" simultaneously (3 starved months then 3 funded months,
	// never a save/load). There is NO engine-level "recovery" from
	// insolvency once latched; the ONLY way this triple clears to
	// (0,false,"none") is a Load/New Game that resets FinanceAPI's
	// underlying state (see finance's own resetForLoad participant). nil
	// ONLY on the same st.finance.Valid() copy-guard path as CreditRating/
	// Insolvent above (unreachable in production).
	InsolvencyVerdict *string `json:"insolvencyVerdict,omitempty"`
}

// financePayrollShortfallView mirrors internal/ui/screens/finance/wire.go's
// wirePayrollShortfallView field-for-field (BUG-723 round finding F3: this
// comment used to claim that mirror type existed before it actually did —
// it is now added there too, see that file). AmountMicropounds
// is the most recently recorded shortfall for Month (0 means the most
// recent recorded month posted its full private wage bill); Months is
// the current consecutive-shortfall streak (FinanceAPI.
// PayrollShortfallMonths) — 0 exactly when AmountMicropounds is 0, so a
// subscriber can tell "just cleared" (Months drops to 0) from "still
// ongoing" (Months keeps climbing) without re-deriving a streak from a
// single snapshot amount.
type financePayrollShortfallView struct {
	Month             int64 `json:"month"`
	AmountMicropounds int64 `json:"amountMicropounds"`
	Months            int   `json:"months"`
}

// buildFinanceBalanceSheetPatch returns the "f2.finance" balanceSheet-only
// patch (the design's §6 fast-follow, mirroring buildServicesCapacityDemandPatch's
// shape exactly).
//
// The city balance sheet this slice publishes is deliberately narrow:
// Assets = {Treasury, Reserves} (the city's own RoleMoney accounts),
// Liabilities = {Outstanding Debt} (FinanceAPI.OutstandingDebt, the
// maintained loan-principal running total), NetWorth = Assets - Liabilities.
// Households/Firms cash accounts (AcctHouseholds/AcctFirms) are deliberately
// EXCLUDED — they are the city's citizens'/firms' own money, not the city's
// assets; a city balance sheet that included them would overstate net worth
// by counting money the city does not own.
//
// Treasury is sourced from simState's publish-only mirror (treasuryPub, see
// setTreasury / BUG-324), not from engine.finance's AcctTreasury ledger
// account. BUG-333 r2 honest claim: this is consistency-with-chrome hygiene,
// NOT a race fix — the previous ledger read was already lock-safe
// (AccountBalance takes f.mu internally), and BUG-333's filed zeros symptom
// was actually fixed by BUG-355, which keeps the mirror synced to the ledger
// at every phase boundary (syncMoneyFromLedger). What this read buys is that
// F2's balance sheet renders the SAME single published source as
// chrome.topbar, so the two can never disagree about the player's money —
// even if a future change lets mirror and ledger diverge between phase
// boundaries.
//
// Reserves are sourced from finance.AcctReserves; baseline one does not use
// reserves, so this is currently always zero. Per the r1 addendum's corrected
// contract (subscribe.go's ViewPatchFunc doc comment): this function runs on
// the subscription pump goroutine, concurrently with tick-phase writes to
// simState — safe because Treasury reads the lock-free atomic mirror and
// Reserves/OutstandingDebt go through FinanceAPI's own accessor methods
// (each takes f.mu internally).
func (st *simState) buildFinanceBalanceSheetPatch() (json.RawMessage, error) {
	// Treasury comes from the published mirror — BUG-324's single published
	// source, the same read chrome.topbar performs (BUG-333: consistency
	// hygiene, not a race fix; the old ledger read was equally lock-safe).
	treasury := st.publishedTreasury()
	reserves, ok := st.finance.AccountBalance(finance.AcctReserves)
	if !ok {
		return nil, moduleFailed(st.cid, "finance", fmt.Sprintf("AccountBalance(%s) not found", finance.AcctReserves))
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

	// BUG-737 (FEAT-143 AC-7): the infinite/unlimited indicator, read
	// live from the composed *gameinit.GameInit every publish tick
	// (never cached — a session's mode never changes post-Wire, AC-3, but
	// this stays a live read rather than a Wire-time snapshot so it can
	// never silently drift from finance's own mode gate). st.gameInit is
	// never nil after a successful Wire (wireGameInit, compose_gameinit.go)
	// -- it CAN be nil in a test that hand-constructs a bare &simState{}
	// without going through Wire (e.g. bug308_test.go), so this guards
	// against that the same way DeathServicesRunStatus guards
	// st.deathServices == nil: omit the field (nil pointer, omitempty)
	// rather than publish a wrong true/false value or panic. The SEC-020
	// copy-guard's impossible-in-production error path takes the same
	// omit-the-field branch.
	var unlimitedMoney *bool
	if st.gameInit != nil {
		if unlimited, uerr := st.gameInit.Unlimited(st.cid); uerr == nil {
			unlimitedMoney = &unlimited
		}
	}

	// BUG-723: read FinanceAPI's monitorable payroll-shortfall surface
	// (BUG-548's RecordPayrollShortfall) every publish tick, live, exactly
	// like UnlimitedMoney above — never cached, so a shortfall recorded or
	// cleared this month is reflected on the very next publish.
	//
	// Round finding F5: PayrollShortfallStatus() reads all three fields
	// under ONE RLock acquisition — this publish path runs concurrently
	// with tick-phase writes (RecordPayrollShortfall taking the write
	// lock), so two SEPARATE calls (PayrollShortfall() then
	// PayrollShortfallMonths()) could observe a torn snapshot if a clear
	// landed exactly between them. See PayrollShortfallStatus's doc
	// comment (insolvency.go) for the measured torn-read rate under
	// concurrent load before this fix.
	shortfallMonth, shortfallAmount, shortfallMonths := st.finance.PayrollShortfallStatus()
	payrollShortfall := &financePayrollShortfallView{
		Month:             shortfallMonth,
		AmountMicropounds: int64(shortfallAmount),
		Months:            shortfallMonths,
	}

	// BUG-759 round REJECT (opus-round-bug759): CreditRatingNow() returns
	// creditScoreMin (0) on a SEC-020 copy-guard violation, the same
	// value a genuinely bankrupt city would report — publishing that
	// unconditionally would read as "worst possible rating" instead of
	// "unavailable". st.finance.Valid() distinguishes the two BEFORE the
	// read; only sent (non-nil, via the omitempty tag) when the handle
	// really is live. st.finance is itself never nil after a successful
	// Wire (unlike st.gameInit above, which a hand-constructed test
	// simState can leave nil) — the copy-guard is the only degraded path
	// here.
	var creditRating *int
	var insolvencyMonths *int
	var insolvent *bool
	var insolvencyVerdict *string
	if st.finance.Valid() {
		rating := int(st.finance.CreditRatingNow())
		creditRating = &rating

		// BUG-769 round fix (opus-round-bug769, F1/F4): InsolvencyStatus()
		// reads Months and IsInsolvent under ONE RLock (insolvency.go),
		// replacing two separate calls (InsolvencyMonths() then
		// IsInsolvent()) that could observe a torn snapshot if
		// RecordMonthResult's write lock landed exactly between them —
		// same torn-read class PayrollShortfallStatus already guards
		// against a few lines above. Both live every publish tick, never
		// cached.
		months, isInsolvent := st.finance.InsolvencyStatus()
		insolvencyMonths = &months
		insolvent = &isInsolvent

		// BUG-769 round fix (F1): the atomic verdict mirror this call site
		// used to read was written ONLY at a month boundary while
		// Insolvent/InsolvencyMonths above are read LIVE — so a
		// Save(solvent)->starve->Load sequence could publish a stale
		// "insolvency" verdict alongside insolvent=false/months=0 on the
		// SAME patch (finance's own resetForLoad participant zeroes
		// insolvencyMonths/gameOver on Load; nothing reset the mirror).
		// Fixed per the round's own recommendation: the mirror is DELETED
		// and EvaluateInsolvency is called LIVE, right here, on the exact
		// same st.finance handle InsolvencyStatus() just read — it is a
		// pure reader (spiral/death.go calls FinanceAPI.IsInsolvent(),
		// keeps no insolvency-specific state of its own), so this call can
		// never itself introduce a NEW torn read or staleness window; it
		// is exactly as fresh as isInsolvent above on every publish.
		//
		// st.spiral is never nil after a successful Wire, but several
		// pre-existing tests hand-construct a bare &simState{cid, finance}
		// (bug308_test.go, bug333_test.go) that bypasses Wire entirely and
		// leaves it nil — guarded exactly like st.gameInit's own
		// UnlimitedMoney nil-check above, rather than assuming Wire ran.
		if st.spiral != nil {
			verdict := st.spiral.EvaluateInsolvency(st.finance)
			verdictStr := verdict.String()
			insolvencyVerdict = &verdictStr
		}
	}

	patch := financeBalanceSheetWirePatch{
		SchemaVersion:     financeWireSchemaVersion,
		UnlimitedMoney:    unlimitedMoney,
		PayrollShortfall:  payrollShortfall,
		CreditRating:      creditRating,
		InsolvencyMonths:  insolvencyMonths,
		Insolvent:         insolvent,
		InsolvencyVerdict: insolvencyVerdict,
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
