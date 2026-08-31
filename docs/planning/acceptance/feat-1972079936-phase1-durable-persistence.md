---
mkey: FEAT-1972079936
phase: 1 of 5 (Compute Offload to Azure, Path A)
title: Durable server-side persistence (journal + snapshot store)
status: DRAFT for Aaron review
author: BA analyst pass, 2026-08-31
depends_on: docs/planning/compute-offload-architecture.md (§5 Phase 1)
spec_ref: docs/planning/compute-offload-architecture.md; FEAT-1972079852 (engine-owns-journal DD); FEAT-1972079897/854 (hard-reset-replay, journal/genesis replay); feat.checkpoint (FEAT-064)
---

# FEAT-1972079936 Phase 1 — Durable Server-Side Persistence

## Overview

Today the only durable copy of a city is the browser's `localStorage`
savepoint (`webconsole/src/sim/replay.ts`: snapshot + journal tail, rotated
across `SAVEPOINT_CAP` slots, quota-bounded, single-tab, and silently
clobberable by a second tab or a reload race — this is exactly how BUG-469
lost a 714k-pop city). The Go engine already has the two structural
ingredients this phase needs, in-process but not yet durable:

- **The journal.** `internal/harness/replay.Recorder` (`record.go`) captures
  every accepted `protocol.Command`/`CommandResult`/`Event`/`Delta` in
  strict arrival order. Since FEAT-1972079852 inc3/inc4 (Aaron's
  engine-owns-journal DD, 2026-08-31), `engine.core.Engine` calls
  `CommandJournaler.ObserveCommand` for every accepted command via the
  `SetCommandJournaler`/`WithCommandJournaler` seam
  (`internal/engine/core/commands.go:62-160`) — a `*replay.Recorder`
  satisfies that interface structurally, no adapter needed. **But** that
  same file documents the gap this phase exists to close (commands.go:79-85,
  "DURABILITY GAP"): *"harness.replay.Recorder buffers records in memory
  only and loses them on crash — wiring this seam does not change that."*
  (ASM-470). `feat.checkpoint`'s own doc.go says the identical thing from
  the snapshot side (`internal/engine/checkpoint/doc.go:70-75`): AC-11/AC-12
  (a per-branch command log + replay-based integrity verification) are
  blocked on Recorder's durability gap, "the wrong shape for an always-on
  fork log."
- **The snapshot.** `internal/engine/checkpoint.Manager`
  (`internal/engine/checkpoint/checkpoint.go`) already does whole-state
  checkpointing with parent/child lineage, atomic promotion (`saveBundle`:
  save then write the lineage sidecar, roll back on any failure), and
  bounded pruning — built on `internal/engine/save.Manager` /
  `internal/foundation/serialize`'s bundle format (header.json + shards/).
  This is feat.checkpoint's existing "snapshot" primitive; Phase 1 does not
  reinvent it, it points it at a new root.

**What Phase 1 does:** close the Recorder durability gap with a `Store`
abstraction, wire `metroserve` (`cmd/metroserve/main.go`) to flush every
journaled command through it, use `checkpoint.Manager` (rooted at the store,
not a local scratch dir) for periodic snapshots, and prove a city rehydrates
byte-identically in a **fresh process** from store contents alone. This
directly kills the localStorage-quota/single-tab-race class the epic doc
names as the Phase 1 deliverable, and is the prerequisite for Phase 2
(failover-by-replay) — same store, same rehydrate path, just triggered by a
dead instance instead of a deliberate restart.

