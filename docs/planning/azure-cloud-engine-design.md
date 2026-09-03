# Azure Cloud Engine — Design Document

**Status:** DRAFT for Aaron. **Docs-only** — no code, no commit, nothing deployed.
**Date:** 2026-09-03. **Author:** Bev (lead).
**Commissioned by:** Aaron, 2026-09-03 — *"I want engines up, and maybe the NLB API gateway, and maybe some redis cache if needed. Let's go minimal small to start with and smoke test, then increment, ensuring we keep low latency and the background threads are offloaded to the cloud. … describe the data store for buildings and people — are we using a database or tiny files?"*

**Parent:** `FEAT-1972079936` — *EPIC: Compute offload to Azure (Path A)*. This document **concretises Phase 4** (Azure deploy + gateway/NLB + auth/TLS) and answers open question 4 of `docs/planning/compute-offload-architecture.md` ("hosting specifics"). That document is the strategy; this one is the deployment. **No Phase 4 acceptance document existed before this one.**

**Related, and read before writing this:** `docs/cloud.md` (provider decision), `docs/planning/phase3-convergence-plan.md` (the tolerance contract — §5.2), `docs/planning/acceptance/FEAT-2326609764.md` (the 100M spatial-partition spec — §2), `MOD-069` (Azure tiers).

**Linked from:** `A2Bev001.md` v22, questions **Q100138–Q100152**.
**BOW item carrying the increment plan:** **`FEAT-2326609775`** (GUID `81834f57-ad8a-4b13-b86f-c8e3c11c246c`).

---

## 0. The answer in one page

| Aaron's ask | The recommendation | Why |
|---|---|---|
| **"engines up"** | `cmd/metroserve` in a container on **Azure Container Apps**, `uksouth`, scale-to-zero, **max 1 replica per city**. | It is already an HTTP+WebSocket server with a configurable bind address. Container Apps gives free TLS, a stable WSS FQDN, an L7 load balancer, session affinity and scale-to-zero in **one** resource. **Aaron already has a Container Apps environment and a registry in this subscription** (§1.5) — inc1 reuses them. |
| **"maybe the NLB API gateway"** | **Defer.** Container Apps ingress *already is* the L7 load balancer, TLS terminator and sticky router. | **Criterion that reverts this (§3.4):** a second backend behind one origin, a WAF, multi-region, or a custom domain with caching. None are true at inc1. |
| **"maybe some redis cache if needed"** | **Not needed. Do not deploy it.** | **Signal that would justify it (§3.5):** two replicas must share one city's hot state, or p95 blob-read on a hot path exceeds 150 ms. Today the engine is **structurally limited to one replica per city** (§1.4) and its hot state is in-process Go memory — faster than Redis, and £0. |
| **"database or tiny files?"** | **Neither. Azure Blob Storage, chunked at a boundary the engine already has, plus an append-only journal.** | Derived in §4. A database costs 10×–1000× more for a workload that never queries and always full-scans. "Tiny files" costs 49,174 → 100,000,000 blob operations per save. The right granularity is ~256–1,500 *medium* blobs. |
| **"background threads offloaded"** | Yes — **but the tick is not a background thread.** §3.1 draws the line. | §6's arithmetic permits a cloud round-trip per *tick* but forbids one per *frame* — and the shipped turbo speed (160 ms/tick) is far tighter than Aaron's stated 333 ms. |
| **"keep low latency"** | Hard rule: **nothing on the frame path or the input→feedback path may await the cloud.** | §6, proven by the inc1 smoke test measuring p95 from Aaron's actual browser. |
| **"minimal small to start, smoke test, then increment"** | **inc1 = one container, one `/health` endpoint, one measured round-trip. ~£2/month, ~1 day.** | §7. The webconsole's live-engine client **already exists and is flag-gated off** (§1.6), so inc1 needs almost no client code. |

**Three things need Aaron before the plan is safe:**
1. **Q100138** — what the cloud engine is *for*. Three of our own decisions currently contradict each other (§2).
2. **Q100145** — the handshake **refuses any client whose build string differs from the server's** (§1.3). Every cloud deploy therefore locks out every open browser tab. This is a deployment-shaped problem, not a bug.
3. **Q100147** — the shipped turbo speed is **6.25 game-days/second**, not the 3/s Aaron stated. §6's budget depends on which is real.

---

## 1. What already exists — verified on disk at `88f9bce`, 2026-09-03

Everything here was read, not remembered. Estimates are labelled.

### 1.1 The engine host is already a network server

`cmd/metroserve/main.go`:

| Flag | Default | Note |
|---|---|---|
| `-addr` | `localhost:9999` | **Binds loopback.** A container passes `-addr 0.0.0.0:8080`. The flag already accepts it — **no code change needed.** |
| `-seed` | `1` | World seed. |
| `-tick-interval` | `250ms` | Wall-clock gap between single-tick `AdvanceTicks`. |
| `-persist-dir` | `""` (OFF) | Non-empty switches to the multi-city **`CityHost`** with connection→city routing (Phase 2 inc2). Empty is the legacy single-city path. |
| `-city` | `default` | City id. Tenant is the placeholder `"local"`. |
| `-snapshot-every` | `compose.SnapshotCadenceTicks` = **360** | One simulated year. `0` = off. Ignored without `-persist-dir`. |
| `-version` | | Prints `buildinfo.String()` and exits. |

It serves **exactly one route: `/ws`** — `mux.Handle("/ws", wsserver.New(transport, buildinfo.Version, DefaultHandshakeTimeout))`.

> **There is no `/health` endpoint anywhere in `cmd/` or `internal/`.** `/ws` cannot serve as a readiness probe because it demands a WebSocket upgrade *and* a version handshake. **This is the one genuine code addition inc1 needs.**

> **There is no `Dockerfile`, no `.bicep`, no `.tf`, and no Azure reference in `.github/workflows/`** (only `ci.yml`). Nothing has been deployed. Go is `1.25`.

### 1.2 Persistence is already abstracted behind a registered interface

`code.json` registers **`int.persist`** (module GUID `82897e55-1135-46e1-9b71-7d18c99a6b7a`, path `internal/persist/`):

- **Inbound contract `Store`** (GUID `087edd5a-…`): *"Go interface, opaque `[]byte` payloads"*; pattern *"per-`CityKey` journal append + snapshot put/get/list, cross-process rehydrate"*.
- **Registered consumers:** `feat.compositionroot` only. **Outbound calls: none.** Keyed by `persist.CityKey{TenantID, CityID}`.

**This is the most important architectural fact in this document:** an Azure Blob backend is a *new implementation of an already-registered inbound contract*. It introduces no new graph edge (§9).

