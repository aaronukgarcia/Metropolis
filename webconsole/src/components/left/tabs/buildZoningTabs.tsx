// buildZoningTabs.tsx — FEAT-2326609720 inc2, Build & Zoning group child tabs.
//
// §1 rows 10/17/18/19/21: Structures is the REMAINDER of the old RightDock
// `status` tab (the "what's built" content, split away from Wellbeing/Housing
// which went to Population). Lines & Networks / Unlocks / Specialists /
// Reference are direct relocations of RightDock's `lines`/`xp`/`specialists`/
// `units` tabs — unchanged content, new home.

import { useSim } from '../../../sim/simContext';
import { levelOf, xpForLevel } from '../../../sim/engine';
import {
  FAMILIES,
  SPECS,
  UNIT_REGISTRY,
  PHYSICAL_ENTITIES,
  lineUsageOf,
} from '../../../sim/data';
import { fmtMoney, fmtNum } from '../../../sim/utils';
import { ragForLineSaturation, ragColor } from '../../ragThresholds';

// §1 row 10 — remainder of RightDock `status` (Structures table + fiscal hint).
export function StructuresTab() {
  const { state } = useSim();
  const income = state.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = state.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  return (
    <>
      <h4>Structures</h4>
      <table className="table">
        <thead>
          <tr><th>Type</th><th>Count</th><th>Upkeep</th></tr>
        </thead>
        <tbody>
          {FAMILIES.map((fam) => {
            let count = 0;
            let upkeep = 0;
            for (const b of state.buildings) {
              const sp = SPECS[b.spec];
              if (sp?.kind === fam.kind) {
                count++;
                upkeep += sp.upkeep;
              }
            }
            if (count === 0) return null;
            return (
              <tr key={fam.kind}>
                <td>
                  <span className="swatch" style={{ background: fam.color }} />
                  {fam.label}
                </td>
                <td>{count}</td>
                <td>{fmtMoney(upkeep)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="hint">
        Fiscal state {income - expense >= 0 ? 'solvent' : 'in deficit'} · net{' '}
        {fmtMoney(income - expense)}/tick
      </p>
    </>
  );
}

// §1 row 17 — relocated from RightDock `lines`.
export function LinesNetworksTab() {
  const { state } = useSim();
  const lines = lineUsageOf(state);
  return (
    <>
      <h4>Line saturation</h4>
      {lines.length === 0 && (
        <p className="muted">No road or rail lines on the map yet.</p>
      )}
      {lines.map((ln) => {
        const pct = Math.round(ln.saturation * 100);
        // §2 row 8 — RAG via ragForLineSaturation (reuses the 0.8 coverage line).
        const rag = ragForLineSaturation(ln.saturation, ln.overCapacity);
        const col = ragColor(rag);
        return (
          <div key={ln.spec} className="wb-row" title={
            `${ln.name}: ${fmtNum(ln.usage)} / ${fmtNum(ln.capacity)} per tick across ${ln.tiles} tile${ln.tiles === 1 ? '' : 's'}` +
            ` — headroom ${fmtNum(ln.headroom)}${ln.overCapacity ? ' (OVER CAPACITY)' : ''}`
          }>
            <span className="d-label">{ln.name}</span>
            <div className="d-bar">
              <span
                className={`d-fill ${ln.overCapacity ? 'neg' : 'pos'}`}
                style={{ left: 0, width: `${Math.max(0, Math.min(100, pct))}%`, background: col }}
              />
            </div>
            <span className="mono d-val" style={{ color: col }}>
              {pct}%
            </span>
          </div>
        );
      })}
      {lines.length > 0 && (
        <p className="hint">
          Usage is commuter flow (rail/HS1, from connected stations) or traffic demand (road/M20),
          against per-tile capacity. Green = headroom; amber ≥ 80%; red = over capacity.
          Capacities are placeholder-balance pending sign-off.
        </p>
      )}
    </>
  );
}

// §1 row 18 — relocated from RightDock `xp`.
export function UnlocksTab() {
  const { state } = useSim();
  const level = levelOf(state.xp);
  const cur = xpForLevel(level);
  const next = xpForLevel(level + 1);
  const frac = next > cur ? (state.xp - cur) / (next - cur) : 0;
  const byUnlock = new Map<number, string[]>();
  for (const sp of Object.values(SPECS)) {
    if (sp.unlock > 20 || sp.category === 'network') continue;
    const list = byUnlock.get(sp.unlock) ?? [];
    list.push(sp.name);
    byUnlock.set(sp.unlock, list);
  }
  return (
    <>
      <div className="xp-head">
        <span className="level-badge">{level}</span>
        <div>
          <b>City level {level}</b>
          <p className="muted">
            {fmtNum(state.xp)} XP · {fmtNum(Math.max(0, next - state.xp))} to level {level + 1}
          </p>
        </div>
      </div>
      <div className="xp-bar">
        <div style={{ width: `${Math.min(100, frac * 100)}%` }} />
      </div>
      <h4>Unlock ladder</h4>
      <table className="table">
        <thead>
          <tr><th>Lv</th><th>Unlocks</th></tr>
        </thead>
        <tbody>
          {Array.from({ length: 20 }, (_, i) => i + 1).map((lv) => (
            <tr key={lv} className={level >= lv ? '' : 'muted'}>
              <td>{lv}</td>
              <td>{(byUnlock.get(lv) ?? []).join(', ') || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="hint">XP accrues per tick (+1) and per structure placed (+4).</p>
    </>
  );
}

// §1 row 19 — relocated from RightDock `specialists`.
export function SpecialistsTab() {
  const { state } = useSim();
  const level = levelOf(state.xp);
  const landmarks = Object.values(SPECS).filter((sp) => sp.kind === 'landmark' || sp.id === 'uni');
  return (
    <ul className="specialist-list">
      {landmarks.map((sp) => {
        const locked = level < sp.unlock;
        const count = state.buildings.filter((b) => b.spec === sp.id).length;
        return (
          <li key={sp.id} className={locked ? 'locked' : ''}>
            <div>
              <b>{sp.name}</b>
              <p className="muted">{sp.blurb}</p>
            </div>
            <span className={`chip ${locked ? 'blocked' : 'done'}`}>
              {locked ? `Lv ${sp.unlock}` : `${count} built`}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

// §1 row 21 — relocated from RightDock `units`, as a low-frequency reference sub-tab.
export function ReferenceTab() {
  return (
    <>
      <p className="hint">Registry-sourced units — every displayed figure derives from these dimensions.</p>
      <table className="table">
        <thead>
          <tr><th>Unit</th><th>Dimension</th><th>Note</th></tr>
        </thead>
        <tbody>
          {UNIT_REGISTRY.map((u) => (
            <tr key={u.unit}>
              <td className="mono">{u.unit}</td>
              <td>{u.dimension}</td>
              <td className="muted">{u.note}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h4>Physical entities (metres)</h4>
      <table className="table">
        <thead>
          <tr><th>Entity</th><th>L × W × H</th></tr>
        </thead>
        <tbody>
          {PHYSICAL_ENTITIES.map((e) => (
            <tr key={e.id}>
              <td>{e.label}</td>
              <td className="mono">{e.x} × {e.y} × {e.z} m</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
