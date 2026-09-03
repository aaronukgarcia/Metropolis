// webgpuSupport.ts — FEAT-2326609760 GPU acceleration spike, Phase 1.
//
// Feature-detection + device acquisition for the WebGPU renderer, isolated
// from mapRenderer.ts so it can be unit-tested with a mocked `navigator.gpu`
// without needing a real GPU or a real canvas (jsdom has neither — see the
// plan's Phase 0 note that Aaron's own machine supplies the real-GPU numbers
// later; this module's job is only to be RIGHT about when to fall back, not
// to be fast).
//
// Every path is fail-closed to Canvas2D: an exception anywhere in the
// acquisition chain (missing navigator.gpu, requestAdapter/requestDevice
// rejecting, either resolving null) is caught and reported as a `reason`
// string rather than thrown — a GPU spike must never crash page boot on a
// browser/driver that doesn't support it (GR#1: log + typed + recoverable).

/** Minimal structural shape this module needs from the real GPUDevice type,
 * kept loose (not importing "@webgpu/types") so the whole render/ tree stays
 * dependency-free per the project's near-zero-dependency posture (the plan's
 * §1.3 note) and so test mocks can implement exactly this surface. */
export interface MinimalGPUDevice {
  readonly lost: Promise<{ reason: string; message: string }>;
  createBuffer(desc: { size: number; usage: number; label?: string }): unknown;
  createShaderModule(desc: { code: string; label?: string }): unknown;
  createRenderPipeline(desc: unknown): unknown;
  createBindGroup(desc: unknown): unknown;
  createCommandEncoder(desc?: { label?: string }): {
    beginRenderPass(desc: unknown): {
      setPipeline(p: unknown): void;
      setBindGroup(index: number, bg: unknown): void;
      setVertexBuffer(slot: number, buf: unknown): void;
      draw(vertexCount: number, instanceCount?: number): void;
      end(): void;
    };
    finish(): unknown;
  };
  readonly queue: {
    writeBuffer(buffer: unknown, offset: number, data: ArrayBufferView): void;
    submit(cmds: unknown[]): void;
  };
  destroy(): void;
}

export interface MinimalGPUAdapter {
  requestDevice(): Promise<MinimalGPUDevice>;
}

export interface MinimalGPUNavigator {
  requestAdapter(): Promise<MinimalGPUAdapter | null>;
  getPreferredCanvasFormat?(): string;
}

export type WebGPUAcquireResult =
  | { supported: true; device: MinimalGPUDevice; adapter: MinimalGPUAdapter; format: string }
  | { supported: false; reason: string };

/**
 * Feature-detects and acquires a WebGPU device. `gpuOverride` lets tests
 * inject a fake `navigator.gpu`-shaped object without touching the real
 * global; production callers omit it and this reads `navigator.gpu`.
 */
export async function acquireWebGPU(
  gpuOverride?: MinimalGPUNavigator | undefined
): Promise<WebGPUAcquireResult> {
  try {
    const gpu: MinimalGPUNavigator | undefined =
      gpuOverride ??
      (typeof navigator !== 'undefined' ? (navigator as unknown as { gpu?: MinimalGPUNavigator }).gpu : undefined);
    if (!gpu) {
      return { supported: false, reason: 'navigator.gpu is undefined (browser has no WebGPU support)' };
    }
    const adapter = await gpu.requestAdapter();
    if (!adapter) {
      return { supported: false, reason: 'navigator.gpu.requestAdapter() resolved null (no compatible adapter)' };
    }
    const device = await adapter.requestDevice();
    if (!device) {
      return { supported: false, reason: 'adapter.requestDevice() resolved null/undefined' };
    }
    const format = gpu.getPreferredCanvasFormat ? gpu.getPreferredCanvasFormat() : 'bgra8unorm';
    return { supported: true, device, adapter, format };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return { supported: false, reason: `WebGPU acquisition threw: ${message}` };
  }
}

/**
 * Wires `device.lost` to `onLost`, per the plan's risk #2 (context loss must
 * fall back to Canvas2D without blanking the map). `device.lost` resolves
 * exactly once per device (never rejects, per the WebGPU spec) — this never
 * needs a catch, but `onLost` itself is wrapped so a bug in the fallback
 * handler can't leave an unhandled rejection dangling.
 */
export function watchForContextLoss(
  device: MinimalGPUDevice,
  onLost: (reason: string, message: string) => void
): void {
  const safeOnLost = (reason: string, message: string): void => {
    try {
      onLost(reason, message);
    } catch {
      // A bug in the caller's own fallback handler must never surface as an
      // unhandled rejection out of this watcher — the caller already has
      // its own error trapping duty for whatever it does inside onLost.
    }
  };
  device.lost
    .then((info) => safeOnLost(info.reason, info.message))
    .catch((err) => {
      const message = err instanceof Error ? err.message : String(err);
      safeOnLost('watch-handler-error', message);
    });
}
