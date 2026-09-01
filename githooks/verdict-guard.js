// Module key: tool.gitverdictguard (BOW ref pending — see BUG-340/BUG-336)
// Spec ref: GR#23 (Nothing Is Committed Un-Attacked); GR#23 independence
// amendment; M0-ENG §5 (hooks)

/**
 * githooks/verdict-guard.js — TRACKED canonical source for the git-side half
 * of GR#23's Destructive-verdict gate (BUG-340/BUG-336, deliverable 1).
 *
 * WHY THIS EXISTS ON TOP OF claude-destructive-guard.js.
 *
 * claude-destructive-guard.js is a PreToolUse hook: it only ever sees a
 * commit that arrives as a Bash/PowerShell tool call inside a Claude Code
 * session. Every OTHER route to `git commit` — a plain terminal, a non-Claude
 * lane (Bro/Ben today, per CLAUDE.md), a script, an IDE's built-in git
 * integration — never passes through that hook at all, so a code-bearing
 * commit from any of those routes reaches history with ZERO Destructive
 * verdict enforcement. This file closes that gap the same way githooks/
 * commit-msg closed the identity gap for non-Claude commits (FEAT-045): a
 * git-level hook fires for EVERY tool that shells out to `git commit`,
 * regardless of what invoked it.
 *
 * WHY commit-msg's SLOT, NOT a fresh hook file. `commit-msg` already fires
 * for `commit` and `merge` (see githooks/commit-msg's own header for the
 * live evidence and the ASM-386 cherry-pick/revert/am gap, which applies
 * here identically — this file inherits that same disclosed limitation).
 * This module is a plain Node module (no shebang, not itself installed as a
 * hook file) that githooks/commit-msg `require()`s and calls as a THIRD
 * check in the same slot, alongside FEAT-045 (identity) and FEAT-046
 * (codename) — see commit-msg's own header for why one hook slot can only
 * run one script and therefore chains checks internally.
 *
 * WHAT THIS CHECK DOES. Reads the ACTUAL staged diff (`git diff --cached
 * --name-only`) and the ACTUAL commit message (the file path commit-msg's
 * own argv[2] names) directly from git — no command-string parsing needed
 * here (unlike claude-destructive-guard.js, which has to reconstruct intent
 * from a Bash/PowerShell command string because a PreToolUse hook only ever
 * sees that string). Classification of "code-bearing" and "exempt
 * (docs/test-only)" is DERIVED FROM, not reimplemented against,
 * claude-destructive-guard.js's own exported pure functions
 * (isEnforcedDirPath / isGuardOrHookPath / isExemptFileSet /
 * deriveRootGuardScripts / isRootLevel / normalizeGitPath / extractTags) —
 * GR#3, one source of truth for "what counts as code-bearing", never a
 * second hand-maintained copy that could silently drift from the
 * PreToolUse guard's own answer.
 *
 * If the staged set is code-bearing and not exempt, every `[mkey]`/`[CODE]`
 * tag in the message must resolve to a BOW item carrying a covering
 * Destructive ACCEPT verdict — same verdict-tie rule (a git ref recorded
 * at-or-after the accept verdict voids it) and same BUG-340 self-verdict
 * refusal (a verdict recorded by THIS SAME session does not count) as
 * claude-destructive-guard.js implements — again by calling claude-bow.js's
 * shared query functions, never a second query.
 *
 * POSTURE — fail-closed on a DEFINITIVE no-verdict, but DB-unreachable does
 * not brick every tool's commits silently: it fails closed TOO (a real
 * "cannot verify" IS a GR#23 failure to verify), but the deny message names
 * the exact remediation (the operator-only bypass env var below) loudly, so
 * an outage never becomes an unrecoverable lockout for a non-Claude lane
 * that cannot read this file's source to find the escape hatch on its own.
 * This mirrors claude-destructive-guard.js's own AC-22/AC-18 reasoning
 * (a false block costs seconds to diagnose and bypass; an un-attacked
 * commit on a public repo cannot be undone).
 *
 * BYPASS (operator-only, set BEFORE the session/shell starts — never
 * inline in the commit command itself, same convention as
 * CLAUDE_DISABLE_DESTRUCTIVE_GUARD): CLAUDE_DISABLE_GIT_VERDICT_GUARD=1
 *
 * PROPORTIONALITY TIER (FEAT-077, unchanged): a commit whose ENTIRE staged
 * set is docs-only (`*.md`) or test-only (`*.test.js`/`*_test.go`) is exempt
 * — Tester-level verification suffices, no Destructive verdict required.
 *
 * SCOPE: same as claude-destructive-guard.js's own ASM-193 ruling — only a
 * genuine `commit`/`merge` invocation of THIS hook slot is covered.
 * cherry-pick/revert/am are the same disclosed ASM-386 gap as the identity
 * and codename checks in this same slot.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

const BYPASS_ENV = 'CLAUDE_DISABLE_GIT_VERDICT_GUARD';

/** Same two-candidate resolution shape as commit-msg's own
 * resolveIdentityModulePath()/resolveCodenameScanModulePath() — this
 * module's installed location (required FROM .git/hooks/commit-msg) differs
 * from its tracked-source location (required directly by tests from
 * githooks/), so both candidates are tried, existence-checked, never
 * guessed. */
