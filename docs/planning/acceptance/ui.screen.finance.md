BOW code: FEAT-014

# Acceptance criteria — ui.screen.finance (FEAT-014)

**BOW code:** FEAT-014
**Spec refs:** §13-F2 (`docs/METROPOLIS-MASTER-v2.1.md` line 247); §54 The Fiscal Circuit — top-down (lines 678-686); §39 Taxation — fine-grain controls (lines 556-563); UI-SPEC §2 (text Sankey, line 739); `int.protocol` (INT-001); `ui.dash`/`MOD-038` (drill-through, dependency); `ui.diagrams`/`MOD-037` (text Sankey engine, dependency).
**Date:** 2026-08-11
**Status:** draft-ahead (Sprint 8) — **owner of the Shared F-Screen Contract** (see below), inherited by FEAT-015/016/017/018/019/020/021
**Package under test:** `internal/ui/screens/finance/` (confirm via `node claude-bow.js show FEAT-014` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/finance/...`.

## User stories

- As **the player**, I need F2 to show P&L, balance sheet, loans and my credit rating, so I can run the city's books the way I'd run a real one.
- As **the player**, I need budget/tax sliders with elasticity curves and an incidence display ("who actually pays"), so raising a tax is an informed trade-off, not a blind lever.
- As **the player**, I need the §54 Fiscal Circuit master view (money in via exports/tourism/out-commuter wages/FDI/grants; money out via imports/leakage/interest) as one screen, so the whole-economy shape is visible without hunting across F2/F5/F6.
- As **the player**, I need gross-vs-net public payroll shown honestly (wage cost *and* the income-tax clawback), so I learn the civil-service truth (§54) rather than being told a number that hides it.

## Scope

The F2 screen: P&L, balance sheet, loans + credit rating, the §39 tax-instrument panel (elasticity + incidence), gross-vs-net public payroll, and the §54 Fiscal Circuit Sankey master view — all sourced via `int.protocol` view subscriptions against `harness.stub` (Sprint 8) and, unchanged, a real engine later. The Sankey's *layout* (proportional block-width bands) is `ui.diagrams`/`MOD-037`'s job; this screen supplies it band data, not diagram-drawing code.

## Shared F-Screen Contract (SF) — owned here, inherited by all eight FEAT-014..021 files

Every S8 F-screen (`ui.screen.finance/build/services/trade/demo/proj/ticker/menu`) carries this identical contract. It is authored once, here, because restating it in eight files is a GR#3 single-source-of-truth violation waiting to drift (a screen that quietly stops honouring SF-3 while its sibling still does is exactly the failure mode this avoids). Each other file's Acceptance Criteria section states only its screen-specific ACs and a one-line pointer back to this section — **it does not re-paste this text.** If a future edit is needed, it is made here once.

- **SF-1 (GR#20, structural).** The screen subscribes to named `int.protocol` view(s) and renders exclusively from the resulting `Delta` stream — no direct call into any engine-internal type. Check: `go list -deps ./internal/ui/screens/<pkg>/...` shows no import of `internal/engine/...`, only `internal/protocol` (and `internal/ui/...`). This alone is necessary but **not sufficient** — see SF-3.
- **SF-2 (field traceability).** `doc.go` states, per displayed figure/widget, the exact view-subscription field it is sourced from (a table or list: "P&L revenue tile ← `finance.pl.revenue`"). This is binding, not illustrative — a figure with no named source field is undocumented, not merely under-documented.
- **SF-3 (the "stub cannot fake" check — ASM-257).** A screen wired to a stub that always emits the *same* canned values renders perfectly and passes SF-1 and a superficial read of SF-2 while never actually being wired to anything. The check that catches this: for each figure named in SF-2, a passing test drives `harness.stub` through **two distinct scripted delta sequences that differ in exactly that field's value** and asserts (a) the bound widget's rendered output differs correspondingly between the two runs, and (b) every *other* widget's rendered output is byte-identical between the two runs. A lazy-but-plausible implementation that hardcodes a value, computes it independently of the subscribed view, or wires the wrong field will fail (a) or (b). This is a per-screen obligation — each screen's own test drives its own fields; SF-3 is the *shape* of the check, not a shared test file.
- **SF-4 (transport-swap transparency).** The same test suite that exercises SF-1/SF-3 against `harness.stub` must require no code change to run against a real engine once one exists behind the same `Transport` (per `int.protocol`'s "the UI cannot tell the difference" contract) — checked structurally via SF-1 (no engine-internal imports) exactly as `ui.screen.map`'s AC-7 already established; this item's own test suite does not perform the actual swap (that is `feat.skeleton`'s/a later integration item's concern).
- **SF-5 (drill-through, consumed not reimplemented).** Every number this screen displays is registered into `ui.dash`'s (`MOD-038`) drill-through graph and `Enter` opens its source per UI-SPEC §4's absolute rule — this screen supplies `MOD-038`'s registration API with (widget, source-drill-target) pairs; it does not implement navigation, dead-end detection, or the graph itself. Check: every SF-2-documented figure has a corresponding drill-target registration; a passing test asserts `Enter` on each registered figure invokes the documented target rather than being a dead end.
- **SF-6 (alert-jump, consumed not reimplemented).** Where this screen is a valid landing target for the bottom alert stack (§13: "each alert jumps to its screen"), it exposes a named jump-anchor per alert category it can be the target of (e.g. "Loan payment due" → the loans pane) that the alert-stack module calls into — this screen does not own the alert-priority/colour-coding logic, only the landing anchor.
- **SF-7 (error handling).** A delta for an unknown/stale subscription is dropped and logged via `foundation.errors` (registry-sourced) rather than applied or causing a panic (mirrors `ui.screen.map` AC-8). Data that has become unavailable since the last delta (e.g. a closed loan) shows a clear "no longer available" state, not stale/corrupted data (mirrors AC-9).
- **SF-8 (determinism).** Rendering is a pure function of (view-model state, navigation/selection state) — identical inputs render identically across repeated calls; no `time.Now()`-driven content beyond the shared 300ms threshold-pulse primitive from `ui.widgets`. `grep -rn "time.Now" internal/ui/screens/<pkg>/*.go` (excluding `_test.go`) returns no matches.
- **SF-9 (race safety).** `go test ./internal/ui/screens/<pkg>/... -race -count=1` passes with no data race between the delta-applying goroutine and the render path.
- **SF-10 (documentation).** The package doc states its module key, cites its spec refs, and documents the view-subscription name(s) it depends on (the source data for SF-2).

## Acceptance criteria

Screen-specific. SF-1..SF-10 above apply and are not restated.

### Functional

- **FIN-1.** A P&L view (revenue/expense line items, selectable period) renders from `engine.finance`'s view fields, SF-2-traceable.
- **FIN-2.** A balance sheet view (assets/liabilities/net worth) renders similarly.
- **FIN-3.** A loans panel lists active loans (principal, rate, term, next payment) plus a new-loan request flow issuing a `protocol.Command`, and a credit-rating figure with its 12-cell sparkline trend (`ui.widgets`, reused).
- **FIN-4.** Budget/tax-instrument sliders (§39: council-tax bands, business rates, congestion charge, etc.) drive `engine.tax`/`engine.finance` parameters, each showing its elasticity response curve and an incidence display ("who actually pays") per instrument. Slider min/max/step are **not** spec-fixed — see **ASM-256**.
- **FIN-5.** A gross-vs-net public payroll line shows both the wage cost and the income-tax clawback as two distinct figures (§54's civil-service truth) — a passing test asserts both figures render and are never collapsed into a single net-only number.
- **FIN-6.** The §54 Fiscal Circuit Sankey master view renders money-in bands (exports, tourism, out-commuter wages, FDI, grants) and money-out bands (imports, leakage, interest) with proportional block widths, updating monthly, via `ui.diagrams`' (`MOD-037`) text-Sankey engine — this screen supplies band data (source, amount) to that engine; it does not implement layered-graph layout itself (out of scope, see below).
- **FIN-7 (SF-5 applied).** Every P&L/balance-sheet/Sankey-band figure is Enter-selectable to its ledger-line source.

### Error handling

- **FIN-8.** A loan request rejected by the engine (e.g. insufficient credit rating) surfaces the rejection reason on the panel, never a silent no-op (mirrors `ui.screen.debug` AC-12's toggle-rejection pattern).
- **FIN-9.** SF-7 applies: missing/unavailable loan or Sankey-source data shows "unavailable", not blank or stale.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites §54 and §39 by name for the Fiscal Circuit and tax-instrument panel respectively.

## Out of scope

- The text-Sankey layout algorithm itself (layered graph drawing, small-*n*, cache-until-topology-changes) — `MOD-037`.
- The drill-through graph/navigation engine and dashboard layout editor — `MOD-038`.
- F4/F5/F6's own screens (services funding, trade balance-of-trade, workforce) — separate items; this screen only shows the Fiscal Circuit's *aggregate* money flows, not those screens' own detail views.
- Central-bank rate-outlook projection — `ui.screen.proj` (FEAT-019, §45).

## Escalations

- **ASM-256** (P1, code-path `internal/ui/screens/finance/`): budget/tax-instrument slider ranges have no spec-mandated numbers — flagged for Aaron, not invented as a BA judgment call.
- **ASM-257** (P1, code-path this file): the SF-3 differential single-field mutation test is this BA's chosen mechanism for "stub cannot fake reading the real engine" — no spec text mandates this exact shape; flagged for Bill in case a different verification (e.g. a live two-transport swap test once a real engine exists) is preferred. Because SF-3 is shared by all eight files, a change here is a change to all eight, not eight independent edits.
- **Dependency status at draft time.** `MOD-037` (`ui.diagrams`) and `MOD-038` (`ui.dash`) are both **open**, same-sprint (Sprint 8), parallel-build items this screen depends on for FIN-6 and SF-5 respectively — standard two-track risk; FIN-6/SF-5 need re-confirming against their actual landed APIs at dispatch, per the pattern already used in `ui.keys.md`'s refresh passes.
- **BUG-058 candidate (missing registry edges).** `code.json`'s `ui.screen.finance` outbound calls list only `engine.finance`, `engine.tax`, `engine.fiscal`. §54 explicitly requires the Fiscal Circuit's money-in sources (exports §33 → `engine.freight`/`engine.logistics`; tourism §44 → `engine.tourism`; out-commuter wages §21 → `engine.extcommute`; FDI §46 → `engine.fdi`; grants §55 → `engine.defence`) and money-out sinks (imports, leakage, interest). If `engine.fiscal` itself aggregates and re-exposes all of these as its own view fields, no edge gap exists; if not, FIN-6 cannot be built as specified without additional outbound edges that are not currently registered. **Not editing `code.json`** — flagging for Bill to confirm `engine.fiscal`'s actual field set at dispatch and file against BUG-058 if the gap is real.
