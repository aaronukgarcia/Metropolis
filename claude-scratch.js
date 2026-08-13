#!/usr/bin/env node
/**
 * claude-scratch.js — FEAT-058: one-command scratch-copy helper.
 *
 * WHY THIS EXISTS (read before reaching for `git stash`):
 * `git stash` is BANNED project-wide (dev-team-process.md v1.5.1) — it is an
 * easy reach for an agent trying to snapshot uncommitted work before doing
 * something risky, but it operates on the ENTIRE working tree, not just the
 * caller's files. With several concurrent agents live, one stash sweeps away
 * every other agent's uncommitted work, and popping it back is a merge, not
 * an undo. `git archive` looks like the safe alternative but silently drops
 * uncommitted work — it only archives committed content, so it lies about
 * what it captured. Three agents lost time to this exact gap (BOW FEAT-058).
 * This tool is the sanctioned "snapshot my current mess" operation: a pure
 * filesystem copy, driven by `git status --porcelain` (git's own ignore
 * engine decides what ".gitignore" means — this file does not re-parse
 * ignore rules itself, per GR#3), that NEVER touches git state — no stash,
 * no index writes, no ref creation, no commits, ever.
 *
 * USAGE:
 *   node claude-scratch.js snapshot
 *     Copies every tracked-but-modified file and every untracked file
 *     (honouring .gitignore) from the current working tree into a fresh
 *     timestamped folder under .scratch/, then prints the destination path
 *     to stdout on success.
 *
 * Exit codes: 0 on success. 1 on any failure — not a git repository, git
 * not on PATH, `git status` itself reporting a warning/error partway
 * through enumeration (e.g. a path too long for it to open — see
 * `getChangedPaths`), the destination folder cannot be created, or a source
 * file cannot be read mid-copy. Failures abort the ENTIRE run and
 * best-effort remove whatever partial destination folder was created —
 * this tool never reports success on a half-copied snapshot (GR#1).
 *
 * KNOWN LIMITATION (disclosed, not silently accepted): a directory
 * junction/reparse point inside the working tree is detected and SKIPPED
 * with a loud stderr warning, never followed — see `pathCrossesReparsePoint`
 * — so its content (which may live entirely outside this repository) is
 * never copied. `pathCrossesReparsePoint` walks every segment of the path,
 * including the FINAL one, so both an ancestor directory junction and a
 * leaf-level reparse point (a symlinked/junctioned file or directory that
 * is itself the thing being copied, not merely reached through one) are
 * detected by the same `isSymbolicLink()` mechanism, regardless of whether
 * the caller's path string has a trailing slash. This was verified
 * end-to-end against real Windows junctions in both the ancestor and leaf
 * positions; a plain file-level symlink was NOT independently verified
 * end-to-end (this project's accounts lack symlink-creation privilege on
 * this platform) — it is expected to behave identically because it goes
 * through the identical `lstatSync().isSymbolicLink()` check with no
 * junction-specific branching, but that expectation remains unproven by a
 * real file-symlink test on this account.
 *
 * DISCLOSED RESIDUAL (BUG-147, narrowed not eliminated): the reparse-point
 * check and the actual file read are two separate syscalls, not one atomic
 * operation, so there is a real (if narrow) window in which a concurrent
 * process — this tool's own stated threat model is several concurrent
 * agents live in the same working tree — can swap an ancestor directory
 * for a junction between the check and the read. Node exposes no
 * `O_NOFOLLOW` on Windows, so the window can't be closed outright;
 * `copyPaths` instead re-runs the same check immediately after each copy
 * and discards+reports any file whose path shows a reparse point at that
 * point, catching the case where the junction is still present right
 * after the read. This is a real narrowing, not a full fix: an attacker
 * who swaps the junction in, lets the copy read through it, and swaps
 * back before the post-copy re-check runs would still evade detection.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { execFileSync, spawnSync } = require('child_process');

/** Thrown for any fatal condition; caught once at the CLI boundary. */
class ScratchError extends Error {}

