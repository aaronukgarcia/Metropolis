# FEAT-1972079942 — CityHost idle-city eviction (bounded host memory)

**Epic context:** FEAT-1972079936 Phase 2. inc1 (CityHost registry) + inc2 (connection routing)
are landed. This closes Phase 2's single-process scope: unload cities with no active connections
so the host's memory is bounded, rehydrating on demand from the journal (inc4's proven path).

**Scope discipline:** ADDITIVE. Do NOT change inc2's `TransportResolver func(tenant, city string)
(protocol.Transport, error)` signature or its per-connection binding — those are freshly rounded.
Add eviction via NEW optional hooks. New/changed code in `cmd/metroserve/` + a small ADDITIVE
optional hook in `internal/protocol/wsserver/`.

## Design (authoritative)

### AC-1 — connection lifecycle hook on wsserver (additive, optional)
Add an OPTIONAL `WithConnectionLifecycle(onOpen, onClose func(tenantID, cityID string))` Option to
`wsserver.Server`. When set, the server calls `onOpen(tenant, city)` right after a connection's
handshake successfully binds it to a city, and `onClose(tenant, city)` exactly once when that
connection ends (normal close, error, or server shutdown) — with the SAME tenant/city that was
resolved. When NOT set, behaviour is byte-for-byte unchanged (every existing test passes). The
hooks must fire exactly-once-paired per connection (no double onClose, no missed onClose on any
disconnect path) and be safe under concurrent connections (`-race`).

### AC-2 — CityHost active-connection refcount
`CityHost` tracks a per-city active-connection count. `metroserve`'s lifecycle closure wires
wsserver's onOpen→`host.Acquire(key)` (++count) and onClose→`host.Release(key)` (--count). Counts
are guarded by the host mutex (the same one guarding the city map). `Acquire` on a not-yet-running
city is not required to build it (routing's GetOrCreate already did/ will) — but Acquire/Release
must never make count negative or panic on an unknown key (log + ignore a stray Release).

### AC-3 — idle eviction loop
The host runs a single background evictor (started in `NewCityHost`, stopped in `Close`) that
periodically finds cities with **count == 0 for longer than an idle timeout** and evicts them
(the existing `Shutdown(key)` teardown). `IdleEvictTimeout` + the sweep interval are named consts
with PLACEHOLDER + balance-regime comments (proposal: evict after 5 min idle, sweep every 30s —
Aaron retunes). A city's "idle since" timestamp is set when its count drops to 0 and cleared when
it rises above 0.

### AC-4 — evict-vs-reconnect race safety (the P0)
Eviction MUST NOT race a reconnect. Under the host mutex: the evictor re-checks `count == 0` AND
idle-elapsed immediately before tearing a city down; an `Acquire` (or `GetOrCreate`) that arrives
concurrently either (a) happens-before the evict decision (count > 0 → not evicted) or (b)
happens-after teardown completes and rebuilds cleanly via GetOrCreate. There must be NO window
where a connection is bound to a city that is being/has been torn down, and NO lost city (evicted
while a connection is live). Prove with a concurrent Acquire-vs-evict stress test, `-race -count=3`.
Rehydration correctness: an evicted-then-reconnected city rehydrates byte-identically from its
journal (digest matches pre-eviction) — leans on inc4's guarded rehydrate + inc1's restart test.

### AC-5 — no leaks, clean shutdown
The evictor goroutine is joined on `Close`. Evicting a city joins its 3 per-city goroutines
(inc1's `stop()`). No goroutine leak after a create→idle→evict→Close cycle (goroutine-count delta
check).

### AC-6 — default/backward-compat
No lifecycle hook installed (single-city metroserve, or any existing wsserver caller) → no
eviction, no refcount, behaviour byte-for-byte unchanged. GR#21: the evictor's scan over the city
map must be order-independent (no map-range-dependent behaviour); no time.Now in a determinism
path (the idle clock is wall-clock but only gates eviction, never sim state — document this).

## Gates (as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./internal/protocol/... ./cmd/metroserve/... -race -count=2`, FULL `go test ./...` (BUG-464 fixed — any cmd/metropolis flake is NEW), `golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- ADDITIVE: no change to inc2's TransportResolver signature or the no-hook wsserver path (byte-for-byte).
- Evict-vs-reconnect race safety is the correctness bar; no lost/torn city; `-race` clean.
- No goroutine leaks (evictor + per-city).
- Idle timeout/sweep are PLACEHOLDER balance numbers.
- Independent Destructive round (attacker ≠ author) before commit (GR#23): attack the evict-vs-
  reconnect race, the exactly-once onOpen/onClose pairing across every disconnect path, refcount
  underflow, rehydrate-after-evict correctness, and the no-hook backward-compat path.
