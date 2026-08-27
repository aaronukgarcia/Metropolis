// ErrorBoundary.tsx — catch a render/runtime crash instead of a white screen.
//
// Aaron, 2026-08-27: "the game should continue in most cases; if it doesn't then
// fair enough it crashes and the crash should be caught." A hot version upgrade
// should never reset the sim (see liveVersion.tsx), but if some future change
// genuinely breaks the React tree, we want a caught, visible error — not a blank
// page and not a silent reset. This boundary renders the error and offers a
// manual reload, and forwards the message to the error registry (recordError).

import { Component, type ErrorInfo, type ReactNode } from 'react';
import { recordError } from '../sim/backend';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // GR#1: surface the FULL context — the JS stack AND the React component tree
    // (info.componentStack = what triggered it, e.g. a useSim consumer rendered
    // outside SimProvider) — not just the message. This is an INTERNAL change;
    // ErrorBoundary's props are unchanged (App.tsx owns that).
    recordError(error.message, {
      type: 'render-crash',
      stack: error.stack,
      componentStack: info.componentStack ?? undefined,
    });
    // eslint-disable-next-line no-console
    console.error('[Metropolis] render crash caught by ErrorBoundary', error, info);
  }

  render() {
    const { error } = this.state;
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
