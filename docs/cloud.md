# Cloud in Metropolis — what it's for, which provider, and how it stays safe

**What this is:** a decision document, not a spec — nothing described here is built.
It surveys the five cloud use cases the architecture already has seams for, adjudicates
whether the master doc's incumbent "Azure" placeholder should stand, and sets out the
secure/resilient-by-design contract every future remote call must follow.

**Audience:** Aaron, and anyone at the M2 review point re-checking this decision. This
document originally existed to get a provider decision (and a first read on budget
appetite) out of Aaron directly; §2 now records that decision rather than proposing one —
see "Open questions for Aaron" (§5) for what's still outstanding.

**Status:** DECIDED. **Aaron ruling, 2026-08-09: Azure confirmed as the cloud platform,
standing until Aaron says otherwise.** See §2 for the ruling, the reasoning preserved as
history, and the M2 review trigger. Two of the three original open questions in §5 are
answered by the ruling and by the existing-estate detail below; one (M2 budget appetite)
remains genuinely open.

**Spec refs:** `docs/METROPOLIS-MASTER-v2.1.md` §15 (Architecture — cloud path) and A9
(Cloud thresholds); `docs/design/solver-contract.md` (INT-003, the frozen interface any
cloud backend implements against); `docs/planning/sprint-plan-v1.md` §6 (Go vs C#
adjudication — the perf-CI guard-rail that triggers pulling GPU/cloud forward) and its
"Unscheduled (future)" row; BOW items `cloud.azure` (MOD-069), `cloud.gpu`, `cloud.netpolicy`
(INT-004).

First, the thing that needs clearing up: you may have noticed "AWS" go past in a recent
commit — that was only a regex in the secret-scanning pre-commit hook
(`claude-secret-guard.js`, pattern `AKIA[0-9A-Z]{16}` for AWS access-key IDs), which
scans for *every* major provider's key format regardless of which provider we use, the
same way it scans for GitHub tokens and OpenAI keys. It is not a provider decision and
was never meant to look like one. You have not been consulted on a cloud provider before
now — this document is that consultation.

---

## 1. What cloud is actually for, use by use

The design principle throughout is: **the seams exist from day one (gRPC solver
contract, object-storage-shaped save references); cloud backends behind those seams do
not.** v1 ships entirely local. Nothing about the architecture requires cloud to work —
cloud is capacity added later, on the same interfaces, only when a real number says it's
needed. Five uses, in the order they'd actually arrive:

### (a) Blob-style save storage — early, optional quality-of-life
Off-machine backup/sync of save files. Not required for the game to function — saves are
sharded gzipped NDJSON (or the binary shard format above the JSON size threshold, A3) on
local disk either way. This is the cheapest, lowest-risk cloud item and could land any
time after v1 without touching the engine: it's a file upload behind a "sync my saves"
menu option, nothing in the simulation depends on it existing.

### (b) Batch compute for M2 balance-tuning — the biggest and earliest real need
Sprint plan S8 (M2) runs thousands of headless parameter sweeps — each a `metropolis
-headless` run of up to hundreds of simulated game-years — to tune the pacing knob and
growth curves until "0→100M citizens, achievable but hard" holds. Doing that on one
development machine is slow by construction: it's an embarrassingly parallel batch of
independent, deterministic, short-lived jobs. This is genuinely the first cloud need on
the roadmap by calendar time, arriving at M2/S8, well before any player-facing cloud
feature. It costs the architecture nothing extra: H-HEADLESS is already the exact
interface a batch runner invokes (`-seed N -months M -out snap.json`), built as a
permanent test-estate fixture regardless of where it runs.

### (c) Stateless solver offload slots — traffic equilibrium, deep projections, batch life-writing
The four `int.solver` problem kinds (`TrafficAssignment`, `ColdPassBatch`,
`DeepProjection`, `LifeWriting` — see `docs/design/solver-contract.md`) are the heavy,
parallelisable solves. Each is a stateless request/response call with **mandatory local
fallback**: CPU always works (priority 0), a GPU sidecar can accelerate locally later
(priority ~50), and a cloud backend can accelerate further (priority ~100) — the engine
cannot tell which one answered except by latency, and the result must be bit-identical
across all three (determinism rule, solver-contract.md). Adoption trigger is explicit and
mechanical, not aspirational: the sprint plan's guard-rail (§6) says if S3's cold pass or
S5's traffic equilibrium (SUE) breaches perf-CI budget by more than 2× on the 10M-citizen
synthetic benchmark, the response is to pull the GPU sidecar (or, beyond that, cloud)
forward — never to rewrite the engine. No cloud spend happens speculatively.

