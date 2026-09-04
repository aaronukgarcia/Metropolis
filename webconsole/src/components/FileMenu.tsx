import { useEffect, useRef, useState } from 'react';
import { useSim } from '../sim/simContext';
import { fmtNum } from '../sim/utils';
import type { NamedSaveMeta } from '../sim/namedsaves';
import type { RecentOpened } from '../sim/recentfiles';

export function FileMenu() {
  const { cityName, listSaves, listRecent, saveGame, saveGameAs, loadGame, loadNamed, renameCity, exportCity, importCity } =
    useSim();
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<'menu' | 'saveAs' | 'rename' | 'load'>('menu');
  const [name, setName] = useState(cityName);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const close = () => {
    setOpen(false);
    setMode('menu');
  };

  return (
    <div className="brand-menu" ref={root}>
      <button
        className={`btn tiny${open ? ' active' : ''}`}
        onClick={() => {
          setName(cityName);
          setMode('menu');
          setOpen((v) => !v);
        }}
        title={`File — ${cityName}`}
      >
        File
      </button>
      {open && (
        <div className="brand-menu-pop" role="menu">
          <div className="brand-menu-city muted mono" title={cityName}>
            {cityName}
          </div>
          {mode === 'menu' && (
            <>
              <button className="brand-menu-item" onClick={() => void saveGame().then(close)}>
                Save
              </button>
              <button
                className="brand-menu-item"
                onClick={() => {
                  setName(cityName);
                  setMode('saveAs');
                }}
              >
                Save As…
              </button>
              <button
                className="brand-menu-item"
                onClick={() => {
                  setMode('load');
                }}
              >
                Load…
              </button>
              <button
                className="brand-menu-item"
                onClick={() => {
                  setName(cityName);
                  setMode('rename');
                }}
              >
                Rename…
              </button>
              <button className="brand-menu-item" onClick={() => void exportCity().then(close)} title="Download a compressed backup of this city">
                Export City
              </button>
              <button className="brand-menu-item" onClick={() => void importCity().then(close)} title="Load a city from an exported/saved file">
                Import City…
              </button>
            </>
          )}
          {mode === 'saveAs' && (
            <form
              className="brand-menu-form"
              onSubmit={(e) => {
                e.preventDefault();
                // BUG-445/AC-5: a slug collision with a DIFFERENT city is
                // refused by the store until the user explicitly confirms —
                // never overwritten silently. A same-city re-save has no
                // collision and proceeds straight through.
                void saveGameAs(name).then((result) => {
                  if (result.ok) {
                    close();
                    return;
                  }
                  if (result.collision) {
                    const overwrite = window.confirm(
                      `A city named "${result.collision.existingName}" already exists. Overwrite it?`,
                    );
                    if (overwrite) {
                      void saveGameAs(name, { confirmedOverwrite: true }).then(close);
                    }
                  }
                });
              }}
            >
              <input
                className="brand-menu-input"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                maxLength={40}
                aria-label="Save as name"
              />
              <button className="btn tiny accent" type="submit">
                Save As
              </button>
            </form>
          )}
          {mode === 'rename' && (
            <form
              className="brand-menu-form"
              onSubmit={(e) => {
                e.preventDefault();
                // BUG-445/AC-5: same collision-confirm contract as Save As.
                const result = renameCity(name);
                if (result.ok) {
                  close();
                  return;
                }
                if (result.collision) {
                  const overwrite = window.confirm(
                    `A city named "${result.collision.existingName}" already exists. Overwrite it?`,
                  );
                  if (overwrite && renameCity(name, { confirmedOverwrite: true }).ok) close();
                }
              }}
            >
              <input
                className="brand-menu-input"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                maxLength={40}
                aria-label="Rename city"
              />
              <button className="btn tiny accent" type="submit">
                Rename
              </button>
            </form>
          )}
          {mode === 'load' && (
            <LoadList
              recents={listRecent()}
              saved={listSaves()}
              onFromFile={() => {
                void loadGame().then(close);
              }}
              onNamed={(slug) => {
                void loadNamed(slug).then(close);
              }}
            />
          )}
        </div>
      )}
    </div>
  );
}

function LoadList({
  recents,
  saved,
  onFromFile,
  onNamed,
}: {
  recents: RecentOpened[];
  saved: NamedSaveMeta[];
  onFromFile: () => void;
  onNamed: (slug: string) => void;
}) {
  const recentSlugs = new Set(recents.map((r) => r.slug));
  const extra = saved.filter((s) => !recentSlugs.has(s.slug));
  return (
    <>
      <button className="brand-menu-item" onClick={onFromFile}>
        From file…
      </button>
      {recents.length > 0 && <div className="brand-menu-city muted">Last 10 opened</div>}
      {recents.map((s) => (
        <button key={`recent-${s.slug}-${s.openedAt}`} className="brand-menu-item" onClick={() => onNamed(s.slug)}>
          {s.name}
          <span className="muted"> t{fmtNum(s.tick)}</span>
        </button>
      ))}
      {extra.length > 0 && <div className="brand-menu-city muted">Saved cities</div>}
      {extra.map((s) => (
        <button key={s.slug} className="brand-menu-item" onClick={() => onNamed(s.slug)}>
          {s.name}
          <span className="muted"> t{fmtNum(s.tick)}</span>
        </button>
      ))}
    </>
  );
}
