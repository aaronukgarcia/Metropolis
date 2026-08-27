import type { SimState } from '../../sim/types';
import { buildFiscalSankey } from '../../sim/sankey';
import { fmtMoney } from '../../sim/utils';

interface Props {
  state: SimState;
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

export function Sankey({ state, width = 320, height = 260 }: Props) {
  // Derive the fiscal graph from SimState via the conservation-checked seam.
  const graph = buildFiscalSankey(state);

  const pad = 16;
  const gap = 7;
  const srcX = pad;
  const dstX = width - pad - 78;
  const midX = (width - 20) / 2;
  const midW = 20;

  // Total to size the scale; use max(inflows, outflows) to avoid vertical clipping.
  const ti = graph.conservation.totalIn;
  const to = graph.conservation.totalOut;
  const total = Math.max(ti, to, 1);

  // Position stack: inflow sources on the left, outflow sinks on the right.
  const inflows = graph.nodes.filter((n) => n.type === 'source');
  const outflows = graph.nodes.filter((n) => n.type === 'sink');
  const reserve = graph.nodes.find((n) => n.type === 'reserve');

  const scaleIn = (height - pad * 2 - Math.max(0, inflows.length - 1) * gap) / total;
  const scaleOut = (height - pad * 2 - Math.max(0, outflows.length - 1) * gap) / total;
  const scale = Math.min(scaleIn, scaleOut);

  // Helper: stack nodes vertically, returning y and h for each.
  const stack = (nodes: typeof inflows, s: number): { y: number; h: number }[] => {
    const totalH = nodes.reduce((a, b) => a + Math.max(b.value * s, 2), 0) + (nodes.length - 1) * gap;
    let y = (height - totalH) / 2;
    return nodes.map((n) => {
      const h = Math.max(n.value * s, 2);
      const res = { y, h };
      y += h + gap;
      return res;
    });
  };

  const inPos = stack(inflows, scale);
  const outPos = stack(outflows, scale);

  // Reserve/deficit node (if net change exists).
  let reserveY = 0;
  let reserveH = 0;
  if (reserve) {
    const reserveH_ = Math.max(reserve.value * scale, 2);
    reserveY = (height - reserveH_) / 2;
    reserveH = reserveH_;
  }

  // Treasury node height: center-scaled total, minimum 6px.
  const midH = Math.max(total * scale, 6);
  const midY = (height - midH) / 2;

  return (
    <>
      <svg className="sankey" viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Money flow sankey">
        {/* Inflow source nodes and ribbons to Treasury. */}
        {inflows.map((node, i) => (
          <g key={node.id}>
            <path
              d={ribbon(
                srcX + 76,
                inPos[i].y,
                inPos[i].h,
                midX,
                midY + (midH * offsetBefore(inflows, ti, i)) / Math.max(ti, 1),
                Math.max(node.value * scale, 2)
              )}
              fill="var(--done)"
              opacity="0.32"
            />
            <rect x={srcX + 74} y={inPos[i].y} width={4} height={inPos[i].h} rx={2} fill="var(--done)" />
            <text x={srcX + 70} y={inPos[i].y + inPos[i].h / 2 + 3.5} textAnchor="end" className="sk-label">
              {node.label}
            </text>
            <text x={srcX + 70} y={inPos[i].y + inPos[i].h / 2 + 14.5} textAnchor="end" className="sk-value in">
              {fmtMoney(node.value)}
            </text>
          </g>
        ))}

        {/* Outflow sink nodes and ribbons from Treasury. */}
        {outflows.map((node, i) => (
          <g key={node.id}>
            <path
              d={ribbon(
                midX + midW,
                midY + (midH * offsetBefore(outflows, to, i)) / Math.max(to, 1),
                Math.max(node.value * scale, 2),
                dstX - 4,
                outPos[i].y,
                outPos[i].h
              )}
              fill="var(--danger)"
              opacity="0.3"
            />
            <rect x={dstX - 4} y={outPos[i].y} width={4} height={outPos[i].h} rx={2} fill="var(--danger)" />
            <text x={dstX + 4} y={outPos[i].y + outPos[i].h / 2 + 3.5} className="sk-label">
              {node.label}
            </text>
            <text x={dstX + 4} y={outPos[i].y + outPos[i].h / 2 + 14.5} className="sk-value out">
              {fmtMoney(node.value)}
            </text>
          </g>
        ))}

        {/* Treasury node. */}
        <rect x={midX} y={midY} width={midW} height={midH} rx={4} fill="var(--warn)" />
        <text x={midX + midW / 2} y={midY - 5} textAnchor="middle" className="sk-label">
          Treasury
        </text>

        {/* Reserve node (if deficit or surplus). */}
        {reserve && (
          <g key="reserve">
            <rect x={width - pad - 20} y={reserveY} width={4} height={reserveH} rx={2} fill={reserve.value > 0 ? 'var(--done)' : 'var(--danger)'} />
            <text x={width - pad - 25} y={reserveY + reserveH / 2 + 3.5} textAnchor="end" className="sk-label">
              {reserve.label}
            </text>
            <text x={width - pad - 25} y={reserveY + reserveH / 2 + 14.5} textAnchor="end" className="sk-value">
              {fmtMoney(reserve.value)}
            </text>
          </g>
        )}

        {/* Net figure: use the seam's conserved fund delta, not ti-to. */}
        <text x={midX + midW / 2} y={midY + midH + 13} textAnchor="middle" className="sk-value net">
          net {fmtSigned(graph.conservation.netChange)}
        </text>

        {/* Conservation warning: if conservation check failed, show a visible warning marker. */}
        {!graph.conservation.balances && (
          <g>
            <circle cx={width - 20} cy={10} r={8} fill="var(--danger)" opacity="0.7" />
            <text x={width - 20} y={15} textAnchor="middle" fill="white" fontSize="12" fontWeight="bold">
              !
            </text>
            <title>Conservation check failed: flows do not reconcile with fund change. See debug output.</title>
          </g>
        )}
      </svg>

      {/* Inline conservation warning text below diagram. */}
      {!graph.conservation.balances && (
        <p className="hint warn-text" style={{ marginTop: '8px' }}>
          ⚠ Conservation mismatch: recorded flows ({fmtMoney(graph.conservation.totalIn)} in, {fmtMoney(graph.conservation.totalOut)} out) do not match
          fund change ({fmtSigned(graph.conservation.netChange)}). Check game logs.
        </p>
      )}
    </>
  );
}

function fmtSigned(n: number): string {
  return `${n >= 0 ? '+' : '-'}${fmtMoney(Math.abs(n))}`;
}

function offsetBefore(
  nodes: ReturnType<typeof buildFiscalSankey>['nodes'],
  total: number,
  index: number
): number {
  if (total <= 0) return 0;
  let acc = 0;
  for (let i = 0; i < index; i++) acc += nodes[i].value;
  return (acc / total) * 1;
}
