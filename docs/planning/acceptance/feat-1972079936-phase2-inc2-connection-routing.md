# FEAT-1972079936 Phase 2 inc2 — connection→city routing (handshake-selected)

**Epic:** FEAT-1972079936. Phase 2 inc1 landed the `CityHost` registry (`097f6f0`). **inc2**
(this) routes each incoming WS connection to its target city: the client names a city in the
connect handshake, and `wsserver` binds that connection to `host.GetOrCreate(cityKey).Transport`.
**Additive + backward-compatible** — Go-only, no webconsole client change required (an absent
cityId → a "default" city, so today's single-city client keeps working unchanged).

## Context (reuse, committed)
- `internal/protocol/wsserver/server.go`: `Server` wraps ONE `transport protocol.Transport`
  (line ~196); the handshake params struct (~line 140-163) carries `ClientMinVersion`/
  `ClientMaxVersion`/`Capabilities`; `New(transport, engineVersion, handshakeTimeout, ...Option)`.
- `cmd/metroserve/cityhost.go`: `CityHost.GetOrCreate(ctx, persist.CityKey) (*runningCity, error)`;
  `runningCity` exposes its `Transport` (add an accessor if not already exported).
- Phase 0's version handshake is the extension point (this is exactly why the handshake exists).

## Design (authoritative) — ADDITIVE, backward-compatible

### AC-1 — handshake carries an optional city
Add to the client handshake params struct (server.go) an optional `CityID string json:"cityId,omitempty"`
(and `TenantID string json:"tenantId,omitempty"` — defaults to the "local" placeholder when
absent, matching inc1/inc4). Absent/empty cityId → the documented default city id ("default").
An old client that never sends the field is indistinguishable from one asking for "default" — and
gets the default city, preserving today's behaviour exactly. Update the TS mirror types ONLY as
type definitions if trivially needed for compilation, but do NOT wire the webconsole client to
send a city in this increment (that's a separate deferred change — the server must work with
today's client sending no cityId).

### AC-2 — a transport resolver on the Server (additive)
Add an OPTION `WithCityHost(host *cityhost.CityHost)` (or a `WithTransportResolver(func(persist.CityKey) (protocol.Transport, error))`) that installs a per-connection transport resolver. When NO resolver is installed, the Server behaves EXACTLY as today (its single `transport` field serves every connection — every existing test and caller unaffected, byte-for-byte). When a resolver IS installed, the single `transport` field is unused and the connection's transport is resolved during the handshake.
- NOTE the import direction: `wsserver` is `internal/protocol/` (interface layer) and `cityhost` is in `cmd/metroserve` (a main package) — `internal/protocol` CANNOT import a `cmd/` package. Therefore the resolver MUST be a plain `func(persist.CityKey) (protocol.Transport, error)` (or an interface declared in wsserver), and `cmd/metroserve` supplies a closure over its `CityHost`. Do NOT make wsserver import cityhost. If `persist.CityKey` in the signature creates an unwanted `internal/protocol → internal/persist` edge, declare a tiny local key type/interface in wsserver instead and have metroserve adapt — flag which you chose and why. Keep wsserver's dependency surface minimal (register any genuinely new GR#25 edge, or avoid it by using a locally-declared key type).

### AC-3 — resolve during handshake, bind the connection
In the handshake path, after version/capability negotiation succeeds and a resolver is installed:
build the CityKey from the handshake's tenant/city (defaults applied), call the resolver, and bind
THIS connection's command-send + subscription-pump to the resolved transport for the rest of the
connection's life. A resolver error (e.g. corrupt-journal city = fatal per inc4) → the handshake
is REFUSED with a clear error (a new MET-P code or an existing suitable one), never served against
a fallback city. The negotiated version never changes mid-session (existing invariant) — nor does
the bound city.

### AC-4 — two clients, two cities, one server
The acceptance bar: a single `wsserver` (resolver = a real `CityHost`) serving TWO connections
that name DIFFERENT cities routes each to its own engine — commands on connection A affect only
city A's state, and a subscription on B sees only B's deltas. Test at the server/handshake seam
(reuse the existing wsserver test harness that drives a handshake + a command over an in-proc
transport). Prove cross-routing isolation and that same-city connections share one engine.

### AC-5 — metroserve serves the host
`cmd/metroserve/main.go`: when persistence/multi-city is enabled, build a `CityHost` and pass its
resolver to `wsserver.New(..., WithCityHost/Resolver)`. The default single-city path (no host)
stays as today. The tickLoop/command-loop wiring moves under the CityHost's per-city lifecycle
(inc1 already runs per-city pump/loop/tick), so metroserve's top-level single-city tick driver is
replaced by the host's — OR, to keep inc2 minimal, metroserve pre-creates the "default" city via
GetOrCreate and serves the host, letting per-city lifecycle own the loops. Choose the smaller diff;
document it.

### AC-6 — backward-compat + determinism
- No resolver installed → every existing wsserver test passes unchanged (the single-transport path
  is untouched). Assert this explicitly.
- An old client (no cityId) against a resolver-equipped server → routed to "default", served
  normally.
- GR#21: no map-range/time.Now nondeterminism in the routing path.

## Out of scope (later)
- The webconsole CLIENT sending its cityId (separate small change; touches the live-dogfood
  webconsole — do NOT do it here).
- Idle-city eviction / capacity limits (inc3).
- Cross-process failover + sticky routing + NLB (inc3 + Phase 4).

## Gates (as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./internal/protocol/... ./cmd/metroserve/... -race -count=2`, FULL `go test ./...` (webconsole node-test must also stay green if any TS type changed), `golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- Fully backward-compatible: no resolver → today's exact behaviour; no webconsole client change.
- `internal/protocol/wsserver` must NOT import the `cmd/metroserve` package (import-direction). Use a func/interface resolver.
- Keep wsserver's new dependency surface minimal; register any genuinely new GR#25 edge (or avoid it with a locally-declared key type) — flag the choice.
- Independent Destructive round (attacker ≠ author) before commit (GR#23): attack cross-city routing isolation, the default-city fallback, a resolver error refusing cleanly (not falling back), the no-resolver backward-compat path, and per-connection transport binding under concurrent connections.