/**
 * Resolve the git repository root for `cwd`. Throws ScratchError if `cwd`
 * is not inside a git working tree or git is not on PATH.
 */
function getRepoRoot(cwd) {
  let out;
  try {
    out = execFileSync('git', ['rev-parse', '--show-toplevel'], {
      cwd,
      encoding: 'utf8',
    });
  } catch (err) {
    throw new ScratchError(
      `not a git repository (or git is not on PATH): ${err.message}`
    );
  }
  return path.normalize(out.trim());
}

/**
 * Matches git's own conventional stderr prefixes for a warning or error
 * line (e.g. "warning: could not open directory '...': Filename too long").
 * Deliberately NOT a literal match on any one specific message (GR#15) —
 * matching the prefix convention catches every flavour of "git silently
 * couldn't enumerate part of the tree", not just the long-path case this
 * check was born from. `m` flag so it matches after embedded newlines too.
 */
const GIT_STDERR_WARNING_RE = /^\s*(warning|error):/im;

/**
 * Return the list of repo-root-relative paths that must be copied: every
 * tracked-but-modified file and every untracked file, exactly as reported
 * by `git status --porcelain=v1 -z -uall --no-renames`. Ignored files never
 * appear in that output at all — this is how ".gitignore" is honoured,
 * without this file re-implementing any ignore-pattern parsing (GR#3).
 *
 * Deleted files (staged or unstaged) are reported by git status but have
 * nothing on disk to copy, so they are skipped rather than treated as an
 * error — a deletion is a legitimate part of "uncommitted state".
 *
 * Defence in depth: paths under `.scratch/` are always excluded, even if a
 * caller's .gitignore does not (yet) list it — this tool must never copy
 * its own previous output into a new snapshot.
 *
 * P0 FIX (Destructive round 1, 2026-08-11): on this platform, an untracked
 * file buried deep enough (~850+ char path) makes `git status` itself emit
 * a STDERR warning ("warning: could not open directory '...': Filename too
 * long") and return EMPTY stdout for that subtree — exit code is still 0,
 * because it's a warning, not a git failure. Trusting stdout/exit-code
 * alone (the previous behaviour) meant this tool could not tell "genuinely
 * nothing changed" apart from "git silently couldn't enumerate part of the
 * tree", and reported success on an incomplete/empty snapshot — the EXACT
 * failure mode (silent data loss reported as success) this tool exists to
 * prevent (see file header: `git archive` does the same thing for
 * uncommitted work). So: stderr is now captured and inspected, and ANY
 * warning/error-shaped line is fatal (ScratchError), not just the specific
 * "Filename too long" wording — per GR#1's posture for this tool, ambiguous
 * input must never be reported as success. `spawnSync` (not
 * `execFileSync`) is used here specifically because it exposes stdout and
 * stderr as separate fields regardless of exit code; `execFileSync` only
 * returns stdout on success and does not give the caller a way to inspect
 * stderr when the process exits 0.
 */
