// FEAT-1972079926 — arrivals-by-mode Sankey. Inline SVG, no new dependencies.
// Renders the REAL recorded monthly arrivals-by-mode split (GR#15: never a
// fabricated split) — five left sources (Road, Low-speed rail, HS rail, Sea,
// Plane) flow into a single right "Move-ins" node. Band widths are
// proportional to the actual recorded totals over the selected window. When
// there is no recorded history yet (a fresh city, or a legacy save predating
// this feature) the panel renders an honest empty state instead of inventing
// numbers. Mirrors PopulationSankey.tsx's (FEAT-1972079925) structure.

import { useState } from 'react';
import type { MonthlyArrivalsByMode } from '../sim/types';
import { fmtNum } from '../sim/utils';
import { arrivalsByModeSankeyModel } from './arrivalsByModeSankeyModel.ts';
import type { SankeyWindow } from './arrivalsByModeSankeyModel.ts';

export { arrivalsByModeSankeyModel };
export type { SankeyWindow, ArrivalsByModeFlows } from './arrivalsByModeSankeyModel.ts';

const COL_LEFT = 30;
const COL_RIGHT = 260;
const NODE_W = 26;
const CHART_TOP = 20;
const CHART_H = 200;
const GAP = 6;

interface Band {
  label: string;
  value: number;
  y0: number;
  y1: number;
}

/** Stack a set of {label,value} rows top-to-bottom within [top, top+height], with a fixed gap between bands, proportioned by value. */
function stack(rows: { label: string; value: number }[], top: number, height: number, gap: number): Band[] {
  const total = rows.reduce((a, r) => a + r.value, 0);
  const usable = Math.max(0, height - gap * Math.max(0, rows.length - 1));
  let y = top;
  const out: Band[] = [];
  for (const r of rows) {
    const h = total > 0 ? (r.value / total) * usable : usable / rows.length;
    out.push({ label: r.label, value: r.value, y0: y, y1: y + h });
    y += h + gap;
  }
  return out;
}

/** A single ribbon from a left band to a right band, tapered by height (SVG cubic bezier, horizontal control offset). */
function Ribbon({ from, to, color }: { from: Band; to: Band; color: string }) {
  const x0 = COL_LEFT + NODE_W;
  const x1 = COL_RIGHT;
  const mid = (x0 + x1) / 2;
  const d = [
    `M ${x0} ${from.y0}`,
    `C ${mid} ${from.y0}, ${mid} ${to.y0}, ${x1} ${to.y0}`,
    `L ${x1} ${to.y1}`,
    `C ${mid} ${to.y1}, ${mid} ${from.y1}, ${x0} ${from.y1}`,
    'Z',
  ].join(' ');
  return <path d={d} fill={color} opacity={0.55} />;
}

function Node({ x, band, fill }: { x: number; band: Band; fill: string }) {
  const h = Math.max(1, band.y1 - band.y0);
  return (
    <g>
      <rect x={x} y={band.y0} width={NODE_W} height={h} fill={fill} rx={2} />
    </g>
  );
}

function NodeLabel({ x, band, align, text }: { x: number; band: Band; align: 'start' | 'end'; text: string }) {
  const midY = (band.y0 + band.y1) / 2;
  return (
    <text x={x} y={midY} dy="0.32em" textAnchor={align} fontSize="10" fill="var(--text, #cfd8e3)">
      {text}
    </text>
  );
}

const MODE_COLOR: Record<string, string> = {
  Road: 'var(--accent, #4c9aff)',
  'Low-speed rail': 'var(--done, #3fb950)',
  'HS rail': 'var(--warn, #e3b341)',
  Sea: '#4a9dae',
  Plane: '#5eb3d6',
};

export function ArrivalsByModeSankey({ history }: { history: MonthlyArrivalsByMode[] | undefined }) {
  const [windowSel, setWindowSel] = useState<SankeyWindow>('month');
  const model = arrivalsByModeSankeyModel(history, windowSel);

  if (model.empty) {
    return (
      <>
        <div className="row-actions">
          <button
            className={`btn tiny${windowSel === 'month' ? ' toggle on' : ''}`}
            onClick={() => setWindowSel('month')}
          >
            Last month
          </button>
          <button
            className={`btn tiny${windowSel === 'year' ? ' toggle on' : ''}`}
            onClick={() => setWindowSel('year')}
          >
            Last 12 months
          </button>
        </div>
        <p className="hint">
          No arrivals-by-mode history recorded yet — the Sankey fills in once a full in-game month
          has closed (this is a fresh city or a save from before FEAT-1972079926).
        </p>
      </>
    );
  }

  const leftRows = [
    { label: 'Road', value: model.road },
    { label: 'Low-speed rail', value: model.railLow },
    { label: 'HS rail', value: model.railHs },
    { label: 'Sea', value: model.sea },
    { label: 'Plane', value: model.plane },
  ].filter((r) => r.value > 0);

  const leftBands = stack(leftRows.length > 0 ? leftRows : [{ label: 'Road', value: 0 }], CHART_TOP, CHART_H, GAP);
  const inSlices = stack(leftRows.length > 0 ? leftRows : [{ label: 'Road', value: 1 }], CHART_TOP, CHART_H, 0);

  // Single right-hand sink: every mode feeds the same "Move-ins" node, sized
  // to the full chart height (the shared conduit, not scaled to any one flow).
  const rightBand: Band = { label: 'Move-ins', value: model.totalIn, y0: CHART_TOP, y1: CHART_TOP + CHART_H };

  return (
    <>
      <div className="row-actions">
        <button
          className={`btn tiny${windowSel === 'month' ? ' toggle on' : ''}`}
          onClick={() => setWindowSel('month')}
        >
          Last month
        </button>
        <button
          className={`btn tiny${windowSel === 'year' ? ' toggle on' : ''}`}
          onClick={() => setWindowSel('year')}
        >
          Last 12 months
        </button>
        <span className="hint">
          {model.monthsCovered} month{model.monthsCovered === 1 ? '' : 's'} of real recorded flow
        </span>
      </div>
      <svg viewBox={`0 0 ${COL_RIGHT + NODE_W + 90} ${CHART_TOP * 2 + CHART_H}`} width="100%" role="img" aria-label="Arrivals by mode Sankey">
        {leftBands.map((b, i) => (
          <Ribbon key={b.label} from={b} to={inSlices[i]} color={MODE_COLOR[b.label] ?? 'var(--accent)'} />
        ))}
        {leftBands.map((b) => (
          <g key={`node-${b.label}`}>
            <Node x={COL_LEFT} band={b} fill={MODE_COLOR[b.label] ?? 'var(--accent)'} />
            <NodeLabel x={COL_LEFT - 6} band={b} align="end" text={`${b.label} ${fmtNum(b.value)}`} />
          </g>
        ))}
        <Node x={COL_RIGHT} band={rightBand} fill="var(--accent-soft, #12233d)" />
        <text
          x={COL_RIGHT + NODE_W / 2}
          y={CHART_TOP + CHART_H + 14}
          textAnchor="middle"
          fontSize="10"
          fill="var(--text, #cfd8e3)"
        >
          Move-ins
        </text>
      </svg>
      <p className="hint">
        Move-ins by mode: road {fmtNum(model.road)} · low-speed rail {fmtNum(model.railLow)} · HS rail{' '}
        {fmtNum(model.railHs)} · sea {fmtNum(model.sea)} · plane {fmtNum(model.plane)} · total{' '}
        {fmtNum(model.totalIn)}
      </p>
    </>
  );
}
