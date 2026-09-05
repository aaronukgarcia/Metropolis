// webconsole/testsupport/mutant.mjs — BUG-739 process-level mutation
// isolation for GR#24 RED-PROOF tests. Deliberately NOT under webconsole/test
// — see the TESTSUPPORT_DIR comment below for why.
//
// THE DEFECT THIS REPLACES (BUG-739, independent round, 2026-09-05): CI's
// node-test job runs the bare node test runner sharded 3 ways at the repo
// root at DEFAULT CONCURRENCY. Several webconsole tests (see the old
// tools/test/scoped.mjs FILE_MUTATING_TEST_BASENAMES allowlist) proved their
// RED-PROOF by mutating a REAL shared file under webconsole/src/*.ts IN
// PLACE (cp file file.bak; write mutated content over the real file; spawn a
// child process to observe the mutant; restore from the .bak in a finally).
// scoped.mjs could serialise these against EACH OTHER, but CI never invokes
// scoped.mjs — it invokes the bare runner directly — so an unrelated
// IMPORTING sibling landing in the SAME shard as a mutating test could
// transiently import the sabotaged real file while the mutation was live,
// with no serialisation possible from outside the process (reproduced: an
// importer of src/sim/data.ts observed BUG-739's own attract/fertility-style
// mutant mid-flight).
//
// THE FIX: never touch the real src tree at all. Instead:
//   1. Copy the WHOLE webconsole/src directory into a private, disposable
//      temp directory (a "shadow" tree) — cheap (webconsole/src is ~2.7MB /
//      ~120 files; a full recursive copy costs low-single-digit
//      milliseconds, immaterial next to this project's existing test
//      timeouts).
//   2. Overwrite ONLY the target file inside the SHADOW copy with the
//      mutated content.
//   3. Spawn a fresh child `node` process whose entry script is written
//      INTO the shadow tree (so its own relative imports resolve within the
//      shadow tree, not the real one) and run it.
//
// Because Node resolves a relative import specifier relative to the
// IMPORTING module's own location, every module reached transitively from
// the child script — including modules that import the mutated file, not
// just the mutated file itself — naturally observes the mutant. No loader
// hook, no `module.register()`, no redirect table: the module graph is
// simply rooted somewhere else for the duration of one child process. This
// was proven against a two-file fixture (a leaf module + a module that
// imports it) before being wired into any real test — direct AND transitive
// importers both observed the mutation, and the real fixture files were
// verified byte-identical after every run.
//
// (A `module.register()` resolve-hook redirect approach was tried first and
// rejected: it worked for a plain-JS two-file fixture, but Node's TS-aware
// resolution for a RELATIVE import inside an already-resolved .ts module
// does not appear to re-enter the public resolve hook chain at all for that
// nested specifier — reproducibly zero hook invocations for the nested
// import in a live trace, on this Node version — making a redirect-table
// approach unreliable for exactly the multi-file case this fix needs to
// cover. The whole-tree shadow copy has no such gap: it needs no hook to
// fire correctly, it just needs `cp -r` to have happened before the child
// starts.)
//
// webconsole/src has no non-erasable TypeScript syntax (no enums,
// namespaces, decorators) in the modules this helper is used against, so a
// plain `node` child process uses this project's already-relied-upon native
// TypeScript type-stripping (the SAME mechanism webconsole/package.json's
// own `"test": "node --test \"test/*.test.mjs\""` already depends on to
// import src/sim/*.ts directly) — no tsx/loader flag is required for the
// .mjs-based RED-PROOFs. A caller whose child needs tsx (e.g. because the
// child itself touches JSX) can pass `extraArgs` to inject `--import
// <tsx-loader-url>`.
//
// MUTATION-TESTING PROOF KNOB (do not use outside that purpose): setting
// MUTANT_UNSAFE_WRITE_IN_PLACE=1 makes this helper skip the shadow copy
// entirely and mutate the REAL src file directly (restoring it afterwards)
// — i.e. it deliberately reintroduces BUG-739's exact defect. This exists
// ONLY so the round's CI-shape regression test (tools/scoped-runner-attack.mjs)
// can prove the shadow-copy mechanism is actually load-bearing: flip the
// knob, show the concurrent-importer test goes red, flip it back.
//
// R4 (BUG-739 round REJECT, 2026-09-05): a mutated file's SIBLINGS inside the
// shadow copy (e.g. src/sim/engine.ts importing src/sim/data.ts) correctly
// see the mutant, because both live in the SAME shadow tree. But a test
// SUPPORT file loaded by ABSOLUTE real path — e.g.
// webconsole/test/scale/fixture.mjs, which every one of this file's own
// callers reaches via `pathToFileURL(path.join(<real test dir>, 'scale',
// 'fixture.mjs'))` rather than a shadow-relative specifier — loads the REAL,
// unmutated src it was built against, NOT the shadow's mutated copy, because
// its own internal relative imports (`'../src/sim/engine.ts'`) resolve
// relative to ITS OWN (real, on-disk) location, outside the shadow root
// entirely. This is CORRECT and intentional for a state-building helper like
// fixture.mjs, whose job is only to build a plausible SimState shape and
// which the tests calling it don't need mutated — but it means a RED-PROOF
// MUST observe the mutation through a function imported from INSIDE the
// shadow tree (a shadow-relative specifier such as `./sim/data.ts` /
// `./sim/engine.ts` as childBody's own import, or engine.ts/data.ts's own
// internal imports of each other) — never assume calling ANY helper that
// happens to also import the mutated module will see the mutant. Every
// caller of this file today gets this right (the mutated function itself is
// always imported shadow-relative even when a real-path fixture builds the
// input state), but a future caller reusing fixture.mjs INSIDE the shadow
// tree instead — expecting fixture.mjs's OWN behaviour to reflect a
// mutation — would silently observe the real, unmutated fixture.mjs. If a
// RED-PROOF ever needs fixture.mjs itself to see the mutant, copy it into
// the shadow tree too (as runMutantSelfReinvoke already does for the whole
// webconsole/test directory) rather than referencing it by real absolute path.

