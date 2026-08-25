// Package finance implements the Metropolis economy's money spine
// (BOW MOD-022; module key `engine.finance`; GUID
// e8ad34bf-d214-46b0-9351-ae1456b358e4; spec §7 "Economy, Land &
// Finance" and §II.4's Part II "Economy" summary).
//
// It owns the double-entry money ledger, the §7 money-flow chain
// (wages → household spend → tax → budget → opex/imports/debt/
// construction), continuous land pricing, milestone-gated loan
// facilities with credit-rating-driven interest, the insolvency
// game-over condition, a v1 firm P&L stand-in, and investment-programme
// payback modelling. Every monetary value in this package is an int64
// micro-pound: one pound sterling = 1,000,000 micro-pounds, stored as
// int64 — never a float32/float64, so no money computation can lose
// precision (M0-ENG §1.2 point 4, GR#16).
//
// # The conservation hook (US-3)
//
// engine.invariant (MOD-019) owns the conservation invariant itself —
// the registry, the hard-assert/log behaviour, and the tick-phase
// wiring are all that package's responsibility. This package's job is
// narrower: it maintains a running [FinanceAPI.TotalMoneyInCirculation]
// total (so a per-tick check never walks the whole ledger from
// scratch), tracks the per-tick net external money flow, and exposes
// both through [FinanceAPI.MoneyStock] / [FinanceAPI.MoneyStockReading].
// The latter returns an engine.invariant.StockReading (Opening/Closing/
// TrackedDelta) ready to slot into a SnapshotProvider's
// Readings[StockMoney] entry, so engine.invariant's MoneyInvariant has
// real data to check instead of a synthetic stock.
//
// # Money model
//
// Accounts are classified by role: RoleMoney accounts hold real money
// (citizen wealth, firm cash, the city treasury, reserves); RoleExternal
// is the outside-world source/sink (imports, opex paid away, reserve
// interest, loan disbursements); RoleLiability is debt owed. A posted
// transaction's net change to RoleMoney accounts is its money delta: an
// internal transfer (wages, tax, spend) has delta zero, while a loan
// disbursement, reserve-interest accrual, or an external payment has a
// non-zero delta that is accumulated into the per-tick TrackedDelta.
// Double-entry balance (every transaction's total debits equal its total
// credits) is enforced at post time, so money is conserved unless an
// external flow explicitly creates or destroys it — and every such flow
// is tracked.
//
// # Determinism
//
// Nothing in this package reads the wall clock; the simulation month is
// injected via [FinanceAPI.BeginMonth]. No monetary sum ranges over a
// map in map-iteration order — account-key iteration in
// [FinanceAPI.RecomputeMoneyStock] sorts keys first (GR#21, AC-14).
//
// # Borrowing instruments (FEAT-057)
//
// FEAT-057 adds a data-driven borrowing-instrument taxonomy on top of the
// milestone-gated loan facilities: a source split ([LoanSourceIMF]
// lender-of-last-resort vs [LoanSourceGovernment]), a secured/unsecured
// axis ([Security] + [Collateral]) where secured borrowing earns a
// strictly lower rate floor, live-computed revenue-share repayments
// ([RevenueShareTerms]), and PFI-style facility funding ([PFIFacility]:
// deferred capex + recurring [PFIFacility.UnitaryCharge] over
// [PFIFacility.MinimumTermMonths] with an explicit lock-in choice). No new
// instrument bypasses the existing MOD-022 machinery: every instrument's
// obligation is included in [FinanceAPI.MonthlyObligations] (the set
// MOD-022 AC-7 / [FinanceAPI.IsInsolvent]'s "obligations met" signal is
// computed against), and every instrument's outstanding/committed exposure
// is included in the debt/revenue denominator [FinanceAPI.CreditRatingNow]
// feeds to [CreditRating] (MOD-022 AC-5). Every rate range, spread,
// revenue-share percentage bound, and PFI unitary-charge multiplier is a
// placeholder pending Aaron's balance pass, sourced from
// data/borrowing_instruments.json (GR#15) — never a Go literal.
//
// # Known limitation — no settlement path yet (FEAT-057 r1 REJECT disclosure)
//
// Borrowing instruments have NO settlement/repayment path in this increment:
// a [BorrowingInstrument]'s Outstanding is fixed at origination and never
// amortised, [FinanceAPI.MonthlyPayment] charges straight-line principal
// without reducing that balance, and a [PFIFacility]'s committed exposure
// runs down only as [PFIFacility.AdvanceMonth] advances ElapsedMonths
// toward MinimumTermMonths. Outstanding therefore only grows (with each new
// issuance) and the AC-7 credit-rating debt denominator keeps counting the
// full principal for the instrument's whole life. Building the settlement
// path — actually reducing Outstanding as monthly payments are made, and
// retiring PFI commitments as the unitary-charge stream is paid — is
// separate BOW-tracked work; until it lands, this surface is deliberately
// disclosed rather than silently half-built.
package finance