**Out of scope for Phase 1** (later phases): Azure Blob backing (Phase 4 —
the `Store` interface exists precisely so this is a second implementation,
not a rewrite), multi-tenant session routing/NLB (Phase 2), auth/identity
(Phase 2 open question #3), engine convergence of any gameplay domain
(Phase 3). The webconsole's TS mock-sim localStorage path is **not removed**
— it remains the offline/mock fallback (see AC-9).

---

## AC-1 — `Store` interface: shape and namespace scheme

**Criterion:** a new package (proposed `internal/engine/persist`, name TBD by
the Architect — see GR#25 section) defines a `Store` interface covering
exactly four operations, with no Azure-specific type anywhere in the
interface or its call sites:

```go
type CityKey struct {
    TenantID string // player/account identity (Phase 1: a single fixed value is acceptable; see Open Question 3)
    CityID   string // one city/savegame within that tenant
}

type Store interface {
    AppendCommand(ctx context.Context, city CityKey, rec serialize.Record) error
    PutSnapshot(ctx context.Context, city CityKey, snap SnapshotBundle) (SnapshotID, error)
    LoadLatest(ctx context.Context, city CityKey) (SnapshotBundle, []serialize.Record, error) // nearest snapshot + journal tail since it
    ListCities(ctx context.Context, tenant string) ([]CityKey, error)
}
```

- `serialize.Record` (`internal/foundation/serialize`) is reused verbatim
  for journal entries — the SAME type `replay.Recorder.Records()` already
  returns (record.go:24, `KindCommand`/`KindResult`/`KindEvent`/`KindDelta`)
  — never a bespoke journal-entry type.
- `SnapshotBundle` wraps the existing `checkpoint.Checkpoint` /
  `save.Manager` bundle shape (header.json + shards), not a new format.
- Key/namespace scheme: `{tenant}/{cityID}/journal/*` and
  `{tenant}/{cityID}/snapshots/{snapshotID}/*` — chosen so a local-disk impl
  is literally a directory tree (`root/{tenant}/{cityID}/...`) and an Azure
  Blob impl (Phase 4) is the same path as a blob-name prefix, with zero
  interface change.
- **Check:** a local-disk `Store` implementation and a fake in-memory `Store`
  (for fast unit tests of everything above it) both satisfy the interface;
  the engine-side and metroserve-side code that calls `Store` methods is
  typed against the interface, never a concrete struct.
- **Mutation:** delete the local-disk implementation's package import and
  substitute the in-memory fake at the call site — the calling code (the
  journaler seam, the checkpoint wiring) must compile and its tests must
  still pass unmodified, proving the abstraction isn't leaky.
- **False-pass guard:** a test that only instantiates the interface and
  never calls a method through a variable of the interface type (only ever
  through the concrete type) does not prove the abstraction — the test must
  hold a `var s Store = <impl>` and call every method through `s`.

## AC-2 — journal persistence: every accepted command is durably appended

**Criterion:** a `Store`-backed `CommandJournaler` (call it
`persist.DurableJournaler`, composing a `*replay.Recorder` for in-memory
ordering plus a `Store` for the durable append) is wired via
`engine.SetCommandJournaler` in `metroserve`'s composition
(`cmd/metroserve/main.go:74-78`, alongside `compose.Wire`). Every command
`engine.core.Engine`'s `accept()` path journals (per the existing
`CommandJournaler` contract, commands.go:86-88) is appended to the `Store`
before `HandleCommand` returns success to the caller — not batched,
deferred, or best-effort.

- **Check:** send N commands over the wire to a running `metroserve`
  (or directly to the wired `*core.Engine` in a test), then read the
  `Store`'s journal for that `CityKey` and confirm exactly N
  `serialize.Record{Kind: "command", ...}` entries, byte-identical to what
  `protocol.EncodeCommand` produced for each (same encoding
  `replay.Recorder.ObserveCommand` already uses, record.go:98-107).
- **Mutation:** make the append a no-op (comment out the `Store.AppendCommand`
  call, or make it silently swallow its own error) — the test must fail:
  reading the journal after N commands must show fewer than N records, or
  the accepted-command count must not match the persisted-record count.
- **False-pass guard:** do not assert only "the command list is non-empty" —
  assert an exact count and exact per-record content (kind + decoded
  correlation ID), so a journaler that persists the wrong command, or drops
  every third one, is caught.
- A command's success response to the client is **not** returned until the
  durable append succeeds (fail-closed — mirrors GR#27's "capture before
  wipe" posture: no acknowledged command may exist only in memory). If the
  append fails, the command is rejected with a registry error (GR#7 — a new
  `MET-Pxxx` code via `tools/plan/add-error.js`, not an ad hoc string), never
  silently accepted-but-unpersisted.

## AC-3 — snapshot cadence and the rehydrate algorithm

**Criterion:** `checkpoint.Manager` (rooted at the `Store`'s snapshot
namespace, via a `Store`-backed adapter satisfying whatever minimal
save-target seam `save.Manager` needs — or, if `save.Manager` is
disk-root-coupled today, a thin shim translating its bundle writes into
`Store.PutSnapshot` calls; the Architect resolves which, see GR#25 section)
takes a snapshot every `SnapshotCadenceTicks` ticks (a single named
constant, BALANCE PLACEHOLDER per the standing regime — no hardcoded magic
number at the call site, mirrors `checkpoint.MaxRetainedForks`'s existing
precedent, checkpoint/doc.go:47-51).

**Rehydrate algorithm** (`Store.LoadLatest`):
1. Find the newest `SnapshotBundle` for the `CityKey`.
2. Load it via the existing `save.Manager.Load`/`checkpoint.Manager.Load`
   path (reused verbatim — never reimplemented, GR#3/GR#20).
3. Read every journal `Record` appended **after** that snapshot's tick
   (the journal tail).
4. Replay the tail through the engine's own command-application path (the
   SAME mechanism `harness.replay.EnginePlayer` already proves —
   `internal/harness/replay/player_engine.go` — reused, not reimplemented).
5. The resulting live state is the rehydrated city.

- **Check:** drive a city N snapshot-cadences deep (so at least 2 snapshots
  + a non-empty tail exist), record its full state hash (or a full-state
  diff via the existing consistency/conservation checks —
  `internal/harness/replay/compare.go`'s `CompareResult`), then rehydrate
  from the `Store` in a **separate `*core.Engine` instance** and assert the
  rehydrated state is byte-identical (AC-6 below formalizes this as its own
  process-boundary test).
- **Mutation:** skip loading the snapshot tail (start replay from the
  snapshot's own tick + apply zero tail records) — rehydrated state must now
  diverge from live state whenever a non-empty tail exists, and the test
  must catch that divergence.
- **False-pass guard:** the test city must have a non-trivial tail (more
  than zero commands after the last snapshot) — a test that always
  snapshots on every command exercises the "tail is empty" degenerate case
  only and would pass even with tail-replay silently broken.

## AC-4 — conservation/consistency across rehydrate

**Criterion:** every conservation/invariant check the engine already runs
during live play (`engine.invariant`, `internal/harness/replay/compare.go`'s
`CompareResult`) is run against the rehydrated state and must report zero
violations — the store must never be a route to a state the live engine
itself would refuse to produce.

- **Check:** rehydrate a city with citizens/finance/buildings active, run
  the existing invariant/conservation suite against the rehydrated state,
  assert zero failures.
- **Mutation:** corrupt one journal record's payload after it's durably
  stored (flip a field in the persisted bytes) — replay must now either (a)
  detect the corruption and fail closed (reusing `fixtureCorruptError`-style
  detection, record.go:104/113/124/134's pattern) or (b) produce a state
  that the conservation check catches. Silent divergence (a corrupted
  record replays as if valid, producing a wrong-but-undetected state) is
  the failure this AC exists to rule out.

## AC-5 — crash/atomicity safety (the BUG-469 fix, server-side)

**Criterion:** a process crash or forced kill **during** a journal append or
a snapshot write leaves the `Store` in a state from which the LAST
successfully-completed append/snapshot is still loadable — never a torn,
half-written record that corrupts every subsequent read.

- For the local-disk `Store` impl: use the same write-then-atomic-rename
  discipline `checkpoint.Manager.saveBundle` already follows for its
  lineage sidecar (checkpoint.go:308-323 — write, then promote; on failure,
  remove the half-written artifact) — write to a temp path in the same
  directory, `fsync`, then atomic rename into place; a journal append is a
  single `O_APPEND` write of one complete `serialize.Record` (JSON-per-line
  or length-prefixed, Architect's call) — never a partial-record write left
  visible to a concurrent reader.
- **Check:** a fault-injection test that kills the writer process (or
  simulates it — e.g. a wrapped `io.Writer` that returns a partial-write
  error, or panics, mid-append) after N complete appends and 1 partial one;
  reopening the `Store` and reading the journal must yield exactly N
  records — the partial write must be truncated/ignored, not surfaced as a
  corrupt-but-present record.
- **Mutation:** remove the fsync/atomic-rename step (write-in-place,
  no temp+rename) — the test must now be able to observe a torn record on
  a simulated kill-mid-write, proving the test can actually detect the
  hazard it's guarding.
- **False-pass guard:** the test must inject the fault at a byte offset
  strictly inside a record's serialized bytes (not conveniently between two
  records) — a test that only ever kills the writer between complete
  records never exercises the failure mode the AC is titled for.

## AC-6 — rehydrate-on-any-instance (fresh-process proof)

**Criterion:** a city saved by process A loads identically in a **freshly
started process B that has never held that city in memory**, given only
the `Store`'s on-disk contents and the `CityKey`.

- **Check:** an integration test (or a two-binary test harness spawning
  `metroserve` twice against the same `-store-root`, or two independent
  `*core.Engine` + `compose.Wire` instances in one test binary, each with
  its own `Store` handle open on the same root) — process A plays a city,
  persists journal+snapshots, exits (or is simply discarded, no explicit
  shutdown message passed to B); process B constructs a brand-new
  `*core.Engine`, calls `Store.LoadLatest` for the same `CityKey`, replays,
  and its resulting state matches A's last-known state exactly (same
  approach as AC-3's byte-identical assertion, but now genuinely
  cross-process/cross-Engine-instance rather than same-process).
- **Mutation:** have process B start from a **fresh in-memory-only**
  `replay.Recorder`/`checkpoint.Manager` pair that never touches the shared
  `Store` root (i.e., simulate "the store handle is scoped wrong") — the
  test must fail, because B now has no data for that `CityKey` and either
  errors or produces a blank city instead of A's city.
- This AC is the structural basis Phase 2 cites for "failover-by-replay" —
  cross-reference it there; Phase 1 does not implement failover itself
  (no dead-instance detection, no session registry), only proves the
  primitive it depends on.

## AC-7 — the webconsole becomes a thin client for persistence (boundary definition)

**Criterion:** Phase 1 must be provable server-side (via `metroserve` + a
`Store`, exercised over the real protocol) **before** the browser stops
running the mock sim (Phase 3). Define the boundary precisely:

- When the webconsole is connected to a live `metroserve` instance (the
  FEAT-1972079852 adapter path, `LiveEngineBadge`), every command the
  player issues travels over the wire and is durably journaled server-side
  per AC-2 — the webconsole issues **no** `persistSavepoint` call for that
  session; the server is the sole durable copy.
- When the webconsole is running the offline/mock TS sim (no live engine
  connection), `webconsole/src/sim/replay.ts`'s existing
  `persistSavepoint`/`restoreFromSavepoint`/localStorage path is
  **unchanged** — Phase 1 does not touch or gate that file's behaviour.
- **Check:** a test (webconsole-side, `webconsole/test/*`) asserting that
  when the store/adapter reports a live-engine connection, no
  `localStorage.setItem` under the `metropolis.savepoint.*` key prefix
  occurs during a play session; a companion test asserts the mock-mode path
  still writes savepoints exactly as today (regression guard on `replay.ts`
  behaviour — reuse `webconsole/test/capture-before-wipe.test.mjs`'s
  fixture style).
- **Mutation:** remove the live/mock branch check (always call
  `persistSavepoint` regardless of connection state) — the live-mode test
  must now fail by observing a spurious localStorage write.
- **False-pass guard:** the test must actually exercise a command/action
  during the "connected" state, not merely check the connection flag in
  isolation — a no-op session passes trivially either way.

## Determinism section (GR#21)

- Replay is **byte-identical**: two rehydrates of the same `CityKey` at the
  same store state produce the same bytes, the same tick, the same citizen/
  finance/building state — no wall-clock, no map-iteration-order dependency,
  no goroutine-race-order dependency in the `Store` read/replay path (the
  map-range-with-break class, GR#21/Vestige `metropolis-map-range-break-gotcha`,
  applies to any new iteration over journal records or snapshot shard lists
  — classify every such loop; order-independent folds only, or an explicit
  sort key).
- The `Store` **never influences sim computation** — it is a pure
  persistence sink/source. No `Store` method may be called from inside a
  tick's compute path in a way that could change its outcome (e.g. no
  "check if a snapshot exists" branch inside gameplay logic). The only
  legitimate call sites are: (a) `CommandJournaler.ObserveCommand` — a
  write, after acceptance, never gating acceptance; (b) the snapshot-cadence
  timer — a write, decoupled from gameplay outcome; (c) boot-time rehydrate
  — a read, before the tick loop starts.
- Snapshot bundles carry no timestamp/random value as load-bearing state
  (mirrors `checkpoint.Checkpoint`'s existing AC-13 discipline,
  checkpoint/doc.go:77-87) — only `CreatedAtTick`/lineage/`ParentID`.

## GR#25 edge-audit (for the Architect — NOT registered here)

New cross-module edges this phase's design implies, to be reviewed and
(if approved) registered in `code.json`/`master-plan-v2.1.json` by the
Architect **before** any acceptance-criteria prose or code depending on them
lands (GR#25 — this document itself must not smuggle unregistered edges
into implementation; the list below is the audit deliverable, not a
registration):

1. **`engine.core` → `<new persist module>`** (the `Store`-backed
   `CommandJournaler` implementation) — parallels the already-registered
   `engine.core -> harness.replay` edge (landed 7b68d10, per
   commands.go:67-69) but for the durable layer; needs its own edge unless
   the Architect decides the new package folds *into* `harness.replay`
   itself (in which case no new module key is needed, only a new file/
   capability inside the existing module — Architect's call, flagged as an
   open design question below too).
2. **`feat.checkpoint` → `<new persist module>`** — `checkpoint.Manager`
   needs to write bundles through `Store` instead of (or in addition to) a
   bare `os.*` root path; today `feat.checkpoint`'s registered edges
   (code.json lines ~781, ~1103, ~3002, ~5529, ~9207, ~10720, ~10864) do not
   include a store/persist dependency.
3. **`cmd/metroserve` (int.protocol transport host) → `<new persist module>`**
   — main.go wires the journaler seam; today metroserve's registered edges
   are all `int.protocol`-side (code.json's many `int.protocol` edge rows);
   none reach a persistence module because none existed before this phase.
4. **Possible new module key** — if the Architect decides `Store` merits
   its own module (rather than folding into `harness.replay` or
   `feat.checkpoint`), it needs a master-plan entry (a `mod.persist` or
   `engine.store` key, GUID, and BOW `MOD-0xx` item) before any of edges
   1–3 above can cite it by name.

**Count: 3 candidate new edges + 1 open module-key decision.**

## Increments

- **inc1 — `Store` interface + local-disk impl + journal persistence +
  cross-process rehydrate proof.** AC-1, AC-2, AC-6 (using the local-disk
  impl only; no snapshots yet — replay from genesis if that's still
  affordable at this stage, or accept a "snapshot-free" fresh-process test
  scoped to a small city). Ships: the durability-gap fix
  (`replay.Recorder` records are no longer lost on crash when a `Store` is
  wired), proven in a fresh process.
- **inc2 — snapshot cadence + snapshot-anchored rehydrate + crash
  atomicity.** AC-3, AC-4, AC-5. Ships: bounded replay cost via
  `checkpoint.Manager`-on-`Store`, and the torn-write fault-injection proof
  that is the direct BUG-469-class fix.
- **inc3 — `metroserve` wiring + per-tenant namespacing + the webconsole
  thin-client boundary.** AC-2 (wired into the real `cmd/metroserve/main.go`
  binary, not just a test harness), AC-7. Ships: a running `metroserve`
  instance that durably persists to local disk today, with the `CityKey`
  namespace scheme already multi-tenant-shaped for Phase 2, and the
  webconsole-side boundary test proving mock mode is untouched.

Each increment is independently shippable and GR#23-round-able on its own
(no increment depends on Azure Blob, session routing, or engine convergence
— those are later phases).

## Open questions for Aaron

1. **Snapshot cadence value.** `SnapshotCadenceTicks` is a placeholder per
   the standing balance-number regime (Vestige `metropolis-balance-number-regime`)
   — proposed default and final value need Aaron's row-by-row approval, not
   an interview re-litigating the regime itself.
2. **Retention/pruning of old snapshots + journal history.** `checkpoint.Manager`
   already bounds fork retention via `MaxRetainedForks`
   (checkpoint/doc.go:44-51) — does Phase 1 apply the same bounded-retention
   policy to the linear (non-forked) snapshot chain, or keep every snapshot
   forever until Phase 4 adds real storage-cost pressure? Journal
   compaction (dropping a journal prefix once a snapshot supersedes it)
   needs an explicit ruling too — silently deleting a "superseded" prefix
   removes the ability to replay from genesis, which the epic's Phase-3
   convergence story may still want (before/after comparison per GR#27's
   rationale).
3. **Per-tenant isolation model for Phase 1.** The epic's own open question
   #1 (multi-tenant single-player vs shared-world) is upstream of this: does
   Phase 1 need a *real* tenant identity (an account/device token) or is a
   single fixed `TenantID` (today's de facto "one player, one city") an
   acceptable placeholder until Phase 2's auth work lands? The `CityKey`
   shape in AC-1 is designed to make this a later parameter, not a later
   rewrite — but Aaron should confirm the placeholder is acceptable for
   Phase 1's own acceptance (i.e., Phase 1 can close without real
   multi-tenant auth).
4. **Local-disk-only for Phase 1, Azure Blob deferred — confirm.** The epic
   doc explicitly says "local disk first, Azure Blob later" (§5); this
   document assumes that and defines `Store` so a Blob impl is additive.
   Confirm no Phase 1 acceptance criterion requires touching Azure.
5. **Migrating an existing browser localStorage save into the server
   store.** BUG-469's victim (and every existing dogfood city) has a city
   that exists ONLY as a browser savepoint today. Does Phase 1 need an
   explicit "import a `Savepoint` (webconsole/src/sim/replay.ts's type) into
   a server `Store`" tool/command, or is that out of scope until a live
   engine domain exists that the imported city could actually run on
   (Phase 3 convergence)? If in scope, it needs its own AC (not written
   here, pending this ruling).
6. **Where does the new persistence code live?** New module key
   (`mod.persist`/`engine.store`), or a capability folded into the existing
   `harness.replay` and/or `feat.checkpoint` modules? This gates the GR#25
   edge-audit's edge count and must be resolved by the Architect before
   inc1 starts (see GR#25 section above).
