// sankey.ts — Fiscal flow Sankey graph builder (FEAT-1972079848)
//
// Constructs a money-flow graph from SimState.lastFlows, deriving source → Treasury → sink
// structure and verifying conservation: Σinflows = Σoutflows + Δreserves.
// Pure derivation; deterministic from state alone (no Date.now, no Math.random).

import type { SimState } from './types';

/**
 * A node in the fiscal Sankey graph.
 * id: unique string identifier (used in links)
 * label: human-readable name
 * value: amount (in pounds) — sum of all flows in/out of this node
 * type: semantic node class
 *   - 'source': inflow stream (tax, tourism, etc.)
 *   - 'sink': outflow stream (wages, upkeep, etc.)
 *   - 'treasury': the central account
 *   - 'reserve': synthetic node showing net surplus/deficit
 */
export interface SankeyNode {
  id: string;
  label: string;
  value: number;
  type: 'source' | 'sink' | 'treasury' | 'reserve';
}

/**
 * A directed edge in the Sankey graph.
 * source: node id → target: node id
 * value: amount flowing (in pounds)
 */
export interface SankeyLink {
  source: string;
  target: string;
  value: number;
}

/**
 * Conservation audit: verifies money-flow balance.
 * totalIn: sum of all inflows (should equal outflows + Δreserves)
 * totalOut: sum of all outflows
 * netChange: change in reserves (fundsAtTickEnd - fundsAtTickStart)
 * balances: true iff totalIn = totalOut + netChange (within 1p rounding tolerance)
 */
export interface ConservationCheck {
  totalIn: number;
  totalOut: number;
  netChange: number;
  balances: boolean;
}

/**
 * Fiscal Sankey graph derived from SimState.lastFlows.
 * nodes: all sources, Treasury, sinks, and reserves (if net != 0)
 * links: edges from sources → Treasury, Treasury → sinks, Treasury ↔ Reserves
 * conservation: audit of money-flow balance
 */
export interface FiscalSankey {
  nodes: SankeyNode[];
  links: SankeyLink[];
  conservation: ConservationCheck;
}

/**
 * Build a fiscal Sankey graph from the current state.
 *
 * Returns nodes and links for a flow diagram:
 * - Inflow sources (Council Tax, Business Tax, etc.) connect to Treasury
 * - Treasury connects to outflow sinks (Wages, Upkeep, etc.)
 * - If funds changed (surplus or deficit), Treasury also connects to/from a Reserve node
 *
 * Conservation is checked: Σinflows = Σoutflows + Δreserves, accounting for rounding.
 * The conservative tolerance is 1 pence to allow for round-trip Math.round errors.
 *
 * Pure & deterministic: same state → identical graph, no random or time-based values.
 */
export function buildFiscalSankey(s: SimState): FiscalSankey {
  const nodes: SankeyNode[] = [];
  const links: SankeyLink[] = [];

  const totalIn = s.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
  const totalOut = s.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
  const netChange = s.fundsAtTickEnd - s.fundsAtTickStart;

  // Conservation check: accounting for 1p rounding tolerance.
  const expectedBalance = totalOut + netChange;
  const conservationError = Math.abs(totalIn - expectedBalance);
  const balances = conservationError <= 1;

  // Inflow source nodes.
  for (const flow of s.lastFlows.inflows) {
    nodes.push({
      id: `src-${flow.label}`,
      label: flow.label,
      value: flow.value,
      type: 'source',
    });
  }

  // Treasury node (always present).
  nodes.push({
    id: 'treasury',
    label: 'Treasury',
    value: Math.max(totalIn, totalOut), // Use the larger to size the node visibly
    type: 'treasury',
  });

  // Outflow sink nodes.
  for (const flow of s.lastFlows.outflows) {
    nodes.push({
      id: `snk-${flow.label}`,
      label: flow.label,
      value: flow.value,
      type: 'sink',
    });
  }

  // Reserve node (if there's a net change in funds — surplus or deficit).
  if (netChange !== 0) {
    nodes.push({
      id: 'reserve',
      label: netChange > 0 ? 'Reserves (surplus)' : 'Reserves (deficit)',
      value: Math.abs(netChange),
      type: 'reserve',
    });
  }

  // Links: sources → Treasury.
  for (const flow of s.lastFlows.inflows) {
    links.push({
      source: `src-${flow.label}`,
      target: 'treasury',
      value: flow.value,
    });
  }

  // Links: Treasury → sinks.
  for (const flow of s.lastFlows.outflows) {
    links.push({
      source: 'treasury',
      target: `snk-${flow.label}`,
      value: flow.value,
    });
  }

  // Links: Treasury ↔ Reserve (if any).
  if (netChange !== 0) {
    if (netChange > 0) {
      // Surplus: Treasury → Reserves
      links.push({
        source: 'treasury',
        target: 'reserve',
        value: netChange,
      });
    } else {
      // Deficit: Reserves → Treasury (draw-down)
      links.push({
        source: 'reserve',
        target: 'treasury',
        value: Math.abs(netChange),
      });
    }
  }

  return {
    nodes,
    links,
    conservation: {
      totalIn,
      totalOut,
      netChange,
      balances,
    },
  };
}
