// notificationLayout.ts — BUG-1160: the pure half of the ONE notification
// layout manager (UI state only — never SimState, never journaled).
//
// Aaron's screenshot (2026-10-31) showed the placement-confirm dialog and the
// Fix All toast stacked on the same rectangle. Root cause: every transient
// overlay positioned itself independently (position:fixed/absolute with its
// own top/left/transform), so two of them could always land on the same
// pixels. The cure is structural: every transient overlay occupies a SLOT in
// ONE flow column (NotificationHost.tsx); a slot is a flex child, so two
// slots can never share a rectangle. This module decides the ORDER of the
// slots — deterministic (GR#21: no wall-clock, no Math.random, no map
// iteration order) and critical-first, so a critical alert is always the top
// slot and is never pushed out of view by later, lesser notifications.

export type NotificationKind = 'critical' | 'dialog' | 'banner' | 'toast';

/** Lower rank = nearer the top of the column. Critical alerts always lead. */
export const NOTIFICATION_KIND_RANK: Readonly<Record<NotificationKind, number>> = {
  critical: 0,
  dialog: 1,
  banner: 2,
  toast: 3,
};

export interface NotificationRegistration {
  /** Stable unique id of the overlay (e.g. 'news-feed'). */
  id: string;
  kind: NotificationKind;
  /** Registration sequence number (monotonic, assigned by the host). */
  seq: number;
}

export interface NotificationSlotAssignment {
  id: string;
  kind: NotificationKind;
  /** 0-based position in the single column. Distinct for every overlay. */
  slot: number;
}

/** Deterministic order: kind rank, then registration order, then id. */
export function layoutNotifications(items: readonly NotificationRegistration[]): NotificationSlotAssignment[] {
  const sorted = [...items].sort((a, b) => {
    const r = NOTIFICATION_KIND_RANK[a.kind] - NOTIFICATION_KIND_RANK[b.kind];
    if (r !== 0) return r;
    if (a.seq !== b.seq) return a.seq - b.seq;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
  return sorted.map((it, slot) => ({ id: it.id, kind: it.kind, slot }));
}
