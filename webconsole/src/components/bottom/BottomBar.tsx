import { useEffect, useRef, useState } from 'react';
import { PALETTE, SPECS } from '../../sim/data';
import type { ToolMode } from '../../sim/types';
import { useSim, levelOf } from '../../sim/store';
import { fmtMoney } from '../../sim/utils';
import { Panel } from '../Tabs';

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
  const level = levelOf(state.xp);
  const [fam, setFam] = useState(PALETTE[0].title);
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
          {famDef.items.map((id) => {
            const sp = SPECS[id];
            if (!sp) {
              // a palette id missing from SPECS must degrade to a skipped entry,
              // never a render-time throw that unmounts the whole app
              console.error(`palette id "${id}" has no SPECS entry — skipped`);
              return null;
            }
            const locked = sp.unlock > level;
            const active = state.tool.mode === 'build' && state.tool.spec === id;
            return (
              <button
                key={id}
                className={`pal-item${active ? ' active' : ''}${locked ? ' locked' : ''}`}
                disabled={locked || state.funds < sp.cost}
                title={
                  locked
                    ? `${sp.name} — unlocks at city level ${sp.unlock}`
                    : `${sp.name} — ${sp.blurb}, upkeep ${fmtMoney(sp.upkeep)}/tick`
                }
                onClick={() => dispatch({ type: 'tool', tool: { mode: 'build', spec: id } })}
              >
                <span className="swatch big" style={{ background: sp.color }} />
                <span className="pal-text">
                  <span className="pal-name">{sp.name}</span>
                  <span className="pal-cap">{sp.blurb}</span>
                </span>
                <span className="pal-cost">
                  {locked ? `Lv ${sp.unlock}` : `${fmtMoney(sp.cost)} · ${sp.w}×${sp.h}`}
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
