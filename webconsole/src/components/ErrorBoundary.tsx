// ErrorBoundary.tsx — catch a render/runtime crash instead of a white screen.
//
// Aaron, 2026-08-27: "the game should continue in most cases; if it doesn't then
// fair enough it crashes and the crash should be caught." A hot version upgrade
// should never reset the sim (see liveVersion.tsx), but if some future change
// genuinely breaks the React tree, we want a caught, visible error — not a blank
// page and not a silent reset. This boundary renders the error and offers a
// manual reload, and forwards the message to the error registry (recordError).

import { Component, type ErrorInfo, type ReactNode } from 'react';
import { recordError, normalizeThrowable, withTapSuppressed } from '../sim/backend';
import { versionRaw } from '../sim/version';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
  // BUG-434 round-r1 BAR-3: cascade errors (from failed cleanup/sibling components
  // after the first crash) are COUNTED but must never replace the DISPLAYED error —
  // the first error is the root cause; a cascade is noise caused by the first crash,
  // not a new incident. Surfaced in the details section below the primary message.
  cascadeCount: number;
  lastCascadeMessage: string | null;
  // FEAT-1972079916: track the registry-coded error record so we can display
  // code, correlationId, tick, version on the crash screen.
  errorRecord?: { code?: string; correlationId?: number; tick?: number };
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, cascadeCount: 0, lastCascadeMessage: null, errorRecord: undefined };
  private errorRecorded = false; // BUG-434: track if we've already recorded an error
  // BAR-F5 (round-r1): the first error's correlation id, so a later cascade
  // row can carry a link back to the root cause in the ring/debug.json.
  private firstCorrelationId: number | undefined = undefined;

  // BUG-434 round-r1 BAR-3 FIX: getDerivedStateFromError(error) receives ONLY the
  // error — React does NOT pass the current/prior state to it (unlike
  // getDerivedStateFromProps(props, state)) — so it has no way to know whether an
  // error has already been displayed and cannot implement "first-error-wins" on its
  // own. We still declare it (required for a class to be recognised as an error
  // boundary and to guarantee the crashed subtree is unmounted before paint), but it
  // returns `null` — no state change — deferring the actual decision to
  // componentDidCatch below, whose functional setState updater DOES see prior state.
  static getDerivedStateFromError(): Partial<State> | null {
    return null;
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // BUG-442: normalize the error in case it's not a real Error object
    const normalized = normalizeThrowable(error);

    // BUG-434 round-r1 BAR-3 FIX: first-error-wins DISPLAY semantics. The functional
    // setState updater sees the CURRENT state, so it can tell a first error (state.error
    // still null) from a cascade (state.error already set) and only ever grows
    // cascadeCount/lastCascadeMessage for the latter — the displayed `error` is set
    // exactly once, from the first crash, and never overwritten by a later one.
    this.setState((prev): State => {
      if (prev.error !== null) {
        return {
          error: prev.error,
          cascadeCount: prev.cascadeCount + 1,
          lastCascadeMessage: normalized.message,
          errorRecord: prev.errorRecord,
        };
      }
      return {
        error: normalized,
        cascadeCount: prev.cascadeCount,
        lastCascadeMessage: prev.lastCascadeMessage,
        errorRecord: prev.errorRecord,
      };
    });

    // BUG-434 capture-and-hold semantics for the DISPLAY: only the first error is
    // ever shown (see setState above). BAR-F5 (round-r1): the RING/debug.json is
    // different — every error, including cascades, gets its own row so no crash
    // is silently dropped from the record; a cascade row just carries
    // cascade:true + a firstCorrelationId link back to the root cause instead of
    // becoming the displayed error.
    // BAR-F1: a NAMED code on the thrown value (codedError) wins over the
    // generic MET-V801 RenderCrash fallback.
    const code = (normalized as any).code ?? 'MET-V801';

    if (this.errorRecorded) {
      // A cascade: record it linked to the first error, but do not touch
      // state.error/errorRecord (those stay pinned to the first crash).
      recordError(normalized.message, {
        type: 'render-crash',
        stack: normalized.stack,
        componentStack: info.componentStack ?? undefined,
        code,
        cascade: true,
        firstCorrelationId: this.firstCorrelationId,
      });
      return;
    }
    this.errorRecorded = true;

    // GR#1/GR#7: surface the FULL context — the JS stack AND the React component tree
    // (info.componentStack = what triggered it, e.g. a useSim consumer rendered
    // outside SimProvider) — not just the message. Record with a registry-sourced code.
    // This is an INTERNAL change; ErrorBoundary's props are unchanged (App.tsx owns that).
    const errorRecord = recordError(normalized.message, {
      type: 'render-crash',
      stack: normalized.stack,
      componentStack: info.componentStack ?? undefined,
      code,
    });
    this.firstCorrelationId = errorRecord.correlationId;

    // FEAT-1972079916: capture the error record's code and correlationId so we
    // can display them on the crash screen.
    this.setState((prev): State => ({
      ...prev,
      errorRecord: {
        code: errorRecord.code,
        correlationId: errorRecord.correlationId,
        tick: errorRecord.tick,
      },
    }));

    // BAR-F6 (round-r1): suppress the tap for this diagnostic log — it is
    // ABOUT the error already recorded above, not a new incident, and must not
    // mint a second (generic MET-V804) ring row for the same crash.
    withTapSuppressed(() => {
      // eslint-disable-next-line no-console
      console.error('[Metropolis] render crash caught by ErrorBoundary', error, info);
    });
  }

  render() {
    const { error, cascadeCount, lastCascadeMessage, errorRecord } = this.state;
    if (!error) return this.props.children;
    return (
      <div
        role="alert"
        style={{
          position: 'fixed',
          inset: 0,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: '12px',
          background: 'var(--bg, #0e1116)',
          color: 'var(--text, #e6e6e6)',
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
          padding: '24px',
          textAlign: 'center',
          zIndex: 10000,
        }}
      >
        <div style={{ fontSize: '15px', color: 'var(--danger, #e5534b)' }}>
          The console hit a render error and stopped.
        </div>

        {/* FEAT-1972079916: error envelope with code, correlation id, tick, version */}
        {errorRecord && (
          <div
            style={{
              fontSize: '11px',
              opacity: 0.8,
              maxWidth: '80ch',
              padding: '8px',
              background: 'var(--panel, #1b1f27)',
              border: '1px solid var(--border, #3d444d)',
              borderRadius: '4px',
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
              textAlign: 'left',
            }}
          >
            {errorRecord.code && (
              <div>
                <strong>Code:</strong> {errorRecord.code}
              </div>
            )}
            {errorRecord.correlationId && (
              <div>
                <strong>CorrelationID:</strong> {errorRecord.correlationId}
              </div>
            )}
            {errorRecord.tick !== undefined && (
              <div>
                <strong>Tick:</strong> {errorRecord.tick}
              </div>
            )}
            <div>
              <strong>Version:</strong> {versionRaw}
            </div>
          </div>
        )}

        <pre
          style={{
            maxWidth: '80ch',
            maxHeight: '40vh',
            overflow: 'auto',
            background: 'var(--panel, #1b1f27)',
            border: '1px solid var(--danger, #e5534b)',
            borderRadius: '8px',
            padding: '12px',
            fontSize: '12px',
            whiteSpace: 'pre-wrap',
          }}
        >
          {error.message}
        </pre>
        {cascadeCount > 0 && (
          <details style={{ fontSize: '11px', opacity: 0.7, maxWidth: '80ch' }}>
            <summary>
              {cascadeCount} cascade error{cascadeCount === 1 ? '' : 's'} suppressed after the first (root-cause) error
            </summary>
            {lastCascadeMessage && <div style={{ marginTop: '4px' }}>Last cascade: {lastCascadeMessage}</div>}
          </details>
        )}
        <button
          className="btn"
          onClick={() => window.location.reload()}
          style={{ pointerEvents: 'auto' }}
        >
          Reload console
        </button>
        <div style={{ fontSize: '11px', opacity: 0.7 }}>
          Your last autosave is preserved — a reload resumes from it.
        </div>
      </div>
    );
  }
}
