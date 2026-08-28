import { useEffect } from 'react';
import type { Spec } from '../sim/data';
import { fmtMoney, fmtNum } from '../sim/utils';

// FEAT-1972079860 — Spec requirements and deliverables modal card.
// Displays unlock requirement and what a spec delivers (cost, jobs, served, mw,
// residents, tourism, upkeep). Sourced entirely from the Spec object (GR#3 SSOT).

interface SpecCardProps {
  spec: Spec | null;
  onClose: () => void;
}

export function SpecCard({ spec, onClose }: SpecCardProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  if (!spec) return null;

  // Build deliverables list from spec properties.
  // Only include properties that are defined and non-zero (except cost/upkeep which may be 0).
  const deliverables: Array<{ label: string; value: string }> = [];

  if (spec.residents && spec.residents > 0) {
    deliverables.push({ label: 'Houses', value: `${fmtNum(spec.residents)} residents` });
  }
  if (spec.jobs && spec.jobs > 0) {
    deliverables.push({ label: 'Provides', value: `${fmtNum(spec.jobs)} jobs` });
  }
  if (spec.mw && spec.mw > 0) {
    deliverables.push({ label: 'Generates', value: `${fmtNum(spec.mw)} MW of power` });
  }
  if (spec.served && spec.served > 0) {
    deliverables.push({ label: 'Serves', value: `${fmtNum(spec.served)} population` });
  }
  if (spec.tourism && spec.tourism > 0) {
    deliverables.push({ label: 'Attracts', value: `${fmtNum(spec.tourism)} tourists` });
  }
  if (spec.children && spec.children > 0) {
    deliverables.push({ label: 'Accommodates', value: `${fmtNum(spec.children)} children` });
  }

  // Cost and upkeep are always shown if non-zero or if cost is explicitly in the spec
  if (spec.cost > 0) {
    deliverables.push({ label: 'Costs', value: `${fmtMoney(spec.cost)} to place` });
  }
  if (spec.upkeep > 0) {
    deliverables.push({ label: 'Upkeep', value: `${fmtMoney(spec.upkeep)}/tick` });
  }

  return (
    <div className="spec-card-backdrop" onClick={onClose} role="presentation">
      <section
        className="panel spec-card-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="spec-card-title"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="panel-h spec-card-header">
          <span className="spec-card-swatch" style={{ background: spec.color }} />
          <span id="spec-card-title" className="panel-title">
            {spec.name}
          </span>
          <button className="btn tiny" onClick={onClose} aria-label={`Close ${spec.name}`}>
            Close
          </button>
        </header>

        <div className="panel-body spec-card-body">
          {/* Requirements section */}
          <div className="spec-card-section">
            <h3 className="spec-card-heading">Requirements</h3>
            <p className="spec-card-text">Unlocks at city level {spec.unlock}</p>
          </div>

          {/* Deliverables section */}
          {deliverables.length > 0 && (
            <div className="spec-card-section">
              <h3 className="spec-card-heading">What it delivers</h3>
              <ul className="spec-card-list">
                {deliverables.map((item, idx) => (
                  <li key={idx} className="spec-card-item">
                    <span className="spec-card-item-label">{item.label}</span>
                    <span className="spec-card-item-value">{item.value}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {/* Blurb section */}
          {spec.blurb && (
            <div className="spec-card-section">
              <h3 className="spec-card-heading">Details</h3>
              <p className="spec-card-blurb">{spec.blurb}</p>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
