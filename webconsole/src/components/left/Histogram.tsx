import type { TickRecord } from '../../sim/types';
import { fmtMoney } from '../../sim/utils';

interface Props {
  history: TickRecord[];
  window?: number;
}

export function Histogram({ history, window: win = 72 }: Props) {
  const data = history.slice(-win);
  if (data.length < 2) return <div className="empty">Run the clock to collect history…</div>;

  const w = 320;
  const h = 210;
  const padL = 40;
  const padR = 8;
  const padT = 10;
  const padB = 18;
  const plotW = w - padL - padR;
  const plotH = h - padT - padB;
  const midY = padT + plotH / 2;

  const nets = data.map((d) => d.income - d.expense);
  const maxNet = Math.max(1, ...nets.map(Math.abs));
  const barW = plotW / data.length;

  const fundsMin = Math.min(...data.map((d) => d.funds));
  const fundsMax = Math.max(...data.map((d) => d.funds));
  const fundsSpan = Math.max(1, fundsMax - fundsMin);
  const fy = (f: number) => padT + plotH - ((f - fundsMin) / fundsSpan) * plotH * 0.9 - plotH * 0.05;
  const linePts = data.map((d, i) => `${padL + i * barW + barW / 2},${fy(d.funds)}`).join(' ');

  return (
    <svg className="histogram" viewBox={`0 0 ${w} ${h}`} role="img" aria-label="Net income and treasury trend">
      {[0.25, 0.75].map((f) => (
        <line key={f} x1={padL} x2={w - padR} y1={padT + plotH * f} y2={padT + plotH * f} className="grid-line" />
      ))}
      {nets.map((n, i) => {
        const bh = (Math.abs(n) / maxNet) * (plotH / 2);
        const x = padL + i * barW;
        return (
          <rect
            key={i}
            x={x}
            y={n >= 0 ? midY - bh : midY}
            width={Math.max(barW - 0.6, 0.8)}
            height={bh}
            fill={n >= 0 ? 'var(--done)' : 'var(--danger)'}
            opacity="0.55"
          >
            <title>{`Tick ${data[i].tick}: net ${fmtMoney(n)}`}</title>
          </rect>
        );
      })}
      <line x1={padL} x2={w - padR} y1={midY} y2={midY} className="zero-line" />
      <polyline points={linePts} fill="none" stroke="var(--accent)" strokeWidth="1.6" />
      <text x={4} y={midY + 3} className="axis-label">±{fmtMoney(maxNet)}</text>
      <text x={4} y={padT + 8} className="axis-label">{fmtMoney(fundsMax)}</text>
      <text x={4} y={h - padB} className="axis-label">{fmtMoney(fundsMin)}</text>
      <text x={padL} y={h - 5} className="axis-label">tick {data[0].tick}</text>
      <text x={w - padR} y={h - 5} textAnchor="end" className="axis-label">tick {data[data.length - 1].tick}</text>
    </svg>
  );
}
