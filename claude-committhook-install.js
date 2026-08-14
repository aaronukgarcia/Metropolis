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

const CANONICAL_SOURCE = path.join(__dirname, 'githooks', 'commit-msg');

function hooksDir(repoRoot) {
  return path.join(repoRoot, '.git', 'hooks');
}

function installedPath(repoRoot) {
  return path.join(hooksDir(repoRoot), 'commit-msg');
}

function readCanonical() {
  return fs.readFileSync(CANONICAL_SOURCE);
}

/** Copies the tracked canonical source into `<repoRoot>/.git/hooks/commit-msg`
 * and makes it executable (POSIX; a best-effort no-op on Windows, which does
 * not gate git-hook execution on the POSIX exec bit the way native POSIX git
 * does — Git for Windows dispatches via its bundled shell reading the
 * shebang line regardless of the exec bit). Returns the installed path. */
function install(repoRoot) {
  const dir = hooksDir(repoRoot);
  fs.mkdirSync(dir, { recursive: true });
  const dest = installedPath(repoRoot);
  fs.writeFileSync(dest, readCanonical());
  try {
    fs.chmodSync(dest, 0o755);
  } catch {
    /* Windows: see comment above — not a failure worth reporting. */
  }
  return dest;
}

/** Returns { state: 'healthy'|'stale'|'absent', path, note? }. Never throws
 * on a missing/unreadable installed file — that is the "absent" state, not
 * an error this function propagates. */
function verify(repoRoot) {
  const dest = installedPath(repoRoot);
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
  const canonical = readCanonical();
  const matches = installed.length === canonical.length && Buffer.compare(installed, canonical) === 0;
  return { state: matches ? 'healthy' : 'stale', path: dest };
}

/** One human-readable line naming the state explicitly — AC-13's
 * "absent"/"stale" distinction must reach a human as TEXT, not merely a
 * non-zero exit code (a session-start summary has nothing to print from an
 * exit code alone). Used both by the CLI below and by claude-startup.js's
 * unconditional session-start output (AC-14). */
function summaryLine(repoRoot) {
  const result = verify(repoRoot);
  if (result.state === 'healthy') {
    return 'COMMIT-MSG IDENTITY HOOK: healthy (installed, matches tracked source).';
  }
  if (result.state === 'stale') {
    return (
      'COMMIT-MSG IDENTITY HOOK: STALE — installed copy does not match the ' +
      `tracked source (${CANONICAL_SOURCE}). Commits may be running an ` +
      'outdated check. Re-run: node claude-committhook-install.js install'
    );
  }
  return (
    'COMMIT-MSG IDENTITY HOOK: ABSENT — commits on this machine are NOT ' +
    'identity-protected by githooks/commit-msg. Run: ' +
    'node claude-committhook-install.js install'
  );
}

module.exports = {
  CANONICAL_SOURCE,
  hooksDir,
  installedPath,
  install,
  verify,
  summaryLine,
};

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

if (require.main === module) {
  const [, , cmd, repoRootArg] = process.argv;
  const repoRoot = repoRootArg || __dirname;

  if (cmd === 'install') {
    const dest = install(repoRoot);
    console.log(`Installed commit-msg hook to ${dest}`);
    console.log(summaryLine(repoRoot));
  } else if (cmd === 'verify') {
    console.log(summaryLine(repoRoot));
    process.exitCode = verify(repoRoot).state === 'healthy' ? 0 : 1;
  } else {
    console.error('Usage: node claude-committhook-install.js <install|verify> [repoRoot]');
    process.exitCode = 2;
  }
}
