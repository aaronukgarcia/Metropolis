// notificationSlot.tsx — BUG-1160: the react-dom-FREE half of the notification
// manager. Components that load on the provider-free path (store.tsx imports
// LineageRecoveryBanner, and DOM-less test children load store.tsx) must never
// pull react-dom in at module load, because react-dom touches `document`.
// So the context + <NotificationSlot> live here with NO react-dom import; the
// provider (NotificationHost.tsx) imports react-dom and injects its
// createPortal through the context. Without a provider a slot renders its
// children inline (isolated tests / SSR).

import { createContext, useContext, useId, useLayoutEffect, type ReactNode } from 'react';
import type { NotificationKind } from './notificationLayout';

export interface NotificationCtx {
  stackEl: HTMLElement | null;
  /** BUG-1160 r4: the viewport-fixed MODAL HOST dialog slots portal into. */
  modalEl: HTMLElement | null;
  setModalEl: (el: HTMLElement | null) => void;
  /** Registers a slot under a unique key; returns the unregister function. */
  register: (key: string, kind: NotificationKind) => () => void;
  slotOf: (key: string) => number;
  /** react-dom's createPortal, injected by the provider (see header). */
  portal: (node: ReactNode, el: HTMLElement) => ReactNode;
  /** True while a dialog slot is registered: every other slot is inert. */
  hasDialog: boolean;
  /** Ref callback for the stack element (used by <NotificationStack>). */
  setStackEl: (el: HTMLElement | null) => void;
}

export const NotificationContext = createContext<NotificationCtx | null>(null);

export function NotificationSlot({
  id,
  kind,
  children,
}: {
  id: string;
  kind: NotificationKind;
  children: ReactNode;
}) {
  const ctx = useContext(NotificationContext);
  // BUG-1160 F5: two owners may share an `id` (MapView and DemandDock both
  // mount the placement confirm); the registry key is id + a per-instance
  // useId so they are distinct slots, never one replacing the other.
  const key = `${id}#${useId()}`;
  const register = ctx?.register;
  useLayoutEffect(() => (register ? register(key, kind) : undefined), [register, key, kind]);
  // r4 H1: dialogs are modals, not notifications - they render in the
  // viewport-fixed modal host, never in the map-anchored stack.
  const target = ctx ? (kind === 'dialog' ? ctx.modalEl : ctx.stackEl) : null;
  if (!ctx || !target) return <>{children}</>;
  const slot = ctx.slotOf(key);
  // BUG-1160 r3 G2: while a dialog is open every non-dialog slot and ALL its
  // descendants are inert (not focusable, not clickable), via the inert
  // attribute - a child's own pointer-events can never beat it.
  const inertProps = ctx.hasDialog && kind !== 'dialog' ? ({ inert: '' } as Record<string, string>) : {};
  return (
    <>
      {ctx.portal(
        <div
          className={`notif-slot notif-${kind}`}
          data-notif-id={id}
          data-notif-kind={kind}
          data-notif-slot={slot}
          style={{ order: slot }}
          {...inertProps}
        >
          {children}
        </div>,
        target
      )}
    </>
  );
}

/** True when a NotificationProvider is mounted above the caller. */
export function useHasNotificationHost(): boolean {
  return useContext(NotificationContext) !== null;
}

/**
 * BUG-1160 r4 H1: the MODAL HOST - position:fixed, viewport-centred, at
 * Z_INDEX.NOTIF_MODAL_HOST (above the stack, below every blocking modal).
 * Rendered by the provider; dialog slots portal into it.
 */
export function NotificationModalHost() {
  const ctx = useContext(NotificationContext);
  if (!ctx) return null;
  return <div className="notif-modal-host" data-notif-modal-host ref={ctx.setModalEl} />;
}

/**
 * BUG-1160 r3 G1: the stack element. Rendered INSIDE the map element
 * (.map-wrap, position:relative) so the column is anchored to the map's
 * top-left and bounded by the map column - it can never reach the build tree
 * (left column) or the DemandDock (right column). The provider, when
 * `anchored`, does not render a stack of its own.
 */
export function NotificationStack() {
  const ctx = useContext(NotificationContext);
  if (!ctx) return null;
  return (
    <div
      className={`notif-stack${ctx.hasDialog ? ' has-dialog' : ''}`}
      data-notif-stack
      data-notif-anchored
      ref={ctx.setStackEl}
    />
  );
}