import { cpSync, mkdtempSync, rmSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname, relative } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';

// BUG-739 CI-shape follow-up (2026-09-05, same-session P1): this file used
// to live at webconsole/test/helpers/mutant.mjs (and its .d.mts sibling) —
// CI's root `node --test --test-shard=N/3` discovers EVERY file under any
// `test/`-named directory tree, so it auto-discovered mutant.d.mts, tried to
// run it AS a test, and Node's TypeScript type-stripping choked on a bare
// `export const X: T;` ambient declaration (`'const' declarations must be
// initialized`) — a same-session CI red (run 33970897836, node-test shard
// 3). Moved BOTH files here, to webconsole/testsupport/ (a directory NOT
// named test/ or __tests__/, so CI's discovery glob never reaches it) — see
// the reproduction/fix proof in this round's own verification notes.
const TESTSUPPORT_DIR = dirname(fileURLToPath(import.meta.url));
// webconsole/testsupport -> webconsole/src
export const SRC_ROOT = join(TESTSUPPORT_DIR, '..', 'src');
// webconsole/testsupport -> webconsole
const WEBCONSOLE_ROOT = join(TESTSUPPORT_DIR, '..');
const WEBCONSOLE_TEST_ROOT = join(WEBCONSOLE_ROOT, 'test');

