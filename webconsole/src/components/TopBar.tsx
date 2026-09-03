import { useEffect, useRef, useState } from 'react';
import { useSim } from '../sim/simContext';
import { levelOf, xpForLevel, wellbeingOf, UNLOCK_ALL_COST } from '../sim/engine';
import { ragForWellbeing, ragColor } from './ragThresholds';
import { fmtMoney, fmtNum, gameDate } from '../sim/utils';
import { useLiveVersion } from '../sim/liveVersion';
import { TrendArrows } from './Trend';
import { useBusy } from './Busy';
import { AboutModal } from './About';
import { FileMenu } from './FileMenu';
import { ConfigMenu } from './ConfigMenu';
import { LiveEngineBadge } from './LiveEngineBadge';
import { engineLagTracker, engineLagClassOf, type EngineLagSnapshot } from '../sim/engineLag';
import { webWorkerOffloadEnabled } from '../sim/webWorkerFlag';
import { getGlobalWorkerQueueTracker } from '../sim/workerQueueDepth';
// import { StaleBuildBanner } from './StaleBuildBanner'; // BUG-564: unmounted, see comment at the former mount site below

const SPEEDS: { v: 0 | 1 | 2 | 3; label: string }[] = [
  { v: 0, label: 'Pause' },
  { v: 1, label: 'Play' },
  { v: 2, label: 'Fast' },
  { v: 3, label: 'Turbo' },
];

/** Safe performance.now() reader — mirrors the optional-chaining idiom used
 *  throughout store.tsx/perfhud.ts for a runtime that may not have a global
 *  `performance` (defensive only; every real target, including this
 *  codebase's own SSR smoke test, provides one). */
function nowMs(): number {
  return typeof performance !== 'undefined' ? performance.now() : 0;
}

/**
 * EngineLagChip — BUG-618 (P1): the ENGINE LAG GAUGE. ALWAYS rendered, no
 * dev gating, both worker-offload flag states — see engineLag.ts's header
 * for the full "what was wrongly built instead" context (a Construction
 * Queue tab and a QueueDepthHud worker line that says nothing useful in
 * Aaron's own flag-off play sessions).
 *
 * Three signals surfaced in the compact chip label / expandable popover:
 *   - TICK BACKLOG (engineLagTracker.recordTickScheduled/recordTickCompleted,
 *     fed by store.tsx's tick-driver interval and its two apply sites).
 *   - TICK COST RATIO (lastTickMs / intervalMs, same tracker).
 *   - STALL DETECTOR — this component's OWN requestAnimationFrame heartbeat
 *     below feeds engineLagTracker.recordFrameGap: a gap between two
 *     consecutive painted frames > STALL_THRESHOLD_MS means the main thread
 *     was blocked. Retroactive by construction — a stall cannot be observed
 *     while it is happening (the thread is blocked, nothing can run) so it
 *     is reported the instant the NEXT frame finally paints, exactly per
 *     the brief's "on the next paint show ... for a few seconds".
 *
 * Pure UI-layer instrumentation: no SimState read beyond the single `speed`
 * prop below (never any other SimState field), no Date.now, no
 * reducer/capture-path involvement (GR#21/GR#27) — see engineLag.ts's header.
 *
 * F1 fix (independent round REJECT, 2026-09-03, "the killer — pause
 * honesty"): `speed` is TopBar's own `state.speed` (the same value already
 * driving store.tsx's tick-driver effect) passed straight through — while
 * paused (speed === 0) the chip reads "Engine: paused" (green) regardless of
 * whatever backlog/ratio the tracker still holds from the instant before
 * pause, rather than trusting store.tsx's engineLagTracker.settle() call to
 * have already landed by render time (it is dispatched from a useEffect,
 * which commits AFTER paint — a naive backlog-only read could flash "N
 * behind" for one frame on every pause). A stall detected WHILE paused still
 * overrides to "stalled" — a blocked main thread is a real fact regardless
 * of sim speed, never suppressed just because the game clock isn't ticking.
 */
