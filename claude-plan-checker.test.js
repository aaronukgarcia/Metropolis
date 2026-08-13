/**
 * claude-plan-checker.test.js — BUG-088 extraction proof for
 * claude-plan-checker.js.
 *
 * Proves:
 *   1. AC-B4: no boundary-regex/quote-mask/engage-decision trigger machinery
 *      lives in this module.
 *   2. AC-B2: header names the ASM-386 verb-coverage gap.
 *   3. AC-D4: checkPlan() reproduces claude-plan-guard.js's own drift
 *      detection — a clean working tree (already regenerated) reports
 *      "clean"; documents the commit-msg-timing divergence in its header.
 *   4. AC-E1/AC-F1: three-state contract, internal error never silently
 *      downgraded to "clean" (missing generate.js).
 *
 * Run: node --test claude-plan-checker.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

const ROOT = __dirname;
const checker = require('./claude-plan-checker.js');

// ---------------------------------------------------------------------------
// BUG-112 FIX (2026-08-13): the BUG-088 P1 correction above (superseded)
// still renamed the REAL, SHARED tools/plan/generate.js away and back to
// simulate "generate.js is missing" — even with a unique per-process backup
// name and a bounded restore retry, the SOURCE path being renamed away was
// still this project's one real generate.js. Any other concurrent process
// (another `node --test` run, or this session's own parallel-agent pattern)
// calling checker.checkPlan() during that window observed a spurious
// internal-error, because fs.existsSync(GENERATE_PATH) genuinely returned
// false for everyone, not just the test's own process. Confirmed via BUG-112
// (Destructive round 3 on the BUG-088 fix): reproduced at 4-way and 16-way
// concurrency.
//
// Fix: never touch the real file at all. checkPlan() resolves GENERATE_PATH
// relative to this module's own __dirname at require() time, so copying
// claude-plan-checker.js into an throwaway scratch directory (deliberately
// WITHOUT a tools/plan/generate.js alongside it) and requiring that copy
// fresh gives a checker instance whose GENERATE_PATH points at a path that
// has never existed and is never shared with any other process — no rename,
// no shared mutable state, no cross-process race, same assertion.
function loadScratchCheckerMissingGenerate() {
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'planchecker_missing_generate_'));
  const scratchModulePath = path.join(scratchDir, 'claude-plan-checker.js');
  fs.copyFileSync(path.join(ROOT, 'claude-plan-checker.js'), scratchModulePath);
  // Deliberately do NOT create scratchDir/tools/plan/generate.js — that's
  // the whole point of this fixture.
  const scratchChecker = require(scratchModulePath);
  return { scratchChecker, scratchDir };
}

test('AC-B4: claude-plan-checker.js contains no boundary-regex/quote-mask trigger machinery', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(!/buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit/.test(src));
});

test('AC-B2: header names cherry-pick/revert/am + ASM-386', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(/ASM-386/.test(src));
  assert.ok(/cherry-pick/.test(src));
  assert.ok(/revert/.test(src));
  assert.ok(/\bam\b/.test(src));
});

test('AC-D4: header documents the commit-msg-timing divergence for the regeneration side effect', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(/write-tree/.test(src), 'header should reference the git write-tree timing distinction');
  assert.ok(/pre-commit/.test(src) && /commit-msg/.test(src));
});

// ---------------------------------------------------------------------------
// AC-D4 fixture parity: checkPlan() against this repo's ACTUAL working tree
// (already regenerated as of every commit, per the plan-guard's own
// enforcement) should report clean, exactly like claude-plan-guard.js's own
// hash-compare would.
// ---------------------------------------------------------------------------

test('AC-D4: checkPlan() reports {status:"clean"} against an already-regenerated working tree', () => {
  const result = checker.checkPlan();
  // If this genuinely fails, code.json/bow-import.json are ALREADY drifted
  // in this working tree independent of this test — not this test's fault,
  // but worth surfacing via the assertion message rather than a bare diff.
  assert.equal(
    result.status,
    'clean',
    `expected a clean plan-drift check against the current working tree; got ${result.status}: ${JSON.stringify(result.findings || result.error)}`
  );
});

// ---------------------------------------------------------------------------
// AC-E1 / AC-F1: three-state contract.
// ---------------------------------------------------------------------------

test('AC-F1: checkPlan() returns {status:"internal-error"} (never silently "clean") when generate.js is missing', () => {
  assert.ok(fs.existsSync(checker.GENERATE_PATH), 'sanity: this repo\'s real tools/plan/generate.js must exist (untouched by this test)');
  const { scratchChecker, scratchDir } = loadScratchCheckerMissingGenerate();
  try {
    const result = scratchChecker.checkPlan();
    assert.equal(result.status, 'internal-error');
    assert.ok(result.error instanceof Error);
  } finally {
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
});

// ---------------------------------------------------------------------------
// BUG-194: concurrent checkPlan() stress test.
//
// AC-D4's real-repo checkPlan() call above and checkPlan() itself both
// regenerate code.json/tools/plan/bow-import.json IN PLACE against the real
// working tree (production behaviour, not a fixture — see the module
// header's "ONE DOCUMENTED DIVERGENCE" note). Before BUG-194's fix, two
// concurrent checkPlan() calls (this project routinely runs several agent
// sessions' `node --test` in parallel) could interleave their
// hash-before/regenerate/hash-after critical sections and one would observe
// a spurious "found-problems" caused purely by the other's regeneration
// landing mid-comparison, not by any real staleness/hand-edit. Reproduced at
// 3-4-way concurrency in ~1 of 10 rounds prior to the fix (see BUG-194).
//
// checkPlan() itself is synchronous (spawnSync-based), so concurrency within
// a single Node process doesn't interleave — the race needs separate OS
// processes, same as the real-world scenario (separate `node --test`
// invocations). Each round below spawns N fresh child processes that each
// require claude-plan-checker.js fresh and call checkPlan() once, so the
// mutex directory (LOCK_PATH) is exercised across real process boundaries,
// not just within one event loop.
function runCheckPlanInSubprocess() {
  return new Promise((resolve, reject) => {
    const script =
      'const r = require(' + JSON.stringify(require.resolve('./claude-plan-checker.js')) + ').checkPlan();' +
      'process.stdout.write(JSON.stringify({status: r.status, findings: r.findings || null, ' +
      'error: r.error ? String(r.error && r.error.message || r.error) : null}));';
    const child = spawn(process.execPath, ['-e', script], { cwd: ROOT });
    let out = '';
    let err = '';
    child.stdout.on('data', (d) => { out += d; });
    child.stderr.on('data', (d) => { err += d; });
    child.on('error', reject);
    child.on('close', (code) => {
      try {
        resolve(JSON.parse(out));
      } catch (parseErr) {
        reject(new Error(
          `checkPlan() subprocess produced non-JSON output (exit ${code}): ` +
          `stdout=${JSON.stringify(out)} stderr=${JSON.stringify(err)} (${parseErr.message})`
        ));
      }
    });
  });
}

const LOCK_TIMEOUT_MS_FOR_TEST = 35000; // slightly above checker.js's own 30s LOCK_TIMEOUT_MS

test('BUG-194: N-way concurrent checkPlan() subprocesses never spuriously report found-problems, across many rounds', async () => {
  const ROUNDS = 8;
  const CONCURRENCY = 4;

  for (let round = 1; round <= ROUNDS; round += 1) {
    const results = await Promise.all(
      Array.from({ length: CONCURRENCY }, () => runCheckPlanInSubprocess())
    );

    for (const result of results) {
      assert.notEqual(
        result.status,
        'internal-error',
        `round ${round}: unexpected internal-error from a concurrent checkPlan(): ${result.error}`
      );
      assert.equal(
        result.status,
        'clean',
        `round ${round}: a concurrent checkPlan() reported "found-problems" against an ` +
        `already-clean working tree — this is exactly the BUG-194 race (or a genuine ` +
        `plan-drift regression) if it fires: ${JSON.stringify(result.findings)}`
      );
    }
  }

  // Sanity: the lock directory must not persist indefinitely (a leaked lock
  // would eventually wedge every future checkPlan() until its timeout).
  // NOTE: this can NOT assert the lock is instantaneously absent right after
  // this test's own subprocesses finish — this project routinely runs
  // several `node --test` invocations of THIS SAME file concurrently (that
  // is the whole scenario BUG-194 is about), and another such process's
  // checkPlan() may legitimately hold LOCK_PATH at that exact instant. So
  // this polls briefly instead of asserting a single point-in-time read.
  const clearDeadline = Date.now() + LOCK_TIMEOUT_MS_FOR_TEST;
  while (fs.existsSync(checker.LOCK_PATH) && Date.now() < clearDeadline) {
    await new Promise((r) => setTimeout(r, 100));
  }
  assert.ok(
    !fs.existsSync(checker.LOCK_PATH),
    'plan-checker lock directory is still present well after this test\'s own checkPlan() calls ' +
    'completed — either this process leaked it, or another process has wedged past its own timeout'
  );
});

// ---------------------------------------------------------------------------
// BUG-197: orphaned-lock staleness recovery.
//
// Uses an isolated scratch copy of the module (same technique as the
// BUG-112 "missing generate.js" fixture above) so LOCK_PATH is private to
// this test and never collides with the real repo's shared lock or with
// another concurrent `node --test` process's copy of this same suite.
// ---------------------------------------------------------------------------

function loadScratchChecker() {
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'planchecker_lock_scratch_'));
  const scratchModulePath = path.join(scratchDir, 'claude-plan-checker.js');
  fs.copyFileSync(path.join(ROOT, 'claude-plan-checker.js'), scratchModulePath);
  const scratchChecker = require(scratchModulePath);
  return { scratchChecker, scratchDir };
}

// Spawns a detached-in-spirit child that acquires the given checker module's
// lock and then just sits there (setInterval keeps the event loop alive)
// WITHOUT ever calling releaseLock() — this is the actual repro shape: a
// process that holds the lock and then dies with no chance to run its own
// `finally { releaseLock() }`.
function spawnLockHolder(scratchModulePath) {
  const script =
    'const c = require(' + JSON.stringify(scratchModulePath) + '); ' +
    'c.acquireLock(); ' +
    'process.stdout.write("LOCKED\\n"); ' +
    'setInterval(() => {}, 1000);';
  return spawn(process.execPath, ['-e', script]);
}

// Polls a predicate until it's true or a deadline passes.
async function waitFor(predicate, timeoutMs, intervalMs = 25) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) return true;
    await new Promise((r) => setTimeout(r, intervalMs));
  }
  return predicate();
}

test('BUG-197 repro: acquireLock() reaps a lock orphaned by a SIGKILLed holder instead of blocking the full timeout', async () => {
  const { scratchChecker, scratchDir } = loadScratchChecker();
  const scratchModulePath = path.join(scratchDir, 'claude-plan-checker.js');
  const holder = spawnLockHolder(scratchModulePath);

  try {
    // Wait for the child to actually hold the lock (pid file written).
    await waitFor(() => fs.existsSync(scratchChecker.LOCK_PID_FILE), 5000);
    assert.ok(fs.existsSync(scratchChecker.LOCK_PID_FILE), 'holder should have created the lock + pid file');
    const holderPid = fs.readFileSync(scratchChecker.LOCK_PID_FILE, 'utf8').trim();
    assert.equal(holderPid, String(holder.pid));
    assert.ok(scratchChecker.isProcessAlive(holder.pid), 'sanity: holder must be alive before the kill');

    // Simulate the SIGKILL-mid-checkPlan() crash: the holder never gets to
    // run releaseLock(). Wait for the OS to confirm the process is actually
    // gone before probing, so the test isn't racing its own kill signal.
    const exited = new Promise((resolve) => holder.on('exit', resolve));
    holder.kill('SIGKILL');
    await exited;
    assert.ok(!scratchChecker.isProcessAlive(holder.pid), 'sanity: holder pid must read as dead after SIGKILL');

    // The orphaned lock directory is still present on disk — this is
    // BUG-197's exact repro condition.
    assert.ok(fs.existsSync(scratchChecker.LOCK_PATH), 'orphaned lock directory must still exist after the kill');

    // Before the fix, this next acquireLock() would block the full 30s
    // LOCK_TIMEOUT_MS and then throw. After the fix, it must detect the dead
    // PID and recover quickly.
    const start = Date.now();
    scratchChecker.acquireLock();
    const elapsedMs = Date.now() - start;
    scratchChecker.releaseLock();

    assert.ok(
      elapsedMs < 5000,
      `acquireLock() took ${elapsedMs}ms to recover an orphaned lock — expected fast reap (<5000ms), ` +
      `not a near-${scratchChecker.LOCK_TIMEOUT_MS}ms block`
    );
  } finally {
    try { holder.kill('SIGKILL'); } catch { /* already dead */ }
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
});