// R5 (BUG-739 round REJECT, 2026-09-05): createMutantShadow() hands its
// shadow directory to the CALLER to clean up (it can't wrap the caller's own
// `await import(...)` + assertions in a try/finally itself) — if the caller
// throws somewhere its own try/finally doesn't cover, or the whole process
// dies (an uncaught exception, a SIGKILL from an external watchdog) before
// `cleanup()` runs, the temp directory leaks. Register every shadow root
// created by ANY function in this file in a process-wide set and sweep
// whatever is still pending on process exit — a synchronous best-effort net
// (rmSync is sync-safe inside an 'exit' handler; async cleanup is not) that
// catches the leak case without requiring every caller to get its own
// try/finally exactly right.
const pendingShadowRoots = new Set();
let exitHandlerRegistered = false;
function registerShadowRoot(dir) {
  pendingShadowRoots.add(dir);
  if (!exitHandlerRegistered) {
    exitHandlerRegistered = true;
    process.on('exit', () => {
      for (const d of pendingShadowRoots) {
        try {
          rmSync(d, { recursive: true, force: true });
        } catch {
          // best-effort — process is exiting, nothing more we can do
        }
      }
    });
  }
}
function unregisterShadowRoot(dir) {
  pendingShadowRoots.delete(dir);
}

/**
 * @param {object} opts
 * @param {string} opts.targetRelPath - path relative to webconsole/src, e.g. 'sim/data.ts'
 * @param {(original: string) => string} opts.mutate - must return content different from the input
 * @param {string} opts.childBody - ESM source run in a fresh child process; import specifiers
 *   should be relative to webconsole/src (the child script is written AT the shadow copy's
 *   root, mirroring src/ directly), e.g. `import { x } from './sim/data.ts';`
 * @param {number} [opts.timeoutMs]
 * @param {string[]} [opts.extraArgs] - extra node CLI args inserted before the child script path
 *   (e.g. ['--import', tsxLoaderUrl] if the child needs tsx)
 * @returns {string} the child process's captured stdout
 */
export function runWithMutant({ targetRelPath, mutate, childBody, timeoutMs = 60000, extraArgs = [] }) {
  const targetAbsReal = join(SRC_ROOT, targetRelPath);
  if (!existsSync(targetAbsReal)) {
    throw new Error(`runWithMutant: target not found under SRC_ROOT: ${targetRelPath}`);
  }
  const original = readFileSync(targetAbsReal, 'utf8');
  const mutated = mutate(original);
  if (mutated === original) {
    throw new Error('runWithMutant: mutate() did not change the source — RED-PROOF setup is broken');
  }

  const unsafeWriteInPlace = process.env.MUTANT_UNSAFE_WRITE_IN_PLACE === '1';

  if (unsafeWriteInPlace) {
    // See the "MUTATION-TESTING PROOF KNOB" header comment above — this
    // branch deliberately recreates BUG-739's original defect and must
    // never be exercised outside that proof.
    writeFileSync(targetAbsReal, mutated, 'utf8');
    const childPath = join(SRC_ROOT, '__mutant_unsafe_child.mjs');
    writeFileSync(childPath, childBody, 'utf8');
    try {
      return execFileSync(process.execPath, [...extraArgs, childPath], {
        encoding: 'utf8',
        timeout: timeoutMs,
        cwd: SRC_ROOT,
      });
    } finally {
      writeFileSync(targetAbsReal, original, 'utf8');
      rmSync(childPath, { force: true });
    }
  }

  const shadowRoot = mkdtempSync(join(tmpdir(), 'metropolis-mutant-'));
  registerShadowRoot(shadowRoot);
  try {
    cpSync(SRC_ROOT, shadowRoot, { recursive: true });
    writeFileSync(join(shadowRoot, targetRelPath), mutated, 'utf8');
    const childPath = join(shadowRoot, '__mutant_child.mjs');
    writeFileSync(childPath, childBody, 'utf8');
    return execFileSync(process.execPath, [...extraArgs, childPath], {
      encoding: 'utf8',
      timeout: timeoutMs,
      cwd: shadowRoot,
    });
  } finally {
    rmSync(shadowRoot, { recursive: true, force: true });
    unregisterShadowRoot(shadowRoot);
    const after = readFileSync(targetAbsReal, 'utf8');
    if (after !== original) {
      // Fail LOUD (BUG-708 precedent) — this helper must never be the
      // thing that sabotages the real tree; if this ever fires it means a
      // bug in THIS helper, not in the test using it.
      throw new Error(
        `runWithMutant SAFETY VIOLATION: real file ${targetAbsReal} changed on disk during a ` +
          'mutant run — this helper must never write outside its shadow temp directory.'
      );
    }
  }
}

