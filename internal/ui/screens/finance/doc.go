// Package finance is F2, the Finance screen (FEAT-014).
//
// Spec refs:
//   - §13-F2: Finance screen (GDD line 249)
//   - §54: The Fiscal Circuit (Sankey, public payroll clawback, GDD lines 680-688)
//   - §39: Taxation (tax-instrument sliders, GDD lines 558-565)
//
// View subscription:
//   - "f2.finance": the view delivering P&L aggregates, balance-sheet lists, active loans,
//     tax-instrument settings/elasticity curves, gross-vs-net public payroll, and Fiscal Circuit
//     Sankey flow bands.
//
// Gating Notes & Architecture Seams (BUG-058 / ASM-1482):
//   - Delta path — UNBLOCKED (FEAT-208 increment 2, 2026-08-20): ASM-1482's
//     core routing seam (internal/ui/router) now exists and is wired into
//     the real composition root (cmd/metropolis/boot.go's bootCore): a
//     real *Screen is constructed, primed against a live Subscribe to
//     "f2.finance", and bound into router.Router via BindSubscription, so
//     ApplyDelta now receives real protocol.Delta traffic end to end
//     (transport -> engine.core.Publish -> router -> this screen), not
//     just in this package's own unit tests. Only the balanceSheet
//     sub-view is published server-side so far (compose/finance_publish.go)
//     — PL/loans/creditRating/taxSliders/publicPayroll/sankey remain
//     server-side fast-follows (every field is already `omitempty`, so no
//     schema change is needed when they land); ApplyDelta itself already
//     handles all of them.
//   - FIN-8 command rejection surfacing (ApplyResult) — STILL GATED, but
//     the reason changed: router.RegisterResultHandler is a real, wired
//     surface now (unlike before increment 2), but nothing in this
//     codebase's input-handling layer calls BorrowLoan/RepayLoan/
//     SetTaxRate yet (verified at this dispatch:
//     `grep -rln "\.BorrowLoan\|\.SetTaxRate\|\.RepayLoan" internal cmd`
//     outside this package's own screen.go/tests returns zero matches) —
//     there is no command-issuing call site to register a CorrelationID
//     from. ApplyResult stays fully designed/implemented/verified in unit
//     tests, unreachable in the real binary specifically for lack of an
//     input-layer caller, not for lack of a routing seam. Do not invent
//     custom/ad-hoc wiring to close this gap; it is F2's own input-wiring
//     scope, not a router or composition-root gap.
package finance
