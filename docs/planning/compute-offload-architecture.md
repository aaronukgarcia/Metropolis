# Compute Offload to Azure — Architecture & Phased Plan (Path A)

**Status:** DRAFT for Aaron review. **Decided:** Path A (converge on the Go engine; webconsole becomes a thin client). **Date:** 2026-08-31. **Northstar:** realises waypoint 3 (engine convergence — "the Go engine is the product").

**Why now:** the dogfood keeps hitting *browser* ceilings — the 5 MB localStorage quota, a 2.5 GB tab OOM on replay, UI slowdown at 700k citizens. These are all symptoms of running a large deterministic simulation inside a browser tab. Offloading compute is the permanent cure.

---

## 1. What already exists (the seam is built)

- `cmd/metroserve` + `internal/protocol/wsserver` — a WebSocket JSON-RPC server (INT-005 transport).
- FEAT-1972079852 adapter (inc1 landed): the webconsole store boundary can speak the real UI/engine protocol; `LiveEngineBadge` proves the wire end-to-end; the **engine owns the journal** (Aaron DD); mock sim stays as the offline fallback.
- The engine already runs **out-of-process behind a socket**. That is step one of offload, done.

## 2. Aaron's target topology (captured)

```
        clients (browsers, thin)
                 │  WSS (versioned JSON-RPC)
        ┌────────▼─────────┐
        │  API Gateway     │  (version routing, auth, TLS termination)
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │   NLB (sticky)   │  session affinity → same engine instance
        └────────┬─────────┘
        ┌────┬────┴───┬─────────┐
     ┌──▼─┐ ┌─▼──┐  ┌─▼──┐   engine instances (N)
     │eng │ │eng │  │eng │   each hosts M independent city sessions
     └──┬─┘ └─┬──┘  └─┬──┘
        └──────┴───────┴──── durable store (journal + snapshots)
```

- **Sticky sessions:** a player is pinned to the engine instance holding their live in-memory city, so a long-lived WS connection stays on one backend.
- **Elastic sharing:** 10 players can share one engine instance, or scale out to 10 instances — capacity is a tuning knob, not a fixed 1:1.

## 3. Architect input ("open to other ideas")

Three additions the determinism buys us — worth baking in from the start:

1. **Sticky = performance, not correctness.** Because the engine is deterministic and *owns the journal*, ANY instance can rebuild ANY city by replaying its journal from genesis (+ nearest snapshot). So session affinity is an optimisation to keep hot state resident on one node — **not** a hard requirement. Failover becomes "rehydrate the city on another instance from the durable journal," which the NLB can do transparently. This makes the system far more robust than a typical stateful-session design.

2. **Durable journal + snapshots as the state tier** (Azure Blob/Table, or a DB). The authoritative persisted state is the action journal (already the engine's SSOT) plus periodic snapshots to bound replay cost — the *same* journal + snapshot + hard-reset-replay machinery already in the codebase (FEAT-1972079897/…854), just pointed at durable server storage instead of localStorage. **This also permanently kills the localStorage quota problem** — saves live server-side.

3. **Version negotiation, not a single version number.** Your "client isn't forced to upgrade" requirement is best served by a **semver'd protocol with a connect-time handshake**: client declares its supported version + capabilities; the server supports a *window* of versions (e.g. current + 2 back) and speaks the highest both share; a capabilities exchange lets a client use only features both sides have. This **refines an existing DD**: FEAT-1972079852 today says "version mismatch at connect = REFUSE TO CONNECT." Your requirement changes that to **graceful multi-version support** — the server maintains compat shims for older protocol versions and deprecates on a published schedule. (Flagged as DD-change below.)

## 4. The long pole (the real cost of Path A)

**Engine convergence.** What you *play* today is the webconsole's **TS mock sim** — months of finance, demographics, roads, density, IMF, tourism logic live there. The authoritative **Go engine** must reach parity before a server-hosted engine is worth connecting to. This is incremental and already has the mechanism: the adapter flips the webconsole to the live engine **per-view** as each domain converges (finance view first, already wired). So convergence is a domain-by-domain march, not a big-bang rewrite — but it is the bulk of the work.

## 5. Phased plan

- **Phase 0 — Protocol versioning + handshake** (Go side, startable now, no convergence needed). Semver the JSON-RPC protocol; add the connect-time version+capabilities negotiation; server supports a version window; replace the refuse-on-mismatch path with graceful downgrade. *Deliverable: a client 1 minor behind still connects and works.*
- **Phase 1 — Durable server-side persistence.** Journal + snapshot store behind an interface (local disk first, Azure Blob later); a city rehydrates on any instance from the store. *Deliverable: kill localStorage — saves are server-side; a restarted instance restores a city.*
- **Phase 2 — Multi-session engine host + session routing.** One engine process hosts M independent cities; a session→instance registry; sticky routing; failover-by-replay. *Deliverable: N players across M instances, a killed instance recovers its cities elsewhere.*
- **Phase 3 — Engine convergence (the march).** Domain-by-domain Go parity with the TS dogfood, flipping each webconsole view to live as it lands. *Deliverable: the browser runs zero simulation — pure thin client.*
- **Phase 4 — Azure deploy + gateway/NLB + auth/TLS.** Host metroserve on Azure behind the gateway + NLB; identity/auth at the gateway; TLS; observability. *Deliverable: production offload.*

Phases 0–2 are **independent of convergence** and each delivers standalone value (versioning, server persistence killing the quota problem, scaling). Phase 3 is the long march; Phase 4 is the deploy. They can overlap.

## 6. Open questions for Aaron

1. **Multi-tenant single-player, or shared-world multiplayer?** My reading of "10 players share one engine" = 10 **independent single-player cities** co-hosted in one process (multi-tenancy), NOT a shared world. Shared-world multiplayer is a fundamentally harder design (concurrent authority, conflict, one journal per shared city). **Confirm: independent cities per process?** (I've planned for that.)
2. **Version window depth** — how many past protocol versions must the server support at once (current + 1? + 2? a time-based deprecation window)?
3. **Auth/identity model** — how do players authenticate and own a city (account system, anonymous device token, existing identity)?
4. **Hosting specifics** — Azure Container Apps / AKS / VMSS for the engine instances; which gateway (API Management vs Application Gateway); NLB affinity mechanism (source-IP vs app-token).
5. **Convergence priority order** — which domain converges first after finance (demographics? roads? the economy spine?), to sequence Phase 3.

## 7. Immediate next step

If this framing is right, Phase 0 (protocol versioning + handshake) is the clean starting point — it's Go-side, needs no convergence, and directly delivers your headline requirement (clients not forced to upgrade). I'd scope Phase 0 into BA acceptance criteria next.
