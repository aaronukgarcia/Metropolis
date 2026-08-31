# FEAT-1972079916: Webconsole Error Framework — Registry Codes, First-Error-Wins, Persistent Log

**Feature:** After the useSim crash, Aaron wants error *numbers* and every error like that trapped and logged. Bring the webconsole up to GR#1 (log, type, correlation ID, selectable display) and GR#7 (every error registry-sourced). Half-landed today: ErrorBoundary records the first `recordError` then ignores later ones, but `getDerivedStateFromError` still overwrites the on-screen error with the cascade — the crash Aaron saw displayed the second error and masked the root.

**Mkey:** FEAT-1972079916

## Evidence (why this is P1)

Aaron dogfood 2026-08-28 (BUG-434): sim at −£320m, ~50× +£10m under turbo, time froze, then ErrorBoundary showed `useSim must be used inside SimProvider`. That throw is the *visible* error, probably not the first. Relates BUG-421 (toast-outside-provider class, done) and BUG-437 (QuotaExceeded on GR#27 `preWipeArchive` when the capture payload is huge — this item's error ring must stay bounded so it is not a second quota bomb).

## Design

- **Codes:** webconsole errors get `MET-xxx` from `data/errors.json` via `node tools/plan/add-error.js` (`claim-range` → `add` → `check`). Never hand-edit `errors.json`. feat.* has no default layer — `claim-range` takes `--layer` explicitly. Thrown/trapped records carry `{code, correlationId, tick, appVersion, timestamp}`.
- **Trap everything:** ErrorBoundary (render) + `window` `error` + `unhandledrejection` + a `console.error` tap. FIRST-ERROR-WINS: the boundary *holds* the first error for display and as the root record; later errors are marked cascade, not substituted onto the crash screen.
- **Persistent log:** localStorage ring (~100 entries, survives reload), written at trap time; included in debug JSON export AND the GR#27 capture-before-wipe snapshot; Debug tab selectable display + copy.
- **Crash screen:** code + correlation id + tick + version + first-vs-cascade + copy, alongside the existing friendly message and autosave note.
- **GR#17:** a failed log write is itself a registry-coded, visible error — never silent.

## Acceptance Criteria

- **AC-1 (registry, GR#7).** Every webconsole error code lives in `data/errors.json` `codes` and was allocated with `add-error.js` (`claim-range` for this item → `add` → `check`). Check: `node tools/plan/add-error.js check` exits 0; `grep` of `webconsole/src` finds no hand-typed `MET-[A-Z][0-9]` string that is absent from `data/errors.json`. **False-pass:** a parallel `error-codes.ts` (or similar) that restates MET numbers — GR#3/GR#7 forbid a second table. **Forbidden:** editing `data/errors.json` by hand.

- **AC-2 (envelope).** Every trapped/thrown record carries `{code, correlationId, tick, appVersion, timestamp}`. `code` matches `^MET-[A-Z]\d{3,4}$` and exists in `data/errors.json`. `correlationId` is unique per distinct first occurrence. `tick` is the last-known sim tick at trap time (null/omitted only if no sim state has been seen yet). `appVersion` is the same value the version badge / Debug tab already shows (GR#3 — inject it; do not hardcode a version string in the test). `timestamp` is envelope wall-clock (epoch-ms), not written into sim state. Check: one synthetic throw through `recordError` (or the public trap helper) asserts all five fields.

- **AC-3 (useSim + siblings).** The `useSim` throw in `webconsole/src/sim/store.tsx` and every sibling production `throw new Error(...)` in `webconsole/src` become registry-coded (at least `useBusy` in `Busy.tsx` and the pre-wipe archive parse throw in `captureBeforeWipe.ts`). Check: a test that renders `useSim` outside `SimProvider` asserts the thrown/trapped record's `code` is in the registry and the message still names the provider miss; `grep -n "throw new Error" webconsole/src` has no remaining bare-string throws. **False-pass:** wrapping the message with a `MET-` prefix in the string while `code` stays absent.

- **AC-4 (four trap paths).** Each channel deposits one ring entry of AC-2 shape: (1) ErrorBoundary / render-crash, (2) `window` `error` (`reportWindowError`), (3) `unhandledrejection` (`reportUnhandledRejection`), (4) `console.error` tap. Check: a can-fail test per channel throws a *unique* synthetic error through that channel only and finds exactly one matching ring row. **False-pass:** asserting `console.error` was called, not that a ring row landed. **Recursion:** the tap must not re-enter itself; the boundary's own `console.error` must not become a second *root* (de-dupe or mark cascade of the render-crash). Unknown JS errors wrap a *generic trap-path* registry code plus the original message — do not mint a new MET code per message (DD3).

- **AC-5 (first-error-wins).** The boundary holds the FIRST error for on-screen display and as the root record. A later error in the same boundary is marked cascade and MUST NOT replace the held first on the crash screen. Check: throw unique error A then unique error B through the same boundary; the crash-screen text contains A's message/code/correlation id and a cascade mark for B; it does not present B as the only/primary error. **False-pass:** a test that never throws the second error — the event gap is the cascade. Today's `errorRecorded` flag (record-once, display-last) is not enough.

- **AC-6 (persistent ring).** A localStorage ring holds trapped errors across reloads. Cap is an exported constant, recommended ~100 (GR#15: tests read the constant, do not hardcode 100). Newest retained when full; oldest dropped. Written synchronously at trap time. Check: fill past cap, assert `length === CAP` and the oldest unique message is gone.

- **AC-7 (reload persistence).** After trap, a simulated reload (empty in-memory log, rehydrate from the same storage the production path uses) restores the same codes + correlation ids. Check: record unique E, snapshot storage, reset memory, hydrate, assert E is present with the same `code` and `correlationId`. **False-pass:** persistence only inside one process with no rehydrate step.

- **AC-8 (debug JSON + GR#27).** The ring is present in (a) the Debug-tab debug JSON `errors[]` and (b) the GR#27 pre-wipe snapshot's `errors[]` (today `captureBeforeWipe` already passes `recentErrors()` into the debug builder — after this item, that feed is the *persisted* ring, not a session-only buffer). Check: trap E, build debug JSON, assert E's `code` + `correlationId` in `errors[]`; trap E, run capture-before-wipe against injectable storage, assert the archived debug `errors[]` contains E. This item does **not** compact the rest of the debug dump (BUG-437). The ring stays bounded (AC-6) so it is not a second quota bomb.

- **AC-9 (Debug tab, GR#1 selectable display).** The Debug tab "Errors captured" list shows `code` + correlation id + first-vs-cascade, is user-selectable (`<pre>`/`<code>`/select-all), and has a copy control that puts code + correlation id + message on the clipboard (or a documented test double of clipboard). Check: `errorListModel` (or successor) exposes `code` and cascade; a test asserts the copy payload contains both code and correlation id. **False-pass:** code only in `console` / debug JSON, not on the panel.

- **AC-10 (crash screen).** On a held render-crash the overlay shows: error CODE, correlation id, tick, app version, first-vs-cascade marking, a copy button, plus the existing friendly message, message body, Reload, and autosave note. Check: a mount/boundary test asserts those fields are in the overlay output for the *first* error (AC-5). **False-pass:** adding the fields to the ring but leaving the overlay as message-only (today's screen).

- **AC-11 (GR#17, failed log write).** If the persistent-ring write throws (QuotaExceeded, private mode, thrown `setItem` test double), the failure itself is a registry-coded ring/in-memory entry that the Debug tab / crash path can show — the originating error is not silently dropped from memory. Check: storage `setItem` throws; assert a log-write-failure `code` is visible in `recentErrors()` (or successor) and the original synthetic error is still in the in-memory list. **False-pass:** `try/catch` that swallows the quota error with no registry row.

- **AC-12 (tests in the real gate).** Every trap path has a can-fail test (unique synthetic → AC-2 shape + AC-5 ordering + AC-7 rehydrate). Tests live in `webconsole/test/*.test.mjs` and/or `webconsole/test/mount.test.tsx` — those are what `webconsole/package.json` `"test"` runs. **`webconsole/test/rebuild-prompt.test.tsx` is NOT in the gate; do not put evidence there.** Check: `cd webconsole && npm test` is green; temporarily dropping first-error-wins hold, or skipping one trap channel, turns the matching test red.

## Design Decisions (Aaron unless noted)

- **DD1 — ring cap:** recommended 100; exact value is the exported constant tests read. Balance/ops regime.
- **DD2 — claim-range layer:** `add-error.js` infers no layer for feat.* — builder passes `--layer` (UI overflow `V` is the obvious letter; not asserted here). Do not invent MET numbers in this file (GR#15).
- **DD3 — unknown JS errors:** one generic registry code per trap channel (render / window / rejection / console), original message retained. Specific codes only for known production throws (AC-3) and the log-write failure (AC-11).
- **DD4 — cascade rows stay in the ring** (marked), so the cascade is visible in Debug/debug JSON; they never replace the held first on the crash screen.

## Scope notes

- Webconsole-only. No Go engine, no TUI F12, no metro MariaDB, no ASM-453 backend. The localStorage commit queue is not the BOW.
- Does **not** close BUG-434's insolvency/dispatch root — only the masking (first-error-wins + codes + log).
- Does **not** compact full debug JSON / `consistency.checks` (BUG-437). Fail-closed GR#27 stays; this item only bounds the *error ring*.
- GR#25: no new `code.json` edge. SSOT for codes is the `data/errors.json` *file*, not a Go `errs.New` call.
- Envelope timestamps may use wall-clock; they must not enter sim state / the reducer (GR#21).
)