export function EngineLagChip({ speed }: { speed: number }) {
  const [snapshot, setSnapshot] = useState<EngineLagSnapshot>(() => engineLagTracker.snapshot(nowMs()));
  const [open, setOpen] = useState(false);
  const lastFrameRef = useRef<number | null>(null);
  const rafIdRef = useRef<number | null>(null);

  // Subscribe to every tracker mutation (backlog/duration/interval/stall).
  useEffect(() => engineLagTracker.subscribe(setSnapshot, nowMs()), []);

  // A stall's "for a few seconds" display window must expire even with NO
  // new tracker mutation in between (nothing else may fire for seconds after
  // a stall clears) — a light poll re-reads the snapshot so recentStallMs
  // reliably reverts to null once STALL_DISPLAY_MS has elapsed.
  useEffect(() => {
    const id = setInterval(() => setSnapshot(engineLagTracker.snapshot(nowMs())), 500);
    return () => clearInterval(id);
  }, []);

  // Stall-detector heartbeat: requestAnimationFrame is unavailable under
  // SSR/node-test rendering (mount.test.tsx's renderToString) — skip
  // gracefully rather than throw, exactly the AC-8-style degrade convention
  // used elsewhere in this codebase for an absent browser API.
  useEffect(() => {
    if (typeof requestAnimationFrame === 'undefined' || typeof cancelAnimationFrame === 'undefined') {
      return undefined;
    }
    let cancelled = false;
    const loop = (t: number) => {
      if (cancelled) return;
      if (lastFrameRef.current !== null) {
        engineLagTracker.recordFrameGap(t - lastFrameRef.current, t);
      }
      lastFrameRef.current = t;
      rafIdRef.current = requestAnimationFrame(loop);
    };
    rafIdRef.current = requestAnimationFrame(loop);
    return () => {
      cancelled = true;
      if (rafIdRef.current !== null) cancelAnimationFrame(rafIdRef.current);
    };
  }, []);

  // F1: paused (speed 0) always reads as honest/green UNLESS a stall is
  // actively being reported — see the component header comment above.
  const paused = speed === 0;
  const stalled = snapshot.recentStallMs !== null;
  const cls = stalled ? 'red' : paused ? 'green' : engineLagClassOf(snapshot);
  const label = stalled
    ? `Engine: stalled ${(snapshot.recentStallMs! / 1000).toFixed(1)}s`
    : paused
      ? 'Engine: paused'
      : snapshot.backlog > 0
        ? `Engine: ${snapshot.backlog} behind`
        : 'Engine: OK';

  const workerOn = webWorkerOffloadEnabled();
  const workerQueueDepth = workerOn ? getGlobalWorkerQueueTracker().depth() : null;

  return (
    <span className="stat">
      <button
        type="button"
        className={`engine-lag-chip ${cls}`}
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        title="Engine lag gauge (BUG-618) — click for backlog/tick-cost/stall detail"
      >
        <span className="engine-lag-dot" />
        {label}
      </button>
      {open && (
        <div className="engine-lag-popover" role="dialog" aria-label="Engine lag detail">
          <dl>
            <dt>Backlog</dt>
            <dd>{snapshot.backlog}</dd>
            <dt>Last tick</dt>
            <dd>{snapshot.lastTickMs != null ? `${snapshot.lastTickMs.toFixed(1)} ms` : '—'}</dd>
            <dt>Interval</dt>
            <dd>{snapshot.intervalMs != null ? `${snapshot.intervalMs} ms` : '—'}</dd>
            <dt>Ratio</dt>
            <dd>{snapshot.ratio != null ? `${snapshot.ratio.toFixed(2)}x` : '—'}</dd>
            <dt>Worst stall</dt>
            <dd>{snapshot.worstStallMs > 0 ? `${(snapshot.worstStallMs / 1000).toFixed(1)} s` : 'none'}</dd>
            {workerOn && (
              <>
                <dt>Worker queue</dt>
                <dd>{workerQueueDepth}</dd>
              </>
            )}
          </dl>
        </div>
      )}
    </span>
  );
}

