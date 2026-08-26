// debugactions.ts — FEAT-1972079885: the DebugTab's state-mutating dev
// buttons as a PURE data list, gated on the build mode.
//
// The buttons (+£10,000 / +500 XP / Force fast / Reset city) are developer
// cheats ported from the metropolis-ui fork's debug tab. Like the +£10m
// button (FEAT-1972079883, TopBar DevFundsButton) they must render ONLY in
// dev builds: the DebugTab calls `debugActions(import.meta.env.DEV)`, so a
// production `vite build` (DEV=false) gets an empty list and renders no
// mutation buttons at all. Keeping the list here (pure TS, no JSX, the
// gate as an argument) makes both the gating and the dispatched payloads
// unit-testable under node --test.

import type { Action } from './engine.ts';
import { fmtMoney } from './utils.ts';

export interface DebugActionDef {
  /** Stable identity for React keys and tests. */
  id: string;
  /** Button caption, pre-formatted (money through fmtMoney — GR#3). */
  label: string;
  /** Tooltip explaining the cheat. */
  title: string;
  /** Render with the destructive (red) button style. */
  danger?: boolean;
  /** The exact reducer action the button dispatches. */
  action: Action;
}

/** Funds granted by the Debug-tab money cheat (fork parity: +¤10,000 → +£10,000). */
export const DEBUG_FUNDS_GRANT = 10_000;
/** XP granted by the Debug-tab XP cheat (fork parity: +500 XP). */
export const DEBUG_XP_GRANT = 500;

const DEV_DEBUG_ACTIONS: readonly DebugActionDef[] = [
  {
    id: 'funds',
    label: `+${fmtMoney(DEBUG_FUNDS_GRANT)}`,
    title: `Dev only: grant ${fmtMoney(DEBUG_FUNDS_GRANT)}`,
    action: { type: 'debugFunds', amount: DEBUG_FUNDS_GRANT },
  },
  {
    id: 'xp',
    label: `+${DEBUG_XP_GRANT} XP`,
    title: `Dev only: grant ${DEBUG_XP_GRANT} XP (level rewards fire normally)`,
    action: { type: 'debugXp', amount: DEBUG_XP_GRANT },
  },
  {
    id: 'fast',
    label: 'Force fast',
    title: 'Dev only: jump the clock to maximum speed',
    action: { type: 'speed', speed: 3 },
  },
  {
    id: 'reset',
    label: 'Reset city',
    title: 'Dev only: wipe everything and restart from the M20 junction seed',
    danger: true,
    action: { type: 'reset' },
  },
];

/**
 * The dev buttons the DebugTab should render. `isDev` is the caller's
 * `import.meta.env.DEV`; production builds get an EMPTY list (no cheats).
 */
export function debugActions(isDev: boolean): readonly DebugActionDef[] {
  return isDev ? DEV_DEBUG_ACTIONS : [];
}
