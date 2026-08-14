// Package logistics is the JIT-and-delivery module (MOD-025): the
// per-district/per-commodity movement of goods bounded by a
// capacity-and-shortfall model, reduced to its STUB-FOR-BASELINE depth
// (FEAT-083, head-dev direction 2026-08-14) — just enough for
// engine.market's live availability resolution and engine.build's
// materials flow to run the Baseline One loop, NOT the full JIT queue.
//
// Module key: engine.logistics (see code.json)
// GUID:        c59ecb6e-443f-4012-a0f0-b031de444335
// Spec refs:   §8 (Logistics & Just-in-Time — the four-step daily-tick
//
//	resolution: local stock draw with capacity/holding-cost/
//	shelf-life; replenishment orders against forecast + a
//	player-tunable lean/fat safety buffer; orders become truck
//	movements on finite per-junction slots; deliveries with
//	mode-dependent lead time; perishables expire in queue;
//	shortfalls hit consumption/satisfaction/health/production/
//	construction); §II.5 (Movement — freight shares the road
//	network, trucks are PCUs with junction slot demand,
//	commuters and JIT compete for road space); §13-F5 (Trade &
//	Logistics — import contracts, junction queue live view,
//	per-commodity warehouse buffer policy).
//
// # Stub-for-baseline depth (FEAT-083 — read this before replacing it)
//
// This package is a COARSE approximation, deliberately NOT the full §8
// JIT model. What it does implement (the Baseline One subset):
//
//   - [LogisticsAPI.Stock]/[Provision]/[Draw]/[Restock] — a capacity-
//     bounded local stock shelf with a per-unit holding cost and a
//     per-commodity shelf life, where a draw exceeding the shelf returns
//     a partial fill plus a nonzero shortfall (AC-2, AC-9).
//   - [LogisticsAPI.Deliverable] — the single deterministic "how much of
//     commodity X can actually be delivered to a district this tick"
//     number: min(requested, floor(min(localThroughput, marketCeiling) *
//     shortfallFactor)), with shortfall = requested - delivered. One
//     throughput number per district/commodity with a data-driven
//     shortfall factor stands in for the whole queue/congestion model.
//   - [LogisticsAPI.OrderSize]/[SetBufferPolicy] — a coarse lean/fat
//     safety-buffer sizing of replenishment orders (AC-3).
//   - [LogisticsAPI.SubscribeShortfalls] — a typed, subscribed shortfall
//     event carrying commodity/magnitude/consumer-class (AC-9/AC-10), so
//     downstream modules (satisfaction, production, construction-stall)
//     have a real code path from day one.
//
// What is DEFERRED to after Baseline One runs (per the stub ruling —
// do not treat their absence as a bug in this package):
//
//   - Junction slots and the truck movement scheduler (AC-4), the live
//     per-junction queue state and wait times (AC-5), lead time as a
//     function of transport mode with the 10x sea-bulk ratio (AC-6), and
//     perishable expiry while queued (AC-7) — the "full JIT queue
//     mechanics, truck fleets, and lead-time depth" the ruling names.
//   - Per-district throughput variance: [LogisticsAPI.Deliverable] accepts
//     a district for API uniformity, but at this depth throughput is
//     commodity-global (from data/logistics.json), not per-district.
//   - Shelf-life decay: [Stock].ShelfLife is carried (AC-2) and loaded
//     from data, but no expiry aging runs yet (shelf or queue).
//
// # Capacity-ledger boundary (ASM-191/ASM-232)
//
// This package owns the LIVE per-junction slot ledger and per-commodity
// order book (at full depth) and queries engine.market for price/ceiling
// data as an ordering input ONLY, through [market.MarketAPI]'s registered
// methods — never a write into market-owned state (AC-12). engine.market's
// [market.MarketAPI.Availability] exposes the STATIC logistics-capacity
// ceiling; this package resolves it LIVE. The two capacity concepts are
// related but distinct: market's is a configured figure, logistics's is
// the tick's actual deliverable.
//
// # Queue rendering is not this package's job (ASM-235)
//
// The literal F5 text-column queue view (colour-coded trucks, wait times)
// belongs to ui.screen.trade (a registered consumer). This package only
// exposes queryable queue/stock state through [LogisticsAPI]; it never
// formats or draws it.
//
// # Loading, data, and errors (GR#7/GR#15)
//
// [Load] reads data/logistics.json via foundation/data.LoadLogisticsFile
// (the same generic Load[T] every other §24 config file uses) and
// data/market.json via engine.market.Load (the registered outbound edge),
// then checks all nine §6 commodities are present. Every balance figure —
// throughput, shortfall factor, shelf life, holding cost, and the
// lean/fat safety-buffer multipliers — lives in data/logistics.json, never
// as a Go literal in this package (GR#15; the values themselves are
// unpinned balance data per ASM-234). Every failure is a registry-sourced
// *errs.E (MET-G4xx, this package's claimed sub-range — see errors.go),
// never a panic or a silent default substitution.
package logistics
