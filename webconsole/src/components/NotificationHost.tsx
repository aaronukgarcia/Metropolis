// NotificationHost.tsx — BUG-1160: the ONE anchoring/layout manager for every
// transient overlay (toasts, notices, the placement-confirm dialog, banners).
// UI state only — never SimState.
//
// <NotificationProvider> renders a single fixed FLOW column (.notif-stack),
// BELOW the top bar and at the registered Z_INDEX.NOTIF_STACK (under every
// blocking modal). An overlay wraps its content in <NotificationSlot>
// (notificationSlot.tsx, react-dom-free), which registers here and is portaled
// into the column as a flex child ordered by layoutNotifications(). Flex
// children cannot overlap, text wraps, and the column scrolls rather than
// clipping, so a critical alert (slot 0) is never hidden. While a dialog slot
// is open the stack gets .has-dialog: its own backdrop dims the map and every
// non-dialog slot is dimmed and non-interactive (the dialog stays modal).
//
// This file imports react-dom, so only the app root (App.tsx) and tests may
// import it; components import notificationSlot.tsx instead.

import { useCallback, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { layoutNotifications, type NotificationKind, type NotificationRegistration } from './notificationLayout';
import { NotificationContext, NotificationModalHost, type NotificationCtx } from './notificationSlot';

export { NotificationSlot, NotificationStack, NotificationModalHost, useHasNotificationHost } from './notificationSlot';

const portal = (node: ReactNode, el: HTMLElement): ReactNode => createPortal(node, el);

export function NotificationProvider({ children, anchored = false }: { children: ReactNode; anchored?: boolean }) {
  const [stackEl, setStackEl] = useState<HTMLElement | null>(null);
  const [modalEl, setModalEl] = useState<HTMLElement | null>(null);
  const [items, setItems] = useState<NotificationRegistration[]>([]);
  const seqRef = useRef(0);

  const register = useCallback((id: string, kind: NotificationKind) => {
    const seq = ++seqRef.current;
    setItems((prev) => [...prev.filter((p) => p.id !== id), { id, kind, seq }]);
    return () => setItems((prev) => prev.filter((p) => p.seq !== seq));
  }, []);

  const slots = useMemo(() => {
    const m = new Map<string, number>();
    for (const a of layoutNotifications(items)) m.set(a.id, a.slot);
    return m;
  }, [items]);

  const hasDialog = items.some((i) => i.kind === 'dialog');
  const value = useMemo<NotificationCtx>(
    () => ({ stackEl, modalEl, setModalEl, register, slotOf: (id) => slots.get(id) ?? 0, portal, hasDialog, setStackEl }),
    [stackEl, modalEl, register, slots, hasDialog]
  );

  return (
    <NotificationContext.Provider value={value}>
      {children}
      <NotificationModalHost />
      {!anchored && <div className={`notif-stack${hasDialog ? ' has-dialog' : ''}`} data-notif-stack ref={setStackEl} />}
    </NotificationContext.Provider>
  );
}
