// sync-traffic-data.mjs — mirror the SSOT traffic tables into the webconsole tree.
//
// WHY (BUG-860, 2026-09-09): webconsole/src/sim modules imported
// '../../../data/traffic/*.json' straight out of the repo's data/ directory.
// That resolves in a full checkout, but every harness that shadow-copies
// webconsole/src into a temp dir (testsupport/mutant.mjs, the CI mutant
// self-reinvoke) then fails with ERR_MODULE_NOT_FOUND — the inc2 push
// (160a8b2) reddened all three node-test shards and webconsole-tsx this way
// (BUG-835 predicted it). The fix is an in-tree mirror under
// webconsole/src/sim/traffic-data/ that ships with src/ wherever src/ is
// copied. GR#3: data/traffic/ stays the single source of truth; the mirror is
// a generated artefact that is COMMITTED (so a plain checkout works with no
// build step) and VALIDATED byte-for-byte by
// webconsole/test/traffic-data-mirror.test.mjs — a stale mirror is a red
// test, never silent drift. Runs from predev/prebuild so `npm run dev|build`
// refresh it; run `node scripts/sync-traffic-data.mjs` after editing any
// data/traffic table.
import { readdirSync, readFileSync, writeFileSync, mkdirSync, existsSync, unlinkSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');
const srcDir = join(repoRoot, 'data', 'traffic');
const dstDir = join(repoRoot, 'webconsole', 'src', 'sim', 'traffic-data');

/** The mirrored set: every table in data/traffic/ plus the engine defaults file data/traffic.json. */
export function mirrorPairs() {
  const pairs = [];
  for (const name of readdirSync(srcDir).filter((n) => n.endsWith('.json')).sort()) {
    pairs.push({ src: join(srcDir, name), dst: join(dstDir, name), name });
  }
  // Engine-side tables the traffic modules also consume (explicit, not readdir:
  // data/ holds ~60 tables and only these two are traffic inputs). roads.json
  // is inc3's (trafficAssignment.ts) - the BUG-860 round caught it about to
  // re-introduce the out-of-root import class. wellbeing.json is inc5's
  // (trafficWellbeing.ts, FEAT-2326609798, ASM-1519) - the webconsole did not
  // read data/wellbeing.json at all before this increment (it had its own
  // inline PLACEHOLDER TS constants); the commute stress anchors now come
  // from the SAME SSOT table the Go engine already uses (GR#3).
  for (const extra of ['traffic.json', 'roads.json', 'wellbeing.json']) {
    pairs.push({ src: join(repoRoot, 'data', extra), dst: join(dstDir, extra), name: extra });
  }
  return pairs;
}

/** Mirror files with no SSOT twin (a table deleted from data/traffic) - orphans drift forever, so they are pruned and reported. */
export function orphans() {
  if (!existsSync(dstDir)) return [];
  const expected = new Set(mirrorPairs().map((p) => p.name));
  return readdirSync(dstDir).filter((n) => n.endsWith('.json') && !expected.has(n));
}

export function sync({ log = console.log } = {}) {
  if (!existsSync(dstDir)) mkdirSync(dstDir, { recursive: true });
  let written = 0;
  for (const name of orphans()) {
    unlinkSync(join(dstDir, name));
    written++;
    log(`sync-traffic-data: pruned orphan mirror ${name}`);
  }
  for (const { src, dst, name } of mirrorPairs()) {
    const bytes = readFileSync(src);
    if (existsSync(dst) && readFileSync(dst).equals(bytes)) continue;
    writeFileSync(dst, bytes);
    written++;
    log(`sync-traffic-data: refreshed ${name}`);
  }
  return written;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const n = sync();
  console.log(`sync-traffic-data: ${n} file(s) refreshed into webconsole/src/sim/traffic-data/`);
}
