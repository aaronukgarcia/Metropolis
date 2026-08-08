# int.solver — Solver contract: CPU/GPU/cloud interchangeable offload seam

Module key: `int.solver` · GUID `783c1559-4a3c-4502-91f7-834e6a5fb2c7` ·
BoW code `INT-003` · path `internal/foundation/solver/` · spec ref §15; A4;
A9; M0-ENG §1.

Sprint-0 freeze review page. Covers the seam diagram, the request/response
schema, the determinism and fallback rules, the A4/A9 sizing tables, a
prose-only gRPC mapping sketch for the sidecar, and open questions.

Status: **awaiting freeze review.**

## Why this module exists

M0-ENG §1 fixes the doctrine before any engine code exists: *"the same
solver interface will have three interchangeable backends — CPU (v1,
always works) → GPU sidecar (local acceleration) → cloud (Azure). One
seam, three muscles. Do not entangle CUDA with the engine."* §15 names the
same idea from the game-design side: heavy solves (traffic equilibrium,
deep projections, batch life-writing) are offloadable "as stateless
request/response with local-fallback." A4 adds the sizing discipline that
makes the seam trustworthy at scale (zone-aggregated OD, not raw cells),
and A9 gives the explicit threshold at which local CPU stops being
sufficient.

`int.solver` is the single Go package that makes this concrete: a `Solver`
interface, a versioned `Request`/`Response` pair, a priority/fallback
registry, and one always-available `CPUBackend`. Nothing else in the
codebase is allowed to import CUDA, gRPC stubs, or Azure SDKs on the
strength of routing a solve — they all talk to `int.solver` instead, and
`int.solver` talks to whichever backend happens to be registered.

## The seam

```
                 ┌─────────────────────────────────────────────┐
                 │                  ENGINE                     │
                 │  engine.traffic, engine.citizens, cloud.*,   │
                 │  the projection module, etc.                │
                 └───────────────────┬───────────────────────--┘
                                     │  solver.Get(problem) -> Solver
                                     │  solver.Solve(req) -> resp, err
                                     ▼
                 ┌─────────────────────────────────────────────┐
                 │        int.solver (this package)             │
                 │  contract.go  Solver, Request, Response       │
                 │  problems.go  ProblemKind + payload schemas   │
                 │  sizing.go    A4/A9 capacity-planning tables  │
                 │  registry.go  priority order + fallback chain │
                 │  cpu.go       CPUBackend (mandatory fallback) │
                 └───────┬───────────────┬───────────────┬─────┘
                         │ priority 0    │ priority 50    │ priority 100
                         ▼               ▼                ▼
                 ┌───────────────┐ ┌─────────────┐ ┌────────────────┐
                 │  CPUBackend    │ │ GPU sidecar  │ │  Azure cloud    │
                 │  (this pkg,    │ │ (sidecar/    │ │  (cloud/,       │
                 │  always wins   │ │ solver-gpu/, │ │  cloud.azure,   │
                 │  as last       │ │ cloud.gpu,   │ │  future) speaks │
                 │  resort)       │ │ future) speaks│ │  the same       │
                 │                │ │ the same     │ │  contract over  │
                 │                │ │ contract over│ │  gRPC (+ Blob   │
                 │                │ │ gRPC         │ │  for large refs)│
                 └───────────────┘ └─────────────┘ └────────────────┘
```

The engine never talks to a backend directly. It calls `solver.Get(problem)`
to obtain a `Solver`, then calls `.Solve(req)`. Which physical machine ran
the request is invisible to the caller except through `Response.Backend`
and `Response.Stats` — diagnostic fields, never simulation input (see
Determinism below). `CPUBackend` is always priority 0 (lowest) and always
registered for every `ProblemKind`, which is what makes it the mandatory
fallback rather than just another candidate.

## Request/Response schema

```go
type Request struct {
    Problem       ProblemKind
    SchemaVersion uint32
    Seed          uint64
    Deterministic bool   // v1: MUST be true
    Payload       []byte // problem-specific, versioned by SchemaVersion
}

type Response struct {
    Payload  []byte     // problem-specific result; bit-identical across backends
    Backend  string     // diagnostic only, e.g. "cpu.v1", "gpu.sidecar"
    Stats    SolveStats // diagnostic only
    Warnings []string   // human-readable, never machine-parsed by simulation
}

type SolveStats struct {
    ElapsedMS  int64 // wall-clock diagnostic; the ONE sanctioned time.Now() use
    Iterations int    // diagnostic
}
```

`Payload` is opaque at the contract layer. Its shape is defined per
`ProblemKind` in `problems.go` and versioned independently via
`SchemaVersion`, so a backend can roll forward without breaking an older
engine build mid-deploy.

### The four offload slots (`ProblemKind`)

