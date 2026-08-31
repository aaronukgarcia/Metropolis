---
mkey: FEAT-1972079936
title: "Compute Offload to Azure (Path A) — Phase 0: Protocol Versioning + Connect-Time Negotiation Handshake"
status: draft
author: BA (Bill lane)
date: 2026-08-31
relates:
  - FEAT-1972079936 (epic — Compute Offload to Azure, Path A)
  - FEAT-1972079852 (engine/protocol adapter — origin of the CURRENT refuse-on-mismatch handshake this phase supersedes)
  - INT-005 (the WebSocket JSON-RPC transport contract, int.protocol/wsserver)
  - docs/planning/compute-offload-architecture.md (epic architecture, section 3 point 3 "Version negotiation, not a single version number")
  - docs/design/protocol.md (ProtocolVersion / envelope versioning rules)
supersedes_dd: >
  Aaron DD on FEAT-1972079852 (2026-08-31, wsserver/server.go's own doc comment,
  lines 12-24): "a version mismatch at connect = REFUSE TO CONNECT." Aaron's
  NEW ruling (captured in the compute-offload architecture doc, section 3
  point 3): the client must NOT be forced to upgrade when the backend API
  changes — an older, in-window client connects and works. Only a client
  older than the server's supported window is refused.
---

# FEAT-1972079936 Phase 0 — Protocol Versioning + Connect-Time Negotiation Handshake

## Overview

