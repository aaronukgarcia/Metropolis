BOW code: FEAT-015

# Acceptance criteria — ui.screen.build (FEAT-015)

**BOW code:** FEAT-015
**Spec refs:** §13-F3 (`docs/METROPOLIS-MASTER-v2.1.md` line 248); §22 Unlock Economy (lines 378-384, catalogue/unlock state); §34 Zoning (lines 478-480); `int.protocol` (INT-001); `ui.widgets` (MOD-010, `done` — queue-lane widget, dependency).
**Date:** 2026-08-11
**Status:** draft-ahead (Sprint 8)
**Package under test:** `internal/ui/screens/build/` (confirm via `node claude-bow.js show FEAT-015` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/build/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md` — protocol-only consumption (SF-1), field-traceable docs (SF-2), the differential single-field mutation test that makes "reads the real engine" checkable against a stub that cannot fake it (SF-3), transport-swap transparency (SF-4), drill-through and alert-jump as consumed capabilities (SF-5/SF-6), stale-delta error handling (SF-7), determinism and race safety (SF-8/SF-9), and documentation (SF-10). Not restated here; see that file for the full text.

## User stories

- As **the player**, I need to purchase land, zone it, and queue construction with real lead times, so building the city feels like a logistics operation, not an instant-paint tool.
- As **the player**, I need the build queue to show materials + labour + lead time per item, so I know what's actually happening before it completes.
- As **the player**, I need a catalogue browser driven by `buildings.json` with unlock state visible, so I know what I can build now versus what I'm progressing toward.
- As **the player**, I need demolition available with its real cost, so removing a mistake is a decision, not a free undo.

## Scope

The F3 screen: land purchase, 8-way zoning (§34), build queue (materials/labour/lead time), demolition, and the `buildings.json`-driven catalogue browser with unlock-state display — all sourced via `int.protocol` view subscriptions.

## Acceptance criteria

### Functional

- **BLD-1.** A land-purchase flow: select cell(s)/parcel, show price, issue a `Buy`-class `protocol.Command`. SF-2-traceable.
- **BLD-2.** Zoning: all 8 zone classes (§34: Dwelling, Shop, Office, Entertainment, Farming, Manufacturing, Heavy Industry, Mining) are selectable; a zone-paint action issues the corresponding `Command`(s) — a passing test asserts painting a run of cells issues exactly one command per cell (or the documented batched equivalent), not a silently dropped subset.
- **BLD-3.** The build queue reuses `ui.widgets`' queue-lane widget (not reimplemented) to show materials, labour, and lead-time-remaining per queued item, sourced from `engine.build`'s queue view field.
- **BLD-4.** Demolition requires an explicit confirm step and shows its real cost before the command is issued — a passing test asserts the confirm step is not skippable and the cost shown matches the value the issued `Command` carries.
- **BLD-5.** The catalogue browser lists `buildings.json` entries with an unlock-state badge (locked / unlocked / in-progress toward next tier — **ASM-258**) sourced from `engine.unlocks`, not computed locally from XP/DP thresholds duplicated in the UI (GR#3 — the UI reads the unlock decision, it does not recompute it).
- **BLD-6 (SF-5 applied).** Every cost/lead-time/materials figure in the queue and catalogue is Enter-selectable to its source.

### Error handling

- **BLD-7.** A purchase/build/demolition command rejected by the engine (insufficient funds, permit, or unmet unlock prerequisite) surfaces the rejection reason, never a silent no-op.
- **BLD-8.** SF-7 applies: a catalogue entry whose fixture/unlock data is unavailable at render time shows "unavailable", not a blank row.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites §22 and §34.

## Out of scope

- The unlock economy's own progression-tree mechanics (XP, milestones, Development Points, per-category trees) — `engine.unlocks`, a separate engine module; this screen only *reads* unlock state.
- Materials/labour/lead-time computation itself — `engine.build`; this screen renders the queue, it does not simulate it.
- F1's map overlays for ownership/zoning visualisation — `ui.screen.map` (FEAT-005), already built; F3 issues zone/build commands, F1 shows their result on the map.

## Escalations

- **ASM-258** (P3, code-path `internal/ui/screens/build/`): the catalogue's unlock-state badge convention (locked/unlocked/in-progress) is a UI choice, not spec-fixed — cosmetic-only if wrong, no engine-contract impact.
- No BUG-058 gap found: `code.json`'s `ui.screen.build` outbound calls (`engine.build`, `engine.unlocks`) cover everything §13-F3/§22/§34 require for this screen's scope.