| Kind | Spec | Payload schema | Status |
|---|---|---|---|
| `TrafficAssignment` | §19, §21, §51; A4 | `TrafficAssignmentRequestV1` — zone count, graph ref, OD matrix ref, VDF params, max iterations, convergence epsilon | fields fixed, firms up with `engine.traffic`/`engine.roads` |
| `ColdPassBatch` | §17-§18; A4/A9 | `ColdPassBatchRequestV1` — shard refs, month | minimal stub, TODO-SPEC with `engine.citizens` |
| `DeepProjection` | §15 cloud path | `DeepProjectionRequestV1` — world snapshot ref, horizon months | minimal stub, TODO-SPEC with the projection module |
| `LifeWriting` | §5, §17 | `LifeWritingRequestV1` — citizen shard refs, month | minimal stub, TODO-SPEC with `engine.citizens` |

`EchoProblem` is a fifth, non-spec `ProblemKind` that exists only inside
this package: `CPUBackend` implements it for real (a deterministic
byte-XOR transform keyed off `Seed`), which is what lets the registry,
fallback, and determinism guarantees be tested end-to-end before any real
engine module exists to exercise them. No engine code should ever
construct an `EchoProblem` request.

`TrafficAssignmentRequestV1` carries **references** (`GraphRef`,
`ODMatrixRef`), not embedded graph/matrix data — per A4 discipline the
referenced data can be tens to low-hundreds of MB (see Sizing below) and
does not belong inlined into every request. The reference encoding itself
(shard ID? snapshot generation number? content hash?) is still open — see
Open Questions.

## Determinism rules

This is the rule the whole seam exists to protect, so it is worth stating
twice, in two different failure modes it prevents:

1. **Result determinism.** For a `Deterministic=true` request (v1: always),
   `Response.Payload` MUST be bit-identical for the same
   `(Problem, Seed, Payload)` tuple, on every backend that implements that
   `ProblemKind`. This is what lets the fallback chain (below) swap CPU for
   GPU for cloud mid-request without the player — or the CI determinism
   gate (M0-ENG §1.2: same seed, 120 months, twice, `sha256` match) —
   ever seeing a different world.
2. **Diagnostics never leak into state.** `Response.Backend` and
   `Response.Stats` (which includes the one wall-clock read the whole
   engine is allowed, `ElapsedMS`) exist purely for the F12 info panel and
   failover logging. They MUST NOT be read by simulation logic, summed
   into any aggregate, hashed into a snapshot, or otherwise allowed to
   change what the game simulates. `contract.go`'s doc comments spell this
   out at the field level; a code reviewer should treat any simulation
   code that branches on `Response.Backend` as a contract violation, full
   stop.

