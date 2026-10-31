// buildingMotifs.tsx — FEAT-2326609841 AC-3: the guaranteed never-empty picture
// plate of the property card. A deterministic, hand-composed cartoon SVG per
// ZoneKind (roofs, windows, towers, smoke, trees ...), tinted from the spec's
// own colour and scaled from its footprint. Pure: same spec -> byte-identical
// output (no clock, no randomness, no storage — BUG-602/GR#21). Variation
// between specs of one kind comes from a stable hash of spec.id.
//
// A per-spec art slot sits on top of this: if `webconsole/public/art/<id>.svg`
// (or .png) has been landed (see public/art/README.md for provenance), the
// card shows it; the motif is the fallback when the file is absent or fails to
// load. The motif never disappears — the art overlays it.

import type { Spec } from '../sim/data';

const W = 160;
const H = 96;

/** Stable 32-bit FNV-1a hash of a string (determinism, no Math.random). */
function hash(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

// Every Spec colour in data.ts is 6-digit hex; the shorthand 3-digit form is
// not accepted here (dead branch removed at port), with the neutral fallback
// covering anything malformed.
const HEX = /^#[0-9a-f]{6}$/i;

function rgb(hex: string): [number, number, number] {
  const h = HEX.test(hex) ? hex.slice(1) : '8a94a0';
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}

/** Mix a hex colour toward white (amt>0) or black (amt<0). */
function shade(hex: string, amt: number): string {
  const [r, g, b] = rgb(hex);
  const t = amt >= 0 ? 255 : 0;
  const k = Math.abs(amt);
  const c = (v: number) => Math.round(v + (t - v) * k);
  const p = (v: number) => c(v).toString(16).padStart(2, '0');
  return `#${p(r)}${p(g)}${p(b)}`;
}

const R = (x: number, y: number, w: number, h: number, fill: string, extra = ''): string =>
  `<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="${fill}"${extra ? ' ' + extra : ''}/>`;
const P = (pts: string, fill: string): string => `<polygon points="${pts}" fill="${fill}"/>`;
const C = (x: number, y: number, r: number, fill: string): string =>
  `<circle cx="${x}" cy="${y}" r="${r}" fill="${fill}"/>`;

const INK = '#2b2f36';
const GLASS = '#cfe8ff';
const GROUND_Y = 78;

function windows(x: number, y: number, cols: number, rows: number, cw: number, ch: number, gx: number, gy: number, lit: string): string {
  let out = '';
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      out += R(x + c * (cw + gx), y + r * (ch + gy), cw, ch, lit, `stroke="${INK}" stroke-width="0.6"`);
    }
  }
  return out;
}

function tree(x: number, base: number, s: number): string {
  return R(x - 1.5 * s, base - 8 * s, 3 * s, 8 * s, '#7a5230') + C(x, base - 12 * s, 7 * s, '#3f9b4b') + C(x - 4 * s, base - 9 * s, 5 * s, '#4cad59');
}

function house(x: number, base: number, w: number, h: number, color: string, roof: string): string {
  const doorW = Math.max(4, Math.round(w * 0.2));
  return (
    R(x, base - h, w, h, color, `stroke="${INK}" stroke-width="0.8"`) +
    P(`${x - 3},${base - h} ${x + w / 2},${base - h - Math.round(w * 0.45)} ${x + w + 3},${base - h}`, roof) +
    R(x + w - 8, base - h - Math.round(w * 0.45) + 3, 4, 9, shade(roof, -0.25)) +
    R(x + (w - doorW) / 2, base - Math.round(h * 0.55), doorW, Math.round(h * 0.55), '#6b4226') +
    R(x + 3, base - h + 5, 6, 6, GLASS, `stroke="${INK}" stroke-width="0.6"`) +
    R(x + w - 9, base - h + 5, 6, 6, GLASS, `stroke="${INK}" stroke-width="0.6"`)
  );
}

type Painter = (sp: Spec, col: string, v: number, wScale: number) => string;