/**
 * R1 (BUG-739 round REJECT, 2026-09-05) NON-VACUITY PRECONDITION: runs
 * `childBody` against an UNMUTATED shadow copy (same shadow-copy mechanism
 * as runWithMutant, minus the mutation step) so a RED-PROOF can prove its own
 * probe script actually WORKS — loads, resolves every import, and reaches
 * its "PASSED" marker — before trusting that the SAME probe's failure to
 * reach that marker against a mutated copy means the mutation was detected,
 * rather than the probe having been broken (wrong loader flags, an
 * extensionless-import resolution gap, a typo) all along. A probe that
 * crashes identically whether the source is mutated or not is a vacuous
 * RED-PROOF — this function is how a test rules that out FIRST, in the same
 * shape (throws on non-zero exit, same extraArgs contract) as
 * runWithMutant, so both calls in a test read as a matched pair.
 *
 * @param {object} opts
 * @param {string} opts.targetRelPath - path relative to webconsole/src, e.g. 'sim/data.ts'
 *   (only used to size/anchor the shadow copy; not mutated)
 * @param {string} opts.childBody - same contract as runWithMutant's childBody
 * @param {number} [opts.timeoutMs]
 * @param {string[]} [opts.extraArgs]
 * @returns {string} the child process's captured stdout
 */
export function runBaselineProbe({ targetRelPath, childBody, timeoutMs = 60000, extraArgs = [] }) {
  const targetAbsReal = join(SRC_ROOT, targetRelPath);
  if (!existsSync(targetAbsReal)) {
    throw new Error(`runBaselineProbe: target not found under SRC_ROOT: ${targetRelPath}`);
  }
  const shadowRoot = mkdtempSync(join(tmpdir(), 'metropolis-mutant-baseline-'));
  registerShadowRoot(shadowRoot);
  try {
    cpSync(SRC_ROOT, shadowRoot, { recursive: true });
    const childPath = join(shadowRoot, '__mutant_baseline_child.mjs');
    writeFileSync(childPath, childBody, 'utf8');
    return execFileSync(process.execPath, [...extraArgs, childPath], {
      encoding: 'utf8',
      timeout: timeoutMs,
      cwd: shadowRoot,
    });
  } finally {
    rmSync(shadowRoot, { recursive: true, force: true });
    unregisterShadowRoot(shadowRoot);
  }
}

