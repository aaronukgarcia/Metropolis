// keyhandler.ts — FEAT-1972079861: Keyboard handler factory (real implementation).
//
// Extracted for testability: makeKeydownHandler builds the actual event handler
// that MapView uses. Tests spy on the dependencies to verify correct wiring.

import { findBinding } from './keybindings';

export interface View {
  zoom: number;
  cx: number;
  cy: number;
}

export interface KeydownHandlerDeps {
  // Dispatch store actions
  dispatch: (action: any) => void;
  // State read for speed cycling
  getState: () => { speed: 0 | 1 | 2 | 3 };
  // Camera operations (UI-only)
  setView: (v: View) => void;
  clampView: (v: View, w: number, h: number) => View;
  nudgeZoom: (factor: number) => void;
  // Layer toggles (UI-only component state)
  setShowWater: (v: boolean | ((prev: boolean) => boolean)) => void;
  setShowPower: (v: boolean | ((prev: boolean) => boolean)) => void;
  setShowLines: (v: boolean | ((prev: boolean) => boolean)) => void;
  setShowRefs: (v: boolean | ((prev: boolean) => boolean)) => void;
  // Help overlay state
  setHelpOpen: (v: boolean | ((prev: boolean) => boolean)) => void;
  helpOpen: boolean;
  // Camera state (for pan calculations)
  view: View;
  size: { w: number; h: number };
  // Map dimensions
  MAP_W: number;
  MAP_H: number;
  MIN_ZOOM: number;
  // Text-input safety check
  isTextInput: (target: any) => boolean;
  // Esc handling (MapView-specific)
  cancelToSelect: () => void;
}

/**
 * Factory: create the actual keydown event handler that MapView uses.
 * Returns a handler function that can be passed to addEventListener.
 * Tests inject spy deps to verify the handler calls the right things.
 */
export function makeKeydownHandler(deps: KeydownHandlerDeps): (e: KeyboardEvent) => void {
  return (e: KeyboardEvent) => {
    // SYSTEM KEY EXCEPTION (GR#3 audit note): Esc is handled OUTSIDE the KEYBINDINGS registry.
    // Rationale: Escape is a system cancel/close key with fixed priority semantics (help closes
    // first, then cancelToSelect), not a user-bindable action. It predates the keyboard scheme
    // and is non-negotiable for UX (every modal must close on Esc). Leaving it outside the
    // registry prevents it being accidentally rebindable and keeps the schema focused on game
    // actions. If future binding systems need customizable Escape, extract to registry then,
    // but for SSOT compliance it can stay as a hardcoded system binding.
    if (e.key === 'Escape') {
      e.preventDefault();
      if (deps.helpOpen) {
        deps.setHelpOpen(false);
        return;
      }
      deps.cancelToSelect();
      return;
    }

    // AC-15: Text-input safety — do not fire bindings when focus is on a text input.
    if (deps.isTextInput(e.target)) {
      // Allow Escape to close inputs (handled above)
      return;
    }

    // Look up binding by key (case-insensitive for letter keys)
    const binding = findBinding(e.key);
    if (!binding) return;

    e.preventDefault();

    // FEAT-1972079861: Handle keyboard bindings from registry.
    // Split: store-dispatched actions vs. UI-only state changes.

    if (binding.category === 'tool' && binding.action) {
      // Tool selection (1–9)
      deps.dispatch(binding.action as Parameters<typeof deps.dispatch>[0]);
    } else if (binding.category === 'speed') {
      // Speed control (Space, [, ])
      if (e.key === ' ' || e.key === 'Spacebar') {
        // Space: toggle play/pause (AC-5 Option A per ruling)
        const state = deps.getState();
        deps.dispatch({ type: 'speed', speed: state.speed === 0 ? 1 : 0 });
      } else if (e.key === '[') {
        // BUG-516: [ steps one speed SLOWER: Turbo(3) -> Fast(2) -> Play(1) -> Pause(0),
        // clamped at Pause (mirrors the linear 0..3 order of TopBar's speed buttons).
        const state = deps.getState();
        const next = Math.max(0, state.speed - 1) as 0 | 1 | 2 | 3;
        deps.dispatch({ type: 'speed', speed: next });
      } else if (e.key === ']') {
        // BUG-516: ] steps one speed FASTER: Pause(0) -> Play(1) -> Fast(2) -> Turbo(3),
        // clamped at Turbo (mirrors the linear 0..3 order of TopBar's speed buttons).
        const state = deps.getState();
        const next = Math.min(3, state.speed + 1) as 0 | 1 | 2 | 3;
        deps.dispatch({ type: 'speed', speed: next });
      }
    } else if (binding.category === 'layer') {
      // Layer toggles (W, P, L, R) — UI-only, no dispatch
      switch (e.key.toLowerCase()) {
        case 'w':
          deps.setShowWater((v) => !v);
          break;
        case 'p':
          deps.setShowPower((v) => !v);
          break;
        case 'l':
          deps.setShowLines((v) => !v);
          break;
        case 'r':
          deps.setShowRefs((v) => !v);
          break;
      }
    } else if (binding.category === 'camera') {
      // Camera pan/zoom/fit (Arrow keys, +/−, Home) — UI-only
      const PAN_STEP = 2; // tiles per keypress (tunable constant)
      switch (e.key) {
        case 'ArrowUp':
          deps.setView(deps.clampView({ ...deps.view, cy: deps.view.cy - PAN_STEP }, deps.size.w, deps.size.h));
          break;
        case 'ArrowDown':
          deps.setView(deps.clampView({ ...deps.view, cy: deps.view.cy + PAN_STEP }, deps.size.w, deps.size.h));
          break;
        case 'ArrowLeft':
          deps.setView(deps.clampView({ ...deps.view, cx: deps.view.cx - PAN_STEP }, deps.size.w, deps.size.h));
          break;
        case 'ArrowRight':
          deps.setView(deps.clampView({ ...deps.view, cx: deps.view.cx + PAN_STEP }, deps.size.w, deps.size.h));
          break;
        case '+':
        case '=':
          deps.nudgeZoom(1.5);
          break;
        case '-':
          deps.nudgeZoom(1 / 1.5);
          break;
        case 'Home':
          deps.setView({ zoom: deps.MIN_ZOOM, cx: deps.MAP_W / 2, cy: deps.MAP_H / 2 });
          break;
      }
    } else if (binding.category === 'help') {
      // Help overlay toggle (?)
      deps.setHelpOpen((v) => !v);
    }
  };
}
