# Compute offload: consolidated plan (2026-11-17)

**Status:** the design is locked; nothing is deployed. The pre-cloud defects are landing now. Next come inc2 (the portable store), inc1 on pix, and then the launcher.
**Epic:** FEAT-1972079936. **Phase 4:** FEAT-2326609775.
**Rulings:** `docs/planning/A2Bev001.md` v23–v23.3.

**The review documents, in this folder:**

| Doc | Contents |
|---|---|
| `a1.md` | Round 1: discovery answers and a check of the review's claims |
| `a2.md` | Rounds 2–4: the 100M engine and data tier, FinOps and deploy safety, multiplayer seams. Code-grounded |
| `a3.md` | Where compute could be cheaper; portability |
| `a4.md` | Live AWS and GCP prices; the backend-agnostic launcher |

**Older docs:** `docs/planning/azure-cloud-engine-design.md` and `docs/planning/go-engine-100m-proving-plan.md` both carry status blocks pointing here. `docs/planning/inc1-cost-forecast.md` is the Azure fallback forecast.

---

## 1. Verdict: is the CPU-offload design sound?

**Yes, with the corrections this review uncovered.** The foundation suits a 100M-citizen simulation:
- a deterministic Go engine;
- behind a WebSocket JSON-RPC boundary;
- persisting an append-only command journal plus chunked snapshots.

The review found **five traps** in the original plan. Any one of them would have broken latency, memory or the £20/month budget if deployed as it was.

| # | Trap | Fix | Tracked by | State |
|---|---|---|---|---|
| 1 | **Forgotten-tab billing:** an open tab holds a WebSocket, so the host never scales to zero (~£125/month for one tab, 6× the cap) | Server hibernation after 15 min idle or paused, plus a client disconnect after 5 min hidden (Q100157) | FEAT-9995385595 | open |
| 2 | **~3.7-minute cold start:** no snapshot on SIGTERM or idle eviction, so a 10M wake replays up to 360 day-ticks at ~624 ms each | Snapshot on hibernate and SIGTERM (≤ 20 s budget, Q100163), plus packed binary SoA shard snapshots instead of NDJSON (~400 B/citizen) | BUG-1197 (D2), FEAT-9995385594 (inc2) | D2 building; inc2 ACs reviewed |
| 3 | **Single core in production:** `runShardsParallel` exists, but `c.workers` is 1, and the monthly money walks run serially | Configurable workers; staggered per-shard walks with month-end barrier passes (Q100155) | FEAT-9995385598 (the materialiser part landed as BUG-1198) | open |
| 4 | **Multi-tab race:** one pump per transport, yet a second connection to a city was admitted, with no lease or epoch fence | Refuse duplicate connections (D1), plus conditional-write epoch fencing on the manifest | BUG-1196 (landed), FEAT-9995385594 | D1 landed; fencing in inc2 |
| 5 | **Vendor lock-in:** inc2 on the Azure Blob SDK and lease would trap the project on Azure (~£17 per 125 h at 10M) | S3-compatible store with conditional-write epoch fencing; Cloudflare R2 (zero egress); compute on pix, then Spot VMs, then Azure; Cloudflare Tunnel and Access in front | FEAT-9995385594, FEAT-9995385599, FEAT-9995385600, FEAT-9995385601 | ruled; building in sequence |

## 2. Topology: one engine per city, never several engines on one city

- **One city is strictly one engine process (single writer).**
  - A live city, 1M or 100M citizens, is never split across servers.
  - All 256 citizen shards live in one `metroserve` process's RAM. That means scaling up, not out:

    | City size | RAM |
    |---|---|
    | 10M | 2–4 GiB |
    | 30M | 6–8 GiB with disk paging |
    | 100M | 32–64 GiB |

  - Inside the process, goroutines work across the shards. A single deterministic command queue means no distributed locking.