test('BUG-197 negative control: a genuinely live lock holder is NOT evicted as stale', async () => {
  const { scratchChecker, scratchDir } = loadScratchChecker();
  const scratchModulePath = path.join(scratchDir, 'claude-plan-checker.js');
  const HOLD_MS = 2000;
  const script =
    'const c = require(' + JSON.stringify(scratchModulePath) + '); ' +
    'c.acquireLock(); ' +
    'process.stdout.write("LOCKED\\n"); ' +
    `setTimeout(() => { c.releaseLock(); process.exit(0); }, ${HOLD_MS});`;
  const holder = spawn(process.execPath, ['-e', script]);

  try {
    await waitFor(() => fs.existsSync(scratchChecker.LOCK_PID_FILE), 5000);
    assert.ok(fs.existsSync(scratchChecker.LOCK_PID_FILE), 'holder should have created the lock + pid file');
    assert.ok(scratchChecker.isProcessAlive(holder.pid), 'sanity: holder is alive and genuinely mid-"work"');

    // Directly probe the staleness reaper while the holder is still alive:
    // it must refuse to evict a live holder's lock (this is BUG-194's own
    // concurrency guarantee — don't regress it while fixing BUG-197).
    const reaped = scratchChecker.reapLockIfStale();
    assert.equal(reaped, false, 'a live holder\'s lock must never be reaped as stale');
    assert.ok(fs.existsSync(scratchChecker.LOCK_PATH), 'live holder\'s lock directory must still be present');

    // A waiter's acquireLock() must actually wait for the live holder to
    // finish, not steal the lock early — confirm the elapsed time is close
    // to the holder's real hold duration, not near-instant.
    const start = Date.now();
    scratchChecker.acquireLock();
    const elapsedMs = Date.now() - start;
    scratchChecker.releaseLock();

    assert.ok(
      elapsedMs >= HOLD_MS - 300,
      `acquireLock() returned after only ${elapsedMs}ms while a live holder held the lock for ` +
      `~${HOLD_MS}ms — this would mean a live holder got wrongly evicted`
    );
  } finally {
    try { holder.kill('SIGKILL'); } catch { /* already exited */ }
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
});

test('BUG-197: header no longer makes the false "stale lock still resolves" claim, and documents the real PID-liveness fix', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(
    !/stale lock still resolves once the timeout elapses and the next acquirer just takes over/.test(src),
    'the false BUG-197 header claim must be removed/corrected'
  );
  assert.ok(/BUG-197/.test(src));
  assert.ok(/process\.kill\(pid, 0\)/.test(src), 'header/code should reference the signal-0 liveness probe');
  assert.ok(/ESRCH/.test(src));
});

