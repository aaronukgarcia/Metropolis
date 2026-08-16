BOW code: FEAT-134

# Acceptance criteria — tool.codebaseviz (FEAT-134)

**BOW code:** FEAT-134 (`tool.codebaseviz`, GUID `b9b6903a-1b2a-4352-8a6a-89b6c0097e1c`)
**Deliverable:** `tools/codebase-viz/generate.js` (Node generator) → `tools/codebase-viz/index.html` (self-contained, regenerated page)
**Date:** 2026-08-16
**Status:** active
**Framing:** RETROSPECTIVE — the code already exists (built by a junior, ASM-656..665 logged). This file documents the delivered contract so a Tester can verify it against the code, not against a fresh design. Each AC cites the assumption (`ASM-xxx`) the builder logged, plus the source location in `generate.js` where the behaviour lives.

**Standard gates:** this is a **Node.js tool, not a Go package** — SG-1/SG-2/SG-4/SG-7 (go build/vet/test/determinism-grep) do not apply, matching `tool.bow`/`tool.secretguard` posture. This item's own gates are AC-7/AC-8 (no `Math.random`/wall-clock in the emitted data or layout), AC-9 (no CDN), and AC-13 (degradation, not hard failure). A generated page must render from the file alone with no server and no network.

## User story

- As **Bill (lead)**, I need a regenerable, self-contained webpage that renders the whole codebase at a glance — dependency graph, line-count heat map, per-module status pipeline, and a lost-and-found — so I can see integration drift, unbuilt modules, and pipeline state without hand-reading `code.json` or the BOW.

## Scope

One Node generator that reads the current working tree (`code.json` + metro BOW + git + filesystem + `go test`) and emits a single `index.html` with the data inlined. Four views, one status pipeline, deterministic data, no CDN. Regenerated as code lands so the page visibly improves over time.

## Acceptance criteria

### Functional — the four views

- **AC-1 (dependency graph).** The "Dependency graph" panel renders a directed graph with one node per `code.json` module (`data.modules`, keyed by `m.key`). Edges are the deduplicated, sorted union of each module's `outbound.calls` (module → callee) and the reverse of its `inbound.consumers` (consumer → module). Edge endpoints that are not themselves `code.json` modules are NOT dropped — they render as small dashed "ghost" nodes (`data.ghostNodes`, dashed stroke). Check: `generate.js` edge assembly (`outbound.calls ∪ reverse inbound.consumers`, `edgeSet`) and `ghostNodes` construction; render in `index.html` `renderGraph` (`g-ghost` dashed circles). *(cite ASM-665)*
- **AC-2 (dependency graph layout is deterministic).** The graph layout is a force-directed simulation with NO randomness: nodes are initialised on a circle by sorted key index, then a fixed 320-iteration repulsion + spring + centering pass with a fixed damping schedule. No `Math.random()`, no seed, no wall-clock appears anywhere in the layout path. Same data → same geometry on every load. *(cite ASM-662; `runForce` in `index.html`)*
- **AC-3 (line-count heat map / treemap).** The "Heat map" panel renders a squarified treemap (Bruls/Huizing/van Wijk) where each module's box area is proportional to its working-tree code-line count (`data.modules[].codeLines`), with a 1-line floor for empty modules so every active module gets a visible box. Areas are rescaled so their total fills the container, so relative proportions hold but the pixel scale is NOT literally 1px² = 1 line (the footer says so). Boxes are status-coloured and clickable to a detail panel. *(cite ASM-658, ASM-661; `squarify` in `index.html`)*
- **AC-4 (status pipeline — single cumulative status).** Every module carries exactly one `status` from the ordered pipeline `null → BA story → build → test → QA → committed`, computed as the deepest stage genuinely reached (cumulative, last-reached-wins), and the page renders it in the legend (with live per-stage count), as the graph node fill, and as the treemap box colour. The legend order and description text match the pipeline order. *(cite ASM-656; `status` loop in `generate.js`, `STATUS_ORDER`/`renderLegend` in `index.html`)*
- **AC-5 (status gates, incl. Go-only gates).** The stage gates are: `BA story` = an acceptance file exists at `docs/planning/acceptance/<key>.md`; `build` = at least one code file under the module path, counting ANY `CODE_EXTS` extension (`.go`, `.js`, `.json`, data, …), not only `*.go`; `test` = has `*_test.go` files AND `go test ./...` passes for the module's packages; `QA` = a Destructive `accept` verdict is recorded on the module's BOW item (`bow.accepts > 0`); `committed` = tracked files exist under the path AND no uncommitted diff there. The `test` and `QA` gates are Go-only: non-Go modules (JS tooling, data JSON) skip them and follow `null → BA story → build → committed`. *(cite ASM-656, ASM-657, ASM-659, ASM-660; the `isGo` branch in `generate.js`)*
- **AC-6 (lost-and-found).** The page renders exactly three buckets, each with its count and an explicit "(none)" empty state: (a) **Orphaned Go files** = tracked `.go` files not under any module's directory claim; (b) **Planned / unbuilt modules** = `code.json` modules with zero working-tree code files; (c) **Untracked files** = `git status --porcelain` `??` lines verbatim (which may name directories, not only files). *(cite ASM-664; `orphanedGo`/`unbuiltModules`/`untrackedFiles` in `generate.js`)*

### Determinism

