# Requirement pack: wire-tui-screens (build F3, menu F10, debug F12)

Status: DRAFT for lead review. BA lane output, docs only. No code, no BOW writes.
Mkey: `ui.screen.build`, `ui.screen.menu`, `ui.screen.debug` (one increment; most specific key per screen, see section 4).

## 1. Context and goal

`cmd/metropolis` (the Go TUI binary) imports only five of the fourteen screen packages under `internal/ui/screens/`: `chrome`, `devmode`, `finance`, `map`, `services` (`cmd/metropolis/boot.go:19-23`). The other nine are built, tested, and unreachable from the running binary. Lane F's read-only triage (`temp/review-2026-10-30/lane-f-unwired-triage.md`, Screens table) selected three for a WIRE-NOW increment:

- **build (F3, Land & Construction)**: FEAT-015 is `done` (verified 2026-08-17 on f5c8437, `node claude-bow.js show FEAT-015`). BUG-490 is `done` (landed 1ff1619, 2026-09-05) but the closing fix wired the Go map keys `y`/`b`, not the F3 screen. The BOW history records the exact gap: "F3 'Land & Construction' ... is NOT REGISTERED in the running TUI binary at all (RegisteredIDs() = [map finance services] only)" (BUG-490 comment, 2026-09-01 21:05). Aaron's 2026-09-01 ruling (Q100021, Q100023) then said the reported "build queue" was the **webconsole** (React), so BUG-490 itself stays a separate question. This pack does NOT close BUG-490's webconsole half; see section 3.
- **menu (F10, save/load)**: player-critical in the TUI. Screen package `internal/ui/screens/menu` (13 files, 1634 LOC per triage).
- **debug (F12)**: cheap; developer value. Screen package `internal/ui/screens/debug` (8 files, 1170 LOC per triage).

Northstar stage: **Baseline One** (watchable on the real engine, `docs/planning/northstar.md`). Note the triage caveat: the playable surface today is the webconsole; the TUI is secondary. If Aaron rules the TUI out of scope for Baseline One, this pack is parked, not discarded.

Prior rulings looked up, not restated from memory: Q100021 and Q100023 (BUG-490 comments, 2026-09-01 22:04 and 22:18). Demo-vs-census F6 conflict: triage Screens table (open, see section 3).

## 2. Acceptance criteria

Surfaces are named per AC. "Engine state" means a value produced by the Go engine through the protocol and rendered by the screen. A screen that draws fixed or empty text does not satisfy any AC.

### Screen F3 (build)

**AC-B1 (key reaches screen).** Given the TUI is running on the default map screen, when the player presses `F3`, then the active screen becomes build (`ScreenRegistry.Active()` reports `ui.screen.build`) and the F3 header is drawn.
- Mutation that must fail it: remove the `RegisterGlobal(F3)` line, or register it on the wrong grammar; AC-B1 must go red.

**AC-B2 (renders real engine state).** Given the engine's `f3.build` view is published with a non-empty `queue[]` and `catalogue[]`, when the build screen is active, then the screen shows at least one queue row whose materials/labour/lead-time text equals the engine value, and at least one catalogue entry with its unlock badge.
- Mutation: stub `Screen.Queue()` to return `nil, false` or a hard-coded slice; AC-B2 must go red.
- Precondition for this AC: the engine must publish `f3.build`. See developer direction and the blocker in section 4.

**AC-B3 (BUG-490 re-verify: a queued build has an observable effect).** Given the build screen is active and the city has funds and a legal cell, when the player queues a build via the F3 control (`BuildOn`), then within one tick the `f3.build` queue gains an order with the chosen building type AND the ledger (finance screen, F2) balance changes by the build cost. The change must be visible on the build screen, not only in a unit test.
- Mutation: make `BuildOn` a no-op returning nil; AC-B3 must go red on the queue-row count and the ledger delta.
- Mutation: drop the `RegisterResultHandler` for the build correlation ID; a rejected build must still show the rejection reason (AC-B4), so a dropped handler must go red there.

**AC-B4 (rejection is visible).** Given a build command the engine rejects (insufficient funds), when the result returns, then the build screen displays the engine's rejection reason text, not silence.
- Mutation: discard the `Result` in the build screen's result handler; AC-B4 must go red.

### Screen F10 (menu)

**AC-M1 (key reaches screen).** Given the TUI is running, when the player presses `F10`, then the menu screen becomes active and its header is drawn.
- Mutation: omit the `RegisterGlobal(F10)` line; AC-M1 must go red.

**AC-M2 (renders real save entries).** Given two save files exist in the saves directory, when the menu is active and `Refresh()` has run (`internal/ui/screens/menu/saves.go:36`), then both entries from `SaveEntries()` (saves.go:108) appear on screen by name. Saves are filesystem-backed, not an engine view, so this AC needs no `f10.session` producer; `f10.session` (`menu/wire.go:13`) is for session state only.
- Mutation: make `Refresh` a no-op or hard-code the list in `render.go`; AC-M2 must go red when a third save file is added and not shown.

