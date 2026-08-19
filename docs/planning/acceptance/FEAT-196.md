BOW code: FEAT-196

# Acceptance criteria — household shopping cycles + store restocking supply chain (FEAT-196)

**BOW code:** FEAT-196
**Spec refs:** `docs/planning/proposals/retail-shopping-ecosystem.md` §1 (core loop — weekly grocery / monthly big shop / ad-hoc trips; physical trip vs online order), §2 (random dispersal — per-household seeded stream, Saturday bias, staggered load), §6 (supply chain — stores consume stock per sale, restocked via `engine.freight` lorry deliveries of foodStaples/consumerGoods; shelf-stock conserved: delivered − sold − waste = Δstock; empty shelves → fresh-food access + appeal); `docs/METROPOLIS-MASTER-v2.1.md` §37 (Shopping & Grocery Access); code.json `engine.shopping` (MOD-050) entry (outbound calls `engine.traffic`, `engine.market`, `engine.wellbeing` ONLY — **no edge to `engine.freight`**).
**Date:** 2026-08-18 (BA, Programme B)
**Status:** draft-ahead — post-Baseline-One future-stage item; `MOD-050` is `in_progress` (rework). Not in the ready queue.
**Package under test:** `internal/engine/shopping/` — this feature extends the owning module `engine.shopping` (MOD-050), the proposal's named "target end-state".
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/engine/shopping/...`.
**Cross-ref:** sibling files sharing this package and `data/shopping.json` — `engine.shopping.md` (MOD-050), `FEAT-195.md` (archetypes + tier template), `FEAT-197.md` (online delivery fleets).

## User stories

- **US-1.** As a household, I need a weekly grocery shop and a monthly big shop plus ad-hoc other-shop trips, each with its own day/time drawn from a seeded per-household stream, so my demand disperses across the week (with a Saturday bias) instead of every household spiking identically — producing realistic staggered load on roads, buses, and store queues.
- **US-2.** As the transport network (the registered `engine.traffic` edge), I need physical shopping trips to load the network as real trips per the access model (walk/cycle/bus/car), consistent with `engine.shopping.md` AC-1/AC-4's trip-generation contract.
- **US-3.** As a store, I need my shelf-stock to be a conserved quantity — delivered − sold − waste = Δstock — so running out is a real, arithmetic consequence of out-selling my restocking, never a scripted "empty shelf" event.
- **US-4.** As `engine.freight` (the module that should deliver restock), I need store restocking to arrive as freight commodity flows — but code.json registers no `engine.shopping`→`engine.freight` edge, so this feature must not fake that wiring.

## Scope

Per-household shopping-cycle generation (weekly grocery + monthly big shop + ad-hoc trips) with seeded deterministic dispersal; physical trip generation into `TrafficAPI`; the shopping-owned store shelf-stock ledger with the conservation identity (delivered − sold − waste = Δstock); the empty-shelf consequence reaching fresh-food access (shopping's own accessor) and store appeal/diet via the registered `WellbeingAPI` edge. The restock delivery itself (freight lorry movements) is blocked pending the `engine.shopping`→`engine.freight` edge (AC-5, Escalations).

## Acceptance criteria

### Functional — shopping cycles and dispersal

- **AC-1 (§1 cycle identity — three distinct trip classes, not one stream).** Each household generates a weekly grocery shop, a monthly big shop, and ad-hoc other-shop trips as distinct trip classes, each with its own cadence and destination profile. Check: `go doc ./internal/engine/shopping` shows distinct accessors/types for weekly-grocery vs monthly-big vs ad-hoc trips (or one accessor with a cycle-class parameter); a passing test runs a household through a synthetic month and asserts all three classes were generated, that the weekly count (≈4) differs from the monthly count (1) and the ad-hoc count is data-driven, not a fixed constant (`grep -rn "func Test.*[Cc]ycleClass\\|func Test.*[Ww]eeklyMonthly" internal/engine/shopping/*_test.go`). **Lazy violation this rejects:** a single "shopping trips" stream with a flat weekly rate would satisfy "households shop" while collapsing the weekly/monthly distinction the proposal makes load-bearing.

- **AC-2 (§2 random dispersal — per-household seeded stream, GR#21).** Each household's shop day/time draws from a per-household deterministic stream `det.NewStream(worldSeed, householdID, weekIndex, purposeTag)` with the dispersal parameters (spread shape, Saturday-bias peak weighting) loaded from `data/shopping.json` (GR#15 — not Go literals). Check: `grep -rn "det.NewStream" internal/engine/shopping/*.go` (excluding `_test.go`) shows the dispersal draw site; a passing test constructs two households with the same seed and asserts their assigned shop days are NOT all identical (dispersal actually spreads demand), and that the aggregate daily trip-count curve over a synthetic week peaks on the data-file's configured peak day and is NOT a single-day spike (`grep -rn "func Test.*[Dd]ispersal\\|func Test.*[Ss]aturdayBias" internal/engine/shopping/*_test.go`). **False-pass risk:** a build that draws shop times from a shared/global RNG would still "disperse" on a shallow read but break cross-run determinism — the `det.NewStream` grep plus a byte-identical-across-runs determinism assertion (AC-10) is what rules that out.

- **AC-3 (§1/§2 physical trips load the network via the registered `TrafficAPI` edge).** Physical shopping trips (walk/cycle/bus/car per the access model) file into `engine.traffic` through the already-registered `shopping→traffic` edge, reusing `engine.shopping.md` AC-1/AC-4's trip-generation surface — no new edge, no duplicated trip-assignment logic. Check: `grep -rn "traffic\\.\\|TrafficAPI" internal/engine/shopping/*.go` (excluding `_test.go`) shows the call crossing the registered interface; a passing test generates a week of trips and asserts the count filed into the traffic seam equals the number of physical trips generated (none silently dropped) (`grep -rn "func Test.*[Tt]ripFiling\\|func Test.*[Pp]hysicalTrip" internal/engine/shopping/*_test.go`).

### Functional — supply chain: shelf-stock conservation and restock

- **AC-4 (§6 shelf-stock conservation — the invariant-friendly ledger).** Each store's shelf-stock is a conserved quantity: delivered − sold − waste = Δstock, computed as an arithmetic identity over the store's own ledger, with waste a separate, queryable term (not absorbed into a fudged "sold" figure). Check: `go doc ./internal/engine/shopping` shows a per-store stock accessor exposing delivered, sold, waste, and current stock; a passing test runs a synthetic month and asserts the conservation identity holds to the ledger's own precision for every store — including a store that sells MORE than it was delivered (stock goes negative-or-threshold, per AC-6) and one that overstocks and wastes (`grep -rn "func Test.*[Ss]tockConserv\\|func Test.*[Ss]helfStock" internal/engine/shopping/*_test.go`). **Lazy violation this rejects:** a build that sets `stock = max(0, delivered - sold)` with no separate waste term would "conserve" trivially while hiding the waste line the proposal makes a first-class term.

- **AC-5 (GR#25 — restock delivery via `engine.freight`, STILL BLOCKED; BUG-100 tripwire applied).** Store restocking must arrive as `engine.freight` commodity flows (foodStaples/consumerGoods) from distribution/warehouse tiers, with restock intensity archetype-dependent (AC-7). code.json registers NO `engine.shopping`→`engine.freight` outbound edge (only `engine.traffic`/`engine.market`/`engine.wellbeing`) — the proposal's own §7 names this exact gap ("shopping→freight … must be registered before build prose"). **Tripwire (mechanical, BUG-100):** `node -e "const m=require('./code.json').modules.find(x=>x.key==='engine.shopping'); process.exit(m.outbound.calls.some(c=>c.key==='engine.freight')?1:0)"` must exit **0** (edge still absent). A nonzero exit means the edge has landed and this AC is stale — it MUST be rewritten to a real `FreightAPI` call before the next commit touching `internal/engine/shopping/`; Tester/CI treat a nonzero exit as a hard FAIL. Until the tripwire fires: the store stock ledger accepts a "delivered" input through an explicitly-marked injected seam (a `RestockSource` interface), with a data-file placeholder delivery schedule, and the placeholder is marked in code (`grep -n "PLACEHOLDER\\|STUB\\|TODO" internal/engine/shopping/*.go` referencing the restock source). The conservation identity (AC-4) is proven against that injected seam so nothing re-blocks when the real edge lands.

- **AC-6 (§6 empty shelves → fresh-food access + appeal; the emergent consequence).** When a store's shelf-stock falls below its documented threshold, the store's fresh-food access contribution (shopping's own access-score factor, `engine.shopping.md` AC-5) degrades in the same tick, and the diet/health consequence is routed through the registered `WellbeingAPI` edge (`engine.shopping.md` AC-7) — no separate "empty shelf" event type computed by different code, and no shopping-owned health number. Check: a passing test drives a store's stock below threshold and asserts (a) the fresh-food access factor falls through the ordinary access-score path, and (b) the call into `WellbeingAPI` carries the correspondingly-lowered diet-quality value (`grep -rn "func Test.*[Ee]mptyShelf\\|func Test.*[Ss]tockOutAccess" internal/engine/shopping/*_test.go`). **Lazy violation this rejects:** a hardcoded `if store.Empty { access = EmptyConstant }` branch would satisfy "empty shelves lower access" while bypassing the real access-score formula — the check requires the low score to be reached via the stock→freshness factor of AC-4's ledger, not a bespoke flag.

- **AC-7 (GR#15; restock intensity is archetype-dependent and data-driven).** Restock intensity differs by supermarket archetype (per `FEAT-195.md` AC-1's attribute set) and is loaded from `data/shopping.json`, not a Go literal. Check: a passing test loads two archetypes with different restock-intensity values and asserts the same sales volume produces a different delivered-rate for each (`grep -rn "func Test.*[Rr]estockIntensity" internal/engine/shopping/*_test.go`).

### Error handling

- **AC-8 (GR#7).** A stock mutation against an unregistered store ID, or a negative delivered/sold/waste input, returns a registry-sourced error (new `MET-E`-range code) rather than silently creating a store or clamping the ledger to zero. Check: `grep -n "MET-" internal/engine/shopping/*.go` finds a registry code reference; passing test coverage asserts the error code matches AND no placeholder store was created AND no silent clamp occurred (`grep -rn "func Test.*[U]nregisteredStore\\|func Test.*[Nn]egativeStock" internal/engine/shopping/*_test.go`).

### Determinism & safety

- **AC-9 (GR#21; no map-range, no wall clock on the tick path).** `grep -rn "for .* := range" internal/engine/shopping/*.go` shows no map-iteration feeding a stock/dispersal result without a prior `sort.` ordering; `grep -rn "time.Now\\|time.Since" internal/engine/shopping/*.go` (excluding `_test.go`) returns no matches.

- **AC-10 (GR#21; dispersal determinism).** Repeated runs from identical `(worldSeed, command log)` produce byte-identical shop-day assignments and stock ledgers across worker counts. Check: `grep -rn "func Test.*[Dd]eterminis" internal/engine/shopping/*_test.go` finds the test asserting identical output across at least two worker counts.

- **AC-11.** `go test ./internal/engine/shopping/... -race -count=1` passes; `grep -n "go func()" internal/engine/shopping/*_test.go` finds at least one concurrency test (concurrent sales against the same store's stock ledger).

### Documentation

- **AC-12.** `internal/engine/shopping/doc.go` states the three trip classes (AC-1), the per-household dispersal stream and Saturday bias (AC-2), the shelf-stock conservation identity delivered − sold − waste = Δstock (AC-4), and the `engine.shopping`→`engine.freight` block (AC-5) explicitly. Check: `grep -n "engine.shopping" internal/engine/shopping/doc.go` and `grep -n "conservation\\|Δstock\\|delivered" internal/engine/shopping/doc.go` and `grep -n "BUG-058\\|blocked" internal/engine/shopping/doc.go` all match.

## Out of scope

- `engine.freight`'s own commodity-flow / lorry-movement mechanics — this item only consumes restock deliveries through an injected seam pending the registered edge (AC-5); it does not reimplement freight's tonne accounting.
- `engine.traffic`'s own trip assignment/routing — this item only generates trips into it (AC-3).
- `engine.wellbeing`'s own diet/health driver — this item only supplies the diet-quality input via the registered interface (AC-6).
- The delivery-van side of online shopping — that is `FEAT-197.md`'s scope.
- The specific dispersal-shape parameters, Saturday-bias weight, waste rate, and restock-intensity magnitudes — balance data in `data/shopping.json`, pending M2 tuning (GR#15; balance-number regime).

## Escalations

- **For Ben/Bill — GR#25 edge gap (this file's primary contribution, and the proposal's own named gap).** `engine.shopping`→`engine.freight` has no registered edge despite §6's explicit "restocked via engine.freight lorry deliveries" and the proposal §7's own call-out. Until registered, store restocking cannot legally cross the interface; AC-5 keeps the ledger testable against an injected seam so the algorithmic work isn't blocked, but the real call path is deferred. Also confirming, from this feature's side, the shared `engine.shopping`→household/citizen demand-source gap (`FEAT-195.md` AC-5 / `engine.shopping.md` Escalations), since "per-household" cycle identity (AC-1/AC-2) needs SOME registered household reference to key off of.
- **For Aaron (balance numbers).** Dispersal shape, Saturday-bias weight, per-archetype restock intensity, and the waste rate are placeholders — §2/§6 give mechanism and direction but no figures.