const PAINTERS: Record<string, Painter> = {
  residential: (sp, col, v, ws) => {
    const roofs = ['#b5523b', '#7d4b9b', '#3b6ea5', '#a8742a'];
    const dense = (sp.residents ?? 0) > 400;
    if (dense) {
      // Apartment blocks: stepped slabs with window grids.
      return (
        R(22, 26, 40, GROUND_Y - 26, col, `stroke="${INK}" stroke-width="0.8"`) +
        windows(26, 31, 4, 7, 6, 4, 3, 3, GLASS) +
        R(70, 40, 34, GROUND_Y - 40, shade(col, 0.15), `stroke="${INK}" stroke-width="0.8"`) +
        windows(74, 44, 3, 5, 6, 4, 3, 3, GLASS) +
        R(112, 52, 28, GROUND_Y - 52, shade(col, -0.12), `stroke="${INK}" stroke-width="0.8"`) +
        windows(116, 56, 3, 3, 5, 4, 3, 3, GLASS)
      );
    }
    const n = 2 + (v % 2) + (ws > 1.2 ? 1 : 0);
    let out = '';
    for (let i = 0; i < n; i++) {
      const w = 28 + ((v >> (i * 3)) % 8);
      out += house(10 + i * (130 / n), GROUND_Y, w, 22 + ((v >> (i * 2)) % 10), shade(col, i % 2 ? 0.2 : 0), roofs[(v + i) % roofs.length]);
    }
    return out + tree(150, GROUND_Y, 0.9);
  },
  commercial: (_sp, col) => {
    const stripes = Array.from({ length: 6 }, (_, i) => R(24 + i * 18, 36, 18, 10, i % 2 ? '#ffffff' : '#d9473f')).join('');
    return (
      R(20, 30, 120, GROUND_Y - 30, col, `stroke="${INK}" stroke-width="0.8"`) +
      R(20, 24, 120, 8, shade(col, -0.25)) +
      stripes +
      R(30, 52, 34, 26, GLASS, `stroke="${INK}" stroke-width="0.8"`) +
      R(76, 52, 18, 26, '#6b4226') +
      R(104, 52, 26, 18, GLASS, `stroke="${INK}" stroke-width="0.8"`) +
      `<text x="80" y="22" text-anchor="middle" font-size="8" font-family="sans-serif" font-weight="700" fill="#fff">SHOP</text>`
    );
  },
  office: (_sp, col) =>
    R(50, 8, 60, GROUND_Y - 8, col, `stroke="${INK}" stroke-width="0.8"`) +
    windows(54, 13, 6, 12, 6, 3.5, 3, 2.5, GLASS) +
    R(30, 40, 22, GROUND_Y - 40, shade(col, -0.18), `stroke="${INK}" stroke-width="0.8"`) +
    windows(33, 44, 3, 6, 4.5, 3.5, 2.5, 2.5, GLASS) +
    R(108, 34, 24, GROUND_Y - 34, shade(col, 0.12), `stroke="${INK}" stroke-width="0.8"`) +
    windows(111, 38, 3, 7, 4.5, 3.5, 2.5, 2.5, GLASS) +
    R(79, 2, 2, 8, INK),
  industrial: (_sp, col, v) => {
    let out = '';
    for (let i = 0; i < 3; i++) {
      out += P(`${20 + i * 34},${GROUND_Y - 30} ${20 + i * 34},${GROUND_Y - 44} ${20 + i * 34 + 30},${GROUND_Y - 30}`, shade(col, -0.2));
    }
    out +=
      R(20, GROUND_Y - 30, 102, 30, col, `stroke="${INK}" stroke-width="0.8"`) +
      R(30, GROUND_Y - 14, 18, 14, '#555b66') +
      R(128, 18, 9, GROUND_Y - 18, '#8a6c5a') +
      R(124, 14, 17, 6, '#6e5546') +
      C(134, 8, 6, '#d8dce2') +
      C(142 + (v % 4), 3, 5, '#e8ebef');
    return out;
  },
  mine: (_sp, col) =>
    P(`10,${GROUND_Y} 50,40 90,${GROUND_Y}`, '#7a6a58') +
    P(`60,${GROUND_Y} 100,52 150,${GROUND_Y}`, '#8c7b67') +
    R(108, 24, 4, 54, INK) +
    P(`98,${GROUND_Y} 110,24 122,${GROUND_Y}`, 'none') +
    C(110, 24, 8, shade(col, -0.1)) +
    R(96, GROUND_Y - 14, 28, 14, col, `stroke="${INK}" stroke-width="0.8"`) +
    R(104, GROUND_Y - 8, 12, 8, '#2a2118'),
  park: (_sp, _col, v) =>
    `<ellipse cx="82" cy="${GROUND_Y - 6}" rx="30" ry="9" fill="#5aa9e6" stroke="#2f6ea3" stroke-width="0.8"/>` +
    tree(24, GROUND_Y, 1.3) +
    tree(130, GROUND_Y, 1.1) +
    tree(46 + (v % 6), GROUND_Y - 2, 0.9) +
    R(60, GROUND_Y - 22, 22, 3, '#7a5230') +
    R(62, GROUND_Y - 19, 2, 5, '#7a5230') +
    R(78, GROUND_Y - 19, 2, 5, '#7a5230'),
  power: (sp, col) => {
    const id = sp.id;
    if (id.includes('wind')) {
      const t = (x: number, s: number) =>
        P(`${x - 1.5},${GROUND_Y} ${x + 1.5},${GROUND_Y} ${x + 0.8},${GROUND_Y - 44 * s} ${x - 0.8},${GROUND_Y - 44 * s}`, '#f1f3f5') +
        `<g stroke="#f1f3f5" stroke-width="1.6" stroke-linecap="round"><line x1="${x}" y1="${GROUND_Y - 44 * s}" x2="${x}" y2="${GROUND_Y - 66 * s}"/><line x1="${x}" y1="${GROUND_Y - 44 * s}" x2="${x - 14 * s}" y2="${GROUND_Y - 37 * s}"/><line x1="${x}" y1="${GROUND_Y - 44 * s}" x2="${x + 14 * s}" y2="${GROUND_Y - 37 * s}"/></g>` +
        C(x, GROUND_Y - 44 * s, 2.2, col);
      return t(40, 1) + t(84, 1.15) + t(126, 0.9);
    }
    if (id.includes('solar') || id.includes('pv')) {
      return Array.from({ length: 4 }, (_, i) => P(`${14 + i * 36},${GROUND_Y} ${34 + i * 36},${GROUND_Y} ${40 + i * 36},${GROUND_Y - 22} ${20 + i * 36},${GROUND_Y - 22}`, '#274b8f')).join('') +
        C(138, 18, 9, '#ffd23f');
    }
    // Thermal / nuclear: cooling towers with steam + hall.
    const tower = (x: number) =>
      P(`${x - 16},${GROUND_Y} ${x + 16},${GROUND_Y} ${x + 10},${GROUND_Y - 24} ${x + 13},${GROUND_Y - 44} ${x - 13},${GROUND_Y - 44} ${x - 10},${GROUND_Y - 24}`, col) +
      C(x, GROUND_Y - 52, 7, '#e5e8ec') + C(x + 8, GROUND_Y - 60, 6, '#f0f2f5');
    return tower(44) + tower(100) + R(120, GROUND_Y - 22, 32, 22, shade(col, -0.2), `stroke="${INK}" stroke-width="0.8"`) +
      `<polygon points="134,${GROUND_Y - 16} 128,${GROUND_Y - 6} 133,${GROUND_Y - 6} 131,${GROUND_Y - 1} 139,${GROUND_Y - 11} 134,${GROUND_Y - 11}" fill="#ffd23f"/>`;
  },
  water: (sp, col) =>
    sp.tag === 'waste'
      ? R(22, GROUND_Y - 28, 52, 28, col, `stroke="${INK}" stroke-width="0.8"`) +
        `<ellipse cx="48" cy="${GROUND_Y - 28}" rx="26" ry="5" fill="${shade(col, 0.3)}"/>` +
        R(94, GROUND_Y - 22, 40, 22, shade(col, -0.2), `stroke="${INK}" stroke-width="0.8"`) +
        R(74, GROUND_Y - 8, 20, 4, '#7d6a52')
      : P(`60,${GROUND_Y} 100,${GROUND_Y} 94,${GROUND_Y - 30} 66,${GROUND_Y - 30}`, shade(col, -0.2)) +
        `<ellipse cx="80" cy="${GROUND_Y - 44}" rx="26" ry="16" fill="${col}" stroke="${INK}" stroke-width="0.8"/>` +
        `<path d="M72 ${GROUND_Y - 46} q8 -9 16 0 q-8 11 -16 0z" fill="#e8f6ff"/>` +
        R(110, GROUND_Y - 6, 36, 6, '#6f7f8f'),
  health: (_sp, col) =>
    R(30, 26, 100, GROUND_Y - 26, '#f4f6f8', `stroke="${INK}" stroke-width="0.8"`) +
    R(30, 20, 100, 8, col) +
    R(72, 34, 16, 4, '#d33') + R(78, 28, 4, 16, '#d33') +
    windows(38, 52, 4, 2, 10, 7, 6, 4, GLASS) +
    R(112, 56, 12, 22, '#6b8aa8'),
  police: (_sp, col) =>
    R(34, 28, 92, GROUND_Y - 28, '#e6ebf2', `stroke="${INK}" stroke-width="0.8"`) +
    R(34, 22, 92, 8, col) +
    `<path d="M80 34 l11 4 v9 q0 8 -11 12 q-11 -4 -11 -12 v-9z" fill="${col}" stroke="#fff" stroke-width="1"/>` +
    C(48, 20, 4, '#3d7bff') + C(112, 20, 4, '#e53935') +
    R(72, 58, 16, 20, '#4a5568'),
  fire: (_sp, col) =>
    R(26, 30, 108, GROUND_Y - 30, '#d94a3a', `stroke="${INK}" stroke-width="0.8"`) +
    R(26, 24, 108, 8, shade(col, -0.2)) +
    R(36, 46, 28, 32, '#f1f1f1', `stroke="${INK}" stroke-width="0.8"`) +
    R(96, 46, 28, 32, '#f1f1f1', `stroke="${INK}" stroke-width="0.8"`) +
    R(130, 10, 8, 68, '#9a3128') + P('130,10 134,2 138,10', '#ffcf3f'),
  school: (_sp, col) =>
    R(24, 38, 112, GROUND_Y - 38, '#c9753d', `stroke="${INK}" stroke-width="0.8"`) +
    P('20,38 80,18 140,38', col) +
    R(70, 8, 20, 14, shade(col, -0.2)) + C(80, 12, 3, '#ffd23f') +
    windows(32, 46, 5, 1, 12, 10, 8, 0, GLASS) +
    R(72, 58, 16, 20, '#6b4226') +
    R(144, 18, 1.5, 36, INK) + P('145.5,18 156,22 145.5,26', '#e53935'),
  road: () =>
    R(0, 34, W, 34, '#4a4f57') + R(0, 31, W, 3, '#8a9099') + R(0, 68, W, 3, '#8a9099') +
    Array.from({ length: 6 }, (_, i) => R(6 + i * 28, 49, 16, 3, '#f2d64b')).join(''),
  motorway: () =>
    R(0, 24, W, 52, '#3d4249') + R(0, 22, W, 3, '#c9ced6') + R(0, 75, W, 3, '#c9ced6') +
    R(0, 49, W, 3, '#3a7bd5') +
    Array.from({ length: 6 }, (_, i) => R(8 + i * 28, 36, 16, 2.5, '#fff') + R(8 + i * 28, 62, 16, 2.5, '#fff')).join('') +
    R(20, 4, 36, 14, '#1d6f42') + `<text x="38" y="14" font-size="9" font-family="sans-serif" font-weight="700" text-anchor="middle" fill="#fff">M20</text>`,
  rail: () =>
    R(0, 40, W, 16, '#7c6a58') +
    Array.from({ length: 16 }, (_, i) => R(i * 10 + 2, 38, 4, 20, '#4a3a2c')).join('') +
    R(0, 43, W, 2.5, '#c3c8d0') + R(0, 51, W, 2.5, '#c3c8d0'),
  station: (_sp, col) =>
    R(0, GROUND_Y - 6, W, 6, '#9ca3ad') +
    R(14, 30, 132, 5, col) + R(20, 35, 3, 37, INK) + R(137, 35, 3, 37, INK) + R(78, 35, 3, 37, INK) +
    R(46, 44, 68, 28, '#e8e2d2', `stroke="${INK}" stroke-width="0.8"`) +
    `<circle cx="80" cy="22" r="7" fill="#fff" stroke="${INK}" stroke-width="1"/><path d="M80 22 l0 -5 M80 22 l4 2" stroke="${INK}" stroke-width="1"/>`,
  pylon: () =>
    `<g stroke="${INK}" stroke-width="2" fill="none"><path d="M80 6 L62 ${GROUND_Y} M80 6 L98 ${GROUND_Y} M70 40 L90 40 M66 58 L94 58 M72 24 L88 24 M70 40 L88 24 M90 40 L72 24 M66 58 L90 40 M94 58 L70 40"/><path d="M52 24 L108 24 M60 38 L100 38"/></g>` +
    `<g stroke="${INK}" stroke-width="0.8" fill="none"><path d="M0 30 Q26 40 52 24 M108 24 Q134 40 160 30"/></g>`,
  transport: (_sp, col) =>
    R(18, 36, 124, 32, col, `stroke="${INK}" stroke-width="0.8"`) + windows(24, 41, 6, 1, 14, 10, 6, 0, GLASS) +
    C(40, 70, 7, INK) + C(120, 70, 7, INK) + C(40, 70, 3, '#aab') + C(120, 70, 3, '#aab'),
  civic: (_sp, col) =>
    P('16,34 80,10 144,34', col) + R(22, 34, 116, 5, shade(col, -0.2)) +
    Array.from({ length: 6 }, (_, i) => R(30 + i * 18, 39, 7, 36, '#efe9dc', `stroke="${INK}" stroke-width="0.5"`)).join('') +
    R(16, 75, 128, 5, '#c9c2b2') + R(10, 80, 140, 4, '#b4ad9d'),
  leisure: (_sp, col) =>
    P('20,60 80,14 140,60', col) + R(20, 58, 120, 20, shade(col, -0.15)) +
    Array.from({ length: 5 }, (_, i) => P(`${20 + i * 24},60 ${44 + i * 24},60 ${32 + i * 24},18`, i % 2 ? 'rgba(255,255,255,0.35)' : 'none')).join('') +
    R(78, 4, 1.5, 12, INK) + P('79.5,4 92,8 79.5,12', '#ffd23f') +
    R(70, 62, 20, 16, '#2b2f36'),
  landmark: (_sp, col) =>
    P('80,4 90,34 122,34 96,52 106,82 80,64 54,82 64,52 38,34 70,34', col) +
    `<path d="M80 12 L86 34 L106 34 L90 46" fill="none" stroke="#fff" stroke-opacity="0.5" stroke-width="1.2"/>` +
    R(56, GROUND_Y + 4, 48, 6, shade(col, -0.3)),
};

