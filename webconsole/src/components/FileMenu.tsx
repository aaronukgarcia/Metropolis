import { useEffect, useRef, useState } from 'react';
import { useSim } from '../sim/simContext';
import { fmtNum } from '../sim/utils';
import type { NamedSaveMeta } from '../sim/namedsaves';
import type { RecentOpened } from '../sim/recentfiles';
import type { RecoverableLineage } from '../sim/replay';

export function FileMenu() {
  const { cityName, listSaves, listRecent, saveGame, saveGameAs, loadGame, loadNamed, renameCity, exportCity, importCity, listCities, listCitiesAll, adoptCity } =
    useSim();
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<'menu' | 'saveAs' | 'rename' | 'load' | 'cities'>('menu');
  // BUG-1156 r4: read from storage when the Cities view OPENS (not per render, BUG-602).
  const [cities, setCities] = useState<RecoverableLineage[]>([]);
  // BUG-1156 r5: the IndexedDB half of the list is async - the localStorage rows show at once, this flags the pending union.
  const [citiesLoading, setCitiesLoading] = useState(false);
  const citiesReq = useRef(0);
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
                  // Storage reads happen HERE (open handler), never in render (BUG-602). The sync
                  // localStorage rows paint first; the two-tier union (IndexedDB too) replaces them.
                  const req = ++citiesReq.current;
                  setCities(listCities ? listCities() : []);
                  setMode('cities');
                  if (listCitiesAll) {
                    setCitiesLoading(true);
                    void listCitiesAll()
                      .then((all) => {
                        if (citiesReq.current === req) setCities(all);
                      })
                      .catch(() => undefined)
                      .finally(() => {
                        if (citiesReq.current === req) setCitiesLoading(false);
                      });
                  }
                }}
              >
                Cities…
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
          {mode === 'cities' && (
            <CitiesList cities={cities} loading={citiesLoading} onOpen={(id, idbOnly) => adoptCity?.(id, idbOnly)} />
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

/**
 * BUG-1156 r4: the persistent Cities list - EVERY lineage with a valid savepoint,
 * whatever the boot state: the current city (marked, not openable), real cities,
 * then scratch ("Recovered session") cities. Open adopts the lineage and reloads.
 */
function CitiesList({
  cities,
  loading,
  onOpen,
}: {
  cities: RecoverableLineage[];
  loading: boolean;
  onOpen: (lineageId: string, idbOnly?: boolean) => void;
}) {
  if (cities.length === 0 && loading) return <div className="brand-menu-city muted" data-cities-loading>Loading saved cities…</div>;
  if (cities.length === 0) return <div className="brand-menu-city muted">No saved cities found</div>;
  return (
    <div data-cities-list>
      {loading && (
        <div className="brand-menu-city muted" data-cities-loading>
          Checking durable storage…
        </div>
      )}
      {cities.map((c) => (
        <div key={c.lineageId} className="brand-menu-item" data-city={c.lineageId} data-scratch={c.scratch ? 'true' : 'false'}>
          <strong>{c.label}</strong>
          {c.current ? ' (current)' : ''}
          <span className="muted">
            {' '}
            t{fmtNum(c.snapshotTick)}
            {c.population !== undefined ? `, pop ${fmtNum(c.population)}` : ''}, saved {c.savedAt}
          </span>{' '}
          {c.current ? null : (
            <button className="btn tiny" onClick={() => onOpen(c.lineageId, c.idbOnly)}>
              Open
            </button>
          )}
        </div>
      ))}
    </div>
  );
}