- **AC-7 (no randomness in the emitted data).** The inlined JSON (`data`) contains no random values and no wall-clock timestamps: `grep -nE "Math.random|Date\(|Date.now|new Date|setTimeout|setInterval|performance.now" tools/codebase-viz/generate.js` matches nothing. Every list (`modules`, `edges`, `ghostNodes`, `orphanedGo`, `unbuiltModules`, `untrackedFiles`) is sorted, and the page pins `headCommit` (from `git rev-parse HEAD`) plus a `dirty` flag so the exact tree the viz reflects is identifiable. Regenerating against the same committed tree + BOW + git state produces byte-identical data. *(cite ASM-662; header comment + `main()`)*
- **AC-8 (deterministic geometry).** Both renderers are pure functions of their input with no seeds and no `Math.random()`: the graph force layout (fixed iteration count, fixed initial ring — AC-2) and the treemap (deterministic greedy squarify over an area-sorted input). Same data → same node positions and same box rectangles. *(cite ASM-661, ASM-662)*

### Self-containment

- **AC-9 (no CDN / no external requests).** The generated `index.html` is fully self-contained: all CSS and JS are inline. The only `http://` strings in the output are the SVG namespace URI (`http://www.w3.org/2000/svg`, a namespace, not a fetch) and the internal `url(#arrow)` marker reference — there is no `<script src>`, no `<link rel="stylesheet">`, no `@import`, no external `url()` fetch, and no font CDN (system-ui fonts only). Opening the file directly over `file://` with no server and no network renders all four views. Check: `grep -nE "<script src|<link|@import|https?://(?!www.w3.org/2000/svg)" tools/codebase-viz/generate.js` finds nothing beyond the SVG namespace lines.

### Data sources — everything derived, nothing hardcoded

- **AC-10 (code.json).** Module identity, guid, seq, layer, priority, milestone, title, path, and outbound/inbound edges are read from `code.json` (`data.modules`), never hardcoded. A module's file/directory claims are parsed from its `path` field, handling dirs (`internal/engine/core/`), single files (`claude-bow.js`), compound paths (`claude-bow.js + .claude/commands/sprint.md`), annotated placeholders (`cloud/ (planned — unbuilt)`), and `/` (claims nothing). *(header comment + `parseClaims`/`underClaims`)*
- **AC-11 (BOW + Destructive verdicts).** Per-module BOW state (guid, code, status, and Destructive `accept`/`reject` counts) comes from a single MariaDB round-trip joining `bow_items` to `bow_destructive_verdicts` on `item_guid`, grouped by mkey, run through the `MYSQL_BIN` client and honouring `METRO_DB_HOST/PORT/USER/PASSWORD/NAME` env overrides. *(header comment + `loadBow`)*
- **AC-12 (git + filesystem + go test).** Tracked/untracked/modified file sets come from `git ls-files`, `git ls-files --others --exclude-standard`, `git diff HEAD --name-only`, and `git status --porcelain`; `headCommit` from `git rev-parse HEAD`. Line counts are read from the working-tree files themselves (CRLF-normalised, single trailing newline stripped) for every tracked + untracked code file. `go test ./... -count=1 -json` supplies per-package pass/fail. *(header comment + `loadGitFiles`/`countLines`/`loadGoTest`)*

### Error handling / degradation

- **AC-13 (degradation, not hard failure).** If the BOW query fails or the MariaDB client is unreachable, the generator prints a `WARN:` to stderr and continues with an empty BOW map — Go modules then cannot reach `QA`/`committed` (those gates require `bow.accepts > 0`), but the page still renders with the degraded stages visible. If `go test` cannot be spawned or yields no package results, the generator prints a `WARN:` and emits `goTestRan:false`, so every Go module's `test` stage is unmet (`testPassed:false`). Neither failure crashes the run nor produces a broken page. *(`loadBow`/`loadGoTest` try/catch + `ran`/`{}` returns)*

## Assumptions logged

The builder logged ASM-656..665 (status precedence, build extension set, heat-map rescaling, untracked-vs-committed, single `go test` run, treemap algorithm, deterministic layout, colour ramp, lost-and-found rules, edge/ghost policy). This retrospective BA review adds two more:

- **ASM-793** — `tool.codebaseviz` is not yet a `code.json` module (verified: 146 modules, no such key), so the page does not render its own node and its files surface only in lost-and-found's untracked bucket; self-representation is deferred until the master-plan → `tools/plan/generate.js` pipeline registers it. This does not fail the four-view contract.
- **ASM-794** — `index.html` is a regenerated artifact (`generate.js` overwrites it every run via `writeFileSync`); `generate.js` is the single source of truth (GR#3) and `index.html` must never be hand-edited.

## Out of scope

- Registering `tool.codebaseviz` itself in `code.json` — that is the master-plan → `tools/plan/generate.js` pipeline's job, not this deliverable's (see ASM-793).
- A live server / build step — the page must open directly from `file://` (AC-9).
- Sugiyama/hierarchical graph layout — the deterministic force layout (ASM-662) is the accepted contract.

## Escalations

1. **`tool.codebaseviz` is absent from `code.json`** (146 modules, no such key). Consequence: the tool cannot visualise itself — its `tools/codebase-viz/generate.js` and `index.html` (currently untracked) appear only in the "Untracked files" bucket, and the module is not a node in the graph/heatmap nor listed under "Planned / unbuilt". Bill should register the module in the master plan so the next `code.json` regeneration includes it, closing the self-representation gap (ASM-793). The BA cannot make that write herself.
2. **`index.html` is committed/untracked as a build artifact** alongside the generator. It is overwritten on every `generate.js` run, so any hand-edit is silently lost. If the team wants the rendered page reviewed/committed as a snapshot, treat it as generated output (ASM-794) and regenerate before review rather than editing it.
