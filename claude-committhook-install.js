// Module key: tool.committhookinstall (see code.json; GUID 634fa535-7bfd-400c-a773-b6a68722e0b2)
// Spec ref: M0-ENG §5 (hooks)

/**
 * claude-committhook-install.js — install & verify the tracked commit-msg
 * git hook (FEAT-045, deliverable 3). See githooks/commit-msg for the
 * enforcing hook itself and its header for the hook-point rationale,
 * fail-closed posture, and the `--no-verify` residual gap (AC-15).
 *
 * THREE STATES (AC-13) — deliberately never collapsed into one another,
 * because "silently unprotected" is the exact failure mode this exists to
 * prevent (a hook nobody notices is missing protects nothing):
 *
 *   healthy — .git/hooks/commit-msg exists and matches the tracked source
 *             (githooks/commit-msg) byte-for-byte.
 *   stale   — .git/hooks/commit-msg exists but does NOT match the tracked
 *             source (e.g. after a `git pull` moved the canonical file on
 *             and the installed copy did not follow).
 *   absent  — .git/hooks/commit-msg does not exist at all. Commits on this
 *             machine are NOT identity-protected by this control. An
 *             existence check ALONE cannot distinguish this from "healthy"
 *             or "stale" — this module always reads and compares content,
 *             never just checks that *a* file is present (AC-13's own
 *             "lazy implementation" trap: an existing-but-hand-edited or
 *             leftover file must read as "stale", not "healthy").
 *
 * COPY, NOT SYMLINK (AC-12). A symlink is the simpler mechanism, but symlink
 * creation on Windows needs elevated privileges or Developer Mode enabled by
 * default — a real deployability concern on this project's own
 * documented-Windows environment (CLAUDE.md). A copy avoids that, at the
 * cost of needing an explicit "did the source move on and the copy go
 * stale" check — which is exactly what verify()/summaryLine() below provide,
 * so the tradeoff is not a silent one.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

// BUG-340/BUG-336 (deliverable 3): generalised from a single hardcoded
// commit-msg install to a small TABLE of tracked hooks, so pre-push (the
// GR#28 fast floor gate) installs through the exact same
// copy-not-symlink / healthy-stale-absent machinery, never a second,
// hand-duplicated install path. commit-msg's own behaviour (paths, state
// semantics) is UNCHANGED — see the AC-12/AC-13 tests, which still pass
// unmodified against this generalised shape.
const HOOKS = {
  'commit-msg': {
    label: 'COMMIT-MSG IDENTITY HOOK',
    source: path.join(__dirname, 'githooks', 'commit-msg'),
  },
  'pre-push': {
    label: 'PRE-PUSH GR#28 FLOOR GATE',
    source: path.join(__dirname, 'githooks', 'pre-push'),
  },
};

// Back-compat: the original single-hook export name, still pointing at
// commit-msg specifically — existing callers (claude-startup.js's session
// summary, this file's own pre-existing tests) keep working unchanged.
const CANONICAL_SOURCE = HOOKS['commit-msg'].source;

// BUG-340 r1 F7(b) (independent round REJECT): a git WORKTREE (this repo's
// own `.claude/worktrees/<name>` lanes included) has `<repoRoot>/.git` as a
// FILE (containing `gitdir: <path-to-the-real-.git-dir>`), not a directory —
// `path.join(repoRoot, '.git', 'hooks')` then resolves to a path UNDER a
// FILE, which never exists, so install()/verify() silently operate on a
// hooks dir hooks NEVER actually fire from (git resolves hooks against the
// COMMON git dir, shared across every worktree of the same repo) and
// claude-startup.js's session summary printed false "ABSENT" lines for
// every worktree lane. `git rev-parse --git-common-dir` is git's own
// authoritative answer for "where do hooks for THIS checkout actually live"
// — it returns the MAIN checkout's .git dir even when invoked from a
// worktree, exactly the worktree-aware answer needed here. Falls back to
// the plain `<repoRoot>/.git/hooks` guess (unchanged legacy behaviour) when
// `repoRoot` is not a real git repo at all (e.g. this file's own throwaway
// test fixtures, which never `git init`) — git-common-dir throws there, and
// the fallback keeps every pre-existing test passing unmodified.
function hooksDir(repoRoot) {
  try {
    const out = execFileSync('git', ['rev-parse', '--git-common-dir'], {
      cwd: repoRoot, encoding: 'utf8', timeout: 5000, stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    if (out) {
      const gitDir = path.isAbsolute(out) ? out : path.resolve(repoRoot, out);
      return path.join(gitDir, 'hooks');
    }
  } catch {
    /* not a git repo (or git unavailable) — fall through to the legacy guess */
  }
  return path.join(repoRoot, '.git', 'hooks');
}

