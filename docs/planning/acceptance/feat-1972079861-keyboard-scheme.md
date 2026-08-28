# FEAT-1972079861: React Console Keyboard Control Scheme and Help Overlay

**Feature:** Comprehensive keyboard bindings for tools, speed, layers, and camera control, plus an in-game help overlay listing all bindings.

**Mkey:** FEAT-1972079861

---

## Overview

Extends the existing keyboard controls (number keys 1–9 for tools, Escape for cancel) with:
1. **Game speed:** Pause, Play, Fast, Turbo (currently buttons only in TopBar)
2. **Layer toggles:** Water, Power, Lines, Refs overlays (currently buttons only)
3. **Camera controls:** Pan (keyboard arrows), zoom in/out, fit-all-map reset
4. **Help overlay:** Single keystroke to open/close a modal listing every binding
5. **Binding registry:** Single source of truth (SSOT per GR#3) — a `KeyBindings` object defining all bindings; the help overlay renders FROM that registry, never hand-maintained
6. **Text-input safety:** Bindings are disabled when focus is on a text input (no accidental tool switches while renaming)

All bindings dispatch existing store actions (no new logic, no sim-state mutation outside the reducer).

---

## Design Decisions Flagged for Lead

### DD1: Speed Keys
**Which keys cycle through Pause/Play/Fast/Turbo?**
- **Option A (CHOSEN UNLESS OVERRULED):** Space for Play/Pause toggle (most common game convention); +/− or [ ] for Fast/Turbo cycle
- **Option B:** 0/1/2/3 (matches SPEEDS array index) — direct mapping, always unambiguous
- **Option C:** P for Play, [ and ] for cycle left/right through speeds (mnemonic but fewer modifiers)

**Consequence:** Space is conventionally pause in games but interferes with form submission in some UIs. 0/1/2/3 is direct but less discoverable. [ ] is discoverable in the help overlay but keyboard-layout-dependent (AZERTY differs from QWERTY).

**Flag for Aaron:** Which binding, and if Option A (Space), confirm it's OK despite form-submission risk in unrelated UI contexts.

---

### DD2: Layer Toggle Keys
**Which keys toggle water/power/lines/refs?**
- **Option A (CHOSEN UNLESS OVERRULED):** W/P/L/R (first letters, mnemonic, minimal interference with common bindings)
- **Option B:** F1/F2/F3/F4 (F-keys reserved for help/about/settings, following convention) — but F-keys vary per browser/OS
- **Option C:** Shift+W/Shift+P/Shift+L/Shift+R (modifiers to avoid conflicts with typing)

**Consequence:** Option A is the most discoverable but does interfere with Ctrl+W (close tab) and Ctrl+P (print). Option B is future-proof but F-key handling is unreliable cross-browser. Option C is safest but requires extra keystrokes.

**Flag for Aaron:** Which binding. If Option A, confirm W/P/L/R are acceptable despite single-letter side effects.

---

### DD3: Camera Pan Keys
**Which keys move the camera (pan up/down/left/right)?**
- **Option A (CHOSEN UNLESS OVERRULED):** Arrow keys (↑ ↓ ← →) — standard, no learning curve
- **Option B:** WASD (game-convention alternative) — conflicts with web shortcuts (Ctrl+W, Ctrl+S)
- **Option C:** Mouse only — keyboard pan is "nice-to-have" for accessibility; defer to Option A

**Consequence:** Arrow keys are universally understood but less comfortable for left-hand-only control. WASD is comfortable but OS shortcuts interfere. No keyboard pan is simplest (mouse/trackpad users are not disadvantaged on a city sim).

**Flag for Aaron:** Confirm Option A (arrows), or clarify if keyboard pan is optional for baseline one.

---

### DD4: Camera Zoom & Fit Keys
**Which keys zoom and reset to fit-all?**
- **Option A (CHOSEN UNLESS OVERRULED):** 
  - `+` and `−` (numpad or shift-equals) for zoom in/out
  - `Home` for reset (fit whole map to view)
  - Fallback if numpad absent: `]` and `[` for zoom (right-hand reach)
- **Option B:** Ctrl+scroll (already works via mouse) + `0` for fit-all (mimics browser zoom)
- **Option C:** Only mouse wheel zoom (keyboard zoom is "nice-to-have"); keyboard `Home` for fit-all only

**Consequence:** Option A is explicit and the help overlay makes it discoverable. Option B reuses browser muscle memory but conflicts with Ctrl shortcuts. Option C is minimal (avoids binding proliferation).

**Flag for Aaron:** Confirm Option A (numpad +/−, Home), or if keyboard zoom is optional, whether to include `Home` for fit-all.

---

### DD5: Help Overlay Key
**Which single key opens/closes the help overlay (listing all bindings)?**
- **Option A (CHOSEN UNLESS OVERRULED):** `?` (shift+/) — mnemonic ("help?"), found on most keyboards, does not interfere with text
- **Option B:** `F1` — standard across Windows/Linux, but unreliable in browsers (may open browser help instead)
- **Option C:** Backtick `` ` `` — hidden but out of the way (vim/game-dev convention)

**Consequence:** Option A is the most intuitive and cross-browser. Option B is standard but browser-intercepted. Option C is discoverable only from tutorials.

**Flag for Aaron:** Confirm Option A (`?`), or specify an alternative.

---

### DD6: Binding Registry Scope
**Does the registry include ONLY keyboard bindings, or also mouse/UI-only actions?**
- **Option A (CHOSEN UNLESS OVERRULED):** Keyboard only (speed, layers, camera, help). UI buttons for actions without keyboard shortcuts (e.g., "Start Over", unlock-all) are documented separately in tooltips.
- **Option B:** Universal binding table, including deprecated/unmapped actions and alternatives (future-proof but verbose)

**Consequence:** Option A keeps the help overlay focused and readable (not a full manual). Option B is complete but harder to maintain.

**Flag for Aaron:** Confirm Option A — keyboard is the binding registry scope.

---

## Acceptance Criteria

### AC-1: Binding Registry — Single Source of Truth
**All keyboard bindings are defined in a single `KeyBindings` TypeScript object (SSOT per GR#3/GR#15).**

- **Location:** New file `webconsole/src/sim/keybindings.ts` exports a constant `KEYBINDINGS: readonly KeyBinding[]`.
- **Structure (TypeScript):**
  ```typescript
  export interface KeyBinding {
    key: string;                                  // e.g., '1', 'ArrowUp', '?'
    action: { type: string; [key: string]: any }; // dispatch action (e.g., { type: 'tool', ... })
    category: 'tool' | 'speed' | 'layer' | 'camera' | 'help'; // UI grouping
    label: string;                                 // display name (e.g., 'Residential Zone')
    description?: string;                         // optional longer help text
  }
  export const KEYBINDINGS: readonly KeyBinding[] = [
    { key: '1', action: { type: 'tool', tool: { mode: 'build', spec: PALETTE_FLAT[0] } }, category: 'tool', label: 'Tool 1' },
    { key: ' ', action: { type: 'speed', speed: 1 }, category: 'speed', label: 'Play/Pause' },
    { key: 'w', action: { type: 'toggleLayer', layer: 'water' }, category: 'layer', label: 'Water' },
    // ... more bindings
  ];
  ```
- **Guarantee:** Every key in KEYBINDINGS is unique (no duplicate keys); every action is a valid store dispatch (type exists in engine.ts `Action` union).
- **Test:** Unit test: parse KEYBINDINGS, verify all keys are unique, verify all action types exist in store. No hand-maintained list is consulted elsewhere.

---

### AC-2: Help Overlay Derived from Registry (GR#3 SSOT)
**The help overlay renders EXACTLY from the KEYBINDINGS registry — never a hardcoded list.**

- **Component:** New `HelpOverlay.tsx` component (or modal inside MapView).
- **Rendering:** Loop over KEYBINDINGS, group by category, render each binding's key + label + optional description.
- **Guarantee:** If a binding is added to KEYBINDINGS, it appears in the help overlay automatically. If a binding is removed from KEYBINDINGS, it disappears from the overlay. No manual sync required.
- **Visual:** Help overlay is a modal (semi-transparent dark background, white text) showing:
  ```
  TOOLS
  1–9        Build tools (Residential, Commercial, Road, etc.)

  SPEED
  Space      Play / Pause
  [ ]        Cycle through speeds (Fast, Turbo, back to Pause)

  LAYERS
  W          Water network overlay
  P          Power infrastructure overlay
  L          Line saturation overlay
  R          Building reference IDs
  
  CAMERA
  Arrow keys Pan up/down/left/right
  + −        Zoom in / Zoom out
  Home       Fit map to view
  
  Esc        Close help (or cancel current action)
  ```
  Categories are collapsible (optional) or always expanded (simple).
- **Test:** 
  1. Render HelpOverlay with KEYBINDINGS.
  2. Verify every binding entry appears in the overlay.
  3. Modify KEYBINDINGS (add a binding), re-render, verify overlay updated.
  4. Verify no hardcoded strings appear in HelpOverlay.tsx that are also in KEYBINDINGS (use a linter rule if feasible).

---

### AC-3: Keyboard Event Handler — Global Listener
**A single global `onKeyDown` handler consumes KEYBINDINGS and dispatches actions.**

- **Location:** `MapView.tsx` useEffect (existing keydown listener at line 642–656), extended to handle all bindings from KEYBINDINGS.
- **Logic:**
  ```typescript
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      // Do not fire if focus is on a text input
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
        return;
      }
      const binding = KEYBINDINGS.find(b => b.key.toLowerCase() === e.key.toLowerCase());
      if (!binding) return;
      
      e.preventDefault();
      dispatch(binding.action);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [dispatch]);
  ```
- **Text Input Safety:** Check if `e.target` is an input or textarea. If so, do NOT fire (typing in a field must not trigger tools).
- **Modifier Keys:** Bindings ignore Ctrl/Shift/Alt unless specified in KEYBINDINGS.key (e.g., 'Shift+W' if needed).
- **Test:** 
  1. Press '1' on the map → tool switches to Residential.
  2. Click in a text input field, press '1' → no tool switch.
  3. Press '?' → help overlay opens.
  4. Press Escape or '?' again → help overlay closes.

---

### AC-4: Tool Selection Keys 1–9 (Existing, Documented)
**Number keys 1–9 map to PALETTE_FLAT entries and dispatch `{ type: 'tool', tool: { mode: 'build', spec: PALETTE_FLAT[n-1] } }`.**

- **Scope:** PALETTE_FLAT defines the available building specs by order (residential, commercial, industrial, etc.).
- **Binding:** Already implemented in MapView.tsx line 650–651; move into KEYBINDINGS registry (backward-compatible).
- **Test:** Press '1', verify Residential tool activates. Press '5', verify tool switches to PALETTE_FLAT[4].

---

### AC-5: Speed Control Keys
**Keyboard bindings for speed control dispatch `{ type: 'speed', speed: X }` where X ∈ {0, 1, 2, 3}.**

- **Bindings (per DD1 Option A — subject to Aaron's approval):**
  - `Space`: Toggle Play/Pause (if speed is 0, set to 1; if speed > 0, set to 0)
  - `[` (left bracket): Cycle down (3 → 2 → 1 → 0 → 3)
  - `]` (right bracket): Cycle up (0 → 1 → 2 → 3 → 0)
- **Fallback:** If Space conflicts, use `P` for Play and `0` for Pause (direct mapping).
- **Logic:**
  ```typescript
  { key: ' ', action: { type: 'speed', speed: state.speed === 0 ? 1 : 0 }, category: 'speed', label: 'Play / Pause' },
  { key: '[', action: { type: 'cycleSpeed', direction: -1 }, category: 'speed', label: 'Slower' },
  { key: ']', action: { type: 'cycleSpeed', direction: 1 }, category: 'speed', label: 'Faster' },
  ```
  (If cycleSpeed is new, add it to engine.ts Action union; otherwise compute the next speed inline in the handler.)
- **Test:**
  1. Press Space when paused (speed=0) → game resumes (speed=1).
  2. Press Space again → game pauses (speed=0).
  3. Press `]` three times from Pause → cycles 0 → 1 → 2 → 3.
  4. Press `[` from Turbo (3) → cycles 3 → 2.

---

### AC-6: Layer Toggle Keys
**Keyboard bindings for layer overlays dispatch existing toggle actions (or new `toggleLayer` action).**

- **Bindings (per DD2 Option A — subject to Aaron's approval):**
  - `W`: Toggle water network overlay → `{ type: 'toggleLayer', layer: 'water' }`
  - `P`: Toggle power infrastructure overlay → `{ type: 'toggleLayer', layer: 'power' }`
  - `L`: Toggle line saturation overlay → `{ type: 'toggleLayer', layer: 'lines' }`
  - `R`: Toggle building reference IDs overlay → `{ type: 'toggleLayer', layer: 'refs' }`
- **Alternative (if store doesn't have a unified toggle action):** Dispatch directly to MapView setShowWater/setShowPower/etc. via a context action (less clean but works if reducers can't absorb it).
- **Test:**
  1. Press 'W' on map → water overlay appears. Press 'W' again → overlay disappears.
  2. Press 'P' → power overlay toggles on/off.
  3. Verify each toggle updates the UI state and button active state (in MapView.tsx `map-zoom` button group, lines 876–902).

---

### AC-7: Camera Pan Keys
**Arrow keys dispatch camera pan actions (move cx/cy view center in the direction pressed).**

- **Bindings (per DD3 Option A):**
  - `ArrowUp`: Pan up (decrease cy by step) → `{ type: 'panCamera', dx: 0, dy: -step }`
  - `ArrowDown`: Pan down (increase cy by step) → `{ type: 'panCamera', dx: 0, dy: +step }`
  - `ArrowLeft`: Pan left (decrease cx by step) → `{ type: 'panCamera', dx: -step, dy: 0 }`
  - `ArrowRight`: Pan right (increase cx by step) → `{ type: 'panCamera', dx: +step, dy: 0 }`
- **Step size:** PLACEHOLDER (e.g., 5 tiles per keypress, or 1 tile per keypress). Tunable constant in keybindings.ts.
- **Clamping:** Pan is clamped to valid map bounds (like mouse pan), never goes off-map.
- **Logic:** Either new `panCamera` action in engine.ts, or compute new view in MapView useEffect and call setView.
- **Test:**
  1. Press ArrowUp → camera moves up by step tiles.
  2. Press ArrowDown → camera moves down.
  3. Pan to map edge (e.g., cy=0) and press ArrowUp → view clamps, no wrap-around.
  4. Repeated presses cycle through the map.

---

### AC-8: Camera Zoom Keys
**+/− keys (and fallback [ ] keys) dispatch zoom actions; Home key resets to fit-all.**

- **Bindings (per DD4 Option A):**
  - `+` (or `=` on US keyboard): Zoom in → multiply zoom by 1.5 (same as mouse wheel up) → `{ type: 'zoomCamera', factor: 1.5 }`
  - `−` (or `-`): Zoom out → multiply zoom by 1/1.5 → `{ type: 'zoomCamera', factor: 0.667 }`
  - `[`: Fallback zoom-out → `{ type: 'zoomCamera', factor: 0.667 }`
  - `]`: Fallback zoom-in → `{ type: 'zoomCamera', factor: 1.5 }`
  - `Home`: Reset to fit-all (zoom to MIN_ZOOM, center to map center) → `{ type: 'fitCamera' }`
- **Clamping:** Zoom is clamped to [MIN_ZOOM, MAX_ZOOM] (existing constants in MapView.tsx line 43–44: 1 to 48).
- **Logic:** Either new actions in engine.ts or compute in MapView and call setView (same as nudgeZoom button, line 617).
- **Test:**
  1. Press `+` twice → zoom increases from 2.2 to ~4.95.
  2. Press `−` five times from zoom=48 (MAX_ZOOM) → zoom clamps to 1 (MIN_ZOOM), does not go lower.
  3. Press `Home` → camera resets to zoom=1, center=(MAP_W/2, MAP_H/2), full map visible.

---

### AC-9: Help Overlay Toggle
**The `?` key (or fallback F1 per DD5) opens/closes the help overlay; Escape also closes it.**

- **Binding (per DD5 Option A):**
  - `?`: Toggle help overlay open/closed → `{ type: 'toggleHelp' }`
  - `F1`: Fallback (if ? not available per browser) → same action
  - `Escape`: Close help (already closes other modals/tools) → works if help is open
- **State:** Help overlay state is component-local (MapView state variable `const [helpOpen, setHelpOpen]`) or global in SimState (simpler for AC but changes the store).
  - **Recommendation:** Component-local (does not affect sim, does not go in journal, never resets on load). Add `helpOpen` to MapUiState (uistate.ts) for consistency with water/power toggles.
- **UI:** Modal overlay center-screen, semi-transparent backdrop, scrollable if tall, close button in corner + Escape key.
- **Test:**
  1. Press `?` → help overlay appears, showing all bindings grouped by category.
  2. Press `?` again → help overlay closes.
  3. Press Escape while help is open → help overlay closes.
  4. Verify help overlay renders EXACTLY from KEYBINDINGS (no hardcoded text).

---

### AC-10: No Binding Conflicts with Browser/OS
**Verify that no binding interferes with browser/OS shortcuts or breaks common workflows.**

- **Reserved keys (DO NOT BIND):**
  - `Ctrl+T`, `Ctrl+W` (new/close tab)
  - `Ctrl+R`, `F5` (reload)
  - `Ctrl+S` (save)
  - `Ctrl+P` (print)
  - `Ctrl+L` (address bar)
  - `Alt+F4` (close window)
- **Acceptable overlaps (user can still trigger if modifier pressed):**
  - Single-letter keys (W/P/L/R) can shadow Ctrl+W, Ctrl+P if user holds Ctrl → this is OK, not a blocker (Ctrl+W closes tab, not our binding)
  - Space can shadow form submission if focus is on a button with onClick — text-input safety (AC-3) mitigates most cases
- **Test:** Cross-browser (Chrome, Firefox, Safari) on macOS and Windows:
  1. Press each bound key (1–9, ?, W/P/L/R, [ ], +−, arrows, Home, Space).
  2. Verify none open unexpected dialogs (e.g., F1 opens browser help on Firefox).
  3. Verify Ctrl+Tab, Ctrl+W, Ctrl+S still work (not hijacked by handlers).
  4. Verify typing in a text input is not interrupted by single-letter bindings (AC-3 text-input safety).

---

### AC-11: Determinism — No Sim-State Mutation Outside Reducer
**All keyboard actions dispatch through the store reducer; no direct state mutation.**

- **Rule:** Every binding's action object (AC-1 `KeyBinding.action`) is a valid `Action` from engine.ts, dispatched via `dispatch(action)`.
- **Consequence:** Keyboard bindings do not mutate SimState outside the reducer. All game logic (speed, tool, layer toggles, camera) flows through the reducer and is deterministic.
- **Exception:** Camera pan/zoom/help-overlay are UI-only (MapView component-local state), so they're excluded from determinism constraints. But speed, tool, layers are store-backed, so they ARE deterministic.
- **Test:** 
  1. Keyboard action `{ type: 'speed', speed: 1 }` is identical to TopBar button click (both dispatch same action).
  2. Replay a recorded journal with keyboard inputs — verify identical tick sequence to original.
  3. No `setView(...)` calls outside the reducer; all camera changes go through a single canonical view setter.

---

### AC-12: No Silent Key-Binding Conflicts
**If two bindings map to the same key, the build fails (linter rule or test).**

- **Check:** Unit test in `keybindings.test.mjs` (or similar):
  ```typescript
  test('KEYBINDINGS: no duplicate keys', () => {
    const keys = KEYBINDINGS.map(b => b.key.toLowerCase());
    const unique = new Set(keys);
    assert.equal(keys.length, unique.size, `Duplicate keys: ${keys.filter((k, i) => keys.indexOf(k) !== i)}`);
  });
  ```
- **Failure:** If AC-12 fails, commit is blocked (add to pre-commit hook or CI).
- **Test:** Add a second binding with key 'w' → test fails. Remove it → test passes.

---

### AC-13: Help Overlay Does Not Depend on Hardcoded Strings
**Every string in HelpOverlay that describes a binding is derived from KEYBINDINGS (category names, labels, descriptions) — no magic strings.**

- **Auditable:** Grep HelpOverlay.tsx for literal strings matching binding labels (e.g., "Residential Zone", "Water", "Play / Pause") — should find ZERO hits (all text comes from KEYBINDINGS data).
- **Consequence:** If a binding's label changes (e.g., "Water" → "Water Network"), the overlay updates automatically.
- **Test:** 
  1. Modify KEYBINDINGS[i].label from "Water" to "Water Network".
  2. Render HelpOverlay.
  3. Verify overlay shows "Water Network", not "Water".
  4. Grep HelpOverlay source for "Water Network" → should fail (text is data-driven, not hardcoded).

---

### AC-14: Binding Registry Can Fail to Load Without Crashing App
**If KEYBINDINGS is corrupted or missing, the app still runs (keyboard handler gracefully ignores invalid entries).**

- **Guard:** Try-catch in keydown handler and HelpOverlay render.
- **Fallback:** If KEYBINDINGS is empty or malformed, keyboard has no effect (not a crash). If HelpOverlay fails to render, clicking `?` does nothing (not a crash).
- **Log:** Errors logged to console (error registry code if available, e.g., `UI-KEYBINDING-001`).
- **Test:** 
  1. Corrupt KEYBINDINGS (e.g., action.type missing).
  2. Press a key → handler catches error, logs warning, does not dispatch.
  3. App continues; map stays visible.
  4. Verify error logged (grep console output).

---

### AC-15: No Keyboard Bindings in Text Fields
**When focus is on a text input (e.g., building rename, chat), keyboard bindings are inactive; typing is never interrupted.**

- **Implementation:** AC-3 text-input check (`e.target instanceof HTMLInputElement`).
- **Scope:** All inputs/textareas (form fields, search boxes, future chat/chat features).
- **Test:**
  1. Click on a text input field.
  2. Type '1' → no tool switch, character '1' appears in field.
  3. Type 'w' → no water-layer toggle, character 'w' appears in field.
  4. Press Escape in a text field → close input (or standard behavior, if already defined).
  5. Verify Escape + help-overlay interaction: if help is open and user is in a text field, Escape closes help (bindings are inactive while typing).

---

## False-Pass Notes

### FP-1: Help Overlay Cosmetics
**An AC-2 test that renders HelpOverlay and visually inspects it does NOT verify it is derived from KEYBINDINGS.**
- **Why:** Visual inspection cannot prove data-driven origin.
- **Real check:** AC-2 and AC-13 linter/grep tests that verify no hardcoded strings match binding labels.

### FP-2: Keyboard Handler Works Everywhere
**A test that presses '1' on the map and confirms tool switches does NOT prove text-input safety (AC-15).**
- **Why:** The test never ran in a text input context.
- **Real check:** AC-15 test must explicitly click into a text field and verify '1' is typed, not dispatched.

### FP-3: Binding Unique Test Without Full Map Traversal
**A test that checks `KEYBINDINGS.map(b => b.key).length === new Set(...).size` does NOT guarantee no duplicates if the set's stringification is case-insensitive.**
- **Why:** `.toLowerCase()` normalization is transparent to the set if not applied before construction.
- **Real check:** AC-12 test must normalize keys BEFORE adding to set.

### FP-4: Camera Pan Without Clamping Test
**A test that pans from the map center and confirms camera moves does NOT verify bounds-clamping (AC-7).**
- **Why:** Center pans are always valid; edge case is required.
- **Real check:** AC-7 test must pan to edges, confirm clamp-to-bounds behavior.

---

## Testing Strategy

### Unit Tests (webconsole/test/keybindings.test.mjs)
- KEYBINDINGS structure: all keys unique, all action types valid (AC-1, AC-12)
- No hardcoded strings in HelpOverlay matching binding labels (AC-13)
- Text-input safety: onKeyDown skips inputs (AC-15)
- Binding registry scope: keyboard only, not mouse/UI (AC-6)

### Integration Tests (webconsole/test/keyboard-integration.test.mjs)
- Press each bound key on the map, verify correct dispatch (AC-3, AC-4, AC-5, AC-6, AC-7, AC-8, AC-9)
- Help overlay open/close toggle (AC-9)
- Camera pan/zoom/fit-all (AC-7, AC-8)
- Determinism: keyboard dispatch identical to button dispatch (AC-11)

### E2E / Dogfood Tests
- Launch webconsole, press keys, visually verify overlay toggles, speed changes, camera moves
- Confirm help overlay renders all bindings (AC-2)
- Confirm no browser shortcuts are hijacked (AC-10)

---

## Implementation Notes

### New Files
- `webconsole/src/sim/keybindings.ts` — KEYBINDINGS registry and TypeScript types
- `webconsole/src/components/HelpOverlay.tsx` — help modal component

### Modified Files
- `webconsole/src/components/MapView.tsx` — extend keydown handler to use KEYBINDINGS (AC-3), add help-overlay state
- `webconsole/src/sim/uistate.ts` — optionally add `helpOpen: boolean` to MapUiState for consistency with water/power
- `webconsole/src/sim/engine.ts` — optionally add new action types (`panCamera`, `zoomCamera`, `fitCamera`, `toggleHelp`, `toggleLayer`) if not already dispatch-able from components

### Existing State Used
- `state.speed` (engine.ts) — dispatches speed action
- `state.tool` (engine.ts) — dispatches tool action
- Component-local MapView state — `showWater`, `showPower`, `showLines`, `showRefs`, `view` (zoom/center) — keyboard toggles these
- Camera clamping — reuse existing `clampView(...)` function (MapView.tsx line 52–60)
- Layer rendering — reuse existing if blocks (MapView.tsx lines 343, 376, 417, 300)

---

## References

- **Existing keyboard handler:** MapView.tsx lines 642–656
- **Speed controls:** TopBar.tsx lines 9–74
- **Layer buttons:** MapView.tsx lines 876–902
- **Camera pan/zoom:** MapView.tsx lines 625–628, 617, 906
- **KEYBINDINGS pattern:** Inspired by GR#3 (SSOT), GR#15 (validators derive from data)
- **Help overlay pattern:** Common in games (pause menu) and web apps (keyboard shortcuts modal)

---

## Acceptance Criteria Summary

| AC   | Category | Check | Failure Mode |
|------|----------|-------|--------------|
| AC-1 | Registry | KEYBINDINGS defined, keys unique, actions valid | Build fails; linter catches duplicates |
| AC-2 | Overlay  | Help overlay renders from KEYBINDINGS | Overlay text doesn't match binding labels |
| AC-3 | Handler  | Global keydown listener uses KEYBINDINGS | Bindings don't fire or fire on wrong keys |
| AC-4 | Tools    | Keys 1–9 in KEYBINDINGS, dispatch tool actions | Tools don't switch on number press |
| AC-5 | Speed    | Space/[/] in KEYBINDINGS, dispatch speed action | Speed doesn't change on keypress |
| AC-6 | Layers   | W/P/L/R in KEYBINDINGS, toggle overlays | Layers don't toggle on keypress |
| AC-7 | Camera   | Arrows in KEYBINDINGS, pan camera (clamped) | Camera doesn't move or goes off-map |
| AC-8 | Camera   | +/−/Home in KEYBINDINGS, zoom and fit | Zoom doesn't change or fit doesn't reset |
| AC-9 | Help     | ? key toggles help overlay, Esc closes | Help overlay doesn't open/close |
| AC-10| Conflict | No browser/OS shortcuts hijacked | Ctrl+T, F5, etc. fail; print dialogs lost |
| AC-11| Determinism | All actions dispatch through reducer, no side effects | Keyboard action differs from button click |
| AC-12| Safety   | No duplicate binding keys; linter enforces | Two keys map to same action; random behavior |
| AC-13| Maintenance | HelpOverlay text is 100% data-driven | Labels hardcoded; manual sync required |
| AC-14| Robustness | Broken KEYBINDINGS doesn't crash app | Corrupted binding causes app failure |
| AC-15| UX       | Bindings inactive in text inputs | '1' switches tool instead of typing |

---

## Non-Acceptance Criteria (Out of Scope)

- **Customizable keybindings:** Players cannot rebind keys (future feature, FEAT-XXXX candidate).
- **Gamepad support:** Controller input (future feature).
- **Keyboard layout auto-detection:** QWERTY/AZERTY/Dvorak variants (future refinement; bindings are layout-specific today).
- **Macro/combo bindings:** Complex multi-key sequences (out of scope for baseline).
- **Remappable action names:** Action labels are hard-wired from KEYBINDINGS (not user-editable).
- **Accessibility keyboard shortcuts beyond text-input safety:** Voice control, screen-reader integration (separate epic).
