import { useEffect, useRef, useState } from 'react';
import {
  createFpsTracker,
  createRenderThrottle,
  sampleFrame,
  fpsMetrics,
  tickMetrics,
  jsHeapMemory,
  networkMetrics,
  isRenderDue,
  getGlobalTickTracker,
  setFpsTracker,
  type PerfHudSnapshot,
  type FpsTrackerState,
  type RenderThrottle,
} from '../sim/perfhud';
// FEAT-webworker-sim-offload / FEAT-2326609734 (2026-09-02): queue-depth
// groundwork — the worker's backlog (ticks posted, not yet acknowledged),
// read from the same global-singleton idiom as getGlobalTickTracker above.
// Zero-lag (no worker, or the metropolis.webworker flag off) always reads 0.
import { getGlobalWorkerQueueTracker } from '../sim/workerQueueDepth';

/**
 * Performance HUD overlay — DEV-gated, corner-anchored.
 * Displays fps, frame time p95, memory (Chrome-only), sim-tick duration, and network.
 * Samples FPS every animation frame, renders the panel 1Hz.
 * Uses house palette colors (monospace, compact).
 *
 * Keyboard toggle: press 'P' to show/hide (when dev mode is active).
 */
export function PerfHud() {
  // Only render in dev builds
  if (!import.meta.env.DEV) return null;

  const [visible, setVisible] = useState(false);
  const [snapshot, setSnapshot] = useState<PerfHudSnapshot | null>(null);
  const [workerQueueDepth, setWorkerQueueDepth] = useState(0);
  // N1 fix / FEAT-2326609734 AC-7 honesty (independent round 2 REJECT,
  // 2026-09-02): depth() alone reads 0 both when genuinely caught up AND
  // when every tick is being discarded via supersede under sustained input
  // contention (each supersede drains its own slot) — the attacker flagged
  // this "silently reads caught up while the clock is stalled" gap twice.
  // supersedeStreak surfaces the difference.
  const [workerSupersedeStreak, setWorkerSupersedeStreak] = useState(0);

  const fpsTrackerRef = useRef<FpsTrackerState>(createFpsTracker());
  const renderThrottleRef = useRef<RenderThrottle>(createRenderThrottle());
  const tickTrackerRef = useRef(getGlobalTickTracker());

  // Keyboard shortcut: 'P' to toggle
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'p' || e.key === 'P') {
        e.preventDefault();
        setVisible((v) => !v);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  // Animation frame loop: sample FPS every frame, update snapshot 1Hz
  useEffect(() => {
    if (!visible) return;

    // Register the FPS tracker globally so debugjson can access it
    setFpsTracker(fpsTrackerRef.current);

    let frameId: number;
    const loop = () => {
      const nowMs = performance.now();
      sampleFrame(fpsTrackerRef.current, nowMs);

      // Render throttle: update snapshot only every 1000 ms
      if (isRenderDue(renderThrottleRef.current, nowMs)) {
        const snap: PerfHudSnapshot = {
          fps: fpsMetrics(fpsTrackerRef.current),
          tick: tickTrackerRef.current ? tickMetrics(tickTrackerRef.current) : { avgMs: 0, p95Ms: 0, worstMs: 0 },
          memoryBytes: jsHeapMemory(),
          network: networkMetrics(),
          snapshotAtMs: nowMs,
        };
        setSnapshot(snap);
        const tracker = getGlobalWorkerQueueTracker();
        setWorkerQueueDepth(tracker.depth());
        setWorkerSupersedeStreak(tracker.supersedeStreak());
      }

      frameId = requestAnimationFrame(loop);
    };

    frameId = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(frameId);
  }, [visible]);

  if (!visible || !snapshot) return null;

  return (
    <div className="perf-hud">
      <div className="perf-header">
        <span className="perf-title">Performance</span>
        <button
          className="perf-close"
          onClick={() => setVisible(false)}
          title="Close HUD (press P to toggle)"
        >
          ✕
        </button>
      </div>
      <div className="perf-metrics">
        <div className="perf-row">
          <span className="perf-label">FPS</span>
          <span className="perf-value">
            {snapshot.fps.avgFps.toFixed(1)} avg / {snapshot.fps.p95Fps.toFixed(1)} p95
          </span>
        </div>
        <div className="perf-row">
          <span className="perf-label">Frame</span>
          <span className="perf-value">
            {snapshot.fps.avgFrameMs.toFixed(1)}ms avg / {snapshot.fps.p95FrameMs.toFixed(1)}ms p95
          </span>
        </div>
        <div className="perf-row">
          <span className="perf-label">Tick</span>
          <span className="perf-value">
            {snapshot.tick.avgMs.toFixed(2)}ms avg / {snapshot.tick.p95Ms.toFixed(2)}ms p95
          </span>
        </div>
        <div className="perf-row">
          <span className="perf-label">Memory</span>
          <span className="perf-value">
            {snapshot.memoryBytes === null
              ? 'n/a (Chrome only)'
              : `${(snapshot.memoryBytes / 1024 / 1024).toFixed(1)} MB`}
          </span>
        </div>
        <div className="perf-row">
          <span className="perf-label">Network</span>
          <span className="perf-value">
            {snapshot.network.fetchCount} calls / {(snapshot.network.fetchBytes / 1024).toFixed(1)} KB
          </span>
        </div>
        {/* FEAT-webworker-sim-offload / FEAT-2326609734: sim-worker backlog
            readout — 0/"caught up" whenever the worker offload is
            disabled/unavailable (AC-7 zero-lag case) or genuinely idle.
            N1 fix (independent round 2 REJECT, 2026-09-02): a nonzero
            depth() (posted-not-yet-acked) OR a nonzero supersedeStreak
            (ticks being discarded under sustained input contention, which
            drains its own slot immediately so depth() alone reads 0) both
            surface as NOT caught up — depth()===0 no longer means "caught
            up" on its own. */}
        <div className="perf-row" data-testid="worker-queue-depth-row">
          <span className="perf-label">Sim queue</span>
          <span className="perf-value" data-testid="worker-queue-depth-value">
            {workerQueueDepth === 0 && workerSupersedeStreak === 0
              ? 'caught up'
              : workerSupersedeStreak > 0
                ? `${workerSupersedeStreak} superseded — catching up`
                : `${workerQueueDepth} pending`}
          </span>
        </div>
      </div>
      <div className="perf-footer">
        <span className="perf-hint">Press P to toggle · {snapshot.network.fetchCount === 0 ? 'no network activity' : 'active network'}</span>
      </div>
    </div>
  );
}
