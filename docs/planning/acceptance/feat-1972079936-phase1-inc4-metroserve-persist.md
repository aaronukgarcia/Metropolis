# FEAT-1972079936 Phase 1 inc4 — metroserve durable persistence + rehydrate-on-startup

**Epic:** FEAT-1972079936 (Compute offload, Path A). inc1 = Store, inc2 = journaler wiring +
genesis restore (`15918de`), inc3 = snapshot cadence (BLOCKED on FEAT-1972079941, deferred).
**inc4** (this) wires the durable Store into the `metroserve` network host so a persisted city
**survives a server restart** — the real server-side "kill localStorage" win, using inc2's
already-proven genesis restore (no snapshots needed).

## Context (committed, reuse — do not modify)
- `internal/persist`: `NewDiskStore(root)`, `Store`, `CityKey{TenantID,CityID}`.
- `internal/engine/compose`: `Deps.PersistStore`/`Deps.PersistCity`; `Wire(e, deps)`;
  `persistCommandJournaler` (write-through, fail-closed); `RestoreCommands(ctx, store, city)`
  (genesis replay, decode-errors-surface) — inc2's round-tripped restore.
- `cmd/metroserve/main.go`: today calls `compose.Wire(e, nil)` (line 75) → NO persistence;
  single-city host; `tickLoop` sends `AdvanceTicks{1}` per wall-clock interval.

## Design (authoritative)

### AC-1 — flags, default-off
Add to `metroserve`'s flag set: `--persist-dir` (string, default "" = persistence OFF,
behaviour byte-for-byte unchanged) and `--city` (string, default "default" = the CityID).
TenantID is the placeholder `"local"` (single-player, Phase 2 = real identity) — a documented
PLACEHOLDER const, not hardcoded inline.

### AC-2 — durable wiring
When `--persist-dir` is non-empty: `persist.NewDiskStore(persistDir)` (surface a construction
error → exit non-zero with a registry-appropriate message), build
`CityKey{TenantID:"local", CityID: *city}`, and pass `compose.Wire(e, &compose.Deps{PersistStore: store, PersistCity: city})`
instead of `nil`. Every accepted command (incl. the tickLoop's `AdvanceTicks`) is now durably
journaled by inc2's adapter. When `--persist-dir` is empty, keep `compose.Wire(e, nil)` exactly
as today.

### AC-3 — rehydrate-on-startup
When persistence is on AND the store already holds a journal for the city (`store.Exists` /
a non-empty `ReadJournal`), **before** starting the tick loop / command loop / WS server:
`RestoreCommands(ctx, store, city)` and replay the returned commands into `e` via the engine's
normal command path (the same path inc2's restore test uses), rebuilding the city to its last
persisted state. An empty/absent journal → a fresh genesis city (today's behaviour). Log a
concise "rehydrated N commands for city X" (or "starting fresh") line to stdout. A restore
error (corrupt journal surfaced by DecodeCommand) is FATAL — exit non-zero rather than silently
starting a fresh city over a persisted one (that would be the data-loss this epic kills).

### AC-4 — restart round-trip (the acceptance bar)
Because replaying millions of tick commands through a live binary is impractical to test at the
process level, prove the wiring at the `run()`/helper seam: factor the persist-store construction
+ rehydrate into a small testable helper (e.g. `setUpPersistence(e, persistDir, city) (…, error)`
or similar) that `run()` calls. Test: build engine A with a `t.TempDir()` DiskStore, submit a
deterministic command sequence (ticks + a couple of build/finance commands) through the live
compose path, then build a FRESH engine B pointed at the SAME dir via the helper (rehydrate), and
assert `A.StateDigest() == B.StateDigest()`. This proves metroserve's persist+rehydrate wiring is
lossless. Prove fail-able (skip the rehydrate call → digests diverge). Also a test that
`--persist-dir ""` wires nil (no DiskStore created, `compose.Wire(e,nil)` path).

### AC-5 — isolation & determinism
Different `--city` values persist/rehydrate to isolated journals (city A's restart never loads
city B). GR#21: no map-range/`time.Now` nondeterminism in the rehydrate path (the tickLoop's
wall-clock is pre-existing and not part of restore — replay uses journaled command order).

## Known limitation (documented, not a defect)
Rehydrate replays the FULL journal from genesis, so a long-running server's restart is O(commands)
— potentially slow for a city advanced for many sim-years (the tickLoop journals one AdvanceTicks
per interval). This is exactly what inc3's snapshot cadence would bound; it's deferred
(FEAT-1972079941). For Phase 1 the correctness (lossless resume) is the requirement; restore
speed is a later optimization. Note this in metroserve's doc comment.

## Gates (as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./cmd/metroserve/... ./internal/engine/compose/... ./internal/persist/... -race -count=2`, FULL `go test ./...`, `golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- `--persist-dir ""` (default) → behaviour byte-for-byte unchanged (nil Deps path).
- Reuse inc2's `RestoreCommands` — do NOT re-implement restore.
- A corrupt-journal restore is FATAL (never silently start fresh over a persisted city).
- Independent Destructive round (attacker ≠ author) before commit (GR#23): attack the
  corrupt-journal-fatal path, city isolation, the restart round-trip losslessness, and the
  default-off invariant.
