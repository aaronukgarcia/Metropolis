// FEAT-1972079925 — population demographic Sankey. Inline SVG, no new
// dependencies. Renders the REAL recorded monthly demographic flows (GR#15:
// never a fabricated split) — left sources (Births, Move-ins) flow into a
// centre Population node, which flows out to right sinks (Deaths, Move-outs).
// Band widths are proportional to the actual recorded totals over the
// selected window. When there is no recorded history yet (a fresh city, or a
// legacy save predating this feature) the panel renders an honest empty
// state instead of inventing numbers.

import { useState } from 'react';
import type { MonthlyDemographics } from '../sim/types';
import { fmtNum } from '../sim/utils';
import { demographicSankeyModel } from './populationSankeyModel.ts';
import type { SankeyWindow } from './populationSankeyModel.ts';

export { demographicSankeyModel };
export type { SankeyWindow, SankeyFlows } from './populationSankeyModel.ts';

const COL_LEFT = 30;
const COL_CENTER = 185;
const COL_RIGHT = 340;
const NODE_W = 26;
const CHART_TOP = 20;
const CHART_H = 160;
const GAP = 8;

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
  const x1 = COL_CENTER;
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

function RibbonOut({ from, to, color }: { from: Band; to: Band; color: string }) {
  const x0 = COL_CENTER + NODE_W;
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

export function PopulationSankey({ history }: { history: MonthlyDemographics[] | undefined }) {
  const [windowSel, setWindowSel] = useState<SankeyWindow>('month');
  const model = demographicSankeyModel(history, windowSel);

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
          No demographic history recorded yet — the Sankey fills in once a full in-game month has
          closed (this is a fresh city or a save from before FEAT-1972079925).
        </p>
      </>
    );
  }

  const leftRows = [
    { label: 'Births', value: model.births },
    { label: 'Move-ins', value: model.moveIns },
  ].filter((r) => r.value > 0);
  const rightRows = [
    { label: 'Deaths', value: model.deaths },
    { label: 'Move-outs', value: model.moveOuts },
  ].filter((r) => r.value > 0);

  const leftBands = stack(leftRows.length > 0 ? leftRows : [{ label: 'Births', value: 0 }], CHART_TOP, CHART_H, GAP);
  const rightBands = stack(rightRows.length > 0 ? rightRows : [{ label: 'Deaths', value: 0 }], CHART_TOP, CHART_H, GAP);

  // Centre population node spans the full chart height — it is the shared
  // conduit, not scaled to any single flow.
  const centerBand: Band = { label: 'Population', value: model.totalIn, y0: CHART_TOP, y1: CHART_TOP + CHART_H };

  // Ribbons in: each left band maps to a proportional slice of the centre
  // node (by its share of totalIn). Ribbons out: each right band maps to a
  // proportional slice of the centre node (by its share of totalOut).
  const inSlices = stack(leftRows.length > 0 ? leftRows : [{ label: 'Births', value: 1 }], CHART_TOP, CHART_H, 0);
  const outSlices = stack(rightRows.length > 0 ? rightRows : [{ label: 'Deaths', value: 1 }], CHART_TOP, CHART_H, 0);

  const colorFor: Record<string, string> = {
    Births: 'var(--done, #3fb950)',
    'Move-ins': 'var(--accent, #4c9aff)',
    Deaths: 'var(--danger, #ff7b72)',
    'Move-outs': 'var(--warn, #e3b341)',
  };

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
      <svg viewBox={`0 0 ${COL_RIGHT + NODE_W + 60} ${CHART_TOP * 2 + CHART_H}`} width="100%" role="img" aria-label="Population Sankey">
        {leftBands.map((b, i) => (
          <Ribbon key={b.label} from={b} to={inSlices[i]} color={colorFor[b.label] ?? 'var(--accent)'} />
        ))}
        {rightBands.map((b, i) => (
          <RibbonOut key={b.label} from={outSlices[i]} to={b} color={colorFor[b.label] ?? 'var(--danger)'} />
        ))}
        {leftBands.map((b) => (
          <g key={`node-${b.label}`}>
            <Node x={COL_LEFT} band={b} fill={colorFor[b.label] ?? 'var(--accent)'} />
            <NodeLabel x={COL_LEFT - 6} band={b} align="end" text={`${b.label} ${fmtNum(b.value)}`} />
          </g>
        ))}
        <Node x={COL_CENTER} band={centerBand} fill="var(--accent-soft, #12233d)" />
        <NodeLabel x={COL_CENTER + NODE_W / 2} band={centerBand} align="start" text="" />
        <text
          x={COL_CENTER + NODE_W / 2}
          y={CHART_TOP + CHART_H + 14}
          textAnchor="middle"
          fontSize="10"
          fill="var(--text, #cfd8e3)"
        >
          Population
        </text>
        {rightBands.map((b) => (
          <g key={`node-${b.label}`}>
            <Node x={COL_RIGHT} band={b} fill={colorFor[b.label] ?? 'var(--danger)'} />
            <NodeLabel x={COL_RIGHT + NODE_W + 6} band={b} align="start" text={`${b.label} ${fmtNum(b.value)}`} />
          </g>
        ))}
      </svg>
      <p className="hint">
        In: {fmtNum(model.totalIn)} (births {fmtNum(model.births)} + move-ins {fmtNum(model.moveIns)}) · Out:{' '}
        {fmtNum(model.totalOut)} (deaths {fmtNum(model.deaths)} + move-outs {fmtNum(model.moveOuts)}) · net{' '}
        {fmtNum(model.totalIn - model.totalOut)}
      </p>
    </>
  );
}
