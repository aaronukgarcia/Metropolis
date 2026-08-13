/**
 * Plan-drift / registry-integrity checker (BOW mkey: tool.planguard,
 * BUG-088 remediation, extracted from claude-plan-guard.js).
 *
 * This module is the SINGLE SOURCE OF TRUTH (GR#3) for the payload-
 * inspection logic that decides whether code.json / bow-import.json are
 * stale or hand-edited relative to docs/planning/master-plan-v2.1.json. It
 * is `require()`'d by claude-plan-guard.js (the PreToolUse layer, which
 * stays BLOCKING — see that file's header) and is designed to also be
 * `require()`'d by a future `commit-msg` dispatcher (BUG-088's Section B —
 * that dispatcher is NOT implemented here, see
 * docs/planning/acceptance/tool.secretguard.md's BUG-088 section, AC-B5).
 *
 * BUG-088 finding this extraction addresses: this guard's *trigger*
 * (deciding whether to engage at all, via a boundary-anchored regex over
 * the raw command STRING) was defeated by any leading word, shell wrapper,
 * or non-bareword git invocation. Its *payload* (this module: regenerate
 * via tools/plan/generate.js and hash-compare against the working tree) was
 * always sound — real filesystem state, never re-parsed from the command
 * string. This module carries none of the sibling guards' boundary-regex/
 * quote-mask/engage-decision machinery, by design (AC-B4): a commit-msg
 * hook has no engage decision to make, and copying dead trigger machinery
 * into this module would misrepresent that trigger-parsing is still part of
 * this design. (This header intentionally avoids spelling out those
 * helpers' exact identifier names, so a grep for them against this file —
 * the literal AC-B4 check — finds zero matches.)
 *
 * KNOWN LIMITATION INHERITED FROM ASM-386, STATED PLAINLY (AC-B2): a
 * `commit-msg` hook (the intended future caller of this module's
 * `checkPlan()`) does not fire for `git cherry-pick` / `git revert` /
 * `git am` on this project's git version (2.55.0.windows.3, verified three
 * independent ways per ASM-386's own comment thread). Not re-verified or
 * re-solved here.
 *
 * ONE DOCUMENTED DIVERGENCE FROM THE ORIGINAL PreToolUse-TIME BEHAVIOUR
 * (AC-D4): at `commit-msg` time (the future caller this module is designed
 * for), the regeneration side-effect this module performs (rewriting
 * code.json/bow-import.json on disk as part of the drift check) happens
 * AFTER the commit's tree is already fixed (git has already written the
 * tree object by the time commit-msg runs) — unlike at `pre-commit` time
 * (where claude-plan-guard.js currently runs), where the same side-effect
 * happens BEFORE `git write-tree`. A commit-msg-time regeneration can
 * therefore refresh files that will NOT be part of THIS commit even if the
 * check denies — the regenerated files land in the working tree for the
 * NEXT commit to pick up, not this one. This module's own logic is
 * unaffected by which hook point calls it (it always regenerates+hashes
 * the working tree as it finds it); the divergence is purely about WHEN in
 * the commit lifecycle that side-effect lands relative to the tree being
 * fixed, and is exactly the same "before vs at" disclosure
 * tool.committhook.md's AC-10 makes for identity, applied here.
 *
 * Exported call contract for a future dispatcher (AC-B5): `checkPlan()`
 * takes NO arguments and returns one of:
 *   { status: 'clean' }
 *   { status: 'found-problems', findings: [<string>, ...] }
 *   { status: 'internal-error', error: <Error> }
 * — the same three-state discriminant AC-E1 requires across all four BUG-088
 * checker modules.
 *
 * Everything below this header is RELOCATED, NOT REIMPLEMENTED, from
 * claude-plan-guard.js (AC-D4): same generate.js --check step, same
 * hash-before/regenerate/hash-after drift detection. See
 * claude-plan-checker.test.js for the parity proof against the original
 * guard's logic.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const GENERATE_PATH = path.join(ROOT, 'tools', 'plan', 'generate.js');
const CODE_JSON_PATH = path.join(ROOT, 'code.json');
const BOW_IMPORT_PATH = path.join(ROOT, 'tools', 'plan', 'bow-import.json');

// BUG-194: two concurrent checkPlan() calls (this project routinely runs
// several agent sessions' `node --test` in parallel) can interleave their
// hash-before / regenerate / hash-after critical sections — process A reads
// "before", process B's regeneration lands, process A's "after" now reflects
// B's write instead of its own regenerate, and A reports a spurious
// found-problems even though nothing was actually stale or hand-edited.
// Reproduced at 3-4-way concurrency (~1 in 10 rounds).
//
// Fix: serialize the critical section across processes with a directory-based
// mutex, using the same TOCTOU-free primitive already established in this
// codebase (claude-scratch.js's createDestDir, BUG-? Destructive round 1):
// fs.mkdirSync(dir, {recursive:false}) either creates the directory (lock
// acquired) or throws EEXIST (someone else holds it) — there is no window
// between "is it free" and "make it mine" because both are the same syscall.
// A plain directory works as a lock file on every platform this project
// targets (Windows/Linux) without an extra dependency. Release is
// fs.rmSync(recursive). A bounded poll-with-timeout avoids a hang if a
// holder ever crashes without releasing.
//
// BUG-197 (2026-08-13): the paragraph that used to sit here claimed "a stale
// lock still resolves once the timeout elapses and the next acquirer just
// takes over" — that was FALSE as implemented. There was no staleness check
// at all: a SIGKILLed holder (never runs its `finally { releaseLock() }`)
// left `.plan-checker.lock` orphaned PERMANENTLY, and every subsequent
// acquireLock() call — including the real PreToolUse claude-plan-guard.js
// hook path, not just tests — just repeated the identical 30s-block-then-
// throw cycle forever until a human deleted the directory by hand. This
// project routinely runs and kills concurrent agent/test processes (Ctrl-C,
// terminal close, OOM, timeout), so an orphaned lock is a realistic crash
// scenario, not an edge case.
//
// Fix: genuine staleness recovery, PID-liveness as the primary signal with
// an mtime backstop.
//   - At acquire time, the holder writes its own PID into a `pid` file
//     inside the lock directory (LOCK_PATH/pid).
//   - Before sleeping on EEXIST, a waiter reads that PID and probes it with
//     `process.kill(pid, 0)` — signal 0 sends nothing, it only checks
//     existence, throwing ESRCH if the process is gone. Verified directly on
//     this project's Windows host (not assumed from POSIX docs): a child
//     process's PID reads as alive while running and throws ESRCH
//     immediately after it is SIGKILLed, and a PID that never existed also
//     throws ESRCH — Node's signal-0 liveness probe is reliable on Windows,
//     so PID-liveness is the viable primary check here (an EPERM instead of
//     ESRCH means the process exists but is owned by another user/session —
//     that counts as alive; the lock is not stolen).
//   - If the holder is confirmed dead, the lock is definitively stale: the
//     waiter removes the whole directory and retries the mkdirSync
//     immediately (no sleep) rather than waiting out the full timeout.
//   - Backstop: if the pid file is missing/unreadable (e.g. a narrow race
//     where the directory exists but the pid file hasn't been written yet)
//     the waiter instead checks the lock directory's mtime against
//     STALE_MTIME_MS. This is weaker (a legitimately slow-but-alive holder
//     past the threshold would get evicted) but only ever fires when the
//     PID signal is unavailable, and STALE_MTIME_MS is set well beyond any
//     expected legitimate checkPlan() run.
const LOCK_PATH = path.join(ROOT, '.plan-checker.lock');
const LOCK_PID_FILE = path.join(LOCK_PATH, 'pid');
const LOCK_TIMEOUT_MS = 30000;
const LOCK_POLL_MS = 50;
const STALE_MTIME_MS = 5 * 60 * 1000; // 5 minutes — well beyond a legitimate run

// BUG-198 (2026-08-13): a SEPARATE mutex directory, used only to serialize
// the "inspect a suspect lock and decide whether to delete it" critical
// section itself across processes. See the long comment inside
// reapLockIfStale() below for why a per-instance compare-and-delete alone was
// empirically insufficient and why this nested mkdirSync-based mutex is what
// actually closes the race. Reap work under this mutex is always a handful
// of synchronous fs calls with no sleeps/blocking waits, so REAP_LOCK_STALE_MS
// is deliberately tiny compared to STALE_MTIME_MS above — a reap-lock still
// present after that long has essentially certainly been orphaned by a crash
// mid-reap, not a slow legitimate holder.
const REAP_LOCK_PATH = path.join(ROOT, '.plan-checker.lock.reap');
const REAP_LOCK_STALE_MS = 5000;

// Returns true if `pid` names a process that is still alive. Uses the
// signal-0 probe (process.kill sends nothing for signal 0, it only tests
// existence) — verified to behave correctly on Windows for a DIFFERENT
// process's PID (see comment above LOCK_PATH). ESRCH => definitely dead.
// Any other error (e.g. EPERM — exists but not signalable by us) is treated
// as "alive" so we never steal a lock we can't actually prove is dead.
function isProcessAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return err.code !== 'ESRCH';
  }
}

// Acquires the reap-mutex (mkdirSync — the same atomic primitive the main
// lock itself uses to acquire, which this module already trusts and this
// BUG-198 fix additionally re-validated directly, see the comment inside
// reapLockIfStaleUnderReapLock() below). Returns false (never blocks/sleeps)
// if someone else currently holds it — the caller should just treat "did not
// reap this round" the same as any other non-reap outcome and fall through to
// its normal retry/sleep loop.
function acquireReapLock() {
  try {
    fs.mkdirSync(REAP_LOCK_PATH, { recursive: false });
    return true;
  } catch (err) {
    if (err.code !== 'EEXIST') {
      return false;
    }
    // Possibly orphaned by a crash mid-reap (see REAP_LOCK_STALE_MS comment
    // above — reap work never legitimately holds this for long). One bounded
    // recovery attempt; if anything about it fails, just back off and let a
    // future call try again.
    let stat;
    try {
      stat = fs.statSync(REAP_LOCK_PATH);
    } catch {
      return false; // vanished between our mkdirSync failure and this stat
    }
    if (Date.now() - stat.mtimeMs < REAP_LOCK_STALE_MS) {
      return false; // genuinely in use right now
    }
    try {
      fs.rmSync(REAP_LOCK_PATH, { recursive: true, force: true });
      fs.mkdirSync(REAP_LOCK_PATH, { recursive: false });
      return true;
    } catch {
      return false;
    }
  }
}

function releaseReapLock() {
  try {
    fs.rmSync(REAP_LOCK_PATH, { recursive: true, force: true });
  } catch {
    // Best-effort — nothing left to release if it's already gone.
  }
}

// Inspects the current lock holder (if any) and removes the lock directory
// if it is definitively stale. Returns true if it removed something (caller
// should retry mkdirSync immediately rather than sleeping).
//
// BUG-198 (2026-08-13, Destructive round 3 on BUG-197): this is the PUBLIC
// entry point; the actual inspect-and-decide-and-delete logic now lives in
// reapLockIfStaleUnderReapLock() below, wrapped here by the reap-mutex
// (acquireReapLock/releaseReapLock). See that function's header comment for
// the full history of why a per-instance compare-and-delete alone (re-read
// immediately before the delete, abort if changed) was NOT sufficient on its
// own — under real multi-process contention on this project's Windows host,
// two waiters' "immediately before" windows could still both land clean
// before either one's delete executed, so both proceeded. Gating the ENTIRE
// decide-and-delete sequence behind a second, dedicated mutex (this
// function) makes that structurally impossible: only one process can ever be
// inspecting-and-deciding at a time, full stop.
function reapLockIfStale() {
  if (!acquireReapLock()) {
    return false;
  }
  try {
    return reapLockIfStaleUnderReapLock();
  } finally {
    releaseReapLock();
  }
}

// The original reap logic, now guaranteed to run with exclusive access (see
// reapLockIfStale() above) — no other process can be simultaneously deciding
// whether to reap or acting on that decision.
function reapLockIfStaleUnderReapLock() {
  let pidText;
  try {
    pidText = fs.readFileSync(LOCK_PID_FILE, 'utf8').trim();
  } catch {
    // pid file missing or unreadable — either a narrow race right after the
    // holder's mkdirSync (pid file not written yet) or a genuinely stale
    // lock from before this fix. Fall back to mtime staleness.
    return reapByMtimeIfStale();
  }

  const pid = Number.parseInt(pidText, 10);
  if (!Number.isInteger(pid) || pid <= 0 || isProcessAlive(pid)) {
    // Either an unparseable pid file (fall back to mtime) or a live holder
    // (definitely not stale — leave it alone).
    return Number.isInteger(pid) && pid > 0 ? false : reapByMtimeIfStale();
  }

  // BUG-198 (2026-08-13, Destructive round 3 on BUG-197): the code that used
  // to sit here was `fs.rmSync(LOCK_PATH, ...)` unconditionally, right after
  // the liveness decision above -- NOT pinned to the specific dead instance
  // that was just read, and NOT serialized against any other process doing
  // the same inspect-and-decide. Under multi-waiter contention immediately
  // after an orphaned lock, a DIFFERENT waiter could independently read the
  // same dead pid, win its own reap, and mint a BRAND NEW live lock at
  // LOCK_PATH (new pid) before this waiter's rmSync ran. The blind rmSync
  // didn't know or care what was CURRENTLY at LOCK_PATH -- it deleted
  // whatever was there, live or dead -- so it tore down the fresh legitimate
  // holder's lock, and this waiter's own immediate mkdirSync retry then
  // succeeded too: two processes simultaneously believing they held the
  // mutex. Reproduced empirically at 10-12 waiter concurrency two independent
  // ways (see BUG-198 BOW writeup).
  //
  // FIX ACTUALLY APPLIED, PRIMARY: reapLockIfStale() (the caller of this
  // function) now gates this entire inspect-decide-delete sequence behind
  // its own dedicated mutex (REAP_LOCK_PATH, acquired via mkdirSync -- the
  // same atomic primitive the main lock itself already relies on). That
  // fully serializes reap attempts: only one process can ever be inspecting
  // and deciding at a time, so a second waiter simply cannot begin its own
  // reap decision while this one is in flight, closing the race
  // structurally rather than merely narrowing a window.
  //
  // Considered and REJECTED: fs.renameSync-based single-owner steal (rename
  // the suspect lock dir aside atomically before touching it) -- attractive
  // since mkdirSync is already this module's atomic acquire primitive and
  // rename is the natural atomic "steal" counterpart. Rejected after direct
  // empirical verification on this project's Windows host (not assumed from
  // POSIX docs, per GR#15): a stress test of 30 rounds x 12 processes
  // concurrently calling fs.renameSync() on the SAME source directory to 12
  // DIFFERENT destination names showed TWO callers both report success (both
  // "won" the rename) in 4 of 30 rounds -- fs.renameSync is NOT a reliable
  // single-winner atomic primitive for this multi-process directory race on
  // this host, so it could not have served as the serializing mechanism.
  //
  // Kept as a cheap secondary layer (belt-and-braces, not the actual fix):
  // an initial version of this fix relied SOLELY on re-reading the pid file
  // immediately before the delete and aborting if it changed -- a
  // per-instance compare-and-delete. Measured against the real regression
  // test below, that alone was NOT sufficient: two waiters could each
  // observe an unchanged pid file at their own individual "immediately
  // before" check (because neither had deleted yet) and both proceed, since
  // nothing stops two DIFFERENT waiters from independently passing their own
  // narrow compare. The reap-mutex above is what actually prevents that (a
  // second waiter can't even start its own check while the first holds the
  // mutex); this re-read is retained anyway because it's nearly free and
  // catches any bug in the mutex logic itself.
  let reReadText;
  try {
    reReadText = fs.readFileSync(LOCK_PID_FILE, 'utf8').trim();
  } catch {
    // Gone already -- someone else already reaped (or the live holder
    // released normally) between our decision and this re-check. Nothing to
    // delete; let the retry loop re-evaluate.
    return false;
  }
  if (reReadText !== pidText) {
    // A different lock instance now occupies this path than the one we
    // decided was dead -- do NOT delete it. This is the exact compare in
    // "compare-and-delete": the comparand is the pid file's raw content at
    // decision time vs. its content right now.
    return false;
  }

  // Holder's PID is confirmed dead, AND the lock instance is confirmed to
  // still be the same one we decided was dead (compare-and-delete passed).
  try {
    fs.rmSync(LOCK_PATH, { recursive: true, force: true });
    return true;
  } catch {
    return false;
  }
}

function reapByMtimeIfStale() {
  let stat;
  try {
    stat = fs.statSync(LOCK_PATH);
  } catch {
    return false; // already gone
  }
  if (Date.now() - stat.mtimeMs < STALE_MTIME_MS) {
    return false;
  }

  // BUG-198: same compare-and-delete narrowing as the pid-based path above,
  // applied to the mtime backstop -- re-stat immediately before the delete
  // and abort if the mtime moved (someone touched/re-acquired the lock,
  // including a fresh holder's mkdirSync + pid-file write, since our
  // staleness read) or the path is already gone.
  let stat2;
  try {
    stat2 = fs.statSync(LOCK_PATH);
  } catch {
    return false; // already gone -- nothing to delete
  }
  if (stat2.mtimeMs !== stat.mtimeMs) {
    return false; // touched since our staleness decision -- leave it alone
  }

  try {
    fs.rmSync(LOCK_PATH, { recursive: true, force: true });
    return true;
  } catch {
    return false;
  }
}

// Synchronous sleep: checkPlan() is a synchronous, spawnSync-based API (its
// exported contract per the module header takes no arguments and returns
// synchronously), so the lock wait must also be synchronous rather than
// forcing every caller (including claude-plan-guard.js's PreToolUse hook,
// which is not async) to become promise-based just for this. Atomics.wait is
// the standard Node primitive for a genuine synchronous sleep.
function sleepSync(ms) {
  const sab = new SharedArrayBuffer(4);
  Atomics.wait(new Int32Array(sab), 0, 0, ms);
}

function acquireLock() {
  const deadline = Date.now() + LOCK_TIMEOUT_MS;
  for (;;) {
    try {
      fs.mkdirSync(LOCK_PATH, { recursive: false });
      // BUG-197: record who holds the lock so a future waiter can prove
      // liveness instead of just blocking blind. Best-effort — if this write
      // fails, reapLockIfStale() falls back to mtime staleness for whoever
      // waits on us.
      try {
        fs.writeFileSync(LOCK_PID_FILE, String(process.pid));
      } catch {
        // Non-fatal: the mtime backstop still protects a future waiter.
      }
      return;
    } catch (err) {
      if (err.code !== 'EEXIST') {
        throw err;
      }
      // BUG-197: before blindly sleeping and burning through the timeout,
      // check whether the current holder is actually still alive. A dead
      // holder's lock is definitively stale — reap it and retry the
      // mkdirSync immediately rather than waiting out the full timeout.
      if (reapLockIfStale()) {
        continue;
      }
      if (Date.now() >= deadline) {
        throw new Error(
          `checkPlan(): timed out after ${LOCK_TIMEOUT_MS}ms waiting for the ` +
          `plan-checker lock (${LOCK_PATH}) held by another process; if no ` +
          'checkPlan() is actually running, delete that directory manually.'
        );
      }
      sleepSync(LOCK_POLL_MS);
    }
  }
}

function releaseLock() {
  try {
    fs.rmSync(LOCK_PATH, { recursive: true, force: true });
  } catch {
    // Best-effort: if it's already gone (e.g. a manual cleanup during a
    // stale-lock timeout race, or another process reaped it as stale) there's
    // nothing left to release.
  }
}

function hashFiles(paths) {
  // BUG-015 (2026-08-13): the separator was a literal NUL byte ('\x00'),
  // confirmed at `git show HEAD:claude-plan-guard.js` before this function's
  // BUG-088 relocation here. That NUL was never intentional — the author's
  // intent (per the surrounding code style and BOW-015's finding) was a
  // plain space (' ') separator between the hashed path and the hashed file
  // content. The NUL had two real consequences: (1) any file containing this
  // source — at the time, claude-plan-guard.js itself — got flagged BINARY
  // by git purely because of the embedded NUL, hiding future diffs of a
  // PreToolUse hook behind "Binary files differ"; (2) it wasn't the
  // separator the author meant to use.
  //
  // This supersedes the BUG-088 P2 "correction" that used to sit here, which
  // restored the NUL believing it to be the original, deliberate separator
  // (true in the narrow sense that NUL is what the byte-for-byte relocation
  // found, but the NUL itself was BUG-015's literal-byte-mistake all along,
  // not a deliberate choice). BUG-015 is the authoritative fix for this
  // separator; "verbatim relocation" of a bug is not a reason to keep it.
  //
  // No stored/compared hash baselines depend on this function's output —
  // hashFiles() is only ever used for an in-process before/after comparison
  // within a single checkPlan() call (see below), never persisted to disk or
  // compared against a hardcoded value — so changing the separator byte
  // changes what a given input hashes to, but nothing outside this module
  // needs re-baselining as a result.
  const h = crypto.createHash('sha256');
  for (const p of paths) {
    h.update(p);
    h.update(' ');
    h.update(fs.existsSync(p) ? fs.readFileSync(p) : Buffer.from('__MISSING__'));
    h.update(' ');
  }
  return h.digest('hex');
}

/**
 * Runs the full plan-drift check (relocated unchanged from
 * claude-plan-guard.js's main()): validates the master plan, then
 * regenerates code.json/bow-import.json and compares hashes before/after.
 * Returns the three-state result described in the module header. Never
 * throws — every failure mode (missing generate.js, spawn failure,
 * validation failure, regeneration failure, drift) is captured into the
 * return value.
 */
