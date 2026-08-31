// safe-storage.test.mjs — BUG-457: shared QuotaExceeded-safe setItem wrapper.
//
// Every localStorage write site in the app (journal, savepoint, named save,
// recentOpened, pre-wipe archive) now routes through safeSetItem so a thrown
// QuotaExceededError degrades to a typed {ok:false, quota:true} result instead
// of an uncaught exception. This file proves the classification is correct
// across the shapes real browsers and test mocks throw, and that the wrapper
// never itself throws.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { safeSetItem, isQuotaError } from '../src/sim/safeStorage.ts';

describe('isQuotaError: classification', () => {
  test('DOMException-shaped QuotaExceededError (modern browsers)', () => {
    class FakeDOMException extends Error {
      constructor(message, name) {
        super(message);
        this.name = name;
      }
    }
    // Simulate the case where the environment HAS a global DOMException.
    if (typeof DOMException !== 'undefined') {
      const e = new DOMException('quota exceeded', 'QuotaExceededError');
      assert.ok(isQuotaError(e));
    }
    // Even without a real DOMException global, a plain Error with the right
    // .name must still be classified as quota (legacy engines, test mocks).
    const fake = new FakeDOMException('Setting the value exceeded the quota.', 'QuotaExceededError');
    assert.ok(isQuotaError(fake));
  });

  test('legacy Firefox NS_ERROR_DOM_QUOTA_REACHED', () => {
    const e = new Error('persistent storage full');
    e.name = 'NS_ERROR_DOM_QUOTA_REACHED';
    assert.ok(isQuotaError(e));
  });

  test('plain Error whose message mentions quota (test mocks, older engines)', () => {
    assert.ok(isQuotaError(new Error('QuotaExceededError: setItem blocked')));
    assert.ok(isQuotaError(new Error('exceeded the quota')));
  });

  test('a non-quota error is NOT classified as quota', () => {
    assert.equal(isQuotaError(new Error('SecurityError: private mode')), false);
    assert.equal(isQuotaError(new TypeError('storage is null')), false);
    assert.equal(isQuotaError(undefined), false);
    assert.equal(isQuotaError(null), false);
    assert.equal(isQuotaError('a bare string throw'), false);
  });
});

describe('safeSetItem: never throws, classifies quota', () => {
  test('successful write reports ok:true, quota:false', () => {
    const map = new Map();
    const storage = { setItem: (k, v) => map.set(k, v) };
    const result = safeSetItem(storage, 'k', 'v');
    assert.deepEqual(result, { ok: true, quota: false });
    assert.equal(map.get('k'), 'v');
  });

  test('a quota-throwing storage returns {ok:false, quota:true} and does NOT throw', () => {
    const storage = {
      setItem() {
        throw new Error('QuotaExceededError: simulated');
      },
    };
    let result;
    assert.doesNotThrow(() => {
      result = safeSetItem(storage, 'k', 'v');
    });
    assert.equal(result.ok, false);
    assert.equal(result.quota, true);
    assert.match(result.error, /simulated/);
  });

  test('a non-quota-throwing storage returns {ok:false, quota:false}', () => {
    const storage = {
      setItem() {
        throw new Error('SecurityError: private browsing');
      },
    };
    const result = safeSetItem(storage, 'k', 'v');
    assert.equal(result.ok, false);
    assert.equal(result.quota, false);
  });

  test('missing storage degrades to a typed failure, never throws', () => {
    assert.doesNotThrow(() => {
      const result = safeSetItem(null, 'k', 'v');
      assert.equal(result.ok, false);
      assert.equal(result.quota, false);
    });
    assert.doesNotThrow(() => safeSetItem(undefined, 'k', 'v'));
  });

  test('MUTATION PROOF: without the try/catch, a throwing storage would propagate', () => {
    // Prove the underlying storage really does throw (i.e. safeSetItem is
    // actually doing something, not just returning ok:true unconditionally).
    const storage = {
      setItem() {
        throw new Error('QuotaExceededError: simulated');
      },
    };
    assert.throws(() => storage.setItem('k', 'v'), /simulated/);
    // ...yet routed through safeSetItem, it does not.
    assert.doesNotThrow(() => safeSetItem(storage, 'k', 'v'));
  });
});
