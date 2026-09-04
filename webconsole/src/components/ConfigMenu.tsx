import { useEffect, useMemo, useState } from 'react';
import {
  getPrewipeCap,
  setPrewipeCap,
  localStorageUsage,
  fmtStorageBytes,
  TYPICAL_LOCALSTORAGE_QUOTA_BYTES,
} from '../sim/storageConfig';
import { PREWIPE_ARCHIVE_KEY } from '../sim/captureBeforeWipe';
import { FAST_BUILD_FLAG_KEY, FAST_BUILD_MAX_TICKS, isFastBuildEnabled, resetFastBuildFlagCache } from '../sim/debugBuildSpeed';
import { QUEUE_KEY } from '../sim/commitqueue';
import { NAMED_SAVES_INDEX_KEY, NAMED_SAVE_SLOT_PREFIX } from '../sim/namedsaves';
import { SAVEPOINT_KEY_PREFIX, SAVEPOINT_CAP } from '../sim/replay';
import { JOURNAL_KEY } from '../sim/journal';
import { encode, decode } from '../sim/saveCodec';
import { useSim } from '../sim/simContext';
import { CONSOLIDATOR_ENABLED_DEFAULT } from '../sim/engine';

/**
 * BUG-457: how many journal entries Reclaim keeps (rather than deleting the
 * key outright) — enough to still have SOME crash-recovery tail, small enough
 * to free most of the bytes. The old "Clear journal" button still nukes it
 * entirely if the player wants that.
 */
const RECLAIM_JOURNAL_KEEP_ENTRIES = 200;

function keyByteLength(storage: Storage, key: string): number {
  const v = storage.getItem(key);
  return v == null ? 0 : (key.length + v.length) * 2;
}

/**
 * Trim (not delete) the persisted journal down to its newest
 * RECLAIM_JOURNAL_KEEP_ENTRIES entries. A corrupt/unparseable journal is
 * dropped outright (same as before) since it cannot be trimmed safely.
 */
function reclaimJournal(storage: Storage): void {
  try {
    const raw = storage.getItem(JOURNAL_KEY);
    if (!raw) return;
    const parsed = JSON.parse(raw) as { entries?: unknown };
    if (Array.isArray(parsed.entries) && parsed.entries.length > RECLAIM_JOURNAL_KEEP_ENTRIES) {
      storage.setItem(
        JOURNAL_KEY,
        JSON.stringify({ entries: parsed.entries.slice(-RECLAIM_JOURNAL_KEEP_ENTRIES) }),
      );
    }
  } catch {
    storage.removeItem(JOURNAL_KEY);
  }
}

/**
 * Trim (not delete) the pre-wipe archive down to the CURRENT prewipe cap.
 * Writes are already capped going forward (captureBeforeWipe.ts slices on
 * every write); this only matters when the cap was lowered after entries were
 * already archived under a higher one, or the entry format is stale/oversized.
 */
function reclaimPrewipeArchive(storage: Storage): void {
  try {
    const raw = storage.getItem(PREWIPE_ARCHIVE_KEY);
    if (!raw) return;
    // FEAT-1972079935: the archive is stored compressed (encode/decode) — go
    // through decode() here too, or a compressed value looks like corrupt JSON
    // and this would wrongly nuke the whole archive instead of trimming it.
    // decode() is a no-op on a legacy uncompressed value, so this still works
    // for archives written before compression landed.
    const parsed = JSON.parse(decode(raw)) as unknown;
    if (!Array.isArray(parsed)) {
      storage.removeItem(PREWIPE_ARCHIVE_KEY);
      return;
    }
    const cap = getPrewipeCap(storage);
    if (parsed.length > cap) {
      storage.setItem(PREWIPE_ARCHIVE_KEY, encode(JSON.stringify(parsed.slice(-cap))));
    }
  } catch {
    storage.removeItem(PREWIPE_ARCHIVE_KEY);
  }
}

/**
 * BUG-457 / BUG-469: evict superseded autosave slots ONLY — slots beyond the
 * CURRENT SAVEPOINT_CAP, i.e. leftovers from an older, larger cap. Slots
 * `0..SAVEPOINT_CAP-1` are the live rotating autosave HISTORY (BUG-469) and
 * MUST survive Reclaim untouched, same as persistSavepoint's own legacy
 * cleanup sweep — a hardcoded "slot 1+" here would silently delete the
 * rotation history the moment SAVEPOINT_CAP was raised above 1.
 */
function reclaimSuperseededSavepoints(storage: Storage): void {
  for (let slot = SAVEPOINT_CAP; slot < 8; slot++) {
    try {
      storage.removeItem(`${SAVEPOINT_KEY_PREFIX}.${slot}`);
    } catch {
      /* ignore */
    }
  }
}

