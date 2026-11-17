import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { SPECS, placementCost, isFreeZone, constructionTicks, isPlaceable, remainingAllowance, canEnterSim } from '../../sim/data';
import type { ToolMode } from '../../sim/types';
import { useSim } from '../../sim/simContext';
import { specUnlocked } from '../../sim/engine';
import { fmtMoney } from '../../sim/utils';
import { Panel } from '../Tabs';
import { SpecCard } from '../SpecCard';
import { buildTree, flattenVisible, navigate, initialExpanded, pushRecent, capChip, capAria, capTitle } from './buildTree';
import type { TreeNode } from './buildTree';

const TABS = [
  { id: 'build', label: 'Build' },
  { id: 'move', label: 'Move' },
];

export function BottomBar() {
  const [tab, setTab] = useState('build');
  const { state } = useSim();
  // FEAT-9995385591 AC-1.1/AC-1.5: tree expansion and the Recent list live HERE
  // (BottomBar stays mounted across Build/Move tab switches) so they survive a
  // tab switch. UI session state only - never SimState.
  const [expandedList, setExpandedList] = useState<string[]>(initialExpanded);
  const [recent, setRecent] = useState<string[]>([]);
  // AC-1.5: Recent = last N PLACED. A placement appends to state.buildings; when
  // the active build tool's spec shows up in the newly appended tail, record it.
  // Picks that are cancelled (or keys 1-9 that never place) record nothing.
  // Keyed on buildings.length so it costs nothing on ticks that only mutate fields.
  const toolSpec = state.tool.mode === 'build' ? state.tool.spec : undefined;
  const buildingCount = state.buildings.length;
  const prevCount = useRef(buildingCount);
  useEffect(() => {
    const from = prevCount.current;
    prevCount.current = buildingCount;
    if (!toolSpec || buildingCount <= from) return;
    const placed = state.buildings.slice(from).some((b) => b.spec === toolSpec);
    if (placed) setRecent((r) => pushRecent(r, toolSpec));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [buildingCount]);
  return (
    <Panel title="Tools" tabs={TABS} active={tab} onSelect={setTab}>
      {tab === 'build' ? (
        <BuildTab expandedList={expandedList} setExpandedList={setExpandedList} recent={recent} />
      ) : (
        <MoveTab />
      )}
    </Panel>
  );
}

function BuildTab({
  expandedList,
  setExpandedList,
  recent,
}: {
  expandedList: string[];
  setExpandedList: (fn: (prev: string[]) => string[]) => void;
  recent: string[];
}) {
  const { state, dispatch } = useSim();
  const [openSpecId, setOpenSpecId] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [focusedId, setFocusedId] = useState<string | null>(null);
  const pendingFocus = useRef(false);
  const treeRef = useRef<HTMLDivElement>(null);

  const expanded = useMemo(() => new Set(expandedList), [expandedList]);
  const tree = useMemo(() => buildTree(expanded, query, recent), [expanded, query, recent]);
  const rows = useMemo(() => flattenVisible(tree), [tree]);
  // Roving tabindex: exactly one row is tabbable; fall back to the first row.
  const tabbableId = rows.some((r) => r.id === focusedId) ? focusedId : (rows[0]?.id ?? null);

  useEffect(() => {
    if (!pendingFocus.current || !focusedId) return;
    pendingFocus.current = false;
    const el = treeRef.current?.querySelector<HTMLElement>(`[data-nodeid="${focusedId}"]`);
    el?.focus();
  }, [focusedId, rows]);

  const setOpen = (id: string, open: boolean) =>
    setExpandedList((prev) => {
      const has = prev.includes(id);
      if (open && !has) return [...prev, id];
      if (!open && has) return prev.filter((x) => x !== id);
      return prev;
    });

  const pickSpec = (id: string) => {
    const sp = SPECS[id];
    if (!sp) return;
    const isPh = sp.placeholder === true;
    const locked = !specUnlocked(state, sp);
    // FEAT-1972079860 AC-4: clicking a locked spec opens its card.
    if (locked && !isPh) {
      setOpenSpecId(id);
      return;
    }
    // Placeholder / at-cap / unaffordable: no-op (isPlaceable folds in the cap).
    if (isPh || !isPlaceable(state, sp) || state.funds < placementCost(sp)) return;
    dispatch({ type: 'tool', tool: { mode: 'build', spec: id } });
  };

  const activate = (n: TreeNode) => {
    if (n.kind === 'spec' && n.specId) pickSpec(n.specId);
    else setOpen(n.id, !n.expanded);
  };

  const onTreeKeyDown = (e: React.KeyboardEvent) => {
    const key = e.key;
    if (key === 'Enter') {
      const targetId = (e.target as HTMLElement).closest?.('[data-nodeid]')?.getAttribute('data-nodeid') ?? tabbableId;
      const row = rows.find((r) => r.id === targetId);
      if (row) {
        e.preventDefault(); // keeps the button's native Enter-click from double-firing
        e.stopPropagation(); // consumed here: the window keydown (MapView/global shortcuts) must not also see it
        activate(row);
      }
      return;
    }
    if (!['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(key)) return;
    e.preventDefault();
    // Consumed by the tree: stop it reaching MapView's window keydown (arrows pan, Home resets the camera).
    // Keys NOT in the list above return early without stopping, so global shortcuts still work.
    e.stopPropagation();
    const fromId = (e.target as HTMLElement).closest?.('[data-nodeid]')?.getAttribute('data-nodeid') ?? tabbableId;
    const res = navigate(rows, fromId, key);
    if (res.expand) setOpen(res.expand, true);
    if (res.collapse) setOpen(res.collapse, false);
    if (res.focus) {
      pendingFocus.current = true;
      setFocusedId(res.focus);
    }
  };

  const renderSpec = (n: TreeNode) => {
    const id = n.specId!;
    const sp = SPECS[id];
    if (!sp) {
      // a palette id missing from SPECS must degrade to a skipped entry,
      // never a render-time throw that unmounts the whole app
      console.error(`palette id "${id}" has no SPECS entry — skipped`);
      return null;
    }
    // FEAT-1972079877: a placeholder is NEVER placeable; isPlaceable() is the gate.
    const isPh = sp.placeholder === true;
    const locked = !specUnlocked(state, sp);
    // FEAT-2326609761 AC-30: unlocked and real, but the maxPerCity cap is exhausted.
    const atCap = !isPh && !locked && canEnterSim(sp) && remainingAllowance(state, sp) <= 0;
    const broke = !isPh && !locked && !atCap && state.funds < placementCost(sp);
    const active = state.tool.mode === 'build' && state.tool.spec === id;
    return (
      <button
        key={n.id}
        role="treeitem"
        data-nodeid={n.id}
        aria-level={n.level}
        aria-selected={active}
        tabIndex={tabbableId === n.id ? 0 : -1}
        className={`pal-item bt-spec${active ? ' active' : ''}${!isPh && locked ? ' locked' : ''}${isPh ? ' placeholder' : ''}${atCap ? ' atcap' : ''}${broke ? ' broke' : ''}`}
        // aria-disabled (not the disabled attribute) so a keyboard user can still
        // arrow onto the row and read why it is unavailable; pickSpec refuses it.
        aria-disabled={isPh || atCap || broke || undefined}
        aria-label={atCap ? capAria(sp) : !isPh && locked ? `Locked — unlocks at city level ${sp.unlock}` : undefined}
        title={
          isPh
            ? `${sp.name} — coming soon (planned): ${sp.blurb}`
            : atCap
              ? capTitle(sp)
              : locked
                ? `${sp.name} — unlocks at city level ${sp.unlock}. Click for requirements & what it delivers.`
                : `${sp.name} — ${sp.blurb}, upkeep ${fmtMoney(sp.upkeep)}/tick${
                    isFreeZone(sp) ? ` · free to zone · ${constructionTicks(sp)} ticks to build` : ''
                  }`
        }
        onFocus={() => setFocusedId(n.id)}
        onClick={() => pickSpec(id)}
      >
        <span className="swatch big" style={{ background: sp.color }} />
        <span className="pal-text">
          <span className="pal-name">{sp.name}</span>
          <span className="pal-cap">{sp.blurb}</span>
        </span>
        <span className="pal-cost">
          {isPh ? (
            <span className="chip amber">Soon</span>
          ) : atCap ? (
            <span className="chip cap">{capChip(sp)}</span>
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
  };

  const renderNode = (n: TreeNode): ReactNode => {
    if (n.kind === 'spec') return renderSpec(n);
    const firstSpec = n.kind === 'family' ? SPECS[n.children[0]?.specId ?? ''] : undefined;
    const hasActive =
      state.tool.mode === 'build' &&
      n.children.some((c) => c.specId === state.tool.spec || c.children.some((g) => g.specId === state.tool.spec));
    return (
      <div className={`bt-node bt-${n.kind}`} key={n.id}>
        <button
          role="treeitem"
          data-nodeid={n.id}
          aria-level={n.level}
          aria-expanded={n.expanded}
          tabIndex={tabbableId === n.id ? 0 : -1}
          className={`bt-head tree-${n.kind === 'family' ? 'fam' : 'dom'}${n.expanded ? ' open' : ''}${hasActive ? ' has-active' : ''}`}
          onFocus={() => setFocusedId(n.id)}
          onClick={() => activate(n)}
        >
          <span className="bt-caret" aria-hidden="true">
            {n.expanded ? '▾' : '▸'}
          </span>
          {firstSpec && <span className="swatch big" style={{ background: firstSpec.color }} />}
          <span className="tree-title">{n.label}</span>
          <span className="tree-n mono">{n.count}</span>
        </button>
        {n.expanded && n.children.length > 0 && (
          <div role="group" className="bt-group">
            {n.children.map(renderNode)}
          </div>
        )}
      </div>
    );
  };

  return (
    <>
      <div className="bt-wrap">
        <div className="bt-search">
          <input
            type="search"
            className="bt-input"
            placeholder="Search buildings…"
            aria-label="Search buildings"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape' && query) {
                // AC-1.4: Esc clears the filter; do not also drop the active tool.
                e.stopPropagation();
                e.nativeEvent.stopImmediatePropagation?.();
                setQuery('');
              } else if (e.key === 'ArrowDown' && rows[0]) {
                e.preventDefault();
                pendingFocus.current = true;
                setFocusedId(rows[0].id);
              }
            }}
          />
        </div>
        <div className="bt-tree" role="tree" aria-label="Build menu" ref={treeRef} onKeyDown={onTreeKeyDown}>
          {tree.length === 0 ? <p className="hint">No buildings match “{query}”.</p> : tree.map(renderNode)}
        </div>
      </div>
      {/* BUG-1162: the map's tool chip already says "Placing: X"; the hint no longer repeats it. */}
      <p className="hint">Click a category to open it, pick a building, then click the map (drag paints). Keys 1–9 quick-pick.</p>

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
    { mode: 'clone', label: 'Clone', hint: 'Drag to select a region, then click elsewhere to stamp a copy (hotkey: C).' },
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
