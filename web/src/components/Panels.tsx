import { useEffect, useRef, useState } from "react";
import type {
  DeltaFrame,
  EventFrame,
  FinancePatch,
  ResultFrame,
  SankeyBand,
} from "../ws/messages";
import { MoneyFlowChart } from "./MoneyFlowChart";

export interface FiscalPanelProps {
  /** Latest decoded f2.finance patch; null until the first delta lands. */
  fin: FinancePatch | null;
  lastResult: ResultFrame | null;
}

/** micropounds -> whole pounds for display (1 GBP = 1e6 micropounds). */
export function formatPounds(micropounds: number): string {
  return `£${(micropounds / 1_000_000).toLocaleString("en-GB", {
    maximumFractionDigits: 2,
  })}`;
}

export function flowTotals(bands: SankeyBand[]): {
  inflow: number;
  outflow: number;
} {
  let inflow = 0;
  let outflow = 0;
  for (const b of bands) {
    if (b.source === "budget") outflow += b.amount;
    else inflow += b.amount;
  }
  return { inflow, outflow };
}

interface Totals {
  inflow: number;
  outflow: number;
}

/**
 * Month-over-month deltas between successive f2.finance patches. The view
 * publishes one month-close window per finance tick, so each delta IS a
 * month-over-month update; this hook reports how in/out moved versus the
 * previous distinct reading.
 */
function useMonthOverMonth(fin: FinancePatch | null): Totals | null {
  const [deltas, setDeltas] = useState<Totals | null>(null);
  const prevRef = useRef<Totals | null>(null);
  const totals = fin?.sankey ? flowTotals(fin.sankey.bands) : null;

  useEffect(() => {
    if (!totals) return;
    const prev = prevRef.current;
    prevRef.current = totals;
    if (
      prev &&
      (prev.inflow !== totals.inflow || prev.outflow !== totals.outflow)
    ) {
      setDeltas({
        inflow: totals.inflow - prev.inflow,
        outflow: totals.outflow - prev.outflow,
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [totals?.inflow, totals?.outflow]);

  return deltas;
}

/** Budget summary card: treasury pot + month-close in/out with MoM deltas. */
export function BudgetSummaryCard({ fin }: { fin: FinancePatch | null }) {
  const deltas = useMonthOverMonth(fin);
  const bs = fin?.balanceSheet;
  const totals = fin?.sankey ? flowTotals(fin.sankey.bands) : null;

  return (
    <div className="card budget-summary" data-testid="budget-summary">
      <h4>Budget</h4>
      {bs ? (
        <dl>
          <dt>Treasury</dt>
          <dd data-testid="treasury">
            {formatPounds(
              bs.assets.find((a) => a.label === "Treasury")?.valueMicropounds ??
                0,
            )}
          </dd>
          <dt>Reserves</dt>
          <dd>{formatPounds(bs.assets.find((a) => a.label === "Reserves")?.valueMicropounds ?? 0)}</dd>
          <dt>Net worth</dt>
          <dd>{formatPounds(bs.netWorth)}</dd>
        </dl>
      ) : (
        <p className="placeholder">awaiting first fiscal snapshot…</p>
      )}
      {totals && (
        <div data-testid="month-flows">
          <p>
            in {formatPounds(totals.inflow)}
            {deltas && (
              <span data-testid="in-delta">
                {" "}
                ({deltas.inflow >= 0 ? "+" : ""}
                {formatPounds(deltas.inflow)} MoM)
              </span>
            )}
          </p>
          <p>
            out {formatPounds(totals.outflow)}
            {deltas && (
              <span data-testid="out-delta">
                {" "}
                ({deltas.outflow >= 0 ? "+" : ""}
                {formatPounds(deltas.outflow)} MoM)
              </span>
            )}
          </p>
        </div>
      )}
    </div>
  );
}

/**
 * Six tax instrument rates list. Fed by the wire schema's taxSliders
 * sub-view (read-only display here); compose cannot publish it yet because
 * feat.compositionroot has no registered engine.tax edge — the panel says
 * so explicitly instead of showing fabricated figures.
 */
export function TaxRatesList({ fin }: { fin: FinancePatch | null }) {
  const sliders = fin?.taxSliders ?? [];
  return (
    <div className="card tax-rates" data-testid="tax-rates">
      <h4>Tax instruments</h4>
      {sliders.length === 0 ? (
        <p className="placeholder" data-testid="tax-rates-unavailable">
          live rates pending composition-root → engine.tax wiring
        </p>
      ) : (
        <ul>
          {sliders.map((t) => (
            <li key={t.id} data-testid={`tax-rate-${t.id}`}>
              {t.label}: {t.value}%
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Loans summary: outstanding facilities from the f2.finance loans view. */
export function LoansSummary({ fin }: { fin: FinancePatch | null }) {
  const loans = fin?.loans ?? [];
  return (
    <div className="card loans-summary" data-testid="loans-summary">
      <h4>Loans</h4>
      {loans.length === 0 ? (
        <p className="placeholder">no active loans</p>
      ) : (
        <ul>
          {loans.map((l) => (
            <li key={l.id}>
              {l.id}: {formatPounds(l.principalMicropounds)} @{" "}
              {l.ratePercent.toFixed(2)}% · next{" "}
              {formatPounds(l.nextPaymentMicropounds)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Left tab: F2's fiscal panels, fed live by the f2.finance view. */
export function FiscalPanel({ fin, lastResult }: FiscalPanelProps) {
  return (
    <section className="panel panel-fiscal" aria-label="fiscal">
      <h3>Fiscal</h3>
      <BudgetSummaryCard fin={fin} />
      <TaxRatesList fin={fin} />
      <LoansSummary fin={fin} />
      {fin?.sankey && fin.sankey.bands.length > 0 && (
        <div className="card money-flow" data-testid="money-flow">
          <h4>Money flow</h4>
          <MoneyFlowChart bands={fin.sankey.bands} />
        </div>
      )}
      {lastResult && (
        <p data-testid="last-result">
          {lastResult.accepted ? "accepted" : `rejected: ${lastResult.error?.code ?? "?"}`}
        </p>
      )}
    </section>
  );
}

export interface InfoPanelProps {
  deltasSeen: number;
  lastDelta: DeltaFrame | null;
  events: EventFrame[];
}

/** Right tab: entity/inspect placeholder fed by the live delta counters. */
export function InfoPanel({ deltasSeen, lastDelta, events }: InfoPanelProps) {
  return (
    <section className="panel panel-info" aria-label="info">
      <h3>Info</h3>
      <p>deltas received: {deltasSeen}</p>
      {lastDelta && (
        <p>
          subscription {String(lastDelta.subscriptionId)} @ tick{" "}
          {Number(lastDelta.tick)}
        </p>
      )}
      {events.length > 0 && (
        <ul>
          {events.slice(-5).map((ev, i) => (
            <li key={i}>
              [{ev.severity}] {ev.kind}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export interface BuildMovePanelProps {
  onZone: () => void;
}

/** Bottom bar: build/move command placeholder — buttons issue real Zone commands. */
export function BuildMovePanel({ onZone }: BuildMovePanelProps) {
  return (
    <footer className="panel panel-buildmove" aria-label="build-move">
      <h3>Build / Move</h3>
      <button onClick={onZone} disabled={onZone === undefined ? true : false}>
        zone (0,0) dwelling
      </button>
      <span className="placeholder">build queue placeholder</span>
    </footer>
  );
}
