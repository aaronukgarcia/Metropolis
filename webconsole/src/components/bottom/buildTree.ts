// buildTree.ts — FEAT-9995385591 (UX-1 of FEAT-2326609843): pure model for the
// tree-nodal build browser that replaces BuildTab's two-pane hover list.
//
// This module holds NO rules of its own (GR#3): the domain/family/spec grouping
// comes from data.ts (paletteByDomain, PALETTE order, SPECS), and availability
// (locked / placeholder / at-cap / affordable) stays in BottomBar via
// isPlaceable / specUnlocked / remainingAllowance. All state here is UI state —
// expanded nodes, search text, recent picks — and never reaches SimState.
import { PALETTE, SPECS, paletteByDomain } from '../../sim/data';
import type { Spec } from '../../sim/data';

export type TreeKind = 'recent' | 'domain' | 'family' | 'spec';

export interface TreeNode {
  /** Unique id: r:recent | d:<domain> | f:<family> | s:<specId> (s:recent:<specId> under Recent). */
  id: string;
  kind: TreeKind;
  label: string;
  /** 1-based ARIA level. */
  level: number;
  parentId: string | null;
  /** Spec id for kind === 'spec'. */
  specId?: string;
  /** Child count shown as the badge for parents. */
  count?: number;
  /** Effective expansion (search forces everything with matches open). */
  expanded: boolean;
  children: TreeNode[];
}

/** How many recently picked specs the Recent pseudo-family keeps (AC-1.5). */
export const RECENT_MAX = 6;

/** Initial expansion: the family PALETTE[0] (roads) inside its domain, nothing else (AC-1.1). */
export function initialExpanded(): string[] {
  // 'r:recent' = the Recent pseudo-family starts open (it only renders once non-empty).
  const first = PALETTE[0]?.title;
  if (!first) return ['r:recent'];
  const dom = paletteByDomain().find((d) => d.families.some((f) => f.title === first));
  return dom ? ['r:recent', `d:${dom.domain}`, `f:${first}`] : ['r:recent', `f:${first}`];
}

/** Push a spec id to the front of the recent list: de-duplicated, capped, pure. */
export function pushRecent(recent: readonly string[], specId: string): string[] {
  if (recent[0] === specId) return recent as string[];
  return [specId, ...recent.filter((r) => r !== specId)].slice(0, RECENT_MAX);
}

function specMatches(sp: Spec, q: string): boolean {
  return sp.name.toLowerCase().includes(q) || sp.blurb.toLowerCase().includes(q);
}

/**
 * Build the visible tree. `query` filters live across every level on name +
 * blurb (AC-1.4); a family/domain whose own title matches keeps its whole
 * subtree. While searching every surviving node is expanded and Recent hides.
 * Spec order inside a family is PALETTE order, never availability order (AC-1.3).
 */
export function buildTree(expanded: ReadonlySet<string>, query: string, recent: readonly string[]): TreeNode[] {
  const q = query.trim().toLowerCase();
  const searching = q.length > 0;
  const out: TreeNode[] = [];

  const specNode = (id: string, parentId: string, level: number, prefix: string): TreeNode => ({
    id: `${prefix}${id}`,
    kind: 'spec',
    label: SPECS[id]?.name ?? id,
    level,
    parentId,
    specId: id,
    expanded: false,
    children: [],
  });

  if (!searching) {
    const recentIds = recent.filter((id) => SPECS[id]);
    if (recentIds.length > 0) {
      const open = expanded.has('r:recent');
      out.push({
        id: 'r:recent',
        kind: 'recent',
        label: 'Recent',
        level: 1,
        parentId: null,
        count: recentIds.length,
        expanded: open,
        children: open ? recentIds.map((id) => specNode(id, 'r:recent', 2, 's:recent:')) : [],
      });
    }
  }

  for (const { domain, families } of paletteByDomain()) {
    const domMatch = searching && domain.toLowerCase().includes(q);
    const famNodes: TreeNode[] = [];
    for (const fam of families) {
      const famMatch = domMatch || (searching && fam.title.toLowerCase().includes(q));
      const ids = fam.items.filter((id) => SPECS[id] && (!searching || famMatch || specMatches(SPECS[id], q)));
      if (searching && ids.length === 0) continue;
      const fid = `f:${fam.title}`;
      const open = searching || expanded.has(fid);
      famNodes.push({
        id: fid,
        kind: 'family',
        label: fam.title,
        level: 2,
        parentId: `d:${domain}`,
        count: ids.length,
        expanded: open,
        children: open ? ids.map((id) => specNode(id, fid, 3, 's:')) : [],
      });
    }
    if (searching && famNodes.length === 0) continue;
    const did = `d:${domain}`;
    const dopen = searching || expanded.has(did);
    out.push({
      id: did,
      kind: 'domain',
      label: domain,
      level: 1,
      parentId: null,
      count: famNodes.reduce((n, f) => n + (f.count ?? 0), 0),
      expanded: dopen,
      children: dopen ? famNodes : [],
    });
  }
  return out;
}

/** Depth-first list of the rows actually visible (collapsed subtrees are empty). */
export function flattenVisible(nodes: readonly TreeNode[]): TreeNode[] {
  const rows: TreeNode[] = [];
  const walk = (list: readonly TreeNode[]) => {
    for (const n of list) {
      rows.push(n);
      if (n.expanded) walk(n.children);
    }
  };
  walk(nodes);
  return rows;
}

export interface NavResult {
  focus: string | null;
  expand?: string;
  collapse?: string;
}

/**
 * WAI-ARIA tree keyboard model (pure): Down/Up move, Home/End jump, Right
 * expands or enters, Left collapses or goes to the parent. Enter/Space are
 * handled by the component (they pick or toggle).
 */
export function navigate(rows: readonly TreeNode[], focusedId: string | null, key: string): NavResult {
  if (rows.length === 0) return { focus: null };
  const idx = rows.findIndex((r) => r.id === focusedId);
  const cur = idx >= 0 ? rows[idx] : null;
  switch (key) {
    case 'ArrowDown':
      return { focus: rows[Math.min(rows.length - 1, idx + 1)].id };
    case 'ArrowUp':
      return { focus: rows[Math.max(0, idx - 1)].id };
    case 'Home':
      return { focus: rows[0].id };
    case 'End':
      return { focus: rows[rows.length - 1].id };
    case 'ArrowRight':
      if (!cur || cur.kind === 'spec') return { focus: focusedId };
      if (!cur.expanded) return { focus: cur.id, expand: cur.id };
      return { focus: cur.children[0]?.id ?? cur.id };
    case 'ArrowLeft':
      if (!cur) return { focus: focusedId };
      if (cur.kind !== 'spec' && cur.expanded) return { focus: cur.id, collapse: cur.id };
      return { focus: cur.parentId ?? cur.id };
    default:
      return { focus: focusedId };
  }
}

// ---- per-city cap copy (BUG-1185, BottomBar half): derived from the spec's maxPerCity ----

export function capChip(sp: Spec): string {
  const n = sp.maxPerCity ?? 1;
  return n === 1 ? 'One per city' : `${n} per city`;
}

export function capAria(sp: Spec): string {
  const n = sp.maxPerCity ?? 1;
  return n === 1 ? `One per city — ${sp.name} already built` : `${n} per city — all ${n} ${sp.name} built`;
}

export function capTitle(sp: Spec): string {
  const n = sp.maxPerCity ?? 1;
  return n === 1
    ? `${sp.name} — one per city, and this city already has one. Bulldoze the existing one to build another.`
    : `${sp.name} — limited to ${n} per city, and this city already has ${n}. Bulldoze one to build another.`;
}
