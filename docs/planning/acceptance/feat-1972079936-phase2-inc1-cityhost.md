# FEAT-1972079936 Phase 2 inc1 — CityHost: multi-city registry in one process

**Epic:** FEAT-1972079936 (Compute offload, Path A). Phase 0 (versioning) + Phase 1
(durable persistence: Store, journaler, genesis restore, metroserve durable+rehydrate) are
COMPLETE. **Phase 2** = one process hosting M independent single-player cities + routing +
failover-by-replay (Aaron's topology ruling: "each instance hosting M independent city
sessions; elastic"). **inc1** (this) = the **CityHost registry** — the core that owns and
lifecycles N independent running cities. (inc2 = wire connection→city routing through the
handshake; inc3 = failover-by-replay across slots. Sticky routing + NLB = Phase 4.)

## Context (reuse, committed)
- One running city today (`cmd/metroserve/main.go`) = `core.NewEngine(WithWorldSeed)` →
  `compose.Wire(e, deps)` → `protocol.NewInProcTransport(...)` → `e.StartSubscriptionPump(ctx,
  transport)` → `go e.RunCommandLoop(ctx, transport)` → `tickLoop(ctx, transport, …)`.
- inc4 `cmd/metroserve/persist.go`: `setUpPersistence(e, persistDir, cityID, stdout)` builds the
  DiskStore + wires `Deps{PersistStore,PersistCity}` + rehydrates from the journal (the
  `rehydrateGuardStore` prevents double-append). Generalize this PER-CITY.
- `persist.Store` already isolates by `CityKey{TenantID,CityID}` (SHA-256 keys) — N cities
  share one `--persist-dir` root, each its own journal namespace.

## Design (authoritative) — all new code in `cmd/metroserve/` (plan-exempt; matches inc4)

### AC-1 — CityHost type
`cityhost.go`: a `CityHost` owning `map[persist.CityKey]*runningCity` guarded by a `sync.Mutex`
(+ SEC-020 copyguard if it carries the mutex by value in a copyable struct — mirror the persist
Store pattern). A `runningCity` bundles that city's `*core.Engine`, `*compose.Composition`,
`protocol.Transport`, its per-city `context.CancelFunc`, and a `done` signal for its goroutines.
Construct via `NewCityHost(persistDir string, tickInterval time.Duration) (*CityHost, error)`
(persistDir "" = in-memory/no-persist mode allowed for tests, matching inc4 default-off).

### AC-2 — GetOrCreate (the core)
`GetOrCreate(ctx, cityKey) (*runningCity, error)`:
- If the city is already running, return the existing `*runningCity` (idempotent — a second
  caller for the same key gets the SAME engine, never a second one).
- Else build a fresh city: new engine (seed derived deterministically per city — e.g. from the
  cityKey, documented; NOT a global mutable seed), `compose.Wire` with `Deps{PersistStore,
  PersistCity: cityKey}` (persist on when persistDir set), rehydrate from the journal if one
  exists (reuse inc4's guarded-rehydrate logic per-city), then start its pump + command loop +
  a per-city tick driver on a per-city child context, register it in the map, return it.
- Concurrency: two goroutines calling `GetOrCreate` for the SAME key concurrently must yield
  exactly ONE running city (no duplicate engine, no lost goroutine) — hold the lock across the
  check-and-insert, or a per-key construction guard. `-race` clean.
- A construction/rehydrate error (incl. corrupt journal — FATAL per inc4) returns the error and
  registers NOTHING (no half-built city left in the map).

### AC-3 — lifecycle + shutdown
- `Shutdown(cityKey) error`: cancel that city's context, wait for its goroutines (pump/loop/tick)
  to drain, close its transport, remove it from the map. Idempotent (shutting a non-existent /
  already-shut city is a no-op, not a panic).
- `Close() error`: shut down ALL cities cleanly (fan-out cancel + wait). No goroutine leak
  (verify with a goroutine-count check or the loops' `done` channels).

### AC-4 — isolation (the multi-tenant guarantee)
Two different cities NEVER share engine state or journal. A command routed to city A changes only
A's `StateDigest`; B is untouched. A's journal contains only A's commands. Test: run divergent
command sequences on two cities via one host, assert each city's digest matches a standalone
single-city engine fed the same sequence, and neither leaks into the other.

### AC-5 — durability across host restart
With persistDir set: create city A via a host, submit commands, `Close()` the host; create a NEW
host on the same persistDir, `GetOrCreate(A)` → it rehydrates A byte-identically (digest matches).
Proves the host is a thin lifecycle layer over inc4's proven per-city persistence. Prove
fail-able.

### AC-6 — concurrency / -race
Concurrent `GetOrCreate` of DIFFERENT cities proceeds in parallel (not serialized beyond the map
mutex's brief critical section); concurrent same-city yields one. `-race -count=2` clean.
GR#21: no map-range over the city map where iteration order affects behaviour (shutdown-all may
range but must be order-independent); the per-city seed derivation is deterministic (no time.Now).

## Out of scope for inc1 (later increments)
- Connection→city ROUTING through the handshake (inc2) — inc1 is the registry only; a small
  test/helper may call `GetOrCreate` directly, but `wsserver`/`main.go` wiring to route real WS
  connections is inc2.
- Idle-city EVICTION / capacity limits (inc3 or its own inc).
- Failover across separate PROCESSES / sticky routing / NLB (inc3 + Phase 4).

## Gates (as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./cmd/metroserve/... ./internal/engine/compose/... ./internal/persist/... -race -count=2`, FULL `go test ./...` (note BUG-464 is FIXED; any `cmd/metropolis` flake would be a NEW regression), `golangci-lint run ./cmd/metroserve/...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- New code in `cmd/metroserve/` (no new GR#25 edges; if you believe CityHost must be a shared
  module, STOP and flag it rather than editing master-plan/code.json).
- Reuse inc4's guarded per-city rehydrate — do NOT re-implement restore or re-introduce the
  double-append bug (per-city guard).
- Isolation + single-city-per-key are the correctness bar; concurrency `-race` clean.
- No goroutine leaks on Shutdown/Close.
- Independent Destructive round (attacker ≠ author) before commit (GR#23): attack duplicate-city
  races, cross-city isolation, goroutine leaks, corrupt-journal-fatal per city, and restart
  durability.
