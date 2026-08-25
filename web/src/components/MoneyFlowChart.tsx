import { useMemo } from "react";
import {
  sankey,
  sankeyLinkHorizontal,
  type SankeyGraph,
  type SankeyNode,
  type SankeyLink,
  type SankeyExtraProperties,
} from "d3-sankey";
import type { SankeyBand } from "../ws/messages";

export interface MoneyFlowChartProps {
  bands: SankeyBand[];
  width?: number;
  height?: number;
}

interface FlowProps extends SankeyExtraProperties {
  name: string;
}

interface FlowLinkProps extends SankeyExtraProperties {
  amount: number;
}

type FlowNode = SankeyNode<FlowProps, FlowLinkProps>;
type FlowLink = SankeyLink<FlowProps, FlowLinkProps>;

const BUDGET_NODE = "budget";

/**
 * MoneyFlowChart renders the f2.finance `sankey` sub-view as a real
 * d3-sankey diagram: budget-inflow bands flow left-to-right into the
 * central "budget" node, external-outflow bands out of it (ASM-1220 —
 * internal redistribution never arrives as a band). Pure SVG output from
 * fixed dimensions, so it renders identically in vitest/jsdom and the
 * browser.
 */
export function MoneyFlowChart({
  bands,
  width = 320,
  height = 220,
}: MoneyFlowChartProps) {
  const graph = useMemo(() => {
    const names: string[] = [];
    for (const b of bands) {
      if (!names.includes(b.source)) names.push(b.source);
      if (!names.includes(b.target)) names.push(b.target);
    }
    // d3-sankey cannot lay out a graph with zero nodes; render nothing.
    if (names.length === 0) {
      return { nodes: [], links: [] };
    }
    const index = new Map(names.map((n, i) => [n, i]));
    const nodes: FlowNode[] = names.map((name) => ({ name }));    // d3-sankey requires positive link widths; a zero band still gets its
    // node but travels at value 1 micropound so the topology stays honest
    // about WHICH flows exist without inventing magnitude.
    const links: FlowLink[] = bands.map((b) => ({
      source: index.get(b.source) ?? 0,
      target: index.get(b.target) ?? 0,
      value: Math.max(b.amount, 1),
      amount: b.amount,
    }));

    const layout = sankey<FlowNode, FlowLink>().extent([
      [1, 1],
      [width - 1, height - 5],
    ]);
    return layout({
      nodes,
      links,
    } as unknown as SankeyGraph<FlowNode, FlowLink>);
  }, [bands, width, height]);

  const path = sankeyLinkHorizontal<FlowNode, FlowLink>();

  return (
    <svg
      data-testid="money-flow-chart"
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      role="img"
      aria-label="money flow diagram"
    >
      <g>
        {graph.nodes.map((n) => (
          <rect
            key={`n-${n.name}`}
            x={n.x0}
            y={n.y0}
            width={(n.x1 ?? 0) - (n.x0 ?? 0)}
            height={Math.max((n.y1 ?? 0) - (n.y0 ?? 0), 1)}
            fill={n.name === BUDGET_NODE ? "#4a7fb5" : "#8a8f98"}
          >
            <title>{n.name}</title>
          </rect>
        ))}
      </g>
      <g fill="none" strokeOpacity={0.5}>
        {graph.links.map((l, i) => (
          <path
            key={`l-${i}`}
            d={path(l) ?? undefined}
            stroke="#6b9e4e"
            strokeWidth={Math.max(l.width ?? 1, 1)}
          />
        ))}
      </g>
    </svg>
  );
}
