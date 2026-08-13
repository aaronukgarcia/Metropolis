/**
 * PreToolUse hook — Destructive-verdict commit gate (BOW mkey:
 * tool.destructiveguard, FEAT-040).
 *
 * Spec: GR#23 "Nothing Is Committed Un-Attacked" (CLAUDE.md, added
 * 2026-08-10); GR#15 (Validators Derive From Data); GR#12 (Dependency &
 * Completeness Check); docs/planning/acceptance/tool.destructiveguard.md (30
 * ACs); docs/planning/dev-team-process.md §"The Destructive agent (v1.8)".
 *
 * WHY THIS EXISTS — the evidence, not the principle.
 *
 * GR#23 was already mandatory prose in the dev-team process document, and it
 * was skipped anyway: BUG-034 (perf drift guard) and BUG-035 (author guard
 * v1) both reached public `main` on Tester-PASS evidence alone, no
 * Destructive round. Both were later rejected — BUG-035's guard fell to a
 * single ordinary `git -c user.email=... commit`, BUG-034's drift guard fell
 * to a one-line method-value change. A rule that lives in the lead's head
 * dies with the window or with a busy morning. This hook is what turns
 * "remember to do it" into "the tool checks" — the same move
 * claude-codename-guard.js made for GR#22.
 *
 * WHAT THIS GUARD DOES.
 *
 * Intercepts `git commit` only (not merge/cherry-pick/revert/rebase — see
 * "SCOPE" below). If the STAGED files (`git diff --cached --name-only`,
 * never the command string — same AC-3 precedent as claude-bow-ref-check.js)
 * touch a code-bearing path, the commit message must carry at least one
 * `[mkey]`/`[CODE]` tag, EVERY tag must resolve to a live BOW item, and EVERY
 * resolved item's LATEST recorded Destructive verdict (via
 * latestDestructiveVerdict(), required from claude-bow.js — never a second,
 * re-implemented query) must be `accept`. Any item missing a verdict, whose
 * latest verdict is `reject`, or whose tag does not resolve at all, is named
 * individually in the deny reason.
 *
 * THE BYPASS TRAP (AC-16). A code-bearing commit whose message carries ZERO
 * recognisable tags is DENIED, not silently allowed. A gate that only checks
 * "for every tag present, is it verdict-clean" is vacuously happy over an
 * empty tag list — that is the exact loophole GR#23's own description names
 * ("must not become trivially bypassable by omitting the BOW code").
 *
 * ROOT-GUARD-SCRIPT BOUNDARY IS DATA, NOT A NAME PATTERN (AC-11, lead ruling
 * on ASM-192, node claude-bow.js show FEAT-040). "Root guard scripts" means
 * whatever is CURRENTLY WIRED into .claude/settings.json's `hooks` array,
 * parsed at runtime — never a `claude-*guard*.js` filename regex. A name
 * pattern is wrong in both directions: a wired script not named "guard"
 * (e.g. claude-bow-ref-check.js) would escape it, and an unwired scratch
 * file that happens to be named like one would wrongly trigger it.
 * CONSEQUENCE (the ruling is explicit about this): a malformed or unreadable
 * settings.json must DENY when a root-level file is actually staged and its
 * status is therefore ambiguous — never silently fall back to a name
 * pattern, because that reintroduces exactly what the ruling rejected. (A
 * commit that stages no root-level file at all never needs settings.json —
 * its code-bearing status is fully decided by the cmd/internal/data/tools
 * prefix check, so settings.json being broken cannot deny an unrelated
 * docs-only commit; see AC-12 and the code below.)
 *
 * FAIL-CLOSED — DELIBERATELY THE OPPOSITE OF tool.bow's POSTURE. Unlike
 * claude-bow-ref-check.js (a traceability HYGIENE gate, fail-open by
 * design — a DB outage or an unparseable message never brick an unrelated
 * commit), this guard enforces a SECURITY rule. Its whole job is "a
 * code-bearing commit must not reach history without a verdict on file", so
 * an internal error while trying to answer that question (DB unreachable,
 * unparseable message, git failing, settings.json malformed while a root
 * file is staged, any unexpected exception) must DENY, never fall back to
 * "cannot verify, allow with a warning". The two postures are opposite on
 * purpose — do not copy tool.bow's fail-open behaviour into this file.
 *
 * THE ESCAPE HATCH IS OPERATOR-ONLY (AC-18/AC-19, weakness pattern #5 — "a
 * guard must not damage what it protects"). CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1
 * is read from `process.env` in THIS process, never parsed out of the
 * proposed command string. A PreToolUse hook runs in the harness process to
 * decide whether to ALLOW a command, before that command's own child shell
 * exists — an env var an agent writes into the very command it is asking
 * permission for is never visible to the hook evaluating that request, no
 * matter how it is written (`CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1 git commit
 * ...` inline changes nothing here). Set it in the environment that LAUNCHES
 * the session, before the session starts, exactly like
 * claude-secret-guard.js / claude-author-guard.js / claude-codename-guard.js.
 * Cost asymmetry that justifies fail-closed-by-default: a false block costs
 * a human seconds to diagnose and bypass deliberately; a code-bearing commit
 * that skipped adversarial review, on a PUBLIC repo, is not something a
 * later commit can retroactively undo.
 *
 * BOOTSTRAP (AC-20). This guard's OWN commit satisfies GR#23 without
 * circularity because verdict recording is a BOW/database write, independent
 * of git: a Destructive round runs against this file and claude-bow.js's new
 * surface, `node claude-bow.js destructive FEAT-040 --verdict accept ...` is
 * executed, and only THEN is the commit (carrying `[tool.destructiveguard]`
 * or `[FEAT-040]`, in the same commit that wires this hook into
 * .claude/settings.json) proposed. The verdict already exists as queryable
 * data by the time `git commit` runs — this file never needs to have been
 * live even once before the commit it is validating. FEAT-040 is not exempt
 * from the same pipeline (BA -> junior -> Tester -> Destructive -> commit)
 * it mechanises for everyone else.
 *
 * SCOPE (ASM, logged — see report). Only `git commit` is intercepted, same
 * decision claude-author-guard.js made for `git rebase`: `merge`,
 * `cherry-pick`, `revert` replay or combine content that was already
 * attacked when it was first created, so (today) they introduce no
 * un-attacked code — lead ruling on ASM-193 accepts this for the current
 * version and flags it for re-examination the day someone resolves a merge
 * conflict by writing genuinely new code.
 *
 * READ-ONLY WITH RESPECT TO THE BOW (AC-28). This file only ever READS a
 * verdict (via claude-bow.js's latest-verdict lookup and its item-resolution
 * helper). It holds no code path anywhere that WRITES one — a gate that
 * could grant its own passing verdict would defeat the entire point of the
 * rule it enforces. (Deliberately not naming the write-side export here in
 * prose, so a plain textual search of this file for that identifier proves
 * the absence rather than merely reading a comment that asserts it.)
 *
 * Receives JSON on stdin: { tool: "Bash"|"PowerShell", tool_input: { command: "..." } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 * (same convention as every sibling guard in this repo)
 *
 * Sits in .claude/settings.json's PreToolUse Bash and PowerShell matcher
 * arrays, appended immediately after claude-author-guard.js.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');
const mysql = require('mysql2/promise');

// ---------------------------------------------------------------------------
// DEPENDENCY LOADING — deliberately NOT `require`d at module top level
// (round-3 Tester finding against round 2; see ASM-358 for the recorded
// judgement call and its risk, and BUG-075 for the separate finding that a
// round-2 report cited three ASM codes — ASM-347/348/349 — that were never
// actually filed; the real codes for this round's judgement calls are
// ASM-358 through ASM-362, verified by their own `node claude-bow.js show
// FEAT-040` output, not carried forward from any prior report).
//
// THE BUG round 2 SHIPPED: `require('./claude-author-guard.js')` and
// `require('./claude-bow.js')` sat at module top level, OUTSIDE main() and
// therefore outside the `main().catch()` wrapper AC-25's fail-closed
// guarantee depends on. A synchronous throw during `require()` (module
// missing, a syntax error in the file being required, either of which is an
// ORDINARY event while claude-author-guard.js is under live concurrent edit
// in this same tree) happens during MODULE EVALUATION, before main() is even
// called, so main().catch() never attaches and never runs. Node's default
// behaviour for an uncaught exception during module load is to print a
// stack trace to stderr and exit 1 — NO stdout, NO hookSpecificOutput JSON.
// Under the PreToolUse contract that is a *non-blocking error*: the proposed
// `git commit` PROCEEDS. So a broken dependency didn't just disable bypass
// detection — it disabled the entire gate, tag/verdict checks included, in
// exactly the crash-open shape AC-21/AC-25 claim cannot happen.
//
// THE INVARIANT THIS FILE NOW HOLDS: this guard must never terminate without
// emitting a decision (an explicit deny() JSON payload, or the established
// silent-allow exit(0) this guard already uses for every non-commit / exempt
// command). "Terminate" includes module-load time, not just runtime inside
// main() — a load-time throw is exactly as dangerous as a runtime throw, and
// is handled the same way: caught, turned into a decision, never left to
// bubble into an uncaught exit.
//
// THE FIX: both requires move INSIDE loadDependencies(), called from within
// main() (see below), where a throw is an ordinary JS exception inside an
// async function — it rejects main()'s promise and is caught by the existing
// main().catch() net, exactly like any other runtime error already handled
// there (AC-25). No new catch mechanism was invented; the existing one now
// actually covers the path it always claimed to.
//
// SCOPE OF THE FIX (deliberately not `mysql2/promise` too): Ben's brief
// named `claude-author-guard.js` and `claude-bow.js` specifically — both are
// THIS repo's own files, actively hand-edited by other live agents right
// now, which is the concrete, non-hypothetical risk. `mysql2/promise` is a
// vendored npm dependency with its own package-manager integrity guarantees
// (package-lock.json), not a file another agent in this tree is mid-edit on;
// widening this fix to it was out of round-3's stated scope and is not
// re-litigated here.
//
// TESTABILITY SEAM (round-3 addition, mirrors ASM-destructiveguard-root-via-
// cwd's existing precedent of a narrow, documented seam rather than testing
// against this repo's own live, concurrently-edited files): the two
// dependency paths are resolved via optional env var overrides,
// CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH / CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH,
// defaulting to the real sibling files in THIS directory when unset (so
// production behaviour — including which path check gets exercised — is
// byte-for-byte unchanged from before this fix). This lets the test suite
// point at disposable fixture files (missing / syntax-error / wrong-exports)
// under the OS temp dir WITHOUT ever touching claude-author-guard.js or
// claude-bow.js, both explicitly off-limits in this round because other
// agents are live-editing them.
//
// ROUND-4 NOTE: `requiredAuthorGuardFns` below shrank from four names to
// one (`findCommitInvocation`) because round 4 stopped reassembling
// recognition out of claude-author-guard.js's individual primitives
// (gatherScanTexts/buildQuoteMask/parseGitInvocation/resolveAlias) and now
// calls its single top-level recogniser directly — see the COMMAND
// RECOGNITION block below for why (ASM-360's predicted drift, arrived
// same-day as `"git" commit` / `'git' commit`). The three dependency-
// failure fixtures below (MISSING / SYNTAX ERROR / WRONG EXPORTS) were
// re-run against this smaller list and still deny correctly — see the test
// file's round-4 section.
function loadDependencies() {
  const authorGuardPath =
    process.env.CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH || path.join(__dirname, 'claude-author-guard.js');
  const bowPath =
    process.env.CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH || path.join(__dirname, 'claude-bow.js');

  // eslint-disable-next-line global-require, import/no-dynamic-require
  const authorGuardMod = require(authorGuardPath);
  // eslint-disable-next-line global-require, import/no-dynamic-require
  const bowMod = require(bowPath);

  // WRONG-EXPORTS check, done explicitly rather than left to surface later
  // as an unexplained TypeError deep inside isCommitInvocation()/main() — a
  // dependency that loads (no syntax error, file present) but exports the
  // wrong shape (e.g. mid-refactor, exports temporarily renamed/removed) is
  // exactly as dangerous as a missing file and must fail the same way, here,
  // with a message that names what's missing.
  const requiredAuthorGuardFns = ['findCommitInvocation'];
  const missingAuthorGuardFns = requiredAuthorGuardFns.filter(fn => typeof authorGuardMod[fn] !== 'function');
  if (missingAuthorGuardFns.length) {
    throw new Error(
      `claude-author-guard.js loaded but did not export the expected function(s): ${missingAuthorGuardFns.join(', ')} ` +
        '(wrong module shape — cannot recognise a git commit invocation without these)'
    );
  }

  const requiredBowFns = ['findItemByRef', 'latestDestructiveVerdict'];
  const missingBowFns = requiredBowFns.filter(fn => typeof bowMod[fn] !== 'function');
  if (missingBowFns.length) {
    throw new Error(
      `claude-bow.js loaded but did not export the expected function(s): ${missingBowFns.join(', ')} ` +
        '(wrong module shape — cannot verify a Destructive verdict without these)'
    );
  }

  return {
    authorGuard: authorGuardMod,
    findItemByRef: bowMod.findItemByRef,
    latestDestructiveVerdict: bowMod.latestDestructiveVerdict,
  };
}

// Self-contained (NO dependency `require`s — must work even when
// loadDependencies() itself is what just failed) best-effort check: does
// `command` plausibly contain a git commit invocation? This is deliberately
// COARSER than isCommitInvocation() below (which needs authorGuard's real
// parser: wrapper recursion, quote masking, alias resolution) — it exists
// ONLY to decide "is a dependency-load failure worth denying over", the same
// narrowing AC-24 already applies to the unparseable-stdin case (fail-closed
// is scoped to commit-shaped input, not every Bash/PowerShell command this
// hook's matcher sees). A false POSITIVE here just means an unrelated
// command gets a clear, fixable deny message while a dependency is broken —
// seconds to diagnose, the same cost-asymmetry argument this file's header
// already makes. A false NEGATIVE would mean a real commit silently proceeds
// while recognition machinery is down — the exact crash-open shape this fix
// exists to close.
//
// BUG-123 ROUND 10 (attacker "Thresher" REJECT, applied here proactively for
// consistency — the sibling copies of this exact construct in
// claude-secret-guard.js / claude-plan-guard.js were the ones caught live,
// but this file is the pattern's origin and shares the identical gap): this
// used to be a single regex capping the gap between "git" and "commit" at
// ~200 chars. A single legitimately long `-c` option value (e.g. a 250-char
// `-c user.name=...`) pushes a real commit's git-to-commit distance past
// that window, so with a broken dependency NOTHING catches it — the exact
// silent-allow this fallback exists to prevent. This fallback only runs on
// the rare dependency-broken path, so it never needed to be bounded for
// performance; it needs to find "commit" anywhere after "git" in the WHOLE
// string. Rebuilt as plain indexOf/startsWith scanning rather than a regex —
// each scan is strictly O(n) with zero backtracking, immune to
// catastrophic-backtracking-style ReDoS regardless of adversarial input
// length or shape (measured: a 1MB non-matching string completes in low
// single-digit milliseconds).
function isWordChar(ch) {
  return !!ch && /[a-z0-9_]/.test(ch);
}

function looksLikeCommitFallback(command) {
  const lower = (command || '').toLowerCase();
  let idx = 0;
  while (idx < lower.length) {
    const gitIdx = lower.indexOf('git', idx);
    if (gitIdx === -1) return false;
    const beforeCh = gitIdx > 0 ? lower[gitIdx - 1] : '';
    const afterStart = gitIdx + 3;
    const hasSuffix = lower.startsWith('.exe', afterStart) || lower.startsWith('.cmd', afterStart);
    const afterEnd = hasSuffix ? afterStart + 4 : afterStart;
    const afterCh = afterEnd < lower.length ? lower[afterEnd] : '';
    if (!isWordChar(beforeCh) && !isWordChar(afterCh)) {
      // Found a bare "git" (or git.exe/git.cmd) token — now look for "commit"
      // anywhere after it, no distance bound at all.
      return lower.indexOf('commit', afterEnd) !== -1;
    }
    idx = gitIdx + 3;
  }
  return false;
}

// ASM-destructiveguard-root-via-cwd (logged): every sibling guard in this
// repo hardcodes ROOT = __dirname, assuming the harness always launches
// hooks with cwd already at the project root. This guard instead resolves
// ROOT from process.cwd(). In production that is the same directory (the
// harness sets cwd to the project root before invoking any PreToolUse
// hook), so behaviour is unchanged; the payoff is that a test harness can
// point this guard at an isolated throwaway git repo + its own
// .claude/settings.json fixture without ever staging/unstaging files in
// THIS repo's live shared index (many agents are concurrently active in
// this tree — touching the real index from a test would be a real hazard).
// If that production assumption is ever wrong (some future harness invokes
// hooks from a different cwd), this guard fails toward the fail-closed
// default anyway: a wrong ROOT makes `git diff --cached` or
// `.claude/settings.json` unreadable, both of which DENY per AC-25/AC-11.
function rootDir() {
  return process.cwd();
}

// ---------------------------------------------------------------------------
// COMMAND RECOGNITION — is this proposed command a `git commit` invocation
// at all?
//
// ROUND-4 REWRITE (ASM-360's predicted failure, arrived within the same day
// it was filed). Rounds 2/3 kept a LOCAL `GIT_TOKEN_RE` here — described at
// the time as "reusing claude-author-guard.js's gatherScanTexts /
// buildQuoteMask / parseGitInvocation / resolveAlias, but with our own
// token regex" — and ASM-360 named exactly the risk of that shape: two
// independently hand-maintained recognisers sharing primitives but not the
// actual matching regex will drift, silently, the moment one of them gets a
// fix the other doesn't. It happened same-day: round 3 added the
// executable-suffix and PATH-prefix tolerances to THIS file's copy of
// GIT_TOKEN_RE, but its quoted-path alternative required a path separator
// before "git" (`[^"]*[\\/]git...`) — a stricter shape than
// claude-author-guard.js's own quoted alternative, which has always allowed
// a BARE quoted executable name with NO separator
// (`(?:[^"]*[\\/])?git...`, the whole prefix group optional). Net effect:
// `"git" commit` (or `'git' commit`) — an ordinary, fully-executable POSIX
// invocation, no exotic tooling required — matched the author guard's
// commit-identity check but not this file's own trigger regex, so
// isCommitInvocation() returned false and EVERY downstream check (AC-16's
// zero-tag bypass trap included) was skipped entirely. A total bypass, not
// a partial one — reproduced end to end against a real repo with healthy
// dependencies and no overrides.
//
// THE FIX Ben directed and this round implements: stop maintaining a second
// copy at all. `authorGuard.findCommitInvocation()` — already the ONE
// tested, Destructive-hardened recogniser in this repo for "does this
// command string contain a real `git <verb>` invocation, wherever it's
// hiding" — is now the SOLE recognition mechanism this file uses. It
// already does everything the local machinery was reassembling by hand:
// line-continuation normalisation, bounded wrapper recursion (bash/sh/zsh/
// dash/ksh -c, powershell/pwsh -Command, cmd /c), quote-aware token
// boundaries (including the heredoc- and backslash-escape-aware fixes from
// BUG-077/BUG-078), the `-C`/`-c` global-option parse, and bounded/
// cycle-guarded alias resolution. There is no longer a second GIT_TOKEN_RE
// in this file for the two to drift apart from — the class of bug ASM-360
// warned about is closed by construction, not by a promise to keep two
// regexes in sync by hand a fourth time.
//
// SCOPE STAYS NARROWED TO "commit" (ASM-193/ASM-359, unchanged, verified
// still true after this rewrite). `findCommitInvocation()` matches against
// claude-author-guard.js's WIDER `KNOWN_COMMIT_VERBS` set (commit,
// cherry-pick, revert, am, merge) — reusing its recognition machinery must
// not silently reuse its scope too. This file makes its OWN scope decision,
// exactly as before: it accepts the invocation `findCommitInvocation()`
// found ONLY when the verb it actually resolved to is the literal string
// `commit`. cherry-pick/revert/am/merge invocations are found by the shared
// parser (so the FUNCTION CALL succeeds) but rejected by this file's own
// equality check immediately afterward (so this GUARD still does not fire)
// — see isCommitInvocation() below and the regression tests proving all
// four stay un-intercepted.
//
// KNOWN NARROWING FROM THIS REWRITE, LOGGED (not silently accepted):
// `findCommitInvocation()` returns the FIRST invocation in scan order whose
// resolved verb is in KNOWN_COMMIT_VERBS, then stops — so a single command
// string chaining a commit-creating verb this file doesn't scope to BEFORE
// a real `git commit` (e.g. `git cherry-pick X; git commit -m "..."`) would
// have its `cherry-pick` returned (and rejected by the verb-equality check
// below, same as a standalone cherry-pick), never reaching the LATER
// `commit` in the same string — the old local loop kept scanning past a
// non-"commit"-resolving match; the shared function does not keep scanning
// past a DIFFERENT known-commit-creating verb once it has found one. This
// is a real, non-hypothetical narrowing versus the pre-fix local loop, but
// it trades a theoretical multi-verb-chain gap for eliminating the proven,
// same-day, zero-tag TOTAL bypass class ASM-360 predicted — the asymmetry
// favours the fix. Logged as ASM-363 (`node claude-bow.js show ASM-363`) for
// the day a Destructive round wants to attack it directly.
function isCommitInvocation(command, authorGuard) {
  const inv = authorGuard.findCommitInvocation(command);
  return !!inv && inv.verb === 'commit';
}

// Directory-prefix half of the enforced-path set (AC-11). The root-script
// half is derived at runtime from .claude/settings.json — see
// deriveRootGuardScripts() below, never a second hardcoded list.
const ENFORCED_DIR_RE = /^(cmd|internal|data|tools)\//;

// Bracketed tags: [tool.destructiveguard], [FEAT-040]. Extraction shape
// reused VERBATIM from claude-bow-ref-check.js (AC-13) — do not diverge on
// edge-case behaviour (a malformed tag is still extracted, then denied at
// the BOW-lookup step as "unresolvable", not silently skipped).
const TAG_RE = /\[([^\[\]\n]+)\]/g;

// Matches -m "..." / -m '...' / --message="..." / --message '...', allowing
// escaped quotes inside the body. Multiple matches = multiple -m flags
// (git's own paragraph-joining behaviour). Reused verbatim from
// claude-bow-ref-check.js's extractMessage/MSG_FLAG_RE.
const MSG_FLAG_RE = /(?:-m|--message)(?:=|\s+)(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')/g;

// ---------------------------------------------------------------------------
// Hook plumbing
// ---------------------------------------------------------------------------

function deny(reason) {
  process.stdout.write(JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'deny',
      permissionDecisionReason: reason,
    },
  }));
  process.exit(0);
}

function allowSilently() {
  process.exit(0);
}

// ---------------------------------------------------------------------------
// Commit-message extraction (verbatim shape from claude-bow-ref-check.js)
// ---------------------------------------------------------------------------

function extractMessage(command) {
  const parts = [];
  let m;
  MSG_FLAG_RE.lastIndex = 0;
  while ((m = MSG_FLAG_RE.exec(command))) {
    const raw = m[1] !== undefined ? m[1] : m[2];
    parts.push(raw.replace(/\\(["'\\])/g, '$1'));
  }
  if (parts.length === 0) return null;
  return parts.join('\n');
}

function extractTags(message) {
  const tags = [];
  TAG_RE.lastIndex = 0;
  let m;
  while ((m = TAG_RE.exec(message))) {
    const tag = m[1].trim();
    if (tag) tags.push(tag);
  }
  return tags;
}

// ---------------------------------------------------------------------------
// Root-guard-script derivation (AC-11, GR#15 — data-derived, never a name
// pattern; lead ruling on ASM-192)
// ---------------------------------------------------------------------------

/**
 * Every root-level `.js` filename referenced inside any `command` string of
 * any hook entry in .claude/settings.json's `hooks` object, across every
 * event type and every matcher. Deliberately not restricted to a leading
 * "node " prefix or to PreToolUse only — a wired hook of ANY event type is
 * still a "root guard script" for the purposes of this boundary, and this
 * stays robust to the command string's exact shape without becoming a
 * second, driftable list. Throws on a missing or unparseable settings.json
 * — callers MUST treat that as fail-closed (see AC-11's consequence clause),
 * never as "no root scripts wired".
 */
