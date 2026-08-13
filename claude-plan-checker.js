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
    const outputPaths = [CODE_JSON_PATH, BOW_IMPORT_PATH];
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
  hashFiles,
  checkPlan,
};
