// gen-version.mjs -- build-time git-derived version + changelog generator.
//
// FEAT-1972079872 (Command Console version badge + About page).
// GR#2: app version = git describe; NEVER a hand-maintained constant.
//
// Runs `git describe` + `git log` at BUILD time and writes a GITIGNORED
// generated module (src/generated/version.ts) that the app imports. When git
// is unavailable (tarball build, no .git, git not on PATH) we fall back to a
// safe placeholder so the build NEVER breaks -- the fallback is clearly marked
// so a reviewer can tell a real stamp from a degraded one.
//
// SSOT: git. There is no version number to hand-edit here or anywhere in the
// app; a new commit IS the version bump and IS the newest About entry.

import { execFileSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, resolve } from 'node:path';
import { mkdirSync, writeFileSync } from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, '..', '..'); // webconsole/scripts -> repo root
const OUT_FILE = resolve(__dirname, '..', 'src', 'generated', 'version.ts');
// LIVE version file (Aaron, 2026-08-27): a plain JSON at the webconsole root that
// is NOT in Vite's module graph and NOT watched for HMR (see vite.config.ts's
// server.watch.ignored + the /version.json middleware). The post-commit hook
// rewrites ONLY this file, so a commit updates the running app's version HOT —
// no page reload, no sim reset. The app polls /version.json for it.
const LIVE_FILE = resolve(__dirname, '..', 'version.live.json');

// CAP: the About changelog is baked at build time from the last N commits.
// This is a deliberate, documented cap (no-silent-caps rule) -- the About page
// notes it in its footer so a viewer knows the list is bounded, not truncated
// silently. Bump if a fuller history is wanted; it only affects build size.
export const CHANGELOG_COMMIT_CAP = 100;

// Field delimiter for `git log --pretty` output. 0x1f (unit separator) never
// appears in a commit subject or ref name, so splitting is unambiguous.
const SEP = String.fromCharCode(31); // 0x1f unit separator

const FALLBACK = {
  version: 'dev',
  numericVersion: '0.0.0.0',
  sha: 'dev',
  gitAvailable: false,
  generatedAt: new Date().toISOString(),
  changelog: [],
};

export function git(args) {
  return execFileSync('git', args, {
    cwd: REPO_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'ignore'],
  }).trim();
}

/**
 * Short commit sha of HEAD, or 'dev' when git is unavailable. This is the
 * comparison key for FEAT-2326609725's stale-build guard: unlike `version`
 * (a describe string) or `numericVersion` (commits-since-tag, which the
 * pre-existing hot-upgrade path already advances live via a poll), a sha is
 * an exact, unambiguous identity for "which commit is this" with no parsing.
 */
function computeShortSha() {
  try {
    return git(['rev-parse', '--short', 'HEAD']) || 'dev';
  } catch {
    return 'dev';
  }
}

/**
 * A 1.2.3.4-style numeric version whose 4th component INCREASES on every commit,
 * so the title/badge visibly ticks up with each commit (Aaron, 2026-08-27).
 *
 * Derived (GR#2: from git, never hand-maintained): MAJOR.MINOR.PATCH come from
 * the latest semver tag; the 4th part is the number of commits SINCE that tag
 * (so a new commit bumps 0.3.0.61 -> 0.3.0.62, and a new tag resets to .0 while
 * bumping the semver, e.g. 0.4.0.0 — still monotonically greater). With no tag
 * in history it degrades to 0.0.0.<total-commit-count> so it still always rises.
 */
function computeNumericVersion() {
  try {
    // --long always emits the "-<n>-g<hash>" suffix, even when exactly on a tag,
    // so parsing is uniform (e.g. "v0.3.0-0-g<hash>" on the tag itself).
    const long = git(['describe', '--tags', '--long']);
    const m = long.match(/^v?(\d+)\.(\d+)\.(\d+)-(\d+)-g[0-9a-f]+/i);
    if (m) {
      const [, maj, min, patch, since] = m;
      return `${maj}.${min}.${patch}.${since}`;
    }
  } catch {
    // no tag reachable — fall through to a total-commit-count based number
  }
  try {
    const count = git(['rev-list', '--count', 'HEAD']);
    if (/^\d+$/.test(count)) return `0.0.0.${count}`;
  } catch {
    // git unavailable
  }
  return '0.0.0.0';
}

/**
 * Collect version + changelog from git. Never throws -- returns FALLBACK-shaped
 * data with gitAvailable:false when git can't be read.
 */
export function generate() {
  try {
    const version = git(['describe', '--tags', '--always', '--dirty']) || 'unknown';

    const raw = git([
      'log',
      `-${CHANGELOG_COMMIT_CAP}`,
      `--pretty=format:%h%x1f%s%x1f%cI%x1f%D`,
    ]);

    const changelog = raw
      .split('\n')
      .filter((l) => l.length > 0)
      .map((line) => {
        const [hash, subject, date, refs] = line.split(SEP);
        // %D gives ref names like "HEAD -> main, tag: v1.2.0". Extract a tag if
        // this commit is a milestone/version boundary.
        let tag;
        if (refs) {
          const m = refs.match(/tag:\s*([^,]+)/);
          if (m) tag = m[1].trim();
        }
        return tag ? { hash, subject, date, tag } : { hash, subject, date };
      });

    return {
      version,
      numericVersion: computeNumericVersion(),
      sha: computeShortSha(),
      gitAvailable: true,
      generatedAt: new Date().toISOString(),
      changelogCap: CHANGELOG_COMMIT_CAP,
      changelog,
    };
  } catch {
    return { ...FALLBACK, changelogCap: CHANGELOG_COMMIT_CAP, generatedAt: new Date().toISOString() };
  }
}