/**
 * Variant for the "revert a fix, then re-run a subset of THIS SAME test
 * file's own tests as a fresh child process and confirm they now fail"
 * pattern (BUG-509/BUG-606/BUG-662/BUG-685-686/largest-first-reround), which
 * needs more than just the mutated src file: the re-invoked test file itself
 * may import test-support modules (scale/fixture.mjs, other test/helpers/*)
 * via its own normal `../src/...`-relative conventions. Rather than
 * enumerate every such dependency, this mirrors the WHOLE webconsole/src AND
 * webconsole/test trees into one shadow "webconsole" directory (cheap: both
 * together are a few MB / a few hundred files, sub-second to copy) and
 * re-invokes the shadow copy of the SAME test file at its equivalent
 * position, so every relative import it makes resolves inside the shadow
 * tree exactly as it would in the real one — the mutated file included.
 *
 * @param {object} opts
 * @param {string} opts.targetRelPath - path relative to webconsole/src, e.g. 'sim/engine.ts'
 * @param {(original: string) => string} opts.mutate
 * @param {string} opts.testFileAbsPath - absolute path of the calling test file (import.meta.url via fileURLToPath)
 * @param {string} [opts.testNamePattern] - passed through as `--test-name-pattern`
 * @param {number} [opts.timeoutMs]
 * @returns {{ failed: boolean, output: string, exitCode: number|null, stdout: string, stderr: string, crashed: boolean }}
 *   `failed`: the child exited non-zero. `crashed` (R2, BUG-739 round REJECT
 *   2026-09-05): true if the child died BEFORE node's test runner ever
 *   printed its own structured summary (no `ℹ tests`/`# tests`/TAP `not
 *   ok`/`ok ` line anywhere in the output) — e.g. a module-resolution error,
 *   a syntax error, or a signal kill at load time, as opposed to a REAL
 *   assertion inside a test actually running and failing. A mutation whose
 *   RED-PROOF only checks `failed` (or a bare `/not ok|fail/i` match against
 *   the output) cannot tell "the mutant was detected" apart from "the child
 *   process crashed for an unrelated reason" — prepending garbage to the
 *   mutated file, for instance, reddens `failed` exactly the same way a real
 *   detection would. Callers MUST check `!crashed` before trusting `failed`,
 *   and should additionally assert the OUTPUT contains the SPECIFIC expected
 *   assertion message for their mutation, never bare exit status alone.
 */
export function runMutantSelfReinvoke({ targetRelPath, mutate, testFileAbsPath, testNamePattern, timeoutMs = 120000 }) {
  const targetAbsReal = join(SRC_ROOT, targetRelPath);
  if (!existsSync(targetAbsReal)) {
    throw new Error(`runMutantSelfReinvoke: target not found under SRC_ROOT: ${targetRelPath}`);
  }
  const original = readFileSync(targetAbsReal, 'utf8');
  const mutated = mutate(original);
  if (mutated === original) {
    throw new Error('runMutantSelfReinvoke: mutate() did not change the source — RED-PROOF setup is broken');
  }

  const relTestPath = relative(WEBCONSOLE_TEST_ROOT, testFileAbsPath);

  const unsafeWriteInPlace = process.env.MUTANT_UNSAFE_WRITE_IN_PLACE === '1';
  if (unsafeWriteInPlace) {
    // See mutant.mjs's header "MUTATION-TESTING PROOF KNOB" comment —
    // deliberately recreates BUG-739's original defect for the round's own
    // mutation-testing proof; never exercised otherwise.
    writeFileSync(targetAbsReal, mutated, 'utf8');
    try {
      return runNodeTestChild(testFileAbsPath, testNamePattern, WEBCONSOLE_ROOT, timeoutMs);
    } finally {
      writeFileSync(targetAbsReal, original, 'utf8');
    }
  }

  const shadowRoot = mkdtempSync(join(tmpdir(), 'metropolis-mutant-webconsole-'));
  registerShadowRoot(shadowRoot);
  try {
    cpSync(SRC_ROOT, join(shadowRoot, 'src'), { recursive: true });
    cpSync(WEBCONSOLE_TEST_ROOT, join(shadowRoot, 'test'), { recursive: true });
    // The re-invoked test FILE's own top-level `import ... from
    // '../testsupport/mutant.mjs'` (this very module) executes on load
    // regardless of which single test --test-name-pattern selects, so the
    // shadow needs a testsupport/ sibling too or the whole file fails to
    // load with ERR_MODULE_NOT_FOUND (BUG-739 CI-shape follow-up, same move
    // that relocated this helper out of webconsole/test/).
    cpSync(join(WEBCONSOLE_ROOT, 'testsupport'), join(shadowRoot, 'testsupport'), { recursive: true });
    writeFileSync(join(shadowRoot, 'src', targetRelPath), mutated, 'utf8');
    const shadowTestPath = join(shadowRoot, 'test', relTestPath);
    return runNodeTestChild(shadowTestPath, testNamePattern, shadowRoot, timeoutMs);
  } finally {
    rmSync(shadowRoot, { recursive: true, force: true });
    unregisterShadowRoot(shadowRoot);
    const after = readFileSync(targetAbsReal, 'utf8');
    if (after !== original) {
      throw new Error(
        `runMutantSelfReinvoke SAFETY VIOLATION: real file ${targetAbsReal} changed on disk during a ` +
          'mutant run — this helper must never write outside its shadow temp directory.'
      );
    }
  }
}

