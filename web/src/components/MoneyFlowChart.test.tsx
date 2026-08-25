import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MoneyFlowChart } from "./MoneyFlowChart";
import type { SankeyBand } from "../ws/messages";

const BANDS: SankeyBand[] = [
  // ASM-1220-shaped: budget inflow (tax) and external outflows only.
  { source: "tax.income", target: "budget", amount: 4_000_000 },
  { source: "tax.sales", target: "budget", amount: 1_500_000 },
  { source: "budget", target: "imports", amount: 2_000_000 },
  { source: "budget", target: "debt.service", amount: 300_000 },
];

describe("MoneyFlowChart", () => {
  it("renders one node per distinct band endpoint and the budget hub", () => {
    render(<MoneyFlowChart bands={BANDS} />);
    const svg = screen.getByTestId("money-flow-chart");
    expect(svg).toBeTruthy();
    // 5 distinct endpoints: two tax sources + budget + imports + debt.
    expect(svg.querySelectorAll("rect")).toHaveLength(5);
    expect(svg.querySelectorAll("path")).toHaveLength(BANDS.length);
  });

  it("renders an honest empty diagram for zero bands rather than crashing", () => {
    render(<MoneyFlowChart bands={[]} />);
    const svg = screen.getByTestId("money-flow-chart");
    expect(svg.querySelectorAll("rect")).toHaveLength(0);
    expect(svg.querySelectorAll("path")).toHaveLength(0);
  });

  it("keeps zero-amount flows visible as topology without inventing magnitude", () => {
    render(
      <MoneyFlowChart
        bands={[{ source: "tax.corp", target: "budget", amount: 0 }]}
      />,
    );
    const svg = screen.getByTestId("money-flow-chart");
    expect(svg.querySelectorAll("rect")).toHaveLength(2);
    expect(svg.querySelectorAll("path")).toHaveLength(1);
  });
});
