# Proposal: The Integration Engine (foundation)

**Author:** Bev, 2026-08-18 · **Status:** Aaron-directed + 4 architecture rulings (2026-08-18 interview). This is the FIRM FOUNDATION to build BEFORE wiring modules (FEAT-169/167) or the 31 dormant modules. Grounded in the existing deterministic shard/phase/checkpoint architecture.

## 0. Why
The audit is blunt: 43 engine modules are built, but only ~8 are wired into the live tick and 31 do nothing in the running game. Rather than hand-wire 31 modules ad-hoc, build one **integration substrate** every module plugs into — contract-driven, queue-backed, monitored, resilient, and ready to offload compute to the cloud at 100M scale — then wire modules against it uniformly. Nail the foundation once; build the city on it.

## 1. Agreed principles (the convergence)

1. **Determinism is sacred (GR#21) and location-transparent.** Every integration's work is a **pure, seeded function over a shard of state** with a **fixed ordered merge**. WHERE it runs — a local goroutine today, a cloud worker later — is a runtime detail that can NEVER change the result. Byte-identical output regardless of execution location or worker count. This extends the existing `det.RunPhase` shard model; it does not replace it.
2. **Contract-first, ICD-first (GR#20/25).** No integration is built before its **Interface Control Document (ICD)** exists: a self-describing contract (inputs, outputs, shard scope, update class, determinism guarantees, error/registry codes, resilience behaviour) registered against `code.json`. BAs write the ICD; devs build against it. The ICD is the single source of truth for how an integration works.
3. **FIFO within a priority tier; coalesce only pure telemetry.** The catch-up/overflow queue processes **FIFO within each tier** (preserves causal order = deterministic). Tiers by importance: **T0 critical** (every-tick, must-not-drop), **T1 batchable** (heavy workloads processed in batches on a cadence), **T2 coalescible** (pure display/telemetry state where latest-wins is safe). FILO is banned for anything authoritative.
4. **Backpressure, never silent drop.** When the engine can't keep up, work **flows to a disk-backed overflow queue** and the sim applies backpressure (slows logical time / batches) to CATCH UP — it never silently drops authoritative work. A dropped update is a registry error (GR#17), not a shrug.
5. **Resilience by design, from day one.** Every integration is modelled as if it COULD be a remote service even while it's in-process today: it supports **retry, catch-up from a checkpoint, reconnect (incl. re-authentication + name lookup)**. Local = the degenerate always-connected case; the contract is future-proof for the cloud.
6. **Crash-recoverable to last save.** On a terminal/client crash or reboot, the engine **rebuilds full state deterministically from the last checkpoint** and replays/catches-up the queue to the crash point. State-rebuild is a first-class, tested path, not an afterthought.
7. **Observable, from day one.** Every integration reports **status (up/down/degraded), queue depth, throughput, and peak load**. A **local web dashboard** (served by the engine, mirroring the BOW web-UI pattern) shows the live integration map. Monitoring is part of the contract, not bolted on.
8. **Regression + determinism gates from day one.** Every integration ships with: a determinism test (byte-identical across worker counts + local-vs-simulated-remote), a resilience test (disconnect mid-update → retry → catch-up → reconnect+re-auth → identical final state), and a contract-conformance test (matches its ICD). No integration merges without them.
9. **Front-end-agnostic.** The integration engine is backend, behind the protocol/GR#20 UI-engine split. The Go TUI, a future web/React front-end, and the web dashboard all speak the same protocol; the engine never assumes a front-end.

## 2. Architecture (shape; concrete types finalised against the real seams)

- **`Integration` contract:** a module registers an integration described by its ICD. The engine owns a registry of integrations (keyed by GUID, like code.json edges). Each integration exposes: `RunShard(shard, seededState) → effects` (pure, deterministic), `Merge(orderedEffects)` (fixed order), an **update class** (T0/T1/T2), a **shard scope** (single-shard fast-path reuses BUG-269), and resilience hooks (`Checkpoint()`, `CatchUpFrom(checkpoint)`, `Reconnect()`).
- **Location-transparent executor:** wraps `det.RunPhase`. A shard's work is dispatched to a **worker** — today an in-process goroutine; the same interface lets a future **cloud worker** (lambda-style) run the identical pure function. Results merge in fixed (shard, sequence) order → byte-identical. A `WorkerPool` abstraction hides local-vs-remote; the deterministic merge is invariant.
- **Priority-tiered queue with disk overflow:** an ordered queue per tier between the tick driver and phase execution. In-memory FIFO until a high-water mark → **flows to a disk-backed segment** (append-only log, FIFO replay) → drains on catch-up. T1 heavy jobs batch; T2 telemetry coalesces. Backpressure signals the tick driver to slow rather than drop.
- **Resilience layer:** a connection state machine per integration (Connected / Retrying / CatchingUp / Reconnecting). On failure: retry with backoff → if still down, checkpoint the pending work → on recovery, re-authenticate + name-lookup the endpoint (no-op locally), then catch up FIFO from the checkpoint. All deterministic (retry counts/backoff are logical, not wall-clock, so replays are identical).
- **Crash recovery:** on start, if a crash is detected, rebuild state from the last checkpoint (existing save/checkpoint system) + replay the persisted overflow queue to the crash tick. Deterministic replay guarantees the rebuilt state equals the pre-crash state.
- **Monitoring:** each integration writes status/queue/throughput/peak to an observable surface; a local web server (BOW-server pattern) renders the integration map + queue timelines + peak-load history. Debug-mode gated.

## 3. Update classes (heavy vs light)
- **T0 critical (every tick):** population, money, conservation — small, must run every tick, never queued past one tick.
- **T1 batchable (heavy):** large sweeps (e.g. 100M-citizen demographic passes, traffic assignment, freight routing) — processed in batches on a cadence, sharded, cloud-offloadable; the queue absorbs bursts.
- **T2 coalescible (telemetry):** dashboard/UI state — latest-wins, safe to drop intermediate frames.
The ICD declares each integration's class.

## 4. The cloud path (later, determinism-preserved)
Because a shard's work is a pure seeded function with an ordered merge, moving it to a cloud worker changes nothing observable: the same inputs (seed, shard state) produce the same effects, merged in the same order. Offload is opt-in per integration (heavy T1 classes first), gated behind a worker-transport abstraction, and validated by the same determinism test (local run == simulated-remote run, byte-identical). No cloud offload ships until it passes that test. Runs 100% local on the laptop today.

## 5. ICD template (self-describing interface control document)
Each integration gets an ICD (a structured doc + a code.json registration) covering: identity (GUID, name, owning module), purpose, **inputs** (source module, shard-state read), **outputs** (effects, target stocks/edges), **update class** (T0/T1/T2), **shard scope** (single/all), **determinism guarantee** (seed key, merge order), **error/registry codes**, **resilience behaviour** (retry policy, catch-up semantics, reconnect/re-auth), **monitoring signals** (status/queue/throughput), and **acceptance/regression tests** required. The BA authors it before the dev builds. Template drafted with the boilerplate.

## 6. Build order (Aaron-directed)
1. **This foundation:** integration engine boilerplate + ICD template + monitoring dashboard + the day-one regression/resilience/determinism test harness.
2. **ICD stubs for the 31 dormant modules** (self-describing, so devs build against them).
3. **Build the 31 into the tick** behind their ICDs, each with the resilience + determinism + contract tests.
4. **THEN FEAT-169** (citizens cold pass — mortality + fertility live) and **FEAT-167** (real attractiveness terms) as the first two real integrations on the new substrate.

Determinism, GR#20/25 contracts, and the balance-number regime apply throughout; all thresholds/cadences are placeholders.

## 7. Concrete architecture (grounded in the real seams, 2026-08-18 audit)

The design maps cleanly onto existing machinery — we extend, not replace:

- **Location-transparent executor = a `det.RunPhase` variant.** `det.RunPhase[T,M](corrID, workers, zero, ShardFunc, combine, applyMsg)` already fans 256 shards across workers and merges via `MergeInOrder` (shard order) + `ApplyBarrier` ((shard,sequence) order), byte-identical regardless of worker count. The executor parameterises the *worker* behind a `WorkerPool` interface: a local goroutine today, a remote/cloud worker later. `ShardFunc` stays a pure seeded function (`det.Stream` is position-independent Philox — safe across goroutines/nodes). The BUG-269 `SingleShardHook` fast-path carries over for T0 light hooks.
- **Queue/overflow layer wraps `protocol.Transport`.** `Transport{SendCommand, Results, Events, Deltas, Close}` is *already* documented as the gRPC/out-of-process/cloud swap point, and `SendCommand` already returns `ErrCommandQueueFull` (existing backpressure). The integration queue is a `Transport` implementation that, on full, **spills to a disk-backed FIFO priority-tiered segment** instead of failing, then drains back as capacity frees. Every caller (headless `driveTicks`, interactive `boot`, tests) goes through `Transport` uniformly, so this is a single clean insertion point — and the same seam the cloud transport uses.
- **The overflow queue doubles as the durable command log → crash recovery.** Today `save`/`checkpoint` can rewind to the last *completed* checkpoint but cannot replay forward (the replay `Recorder` is in-memory only, ASM-442/470 blocked). The disk-backed, append-only, FIFO overflow queue IS the missing durable command log: on crash/reboot, load the last checkpoint (`checkpoint.Manager.Load`/`CurrentID`, byte-deterministic) then **replay the persisted command log to the crash tick** → rebuilt state == pre-crash state. This closes the crash-recovery gap as a by-product.
- **ICD extends `code.json`, not a parallel registry.** Each module's `inbound`/`outbound`/`consumers`/`calls` GUID-pinned edges gain contract fields: request/response types, update class (T0/T1/T2), shard scope, determinism (seed key + merge order), queue semantics, resilience policy, monitoring signals. GR#25's edge-first rule already governs it.
- **Monitoring taps existing hooks + the BOW-server pattern.** `core.WithPhaseObserver(kind,tick,month)` gives per-phase timing; `invariant.WithLogSink` gives conservation/violation events. The dashboard is a zero-dependency local Node `http` server (mirroring `tools/bow-server.js` + a template) reading those signals — debug-gated, front-end-agnostic.
- **Home:** a new `internal/foundation/integration/` package, built incrementally, formally registered via the register-guid/master-plan flow once its core stabilises.

## 8. Build increments (each ships with determinism + resilience + contract tests)
1. **Executor core** — `Integration` contract + `WorkerPool`-parameterised `RunPhase` variant + determinism test (local == simulated-remote worker, byte-identical across worker counts). *(smallest solid core — first)*
2. **Queue layer** — `Transport`-wrapping FIFO priority-tiered queue with disk overflow + backpressure + catch-up.
3. **Resilience + crash-recovery** — retry/catch-up/reconnect state machine + command-log replay from last checkpoint.
4. **ICD template + code.json extension** + the day-one regression harness.
5. **Monitoring dashboard.**
6. **ICD stubs for the 31 dormant modules → wire each into the tick → then FEAT-169/167.**