export function TopBar() {
  const { state, dispatch } = useSim();
  const version = useLiveVersion();
  const [aboutOpen, setAboutOpen] = useState(false);
  const level = levelOf(state.xp);
  const cur = xpForLevel(level);
  const next = xpForLevel(level + 1);
  const frac = Math.min(100, ((state.xp - cur) / Math.max(1, next - cur)) * 100);
  const wb = wellbeingOf(state);
  // BUG-580: was a local nested ternary duplicating ragThresholds.ts's
  // RAG_THRESHOLDS.WELLBEING (GREEN 70 / AMBER 45, HUD inc2 FEAT-2326609720)
  // — now the single source (AC-8): populationTabs.tsx's WellbeingTab
  // consumes the SAME ragForWellbeing/ragColor pair, so a future retune of
  // RAG_THRESHOLDS.WELLBEING moves both.
  const wbColor = ragColor(ragForWellbeing(wb.overall));
  return (
    <header className="topbar">
      {/* FEAT-2326609725 / BUG-564: StaleBuildBanner UNMOUNTED (Aaron, 2026-09-02).
          The detection misfires in active dev: "running" (APP_VERSION_SHA) is
          frozen at dev-server START while "disk" (live git HEAD, recomputed by
          the /version.json middleware) advances on every commit — so after any
          commit the banner shows a permanent mismatch that Reload can NEVER
          clear (only a dev-server restart re-stamps the running sha), even
          though vite HMR has kept the actual running code current. A warning
          the player cannot act on is worse than none. The component + its
          tests stay; re-mount only after the BUG-564 rework (detect genuine
          staleness via HMR-connection liveness, not sha comparison).
      <StaleBuildBanner /> */}
      <div className="brand">
        <span className="brand-mark" />
        Metropolis
        <span className="muted">Command Console</span>
        <FileMenu />
        <ConfigMenu />
        <button
          className="version-badge mono"
          title={`Version ${version.raw} — updates hot on each commit; click for About & changelog`}
          onClick={() => setAboutOpen(true)}
        >
          {version.label}
        </button>
        {/* FEAT-1972079852 inc1: dev-only, feature-flagged (off by default via
            localStorage 'metropolis.liveEngine') live Go engine indicator.
            Renders null unless explicitly enabled -- never affects the mock
            sim's own funds/tick display above/below. */}
        <LiveEngineBadge />
      </div>
      <div className="top-stats">
        <span className="stat acc">
          {fmtMoney(state.funds)}
          <TrendArrows series={state.history.map((h) => h.funds)} gentle={15} fast={150} label="Treasury" />
        </span>
        <span className="stat">
          {fmtNum(state.population)} citizens
          <TrendArrows series={state.history.map((h) => h.population)} gentle={0.05} fast={1} label="Population" />
        </span>
        <span className="stat" title={`Wellbeing ${wb.overall}/100`}>
          <span className="wb-dot" style={{ background: wbColor }} />
          {wb.overall}
          <span className="muted"> wellbeing</span>
          <span className="xp-mini">
            <span style={{ width: `${wb.overall}%`, background: wbColor }} />
          </span>
        </span>
        <span className="stat mono">{gameDate(state.tick)}</span>
        <EngineLagChip speed={state.speed} />
        {/* BUG-497 (2): once declineState is set advance() freezes the clock forever
            BY DESIGN (see engine.ts's declineState hard-stop) — a frozen clock with
            no signal reads as a hang. This badge makes the freeze unmistakably
            intentional wherever the HUD is visible (defence-in-depth alongside the
            DeclineScreen overlay itself, which is the primary game-over surface). */}
        {state.declineState && (
          <span
            className="stat sim-ended-badge"
            role="status"
            title="Persistent insolvency ended the game — the clock is frozen by design, not hung."
          >
            ⏸ SIMULATION ENDED
          </span>
        )}
        <span className="stat" title={`Level ${level}: ${state.xp} XP`}>
          Lv {level}
          <span className="xp-mini">
            <span style={{ width: `${frac}%` }} />
          </span>
        </span>
      </div>
      <div className="speed-ctl">
        {SPEEDS.map((s) => (
          <button
            key={s.v}
            className={`btn tiny${state.speed === s.v ? ' active' : ''}`}
            onClick={() => dispatch({ type: 'speed', speed: s.v })}
          >
            {s.label}
          </button>
        ))}
      </div>
      {aboutOpen && <AboutModal onClose={() => setAboutOpen(false)} />}
    </header>
  );
}

