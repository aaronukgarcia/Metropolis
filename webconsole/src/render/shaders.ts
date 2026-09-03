// shaders.ts — FEAT-2326609760 GPU acceleration spike, Phase 1.
//
// WGSL for the instanced building/road quad renderer. One unit-quad geometry
// (a hard-coded triangle-strip in vertex-shader-index space — no vertex
// buffer needed for the quad itself, per §1.2 of the plan's "one quad
// geometry, N instances" recipe), instanced by the STATIC/DYNAMIC buffers
// built in instanceBuilder.ts. The camera transform is a single uniform
// (§2.2 "camera as a uniform" — panning/zooming updates one small buffer,
// never the per-instance data).
//
// Layout here MUST stay in lockstep with instanceBuilder.ts's documented
// STATIC_FLOATS_PER_INSTANCE (8) / DYNAMIC_FLOATS_PER_INSTANCE (4) — changing
// one without the other silently misreads the buffer as garbage floats.

/** uCamera: vec4<f32>(scale, offsetX, offsetY, viewportAspectUnused). Mirrors
 * MapView.tsx's `geom.{s,ox,oy}` — screen_px = camera.xy_offset + tile * scale,
 * then normalised to clip space by dividing by the half-viewport size (bound
 * as uViewport below) so this shader needs no separate projection matrix. */
export const CAMERA_UNIFORM_FLOATS = 4;

/** uViewport: vec2<f32>(halfWidthPx, halfHeightPx). */
export const VIEWPORT_UNIFORM_FLOATS = 2;

export const INSTANCED_QUAD_WGSL = /* wgsl */ `
struct Camera {
  scale: f32,
  offsetX: f32,
  offsetY: f32,
  _pad: f32,
};
struct Viewport {
  halfW: f32,
  halfH: f32,
};

@group(0) @binding(0) var<uniform> camera: Camera;
@group(0) @binding(1) var<uniform> viewport: Viewport;

struct StaticInstance {
  @location(0) pos: vec2<f32>,   // tile x, y
  @location(1) size: vec2<f32>,  // tile w, h
  @location(2) color: vec4<f32>, // r, g, b, a
};
struct DynamicInstance {
  @location(3) online: f32,
  @location(4) occupancy: f32,
  @location(5) utilisation: f32,
  @location(6) tier: f32,
};

struct VertexOut {
  @builtin(position) clipPos: vec4<f32>,
  @location(0) color: vec4<f32>,
  @location(1) alpha: f32,
};

// Unit quad corners, drawn as a triangle-strip (4 verts, no index buffer).
const QUAD: array<vec2<f32>, 4> = array<vec2<f32>, 4>(
  vec2<f32>(0.0, 0.0),
  vec2<f32>(1.0, 0.0),
  vec2<f32>(0.0, 1.0),
  vec2<f32>(1.0, 1.0),
);

@vertex
fn vs_main(
  @builtin(vertex_index) vertexIndex: u32,
  inst: StaticInstance,
  dyn: DynamicInstance,
) -> VertexOut {
  var out: VertexOut;
  let corner = QUAD[vertexIndex];
  let tilePx = vec2<f32>(
    inst.pos.x + corner.x * inst.size.x,
    inst.pos.y + corner.y * inst.size.y,
  );
  let screenPx = vec2<f32>(
    camera.offsetX + tilePx.x * camera.scale,
    camera.offsetY + tilePx.y * camera.scale,
  );
  // screen px -> clip space [-1,1], flipping Y (screen Y grows downward).
  let clipX = screenPx.x / viewport.halfW - 1.0;
  let clipY = 1.0 - screenPx.y / viewport.halfH;
  out.clipPos = vec4<f32>(clipX, clipY, 0.0, 1.0);

  // Offline buildings dim, exactly as MapView.tsx's baseAlpha branch does
  // (online ? 1 : 0.45). Occupancy/utilisation/tier are carried through for
  // a future fragment-shader fill-fraction effect (Phase 2+); Phase 1 only
  // needs the base tint + online dimming to match the plan's "position/
  // size/colour" visual-parity bar.
  out.alpha = select(0.45, 1.0, dyn.online > 0.5);
  out.color = inst.color;
  return out;
}

@fragment
fn fs_main(in: VertexOut) -> @location(0) vec4<f32> {
  return vec4<f32>(in.color.rgb, in.color.a * in.alpha);
}
`;
