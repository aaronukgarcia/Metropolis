import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  BudgetSummaryCard,
  FiscalPanel,
  flowTotals,
  formatPounds,
  LoansSummary,
  TaxRatesList,
} from "./Panels";
import type { FinancePatch } from "../ws/messages";

const MOCK_FIN: FinancePatch = {
  schemaVersion: 1,
  balanceSheet: {
    assets: [
      { label: "Treasury", valueMicropounds: 10_000_000 },
      { label: "Reserves", valueMicropounds: 250_000 },
    ],
    liabilities: [{ label: "Outstanding Debt", valueMicropounds: 0 }],
    netWorth: 10_250_000,
  },
  loans: [
    {
      id: "loan-1",
      principalMicropounds: 5_000_000,
      ratePercent: 4.25,
      termMonths: 24,
      nextPaymentMicropounds: 218_750,
    },
  ],
  sankey: {
    bands: [
      { source: "tax.income", target: "budget", amount: 2_000_000 },
      { source: "tax.sales", target: "budget", amount: 500_000 },
      { source: "budget", target: "imports", amount: 300_000 },
      { source: "budget", target: "debt.service", amount: 100_000 },
    ],
  },
};

describe("formatPounds / flowTotals", () => {
  it("renders micropounds as pounds", () => {
    expect(formatPounds(10_000_000)).toBe("£10");
  });

  it("splits ASM-1220 in/out totals around the budget node", () => {
    const t = flowTotals(MOCK_FIN.sankey!.bands);
    expect(t.inflow).toBe(2_500_000);
    expect(t.outflow).toBe(400_000);
  });
});

describe("BudgetSummaryCard", () => {
  it("renders treasury, reserves and net worth from the patch", () => {
    render(<BudgetSummaryCard fin={MOCK_FIN} />);
    expect(screen.getByTestId("treasury").textContent).toBe("£10");
    expect(screen.getByTestId("month-flows").textContent).toContain("in £2.5");
    expect(screen.getByTestId("month-flows").textContent).toContain(
      "out £0.4",
    );
  });

  it("shows the awaiting placeholder before the first delta", () => {
    render(<BudgetSummaryCard fin={null} />);
    expect(screen.getByText(/awaiting first fiscal snapshot/i)).toBeTruthy();
    expect(screen.queryByTestId("month-flows")).toBeNull();
  });

  it("surfaces month-over-month deltas when the next month-close moves the flows", async () => {
    const { rerender } = render(<BudgetSummaryCard fin={MOCK_FIN} />);
    expect(screen.queryByTestId("in-delta")).toBeNull();

    // Next monthly delta: tax inflow rises by £1, outflow unchanged.
    const nextMonth: FinancePatch = {
      ...MOCK_FIN,
      sankey: {
        bands: [
          ...MOCK_FIN.sankey!.bands.filter((b) => b.source !== "tax.sales"),
          { source: "tax.sales", target: "budget", amount: 1_500_000 },
        ],
      },
    };
    rerender(<BudgetSummaryCard fin={nextMonth} />);

    // Effects flush asynchronously; wait for the MoM markers.
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByTestId("in-delta").textContent).toMatch(/\+£1.*MoM/);
    expect(screen.getByTestId("out-delta").textContent).toMatch(/£0.*MoM/);
  });
});

describe("TaxRatesList", () => {
  it("says explicitly that live rates are pending instead of fabricating figures", () => {
    render(<TaxRatesList fin={MOCK_FIN} />);
    expect(screen.getByTestId("tax-rates-unavailable")).toBeTruthy();
  });

  it("lists all six instruments with their rates once published on the wire", () => {
    const ids = [
      "vat",
      "import-duties",
      "corporation-tax",
      "paye",
      "council-tax",
      "business-rates",
    ];
    const withRates: FinancePatch = {
      ...MOCK_FIN,
      taxSliders: ids.map((id, i) => ({
        id,
        label: id,
        value: 5 + i,
        min: 0,
        max: 100,
        step: 0.5,
        incidenceDescription: "",
      })),
    };
    render(<TaxRatesList fin={withRates} />);
    for (let i = 0; i < ids.length; i++) {
      expect(screen.getByTestId(`tax-rate-${ids[i]}`).textContent).toBe(
        `${ids[i]}: ${5 + i}%`,
      );
    }
  });
});

describe("LoansSummary", () => {
  it("renders each active loan with principal, rate and next payment", () => {
    render(<LoansSummary fin={MOCK_FIN} />);
    const item = screen.getByText(/loan-1:/).textContent ?? "";
    expect(item).toContain("£5");
    expect(item).toContain("4.25%");
    expect(item).toContain("£0.22");
  });

  it("shows no-active-loans when the list is empty or absent", () => {
    render(<LoansSummary fin={{ schemaVersion: 1 }} />);
    expect(screen.getByText(/no active loans/i)).toBeTruthy();
  });
});

describe("FiscalPanel (left tab)", () => {
  it("composes budget card, tax rates, loans and the money-flow chart", () => {
    render(
      <FiscalPanel
        fin={MOCK_FIN}
        lastResult={null}
      />,
    );
    expect(screen.getByLabelText("fiscal")).toBeTruthy();
    expect(screen.getByTestId("budget-summary")).toBeTruthy();
    expect(screen.getByTestId("tax-rates")).toBeTruthy();
    expect(screen.getByTestId("loans-summary")).toBeTruthy();
    expect(screen.getByTestId("money-flow-chart")).toBeTruthy();
  });

  it("withholds the sankey card until the view publishes bands", () => {
    render(
      <FiscalPanel
        fin={{ schemaVersion: 1, balanceSheet: MOCK_FIN.balanceSheet }}
        lastResult={null}
      />,
    );
    expect(screen.queryByTestId("money-flow")).toBeNull();
  });
});