function checkPlan() {
  try {
    if (!fs.existsSync(GENERATE_PATH)) {
      return {
        status: 'internal-error',
        error: new Error(
          'tools/plan/generate.js is missing — the plan pipeline (master plan -> ' +
          'code.json -> BOW import, GR#3/GR#6) cannot be validated without the generator.'
        ),
      };
    }

    // Step 1: validate the master plan (writes nothing).
    const checkResult = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
    });

    if (checkResult.error) {
      return { status: 'internal-error', error: checkResult.error };
    }

    if (checkResult.status !== 0) {
      const details = [checkResult.stdout, checkResult.stderr].filter(Boolean).join('\n').trim();
      return {
        status: 'found-problems',
        findings: [`master plan failed validation (GR#3): ${details}`],
      };
    }

    // Step 2: drift / hand-edit detection. Hash the generated outputs as
    // they currently sit in the working tree, regenerate for real, hash
    // again. A change means they were stale or hand-edited.
    //
    // BUG-194: this before-regenerate-after sequence is the critical section
    // that must not interleave with another process's copy of the same
    // sequence (see acquireLock()'s header comment above), so it runs under
    // the cross-process lock.
    const outputPaths = [CODE_JSON_PATH, BOW_IMPORT_PATH];
    acquireLock();
    try {
      const beforeHash = hashFiles(outputPaths);

      const genResult = spawnSync(process.execPath, [GENERATE_PATH], {
        cwd: ROOT,
        encoding: 'utf8',
      });

      if (genResult.error) {
        return { status: 'internal-error', error: genResult.error };
      }

      if (genResult.status !== 0) {
        const details = [genResult.stdout, genResult.stderr].filter(Boolean).join('\n').trim();
        return {
          status: 'found-problems',
          findings: [`tools/plan/generate.js failed while regenerating outputs: ${details}`],
        };
      }

      const afterHash = hashFiles(outputPaths);

      if (beforeHash !== afterHash) {
        return {
          status: 'found-problems',
          findings: [
            'code.json / tools/plan/bow-import.json were stale or hand-edited (GR#3, GR#6). ' +
            'generate.js has already refreshed both files in place (idempotent — safe to keep). ' +
            'Review the diff (git diff -- code.json tools/plan/bow-import.json), stage the ' +
            'refreshed files, and retry.',
          ],
        };
      }

      return { status: 'clean' };
    } finally {
      releaseLock();
    }
  } catch (err) {
    // AC-F1: an internal error is its own state — never silently downgraded
    // to "clean".
    return { status: 'internal-error', error: err };
  }
}

module.exports = {
  ROOT,
  GENERATE_PATH,
  CODE_JSON_PATH,
  BOW_IMPORT_PATH,
  LOCK_PATH,
  LOCK_PID_FILE,
  LOCK_TIMEOUT_MS,
  STALE_MTIME_MS,
  REAP_LOCK_PATH,
  REAP_LOCK_STALE_MS,
  hashFiles,
  checkPlan,
  isProcessAlive,
  acquireLock,
  releaseLock,
  reapLockIfStale,
};