// ---------------------------------------------------------------------------
// BUG-198: multi-waiter reap-race regression.
//
// BUG-197's own repro/negative-control tests above only ever exercise a
// SINGLE waiter against one orphaned lock, so they never covered the real gap
// the BUG-198 attacker found: reapLockIfStale()'s removal was not pinned to
// the specific dead lock instance it read, so a DIFFERENT waiter reaping and
// re-acquiring in between a first waiter's decision and its rmSync could get
// its brand-new live lock deleted out from under it, producing two
// simultaneous "holders". This test reproduces that exact shape: many real
// concurrent processes (not just two) racing against one orphaned lock,
// repeated across many batches, using the same two detection techniques the
// attacker used:
//   1. An EEXIST-on-open canary file inside the critical section
//      (fs.openSync(path, 'wx')) -- if a second process ever enters the
//      critical section while a first is still inside it, its 'wx' open
//      throws EEXIST. That's an unambiguous double-acquire signal.
//   2. A wall-clock enter/exit interval log -- overlapping [enter, exit]
//      windows across different waiter pids is a second, independent way to
//      see the same violation.
// ---------------------------------------------------------------------------

// A "waiter" process: attempts acquireLock() against the SAME (shared,
// pre-existing) scratch checker module, then inside the critical section (a)
// tries to exclusively create a canary file (fails loudly with EEXIST if
// someone else is already inside) and (b) appends a
// "pid enter=<ms> exit=<ms>" line to an interval log, holding the lock for a
// short simulated "work" duration before releasing. Reports what it observed
// on stdout as JSON.
function waiterScript(scratchModulePath, canaryPath, intervalLogPath, holdMs) {
  return `
    const fs = require('fs');
    const c = require(${JSON.stringify(scratchModulePath)});
    const canaryPath = ${JSON.stringify(canaryPath)};
    const intervalLogPath = ${JSON.stringify(intervalLogPath)};
    let violation = null;
    try {
      c.acquireLock();
      const enter = Date.now();
      let canaryFd = null;
      try {
        canaryFd = fs.openSync(canaryPath, 'wx');
      } catch (err) {
        violation = 'canary-EEXIST: another process was already inside the critical section';
      }
      // Simulated work while holding the lock.
      const busyUntil = Date.now() + ${holdMs};
      while (Date.now() < busyUntil) { /* spin briefly, no sleep needed */ }
      const exit = Date.now();
      if (canaryFd !== null) {
        fs.closeSync(canaryFd);
        fs.unlinkSync(canaryPath);
      }
      fs.appendFileSync(intervalLogPath, process.pid + ' ' + enter + ' ' + exit + '\\n');
      c.releaseLock();
      process.stdout.write(JSON.stringify({ ok: true, violation }));
    } catch (err) {
      process.stdout.write(JSON.stringify({ ok: false, error: String(err && err.message || err) }));
    }
  `;
}

