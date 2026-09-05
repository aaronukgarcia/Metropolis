// debugTab.tsx — FEAT-2326609720 inc2, §1 row 22 relocation.
//
// Stays "exactly as-is" (§1 row 22 / AC-1: Debug is dev-gated, outside the
// six approved groups) — just relocated from RightDock into LeftDock's own
// dev-gated tab, per open question 3 recommendation (a). Content/logic
// UNCHANGED from the original RightDock DebugTab. RightDock.tsx re-exports
// this component under the SAME name because bug512-bug513 test imports
// DebugTab from '../src/components/right/RightDock.tsx'.

import { useEffect, useRef, useState } from 'react';
import { useSim } from '../../../sim/simContext';
import { useBusy } from '../../Busy';
import { commitDebug, debugSinkStatus, errorListModel, pendingCommits, recentErrors } from '../../../sim/backend';
import { debugActions } from '../../../sim/debugactions';
import { buildDebugJson, debugJsonText } from '../../../sim/debugjson';
import { foldGraceHistory, GRACE_RATE_WINDOW_SIZE } from '../../../sim/consistency';
import { currentMapUi } from '../../../sim/uistate';
import { versionRaw } from '../../../sim/version';
import { nextRefreshDue } from '../../../sim/throttle';
import { fmtNum } from '../../../sim/utils';