function installedPath(repoRoot, hookName = 'commit-msg') {
  return path.join(hooksDir(repoRoot), hookName);
}

function readCanonical(hookName = 'commit-msg') {
  const hook = HOOKS[hookName];
  if (!hook) throw new Error(`unknown hook "${hookName}" (known: ${Object.keys(HOOKS).join(', ')})`);
  return fs.readFileSync(hook.source);
}

/** Copies the tracked canonical source for `hookName` into
 * `<repoRoot>/.git/hooks/<hookName>` and makes it executable (POSIX;
 * best-effort no-op on Windows — see the original AC-12 comment: Git for
 * Windows dispatches via its bundled shell reading the shebang line
 * regardless of the exec bit). Returns the installed path. */
function install(repoRoot, hookName = 'commit-msg') {
  const dir = hooksDir(repoRoot);
  fs.mkdirSync(dir, { recursive: true });
  const dest = installedPath(repoRoot, hookName);
  const canonical = readCanonical(hookName);

  // BUG-340 r1 F7(a) (independent round REJECT): NEVER silently clobber an
  // existing file at `dest` whose content differs from what we are about to
  // write — it could be a hand-written custom hook (a developer's own
  // pre-push/commit-msg script), and overwriting it with zero trace would
  // destroy that work exactly the way GR#24 ("No Code Left Behind") forbids
  // for the working tree. Back it up to `<dest>.bak-<timestamp>` and warn
  // LOUDLY before overwriting. Skipped when the existing file is ALREADY
  // byte-identical to canonical (a plain re-install / idempotent re-run) —
  // that is not a loss of anything, and backing it up every time would spam
  // `.bak-*` files on every routine reinstall.
  if (fs.existsSync(dest)) {
    let existing = null;
    try {
      existing = fs.readFileSync(dest);
    } catch {
      /* unreadable existing file — treat as "nothing to preserve", fall through to overwrite */
    }
    if (existing && !(existing.length === canonical.length && Buffer.compare(existing, canonical) === 0)) {
      const backupPath = `${dest}.bak-${Date.now()}`;
      fs.writeFileSync(backupPath, existing);
      // eslint-disable-next-line no-console
      console.warn(
        `WARNING: ${dest} already existed with DIFFERENT content (a custom hook, or a stale install) — ` +
          `backed up to ${backupPath} before installing the tracked ${hookName} source. Review the backup ` +
          'if it carried logic you need to preserve.'
      );
    }
  }

  fs.writeFileSync(dest, canonical);
  try {
    fs.chmodSync(dest, 0o755);
  } catch {
    /* Windows: see comment above — not a failure worth reporting. */
  }
  return dest;
}

/** Installs EVERY tracked hook in HOOKS. Returns { hookName: destPath }. */
function installAll(repoRoot) {
  const result = {};
  for (const hookName of Object.keys(HOOKS)) {
    result[hookName] = install(repoRoot, hookName);
  }
  return result;
}

/** Returns { state: 'healthy'|'stale'|'absent', path, note? } for `hookName`.
 * Never throws on a missing/unreadable installed file — that is the
 * "absent" state, not an error this function propagates. */
