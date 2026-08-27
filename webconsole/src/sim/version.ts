// version.ts — presentation helpers over the build-time git-derived version.
//
// FEAT-1972079872. This module holds NO version number of its own: it reads
// everything from ../generated/version (which is produced from `git describe`
// + `git log` at build time — GR#2). It only decides how to DISPLAY that
// git-derived string. A hardcoded semver here would be a rule violation and is
// caught by test/version.test.mjs.

import { versionInfo } from '../generated/version';

/**
 * Human badge form of the git-describe string.
 *
 * `git describe --tags` yields forms like:
 *   - "v1.2.0"                    (exactly on a milestone tag)
 *   - "v1.2.0-7-gabc1234"         (7 commits past v1.2.0)
 *   - "abc1234"                   (no tag reachable, --always short hash)
 *   - "...-dirty"                 (uncommitted changes at build)
 *
 * We surface a compact label. When the describe output already starts with a
 * "v", we keep it; when it is a bare hash (no milestone tag yet in this repo),
 * we prefix a short marker so it still reads as a build identifier rather than
 * a stray token. The full raw string is always available for the About page.
 */
export function versionBadgeLabel(): string {
  // Aaron (2026-08-27): the title/badge must show a 1.2.3.4-style number that
  // INCREASES on every commit. numericVersion is MAJOR.MINOR.PATCH.<commits-since-tag>
  // (derived from git in gen-version.mjs — GR#2, never hand-maintained), so it
  // ticks up 0.3.0.61 -> 0.3.0.62 with each commit. The full git-describe string
  // stays available in the tooltip / About page (versionRaw).
  const n = versionInfo.numericVersion;
  if (n && n !== '0.0.0.0') return `v${n}`;
  // Degraded (git unavailable / no history): fall back to the describe string.
  const v = versionInfo.version;
  if (!v || v === 'dev' || v === 'unknown') return 'dev';
  return v;
}

/** The 1.2.3.4-style numeric version (increments per commit). */
export const versionNumeric = versionInfo.numericVersion;

/** The full, unabbreviated git-describe string (for tooltips / About). */
export const versionRaw = versionInfo.version;

/**
 * Footer note for the About changelog making the build-time cap explicit
 * (no-silent-caps rule): the list is the last N commits, not the whole history.
 */
export const CHANGELOG_CAP_NOTE =
  `Changelog shows the most recent ${versionInfo.changelogCap} check-ins, ` +
  `baked into the build from git log. Older history lives in git.`;
