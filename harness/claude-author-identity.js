// Module key: tool.authoridentity (see code.json; GUID b853fd24-0f59-47c0-96c1-4acd87445d5c)
// Spec ref: GR#2; BUG-035

/**
 * claude-author-identity.js — shared sanctioned-identity derivation
 * (FEAT-045 / BOW mkey candidate: tool.committhook; see the registry-key
 * escalation in docs/planning/acceptance/tool.committhook.md).
 *
 * Extracted out of claude-author-guard.js (the PreToolUse guard, now
 * demoted — see that file's header) so it can be required, UNCHANGED, by
 * BOTH the demoted advisory guard AND the enforcing `.git/hooks/commit-msg`
 * hook (githooks/commit-msg, the tracked canonical source). This is the
 * ENTIRE point of this file's existence (AC-4): one derivation, two
 * consumers, so a future fix to the derivation logic cannot silently
 * diverge between an advisory layer and the enforcing one.
 *
 * THE LOGIC ITSELF IS UNCHANGED FROM claude-author-guard.js's PRE-DEMOTION
 * VERSION (read BUG-035/044-052 in that file's git history before touching
 * this — GR#6). This file is a MOVE, not a REWRITE. Do not "improve" the
 * derivation here without a fresh BOW item — this item's scope is moving
 * the code, not changing what it decides (see the acceptance file's
 * "Out of scope").
 *
 * ASM-226 (2026-08-13) IS that fresh BOW item, narrowly scoped: it changes
 * only the HISTORY-SCAN CAP from a hardcoded 2000 to a value DERIVED at
 * runtime from the repo's commit count (deriveScanLimit()), per GR#15. The
 * three trust sources and HISTORY_THRESHOLD are untouched.
 *
 * THREE SOURCES, IN ORDER OF TRUST (AC-4):
 *
 *   1. THE CURRENTLY CONFIGURED GIT IDENTITY — `git config user.email`,
 *      local then global. Trusted UNCONDITIONALLY.
 *   2. EMAILS SEEN REPEATEDLY IN THE TRUNK BRANCH'S OWN HISTORY (`main` if
 *      it exists locally, else `master`, else current branch) — as author
 *      OR committer, at or above THRESHOLDS.HISTORY_THRESHOLD times, scanned
 *      over the most recent deriveScanLimit() commits (the repo's real
 *      commit count, capped at THRESHOLDS.HISTORY_SCAN_LIMIT — ASM-226).
 *   3. CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES — an operator-set env var, for a
 *      legitimate second contributor who has no history yet.
 *
 * WHY (1) IS TRUSTED UNCONDITIONALLY AND WHY (2) ALONE WOULD BE WRONG HERE
 * (AC-5 — restated here, not re-derived from first principles by whoever
 * reads githooks/commit-msg next):
 *
 *   BUG-036: a frequency-only derivation from THIS repo's own rewritten
 *   history would have sanctioned the wrong address and bricked every
 *   legitimate commit, because `git-filter-repo` rewrites history but does
 *   NOT touch local config. History alone would sanction the wrong address
 *   on a repo whose history has been deliberately rewritten (as this one
 *   has, for BUG-042's codename/identity reasons) — the locally configured
 *   `git config user.email` is the one source that survives a history
 *   rewrite unchanged, which is exactly why it is trusted unconditionally
 *   rather than merely weighted highest.
 *
 * MATCHING IS BY EMAIL ONLY (ASM-author-guard-email-only, unchanged — see
 * claude-author-guard.js's header for the original BUG-035 evidence: same
 * person, different name casing across author/committer on this repo's own
 * HEAD at design time).
 *
 * RUNTIME-CONFIGURABLE THRESHOLDS (AC-4's "or an equivalent
 * runtime-configurable value"): `THRESHOLDS` is a plain mutable object, not
 * a frozen constant, so a test can flip `THRESHOLDS.HISTORY_THRESHOLD` and
 * observe the SAME change through every consumer that requires this module
 * — there is exactly one copy of these numbers in the process, not one per
 * requiring file.
 *
 * The history-scan cap is additionally derived per-invocation from the
 * repo's commit count (deriveScanLimit()) and operator-overridable via
 * CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT (ASM-226).
 *
 * WHAT THIS FILE DELIBERATELY DOES NOT DO (AC-4's "lazy implementation"
 * trap, stated so nobody re-adds it later): there is NO embedded fallback
 * sanctioned-identity list anywhere in this file or in either consumer. If
 * this module cannot derive a sanctioned identity (or throws), that is the
 * true answer — every caller's job is to decide what "no answer" means for
 * its own fail-open/fail-closed posture (see claude-author-guard.js for
 * fail-open, githooks/commit-msg for fail-closed), never to substitute a
 * second, undeclared source of truth.
 *
 * TEST-ONLY ESCAPE HATCH: if the env var
 * CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR=1 is set, historyEmails() throws
 * deliberately instead of catching its own git-invocation errors. This
 * exists SOLELY so AC-4/AC-8/AC-16's tests can prove the fail-open/
 * fail-closed behaviour of each CONSUMER without needing to actually break
 * git on the test machine. It is never read anywhere except here, and it
 * changes nothing about the derivation logic's real-world behaviour when
 * unset.
 */