function runWaiter(scratchModulePath, canaryPath, intervalLogPath, holdMs) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['-e', waiterScript(scratchModulePath, canaryPath, intervalLogPath, holdMs)]);
    let out = '';
    let err = '';
    child.stdout.on('data', (d) => { out += d; });
    child.stderr.on('data', (d) => { err += d; });
    child.on('error', reject);
    child.on('close', () => {
      try {
        resolve(JSON.parse(out));
      } catch (parseErr) {
        reject(new Error(`waiter produced non-JSON output: stdout=${JSON.stringify(out)} stderr=${JSON.stringify(err)} (${parseErr.message})`));
      }
    });
  });
}

// Parses the interval log and returns every pair of overlapping
// [enter, exit] windows found (should always be empty if mutual exclusion
// held for the whole batch).
function findOverlaps(intervalLogPath) {
  if (!fs.existsSync(intervalLogPath)) return [];
  const lines = fs.readFileSync(intervalLogPath, 'utf8').trim().split('\n').filter(Boolean);
  const intervals = lines.map((line) => {
    const [pid, enter, exit] = line.split(' ');
    return { pid, enter: Number(enter), exit: Number(exit) };
  });
  const overlaps = [];
  for (let i = 0; i < intervals.length; i += 1) {
    for (let j = i + 1; j < intervals.length; j += 1) {
      const a = intervals[i];
      const b = intervals[j];
      if (a.enter < b.exit && b.enter < a.exit) {
        overlaps.push([a, b]);
      }
    }
  }
  return overlaps;
}