/**
 * IN-PROCESS variant for a RED-PROOF that needs to `await import(...)` the
 * mutated module itself (rather than spawning a child process) — e.g. to
 * reuse constants/helpers already in scope in the calling test file, the way
 * a bare cache-busting `import('../src/sim/engine.ts?redproof=' + Date.now())`
 * of the REAL file used to. A distinct shadow-copy path is a cache MISS on
 * its own (Node's ESM cache key is the resolved URL), so no query-string
 * trick is needed once the module lives at a genuinely different path.
 *
 * @param {object} opts
 * @param {string} opts.targetRelPath - path relative to webconsole/src, e.g. 'sim/engine.ts'
 * @param {(original: string) => string} opts.mutate
 * @returns {{ importUrl: (relPath: string) => string, cleanup: () => void }}
 *   `importUrl('sim/engine.ts')` returns a `file:` URL suitable for
 *   `await import(...)`, resolved against the shadow copy (so it also works
 *   for a module that imports the SAME shadow tree's other files, e.g.
 *   engine.ts importing data.ts, both from the shadow). `cleanup()` removes
 *   the shadow directory and verifies the real file was never touched — call
 *   it in a `finally`. R5 (BUG-739 round REJECT, 2026-09-05): the shadow
 *   directory is ALSO registered against this module's process-exit sweep
 *   (see registerShadowRoot's comment above) as a best-effort backstop, in
 *   case the caller's own `finally` never runs (an uncaught exception
 *   outside it, or the process dying outright) — `cleanup()` remains the
 *   primary, deterministic path and unregisters the directory from that
 *   sweep once it has actually run.
 */
export function createMutantShadow({ targetRelPath, mutate }) {
  const targetAbsReal = join(SRC_ROOT, targetRelPath);
  if (!existsSync(targetAbsReal)) {
    throw new Error(`createMutantShadow: target not found under SRC_ROOT: ${targetRelPath}`);
  }
  const original = readFileSync(targetAbsReal, 'utf8');
  const mutated = mutate(original);
  if (mutated === original) {
    throw new Error('createMutantShadow: mutate() did not change the source — RED-PROOF setup is broken');
  }
  if (process.env.MUTANT_UNSAFE_WRITE_IN_PLACE === '1') {
    // See mutant.mjs's header "MUTATION-TESTING PROOF KNOB" comment —
    // deliberately recreates BUG-739's original defect; never exercised
    // otherwise. Imports the REAL path directly with a cache-busting query,
    // exactly like the pre-BUG-739 idiom this helper replaces.
    writeFileSync(targetAbsReal, mutated, 'utf8');
    return {
      importUrl: (relPath) =>
        `${pathToFileURL(join(SRC_ROOT, relPath)).href}?mutant-unsafe=${Date.now()}-${Math.random()}`,
      cleanup: () => {
        writeFileSync(targetAbsReal, original, 'utf8');
      },
    };
  }

  const shadowRoot = mkdtempSync(join(tmpdir(), 'metropolis-mutant-inproc-'));
  registerShadowRoot(shadowRoot);
  cpSync(SRC_ROOT, shadowRoot, { recursive: true });
  writeFileSync(join(shadowRoot, targetRelPath), mutated, 'utf8');
  return {
    importUrl: (relPath) => pathToFileURL(join(shadowRoot, relPath)).href,
    cleanup: () => {
      rmSync(shadowRoot, { recursive: true, force: true });
      unregisterShadowRoot(shadowRoot);
      const after = readFileSync(targetAbsReal, 'utf8');
      if (after !== original) {
        throw new Error(
          `createMutantShadow SAFETY VIOLATION: real file ${targetAbsReal} changed on disk during a ` +
            'mutant run — this helper must never write outside its shadow temp directory.'
        );
      }
    },
  };
}