*(Noted for the edge-lint backlog: the Phase 1 architect decision intended `engine.core`→`int.persist`, `engine.checkpoint`→`int.persist` and `cmd.metroserve`→`int.persist` to be registered; only `feat.compositionroot` appears. A GR#25 completeness gap, not a blocker for this design.)*

### 1.3 ⚠ The handshake enforces **build-string equality**, not just protocol semver

Phase 0 shipped semver negotiation, a 3-major version window and fine-grained capability flags — all correct. But `wsserver` **also** compares the client's engine build string to the server's via `normalizeVersion()`, and **refuses the connection with `MET-P010` on any mismatch.**

Consequences, stated plainly:

- A browser tab loaded from build `A` **cannot connect** to a cloud engine deployed at build `B`.
- **Every cloud deploy therefore invalidates every open client.** With GitHub Actions deploying on every green `main` (Aaron's Q100026), that is potentially several forced reloads a day.
- This is *not* what Aaron's *"client is not forced to upgrade"* requirement asked for. The semver window delivers that requirement; the build-string gate then overrides it.

It is defensible today (one user, one machine, determinism paranoia) and indefensible the moment the deploy pipeline is automatic. **Q100145.**

### 1.4 ⚠ Two structural limits that force **one replica per city**

1. **`internal/persist`'s local-disk store takes no file lock and holds no ownership lease.** Two `metroserve` processes pointed at the same `persist-dir` would interleave journal appends and corrupt the city. Nothing detects it.
2. **`wsserver` supports exactly one live WebSocket connection at a time per `Transport`.** A second client for the same city races the first.

Neither is a defect at today's scale — they are simply the boundary of what has been built. But together they mean **`max-replicas` must be 1**, and Aaron's *"10 players share one engine, or scale to 10"* elasticity needs an ownership lease (a blob lease is the natural fit) before horizontal scale is safe. This *also* independently kills the case for Redis (§3.5): there is nothing to share. **Q100146.**

*(Consistent with `MOD-069`'s 2026-08-09 ruling: **exactly one instance per stateful session**, or connection ping-pong.)*

### 1.5 ✅ Aaron already has an Azure estate — inc1 reuses it

Verified from `MOD-069` / `docs/cloud.md`:

| Resource | Value |
|---|---|
| Subscription | `8e1afaa3-1ce8-4269-9f57-71fdd88c70c3` |
| Resource group | `garcia` |
| Region | `uksouth` |
| Container Registry | `prixsixacr` |
| Container Apps environment | `prixsix-env` (scale-to-zero already in use) |
| Storage account | `garcialtdstorage` |

**The registry and the Container Apps environment already exist.** inc1 does not create them — it pushes one new image and creates one new Container App. That removes the largest fixed cost line (~£4/month ACR) *and* most of the setup.

**`MOD-069` AC-4 is explicit: Metropolis takes its OWN container app — it never reuses the existing `whatsapp-session` app.** Honoured in §7.

**Open choice:** reuse the shared `garcia` RG/`prixsix-env`, or stand up a clean `rg-metropolis-dev`. Reuse is cheaper and faster; a separate RG is deletable in one command, which is the real cost-runaway lever. **Q100140(b).**

### 1.6 ✅ The webconsole already has a live-engine client — flag-gated OFF

`webconsole/src/sim/protocolClient.ts` (~800 lines) is a complete WebSocket JSON-RPC client: handshake, command send, result/event/delta handling, reconnect.

- Enabled by `localStorage['metropolis.liveEngine']`.
- Endpoint from `localStorage['metropolis.liveEngineUrl']`, default `ws://localhost:9999/ws`.

**inc1's smoke test is therefore mostly a matter of pointing an existing, already-built client at an Azure URL.** This is what makes a one-day inc1 credible rather than optimistic.

### 1.7 The persistence write path — two facts that matter for cloud storage

- **`AppendJournal` calls `f.Sync()` on every record.** At `-tick-interval 250ms` that is 4 fsyncs/second/city. On a local SSD, free. **On an Azure Files SMB mount (a network filesystem, ~5–20 ms per fsync) it is 2–8 % duty cycle per city at normal speed, and worse at turbo or with several cities.** §7 makes measuring this an explicit inc1 gate.
- **Snapshots are a ZIP of the save bundle, and `RestoreLatestSnapshotOrGenesis` reads the *entire* journal into memory** — the journal is never pruned. Restore cost is therefore O(total history), permanently. Aaron already anticipated this (Phase 1 ruling: *"compaction/prefix-drop is DEFERRED… likely Phase 4 when real storage-cost pressure and the Azure Blob backend land"*). **Phase 4 is now, so journal compaction becomes a live inc4 item** (§7).

### 1.8 What has landed on the epic

**Phase 0 complete** (protocol semver, handshake, 3-major window, capability flags). **Phase 1 complete** (`int.persist` `Store`, local-disk impl, journal, snapshot cadence N=360, snapshot-aware restore, and inc3b's durable metroserve host, `a990ec9`). **Phase 2 complete** (multi-tenant `CityHost`, connection→city routing, failover-by-replay). **Phase 3 inc1+inc2 landed** — `internal/converge` A/B harness plus a real webconsole finance trajectory fixture, report-only. **Phase 4: not started — this document.**

### 1.9 Aaron's rulings already on the record (do not re-ask)

| Ref | Ruling |
|---|---|
| Q100024 | Use the **existing Azure subscription**, with a **hard spend cap set in Azure itself** — required, not optional. |
| Q100025 | Access is **private**: *"simple port knock to protect with a password"* — not full auth. |
| Q100026 | Deploy via **GitHub Actions on green `main`**; credentials as encrypted repo secrets (public repo noted and accepted). |
| Q100027 | Demo city: **10M citizens = goal, 20M = stretch.** |
| Q100028 | **Fresh start at cutover** — no journal-replay continuity for the first flip. |
| Q100100 | Normal 1 s = 1 day; **turbo 1 s = 3 days**. *(Shipped code says 6.25 — §6.1, Q100147.)* |
| Q100114 | Finish line = **100M people**. |
| 2026-08-31 | Topology: gateway + NLB with **sticky sessions** → engine instances hosting M cities. |
| 2026-08-31 | **Multi-tenant single-player** — independent cities co-hosted, *not* shared-world multiplayer. |
| 2026-08-31 | Convergence is **incremental, per-domain**; web stays local; each flip A/B-gated and reversible. |
| 2026-09-01 | Phase 4 (**a real Azure deployment**) is the explicit Baseline One goal-1 target. Timeline ASAP. |

---

## 2. The tension that must be resolved before increment 2

Three positions are on the record and they disagree:

1. **`FEAT-1972079936` (Aaron, 2026-08-31) — Path A:** converge on the Go engine; *"the browser runs zero simulation — pure thin client"*. Motivated by browser ceilings (5 MB localStorage, 2.5 GB tab OOM, slowdown at 700k citizens).
2. **`FEAT-2326609764` §11 rec 3 (Bev, 2026-09-03; Aaron has not yet ruled):** *"Do NOT move the heavy path to the Go engine… porting the derivations duplicates ~29 derivation functions across two languages, creates a byte-identity obligation across a language boundary, and buys a constant factor that density supplies for free."*
3. **Aaron, today:** *"background threads are offloaded to the cloud"* + *"keep low latency"*.

Position 3 is narrower than 1, compatible with 2, and the most recent. **This document builds position 3:**

> **At inc1–inc4 the cloud engine is a durable, verifying shadow — not a replacement for the browser sim.** It takes custody of state, replays the journal, proves parity domain-by-domain, and runs work that is genuinely background. The browser keeps the tick, because §6 says a round-trip is affordable per *tick* but not per *frame*, and because §11's cost of duplicating 29 derivations has not been paid and need not be paid to get value from the cloud.
>
> Position 1 stays the long-run destination and nothing here forecloses it. The per-domain A/B cutover Aaron already approved is precisely the mechanism that walks from 3 to 1, one reversible domain at a time.

**Q100138 asks Aaron to confirm this reading.** If he means position 1, inc2 onward change shape and §4's answer moves from the ~300 KB TS shape to the 7.5 GB Go shape.

---

## 3. (a) Topology — what runs where

### 3.1 The split

```
  ┌──────────────────────── BROWSER (Aaron's machine) ───────────────────────┐
  │  UI + renderer          ── owns the frame. Never awaits the cloud.       │
  │  TS sim (webconsole)    ── owns the TICK. Authoritative at inc1-4.       │
  │  Journal (append-only)  ── SSOT of what happened. Uploaded async/batched.│
  │  Local save (IndexedDB) ── stays, as the offline fallback.               │
  │  protocolClient.ts      ── ALREADY BUILT, flag-gated off (§1.6)          │
  └────────────────────────────────┬─────────────────────────────────────────┘
                                   │  WSS (versioned JSON-RPC, /ws)
                                   │  async, batched, never on the frame path
  ┌────────────────────────────────▼─────────────────────────────────────────┐
  │  AZURE CONTAINER APPS   (uksouth, env `prixsix-env`, scale-to-zero)      │
  │  ┌──────────────────────────────────────────────────────────────────┐    │
  │  │ ingress: HTTPS/WSS + TLS + L7 LB + session affinity  [BUILT IN]  │    │
  │  └────────────────────────────┬─────────────────────────────────────┘    │
  │  ┌────────────────────────────▼─────────────────────────────────────┐    │
  │  │ metroserve  (-addr 0.0.0.0:8080)   MAX 1 REPLICA PER CITY (§1.4) │    │
  │  │   • /health   ← NEW at inc1                                      │    │
  │  │   • /ws       ← exists                                           │    │
  │  │   • CityHost: M cities, CityKey{Tenant,City}                     │    │
  │  │   • BACKGROUND: journal replay, converge A/B, projections        │    │
  │  └────────────────────────────┬─────────────────────────────────────┘    │
  └────────────────────────────────┼─────────────────────────────────────────┘
                                   │  int.persist.Store  (already registered)
  ┌────────────────────────────────▼─────────────────────────────────────────┐
  │  AZURE BLOB STORAGE  (§4)                                                │
  │    <tenant>/<city>/manifest.json          latest tick, part index        │
  │    <tenant>/<city>/journal/NNNNNNNN.seg   append-only sealed segments    │
  │    <tenant>/<city>/snapshot/<tick>/…      manifest + chunked parts       │
  └──────────────────────────────────────────────────────────────────────────┘
```

**What is genuinely "a background thread", and therefore offloadable:**

| Work | Local cost today | Offload? | Increment |
|---|---|---|---|
| Reducer tick | **~34 ms** at 49,174 buildings (§6.2) | **No** — it *is* the game. | — |
| Display / derive pass | 111 ms post-BUG-642 | **No** — feeds the frame. | — |
| Input → placement feedback | must be < 100 ms | **No** — §6. | — |
| Durable save / snapshot custody | blocked by the 5 MB quota | **Yes** | **inc2** |
| Journal replay + converge A/B | not run continuously today | **Yes — best first candidate** | **inc3** |
| Consolidator plan (`FEAT-2326609761`) | not built; expect 100s of ms | **Yes** | inc3/4 |
| Projections, what-if / balance solves | not built | **Yes** | inc4 |
| Full Go-engine authoritative sim | — | **Only after per-domain A/B parity** | inc5+ |

### 3.2 "Engines up" as Azure resources — the compute choice

| Option | Verdict | Reasoning |
|---|---|---|
| **Azure Container Apps** | ✅ **Recommended** | One resource gives: HTTPS ingress with free managed TLS and a stable FQDN, an **L7 load balancer**, **built-in session affinity** (exactly Aaron's stated sticky topology), **scale-to-zero**, revision-based deploy with instant rollback, and volume mounts. Native WebSocket support. **It makes the gateway question disappear at inc1 rather than merely deferring it.** Aaron's environment already exists (§1.5), and `MOD-069` already chose this shape on cost (2026-08-09: scale-to-zero ≈ $4–10/mo vs $32 always-on). |
| App Service (Linux container) | ❌ | No scale-to-zero below B1 (£10–13/mo always-on); cookie-based ARR affinity is awkward for WSS; no revision model. Worse for the same money. |
| AKS | ❌ **not yet** | Right at 10+ replicas across regions. Today: a control plane, node pool, ingress controller and cert-manager to operate one container. Revisit at §3.4's trigger. |
| VM / VMSS | ❌ | Cheapest raw vCPU, but Aaron then owns patching, TLS, systemd and deploys. Contradicts *"minimal small to start"*. |
| Container Instances | ❌ | No ingress, no TLS, no LB, no scale-to-zero billing. Wrong shape. |
| Functions | ❌ | The engine is a **long-lived stateful process holding a hot city in memory** — the opposite of Functions. |

**Sizing:** inc1 at **0.5 vCPU / 1 GiB**, `min-replicas 0`, **`max-replicas 1` (mandatory, §1.4)**. Watch **memory, not CPU** — §4.2's cold store is 75 B/citizen, so Aaron's Q100027 **10M-citizen demo is ~750 MB of cold store alone** and needs 2–4 GiB. **Size for memory at inc4, not inc1.**

### 3.3 Region, resource group, and the spend cap

- **Region `uksouth`** — matches the existing estate, and §6's entire latency case rests on a UK-to-UK RTT.
- **Resource group:** reuse `garcia`, or a clean `rg-metropolis-dev` that can be deleted in one command. That deletability is the real runaway lever. **Q100140(b).**
- **Aaron's Q100024 hard cap is an inc1 setup step, not a follow-up.** Concretely: an Azure **Budget** on the RG with alerts at 50/80/100 % plus an action group. **Stated honestly: Azure Budgets *alert*; they do not *stop*.** The only true hard stop on pay-as-you-go is an automation runbook that disables the app at 100 %. **Recommend building that runbook in inc1** — ~20 lines, and the difference between a £2 month and a £500 surprise. **Q100141.**

### 3.4 The NLB / API gateway — deferred, with the criterion that reverts it

Aaron said *"maybe"*. The honest answer is **he already has it**: Container Apps ingress terminates TLS, provides a stable WSS FQDN, load-balances replicas, and supports **session affinity** — his 2026-08-31 sticky requirement. Front Door (~£26/mo) or Application Gateway v2 (~£20/mo) at inc1 would be a second load balancer in front of the first.

**Deploy a real gateway on the first of these becoming true:**

1. **More than one backend service** behind one origin (a separate solver or asset service alongside metroserve).
2. A **WAF** is needed — i.e. the endpoint stops being private. Aaron's Q100025 ruling means this is not true today.
3. **Multi-region** — the moment there is a second region, something must route between them.
4. A **custom domain plus edge caching**, or rate-limiting Container Apps cannot express.
5. Measured evidence that Container Apps ingress is itself the bottleneck (§6.5's smoke test would show it).

Until then the money buys engine memory instead.

### 3.5 Redis — not deployed, with the signal that would justify it

Aaron said *"if needed"*. It is not, and the reason is structural: **Redis exists to share mutable state at low latency between processes that do not share memory.** Today there is **one engine process per city — enforced, not merely chosen (§1.4)** — and its hot state is in that process's Go heap, ~100× faster than a Redis round-trip and free. Basic C0 (~£12/mo) now would be cargo-cult.

**Deploy Redis on the first of these becoming true:**

1. **Two or more replicas serve the same city concurrently** — which requires the ownership lease of §1.4 to exist first.
2. Measured **p95 blob read > 150 ms on a hot path**. Then Redis is a read-through cache in front of Blob.
3. **Cross-replica coordination** appears: a session→replica registry, a distributed lock on a journal, or a work queue for offloaded jobs. *(If it is only the queue, prefer **Azure Storage Queues** — pennies, no new always-on resource.)*
4. Ephemeral pub/sub fan-out to multiple clients of one city.

**Explicit non-reason:** *"caching derived aggregates"* does not justify Redis. They are cheap to recompute in-process and expensive to invalidate correctly across a network — and §5.4's `memoOnState` identity constraint makes a network cache actively harmful.

---

## 4. (b) The data store for buildings and people — "database or tiny files?"

### 4.1 First, what a savepoint actually contains — measured, not assumed

Measuring `saves/Dev-city1.json`, the one real save in the repo:

```
file                102,609 bytes raw   →   15,345 bytes gzip  (6.7×)
  savepoint         102,442
    snapshot        102,312
      buildings      73,645   (1,855 entries)  →  9,522 gz
      history        17,281   (240 entries)    →  1,650 gz
      roadConnectivity 8,534  (sorted string[] of "x,y")  →  1,929 gz
      ledger          1,936   (33 entries)     →    241 gz
      … 25 further scalar/small keys, together < 600 bytes
  journal           {"entries":[]}
```

> ### The finding that decides this section
>
> **There are no people in the savepoint.** The only population key is `population` — a **1-byte scalar**. The webconsole does not persist citizens at all; they are a derived aggregate. **The savepoint is buildings, and nothing else that scales with population.**

And on Aaron's real city the webconsole's own measurement (`saveCodec.ts`) is **~1.77 MB raw → ~300 KB stored**, via **`lz-string` `compressToUTF16` (~5–6×)** — which is the ~302 KB figure in circulation, now with its source. *(`A2Bev001.md`'s 550 KB figure was a different, later city whose `history` and `roadConnectivity` had grown.)*

**Caveat (ASM-1500):** a building record now carries **11 fields** (`id, spec, x, y, builtTick, bridgeOver, capacityTier, lastAutoScaleTick, heightStoreys, footprintW, footprintH, scaleLocked`), not the 4 in the older `Dev-city1.json` (`{"id":1,"spec":"m20","x":0,"y":56}`). Per-building cost is therefore **materially higher than that file's 39.7 B raw / 5.1 B gzipped**, and every building extrapolation below inherits the uncertainty. **Q100152 asks to re-measure before inc2.**

### 4.2 …and the Go engine is a completely different shape

`internal/engine/citizens/citizen.go` defines a per-citizen struct of ~18 fields; `coldshard.go` defines a **structure-of-arrays cold store across 256 shards**, whose own tests assert the cost:

- `TestColdShardBytesPerCitizen` — asserts **60–100 B/citizen**; the doc comment records the measured **~75 B**.
- `TestColdStore100MProjection` — asserts 100M citizens land in a **6–10 GB** band. At 75 B: **7.5 GB**.

Two data shapes, four orders of magnitude apart:

| | buildings | people | state at 100M |
|---|---|---|---|
| **TS webconsole** (what Aaron plays) | individual records, 11 fields | **not stored** — one scalar | ~157k buildings ⇒ **~1–3 MB compressed** |
| **Go engine** (`engine.citizens`) | (world/build state) | **individual**, SoA, 75 B each | 100M ⇒ **7.5 GB live**, ~1.5–2.5 GB compressed |

**This fork is the real content of Aaron's question**, and which side the cloud stores depends entirely on Q100138.

### 4.3 The options, weighed with arithmetic

Take the demanding case — Aaron's Q100027 **10M-citizen demo** on the Go engine: 10M × 75 B = **750 MB live**, ~150–250 MB compressed per snapshot, at 360-tick cadence ≈ **one snapshot every 6 minutes** at 1 s/day.

| Option | Cost / behaviour at 10M citizens | Verdict |
|---|---|---|
| **"Tiny files"** — one blob per building or citizen | 10M PUTs per full save ≈ **£4 per save**; ×10/hour = **£40/hour**. Latency: 10M round-trips is hours. | ❌ **Catastrophic.** The option Aaron named, and the first to rule out. |
| **One giant blob** — whole snapshot, single PUT | ~200 MB/snapshot, 10/hr = 2 GB/hr. Storage trivial; **write time is the problem** — fine in-region (~2 s), but 80 s from a browser on 20 Mbps upload. | ⚠️ Fine at ≤10 MB; wrong above it. |
| **Chunked blobs** — one blob per cold shard (256) or per sector | 256 parts × ~800 KB compressed. **Write only changed shards** — the SoA sharding and `FEAT-2326609764`'s dirty-sector tracking already identify them. Typical delta: a few MB. Restore: 256 parallel in-region GETs, ~1–2 s. | ✅ **Recommended.** |
| **Azure SQL / PostgreSQL** — row per citizen | 10M rows × ~120 B + indexes ≈ **2–3 GB**, full rewrite per snapshot. Serverless GP awake 4 h/day ≈ **£50/mo** before storage. Buys indexed query and partial update — **which the sim never uses**: the monthly pass iterates *every* row in index order, the worst pattern for a row store and the best for a columnar blob. | ❌ 10–50× the cost for capability never exercised. |
| **Cosmos DB** — document per citizen | 10M items × ~5 RU = 50M RU per full snapshot. **Hundreds of pounds/month** at this write rate. | ❌ Order of magnitude wrong. |
| **Azure Table Storage** | £0.05/GB/mo + transactions; 10M entities in 100-entity batches = 100k batch txns ≈ **£0.40/save**, ×10/hr = £4/hr. Also loses the SoA columnar layout. | ❌ ~100× Blob for no benefit. |

**Restore latency** — the other half. Aaron's Q100133 contract is *instant boot + background catch-up*, and `RestoreLatestSnapshotOrGenesis` already does snapshot + journal-tail replay. Chunked blob restore is 256 parallel in-region GETs (~1–2 s); a database is a 10M-row SELECT (minutes) that must then be **deserialised into the SoA columns anyway**. **The database loses on the metric that matters most.**

### 4.4 Recommendation

> **Azure Blob Storage (hot tier, LRS), as the backing implementation of the already-registered `int.persist.Store` interface. No database for simulation state.**
>
> **Layout** (per `CityKey{TenantID, CityID}`):
>
> ```
> <tenant>/<city>/manifest.json                       small, versioned: latest snapshot tick,
>                                                     part index, journal head
> <tenant>/<city>/journal/00000001.seg                append-only sealed segments
> <tenant>/<city>/snapshot/<tick>/meta.json
> <tenant>/<city>/snapshot/<tick>/part-000..255.bin   one part per cold shard / sector group
> ```
>
> **Rules:**
> 1. **Chunk at a boundary the engine already has** — the 256 cold shards (`coldshard.go`), and the 1000 m sectors once `FEAT-2326609764` lands. Never invent a new partitioning: the dirty-set that makes delta writes possible only exists at those boundaries.
> 2. **Start single-blob; switch to parts at a stated threshold — 10 MB compressed** (≈ 500k citizens on the Go engine, or ≈ 1.5M buildings on the TS model). Below that, chunking complexity is not worth it.
> 3. **The journal is append-only and never rewritten.** It is the SSOT (Aaron's DD: the engine owns the journal). Aaron's Q100102 rolling window prunes *old sealed segments*; it never mutates a live one.
> 4. **Payloads stay opaque `[]byte`** — literally the registered contract's format. The Blob backend must not learn what a building is. That is what keeps the swap a swap, and what keeps inc2 GR#25-clean.
> 5. **Compress before upload** — gzip or zstd server-side (measured 6.7× on real JSON; SoA integer columns should do better). Note the *client* currently uses `lz-string compressToUTF16`, chosen for localStorage's UTF-16 constraint; the cloud has no such constraint and should use a better codec.
> 6. **Immutable snapshots, mutable manifest.** Write a snapshot directory completely, *then* update the manifest in one atomic PUT with an ETag precondition. That single ordering rule makes a torn upload harmless.
> 7. **Replace the per-record `f.Sync()` with a batched durability contract** (§1.7). Per-record fsync against network storage is the wrong shape; fsync-per-batch with an explicit "commands since last durable point" bound is the right one.

**Where a database *is* right:** the tiny genuinely-relational metadata — city registry (who owns which `CityKey`), save index, deploy/audit records. That is **kilobytes** and belongs in **Azure Table Storage or a single JSON blob**, not a SQL server. It becomes a real database when real user accounts exist, which Aaron's Q100025 ruling defers.

### 4.5 The bonus this buys

The **5 MB `localStorage` quota** — the ceiling that motivated the whole epic and the subject of Aaron's still-open Q100121 — **is gone the moment inc2 lands**, independently of any convergence work. That makes inc2 the highest-value increment in this plan.

---

## 5. (c) The protocol — how browser and cloud stay consistent

### 5.1 Authority

**At inc1–inc4 the browser is authoritative.** The cloud engine is a *verifying shadow*: it never tells the browser what the state is, only whether it **agrees**.

Determinism is what makes that useful: the journal is a total order of commands, so any replayer from the same genesis reaches the same state. **At inc5+** each domain flips to cloud-authoritative only after its A/B result passes, and each flip is independently reversible — the cutover model Aaron approved on 2026-08-31.

### 5.2 ⚠ "Identical" is the wrong bar — the tolerance contract already exists

`docs/planning/phase3-convergence-plan.md` establishes, and I am adopting, that **byte-identical TS↔Go parity is impossible and is not the goal.** The two models are deliberately different: TS holds a scalar `funds`, Go a 6-account double-entry ledger; TS settles per-tick, Go per-month; TS uses doubles, Go int64 micro-pounds; TS uses no RNG for aggregate demographics, Go draws per-citizen Philox.

Divergence detection therefore uses that document's **three-tier tolerance contract** (exact / bounded / directional per field), which `internal/converge`'s `compare.go` and `tolerance.go` already implement. **This corrects any expectation that the shadow proves equality — it proves conformance to a declared tolerance.**

### 5.3 The sync contract

```
browser                                        cloud engine
───────                                        ────────────
append cmd to local journal  (seq N)
render immediately from local state ─────────────────────────► never blocks
                                    │
      batch of commands [seq N..M]  └── WSS ──► CityHost routes by CityKey,
                                                replays into its own engine
                              ◄────────────────  ack{ackedSeq:M, tick, domainReport}
compare per-domain report against the tolerance contract
  within tolerance → nothing to do
  outside          → registry error + visible badge; KEEP PLAYING on local
```

Four required properties, each a build requirement rather than a hope:

1. **Idempotent append.** Re-uploading a prefix after reconnect must be a no-op — requiring a **monotonic sequence number** the store honours. **`BUG-480`** (*"a swallowed append bricks newest-snapshot restore"*) shows the append error path is not yet trustworthy. **`BUG-480` blocks inc2. Q100143.**
2. **Divergence detection reuses `internal/converge`'s comparison logic.** ⚠ Note honestly: `internal/converge` has **no non-test callers anywhere in the tree** — it is a `go test`-only parity gate with no binary and no service. Its *tolerance and comparison* code is directly reusable; the *harness around it* (a callable entry point, a result type, a surface to report on) is new work in inc3. Cheap, but not free.
3. **Divergence is loud, never silent** (GR#1, GR#17): a registry error plus a badge. It must **never** silently correct the browser — that would be the cloud quietly rewriting Aaron's city.
4. **Offline is a first-class state, not an error.** The webconsole stays **fully playable with no cloud at all**; the journal buffers locally (IndexedDB, per Q100121) and drains on reconnect. Breaking this invariant turns a network blip into a lost city — exactly what GR#24 and GR#27 exist to prevent.

### 5.4 ⚠ Two client-side constraints any cloud split must respect

- **`memoOnState` caches on `SimState` object identity.** Any boundary that *deserialises* state fresh destroys all 26 memos plus the 3 `WeakMap` caches, turning a 34 ms tick into a full recompute. **A cloud response must therefore be applied as a patch into the existing state object, never as a wholesale replacement.**
- **The existing Web Worker ships the full ~1.77 MB state per `postMessage` per tick** (and is off by default). A cloud equivalent would be far worse. **A `StatePatch` / delta protocol is a hard prerequisite for any per-tick cloud interaction** — never ship full state per tick.

### 5.5 Versioning and access

- **Protocol semver, the 3-major window and capability flags are built and correct.** But **§1.3's build-string equality gate overrides them**: a client on a different build is refused with `MET-P010`. With deploy-on-green-`main`, every deploy invalidates every open tab. **Q100145.**
- **Access (Aaron's Q100025 — "port knock + password"):** Container Apps gives HTTPS/WSS and a non-guessable FQDN; on top, a **shared secret required in a header, rejected before the WS upgrade**. Stated plainly: **this is obscurity plus a password, not authentication.** Proportionate for a private single-user endpoint; not proportionate the day a second person has the link. **Q100144.**
- **`wsserver`'s `upgrader.CheckOrigin` returns `true` unconditionally** — no origin check. Acceptable on loopback; on a public FQDN it should be an allow-list. **Fold into inc1's port-knock work.**
- **Secrets and a public repo:** Aaron's Q100026 accepted encrypted repo secrets. **Recommend upgrading to GitHub OIDC federated credentials** — same setup effort, and no long-lived Azure secret exists to leak. **Q100142.**

---

## 6. (d) The latency budget

### 6.1 ⚠ The budget — and a discrepancy between the ruling and the code

Aaron's Q100100 ruling: normal 1 s = 1 day; **turbo 1 s = 3 days (333 ms/tick)**. The shipped webconsole speed ladder is **900 ms / 420 ms / 160 ms**:

| Speed | Shipped ms/tick | Game-days per second |
|---|---|---|
| Normal | **900 ms** | 1.1 |
| Fast | **420 ms** | 2.4 |
| **Turbo** | **160 ms** | **6.25** |

Normal is essentially on target. **Turbo ships at 6.25 days/s, twice Aaron's stated 3.** §6.4 uses the *shipped* 160 ms because that is what a round-trip must actually fit inside — the stricter of the two. **Q100147.**

| Path | Budget |
|---|---|
| One game day, normal | **900 ms** (shipped) / 1000 ms (stated) |
| One game day, turbo | **160 ms** (shipped) / 333 ms (stated) |
| One render frame | **16 ms** @60 fps, 50 ms @20 fps |
| Input → visible feedback | **< 100 ms** — this is what "low latency" means in practice |

### 6.2 What is already spent, locally

| | Measured | Source |
|---|---|---|
| Per-tick cost | **~0.7 ms per 1,000 buildings** | post-BUG-642/643 profiling |
| ⇒ Aaron's 49,174-building city | **~34 ms/tick** | derived |
| Display pass | **111 ms** | `BUG-642`, `9dc429a` |

At **900 ms normal**, 34 ms of tick is 4 % — comfortable. At **160 ms turbo** it is **21 %** — and the 111 ms display pass, if it runs on a turbo tick, is **69 % of budget on its own.** Turbo is already the tight case before a single network packet.

### 6.3 What a cloud round-trip costs

UK home broadband → `uksouth`, established WSS: **RTT ≈ 10–25 ms typical, 40–60 ms p95**, plus in-engine handling. **These are estimates, and inc1's entire purpose is to replace them with measurements from Aaron's browser on Aaron's connection (ASM-1502).**

### 6.4 The rules that follow

| Rule | Arithmetic |
|---|---|
| ✅ **One async round-trip per tick is affordable at normal speed.** | 40 ms of 900 ms = 4.4 %. |
| ⚠️ **At turbo it is not comfortable.** | 40 ms of 160 ms = **25 %**; p95 60 ms = **38 %**. Cloud interaction must be **batched across ticks**, not per-tick, at turbo. |
| ❌ **Zero round-trips on the frame path.** | 40 ms against a 16 ms frame is 2.5 dropped frames. Non-negotiable. |
| ❌ **Zero round-trips on the input→feedback path.** | 60 ms p95 plus a 111 ms display pass breaches 100 ms outright. Placement feedback comes from local state. |
| ✅ **Batch, never chatter.** | 300 commands as one message costs one RTT; as 300 messages, 300. The journal is already an ordered batch — send it as one. |
| ✅ **Never ship full state.** | §5.4: 1.77 MB per tick is ~90 Mbps at turbo. Deltas only. |
| ✅ **Every cloud call has a local answer.** | If the cloud is slow or gone, play continues. Cloud results *annotate*; they never *gate*. |

> **The single design invariant:** *if any code path can be made to wait on the network before the user sees a response, that path is a bug.*

### 6.5 The smoke test that proves it — inc1's real deliverable

From Aaron's browser, against the deployed engine (using the existing `protocolClient.ts`, §1.6):

1. `GET /health` × 20 — report p50/p95.
2. Open WSS, complete the handshake — report completion time **and confirm the build-string gate behaviour (§1.3) end-to-end**.
3. 1,000 minimal round-trips — report **p50 / p95 / p99 / max**.
4. One batch of 360 `AdvanceTicks` (a simulated year) — report total wall time and derived per-tick cost.
5. **Measure journal-append latency against the Azure Files mount** (§1.7's per-record `f.Sync()`) — report p50/p95 per append.
6. Kill the container revision mid-run — report reconnect time; confirm the city resumes at the same tick.

**Pass:** p95 round-trip **< 100 ms**, journal-append p95 **< 25 ms**, no lost commands across the restart.
**If round-trip p95 ≥ 100 ms:** the offload design becomes **asynchronous-only with no exceptions**, and inc5's browser-as-thin-client becomes infeasible from Aaron's connection — a finding worth having early and cheaply.
**If append p95 ≥ 25 ms:** Azure Files is the wrong inc1 store; switch to a managed disk or bring the Blob `Store` (inc2) forward.
**That is why this is increment 1.**

---

## 7. (e) The increment plan

### inc1 — "one engine up, one round-trip measured" · ~1 day · ~£2/month

**Goal:** a real, reachable, durable metroserve in Azure, and a *number* for the round-trip. Nothing else.

1. **`Dockerfile`** — multi-stage, Go 1.25 builder → distroless/static runtime, `buildinfo` ldflags preserved (GR#2: version from `git describe`, never a file).
2. **`/health` endpoint** — the one code change. Returns `buildinfo.String()`, current tick, hosted-city count. Serves the Container Apps readiness probe *and* answers "is it up?". Needs an error code from the next free P-block (**MET-P040+**; claim via `tools/plan/add-error.js`, never by hand — GR#7).
3. **Azure resources**, reusing the existing estate (§1.5): push to **`prixsixacr`**; create **one new Container App** in **`prixsix-env`**, `uksouth` — **never reusing `whatsapp-session`** (`MOD-069` AC-4). 0.5 vCPU / 1 GiB, `min-replicas 0`, **`max-replicas 1`**, external ingress, session affinity on. Add an **Azure Files share** (on `garcialtdstorage`) mounted at `/data`.
4. **Run as:** `metroserve -addr 0.0.0.0:8080 -persist-dir /data -city dogfood -tick-interval 1s -snapshot-every 360`.
   *The Azure Files mount gives real durability with **zero Go code**, deferring the whole Blob `Store` to inc2. That is what keeps inc1 to one day — with §6.5 step 5 measuring whether the fsync cost is acceptable.*
5. **Spend cap** (Q100024): Budget on the RG, alerts at 50/80/100 %, **plus the automation runbook that disables the app at 100 %** (§3.3).
6. **Port-knock secret** — shared secret header checked before the WS upgrade; **and tighten `CheckOrigin` to an allow-list** (§5.5).
7. **GitHub Actions deploy on green `main`** (Q100026), preferably via OIDC (Q100142).
8. **The smoke test of §6.5**, with its numbers written back into this document.

**Explicitly NOT in inc1:** Blob `Store`, Redis, gateway, multi-city, any convergence, any webconsole change beyond flipping `metropolis.liveEngineUrl` and a small measurement harness.

**Done when:** Aaron opens a URL, `/health` returns the build string, the smoke test yields a p95 number, he kills the revision, and the city is still at the same tick.

### inc2 — "the quota dies" · ~2–3 days · ~£3/month

The Azure Blob implementation of `int.persist.Store` (§4.4 layout), plus the webconsole uploading its journal and restoring from the cloud. Includes §4.4 rule 7 (batched durability replacing per-record fsync). **Blocked on `BUG-480`** (§5.3). Delivers: server-side saves, and the **5 MB `localStorage` ceiling gone** — answering Aaron's Q100121 with hardware instead of a choice between quotas.
**Gate:** save a city, clear all browser storage, reload, get the city back.

### inc3 — "background threads, actually offloaded" · ~3–4 days · ~£8/month

The first genuinely offloaded background work: the **continuous converge A/B**. The cloud engine replays the live journal and reports per-domain conformance against the §5.2 tolerance contract; the webconsole shows a parity badge. This is Aaron's literal ask, and it produces the evidence every future Phase-3 flip depends on. Reuses `internal/converge`'s comparison and tolerance logic; **adds** a callable non-test entry point and result surface (§5.3 note 2).
**Gate:** play for 10 minutes; the cloud reports a per-domain divergence table without the browser ever having waited on it.

### inc4 — "scale and the demo city" · ~1 week · ~£35/month

Multi-city `CityHost` deployed (already built), memory-sized replicas for Aaron's Q100027 **10M-citizen demo**, chunked snapshots once the 10 MB threshold is crossed (§4.4 rule 2), **journal compaction** (§1.7 — restore is O(total history) and Aaron already deferred this *to Phase 4*), the **ownership lease** that §1.4 requires before `max-replicas > 1`, and consolidator/projection jobs offloaded. **Redis and the gateway are re-evaluated here against §3.4/§3.5 — and deployed only if a trigger has actually fired.**

### inc5 — "the first flip" · Phase 3 continues · ~£35–75/month

The first domain (finance) flips to cloud-authoritative, gated on the §5.2 tolerance contract and reversible. The long pole, and the step that starts walking from §2 position 3 to position 1.

---

## 8. (f) Cost

List-price order-of-magnitude, `uksouth`, GBP, **to be verified against the actual subscription at deploy** (ASM-1501).

| Resource | inc1 | inc2 | inc3 | inc4 |
|---|---|---|---|---|
| Container Apps (0.5 vCPU/1 GiB, scale-to-zero, ~4 h/day) | £1 | £1 | £3 | — |
| Container Apps (2 vCPU/4 GiB, warm during play) | — | — | — | £25 |
| Container Registry — **`prixsixacr` already exists** | **£0** | £0 | £0 | £0 |
| Storage — Azure Files share | £1 | — | — | — |
| Storage — Blob hot LRS + transactions | — | £1 | £2 | £5 |
| Log Analytics (5 GB/month free tier) | £0 | £0 | £1 | £3 |
| Redis (only if §3.5 triggers) | — | — | — | £0–12 |
| Gateway (only if §3.4 triggers) | — | — | — | £0–26 |
| **Estimated monthly total** | **~£2** | **~£3** | **~£8** | **~£33–71** |

**Cost notes worth stating plainly:**

- **Scale-to-zero is the whole cost story at inc1–3.** An always-on 0.5 vCPU / 1 GiB container is ~£35/month; scaled to zero and used 4 h/day it is ~£1. The Container Apps free monthly grant (180,000 vCPU-s + 360,000 GiB-s) covers most of that outright. `MOD-069` reached the same conclusion in 2026-08.
- **Reusing `prixsixacr` and `prixsix-env` removes what would otherwise be inc1's largest line** (~£4/month ACR — more than the compute).
- **Blob storage is genuinely negligible:** even 20 GB of retained snapshots is ~£0.30/month. §4's entire recommendation costs less than a coffee.
- **The risk is not the steady state, it is a runaway.** A stuck replica or a scale-out misconfiguration turns £2 into £500. Hence §3.3's insistence that the **runbook** — not just the alert — lands in inc1.

---

## 9. GR#25 — graph conformance and prerequisites

**This document references only modules registered in `code.json` at `88f9bce`:** `cmd.metroserve`, `int.persist` (GUID `82897e55-…`, inbound `Store` GUID `087edd5a-…`), `int.protocol`, `engine.core`, `engine.citizens`, `engine.checkpoint`, `feat.compositionroot`, `harness.replay`, `cloud.azure` (path `cloud/`, inbound `CloudServices`, outbound → `int.solver`, `int.serializer`).

**What needs no new edge:**

> The **Azure Blob backend is a new implementation of the existing `int.persist.Store` inbound contract** — *"Go interface, opaque `[]byte` payloads"*. Implementing a registered inbound contract creates no outbound edge. This was the explicit intent of the Phase 1 architect decision (2026-08-31): *"the `Store` interface makes it a swap, nothing hard-codes Azure."* **inc2 is GR#25-clean as designed.**

**Prerequisites — new registrations that MUST land in `master-plan-v2.1.json` and be regenerated via `tools/plan/generate.js` BEFORE the corresponding increment's acceptance criteria are written.** Stated as prerequisites, not prosed into existence:

| # | Registration | Needed by | Note |
|---|---|---|---|
| P1 | `cmd.metroserve`'s `/health` surface | inc1 | If the handler lives in `cmd/metroserve` and calls only existing collaborators, no new edge — **confirm at criteria time, do not assume.** |
| P2 | Error range **MET-P040–P049** | inc1 | Via `add-error.js claim-range` → `add` → `check`. Owner must be a **module key** (`int.protocol` or `cmd.metroserve`), never a FEAT-/BUG- code, or the Go scanner reddens CI. *(Existing neighbours: protocol `P010–P035`, persist `G808–G811`, converge `H500–H503`.)* |
| P3 | `cloud.azure` → `int.persist` | inc2, **only if** the Blob backend is placed in `cloud.azure` | **Recommend instead: place it under `internal/persist/` as a second `Store` implementation** — then P3 is unnecessary and inc2 stays edge-free. |
| P4 | Some module → `cloud.azure` | whenever `cloud.azure` first gains real use | `cloud.azure`'s `CloudServices` inbound has **zero registered consumers** — it is an orphan node today. |
| P5 | **`BUG-560`'s cluster** — `cloud.azure`→`balance.harness`, `cloud.azure`→`cloud.netpolicy`, and `cloud.netpolicy`'s inbound contract (INT-004) | before any `cloud.*` code lands | **Already an open item.** Must close first, or the spec-guard blocks the commit. |
| P6 | `internal/converge` → whatever surfaces the continuous A/B result | inc3 | Register before inc3's criteria are written. |
| P7 | The missing `int.persist` inbound consumers (`engine.core`, `engine.checkpoint`, `cmd.metroserve`) | housekeeping | §1.2 — a pre-existing completeness gap, worth closing with P5. |

**Also note:** `FEAT-2326609764` carries an explicit GR#25 statement that it proposes *no* Go-engine collaboration, and that revisiting the Go route needs a fresh BA pass with edges registered first. **§2 is that revisit being opened — and it is opened as a question to Aaron, not as a decision.**

---

## 10. Assumptions — ASM- candidates, each with a recommendation

| ASM | Assumption | Recommendation |
|---|---|---|
| **ASM-1500** | Per-building storage cost extrapolates from `saves/Dev-city1.json` (4-field records, 39.7 B raw / 5.1 B gz). | **Do not accept — re-measure in inc1.** Building records now carry **11 fields**, so the real cost is materially higher. Every §4 building extrapolation inherits this. Ten minutes to fix (Q100152). |
| **ASM-1501** | §8's list prices are within ~30 % of Aaron's actual billing. | **Accept for planning; verify at inc1 from the first real invoice.** Credits, EA discounts and free-tier grants all move this. |
| **ASM-1502** | UK-home → `uksouth` RTT is 10–25 ms p50 / 40–60 ms p95. | **Do not accept — this is exactly what §6.5 measures.** The whole §6 case rests on it and one day of work retires it. |
| **ASM-1503** | The Go cold store's **~75 B/citizen** is the right basis for cloud snapshot sizing. | **Accept.** Asserted by a live test (60–100 B band), with a companion test pinning the 100M projection to 6–10 GB. |
| **ASM-1504** | `int.persist.Store`'s append is (or can cheaply be made) **idempotent on a monotonic sequence number**. | **Verify before inc2 criteria.** `BUG-480` says the append error path is not yet trustworthy. If not idempotent, inc2 grows ~1 day. |
| **ASM-1505** | The browser stays authoritative through inc4 (§2 position 3). | **Aaron to confirm — Q100138.** If he means position 1, inc2–inc5 change shape and §4's answer moves to the 7.5 GB Go shape. |
| **ASM-1506** | Container Apps' built-in session affinity satisfies Aaron's sticky-session requirement. | **Accept, and note it is not load-bearing:** determinism means any replica can rebuild any city by replay, so affinity is a performance optimisation. Already the epic's own architect note. |
| **ASM-1507** | 0.5 vCPU / 1 GiB suffices for inc1–inc3 with Aaron's ~49k-building dogfood city. | **Accept for inc1–3; size for memory at inc4** — the 10M-citizen demo needs ~750 MB of cold store alone. |
| **ASM-1508** | Aaron's Q100025 "port knock + password" stays proportionate through inc4. | **Accept while the endpoint is Aaron-only.** It stops being proportionate the day a second person has the URL — Q100144, not assumed away. |
| **ASM-1509** | Azure Files fsync latency is low enough for a per-record `f.Sync()` journal at 1–4 ticks/second. | **Do not accept — §6.5 step 5 measures it.** If p95 > 25 ms, inc1's storage choice changes (managed disk, or pull inc2 forward). |
| **ASM-1510** | The build-string equality gate (§1.3) can be relaxed to protocol-semver-only without weakening determinism. | **Recommend yes** — the wire version already never reaches the engine or the journal, which is what determinism actually requires — **but this is Aaron's call (Q100145)**, because it was his *"engine build string stays client-visible"* ruling that shaped it. |

---

## 11. Open questions for Aaron — Q100138–Q100152

*(Ready to paste into `A2Bev001.md`. Each carries my recommendation; "rec-on-all" is a valid answer.)*

**Q100138 — What is the cloud engine actually *for*: a shadow, or the engine?** Three of our own decisions disagree (§2). `FEAT-1972079936` Path A says the browser eventually runs **zero** simulation. `FEAT-2326609764` §11 says explicitly do **not** move the heavy path to Go, because it duplicates ~29 derivations across two languages to buy a constant factor that density gives free. Your words today — *"background threads are offloaded"* — are narrower than both and compatible with the second. **Rec: the narrow reading — the cloud is a durable, verifying shadow through inc4, and Path A's thin client arrives later, one domain at a time, via the A/B cutover you already approved.** It gets you the storage win and the offload win in days rather than months and forecloses nothing. This shapes every other increment, so it is the one I most need.

**Q100139 — Is ~£2–8/month at inc1–3 and ~£33–71 at inc4 what you want to spend?** (§8.) **Rec: yes, and set the hard cap at £50/month initially** — high enough that a legitimate inc4 does not trip it, low enough that a runaway is caught in hours rather than at the invoice. Easy to raise later.

**Q100140 — (a) Container Apps, no gateway and no Redis yet — agreed? (b) Reuse your existing `garcia` RG and `prixsix-env`, or a clean `rg-metropolis-dev`?** On (a): Container Apps ingress *already is* your L7 load balancer, TLS terminator and sticky router, so a separate gateway would be a second one; and Redis has nothing to share while the engine is structurally limited to one replica per city (§1.4). **Rec (a): defer both, with the written triggers in §3.4/§3.5 so it is a criterion and not a vibe.** On (b): reuse is cheaper and faster (your ACR and Container Apps environment already exist, saving ~£4/month and most of the setup), but a separate RG can be deleted in one command, which is the real runaway lever. **Rec (b): reuse `prixsixacr` and `prixsix-env`, but put the Metropolis app and storage in a NEW `rg-metropolis-dev`** — you keep the cheap shared plumbing and still get the single-command blast radius.

**Q100141 — Do you want a hard *stop*, or just alerts?** Azure Budgets **alert**; they do not stop spending. The only real hard stop is an automation runbook that disables the app at 100 %. **Rec: build the runbook in inc1.** It is ~20 lines and it is the difference between a £2 month and a £500 surprise. You asked for a hard cap, and an alert is not one.

**Q100142 — Deploy credentials: repo secrets (your Q100026 ruling) or OIDC?** The repo is **public**. **Rec: upgrade to GitHub OIDC federated credentials** — same setup effort, but no long-lived Azure secret exists to leak. I would treat your original ruling as satisfied by this rather than overridden, but it is your call.

**Q100143 — `BUG-480` blocks inc2. Fix it first, or run it alongside inc1?** A swallowed journal append that bricks newest-snapshot restore is exactly the failure that would make cloud persistence *worse* than localStorage. **Rec: close `BUG-480` as a P1 immediately after inc1.** inc1 does not depend on it (it uses an Azure Files mount, not the `Store`), so nothing is blocked meanwhile.

**Q100144 — When does "port knock + password" stop being enough?** Your Q100025 ruling is proportionate for a private, Aaron-only endpoint and I am building to it. It stops being proportionate the moment a second person has the URL, or the day a save you care about lives only in the cloud. **Rec: keep it through inc4, and treat "anyone else gets a link" as the automatic trigger for real auth** — not a judgement call made later under pressure. I will also tighten the WebSocket origin check in inc1, which is currently wide open (§5.5).

**Q100145 — The handshake refuses any client whose *build string* differs from the server's. Relax it?** (§1.3.) Phase 0 gave you exactly what you asked for — semver negotiation, a 3-major window, capability flags, so a client is never forced to upgrade. But `wsserver` *also* compares engine build strings and refuses with `MET-P010` on any mismatch. With GitHub Actions deploying on every green `main`, **every deploy would lock out every open browser tab** — possibly several forced reloads a day. **Rec: relax the gate to protocol-semver-only, and keep the build string visible but non-blocking** (which was the spirit of your "keep the engine build string client-visible" ruling). Determinism does not need this gate: the wire version never reaches the engine or the journal. If you would rather keep it strict, the alternative is deploying on a tag rather than on every green `main` — tell me which and I will build to it.

**Q100146 — Accept one engine replica per city for now?** Two structural limits force it (§1.4): the local-disk store takes **no file lock or ownership lease**, so two processes on the same city would corrupt its journal; and `wsserver` supports **one live WebSocket per city**. Neither is a defect — they are just the edge of what is built — but together they mean your *"10 players share one engine, or scale to 10"* elasticity needs an ownership lease (a blob lease fits naturally) before horizontal scale is safe. **Rec: accept `max-replicas 1` through inc3, and build the lease in inc4** where it is needed anyway for the multi-city demo. It costs nothing now and it is the honest state of the system.

**Q100147 — Turbo ships at 6.25 game-days/second, not the 3 you specified.** The speed ladder is 900 / 420 / **160** ms per tick; your Q100100 ruling said turbo = 333 ms. Normal is on target; turbo is twice as fast as you asked for. **Rec: tell me which is right.** If 160 ms is what you actually enjoy playing, I will treat it as the real budget — but it is tight: at 49k buildings the tick is ~34 ms (21 % of it) and a cloud round-trip would be another 25 %, which is why §6.4 says cloud work must batch across ticks at turbo rather than happen per-tick. If 333 ms was the intent, turbo is currently a bug.

**Q100148 — Which background job do you most want offloaded first?** inc3 picks one. (A) the **continuous converge A/B** — the cloud replays your live journal and tells you where Go and TS disagree; (B) the **consolidator plan** (`FEAT-2326609761`), whose first pass on your city is the 20,000-stranded-dwellings job; (C) long-horizon **projections / what-if**. **Rec: A** — it reuses `internal/converge` unchanged so it is the cheapest, it is the evidence every future Phase-3 flip depends on, and it turns "are the two engines the same?" from an open worry into a number on your screen. B is the one you would *feel* most, so if you want the visible win first, say B and I will sequence it.

**Q100149 — Snapshot retention in the cloud.** You ruled a rolling window for the local journal (Q100102) and ~10-minute snapshots (Q100101). Blob storage is cheap enough to keep **everything**: a year of 10-minute snapshots at inc1 scale is under £1/month. **Rec: keep every snapshot for 30 days, then thin to one per game-year, and never prune the journal in the cloud at all.** Storage is not the constraint it was in a browser, and being able to rewind your city to any 10-minute mark is worth more than the pennies. (Separately, inc4 must add journal *compaction for restore speed* — restore currently reads the entire journal into memory, which is O(all history forever).)

**Q100150 — The 10M demo city (your Q100027): browser, cloud, or both?** 10M citizens on the Go engine is **750 MB of cold store** — a cloud-shaped object, not a browser-shaped one. On the TS model it is a scalar and costs nothing, but then it is not really 10M simulated people. **Rec: cloud-side for the real 10M** — it is the clearest thing the cloud gives you that a browser structurally cannot, and it makes the demo an argument for the architecture rather than a number in a HUD.

**Q100151 — Should the webconsole *show* the cloud?** A small status element: connected / offline / syncing, last upload, parity badge, engine build string. **Rec: yes, and make it honest** — including a visible "OFFLINE — playing locally, N commands buffered" state. Goal 5 is backend visibility, and a sync system you cannot see is one you cannot trust. It is also how you would catch a silent divergence (GR#17).

**Q100152 — May I re-measure your real save before finalising the storage sizing?** My §4 numbers lean on `saves/Dev-city1.json` (2026-08-30), whose building records have **4 fields**; yours now have **11** (footprint, height, capacity tier, auto-scale state). Every building extrapolation is therefore optimistic (ASM-1500). **Rec: yes — send me one current save, or let me use the env-gated local path from `FEAT-2326609764` Q3, and I will re-derive §4 before inc2.** Ten minutes, and it is the number the whole storage plan rests on.

---

## 12. What I recommend happens next

1. **Aaron answers Q100138** (shadow vs engine) — the only one that blocks *design* rather than build.
2. **inc1 is dispatched regardless** — valid under every answer to Q100138, ~£2/month, ~1 day, and it retires three estimates (ASM-1502 latency, ASM-1509 fsync, ASM-1500 building size) with measurements. Nothing downstream should be planned on a guess that one day of work can replace.
3. **Q100145 (build-string gate) is answered before the deploy pipeline is switched on** — otherwise the first automatic deploy locks Aaron out of his own tab.
4. **`BUG-480` is raised to P1** behind inc1 (Q100143).
5. **`BUG-560`'s edge cluster (P5, §9) closes** before any `cloud.*` code lands.
6. **§4 is re-derived against a current save** (Q100152) before inc2's acceptance criteria are written.

---

*Bev, 2026-09-03. Docs-only. Every measurement here was taken from the tree at `88f9bce`; every estimate is labelled as one.*