function deriveRootGuardScripts() {
  const settingsPath = path.join(rootDir(), '.claude', 'settings.json');
  let raw;
  try {
    raw = fs.readFileSync(settingsPath, 'utf8');
  } catch (err) {
    throw new Error(`cannot read .claude/settings.json to derive the root-guard-script set: ${err.message}`);
  }
  let parsed;
  try {
    parsed = JSON.parse(raw.replace(/^\uFEFF/, ''));
  } catch (err) {
    throw new Error(`.claude/settings.json is not valid JSON: ${err.message}`);
  }

  const scripts = new Set();
  const hooks = parsed && typeof parsed === 'object' ? parsed.hooks : null;
  if (hooks && typeof hooks === 'object') {
    for (const eventName of Object.keys(hooks)) {
      const matcherEntries = hooks[eventName];
      if (!Array.isArray(matcherEntries)) continue;
      for (const entry of matcherEntries) {
        const hookList = entry && Array.isArray(entry.hooks) ? entry.hooks : [];
        for (const h of hookList) {
          if (!h || typeof h.command !== 'string') continue;
          const re = /([A-Za-z0-9_.-]+\.js)\b/g;
          let m;
          while ((m = re.exec(h.command)) !== null) {
            scripts.add(m[1]);
          }
        }
      }
    }
  }
  return scripts;
}

