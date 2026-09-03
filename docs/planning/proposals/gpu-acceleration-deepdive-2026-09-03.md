# GPU Memory & Compute for Game Resource Management — Deep Dive (planning only)

**Date:** 2026-09-03
**Scope:** planning/research only. No code written, no dependencies added, no BOW verdicts required for this document (docs-only, GR#23 proportionality tier).
**Ask (Aaron):** "plan a deep dive into using the GPU memory more for game resource management and faster processing."
**Trigger context:** BUG-622 ("Aaron's live dogfood city, 1.4M population / ~13k buildings, wedged the main thread for minutes on load"), the FEAT-2326609759 scale gate that now bounds steady-state cost at that fixture size, and the already-in-flight FEAT-webworker-sim-offload effort that moves *tick computation* off the main thread onto a Web Worker.

---

## 0. What "GPU" would mean here, concretely

The webconsole today has **zero GPU-facing code and zero GPU-facing dependencies**. `webconsole/package.json` runtime deps are exactly `lz-string`, `react`, `react-dom` (confirmed 2026-09-03 read) — no `regl`, `pixi.js`, `three`, `twgl.js`, WebGL, or WebGPU anywhere in the tree. Every pixel drawn today goes through the 2D Canvas API (`CanvasRenderingContext2D`) in `webconsole/src/components/MapView.tsx`. This document is therefore a **from-zero** proposal, not a tuning pass on existing GPU code.

---

## 1. Where the GPU can help TODAY (rendering)

### 1.1 What MapView.tsx actually does, today, per redraw

`webconsole/src/components/MapView.tsx`'s single draw `useEffect` (lines 241-650) is one big imperative 2D-canvas painter, re-run whenever its dependency array changes:

- **The redraw is timer-forced, not just tick-forced.** `webconsole/src/components/MapView.tsx:172-175` runs `setInterval(() => setFrame((f) => f + 1), 50)` unconditionally for the component's whole lifetime — a bare `frame` counter in the draw effect's own dependency list (`MapView.tsx:650`). That means the canvas repaints **20 times a second, forever**, whether or not the sim is paused, whether or not anything visible changed (trains/flash overlays are the only things that actually need the 50ms cadence — see FEAT-1972079902's comment at `MapView.tsx:536-542`).
- **The main building loop is one `for (const b of state.buildings)` with 4-7 `ctx.fillRect`/`ctx.strokeRect` calls per building** (`MapView.tsx:290-388`): base fill, optional occupancy under/over-fill (2 rects, `:307-317`), optional utilisation bar (`:324-337`), offline hatch strokes (`:339-348`), multi-tile border stroke (`:349-353`), density-tier stroke (`:357-362`), selection outline, optional ref-id label (measureText + 2 fills, `:373-387`).
- **On top of that single pass, up to 5 more full-or-partial `for (const b of state.buildings)` scans run in the same effect, every redraw**: the disconnected-road flash pass (`:402-413`, full scan, no cache), the water overlay (`:417`, uses the `overlaySubsetsOf` cache), the power overlay dim pass (`:458-468`, explicitly documented as "still O(all buildings) by necessity"), the line-saturation overlay (`:498`, cached subset), the rail/train tile classification pass (`:546-561`, full scan every redraw to feed `buildRailGeometry`), and the station-dot pass (`:521-534`, full scan). The `overlaySubsetsOf` WeakMap cache (`MapView.tsx:92-113`, added for BUG b2d31bc7 FIX 5) already killed 3 of what would otherwise be *more* full scans — the file's own history shows this exact pattern (many full-building passes per frame) has already needed one optimisation round.
- At the BUG-622/scale-gate fixture size (**13,000 buildings**, `test/scale/fixture.mjs:55`), that is conservatively **13,000 × ~3 draw calls (main loop, non-overlay case) × 20 fps ≈ 780,000 Canvas2D API calls/second**, climbing well past 1M/s the moment any overlay (water/power/lines) is toggled on, since those add a second or third full pass. Every one of those calls is a synchronous main-thread call into the browser's own software/GPU-backed 2D rasteriser — there is no batching, no instancing, and no way for the browser to coalesce 13,000 unrelated `fillRect` calls into one GPU draw call, because Canvas2D's API contract is inherently per-shape, immediate-mode.
- **This is very plausibly the dominant contributor to the "2-minute day" symptom BUG-622 reports**, alongside (not instead of) the load-path/tick-cost issues the scale gate and FEAT-webworker-sim-offload are separately chasing: `scale-gate.test.mjs`'s own bound derivation measured **steady-state tick cost at 16-27ms locally / ~54ms on CI** at 13k buildings, and render-path derivation cost (wellbeingOf+serviceCoverageOf+demandFixPlan) at **7.5-9.9ms locally / ~61ms on CI** (`test/scale-gate.test.mjs:31-49,77-87`) — those numbers are all *data derivation*, not paint. Nothing in this repo has yet measured MapView's own canvas paint cost at 13k buildings; that is Phase 0 below, and it is the single most important number this whole proposal needs before committing further.

### 1.2 The instanced-rendering opportunity

Every building on the map is one of a small, closed catalogue of specs (`SPECS` in `webconsole/src/sim/data.ts`) drawn as an axis-aligned rectangle at `(b.x, b.y)` sized `sp.w × sp.h`, tinted by `sp.color` and a handful of per-building scalars (online/offline, occupancy fraction, utilisation ratio, tier). That is exactly the shape WebGL2/WebGPU instanced rendering exists for:

- **One quad geometry, N instances.** A single unit-quad vertex buffer, drawn `buildings.length` times via `gl.drawArraysInstanced` (WebGL2) or an equivalent WebGPU instanced draw, with **per-instance attributes** (position, size, colour, occupancy fraction, alpha) read from a single instance buffer rather than re-issued as N separate draw calls. This turns 13,000+ draw calls into **one**.
- **GPU buffers updated only on placement, not per tick.** The instance buffer's static fields (spec id → size/base colour, x/y position) change only when a building is placed, moved, or bulldozed — i.e. on a `place`/`placeMany`/`bulldoze`/`relocate` dispatch, which is rare relative to the 20fps repaint cadence. Only the *dynamic* per-instance fields that the sim tick can change (online/offline flag, occupancy fraction, utilisation ratio, density tier — all already computed once per tick by `isOnline`/`blockOccupancy`/`utilisationOf`/`densityTier` in `data.ts`) need a re-upload, and only on tick, not on every 50ms `frame` bump that currently forces a full CPU re-walk and re-paint today.
- **Camera transform on-GPU.** `MapView.tsx`'s `geom.s/ox/oy` (pan/zoom transform, computed once per render in the `geom` `useMemo` at `:209-213`) becomes a single small uniform/mat3 uploaded once per frame instead of being baked into every `fillRect`'s pixel coordinates on the CPU (`px = geom.ox + b.x * geom.s`, computed 13,000+ times per redraw today at `:294-295` etc.). Panning/zooming becomes a uniform update, not a full re-walk of `state.buildings`.
- **Realistic expected win (Phase 1, order-of-magnitude, to be confirmed by Phase 0 measurement, not assumed):** collapsing "N draw calls + N×(pw,py,fillStyle) CPU computations per frame" into "1 draw call + a camera-uniform update per frame, with instance-buffer uploads only on placement" is the textbook case instanced rendering exists for and routinely turns tens-of-thousands of draw calls into single-digit-millisecond frames on integrated GPUs — but this repo has no existing WebGL/Canvas2D bake-off measurement of its own to cite, so Phase 0 (§5) must produce one before Aaron is asked to commit engineering time past a spike.

### 1.3 Library choice: regl vs twgl vs raw WebGL2 vs WebGPU

None of these are in the dependency tree today, so the choice is genuinely open:

| Option | Fit for this codebase | Risk |
|---|---|---|
| **Raw WebGL2** (no library) | Zero new dependency (matches the project's current near-zero-dependency posture: `lz-string`+`react`+`react-dom` only) — but instanced-attribute buffer/VAO boilerplate is real and easy to get subtly wrong. | Medium effort, zero supply-chain risk, but more code to review/maintain in-house. |
| **twgl.js** | Thin (~20KB) helper library that removes WebGL boilerplate (buffer/attribute setup, uniform setting) without imposing a scene-graph or renderer abstraction — closest in spirit to the current "no framework, own the render loop" style already in `MapView.tsx`. | Low effort, tiny dependency, no scene-graph lock-in. **Front-runner** for a codebase this size and this dependency-averse. |
| **regl** | Functional/declarative WebGL wrapper, good ergonomics for instanced draws, but brings its own state-management model that would sit awkwardly next to the existing imperative canvas-effect style. | Medium effort, adds a real abstraction layer to learn/maintain. |
| **pixi.js** | Full 2D scene-graph renderer (WebGL/WebGPU-backed) with built-in sprite batching/instancing, culling, and text — would likely subsume most of MapView's manual draw code. | Largest dependency (~500KB), biggest behavioural change, but also the least custom code to maintain long-term. Worth a real bake-off against twgl in Phase 0/1, not dismissed outright. |
| **WebGPU direct** | Newest API, best long-term compute story (see §3), but browser support in 2026 is not yet universal (see §3's support matrix) and the API is more verbose than WebGL2 for a simple instanced-quad case. | Higher effort now for a capability (compute shaders) this codebase doesn't need for rendering alone yet. Better framed as a *Phase 3+* target once §3's display-compute case is real, with WebGL2 instancing as the near-term rendering win. |

**Recommendation for Phase 1:** WebGL2 + twgl.js (or a from-scratch minimal instancer if Aaron prefers zero new runtime deps) for the rendering layer; hold WebGPU for the compute case in §3, once there is a concrete display-only derivation worth moving.

### 1.4 Progressive-enhancement fallback — non-negotiable

GR#27 (Capture Before Wipe) and the project's general "dogfood must never break" posture (Aaron's directive, `docs/planning/northstar.md`) both point the same way here: **the existing Canvas2D path must remain the fallback for any browser/GPU combination that can't do WebGL2 instancing**, not be deleted. Concretely:

- Feature-detect at mount (`canvas.getContext('webgl2')` returning non-null, plus a check for `ANGLE_instanced_arrays`/native ES3 instancing support) and fall back to the exact current Canvas2D path when unavailable — a pure capability check, no user-visible toggle needed for this reason alone, though a manual override (§6) is worth offering during rollout.
- **This must be flag-gated regardless of feature-detection** (see Phase 1 in §5) — a WebGL renderer is new code with new failure modes (context loss, driver bugs, out-of-memory on integrated GPUs at high building counts) that the existing Canvas2D path has never had, and Aaron's dogfood city is exactly the worst case to discover a WebGL edge case in blind.
- **Context-loss handling is mandatory, not optional**, if this ships: WebGL contexts can be lost at any time (driver crash, GPU memory pressure, tab backgrounding on some platforms) and the `webglcontextlost`/`webglcontextrestored` events must be wired to fall back to Canvas2D gracefully rather than leave the map blank — this is a known sharp edge for any WebGL-in-a-long-running-SPA use case and this app's multi-hour dogfood sessions are exactly the profile that will hit it eventually.

---

## 2. GPU memory as resource storage

### 2.1 What stays canonical on CPU vs what lives GPU-side

The determinism constraint (GR#21: byte-identical replay) draws a hard line here, and it must be drawn conservatively:

- **Canonical, deterministic sim state stays exactly where it is today: CPU-side, in `SimState.buildings` (plain JS objects/arrays), mutated only through the `reducer`/`runTick` path** (`webconsole/src/sim/engine.ts`), journaled and replayed byte-for-byte. Nothing in this proposal touches that. The GPU never becomes a second source of truth for anything the journal/save format persists or the reducer reads back.
- **What can legitimately live GPU-side is a *render-side mirror*:** a Structure-of-Arrays (SoA) typed-array representation of the subset of building fields the renderer needs — `x`, `y`, `w`, `h`, a colour index/RGBA, an online flag, an occupancy fraction, a utilisation ratio, a density tier — built from `state.buildings` + `SPECS` + the existing per-building derivations (`isOnline`, `blockOccupancy`, `utilisationOf`, `densityTier`, all already pure functions of `SimState` in `data.ts`). This mirror is **display-only**: destroying it and rebuilding it from `state.buildings` at any time must be lossless and must never be read back into the reducer.
- This is the same discipline the RNG-stateless-serialization Vestige memory already establishes for a different subsystem (`det.NewStream` per-draw in the Go engine means serialized state needs only data, not RNG state) — the general principle "the deterministic core owns only the data the replay contract needs; everything else is a derived, rebuildable view" applies directly to a GPU mirror too.

### 2.2 Sync discipline: dirty-range uploads on placement

- **On `place`/`placeMany`/`bulldoze`/`relocate`/`stampRegion`:** these are the only actions that change `state.buildings.length` or any building's static (x/y/spec) fields. The render-side mirror's static SoA arrays get a **dirty-range upload** — `gl.bufferSubData`/WebGPU `queue.writeBuffer` at the specific instance index/range that changed, not a full re-upload of all 13,000+ instances' worth of data. Appends (new buildings) extend the buffer; the existing `overlaySubsetsOf`/`memoOnState` WeakMap-cache idiom already used throughout `data.ts` and `MapView.tsx` (`memoOnState` at `data.ts:2267-2275`, `overlaySubsetsCache` at `MapView.tsx:92-113`) is the precedent to follow: key the "has this been uploaded" cache on the same `state.buildings` array-identity discipline the rest of the codebase already relies on (immutable-per-tick arrays, new reference on any change).
- **On tick:** only the *dynamic* fields (online flag, occupancy, utilisation, density tier — all recomputed once per tick by the existing `data.ts` functions) need a re-upload, and only for buildings whose values actually changed since the last tick if a cheap diff is worth the complexity (a first cut can simply re-upload the whole dynamic-fields buffer every tick — at 13k buildings × ~4 floats × 4 bytes ≈ 208KB, a `bufferSubData` of that size is not itself the bottleneck; the current *36,000+ CPU draw-call* cost is the bottleneck this whole section exists to remove, so don't over-engineer the upload diffing before Phase 0 proves it matters).
- **Never upload on the 50ms `frame` timer alone.** That timer exists today only to animate trains/flashes (§1.1) — those are exactly the things that *should* stay driven by a lightweight per-frame uniform (e.g. a `uTime` uniform driving the sinusoidal flash/train-position shader math already computed in `trains.ts`'s `trainPositions`), not a full instance-buffer re-upload.

---

## 3. GPU compute for derivations

### 3.1 The honest split: display-only vs sim-affecting

This is the section where the determinism tension is sharpest, and it must be named plainly rather than glossed over.

**Display-only, safe for GPU float math (candidates):**
- Coverage/utilisation *visualisation* — e.g. rendering the existing `serviceCoverageOf`/`utilisationOf` results as a heatmap tint, once those values are already computed CPU-side deterministically. The GPU only *renders* a value it did not derive.
- Traffic/congestion *visualisation* — colour-coding the line-saturation overlay (`lineUsageOf`, already `memoOnState`'d) or a future road-tile density texture, again rendering an already-CPU-derived number.
- A genuinely GPU-computed *display-only* aggregate — e.g. a density/heatmap texture built by rendering all building footprints additively into an off-screen framebuffer (a "splat and blur" GPU technique) purely to drive a visual glow/heat effect that **feeds nothing back into `SimState`, `demandOf`, `wellbeingOf`, or any reducer path**. This is the one case where GPU-side float reduction is fine specifically *because* its output is provably a dead end for the sim — it never reaches a `dispatch`.

**Sim-affecting, must stay CPU/deterministic (non-negotiable, named specifically):**
- **The wellbeing → move-out chain is the flagship example and must be called out by name.** `wellbeingOf` (engine.ts, via the `memoOnState`'d `buildWellbeingCoreParts` at `engine.ts:4423-4424`) folds over `s.buildings` to produce `wbOverall`, which directly drives `moveOutRate` in `advance()`'s population-growth step (`engine.ts:1293,1313-1315`: `MOVE_OUT_BASE_RATE * (1 + WELLBEING_MOVEOUT_FACTOR * (100 - wbOverall) / 100)`), which drives `moveOuts`, which drives `population`, which is persisted to the journal and replayed. **This entire chain must never move to a GPU reduction.** Any GPU float sum over N buildings can produce a different bit pattern than a CPU sum over the same N values depending on reduction order/tree shape (GPU work-group reduction trees do not guarantee the same left-to-right associativity as a CPU `for` loop) — GR#21's byte-identical-replay contract would break the instant this path touched a GPU shader, and it would break *silently*, showing up only as an eventual replay-divergence bug report, exactly the failure mode GR#21 exists to prevent.
- **By the same reasoning, every other reducer-path fold stays CPU:** `serviceCapacityAggregates` (`data.ts:2298`), `countByKindOnline` (`data.ts:1750`), `computePowerStats`, `demandOf`, `computeFlows`, `crimeRateOf` — all of these feed `advance()`/the reducer, all are already `memoOnState`'d single CPU passes, and none of them are candidates for GPU compute under this proposal. The GPU compute case in this document is deliberately narrow: **rendering-adjacent, display-only aggregates that provably never re-enter the reducer**, full stop. If a future feature wants a GPU-accelerated *sim* derivation, that requires either (a) a fixed, order-independent reduction algorithm proven bit-identical across CPU and GPU (a much harder, separate research project — pairwise/Kahan summation schemes exist but need per-platform validation), or (b) accepting the derivation into a documented "engine.ts vs webconsole may diverge here" carve-out, which no one has proposed and this document does not recommend.

### 3.2 WebGPU compute shaders vs WebGL2 transform feedback

For the narrow display-only case in §3.1:

- **WebGL2 transform feedback** can do simple GPU-side reductions/transforms (e.g. computing per-tile density from per-building positions) without a full compute-shader API, and has the widest 2026 browser support of the two (see §3.3). Adequate for Phase 3-scale ambitions (a density/heat texture, not a general compute platform).
- **WebGPU compute shaders** are the more capable, more future-proof option (general-purpose compute, storage buffers, work-group shared memory) but with a narrower 2026 support footprint. Worth adopting only once a concrete display-only workload (e.g. a density-texture build for the LOD case in §4) actually needs compute-shader generality that transform feedback can't express — not speculatively.
- **Recommendation:** if/when a Phase 3 display-compute need materialises, prefer WebGL2 transform feedback first (narrower API, wider support, reuses the same context as the Phase 1 instanced renderer) and only reach for WebGPU compute if the workload genuinely needs it.

### 3.3 Browser support matrix (2026)

| API | Chrome/Edge | Firefox | Safari |
|---|---|---|---|
| WebGL2 (rendering + transform feedback) | Full support since 2019-era Chrome 56 | Full support since Firefox 51 | Full support since Safari 15 (2021) |
| WebGPU | Shipped (Chrome 113+, 2023) | Behind experimentation as of late 2025/early 2026 per public tracking (Nightly flag historically; GA timing not confirmed here — **verify against caniuse.com at implementation time**, do not trust this document's snapshot for a ship decision) | Shipped in Safari 18 (2024) on macOS/iOS; per-platform rollout should be re-checked at implementation time |

**Practical read for this project:** WebGL2 is safe to assume universally available on any browser this project already targets for the webconsole today (no browser-support floor is documented elsewhere in this repo to check against, so this assumes parity with whatever the existing dev/dogfood browser is — confirm with Aaron, see §6). WebGPU is close to universal on Chromium and Safari but Firefox's status should be re-verified at implementation time, not assumed from this document.

---

## 4. The 100M path

- **The Go engine (`internal/`, per `CLAUDE.md`) is, and remains, the real 100M-citizen carrier.** Option B (no culls, up to 100M at adaptive fidelity) is a server/simulation-side commitment about *what the deterministic model tracks*, not about what any client renders. Nothing in this proposal changes that split, and nothing here proposes moving simulation authority to a browser tab.
- **What the webconsole can realistically host at that scale is aggregation, not instancing.** Instanced rendering (§1) scales well into the tens of thousands of discrete objects — genuinely rendering 100M individual citizen or even building instances as separate GPU instances is not a sane target (100M instance-buffer entries alone, at even 16 bytes/instance, is 1.6GB of GPU-resident data before a single pixel is drawn, ignoring the fill-rate cost of drawing that many primitives even instanced). The correct client-side approach at that scale is **LOD (level-of-detail) density textures**: bucket citizens/buildings into a coarse spatial grid, accumulate per-cell counts/aggregates (population density, average wellbeing, congestion) into a texture (built either on CPU and uploaded, or via the GPU transform-feedback/compute path in §3.2 once that display-only aggregate exists), and render that texture as a heatmap/density overlay rather than as discrete instances. This is exactly how every real-world "render a million+ entities" client (traffic simulators, epidemiological dashboards, population-density maps) solves the same problem, and it composes cleanly with the existing map: individual buildings render as instances (§1) at the zoom levels where they're legible, and the view degrades to a density texture at zoom levels/population scales where individual instances would be illegible noise anyway.
- **How Azure backend + GPU client complement each other:** per the existing Azure-epic Vestige memory (`metropolis-state-2026-08-31-azure.md`), the backend-hosting work is about *where the deterministic sim runs and persists* (server-authoritative ticks, snapshot cadence, multi-client hosting per the `metroserve`/`compose` work visible in this session's own git log). A GPU-accelerated client is a **consumer** of that server's state stream, not a competing authority — the server sends the (already deterministic, already-computed) building/aggregate deltas, and the client's GPU-resident mirror (§2) is rebuilt/updated from those deltas exactly the way it would be rebuilt from a local `SimState`. This proposal does not require or assume any change to that server/client split; it is purely about what the client does with the state it already receives.

---

## 5. Phased plan

Every phase names its determinism guarantee explicitly — per GR#21, any phase whose guarantee cannot be stated in one sentence should not proceed.

### Phase 0 — Measure (no code shipped, spike-only)

- **Goal:** get a real number for MapView's own paint cost at the BUG-622/scale-gate fixture size (13k buildings), the way `scale-gate.test.mjs` already got real numbers for tick cost and derivation cost. Today nothing measures Canvas2D paint time in isolation — the scale gate explicitly only covers `reducer`/`wellbeingOf`/`serviceCoverageOf`/`demandFixPlan` (`test/scale-gate.test.mjs:160-186`), not the render path.
- **How:** instrument the existing draw effect with `performance.now()` brackets (temporarily, or as a permanent dev-only counter gated behind a flag) against `buildScaleFixture()`'s output rendered through the real `MapView` component (jsdom/headless-Chrome via the existing test infra, or a manual dogfood-session profile capture — Aaron already has the real 1.4M-pop city to profile directly).
- **Acceptance shape:** a written number (median ms/frame at 13k buildings, with/without each overlay toggled) attached to BUG-622's own thread, establishing whether §1's diagnosis ("canvas paint is the dominant BUG-622 contributor") is right before any rendering code is written. If Phase 0 shows canvas paint is *not* the dominant cost, the rest of this plan is deprioritised in favour of whatever Phase 0 actually finds.
- **Risk:** low — this is instrumentation only, no behaviour change.
- **Determinism guarantee:** trivial — no sim-affecting code touched; a `performance.now()` timer is never part of the journal or replay.

### Phase 1 — Instanced map layer behind a flag

- **Goal:** WebGL2-instanced rendering of the building layer only (not overlays yet), feature-detected + explicitly flag-gated (mirroring the existing `webWorkerFlag.ts` precedent from FEAT-webworker-sim-offload — same "new subsystem ships behind an opt-in flag, defaults off, Canvas2D stays the shipped default until proven" pattern), with automatic Canvas2D fallback on missing WebGL2/instancing support or context loss.
- **Acceptance shape:** the flagged path renders visually equivalent output (building position/size/colour/occupancy) to the current Canvas2D path at a handful of building counts (small fixture, the 13k scale-gate fixture), verified by a snapshot/pixel-diff or a manual dogfood comparison; Phase 0's measured baseline improves by a documented factor with the flag on.
- **Risks:** context-loss handling (§1.4) genuinely not exercised until a real multi-hour dogfood session hits it; a new rendering code path is a new place for a visual bug to hide (GR#27's capture-before-wipe discipline is unaffected — this never touches sim state); library choice (twgl vs raw vs pixi, §1.3) needs a real short bake-off, not just this document's recommendation.
- **Determinism guarantee:** the rendered pixel is a pure function of already-deterministic `SimState` + `SPECS` — this phase adds *how* a value is painted, never *what* value is computed. No journal/replay path is touched.

### Phase 2 — Typed-array mirrors + dirty uploads

- **Goal:** replace the "rebuild everything from `state.buildings` every relevant redraw" approach with the SoA typed-array mirror + dirty-range upload discipline from §2.
- **Acceptance shape:** instance-buffer uploads measured (via the same `performance.now()` instrumentation from Phase 0) to scale with *changed* buildings/ticks, not with total building count — i.e. placing one building on a 13k-building city costs roughly the same as placing one building on a 100-building city.
- **Risks:** getting the dirty-range bookkeeping wrong (an unflagged dirty range = a stale visual, not a sim bug, since the mirror is display-only — lower blast radius than a reducer bug, but still a real dogfood-visible defect class to test for).
- **Determinism guarantee:** identical to Phase 1 — the mirror is provably display-only and rebuildable; a bug here can make the map look wrong, never make the sim compute wrong, and this must be provable by construction (the mirror has no write path back into `SimState`).

### Phase 3 — Display-compute shaders

- **Goal:** move a concrete display-only aggregate (starting candidate: a density/heat overlay, per §3.1/§4) onto WebGL2 transform feedback (or WebGPU compute if a concrete need for its extra generality has emerged by then, per §3.2).
- **Acceptance shape:** the GPU-computed display value is checked, in a test, against the equivalent CPU-computed value at a tolerance appropriate for a *visual* effect (not bit-identical — nothing here needs bit-identity because nothing here is sim-affecting, which is precisely the point of §3.1's split) — and a written assertion/comment in that test naming which `SimState`-derived reducer path this value is proven **not** to feed (mirroring the discipline `wellbeingCoreOf`'s own doc comment already uses to prove it can't recurse into `crimeRateOf`, `engine.ts:4408-4422`).
- **Risks:** this is the phase where the determinism tension in §3.1 is easiest to violate by accident — a future feature request ("can wellbeing consider the heatmap?") must be refused or routed back through a CPU-computed equivalent, not satisfied by reading the GPU texture back into the reducer. This risk should be named in the PR/BOW item explicitly, not left implicit.
- **Determinism guarantee:** must be stated per-workload at the time each one lands; the blanket rule from §3.1 ("provably display-only, never re-enters the reducer") is the acceptance bar, and "provably" should mean a code-level fact (no import/call path from the GPU-fed component into `dispatch`/`reducer`/`advance`), not just a comment's promise.

### Phase 4 — LOD/density at 1M+

- **Goal:** the §4 density-texture approach, wired to real backend-streamed state at the scale where per-building/per-citizen instancing stops being viable (well above the current 13k/1.4M dogfood ceiling — this phase is explicitly aimed at the Go engine's 100M-citizen ambition, not at today's ceiling).
- **Acceptance shape:** a viewer can navigate a city at population/building counts far beyond individual-instance rendering limits, with the view degrading gracefully from instances → density texture as zoom/scale crosses a documented threshold.
- **Risks:** this phase depends on backend-streaming work (Azure epic) that is its own large, separately-scoped effort — this phase should not be started until that dependency is real, not speculative.
- **Determinism guarantee:** identical framing to Phase 3 — the density texture is a rendering-only aggregate of already-computed (server-side, deterministic) data; the client never originates a value that affects sim state.

---

## 6. Open questions for Aaron

1. **Hardware/browser target.** What GPU(s) and browser(s) should Phase 0's measurement and Phase 1's bake-off actually target? (Your own dogfood machine's GPU model/driver, plus whichever browser(s) the webconsole is expected to run in — nothing in this repo currently documents a supported-browser floor to check WebGPU/WebGL2 availability against.)
2. **Minimum browser support floor.** Is a WebGL2-required, Canvas2D-fallback posture (§1.4) acceptable, or does the webconsole need to stay usable on something that lacks WebGL2 entirely (unlikely in 2026, but worth confirming rather than assuming)?
3. **Flag-gated rollout appetite.** Given FEAT-webworker-sim-offload already established the "new subsystem ships behind an opt-in flag, defaults off" pattern for a *different* main-thread-offload effort — is that the right model for a GPU rendering path too, or would you rather this ship as the default path once Phase 1's bake-off passes, with Canvas2D demoted to the fallback rather than kept as the shipped default during rollout?
4. **Priority relative to FEAT-webworker-sim-offload.** That effort is already in flight and targets the *tick computation* cost; this proposal targets *paint* cost. Should Phase 0's measurement happen now (this week) to establish which of the two is actually the bigger BUG-622 contributor, before committing further engineering time to either in isolation?
5. **Effort budget.** Is this deep dive meant to produce a BOW item queued behind current in-flight work (FEAT-webworker-sim-offload, BUG-617's chunked restore), or is Aaron looking to fast-track Phase 0/1 as a parallel spike given BUG-622's user-visible severity?