### (d) Cloud citizen-shard service beyond ~20–30M citizens (Amendment A9)
Past the explicit local-CPU ceiling (A9: "local CPU covers ≲20–30M citizens
end-to-end"), the cold-pass and citizen shard store migrate to a cloud citizen-shard
service on the same solver seam, so a save that outgrows one machine keeps growing
without the player's experience changing. This is the furthest-out of the four active
uses — it only matters for a save that gets very large, which is itself a late-game,
success-case problem.

### (e) Explicitly shelved / out of scope
- **Persistent shared-world server.** Multiplayer/shared worlds are recorded as
  "architecture-ready, shelved" (Master doc V.1) — the protocol/seam design would support
  it, but nothing is planned or costed toward building it, and it does not appear on the
  sprint roadmap (`docs/planning/sprint-plan-v1.md` "Unscheduled (future)" row).
- **LLM soft layer.** An optional post-v1 feature (ticker prose, an advisor persona) —
  "never the number cruncher" (§15). Not on the critical path to v1 and not assumed by
  any of (a)–(d).

**What each costs the architecture today: nothing.** All four active uses are already
routed through interfaces that exist for local-only reasons anyway (the solver contract,
the headless harness, the save serializer) — cloud backends are additional
`registry.Register` calls, not new call sites in the engine. That is the entire point of
building the seam before the backend.

---

## 2. Provider adjudication — does "Azure" actually hold?

The master design document names Azure throughout (§15 "cloud (Azure)"; M0-ENG §1 "CPU
→ GPU sidecar → cloud (Azure)"; the process topology diagram; `code.json`'s
`cloud.azure` module). Worth being honest about how that got there: it reads as the
provider that was on hand to write down when the seam-not-backend principle was designed
— a placeholder that never got revisited, not a comparison that was actually run.
Assessed honestly against the four concrete loads above:

| Load | Azure | AWS | GCP | Notes |
|---|---|---|---|---|
| **Batch compute** (thousands of short, interruptible, deterministic headless runs — use (b)) | Azure Batch: low-priority VMs, straightforward job/pool model | AWS Batch on EC2 Spot: same shape, deeper spot-market liquidity, generally the most mature/cheapest spot pricing of the three | GCP Batch: newer product, Spot VMs, smallest ecosystem of examples for this pattern | All three fit — this workload (stateless, restartable, no shared state) is exactly what spot/low-priority tiers are for. AWS has the edge on spot price depth and tooling maturity; Azure and GCP are both workable. Not a decisive difference at single-developer scale. |
| **Blob storage** (save sync — use (a)) | Blob Storage | S3 | Cloud Storage | Near-total parity: object PUT/GET, lifecycle rules, cheap egress-light small-file storage. Any of the three is fine; this criterion doesn't move the needle. |
| **Stateless gRPC services** (solver offload — use (c)) | Container Apps: scale-to-zero, gRPC support, but the youngest of the three products in this class | AWS Fargate: mature, but classic Fargate doesn't scale to zero as cleanly (you pay for a running task, not per-request) | Cloud Run: scale-to-zero is the product's headline feature, gRPC-native, generally the cheapest idle cost for bursty, occasionally-called services | For *this specific shape* — a solver call that fires occasionally and should cost ~nothing while idle — Cloud Run's scale-to-zero economics are the best fit of the three. Azure Container Apps is close behind and workable; Fargate is the weakest fit for spiky, low-frequency traffic. |
| **GPU instances** (far-future cloud-GPU tier, use (c)/(d) at the far end) | Available, generally tighter capacity/quota friction for a small/new account | Broadest GPU SKU range, most mature spot-GPU market | Competitive GPU availability, improving quota story | This tier is years out on the roadmap (unscheduled — triggered only by a perf-CI breach); not worth weighting heavily today. All three are viable when it matters; AWS is marginally ahead on raw availability. |
| **Developer-tooling fit** (Windows-first single developer, no existing org cloud estate) | Best native Windows-developer story (Visual Studio/`.NET`/PowerShell tooling lineage), free-tier credits comparable to the others | Broadest documentation and community-example base for almost any problem you'll hit; CLI/SDK quality is strong and Go-friendly | Cleanest CLI/SDK ergonomics of the three in many developers' experience, smallest "getting lost in the console" tax | For a solo Windows developer with no existing cloud estate, weight goes to whichever has the shallowest learning curve and best free-tier runway to try things without commitment risk — Azure and AWS are both reasonable here; GCP's simpler console is a genuine but secondary plus. |

**Ruling (Aaron, 2026-08-09), recorded on BOW item `cloud.azure` / MOD-069: Azure is
confirmed as the cloud platform, standing until Aaron says otherwise.** This closes the
provider question below — the original "keep Azure, low confidence, revisit at M2"
recommendation and its reasoning are preserved as history immediately below, since they
are still the honest record of *why*, but the decision itself is no longer provisional.

**Reasoning preserved from the original comparison (history):** no load above gave any
provider a decisive win — this was a "any of the three would work fine" situation, which
meant the deciding factors were really operational (what Aaron already knows, what's
easiest to set up alone, what free-tier credit is already available) rather than
technical. Azure was the incumbent in the design docs, had no disqualifying weakness for
any of the four uses, and switching cost was genuinely low at the time this was written —
so there was no engineering reason to force a change. The comparison above was
qualitative service-shape adjudication, not benchmarked pricing or a trial account; it
was superseded by the fact below, exactly as flagged as a possibility at the time.

**What actually decided it:** Aaron already has a live garcia.ltd Azure estate, in active
use for other work (Prix Six's WhatsApp worker) — a storage account (resource group
`garcia`, region `uksouth`, Pay-As-You-Go subscription), an ACR instance, and a Container
Apps environment with the scale-to-zero pattern already proven in production (~£4–10/mo
observed cost, not a paper estimate). This is exactly the kind of "existing
credits/familiarity" fact the original recommendation said should override the
qualitative table if it existed — and it does.

**Existing-estate reuse — lead ruling:** Metropolis's Blob saves get **their own
container** in the existing storage account, never the `whatsapp-session` container
Prix Six's worker uses — different lifecycle, different owner; sharing it would let
Prix Six ops delete Metropolis saves incidentally. The proven scale-to-zero Container
Apps pattern applies directly to use (c) (stateless solver offload) when that tier is
built. Prix Six's hard-won operational lesson carries over to any Metropolis worker
built on this estate: exactly **one instance per stateful session**, or you get
`connectionReplaced` ping-pong.

**M2 review trigger, unchanged:** the concrete first commitment is still the M2
batch-compute choice (use (b), Sprint S8) — that remains the natural point to compare
Azure Batch pricing against alternatives with real numbers if Aaron ever wants to
revisit, rather than a scheduled re-decision. Nothing below requires that revisit to
happen; it is a standing option, not a deadline.

**This was Aaron's decision, not an engineering one** — nothing above was a technical
constraint that locked in a provider; the existing estate is what settled it.

**Switching cost today is low, and stays low until M2.** Nothing cloud-shaped is built
yet. The solver contract is provider-neutral (gRPC + opaque byte payloads); the save path
is provider-neutral (object storage semantics, not an SDK baked into the engine). The
concrete first commitment is the M2 batch-compute choice (use (b), Sprint S8) — that is
the natural decision point to actually compare Azure Batch vs AWS Batch vs GCP Batch
pricing with real numbers, rather than deciding it now on paper.

---

## 3. Secure by design & resilient by design

This is the pattern **every** future remote call must follow — cloud or otherwise. It
becomes contract language the day any cloud item (`cloud.gpu`, `cloud.azure`) actually
builds against the frozen solver contract.

- **Authentication:** short-lived tokens or managed identity only. Never a long-lived key
  committed to config or code — this is the repo-side half of what
  `claude-secret-guard.js` already enforces mechanically (the AWS/GitHub/OpenAI/etc.
  regex bank the opening clarification above describes); the cloud-side half is
  choosing services and auth flows that never require a static secret to sit in a
  config file in the first place.
- **Transport:** TLS everywhere, no exceptions, no "internal network so it's fine."
- **Authorisation:** least-privilege per service — a batch-compute job identity gets
  exactly the storage/queue permissions it needs and nothing else; a solver-offload
  identity never gets save-storage write access, etc.
- **Correlation IDs:** already part of the protocol design (`int.protocol` propagates
  correlation IDs command → phase → delta → log, per the Sprint 0 scope). Any cloud call
  extends the same correlation ID through the request, so a failed cloud solve is
  traceable end-to-end through the same NDJSON logs and F12 panel as a local one — not a
  separate, harder-to-debug failure mode.
- **Retries:** exponential backoff with jitter, budget-bounded (a capped number of
  attempts / total time, never an unbounded retry loop). Safe by construction because the
  solver contract's requests are stateless request/response with explicit request
  identity — idempotent by design, so a retry after a timeout is never a "did it already
  happen?" problem.
- **Rate limiting:** client-side token bucket, and honour server-side throttle signals
  rather than hammering through them.
- **Timeouts:** mandatory on every outbound call — no call without a bound on how long it
  may hang.
- **Failure mode:** the solver contract's mandatory local-fallback rule means a cloud
  outage degrades to local compute (slower, never wrong — determinism requires
  bit-identical results across backends), never to a broken game. This is the single most
  important resilience property in the whole design: cloud is strictly an accelerant,
  never a dependency the game can't run without.

**What exists today:** nothing remote. No cloud call has been written, so there is
nothing cloud-shaped to be insecure yet — the entire section above is what gets enforced
the day the first one is.

**Where this gets enforced going forward:** the BOW item `cloud.netpolicy` (network
policy for any remote call) and the solver contract freeze — once `int.solver`'s freeze
review closes (currently pending, per `docs/design/solver-contract.md`'s "Status:
awaiting freeze review"), a backend that doesn't follow this pattern doesn't get to
register.

---

## 4. Costs — shape, not quotes

Order-of-magnitude honesty only; nobody should budget against these numbers.

- **Blob save sync (a):** trivially cheap — a handful of megabytes per save, occasional
  writes. Rounds to "free tier covers it" for a single-developer/small-player-base
  product.
- **Batch balance-tuning (b):** the real cost line, and the earliest one. Thousands of
  headless runs, each simulating up to hundreds of game-years, on interruptible/low-
  priority compute. Order of magnitude: dozens to low hundreds of pounds per sweep
  campaign on spot-tier pricing, depending on how aggressively parameters are swept and
  how long each run is left to simulate — this needs an actual per-run timing number from
  H-HEADLESS before it's worth pricing more precisely, which is itself an M1/M2 output,
  not something knowable today.
- **Solver offload (c):** near-zero until it's actually triggered (it's gated behind a
  perf-CI breach, not scheduled), then scales with call frequency × solve size — a
  traffic-assignment solve on a ~5,000-zone OD matrix is a sub-second-to-seconds compute
  job, so per-call cost is small; the open question is call *frequency* in the live game,
  which isn't known yet.
- **Cloud citizen-shard tier (d):** the most expensive item on this list if it's ever
  needed, because it implies both storage (tens of GB of citizen state) and compute
  (cold-pass processing) running continuously for a save past the ~20–30M citizen line —
  but it's also the furthest out and the least likely to be needed for most players.
- **GPU instances (far future):** cloud GPU-hours are not cheap per hour, but this tier
  only activates on a measured perf-CI breach, so it is spend triggered by evidence, not
  a standing cost.

## 5. Open questions for Aaron

1. ~~**Does Azure stay, or does a different provider get picked now?**~~ **ANSWERED —
   Azure confirmed, 2026-08-09** (§2). The existing garcia.ltd Azure estate settled it.
2. **What's the actual budget appetite for M2 batch-tuning (use (b))?** Still OPEN. This
   is the first real spend on the roadmap (Sprint S8) and the order-of-magnitude cost
   above can't be tightened until H-HEADLESS produces a real per-run timing — worth
   flagging now so there's no surprise when S8 arrives. The Azure decision doesn't answer
   this; it only fixes which provider's Batch pricing to check once a timing number
   exists.
3. ~~**Is a personal/free-tier account sufficient, or does this want a proper billing
   account with spend alerts from the start?**~~ **ANSWERED — 2026-08-09.** Not a
   personal/free-tier account: the existing garcia.ltd estate is already a proper
   Pay-As-You-Go billing account with production use (Prix Six), reused rather than
   started fresh. Metropolis gets its own storage container within it (§2's
   existing-estate reuse ruling) rather than its own separate account.
