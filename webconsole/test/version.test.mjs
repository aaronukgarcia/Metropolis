// version.test.mjs — mechanical check for FEAT-1972079872 (AC-4 / AC-6).
//
// Run with: `npm test` (node --test test/). Two assertions:
//   (a) the git-derived version module exposes a NON-EMPTY version string;
//   (b) NO hand-maintained version literal exists in webconsole/src outside the
//       generated module — i.e. nobody has re-introduced a hardcoded semver
//       (the exact anti-pattern GR#2 forbids). This test FAILS if they do.
//
// Wiring into a gate: add `npm --prefix webconsole test` to CI (and/or a
// pre-push guard) alongside `tsc --noEmit` and `vite build`. Because (b) scans
// source text, a regression (someone hardcoding "1.2.3") turns the gate red.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve, join, relative, sep } from 'node:path';
import { generate } from '../scripts/gen-version.mjs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = resolve(__dirname, '..', 'src');
const GENERATED = resolve(SRC, 'generated');

test('(a) git-derived version module exposes a non-empty version string', () => {
  const data = generate();
  assert.equal(typeof data.version, 'string');
  assert.ok(data.version.length > 0, 'version must be non-empty');
  // The changelog must be an array (empty is allowed only in the git-unavailable
  // fallback; in a real repo checkout it should enumerate history).
  assert.ok(Array.isArray(data.changelog), 'changelog must be an array');
  if (data.gitAvailable) {
    assert.ok(data.changelog.length > 0, 'a git checkout must yield changelog entries');
    // AC-3 spirit: the version is HEAD-derived, so it embeds the short hash.
    assert.match(
      data.version,
      /[0-9a-f]{7,}|dirty|^v\d/,
      'version should be a git-describe form (tag and/or short hash)'
    );
  }
});

// Recursively list *.ts / *.tsx under src/, excluding the generated dir.
function collectSourceFiles(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (resolve(full) === GENERATED) continue; // skip generated module
    const st = statSync(full);
    if (st.isDirectory()) out.push(...collectSourceFiles(full));
    else if (/\.tsx?$/.test(name)) out.push(full);
  }
  return out;
}

test('(b) no hand-maintained version literal outside the generated module', () => {
  const files = collectSourceFiles(SRC);
  // A hardcoded semver assigned to a version-ish constant, e.g.
  //   const APP_VERSION = "1.2.3"      const version = 'v2.0.1'
  //   VERSION: "1.0.0"                 appVersion = `1.4.7`
  // We deliberately target the ASSIGNMENT of a semver to a version-named
  // identifier so ordinary numbers ("1.2.3" in comments/data) don't trip it.
  const banned = /\b(?:app[_-]?version|version|ver)\b\s*[:=]\s*[`'"]v?\d+\.\d+\.\d+/i;
  const offenders = [];
  for (const f of files) {
    const text = readFileSync(f, 'utf8');
    text.split('\n').forEach((line, i) => {
      if (banned.test(line)) {
        offenders.push(`${relative(SRC, f).split(sep).join('/')}:${i + 1}  ${line.trim()}`);
      }
    });
  }
  assert.deepEqual(
    offenders,
    [],
    'Hand-maintained version literal(s) found — version must derive from git ' +
      '(GR#2). Offending lines:\n' + offenders.join('\n')
  );
});
