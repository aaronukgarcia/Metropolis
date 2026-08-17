// Package fiscal implements the Metropolis economy's fiscal circuit, the
// municipality-quality view and the welfare ledger (BOW MOD-066; module key
// `engine.fiscal`; GUID 7c79679c-1e54-4415-b176-f646fea3054b; spec §54 "The
// Fiscal Circuit — top-down" and Supp.3 civic).
//
// It is the whole-economy F2 master view: a Sankey-topology query over
// engine.finance's double-entry ledger — money entering (exports, tourism,
// out-commuter wages, FDI, grants) and leaving (imports, leakage, interest),
// with the tax machine decomposed by type (income, sales, corporation, rates
// & council tax, duties/levies) — plus the §54 "civil-service truth" (public
// wages shown gross and net of the income-tax clawback), the municipality as
// a modelled department (planning & administration funding drives permit
// speed, build-cost error rate, layout bonuses and corruption risk), the
// childcare subsidy shown as a net line, and the benefits/social-housing
// postings that stabilise the low-wage workforce.
//
// # One ledger, one truth, many views (GR#3)
//
// This package owns NO independent money total. Every figure it exposes —
// the money-in sum, the money-out sum, the budget balance, every Sankey node
// and edge amount — is recomputed at query time from engine.finance's ledger
// (FinanceAPI.TotalMoneyInCirculation / ImportsTotal / DebtServiceTotal /
// Lines / LinesByCategory) and engine.tax's per-instrument revenue
// (TaxAPI.Instruments). There is no persisted running accumulator here that
// could drift from the ledger engine.invariant actually checks; a caller can
// always re-open any figure to the finance/tax lines that compose it.
//
// # Provisional money-in producer nodes
//
// The §54 money-in producers engine.export, engine.tourism, engine.fdi and
// engine.defence (grants) are later sprints and do not exist yet. Their
// Sankey nodes are present — queryable, named, and part of the fixed
// topology — but carry amount zero and a Provisional flag, never a
// fabricated non-zero figure standing in for unbuilt data (AC-2). The
// drill-through of a Provisional node is an empty line set (there is no
// producer to have posted a line); the money-out nodes imports and interest
// are real and drill-through to engine.finance's CatImports / CatDebtService
// lines, and the budget node to AcctTreasury's lines.
//
// # Determinism & clock
//
// Nothing in this package reads the wall clock (AC-12); the fiscal circuit
// reads finance/tax at query time, which advance on the simulation's monthly
// tick. Every multi-node or multi-category summation iterates a fixed,
// ordered slice — never Go map-iteration order (GR#21, AC-11).
//
// # Dependencies (GR#20, contract-first)
//
// This package consumes engine.finance (concrete *finance.FinanceAPI) and
// engine.tax (concrete *tax.TaxAPI) through their registered inbound
// contracts alone, wired via SetFinance/SetTax — the same concrete-dependency
// seam engine.tax/engine.services use for engine.finance. The registered
// engine.fiscal → engine.services edge is exercised at the composition root,
// which sources the civil-service gross wage bill (summing
// ServicesAPI.GrossWageCost across registered services) and the planning
// funding level; this package holds them as documented state fields rather
// than importing engine.services for a per-service query it has no ServiceID
// enumeration to drive (see ASM on the edge decision).
package fiscal