**AC-M3 (save round-trip changes the world).** Given the menu is active and the engine tick is T, when the player runs `Save(name, send)` (saves.go:178), advances the clock, then runs `Load(path, send)` (saves.go:164) on that save, then the engine tick returns to T and the screen reflects the load.
- Mutation: make `Save` return nil without sending the command; AC-M3 must go red on the tick comparison.

### Screen F12 (debug)

**AC-D1 (key reaches screen).** Given the TUI is running, when the player presses `F12`, then the debug screen becomes active and its header is drawn.
- Mutation: remove the F12 registration; AC-D1 must go red.

**AC-D2 (renders real build identity and error tail).** Given the debug screen is active, then it shows the build identity (version/commit) that `git describe` ldflags produce, and the most recent error-tail entries from the engine's error stream, and the values change when a new error is emitted.
- Mutation: hard-code the build string or the tail buffer; AC-D2 must go red when a test error is emitted and not shown.

**AC-D3 (no key theft).** Given the F1, F2, F4 and map bindings from `boot.go` are unchanged, when the player presses each of them before and after the new F3/F10/F12 registrations, then each still activates its existing screen.
- Mutation: register F3 on the map grammar instead of the global chrome grammar; AC-D3 must go red because map's own keys shadow it.

## 3. Non-goals

- **BUG-490 webconsole half**: the build-queue report Aaron made was about the React webconsole (Q100021). Rejection feedback there is BUG-490's re-scoped fix (toast per rejected placement, Q100037), not this pack. Pack AC-B3 is the TUI re-verify only.
- **F6 slot conflict (decision for Aaron)**: `census` (triage: PARK, pairs with engine.census MOD-078) and `demo` (PARK) both claim F6 per the triage Screens table. This pack does not wire either. Aaron must decide which owns F6 and which package is deleted. Recorded as an open ruling, not a silent default.
- **PARK screens**: census, demo, districts (F8), proj (F7), ticker (F9, BUG-555 open), trade (F5). Not wired here.
- **Engine packages**: the 27 unwired engine packages (policies, social, fiscal, coastal, defence, etc.) are separate increments with their own compose rows. No compose.go change here beyond what `f3.build`/`f10.session`/debug need.
- **Webconsole**: no change to `webconsole/`.
- **Balance**: no tuning of build cost, lead time, or save-slot limits (balance number regime: placeholder values, directional tests only).

## 4. Developer direction

### Registration pattern to follow (cmd/metropolis/boot.go)

- ScreenID constants are declared at `boot.go:114-116` (`screenIDMap`, `screenIDFinance`, `screenIDServices`). Add `screenIDBuild`, `screenIDMenu`, `screenIDDebug` in the same block. Comment at `boot.go:111` already anticipates "districts/menu/debug".
- Screen structs are fields on the `w` (world/wiring) struct, e.g. `financeScreen *financescreen.Screen` at `boot.go:241-242`, constructed with a correlation ID at `boot.go:599-600` (`financescreen.New(financeCorrID)`).
- Data screens are primed and bound BEFORE `router.Run`: `primeScreenSubscription(...)` at `boot.go:694-709` (services then finance), with `Subscribe(transport.SendCommand)`, `BindSubscription`, `ApplyDelta` as the three callbacks. Build must follow this exact shape with `ViewSubscriptionName = "f3.build"` (`internal/ui/screens/build/wire.go:12`). Menu's view is `f10.session` (`internal/ui/screens/menu/wire.go:13`, `menu.Subscribe(view, send)` at `screen.go:154`).
- Registration in the registry: `w.screens.Register(core.ScreenEntry{ID: ..., Draw: ..., Grammar: ...})` at `boot.go:879-900`, error wrapped as `codeBootFailure` (copy the existing pattern exactly).
- F-key binding: `fKeyGlobal(id, name)` at `boot.go:917-918` and `RegisterGlobal(keys.Key{Special: "F2"}, ...)` at `boot.go:77-87`. Add F3, F10, F12 the same way on `w.chromeGrammar`. Check first that F10 and F12 are not already bound by `chrome` or `devmode` (grep `Special: "F10"` and `"F12"` across `internal/ui`).
- Result routing for commands: `w.router.RegisterResultHandler(cmd.CorrelationID, w.servicesScreen)` at `boot.go:843`. Build must register its screen the same way for `BuildOn`/`BuyLand` results (AC-B4).

