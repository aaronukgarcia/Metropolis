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
package finance
