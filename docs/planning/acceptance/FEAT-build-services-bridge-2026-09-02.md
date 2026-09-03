BOW code: (none yet — requires Architect edge registration before feature-keying)

# Acceptance criteria — Build-Services Bridge (GR#25 edge registration required)

**Feature:** Bridge between `engine.build` (completed buildings) and `engine.services` (capacity/coverage)

**Spec refs:** §7 (Zoning & construction), §10 (Service capacity/coverage framework), §54 (fiscal consequences scaling with service capacity changes); code.json module edges (see GR#25 finding below)

**Date drafted:** 2026-09-02

**Status:** BLOCKED pending Architect edge registration (GR#25; see Finding section below)

## GR#25 Finding: Code.json Graph Check

**Critical:** Before any speculative prose or code is written, the graph edges must be verified against the runtime reality.

### Current state (verified 2026-09-02):

**`engine.build` outbound edges (code.json):**
- Calls to: `engine.world`, `engine.finance`, `engine.logistics`, `engine.market`, `foundation.data`, `engine.season`, `foundation.errors`, `foundation.num`, `engine.staffing`, **`engine.firms`**, `int.serializer`
- **Does NOT call `engine.services`**

**`engine.services` inbound consumers (code.json):**
- Consumed by: `engine.capexport`, `engine.census`, `engine.coastal`, `engine.comms`, `engine.crime`, `engine.deathservices`, `engine.dispatch`, `engine.education`, `engine.fiscal`, `engine.prison`, `engine.refuse`, `engine.social`, `engine.staffing`, `engine.wellbeing`, `feat.compositionroot`, `ui.screen.services`
- **Does NOT list `engine.build` as a consumer**

**Participant.go header declarations (verified in code):**
- `internal/engine/build/participant.go` (lines 1–60): Declares the serialization participant edge `engine.build → int.serializer`. Makes no mention of `engine.services`.
- No `internal/engine/services/participant.go` exists; services does not yet participate in the serialization pattern.

### Verdict:

**A NEW edge `engine.build → engine.services` is REQUIRED and is NOT YET REGISTERED.**

The bridge cannot be implemented without it. The current code shows:
- `engine.build/build.go:622–631`: Building completion marks order complete, lands zone/structure, calls `world.SetStructure()` — **but never calls `engine.services` to register the service** (this is the gap).
- `engine.services/api.go:193–213`: `RegisterService()` method exists and is ready to accept registration, but no producer calls it on behalf of completed buildings.
- **Conclusion:** The missing edge is a code-boundary violation that must be registered in `code.json` and regenerated before any bridge implementation proceeds.

## The Gap (for context)

When `engine.build` completes a building of a service type (fire station, clinic, school, etc.), the completed structure lands on the map and registers with `engine.world` (via `SetStructure`). However, the structure never registers with `engine.services`, so:

- Service capacity does not increase despite the building being complete
- Coverage calculations in `engine.services.CoverageSummary()` do not reflect the new capacity
- The service→wellbeing→migration chain (AC-19 in `engine.services.md`) sees no response to player building
- Coverage appears static even though the player invested in service infrastructure

### Current proof of the gap:

**File:** `internal/engine/build/build.go:622–631`

```go
if order.materialsRemaining == 0 && order.labourRemaining == 0 && order.leadTimeRemaining == 0 {
	order.complete = true
	key := cellKey{tile: order.tile, local: order.local}
	b.zoneState[key] = order.zone
	b.structures[key] = order.id
	// Sync the structure reference to world.structureRef so the viewport publishes it
	if err := b.world.SetStructure(order.tile, order.local, uint32(order.id), b.correlationID); err != nil {
		return err
	}
	// ← NO CALL TO engine.services.RegisterService() HERE
}
```

Compare to `engine.services` API which expects:

**File:** `internal/engine/services/api.go:193–213`

```go
func (a *ServicesAPI) RegisterService(spec ServiceSpec) error {
	// ... validation ...
	a.instances[spec.ID] = &serviceInstance{spec: spec}
	return nil
}
```

And the spec is built from building data:

**File:** `internal/engine/services/service.go:70–84`

```go
func ServiceSpecFromBuilding(id ServiceID, kind ServiceKind, entry data.BuildingEntry) ServiceSpec {
	// Derives spec.CapacityRaw, spec.Milestone from the catalogue entry
	// Leaves coverage/location/staffing for the caller to supply
	return ServiceSpec{
		ID:          id,
		Kind:        kind,
		CapacityRaw: entry.CapacityRaw,
		Milestone:   step.Milestone,
		UpgradePath: []UpgradeStep{step},
	}
}
```

## Acceptance Criteria (blocked — see BUG-XXXX dependency)

**AC-1 (GR#25 prerequisite).** The Architect has registered a new outbound edge in `code.json` from `engine.build` to `engine.services`, run `tools/plan/generate.js` to regenerate `code.json`, and verified the edge now appears in both the build module's outbound list and the services module's inbound consumer list. Once registered, this feature unblocks and a new `[mkey]` is issued (e.g. `FEAT-build-services-reg` or assigned from the sequence). **Check:** `grep -A20 '"engine.build"' code.json | grep -q 'engine.services'` finds the new call.

---

### Functional ACs (pending AC-1)

- **AC-2.** When `engine.build` completes a building (on tick when materials/labour/lead-time all reach zero), and the building's catalogue entry declares a service kind (e.g. a clinic declares kind=`Healthcare`, a fire station declares kind=`Fire`), `engine.build` calls `engine.services.RegisterService()` with a spec derived from:
  - The building's world location (tile/cell coordinates converted to X/Y in service-grid terms),
  - The building's catalogue entry (via `ServiceSpecFromBuilding`),
  - The building's service-specific coverage radius and staffing need (pushed by the composition root or derived from the catalogue's unlock tier).
  - **Check:** `internal/engine/build/build.go:622–631` now includes a call to `b.services.RegisterService(...)` (or equivalent injected dependency) on successful completion. The call happens once per building, after `world.SetStructure()` succeeds.

- **AC-3.** Coverage recomputes correctly after registration. A tick after `engine.build` registers a service, `engine.services.CoverageSummary()` reflects the new capacity in `TotalCapacity` (increased by the building's capacity) and `CoverageRatio` (recomputed as `Σcapacity/Σdemand`). **Check:** An integration test builds a clinic (capacity 150 visits/day) with no other services, pushes demand 200 visits/day, snapshots the initial coverage ratio (200/0 ≈ undefined or clamped, depending on the empty-case rule), builds the clinic, asserts the new ratio ≈ 150/200 = 0.75.

- **AC-4 (GR#21 determinism).** Building registration is deterministic: same seed + same build order + same demand = identical service registrations and coverage ratios across reruns, regardless of worker count. No map-range nondeterminism in the registration or coverage aggregation path. **Check:** A determinism test builds a fixed set of buildings in a deterministic order, takes a `CoverageSummary()` snapshot, runs the same build sequence again (different game instance, identical seed/journal), asserts the snapshots are byte-identical.

- **AC-5.** Conservation: the total capacity registered across all services equals the sum of every completed service-type building's catalogue capacity. Demolished buildings (not yet in scope for this AC, but noted for AC-X) reduce capacity; registering a building increases capacity; no phantom gains or losses. **Check:** A test completes N buildings, queries `ServiceIDs()` and sums their `Capacity()` values, asserts the total equals the sum of those N buildings' catalogue capacities (read from the buildings.json fixture used to create the queue).

- **AC-6.** The service→wellbeing→migration chain now responds to player building. A simplified end-to-end test: complete a healthcare building (capacity 100), observe via mock `engine.wellbeing` that `wellbeing.ServiceCoverageMet` (or the coverage field it consumes) now reflects the new coverage, which in turn affects citizen migration attraction scores (mocked via `engine.attract`). No production wiring required for this AC (the composition root's future work), but the test harness must wire the three modules together to prove the signal propagates. **Check:** `grep -rn "func Test.*[Ee]ndToEnd\|func Test.*[Ss]erviceChain" internal/engine/build/*_test.go` finds an integration test that (a) completes a service-type building, (b) reads `engine.services.CoverageSummary()`, (c) confirms it reflects the new capacity.

### Error handling ACs

- **AC-7 (GR#7).** A building whose catalogue entry lacks a declared service kind (or declares an unknown/unregistered service kind) completes normally but does NOT attempt to register with `engine.services`. The completion succeeds; the building lands on the map and exists in the world. A warning or info-level log may be emitted (optional), but no error blocks the build completion. **Check:** A test completes a residential zone (kind = "" or kind = "Residential", not a service kind), asserts the build completes and the zone lands on the map, asserts `engine.services.ServiceIDs()` includes no new service instance.

- **AC-8 (GR#7).** Registering a building as a service fails with a registry-sourced error (new `MET-E` code, e.g. `MET-E8501`) if (a) `engine.services` has not been initialized/injected into `engine.build`, (b) the building's service kind is not registered in `engine.services`, or (c) the building's location or coverage radius is non-finite (NaN/±Inf). The build order remains incomplete and does NOT land the zone/structure on the map; the error propagates to the caller. **Check:** `grep -n "MET-E" internal/engine/build/errors.go` finds a new registry code; a test asserts building a fire station when the `Fire` service kind is unregistered returns that code and the zone does not land.

### Scope notes

- **Demolition/offline service deregistration:** Currently out of scope. This AC specifies registration on build completion. A future feature (or amendment to this one) will specify how demolished buildings deregister with services, lowering capacity. For now, completed buildings remain registered forever.
- **Multi-building service instances:** This AC assumes one building = one service instance. A future enhancement could allow upgrading (e.g. clinic→hospital), which the services API already supports via `Upgrade()`, but that wiring is deferred.
- **Composition root wiring:** This AC specifies the `engine.build → engine.services` call path in the build module; the composition root's job to inject the services dependency is separate follow-up work (already sketched in FEAT-082 composition root).

## Open Questions for Aaron

1. **Service kind mapping:** How should `engine.build` determine whether a completed building declares a service? The catalogue (data/buildings.json) must have a field that signals "this is a Fire Station service". Does the buildings.json entry have a `serviceKind` field, or should it be derived from the zone type (e.g. zone=`Fire` → kind=`Fire`)? Recommendation: add a `serviceKind: ""` field to data.BuildingEntry (empty = not a service, "Fire" = Fire kind). This keeps the coupling explicit and centralized.

2. **Location mapping:** `engine.build` stores cell locations as (TileCoord, CellLocal) world coordinates. `engine.services.ServiceSpec` expects (X, Y) floats for `CoverageSummary` aggregation. Should the conversion be:
   - (a) `X = tile.X + local.Col / 16.0`, `Y = tile.Y + local.Row / 16.0` (treating cells as a 16×16 grid within each tile), or
   - (b) Some other grid convention?
   Recommendation: Verify with the world module authors what the canonical (X, Y) → (TileCoord, CellLocal) mapping is, then apply its inverse here.

3. **Coverage radius source:** `ServiceSpec` requires `CoverageRadius` (spatial reach). Where should it come from?
   - (a) Hard-code per service kind (e.g. Fire stations always 500m radius), or
   - (b) Load from data/buildings.json per building, or
   - (c) Load from a services-specific config (e.g. data/services.json)?
   Recommendation: Option (b) or (c); avoid hardcoding. Suggest adding a `coverageRadius: 0.0` field to BuildingEntry (or a separate coverage.json lookup table by serviceKind). The catalogue is the SSOT per AC-10 of engine.services.md.

4. **Staffing need source:** Similar to coverage: where does `spec.StaffingNeed` come from?
   - (a) Hard-code or derive from capacity (e.g. 1 staff per 50 visits), or
   - (b) Load from catalogue?
   Recommendation: Option (b); add a `staffingNeed: 0.0` field to BuildingEntry so AC-10 (capacity sourced from catalogue) extends to staffing as well.

5. **Registration timing:** Should services be registered on the tick they complete (same tick as `world.SetStructure()`), or on the following tick? Recommendation: Same tick (AC-2 as written); this keeps the completion atomic and avoids a one-tick lag where the building exists on the map but has no capacity.

6. **Demolished buildings (out of scope, but noting for future):** Once demolition is implemented, should deregistering a service (e.g. when a fire station is demolished) happen synchronously in `engine.build`, or should the demolition event be observed by services separately? Recommendation: Symmetric to registration — if build registers on completion, it should deregister on demolition. Future AC can specify an `UnregisterService(id ServiceID)` call in the demolition path.

## Escalations

- **Blocking:** This feature requires GR#25 Architect edge registration before implementation. An `EDGE-build-to-services` (or similar naming) item should be created and tracked until code.json is regenerated. Once regenerated, a new feature BOW item can be created with a proper `[mkey]` tag.

- **Data schema:** The decisions on questions #1–4 above require updates to data/buildings.json schema. Coordinate with the data module owners to confirm BuildingEntry can carry `serviceKind`, `coverageRadius`, and `staffingNeed` fields (or confirm a separate lookup is preferred). If schema changes are needed, they must land before this feature's junior developer starts implementation.


---

## ADDENDUM (2026-09-02, post-round amendments)

**AC-8 amended (error codes):** the AC's literal ask for a NEW MET-E code in build/errors.go is
superseded. The build correctly REUSES registry-sourced codes (MET-G508 ErrDependencyMissing for a
missing dependency; engine.services' own MET-G1201/G1207/G1209 propagated directly for unknown-kind /
duplicate / non-finite) — GR#3-sound, no duplicate codes for the same failure class. The AC's check is
"the failure surfaces a registry-sourced code", not "a code minted in build's file".

**AC added (durability, from the round's REJECT):** registered service capacity MUST survive the
durable-host restore path (RestoreLatestSnapshotOrGenesis) and a Composition.Load into a live
composition. Registration is idempotent (ErrDuplicateService treated as success, per the
refuse/round.go convention) and re-driven for already-complete service orders after any load. The
round's attack tests (attack_servicesbridge_round_test.go: LosesCapacityAcrossRestore,
RewindLoadIntoLiveServicesDoubleRegisters) are the acceptance bar and are kept as regressions.

**Open point (Row axis):** CellLocal.Row's growth direction is undocumented in engine.world; the
bridge must pin the verified convention with a comment citing the world source and a test, not an
assumption (placement error up to 2km otherwise).