function verify(repoRoot, hookName = 'commit-msg') {
  const dest = installedPath(repoRoot, hookName);
  if (!fs.existsSync(dest)) {
    return { state: 'absent', path: dest };
  }
  let installed;
  try {
    installed = fs.readFileSync(dest);
  } catch (err) {
    // Present but unreadable (permissions, etc.) is functionally the same
    // as absent from this project's point of view: it cannot be relied on
    // to run, so it must not be reported as "healthy".
    return { state: 'absent', path: dest, note: `unreadable: ${err.message}` };
  }
  const canonical = readCanonical(hookName);
  const matches = installed.length === canonical.length && Buffer.compare(installed, canonical) === 0;
  return { state: matches ? 'healthy' : 'stale', path: dest };
}

/** { hookName: verify() result } for every tracked hook. */
function verifyAll(repoRoot) {
  const result = {};
  for (const hookName of Object.keys(HOOKS)) {
    result[hookName] = verify(repoRoot, hookName);
  }
  return result;
}

/** One human-readable line naming the state explicitly — AC-13's
 * "absent"/"stale" distinction must reach a human as TEXT, not merely a
 * non-zero exit code (a session-start summary has nothing to print from an
 * exit code alone). Used both by the CLI below and by claude-startup.js's
 * unconditional session-start output (AC-14). */
function summaryLine(repoRoot, hookName = 'commit-msg') {
  const hook = HOOKS[hookName];
  const label = hook ? hook.label : hookName.toUpperCase();
  const result = verify(repoRoot, hookName);
  if (result.state === 'healthy') {
    return `${label}: healthy (installed, matches tracked source).`;
  }
  if (result.state === 'stale') {
    return (
      `${label}: STALE — installed copy does not match the tracked source ` +
      `(${hook ? hook.source : hookName}). Commits/pushes may be running an ` +
      'outdated check. Re-run: node claude-committhook-install.js install'
    );
  }
  // BUG-340/BUG-336: commit-msg keeps its ORIGINAL exact wording ("NOT
  // identity-protected") — claude-committhook-install.test.js's AC-14
  // behavioral test asserts this literal phrase from claude-startup.js's
  // captured output, and that assertion predates this generalisation. Every
  // OTHER hook (pre-push today) gets a hook-appropriate phrasing instead of
  // a copy-pasted "identity-protected" that would be wrong for it.
  if (hookName === 'commit-msg') {
    return (
      `${label}: ABSENT — commits on this machine are NOT identity-protected by githooks/commit-msg. Run: ` +
      'node claude-committhook-install.js install'
    );
  }
  return (
    `${label}: ABSENT — this machine is NOT protected by githooks/${hookName}. Run: ` +
    'node claude-committhook-install.js install'
  );
}

/** All tracked hooks' summary lines, one per line — the shape
 * claude-startup.js's unconditional session summary wants (every hook's
 * state surfaced, never just the first one). */
function summaryLines(repoRoot) {
  return Object.keys(HOOKS).map((hookName) => summaryLine(repoRoot, hookName));
}

module.exports = {
  HOOKS,
  CANONICAL_SOURCE,
  hooksDir,
  installedPath,
  install,
  installAll,
  verify,
  verifyAll,
  summaryLine,
  summaryLines,
};

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

if (require.main === module) {
  const [, , cmd, repoRootArg] = process.argv;
  const repoRoot = repoRootArg || __dirname;

  if (cmd === 'install') {
    const installed = installAll(repoRoot);
    for (const [hookName, dest] of Object.entries(installed)) {
      console.log(`Installed ${hookName} hook to ${dest}`);
    }
    for (const line of summaryLines(repoRoot)) console.log(line);
  } else if (cmd === 'verify') {
    const all = verifyAll(repoRoot);
    for (const line of summaryLines(repoRoot)) console.log(line);
    process.exitCode = Object.values(all).every((r) => r.state === 'healthy') ? 0 : 1;
  } else {
    console.error('Usage: node claude-committhook-install.js <install|verify> [repoRoot]');
    process.exitCode = 2;
  }
}