`Request.Deterministic` is carried as an explicit field, not assumed, so
the schema does not need to change shape the day a legitimately
non-deterministic backend appears (e.g. a trained surrogate model, per
§15's "surrogate-model path ... if any subsystem outgrows local"). Until
that day every implementation should treat `Deterministic=false` as an
error rather than silently accepting it — the exact error contract for
that case is deferred (see Open Questions) since no such backend exists
yet to test against.

## Fallback semantics

`registry.go` implements the mandatory local-fallback rule two ways:

- **Loud failure on missing fallback.** `Get(problem)` returns
  `(nil, ErrNoFallback)` if no registered backend's `Supports(problem)`
  returns true for that kind. In a correctly wired binary this can only
  happen if `CPUBackend` was never registered — a startup bug, not a
  runtime condition. `Get`'s signature is `(Solver, error)` rather than a
  bare `Solver`, specifically so this case is a typed, returned error
  (Golden Rule #1) instead of a nil-pointer landmine or a panic.
- **Transparent failover on backend error.** `Get` returns a `chainSolver`
  that holds every candidate for that `ProblemKind`, sorted
  highest-priority first (ties broken by registration order). Calling
  `.Solve` on it tries each candidate in turn; the first success wins. If
  a candidate errors, the chain wraps the error with the backend's name,
  invokes an optional failover hook (`Registry.SetFailoverHook`, used by
  tests to observe the event without inspecting internals), and moves to
  the next candidate. The caller only ever sees total success or total
  failure — never a bare error from an intermediate backend — though if
  every candidate fails, the final error is an `errors.Join` of all of
  them for diagnosis.

Priority is an `int`; higher wins first. The convention this package uses
(not enforced by the type system) is `CPUBackend` at priority `0`, the GPU
sidecar around `50`, and cloud around `100` — expressing "prefer the
fastest tier that's actually available," with CPU as the guaranteed floor.

## Sizing tables (A4/A9)

| Quantity | Value | Spec |
|---|---|---|
| OD zone count | 1e3 – 1e4 | A4 |
| OD matrix worked example | 5,000 zones ⇒ "~100MB" | A4/R3 |
| Road graph edges | 1e5 – 1e6, even late-game | R3 |
| GPU sidecar VRAM envelope | 4 GB (RTX 3050-class reference card) | M0-ENG §1 |
| Local-CPU citizen ceiling | 20–30 million citizens | A9 |

`sizing.go` turns these into typed constants (`ODZoneCountMin/Max`,
`RoadGraphEdgesMin/Max`, `GPUVRAMEnvelopeBytes`,
`LocalCitizenCeilingLow/High`) plus four helpers used by capacity planning
and (eventually) the F12 panel:

- `EstimateODBytes(zones int) int64` — dense zones×zones OD matrix size.
- `EstimateRoadGraphBytes(edges int, bytesPerEdge int64) int64` — road
  graph size at a caller-supplied or default per-edge footprint.
- `FitsGPUEnvelope(totalBytes int64) bool` — rough check against the 4GB
  budget.
- `ExceedsLocalCPUCeiling` / `ExceedsLocalCPUCeilingHigh(citizenCount
  int64) bool` — A9 threshold checks at the low (20M) and high (30M) ends
  of the range.

## gRPC mapping sketch (prose only — no code, no deps added)

v1 ships CPU-in-process only; nothing in this package imports gRPC. The
mapping below is the intended shape for when the sidecar/cloud tiers land,
so the frozen `Solver` interface does not need to change shape later —
only a new backend implementation is added.

- `Solver.Solve(Request) (Response, error)` maps onto a single unary RPC,
  `SolverService.Solve(SolveRequest) returns (SolveResponse)`, matching
  the in-process contract field-for-field (`Problem`, `SchemaVersion`,
  `Seed`, `Deterministic`, `Payload` in; `Payload`, `Backend`, `Stats`,
  `Warnings` out).
- Large referenced data (`GraphRef`, `ODMatrixRef` and friends) is **not**
  streamed through the RPC payload — the request carries references only,
  and the sidecar/cloud process is expected to fetch the referenced
  data through whatever shared channel already carries it (local shared
  memory / mmap for the sidecar; Blob for cloud, per `int.serializer` and
  the cloud path in §15). This keeps the RPC message itself small
  regardless of OD/graph size.
- A gRPC-backed `Solver` implementation is just another `registry.Register`
  call at process init — the engine-side code that calls `Get`/`Solve`
  does not change at all. This mirrors the existing "in-process channel
  v1, gRPC by config flag" pattern already used for the engine↔UI
  protocol (§15, M0-ENG §1's process topology).
- Streaming/partial results are explicitly out of scope for v1: the
  contract is one request, one response. If a future backend needs
  progress reporting (e.g. a long cloud batch), that is a `Warnings`- or
  new-field-level addition to `Response`, not a shape change to `Solver`.

## Open questions (for Aaron, freeze review)

1. **OD sizing table doesn't reconcile.** R3's worked example is "~5,000
   zones ⇒ ~100MB of OD." `EstimateODBytes` uses `float64` cells (matching
   this BoW item's own `TrafficAssignmentRequestV1`/`VDFParamsV1` use of
   `float64` throughout), which gives 5,000² × 8 bytes = 200MB, not
   ~100MB. The 100MB figure only falls out of `float32` cells. Either the
   spec's worked example assumed `float32` OD cells (and the payload
   schema should say so explicitly), or "~100MB" was a rounder
   approximation than its own arithmetic supports. `sizing.go` currently
   computes the conservative (2×) `float64` estimate and flags the
   discrepancy in its doc comment rather than silently picking whichever
   cell width makes the numbers match. Needs a decision either way before
   `engine.traffic` builds against it.
2. **Reference encoding for `GraphRef`/`ODMatrixRef`.** Currently typed as
   opaque `string`. Candidates: a shard ID + generation number pair (fits
   the 256-shard determinism model, M0-ENG §1.2), a content hash (fits the
   route-cache's "contents-independent" requirement, M0-ENG §1.3), or a
   simple monotonic snapshot counter. This should probably be decided
   alongside `int.serializer`/`engine.roads`, not unilaterally here.
3. **`Deterministic=false` error contract.** No backend needs to reject a
   non-deterministic request today because none exists yet. When the
   surrogate-model path (§15) eventually appears, does `Solve` return a
   typed `ErrNonDeterministicNotSupported`-style error, or does the
   contract grow a `Backend.SupportsNonDeterministic(problem)` capability
   query instead of an error-per-call? Deferred until there's a real
   second data point.
4. **Priority number convention isn't type-enforced.** `0`/`50`/`100` for
   CPU/GPU/cloud is a documented convention in this file and in
   `registry.go`'s comments, not a constraint the compiler checks. Worth
   revisiting once the GPU sidecar module (`cloud.gpu`, MOD-068) actually
   registers a backend — do we want named priority constants
   (`PriorityCPU`, `PriorityGPU`, `PriorityCloud`) exported from this
   package instead of bare integers at each call site?
5. **`ColdPassBatch`/`DeepProjection`/`LifeWriting` payload schemas are
   intentionally minimal stubs.** They exist so `ProblemKind` has all four
   spec'd slots represented, but their real field lists depend on
   `engine.citizens` and the (not-yet-BoW-tracked) projection module. Not
   blocking for this freeze review — flagged so the freeze doesn't
   accidentally read as "these three are done."
