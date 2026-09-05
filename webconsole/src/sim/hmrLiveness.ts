// hmrLiveness.ts — BUG-564 rework: track whether Vite's HMR websocket is
// actually connected. Dev-only; a production build has no HMR at all.
//
// Why this exists: staleBuildGuard.ts's old checkStaleBuild-only path
// misfired throughout active dev because the frozen build-time sha always
// falls behind live git HEAD after the first commit of a session — even
// though HMR was, in fact, keeping the running module graph current. The
// genuine "dead server" signature this guard is meant to catch is not "sha
// differs", it's "HMR has stopped delivering patches" — i.e. the dev-server
// websocket connection is down. Vite's client fires the documented custom HMR
// events 'vite:ws:disconnect' / 'vite:ws:connect' on import.meta.hot exactly
// for this (see https://vite.dev/guide/api-hmr — "Custom Events").
//
// Kept dependency-free (no React) so it's testable in isolation from the
// component; see test/stale-build-guard.test.tsx.

type Listener = (connected: boolean) => void;

/** True only under a Vite dev server that has HMR wired up at all. */
export function hasHmr(): boolean {
  try {
    return typeof import.meta !== 'undefined' && !!(import.meta as any).hot;
  } catch {
    // Some non-Vite consumers (plain node --test import of this module)
    // don't even have import.meta defined the way Vite's transform expects —
    // treat that as "no HMR", never throw.
    return false;
  }
}

/**
 * Subscribe to HMR websocket connect/disconnect. Reports the current
 * best-known state immediately (true — "connected" — whenever HMR exists at
 * all, since import.meta.hot is only present once the client module has
 * loaded, which means it already has a live connection), then again on every
 * subsequent connect/disconnect. When HMR doesn't exist (production build,
 * or a non-Vite runtime), reports connected=true once and never again —
 * callers must gate on `hasHmr()` / `isDevServer` separately so this signal
 * is never actually consulted outside dev.
 *
 * Returns an unsubscribe function.
 */
export function subscribeHmrConnection(listener: Listener): () => void {
  const hot = hasHmr() ? (import.meta as any).hot : null;
  if (!hot) {
    listener(true);
    return () => {};
  }
  listener(true);
  const onDisconnect = () => listener(false);
  const onConnect = () => listener(true);
  hot.on('vite:ws:disconnect', onDisconnect);
  hot.on('vite:ws:connect', onConnect);
  return () => {
    // hot.off exists on Vite >=5.1's HMR client; guard defensively for older
    // clients rather than throwing on unmount.
    if (typeof hot.off === 'function') {
      hot.off('vite:ws:disconnect', onDisconnect);
      hot.off('vite:ws:connect', onConnect);
    }
  };
}