function resolveSiblingModulePath(filename) {
  const candidates = [
    path.join(__dirname, '..', filename), // tracked source, required from githooks/
    path.join(__dirname, '..', '..', filename), // installed, required from .git/hooks/
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error(`${filename} not found relative to ${__dirname} (tried: ${candidates.join(', ')})`);
}

/** Loads ONLY claude-destructive-guard.js and validates its shape — the
 * lightweight half of this file's dependencies, needed for EVERY commit
 * (staged-file classification: enforced-dir / guard-hook-path / exempt /
 * root-guard-script). Deliberately split from loadVerdictDeps() below: this
 * module has NO heavy top-level requires of its own (no DB driver, no
 * claude-bow.js), so a commit that turns out to be non-code-bearing or
 * exempt never pays the cost of (or needs the presence of) claude-bow.js /
 * claude-db.js / mysql2 at all — same "keep runtime fast" reasoning
 * claude-destructive-guard.js itself applies to its own lazily-required
 * author-guard/bow deps. Same "wrong shape is as dangerous as missing"
 * posture as that file's own loadDependencies(). */
function loadClassificationDeps() {
  const destructiveGuardPath =
    process.env.CLAUDE_VERDICT_GUARD_DESTRUCTIVEGUARD_PATH || resolveSiblingModulePath('claude-destructive-guard.js');
  // eslint-disable-next-line global-require, import/no-dynamic-require
  const dg = require(destructiveGuardPath);

  const requiredDgFns = [
    'isEnforcedDirPath', 'isGuardOrHookPath', 'isExemptFileSet', 'deriveRootGuardScripts',
    'isRootLevel', 'normalizeGitPath', 'extractTags', 'noTagDenyMessage',
    'verdictDenyMessage', 'postAttackDenyMessage', 'selfVerdictDenyMessage',
  ];
  const missingDgFns = requiredDgFns.filter((fn) => typeof dg[fn] !== 'function');
  if (missingDgFns.length) {
    throw new Error(
      `claude-destructive-guard.js loaded but did not export the expected function(s): ${missingDgFns.join(', ')} ` +
        '(wrong module shape — cannot classify code-bearing/exempt commits without these)'
    );
  }
  return { dg };
}

/** Loads claude-bow.js and claude-db.js — the DB half of this file's
 * dependencies. Called ONLY once a commit has already been determined
 * code-bearing, non-exempt, AND carrying at least one tag (see
 * checkStagedCommit() below) — a non-code-bearing or exempt commit, or a
 * code-bearing one with zero tags (denied before ever reaching here), never
 * needs a DB driver present at all. */
function loadVerdictDeps() {
  const bowPath = process.env.CLAUDE_VERDICT_GUARD_BOW_PATH || resolveSiblingModulePath('claude-bow.js');
  const dbPath = process.env.CLAUDE_VERDICT_GUARD_DB_PATH || resolveSiblingModulePath('claude-db.js');

  // eslint-disable-next-line global-require, import/no-dynamic-require
  const bow = require(bowPath);
  // eslint-disable-next-line global-require, import/no-dynamic-require
  const dbMod = require(dbPath);
  if (typeof dbMod.connect !== 'function') {
    throw new Error('claude-db.js loaded but did not export the expected function: connect');
  }
  const requiredBowFns = [
    'ensureSchema', 'findItemByRef', 'latestDestructiveVerdict',
    'latestGitRefForItem', 'currentSessionIdentity', 'isSelfVerdict',
  ];
  const missingBowFns = requiredBowFns.filter((fn) => typeof bow[fn] !== 'function');
  if (missingBowFns.length) {
    throw new Error(
      `claude-bow.js loaded but did not export the expected function(s): ${missingBowFns.join(', ')} ` +
        '(wrong module shape — cannot verify a Destructive verdict without these)'
    );
  }
  if (!bow.TYPE_PREFIX || typeof bow.TYPE_PREFIX !== 'object' || !Object.values(bow.TYPE_PREFIX).length) {
    throw new Error('claude-bow.js loaded but did not export a usable TYPE_PREFIX');
  }

  return { bow, db: dbMod, typePrefixes: new Set(Object.values(bow.TYPE_PREFIX)) };
}

/** Back-compat combined shape — both phases at once. checkStagedCommit()
 * below never calls this; it calls the two phases separately so a
 * non-code-bearing/exempt commit skips the DB half entirely. Kept for any
 * caller (e.g. a test) that wants everything in one call. */
function loadDependencies() {
  const { dg } = loadClassificationDeps();
  const { bow, db, typePrefixes } = loadVerdictDeps();
  return { dg, bow, db, typePrefixes };
}

/** `git diff --cached --name-only` from `cwd` (defaults to process.cwd(),
 * which for an installed git hook is the repo's top-level working directory
 * — the same assumption claude-destructive-guard.js's own rootDir() makes,
 * for the same reason: a test harness can override `cwd` to point at an
 * isolated throwaway repo). Normalised via the SAME normalizeGitPath()
 * claude-destructive-guard.js uses for its own staged-file classification. */
// BUG-340 r1 F3 (independent round REJECT, finding A1): `--no-renames` is
// REQUIRED here. Without it, `git diff --cached --name-only` collapses a
// rename to ONLY the destination path — the deleted source is invisible to
// this function entirely. Live-verified in round r1: renaming
// `internal/foo.go` -> `internal/foo.md` (or `claude-destructive-guard.js`
// -> `claude-destructive-guard.test.js`) reported staged === [the new path]
// only, which isExemptFileSet() then reads as an all-docs/all-test-only
// commit — a silent, zero-verdict way to delete a code-bearing file from an
// enforced directory. `--no-renames` forces git to report BOTH sides
// (delete + add) as two separate entries, so the deleted code-bearing path
// is classified on its own merits.
function getStagedFiles(dg, cwd) {
  const raw = execFileSync('git', ['diff', '--cached', '--no-renames', '--name-only'], { cwd, encoding: 'utf8' });
  return raw.split('\n').map((f) => f.trim()).filter(Boolean).map(dg.normalizeGitPath);
}

/** True when `files` (already normalised) touch an enforced directory, a
 * guard/hook path, or a root script wired into .claude/settings.json — the
 * SAME three-part code-bearing test claude-destructive-guard.js applies,
 * reusing its own exported predicates rather than a second copy. Throws
 * (fail-closed, per AC-11's consequence in claude-destructive-guard.js) if
 * settings.json needs consulting and cannot be read/parsed. */
function isCodeBearing(dg, files) {
  if (files.some(dg.isEnforcedDirPath) || files.some(dg.isGuardOrHookPath)) return true;
  const rootLevelFiles = files.filter(dg.isRootLevel);
  if (!rootLevelFiles.length) return false;
  const rootScripts = dg.deriveRootGuardScripts();
  return rootLevelFiles.some((f) => rootScripts.has(f));
}

/** Core verdict evaluation, factored out from main() for direct unit
 * testing against a fake `db`/`bow` (no real DB, no real git) — mirrors
 * claude-destructive-guard.js's own checkAgainstSanctioned() shape (pure
 * decision logic, separate from stdin/DB plumbing). Returns
 * { unresolved, missing, selfVerdicts, postAttack } — all four arrays,
 * empty when everything is clean. */
async function evaluateTags(db, bow, tags, mySessionId, myRepoRoot) {
  const unresolved = [];
  const missing = [];
  const selfVerdicts = [];
  const postAttack = [];
  for (const tag of tags) {
    // eslint-disable-next-line no-await-in-loop
    const item = await bow.findItemByRef(db, tag);
    if (!item) {
      unresolved.push(tag);
      continue;
    }
    // eslint-disable-next-line no-await-in-loop
    const verdict = await bow.latestDestructiveVerdict(db, tag);
    if (!verdict || verdict.verdict !== 'accept') {
      missing.push({ code: item.code, title: item.title, state: verdict ? verdict.verdict : 'none' });
      continue;
    }
    // BUG-340 r1 F1: same shared "session AND cwd both match" rule as
    // claude-destructive-guard.js — bow.isSelfVerdict() is the ONE place
    // this logic lives (GR#3), never a second hand-copied comparison here.
    const recorder = (verdict.recorder_session || '').trim();
    const recorderCwd = (verdict.recorder_cwd || '').trim();
    if (bow.isSelfVerdict(verdict, mySessionId, myRepoRoot)) {
      selfVerdicts.push({ code: item.code, title: item.title, recorder, recorderCwd });
      continue;
    }
    // eslint-disable-next-line no-await-in-loop
    const gitRef = await bow.latestGitRefForItem(db, tag);
    if (gitRef && new Date(gitRef.created_at).getTime() >= new Date(verdict.created_at).getTime()) {
      postAttack.push({
        code: item.code, title: item.title, refHash: gitRef.commit_hash,
        refAt: gitRef.created_at, verdictAt: verdict.created_at,
      });
    }
  }
  return { unresolved, missing, selfVerdicts, postAttack };
}

function dbUnreachableMessage(err, tags) {
  return (
    `🛑 GIT VERDICT GUARD (GR#23, BUG-340/BUG-336): metro MariaDB is unreachable ` +
    `(${err.message}) — cannot verify Destructive verdict(s) for [${tags.join('], [')}]. Denying ` +
    `(fail-closed; a DB outage is not proof a verdict exists).\n\n` +
    `Fix the DB connection and retry, OR bypass deliberately (operator-only, set BEFORE the shell ` +
    `starts, never inline in the commit command): ${BYPASS_ENV}=1\n`
  );
}

function internalErrorMessage(err) {
  return (
    `🛑 GIT VERDICT GUARD internal error: ${err && err.message ? err.message : err}\n\n` +
    `Failing closed deliberately (GR#23 is a security rule). Bypass only if you have checked by ` +
    `hand (operator-only, set BEFORE the shell starts): ${BYPASS_ENV}=1\n`
  );
}

/** Full entry point. `cwd` defaults to process.cwd() (production); tests
 * override it to point at a throwaway repo. Returns { ok: true } or
 * { ok: false, reason }. Never throws for a normal deny (that is a decision
 * to report, not an internal error) — throws bubble to the caller's own
 * fail-closed wrapper (see the CLI block below), exactly like
 * claude-destructive-guard.js's main()/main().catch() split.
 */
async function checkStagedCommit(cwd) {
  if (process.env[BYPASS_ENV] === '1') {
    return { ok: true, bypassed: true };
  }

  // Phase 1: classification only (dg — lightweight, no DB driver needed).
  // A non-code-bearing or exempt commit returns here WITHOUT ever loading
  // claude-bow.js/claude-db.js — see loadClassificationDeps()'s own header.
  const { dg } = loadClassificationDeps();

  const stagedFiles = getStagedFiles(dg, cwd);
  if (!isCodeBearing(dg, stagedFiles)) {
    return { ok: true };
  }
  if (dg.isExemptFileSet(stagedFiles)) {
    // FEAT-077 proportionality tier: docs-only/test-only, no verdict needed.
    return { ok: true };
  }

  const messagePath = process.argv[2];
  let message = '';
  if (messagePath) {
    try {
      message = fs.readFileSync(messagePath, 'utf8');
    } catch (err) {
      return { ok: false, reason: `could not read the commit-message file (${err.message}) — failing closed.` };
    }
  }

  // Phase 2: the DB half — loaded ONLY now that the commit is confirmed
  // code-bearing and non-exempt.
  const { bow, db: dbMod, typePrefixes } = loadVerdictDeps();

  const tags = dg.extractTags(message, typePrefixes);
  if (tags.length === 0) {
    return { ok: false, reason: dg.noTagDenyMessage(BYPASS_ENV) };
  }

  // BUG-340/BUG-336: uses claude-db.js's throw-on-failure connect() directly
  // (a bounded 4s connectTimeout, same as claude-destructive-guard.js's own
  // connectReadOnly()) — NOT claude-bow.js's `connect` export, which is the
  // interactive-CLI flavour that prints a generic message and calls
  // process.exit(1) on failure. This module needs the throw so it can
  // report the SPECIFIC, loudly-bypassable deny message below, not a bare
  // process exit with no remediation text.
  let db;
  try {
    db = await dbMod.connect({ connectTimeout: 4000 });
  } catch (err) {
    return { ok: false, reason: dbUnreachableMessage(err, tags) };
  }
  try {
    await bow.ensureSchema(db);
    const mySessionId = bow.currentSessionIdentity();
    // BUG-340 r1 F1: the COMMITTER's repo root for this git-side hook is the
    // `cwd` this whole check ran against (git's own hook contract fires with
    // cwd already at the repo top-level) — resolved via git itself rather
    // than trusted blindly, same posture as claude-destructive-guard.js's
    // own repoRootForDir().
    let myRepoRoot = null;
    try {
      myRepoRoot = execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd, encoding: 'utf8' }).trim() || null;
    } catch { /* unresolvable -> isSelfVerdict() treats null as "never matches" */ }
    const { unresolved, missing, selfVerdicts, postAttack } = await evaluateTags(db, bow, tags, mySessionId, myRepoRoot);
    if (unresolved.length || missing.length) {
      return { ok: false, reason: dg.verdictDenyMessage(unresolved, missing, BYPASS_ENV) };
    }
    if (selfVerdicts.length) {
      return { ok: false, reason: dg.selfVerdictDenyMessage(selfVerdicts, BYPASS_ENV) };
    }
    if (postAttack.length) {
      return { ok: false, reason: dg.postAttackDenyMessage(postAttack, BYPASS_ENV) };
    }
    return { ok: true };
  } finally {
    try { await db.end(); } catch { /* ignore */ }
  }
}

module.exports = {
  BYPASS_ENV,
  resolveSiblingModulePath,
  loadDependencies,
  loadClassificationDeps,
  loadVerdictDeps,
  getStagedFiles,
  isCodeBearing,
  evaluateTags,
  dbUnreachableMessage,
  internalErrorMessage,
  checkStagedCommit,
};