'use strict';

const { execFileSync } = require('child_process');
const os = require('os');
const path = require('path');

// ---------------------------------------------------------------------------
// Runtime-configurable thresholds (AC-4) — a mutable object, not a const.
// ---------------------------------------------------------------------------

const THRESHOLDS = {
  // Policy threshold (unchanged): an identity must appear at or above this
  // many times (as author OR committer) to be sanctioned from history alone.
  HISTORY_THRESHOLD: 3,
  // BUG-052 + ASM-226 (GR#15): HISTORY_SCAN_LIMIT is now the RESOURCE
  // CEILING, not a validator's expected count. The actual number of commits
  // scanned is DERIVED at guard-run time from the repo's real commit count
  // (deriveScanLimit() below) and capped at this ceiling, so a large/old
  // history can never drive unbounded cost while a young repo never asks git
  // for commits that do not exist. Operator-overridable per-invocation via
  // CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT (a positive integer wins; anything
  // else falls back to this documented default).
  HISTORY_SCAN_LIMIT: 2000,
};

/** BUG-136: GIT_DIR (and GIT_WORK_TREE/GIT_COMMON_DIR) redirect git's own
 * repo-discovery to an attacker-fabricated repo, defeating `--local`'s
 * "trusted unconditionally" resolution the exact same way SEC-052 round 2
 * found GIT_CONFIG_GLOBAL/HOME defeating `--global` — live-verified:
 * `GIT_DIR=<evil>/.git git config --local user.email` returns the
 * fabricated repo's value regardless of real cwd, from both an unrelated
 * cwd and from inside this repo. Fix: strip the three redirecting vars from
 * the child's env so git falls back to its normal discovery (walk up from
 * cwd to find `.git`) — the same "immune to the current invocation's
 * environment" shape as round 2's global-scope fix, WITHOUT hardcoding cwd
 * (every consumer and every test in this file deliberately identifies "the
 * repo in question" by process.cwd(), per this file's own `withCwd()`
 * fixture comment — hardcoding a fixed repo root here would silently break
 * that design, not fix the bug). */
function git(args) {
  const env = { ...process.env };
  delete env.GIT_DIR;
  delete env.GIT_WORK_TREE;
  delete env.GIT_COMMON_DIR;
  return execFileSync('git', args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'ignore'],
    env,
  }).trim();
}

/** SEC-052 ROUND 2: candidate file paths for the GLOBAL git-config scope,
 * resolved from a home directory that is IMMUNE to the CURRENT invocation's
 * environment — unlike git's own `--global` resolution, which is not.
 *
 * ROUND 1 (`--local`/`--global` scope flags) correctly defeated `-c
 * user.email=` and the GIT_CONFIG_COUNT/KEY_n/VALUE_n env-var form (both
 * verified again below — still true). But round 2's Destructive reattack
 * (Magpie) found `--global` itself is redirectable: git resolves "the
 * global config file" via the `GIT_CONFIG_GLOBAL` env var (git 2.32+) or
 * `HOME` (classic; `USERPROFILE` too, when `HOME` is unset), and NEITHER is
 * filtered before this hook's child process reads it — the exact same
 * "child process of the very invocation being checked" exposure as round
 * 1's bug, just one scope over. Live-verified on this machine:
 *
 *   GIT_CONFIG_GLOBAL=<evil>.gitconfig git config --global user.email
 *     -> returns the FABRICATED value from <evil>.gitconfig.
 *   HOME=<evil-home> git config --global user.email   (with a real
 *   .gitconfig planted at <evil-home>/.gitconfig)
 *     -> also returns the fabricated value.
 *
 * `os.userInfo().homedir` (NOT `os.homedir()`) is the fix: `os.homedir()`
 * reads `USERPROFILE`/`HOME` directly on this platform and is therefore
 * just as poisonable as git's own resolution — empirically confirmed to
 * flip under a poisoned env in this round's probe. `os.userInfo().homedir`
 * instead resolves via the OS user-profile API (GetUserProfileDirectoryW on
 * Windows, through Node's native binding) and was empirically verified,
 * live, to return the REAL logged-in user's home directory UNCHANGED even
 * with `HOME`, `USERPROFILE`, AND `GIT_CONFIG_GLOBAL` all attacker-set
 * simultaneously for the invocation. Reading the file at that path via
 * `git config --file <path> user.email` was separately verified immune to
 * `-c user.email=` and the GIT_CONFIG_COUNT/KEY_n/VALUE_n form too (an
 * explicit `--file` target, unlike `--global`, is not itself a resolution
 * step that a command-scoped override can redirect).
 *
 * Two candidate paths, in git's own precedence order (git reads the XDG
 * file first, then `~/.gitconfig` — so `~/.gitconfig` wins on a clash,
 * hence it is tried FIRST here so the earliest hit matches git's real
 * effective value): `~/.gitconfig` (git's default global file, and where
 * `git config --global` writes) then the XDG fallback
 * `~/.config/git/config`. `XDG_CONFIG_HOME` itself is deliberately NOT
 * consulted — it is exactly the same shape of attacker-controllable env
 * var as `GIT_CONFIG_GLOBAL`/`HOME`, and a legitimately relocated XDG git
 * config is out of scope for this hook (standard-location global config is
 * the case the fallback exists for at all — see the module header).
 *
 * THIRD SCOPE CHECKED (per this round's brief): `--system` has the same
 * shape of bug (`GIT_CONFIG_SYSTEM` env var redirects it), but this
 * module's fallback chain never reads system scope at all (local, then
 * global only) — there is nothing here for that redirection to reach, so
 * it is noted, not fixed. */