/**
 * The picture for a spec as a complete standalone SVG string. Deterministic in
 * (spec.id, spec.kind, spec.color, spec.w/h, spec.tag, spec.residents): same
 * spec -> byte-identical string (AC-3 determinism).
 */
export function motifSvgOf(sp: Pick<Spec, 'id' | 'kind' | 'color' | 'w' | 'h' | 'tag' | 'residents'>): string {
  const col = HEX.test(sp.color) ? sp.color : '#8a94a0';
  const v = hash(sp.id);
  const painter = PAINTERS[sp.kind] ?? PAINTERS.landmark;
  const footprintScale = Math.max(1, Math.min(2, Math.sqrt((sp.w || 1) * (sp.h || 1)) / 2));
  const flat = sp.kind === 'road' || sp.kind === 'motorway' || sp.kind === 'rail';
  const sky = `<defs><linearGradient id="sky-${sp.kind}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${shade(col, 0.82)}"/><stop offset="1" stop-color="${shade(col, 0.55)}"/></linearGradient></defs>`;
  const ground = flat ? '' : R(0, GROUND_Y, W, H - GROUND_Y, '#7fb069') + R(0, GROUND_Y, W, 2, '#5c8f4a');
  const cloud = flat ? '' : C(20 + (v % 40), 14, 5, '#ffffff') + C(26 + (v % 40), 12, 6, '#ffffff') + C(33 + (v % 40), 15, 4, '#ffffff');
  const body = painter(sp as Spec, col, v, footprintScale);
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" width="100%" height="100%" preserveAspectRatio="xMidYMid slice" role="img" aria-label="${sp.kind}" data-motif="${sp.kind}">` +
    sky +
    R(0, 0, W, H, `url(#sky-${sp.kind})`) +
    cloud +
    ground +
    body +
    `</svg>`
  );
}

/**
 * Spec ids that have a landed art file in webconsole/public/art/<id>.svg.
 * Empty until art lands (see public/art/README.md); adding an id here is the
 * ONLY step that switches the card from motif to art for that spec, so the
 * card never fires a request for a file that does not exist.
 */
export const LANDED_ART: ReadonlySet<string> = new Set<string>([]);

/** Where a landed per-spec art file would live (served from webconsole/public). */
export function artUrlOf(specId: string): string {
  return `/art/${specId}.svg`;
}

/**
 * The picture plate. Always renders the deterministic motif; if a per-spec art
 * file exists it is layered over it (a failed load hides itself, leaving the
 * motif — the plate is NEVER empty).
 */
export function BuildingPicture({ spec }: { spec: Spec }) {
  const svg = motifSvgOf(spec);
  return (
    <div className="pc-plate" data-testid="pc-plate" data-kind={spec.kind}>
      <div className="pc-motif" dangerouslySetInnerHTML={{ __html: svg }} />
      {LANDED_ART.has(spec.id) && (
        <img
          className="pc-art"
          src={artUrlOf(spec.id)}
          alt=""
          onError={(e) => {
            (e.currentTarget as HTMLImageElement).style.display = 'none';
          }}
        />
      )}
    </div>
  );
}
