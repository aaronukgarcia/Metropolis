// BUG-749 (P1, CI blind spot): CI's node-test job (.github/workflows/ci.yml,
// `node --test --test-shard=N/3` at the repo root) can never execute a
// webconsole/test/*.test.tsx file — bare node cannot strip JSX. Those files
// were only ever exercised through tools/test/scoped.mjs (a build agent's
// discipline, not a CI gate) until the new "webconsole-tsx" CI job was added,
// which invokes `node tools/test/scoped.mjs --webconsole-tsx-all` — a mode
// that runs the FULL `test/*.test.tsx` glob under webconsole (see
// WEBCONSOLE_TSX_GLOB in scoped.mjs), not the older curated
// WEBCONSOLE_TSX_FILES subset that --webconsole-ci/npm test cover.
//
// This guard proves that glob actually reaches every real tsx test file, so
// a future .test.tsx landing somewhere the CI job's glob would not match
// (e.g. a NESTED subdirectory under webconsole/test/, which the flat
// `test/*.test.tsx` pattern does not recurse into) fails LOUDLY here instead
// of silently reopening the exact CI blind spot BUG-749 closed. It also pins
// that scoped.mjs still exposes the flag by that literal name, so a future
// rename/removal of --webconsole-tsx-all is caught here rather than only by
// the CI job itself going red days later.
//
// Lives under tools/test/ (BUG-543: CI's root `node --test` auto-discovers
// ANY .mjs/.js file under a `test/` directory, regardless of name) — this
// file IS a genuine test suite (real `test()` cases below), so it is meant
// to run for real under that auto-discovery; no NODE_TEST_CONTEXT skip is
// needed (that guard in tools/test/scoped.mjs exists only because scoped.mjs
// itself is a CLI tool with no test() cases of its own, not because every
// file under tools/test/ needs one).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs';
import { resolve, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const WEBCONSOLE_TEST_DIR = resolve(REPO_ROOT, 'webconsole', 'test');
const SCOPED_MJS_PATH = resolve(REPO_ROOT, 'tools', 'test', 'scoped.mjs');

// Recursively collect every *.test.tsx anywhere under webconsole/ (not just
// webconsole/test/) so a misplaced file (a nested subdir, or a stray tsx test
// dropped in webconsole/src by mistake) is caught too, not just files that
// already live where the glob expects them.
function findAllTestTsxFiles(root) {
  const out = [];
  const walk = (dir) => {
    let entries;
    try { entries = readdirSync(dir); } catch { return; }
    for (const name of entries) {
      if (name === 'node_modules') continue;
      const full = join(dir, name);
      let st;
      try { st = statSync(full); } catch { continue; }
      if (st.isDirectory()) walk(full);
      else if (st.isFile() && name.endsWith('.test.tsx')) out.push(full);
    }
  };
  walk(root);
  return out;
}

test('scoped.mjs still exposes --webconsole-tsx-all (BUG-749 CI job dependency)', () => {
  const src = readFileSync(SCOPED_MJS_PATH, 'utf8');
  assert.match(
    src,
    /--webconsole-tsx-all/,
    'the "webconsole-tsx" CI job invokes this exact flag — if it is renamed or removed here, ' +
    'update .github/workflows/ci.yml in the SAME commit'
  );
});

test('every webconsole/test/*.test.tsx file is directly under test/ (matched by the flat CI glob)', () => {
  assert.ok(existsSync(WEBCONSOLE_TEST_DIR), `expected ${WEBCONSOLE_TEST_DIR} to exist`);

  const allTsxTests = findAllTestTsxFiles(resolve(REPO_ROOT, 'webconsole'));
  assert.ok(allTsxTests.length > 0, 'expected at least one *.test.tsx file under webconsole/ — if this suite was deleted entirely, this assertion should be revisited, not silently weakened');

  // The CI job's runner mode (scoped.mjs --webconsole-tsx-all) expands the
  // FLAT glob `test/*.test.tsx` (webconsole/test/*.test.tsx, one path
  // segment, no recursion). Any *.test.tsx file that does NOT resolve
  // directly inside webconsole/test/ (a nested subdirectory, or a file
  // living outside webconsole/test/ entirely) would silently NOT be covered
  // by that glob — exactly the class of blind spot BUG-749 exists to close.
  const notFlatUnderTestDir = allTsxTests.filter((f) => dirname(f) !== WEBCONSOLE_TEST_DIR);

  assert.deepEqual(
    notFlatUnderTestDir,
    [],
    `found *.test.tsx file(s) the CI "webconsole-tsx" job's flat glob (test/*.test.tsx) would NOT match ` +
    `(they live in a subdirectory or outside webconsole/test/ entirely): ${notFlatUnderTestDir.join(', ')}. ` +
    `Either move the file(s) directly into webconsole/test/, or widen WEBCONSOLE_TSX_GLOB in ` +
    `tools/test/scoped.mjs (and the CI job comment referencing it) to a recursive pattern in the SAME commit.`
  );
});
