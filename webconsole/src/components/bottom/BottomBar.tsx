import { useEffect, useRef, useState } from 'react';
import { PALETTE, SPECS, placementCost, isFreeZone, constructionTicks, isPlaceable, sortPaletteItems } from '../../sim/data';
import type { ToolMode } from '../../sim/types';
import { useSim } from '../../sim/simContext';
import { specUnlocked } from '../../sim/engine';
import { fmtMoney } from '../../sim/utils';
import { Panel } from '../Tabs';
import { SpecCard } from '../SpecCard';

const TABS = [
  { id: 'build', label: 'Build' },
  { id: 'move', label: 'Move' },
];

export function BottomBar() {
  const [tab, setTab] = useState('build');
  return (
    <Panel title="Tools" tabs={TABS} active={tab} onSelect={setTab}>
      {tab === 'build' ? <BuildTab /> : <MoveTab />}
    </Panel>
  );
}

function BuildTab() {
  const { state, dispatch } = useSim();
  const [fam, setFam] = useState(PALETTE[0].title);
  const [openSpecId, setOpenSpecId] = useState<string | null>(null);
  const famDef = PALETTE.find((p) => p.title === fam) ?? PALETTE[0];

  // FEAT-1972079876 scroll-reset: the type list for each family shares one
  // scroll container. Without this, scrolling down inside (say) Education then
  // switching to Landmarks left the shorter list scrolled off-screen, looking
  // empty. Resetting scrollTop to 0 on every family change guarantees the new
  // family's first entries are visible immediately.
  const detailRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (detailRef.current) detailRef.current.scrollTop = 0;
  }, [fam]);

  // FEAT-1972079860 AC-1: Sort items available-first, locked by unlock level, then placeholders.
  const sortedItems = sortPaletteItems(state, famDef.items);

  return (
    <>
      <div className="tree-wrap">
        <div className="tree-fams">
          {PALETTE.map((g) => {
            const color = SPECS[g.items[0]]?.color ?? '#888';
            const hasActive = g.items.some((id) => state.tool.mode === 'build' && state.tool.spec === id);
            return (
              <button
                key={g.title}
                className={`tree-fam${fam === g.title ? ' open' : ''}${hasActive ? ' has-active' : ''}`}
                onMouseEnter={() => setFam(g.title)}
                onFocus={() => setFam(g.title)}
                onClick={() => setFam(g.title)}
              >
                <span className="swatch big" style={{ background: color }} />
                <span className="tree-title">{g.title}</span>
                <span className="tree-n mono">{g.items.length}</span>
              </button>
            );
          })}
        </div>
        <div className="tree-detail" ref={detailRef}>
          {sortedItems.map((id) => {
            const sp = SPECS[id];
            if (!sp) {
              // a palette id missing from SPECS must degrade to a skipped entry,
              // never a render-time throw that unmounts the whole app
              console.error(`palette id "${id}" has no SPECS entry — skipped`);
              return null;
            }
            // FEAT-1972079877: a placeholder ("coming soon" roadmap type) is NEVER
            // placeable — greyed-out, desaturated, disabled, and clicking does
            // nothing. isPlaceable() is the single gate (placeholder-aware); the
            // per-spec `locked` flag still drives the real specs' unlock badge.
            const isPh = sp.placeholder === true;
            const locked = !specUnlocked(state, sp);
            const active = state.tool.mode === 'build' && state.tool.spec === id;
            return (
              <button
                key={id}
                className={`pal-item${active ? ' active' : ''}${!isPh && locked ? ' locked' : ''}${isPh ? ' placeholder' : ''}`}
                disabled={isPh || (!locked && state.funds < placementCost(sp))}
                aria-disabled={isPh || undefined}
                aria-label={!isPh && locked ? `Locked — unlocks at city level ${sp.unlock}` : undefined}
                title={
                  isPh
                    ? `${sp.name} — coming soon (planned): ${sp.blurb}`
                    : locked
                      ? `${sp.name} — unlocks at city level ${sp.unlock}. Click for requirements & what it delivers.`
                      : `${sp.name} — ${sp.blurb}, upkeep ${fmtMoney(sp.upkeep)}/tick${
                          isFreeZone(sp) ? ` · free to zone · ${constructionTicks(sp)} ticks to build` : ''
                        }`
                }
                onClick={() => {
                  // FEAT-1972079860 AC-4: Click locked spec opens card.
                  if (locked && !isPh) {
                    setOpenSpecId(id);
                    return;
                  }
                  // A placeholder can never be selected/placed (defence in depth
                  // alongside disabled): clicking is a no-op.
                  if (isPh || !isPlaceable(state, sp)) return;
                  dispatch({ type: 'tool', tool: { mode: 'build', spec: id } });
                }}
              >
                <span className="swatch big" style={{ background: sp.color }} />
                <span className="pal-text">
                  <span className="pal-name">{sp.name}</span>
                  <span className="pal-cap">{sp.blurb}</span>
                </span>
                <span className="pal-cost">
                  {isPh ? (
                    <span className="chip amber">Soon</span>
                  ) : locked ? (
                    `Lv ${sp.unlock}`
                  ) : isFreeZone(sp) ? (
                    `Free · ${constructionTicks(sp)}t · ${sp.w}×${sp.h}`
                  ) : (
                    `${fmtMoney(sp.cost)} · ${sp.w}×${sp.h}`
                  )}
                </span>
              </button>
            );
          })}
        </div>
      </div>
      <p className="hint">
        {state.tool.mode === 'build' && state.tool.spec
          ? `Placing ${SPECS[state.tool.spec].name} — click a tile or drag to paint. Esc to let go.`
          : 'Hover a category to browse, pick a structure, then click the map (drag paints). Keys 1–9 quick-pick.'}
      </p>

      {/* FEAT-1972079860 AC-4/AC-5: Spec card modal for locked items */}
      {openSpecId && <SpecCard spec={SPECS[openSpecId] ?? null} onClose={() => setOpenSpecId(null)} />}
    </>
  );
}

function MoveTab() {
  const { state, dispatch } = useSim();
  const modes: { mode: ToolMode; label: string; hint: string }[] = [
    { mode: 'select', label: 'Select', hint: 'Click any structure to inspect it.' },
    { mode: 'move', label: 'Move', hint: `Click a structure, then an empty spot (${fmtMoney(25)} per relocation).` },
    { mode: 'bulldoze', label: 'Bulldoze', hint: 'Click a structure to demolish it for a 25% refund. Drag to clear a row.' },
  ];
  const activeMode = state.movingId != null ? 'move' : state.tool.mode;
  const hint =
    state.movingId != null
      ? `Relocating #${state.movingId} — click a destination tile.`
      : modes.find((m) => m.mode === state.tool.mode)?.hint ?? '';
  return (
    <>
      <div className="palette">
        {modes.map((m) => (
          <button
            key={m.mode}
            className={`pal-item${activeMode === m.mode ? ' active' : ''}`}
            onClick={() => {
              if (activeMode === 'move' && state.movingId != null) dispatch({ type: 'cancelMove' });
              else dispatch({ type: 'tool', tool: { mode: m.mode } });
            }}
          >
            <span className="pal-name">{m.label}</span>
          </button>
        ))}
      </div>
      <p className="hint">{hint}</p>
    </>
  );
}
