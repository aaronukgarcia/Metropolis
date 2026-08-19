BOW code: FEAT-197

# Acceptance criteria — online shopping & delivery van fleets (FEAT-197)

**BOW code:** FEAT-197
**Spec refs:** `docs/planning/proposals/retail-shopping-ecosystem.md` §5 (two streams loading the road network with vans: supermarket delivery — store-owned van fleets, archetype-dependent, slots bounded by vans + road time, failure → missed deliveries → satisfaction hit; general online retail via an Amazon-analogue "fulfilment giant" from fulfilment_centre/parcel_hub/last_mile_depot on last-mile van routes; online share grows with era/tier and undercuts high-street footfall); `docs/METROPOLIS-MASTER-v2.1.md` §35 (Communications, internet & e-commerce — online share a modelled coefficient, "no trip, van instead") and §37 (Shopping & Grocery Access); code.json `engine.shopping` (MOD-050) entry (outbound calls `engine.traffic`, `engine.market`, `engine.wellbeing` ONLY — **no edge to `engine.freight`/`engine.comms`**).
**Date:** 2026-08-18 (BA, Programme B)
**Status:** draft-ahead — post-Baseline-One future-stage item; `MOD-050` is `in_progress` (rework). Not in the ready queue.
**Package under test:** `internal/engine/shopping/` — this feature extends the owning module `engine.shopping` (MOD-050).
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/engine/shopping/...`.
**Cross-ref:** sibling files sharing this package and `data/shopping.json` — `engine.shopping.md` (MOD-050; its AC-3 already specifies the household-side "no trip, van instead" displacement this feature builds on), `FEAT-195.md` (archetypes incl. delivery capability), `FEAT-196.md` (household cycles + restock).

## User stories

- **US-1.** As a household, I need online ordering to displace physical shopping trips — no household-side trip is generated for delivery-served demand (per §35's "no trip, van instead") — so the online share genuinely reduces footfall rather than relabelling it.
- **US-2.** As the road network, I need the delivery vans (supermarket fleet and fulfilment-giant last-mile) to occupy road space like buses (depot → route → road occupancy), so online shopping is a real road load, not free teleportation.
- **US-3.** As a supermarket archetype, I need delivery capability to be archetype-dependent (mainstream/value big fleets; discounters little/none), so "can this store deliver" is a data-driven attribute, not a uniform yes.
- **US-4.** As code.json, I need the online-delivery share input to come from `engine.comms` (the module that owns e-commerce share per §35) — and I currently have no registered edge to it, so this feature must not pretend that wiring exists.

## Scope

The online-share displacement mechanism (online demand removes household physical trips, generating zero household-side delivery trips); the archetype-dependent delivery-capability attribute (GR#15); the delivery-slot bound (vans × road time) with the missed-delivery → satisfaction consequence routed through the registered `WellbeingAPI` edge. The van fleet itself (depot → route → road occupancy), the fulfilment-giant last-mile routes, and the online-share source are blocked pending `engine.shopping`→`engine.freight` (and `engine.shopping`→`engine.comms`) edges (AC-2/AC-4/AC-6, Escalations).

## Acceptance criteria

### Functional — displacement and delivery capability

- **AC-1 (§35/§37 online share displaces physical trips — the household side, testable now).** Raising the online-delivery share reduces the count of physical household-generated shopping trips into `TrafficAPI` proportionally, and generates ZERO household-side "delivery trips" — the delivery van movement is a different module's logistics movement, never a shopping-generated trip. Check: a passing test raises the online share and asserts total household-generated physical trips into the traffic seam fall (not rise, not stay flat with a phantom delivery trip added), reusing `engine.shopping.md` AC-3's own displacement check (`grep -rn "func Test.*[Oo]nlineDisplace\\|func Test.*[Nn]oDeliveryTrip" internal/engine/shopping/*_test.go`). **Lazy violation this rejects:** a build that keeps physical trips constant and appends an equal number of "delivery trips" would satisfy "online shopping exists" while making online share ADD load instead of displacing it — the check requires the physical-trip count to fall.

- **AC-2 (GR#25 — online-share source from `engine.comms`, STILL BLOCKED; BUG-100 tripwire applied).** The online-delivery share input is meant to come from `engine.comms`'s e-commerce-share coefficient (§35), but code.json registers NO `engine.shopping`→`engine.comms` outbound edge (comms has zero registered consumers) — the same gap `engine.shopping.md` AC-3 already flags. **Tripwire (mechanical, BUG-100):** `node -e "const m=require('./code.json').modules.find(x=>x.key==='engine.shopping'); process.exit(m.outbound.calls.some(c=>c.key==='engine.comms')?1:0)"` must exit **0** (edge still absent). A nonzero exit means the edge has landed and this AC is stale — it MUST be re-armed to a real `CommsAPI` call before the next commit touching `internal/engine/shopping/`; Tester/CI treat a nonzero exit as a hard FAIL. Until the tripwire fires: the share input arrives through an explicitly-marked injected seam, backed by a data-file placeholder (per-tier/era share), with the placeholder marked in code (`grep -n "PLACEHOLDER\\|STUB\\|TODO" internal/engine/shopping/*.go` referencing the online-share source).

- **AC-3 (GR#15; archetype-dependent delivery capability).** Delivery capability (fleet size / delivery availability) is an archetype attribute in `data/shopping.json`: Mainstream and Value big-box carry large fleets, Discounter little/none, Convenience little/none. Check: a passing test loads `data/shopping.json` and asserts the Mainstream/Value delivery-capability values exceed the Discounter/Convenience values by a documented margin (`grep -rn "func Test.*[Dd]eliveryCapability" internal/engine/shopping/*_test.go`). A uniform delivery flag across archetypes fails.

- **AC-4 (GR#25 — the delivery van fleet is freight/logistics-owned, NOT shopping-owned; STILL BLOCKED).** The supermarket delivery van fleet (depot → route → road occupancy, modelled "like buses") belongs to the freight/logistics layer, not `engine.shopping` — this feature must not implement its own van-fleet/route/occupancy state machine. code.json registers NO `engine.shopping`→`engine.freight` edge. **Tripwire (mechanical, BUG-100):** `node -e "const m=require('./code.json').modules.find(x=>x.key==='engine.shopping'); process.exit(m.outbound.calls.some(c=>c.key==='engine.freight')?1:0)"` must exit **0** (edge still absent). Check (now): `grep -rn "type.*VanFleet\\|type.*DeliveryRoute\\|depot" internal/engine/shopping/*.go` (excluding `_test.go`) finds no shopping-owned van-fleet/route type — the van load is expressed through the registered `TrafficAPI` seam as a demand-shaped load the owning module will later supply, not as a shopping-internal fleet simulation. Once the `engine.shopping`→`engine.freight` edge lands, this AC re-arms to: the van fleet arrives via `FreightAPI`, and shopping only consumes it to size slots (AC-5).

- **AC-5 (§5 slots bounded by vans × road time; saturation → missed deliveries → satisfaction via the registered edge).** Delivery slot capacity is bounded by van count × road time (a data-driven formula from `data/shopping.json`), not an unbounded queue; when slots are exhausted, the resulting missed deliveries reduce satisfaction through the registered `WellbeingAPI` edge (not a shopping-owned happiness number). Check: a passing test feeds a synthetic van-fleet input (injected seam per AC-4) and asserts (a) slot capacity grows with van count and shrinks with road time, (b) demand exceeding slot capacity produces a nonzero missed-delivery count, and (c) the missed-delivery consequence crosses the `WellbeingAPI` seam with a lowered satisfaction value (`grep -rn "func Test.*[Dd]eliverySlot\\|func Test.*[Mm]issedDelivery" internal/engine/shopping/*_test.go`). **Lazy violation this rejects:** a build that lets slots expand to absorb any demand (never missing a delivery) would satisfy "slots exist" while making the capacity bound meaningless — the saturation/missed-delivery test is what catches it.

- **AC-6 (GR#25 — fulfilment-giant last-mile routes are freight/logistics-owned, NOT shopping-owned; STILL BLOCKED).** General online retail is fulfilled from `fulfilment_centre`/`parcel_hub`/`last_mile_depot` catalogue entries on last-mile van routes — that is the freight/logistics layer's movement machinery, not `engine.shopping`'s. This feature only models the demand-side displacement (AC-1) and the share/era growth driving it, not parcel routing or depot placement. Check: `grep -rn "fulfilment\\|last_mile_depot\\|parcel_hub" internal/engine/shopping/*.go` (excluding `_test.go`) finds no shopping-owned parcel-routing/depot logic — the catalogue references, if present, are data lookups only, not a reimplementation of `engine.logistics`' movement scheduler. The `engine.shopping`→`engine.freight`/`engine.logistics` edges remain unregistered (tripwire per AC-4); this AC lists the pair for registration, not as prose describing a call this package makes today.

### Error handling

- **AC-7 (GR#7).** An online-delivery share input above 100% or below 0% is rejected with a typed error, never silently clamped (mirrors `engine.shopping.md` AC-10). Check: `grep -rn "func Test.*[Oo]utOfRangeShare" internal/engine/shopping/*_test.go`.

- **AC-8 (GR#7).** A delivery-slot computation against an unregistered store/archetype, or a negative van count, returns a registry-sourced error rather than a silently-computed zero slot count. Check: `grep -n "MET-" internal/engine/shopping/*.go` finds a registry code reference; `grep -rn "func Test.*[Uu]nregisteredStore\\|func Test.*[Nn]egativeVan" internal/engine/shopping/*_test.go`.

### Determinism & safety

- **AC-9 (GR#21).** Displacement, slot-bounding, and any slot-assignment draws are deterministic functions of `(worldSeed, tick, prior state, commands)`; any draw uses `det.NewStream` — no shared/global RNG, no `math/rand`, no map-iteration order feeding a result. Check: `grep -n "rand.New\\|math/rand\\"" internal/engine/shopping/*.go` (excluding `_test.go`) finds no shared/global RNG source; `grep -rn "func Test.*[Dd]eterminis" internal/engine/shopping/*_test.go` finds the determinism test.

- **AC-10 (SG-7; GR#21).** `grep -rn "time.Now\\|time.Since" internal/engine/shopping/*.go` (excluding `_test.go`) returns no matches.

- **AC-11.** `go test ./internal/engine/shopping/... -race -count=1` passes; `grep -n "go func()" internal/engine/shopping/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-12.** `internal/engine/shopping/doc.go` states the two online streams (supermarket delivery + fulfilment giant), the displacement rule (AC-1), the van-fleet-ownership boundary (AC-4), and the two GR#25 blocks (`engine.comms` share source, `engine.freight`/`engine.logistics` van fleet) explicitly. Check: `grep -n "engine.shopping" internal/engine/shopping/doc.go` and `grep -n "online\\|delivery\\|van" internal/engine/shopping/doc.go` and `grep -n "BUG-058\\|blocked" internal/engine/shopping/doc.go` all match.

## Out of scope

- `engine.comms`' own e-commerce-share computation — this item only consumes a share value through an injected seam (AC-2).
- `engine.freight`/`engine.logistics`' own van-fleet, last-mile route, and parcel-movement machinery — this item only consumes a fleet input to size slots (AC-4/AC-5/AC-6); it does not build a second van-fleet or parcel-routing path (GR#3).
- `engine.wellbeing`' own satisfaction driver — this item only supplies the missed-delivery consequence through the registered interface (AC-5).
- The town-centre-vitality / `engine.cafe` street-life tension (§5) — a downstream design consequence of AC-1's footfall reduction, expressed via the shared `engine.wellbeing` output; no `engine.shopping`→`engine.cafe` edge is asserted or assumed here.
- Online-share-by-era/tier magnitudes, van-capacity figures, and delivery-failure rates — balance data in `data/shopping.json`, pending M2 tuning (GR#15; balance-number regime).

## Escalations

- **For Ben/Bill — GR#25 edge gaps (this file's primary contribution, and the proposal's own named gap).** (1) `engine.shopping`→`engine.freight` (delivery van fleet, fulfilment-giant last-mile) has no registered edge — the proposal §7 names it explicitly ("shopping→freight … must be registered before build prose"). (2) `engine.shopping`→`engine.comms` (online-delivery share) has no registered edge — comms has zero consumers today. (3) `engine.shopping`→`engine.logistics` is likewise absent if the last-mile parcel routing is to be consumed directly rather than via freight; freight already calls logistics, so `shopping`→`freight` is the primary edge, `shopping`→`logistics` the secondary. All three must be registered before the build prose for this feature is legal under GR#25.
- **For Aaron (balance numbers).** Online-share-by-era/tier, per-archetype delivery-fleet size, van capacity, and delivery-failure rates are placeholders — §5 gives mechanism and direction but no figures.