function getChangedPaths(repoRoot, { spawnSyncFn = spawnSync } = {}) {
  const result = spawnSyncFn(
    'git',
    ['status', '--porcelain=v1', '-z', '-uall', '--no-renames'],
    { cwd: repoRoot, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 }
  );

  if (result.error) {
    throw new ScratchError(`git status failed: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new ScratchError(
      `git status exited with code ${result.status}: ${result.stderr || '(no stderr)'}`
    );
  }
  if (result.stderr && GIT_STDERR_WARNING_RE.test(result.stderr)) {
    throw new ScratchError(
      `git status reported a warning/error while enumerating the working ` +
        `tree — the resulting file list may be incomplete, so this run is ` +
        `refused rather than risking a silent partial snapshot: ${result.stderr.trim()}`
    );
  }

  const raw = result.stdout;
  const entries = raw.split('\0').filter((e) => e.length > 0);
  const paths = [];
  for (const entry of entries) {
    // Porcelain v1 format: two status chars, one space, then the path.
    const statusCode = entry.slice(0, 2);
    const relPath = entry.slice(3);
    if (!relPath) continue;

    // A deletion (index or worktree side) has no file left to copy.
    if (statusCode.includes('D')) continue;

    // Never re-copy our own output directory.
    if (relPath === '.scratch' || relPath.startsWith('.scratch/')) continue;

    paths.push(relPath);
  }
  return paths;
}

/** Zero-pad `n` to `width` digits. */
function pad(n, width) {
  return String(n).padStart(width, '0');
}

/**
 * Local-time timestamp folder name, e.g. "2026-08-11T161200".
 * ASM: local time (not UTC) and no colons (Windows path legality) — see
 * ASM-xxx logged against this file for the reasoning and what breaks if a
 * caller expected UTC or ISO-8601-with-colons instead.
 */
function formatTimestamp(date) {
  const yyyy = date.getFullYear();
  const mm = pad(date.getMonth() + 1, 2);
  const dd = pad(date.getDate(), 2);
  const HH = pad(date.getHours(), 2);
  const MM = pad(date.getMinutes(), 2);
  const SS = pad(date.getSeconds(), 2);
  return `${yyyy}-${mm}-${dd}T${HH}${MM}${SS}`;
}

/**
 * Pick a destination folder under `scratchRoot` for `date`, disambiguating
 * with a "-2", "-3", ... suffix if two runs land in the same second.
 *
 * NOT race-safe on its own: this is a plain existence check, with no
 * guarantee that nothing creates `candidate` between this function
 * returning and the caller creating it. Kept (and still tested) as a pure
 * "what name would I try next" helper; `runSnapshot` itself no longer uses
 * this function to select AND create the destination — see `createDestDir`
 * below, which closes the gap with an atomic create-or-retry loop.
 */
function pickDestDir(scratchRoot, date) {
  const base = formatTimestamp(date);
  let candidate = path.join(scratchRoot, base);
  let n = 2;
  while (fs.existsSync(candidate)) {
    candidate = path.join(scratchRoot, `${base}-${n}`);
    n += 1;
  }
  return candidate;
}

/**
 * Atomically reserve AND create a fresh destination folder under
 * `scratchRoot` for `date`, disambiguating with a "-2", "-3", ... suffix on
 * collision. Returns the created absolute path.
 *
 * P0 FIX (Destructive round 1, 2026-08-11): the previous approach —
 * `fs.existsSync(candidate)` to probe for a free name, then a separate
 * `fs.mkdirSync(dest, {recursive: true})` to create it — has a TOCTOU gap
 * between the check and the create. `mkdirSync({recursive: true})` does
 * NOT throw if the target already exists, so two concurrent invocations of
 * this tool could both probe, both see "free", and both write into the
 * IDENTICAL destination, silently overwriting each other's copied files
 * with zero error to either caller. Proven live with 6 concurrent
 * processes: two independent pairs genuinely collided on the same folder.
 *
 * The fix: `fs.mkdirSync(candidate, {recursive: false})` — NOT recursive —
 * throws `EEXIST` if the directory already exists. That's exactly the
 * atomic "claim this name or find out someone beat me to it" primitive
 * needed here: on EEXIST, advance to the next `-n` candidate and retry;
 * any other error is fatal. There is no window between "is it free" and
 * "make it mine" because those are now the same syscall.
 *
 * The PARENT (`scratchRoot`, i.e. `.scratch/` itself) is still created
 * with `{recursive: true}` — that's safe here because creating a shared
 * parent directory is idempotent (concurrent callers all just want it to
 * exist, no data is written directly into it, and "already exists" for the
 * parent is not a collision, unlike the per-run leaf folder which two
 * runs must never actually share).
 */
function createDestDir(scratchRoot, date) {
  try {
    fs.mkdirSync(scratchRoot, { recursive: true });
  } catch (err) {
    throw new ScratchError(
      `could not create scratch root "${scratchRoot}": ${err.message}`
    );
  }

  const base = formatTimestamp(date);
  let candidate = path.join(scratchRoot, base);
  let n = 2;
  for (;;) {
    try {
      fs.mkdirSync(candidate, { recursive: false });
      return candidate;
    } catch (err) {
      if (err.code === 'EEXIST') {
        candidate = path.join(scratchRoot, `${base}-${n}`);
        n += 1;
        continue;
      }
      throw new ScratchError(
        `could not create destination folder "${candidate}": ${err.message}`
      );
    }
  }
}

/**
 * True if ANY segment of `relPath` — every ancestor directory AND the
 * final path component (the leaf) itself — is a symbolic link or Windows
 * junction/reparse point, when resolved under `repoRoot`.
 *
 * P1 FIX (Destructive round 1, 2026-08-11): `git status` happily enumerates
 * untracked content reached through a junction as ordinary untracked
 * files, and a naive copy follows right along — pulling content from
 * OUTSIDE the repository entirely into `.scratch/`, breaking this tool's
 * "only ever touches this repo's own uncommitted state" trust boundary.
 * `fs.lstatSync(...).isSymbolicLink()` reports true for both real symlinks
 * and Windows junctions (junctions are reparse points Node's fs layer
 * surfaces the same way), so a single check covers both without needing
 * elevated privileges to detect (only to *create* a non-junction symlink
 * requires that on Windows — detection needs none).
 *
 * P1 FIX round 2 (Destructive round 2, 2026-08-11): the original version
 * only walked `relPath.split(/[\\/]/).slice(0, -1)` — the ANCESTOR segments
 * — and never lstat'd the leaf itself. That made a reparse point that IS
 * the leaf (most importantly a genuine file-level symlink) invisible to
 * this check entirely: `pathCrossesReparsePoint(root, 'linked_repo')`
 * returned `false` while `pathCrossesReparsePoint(root, 'linked_repo/')`
 * returned `true` for the identical target, purely because the trailing
 * slash produced an extra empty split segment that happened to land the
 * leaf inside the "ancestor" list. The one existing test only passed
 * because git's porcelain format happens to append a trailing `/` for
 * directory-shaped untracked entries — an accident of git's output
 * format, not this function's own logic. Fixed by walking every non-empty
 * segment (ancestors AND leaf) instead of slicing off the last one, using
 * `.filter(Boolean)` so a trailing slash (or backslash) produces no
 * spurious segment either way — the result is now independent of how the
 * caller formats the path.
 */
function pathCrossesReparsePoint(repoRoot, relPath) {
  const segments = relPath.split(/[\\/]/).filter(Boolean);
  let current = repoRoot;
  for (const segment of segments) {
    current = path.join(current, segment);
    let st;
    try {
      st = fs.lstatSync(current);
    } catch (_err) {
      // Can't stat an intermediate dir — let the actual copy attempt
      // surface that failure naturally rather than guessing here.
      return false;
    }
    if (st.isSymbolicLink()) return true;
  }
  return false;
}

/**
 * Bounded-retry wrapper around `fs.rmSync(target, {force: true})`, for the
 * one call site (the BUG-147 post-copy discard, below) where "the delete
 * itself failed" must never be swallowed as best-effort.
 *
 * BUG-149 FIX (Destructive round 2, 2026-08-13): the discard call used to be
 * a bare try/catch that ate ANY rmSync failure — including a real, non-
 * transient one — and then proceeded exactly as if the delete had
 * succeeded: `skipped.push(relPath)` and a stderr message claiming "Not
 * trusted; not kept." even when the untrusted copy was still sitting on
 * disk. Live-verified: an EPERM injected on the discard rmSync left the
 * malicious file present while every signal the tool gave (return value,
 * stderr text) said the opposite.
 *
 * This project's own established pattern for a transient filesystem failure
 * (see `restoreWithRetry` in claude-plan-checker.test.js and
 * claude-secret-checker.test.js — a short bounded retry with a synchronous
 * busy-wait, since this is a sync call chain with no sleep primitive
 * available) is reused here rather than inventing a new shape: a locked
 * file, an AV scanner, or NTFS metadata lag can all make a delete fail
 * transiently and succeed moments later, so a few short retries absorb that
 * without masking a genuine failure. If every attempt fails, this throws —
 * unlike the old silent catch, a persistent failure to discard untrusted
 * content is NOT best-effort-and-move-on; it is fatal, because "not kept"
 * would otherwise be reported when it plainly was.
 */
function rmSyncWithRetry(target, attempts = 5, delayMs = 20) {
  let lastErr;
  for (let i = 1; i <= attempts; i++) {
    try {
      fs.rmSync(target, { force: true });
      return;
    } catch (err) {
      lastErr = err;
      const until = Date.now() + delayMs;
      while (Date.now() < until) { /* brief busy-wait; no sleep primitive in sync context */ }
    }
  }
  throw lastErr;
}

/**
 * Copy every path in `relPaths` from `repoRoot` into `destDir`, preserving
 * relative directory structure. Throws ScratchError on the first failure —
 * this function does not attempt to continue past a bad file, so the
 * result is never a silently-partial copy (GR#1). Caller is responsible
 * for cleaning up a partial `destDir` on error.
 *
 * Paths reached through a directory junction/reparse point are a deliberate
 * exception to "copy everything git reports": they are SKIPPED (not
 * copied, not a fatal error) with a loud warning to stderr naming what was
 * skipped and why (P1 fix — see `pathCrossesReparsePoint`). Skipping,
 * rather than throwing, keeps the tool usable in a tree that happens to
 * contain a junction elsewhere; a loud disclosed warning, rather than
 * silent inclusion or silent exclusion, is what keeps this in line with
 * GR#1's "never claim success on ambiguous input" without turning an
 * unrelated junction into an outright run failure for every other file.
 *
 * Returns the list of relPaths that were skipped this way, so callers
 * (and tests) can assert on it directly rather than scraping stderr.
 */
function copyPaths(repoRoot, relPaths, destDir) {
  const skipped = [];
  for (const relPath of relPaths) {
    if (pathCrossesReparsePoint(repoRoot, relPath)) {
      skipped.push(relPath);
      process.stderr.write(
        `WARNING: skipping "${relPath}" — its path passes through a ` +
          `directory junction/reparse point, so its content lives outside ` +
          `this repository's own working tree. This tool only copies the ` +
          `repo's own uncommitted state; the junction target was NOT ` +
          `followed or copied.\n`
      );
      continue;
    }
    const src = path.join(repoRoot, relPath);
    const dest = path.join(destDir, relPath);
    try {
      fs.mkdirSync(path.dirname(dest), { recursive: true });
      fs.copyFileSync(src, dest);
    } catch (err) {
      throw new ScratchError(`failed to copy "${relPath}": ${err.message}`);
    }
    // BUG-147: the check above and copyFileSync are two separate syscalls,
    // not one atomic operation — a concurrent process can swap an ancestor
    // directory for a junction in the real (if narrow) window between them,
    // and copyFileSync will follow it. Node exposes no O_NOFOLLOW on
    // Windows (fs.constants.O_NOFOLLOW is undefined here), so the window
    // can't be closed outright; instead, re-run the same check immediately
    // after the copy. If a reparse point is present on the path NOW, the
    // content that was just read may have come from outside the repo —
    // discard it and report it the same way a pre-copy detection would,
    // rather than trusting a copy whose source identity can no longer be
    // vouched for. This narrows the exploitable window from "the whole
    // loop iteration" to "the fixed cost of one copyFileSync call" and
    // catches the case (the one live-demonstrated) where the junction is
    // still present right after the read completes.
    if (pathCrossesReparsePoint(repoRoot, relPath)) {
      // BUG-149: the discard MUST actually succeed before the tool is
      // allowed to claim "not kept" — a caught-but-swallowed rmSync failure
      // here previously left the untrusted copy on disk while reporting the
      // opposite. rmSyncWithRetry absorbs transient failures (lock/AV/NTFS
      // lag) and, if the file genuinely could not be removed, throws —
      // which this call deliberately does NOT catch, so it propagates as a
      // fatal ScratchError (via runSnapshot's existing catch-and-cleanup),
      // never as a silent "skipped".
      try {
        rmSyncWithRetry(dest);
      } catch (err) {
        throw new ScratchError(
          `failed to discard untrusted copy of "${relPath}" after a ` +
            `directory junction/reparse point appeared on its path during ` +
            `the copy (a TOCTOU race, BUG-147): the malicious/outside-repo ` +
            `content could NOT be removed from the scratch destination ` +
            `(${err.message}). This snapshot cannot be trusted as clean — ` +
            `aborting the run rather than reporting a false "not kept".`
        );
      }
      skipped.push(relPath);
      process.stderr.write(
        `WARNING: discarding "${relPath}" — a directory junction/reparse ` +
          `point appeared on its path during the copy (a TOCTOU race, ` +
          `BUG-147), so the content just copied may have come from ` +
          `outside this repository. Not trusted; not kept.\n`
      );
    }
  }
  return skipped;
}

