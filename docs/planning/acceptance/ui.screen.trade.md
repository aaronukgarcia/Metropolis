BOW code: FEAT-017

# Acceptance criteria — ui.screen.trade (FEAT-017)

**BOW code:** FEAT-017
**Spec refs:** §13-F5 (`docs/METROPOLIS-MASTER-v2.1.md` line 250); §33 The Freight Harbour — Tonnes & Chains (lines 462-476); §50 Oils, Rubber, Plastics & the Chemical Network (line 658); `int.protocol` (INT-001); `ui.widgets` (MOD-010, `done` — queue-lane widget, dependency); `ui.diagrams`/`MOD-037` (chain diagrams, dependency).
**Date:** 2026-08-11
**Status:** draft-ahead (Sprint 8)
**Package under test:** `internal/ui/screens/trade/` (confirm via `node claude-bow.js show FEAT-017` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/trade/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md`. Not restated here; that file is authoritative.

## User stories

- As **the player**, I need to see import contracts and manage them, so trade dependency is a visible, negotiable position rather than an invisible drain.
- As **the player**, I need the junction queue live view — the signature truck-glyph image — so freight congestion reads as literally as the map's traffic.
- As **the player**, I need warehouse stock/buffer policy per commodity, so I can decide how much slack I'm paying to hold.
- As **the player**, I need the balance-of-trade extension (t/day and £/day, by commodity and artery) and the pipeline-vs-truck safety trade view, so I can see whether I'm an importer or exporter and whether my hazardous freight is on the road or in a pipe.

## Scope

The F5 screen: import contracts, junction queue live view, warehouse stock/buffer policy per commodity, port (when unlocked), balance-of-trade extension (§33), pipeline-vs-truck safety trade view (§50) — sourced via `int.protocol` view subscriptions.

## Acceptance criteria

### Functional

- **TRD-1.** An import-contract list/management view (term, cancellation penalty, £/unit) with create/cancel actions issuing `protocol.Command`s, sourced from `engine.freight`/`engine.logistics`, SF-2-traceable.
- **TRD-2.** The junction queue live view reuses `ui.widgets`' queue-lane widget verbatim (not reimplemented) — cargo-coded glyphs growing leftward with a wait-time figure per §33/UI-SPEC §2's "queues rendered literally" — sourced from the junction-queue view field.
- **TRD-3.** A warehouse stock/buffer policy control exists per commodity (set-buffer `Command`). Ranges/units are **not** spec-fixed beyond t/day for flow figures — see **ASM-251**.
- **TRD-4.** A port panel (berths, crane rate, customs throughput) renders only once unlocked for the current tier; before unlock it reads and reflects the unlock state (via `engine.unlocks`-sourced data on the view), it does not implement its own tier-gating logic.
- **TRD-5.** The balance-of-trade extension shows t/day and £/day by commodity and by artery (§33), each figure drill-through-traceable (SF-5) to its commodity/artery source.
- **TRD-6.** A pipeline-vs-truck safety trade view (§50) compares the chemical/fuel pipeline grid's capacity against truck-movement count/risk for the same corridor. This requires the chemical/fuel network's data (refinery/petrochemical/tank-farm throughput, pipeline leak-risk) as well as truck-movement counts — the chemical/fuel side is **not currently a registered `code.json` outbound edge for this screen**; see Escalations (BUG-058 candidate). TRD-6 cannot be built against a named, SF-2-traceable field until that edge (or its absence, confirmed deliberate) is resolved.

### Error handling

- **TRD-7.** A contract cancellation attempted past its penalty-free window surfaces the penalty amount before commit, never a silent rejection or a silently-applied charge with no explanation.
- **TRD-8.** SF-7 applies: unavailable freight/port data shows "unavailable", not blank.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites §33 and §50.

## Out of scope

- Junction slot allocation and traffic equilibrium computation — `engine.traffic`/`engine.roads`; this screen only renders the resulting queue.
- The three diagram engines' layout algorithm (chain diagrams for §33 production chains) — `MOD-037`; this screen supplies the engine data, not the layout.
- Port ecosystem construction (berths, cranes as buildable objects) — `ui.screen.build` (FEAT-015); F5 shows operational state, not the build flow.

## Escalations

- **ASM-251** (P2, code-path `internal/ui/screens/trade/`): warehouse buffer-policy ranges/units beyond t/day have no spec-mandated values.
- **BUG-058 candidate (missing registry edges).** `code.json`'s `ui.screen.trade` outbound calls list only `engine.logistics` and `engine.freight`. This screen's own spec ref explicitly cites §50 (the chemical/fuel pipeline network), and TRD-6's pipeline-vs-truck view is named in this item's own BOW description — but neither `engine.chemicals` nor `engine.fuel` (both exist as registered `code.json` modules) is a registered outbound edge for `ui.screen.trade`. This is the clearest gap found across all eight screens in this assignment: the spec ref and the BOW description both name the requirement, the source modules exist in the registry, and the edge is simply absent. Not editing `code.json` — flagging for Bill to add the edges (or confirm the omission is intentional, e.g. if `engine.freight` re-exposes chemical/fuel data itself) and file against BUG-058.
