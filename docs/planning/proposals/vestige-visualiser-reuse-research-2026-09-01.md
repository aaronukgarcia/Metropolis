# Vestige Memory-Space Visualiser — Reuse Research (FEAT-2326609715)

**Date:** 2026-09-01
**Scope:** read-only research only. No code written, no BOW verdicts, no writes to the live Vestige store.
**Ask (Aaron):** "deep dive into what the feature already has and how much overlap is there, what can be re-used — let's not reinvent the wheel."

---

## (a) What upstream already has

### Discussion #148 "Neat memory overview" — NOT a reusable visualiser

`https://github.com/samvallad33/vestige/discussions/148` is a three-comment thread, opened 2026-07-26 by user `Jelloeater`, in the "Show and tell" category:

- The opening comment is a **link to an external, third-party site** — `carsteneu.github.io/ai-memory-comparison/` — plus a screenshot of that site's comparison table (an 81-system feature matrix across AI memory tools, Vestige included). It is **not** something built on top of Vestige's own data; it doesn't touch our store, our MCP tools, or our SQLite schema.
- The two follow-up comments (from `jelloeater-agent`) are prose evaluations of where Vestige sits on that external matrix ("Has: FSRS-6 decay, supersede, contradiction detection... Missing: published benchmarks, procedural memory, code graph integration, web/TUI d[ashboard]...").
- **Verdict: nothing here is reusable code or a visualiser artefact.** It's useful only as a pointer that (at the time, pre-v2.3.0) even Vestige's own users rated "web/TUI dashboard" as a gap — which the maintainer then closed with the Cognitive Observatory (below).

### The real find: upstream already ships a mature memory-space visualiser

`apps/dashboard` in the vestige repo is a full SvelteKit 5 + Three.js application, shipped and documented since **v2.3.0 "Cognitive Observatory + Zero-Knowledge Sync"** (2026-07-26):

- Run via `vestige dashboard` → **http://localhost:3927/dashboard** (README, "The dashboard" section). Quote: *"A living WebGPU observatory of your memory... memories appear, link, strengthen, and fade in real time, 1000+ nodes at 60fps. It renders a deterministic 12-second loop of your store's life that you can export as an mp4 with one click, and mints a **brain print**, a signature seeded from your store's shape."*
- Architecture (from `apps/dashboard/src/lib/graph/*` and `OBSERVATORY-SPEC.md`, read via `gh api`):
  - `Graph3D.svelte` — owns the Three.js/WebGL scene: `ForceSimulation`, `NodeManager`, `EdgeManager`, `ParticleSystem`, `EffectManager`, DreamMode, post-processing.
  - `force-sim.ts` — CPU O(N²) force-directed layout (repulsion/attraction/damping/centering).
  - `scene.ts` — Three.js `WebGLRenderer` + `EffectComposer`.
  - `cinema/pathfinder.ts` + `cinema/director.ts` — deterministic "recall path" story-beat generation and camera choreography (already used for a `MemoryCinema` feature at `/graph`).
  - Feature/Field/Classic toggle: browsers without WebGPU fall back to the Classic Three.js renderer automatically.
- **`OBSERVATORY-SPEC.md` (dated after v2.3.0, status "RESEARCH → SPEC ONLY, do not implement until Sam explicitly asks")** describes a *planned* GPU-resident WebGPU upgrade (compute-boids ping-pong buffers, GPU force layout, path-lighting demo mode) layered on top of the existing Three.js substrate — this is forward roadmap, not yet built.
- Package: `@vestige/dashboard` v2.6.1, deps: `three ^0.172.0`, SvelteKit 5, Tailwind 4, Vite 6, Vitest, Playwright for e2e. This is a **separate, standalone web app** with its own dev server and its own build pipeline (`pnpm`-workspace member) — it is not bundled inside the `vestige-mcp*.exe` binaries we already run (the GitHub release assets are platform MCP binaries only; no dashboard artifact is attached to releases). It talks to a local HTTP API (`api.graph(...)` pattern in the spec) served by the Rust core when you run `vestige dashboard`, not to the MCP stdio tools directly.
- **PR #192** (merged ~2026-09-01, "Closes #191... everything Aaron asked for in #191/#190/#182 that can ship in a patch release") is **not** visual — it's the v2.6.0-era fixes: legacy-profile dimension auto-repair, CLI honesty about the embedder, `upgrade --dry-run`, and the new `state` node type with TTL. Confirmed nothing export/visual-shaped shipped in it.
- Release history around it: v2.3.0 is genuinely "the biggest visual change Vestige has had"; v2.4.0–v2.6.0 are all backend/data-integrity releases (decay-curve bugs, consolidation GC bug, Granite embedding profile) — no further visual work landed after the Observatory outside the still-unbuilt WebGPU spec.

