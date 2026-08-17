// Package capexport implements Metropolis' service-capacity export (BOW
// MOD-049; module key `engine.capexport`; GUID
// 35618942-25cf-4398-8809-c96635362d49; spec §36 "Service Capacity Export —
// selling your slack").
//
// §36's one mechanic, many markets: any service with measurable capacity runs
// a surplus book (capacity − internal demand), and the slack is sold to
// off-map neighbours on contracts carrying a £/unit rate, a term, and a
// cancellation penalty. The rule that makes it a game: contracts are
// COMMITMENTS — your own demand grows into sold capacity, and you face a
// penalty-payment vs service-cut choice at the crossing. Slack is never free
// money; it's a short position on your own growth.
//
// # The ten in-scope service lines (AC-5, GR#15)
//
// refuse collection & disposal, incineration, toxic/hazardous waste
// processing, sewage & water treatment, hospital beds, university places,
// crematorium/cemetery, prison places, port transshipment, and fire/ambulance
// mutual aid. Each line's label, unit and placeholder per-unit rate are loaded
// from data/capexport.json (never a Go literal); the Go [ExportableService]
// constants are the stable keys, and a drift test proves the enum and the data
// file agree (weakness pattern #2).
//
// Surplus power — §36's eleventh row ("Surplus power | £/MWh via Sellindge") —
// is OUT OF SCOPE, blocked pending BUG-058: there is no registered
// engine.capexport → engine.consumption edge, and power capacity/demand lives
// in engine.consumption, not engine.services (ASM-309). The catalogue
// deliberately excludes that line rather than faking the data source; the
// BUG-100 tripwire in the acceptance file re-arms it the moment the edge
// lands.
//
// # The surplus book and the crossing (§36, US-1/US-3/AC-2)
//
// [CapExportAPI.SurplusBook] reports capacity − internal demand per line,
// sourced live from engine.services' ServicesAPI through an explicit
// [CapExportAPI.BindServiceLine] binding (GR#20 — never a concrete services
// struct field read directly). [CapExportAPI.Crossing] reports when internal
// demand exceeds capacity − committed (the headroom), i.e. the shortfall.
// [CapExportAPI.RegisterContractCurves] registers the internal-demand and
// internal-headroom curves with engine.projections' ProjectionsAPI so F7
// (ui.screen.proj) renders the crossing years out — the projected demand curve
// compounds forward by a data-sourced placeholder growth rate (ASM-309).
//
// # The choice at the crossing (§36, AC-3)
//
// Exactly two command paths, with distinct measurable consequences:
//
//   - [CapExportAPI.PayCancellationPenalty] — posts the contract's
//     cancellation penalty through FinanceAPI (trade-tagged, a debit to the
//     city) and cancels the contract, leaving citizens' current coverage
//     unchanged.
//   - [CapExportAPI.CutInternalService] — keeps the contract intact and
//     records the cut: citizens' coverage drops by the shortfall (read back
//     via [CapExportAPI.CitizenCoverage] and [CapExportAPI.Cut]), no penalty
//     posted.
//
// # Contracts are commitments, not toggles (§36, US-2/AC-4)
//
// A [Contract] carries a term, a per-unit monthly rate, and a documented
// cancellation-penalty function — penalty = remainingTermMonths × rate ×
// quantity — never a boolean "exporting" flag. Issuing a contract is a
// command ([CapExportAPI.IssueContract]) producing a durable, queryable
// record; cancelling before term-end computes a nonzero penalty, at/after
// term-end exactly zero.
//
// # The trade ledger (AC-7, GR#3)
//
// Every contract-revenue posting ([CapExportAPI.AccrueRevenue]) and
// cancellation-penalty posting ([CapExportAPI.PayCancellationPenalty]) goes
// through FinanceAPI tagged with [CatTradeExport] ("trade.export"), a distinct
// trade/export category — never folded into generic opex/income. A future
// balance-of-trade aggregate (F2/F5, not this item) can sum that tag to get
// "money entering via exports" without re-deriving it, and the two postings
// are genuinely bidirectional: revenue credits the city, the penalty debits
// it.
//
// # Determinism (GR#21)
//
// Nothing in this package reads the wall clock (the simulation month is
// injected via [CapExportAPI.SetMonth]); there is no shared/global RNG — the
// off-map counterparty is modelled deterministically (the data-sourced
// catalogue rate, no stochastic negotiation), so no counter-based hash stream
// is needed. Every enumeration (catalogue order, contract listing, curve
// registration) iterates a fixed, sorted order, never map-iteration order.
//
// # Dependencies (GR#20, contract-first)
//
// engine.services (ServicesAPI — capacity/demand), engine.finance (FinanceAPI
// — ledger postings), engine.projections (ProjectionsAPI — curve
// registration). All three are concrete types wired via
// SetServices/SetFinance/SetProjections, matching engine.fiscal's
// engine.finance/engine.tax seam; an unwired dependency fails closed with
// ErrDependencyMissing. The engine.prison → engine.capexport consumer edge is
// not yet registered; AC-6's per-service committed-capacity accessor
// ([CapExportAPI.Committed]) exists so §43's prison-overcrowding edge can be
// wired the moment it is.
package capexport
