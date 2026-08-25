# ICD: cloud.azure → int.solver + int.serializer (Azure tiers)

> Interface Control Document per `docs/planning/icd/TEMPLATE.md`. THIN ICD STUB (FEAT-192 / Tier D): the module is registered in `code.json` (seq 900, milestone `future`) but unbuilt — `cloud/` has zero files on `origin/main`. This stub documents the inbound contract, seams/cadence/failure modes, and the GR#25 edge list so the dev builds the cloud tier against it.

---

## 1. Identity

- **GUID:** `2167acd8-4aad-4bde-8f02-ac12f3d08c20` *(`cloud.azure`'s own module GUID — no dedicated edge GUID exists yet; this stands in until any new edge is registered, per §12)*
- **Name:** `cloud.azure.tiers`
- **Owning module (mkey):** `cloud.azure`
- **code.json edge ref(s):** outbound edges are live — `int.solver` (`783c1559-4a3c-4502-91f7-834e6a5fb2c7` / inbound `d20c26e9-5a1a-4628-9c93-f95938547f19`) and `int.serializer` (`8ee49e96-0de9-4326-9b90-e94622874f94` / inbound `2ed08d03-985d-4f84-941b-12aa7d3285f2`). Inbound contract `CloudServices` (`049859f7-ce29-44be-9dac-2d7656c83c3f`), format "gRPC (Solver wire contract) + Blob API", pattern "same seams as local; thresholds explicit in A9".

---

## 2. Purpose

`cloud.azure` (MOD-069, future) is the Azure tier: Blob saves (durable checkpoint persistence to the cloud), Batch tuning (offloading heavy solver sweeps to Azure Batch), solver offload, and cloud citizen shards (location-transparent sharding at 100M scale). Its governing constraint is "same seams as local" — every cloud capability exposes the identical contract the local path already implements, so the engine never forks on cloud-vs-local: the same determinism gate applies, and the same local fallback keeps the game runnable with zero cloud. This module is the persistence/offload complement to `cloud.gpu`, not a new execution model.

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `int.solver` (Solver wire contract) | A seeded solver problem for offload/batch tuning — same request shape the local solver consumes | gRPC request |
| `int.serializer` (serializer contract) | Checkpoint/save blobs to persist or restore — the same byte stream the local `save`/`checkpoint` path writes | serialized bytes |
| `cloud.azure` (config) | A9 thresholds — the explicit cut-over points where local hands off to cloud (capacity, cost, size) | config |

The cloud tier holds no sim authority: it is stateless against the solver and a pass-through against the serializer. Thresholds live in explicit A9 config, never implicit.

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| Solver result byte-identical to local | returned to the solver consumer; no conservation-accumulator effect | gRPC response |
| Durable Blob save / restore | the `save`/`checkpoint` durable store (cloud copy of the same serializer bytes) | serialized bytes |

No sim ledger or accumulator is mutated. Blob saves are the cloud copy of the existing checkpoint bytes — the authoritative state remains the local checkpoint; the cloud copy is durability, not a second source of truth (GR#3).

---

## 5. Update Class

**T1** — batchable. Solver offload and Batch tuning are heavy, shardable, cloud-offloadable workloads (the canonical T1 class, proposal §4). Blob saves are persistence on a cadence, not every-tick critical work. Nothing here is in the critical every-tick tier, and nothing is coalescible display telemetry — saves and solver results are authoritative and must be merged/durably written in order, never dropped.

---

## 6. Shard Scope

**Single-shard per request** in the integration sense: one stateless solver/serializer call per problem/checkpoint. The shard fan-out (including cloud citizen shards) is the *caller's* responsibility and unchanged — the cloud tier reimplements the local contract for each single call, with no shard structure of its own.

---

## 7. Determinism Guarantee

Cloud offload runs the same pure seeded solver function as local and returns byte-identical results for the same seed/problem (location-transparent, proposal §4). Blob saves are byte-identical copies of the local serializer output — a restore reproduces the exact checkpoint, so cloud-vs-local is unobservable. **No wall-clock time is read anywhere in this integration** — cut-over thresholds are explicit A9 config values, and any retry/backoff is logical (tick/attempt counters), never `time.Now()`.

---

## 8. Error / Registry Codes

`cloud.azure` has **no MET range of its own yet** (cloud layer, future milestone). It surfaces `int.solver`/`int.serializer`'s `foundation.errors` surface for contract failures, and maps Azure transport/Blob failures (unreachable, throttled, quota) to registry codes when built (GR#7). Open item (§12 OD-2): claim a cloud error range before the tier lands.

---

## 9. Resilience Behaviour

Remote integrations: `integration.Connection` with `Reconnect` + re-auth/name-lookup (proposal §1 point 5), and the local path as the degenerate always-connected fallback — on any cloud failure the engine re-runs the identical seeded solver locally or reads/writes the local checkpoint, yielding the identical result. Retry is logical backoff; catch-up is trivial for stateless solver work and re-issuable for Blob saves (idempotent, byte-identical). The consumer's `QueuedTransport` absorbs bursts; the cloud tier owns no queue of its own.

---

## 10. Monitoring Signals

**Status:** each tier up / down / degraded (Blob path, Batch path, solver-offload path reported separately). **Throughput / queue depth:** offloaded solver requests per second and the consumer queue depth. **Peak load:** per-tier wall-clock. The critical signal is the **local-fallback activation count** and the **Blob save/restore success rate** — a save that fails must be a registry error (GR#17), never a silent drop.

---

## 11. Required Tests

- **Determinism equivalence:** local vs Azure-offloaded solver runs produce byte-identical results; a Blob save → restore reproduces the exact local checkpoint.
- **Resilience / fallback:** cloud down mid-sweep → local fallback → identical final state; reconnect + re-auth on recovery; a failed Blob save surfaces a registry error.
- **Contract-conformance:** the gRPC solver request and the serializer bytes match their contracts exactly (this ICD's §3/§4).
- **AC-coverage:** the MOD-069 acceptance criteria must exist and pass `tools/plan/spec-lint.js` clean before build (GR#25 standing rule).

---

## 12. Change Control

Additive-only: a later revision may ADD an Input/Output without a version bump provided no existing field's type or semantics changes; any REMOVAL or semantic change to an existing Input/Output/Update-Class/Determinism guarantee requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected code.

**GR#25 edge list (outbound edges this module needs — registered vs new):**

| Edge | Status |
|---|---|
| `cloud.azure → int.solver` | ALREADY in code.json (`d20c26e9-5a1a-4628-9c93-f95938547f19`) |
| `cloud.azure → int.serializer` | ALREADY in code.json (`2ed08d03-985d-4f84-941b-12aa7d3285f2`) |

No NEW edges are required: both outbound edges are already registered, and the "same seams as local" pattern means the cloud tier plugs into existing contracts without new ones.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **Future-milestone sequencing.** `cloud.azure` is `future` (snull) — do not build until the local solver, serializer, and save/checkpoint paths land; the cloud tier wraps those seams and must not fork them (GR#3 single source of truth).
2. **Registry range claim.** A cloud-layer error range is needed before Azure transport/Blob failures can be registry-sourced (GR#7).

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-20 | Initial ICD (FEAT-192 Tier D stub) |
