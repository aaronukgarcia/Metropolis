BOW code: FEAT-016

# Acceptance criteria — ui.screen.services (FEAT-016)

**BOW code:** FEAT-016
**Spec refs:** §13-F4 (`docs/METROPOLIS-MASTER-v2.1.md` line 251); §26 Emergency & Care Dispatch Model (lines 410-412); §54 The Fiscal Circuit — Public Service Pie (line 684); `int.protocol` (INT-001); `ui.dash`/`MOD-038` (drill-through, dependency).
**Date:** 2026-08-11
**Status:** draft-ahead (Sprint 8)
**Package under test:** `internal/ui/screens/services/` (confirm via `node claude-bow.js show FEAT-016` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/services/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md` — protocol-only consumption, field-traceable docs, the differential single-field mutation test for "reads the real engine, not a plausible stub" (SF-3), transport-swap transparency, drill-through/alert-jump as consumed capabilities, stale-delta handling, determinism, race safety, documentation. Not restated here.

## User stories

- As **the player**, I need per-service funding sliders with capacity-vs-demand visible, so I can see the consequence of a cut before the ticker tells me about it.
- As **the player**, I need response-time distributions and waiting lists, so emergency/care service quality is a number I can manage, not a mystery.
- As **the player**, I need a coverage map jump, so I can see *where* a service is thin without leaving F4 to hunt for the right F1 overlay.
- As **the player**, I need the Public Service Pie allocation visible against its per-1k-population benchmark, so I understand whether my staffing ratio is generous or dangerously thin at my current scale.

## Scope

The F4 screen: per-service funding sliders, capacity-vs-demand, coverage map jump, response-time distributions (§26), waiting lists, Public Service Pie allocation (§54) — sourced via `int.protocol` view subscriptions.

## Acceptance criteria

### Functional

- **SVC-1.** A funding slider exists per service category (police, fire, health, education, refuse, etc.), driving `engine.services` parameters via `protocol.Command`. Slider ranges are **not** spec-fixed — see **ASM-250**.
- **SVC-2.** A capacity-vs-demand gauge/chart renders per service, sourced from `engine.services`' view fields, SF-2-traceable.
- **SVC-3.** A coverage-map jump: `Enter`/an action on a service's coverage figure switches to F1 and selects that service's coverage overlay (per §13-F1's per-service coverage overlay) — this is cross-screen drill-through (SF-5): the jump target is `ui.screen.map`'s existing overlay-cycle state, not a second coverage renderer built here. Registered as a `dash.DrillTarget{ViewName, EntityID}` — the canonical shape, not a bespoke `WidgetID`+`Target` seam (BUG-239, `ui.screen.demo`).
- **SVC-4.** Response-time distribution charts (fire, ambulance, air ambulance, police — §26's unified dispatch model) render from `engine.dispatch`'s per-unit response data.
- **SVC-5.** Waiting-list figures (e.g. hospital non-urgent care, §26) render with a 12-cell sparkline trend.
- **SVC-6.** The Public Service Pie allocation view (§54: per-1k-population targets — police ~2.4, teachers per pupil, nurses & GPs, firefighters, social workers, refuse crews, council officers) shows the benchmark ratio alongside the player's actual funding level per slice, sourced from `engine.fiscal` if it carries the benchmark data, or `engine.services` if the benchmark is service-owned — **the exact source module is not currently a registered `code.json` outbound edge for this screen; see Escalations (BUG-058 candidate)**, and the owning BA/dev must confirm at dispatch which module actually exposes the Pie's target ratios before wiring SVC-6.

### Error handling

- **SVC-7.** SF-7 applies: dispatch/coverage data unavailable at boot shows an "unavailable" state per pane, not a blank panel (mirrors `ui.screen.debug` AC-11).
- **SVC-8.** A funding-slider change that the engine rejects (e.g. below a hard floor) surfaces why, rather than silently reverting with no feedback.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites §26 and §54.

## Out of scope

- The dispatch engine's routing/outcome computation — `engine.dispatch`; this screen only renders its response-time output.
- F1's coverage overlay rendering itself — `ui.screen.map` (FEAT-005), already built; SVC-3 jumps to it, does not duplicate it.
- The dashboard drill-through graph and layout editor — `MOD-038`.

## Escalations

- **ASM-250** (P1, code-path `internal/ui/screens/services/`): per-service funding slider ranges have no spec-mandated numbers — the §54 Public Service Pie gives *target ratios*, not slider bounds; flagged for Aaron.
- **BUG-058 candidate (missing registry edge).** `code.json`'s `ui.screen.services` outbound calls list only `engine.services` and `engine.dispatch`. This screen's own spec ref explicitly includes §54 (Public Service Pie), and SVC-6 cannot be built against a named, traceable field (SF-2) without knowing whether `engine.fiscal` is the actual source of the Pie's benchmark ratios — it is not currently a registered outbound edge for this screen. Not editing `code.json` — flagging for Bill to confirm at dispatch and file against BUG-058 if the gap is real.