/** True if `filePath` (as reported by `git diff --cached --name-only`,
 * forward or back slashes) sits under an enforced directory prefix. Never
 * needs settings.json — see isRootLevel() for the half that does. */
function isEnforcedDirPath(filePath) {
  return ENFORCED_DIR_RE.test(filePath.replace(/\\/g, '/'));
}

/** True if `filePath` has no directory component at all (a root-level file
 * — the only shape that can ever need the settings.json-derived set). */
function isRootLevel(filePath) {
  return !filePath.replace(/\\/g, '/').includes('/');
}

// ---------------------------------------------------------------------------
// BOW connection (mirrors claude-bow.js / claude-bow-ref-check.js env vars)
// ---------------------------------------------------------------------------

async function connectReadOnly() {
  return mysql.createConnection({
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
    connectTimeout: 4000,
  });
}

// ---------------------------------------------------------------------------
// Deny-message builders
// ---------------------------------------------------------------------------

function noTagDenyMessage() {
  return [
    '\uD83D\uDED1 DESTRUCTIVE GUARD (GR#23, tool.destructiveguard / FEAT-040): this commit ' +
      'touches code-bearing paths (cmd/, internal/, data/, tools/, or a root script wired into ' +
      '.claude/settings.json) but its message carries NO [mkey]/[CODE] BOW tag at all.',
    '',
    'GR#23 requires every code-bearing commit to name the BOW item(s) it closes, so a recorded ' +
      'Destructive verdict can be checked against it. Omitting the tag is not an exemption from ' +
      'that — it is precisely the bypass this gate exists to close.',
    '',
    'Add a tag, e.g.:',
    '  git commit -m "[tool.destructiveguard] ..."',
    '  git commit -m "[FEAT-040] ..."',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this ' +
      'command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

function verdictDenyMessage(unresolved, missing) {
  const lines = [
    '\uD83D\uDED1 DESTRUCTIVE GUARD (GR#23, tool.destructiveguard / FEAT-040): missing or ' +
      'non-accepted Destructive verdict(s).',
    '',
  ];
  if (missing.length) {
    lines.push('Items lacking an accepted verdict:');
    for (const m of missing) {
      const state = m.state === 'none' ? 'NO VERDICT RECORDED' : `latest verdict: ${m.state.toUpperCase()}`;
      lines.push(`  - ${m.code} "${m.title}" — ${state}`);
    }
    lines.push('');
  }
  if (unresolved.length) {
    lines.push('Tag(s) that do not resolve to any live BOW item (cannot have a verdict by definition):');
    for (const t of unresolved) lines.push(`  - [${t}]`);
    lines.push('');
  }
  lines.push('Record a verdict: node claude-bow.js destructive <code> --verdict accept|reject --attacker "<name>" [--class c1,c2] [--findings SEC-001,...] [--note "..."]');
  lines.push('Check a verdict:  node claude-bow.js verdict <code>');
  lines.push('GR#23: a Tester PASS proves the criteria hold; only a Destructive round proves the code survives someone actively trying to break it.');
  lines.push('Emergency bypass (operator-only, set BEFORE the session starts — never inline in this command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1');
  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  if (process.env.CLAUDE_DISABLE_DESTRUCTIVE_GUARD === '1') {
    allowSilently();
    return;
  }

  let input = '';
  process.stdin.setEncoding('utf8');
  for await (const chunk of process.stdin) input += chunk;

  let command;
  try {
    // Strip a UTF-8 BOM (PowerShell pipes prepend one) before parsing.
    const data = JSON.parse(input.replace(/^\uFEFF/, ''));
    command = data && data.tool_input ? String(data.tool_input.command || '') : '';
  } catch {
    // AC-24: fail-closed is scoped to COMMIT-SHAPED input, matching
    // claude-secret-guard.js's posture (not claude-bow-ref-check.js's blanket
    // fail-open) — a hook-input hiccup must never brick `git status` / `npm
    // install`, but if the raw text plainly looks like a git commit, we
    // cannot verify it and this guard is fail-closed by design.
    if (/git\s+commit/.test(input)) {
      deny(
        '\uD83D\uDED1 DESTRUCTIVE GUARD: hook input was unparseable but appears to contain a ' +
          'git commit — denying (fail-closed; cannot verify a verdict exists). Retry the commit.'
      );
    }
    allowSilently();
    return;
  }

  // Load claude-author-guard.js / claude-bow.js NOW, inside main()'s reach,
  // so a load-time throw (missing file, syntax error, wrong exports — see
  // the DEPENDENCY LOADING section above) rejects this async function and is
  // caught by the main().catch() net below (AC-25's existing fail-closed
  // wrapper), rather than crashing the process before that wrapper ever
  // attaches. A load failure here does NOT unconditionally deny (that would
  // brick every Bash/PowerShell command in the session over a bug in a file
  // this guard doesn't even need for e.g. `ls`) — it denies only when the
  // raw command text plausibly looks like a git commit (looksLikeCommitFallback,
  // dependency-free by construction), the same commit-shaped narrowing AC-24
  // already applies to unparseable stdin.
  let deps;
  try {
    deps = loadDependencies();
  } catch (err) {
    if (looksLikeCommitFallback(command)) {
      deny(
        `🛑 DESTRUCTIVE GUARD: a required dependency failed to load (${err.message}) — ` +
          'cannot verify Destructive verdicts. Denying (fail-closed; this command looks like it ' +
          'could be a git commit). Fix claude-author-guard.js / claude-bow.js and retry.\n\n' +
          'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this ' +
          'command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1'
      );
      return;
    }
    allowSilently();
    return;
  }
  const { authorGuard, findItemByRef, latestDestructiveVerdict } = deps;

  if (!isCommitInvocation(command, authorGuard)) {
    allowSilently();
    return;
  }

  // From here on this is a real `git commit` invocation — every further
  // failure path DENIES (fail-closed), the opposite posture from
  // claude-bow-ref-check.js. See header.

  const stagedRaw = execSync('git diff --cached --name-only', {
    cwd: rootDir(),
    encoding: 'utf8',
    timeout: 5000,
  });
  const stagedFiles = stagedRaw.split('\n').map(f => f.trim()).filter(Boolean);

  let codeBearing = stagedFiles.some(isEnforcedDirPath);
  if (!codeBearing) {
    const rootLevelFiles = stagedFiles.filter(isRootLevel);
    if (rootLevelFiles.length) {
      // Only consult settings.json when it can actually change the answer —
      // a docs-only / dir-only commit must stay silently exempt (AC-12)
      // even if settings.json is unrelated-ly broken. Throws -> outer catch
      // denies (AC-11's fail-closed consequence).
      const rootScripts = deriveRootGuardScripts();
      codeBearing = rootLevelFiles.some(f => rootScripts.has(f));
    }
  }
  if (!codeBearing) {
    allowSilently();
    return;
  }

  const message = extractMessage(command);
  if (message === null) {
    // AC-23: deliberate divergence from claude-bow-ref-check.js's warn-allow
    // for the same "can't parse the message" case — this guard is
    // fail-closed, so "cannot verify" means deny, not allow-with-a-warning.
    deny(
      '\uD83D\uDED1 DESTRUCTIVE GUARD: this commit touches code-bearing paths but the commit ' +
        'message could not be extracted from the command (commit-from-file / -F / heredoc / ' +
        'editor flow?). This guard is fail-closed (GR#23 is a security rule) — use `-m "[CODE] ' +
        '..."` so the BOW tag(s) can be verified, or record the verdict and retry with an ' +
        'extractable message.\n\nEmergency bypass (operator-only, set BEFORE the session starts): ' +
        'CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1'
    );
    return;
  }

  const tags = extractTags(message);
  if (tags.length === 0) {
    deny(noTagDenyMessage());
    return;
  }

  let db;
  try {
    db = await connectReadOnly();
  } catch (err) {
    // AC-22: DB unreachable -> cannot verify a verdict exists -> deny. The
    // inverse of claude-bow-ref-check.js's AC-10 (which allows here).
    deny(
      `\uD83D\uDED1 DESTRUCTIVE GUARD: metro MariaDB is unreachable (${err.message}) — cannot ` +
        `verify Destructive verdict(s) for [${tags.join('], [')}]. Denying (fail-closed). Fix the ` +
        'DB connection and retry.'
    );
    return;
  }

  try {
    const unresolved = [];
    const missing = [];
    for (const tag of tags) {
      // eslint-disable-next-line no-await-in-loop
      const item = await findItemByRef(db, tag);
      if (!item) {
        unresolved.push(tag);
        continue;
      }
      // eslint-disable-next-line no-await-in-loop
      const verdict = await latestDestructiveVerdict(db, tag);
      if (!verdict || verdict.verdict !== 'accept') {
        missing.push({ code: item.code, title: item.title, state: verdict ? verdict.verdict : 'none' });
      }
    }

    if (unresolved.length || missing.length) {
      deny(verdictDenyMessage(unresolved, missing));
      return;
    }

    allowSilently();
  } finally {
    try { await db.end(); } catch { /* ignore */ }
  }
}

if (require.main === module) {
  main().catch((err) => {
    // Top-level fail-closed net (AC-25): git invocation failure, a malformed/
    // unreadable settings.json when a root file was staged, or any other
    // unexpected exception anywhere above — mirrors claude-author-guard.js's
    // main() wrapper. Anything that reaches here means "could not verify",
    // and this guard's entire posture is fail-closed on "could not verify".
    deny(
      `\uD83D\uDED1 DESTRUCTIVE GUARD internal error: ${err && err.message ? err.message : err}\n\n` +
        'Failing closed deliberately (GR#23 is a security rule, see this file\'s header). Bypass ' +
        'only if you have checked by hand: CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1'
    );
  });
} else {
  module.exports = {
    isCommitInvocation,
    ENFORCED_DIR_RE,
    extractMessage,
    extractTags,
    deriveRootGuardScripts,
    isEnforcedDirPath,
    isRootLevel,
    noTagDenyMessage,
    verdictDenyMessage,
    loadDependencies,
    looksLikeCommitFallback,
  };
}
