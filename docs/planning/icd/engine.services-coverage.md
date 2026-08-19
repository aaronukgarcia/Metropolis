# ICD: Services Coverage & Enumeration Aggregate

> **What this is:** an Interface Control Document for the `engine.services` city/district coverage + enumeration aggregate query — the surface that makes FEAT-167's `ServiceCoverage` migration term computable from real service state instead of the flat `baselineOneTermValue = 50.0` placeholder documented in `docs/planning/icd/engine.attract-terms.md` §3. Authored against `docs/planning/icd/TEMPLATE.md` (Integration Engine proposal §2/§5/§7/§8). A dev builds the aggregate against this document, not against a conversation.

---

## 1. Identity

- **GUID:** `ab443390-0a8a-459a-ae5a-4b8a35308751` — `engine.services`' own registered `code.json` module GUID. Standing in as this surface's identity GUID: the aggregate is a set of new query methods ON the already-registered `ServicesAPI` (inbound guid `3e1f24aa-a539-447c-9e63-874fa58f3f9e`), not a new module, so the module's own GUID is the correct identity.
- **Name:** `services.coverage.aggregate`
- **Owning module (mkey):** `engine.services`
- **code.json edge ref(s):**
  - **Already registered (reused, no action needed):** `ui.screen.services → engine.services` (edge guid `96581f8a-77b4-449e-a592-3a7a3ea149e5`, `ServicesAPI` inbound guid `3e1f24aa-a539-447c-9e63-874fa58f3f9e`) — the enumeration output (`ServiceIDs`/`ServiceKinds`) is already consumable by the services screen with no new edge.
  - **NOT YET REGISTERED — pending GR#25 registration before the aggregate may be cited as live input to migration:** `feat.compositionroot → engine.services` (compose's outbound `calls` array has no `engine.services` entry today; `engine.services`'s inbound `consumers` likewise omit `feat.compositionroot`). This edge is required for the composition root to (a) push per-district/per-service demand via `UpdateDistrictDemand`/`UpdateDemand` and (b) read `CoverageSummary`/`CoverageByDistrict` back out to feed the `ServiceCoverage` term. The aggregate itself is fully buildable and testable in isolation before this edge lands — this ICD's Inputs/Outputs describe the query surface, not a live compose call.

---

## 2. Purpose

Today nothing can answer "how well is the city covered by services?" as a single number or per-district breakdown: `ServicesAPI` exposes only per-`ServiceID` accessors (`Capacity`, `Demand`, `Quality`, …) and no enumeration (`ServiceIDs()` does not exist — every call requires a caller-known id), so FEAT-167's `ServiceCoverage` term is stuck at a flat 50.0. This surface adds the missing aggregate: a deterministic enumeration of the registered surface, a city-wide `CoverageSummary` (Σcapacity vs Σdemand), and a per-district `CoverageByDistrict` breakdown over caller-pushed district demand. It is the "coverage/enumeration aggregate" the FEAT-167 discovery named as the prerequisite for wiring `ServiceCoverage` honestly.

---

## 3. Inputs