// FEAT-1972079880: the snapshot JSON used to be rebuilt from live state on
// EVERY render — the sim context updates each tick (as fast as 160 ms), so the
// <pre> text mutated constantly and text selection was destroyed before a
// human could copy it. The panel now freezes a "frame" (formatted text + the
// data it came from) and only retakes it when nextRefreshDue says a full
// SNAPSHOT_REFRESH_MS (15 s) has passed. Between refreshes the <pre> renders
// the SAME string, so React never touches the DOM text node and selection
// survives; the element itself is never re-mounted (stable tree position, no
// key changes). A 1 s wall-clock interval drives the countdown label — that
// updates a SIBLING element, which does not disturb selection inside the pre.
export function DebugTab() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(pendingCommits());
  // BUG-691: seed from backend.ts's module-level last-known reachability (NOT
  // component state) so a tab that was closed while the sink went down still
  // shows the true current status on mount, rather than resetting to "no
  // commit this session" and hiding the fact that the last real attempt
  // failed. Mirrors the quiet-indicator idiom store.tsx uses for autoSaveError.
  const [sinkDown, setSinkDown] = useState(() => debugSinkStatus().unreachable);

  // BUG-624/BUG-640: a rolling queue of PAST refreshes' raw (ungraced)
  // flows-check failure snapshots, held in a component ref — NOT SimState,
  // so determinism is untouched and buildDebugJson stays a pure function of
  // its explicit arguments. This is the windowed history for the two
  // grace-eligible per-line checks (consistency.ts's GRACE_ELIGIBLE_LINE_IDS):
  // a building whose construction completes exactly on a tick boundary makes
  // the recompute see one more online building than the actual flow charged
  // for — a transient, self-healing divergence, not a real defect (BUG-624).
  //
  // BUG-640 round 1 (independent round, 2026-09-03): the ORIGINAL
  // single-look-back ("did it fail on the immediately preceding refresh")
  // rule had a structural blind spot — a defect that raw-fails on every
  // OTHER refresh (alternating parity) always saw an empty history, because
  // the intervening healthy refresh reset it, and was graced FOREVER (20/20
  // tampered ticks graced in the attack round). The round-1 fix kept the
  // last `GRACE_WINDOW_SIZE - 1` raw-failure snapshots and folded them into
  // a per-id occurrence count.
  //
  // BUG-640 round 2 (independent REJECT round, same day): the round-1
  // bare-occurrence count had ITS OWN false-positive blind spot — a
  // steady, realistic every-3-tick build cadence reds 10%+ of refreshes,
  // because TWO INDEPENDENT genuine online-flip transients (different
  // buildings, different ticks) landing inside one window of each other
  // were miscounted as "the same defect recurring". The fix: track the
  // per-id DELTA (`rawFailedSignatures`, not just the bare id set) and only
  // count a repeat toward the threshold when it carries the SAME signature
  // (identical delta, sign and magnitude) — a persisting/recurring defect
  // reproduces the identical divergence every time, while unrelated
  // transients generically diverge by different amounts.
  //
  // BUG-640 round 4 (independent REJECT re-round, same day — "the gap-free
  // hole"): the round-3 sustained-divergence backstop only ever looked at
  // the last GRACE_WINDOW_SIZE (6) snapshots and required them to be
  // GAP-FREE — a drifting defect with a healthy check every 4th/5th/6th
  // check (duty cycle up to ~83%) never satisfies that and was graced
  // FOREVER. The fix adds a second, gap-TOLERANT "K of N raw-failure-rate"
  // backstop (GRACE_RATE_WINDOW_SIZE / GRACE_RATE_THRESHOLD — see
  // consistency.ts's doc comment for the measured tuning) that needs a much
  // LONGER trailing history than the fast tier to have enough data to ever
  // fire, so the rolling queue below now holds up to
  // `GRACE_RATE_WINDOW_SIZE - 1` (not `GRACE_WINDOW_SIZE - 1`) past
  // snapshots — foldGraceHistory derives BOTH the fast (gap-free-run) and
  // slow (rate) signals from however much history it is actually handed.
  //
  // Starts as an EMPTY array, not undefined — an empty history still folds
  // to an empty Map, which OPTS IN to grace ("no prior failures known yet"
  // is exactly the state of a freshly-mounted panel), whereas `undefined`
  // means "never grace" (that's what every one-shot caller — captureBeforeWipe,
  // every existing test — correctly wants). DebugTab is the continuously-
  // refreshing caller this mechanism exists for, so it opts in from the very
  // first frame.
  const graceHistoryRef = useRef<Array<Readonly<Record<string, number>>>>([]);

  // FEAT-1972079886: the frame is now the FULL-STATE debug.json — every UI
  // tab's status, raw numbers — built by the pure serializer in debugjson.ts.
  // Non-sim inputs (version, wall clock, map camera, captured errors) are
  // gathered HERE, at frame time, so the builder stays pure and the frozen
  // frame is a complete, self-consistent capture of that instant.
  const takeFrame = (s: typeof state) => {
    const at = Date.now();
    const dj = buildDebugJson(
      s,
      {
        appVersion: versionRaw,
        frameAtMs: at,
        map: currentMapUi(),
        errors: recentErrors(),
      },
      foldGraceHistory(graceHistoryRef.current),
    );
    // Carry THIS frame's raw failure SIGNATURES (id -> delta, BUG-640
    // round-2 — not the bare rawFailedLineIds ids, which lost the round-1
    // fix to the every-3-tick false-positive finding) forward into the
    // rolling window, trimmed to the last GRACE_RATE_WINDOW_SIZE - 1
    // snapshots (round-4 — long enough for the slow rate tier to ever have
    // data; foldGraceHistory derives the fast tier's shorter tail-run signal
    // from the same array).
    graceHistoryRef.current = [
      ...graceHistoryRef.current,
      dj.consistency.rawFailedSignatures,
    ].slice(-(GRACE_RATE_WINDOW_SIZE - 1));
    return { at, dj, text: debugJsonText(dj) };
  };
  const [frame, setFrame] = useState(() => takeFrame(state));
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    // BUG-721: window-bound + unref'd — see TopBar.tsx's EngineLagChip for
    // why the bare global setInterval/clearInterval pair is the wrong shape
    // under tsx/jsdom (dom.window.close() cannot stop a Node-bound timer).
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    (id as unknown as { unref?: () => void })?.unref?.();
    return () => window.clearInterval(id);
  }, []);
  // Retake the frame only when the 15 s window has elapsed. The effect closure
  // is recreated every render, so when `now` ticks it sees the LATEST state.
  useEffect(() => {
    if (nextRefreshDue(frame.at, now).due) setFrame(takeFrame(state));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [now]);

  const remainingS = Math.ceil(nextRefreshDue(frame.at, now).remainingMs / 1000);

  function commit() {
    run(async () => {
      setStatus('Committing debug.json…');
      // Commit exactly the frame on screen (WYSIWYG), not fresher live state.
      const r = await commitDebug(frame.dj);
      setStatus(r.message);
      setPending(pendingCommits());
      setSinkDown(debugSinkStatus().unreachable);
    });
  }

  // Download the EXACT on-screen frozen text as debug.json (WYSIWYG — the file
  // is byte-identical to the <pre> contents, not a fresher re-serialization).
  function download() {
    const blob = new Blob([frame.text], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'debug.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }
  // FEAT-1972079885: the state-mutating cheats are DEV-gated exactly like the
  // TopBar +£10m button — debugActions() returns [] in production builds, so
  // the row (and every cheat) vanishes from `vite build` output entirely.
  // BUG-412-class robustness: optional-chain `import.meta.env`, mirroring the
  // exact fix store.tsx already carries — under a bare tsx/Node test runtime
  // (no Vite) `import.meta.env` itself is undefined, and a non-optional
  // `.DEV` access here threw the moment any test rendered DebugTab.
  const devButtons = debugActions(import.meta.env?.DEV);
  const errList = errorListModel(recentErrors());

  return (
    <>
      {devButtons.length > 0 && (
        <div className="row-actions wrap">
          {devButtons.map((a) => (
            <button
              key={a.id}
              className={`btn${a.danger ? ' danger' : ''}`}
              title={a.title}
              onClick={() => dispatch(a.action)}
            >
              {a.label}
            </button>
          ))}
        </div>
      )}
      <div className="row-actions wrap">
        <button className="btn accent" title="Save the on-screen debug.json to the backend for processing (queues locally if offline)" onClick={commit}>
          Commit snapshot
        </button>
        <button className="btn" title="Save the on-screen frozen frame to a local debug.json file" onClick={download}>
          Download debug.json
        </button>
        <button
          className="btn"
          title="Retake the snapshot now instead of waiting for the 15 s refresh"
          onClick={() => setFrame(takeFrame(state))}
        >
          Refresh now
        </button>
        <span className="hint">
          {pending} queued · {status ?? 'no commit this session'}
        </span>
        {/* BUG-691: a quiet, PERSISTENT indicator (survives tab remounts —
            sourced from backend.ts's module state, not the ephemeral `status`
            string above) so a downed debug sink is never silent. Mirrors the
            "⚠ save" quiet-indicator idiom store.tsx uses for autoSaveError. */}
        {sinkDown && (
          <span className="hint" style={{ color: 'var(--warn, #d97706)' }} title="The metro MariaDB debug sink did not respond on the last commit attempt — commits are queuing locally (see MET-V857 in Errors captured below).">
            ⚠ debug sink down
          </span>
        )}
      </div>
      <h4>Errors captured</h4>
      {errList.empty ? (
        <p className="hint">No errors captured this session.</p>
      ) : (
        <ul className="error-list mono">
          {errList.rows.map((e) => (
            <li key={e.correlationId}>
              <details>
                <summary>
                  <span className="muted">{e.time}</span>{' '}
                  <span className="muted">#{e.correlationId}</span>{' '}
                  {/* BUG-513 gap 1 (GR#1 pillar-4): the registry code (MET-xxxx) is the
                      selectable identifier Aaron reports — show it prominently, falling
                      back to the type for older ring entries recorded before codes were
                      captured (never blank, never crash). */}
                  {/* BUG-513 gap-1 nit: `??` only falls back on null/undefined, so a
                      record with code:'' (an empty-but-present code) rendered a blank
                      `[]` instead of falling back to the type. `||` treats '' the same
                      as missing, matching the `e.code && ...` check just below. */}
                  <strong className="err-code" title={e.code ? undefined : 'no code captured for this entry'}>
                    [{e.code || e.type}]
                  </strong>{' '}
                  {e.code && <span className="muted">({e.type})</span>}{' '}
                  {e.msg}
                  {e.count > 1 && <span className="muted"> ×{e.count}</span>}
                </summary>
                <div className="error-detail">
                  {e.count > 1 && (
                    <div className="muted">
                      first {e.firstTime} · last {e.lastTime}
                    </div>
                  )}
                  {e.stateSummary && (
                    <div className="muted">
                      heap: tick {fmtNum(e.stateSummary.tick)} · funds{' '}
                      {fmtNum(e.stateSummary.funds)} · pop {fmtNum(e.stateSummary.population)} ·
                      speed {e.stateSummary.speed}
                    </div>
                  )}
                  {e.componentStack && (
                    <>
                      <div className="muted">component stack (trigger):</div>
                      <pre className="mono">{e.componentStack}</pre>
                    </>
                  )}
                  {e.stack && (
                    <>
                      <div className="muted">stack:</div>
                      <pre className="mono">{e.stack}</pre>
                    </>
                  )}
                </div>
              </details>
            </li>
          ))}
        </ul>
      )}
      <p className="hint">
        Snapshot taken {new Date(frame.at).toLocaleTimeString()} · next update in {fmtNum(remainingS)}s
        — frame is frozen so it can be selected and copied.
      </p>
      <pre className="debug-json mono">{frame.text}</pre>
    </>
  );
}
