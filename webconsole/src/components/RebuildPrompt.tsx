// RebuildPrompt.tsx — FEAT-1972079897 inc2 UX (brief §4.4).
//
// Shown at boot when a persisted save was produced under a DIFFERENT build than
// the one now running. Offers the player three honest choices and, after a
// rebuild, a before/after report that NEVER claims pixel-identity across a rules
// change (brief §3).
//
// Default action is PROMPT — we deliberately do NOT auto-rebuild (brief open
// question 1, recommendation: always prompt; divergence can surprise). The whole
// component is presentational: every decision is driven by the props/callbacks
// the store passes in, so the store owns the state machine and this stays trivial.

import type { RebuildReport, SkippedAction, ReplayProgress } from '../sim/genesisReplay';

export type RebuildPhase = 'prompt' | 'running' | 'report' | 'stalled';

export interface RebuildPromptProps {
  phase: RebuildPhase;
  /** The build the save was stamped with (may be null on a legacy save). */
  savedVersion: string | null;
  /** The build now running. */
  currentVersion: string;
  /** The rebuild report, present only in the 'report' phase. */
  report: RebuildReport | null;
  /** Live progress updates during replay (present during 'running' phase). */
  progress: ReplayProgress | null;
  /**
   * BAR-2: a "~Xm Ys remaining" label derived from the LIVE observed
   * actions/sec (never a canned animation). Null until enough samples have
   * been collected to trust a rate, or when not applicable.
   */
  eta?: string | null;
  /** Stall information (present during 'stalled' phase). */
  stallInfo: { actionsDone: number; actionsTotal: number; phaseLabel: string } | null;
  /** Run genesis replay on the new engine. */
  onRebuild: () => void;
  /** Keep the old snapshot as-is (pre-inc2 behaviour). */
  onKeep: () => void;
  /** Discard and start from the pristine genesis city. */
  onFresh: () => void;
  /** Acknowledge the report and resume live on the new engine. */
  onResume: () => void;
  /** Retry a stalled rebuild. */
  onRetry?: () => void;
  /** Running-phase heading. Defaults to rebuild copy; Load Game passes a load label. */
  busyLabel?: string;
}

const overlayStyle: React.CSSProperties = {
  position: 'fixed',
  inset: 0,
  background: 'rgba(0,0,0,0.55)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 10000,
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
};

const panelStyle: React.CSSProperties = {
  background: 'var(--panel, #1b1f27)',
  color: 'var(--text, #e6e6e6)',
  border: '1px solid var(--accent, #4c8bf5)',
  borderRadius: '10px',
  padding: '18px 20px',
  maxWidth: '460px',
  width: '90vw',
  boxShadow: '0 8px 32px rgba(0,0,0,0.5)',
  fontSize: '13px',
  lineHeight: 1.5,
};

const btnRow: React.CSSProperties = {
  display: 'flex',
  gap: '8px',
  marginTop: '16px',
  flexWrap: 'wrap',
};

function button(primary: boolean): React.CSSProperties {
  return {
    flex: '1 1 auto',
    padding: '8px 12px',
    borderRadius: '6px',
    border: primary ? '1px solid var(--accent, #4c8bf5)' : '1px solid #444',
    background: primary ? 'var(--accent, #4c8bf5)' : 'transparent',
    color: primary ? '#fff' : 'var(--text, #e6e6e6)',
    cursor: 'pointer',
    fontFamily: 'inherit',
    fontSize: '12px',
  };
}

function fmt(n: number): string {
  return n.toLocaleString();
}

function signed(n: number): string {
  return (n > 0 ? '+' : '') + n.toLocaleString();
}