- **Many cities means multi-tenant hosting plus scaling out.**
  - One `metroserve` co-hosts independent single-player cities (`CityHost`, keyed by `CityKey{TenantID, CityID}`), up to its RAM.
  - More cities than one box holds means more independent `metroserve` instances.
  - Each instance owns a distinct set of cities, fenced by the manifest epoch.
- **Future multiplayer keeps the same shape.** It is shelved (FEAT-023, inert slots).
  - Each city stays a single-writer deterministic engine.
  - Separate cities trade asynchronously through journaled external settlement events (`ext` records).
  - Co-op mayors on one city funnel commands through that city's single command queue.

## 3. Hosting and cost (rulings Q100154, Q100159–Q100165)

**Tiers, in order of preference:**

| Tier | Host | Cost | Notes |
|---|---|---|---|
| 1 | **pix** (8 GB, on 24/7) | £0 | steady play up to ~10M |
| 2 | **Spot VMs**: AWS EC2 or GCP Compute Engine, whichever amd64 is cheapest at start | ~£3–4 per month of metered 10M play; £0.68–1.29 per 8-hour 100M proving run | needs D2's ≤ 20 s snapshot and upload |
| 3 | **Azure Container Apps** | ~£17 per 125 h at 10M | the managed fallback, inside the £20 cap |

- **Not used:** Lambda, Function Compute and Firebase (wrong shape); Alibaba (no new account); mainland China (latency and legal overhead).
- **Store:** S3-compatible, Cloudflare R2 primary, minio-go client (aws-sdk-go-v2 acceptable). No hand-rolled signing.
- **Front door:** Cloudflare Tunnel plus Access.
- **CPU architecture:** amd64 only, until an arm64 determinism gate passes (FEAT-9995385602).
- **Guards:**
  - the in-app active-hours meter (FEAT-9995385596);
  - hibernation (FEAT-9995385595);
  - a cost gate before every paid start;
  - the Azure Budget plus kill-switch runbook, if the fallback is ever provisioned.

## 4. Build sequence and BOW

| Step | Item | Status | Depends on |
|---|---|---|---|
| D1 | BUG-1196 one connection per city | **landed** | — |
| D2 | BUG-1197 snapshot on SIGTERM and eviction (≤ 20 s), torn-tail truncation | building | — |
| D3 | BUG-1198 no full-population materialisers | **landed** | — |
| D4 | BUG-1097 handshake build gate relaxed to a warning | **landed** | — |
| D5 | BUG-719 deploy secret seeding | **landed** (follow-up BUG-1199) | — |
| inc2 | FEAT-9995385594 portable S3/R2 store, packed-SoA snapshots, journal v2, epoch fencing | ACs written (94), independent review running | BUG-1197 |
| — | FEAT-9995385595 hibernation and Page Visibility | open | BUG-1197 |
| — | FEAT-9995385596 active-hours meter | open | — |
| — | FEAT-9995385597 waking/draining/hibernating messages; client reconnect | open | — |
| — | FEAT-9995385598 workers and staggered monthly walks | open | — |
| — | FEAT-9995385599 Cloudflare Tunnel and Access | open | — |
| inc1 | FEAT-9995385600 metroserve on pix, measured | open | FEAT-9995385599, -95, -96, BUG-1197; SAFE-552 (pix web host) |
| launcher | FEAT-9995385601 `tools/launcher` with the Spot-reclaim watcher | open | BUG-1197, FEAT-9995385594 |
| — | FEAT-9995385602 arm64 determinism gate | open (P3) | — |

**Related findings filed during the review:**

| Item | Finding |
|---|---|
| BUG-1193 | Worktree Go tests read main's `data/` and error registry |
| BUG-1195 | The bug775 guard test is load-fragile |
| BUG-1199 | The create path writes a placeholder secret |
| BUG-1200 | The confirm dialog has no focus trap or Esc |
| BUG-1202 | BUG-1198 follow-ups |
| BUG-1203 | BUG-1097 follow-ups |
| BUG-1204 | No handshake write deadline |
| SAFE-567 | Cost-gate false positives |
| BUG-1201 | Closed: a harness artefact, not a defect |
