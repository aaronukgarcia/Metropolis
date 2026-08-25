import type { FlowItem } from '../../sim/types';
import { fmtMoney } from '../../sim/utils';

interface Props {
  inflows: FlowItem[];
  outflows: FlowItem[];
  width?: number;
  height?: number;
}

function ribbon(
  x1: number,
  y1: number,
  h1: number,
  x2: number,
  y2: number,
  h2: number
): string {
  const cx = (x1 + x2) / 2;
  return [
    `M${x1},${y1}`,
    `C${cx},${y1} ${cx},${y2} ${x2},${y2}`,
    `L${x2},${y2 + h2}`,
    `C${cx},${y2 + h2} ${cx},${y1 + h1} ${x1},${y1 + h1}`,
    'Z',
  ].join(' ');
}

export function Sankey({ inflows, outflows, width = 320, height = 260 }: Props) {
  const pad = 16;
  const gap = 7;
  const srcX = pad;
  const dstX = width - pad - 78;
  const midX = (width - 20) / 2;
  const midW = 20;

  const ti = inflows.reduce((a, b) => a + b.value, 0);
  const to = outflows.reduce((a, b) => a + b.value, 0);
  const total = Math.max(ti, to, 1);

  const scaleIn = (height - pad * 2 - Math.max(0, inflows.length - 1) * gap) / total;
  const scaleOut = (height - pad * 2 - Math.max(0, outflows.length - 1) * gap) / total;
  const scale = Math.min(scaleIn, scaleOut);

  const stack = (items: FlowItem[], s: number): { y: number; h: number }[] => {
    const totalH = items.reduce((a, b) => a + Math.max(b.value * s, 2), 0) + (items.length - 1) * gap;
    let y = (height - totalH) / 2;
    return items.map((it) => {
      const h = Math.max(it.value * s, 2);
      const res = { y, h };
      y += h + gap;
      return res;
    });
  };

  const inPos = stack(inflows, scale);
  const outPos = stack(outflows, scale);
  const midH = Math.max(total * scale, 6);
  const midY = (height - midH) / 2;

  return (
    <svg className="sankey" viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Money flow sankey">
      {inflows.map((f, i) => (
        <g key={f.label}>
          <path d={ribbon(srcX + 76, inPos[i].y, inPos[i].h, midX, midY + (midH * offsetBefore(inflows, ti, i)) / Math.max(ti, 1), Math.max(f.value * scale, 2))} fill="var(--done)" opacity="0.32" />
          <rect x={srcX + 74} y={inPos[i].y} width={4} height={inPos[i].h} rx={2} fill="var(--done)" />
          <text x={srcX + 70} y={inPos[i].y + inPos[i].h / 2 + 3.5} textAnchor="end" className="sk-label">
            {f.label}
          </text>
          <text x={srcX + 70} y={inPos[i].y + inPos[i].h / 2 + 14.5} textAnchor="end" className="sk-value in">
            {fmtMoney(f.value)}
          </text>
        </g>
      ))}
      {outflows.map((f, i) => (
        <g key={f.label}>
          <path
            d={ribbon(midX + midW, midY + (midH * offsetBefore(outflows, to, i)) / Math.max(to, 1), Math.max(f.value * scale, 2), dstX - 4, outPos[i].y, outPos[i].h)}
            fill="var(--danger)"
            opacity="0.3"
          />
          <rect x={dstX - 4} y={outPos[i].y} width={4} height={outPos[i].h} rx={2} fill="var(--danger)" />
          <text x={dstX + 4} y={outPos[i].y + outPos[i].h / 2 + 3.5} className="sk-label">
            {f.label}
          </text>
          <text x={dstX + 4} y={outPos[i].y + outPos[i].h / 2 + 14.5} className="sk-value out">
            {fmtMoney(f.value)}
          </text>
        </g>
      ))}
      <rect x={midX} y={midY} width={midW} height={midH} rx={4} fill="var(--warn)" />
      <text x={midX + midW / 2} y={midY - 5} textAnchor="middle" className="sk-label">
        Treasury
      </text>
      <text x={midX + midW / 2} y={midY + midH + 13} textAnchor="middle" className="sk-value net">
        net {fmtSigned(ti - to)}
      </text>
    </svg>
  );
}

function fmtSigned(n: number): string {
  return `${n >= 0 ? '+' : '-'}${fmtMoney(Math.abs(n))}`;
}

function offsetBefore(items: FlowItem[], total: number, index: number): number {
  if (total <= 0) return 0;
  let acc = 0;
  for (let i = 0; i < index; i++) acc += items[i].value;
  return (acc / total) * 1;
}