**Bottom line on (a):** upstream already solved the "later, a network-graph view" half of FEAT-2326609715 to a standard well beyond what a from-scratch v1 would produce (force-directed 3D graph, live updates, deterministic replay/export, brain-print). It is a separate app we would have to run alongside the MCP server, pointed at the same local store, not a library we can drop into the webconsole. Discussion #148 contributes nothing code-wise.

---

## (b) v1 data needs → best existing source

| v1 data need | Best existing source | What it gives | Polling cost |
|---|---|---|---|
| Block list (all memories) | `mcp__vestige__memory_status` (`view: stats`) `largestNodes`/`neverAccessed` lists (bounded, 50-200 rows) **or** direct read of `knowledge_nodes` in a **copied** SQLite file | id, contentBytes, contentPreview, tags, memoryType, createdAt | `stats` is a single MCP call, capped at ~200 rows per list — fine for a snapshot, not for "show me all 759". Direct SQLite `SELECT` over a copy has no row cap and is the only path to a true full block-list. |
| Block ages | `memory_status stats` → `counts.byAge` (bucketed: 0-7d/8-30d/31-90d/91-180d/181d+) already computed server-side | Pre-bucketed histogram | One call, cheap — no need to compute ages client-side |
| Retention / decay state | `memory_status stats` → `retentionDistribution.buckets` (bucketed 0-20%/20-40%/.../above100%) **or** per-node `retention` field returned by `mcp__vestige__graph` (`action: memory_graph`) | Aggregate distribution (stats) or per-node value (graph) | Both cheap; graph tool caps at `max_nodes` (≤200) |
| Types (fact/decision/pattern/state/...) | `memory_status stats` → `counts.byMemoryType`; per-node `type` field from `graph`/`memory_graph` | Exact counts, and per-node type for colour-coding blocks | Cheap |
| Retention/lifecycle detail (expired, superseded, silent/dormant/unavailable) | `memory_status stats` → `lifecycle` block; SQLite `memory_states` table (four-state accessibility model: Active/Dormant/Silent/Unavailable — **note the v2.6.0 release finding: this table was measured essentially dead/two-state until the decay-curve fix, so treat pre-v2.6.0-repaired stores' Silent/Unavailable counts with suspicion**) | Lifecycle counts; per-node accessibility state | `stats` is one call; the SQLite table needs a direct read |
| Edges / associative links between memories | `mcp__vestige__graph` — `action: memory_graph` (force-directed subgraph, returns `nodes[]` with **x/y already laid out server-side** and `edges[]`) or `action: associations`/`chain`/`bridges` for specific reasoning paths; SQLite `memory_connections` table (`source_id, target_id, strength, link_type, activation_count`) holds the durable edge store | `memory_graph` is the single best source — it already returns computed 2D coordinates, so a v1 visualiser does **not** need to reimplement force-directed layout | `memory_graph` is one MCP call per view, capped `max_nodes` (default 50, max 200) — fine for a v1 static/periodic snapshot, not a live-updating force sim at 1000+ nodes (that's what the upstream Observatory already does) |
| Live access events ("pulses" when a memory is read/written) | SQLite `memory_access_log` table (id, node_id, access_type, accessed_at) — **only 26 rows in the live local store today**, a genuinely small, cheap table to poll on an interval; **no MCP tool surfaces this directly** — `memory_status changelog` view is the closest MCP-level substitute (state-transition audit trail, not raw access events) | Per-event: which node, what kind of access, when | Direct SQLite poll (`SELECT * WHERE accessed_at > :last_seen`) is trivial and cheap; going through MCP would mean shelling `changelog` repeatedly, coarser granularity |
| Ingest / write events | SQLite `knowledge_nodes.created_at`/`updated_at` diffed against last poll, **or** `agent_traces`/`agent_runs` tables (per-run event log: `event_type`, `tool`, `payload`, `seq`) | Precise write timeline including tool that wrote it | `agent_traces` direct read is cheap (6 rows locally); no MCP tool exposes it as such — closest is `receipt`/`memory_status changelog` |
| Recall/retrieval receipts (what was surfaced, why) | `mcp__vestige__receipt` (`action: get`, needs a `receipt_id`) returns the frozen evidence pack + `activation_path`; SQLite `memory_receipts` (2 rows locally) and `retrieval_replay_capsules`/`retrieval_replay_items` back it | Full receipt detail, good for an "inspector panel" on click | One call per receipt — fine for on-demand detail, not for polling |
| Session/run summary (dashboard header stats) | `memory_status` (`view: health` default, or `view: retention`) | avg retention, distribution, trend, FSRS preview, warnings | One call, already what we use for `stats` |

**General cost note:** every MCP tool call here is a single stdio round-trip to the already-running `vestige-mcp-v2.6.0.exe` process (per `~/.claude.json`), so "polling cost" in the table above is really about **row caps and staleness**, not network latency. The **only genuinely cheap live-polling primitive** is a direct SQLite read of a **copied** file (never the live `.db`/`.db-shm`/`.db-wal` — see rules below) on a timer, or, if live-against-the-real-file access is wanted, a **read-only SQLite connection** (`PRAGMA query_only=1` / open-mode `readonly`) against the live path — WAL mode (confirmed present: `.db-wal`/`.db-shm` sidecar files exist) supports concurrent readers safely without blocking the MCP server's writer.

---

## (c) In-house rendering that can be reused

- **`webconsole/src` already has a lightweight, dependency-free SVG charting pattern** — no D3, no canvas library, no Three.js. Confirmed via `package.json`: webconsole's only runtime deps are `react`, `react-dom`, `lz-string`. Root repo's only dep is `mysql2` (hook-script only).
  - `webconsole/src/components/left/Histogram.tsx` — hand-rolled inline `<svg>` with manual scale math (padding, plot width/height, polyline for a trend line, `<rect>` bars) — this is the exact shape a "defrag block view" wants: N rectangles, sized/coloured by data, laid out on a manually-computed grid.
  - `webconsole/src/components/left/Sankey.tsx` / `webconsole/src/components/populationSankeyModel.ts` / `webconsole/src/components/ArrivalsByModeSankey.tsx` — existing flow/graph-shaped SVG rendering (nodes + flow bands) already solved for a different domain (population flows); the same "compute layout in TS, render as plain SVG rects/paths" approach transfers directly to a memory-block or memory-graph view.
  - `webconsole/src/components/Trend.tsx` — another small SVG chart component, same pattern.
- **None of this pulls in a heavyweight graph/canvas library.** A v1 "defrag-style block view" (rectangles sized by content bytes, coloured by type/retention, tooltipped on hover) is directly buildable with the same idiom already in the codebase — no new dependency needed.
- A v1 **network-graph** view is also buildable in plain SVG using the `x`/`y` coordinates `mcp__vestige__graph` (`memory_graph` action) already computes server-side — we do not need to port or reimplement force-directed layout; we only need to draw circles+lines from data the tool already returns.
- **`tools/vestige/`** (the just-landed lesson-ingest/ruling-ingest modules, `tools/vestige/backfill-rulings.js` + `ruling-ingest.js`) — these are MariaDB-backed (via the shared `claude-db.js`/`mysql2` helper) and **not directly reusable for SQLite access**, but the *pattern* is: a small, explicitly-documented, read-only Node script that SELECTs from the source of truth and writes a reviewable JSON artefact, never mutating the source. That pattern (read-only scanner → reviewable snapshot) is exactly right for a first cut at "dump the memory store to a JSON block-list for the visualiser to render," and should be copied in shape (not code) for a Vestige-SQLite equivalent.

---

## (d) Node/SQLite access options (no driver currently installed)

- Neither root `package.json` nor `webconsole/package.json` has any SQLite dependency (`better-sqlite3`, `sqlite3`, etc. — confirmed absent).
- No `sqlite3` CLI binary found on PATH in this environment (only `sqlite3_analyzer.sh` under mingw64, not the CLI itself).
- **This machine's Node is v25.3.0**, which ships **`node:sqlite`** (the `DatabaseSync` built-in) — confirmed working in this session (used it to read the copied `vestige.db`: 68 tables enumerated, schema + row counts pulled with zero new dependencies). It carries an `ExperimentalWarning` ("might change at any time"), and its stability across the Node version actually used by the webconsole's own tooling/build (check `webconsole`'s own Node engine pin before committing to this) needs a one-line verification before relying on it in shipped tooling.
- **Recommendation for v1:** use `node:sqlite` (zero new dependency, already proven to work against the real file in this session) for any backend/tooling-side SQLite reads, opened `readOnly: true` against either a `VACUUM INTO`-style copy (safest, mirrors what `vestige-cli backup`/`upgrade --dry-run` already do upstream) or a direct read-only handle on the live WAL-mode file (acceptable since WAL supports concurrent readers, but copy-first is strictly safer and matches this task's own ground rules).

---

## (e) Revised effort estimate given reuse findings

The original FEAT-2326609715 framing ("defrag-style block view now, network-graph view later") assumed both would be built from scratch. Reuse changes the picture substantially:

| Layer | Original assumption | Revised, given reuse |
|---|---|---|
| Block-view v1 (ages/types/retention as rectangles) | New component + new layout math | **Small.** `memory_status stats` already returns every bucketed/aggregated field needed (byAge, byMemoryType, retentionDistribution, lifecycle, largestNodes) in one call; rendering is a direct copy of the `Histogram.tsx`/`Sankey.tsx` SVG-rect idiom already in webconsole. Net new code: one data-fetch hook + one SVG component. |
| Live access pulses | Assumed needs new instrumentation | **Small-medium.** `memory_access_log` is a tiny, already-populated table; a direct read-only SQLite poll (via `node:sqlite`, zero new deps) on a short interval is all that's needed — no upstream or in-house instrumentation gap to fill. |
| Network-graph view ("later" phase) | Assumed built from scratch, including force-directed layout | **Reframe as build-vs-extend, not build-from-scratch** (see (e) below) — `mcp__vestige__graph`'s `memory_graph` action already returns laid-out nodes+edges; a v1 SVG network view is a small consumer of that, not a layout-engine project. If the ambition is upstream's live, 1000-node, 60fps, exportable Cognitive Observatory experience, that is **already built and shipped** (v2.3.0+) as a separate local web app (`vestige dashboard` on :3927) — pointing Aaron at that first, rather than rebuilding it, is very likely the right call. |
| SQLite driver plumbing | Assumed needed a new npm dependency | **Zero.** `node:sqlite` (Node 25, already the machine's runtime) covers it with no new dependency, confirmed working against the real file. |

**Net effect:** the "block view" v1 shrinks from a multi-day new-subsystem build to roughly a small feature slice (one data source already returns pre-aggregated everything; one rendering idiom already exists in-house). The "network-graph, later" phase either shrinks the same way (if we just want an in-webconsole SVG view fed by the `graph` MCP tool) or should be **descoped entirely in favour of running upstream's existing `vestige dashboard`** if the ambition is the full living, animated, exportable experience — building that ourselves would be reinventing a shipped, actively-developed (v2.3.0→v2.6.1, with a further WebGPU upgrade already spec'd) open-source app.

---

## (f) Recommendation: build vs extend

1. **Block-view v1: build small, in-house, in the webconsole**, using the existing SVG-rect idiom (`Histogram.tsx`/`Sankey.tsx` pattern) fed by `mcp__vestige__memory_status` (`stats`) plus a direct read-only `node:sqlite` poll of `memory_access_log` for live pulses. This is genuinely new, small work — upstream doesn't have anything shaped like a "defrag block view" (their visual investment went entirely into the 3D force-graph/cinema direction).
2. **Network-graph "later" phase: do NOT build from scratch.** Two honest options, both cheaper than a from-scratch build:
   - **(2a) Extend-light:** a small in-house SVG network view inside the webconsole, fed directly by `mcp__vestige__graph`'s `memory_graph` action (nodes already have x/y, edges already resolved) — appropriate if the ask is "a simple graph panel alongside the block view," not a standalone spectacle.
   - **(2b) Point at upstream:** if the ask is closer to "a living, animated map of the whole memory," that is `vestige dashboard` (localhost:3927) today, already shipped and still being actively extended upstream (the WebGPU Observatory spec). Aaron should be shown this running before any new-build decision is made for the network-graph phase — it may fully satisfy that half of the feature with zero engineering.
3. Either way, **do not attempt to fork or vendor `apps/dashboard`** — it's a large, actively-developed, separately-versioned SvelteKit/Three.js app (AGPL-3.0 licensed) with its own build pipeline; treating it as a dependency to run alongside the MCP server (like Aaron already runs `vestige-mcp*.exe`) is far cheaper and stays in sync with upstream fixes automatically.

---

## Open questions (for Aaron)

1. Has Aaron actually run `vestige dashboard` (localhost:3927) yet? If option 2b already satisfies the "network-graph view, later" half of the feature, that phase of FEAT-2326609715 may be closeable as "use upstream" rather than built.
2. Is the "defrag-style block view" meant to live **inside the Metropolis webconsole** (as this research assumed, matching its existing SVG-chart idiom) or as a **separate standalone tool** — the answer changes whether `node:sqlite` needs to be added to `webconsole/package.json` or just to a root-level tooling script.
3. Does the block view need to be genuinely **live** (sub-second pulses on every recall/ingest), or is a periodic snapshot (e.g. every few seconds via `memory_status stats` + an access-log poll) sufficient for v1? This materially changes whether direct SQLite polling is required at all, or whether MCP calls alone suffice.
4. AGPL-3.0 is upstream's licence (confirmed from the README badge) — if any upstream code/assets end up being adapted rather than just observed, licence compatibility with Metropolis's own repo needs a decision before anything is copied in (current recommendation above avoids this entirely by not vendoring any upstream code).
5. `node:sqlite`'s experimental status (Node's own warning) — is Aaron comfortable depending on it for shipped tooling, or should v1 instead shell out to a bundled `sqlite3` CLI (not currently present on this machine and would need installing) to avoid the experimental-API risk?

---

## Appendix: local store schema snapshot (2026-09-01, read from a COPY of the live file — never the live `.db`)

68 tables in the live `vestige.db` (v2.6.0 schema). Directly relevant to a visualiser, with live row counts at inspection time:

- `knowledge_nodes` (759 rows) — the memory blocks themselves: `id, content, node_type, created_at, updated_at, last_accessed, retention_strength, activation, protected, superseded_by, tags, domains, source_system, ...`
- `memory_access_log` (26 rows) — `id, node_id, access_type, accessed_at` — the cheapest live-pulse source.
- `memory_connections` (0 rows in this store today) — `source_id, target_id, strength, link_type, activation_count` — durable edge store (currently empty locally; `mcp__vestige__graph`'s live edge computation does not appear to depend on this table being populated, since `memory_graph` still returned data with 0 edges for a 1-node query).
- `memory_states` (0 rows) — the four-state accessibility model (Active/Dormant/Silent/Unavailable) — **per the v2.6.0 changelog this was effectively dead/two-state until the recent decay-curve fix**, so don't trust it as a rich source yet.
- `memory_receipts` (2 rows), `retrieval_replay_capsules` (2 rows) / `retrieval_replay_items` (12 rows) — receipt/replay detail, matches the `receipt` MCP tool's `get`/`replay` actions.
- `agent_runs` (4 rows) / `agent_traces` (6 rows) — per-run event stream (`event_type`, `tool`, `payload`, `seq`) — a plausible richer ingest-event source than diffing `knowledge_nodes.created_at`.
- `insights` (3), `intentions` (4), `consolidation_history` (1), `retention_snapshots` (1), `embedding_profile_*` (profile/vector bookkeeping, 759 vectors) — background/administrative tables, lower priority for a visualiser.