Today `internal/protocol` carries exactly **one** wire version: the constant
`protocol.ProtocolVersion = "1.0"` (`internal/protocol/envelope.go:16`), and
`internal/protocol/wsserver/server.go`'s `handshake()` performs a **binary**
check — the client's `clientVersion` string (normalized to strip a volatile
`-dirty` suffix, `normalizeVersion`, `server.go:186`) must **exactly equal**
the server's own `engineVersion` (the git-describe build string, GR#2), or
the connection is refused with `MET-P010` (`ErrHandshakeVersionMismatch`) and
closed. There is no concept of "close enough," no capability exchange, and no
distinction between a wire-breaking change and an additive one — every build
difference, however small, is a full refusal. The TS mirror
(`webconsole/src/sim/wire.ts`'s `normalizeProtocolVersion`,
`webconsole/src/sim/protocolClient.ts`'s handshake send/receive) enforces the
identical binary rule client-side.

This is exactly the "confident wrong data" prevention GR#1 wants for a
**single-binary, single-commit dev loopback** (metroserve and the webconsole
built from the same tree, same commit, same second) — but it is the wrong
shape for the compute-offload target topology, where the server is a
long-lived Azure-hosted process and clients are browser tabs that refresh
on their own schedule, potentially days behind the server's latest deploy.
Forcing every client to match the server's exact build string means **every
server deploy kicks every open tab off** — unacceptable for the offload
epic's premise.

**Phase 0 replaces the single build-string equality check with:**
1. An explicit **semver'd protocol version** (`major.minor`), distinct from
   the engine's own git-describe build string — the wire schema's version,
   not the binary's version.
2. A **connect-time negotiation handshake**: client declares its supported
   protocol version(s) + a capability set; server replies with the highest
   version both sides support (within the server's supported window) and the
   intersected capability set.
3. A **version window** on the server: it simultaneously serves `current`
   down to `current - N` minor versions via compat shims, not just the exact
   latest.
4. **Graceful downgrade, not refuse-on-mismatch**, for anything inside the
   window. Refusal is reserved for a client strictly below the window floor,
   with a clear, actionable error.
5. **No change to determinism or journal semantics** — this phase touches
   wire framing and capability negotiation only; the engine's journal/replay
   machinery (GR#27, hard-reset-replay FEAT-1972079897) is untouched.

This document scopes **Phase 0 only** (the epic's phased plan, architecture
doc section 5). Phases 1-4 (durable persistence, multi-session hosting,
engine convergence, Azure deploy) are separately BOW-tracked and out of scope
here.

---

## Current-state findings (read before building)

- **No semver concept exists today.** `protocol.ProtocolVersion` is a single
  string constant `"1.0"` with no parsed major/minor and no comparison
  operator beyond Go `!=` (`envelope.go:112-115`, `Command.Validate`). There
  is no version-window, no capability list, and no server-side registry of
  "versions I still support." This phase adds all of it from a genuine zero
  baseline — nothing to "extend," a real new mechanism to build.
- **The handshake message shape already exists and is the right place to
  extend, not replace.** `wsserver/server.go`'s `handshakeParams{ClientVersion
  string}` / `handshakeResult{Accepted bool, ServerVersion string}`
  (`server.go:100-109`), framed inside the existing JSON-RPC 2.0
  `rpcMessage` envelope via `methodHandshake` (`server.go:70`), is the
  natural home for new fields — this phase is additive to that struct pair,
  not a new message type.
- **`protocol.ProtocolVersion` (envelope.go) is a DIFFERENT version from the
  handshake's `ClientVersion`/`ServerVersion` (wsserver).** Today they are
  accidentally the same string family (both ultimately derived from
  `buildinfo.Version`, git-describe) but they answer different questions:
  `envelope.ProtocolVersion` gates a `Command`'s own envelope shape
  (`Command.Validate`, checked per-command, every command), while the
  handshake's version today gates the whole *connection*. Phase 0 must
  **decouple these deliberately**: the wire-protocol semver (new) governs
  the handshake and the envelope's `protocolVersion` field it already
  carries; the engine's build/git-describe string stays a separate,
  informational field (useful for diagnostics, never for the accept/refuse
  decision).
- **`docs/design/protocol.md` (lines 73-138) already documents an
  extension rule**: "never bump `ProtocolVersion` [for an additive change] —
  add a new Kind (`AdvanceTicksV2`), not a silent redefinition." That
  existing convention is exactly "minor bump = additive" in spirit; Phase 0
  formalizes it with an actual major/minor pair instead of one opaque
  string.
- **TS side mirrors the Go side field-for-field, deliberately, with no
  shared import** (`wire.ts`'s own doc comment, lines 1-8: "NEVER an import
  of Go source... each side keeps its own copy"). Every new wire field this
  phase adds to the Go handshake structs needs a matching, independently
  written mirror in `wire.ts`, exactly like `HandshakeParams`/
  `HandshakeResult` already are.
- **Error codes MET-P010/011/012 are reserved for handshake failure
  classes** (`internal/protocol/codes.go:54-72`, registered in
  `data/errors.json`); MET-P013 is already reserved as a comment
  ("reserved for the webconsole (TS) protocol client's own..." —
  `codes.go:74`) and MET-P014-018 are taken by post-handshake command
  errors. New codes for this phase (below-floor refusal, capability
  mismatch, malformed negotiation payload) must be claimed via
  `tools/plan/add-error.js` (claim-range → add → check), never hand-picked
  numbers, and must NOT reuse MET-P010 for the new "below-floor" refusal —
  that code's registered meaning is "exact string mismatch," which no
  longer describes the new failure mode.

---

## Definitions this phase establishes

- **Wire protocol version** — a new `major.minor` pair (e.g. `1.0`, `1.1`,
  `2.0`), independent of the engine's git-describe build string. Lives
  alongside `protocol.ProtocolVersion` in `internal/protocol` (the SSOT for
  what a `Command`'s envelope declares) but is now parsed/compared as
  `(major, minor)`, not a bare string equality.
  - **Major bump** — a breaking wire or semantic change: a field removed or
    repurposed, a `Kind`'s payload shape changed incompatibly, a message
    framing change, or a change to what an existing command means once
    accepted. A client whose major differs from every version in the
    server's window cannot be served *at all* on that connection.
  - **Minor bump** — additive and back-compatible: a new field an older
    client simply doesn't send/read, a new `Kind` an older client never
    issues, a new capability. An older-minor client keeps working
    unmodified; it just doesn't get the new capability.
- **Capability set** — a set of string tokens (e.g. `"delta.compaction"`,
  `"command.batchAdvance"`) the client declares it understands and the
  server declares it can serve. The **negotiated** set is the intersection.
  This phase does not need to invent real capability tokens yet (Phase 0 has
  no new features gated behind one) — it needs the **mechanism**: the field
  in the handshake payload, the intersection logic, and a documented
  convention (coarse per-feature-area flags, per the open question below)
  future phases populate.
- **Version window** — the server's config: `current` (the highest version
  it speaks) and `N` (how many minor/major steps back it still serves via
  compat shims). Represented server-side as a small ordered list of
  supported versions with an associated shim per past version.
- **Compat shim** — a small adapter, keyed by the negotiated version, that
  translates an old-version request into the current internal call and/or
  translates the current internal response back into the old version's
  expected shape, so an in-window older client is served correctly without
  the rest of the server needing per-version branches scattered through it.

---

## AC-1: Protocol version is semver'd (major.minor), decoupled from the build string

**Check:** `internal/protocol` exposes a new versioned type (e.g.
`protocol.WireVersion{Major, Minor int}`) with a defined "current" value and
a comparison/parse function, distinct from `buildinfo.Version` /
`engineVersion`. The handshake's `ServerVersion` field (or a renamed/added
field) carries this wire version, not (only) the git-describe string.
`Command.Validate`'s existing `protocolVersion` check is re-expressed in
terms of this type (accepting the current major, at minimum the current
minor — see AC-3 for window behaviour).

**Mutation:** Bump only the minor component of the current wire version in
a test fixture and re-run `Command.Validate` against an envelope stamped
with the *old* minor — it must still validate (additive change, no
refusal). Bump the *major* component and re-run — it must now fail
validation (breaking change, refused) unless the connection negotiated that
old major explicitly via a shim (AC-3).

**False-pass guard:** A test that only checks the current version accepts
itself proves nothing about decoupling — the mutation must independently
flip major vs. minor and observe DIFFERENT outcomes (minor tolerant, major
not) on the SAME test harness, not two separately-written tests that could
each trivially pass.

---

## AC-2: Connect-time negotiation handshake replaces bare version-string equality

**Check:** The handshake request payload (`handshakeParams`, `server.go:101`
today) gains a client-supported-version-range field (e.g.
`clientMinVersion`/`clientMaxVersion`, or a list of concrete versions the
client speaks) and a `capabilities []string` field, alongside the existing
`clientVersion` (kept for diagnostics/build identification, decoupled per
AC-1). The handshake response (`handshakeResult`, `server.go:106`) gains a
`negotiatedVersion` field (the highest version both client and server's
window support) and a `capabilities []string` field (the intersection),
alongside the existing `accepted`/`serverVersion`. `webconsole/src/sim/
wire.ts`'s `HandshakeParams`/`HandshakeResult` interfaces are updated in
lockstep (independently written, no shared import, per that file's own
convention) and `protocolClient.ts`'s handshake send/receive path uses the
new fields.

**Mutation:** Connect a test client declaring a version one minor behind
current with an empty capability set. Assert the server's response carries
`negotiatedVersion` equal to the CLIENT's declared version (not silently the
server's own latest), and `capabilities` is the empty intersection — proving
negotiation actually picks the lower of the two, not just echoing the
server's own maximum regardless of what the client asked for.

**False-pass guard:** A test that only sends a client already AT the
server's current version cannot distinguish "negotiates" from "always
returns server's own version" — the mutation must use a client below
current and confirm the response reflects the CLIENT's ceiling, not the
server's.

---

## AC-3: Server supports a configurable version window (current + N back) via compat shims

**Check:** `wsserver.New` (or a new constructor option) accepts a
version-window configuration (current version + `N`, `N` a plain int,
default from a named constant e.g. `DefaultVersionWindowDepth`). The server
maintains a small per-version shim registry. A real request from a client
negotiated onto an in-window-but-not-current version is routed through that
version's shim and produces a correct, version-appropriate response.

**Mutation:** Configure the window with `N=1`. Connect three clients: (a)
one at `current`, (b) one at `current-1` minor, (c) one at `current-2`
minor. Send the SAME real command (e.g. `AdvanceTicks`) from all three.
Assert (a) and (b) both connect and get correctly-formed, version-appropriate
`CommandResult`s; (c) is refused (AC-4). This proves the window boundary is
enforced at `N`, not merely "anything not exactly current is refused" (the
old behaviour) nor "everything is silently accepted regardless of window."

**False-pass guard:** Testing only the exact-current and the below-floor
cases (skipping the in-window-but-not-current case) would pass under BOTH
the old refuse-on-mismatch behaviour with a permissive bug AND the intended
new behaviour — the in-window-not-current case is the one that actually
discriminates between them and must be explicitly asserted.

---

## AC-4: Below-floor client is refused with a clear, actionable error; in-window client is never refused

**Check:** A client whose declared version is older than `current - N`
(the window floor) gets a typed refusal — a NEW registry error code (NOT a
reuse of `MET-P010`, whose registered meaning is the old exact-mismatch
refusal; claim a new code via `tools/plan/add-error.js`) carrying the
client's version, the server's current version, and the window floor, with
a display message actionable enough to tell the user/developer "upgrade
required, minimum supported version is X." A client within the window,
including the floor version itself, is NEVER refused on version grounds.

**Mutation:** Connect a client at exactly `current - N` (the floor) and
assert it is accepted (boundary-inclusive, not off-by-one excluded). Connect
one at `current - N - 1` and assert refusal with the new code and a
non-empty, field-populated error context (not a generic string). Flip the
boundary check's `<` to `<=` (or vice versa) in a mutation pass and confirm
at least one of these two assertions fails — proving the test actually
pins the boundary rather than merely checking "some refusal happens
somewhere."

**False-pass guard:** Do not assert only "refused" / "not refused" as
booleans — assert the specific error CODE returned on refusal differs from
every other handshake failure code (mirrors `server_test.go`'s existing
`TestHandshake_...` pattern of checking `resp.Error.Code` against a named
constant, e.g. its round-r1 regression test asserting distinct codes never
collide, `server_test.go:435-448`).

---

## AC-5: Capability negotiation gives a client only the features both sides support

**Check:** When client and server capability sets differ, the negotiated
set is the set intersection (not the union, not either side's set alone).
The client-side (`protocolClient.ts`) receives the negotiated capability
set from the handshake response and callers can query it (e.g. a
`hasCapability(name)` helper) before using a feature gated behind one.

**Mutation:** Server declares capabilities `{A, B, C}`; client declares
`{B, C, D}`. Assert the negotiated set is exactly `{B, C}` — neither `A`
(server-only) nor `D` (client-only) appears. Then have the client attempt
to use a hypothetical feature gated on `A` and assert the client-side gate
refuses locally (does not even send the command) rather than sending it and
relying on the server to reject it after the fact.

**False-pass guard:** A test using identical capability sets on both sides
cannot distinguish intersection from union from either side's raw set —
the mutation must use sets that differ in both directions (each side having
something unique) to make the three possible (wrong) implementations
produce three different, distinguishable results.

---

## AC-6: Determinism and journal semantics are untouched by protocol version or negotiation

**Check (invariant, not a one-off test):** The protocol/wire version and
capability negotiation govern ONLY framing and feature availability over
the connection. They must never be read by, or influence, the engine's
simulation logic, its action journal (GR#27, `hard-reset-replay`
FEAT-1972079897), tick advancement, or replay determinism. Concretely:
`core.Engine`'s command processing and `protocol.InProcTransport` (the
actual simulation-facing transport `wsserver.Server` wraps, per its own doc
comment lines 1-11) receive fully-decoded `Command`/`CommandResult`/
`Delta` values with no version tag threaded through into engine-internal
code paths — the negotiated version is consumed and "unwrapped away" at the
`wsserver` shim boundary, never passed further in.

**Mutation:** Run the SAME journal of commands through two connections
negotiated at different (but both in-window) versions and assert the
resulting engine state (tick, funds, population, building counts — the
comparison spine already used elsewhere in this codebase, e.g.
`docs/planning/acceptance/bug-437-prewipe-quota.md`'s spine) is bit-for-bit
identical regardless of negotiated version. A shim that reformats the WIRE
shape may differ observably in the JSON on the socket; the DECODED
`Command`/resulting sim state must not.

**False-pass guard:** Only comparing the wire JSON (which the shims are
SUPPOSED to make differ) would falsely fail; only comparing whether a
result was `Accepted` (too coarse — a shim could route to the wrong
internal command and still get accepted) is insufficient — assert the
actual post-tick engine state spine matches exactly.

---

## AC-7: GR#25 edge audit — no unregistered new cross-module dependency

**Audit performed (BA, this document):** Phase 0's mechanism is scoped
entirely WITHIN `int.protocol` and its `wsserver` sub-package:
- A new version-window/shim registry lives inside `internal/protocol/
  wsserver` (or a new file under `internal/protocol` itself) — an
  internal structure of an already-registered module (`int.protocol`,
  `code.json` key `int.protocol`), not a new outbound edge to another
  module.
- The handshake struct field additions (AC-2) are wire-shape changes to an
  existing message this module already owns end-to-end — no new caller,
  no new callee.
- Nothing in this phase requires `wsserver` to call into `engine.core`,
  `engine.finance`, or any other simulation module beyond what it already
  calls (`transport.SendCommand`/`Results()`/`Events()`/`Deltas()`, already
  registered edges per `server.go`'s existing imports).

**Result: NO new cross-module edge required.** If a future increment
(e.g. a shim needing to query something engine-side it doesn't already
have access to) discovers a genuine new edge, that increment's author must
stop and coordinate with the Architect to register it in `code.json`
BEFORE writing the acceptance prose for that increment (GR#25) — this
document does not pre-authorize any such edge, and none is claimed here.
`claude-spec-guard.js` should be run against this document before commit
to confirm mechanically.

---

## Increments

### Increment 1 — Version field + handshake shape + single-version echo
Add the semver `WireVersion` type (AC-1) and extend the handshake
request/response shape with the new fields (AC-2), but the server still
only actually SERVES its current version (window depth effectively 0/1) —
this increment proves the new shape round-trips correctly end-to-end
(Go <-> TS) without yet implementing window/shim logic. Covers AC-1, AC-2
(negotiation logic exists and is exercised, even though the window is
trivial), and AC-6 (determinism invariant holds from day one — cheap to
prove now, expensive to retrofit later).

### Increment 2 — Version window + one older-version shim (graceful downgrade proof)
Implement the window config (`N` configurable, default TBD pending Aaron's
answer to the open question below) and build exactly ONE real compat shim
for one deliberately-introduced older minor version, proving a real
request/response round-trips correctly on that older version while a
current-version client is served unshimmed. Covers AC-3 in full, and half
of AC-4 (the in-window-connects half).

### Increment 3 — Capability negotiation + below-floor refusal
Add the capability set exchange and intersection logic (AC-5) and the new
below-floor refusal error code + refusal path (the other half of AC-4).
This is the increment that actually replaces the old `MET-P010`
refuse-on-any-mismatch behaviour with graceful-downgrade-except-below-floor
— it should also update/retire the now-superseded parts of
`server_test.go`'s `TestHandshake_VersionMismatch_Refuses` family (rename
or replace with the below-floor and in-window-accepts cases; do not simply
delete coverage — GR#12 dependency/completeness discipline) and
`wsserver/server.go`'s own doc comment (lines 12-24), which currently
documents the SUPERSEDED "REFUSAL, never a silent degrade" rule verbatim
and must be corrected to describe the window/negotiation behaviour, citing
this document and Aaron's superseding DD.

---

## Open questions for Aaron

1. **Version-window depth (N).** How many minor versions back, and/or how
   many major versions back (if any — a major bump may reasonably mean "no
   window, only the negotiated major can be current"), must the server
   simultaneously serve? Is this a fixed count or a time-based deprecation
   window (e.g. "supported for 90 days after superseding")?
2. **Deprecation schedule/policy.** When a version falls out of the window,
   what is the client-facing signal BEFORE that happens (a deprecation
   warning in the handshake response while still in-window, a lead time
   guarantee) versus the hard cutoff this document's AC-4 covers?
3. **Capability granularity.** Should capabilities be coarse (one flag per
   feature AREA, e.g. `"finance.v2"`) or fine-grained (one flag per
   individual new field/behaviour)? Coarse is simpler to reason about and
   matches this codebase's existing view-subscription granularity
   (`f2.finance` etc.); fine-grained gives more precise interop at higher
   bookkeeping cost. This document assumes coarse pending Aaron's call.
4. **Mapping to INT-005.** Does INT-005 (the registered wsserver transport
   contract) need its own version bump/amendment to reflect the new
   handshake shape, or is this treated as an additive, in-place evolution
   of the existing contract (consistent with the "minor bump" definition
   this document establishes)? The BA's reading is the latter (additive),
   but this is an Architect-owned call, not a BA one.
5. **Where the engine build string (git-describe) still matters.** Now
   that the wire version is decoupled from the build string (AC-1), should
   the build string still be surfaced anywhere client-visible (diagnostics
   panel, F12) for support purposes, or dropped from the handshake
   entirely as no-longer-load-bearing?
