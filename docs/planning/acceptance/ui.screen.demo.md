BOW code: FEAT-018

# Acceptance criteria — ui.screen.demo (FEAT-018)

**BOW code:** FEAT-018
**Spec refs:** §13-F6 (`docs/METROPOLIS-MASTER-v2.1.md` line 251); §42 Leisure Time & Exploration (lines 573-575); §21 External Commuting & Housing (lines 372-376); §45 Firms — Entrepreneur Culture to Enterprise (lines 615-623, workforce/white-collar-blue-collar); §27 The Educational Lifecycle (lines 412-414, education pipeline); `int.protocol` (INT-001); `ui.widgets` (MOD-010, `done` — Braille chart, dependency).
**Date:** 2026-08-11
**Status:** draft-ahead (Sprint 8)
**Package under test:** `internal/ui/screens/demo/` (confirm via `node claude-bow.js show FEAT-018` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/demo/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md`. Not restated here; that file is authoritative.

## User stories

- As **the player**, I need a smooth month-age population pyramid, so I can see the city's real age structure rather than a coarse cohort bar chart.
- As **the player**, I need the education pipeline and workforce-by-sector-vs-demand visible, so I can see a skills gap forming before it becomes a ticker story a decade later.
- As **the player**, I need personality and leisure-taste distributions and the "how your city spends Saturday" view, so unmet taste demand tells me literally what to build next.
- As **the player**, I need housing demand-vs-stock by typology and the in/out-commuting leak view, so the dormitory-town strategy and its costs are both visible on the same screen.

## Scope

The F6 screen: month-age population pyramid, education pipeline, workforce by sector/skill vs demand, personality & leisure-taste distributions, "how your city spends Saturday" (§42), housing demand-vs-stock by typology (§21), in/out-commuting leak view (§21) — sourced via `int.protocol` view subscriptions.

## Acceptance criteria

### Functional

- **DEM-1.** A population pyramid renders at month-age resolution using `ui.widgets`' Braille chart (reused, not reimplemented), sourced from `engine.citizens`' age-distribution field, SF-2-traceable.
- **DEM-2.** An education pipeline view shows capacity/funding/attainment per stage (nursery → primary → secondary → sixth form/technical/leave-at-16 → university → adult education, §27). This requires `engine.education`'s data — **not currently a registered `code.json` outbound edge for this screen**; see Escalations (BUG-058 candidate).
- **DEM-3.** A workforce-by-sector/skill-vs-demand view (blue-collar vs white-collar split, §45) renders, cross-referencing firm-side demand with citizen-side skill supply. The firm-demand half requires `engine.firms`' data — **not currently a registered `code.json` outbound edge for this screen**; see Escalations (BUG-058 candidate).
- **DEM-4.** Personality and leisure-taste distribution charts render from `engine.citizens`' eight-axis personality vector aggregate and `engine.leisure`'s taste-weight data.
- **DEM-5.** The "how your city spends Saturday" hours-by-activity view (§42) renders from `engine.leisure`. Its widget shape is not spec-fixed — see **ASM-252**.
- **DEM-6.** A housing demand-vs-stock-by-typology view (§21) renders from `engine.households`.
- **DEM-7.** An in/out-commuting leak view (§21) renders from `engine.extcommute`, showing the out-commuter income-tax gain and the in-commuter wage-leak *distinctly* — a passing test asserts both figures render as separate values, never netted into one combined "commuting" number (mirrors §54's gross-vs-net honesty discipline applied here to commuting).

### Error handling

- **DEM-8.** SF-7 applies: education/workforce/leisure data unavailable at boot shows "unavailable" per pane, not blank.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites §27, §42, §21, §45.

## Out of scope

- The education/workforce/leisure simulation itself — `engine.education`/`engine.firms`/`engine.leisure`; this screen only renders their output.
- Housing typology construction — `ui.screen.build` (FEAT-015); F6 shows demand-vs-stock, not the build flow.

## Escalations

- **ASM-252** (P2, code-path `internal/ui/screens/demo/`): the Saturday hours-by-activity view's widget shape (stacked/grouped bar over `ui.widgets` primitives) is a UI choice, not spec-fixed.
- **BUG-058 candidate (missing registry edges) — two found.** `code.json`'s `ui.screen.demo` outbound calls list `engine.citizens`, `engine.households`, `engine.leisure`, `engine.extcommute`. Two gaps against this item's own scope: (1) `engine.education` is not a registered edge, yet "education pipeline" is named explicitly in FEAT-018's own BOW description and §27 is a cited spec ref; (2) `engine.firms` is not a registered edge, yet §45 (workforce by sector/skill vs demand) is a cited spec ref and §45's own text states the workforce view belongs on F6 ("the F6 workforce view shows both against supply"). Both DEM-2 and DEM-3 cannot be built against named, SF-2-traceable fields until these edges are added or their absence is confirmed intentional (e.g. if `engine.citizens` re-exposes skill/education-stage data itself, which seems unlikely given the module split). Not editing `code.json` — flagging for Bill and filing against BUG-058.
