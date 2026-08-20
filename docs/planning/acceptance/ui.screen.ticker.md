BOW code: FEAT-020

# Acceptance criteria — ui.screen.ticker (FEAT-020)

**BOW code:** FEAT-020
**Spec refs:** §13-F9 (`docs/METROPOLIS-MASTER-v2.1.md` line 256); §29 The News System (lines 428-435); §20 Auto-naming (cited by §29 for real names); `int.protocol` (INT-001); `ui.keys` (MOD-011 — `/` NameIndex search, reused per ASM-254).
**Date:** 2026-08-11
**Status:** done (Sprint 8)
**Package under test:** `internal/ui/screens/ticker/` (confirm via `node claude-bow.js show FEAT-020` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/ticker/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md`. Not restated here; that file is authoritative.

## User stories

- As **the player**, I need the rolling ticker to read like real news from my own city, so every headline is a fact I can trust and trace.
- As **the player**, I need the monthly bulletin front page to surface the 3-5 stories that actually mattered, so I don't have to read every ticker line to stay informed.
- As **the player**, I need an annual review and a searchable full history archive, so the city's story is never lost and doubles as the eventual epilogue.
- As **the player**, I need every story to trace back to a real sim event, so nothing on this screen is invented or misleading (§29: "the facts always come from the engine — no hallucinated news").

## Scope

The F9 screen: rolling ticker, monthly bulletin front page (read-on-pause), annual review, searchable history archive (also the epilogue source) — sourced via `int.protocol` view subscriptions against `engine.news`.

## Acceptance criteria

### Functional

- **TIK-1.** A rolling ticker renders atomic events with real names (§20 auto-naming), sourced from `engine.news`'s event-stream view field, SF-2-traceable; scroll motion uses the shared "ticker scroll" primitive named in UI-SPEC §2, not a bespoke animation.
- **TIK-2.** A Monthly Bulletin front page renders 3-5 salience-ranked stories at month-end, with read-on-pause behaviour (the bulletin stays visible/readable while the sim is paused) — sourced from `engine.news`.
- **TIK-3.** An Annual Review renders year-in-numbers plus the year's biggest story, sourced from `engine.news`.
- **TIK-4.** A searchable full history archive renders, reusing `ui.keys`' `/` `NameIndex` search convention (name-substring match, `n`/`N` stepping) rather than a bespoke query language — see **ASM-254**.
- **TIK-5 (real-events-only, structural).** Every rendered ticker/bulletin/annual-review story carries a reference back to its originating `engine.news` event ID — a passing test asserts no rendered story exists without a traceable source event ID; this is the drill-through rule (SF-5) applied specifically to news content (registered as a `dash.DrillTarget{ViewName, EntityID}` — the canonical shape, not a bespoke `WidgetID`+`Target` seam; BUG-239), and it is the mechanism that makes "no hallucinated news" checkable rather than a promise: a story string with no backing event ID fails the check even if the prose reads plausibly.
- **TIK-6.** The searchable history archive is also the epilogue's data source (win/death screen) — a single store, not a duplicated one (GR#3): a passing test asserts the epilogue reads from the same archive query path this screen exposes, not a second copy.

### Error handling

- **TIK-7.** SF-7 applies: if `engine.news`'s stream is unavailable at boot, the ticker shows a clear "no news feed" state rather than an empty scroll that looks broken.
- **TIK-8.** A search query matching zero archive entries shows an explicit "no matches" state, distinguishable from "still loading".

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites §29 and §20.

## Out of scope

- News generation and salience scoring — `engine.news`; this screen only renders and searches its output.
- The optional LLM soft-layer that rewrites bulletin prose with flavour (§29) — out of v1 scope per spec ("the facts always come from the engine"); this screen renders whatever prose `engine.news` supplies, it does not call an LLM itself.
- The epilogue's own end-game presentation/framing — a separate concern; TIK-6 only guarantees shared data sourcing, not shared screen layout.

## Escalations

- **ASM-254** (P2, code-path `internal/ui/screens/ticker/`): archive search reuses `ui.keys`' NameIndex convention rather than a bespoke query grammar; may need date-range/category filters later.
- No BUG-058 gap found: `code.json`'s `ui.screen.ticker` outbound calls (`engine.news`) cover everything §13-F9/§29 require for this screen's scope — checked and clean.
- **ASM-519 (confirm-and-close).** SF-3 drives screen directly (stub has no f9 view).
- **ASM-520 (confirm-and-close).** Ticker scroll implemented locally (no shared ui.widgets primitive).
- **ASM-521 (confirm-and-close).** Drill-through = DrillTargets pair list (ui.dash OPEN).
- **ASM-522 (confirm-and-close).** Archive search case-insensitive substring; empty query matches nothing.
- **ASM-605 (confirm-and-close).** Archive replacement invalidates search to no-active-search (n/N cursor cannot survive a wholesale archive swap) rather than re-running the query.
- **ASM-606 (confirm-and-close).** empty-after-trimming uses Unicode strings.TrimSpace, not an ASCII-space check, so exotic-whitespace eventIds never reach dash.DrillTarget.EntityID.