| Source | Shard-state read | Type |
|---|---|---|
| `engine.services` (own `instances map[ServiceID]*serviceInstance`) | Each instance's `capacityCeiling()` (AC-9 upgrade-step ceiling), `demand`/`demandDist` (pushed via `UpdateDemand`), `funding`, `spec.CoverageRadius`, and `staffingRatio()` — read-only under `mu.RLock` | internal `serviceInstance` state (never mutated by the aggregate) |
| `engine.services` (own `kinds map[ServiceKind]KindDef`) | Registered kind set, for `ServiceKinds()` enumeration | internal `KindDef` |
| caller-pushed district demand | `UpdateDistrictDemand(district DistrictID, service ServiceID, demand, distance float64)` records, the per-district demand the caller (the composition root, eventually) attributes to each district | `DistrictID` (services-owned, mirroring the engine's existing district-push pattern), `float64` |

The aggregate performs **no cross-module read**: capacity is already sourced from `data.catalogue` at `RegisterService` time via `ServiceSpecFromBuilding` (the registered `engine.services → foundation.data` edge), and demand is pushed in. District identity is **supplied by the caller**, never derived from any spatial module — there is no district concept on a registered edge anywhere in the engine, so the aggregate must not invent one.

---

## 4. Outputs

| Effect | Target consumer / edge | Type |
|---|---|---|
| `ServiceIDs() []ServiceID`, `ServiceKinds() []ServiceKind`, `DistrictIDs() []DistrictID` | read by `ui.screen.services` (registered edge) and, once registered, the composition root | sorted slices, deterministic order |
| `CoverageSummary()` (`ServiceCount`, `TotalCapacity`, `TotalDemand`, `CoverageRatio`, `MeanQuality`) | read by the composition root to derive `ServiceCoverage` (edge pending, §1) | value struct |
| `CoverageByDistrict() []DistrictCoverage` (per-district `TotalCapacity`/`TotalDemand`/`CoverageRatio`/`MeanQuality`) | same consumer | value struct, sorted by `DistrictID` |

`CoverageRatio = 1.0` when `TotalDemand == 0`, else `clamp01(TotalCapacity / TotalDemand)` — the aggregate fraction of demand served, mirroring `ComputeQuality`'s capacityFactor. No output mutates any conserved stock: these are dimensionless query results, not a resource/money/population transfer (no `invariant` accumulator is fed by this surface).

---

## 5. Update Class

**T1.** Coverage is a migration-response *input*, not the conservation-tracked population delta itself — the same reasoning `docs/planning/icd/engine.attract-terms.md` §5 applies to the five attract terms. The aggregate has no tick of its own; the caller samples it once per month (the `attractHook` cadence) or on demand (screens). A value computed one tick late does not violate any invariant, so it is batchable/coalescible, never queued past the same month's use.

---

## 6. Shard Scope

**Single-shard (shard 0), matching the eventual consumer.** The aggregate itself is a whole-city read over a single `ServicesAPI` with no per-shard structure of its own; the consumer that drives it (the composition root's `attractHook`) is already `SingleShard() == true` (shard 0 only). When the demand push + coverage read is wired there, it inherits that contract — one call per month, not once per shard.

---

## 7. Determinism Guarantee

Enumeration and aggregation iterate over **sorted keys** — `ServiceID` and `ServiceKind` ascending, `DistrictID` ascending — never Go map iteration order over `instances`/`kinds`/the district table (GR#21). `CoverageRatio` and `MeanQuality` are pure floating-point folds of already-deterministic state (registration + pushed demand), in fixed source order. **No wall-clock time is read anywhere in this surface** — every value derives from sim state (registration, funding, pushed demand), never `time.Now()`.

---

## 8. Error / Registry Codes

`engine.services` owns `MET-G1200`–`MET-G1299` (`errors.go`). Codes this surface can surface: `MET-G1202` (`ErrServiceNotRegistered`) if `UpdateDistrictDemand` names an unregistered service; `MET-G1208` (`ErrCopiedValue`) if any aggregate accessor is called on a struct-copied `*ServicesAPI`; `MET-G1209` (`ErrNonFiniteInput`) if a pushed demand/distance is NaN/±Inf (rejected at the boundary per SEC-093); and one **new** `MET-G12xx` code (claimed in `data/errors.json`) for querying/pushing a district that was never seen — never a zero-value coverage silently read as "district exists but empty" (AC-23).

---

## 9. Resilience Behaviour

In-process, always-connected — the degenerate `integration.LocalReconnectHooks` case. Every aggregate accessor is a pure read or a validated push: a failure is deterministic given identical inputs, so a bare retry fails identically and the correct "retry" is "fix the caller bug," not a backoff loop. No queue, no remote dispatch (T1, in-process). A crash mid-push is recovered by the existing checkpoint/replay path; the aggregate holds no recoverable state of its own (it recomputes from registration + demand each call).

---

## 10. Monitoring Signals

**Status:** derived from whether an aggregate call returns an error this month (up) vs propagates one (degraded, logged per GR#1/GR#17). **Throughput/liveness:** `CoverageSummary.CoverageRatio` itself is the natural "did service investment move the city" signal — pipe it alongside `netMigration` in the composition root's read-only accessor surface (mirroring the attract-terms ICD §10), so a dashboard can plot coverage against migration on one timeline. **Queue depth:** n/a (T1, no queue). **Peak load:** the wall-clock cost of the fold is linear in registered instances, watched against the BUG-034 perf gate.

---

## 11. Required Tests

- **Enumeration determinism:** `ServiceIDs`/`ServiceKinds`/`DistrictIDs` return sorted, stable results across worker counts — a test registers N instances in unsorted order and asserts ascending output, byte-identical across repeated calls.
- **Summary formula:** two instances (capacities 10/40, demands 20/60) ⇒ `TotalCapacity == 50`, `TotalDemand == 80`, `CoverageRatio ≈ 0.625` (the exact fixture from AC-19).
- **Coverage responds to mutation:** raising one service's demand past capacity lowers `CoverageRatio`; upgrading its capacity raises it (must be able to FAIL — a constant ratio fails here).
- **Per-district isolation:** mutating district A's pushed demand leaves district B's `DistrictCoverage` unchanged.
- **No-mutation read-only:** snapshot `CoverageSummary`/`ServiceIDs` before/after repeated calls — byte-identical, no `FundingLevel`/`Demand` side effect.
- **Unknown-district error:** querying a never-pushed district returns the new registry code, no zero-value record created.
- **AC-coverage:** `docs/planning/acceptance/engine.services.md` AC-18…AC-25 are spec-lint-clean for this surface.

---

## 12. Change Control

Additive-only: a later revision may ADD a new aggregate accessor (e.g. a per-kind coverage breakdown) without a version bump provided it does not change any existing field's type or semantics. Any REMOVAL or semantic change to `CoverageRatio`'s formula, the `DistrictCoverage` shape, or the push-input contract requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected code.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **Consumer edge gap (GR#25):** `feat.compositionroot → engine.services` does not exist in `code.json`; it must be registered before the composition root may push demand and read the coverage summary to drive `ServiceCoverage` (§1). The aggregate itself is buildable and testable in isolation first.
2. **Pre-existing drift (not this surface's to fix, but flagged):** `engine.services.md` AC-3 references `engine.world` ("coverage radius … consumed against `engine.world`"), an edge that is not registered and a module that exposes no district concept. This ICD's push-input district design does **not** rely on that reference; a later registry-sync pass should correct AC-3.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-19 | Initial ICD — city/district coverage + enumeration aggregate for FEAT-167 `ServiceCoverage` (push-input districts, no new outbound edge). |
