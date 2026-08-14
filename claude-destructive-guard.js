// Module key: tool.destructiveguard (see code.json; GUID d7fc564e-96ec-4040-a5ea-21cedf3a0aaa)
// Spec ref: GR#23; GR#24; M0-ENG §5 (hooks)

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
  const requiredAuthorGuardFns = ['findCommitInvocation', 'scanGitInvocations'];
  const missingAuthorGuardFns = requiredAuthorGuardFns.filter(fn => typeof authorGuardMod[fn] !== 'function');
  if (missingAuthorGuardFns.length) {
    throw new Error(
      `claude-author-guard.js loaded but did not export the expected function(s): ${missingAuthorGuardFns.join(', ')} ` +
        '(wrong module shape — cannot recognise a git commit invocation without these)'
    );
  }
  // BUG-232: the fail-closed sweep below consults KNOWN_COMMIT_VERBS directly,
  // so a broken/mid-refactor export of it must fail the same way as a missing
  // function — a falsy KNOWN_COMMIT_VERBS would silently disable the
  // "unrecognised verb" rule (and could even mis-deny a plain `git commit`).
  if (!(authorGuardMod.KNOWN_COMMIT_VERBS instanceof Set)) {
    throw new Error(
      'claude-author-guard.js loaded but did not export a usable KNOWN_COMMIT_VERBS Set ' +
        '(wrong module shape — cannot tell a known commit verb from an unrecognised one without it)'
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

  // BUG-164: TYPE_PREFIX is the same wrong-shape hazard as the functions
  // above — a broken/mid-refactor export here must fail the same way
  // (denied, named reason) rather than surface later as looksLikeRealTag()
  // silently treating every CODE-shaped span as prose (typePrefixes falsy
  // -> `.has` never called -> everything rejected -> AC-13's real-tag cases
  // would wrongly deny). Checked as a plain object with at least one value,
  // not a specific key set, so a future TYPE_PREFIX addition/rename in
  // claude-bow.js needs no matching edit here (GR#15).
  if (!bowMod.TYPE_PREFIX || typeof bowMod.TYPE_PREFIX !== 'object' || !Object.values(bowMod.TYPE_PREFIX).length) {
    throw new Error(
      'claude-bow.js loaded but did not export a usable TYPE_PREFIX ' +
        '(wrong module shape — cannot tell a real BOW code prefix from prose without it)'
    );
  }

  return {
    authorGuard: authorGuardMod,
    findItemByRef: bowMod.findItemByRef,
    latestDestructiveVerdict: bowMod.latestDestructiveVerdict,
    typePrefixes: new Set(Object.values(bowMod.TYPE_PREFIX)),
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

// BUG-152: a bracketed span in ordinary commit-message prose is NOT
// automatically a claimed mkey/CODE tag just because it matches TAG_RE's
// bracket shape above. Observed false positives (same session, three times):
// Go generics `Store[T,PT]` / `atomic.Pointer[Screen]`, and a literal marker
// string quoted in prose, `[REDACTED-GR22]`. Each was extracted as a
// "claimed tag", failed BOW resolution (naturally — it isn't a BOW ref at
// all), and demanded a Destructive verdict for text that was never meant to
// be a tag. Fix direction is the lead's ("only treat a bracketed span as a
// tag if its content matches the SHAPE of a real mkey/CODE") rather than
// positional anchoring — simpler, and it doesn't assume the tag always
// trails the summary line (multiple tags, or a tag on a later paragraph
// line, are both legitimate today, see the multi-tag test above).
//
// Two real reference shapes exist in this project (verified against
// docs/planning/acceptance/*.md filenames AND bow_items.mkey/.code — never
// hand-typed from memory, GR#15):
//   - mkey:  lowercase dotted identifier, each segment [a-z][a-z0-9]*, with
//            optional internal hyphens (e.g. "tool.destructiveguard",
//            "data.modes-naming", "ui.screen.build" — 2 or 3 segments seen).
//   - CODE:  a short BOW type prefix (MOD/FEAT/BUG/INT/ASM/SEC today, per
//            TYPE_PREFIX in claude-bow.js — deliberately matched by SHAPE
//            here, not a hardcoded prefix list, so a future prefix needs no
//            edit here) followed by "-" and a purely-numeric id, e.g.
//            "FEAT-040", "BUG-152".
//
// Checking against these two shapes excludes all three reported fixtures
// without any positional logic: "T,PT" and "Screen" fail both (uppercase,
// no dot, no trailing all-digit suffix); "REDACTED-GR22" fails the CODE
// shape too — its suffix "GR22" is letters+digits mixed, not purely numeric.
// A tag that fails BOTH shapes is simply not a claimed reference and is
// dropped silently here, same as prose was always meant to be treated —
// it is NOT surfaced as "unresolved" (AC-16's zero-tag bypass trap is about
// a message with NO real tags at all, not about prose that happens to
// contain brackets; see the mixed-message regression test).
const MKEY_SHAPE_RE = /^[a-z][a-z0-9]*(-[a-z0-9]+)*(\.[a-z][a-z0-9]*(-[a-z0-9]+)*)+$/;
const CODE_SHAPE_RE = /^[A-Z][A-Z0-9]*-\d+$/;

// BUG-164: CODE_SHAPE_RE alone over-matches ordinary technical abbreviations
// shaped like "WORD-digits" — [UTF-8], [RFC-2119], [SHA-256], [ISO-8601] all
// satisfy it, so a commit message that merely mentions one of these in prose
// (alongside a real, already-verdicted tag) was wrongly denied as carrying
// an "unresolvable" claimed tag. Shape alone can only prove "looks like a
// mkey" (MKEY_SHAPE_RE, which stays shape-only — no dotted-lowercase prefix
// list exists to check against) or "looks like a CODE"; a CODE-shaped span
// is only a genuine claimed BOW reference if its prefix is one of
// claude-bow.js's own real TYPE_PREFIX values (MOD/FEAT/BUG/INT/ASM/SEC
// today). `typePrefixes` is a Set of those values, threaded in by the
// caller (built once in loadDependencies() from claude-bow.js's own
// TYPE_PREFIX export — GR#15, never a second hand-typed prefix list here).
// UTF/RFC/SHA/ISO are not real BOW type prefixes, so this closes BUG-164
// without reopening BUG-152: a genuine-shaped but nonexistent code like
// FEAT-999 still passes this filter (real prefix, purely-numeric id) and is
// correctly left for BOW resolution to reject as unresolvable.
function looksLikeRealTag(tag, typePrefixes) {
  if (MKEY_SHAPE_RE.test(tag)) return true;
  if (!CODE_SHAPE_RE.test(tag)) return false;
  const prefix = tag.slice(0, tag.indexOf('-'));
  return !!typePrefixes && typePrefixes.has(prefix);
}

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

function extractTags(message, typePrefixes) {
  const tags = [];
  TAG_RE.lastIndex = 0;
  let m;
  while ((m = TAG_RE.exec(message))) {
    const tag = m[1].trim();
    // BUG-152: only a bracketed span shaped like a real mkey or CODE is a
    // claimed tag — see looksLikeRealTag()'s comment above for why this is
    // the fix (not positional anchoring) and which three false positives it
    // closes. BUG-164: for the CODE shape, `typePrefixes` (the real
    // claude-bow.js TYPE_PREFIX values) additionally filters out prose
    // abbreviations like [UTF-8]/[RFC-2119] that only coincidentally match
    // the shape — see looksLikeRealTag()'s comment.
    if (tag && looksLikeRealTag(tag, typePrefixes)) tags.push(tag);
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

// FEAT-077: GR#23 proportionality tier — docs-only / test-only exemption
// (Aaron, 2026-08-13, "we are not building NASA code"). A commit whose staged
// set is ENTIRELY docs/tests — and whose argv is classifiable (see
// isArgvClassifiable) — is exempt from the Destructive verdict requirement
// (Tester-level verification suffices). The three exempt shapes are the ONLY
// ones; matching is case-sensitive, git's own casing (no folding anywhere).
const EXEMPT_FILE_RE = /(\.md|\.test\.js|_test\.go)$/;

function isExemptFile(filePath) {
  return EXEMPT_FILE_RE.test(filePath.replace(/\\/g, '/'));
}

function isExemptFileSet(files) {
  return files.length > 0 && files.every(isExemptFile);
}

// ---------------------------------------------------------------------------
// BUG-224: combined `git add` + `git commit` bypass.
//
// THE BUG. This guard decides codeBearing from `git diff --cached
// --name-only` — a snapshot of the index taken WHEN THIS HOOK RUNS, before
// the proposed Bash command has executed at all (a PreToolUse hook fires
// once, for the whole `tool_input.command` string, prior to any of it
// running). A command that combines `git add <paths>` and `git commit -m
// "..."` in the SAME invocation (`&&`, `;`, or a bare newline) stages its own
// files AFTER this guard has already taken its `--cached` snapshot, so that
// snapshot is empty or stale: a commit that genuinely touches cmd/, internal/,
// data/, or tools/ was reaching history with zero recorded Destructive
// verdict, silently, because `codeBearing` was computed against a staging
// area the command's own `git add` hadn't populated yet.
//
// THE FIX. Before deciding codeBearing, scan the command text for `git add`
// invocations (any spelling isCommitInvocation() already tolerates for
// commit — git.exe, quoted full paths, bash -c wrapper bodies — reusing the
// SAME authorGuard primitives: gatherScanTexts() for wrapper recursion,
// buildQuoteMask() so an add mentioned only in commit-message PROSE is never
// mistaken for a real invocation, parseGitInvocation()/resolveAlias() for the
// verb itself). Two outcomes:
//
//   - EVERY found `git add` invocation has a SIMPLE, unambiguous pathspec
//     (bare paths only — no flags, no `.`/`..`, no glob/wildcard characters,
//     at least one path): its exact path arguments are extracted and UNIONED
//     with the `--cached` snapshot before classification. This closes the
//     exact reported bypass (`git add tools/x.js && git commit -m "..."`)
//     without needing to actually run/simulate the add.
//   - ANY found `git add` invocation is NOT simple (a flag like -A/-u/--all/
//     -p/--patch/--interactive, a bare `.`/`..`, a glob character, or no
//     pathspec at all — every one of these can stage files this parser
//     cannot enumerate by reading the command text alone: -A/-u/. depend on
//     the actual working-tree diff, globs depend on the actual filesystem):
//     this guard has no reliable way to know what will be staged, so — same
//     fail-closed posture as every other "cannot verify" path in this file
//     (AC-22/AC-23/AC-25) — it DENIES outright and tells the operator to
//     split `git add` and `git commit` into separate tool calls, rather than
//     guess. This is deliberately the MORE conservative of the two documented
//     options (simulate broadly vs. deny on ambiguity): a false deny here
//     costs seconds (split the call); a guessed-wrong allow could let
//     un-attacked code reach a public repo, which is the exact harm GR#23
//     exists to prevent.
//
// A `git add` invocation is NEVER itself a commit invocation (isCommitInvocation()
// only recognises the literal verb `commit`, see COMMAND RECOGNITION above),
// so a genuinely SEPARATE `git add ...` Bash call is silently allowed exactly
// as before this fix — this logic only ever runs once a REAL commit
// invocation has already been recognised in THIS SAME command string, and it
// only changes what counts as staged for THAT commit's own classification.
//
// KNOWN, DELIBERATE OVER-APPROXIMATION: this does not attempt to determine
// whether a found `git add` invocation runs BEFORE or AFTER the `git commit`
// in the command's actual execution order — any simple `git add` found
// anywhere in the string has its paths unioned in regardless of position.
// Worst case this makes an unrelated trailing `git add` (staging something
// for a future, different commit) force the tag/verdict pipeline on a commit
// that didn't really touch it — an over-block, not a bypass, and consistent
// with this file's fail-closed philosophy (over-caution costs seconds; a
// missed bypass costs a public-repo security guarantee).
// ---------------------------------------------------------------------------

// Same token shape as claude-author-guard.js's own GIT_TOKEN_RE (git /
// git.exe / git.cmd, bare or quoted, boundary-anchored). Duplicated
// deliberately, not reused: authorGuard exports no "find every git
// invocation of any verb" primitive — findCommitInvocation() only ever
// returns the FIRST invocation whose resolved verb is commit-creating, and
// stops there, which is no help for finding a LATER, different-verb `git
// add`. The COMMAND RECOGNITION block above already explains why a second,
// independently-hand-maintained COMMIT recogniser was a proven, same-day
// drift hazard (ASM-360) — that risk was about two regexes doing the SAME
// job (finding `git commit`) and disagreeing. This regex does a DIFFERENT
// job (finding every `git <verb>` token so THIS file can filter for `add`
// itself) that authorGuard has no equivalent for, so there is no sibling
// recogniser for this one to drift out of sync with. The ROUND-4 recognition
// corpus in the test file is mirrored here with an ADD-specific corpus to
// catch any future divergence directly, rather than by promise.
const GIT_TOKEN_FOR_ADD_RE =
  /(?:^|[;&|(\n]|\s)(?:\s*(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S+))*\s*("(?:[^"]*[\\/])?git(?:\.(?:exe|cmd))?"|'(?:[^']*[\\/])?git(?:\.(?:exe|cmd))?'|(?:[^"'<>|&;()\n]*[\\/])?git(?:\.(?:exe|cmd))?)(?=\s)/gi;

/** Scans forward from `start` in `text` for the first UNQUOTED shell
 * boundary character (`;`, newline, `|`, `&`, `)`), per `mask`
 * (authorGuard.buildQuoteMask()'s per-index "inside a quoted region" flags).
 * Bounds a `git add`'s own argument list to its own command segment, so a
 * LATER `&& git commit ...` on the same line is never accidentally tokenized
 * as if it were more `git add` arguments. Returns `text.length` if no
 * boundary is found (the add invocation runs to the end of the text). */
function findUnquotedShellBoundary(text, mask, start) {
  for (let i = start; i < text.length; i++) {
    if (mask[i]) continue;
    const c = text[i];
    if (c === ';' || c === '\n' || c === '|' || c === '&' || c === ')') return i;
  }
  return text.length;
}

/** Finds every `git add` invocation (any spelling GIT_TOKEN_FOR_ADD_RE
 * recognises, including inside a bash -c/pwsh -Command wrapper body via
 * authorGuard.gatherScanTexts()) in `command`. Returns an array of
 * { text, argsText } — `argsText` is the raw, unbounded-position substring
 * from right after the resolved `add` verb up to the next unquoted shell
 * boundary (or end of text), ready for authorGuard.tokenize(). A `git add`
 * mentioned only inside commit-message prose (`-m "run git add first"`) is
 * excluded via the same quote-mask skip logic isCommitInvocation()'s
 * underlying authorGuard.findCommitInvocation() already uses. */
function findGitAddInvocations(command, authorGuard) {
  const results = [];
  const candidates = authorGuard.gatherScanTexts(command, 0);
  for (const text of candidates) {
    GIT_TOKEN_FOR_ADD_RE.lastIndex = 0;
    const quoteMask = authorGuard.buildQuoteMask(text);
    let m;
    while ((m = GIT_TOKEN_FOR_ADD_RE.exec(text)) !== null) {
      const token = m[1];
      const tokenStart = m.index + (m[0].length - token.length);
      const isQuotedPathToken = token[0] === '"' || token[0] === "'";
      const skip = isQuotedPathToken
        ? tokenStart > 0 && quoteMask[tokenStart - 1]
        : quoteMask[tokenStart + token.toLowerCase().lastIndexOf('git')];
      if (skip) continue;
      const inv = authorGuard.parseGitInvocation(text, m.index + m[0].length);
      if (!inv) continue;
      const resolved = authorGuard.resolveAlias(inv.verbWord, 0, new Set());
      if (resolved === 'add') {
        const boundary = findUnquotedShellBoundary(text, quoteMask, inv.verbEnd);
        results.push({ text, argsText: text.slice(inv.verbEnd, boundary) });
      }
      // Keep scanning: multiple `git add` calls in one command string, and/or
      // an unrelated later `git commit`/other verb, must not stop this loop.
    }
  }
  return results;
}

// A pathspec token containing any of these is NOT a bare literal path — git
// itself treats them as glob/wildcard syntax (fnmatch-style), so what
// actually gets staged depends on the real filesystem at execution time,
// which this guard cannot see from the command text alone.
const ADD_GLOB_CHARS_RE = /[*?[\]{}]/;

/** Classifies one `git add` invocation's raw argument text. Returns
 * { ok: true, paths: [...] } only for a SIMPLE, fully-enumerable pathspec —
 * bare literal paths, no flags, no `.`/`..`, no glob characters, at least one
 * path. Anything else (a flag, `.`/`..`, a glob, zero arguments) returns
 * { ok: false } — "this invocation's effect cannot be safely enumerated from
 * the command text alone", the trigger for BUG-224's conservative deny. A
 * bare `--` separator token is skipped (not a path, not ambiguous — it is
 * git's own explicit end-of-options marker, most likely present precisely
 * BECAUSE the caller was already being careful about pathspec vs. flag
 * parsing) rather than treated as an unrecognised flag. */
function classifyAddArgs(argsText, authorGuard) {
  const tokens = authorGuard.tokenize(argsText).filter((t) => t !== '');
  if (tokens.length === 0) return { ok: false, paths: [] };
  const paths = [];
  for (const tok of tokens) {
    if (tok === '--') continue;
    if (tok.startsWith('-')) return { ok: false, paths: [] };
    if (tok === '.' || tok === '..') return { ok: false, paths: [] };
    if (ADD_GLOB_CHARS_RE.test(tok)) return { ok: false, paths: [] };
    paths.push(tok);
  }
  if (paths.length === 0) return { ok: false, paths: [] };
  return { ok: true, paths };
}

/** Top-level BUG-224 entry point. Returns:
 *   { hasAdd: false }                        — no `git add` invocation found
 *                                               anywhere in `command`; caller
 *                                               proceeds exactly as before.
 *   { hasAdd: true, ambiguous: true }         — at least one found `git add`
 *                                               invocation could not be
 *                                               safely classified; caller
 *                                               must deny (fail-closed).
 *   { hasAdd: true, ambiguous: false, paths } — every found `git add`
 *                                               invocation was simple;
 *                                               `paths` (raw, as-written path
 *                                               strings) should be unioned
 *                                               with the `--cached` snapshot
 *                                               before classification. */
function computeAddedPathsOrAmbiguous(command, authorGuard) {
  const invocations = findGitAddInvocations(command, authorGuard);
  if (invocations.length === 0) return { hasAdd: false, ambiguous: false, paths: [] };
  const allPaths = [];
  for (const inv of invocations) {
    const cls = classifyAddArgs(inv.argsText, authorGuard);
    if (!cls.ok) return { hasAdd: true, ambiguous: true, paths: [] };
    allPaths.push(...cls.paths);
  }
  return { hasAdd: true, ambiguous: false, paths: allPaths };
}

function ambiguousAddDenyMessage() {
  return [
    '🛑 DESTRUCTIVE GUARD (GR#23, BUG-224): this command combines a `git add` ' +
      'invocation with a `git commit` invocation in the SAME tool call, and the `git add` ' +
      'shape could not be safely classified (a flag such as -A/-u/--all/-p/--patch, a bare ' +
      '"."/".." , a glob/wildcard character, or no pathspec at all).',
    '',
    'This guard decides whether a commit is code-bearing from `git diff --cached`, which ' +
      'reflects the index state BEFORE this same command\'s own `git add` has run. An ' +
      'ambiguous add\'s effect on the index cannot be determined from the command text alone, ' +
      'so this guard fails closed (GR#23 is a security rule) rather than guess.',
    '',
    'Split the add and commit into SEPARATE tool calls — run `git add ...` as its own Bash ' +
      'call, then `git commit ...` as a second, separate call — so the staged-file check runs ' +
      'against the real, settled index.',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this ' +
      'command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

// ---------------------------------------------------------------------------
// FEAT-077 / BUG-213 / BUG-214: commit-argv classification.
//
// "Classifiable" means the commit's staged set is FULLY determined by the
// `git diff --cached` snapshot this guard takes at hook time. Anything that
// stages files at commit-EXECUTION time — -a/--all (tracked-modified), an
// explicit pathspec / bare `--` (--only/--include), --pathspec-from-file /
// --pathspec-file-nul — or any flag we don't recognise (fail-closed) makes
// the argv UNCLASSIFIABLE, so `--cached` is provably not a truthful preview.
// ---------------------------------------------------------------------------

// Long boolean flags that do NOT alter what gets staged (safe to classify).
const SAFE_COMMIT_FLAGS = new Set([
  '-q', '--quiet',
  '-v', '--verbose',
  '-n', '--no-verify',
  '-s', '--signoff', '--no-signoff',
  '-e', '--edit', '--no-edit',
  '--amend', '--no-amend',
  '--allow-empty', '--allow-empty-message',
  '--dry-run',
  '--status', '--no-status',
  '--porcelain',
  '--reset-author',
]);

// Long value flags (do NOT alter staging). The `=`-form is recognised inline;
// the two-token form consumes the NEXT token as its value.
const SAFE_LONG_VALUE_NAMES = [
  'message', 'author', 'date', 'cleanup',
  'gpg-sign', 'no-gpg-sign', 'template', 'untracked-files',
  'reuse-message', 'reedit-message', 'fixup', 'squash', 'file',
];

// Short-flag letters known to `git commit` — so a combined token like `-am`
// can be decoded rather than dismissed as "unrecognised" (which would still be
// fail-closed, but mis-classify the message that follows `-m`).
const KNOWN_SHORT_LETTERS = new Set(['a', 'q', 'v', 'n', 's', 'e', 'm', 'F', 'c', 'C', 't', 'u', 'S']);
// Short-flag letters that consume the NEXT token as their value.
const SHORT_VALUE_LETTERS = new Set(['m', 'F', 'c', 'C', 't', 'u', 'S']);

// BUG-232: a token that is purely a shell redirection operator, optionally
// fd-prefixed — `>`, `>>`, `<`, `<<`, `2>`, `2>>`, `2<`, `0<&3`, … . The
// `N>&M` form only survives as `N>` once the shell segment is cut at `&`
// (see classifyCommitArgv); the full form is matched so a future boundary
// change stays safe. Deliberately NOT matched: a redirection with an inline
// target (`>file`, `2>err.log`) — that shape is rare, and leaving it to be
// classified as a pathspec keeps the guard fail-closed rather than
// over-allowing.
const SHELL_REDIRECT_TOKEN_RE = /^[0-9]*(?:>>?|<<?)(?:&[0-9]+)?$/;

/** Classifies one commit invocation's argv. Returns
 * { classifiable, allFlag, pathspecFromFile, barePathspec }. Absent/malformed
 * input returns classifiable:false (fail-closed). */
function classifyCommitArgv(inv, authorGuard) {
  const out = { classifiable: true, allFlag: false, pathspecFromFile: false, barePathspec: false };
  if (!inv || typeof inv !== 'object' || typeof inv.suffixStart !== 'number' || typeof inv.text !== 'string') {
    out.classifiable = false;
    return out;
  }
  // BUG-232: bound the commit's argument text to its own shell segment (the
  // same boundary findGitAddInvocations uses for `git add`), so a trailing
  // pipe/redirect/chain — `git commit -m "x" 2>&1 | tail`, `... && git push`
  // — is never tokenized as if its `2>&1` / `tail` / `git` / `push` tokens
  // were commit pathspecs (which used to trip the BUG-214 bare-pathspec deny
  // on an otherwise-clean commit).
  const mask = authorGuard.buildQuoteMask(inv.text);
  const boundary = findUnquotedShellBoundary(inv.text, mask, inv.suffixStart);
  const argsText = inv.text.slice(inv.suffixStart, boundary);
  const tokens = authorGuard.tokenize(argsText).filter((t) => t !== '');
  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    // BUG-232: a shell REDIRECTION operator token (`>`, `>>`, `2>`, `2>>`,
    // `<`, `2<`, `N>&M`, …) is the residue of a trailing redirection
    // (`2>&1`, `> file`) cut at the shell boundary above — shell plumbing,
    // not git argv. Everything from here on is outside the commit's argument
    // list, so stop classifying (the remaining tokens are shell-level, never
    // git pathspecs).
    if (SHELL_REDIRECT_TOKEN_RE.test(tok)) break;
    if (tok.startsWith('--')) {
      if (tok === '--all') { out.allFlag = true; out.classifiable = false; continue; }
      if (tok === '--pathspec-from-file' || tok === '--pathspec-file-nul' || tok.startsWith('--pathspec-from-file=')) {
        out.pathspecFromFile = true; out.classifiable = false; continue;
      }
      if (tok === '--') { out.barePathspec = true; out.classifiable = false; continue; }
      if (SAFE_COMMIT_FLAGS.has(tok)) continue;
      const valueName = SAFE_LONG_VALUE_NAMES.find((n) => tok === `--${n}` || tok.startsWith(`--${n}=`));
      if (valueName) {
        if (tok === `--${valueName}`) i++; // two-token form: consume the value
        continue;
      }
      out.classifiable = false; // unrecognised long flag → fail-closed
      continue;
    }
    if (tok.length > 1 && tok[0] === '-') {
      // Short flag, possibly combined (e.g. -am = -a -m).
      let unknown = false;
      let consumesValue = false;
      for (let j = 1; j < tok.length; j++) {
        const ch = tok[j];
        if (ch === 'a') out.allFlag = true;
        if (SHORT_VALUE_LETTERS.has(ch)) consumesValue = true;
        if (!KNOWN_SHORT_LETTERS.has(ch)) unknown = true;
      }
      if (out.allFlag) out.classifiable = false;
      if (unknown) out.classifiable = false;
      if (consumesValue) i++; // consume the value token (e.g. -m "msg", -am "msg")
      continue;
    }
    // A bare (non-flag) token is an explicit pathspec → --only/--include.
    out.barePathspec = true;
    out.classifiable = false;
  }
  return out;
}

function isArgvClassifiable(inv, authorGuard) {
  return classifyCommitArgv(inv, authorGuard).classifiable;
}

function isExemptCommit(files, inv, authorGuard) {
  return isExemptFileSet(files) && isArgvClassifiable(inv, authorGuard);
}

/** Returns the `git commit` invocation for `command`, or null if it is not a
 * literal `commit` (see isCommitInvocation). */
function getCommitInvocation(command, authorGuard) {
  const inv = authorGuard.findCommitInvocation(command);
  return inv && inv.verb === 'commit' ? inv : null;
}

function pathspecFromFileDenyMessage() {
  return [
    '🛑 DESTRUCTIVE GUARD (GR#23, BUG-213): this commit uses --pathspec-from-file / ' +
      '--pathspec-file-nul, which stages working-tree paths from a FILE this guard cannot ' +
      'enumerate from the command text. `git diff --cached` is therefore not a truthful preview ' +
      'of what will be committed, so this guard fails closed (GR#23 is a security rule).',
    '',
    'Stage the paths explicitly (`git add <paths>`) as its own tool call, then run `git commit` ' +
      'as a separate call — without --pathspec-from-file — so the staged-file check runs against ' +
      'the real, settled index.',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this ' +
      'command): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

function barePathspecDenyMessage() {
  return [
    '🛑 DESTRUCTIVE GUARD (GR#23, BUG-214): this commit carries an explicit pathspec argument (or a ' +
      'bare `--`), which invokes --only/--include semantics — the index is NOT what gets committed, ' +
      'and a glob pathspec cannot be enumerated from the command text. `git diff --cached` is ' +
      'therefore not a truthful preview, so this guard fails closed (GR#23 is a security rule).',
    '',
    'Split into two tool calls: `git add <paths>` first, then `git commit` (no pathspec) — so the ' +
      'staged-file check runs against the real, settled index.',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this ' +
      'command): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

function aliasedCommitDenyMessage() {
  return [
    '🛑 DESTRUCTIVE GUARD (GR#23, BUG-224): this commit reaches the commit verb through a git ' +
      'ALIAS (the verb word is not the literal `commit`). An alias body can smuggle staging-affecting ' +
      'flags (-a/--all, --pathspec-from-file, --only, …) that this guard cannot see from the command ' +
      'text, so it fails closed rather than guess.',
    '',
    'Use the literal `git commit` (not an alias) so the staged-file check runs against the real, ' +
      'settled index.',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this ' +
      'command): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

// ---------------------------------------------------------------------------
// BUG-232: fail-closed git-recognition sweep.
//
// findCommitInvocation() returns null for TWO different reasons: "this
// command contains no commit-creating git verb" (an ordinary non-commit
// command, which must stay silently allowed) and "a git token is present but
// its verb could not be parsed/resolved" (the guard failing OPEN on BUG-232's
// three live bypasses: shell-escape aliases — `!git commit -a`; value-taking
// global options the parser does not consume — `--config-env=alias.X=EV`,
// `--exec-path=...`; and any unrecognised verb word). This sweep re-scans the
// command with authorGuard.scanGitInvocations() — the shared,
// Destructive-hardened scanner that reports `parsed:false` for an unparseable
// git token, `shellEscapeAlias` for a `!`-body alias, and a shell-segment-
// bounded `tail` — and DENIES on any git invocation the parser cannot
// positively resolve to a known verb, EXCEPT a bare `--version`/`--help`
// (which take no value and therefore cannot hide a commit).
// ---------------------------------------------------------------------------

// Memoised set of every git command name, derived at runtime from
// `git --list-cmds=main` (GR#15 — the known-verb set is DATA, never a
// hand-maintained list). Null when derivation failed (git missing / too old
// to support --list-cmds); the sweep then skips the "unrecognised verb" rule
// but keeps the parsed:false and shell-escape-alias rules, neither of which
// needs this list.
let _knownGitCommands = null;
function knownGitCommands() {
  if (_knownGitCommands !== null) return _knownGitCommands;
  try {
    const out = execSync('git --list-cmds=main', { encoding: 'utf8', timeout: 5000 });
    _knownGitCommands = new Set(out.split(/\r?\n/).map((s) => s.trim()).filter(Boolean));
  } catch {
    _knownGitCommands = null; // derivation failed — sweep degrades (see above)
  }
  return _knownGitCommands;
}

/** Returns a deny-message string when `command` contains a git invocation the
 * recognition primitive cannot positively resolve to a known verb, else null.
 * Built on authorGuard.scanGitInvocations() (BUG-232) so this guard's
 * fail-closed sweep shares the ONE scanner rather than re-tokenizing. */
function failClosedSweep(command, authorGuard) {
  const entries = authorGuard.scanGitInvocations(command);
  if (entries.length === 0) return null; // no real git invocation anywhere

  const commitVerbs = authorGuard.KNOWN_COMMIT_VERBS;
  for (const entry of entries) {
    if (!entry.parsed) {
      // A git token was found but no subcommand word could be parsed after it
      // (an unrecognised value-taking global option, or a verbless invocation).
      // Fail closed — UNLESS it is a bare --version/--help, which takes no
      // value and therefore cannot hide a commit.
      const bare = (entry.tail || '').trim();
      if (bare === '--version' || bare === '--help') continue;
      return unparseableGitDenyMessage();
    }
    if (entry.shellEscapeAlias) {
      // The alias body starts with `!` — git hands it to the shell verbatim,
      // so its resolved leading word proves nothing about what actually runs.
      return shellEscapeAliasDenyMessage();
    }
    if (!commitVerbs.has(entry.resolved)) {
      // Lazy list-cmds derivation: only when a NON-commit verb needs
      // classifying, so a plain `git commit` (or an ordinary `npm install`
      // with no git token at all) never pays for the subprocess.
      const known = knownGitCommands();
      if (known && !known.has(entry.resolved)) {
        return unknownGitVerbDenyMessage(entry.resolved);
      }
    }
  }
  return null;
}

function unparseableGitDenyMessage() {
  return [
    '🛑 DESTRUCTIVE GUARD (GR#23, BUG-232): a git invocation was found whose subcommand could not be parsed (an unrecognised value-taking global option such as --config-env=... / --exec-path=..., or another unparseable shape). This guard cannot tell what the invocation does, so it fails closed rather than guess.',
    '',
    'Use a plain, recognisable git spelling (e.g. `git commit -m "..."` without an unparseable prefix), or one of the global options this guard already parses (-C/-c/--no-pager/...).',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

function shellEscapeAliasDenyMessage() {
  return [
    '🛑 DESTRUCTIVE GUARD (GR#23, BUG-232): this command reaches a git alias whose body is a shell-escape (`!...`) — git hands it to the shell verbatim, so the alias\'s leading word says nothing reliable about what actually runs (a `!git commit -a` body runs a commit even though it resolves to `git`). This guard cannot enumerate it, so it fails closed.',
    '',
    'Use the literal `git commit` (not a shell-escape alias) so the staged-file check runs against the real, settled index.',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

function unknownGitVerbDenyMessage(verb) {
  return [
    `🛑 DESTRUCTIVE GUARD (GR#23, BUG-232): the git verb "${verb}" is neither a known commit verb nor a recognised git command (git --list-cmds). This guard cannot tell what it does, so it fails closed rather than guess.`,
    '',
    'Use a recognised git verb (e.g. `git commit`), or fix the spelling.',
    '',
    'Emergency bypass (operator-only, set BEFORE the session starts — never inline in this command, see this file\'s header): CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1',
  ].join('\n');
}

// ---------------------------------------------------------------------------
// BOW connection — delegates to the shared claude-db.js helper (BUG-203,
// GR#3). The `require` is deliberately INSIDE the function (not at module
// top level), same class as the claude-bow.js/claude-author-guard.js lazy
// loads below: a load-time throw in a repo file must never crash this guard
// open. connectReadOnly() is only ever called from within main()'s existing
// try/catch, so a throw here (missing file, syntax error, or DB unreachable)
// is turned into the deny() fail-closed decision, never an uncaught exit.
// ---------------------------------------------------------------------------

async function connectReadOnly() {
  const { connect } = require('./claude-db.js');
  return connect({ connectTimeout: 4000 });
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
  const { authorGuard, findItemByRef, latestDestructiveVerdict, typePrefixes } = deps;

  // BUG-232 fail-closed recognition sweep: run BEFORE the commit check, so a
  // git invocation that findCommitInvocation() reports as "null" because it
  // COULD NOT PARSE (not because it is genuinely a non-commit command) is
  // denied rather than silently allowed.
  const sweepDenial = failClosedSweep(command, authorGuard);
  if (sweepDenial) {
    deny(sweepDenial);
    return;
  }

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

  // BUG-224: `git diff --cached` above reflects the index BEFORE this same
  // command's own `git add` (if any) has run — see the BUG-224 block above
  // for the full account. A found-but-ambiguous `git add` denies outright;
  // a found-and-simple one has its paths unioned into the classification set.
  const addInfo = computeAddedPathsOrAmbiguous(command, authorGuard);
  if (addInfo.ambiguous) {
    deny(ambiguousAddDenyMessage());
    return;
  }
  let classificationFiles = addInfo.hasAdd
    ? Array.from(new Set([...stagedFiles, ...addInfo.paths.map((p) => p.replace(/\\/g, '/'))]))
    : stagedFiles;

  // FEAT-077 / BUG-213 / BUG-214: the commit invocation's OWN argv can stage
  // files the `--cached` snapshot (taken before this command runs) cannot see.
  // Classify it and react fail-closed to each un-enumerable case.
  const commitInv = getCommitInvocation(command, authorGuard);

  // BUG-224 (round-4): an ALIASED commit verb (verbWord !== 'commit') means the
  // alias body's staging flags are invisible to classifyCommitArgv — the exact
  // bypass the round-3 Destructive found. Fail closed rather than guess.
  if (commitInv && commitInv.verbWord !== 'commit') {
    deny(aliasedCommitDenyMessage());
    return;
  }

  const argClass = classifyCommitArgv(commitInv, authorGuard);

  // BUG-213: --pathspec-from-file / --pathspec-file-nul stage working-tree
  // paths the guard cannot enumerate from the command text → deny.
  if (argClass.pathspecFromFile) {
    deny(pathspecFromFileDenyMessage());
    return;
  }
  // BUG-214: an explicit bare pathspec / `--` on the commit means
  // --only/--include semantics (the index is NOT what gets committed) and the
  // pathspec may be a glob → deny, same posture as an ambiguous `git add`.
  if (argClass.barePathspec) {
    deny(barePathspecDenyMessage());
    return;
  }
  // BUG-224 (round-2 REJECT): -a/--all stages tracked-modified files at
  // commit-execution time. Union the working-tree diff so codeBearing sees
  // what will actually be committed, not just the pre-command snapshot.
  if (argClass.allFlag) {
    const wtRaw = execSync('git diff --name-only', {
      cwd: rootDir(),
      encoding: 'utf8',
      timeout: 5000,
    });
    const wtFiles = wtRaw.split('\n').map(f => f.trim()).filter(Boolean);
    classificationFiles = Array.from(new Set([...classificationFiles, ...wtFiles.map((p) => p.replace(/\\/g, '/'))]));
  }

  // FEAT-077 exemption: docs/test-only AND classifiable → exempt (Tester-level
  // suffices, no Destructive verdict), even under an enforced dir.
  if (isExemptCommit(classificationFiles, commitInv, authorGuard)) {
    allowSilently();
    return;
  }

  let codeBearing = classificationFiles.some(isEnforcedDirPath);
  if (!codeBearing) {
    const rootLevelFiles = classificationFiles.filter(isRootLevel);
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

  const tags = extractTags(message, typePrefixes);
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
    getCommitInvocation,
    ENFORCED_DIR_RE,
    extractMessage,
    extractTags,
    looksLikeRealTag,
    deriveRootGuardScripts,
    isEnforcedDirPath,
    isRootLevel,
    noTagDenyMessage,
    verdictDenyMessage,
    loadDependencies,
    looksLikeCommitFallback,
    // BUG-224
    findGitAddInvocations,
    classifyAddArgs,
    computeAddedPathsOrAmbiguous,
    ambiguousAddDenyMessage,
    // FEAT-077 / BUG-213 / BUG-214
    isExemptFile,
    isExemptFileSet,
    isArgvClassifiable,
    isExemptCommit,
    classifyCommitArgv,
    // BUG-232
    failClosedSweep,
    knownGitCommands,
    unparseableGitDenyMessage,
    shellEscapeAliasDenyMessage,
    unknownGitVerbDenyMessage,
  };
}