function globalConfigPaths() {
  const home = os.userInfo().homedir;
  return [
    path.join(home, '.gitconfig'),
    path.join(home, '.config', 'git', 'config'),
  ];
}

/** Source 1: the currently configured git identity (local then global).
 * Trusted unconditionally — see header AC-5 note.
 *
 * Local scope: reads via `git config --local user.email` (round 1,
 * unchanged) — empirically immune to `-c`/env-var config overrides because
 * it reads `.git/config` directly rather than resolving through the
 * command-scoped override layer. `git config --local` exits non-zero when
 * no local value is set (not merely empty), so that failure falls through
 * to the global candidates rather than being treated as "no email
 * configured".
 *
 * Global scope: reads each `globalConfigPaths()` candidate via `git config
 * --file <path> user.email` (round 2 fix — see that function's comment for
 * the full attack/verification writeup) INSTEAD OF `git config --global
 * user.email`, which round 2 proved is itself redirectable via
 * `GIT_CONFIG_GLOBAL`/`HOME`/`USERPROFILE`.
 *
 * TEST-ONLY ESCAPE HATCH (same shape as CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR
 * above): CLAUDE_AUTHOR_IDENTITY_TEST_FORCE_NO_CONFIGURED_EMAIL=1 makes this
 * return null immediately, skipping both scopes. This exists SOLELY because
 * SEC-052 round 2's fix deliberately made the real machine identity
 * un-spoofable by env vars — a test that wants to exercise "zero derivable
 * sanctioned identities" (AC-9) can no longer fake that by pointing
 * HOME/GIT_CONFIG_GLOBAL at an empty directory, since that is now exactly
 * the attack this fix defeats. This flag is SAFE to leave live in
 * production: it can only ever SHRINK the sanctioned set (never add or
 * substitute a value), so at worst it makes the check MORE restrictive —
 * there is no way for an attacker to leverage it to get a fabricated
 * identity accepted, only to (at most) get a legitimate one wrongly
 * flagged, which is the same fail-closed-safe direction as
 * CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR. */
function configuredEmail() {
  if (process.env.CLAUDE_AUTHOR_IDENTITY_TEST_FORCE_NO_CONFIGURED_EMAIL === '1') {
    return null;
  }
  try {
    const e = git(['config', '--local', 'user.email']);
    if (e) return e;
  } catch {
    /* no local value — fall through to global candidates */
  }
  for (const file of globalConfigPaths()) {
    try {
      const e = git(['config', '--file', file, 'user.email']);
      if (e) return e;
    } catch {
      /* file missing, or key unset in it — try the next candidate */
    }
  }
  return null;
}

/** The trunk branch to scan for source 2: `main` if it exists locally, else
 * `master`, else whatever HEAD currently is (a fresh/unborn repo has
 * neither). */
function trunkBranch() {
  for (const name of ['main', 'master']) {
    try {
      git(['show-ref', '--verify', '--quiet', `refs/heads/${name}`]);
      return name;
    } catch {
      /* try next */
    }
  }
  try {
    return git(['rev-parse', '--abbrev-ref', 'HEAD']);
  } catch {
    return null; // unborn HEAD — no commits yet
  }
}

/** ASM-226 (GR#15): resolve the history-scan CEILING. The operator-set env
 * var CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT (a positive integer) wins; anything
 * else — unset, non-numeric, or <= 0 — falls back to the documented default
 * THRESHOLDS.HISTORY_SCAN_LIMIT. This is a resource bound, not a validator's
 * expected value, so a default is the correct shape (see weakness pattern
 * "bound anything that sizes work"). */