/**
 * Run a full snapshot: resolve the repo root, compute the changed-path
 * list, create a fresh timestamped destination under `scratchRootName`
 * (default ".scratch"), and copy every path into it. Returns the absolute
 * destination path on success. On any failure, best-effort removes
 * whatever partial destination it had created and re-throws.
 *
 * `cwd` and `now` are injectable for tests; production use defaults to the
 * real process cwd and the real clock.
 */
function runSnapshot({ cwd = process.cwd(), now = new Date() } = {}) {
  const repoRoot = getRepoRoot(cwd);
  const scratchRoot = path.join(repoRoot, '.scratch');
  const relPaths = getChangedPaths(repoRoot);
  // Atomic create-or-retry (P0 fix) — see createDestDir's doc comment.
  const destDir = createDestDir(scratchRoot, now);

  try {
    copyPaths(repoRoot, relPaths, destDir);
  } catch (err) {
    // Never leave a half-copied snapshot on disk pretending to be complete.
    try {
      fs.rmSync(destDir, { recursive: true, force: true });
    } catch (_cleanupErr) {
      // Best-effort only — the original error is what matters to the caller.
    }
    throw err;
  }

  return destDir;
}

function printUsage() {
  process.stderr.write(
    [
      'Usage: node claude-scratch.js snapshot',
      '',
      '  Copies the working tree\'s uncommitted state (tracked-modified +',
      '  untracked, honouring .gitignore) into a fresh timestamped folder',
      '  under .scratch/, then prints the destination path. Never touches',
      '  git state — no stash, no index, no refs, no commits.',
      '',
    ].join('\n')
  );
}

function main(argv) {
  const subcommand = argv[2];
  if (subcommand !== 'snapshot') {
    printUsage();
    process.exitCode = 1;
    return;
  }

  try {
    const dest = runSnapshot();
    process.stdout.write(dest + '\n');
  } catch (err) {
    process.stderr.write(`ERROR: ${err.message}\n`);
    process.exitCode = 1;
  }
}

if (require.main === module) {
  main(process.argv);
}

module.exports = {
  ScratchError,
  getRepoRoot,
  getChangedPaths,
  formatTimestamp,
  pickDestDir,
  createDestDir,
  pathCrossesReparsePoint,
  copyPaths,
  runSnapshot,
};
