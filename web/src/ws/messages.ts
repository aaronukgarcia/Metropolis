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

export interface ViewportPatch {
  schemaVersion: number;
  full: boolean;
  origin: { x: number; y: number };
  extent: { width: number; height: number };
  cells: ViewportCell[];
}

export const PROTOCOL_VERSION = "1.0";

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