test('BUG-198 repro: many concurrent waiters racing an orphaned lock never double-acquire, across many batches', async () => {
  const WAITERS_PER_BATCH = 12;
  const BATCHES = 10;
  const HOLD_MS = 300;

  let canaryViolations = 0;
  let overlapViolations = 0;
  let totalAcquisitions = 0;

  for (let batch = 1; batch <= BATCHES; batch += 1) {
    const { scratchChecker, scratchDir } = loadScratchChecker();
    const scratchModulePath = path.join(scratchDir, 'claude-plan-checker.js');
    const canaryPath = path.join(scratchDir, 'canary');
    const intervalLogPath = path.join(scratchDir, 'intervals.log');

    try {
      // Orphan a lock the same way BUG-197's own repro does: a real holder
      // process acquires it, then gets SIGKILLed before it can release --
      // leaving LOCK_PATH/pid on disk pointing at a now-dead pid. This is
      // BUG-198's exact repro precondition ("immediately after an orphaned
      // lock").
      const holder = spawnLockHolder(scratchModulePath);
      await waitFor(() => fs.existsSync(scratchChecker.LOCK_PID_FILE), 5000);
      assert.ok(fs.existsSync(scratchChecker.LOCK_PID_FILE), `batch ${batch}: holder should have created the lock + pid file`);
      const exited = new Promise((resolve) => holder.on('exit', resolve));
      holder.kill('SIGKILL');
      await exited;
      assert.ok(!scratchChecker.isProcessAlive(holder.pid), `batch ${batch}: sanity, holder pid must read as dead after SIGKILL`);

      // Now fire many real concurrent waiter processes at the orphaned lock
      // simultaneously -- this is the multi-waiter contention shape the
      // BUG-198 attacker found is required to trigger the race (a single
      // waiter, per BUG-197's own tests, never exercises it).
      const results = await Promise.all(
        Array.from({ length: WAITERS_PER_BATCH }, () =>
          runWaiter(scratchModulePath, canaryPath, intervalLogPath, HOLD_MS))
      );

      for (const result of results) {
        assert.ok(result.ok, `batch ${batch}: a waiter threw instead of completing: ${result.error}`);
        totalAcquisitions += 1;
        if (result.violation) {
          canaryViolations += 1;
          console.log(`batch ${batch}: CANARY VIOLATION: ${result.violation}`);
        }
      }

      const overlaps = findOverlaps(intervalLogPath);
      if (overlaps.length > 0) {
        overlapViolations += overlaps.length;
        console.log(`batch ${batch}: ${overlaps.length} overlapping hold-window pair(s): ${JSON.stringify(overlaps)}`);
      }

      // Every waiter that entered the critical section must have logged an
      // interval -- otherwise the overlap check below would be silently
      // checking fewer intervals than acquisitions actually happened, which
      // would let a violation hide.
      assert.equal(
        fs.readFileSync(intervalLogPath, 'utf8').trim().split('\n').filter(Boolean).length,
        WAITERS_PER_BATCH,
        `batch ${batch}: interval log line count must match the number of waiters (mutex must serialize ALL of them exactly once each)`
      );
    } finally {
      fs.rmSync(scratchDir, { recursive: true, force: true });
    }
  }

  console.log(
    `BUG-198 stress summary: ${BATCHES} batches x ${WAITERS_PER_BATCH} waiters = ${totalAcquisitions} acquisitions; ` +
    `canary violations=${canaryViolations}; overlapping-interval violations=${overlapViolations}`
  );

  assert.equal(canaryViolations, 0, `${canaryViolations} canary EEXIST double-acquire violation(s) observed across ${BATCHES} batches -- BUG-198 regressed`);
  assert.equal(overlapViolations, 0, `${overlapViolations} overlapping hold-window violation(s) observed across ${BATCHES} batches -- BUG-198 regressed`);
});

test('hashFiles() is deterministic and sensitive to content changes', () => {
  const dir = fs.mkdtempSync(path.join(ROOT, '__planchecker_test_'));
  try {
    const p = path.join(dir, 'a.json');
    fs.writeFileSync(p, '{"a":1}', 'utf8');
    const h1 = checker.hashFiles([p]);
    const h2 = checker.hashFiles([p]);
    assert.equal(h1, h2, 'same content must hash identically (determinism)');
    fs.writeFileSync(p, '{"a":2}', 'utf8');
    const h3 = checker.hashFiles([p]);
    assert.notEqual(h1, h3, 'a content change must change the hash (the whole point of the drift check)');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