/**
 * Compute version data LIVE at request time, straight from git HEAD -- never
 * from version.live.json. FEAT-2326609725 (2026-09-02 incident): a long-lived
 * vite dev server kept serving an OLD module graph while /version.json ALSO
 * reported the stale version, because that endpoint read a file that is only
 * rewritten by the post-commit hook -- if that hook doesn't fire (or the dev
 * server predates it, or a rebase/checkout moves HEAD without a commit), the
 * file silently drifts from the real on-disk HEAD and polling it can't catch
 * the drift. This function always asks git directly, so it reflects reality
 * regardless of how long the dev-server process has been running.
 *
 * A short in-process cache (LIVE_CACHE_MS) avoids spawning `git` on every
 * client poll while staying well under any plausible poll interval.
 */
const LIVE_CACHE_MS = 2000;
let liveCache = null; // { data, ts } | null

export function getLiveVersionData() {
  const now = Date.now();
  if (liveCache && now - liveCache.ts < LIVE_CACHE_MS) return liveCache.data;

  let version = 'unknown';
  try {
    version = git(['describe', '--tags', '--always', '--dirty']) || 'unknown';
  } catch {
    // git unavailable -- fall through with the 'unknown' placeholder.
  }

  const data = {
    version,
    numericVersion: computeNumericVersion(),
    sha: computeShortSha(),
    gitAvailable: version !== 'unknown',
    generatedAt: new Date().toISOString(),
  };
  liveCache = { data, ts: now };
  return data;
}

/**
 * Write the live version JSON (version.live.json) that the running app polls.
 * Kept tiny and dependency-free. This is the ONLY file the post-commit hook
 * touches, so a commit never disturbs Vite's module graph (no reload, no reset).
 */
export function writeLiveVersion(data) {
  const live = {
    version: data.version,
    numericVersion: data.numericVersion,
    sha: data.sha,
    gitAvailable: data.gitAvailable,
    generatedAt: data.generatedAt,
  };
  writeFileSync(LIVE_FILE, JSON.stringify(live, null, 2) + '\n', 'utf8');
  return live;
}

/** Regenerate ONLY version.live.json (for the post-commit hot-upgrade hook). */
export function writeLiveVersionOnly() {
  return writeLiveVersion(generate());
}

/** Write src/generated/version.ts from the git data. Returns the data. */
export function writeVersionModule() {
  const data = generate();
  mkdirSync(dirname(OUT_FILE), { recursive: true });

  const banner =
    '// AUTO-GENERATED by webconsole/scripts/gen-version.mjs at build time.\n' +
    '// DO NOT EDIT and DO NOT COMMIT -- this file is gitignored.\n' +
    '// Source of truth is git (GR#2). Regenerated on every build/dev start.\n';

  const body =
    banner +
    '\n' +
    'export interface ChangelogEntry {\n' +
    '  hash: string;\n' +
    '  subject: string;\n' +
    '  date: string;\n' +
    '  tag?: string;\n' +
    '}\n\n' +
    'export interface VersionInfo {\n' +
    '  version: string;\n' +
    '  numericVersion: string;\n' +
    '  sha: string;\n' +
    '  gitAvailable: boolean;\n' +
    '  generatedAt: string;\n' +
    '  changelogCap: number;\n' +
    '  changelog: ChangelogEntry[];\n' +
    '}\n\n' +
    `// Cap: last ${CHANGELOG_COMMIT_CAP} commits (see gen-version.mjs CHANGELOG_COMMIT_CAP).\n` +
    'export const versionInfo: VersionInfo = ' +
    JSON.stringify(data, null, 2) +
    ';\n\n' +
    '// git-describe string (e.g. "v0.3.0-61-g3e0714e"); full detail for About.\n' +
    'export const APP_VERSION = versionInfo.version;\n' +
    '// 1.2.3.4-style number that increments every commit — for the title/badge.\n' +
    'export const APP_VERSION_NUMERIC = versionInfo.numericVersion;\n' +
    '// Short commit sha FROZEN at build/dev-start (FEAT-2326609725): unlike the\n' +
    "// numeric/version above (which the hot-upgrade poll advances live to track\n" +
    '// disk), this constant never changes for the life of the loaded bundle --\n' +
    '// it is exactly "which commit is the JS actually running", the baseline the\n' +
    '// stale-build guard compares against the live server sha.\n' +
    'export const APP_VERSION_SHA = versionInfo.sha;\n' +
    'export const CHANGELOG = versionInfo.changelog;\n';

  writeFileSync(OUT_FILE, body, 'utf8');
  // Keep the live JSON in lockstep at build/dev-start so the initial poll matches
  // the baked-in module (no spurious upgrade toast on first load).
  writeLiveVersion(data);
  return data;
}

// Allow running directly:
//   node scripts/gen-version.mjs              -> writes the module + live JSON
//   node scripts/gen-version.mjs --live-only  -> writes ONLY version.live.json
//                                                (post-commit hot-upgrade hook)
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv.includes('--live-only')) {
    const live = writeLiveVersionOnly();
    process.stdout.write(
      `[gen-version] wrote ${LIVE_FILE} (live-only)\n` +
        `  version: ${live.version}${live.gitAvailable ? '' : ' (FALLBACK -- git unavailable)'}\n`
    );
  } else {
    const d = writeVersionModule();
    process.stdout.write(
      `[gen-version] wrote ${OUT_FILE} + ${LIVE_FILE}\n` +
        `  version: ${d.version}${d.gitAvailable ? '' : ' (FALLBACK -- git unavailable)'}\n` +
        `  changelog entries: ${d.changelog.length}\n`
    );
  }
}
