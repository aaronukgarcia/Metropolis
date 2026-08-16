BOW code: FEAT-019

# Acceptance criteria — ui.screen.proj (FEAT-019)

**BOW code:** FEAT-019
**Spec refs:** §13-F7 (`docs/METROPOLIS-MASTER-v2.1.md` line 254); A5 Slow-Fuse Principle (line 1352, 1367); UI-SPEC §4 Projections pane idiom (line 765); §36 Service Capacity Export (lines 530-548, contracted-vs-internal demand crossing); §45 Firms (rate outlook, line 623); `int.protocol` (INT-001); `ui.widgets` (MOD-010, `done` — Braille chart, dependency).
**Date:** 2026-08-11
**Status:** in_progress (Sprint 8)
**Package under test:** `internal/ui/screens/proj/` (confirm via `node claude-bow.js show FEAT-019` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/proj/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md`. Not restated here; that file is authoritative.

## User stories

- As **the player**, I need every demand/supply curve projected N years forward with history and projection visually distinct, so I never get ambushed by a shortfall I could have seen coming (the anti-ambush machine, §13-F7).
- As **the player**, I need contracted-vs-internal demand crossings shown (§36), so I can see the exact year a service-capacity export contract starts costing me instead of paying me.
- As **the player**, I need the central-bank rate outlook (§45), so a future rate spike is weather I positioned for, not a surprise.
- As **the player**, I need Slow-Fuse confirmations (A5) to render their >5-game-year consequence at the moment I make the decision, on this screen's own projection idiom, wherever that decision is made in the UI.

## Scope

The F7 screen: every demand/supply curve N years forward (history solid, projection dim, confidence bands, threshold lines, queued-decision markers per UI-SPEC §4), contracted-vs-internal demand crossings (§36), rate outlook (§45), and the render target for A5 Slow-Fuse confirmations wherever in the UI they are triggered — sourced via `int.protocol` view subscriptions.

## Acceptance criteria

### Functional

- **PRJ-1.** The UI-SPEC §4 projection-pane idiom renders per curve: history as solid Braille, projection as dim Braille, confidence bands as dim dots, threshold lines, and queued-decision step markers (a planned school appears as a capacity step *before* it's built) — reusing `ui.widgets`' Braille chart (not reimplemented), sourced from `engine.projections`' per-curve view fields, SF-2-traceable.
- **PRJ-2.** The forecast horizon N (years shown) is driven by unlock tier, per §13-F7 ("N grows with unlocked forecasting"), and every curve is seasonally aware. The starting/default N value is **not** spec-fixed — see **ASM-253**.
- **PRJ-3.** A contracted-vs-internal demand crossing chart (§36) renders per exportable service-capacity contract: the internal-demand growth curve and the contracted-away-capacity curve on one chart, so the crossing year is visible. This requires `engine.capexport`'s data — **not currently a registered `code.json` outbound edge for this screen**; see Escalations (BUG-058 candidate).
- **PRJ-4.** A rate-outlook curve (§45: the national base-rate cycle) renders read-only (the player does not control it, only positions for it, per §45's own text). Its source module is **not currently a registered `code.json` outbound edge for this screen** (only `engine.projections` is registered); see Escalations (BUG-058 candidate).
- **PRJ-5 (A5, cross-screen reuse).** Any decision UI element elsewhere in the game whose principal effect lands more than 5 game-years out (>60 months, per the existing `engine.core`-calendar convention already assumed in **ASM-239**) calls into this screen's projection-rendering primitive to render the consequence curve inline in its confirmation step, rather than that other screen reimplementing a projection chart — a passing test triggers a >60-month-consequence decision from another screen's confirmation flow and asserts a projection curve (not just a bare number) is rendered via this item's exported rendering call.

### Error handling

- **PRJ-6.** SF-7 applies: a curve whose source view has not yet delivered data (e.g. `engine.capexport` not yet real) shows "unavailable" or "not yet unlocked" per the reason, never a blank or fabricated flat line.

### Determinism & safety

- SF-8, SF-9 apply as written; confidence-band computation must also be a pure function of the view-model — no independently-sampled randomness inside the widget.

### Documentation

- SF-10 applies; additionally cites A5, §36, §45, and UI-SPEC §4.

## Out of scope

- Projection *computation* (the curves' underlying math/confidence-interval estimation) — `engine.projections`; this screen renders it.
- The confirmation-UI framework that Slow-Fuse decisions are embedded in generally (e.g. a generic "confirm this action" dialog) — this item only owns the projection-rendering call PRJ-5 plugs into, not the dialog chrome itself.
- Service-capacity export contract creation/management — `ui.screen.trade` (FEAT-017)/`ui.screen.services` (FEAT-016); this screen only shows the resulting demand crossing, not the contract flow.

## Escalations

- **ASM-253** (P2, code-path `internal/ui/screens/proj/`): default forecast-horizon N has no spec-mandated starting value.
- **BUG-058 candidate (missing registry edges) — two found.** `code.json`'s `ui.screen.proj` outbound calls list only `engine.projections`. Two gaps: (1) `engine.capexport` (the registered module backing §36's service-capacity export) is not a registered edge, yet FEAT-019's own BOW description explicitly names "contracted-vs-internal demand crossings (§36)" as in scope; (2) no module supplying the §45 rate outlook is registered as an outbound edge, and §45's own text explicitly says "F7 shows the rate outlook" — the source is most likely `engine.finance` or `engine.fiscal`, neither currently wired here. If `engine.projections` itself already aggregates both (pulling from `engine.capexport`/`engine.finance` internally and re-exposing the result), no gap exists; that needs confirming at dispatch. Not editing `code.json` — flagging for Bill and filing against BUG-058.
