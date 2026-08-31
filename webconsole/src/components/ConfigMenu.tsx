import { useEffect, useMemo, useState } from 'react';
import {
  getPrewipeCap,
  setPrewipeCap,
  localStorageUsage,
  fmtStorageBytes,
  TYPICAL_LOCALSTORAGE_QUOTA_BYTES,
} from '../sim/storageConfig';
import { PREWIPE_ARCHIVE_KEY } from '../sim/captureBeforeWipe';
import { QUEUE_KEY } from '../sim/commitqueue';
import { NAMED_SAVES_INDEX_KEY, NAMED_SAVE_SLOT_PREFIX } from '../sim/namedsaves';
import { SAVEPOINT_KEY_PREFIX } from '../sim/replay';
import { JOURNAL_KEY } from '../sim/journal';

export function ConfigMenu() {
  const [open, setOpen] = useState(false);
  const [cap, setCap] = useState(() => {
    try {
      return getPrewipeCap(window.localStorage);
    } catch {
      return 10;
    }
  });
  const [tick, setTick] = useState(0);

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
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);

  const clearPrefix = (prefix: string) => {
    const keys: string[] = [];
    for (let i = 0; i < window.localStorage.length; i++) {
      const k = window.localStorage.key(i);
      if (k && k.startsWith(prefix)) keys.push(k);
    }
    for (const k of keys) window.localStorage.removeItem(k);
    setTick((n) => n + 1);
  };

  return (
    <>
      <button className={`btn tiny${open ? ' active' : ''}`} onClick={() => setOpen(true)} title="Storage and archive settings">
        Config
      </button>
      {open && (
        <div className="about-backdrop" onClick={() => setOpen(false)} role="presentation">
          <section
            className="panel about-panel"
            role="dialog"
            aria-modal="true"
            aria-label="Storage config"
            onClick={(e) => e.stopPropagation()}
          >
            <header className="panel-h">
              <span className="panel-title">Config — storage</span>
              <button className="btn tiny" onClick={() => setOpen(false)}>
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
              <div className="brand-menu-form">
                <button
                  className="btn tiny accent"
                  onClick={() => {
                    window.localStorage.removeItem(PREWIPE_ARCHIVE_KEY);
                    window.localStorage.removeItem(`${SAVEPOINT_KEY_PREFIX}.1`);
                    window.localStorage.removeItem(`${SAVEPOINT_KEY_PREFIX}.2`);
                    window.localStorage.removeItem(JOURNAL_KEY);
                    setTick((n) => n + 1);
                  }}
                >
                  Reclaim storage
                </button>
                <button className="btn tiny" onClick={() => { window.localStorage.removeItem(JOURNAL_KEY); setTick((n) => n + 1); }}>
                  Clear journal
                </button>
                <button className="btn tiny" onClick={() => { window.localStorage.removeItem(PREWIPE_ARCHIVE_KEY); setTick((n) => n + 1); }}>
                  Clear pre-wipe archives
                </button>
                <button className="btn tiny" onClick={() => { window.localStorage.removeItem(QUEUE_KEY); setTick((n) => n + 1); }}>
                  Clear debug queue
                </button>
                <button
                  className="btn tiny"
                  onClick={() => {
                    clearPrefix(NAMED_SAVE_SLOT_PREFIX);
                    window.localStorage.removeItem(NAMED_SAVES_INDEX_KEY);
                  }}
                >
                  Clear named cities
                </button>
                <button className="btn tiny" onClick={() => clearPrefix(SAVEPOINT_KEY_PREFIX)}>
                  Clear autosave slots
                </button>
              </div>
            </div>
          </section>
        </div>
      )}
    </>
  );
}
