import {
  PROTOCOL_VERSION,
  newCorrelationId,
  type CommandPayload,
  type ServerFrame,
} from "./messages";

export type ConnectionStatus = "connecting" | "connected" | "disconnected";

export interface BridgeHandlers {
  onStatus?: (status: ConnectionStatus) => void;
  onFrame?: (frame: ServerFrame) => void;
}

/**
 * WSBridge is the browser side of int.transport: it owns one WebSocket to
 * the Go server, sends protocol.Command envelopes as text frames, and
 * fans every server frame out through onFrame. It auto-reconnects with a
 * simple fixed backoff — the engine keeps simulating without us, and a
 * fresh connection re-subscribes from scratch (full snapshots make this
 * safe).
 */
export class WSBridge {
  private ws: WebSocket | null = null;
  private readonly url: string;
  private readonly handlers: BridgeHandlers;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private closedByUser = false;

  constructor(url: string, handlers: BridgeHandlers = {}) {
    this.url = url;
    this.handlers = handlers;
  }

  connect(): void {
    if (this.ws || this.closedByUser) return;
    this.handlers.onStatus?.("connecting");
    const ws = new WebSocket(this.url);
    this.ws = ws;

    ws.onopen = () => this.handlers.onStatus?.("connected");
    ws.onmessage = (ev) => {
      try {
        const frame = JSON.parse(ev.data as string) as ServerFrame;
        this.handlers.onFrame?.(frame);
      } catch {
        // A malformed frame is dropped loudly in the console; the stream
        // itself stays authoritative.
        console.error("[ws] undecodable frame", ev.data);
      }
    };
    ws.onclose = () => {
      this.ws = null;
      this.handlers.onStatus?.("disconnected");
      if (!this.closedByUser) {
        this.reconnectTimer = setTimeout(() => this.connect(), 2000);
      }
    };
    ws.onerror = () => {
      // onclose follows; nothing else to do here.
    };
  }

  /** Send one command payload wrapped in the v1 envelope. */
  send(payload: CommandPayload): string {
    const correlationId = newCorrelationId();
    const envelope = {
      protocolVersion: PROTOCOL_VERSION,
      correlationId,
      issuedAtTick: 0,
      kind: payload.kind,
      payload,
    };
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(envelope));
    } else {
      console.warn("[ws] command dropped, not connected", envelope.kind);
    }
    return correlationId;
  }

  close(): void {
    this.closedByUser = true;
    if (this.reconnectTimer !== null) clearTimeout(this.reconnectTimer);
    this.ws?.close();
    this.ws = null;
  }
}
