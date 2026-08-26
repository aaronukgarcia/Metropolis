# SWITCHOVER — retire `E:\git\metropolis-ui`, play from `webconsole/`

**FEAT-1972079885** · 2026-08-26

`E:\git\metropolis-ui` was a stale fork of this app. Its every unique feature
(the Debug tab: dev cheat buttons, snapshot commit + local queue, errors-captured
list) now lives in the repo `webconsole/` — most in improved form (frozen
full-state debug.json frame, £ formatting, DEV-gated cheats). Nothing in the
fork is newer than what is here. Verified by whole-tree diff on 2026-08-26:
every fork-only line is a superseded older version of a landed feature.

## Steps

1. **Stop the old dev server** — the terminal running Vite out of
   `E:\git\metropolis-ui` (Ctrl+C in that window).
2. Open a fresh terminal:
   ```powershell
   cd E:\git\Metropolis\webconsole
   npm install
   npm run dev
   ```
   (`npm run dev` auto-generates `src/generated/version.ts` from git first —
   no manual version step.)
3. Browse to the URL Vite prints (default `http://localhost:5173`).
4. **Verify you are on the NEW app:** the top-left brand row shows a clickable
   **version badge — `v0.3.x`** (currently `v0.3.0-22-g864f3f4`; click it for
   About + changelog). The old fork has no badge and still shows `¤` currency.
5. Money renders as **£ with comma thousands** (e.g. `£10,000,000`) everywhere.
6. Once step 4–5 pass, **`E:\git\metropolis-ui` can be deleted** — nothing
   unique remains there. (Your browser's committed-snapshot queue and city are
   per-origin `localStorage`; a same-port dev server keeps them.)

## Feature-parity checklist (fork → webconsole)

| Fork feature | In webconsole? | Where / notes |
|---|---|---|
| Debug tab in Information panel | ✅ | RightDock `Debug` tab |
| `+¤10,000` dev button | ✅ improved | `+£10,000`, DEV-gated (dev builds only) |
| `+500 XP` dev button | ✅ improved | DEV-gated; level rewards fire normally |
| `Force fast` dev button | ✅ improved | DEV-gated |
| `Reset city` dev button | ✅ improved | DEV-gated (non-dev "Start Over" remains in the left dock) |
| `Commit snapshot` + local queue ("N queued") | ✅ improved | Commits the **frozen full-state debug.json frame** (WYSIWYG); queue in `localStorage`, cap 50 — see ASM-453 note in `src/sim/commitqueue.ts` |
| "Errors captured" list | ✅ | Same display; window error/unhandledrejection listeners in `main.tsx` |
| Live snapshot JSON `<pre>` | ✅ improved | 15 s frozen frame (selectable/copyable) + Download debug.json + Refresh now |
| Keyboard shortcuts (Esc, 1–9 palette) | ✅ identical | `MapView.tsx` |
| Sim logic (flows, demand, XP, refunds) | ✅ superset | Extracted to `src/sim/engine.ts`; adds level rewards, free zoning, placementCost refunds |
| Layout / palette / formatting | ✅ newer here | Relayout, palette tree, £/commas, version badge + About, 59-entry catalogue, zoning visuals, milestone rewards |

**Only-in-webconsole extras** (you gain these): version badge + About/changelog,
Download debug.json, Refresh-now, frozen-frame capture, milestone level rewards,
occupancy fills, 59-entry build catalogue, unit tests (`npm test`, 58 green).

## ASM-453 — the no-backend queue contract

There is no debug backend yet: `POST /api/debug/commit` always fails, so every
"Commit snapshot" is **queued client-side** in `localStorage`
(`metropolis.debugQueue`, newest-50 cap) and shown as "N queued". The queue
survives reloads and drains only when a backend exists (oldest first, remove on
2xx). Full contract: `src/sim/commitqueue.ts` header.
