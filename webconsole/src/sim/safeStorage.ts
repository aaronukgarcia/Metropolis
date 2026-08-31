// safeStorage.ts — BUG-457: a single shared QuotaExceeded-safe localStorage.setItem
// wrapper so no write site in the app lets a raw DOMException escape uncaught.
// GR#1/#17: a quota failure is a real, detectable condition the app must report
// through the error registry, not swallow silently — this module only classifies
// the failure; callers decide how to react (retry smaller, report, block a wipe,
// evict, etc).

/** Result of a guarded storage.setItem attempt. */
export interface SafeSetResult {
  ok: boolean;
  /** true when the failure was specifically a storage-quota condition. */
  quota: boolean;
  /** The caught error's message, when ok is false. */
  error?: string;
}

/** The minimal shape safeSetItem needs — injectable for tests. */
export interface SetItemStorage {
  setItem(key: string, value: string): void;
}

/**
 * Detect whether a caught error represents a QuotaExceeded condition across
 * browsers/engines: the modern DOMException('QuotaExceededError') (code 22),
 * legacy Firefox NS_ERROR_DOM_QUOTA_REACHED (code 1014), or a plain Error whose
 * name/message says so (test mocks, older engines, Node harnesses with no
 * DOMException global).
 */
export function isQuotaError(e: unknown): boolean {
  if (typeof DOMException !== 'undefined' && e instanceof DOMException) {
    if (e.name === 'QuotaExceededError' || e.name === 'NS_ERROR_DOM_QUOTA_REACHED') return true;
    if (e.code === 22 || e.code === 1014) return true;
  }
  if (e && typeof e === 'object') {
    const anyE = e as { name?: unknown; code?: unknown; message?: unknown };
    if (anyE.name === 'QuotaExceededError' || anyE.name === 'NS_ERROR_DOM_QUOTA_REACHED') return true;
    if (anyE.code === 22 || anyE.code === 1014) return true;
    if (typeof anyE.message === 'string' && /quota/i.test(anyE.message)) return true;
  }
  return false;
}

/**
 * Wrap storage.setItem so a thrown QuotaExceeded (or any other setItem error)
 * degrades to a typed result instead of an uncaught exception propagating to
 * the caller. This is the single shared write path BUG-457 routes every
 * localStorage write through (journal, savepoint, named save, recentOpened,
 * pre-wipe archive) so quota handling lives in one place, tested once.
 */
export function safeSetItem(
  storage: SetItemStorage | null | undefined,
  key: string,
  value: string,
): SafeSetResult {
  if (!storage) return { ok: false, quota: false, error: 'no storage' };
  try {
    storage.setItem(key, value);
    return { ok: true, quota: false };
  } catch (e) {
    return { ok: false, quota: isQuotaError(e), error: e instanceof Error ? e.message : String(e) };
  }
}
