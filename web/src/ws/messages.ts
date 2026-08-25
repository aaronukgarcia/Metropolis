// Wire types mirroring int.transport's server->client frames
// (internal/transport/server.go) and compose's f1.viewport patch schema
// (internal/engine/compose/viewport_publish.go). Kept as an independent,
// hand-mirrored copy per the same GR#20 seam discipline the Go side
// applies to its own wire structs: the browser never imports engine or
// protocol types, only these.

export interface CellRef {
  x: number;
  y: number;
}

export type CommandPayload =
  | { kind: "Subscribe"; viewName: string; params?: Record<string, string> }
  | { kind: "Unsubscribe"; subscriptionId: string }
  | { kind: "AdvanceTicks"; n: number }
  | { kind: "Buy"; cell: CellRef }
  | { kind: "Zone"; cell: CellRef; zoneType: string }
  | { kind: "Build"; cell: CellRef; buildingType: string }
  | { kind: "Demolish"; cell: CellRef };

export interface CommandEnvelope {
  protocolVersion: string;
  correlationId: string;
  issuedAtTick: number;
  kind: string;
  payload: unknown;
}

export interface ErrorRef {
  code: string;
  display: string;
}

export interface ResultFrame {
  correlationId: string;
  tick: number;
  accepted: boolean;
  error?: ErrorRef;
}

export interface DeltaFrame {
  subscriptionId: string;
  tick: number;
  seq: number;
  patch: unknown;
  correlationId?: string;
}

export interface EventFrame {
  kind: string;
  tick: number;
  severity: string;
  crisis?: boolean;
}

export type ServerFrame =
  | { type: "result"; result: ResultFrame }
  | { type: "delta"; delta: DeltaFrame }
  | { type: "event"; event: EventFrame }
  | { type: "error"; error: ErrorRef & { correlationId?: string } };

// f1.viewport patch (schemaVersion 1, full every time).
export interface ViewportCell {
  x: number;
  y: number;
  terrain?: string;
  elevation?: number;
  road?: string;
  building?: string;
}

// One placed pylon span (FEAT-1972079851). class mirrors engine.power's
// PylonClass String() keys ("localPole" | "standardLattice" | "superGrid"
// today; later trio slices append more).
export interface PowerLine {
  id: number;
  class: string;
  fromX: number;
  fromY: number;
  toX: number;
  toY: number;
  capacityMW: number;
}

export interface ViewportPatch {
  schemaVersion: number;
  full: boolean;
  origin: { x: number; y: number };
  extent: { width: number; height: number };
  cells: ViewportCell[];
  /** Absent entirely while the engine has placed no pylons (omitempty). */
  powerLines?: PowerLine[];
}

export const PROTOCOL_VERSION = "1.0";

/** The f2.finance view (internal/ui/screens/finance/wire.go). */
export const FINANCE_VIEW = "f2.finance";

// --- f2.finance patch (schemaVersion 1) ---------------------------------
// Hand-mirrored copy of ui.screen.finance's wirePatch schema: balanceSheet
// has shipped since FEAT-208 increment 2; sankey + loans are FEAT-233's
// additions (compose publishes them from FinanceAPI's FlowMatrix/Loans
// query seams, per ASM-1220: bands anchor to "budget" and carry the budget
// INFLOW vs EXTERNAL OUTFLOW only). taxSliders exists on the Go schema but
// is not published yet — feat.compositionroot has no registered outbound
// edge to engine.tax, so live rates cannot be sourced without that
// registry change; the field is typed here so the panel lights up the day
// it ships.

export interface BalanceItem {
  label: string;
  valueMicropounds: number;
}

export interface BalanceSheetView {
  assets: BalanceItem[];
  liabilities: BalanceItem[];
  netWorth: number;
}

export interface LoanState {
  id: string;
  principalMicropounds: number;
  ratePercent: number;
  termMonths: number;
  nextPaymentMicropounds: number;
}

export interface TaxSliderState {
  id: string;
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  elasticityCurvePoints?: number[];
  incidenceDescription: string;
}

export interface SankeyBand {
  source: string;
  target: string;
  amount: number;
}

export interface SankeyView {
  bands: SankeyBand[];
}

export interface FinancePatch {
  schemaVersion: number;
  balanceSheet?: BalanceSheetView;
  loans?: LoanState[];
  taxSliders?: TaxSliderState[];
  sankey?: SankeyView;
}

let counter = 0;

/** Mint a client-side correlation id; uniqueness within a session is what matters. */
export function newCorrelationId(): string {
  counter += 1;
  const rnd =
    typeof crypto !== "undefined" && "getRandomValues" in crypto
      ? Array.from(crypto.getRandomValues(new Uint8Array(4)))
          .map((b) => b.toString(16).padStart(2, "0"))
          .join("")
      : "";
  return `web-${Date.now().toString(36)}-${counter}${rnd ? "-" + rnd : ""}`;
}