function MetricRow({ label, before, after, delta }: { label: string; before: number; after: number; delta: number }) {
  return (
    <tr>
      <td style={{ padding: '2px 8px 2px 0', opacity: 0.8 }}>{label}</td>
      <td style={{ padding: '2px 8px', textAlign: 'right' }}>{fmt(before)}</td>
      <td style={{ padding: '2px 8px', textAlign: 'right' }}>{fmt(after)}</td>
      <td style={{ padding: '2px 0 2px 8px', textAlign: 'right', color: delta === 0 ? 'inherit' : 'var(--accent, #4c8bf5)' }}>
        {signed(delta)}
      </td>
    </tr>
  );
}

function Spinner() {
  return (
    <div style={{
      display: 'inline-block',
      width: '16px',
      height: '16px',
      borderRadius: '50%',
      border: '2px solid rgba(76, 139, 245, 0.3)',
      borderTopColor: 'var(--accent, #4c8bf5)',
      animation: 'spin 0.8s linear infinite',
      marginRight: '8px',
      verticalAlign: 'middle',
    }} />
  );
}

function ProgressBar({ percent }: { percent: number }) {
  const bounded = Math.min(100, Math.max(0, percent));
  return (
    <div style={{ display: 'flex', gap: '8px', alignItems: 'center', margin: '10px 0' }}>
      <div style={{
        flex: 1,
        height: '24px',
        background: 'rgba(76, 139, 245, 0.1)',
        border: '1px solid var(--accent, #4c8bf5)',
        borderRadius: '4px',
        overflow: 'hidden',
        position: 'relative',
      }}>
        <div style={{
          position: 'absolute',
          height: '100%',
          width: `${bounded}%`,
          background: 'var(--accent, #4c8bf5)',
          transition: 'width 0.15s ease-out',
        }} />
        <div style={{
          position: 'absolute',
          width: '100%',
          height: '100%',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          fontSize: '11px',
          fontWeight: 'bold',
          color: bounded > 50 ? '#1b1f27' : 'var(--text, #e6e6e6)',
        }}>
          {bounded.toFixed(0)}%
        </div>
      </div>
    </div>
  );
}

function SkippedList({ skipped }: { skipped: SkippedAction[] }) {
  if (skipped.length === 0) {
    return <p style={{ margin: '10px 0 0', opacity: 0.75 }}>No actions were rejected by the new rules.</p>;
  }
  return (
    <div style={{ marginTop: '10px' }}>
      <p style={{ margin: '0 0 4px', color: '#e0a33e' }}>
        {skipped.length} action{skipped.length === 1 ? '' : 's'} skipped as invalid under the new rules:
      </p>
      <ul style={{ margin: 0, paddingLeft: '18px', maxHeight: '120px', overflowY: 'auto' }}>
        {skipped.slice(0, 20).map((s) => (
          <li key={s.index} style={{ opacity: 0.85 }}>
            <code>{s.type}</code> @tick {s.tick}: {s.error}
          </li>
        ))}
        {skipped.length > 20 && <li style={{ opacity: 0.6 }}>…and {skipped.length - 20} more</li>}
      </ul>
    </div>
  );
}