// StartOverButton — relocated out of the TopBar to the LEFT dock (FEAT-1972079874).
// Kept as its own component so it can live inside the build column while still
// reaching the sim dispatch + busy overlay.
export function StartOverButton() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  // Unlock-all is a large cash gate (FEAT-1972079899). Disabled when already unlocked
  // or when the treasury cannot cover UNLOCK_ALL_COST — the reducer is all-or-nothing,
  // so the control never partially applies.
  // DEV-GATED (Aaron ruling Q100047 c = C2): this was a permanently-visible priced
  // gameplay button (a cash god-mode sink) — it must render only in dev builds,
  // same import.meta.env.DEV idiom as DevFundsButton/DevFundsLargeButton below.
  // The reducer action (`unlockAll`) and its cost logic are NOT removed — only the
  // button's production visibility is gated; dev builds and tests still exercise it.
  const canUnlockAll = !state.unlockedAll && state.funds >= UNLOCK_ALL_COST;
  const unlockTitle = state.unlockedAll
    ? 'God mode: every structure already unlocked'
    : state.funds < UNLOCK_ALL_COST
      ? `God mode: unlock all structures — needs ${fmtMoney(UNLOCK_ALL_COST)} in the treasury`
      : `God mode: unlock every structure now for ${fmtMoney(UNLOCK_ALL_COST)}`;
  return (
    <div className="start-over-row">
      <button
        className="btn danger start-over"
        title="God mode: wipe everything and restart from the M20 junction seed"
        onClick={() => run(() => dispatch({ type: 'reset' }))}
      >
        Start Over
      </button>
      {import.meta.env?.DEV && (
        <button
          className="btn accent unlock-all"
          disabled={!canUnlockAll}
          title={unlockTitle}
          onClick={() => run(() => dispatch({ type: 'unlockAll' }))}
        >
          {state.unlockedAll ? 'All Unlocked' : 'Unlock All'}
        </button>
      )}
      <DevFundsButton />
      <DevFundsLargeButton />
    </div>
  );
}

/** Amount granted by the dev funds button (FEAT-1972079883). */
export const DEV_FUNDS_GRANT = 10_000_000;

/** Amount granted by the large dev funds button (FEAT-2326609716; raised
 *  1B -> 1T per Aaron's Q100094 ruling, 2026-09-03: "change the +1B to make
 *  it 1T (trillion)"). */
export const DEV_FUNDS_GRANT_LARGE = 1_000_000_000_000;

// DevFundsButton — DEV-ONLY debug helper (FEAT-1972079883) sitting next to
// Start Over. Renders only when import.meta.env.DEV is true, so a production
// `vite build` (DEV=false) omits it entirely. Grants +£10m via debugFunds.
export function DevFundsButton() {
  const { dispatch } = useSim();
  if (!import.meta.env?.DEV) return null;
  return (
    <button
      className="btn accent dev-funds"
      title={`Dev only: grant ${fmtMoney(DEV_FUNDS_GRANT)}`}
      onClick={() => dispatch({ type: 'debugFunds', amount: DEV_FUNDS_GRANT })}
    >
      +£10m
    </button>
  );
}

// DevFundsLargeButton — DEV-ONLY sibling of DevFundsButton
// (FEAT-2326609716): identical gating (import.meta.env.DEV, omitted from
// production builds) and identical debugFunds path, granting +£1B for
// start-over/big-capex testing. Rendered immediately next to the +£10m
// button wherever DevFundsButton is mounted.
export function DevFundsLargeButton() {
  const { dispatch } = useSim();
  if (!import.meta.env?.DEV) return null;
  return (
    <button
      className="btn accent dev-funds"
      title={`Dev only: grant ${fmtMoney(DEV_FUNDS_GRANT_LARGE)}`}
      onClick={() => dispatch({ type: 'debugFunds', amount: DEV_FUNDS_GRANT_LARGE })}
    >
      +£1T
    </button>
  );
}