### Packages to touch
- `cmd/metropolis/boot.go` (registration, priming, F-keys), `cmd/metropolis/run.go` (if the input router needs a screen-specific draw hook).
- `internal/ui/screens/build`, `menu`, `debug` (entry APIs: `build.New` screen.go:84, `menu.New` screen.go:134, debug package; read before wiring).
- Engine side: the engine must PUBLISH `f3.build` and `f10.session`. Current state: a repo-wide grep of non-test `.go` files outside `internal/ui/screens` finds NO producer for `"f3.build"` or `"f10.session"`. `boot.go:286-287` states outright: "a view compose.go does not publish yet (registering it is a separate, larger increment)". **This is the blocker for AC-B2 and AC-B3** (menu does not need it: saves are filesystem-backed, see AC-M2). Debug's data source (AC-D2) is NOT yet verified: the debug package was not read for its entry API in this pass; the developer must confirm it before the AC is trusted. The developer must first decide the producer (new view publisher in compose, or a view adapter over `engine/build` `BuildAPI.Queue()`), and that decision goes to the lead before code.
- Errors: any new error code only via `tools/plan/add-error.js`, riding in the feature commit. No hand-edited `data/errors.json`.

### Constraints
- Determinism (GGR/GR#21): any map iteration that feeds the queue or slot list must be order-independent or sorted.
- No storage or clock reads in hot paths (BUG-602): the screen Draw path must not call `time.Now` or disk.
- Import-cycle TDZ rule (BUG-1051): no new top-level constants read from engine packages in a new module; constants in the leaf package.
- Units: any new unit string registered in code.json `units` (`/units-lint`).
- Scoped verification only: `go test ./internal/ui/screens/build/... ./cmd/metropolis/...` at most, with `-count=1`, NEVER the full suite (agent test-scope contract). Respect the 30-minute test watchdog. Note the existing `cmd/metropolis` package has known wall-clock flakes (BUG-492 class); do not add new wall-clock bounds.
- Commit format: `[type]: ... [ui.screen.build]` (most specific key), with each screen's regression test named by AC id.

## 5. Tester watch-fors

- **Built-but-not-wired is the house defect class.** A passing `F3`-routes test is NOT enough. Each screen must show engine state changing at the named surface (AC-B2, AC-B3, AC-M2, AC-M3, AC-D2). Run the binary (`cmd/metropolis` headless or interactive) and confirm the change, do not stop at registration grep.
- **Prove each new test can fail.** For every AC, apply the named mutation, watch the test go red, revert. Record the red output as evidence on the BOW item.
- **Stale or stub state.** Check `HaveData()`/`Stale()` (`build/screen.go:224-252`): the screen must show a stale indicator when the subscription is stale, not old numbers as live.
- **Key stealing.** Check AC-D3 by pressing F1/F2/F4 and map keys after the new bindings. The map grammar (`w.mapGrammar`) binds arrow keys and `y`/`b` (`boot.go:1085`); a new F-key must not be registered there.
- **Save/load determinism.** Menu AC-M3 must compare tick values, not wall-clock time.
- **Tester gives PASS/FAIL against the ACs only and never fixes.** FAIL bounces to the same developer.

## 6. Round guidance (GR#23)

Attacker is never the author. Attack in this order:

1. **Stub views**: is the `f3.build` / `f10.session` view actually produced at runtime, or does the screen bind a view nothing serves and silently show "no data"? A green unit test over a hand-fed `ApplyDelta` proves nothing here.
2. **Key collision**: a bound F-key that is already registered by `chrome`/`devmode` on the same grammar, or a map-grammar binding that shadows it (AC-D3).
3. **Result laundering**: a build or save command whose failure result is dropped (no `RegisterResultHandler`), so the screen shows success-looking state (AC-B4).
4. **Stale data presented as live**: `Stale()` true but the render still prints numbers without a marker.
5. **Determinism**: map-order in queue or slot listing, or a clock read in Draw.

Verdict recorded via `node claude-bow.js destructive <code> --verdict accept|reject ...` from a `.claude/worktrees/*` cwd (memory: round verdicts record from a worktree).

## 7. Done means

Observable behaviour: from the running `cmd/metropolis` binary, pressing F3, F10 and F12 each activates its screen; the F3 queue shows an engine-published order after a build, the F10 screen lists real save slots and round-trips a tick, and the F12 screen shows live build identity and error tail; each of these has a regression test that was seen to fail under its named mutation; `go test` on the three screen packages and `cmd/metropolis` (scoped, `-count=1`) is green; the BOW item(s) carry the accepted destructive verdict and the commit refs.

## Open questions for Aaron

1. **F6 owner**: census (engine.census, MOD-078) or demo? Both claim F6 in the triage table. One screen must be deleted or renumbered.
2. **Scope of the TUI**: the triage caveat says the webconsole is the primary player surface. Is wiring the TUI's F3/F10/F12 in Baseline One scope, or should the engine views be exposed through the webconsole instead?
3. **f3.build producer**: approve a new compose view publisher for `f3.build` (and `f10.session`) before the developer starts; it is a larger increment than this pack.
