// AffordabilityConfirm.tsx — BUG-652 follow-up, ROUND r3+r4 (2026-09-04).
//
// Round r2 (INDEPENDENT DESTRUCTIVE, GR#23) REJECTED the r2 estate's
// reducer-side affordability gate on two blocking findings: F1 — the gate
// lived inside the 'place' reducer case, which every replay path (savepoint
// tail, chunked tail, genesis rebuild) drives, so a PRE-EXISTING journal's
// 'place' entries (recorded before the gate existed, carrying no
// confirmation field) were SILENTLY DROPPED on the next load; F2 — the
// notice it set (SimState.affordabilityNotice) had NO reader anywhere under
// src/components, so a live player who tripped it got no building, no
// charge, and no message — a permanent, feedback-free dead end.
//
// THE FIX (r3): the gate moved entirely to the DISPATCH site. The reducer is
// pure again and always places. This component is the UI half of that move —
// component-local React state only (the caller's own useState), never
// SimState, never journaled — mirroring RebuildPrompt.tsx's presentational
// idiom: the caller owns the pending-confirmation state machine, this
// component just renders it and reports back via callbacks.
//
// ROUND r4 FIX: round r3's gate lived at exactly ONE dispatch site (the
// single-tile build click) and was bypassed by every BATCH path
// (drag-paint, stampRegion clone-paste, resolveDemand/resolveDemandAll) —
// see placementGate.ts's own header for the full finding. This component's
// props were generalised from a single-placement shape (spec/x/y) to a bare
// `message` string, since the caller now supplies a `commit` callback
// (placementGate.ts's PendingBatchPlacement) that already closes over
// whatever the ORIGINAL batch dispatch was — this component never needs to
// know whether it is confirming one building or a hundred.

import { Z_INDEX } from './overlayLayers';

export interface AffordabilityConfirmProps {
  /** Pre-formatted confirmation copy naming the batch's real recurring cost
   *  (data.ts's batchPlacementAffordability()'s own `message` field). */
  message: string;
  onConfirm: () => void;
  onCancel: () => void;
}

const overlayStyle: React.CSSProperties = {
  position: 'fixed',
  inset: 0,
  background: 'rgba(0,0,0,0.55)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  // Same rank as RebuildPrompt — a placement decision is exactly as blocking
  // as a boot-time rebuild decision (both must resolve before anything else
  // in the app can proceed sensibly).
  zIndex: Z_INDEX.REBUILD_PROMPT,
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
};

const panelStyle: React.CSSProperties = {
  background: 'var(--panel, #1b1f27)',
  color: 'var(--text, #e6e6e6)',
  border: '1px solid #e0a33e',
  borderRadius: '10px',
  padding: '18px 20px',
  maxWidth: '460px',
  width: '90vw',
  boxShadow: '0 8px 32px rgba(0,0,0,0.5)',
  fontSize: '13px',
  lineHeight: 1.5,
};

function button(primary: boolean): React.CSSProperties {
  return {
    flex: '1 1 auto',
    padding: '8px 12px',
    borderRadius: '6px',
    border: primary ? '1px solid #e0a33e' : '1px solid #444',
    background: primary ? '#e0a33e' : 'transparent',
    color: primary ? '#1b1f27' : 'var(--text, #e6e6e6)',
    cursor: 'pointer',
    fontFamily: 'inherit',
    fontSize: '12px',
  };
}

export function AffordabilityConfirm({ message, onConfirm, onCancel }: AffordabilityConfirmProps) {
  return (
    <div style={overlayStyle} role="dialog" aria-modal="true" aria-label="Confirm expensive placement">
      <div style={panelStyle}>
        <h2 style={{ margin: '0 0 8px', fontSize: '15px', color: '#e0a33e' }}>Big commitment</h2>
        <p style={{ margin: '0 0 8px' }}>{message}</p>
        <div style={{ display: 'flex', gap: '8px', marginTop: '16px', flexWrap: 'wrap' }}>
          <button style={button(true)} onClick={onConfirm}>
            Build anyway
          </button>
          <button style={button(false)} onClick={onCancel}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
