// bug-513-error-code-surfaced.test.mjs — BUG-513 gap 1 (GR#1 pillar-4).
//
// The "Errors captured" panel (RightDock.tsx) rendered `[{e.type}]` for every
// captured error but never the registry MET-xxxx code, even though the code
// IS captured on the ring record (recordError stores it, ErrorRecord.code
// exists). ErrorRow / errorListModel in backend.ts silently dropped the field
// on the way to the presentation model, so the code could never reach the
// screen for anything except a full render crash (ErrorBoundary reads
// errorRecord.code directly and always showed it correctly).
//
// This test proves errorListModel's OUTPUT carries the code end-to-end from a
// recordError() call with a registry code, through the ring, to the row the
// UI renders from. Run with `npm test` (node --test); exercises the shipped
// backend.recordError -> recentErrors -> errorListModel path, no mocks.
//
// RED PROOF (documented, run via scratch-copy cp/mv on backend.ts — never
// git): remove `code: e.code,` from errorListModel's row builder (and/or
// `code?: string;` from the ErrorRow interface) and this test goes RED with
// "expected 'MET-V850', got undefined" — matching the pre-fix shape exactly.
// Restore from the .bak afterward.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { recordError, recentErrors, errorListModel } from '../src/sim/backend.ts';

test('errorListModel surfaces the registry code (MET-xxxx) captured on the ring record', () => {
  const msg = `bug-513 gap1 probe ${Date.now()}-${Math.random()}`;
  recordError(msg, { type: 'app', code: 'MET-V850' });

  const record = recentErrors().find((e) => e.msg === msg);
  assert.ok(record, 'recordError must have appended a ring entry for the probe message');
  assert.equal(record.code, 'MET-V850', 'the ring record itself must carry the code (sanity check)');

  const { rows } = errorListModel(recentErrors());
  const row = rows.find((r) => r.msg === msg);
  assert.ok(row, 'errorListModel must produce a row for the probe message');
  assert.equal(
    row.code,
    'MET-V850',
    'ErrorRow.code must carry the registry code through from the ring record so RightDock can render it — ' +
      'this is exactly what was missing pre-fix (ErrorRow/errorListModel omitted the code field)'
  );
});

test('errorListModel falls back sensibly (never undefined-crashes) when a record has no code', () => {
  const msg = `bug-513 gap1 no-code probe ${Date.now()}-${Math.random()}`;
  recordError(msg, { type: 'window-error' });

  const { rows } = errorListModel(recentErrors());
  const row = rows.find((r) => r.msg === msg);
  assert.ok(row, 'errorListModel must produce a row even for a code-less record');
  // GR#1: never swallow / never blank — the row must still resolve to SOME
  // displayable identifier (RightDock falls back to e.type when e.code is
  // undefined). We only assert the field is legitimately absent here, not
  // silently coerced to an empty string or similar.
  assert.equal(row.code, undefined, 'a record with no code must surface code:undefined, not a fabricated value');
  assert.equal(row.type, 'window-error', 'type must still be present as the fallback display value');
});
