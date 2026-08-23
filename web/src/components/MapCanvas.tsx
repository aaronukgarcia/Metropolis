import { useEffect, useRef } from "react";
import type { ViewportPatch, ViewportCell } from "../ws/messages";

const TERRAIN_COLORS: Record<string, string> = {
  grass: "#5a8f3c",
  woodland: "#2e5d28",
  water: "#2b6a9b",
  shingle: "#b8ad94",
  rock: "#7d7466",
};

function cellColor(cell: ViewportCell): string {
  if (cell.building) return "#c0392b";
  return TERRAIN_COLORS[cell.terrain ?? ""] ?? "#333333";
}

/**
 * MapCanvas is the S1 placeholder consumer of the f1.viewport stream: it
 * paints the latest full snapshot as a flat colour grid. d3-based
 * rendering and pan/zoom arrive later; this only proves the delta path
 * lights pixels.
 */
export function MapCanvas({ patch }: { patch: ViewportPatch | null }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !patch) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    ctx.fillStyle = "#111111";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    if (patch.cells.length === 0) return;

    const scale = Math.max(
      1,
      Math.floor(canvas.width / patch.extent.width),
    );
    for (const cell of patch.cells) {
      ctx.fillStyle = cellColor(cell);
      ctx.fillRect(cell.x * scale, cell.y * scale, scale, scale);
    }
  }, [patch]);

  return (
    <div className="map-canvas">
      <canvas ref={canvasRef} width={400} height={400} />
      {patch === null && <p className="placeholder">awaiting f1.viewport…</p>}
    </div>
  );
}
