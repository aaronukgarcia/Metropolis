// keybindings.ts — FEAT-1972079861: Keyboard control scheme registry (SSOT per GR#3).
//
// Single source of truth for all keyboard bindings. The help overlay renders FROM
// this registry (never hardcoded strings). All actions dispatch through the store
// reducer or modify UI-only component state (camera, layers, help overlay).

import { PALETTE_FLAT } from './data';
import type { Tool } from './types';

export interface KeyBinding {
  /** Key code (e.g., '1', 'ArrowUp', '?', ' ', '[', ']') */
  key: string;
  /** Display label for the help overlay (e.g., 'Residential Zone', 'Play / Pause') */
  label: string;
  /** Optional longer description for the help overlay */
  description?: string;
  /** UI grouping category */
  category: 'tool' | 'speed' | 'layer' | 'camera' | 'help';
  /** Action to dispatch or handler function. For store actions, the type exists in
   * engine.ts Action union. For UI-only (camera/layers/help), the handler is inline
   * in MapView's keydown listener. */
  action?: { type: string; [key: string]: any };
  /** UI-only marker: if true, this binding is handled directly in MapView (no dispatch). */
  uiOnly?: boolean;
}

/**
 * KEYBINDINGS: Single source of truth for all keyboard bindings.
 * Grouped by category: tools (1–9), speed, layers, camera, help.
 */
export const KEYBINDINGS: readonly KeyBinding[] = [
  // ===== TOOLS (1–9) =====
  // Number keys 1–9 map to PALETTE_FLAT entries.
  ...PALETTE_FLAT.slice(0, 9).map((spec, i) => ({
    key: String(i + 1),
    label: spec,
    category: 'tool' as const,
    action: { type: 'tool', tool: { mode: 'build', spec } as Tool },
  })),

  // ===== SPEED CONTROL =====
  // Space = toggle play/pause (computed in handler based on current state)
  {
    key: ' ',
    label: 'Play / Pause',
    category: 'speed',
    description: 'Toggle between paused and playing',
    uiOnly: true, // Computed in handler
  },
  // [ and ] cycle through speeds
  {
    key: '[',
    label: 'Slower',
    category: 'speed',
    description: 'Cycle to slower speed (Pause ← Play ← Fast ← Turbo)',
    uiOnly: true,
  },
  {
    key: ']',
    label: 'Faster',
    category: 'speed',
    description: 'Cycle to faster speed (Pause → Play → Fast → Turbo)',
    uiOnly: true,
  },

  // ===== LAYER TOGGLES =====
  // W, P, L, R toggle the overlay layers (UI-only component state).
  {
    key: 'w',
    label: 'Water',
    category: 'layer',
    description: 'Toggle water network overlay',
    uiOnly: true,
  },
  {
    key: 'p',
    label: 'Power',
    category: 'layer',
    description: 'Toggle power infrastructure overlay',
    uiOnly: true,
  },
  {
    key: 'l',
    label: 'Lines',
    category: 'layer',
    description: 'Toggle line saturation overlay',
    uiOnly: true,
  },
  {
    key: 'r',
    label: 'Refs',
    category: 'layer',
    description: 'Toggle building reference IDs overlay',
    uiOnly: true,
  },

  // ===== CAMERA PAN (Arrow Keys) =====
  {
    key: 'ArrowUp',
    label: 'Pan Up',
    category: 'camera',
    description: 'Pan camera up',
    uiOnly: true,
  },
  {
    key: 'ArrowDown',
    label: 'Pan Down',
    category: 'camera',
    description: 'Pan camera down',
    uiOnly: true,
  },
  {
    key: 'ArrowLeft',
    label: 'Pan Left',
    category: 'camera',
    description: 'Pan camera left',
    uiOnly: true,
  },
  {
    key: 'ArrowRight',
    label: 'Pan Right',
    category: 'camera',
    description: 'Pan camera right',
    uiOnly: true,
  },

  // ===== CAMERA ZOOM & FIT =====
  {
    key: '+',
    label: 'Zoom In',
    category: 'camera',
    description: 'Zoom in (multiply zoom by 1.5)',
    uiOnly: true,
  },
  {
    key: '=',
    label: 'Zoom In',
    category: 'camera',
    description: 'Zoom in (multiply zoom by 1.5) — US keyboard',
    uiOnly: true,
  },
  {
    key: '-',
    label: 'Zoom Out',
    category: 'camera',
    description: 'Zoom out (multiply zoom by 0.667)',
    uiOnly: true,
  },
  {
    key: 'Home',
    label: 'Fit All',
    category: 'camera',
    description: 'Fit entire map to view',
    uiOnly: true,
  },

  // ===== HELP OVERLAY =====
  {
    key: '?',
    label: 'Help',
    category: 'help',
    description: 'Toggle help overlay',
    uiOnly: true,
  },
];

/**
 * Verify KEYBINDINGS at startup (runtime guard).
 * Returns an array of error messages; empty if all OK.
 */
export function validateKeybindings(bindings: readonly KeyBinding[]): string[] {
  const errors: string[] = [];
  const keys = new Set<string>();

  for (let i = 0; i < bindings.length; i++) {
    const b = bindings[i];
    const normalized = b.key.toLowerCase();

    // Check for duplicate keys
    if (keys.has(normalized)) {
      errors.push(`Duplicate key binding: '${b.key}' (appears at index ${i})`);
    }
    keys.add(normalized);

    // Check action validity (if not uiOnly, must have a type)
    if (!b.uiOnly && (!b.action || !b.action.type)) {
      errors.push(`Binding '${b.key}' (index ${i}): action.type is missing`);
    }
  }

  return errors;
}

/**
 * Find a binding by key (case-insensitive).
 */
export function findBinding(key: string): KeyBinding | undefined {
  return KEYBINDINGS.find((b) => b.key.toLowerCase() === key.toLowerCase());
}
