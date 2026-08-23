import type { ConnectionStatus } from "../ws/bridge";

export function ConnectionStatusIndicator({
  status,
}: {
  status: ConnectionStatus;
}) {
  return (
    <span className={`conn-status conn-${status}`} data-testid="conn-status">
      {status === "connected" ? "● connected" : status === "connecting" ? "○ connecting…" : "○ disconnected"}
    </span>
  );
}
