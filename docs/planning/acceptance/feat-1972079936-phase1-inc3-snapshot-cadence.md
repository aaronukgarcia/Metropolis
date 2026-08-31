# FEAT-1972079936 Phase 1 inc3 — snapshot cadence + snapshot-aware restore

> **⛔ BLOCKED (2026-08-31) on FEAT-1972079941** (per-module state+RNG serialization).
> The `save` subsystem has ZERO registered participants (`save.DefaultParticipants = []`),
> so a snapshot can't capture real engine state and **AC-4 (snapshot+tail == genesis,
> byte-identical) is unachievable** — the inc3 builder correctly stopped rather than ship a
> hollow snapshot. Snapshots are only a restore-*speed* optimization; Phase 1's data-loss
> cure does not need them (inc2 genesis restore is already lossless). **The march proceeds to
> inc4 (metroserve genesis-restore wiring) instead**; inc3 resumes if/when FEAT-1972079941
> lands. Criteria below are retained for that resumption.

**Epic:** FEAT-1972079936 (Compute offload, Path A). **Phase 1** = durable persistence.
inc1 = the `internal/persist` Store (`4e266eb`). inc2 = write-through command journaler +
genesis restore (`15918de`). **inc3** (this) = periodic full-state snapshots so restore is
bounded (latest snapshot + short journal tail) instead of always replaying from genesis.

## Aaron's rulings (2026-08-31, recorded on the BOW item)
1. **Snapshot cadence = every 360 ticks (1 simulated year; `DailyTicksPerMonth=30`, year=12×30).**
   PLACEHOLDER per the balance-number regime — Aaron retunes later. Bev's recommendation,
   Aaron said "make a recommendation and go with it."
2. **Keep the FULL journal** for Phase 1 — snapshots are a restore-*speed* optimization, NOT
   a replacement. NO journal compaction / prefix-drop in Phase 1 (deferred, likely Phase 4
   with Azure Blob). Genesis-replay stays intact (GR#27 before/after comparison; Phase-3
   convergence A/B).

## Design (authoritative)

### AC-1 — full-state serialization via the EXISTING save subsystem (do NOT invent)
The snapshot payload MUST be produced by the existing full-state serializer: `internal/engine/save`
(`Manager` / `Participant` over `int.serializer`'s canonical NDJSON `StateSerializer`), the same
mechanism the game-save + checkpoint features use. First establish whether the composition wires
a `save.Manager` / participant set today; if it does, reuse it; if not, wire the participant set
compose already knows (the composed modules) into a save-serialize call. The snapshot bytes are
**opaque to `int.persist`** — `Store.PutSnapshot(ctx, city, bytes)` takes `[]byte`; the
serialization stays entirely on the compose/save side. `int.persist` gains NO new imports.

### AC-2 — snapshot on the cadence boundary
The engine takes a snapshot when its tick count crosses a multiple of the cadence
(`SnapshotCadenceTicks int64 = 360`, a single named const with a PLACEHOLDER + balance-regime
comment). Wire this on the same seam inc2 used: the snapshot trigger observes the engine's tick
(reuse the clock's month/boundary check style, `clock.go:152`). Each snapshot is stored via
`Store.PutSnapshot` under the same `CityKey` the journaler uses, and MUST record the **tick (or
journal offset) at which it was taken** so restore knows where the tail begins — carry that
marker inside the snapshot payload or the snapshot id (the Store's snapshot id/list ordering,
`ListSnapshots`, is available). Fail-closed on a snapshot write error consistent with inc2's
seam behaviour (surface it; do NOT silently skip — a missing snapshot silently degrades restore
to genesis, which is safe correctness-wise but must be observable, see BUG-472's class).

### AC-3 — snapshot-aware restore
Extend inc2's restore: if a snapshot exists for the city, `GetSnapshot` the LATEST, deserialize
it into a fresh composition via the save subsystem's load path, then replay ONLY the journal
commands recorded AFTER that snapshot's tick marker. If NO snapshot exists, fall back to inc2's
full-genesis replay (`RestoreCommands`). Decode/deserialize errors surface (never silent-skip).

### AC-4 — THE determinism bar (GR#12 + the whole epic's correctness)
Snapshot-aware restore MUST yield a **byte-identical `StateDigest`** to full-genesis replay for
the same session. Test: run engine A through ≥2 cadence boundaries (e.g. 800+ ticks) with persist
wired, then (a) restore via snapshot+tail into engine B and (b) restore via full-genesis into
engine C, and assert `A.StateDigest() == B.StateDigest() == C.StateDigest()`. This proves the
snapshot serialization is COMPLETE (nothing missed → no divergence) and the tail boundary is
correct. Prove it can fail (corrupt the snapshot, or off-by-one the tail marker → digests diverge).

### AC-5 — keep-full-journal invariant
A test asserting the journal still contains ALL commands from genesis after snapshots are taken
(no compaction) — i.e. genesis replay is still possible AND still matches. This pins Aaron's
ruling #2 so a future compaction change can't silently regress it.

### AC-6 — default-off + isolation preserved
No snapshot behaviour when persist is not wired (nil Store) — existing paths byte-for-byte
unchanged. Snapshots are per-CityKey isolated (city A's snapshot never used to restore city B).
GR#21: no map-range/`time.Now` nondeterminism in the snapshot/restore path.

## Gates (run as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./internal/engine/compose/... ./internal/engine/save/... ./internal/persist/... -race -count=2`, FULL `go test ./...`, `golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Out of scope (later)
- metroserve durable wiring + rehydrate-on-connect (inc4: `main.go` `compose.Wire(e,nil)` → real Deps).
- Journal compaction / snapshot pruning (deferred per ruling #2).
- Real multi-tenant identity (Phase 2).

## Non-negotiables
- Reuse the existing `save`/`serialize` subsystem for state serialization — do NOT hand-roll.
- `int.persist` stays a pure opaque-bytes leaf (no new imports); the compose/save side owns serialization.
- Default (nil Store) behaviour EXACTLY unchanged.
- Independent Destructive round (attacker ≠ author) before commit (GR#23) — attacking snapshot
  completeness (the AC-4 digest equality under adversarial sessions), the tail-boundary off-by-one,
  a corrupt snapshot, and the keep-full-journal invariant.
