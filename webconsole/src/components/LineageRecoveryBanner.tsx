// LineageRecoveryBanner.tsx — BUG-1156.
//
// Non-blocking banner shown after a boot that REFUSED to restore the current
// lineage while savepoints still exist under OTHER lineage ids (a pointer
// mismatch, not a missing save). The fresh city the player is looking at is a
// SCRATCH city; this lists every recoverable lineage (id, tick, savedAt) with a
// one-click "Adopt" that makes it current and reloads. Chosen over the modal
// RebuildPrompt idiom because nothing here is a correctness gate: the player
// can keep playing the scratch city, and nothing is deleted either way.
// Presentational only — the store owns adoption (outside the reducer, GR#21).

import type { RecoverableLineage } from '../sim/replay';
import { Z_INDEX } from './overlayLayers';

export interface LineageRecoveryBannerProps {
  lineages: readonly RecoverableLineage[];
  onAdopt: (lineageId: string, idbOnly?: boolean) => void;
  onDismiss: () => void;
  /** BUG-1156 r3: honest situation line (e.g. the current save is corrupt but KEPT); shown even when no row is adoptable. */
  note?: string | null;
  /** 'adopted' when the running city IS an adopted scratch city (F4: do not claim it is temporary). */
  kind?: 'temporary' | 'adopted';
}

export function LineageRecoveryBanner({ lineages, onAdopt, onDismiss, note = null, kind = 'temporary' }: LineageRecoveryBannerProps) {
  if (lineages.length === 0 && !note) return null;
  return (
    <div
      className="lineage-recovery-banner"
      role="alert"
      style={{
        position: 'fixed',
        top: '8px',
        left: '50%',
        transform: 'translateX(-50%)',
        maxWidth: '90vw',
        padding: '8px 12px',
        fontSize: '12px',
        color: '#fff',
        background: '#a60',
        borderRadius: '4px',
        fontFamily: 'monospace',
        zIndex: Z_INDEX.LINEAGE_RECOVERY_BANNER,
      }}
    >
      <div>
        {note ? <span data-lineage-note>{note} </span> : null}
        {kind === 'adopted'
          ? 'Other saved cities are waiting — nothing was deleted. Find every city under File → Cities. Quick open:'
          : 'You are on a recovered session city — nothing was deleted, and it is saved like any other. Find every city under File → Cities. Quick open:'}
      </div>
      <ul style={{ margin: '6px 0', paddingLeft: '16px' }}>
        {lineages.map((l) => (
          <li key={l.lineageId} data-lineage={l.lineageId}>
            {l.scratch ? '[scratch] ' : ''}{l.label} ({l.lineageId}) — <strong>tick {l.snapshotTick}</strong>, saved {l.savedAt}
            {l.buildingCount !== undefined ? `, ${l.buildingCount} buildings` : ''}{' '}
            <button type="button" onClick={() => onAdopt(l.lineageId, l.idbOnly)}>
              Adopt
            </button>
          </li>
        ))}
      </ul>
      <button type="button" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  );
}
