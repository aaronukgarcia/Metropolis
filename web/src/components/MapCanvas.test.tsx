import { describe, expect, it } from "vitest";
import { lineCells, MapCanvas } from "./MapCanvas";
import { render } from "@testing-library/react";
import type { ViewportPatch } from "../ws/messages";

describe("lineCells", () => {
  it("walks a horizontal span endpoint-inclusive", () => {
    expect(lineCells(2, 2, 5, 2)).toEqual([
      [2, 2],
      [3, 2],
      [4, 2],
      [5, 2],
    ]);
  });

  it("walks a reverse vertical span endpoint-inclusive", () => {
    expect(lineCells(4, 9, 4, 6)).toEqual([
      [4, 9],
      [4, 8],
      [4, 7],
      [4, 6],
    ]);
  });

  it("walks a diagonal span", () => {
    expect(lineCells(0, 0, 3, 3)).toEqual([
      [0, 0],
      [1, 1],
      [2, 2],
      [3, 3],
    ]);
  });
});

describe("lineCells window clamping", () => {
  const win = { x0: 0, y0: 0, x1: 7, y1: 7 };

  it("bounds a hostile oversized horizontal span to the window", () => {
    const cells = lineCells(-1e9, 3, 1e9, 3, win);
    // Bounded by the window itself, never by the endpoint magnitudes
    // (SEC-039 class: the unclamped walker allocated adx+ady+1 cells).
    expect(cells.length).toBeLessThanOrEqual(
      win.x1 - win.x0 + win.y1 - win.y0 + 2,
    );
    expect(
      cells.every(([x, y]) => x >= 0 && x <= 7 && y >= 0 && y <= 7),
    ).toBe(true);
    expect(cells[0]).toEqual([0, 3]);
    expect(cells[cells.length - 1]).toEqual([7, 3]);
  });

  it("matches the unclipped walk's visible cells for a hostile diagonal", () => {
    const got = lineCells(-1_000_000, -500_000, 1_000_000, 500_000, win);
    expect(got.length).toBeLessThanOrEqual(
      win.x1 - win.x0 + win.y1 - win.y0 + 2,
    );
    // Exactness reference: the plain walk over a scaled-down span of the
    // same slope, filtered to the window.
    const want: Array<[number, number]> = [];
    let x = -16;
    let y = -8;
    for (;;) {
      if (x >= 0 && x <= 7 && y >= 0 && y <= 7) want.push([x, y]);
      if (x === 16 && y === 8) break;
      x += 1;
      y += 1;
    }
    expect(got).toEqual(want);
  });

  it("returns an empty array when the span never enters the window", () => {
    expect(lineCells(-50, -50, -10, -10, win)).toEqual([]);
  });
});

describe("MapCanvas", () => {
  const basePatch: ViewportPatch = {
    schemaVersion: 1,
    full: true,
    origin: { x: 0, y: 0 },
    extent: { width: 8, height: 8 },
    cells: Array.from({ length: 64 }, (_, i) => ({
      x: i % 8,
      y: Math.floor(i / 8),
      terrain: "grass",
    })),
    powerLines: [
      {
        id: 1,
        class: "standardLattice",
        fromX: 1,
        fromY: 1,
        toX: 5,
        toY: 1,
        capacityMW: 40,
      },
    ],
  };

  it("renders the awaiting placeholder with no patch", () => {
    const { getByText } = render(<MapCanvas patch={null} />);
    expect(getByText(/awaiting f1\.viewport/)).toBeTruthy();
  });

  it("renders without throwing for a patch carrying powerLines (toggle on and off)", () => {
    // jsdom's canvas getContext returns null; the component must no-op
    // cleanly in both toggle states rather than crash.
    const { unmount } = render(<MapCanvas patch={basePatch} showPower />);
    unmount();
    render(<MapCanvas patch={basePatch} showPower={false} />);
  });
});
