BOW code: FEAT-021

# Acceptance criteria — ui.screen.menu (FEAT-021)

**BOW code:** FEAT-021
**Spec refs:** §13-F10 (`docs/METROPOLIS-MASTER-v2.1.md` line 257); UI-SPEC §4 (layouts, line 762: "F10 → layouts"); `int.protocol` (INT-001); `int.serializer` (INT-002 — save/load format, dependency); `ui.keys` (MOD-011 — keymap JSON profile, dependency).
**Date:** 2026-08-11
**Status:** done (Sprint 8)
**Package under test:** `internal/ui/screens/menu/` (confirm via `node claude-bow.js show FEAT-021` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/menu/...`.

## Shared contract

This screen inherits the **Shared F-Screen Contract (SF-1..SF-10)** defined once in `ui.screen.finance.md`. Not restated here; that file is authoritative. Note: F10's engine dependency is thinner than the other seven screens (see FEAT-021's own BOW listing — depends only on `MOD-009 ui.core`), so SF-1's "no `internal/engine` import" check is expected to be trivially satisfied here; SF-3's differential-mutation check still applies wherever this screen reads sim-derived data (e.g. sim-date/summary shown per save slot).

## User stories

- As **the player**, I need a save/load browser, so managing my games is as easy as managing files.
- As **the player**, I need a settings panel and remappable keymap/layout profiles, so the game fits my hands and my terminal, not a fixed default.
- As **the player**, I need new-game setup (seed, debug flag), so I can start a specific, reproducible game.

## Scope

The F10 screen: save/load browser, settings, keymap/layout profiles, new game setup (seed, debug flag) — sourced via `int.protocol` view subscriptions and `int.serializer`'s save-bundle API.

## Acceptance criteria

### Functional

- **MEN-1.** A save/load browser lists save files (name, timestamp, sim-date, summary) with load/save/delete actions. Listing and loading go through `int.serializer`'s (`INT-002`) bundle/header API (`Header.WorldSeed`, `CreatedAtTick`, format-version check) — **not currently a registered `code.json` outbound edge for this screen** (only `engine.core` is registered); see Escalations (BUG-058 candidate).
- **MEN-2.** A settings panel renders from a data-driven settings schema (GR#15: validators/fields derive from data, not a hardcoded form) — this AC is deliberately structural since UI-SPEC doesn't enumerate the full settings field list for Sprint 8; a passing test asserts adding a schema entry adds a corresponding rendered control without a code change to the panel itself.
- **MEN-3.** Keymap profile management: load/select/save a `ui.keys` keymap JSON profile — this screen is a *consumer* of `ui.keys`' validated-load path (per `ui.keys.md` AC-11/AC-11b: a profile entry mapping to an unregistered action is rejected per-entry, not silently accepted) — a passing test asserts this screen surfaces `ui.keys`' rejection reporting rather than swallowing it or re-implementing validation.
- **MEN-4.** Dashboard layout profile management (`F10 → layouts`, UI-SPEC §4): load/select/save a layout profile via `ui.dash`'s (`MOD-038`) layout editor — this screen hosts the entry point, `MOD-038` owns the editor itself.
- **MEN-5.** A new-game setup form takes a seed and a debug flag, issuing a new-game `protocol.Command`. Field set is **not** expanded beyond spec's own parenthetical — see **ASM-255**.

### Error handling

- **MEN-6.** Loading a corrupt/incompatible save surfaces `int.serializer`'s own typed error (`CheckFormatVersion`'s major-version-mismatch error, or `ValidateBundle`'s corruption error) verbatim in the browser — reused, not re-derived or genericised into "load failed".
- **MEN-7.** SF-7 applies: settings/keymap/layout data unavailable at boot shows "unavailable", not a blank panel.

### Determinism & safety

- SF-8, SF-9 apply as written.

### Documentation

- SF-10 applies; additionally cites UI-SPEC §4 (layouts) and names the `int.serializer`/`ui.keys`/`ui.dash` APIs this screen is a consumer of (so the dependency chain is traceable, mirroring `ui.screen.debug` AC-15's pattern).

## Out of scope

- The save-bundle format, NDJSON shards, and `metctl export`/`verify` — `int.serializer` (`INT-002`), already active; this screen only browses and triggers load/save.
- The keymap grammar/validation engine and dashboard layout editor's own mechanics — `ui.keys` (`MOD-011`) / `ui.dash` (`MOD-038`); this screen hosts entry points into both, it does not reimplement them.
- Actual settings the panel exposes (audio, display, accessibility, etc.) — content TBD as those subsystems land; MEN-2 only requires the panel be schema-driven, not that any particular setting exists yet.

## Escalations

- **ASM-255** (P2, code-path `internal/ui/screens/menu/`): new-game setup form fields limited to seed + debug flag, per the BOW description's own parenthetical.
- **BUG-058 candidate (missing registry edge).** `code.json`'s `ui.screen.menu` outbound calls list only `engine.core`. The screen's headline feature — the save/load browser (MEN-1) — needs `int.serializer`'s bundle/header API, and neither `int.serializer` nor `feat.saveux` (the module code.json shows depending on `ui.keys`, itself save/UX-flavoured) is a registered outbound edge here. Unlike the other BUG-058 candidates in this batch, this one is a protocol/tooling dependency rather than an `engine.*` module, so it may be intentional if F10 is meant to call `int.serializer` directly rather than through a `code.json`-modelled "outbound call" edge (the master plan's edge model may not track protocol/serializer consumption the same way it tracks engine consumption) — flagging for Bill to confirm whether this is a real BUG-058 gap or a modelling convention this BA misread, rather than asserting it outright.
- **ASM-523 (confirm-and-close).** F10 save-root enumeration injected (BundleLister); engine.save owns layout.
- **ASM-524 (confirm-and-close).** Menu actions issued as protocol.DebugPayload with fixed Op strings (no dedicated Kinds yet).
- **ASM-525 (confirm-and-close).** Save-slot fields derived from Header (CreatedAtTick/GameMonth/WorldSeed/DebugTouched) only.
- **ASM-526 (confirm-and-close).** F10 subscribes to 'f10.session' view (schema v1, screen's own choice).
- **ASM-1443 (FEAT-084 CC fold).** SEC-212's read-path fix adds a NEW registry code `MET-U608` (`ErrProfileReadFailed`) rather than reusing an existing menu code, because none of MET-U600..U607 semantically covers a profile READ/parse failure (U605 is write-only, U601 wire-patch, U604 save-listing) — reusing U605 for a read failure would itself be a misleading GR#7/GR#3 violation a re-attack would flag. One-line revert if the lead wants reuse regardless of semantic mismatch.
