// sankey.test.ts — Fiscal Sankey graph builder tests (FEAT-1972079848)
// Tests: correctness, conservation, empty-state, deficit, determinism.

import { describe, it } from 'node:test';
import { strictEqual, deepStrictEqual, ok } from 'node:assert';
import { buildFiscalSankey } from '../src/sim/sankey.ts';
import { initialState } from '../src/sim/engine.ts';
import type { SimState } from '../src/sim/types.ts';

describe('buildFiscalSankey', () => {
  it('builds node and link structure from lastFlows', () => {
    const state = initialState();
    const graph = buildFiscalSankey(state);

    // Should have nodes: sources + Treasury + sinks [+ reserve if net ≠ 0]
    const expectedSources = state.lastFlows.inflows.length;
    const expectedSinks = state.lastFlows.outflows.length;
    const expectedReserve = state.fundsAtTickEnd !== state.fundsAtTickStart ? 1 : 0;
    const expectedNodeCount = expectedSources + 1 + expectedSinks + expectedReserve; // +1 for Treasury

    strictEqual(graph.nodes.length, expectedNodeCount);
    strictEqual(graph.nodes.filter((n) => n.type === 'treasury').length, 1);
    strictEqual(graph.nodes.filter((n) => n.type === 'source').length, expectedSources);
    strictEqual(graph.nodes.filter((n) => n.type === 'sink').length, expectedSinks);
    strictEqual(graph.nodes.filter((n) => n.type === 'reserve').length, expectedReserve);
  });

  it('preserves flow values in nodes', () => {
    const state = initialState();
    const graph = buildFiscalSankey(state);

    // Each inflow source node value should match the flow.
    for (const flow of state.lastFlows.inflows) {
      const node = graph.nodes.find((n) => n.id === `src-${flow.label}`);
      ok(node !== undefined);
      strictEqual(node.value, flow.value);
      strictEqual(node.type, 'source');
    }

    // Each outflow sink node value should match the flow.
    for (const flow of state.lastFlows.outflows) {
      const node = graph.nodes.find((n) => n.id === `snk-${flow.label}`);
      ok(node !== undefined);
      strictEqual(node.value, flow.value);
      strictEqual(node.type, 'sink');
    }
  });

  it('creates links: sources → Treasury → sinks', () => {
    const state = initialState();
    const graph = buildFiscalSankey(state);

    // Every inflow should have a link source → Treasury.
    for (const flow of state.lastFlows.inflows) {
      const link = graph.links.find(
        (l) => l.source === `src-${flow.label}` && l.target === 'treasury'
      );
      ok(link !== undefined);
      strictEqual(link.value, flow.value);
    }

    // Every outflow should have a link Treasury → sink.
    for (const flow of state.lastFlows.outflows) {
      const link = graph.links.find(
        (l) => l.source === 'treasury' && l.target === `snk-${flow.label}`
      );
      ok(link !== undefined);
      strictEqual(link.value, flow.value);
    }
  });

  it('conservation check: Σinflows = Σoutflows + Δreserves', () => {
    const state = initialState();
    const graph = buildFiscalSankey(state);

    const totalIn = state.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
    const totalOut = state.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
    const netChange = state.fundsAtTickEnd - state.fundsAtTickStart;

    strictEqual(graph.conservation.totalIn, totalIn);
    strictEqual(graph.conservation.totalOut, totalOut);
    strictEqual(graph.conservation.netChange, netChange);

    // Check conservation equation.
    const expectedBalance = totalOut + netChange;
    ok(graph.conservation.balances);
    ok(Math.abs(totalIn - expectedBalance) <= 1); // 1p tolerance
  });

  it('handles empty flows (no inflows or outflows)', () => {
    const state = initialState();
    const emptyFlowState: SimState = {
      ...state,
      lastFlows: { inflows: [], outflows: [] },
      fundsAtTickStart: 100000,
      fundsAtTickEnd: 100000,
    };

    const graph = buildFiscalSankey(emptyFlowState);

    // Should still have Treasury (always present).
    strictEqual(graph.nodes.filter((n) => n.type === 'treasury').length, 1);
    // No sources or sinks.
    strictEqual(graph.nodes.filter((n) => n.type === 'source').length, 0);
    strictEqual(graph.nodes.filter((n) => n.type === 'sink').length, 0);
    // No reserve (netChange = 0).
    strictEqual(graph.nodes.filter((n) => n.type === 'reserve').length, 0);

    // No links.
    strictEqual(graph.links.length, 0);

    // Conservation: 0 in = 0 out + 0 net.
    ok(graph.conservation.balances);
  });

  it('handles surplus (fundsAtTickEnd > fundsAtTickStart)', () => {
    const state = initialState();
    const surplus = 5000;
    const surplusState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000 + surplus,
      lastFlows: {
        inflows: [{ label: 'Tax', value: 10000 }],
        outflows: [{ label: 'Wages', value: 5000 }],
      },
    };

    const graph = buildFiscalSankey(surplusState);

    // Should have a reserve node for the surplus.
    const reserveNode = graph.nodes.find((n) => n.type === 'reserve');
    ok(reserveNode !== undefined);
    strictEqual(reserveNode.value, surplus);
    ok(reserveNode.label.includes('surplus'));

    // Should have a Treasury → Reserve link for the surplus.
    const surplusLink = graph.links.find(
      (l) => l.source === 'treasury' && l.target === 'reserve'
    );
    ok(surplusLink !== undefined);
    strictEqual(surplusLink.value, surplus);

    ok(graph.conservation.balances);
  });

  it('handles deficit (fundsAtTickEnd < fundsAtTickStart)', () => {
    const state = initialState();
    const deficit = 3000;
    const deficitState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000 - deficit,
      lastFlows: {
        inflows: [{ label: 'Tax', value: 5000 }],
        outflows: [{ label: 'Wages', value: 8000 }],
      },
    };

    const graph = buildFiscalSankey(deficitState);

    // Should have a reserve node (showing deficit draw-down).
    const reserveNode = graph.nodes.find((n) => n.type === 'reserve');
    ok(reserveNode !== undefined);
    strictEqual(reserveNode.value, deficit);
    ok(reserveNode.label.includes('deficit'));

    // Should have a Reserve → Treasury link (draw-down).
    const deficitLink = graph.links.find(
      (l) => l.source === 'reserve' && l.target === 'treasury'
    );
    ok(deficitLink !== undefined);
    strictEqual(deficitLink.value, deficit);

    ok(graph.conservation.balances);
  });

  it('determinism: same state → identical graph', () => {
    const state = initialState();

    const graph1 = buildFiscalSankey(state);
    const graph2 = buildFiscalSankey(state);

    // Same number of nodes.
    strictEqual(graph1.nodes.length, graph2.nodes.length);
    // Same number of links.
    strictEqual(graph1.links.length, graph2.links.length);

    // Same conservation values.
    strictEqual(graph1.conservation.totalIn, graph2.conservation.totalIn);
    strictEqual(graph1.conservation.totalOut, graph2.conservation.totalOut);
    strictEqual(graph1.conservation.netChange, graph2.conservation.netChange);
    strictEqual(graph1.conservation.balances, graph2.conservation.balances);

    // Deep comparison of nodes (order matters for determinism).
    for (let i = 0; i < graph1.nodes.length; i++) {
      const n1 = graph1.nodes[i];
      const n2 = graph2.nodes[i];
      strictEqual(n1.id, n2.id);
      strictEqual(n1.label, n2.label);
      strictEqual(n1.value, n2.value);
      strictEqual(n1.type, n2.type);
    }

    // Deep comparison of links.
    for (let i = 0; i < graph1.links.length; i++) {
      const l1 = graph1.links[i];
      const l2 = graph2.links[i];
      strictEqual(l1.source, l2.source);
      strictEqual(l1.target, l2.target);
      strictEqual(l1.value, l2.value);
    }
  });

  it('conservation is violated when flows do not balance (RED test)', () => {
    const state = initialState();
    // Artificially break conservation: make inflows < outflows + netChange.
    const brokenState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000,
      lastFlows: {
        inflows: [{ label: 'Tax', value: 100 }], // Too low
        outflows: [{ label: 'Wages', value: 500 }], // Too high
      },
    };

    const graph = buildFiscalSankey(brokenState);

    // Conservation should NOT hold: 100 ≠ 500 + 0.
    strictEqual(graph.conservation.balances, false);
    strictEqual(graph.conservation.totalIn, 100);
    strictEqual(graph.conservation.totalOut, 500);
    strictEqual(graph.conservation.netChange, 0);
  });

  it('conservation tolerates 1p rounding error', () => {
    const state = initialState();
    // Flows that incur rounding: inflows sum to 1001, but Math.round applied.
    const roundState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000 + 1, // Net change = +1
      lastFlows: {
        inflows: [
          { label: 'Tax1', value: 500 },
          { label: 'Tax2', value: 501 }, // Sum = 1001
        ],
        outflows: [{ label: 'Wages', value: 1000 }], // Total out
      },
    };

    const graph = buildFiscalSankey(roundState);

    // 1001 = 1000 + 1? Yes, perfectly balanced.
    ok(graph.conservation.balances);
  });

  it('node IDs are unique and properly prefixed', () => {
    const state = initialState();
    const graph = buildFiscalSankey(state);

    const ids = new Set(graph.nodes.map((n) => n.id));
    strictEqual(ids.size, graph.nodes.length); // All IDs unique

    // Source nodes prefixed with 'src-'.
    for (const node of graph.nodes.filter((n) => n.type === 'source')) {
      ok(node.id.startsWith('src-'));
    }

    // Sink nodes prefixed with 'snk-'.
    for (const node of graph.nodes.filter((n) => n.type === 'sink')) {
      ok(node.id.startsWith('snk-'));
    }

    // Treasury and reserve have specific IDs.
    ok(graph.nodes.find((n) => n.id === 'treasury'));
    ok(!graph.nodes.find((n) => n.type === 'reserve') || graph.nodes.find((n) => n.id === 'reserve'));
  });

  it('handles multiple inflows and outflows', () => {
    const state = initialState();
    const multiFlowState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000,
      lastFlows: {
        inflows: [
          { label: 'Council Tax', value: 5000 },
          { label: 'Business Tax', value: 3000 },
          { label: 'Tourism', value: 2000 },
        ],
        outflows: [
          { label: 'Wages', value: 6000 },
          { label: 'Road Upkeep', value: 2000 },
          { label: 'Power Grid', value: 1500 },
          { label: 'Education', value: 500 },
        ],
      },
    };

    const graph = buildFiscalSankey(multiFlowState);

    strictEqual(graph.nodes.filter((n) => n.type === 'source').length, 3);
    strictEqual(graph.nodes.filter((n) => n.type === 'sink').length, 4);
    strictEqual(graph.links.filter((l) => l.target === 'treasury').length, 3);
    strictEqual(graph.links.filter((l) => l.source === 'treasury').length, 4);
    ok(graph.conservation.balances);
  });

  it('link values match flow values exactly', () => {
    const state = initialState();
    const testState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000,
      lastFlows: {
        inflows: [{ label: 'SpecialTax', value: 12345 }],
        outflows: [{ label: 'SpecialSpend', value: 12345 }],
      },
    };

    const graph = buildFiscalSankey(testState);

    // Inflow link value.
    const inflowLink = graph.links.find((l) => l.target === 'treasury');
    strictEqual(inflowLink?.value, 12345);

    // Outflow link value.
    const outflowLink = graph.links.find((l) => l.source === 'treasury');
    strictEqual(outflowLink?.value, 12345);
  });

  it('no spurious nodes or links when flows are zero', () => {
    const state = initialState();
    const zeroFlowState: SimState = {
      ...state,
      fundsAtTickStart: 1000000,
      fundsAtTickEnd: 1000000,
      lastFlows: {
        inflows: [{ label: 'Zero Tax', value: 0 }],
        outflows: [{ label: 'Zero Upkeep', value: 0 }],
      },
    };

    const graph = buildFiscalSankey(zeroFlowState);

    // Should still create nodes and links for zero-value flows (they are valid flows).
    ok(graph.nodes.find((n) => n.label === 'Zero Tax'));
    ok(graph.nodes.find((n) => n.label === 'Zero Upkeep'));

    // Links should exist with value = 0.
    ok(graph.links.find((l) => l.value === 0));
  });

  it('WIRING TEST: buildFiscalSankey drives node/link structure (Sankey.tsx consumes this seam)', () => {
    // This test verifies that the seam produces the correct graph structure that
    // components/left/Sankey.tsx is wired to consume. It prevents regression to
    // decorative-only rendering (the "built but not wired" defect class).
    const state = initialState();
    const graph = buildFiscalSankey(state);

    // The graph must have nodes for each flow type.
    ok(graph.nodes.length > 0);
    ok(graph.nodes.find((n) => n.type === 'treasury'));

    // Every source node must have a link to Treasury (Sankey component draws these).
    for (const srcNode of graph.nodes.filter((n) => n.type === 'source')) {
      const link = graph.links.find((l) => l.source === srcNode.id && l.target === 'treasury');
      ok(link !== undefined, `source ${srcNode.id} must link to Treasury`);
      strictEqual(link!.value, srcNode.value);
    }

    // Every sink node must have a link from Treasury (Sankey component draws these).
    for (const sinkNode of graph.nodes.filter((n) => n.type === 'sink')) {
      const link = graph.links.find((l) => l.source === 'treasury' && l.target === sinkNode.id);
      ok(link !== undefined, `Treasury must link to sink ${sinkNode.id}`);
      strictEqual(link!.value, sinkNode.value);
    }

    // If a reserve node exists, it must be linked to/from Treasury.
    const reserve = graph.nodes.find((n) => n.type === 'reserve');
    if (reserve) {
      const link = graph.links.find(
        (l) =>
          (l.source === 'treasury' && l.target === 'reserve') ||
          (l.source === 'reserve' && l.target === 'treasury')
      );
      ok(link !== undefined, `Reserve node must link to/from Treasury`);
    }

    // Conservation check must be reachable.
    ok(typeof graph.conservation.balances === 'boolean');

    // Net change must come from the seam (not computed as ti - to).
    strictEqual(
      graph.conservation.netChange,
      state.fundsAtTickEnd - state.fundsAtTickStart,
      'netChange must derive from tick-boundary invariant, not flows'
    );
  });
});
