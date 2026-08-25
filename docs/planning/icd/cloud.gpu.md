# ICD: cloud.gpu → int.solver (GPU solver sidecar)

> Interface Control Document per `docs/planning/icd/TEMPLATE.md`. THIN ICD STUB (FEAT-192 / Tier D): the module is registered in `code.json` (seq 890, milestone `future`) but unbuilt — `sidecar/solver-gpu/` has zero files on `origin/main`. This stub documents the inbound contract, seams/cadence/failure modes, and the GR#25 edge list so the dev builds the sidecar against it.

---

## 1. Identity

- **GUID:** `a755e8b6-fd25-4843-90e8-f0da23e50726` *(`cloud.gpu`'s own module GUID — no dedicated edge GUID exists yet; this stands in until any new edge is registered, per §12)*
- **Name:** `solver.gpu.sidecar`
- **Owning module (mkey):** `cloud.gpu`
- **code.json edge ref(s):** outbound edge is live — `int.solver` (`783c1559-4a3c-4502-91f7-834e6a5fb2c7` / inbound `d20c26e9-5a1a-4628-9c93-f95938547f19`). Inbound contract `SolverGPU` (`7bc61f07-d9b4-413a-b283-c1c79eaf96c9`), format "gRPC (Solver wire contract)", pattern "stateless; local fallback mandatory".

---

## 2. Purpose

`cloud.gpu` (MOD-068, future) is the C++/CUDA GPU sidecar that accelerates the heavy solver class — the traffic-assignment / projections sweeps already registered behind `int.solver`. It exists to make the 100M-citizen scale tractable by moving a shard's pure seeded solver function to a GPU worker while producing byte-identical results to the local solver. The governing constraint is "local fallback mandatory": the engine must never *depend* on GPU presence, so the sidecar is an opt-in accelerator, not a new execution path — the same determinism test (local == GPU, byte-identical) gates it (proposal §4).

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `int.solver` (Solver wire contract) | A seeded solver problem — the pure request over a shard of state, keyed by seed + shard id | gRPC request (opaque to the sidecar; it is stateless) |

The sidecar is stateless: it receives a fully-specified seeded problem, computes, and returns. It holds no sim state and reads nothing but the request payload.

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| Solver result byte-identical to local `int.solver` | returned over the gRPC wire to the calling solver consumer; no conservation-accumulator effect (pure compute) | gRPC response |

No sim ledger or accumulator is mutated by the sidecar. Its only contract is equivalence with the local solver for the same seed and problem.

---

## 5. Update Class

**T1** — batchable. Solver sweeps (traffic assignment, projections) are the canonical heavy, sharded, cloud-offloadable workload the proposal names first for offload (§4). It is not in the critical every-tick tier and not coalescible telemetry — a solver result is authoritative and must be merged in order, never latest-wins-dropped.

---

## 6. Shard Scope

**Single-shard per request** in the integration sense: one stateless sidecar call per seeded solver problem (the shard fan-out is the solver consumer's responsibility, unchanged). The sidecar itself has no shard structure — `SingleShard() == true`, one call per problem.

---

## 7. Determinism Guarantee

The sidecar implements the same pure seeded solver function as local `int.solver`: identical seed + identical problem → identical result, so offload is location-transparent and byte-identical to a local run (the proposal §4 gate). Merge order stays fixed (shard, sequence) at the consumer, unaffected by whether the work ran locally or on GPU. **No wall-clock time is read anywhere in this integration** — GPU wall-clock is an operational metric only, never a determinism input.

---

## 8. Error / Registry Codes

`cloud.gpu` has **no MET range of its own yet** (cloud layer, future milestone). It surfaces `int.solver`'s `foundation.errors` surface for contract-level failures, and maps gRPC transport failures (unreachable, timeout) to registry codes when built (GR#7). Open item (§12 OD-2): claim a cloud error range before the sidecar lands.

---

## 9. Resilience Behaviour

The sidecar is the first genuinely-remote integration: it uses `integration.Connection` with `Reconnect` + re-auth/name-lookup (per proposal §1 point 5), but the mandatory local fallback is the degenerate always-connected path — on any GPU failure, the solver consumer re-runs the identical seeded function locally and gets the identical result. Retry policy is logical (not wall-clock) backoff; catch-up is trivial because the sidecar is stateless — a failed request is simply re-issued from the same seed. No queue is owned by the sidecar; the consumer's `QueuedTransport` absorbs bursts.

---

## 10. Monitoring Signals

**Status:** sidecar up / down / degraded (up = last request returned; down = local fallback active). **Throughput / queue depth:** solver requests per second and the consumer queue depth. **Peak load:** GPU wall-clock per request. The critical signal is the **local-fallback activation count** — a rising count means the GPU is not paying for itself, surfaced via the registry (GR#17).

---

## 11. Required Tests

- **Determinism equivalence:** local `int.solver` vs the GPU sidecar produce byte-identical results for the same seed/problem set (the proposal §4 gate — this is the test that must pass before any offload ships).
- **Resilience / fallback:** GPU down mid-sweep → local fallback → identical final state; reconnect + re-auth on recovery.
- **Contract-conformance:** the gRPC request/response matches the Solver wire contract exactly (this ICD's §3/§4).
- **AC-coverage:** the MOD-068 acceptance criteria must exist and pass `tools/plan/spec-lint.js` clean before build (GR#25 standing rule).

---

## 12. Change Control

Additive-only: a later revision may ADD an Input/Output without a version bump provided no existing field's type or semantics changes; any REMOVAL or semantic change to an existing Input/Output/Update-Class/Determinism guarantee requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected code.

**GR#25 edge list (outbound edges this module needs — registered vs new):**

| Edge | Status |
|---|---|
| `cloud.gpu → int.solver` | ALREADY in code.json (`d20c26e9-5a1a-4628-9c93-f95938547f19`) |

No NEW edges are required: the single outbound edge to the Solver wire contract is already registered. (The reverse direction — a solver consumer *invoking* the sidecar — is expressed through `int.solver`'s contract, already pinned.)

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **Future-milestone sequencing.** `cloud.gpu` is `future` (snull) — do not build until `int.solver`'s local path and the integration substrate (`internal/foundation/integration/`) land; the sidecar plugs into the same seams and must not fork the solver.
2. **Registry range claim.** A cloud-layer error range is needed before the gRPC transport errors can be registry-sourced (GR#7).

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-20 | Initial ICD (FEAT-192 Tier D stub) |