function resolveScanLimitCeiling() {
  const override = Number.parseInt(process.env.CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT, 10);
  if (Number.isInteger(override) && override > 0) return override;
  return THRESHOLDS.HISTORY_SCAN_LIMIT;
}

/** ASM-226 (GR#15): derive the history-scan cap from the repo's ACTUAL
 * commit count at guard-run time instead of assuming a fixed 2000. The cap
 * is min(realCommitCount, ceiling): a young repo scans exactly the commits
 * that exist (never asking git for 2000 of 5), and a large/old repo is
 * still bounded by the ceiling (BUG-052's resource bound, preserved).
 *
 * THE COUNT IS TAKEN FROM THE TRUNK BRANCH BEING SCANNED (ASM-226 reject),
 * NOT HEAD: `git rev-list --count <trunkBranch()>`. historyEmails() scans
 * trunkBranch() (main, else master, else current HEAD), so the cap must be
 * derived from that SAME branch — deriving it from HEAD under-caps the scan
 * whenever HEAD is not a descendant of the trunk (orphan branch, detached
 * HEAD, or a branch whose base is behind main), which silently drops a
 * legitimate repeat committer out of the sanctioned set (a false-DENY
 * regression vs the pre-ASM-226 hardcoded 2000, which scanned
 * min(2000, trunk-actual)).
 *
 * FAIL-OPEN ON A FAILED DERIVATION: if the trunk cannot be resolved (unborn
 * HEAD, git failure) this returns the ceiling unchanged — never throws,
 * never scans zero. This module has no error surface by design (each
 * consumer decides fail-open vs fail-closed; see the module header), so a
 * failed derivation degrades to the documented default, not to a registry
 * error — the MET-* registry in data/errors.json is the Go app's error
 * path, and this JS hook's own contract is silent fail-open (AC-8 of the
 * FEAT-045 demotion). */
function deriveScanLimit() {
  const ceiling = resolveScanLimitCeiling();
  const branch = trunkBranch();
  if (!branch) return ceiling;
  let count;
  try {
    count = Number.parseInt(git(['rev-list', '--count', branch]), 10);
  } catch {
    return ceiling;
  }
  if (!Number.isInteger(count) || count <= 0) return ceiling;
  return Math.min(count, ceiling);
}

/** Source 2: emails appearing >= THRESHOLDS.HISTORY_THRESHOLD times (author
 * or committer) within the most recent deriveScanLimit() commits of the
 * trunk branch (the repo's real commit count, capped at
 * THRESHOLDS.HISTORY_SCAN_LIMIT — ASM-226). Empty set on a brand-new repo.
 *
 * See header: CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR=1 makes this throw instead
 * of catching, for test use only. */
function historyEmails() {
  if (process.env.CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR === '1') {
    throw new Error('CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR forced failure (test-only escape hatch)');
  }
  const branch = trunkBranch();
  if (!branch) return new Set();
  const limit = deriveScanLimit();
  let raw;
  try {
    raw = git(['log', branch, `--max-count=${limit}`, '--format=%ae%n%ce']);
  } catch {
    return new Set();
  }
  const counts = new Map();
  for (const line of raw.split(/\r?\n/)) {
    const key = line.trim().toLowerCase();
    if (!key) continue;
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  const out = new Set();
  for (const [email, count] of counts) {
    if (count >= THRESHOLDS.HISTORY_THRESHOLD) out.add(email);
  }
  return out;
}

/** Source 3: CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES, operator-set env var —
 * never read from any command text or commit content. */
function extraIdentities() {
  const raw = process.env.CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES || '';
  const out = new Set();
  for (const entry of raw.split(/[;,]/)) {
    const trimmed = entry.trim();
    if (!trimmed) continue;
    const emailMatch = /<([^<>]+)>/.exec(trimmed);
    const email = (emailMatch ? emailMatch[1] : trimmed).trim().toLowerCase();
    if (email) out.add(email);
  }
  return out;
}

/** Union of all three sources, lowercased. Throws if historyEmails() throws
 * (see the test-only escape hatch above) — deliberately NOT caught here, so
 * each consumer decides its own fail-open/fail-closed posture rather than
 * this shared module silently picking one for both. */
function deriveSanctioned() {
  const set = new Set();
  const cfg = configuredEmail();
  if (cfg) set.add(cfg.trim().toLowerCase());
  for (const e of historyEmails()) set.add(e);
  for (const e of extraIdentities()) set.add(e);
  return set;
}

module.exports = {
  THRESHOLDS,
  configuredEmail,
  trunkBranch,
  deriveScanLimit,
  historyEmails,
  extraIdentities,
  deriveSanctioned,
};
