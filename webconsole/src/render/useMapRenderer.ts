// useMapRenderer.ts — FEAT-2326609760 GPU acceleration spike, Phase 1.
//
// THE single integration seam this spike hands to MapView.tsx. Deliberately
// tiny and self-contained: MapView owns its canvas ref, its own `state` and
// `geom`, and its own Canvas2D draw effect exactly as today; this hook is an
// ADDITIONAL, independent consumer of the same canvas that — once wired —
// would take over the building/road paint duty from MapView's own loop.
//
// NOT WIRED INTO MapView.tsx BY THIS SPIKE (see the report's "integration
// diff" for the <20-line patch the lead applies after both this estate and
// BUG-622's skip-identical-frames fix land, per the collision constraint in
// this task's brief). This file has zero imports from MapView.tsx and
// MapView.tsx has zero imports from this file or anything under render/ —
// there is no merge-conflict surface between the two lanes today.
import { useEffect, useRef, type RefObject } from 'react';
import { MapRenderer, type Geom, type RenderMode } from './mapRenderer.ts';
import type { SimState } from '../sim/types.ts';

export interface UseMapRendererResult {
  mode: RenderMode;
  fallbackReason: string | null;
}

/**
 * Owns a MapRenderer bound to `canvasRef.current` for the component's
 * lifetime. Call the returned `render(state, geom)` from the same effect
 * that currently drives MapView's Canvas2D loop (or, once adopted as the
 * default per Aaron's "straight to default" ruling, in PLACE of it).
 */
export function useMapRenderer(canvasRef: RefObject<HTMLCanvasElement | null>): {
  render: (state: SimState, geom: Geom) => void;
  status: () => UseMapRendererResult;
} {
  const rendererRef = useRef<MapRenderer | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const renderer = new MapRenderer(canvas);
    rendererRef.current = renderer;
    void renderer.init();
    return () => {
      renderer.dispose();
      rendererRef.current = null;
    };
    // canvasRef.current intentionally not a dependency — the renderer binds
    // to whichever canvas element exists at mount; MapView's canvas element
    // itself is stable for the component's life (same <canvas> node reused
    // across resizes, per the existing draw effect's own resize handling).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return {
    render: (state: SimState, geom: Geom) => {
      rendererRef.current?.render(state, geom);
    },
    status: () => ({
      mode: rendererRef.current?.mode ?? 'canvas2d',
      fallbackReason: rendererRef.current?.fallbackReason ?? 'renderer not yet initialised',
    }),
  };
}
