// RightDock.tsx — FEAT-2326609720 inc2, AC-3: RETIRED as a docked panel.
//
// Every tab this component used to host now has a new home in LeftDock's
// six-group tab tree (see §1's grouping table): `status`/`population` split
// into Population's Wellbeing/Housing/Demographics tabs + Build & Zoning's
// Structures tab; `rates`/`earnings`/`policy` moved to Finance's Tax
// Settings/Earnings/Policies; `units` moved to Build & Zoning's Reference;
// `power`/`water`/`waste` moved under Services' Utilities domain tab;
// `lines`/`xp`/`specialists` moved to Build & Zoning; `milestones` moved to
// Projections; `debug` moved to LeftDock as its own tab (its ENTRY is
// unconditional — only its cheat buttons stay DEV-gated, see the D1 fix note
// in LeftDock.tsx) (open question 3, recommendation (a)).
//
// D2 fix (independent round REJECT): the component renders nothing AND
// App.tsx no longer mounts it at all — the boundary was extended to cover
// App.tsx + styles.css for this fix, since a null-returning-but-still-mounted
// RightDock left a permanent blank 225px "bottom" grid row the map couldn't
// reclaim. The named exports below are kept ONLY because existing regression
// tests
// (test/mount.test.tsx, test/bug512-bug513-save-error-robustness.test.tsx)
// import PowerTab/WasteTab/EarningsTab/DebugTab directly from this module
// path — they now re-export the SAME components from their real new homes
// (GR#3: no duplicate copy, just a re-export shim for import-path stability).
export { PowerTab, WasteTab } from '../left/tabs/servicesTabs';
export { EarningsTab } from '../left/tabs/financeTabs';
export { DebugTab } from '../left/tabs/debugTab';

// AC-3 regression test asserts RightDock's rendered tab list no longer
// contains any of the eleven relocated ids (`rates`, `earnings`, `policy`,
// `power`, `water`, `waste`, `lines`, `xp`, `specialists`, `milestones`,
// `units`) — trivially true since this component renders no tabs at all.
export function RightDock() {
  return null;
}
