// fakeIndexedDB.mjs — FEAT-2326609778: minimal in-memory IndexedDB shim for
// tests. No `fake-indexeddb` devDependency exists in this project yet (checked
// package.json/package-lock.json before writing this), so per the brief's
// instruction this is a hand-rolled minimal shim scoped to exactly the
// MiniIDBFactory/MiniIDBDatabase/MiniIDBObjectStore subset saveStore.ts's real
// `indexedDBKVStore()` adapter consumes (open/onupgradeneeded/transaction/
// objectStore/get/put/delete/getAllKeys) — enough to exercise the REAL
// production adapter code path, not just the always-available memory fallback.
//
// `backingMaps` is a Map<dbName, Map<key,value>> shared ACROSS factory
// instances constructed with the same `backingMaps` argument, so a test can
// simulate "close the tab, reopen it" by creating a fresh factory pointed at
// the same backing map and asserting the data is still there.

function makeRequest() {
  const req = { result: undefined, error: null, onsuccess: null, onerror: null };
  return req;
}

function resolveRequest(req, result) {
  req.result = result;
  queueMicrotask(() => {
    if (req.onsuccess) req.onsuccess();
  });
}

function rejectRequest(req, error) {
  req.error = error;
  queueMicrotask(() => {
    if (req.onerror) req.onerror();
  });
}

export function createFakeIndexedDBFactory(backingMaps = new Map(), opts = {}) {
  const failNextWrites = opts.failNextWrites ?? { count: 0, error: () => new Error('simulated quota exceeded') };

  function getStoreMap(dbName) {
    let db = backingMaps.get(dbName);
    if (!db) {
      db = new Map();
      backingMaps.set(dbName, db);
    }
    return db;
  }

  return {
    backingMaps,
    failNextWrites,
    open(name, _version) {
      const req = makeRequest();
      req.onupgradeneeded = null;
      req.onblocked = null;
      const storeMap = getStoreMap(name);
      const objectStoreNames = new Set(['kv']); // pre-created for simplicity — mirrors real onupgradeneeded behaviour
      const fakeDb = {
        objectStoreNames: { contains: (n) => objectStoreNames.has(n) },
        createObjectStore: (n) => {
          objectStoreNames.add(n);
        },
        transaction(storeName, _mode) {
          return {
            objectStore(sName) {
              return {
                get(key) {
                  const r = makeRequest();
                  resolveRequest(r, storeMap.has(key) ? storeMap.get(key) : undefined);
                  return r;
                },
                put(value, key) {
                  const r = makeRequest();
                  if (failNextWrites.count > 0) {
                    failNextWrites.count -= 1;
                    rejectRequest(r, failNextWrites.error());
                    return r;
                  }
                  storeMap.set(key, value);
                  resolveRequest(r, key);
                  return r;
                },
                delete(key) {
                  const r = makeRequest();
                  storeMap.delete(key);
                  resolveRequest(r, undefined);
                  return r;
                },
                getAllKeys() {
                  const r = makeRequest();
                  resolveRequest(r, Array.from(storeMap.keys()));
                  return r;
                },
              };
            },
          };
        },
        close() {},
      };
      // Simulate onupgradeneeded firing on first-ever open (fresh backing map) —
      // not load-bearing for these tests (objectStoreNames pre-seeded above) but
      // exercises the code path saveStore.ts's openDb() wires up.
      queueMicrotask(() => {
        req.result = fakeDb;
        if (req.onupgradeneeded) req.onupgradeneeded();
        if (req.onsuccess) req.onsuccess();
      });
      return req;
    },
  };
}
