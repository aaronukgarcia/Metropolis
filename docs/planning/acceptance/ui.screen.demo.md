BOW code: FEAT-018

# Acceptance criteria — ui.screen.demo (FEAT-018)

**BOW code:** FEAT-018
**Spec refs:** §13-F6 (`docs/METROPOLIS-MASTER-v2.1.md` line 251: "F6 Demographics — ASCII population pyramid (by month-age, so it's smooth), education pipeline, workforce by sector/skill, personality & leisure-taste distribution of the city"); §42 Leisure Time & Exploration (line 573, the 168-hour weekly budget this screen's "how your city spends Saturday" view renders); §21 (external commuting / dormitory strategy, line 1297: "housing type variety", commuting mode mix — the in/out-commuting leak view); §45 (line 52: firms/workforce, referenced for the workforce-by-sector framing; see Escalations for the registry gap this creates); `int.protocol` (INT-001); `ui.widgets`/MOD-010 (`done` — supplies the `BrailleCanvas` dot-addressing primitives this screen composes into its month-age population pyramid; no pre-built `Pyramid` symbol exists in `ui.widgets`, dependency); code.json `ui.screen.demo` entry (path `internal/ui/screens/demo/`, outbound calls: `engine.citizens`, `engine.households`, `engine.leisure`, `engine.extcommute`, `ui.core`, `ui.widgets`, `int.protocol` — no `engine.education`/`engine.firms` edge registered, see Escalations).
**Date:** 2026-08-12
**Status:** draft-ahead (Sprint 8)
**Package under test:** `internal/ui/screens/demo/` (confirm via `node claude-bow.js show FEAT-018` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/demo/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md` — protocol-only consumption (SF-1), field-traceable docs (SF-2), the differential single-field mutation test that makes "reads the real engine" checkable against a stub that cannot fake it (SF-3), transport-swap transparency (SF-4), drill-through and alert-jump as consumed capabilities (SF-5/SF-6), stale-delta error handling (SF-7), determinism and race safety (SF-8/SF-9), and documentation (SF-10). Not restated here; see that file for the full text.

## User stories

- As **the player**, I need a smooth month-age population pyramid, so I can read the city's age structure at a glance instead of squinting at a jagged year-bucketed bar chart.
- As **the player**, I need workforce-by-sector/skill shown against demand (white/blue collar), so I can see where a labour shortage is actually biting before a zone's demand bar goes mute on me (§34's "why" promise, consumed here for the demographic angle).
- As **the player**, I need to see how my citizens spend their Saturday (§42's 168-hour budget, one slice of the week made visible), so leisure infrastructure decisions are informed by real time-use, not a guess.
- As **the player**, I need housing demand-vs-stock by typology in one place, so I can see a mismatch (§HS typologies) before it shows up as abandonment or an appeal crisis.
- As **the player**, I need an in/out-commuting leak view (§21), so I can see how many residents work off-map and how many off-map workers fill my local jobs — the dormitory-strategy trade-off made legible.
- As **the player**, I need personality and leisure-taste distributions shown, so I understand why certain venues/policies land differently across my population rather than uniformly.

## Scope

The F6 screen: month-age population pyramid (composed from `ui.widgets`' `BrailleCanvas` dot-addressing primitives — a rendering requirement, not a pre-built `Pyramid` symbol), education-pipeline summary, workforce-by-sector/skill vs demand, personality and leisure-taste distributions, the "Saturday hours-by-activity" view, housing demand-vs-stock by typology, and the in/out-commuting leak view — all sourced via `int.protocol` view subscriptions against `harness.stub` (Sprint 8) and, unchanged, a real engine later.

## Acceptance criteria

### Functional

- **DEMO-1 (§13-F6 population pyramid; a rendering requirement, not a named symbol).** The screen renders a month-age population pyramid, composed from `ui.widgets`' `BrailleCanvas` dot-addressing primitives (MOD-010, already built and general-purpose) rather than a screen-local reimplementation of dot addressing. There is no pre-built `Pyramid` symbol in `ui.widgets` to call — extracting one is deferred (per Bill's 2026-08-12 ruling on ASM-478) until a second screen needs the same composition; speculative reuse is not a GR#3 duplication case. Check: `grep -n "widgets\.BrailleCanvas" internal/ui/screens/demo/*.go` shows the composition, and a passing test feeds a fixture age-distribution and asserts the rendered bar widths trace to the fixture's per-month-age counts (`grep -rn "func Test.*[Pp]yramid" internal/ui/screens/demo/*_test.go`).
- **DEMO-2 (§13-F6 education pipeline).** The screen shows an education-pipeline summary (stage-by-stage counts/flow, per `engine.education`'s registered view) — **blocked pending the `engine.education` outbound edge**, see Escalations. `doc.go` must state this is blocked, not silently omit it (mirrors `ui.screen.districts.md` AC-7's precedent for a partially-buildable AC).
- **DEMO-3 (§13-F6 workforce by sector/skill vs demand).** The screen shows workforce counts by sector/skill against labour demand (white/blue collar split) — **blocked pending an `engine.firms` (or equivalent labour-demand source) outbound edge**, see Escalations. Same partial-block documentation requirement as DEMO-2.
- **DEMO-4 (§42 "how your city spends Saturday").** An hours-by-activity view renders leisure time-use sourced from `engine.leisure`'s registered view field(s), SF-2-traceable — a passing test feeds a fixture activity/hours breakdown and asserts the rendered totals sum to a value no test hardcodes (drawn from the fixture), and that changing the fixture changes the rendered breakdown (`grep -rn "func Test.*[Ss]aturday\|func Test.*[Hh]oursByActivity" internal/ui/screens/demo/*_test.go`).
- **DEMO-5 (housing demand-vs-stock by typology).** The screen shows, per housing typology (§HS), demand versus current stock, sourced from `engine.households`' registered view field, SF-2-traceable — a passing test feeds two fixture states differing only in one typology's demand figure and asserts (a) that typology's rendered demand-vs-stock changes and (b) every other typology's rendered row is byte-identical between the two runs (SF-3-shaped check, applied here specifically).
- **DEMO-6 (§21 in/out-commuting leak view).** The screen shows both directions of the commuting leak — residents working off-map (out) and off-map workers filling local vacancies (in) — sourced from `engine.extcommute`'s registered view field, distinguishing the two directions rather than merging them into one undifferentiated "commuting" number (mirrors `engine.extcommute.md` AC-10's wage-leakage-ledger distinction, consumed here as a render requirement). Check: `grep -rn "func Test.*[Cc]ommut.*[Ll]eak\|func Test.*InOutCommut" internal/ui/screens/demo/*_test.go` finds a test asserting the two directions render as distinct figures.
- **DEMO-7 (personality & leisure-taste distributions).** The screen shows a distribution view of citizen personality traits and leisure-taste weighting, sourced from `engine.citizens`'/`engine.leisure`'s registered fields — a passing test feeds a fixture distribution and asserts the rendered histogram/breakdown traces to it, not a hardcoded illustrative shape.
- **DEMO-8 (SF-5 applied).** Every whole-view aggregate figure this screen displays (pyramid total, workforce totals once DEMO-3 unblocks, typology totals, commuting totals) is registered into `ui.dash`'s (`MOD-038`) drill-through graph per SF-5; `doc.go` states the drill-target per figure.

### Error handling

- **DEMO-9.** SF-7 applies: a delta for an unknown/stale subscription is dropped and logged via `foundation.errors` (registry-sourced), never applied or causing a panic. Data that has become unavailable since the last delta (e.g. a typology retired mid-game) shows a clear "no longer available" state, not stale/corrupted data.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- **DEMO-10.** The package doc states module key `ui.screen.demo`, cites §13-F6, §42, §21, documents the view-subscription name(s) it depends on (the source data for SF-2/DEMO-1..7), and states explicitly, per DEMO-2/DEMO-3, which figures are blocked pending the `engine.education`/`engine.firms` registry-edge gap rather than claiming full §13-F6 coverage.

## Out of scope

- A shared, extracted `Pyramid` widget in `ui.widgets` — this screen composes its month-age pyramid directly from `BrailleCanvas`'s existing dot-addressing primitives instead; extraction into a reusable widget is deferred until a second screen needs the same composition (ASM-478), not attempted speculatively here.
- The general dashboards/layout-editor/drill-through framework (`MOD-038`) — F6 only registers its own figures into it, per the same convention `ui.screen.finance.md`/`ui.screen.districts.md` already established.
- The education lifecycle engine itself (`engine.education`/MOD-041, not yet built) and the firms engine itself (`engine.firms`/MOD-058, not yet built) — this screen renders their data once the registry edge and the engines exist; it does not model education or firm labour demand.
- Personality-trait and leisure-taste *generation* (the underlying distributions themselves) — `engine.citizens`/`engine.leisure`; this screen only renders the resulting distribution.

## Escalations

- **BUG-058-class candidate (missing engine registry edges — `engine.education`/`engine.firms`, still open).** code.json's `ui.screen.demo` outbound calls list `engine.citizens`, `engine.households`, `engine.leisure`, `engine.extcommute`. §13-F6 explicitly requires an "education pipeline" view and a "workforce by sector/skill" view, but no `engine.education` or `engine.firms`/labour-demand outbound edge is registered for `ui.screen.demo`. This is the same class of gap `ui.screen.finance.md` already flagged for its own §54 Fiscal-Circuit sources (a live BUG-058 finding against that screen). DEMO-2 and DEMO-3 are written to reflect exactly this block rather than assuming the edges exist. **Not editing `code.json`** — flagging for Bill to confirm whether `engine.households`/`engine.leisure` already re-expose the needed fields indirectly (in which case no gap exists) or whether new outbound edges must be registered, and to fold this into (or file alongside) the existing BUG-058 thread rather than opening a duplicate root cause.
- **RESOLVED 2026-08-12 (ASM-479): `ui.widgets`/`int.protocol`/`ui.core` registry drift.** As independently verified by the Tester at dispatch, the built screen also needs `ui.widgets` (BrailleCanvas rendering), `int.protocol` (GR#20 — every UI screen communicates via the registered protocol interface), and `ui.core`, matching the pattern `ui.screen.map` already registers. Fixed via `docs/planning/master-plan-v2.1.json`'s `ui.screen.demo.calls` (never a hand-edit of code.json) + `node tools/plan/generate.js` regeneration, folded into the same registry-correction pass as FEAT-011/feat.saveux's registration-accuracy verification per Bill's ruling. `ui.screen.demo`'s outbound calls now correctly list all 7 edges: `ui.core`, `ui.widgets`, `engine.citizens`, `engine.households`, `engine.leisure`, `engine.extcommute`, `int.protocol`.
- **RESOLVED 2026-08-12 (ASM-478): phantom pre-built `Pyramid` symbol in `ui.widgets`.** This doc previously described `ui.widgets`/MOD-010 as already carrying a purpose-built pyramid rendering symbol; `grep -rn "func.*Pyramid" internal/ui/widgets/` finds zero matches. The built screen correctly composes its pyramid from `BrailleCanvas`'s existing dot-addressing primitives instead, satisfying §13-F6's literal requirement. Per Bill's ruling: accepted as a BA doc defect, not a code defect — this doc now describes the requirement (a population-pyramid rendering) rather than a phantom pre-built symbol name. A shared `Pyramid` widget is only worth extracting into `ui.widgets` when a second screen needs one (GR#3 covers actual duplication, not speculative reuse).
- **Dependency status at draft time.** `MOD-038` (`ui.dash`, drill-through/dashboard framework) is open, same-sprint (Sprint 8), parallel-build item this screen depends on for DEMO-8 — same standard two-track risk `ui.screen.finance.md` already flagged for its own FIN-6/SF-5; needs re-confirming against its landed API at dispatch.
- **No new naming-scheme assumption.** DEMO-5's typology enumeration is assumed to be `engine.households`' own registered typology set (§HS), not a screen-invented list — if `engine.households.md`'s typology enum changes shape before dispatch, DEMO-5's fixture shape must be re-confirmed against it, same caveat `ui.screen.districts.md` notes for its own district-cell model (ASM-285).
- **ASM-252 (confirm-and-close).** F6 Saturday-hours view = stacked bar over ui.widgets primitives (no bespoke chart).