/** Runs every Reclaim step and returns the bytes actually freed (measured, not assumed). */
function runReclaim(storage: Storage): number {
  const keysTouched = [
    JOURNAL_KEY,
    PREWIPE_ARCHIVE_KEY,
    ...Array.from({ length: 7 }, (_, i) => `${SAVEPOINT_KEY_PREFIX}.${i + 1}`),
  ];
  const before = keysTouched.reduce((n, k) => n + keyByteLength(storage, k), 0);
  reclaimJournal(storage);
  reclaimPrewipeArchive(storage);
  reclaimSuperseededSavepoints(storage);
  const after = keysTouched.reduce((n, k) => n + keyByteLength(storage, k), 0);
  return Math.max(0, before - after);
}

export function ConfigMenu() {
  const { state, dispatch } = useSim();
  const [open, setOpen] = useState(false);
  const [cap, setCap] = useState(() => {
    try {
      return getPrewipeCap(window.localStorage);
    } catch {
      return 10;
    }
  });
  const [tick, setTick] = useState(0);
  // BUG-457: measured (not assumed) bytes freed by the last Reclaim run.
  const [reclaimMsg, setReclaimMsg] = useState<string | null>(null);
  // BUG-575: "Clear named cities" / "Clear autosave slots" permanently destroy
  // the player's saves and previously fired on the first click with no way
  // back. Gate them behind an explicit second click, mirroring the in-app
  // state-driven confirm idiom FileMenu already uses for a same-consequence
  // decision (BUG-445's save/rename collision confirm) rather than a blocking
  // window.confirm() native dialog.
  const [confirmingClear, setConfirmingClear] = useState<null | 'namedCities' | 'autosaves'>(null);

  const usage = useMemo(() => {
    try {
      return localStorageUsage(window.localStorage);
    } catch {
      return { bytes: 0, keys: [] };
    }
  }, [tick, open]);

  const pct = Math.min(100, (usage.bytes / TYPICAL_LOCALSTORAGE_QUOTA_BYTES) * 100);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closePanel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);

  // BUG-575: closing the panel (any route) disarms a pending confirm — never
  // leave a destructive action armed across a re-open.
  const closePanel = () => {
    setOpen(false);
    setConfirmingClear(null);
  };

  const clearPrefix = (prefix: string) => {
    const keys: string[] = [];
    for (let i = 0; i < window.localStorage.length; i++) {
      const k = window.localStorage.key(i);
      if (k && k.startsWith(prefix)) keys.push(k);
    }
    for (const k of keys) window.localStorage.removeItem(k);
    setTick((n) => n + 1);
  };

  // BUG-575: the actual destructive actions, only ever invoked after the
  // explicit second-click confirm below — never wired directly to the
  // buttons that arm them.
  const clearNamedCities = () => {
    clearPrefix(NAMED_SAVE_SLOT_PREFIX);
    window.localStorage.removeItem(NAMED_SAVES_INDEX_KEY);
    setConfirmingClear(null);
  };
  const clearAutosaveSlots = () => {
    clearPrefix(SAVEPOINT_KEY_PREFIX);
    setConfirmingClear(null);
  };

  return (
    <>
      <button className={`btn tiny${open ? ' active' : ''}`} onClick={() => setOpen(true)} title="Storage and archive settings">
        Config
      </button>
      {open && (
        <div className="about-backdrop" onClick={closePanel} role="presentation">
          <section
            className="panel about-panel"
            role="dialog"
            aria-modal="true"
            aria-label="Storage config"
            onClick={(e) => e.stopPropagation()}
          >
            <header className="panel-h">
              <span className="panel-title">Config — storage</span>
              <button className="btn tiny" onClick={closePanel}>
                Close
              </button>
            </header>
            <div className="panel-body about-body">
              <p className="muted">
                Browser localStorage is about {fmtStorageBytes(TYPICAL_LOCALSTORAGE_QUOTA_BYTES)} and cannot be raised.
                One autosave slot, two named cities, compact pre-wipe (no building list). Use Reclaim if usage is still high.
              </p>
              <div className="storage-meter" title={`${fmtStorageBytes(usage.bytes)} used`}>
                <div className="storage-meter-fill" style={{ width: `${pct}%` }} />
              </div>
              <div className="mono">
                {fmtStorageBytes(usage.bytes)} used of ~{fmtStorageBytes(TYPICAL_LOCALSTORAGE_QUOTA_BYTES)} ({pct.toFixed(0)}%)
              </div>
              <ul className="storage-keys">
                {usage.keys.slice(0, 12).map((k) => (
                  <li key={k.key}>
                    <code>{k.key}</code> {fmtStorageBytes(k.bytes)}
                  </li>
                ))}
              </ul>
              <label className="storage-cap">
                Archive cap
                <input
                  type="number"
                  min={1}
                  max={100}
                  value={cap}
                  onChange={(e) => {
                    const n = Number(e.target.value);
                    setCap(n);
                    try {
                      setPrewipeCap(window.localStorage, n);
                    } catch {
                      /* ignore */
                    }
                  }}
                />
              </label>
              {/* BUG-613 (Aaron: "5 Gorges dam need only 100 ticks to build
                  not 3000" in dev) — the fast-build override had NO UI toggle,
                  so dev sessions never actually got it. DEV-only, same gating
                  as the +£10m/+£1T buttons; flipping resets the BUG-602 flag
                  cache so it applies within the current second, and setTick
                  re-renders the label. Determinism: the flag only ever scales
                  lead-times DOWN on this machine — flag-off play is untouched. */}
              {import.meta.env?.DEV && (
                <label className="brand-menu-row" title={`Dev only: scale construction lead-times down per class, capped at ${FAST_BUILD_MAX_TICKS} ticks (the £5B dam builds in ${FAST_BUILD_MAX_TICKS} ticks, not 3,333)`}>
                  <input
                    type="checkbox"
                    checked={isFastBuildEnabled()}
                    onChange={(e) => {
                      try {
                        if (e.target.checked) window.localStorage.setItem(FAST_BUILD_FLAG_KEY, '1');
                        else window.localStorage.removeItem(FAST_BUILD_FLAG_KEY);
                      } catch {
                        /* storage unavailable — leave the flag as-is */
                      }
                      resetFastBuildFlagCache();
                      setTick((n) => n + 1);
                    }}
                  />
                  Fast build (dev): max {FAST_BUILD_MAX_TICKS} ticks per building
                </label>
              )}
              {/* FEAT-2326609761 inc1 (AC-1, AC-2, ASM-1504): the CONSOLIDATOR
                  toggle. Deliberately DISPATCHES the journalled action rather
                  than writing storage — every OTHER flag on this menu (fast
                  build above, plus liveEngineFlag/webWorkerFlag elsewhere) is
                  localStorage-backed, which is exactly the trap ASM-1504
                  warns against: a localStorage flag would make the same
                  journal replay into a DIFFERENT city depending on which
                  machine/cache loaded it. `checked` reads through the same
                  `?? CONSOLIDATOR_ENABLED_DEFAULT` fallback every other
                  reader uses (GR#16) so an old save with no field shows OFF. */}
              <label
                className="brand-menu-row"
                title="Consolidator (urban regenerator): while enabled, demolishes and rebuilds parts of the city automatically to reduce clutter and cost. Costs real money when it acts."
              >
                <input
                  type="checkbox"
                  checked={state.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT}
                  onChange={() => dispatch({ type: 'toggleConsolidator' })}
                />
                Consolidator (urban regenerator)
              </label>
              {reclaimMsg && <div className="mono muted">{reclaimMsg}</div>}
              <div className="brand-menu-form">
                <button
                  className="btn tiny accent"
                  title="Trims the journal and pre-wipe archive and evicts superseded autosave slots. Never touches the current city, named cities, or the active autosave."
                  onClick={() => {
                    const freed = runReclaim(window.localStorage);
                    const nowUsage = (() => {
                      try {
                        return localStorageUsage(window.localStorage).bytes;
                      } catch {
                        return 0;
                      }
                    })();
                    setReclaimMsg(`Reclaimed ${fmtStorageBytes(freed)} — now ${fmtStorageBytes(nowUsage)} used.`);
                    setTick((n) => n + 1);
                  }}
                >
                  Reclaim storage
                </button>
                <button className="btn tiny" onClick={() => { window.localStorage.removeItem(JOURNAL_KEY); setReclaimMsg(null); setTick((n) => n + 1); }}>
                  Clear journal
                </button>
                <button className="btn tiny" onClick={() => { window.localStorage.removeItem(PREWIPE_ARCHIVE_KEY); setTick((n) => n + 1); }}>
                  Clear pre-wipe archives
                </button>
                <button className="btn tiny" onClick={() => { window.localStorage.removeItem(QUEUE_KEY); setTick((n) => n + 1); }}>
                  Clear debug queue
                </button>
                {/* BUG-575: these two destroy real player saves (named cities /
                    the autosave rotation), unlike the clears above which only
                    touch journal/archive/debug scaffolding — so each is armed
                    by a first click and only fires on an explicit second one. */}
                {confirmingClear === 'namedCities' ? (
                  <span className="brand-menu-form" role="group" aria-label="Confirm clear named cities">
                    <span className="muted">Delete ALL named cities?</span>
                    <button className="btn tiny accent" onClick={clearNamedCities}>
                      Yes, delete
                    </button>
                    <button className="btn tiny" onClick={() => setConfirmingClear(null)}>
                      Cancel
                    </button>
                  </span>
                ) : (
                  <button className="btn tiny" onClick={() => setConfirmingClear('namedCities')}>
                    Clear named cities
                  </button>
                )}
                {confirmingClear === 'autosaves' ? (
                  <span className="brand-menu-form" role="group" aria-label="Confirm clear autosave slots">
                    <span className="muted">Delete all autosave slots?</span>
                    <button className="btn tiny accent" onClick={clearAutosaveSlots}>
                      Yes, delete
                    </button>
                    <button className="btn tiny" onClick={() => setConfirmingClear(null)}>
                      Cancel
                    </button>
                  </span>
                ) : (
                  <button className="btn tiny" onClick={() => setConfirmingClear('autosaves')}>
                    Clear autosave slots
                  </button>
                )}
              </div>
            </div>
          </section>
        </div>
      )}
    </>
  );
}
