# FEAT-1972079936 Phase 1 inc2 — wire the durable Store into the engine journaler

**Epic:** FEAT-1972079936 (Compute offload to Azure, Path A). **Phase 1** = durable
server-side persistence (kills the localStorage/data-loss class). **inc1** landed the
`internal/persist` Store (`4e266eb`). **inc2** (this) makes a *running* engine durably
persist every accepted command, and proves the persisted journal restores byte-identically
(GR#12: no backup without a restore test).

**Registered GR#25 edge:** `feat.compositionroot → int.persist` (committed `ee61083`). The
engine owns the `core.CommandJournaler` interface and *calls* it (dependency inversion,
Aaron's engine-owns-journal DD); the durable adapter is injected by the composition root.
`engine.core` must NOT import `internal/persist`, and `internal/persist` must stay a pure
opaque-bytes leaf (no `protocol`/engine imports) — the adapter, which knows both, lives in
`internal/engine/compose`.

## Design (authoritative — build to this)

### AC-1 — durable write-through journaler adapter
New file `internal/engine/compose/persistjournal.go`. A type (e.g. `persistCommandJournaler`)
that implements **exactly** the `core.CommandJournaler` interface (whatever its full method
set is — inspect `internal/engine/core/commands.go`; at minimum `ObserveCommand`, plus any
`ObserveResult`/`ObserveEvent`/`ObserveDelta` it declares). It **wraps an inner
`core.CommandJournaler`** (never replaces it):
- On `ObserveCommand(cmd)`: call `inner.ObserveCommand(cmd)` **first** (preserve in-memory
  replay). If that errors, return the error (do not persist a command the inner rejected).
- Then serialize via **`protocol.EncodeCommand(cmd)`** — the SAME codec the in-memory
  `harness.replay.Recorder` uses (`record.go:101`), so the durable bytes are byte-identical
  to the in-memory journal. Do NOT invent a serialization.
- Then `store.AppendJournal(ctx, city, data)`.
- **Fail-closed:** if `AppendJournal` errors, `ObserveCommand` returns that error (a failed
  durable persist must be visible, not swallowed — this is the whole point of the data-loss
  cure, and aligns with GR#27's fail-closed capture principle). Any non-command Observe*
  methods delegate straight to `inner` (they are not persisted to the command journal).
- The adapter carries a `context.Context` source (a field or `context.Background()` for
  Phase 1 — no per-call ctx plumbing required yet) and a fixed `persist.CityKey`.

### AC-2 — composition-root wiring, default-off
In `internal/engine/compose/compose.go`, extend `Deps` with an optional durable-persist
config — a `PersistStore persist.Store` and a `PersistCity persist.CityKey` (or a small
`PersistConfig` struct). In the journaler-resolution path (~compose.go:850-862, where
`deps.CommandJournaler` defaults to a fresh `harness.replay.Recorder`):
- If `PersistStore == nil` → **unchanged** behaviour (in-memory journaler only). Every
  existing test and the default path must be byte-for-byte unaffected.
- If `PersistStore != nil` → wrap the resolved journaler:
  `journaler = newPersistCommandJournaler(journaler, PersistStore, PersistCity)` before
  `e.SetCommandJournaler(journaler)`. The inner journaler stays whatever it already was
  (the injected Recorder or a test spy), so replay + durable persist both happen.

### AC-3 — CityKey placeholder (single-player)
Phase 1 is multi-tenant-single-player with a placeholder tenant (epic open-Q #3: a single
fixed `TenantID` is an acceptable placeholder until Phase 2 auth). The composition root
supplies a documented placeholder default (e.g. `CityKey{TenantID: "local", CityID: "default"}`)
when persist is enabled but no city id is configured. Mark it PLACEHOLDER in a comment with
the balance-number-regime note — real tenant/city identity is Phase 2. Do not hardcode it
inside the adapter; it flows in via `Deps`.

### AC-4 — restore path (GR#12: backup implies restore)
A restore function (e.g. `RestoreCommands(ctx, store, city) ([]protocol.Command, error)` in
the compose package, or a documented reuse of an existing replay entrypoint) that reads
`store.ReadJournal(ctx, city)`, decodes each frame via **`protocol.DecodeCommand`**, and
returns the command sequence (or replays it into a supplied engine/composition via the
existing replay path — reuse `harness.replay`'s replay mechanism if one exists rather than
re-implementing command submission). Decode errors surface (a corrupt frame is a real
failure, not a silent skip — the Store already guarantees torn frames never reach here).

### AC-5 — round-trip determinism test (the acceptance bar)
A test that: (a) builds a Composition with a `MemStore` (or `t.TempDir()` `DiskStore`) persist
wired, (b) submits a deterministic sequence of N accepted commands (advance ticks + a few
build/finance commands) through the live engine, (c) reads the persisted journal back via
AC-4, (d) restores into a **fresh** Composition, replays, and asserts the two engines'
debug/state snapshots are **byte-identical**. This proves persist→restore is lossless and
deterministic. Must be `-race` clean. Prove the test can fail (mutate one persisted command
→ snapshots diverge).

### AC-6 — determinism & isolation guarantees preserved
- The persisted command bytes equal the in-memory Recorder's bytes for the same command
  (both `protocol.EncodeCommand`) — a test asserting equality.
- Persist enabled vs disabled produces the **same** engine state (persistence is a side
  channel, never influences the sim) — the byte-identical-state gate from AC-5 with persist
  off vs on.
- No new map-range/`time.Now`/nondeterminism in the adapter (GR#21).

## Gates (all must pass, run as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./internal/engine/compose/... ./internal/persist/... -race -count=2`, FULL `go test ./...` (nothing else regresses), `golangci-lint run ./...` at the CI-pinned v2.5.0, and astgate `TestRun_LiveTree` green (the adapter holds no mutex, so it should add zero copyguard findings; if it does, that's a real finding to fix, not accept).

## Out of scope for inc2 (later increments)
- Snapshot cadence / snapshot persistence (inc3) — this inc is the command-journal write+restore.
- Rehydrate-on-connect in `metroserve` (the transport-side restore) — inc3/Phase 2.
- localStorage→Store migration tool (deferred, epic open-Q #5).
- Journal compaction / retention pruning (needs Aaron's ruling, Phase 1 doc open-Q #2).
- Real multi-tenant identity (Phase 2 auth).

## Non-negotiables
- `engine.core` gains **no** import of `internal/persist`. `internal/persist` gains **no**
  import of `protocol`/engine packages. The adapter in `internal/engine/compose` is the only
  place that knows both.
- Default (no Store configured) behaviour is **exactly** unchanged.
- Independent Destructive round (attacker ≠ author) before commit (GR#23), attacking: the
  fail-closed append-error path, persist-on≡persist-off state determinism, the restore
  round-trip losslessness (incl. a torn/corrupt journal), and codec consistency with the
  in-memory Recorder.