// R2 (BUG-739 round REJECT, 2026-09-05): node's default `spec` reporter
// prints a structured summary line for every completed `--test` run —
// `ℹ tests N` (this Node version's default reporter) or, under `--test-
// reporter=tap`, `# tests N` / per-test `ok N ...` / `not ok N ...` lines.
//
// MEASURED DIRECTLY (not assumed): a garbage-prepend mutation that breaks
// the MUTATED FILE's syntax does NOT simply omit the summary — Node's test
// runner actually catches the import failure of the file under test and
// still emits a full `ℹ tests 1` / `ℹ fail 1` summary, reporting the
// load failure AS a failed test. A bare "does a summary marker exist" check
// would therefore call this case NOT crashed, exactly the false-negative the
// round's own garbage-prepend probe found. So `crashed` also fires when the
// output carries a LOAD-TIME crash signature (a module-resolution error, a
// TypeScript-stripping syntax error, or the raw
// `triggerUncaughtException`/`ERR_UNSUPPORTED_ESM_URL_SCHEME` internals a
// pre-test-harness crash prints) with NO `AssertionError` anywhere in the
// output — a real assertion failure's stack trace always contains
// `AssertionError`, so requiring its ABSENCE keeps this from ever
// mis-flagging a genuine detection that merely mentions a similar word.
const TEST_RUNNER_SUMMARY_MARKER = /ℹ tests \d+|# tests \d+|^(?:not )?ok \d+/m;
const LOAD_TIME_CRASH_SIGNATURE =
  /ERR_MODULE_NOT_FOUND|Cannot find module|ERR_INVALID_TYPESCRIPT_SYNTAX|ERR_UNSUPPORTED_ESM_URL_SCHEME|triggerUncaughtException|ERR_UNKNOWN_FILE_EXTENSION/;

function runNodeTestChild(testFileAbsPath, testNamePattern, cwd, timeoutMs) {
  // NODE_TEST_CONTEXT must NOT be inherited: this helper is normally called
  // FROM inside an outer `node --test` run, which sets that var for the
  // CURRENT process; execFileSync inherits the parent env by default, and an
  // inherited NODE_TEST_CONTEXT makes Node's test runner treat the child as
  // a recursive re-entry, reporting over IPC to the (unrelated) outer runner
  // instead of exiting non-zero on failure — silently turning any RED-PROOF
  // using this helper into a false pass (confirmed empirically by every one
  // of this helper's callers' own prior documented notes to this effect).
  const childEnv = { ...process.env };
  delete childEnv.NODE_TEST_CONTEXT;
  const args = [
    '--test',
    ...(testNamePattern ? [`--test-name-pattern=${testNamePattern}`] : []),
    testFileAbsPath,
  ];
  let failed = false;
  let exitCode = 0;
  let stdout = '';
  let stderr = '';
  try {
    stdout = execFileSync(process.execPath, args, {
      cwd,
      encoding: 'utf8',
      stdio: 'pipe',
      env: childEnv,
      timeout: timeoutMs,
    });
  } catch (err) {
    failed = true;
    exitCode = typeof err.status === 'number' ? err.status : null;
    stdout = err.stdout || '';
    stderr = err.stderr || '';
  }
  const output = stdout + stderr;
  const hasSummary = TEST_RUNNER_SUMMARY_MARKER.test(output);
  const hasLoadTimeCrashSignature = LOAD_TIME_CRASH_SIGNATURE.test(output) && !/AssertionError/.test(output);
  const crashed = !hasSummary || hasLoadTimeCrashSignature;
  return { failed, output, exitCode, stdout, stderr, crashed };
}