export function RebuildPrompt(props: RebuildPromptProps) {
  const { phase, savedVersion, currentVersion, report, progress, stallInfo, eta } = props;
  const busyLabel = props.busyLabel ?? 'Rebuilding your city…';

  return (
    <div style={overlayStyle} role="dialog" aria-modal="true" aria-label="Rebuild city on new build">
      <div style={panelStyle}>
        {phase === 'prompt' && (
          <>
            <h2 style={{ margin: '0 0 8px', fontSize: '15px' }}>New build detected</h2>
            <p style={{ margin: '0 0 8px' }}>
              Your saved city was built on <strong>{savedVersion ?? 'an earlier build'}</strong>. You are now running{' '}
              <strong>{currentVersion}</strong>.
            </p>
            <p style={{ margin: 0, opacity: 0.85 }}>
              <strong>Rebuild</strong> replays your recorded actions on the new engine — the corrected city your moves would
              produce under the new rules. It will not be pixel-identical to the old save, and that is expected.
            </p>
            <div style={btnRow}>
              <button style={button(true)} onClick={props.onRebuild}>
                Rebuild on {currentVersion}
              </button>
              <button style={button(false)} onClick={props.onKeep}>
                Keep old snapshot
              </button>
              <button style={button(false)} onClick={props.onFresh}>
                Start fresh
              </button>
            </div>
          </>
        )}

        {phase === 'running' && (
          <>
            <h2 style={{ margin: '0 0 8px', fontSize: '15px' }}>
              <Spinner />
              {busyLabel}
            </h2>
            {progress && (
              <>
                <p style={{ margin: '0 0 4px', opacity: 0.85, fontSize: '12px' }}>
                  {progress.phaseLabel}
                </p>
                <ProgressBar percent={(progress.actionsDone / progress.actionsTotal) * 100} />
                <p style={{ margin: '4px 0 0', opacity: 0.7, fontSize: '11px' }}>
                  {eta ? eta : 'estimating…'}
                </p>
              </>
            )}
            {!progress && (
              <p style={{ margin: 0, opacity: 0.85 }}>
                Replaying your recorded actions from genesis on <strong>{currentVersion}</strong>. This is headless and usually
                takes under a second.
              </p>
            )}
          </>
        )}

        {phase === 'stalled' && stallInfo && (
          <>
            <h2 style={{ margin: '0 0 8px', fontSize: '15px', color: '#ff6b6b' }}>Rebuild stalled</h2>
            <p style={{ margin: '0 0 8px', opacity: 0.85 }}>
              The rebuild stopped advancing at action <strong>{stallInfo.actionsDone.toLocaleString()}</strong> of{' '}
              <strong>{stallInfo.actionsTotal.toLocaleString()}</strong>.
            </p>
            <p style={{ margin: '0 0 8px', fontSize: '12px', opacity: 0.75 }}>
              {stallInfo.phaseLabel}
            </p>
            <p style={{ margin: 0, opacity: 0.75 }}>
              No progress was detected for several seconds. You can retry the rebuild or choose another option.
            </p>
            <div style={btnRow}>
              <button style={button(true)} onClick={props.onRetry}>
                Retry rebuild
              </button>
              <button style={button(false)} onClick={props.onKeep}>
                Keep old snapshot
              </button>
              <button style={button(false)} onClick={props.onFresh}>
                Start fresh
              </button>
            </div>
          </>
        )}

        {phase === 'report' && report && (
          <>
            <h2 style={{ margin: '0 0 8px', fontSize: '15px' }}>Rebuild complete</h2>
            <p style={{ margin: '0 0 10px', opacity: 0.85 }}>
              Rebuilt on <strong>{currentVersion}</strong>. Deterministic replay under new rules is not identical to the old
              save — the differences below are the corrected simulation, not a bug.
            </p>
            <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: '12px' }}>
              <thead>
                <tr style={{ opacity: 0.7 }}>
                  <th style={{ textAlign: 'left', padding: '0 8px 4px 0' }}></th>
                  <th style={{ textAlign: 'right', padding: '0 8px 4px' }}>old</th>
                  <th style={{ textAlign: 'right', padding: '0 8px 4px' }}>new</th>
                  <th style={{ textAlign: 'right', padding: '0 0 4px 8px' }}>Δ</th>
                </tr>
              </thead>
              <tbody>
                <MetricRow label="tick" before={report.before.tick} after={report.after.tick} delta={report.deltas.tick} />
                <MetricRow
                  label="population"
                  before={report.before.population}
                  after={report.after.population}
                  delta={report.deltas.population}
                />
                <MetricRow label="funds" before={report.before.funds} after={report.after.funds} delta={report.deltas.funds} />
                <MetricRow
                  label="buildings"
                  before={report.before.buildings}
                  after={report.after.buildings}
                  delta={report.deltas.buildings}
                />
              </tbody>
            </table>
            <SkippedList skipped={report.skipped} />
            <div style={btnRow}>
              <button style={button(true)} onClick={props.onResume}>
                Resume on {currentVersion}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
