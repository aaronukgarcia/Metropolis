/**
 * PreToolUse hook — commit-identity guard, DEMOTED TO ADVISORY (FEAT-045,
 * BOW mkey: tool.authorguard; see docs/planning/acceptance/tool.committhook.md
 * for the escalation on this key vs the new hook's registry entry).
 *
 * ============================================================================
 * THE DEMOTION (FEAT-045, Aaron's ruling 2026-08-11) — READ THIS FIRST.
 * ============================================================================
 *
 * Four rounds of this file trying to detect a fabricated commit author by
 * parsing a proposed shell command STRING produced fifteen live bypasses
 * (BUG-044..052, BUG-079..082) and proved the approach architecturally
 * unsound: a regex is not a shell parser, and no amount of additional
 * tokenizing machinery changes that (ASM-350). Aaron's ruling moved REAL
 * enforcement to a `commit-msg` git hook (githooks/commit-msg, installed via
 * claude-committhook-install.js) that reads git's OWN resolved identity —
 * `git var GIT_AUTHOR_IDENT` / `GIT_COMMITTER_IDENT` — instead of parsing
 * anything. This file is what remains at the PreToolUse layer: it NEVER
 * blocks or pauses an agent's tool call any more. It may still warn.
 *
 * THIS FILE NO LONGER EMITS A BLOCKING DECISION OF ANY KIND (AC-6/7/8). No
 * code path here ever produces the harness-blocking permissionDecision
 * values (the ones this file used to write next to that field, spelled
 * "deny" and "ask") — every path exits 0. The detection machinery below
 * (steps 1-7, unchanged internals — the same regex/tokenizer machinery
 * ASM-350 already describes as unsound-by-construction stays exactly as
 * unsound as it is; demotion changes what happens with a positive detection,
 * not the detection quality itself) still runs, and when it believes it has
 * found an unsanctioned identity, this file now says so as INFORMATION ONLY
 * (a non-blocking advisory reason, or a stderr warning with a silent exit 0)
 * — never as a decision the harness could act on as a pause or refusal.
 *
 * WHAT STOPS BEING CAUGHT BEFORE IT STARTS, STATED PLAINLY (AC-10):
 *
 *   - BEFORE vs AT: this PreToolUse layer used to refuse BEFORE a commit was
 *     attempted — the agent never ran the command. The `commit-msg` hook
 *     refuses AT the moment of commit — after `git add`, after the message is
 *     composed, after any pre-commit tooling (linters/formatters wired to a
 *     `pre-commit` hook) has already run against the still-uncommitted
 *     change.
 *   - NOTHING IS LOST by the later catch: the index and working tree are
 *     untouched by a `commit-msg` rejection (verified live in this item's own
 *     research — real throwaway-repo evidence, see githooks/commit-msg's
 *     header) — the only cost is that the developer/agent re-does the
 *     message-composition work.
 *   - WHAT IS GENUINELY NO LONGER CAUGHT PRE-FLIGHT: an agent that would
 *     previously have been warned off attempting the commit at all now
 *     always reaches git. The ONLY remaining hard stop for a
 *     Bash/PowerShell-mediated commit is the `commit-msg` hook itself.
 *   - `git commit --no-verify` is git's own native, always-available hook
 *     bypass. This item does not attempt to close it (not possible from a
 *     hook, by construction — see githooks/commit-msg's header). A path that
 *     bypasses the PreToolUse-mediated tools entirely, or uses
 *     `--no-verify`, was never covered by THIS layer regardless of this
 *     change and is not a new gap introduced by the demotion.
 *
 * FAIL-OPEN, THE OPPOSITE OF THE HOOK IT BACKSTOPS (AC-8, contrast stated by
 * name per AC-16): every internal error in this file — a metro DB read
 * failure, an unreadable git config, a `git log` invocation error, an
 * unparseable stdin JSON, or any uncaught exception in the shared
 * sanctioned-identity module (claude-author-identity.js) — now results in a
 * silent, non-blocking exit 0. This is the EXACT INVERSE of
 * `githooks/commit-msg`, which is FAIL-CLOSED (an internal error there denies
 * the commit). The two layers deliberately disagree on this because they
 * have different costs of being wrong: a false pause at this advisory layer
 * used to cost a human/agent seconds for nothing; the enforcing hook is the
 * only remaining hard stop, so IT fails closed. This file no longer is a
 * hard stop, so it fails open.
 *
 * Spec: BUG-035 (original); BUG-044..BUG-052 and BUG-079..082
 * (Destructive-agent findings against successive versions of this guard,
 * the evidence base for the demotion); FEAT-045 (Aaron's ruling, this
 * demotion); GR#2 (Version/Identity Discipline); GR#15 (Validators Derive
 * From Data — no hardcoded expected values, and see
 * claude-author-identity.js for where the sanctioned-identity derivation now
 * lives, shared verbatim with githooks/commit-msg).
 *
 * WHY THIS FILE'S DETECTION MACHINERY EXISTS AT ALL — the evidence, not the
 * principle (retained from v1-v4, still true, still the reason this file is
 * worth keeping as an ADVISORY layer rather than deleting outright: a warning
 * before the commit is attempted is still useful information, even though it
 * is no longer trusted as a control).
 *
 * On 2026-08-10 an agent verifying git behaviour made a local test commit
 * authored as `test <test@test.com>` and left it on local main (BUG-035).
 * It never reached origin, but this repository is public and its history was
 * rewritten specifically to control which identity appears in it — a
 * fabricated author reaching origin would have undone that, permanently.
 * v1 of this guard (regex-matching `git commit` in the raw command string)
 * shipped the same day and was live for under a day before a Destructive
 * agent broke it nine ways (BUG-044..052) with ordinary git invocations, no
 * exotic tooling. This version is the fix for the CLASS those nine findings
 * share, not nine independent patches.
 *
 * THE CLASS-LEVEL PROBLEM, AND THE DECISION MADE HERE (read this before the
 * per-finding notes below — it is the actual design of this file).
 *
 * v1's failure mode was: "decide whether to engage by regex-matching a shell
 * command string." A regex is not a shell parser, and it is not a git
 * command-line parser either. Seven of the nine findings were different
 * facets of that one fact: `-c key=value` (044), shell quoting inside a
 * wrapper (045), commit-creating porcelain verbs other than the literal word
 * "commit" (046), aliases (047), line-continuation characters (048), the
 * `.exe` suffix (049), and scanning quoted message text as if it were flag
 * syntax (050, the false-positive, which is the *same* category of error —
 * treating raw text as tokens without knowing which characters are inside a
 * string literal).
 *
 * The fix is NOT "add a tenth regex branch." It is: build a small,
 * deliberately narrow, real parser for the one thing this guard needs to
 * parse — a `git <verb> ...` invocation, wherever it appears in the command
 * text — instead of a single anchored pattern match. Concretely:
 *
 *   1. NORMALIZE line-continuations (bash `\<newline>`, PowerShell
 *      `` ` <newline> ``) before anything else runs — BUG-048's shells treat
 *      them as whitespace; so does this guard, now.
 *   2. RECURSE into known "run this string as a command" wrappers (`bash -c`,
 *      `sh -c`, `zsh -c`, `dash -c`, `ksh -c`, `powershell -Command`,
 *      `pwsh -Command`, `cmd /c`), scanning their quoted body the same way as
 *      the outer command, to any bounded depth. This is deliberately not
 *      "special-case bash -c" (BUG-045's literal repro) — it is "any wrapper
 *      of this shape, however many are nested," which is the general form of
 *      the same hole.
 *   3. MATCH the git executable by TOKEN, not substring: boundary-anchored on
 *      both sides, optional `.exe`/`.cmd` suffix (BUG-049), so `git`,
 *      `git.exe`, `/usr/bin/git` all match and `github`/`mygit` do not.
 *   4. PARSE (not regex-scan) the run of recognised global options between
 *      the executable and the subcommand — `-C <dir>` (already handled in
 *      v1) and now `-c <key>=<value>` (BUG-044), collecting `user.email` /
 *      `user.name` overrides as a genuine identity-setting source, same tier
 *      as an env var.
 *   5. READ the subcommand word itself, and check it against a small set of
 *      commit-creating porcelain verbs — `commit`, `cherry-pick`, `revert`,
 *      `am`, `merge` (BUG-046) — rather than the literal string "commit".
 *      `rebase` stays deliberately excluded (see the note below); this is no
 *      longer a silent generalisation of that exclusion, because the other
 *      four verbs are now explicitly enumerated and checked, not swept in
 *      under "not commit."
 *   6. RESOLVE one level of git alias (`git config --get alias.<word>`,
 *      recursed up to a small depth with cycle protection) when the
 *      subcommand word is not itself a known verb (BUG-047) — a read-only
 *      config query, cheap, and it is exactly what git itself would do to
 *      decide what the command means.
 *   7. TOKENIZE (quote-aware) the arguments AFTER the verb, rather than
 *      regex-scanning the raw suffix text, and explicitly skip the token
 *      that is the *value* of `-m`/`--message` when looking for `--author`
 *      (BUG-050) — the fix for the false positive is the same fix as the
 *      fix for the bypasses: know where the token boundaries are before
 *      deciding what a substring means.
 *
 * This still is not a full shell/git parser, and does not try to be one —
 * see "WHAT IS DELIBERATELY NOT HANDLED" below for the residual gap stated
 * plainly rather than hidden. But it replaces "one regex, anchored on
 * literal text" with "tokenize the one grammar this guard actually cares
 * about," which is what closes the *class*, not just the nine instances
 * that happened to get demonstrated.
 *
 * THE FAIL-CLOSED / FALSE-POSITIVE BALANCE, STATED EXPLICITLY (BUG-050 is
 * the counterweight to fail-closed, and the brief is right that it matters
 * as much as the bypasses):
 *
 *   - Anything this guard cannot POSITIVELY resolve to a known-safe verb
 *     stays fail-closed exactly as v1 was: unparsable `--author` value,
 *     internal errors, no sanctioned identity derivable at all → deny.
 *   - But "fail closed on the unparsable" must not mean "fail closed on
 *     text this guard mis-tokenized." BUG-050 was not a case where the
 *     input was genuinely ambiguous — the guard had enough information (an
 *     `-m` flag immediately before the string) to know it was looking at a
 *     message body, and ignored that information. Fixing the tokenizer to
 *     use information it already has is not "loosening" fail-closed; a
 *     guard that blocks its own documentation teaches operators to reach
 *     for CLAUDE_DISABLE_AUTHOR_GUARD=1, which protects nothing (BUG-050's
 *     own framing, and correct). So: fail closed on the UNKNOWN, not on the
 *     KNOWN-BUT-PREVIOUSLY-MISPARSED.
 *
 * WHAT IS DELIBERATELY NOT HANDLED — STATED PLAINLY, NOT HIDDEN:
 *
 *   - `git commit -C <commit>` / `-c <commit>` (v1's ASM-188, the REUSE
 *     flags that copy an arbitrary other commit's author/message) remain
 *     unhandled. Not raised by any of BUG-044..052 either. Logged again
 *     here as ASM-author-guard-reuse-flags-still-open.
 *   - `git merge`, `git commit-tree`, `git fast-import`, `git stash store`
 *     also create commit objects. `merge` is now covered (added to the verb
 *     set, since it is common and its identity fields work identically to
 *     `commit`). `commit-tree`, `fast-import`, and `stash store` are NOT —
 *     they are low-level plumbing an interactive agent essentially never
 *     reaches by accident, and `fast-import` in particular takes a data
 *     stream on stdin this guard cannot inspect from the command string at
 *     all (a real, structural limit of a PreToolUse text-based hook, not a
 *     laziness gap). Logged as ASM-author-guard-plumbing-verbs-unhandled.
 *   - Alias resolution is bounded (depth 4, cycle-guarded) and reads only
 *     the alias's own config value; if an alias body itself contains further
 *     `-c` overrides or wrapper invocations, those are NOT re-parsed out of
 *     the alias definition. Logged as ASM-author-guard-alias-body-not-reparsed.
 *   - The wrapper-recursion list (bash/sh/zsh/dash/ksh -c,
 *     powershell/pwsh -Command, cmd /c) is the set of shells this project's
 *     environment (CLAUDE.md: PowerShell primary, Bash tool for POSIX,
 *     Windows host) actually exposes to an agent. It is not an exhaustive
 *     list of every shell that exists. A wrapper this guard does not
 *     recognise is, by construction, invisible to it — same structural
 *     limit as above, not a parsing bug. Logged as
 *     ASM-author-guard-wrapper-list-not-exhaustive.
 *
 * WHAT COUNTS AS "SANCTIONED" — DERIVED, NOT HARDCODED (GR#15). Unchanged
 * from v1 in shape, extended with one new source:
 *
 *   1. THE CURRENTLY CONFIGURED GIT IDENTITY on this machine/repo —
 *      `git config user.email`. Trusted unconditionally — operator-set data
 *      outside the command text this guard evaluates.
 *   2. EMAILS SEEN REPEATEDLY IN THE TRUNK BRANCH'S OWN HISTORY (`main` if it
 *      exists locally, else `master`, else current branch) — as author OR
 *      committer, over HISTORY_THRESHOLD (3) commits, scanned over the most
 *      recent deriveScanLimit() commits — the repo's real commit count,
 *      capped at HISTORY_SCAN_LIMIT (BUG-052's bound; ASM-226's derivation —
 *      see claude-author-identity.js).
 *   3. EXTENSION FOR A LEGITIMATE SECOND CONTRIBUTOR —
 *      CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES, operator-set env var, never read
 *      from the proposed command text.
 *   4. NEW: `-c user.email=...` / `-c user.name=...` PARSED OUT OF THE
 *      COMMAND ITSELF (BUG-044) is NOT added to the sanctioned set — the
 *      opposite: it is read as the identity the invocation WOULD produce,
 *      and checked against the sanctioned set exactly like an env-var
 *      override. Listing it here so the two roles ("-c defines what this
 *      invocation claims to be" vs "config/history/env define what is
 *      trusted") are not confused.
 *
 * MATCHING IS BY EMAIL ONLY (unchanged from v1 — see ASM-author-guard-email-only,
 * logged originally for BUG-035; real evidence: same person, different name
 * casing across author/committer on this repo's own HEAD at design time).
 *
 * `git rebase` is not intercepted: it replays commits through internal
 * plumbing (not any of the porcelain verbs this guard now enumerates), so an
 * already-vetted author/committer chain passing through it is unaffected.
 * ASM-author-guard-rebase-scope (v1, still true — now explicitly true
 * *because* the verb set is enumerated, not by "not literally commit").
 *
 * BUG-051 — DISCLOSURE: v1 printed the FULL sanctioned email list in every
 * warning. That warning flows into agent transcripts / logs, and the
 * repo's own history was rewritten specifically to keep the operator's real
 * address off this public repo (BUG-042) — undermined by printing it right
 * back out through this guard's own output path. Fixed by naming the FIELD
 * that failed and the COUNT of sanctioned identities, never the addresses
 * themselves. The operator can always check `git config user.email`
 * directly; that is a deliberately higher-friction path than a value that
 * lands in a shared transcript by default. Unchanged by the demotion.
 *
 * BUG-052 — RESOURCE: v1's `git log <trunk>` was unbounded. Capped at
 * claude-author-identity.js's THRESHOLDS.HISTORY_SCAN_LIMIT most-recent
 * commits — generous for THRESHOLDS.HISTORY_THRESHOLD to be reached by any
 * real, actively-committing identity, bounded against unbounded cost on a
 * large/old history. Logged as ASM-author-guard-history-scan-cap.
 * ASM-226 (GR#15): the cap is now DERIVED at guard-run time from the repo's
 * actual commit count (identity.deriveScanLimit()), still bounded by
 * HISTORY_SCAN_LIMIT — see claude-author-identity.js. Unchanged by the
 * demotion (the bound lives in the shared module now).
 *
 * FAIL-OPEN (inverted from v1-v4's fail-closed posture by this demotion —
 * see the block at the top of this header): a false warning costs a human
 * nothing but reading it; a false SILENCE at this layer costs nothing either,
 * because this layer is no longer the control — `githooks/commit-msg` is.
 * Any internal error here is swallowed, silently, exit 0.
 *
 * Deliberate disable (this guard's advisory output entirely, if it becomes
 * noisy — it can no longer block anything, so this is a volume knob, not a
 * safety bypass): CLAUDE_DISABLE_AUTHOR_GUARD=1, set in the harness
 * process's own environment before the session starts.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Every path exits 0. A positive detection may additionally write a
 * non-blocking advisory reason via hookSpecificOutput (harness-visible,
 * non-pausing) or a plain stderr warning — see advise() below.
 */

'use strict';

const fs = require('fs');
const { execFileSync } = require('child_process');
// AC-4: sanctioned-identity derivation lives in ONE shared module, required
// here and by githooks/commit-msg — never reimplemented in either file.
const identity = require('./claude-author-identity.js');
// BUG-123 round 6: buildQuoteMask() (and its heredoc helpers) moved to a
// single shared module, claude-quote-mask.js, so this file,
// claude-pre-commit-check.js and claude-git-commit-trigger.js all consume
// the SAME BUG-077/BUG-078-hardened scanner rather than each carrying (or
// hand-modeling) their own copy — see that module's header for the full
// history of why "modeled on" kept re-shipping already-fixed gaps.
// BUG-044 round 2: consumeShellToken/dequoteShellToken also moved to the
// shared module — parseGitInvocation()'s own `-c`/`-C` option-value scanning
// used a bare `(\S+)` regex fallback that never went through BUG-123's
// hardening (attacker "Corvid" reject, round 2): a value like
// `user.email="fake attacker <fake@evil.com>"` is ONE shell token (the
// unquoted `user.email=` prefix runs straight into the quoted remainder with
// no gap), but `\S+` stops at the first whitespace even inside the open
// quote, truncating to `user.email="fake` and corrupting the verb parse that
// follows. consumeShellToken() (walk to the first position that is BOTH
// unquoted AND whitespace, per buildQuoteMask) and dequoteShellToken() (strip
// the token's own quote characters/escapes once its true end is known) are
// the same fix BUG-123 round 6 applied to claude-git-commit-trigger.js,
// reused here rather than re-patched with yet another bare regex.
// BUG-082: matchHeredocHeader()/findHeredocBodyEnd() (the same heredoc
// helpers buildQuoteMask() already uses internally, and the same ones
// BUG-081/BUG-078 hardened) are pulled in here too, so tokenize() can treat a
// heredoc BODY the same way buildQuoteMask() already treats it for the
// GIT_TOKEN_RE scan: opaque, not scanned as flag/argument syntax. Before this
// fix, tokenize() was a second, independent hand-rolled scanner (quote-aware
// only, no heredoc awareness at all) that never consulted these helpers, so a
// legitimate `git commit -F - <<EOF` whose piped-in message merely MENTIONS
// "--author=<...>" as prose was tokenized as if that prose were real
// command-line flag syntax — the false DENY this bug is about. Reusing the
// SAME helpers already proven correct for the identical "heredoc body is
// opaque" fact (GR#3) rather than writing new heredoc-detection logic here.
const {
  buildQuoteMask,
  consumeShellToken,
  dequoteShellToken,
  matchHeredocHeader,
  findHeredocBodyEnd,
} = require('./claude-quote-mask.js');

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Porcelain verbs that create a commit object and derive author/committer
// from config/env/flags the way `git commit` does. See header item 5.
const KNOWN_COMMIT_VERBS = new Set(['commit', 'cherry-pick', 'revert', 'am', 'merge']);

// Verbs that are common enough that alias resolution should not bother
// querying config for them (cheap short-circuit, not a correctness
// requirement — alias resolution would just no-op on these anyway since
// they already match KNOWN_COMMIT_VERBS or are obviously not aliases).
const MAX_WRAPPER_DEPTH = 4;
const MAX_ALIAS_DEPTH = 4;

const ENV_VAR_NAMES = [
  'GIT_AUTHOR_NAME',
  'GIT_AUTHOR_EMAIL',
  'GIT_COMMITTER_NAME',
  'GIT_COMMITTER_EMAIL',
];

// ---------------------------------------------------------------------------
// Hook plumbing — DEMOTED (AC-6/7/8/9): every path below exits 0. There is
// no function anywhere in this file that writes a harness-blocking decision
// — the pausing/refusing field value this file used to emit next to
// "permissionDecision" (spelled without the adjacent literal here on
// purpose, see AC-6's own grep) simply does not appear in this file at all
// any more, in any branch, reachable or not.
// ---------------------------------------------------------------------------

function allow() {
  process.exit(0);
}

/** AC-9: advisory only. Emits a non-blocking, harness-visible reason via the
 * SAME field the old blocking decision used to travel next to
 * (hookSpecificOutput / permissionDecisionReason) but with the decision
 * itself set to the harness's non-pausing value — proceeds without pausing,
 * exactly like allow(), just with a message attached. Always exits 0. */
function advise(reason) {
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'allow',
        permissionDecisionReason: reason,
      },
    })
  );
  process.exit(0);
}

function readStdin() {
  try {
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

function git(args) {
  return execFileSync('git', args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'ignore'],
  }).trim();
}

// ---------------------------------------------------------------------------
// Step 1: normalize line-continuations (BUG-048)
// ---------------------------------------------------------------------------

/** Bash `\<newline>` and PowerShell `` ` <newline> `` both mean "this is one
 * logical line" to their respective shells. Collapse both to a single space
 * before any matching runs, so the rest of this file never has to know they
 * existed. */
function normalizeContinuations(cmd) {
  return cmd.replace(/\\\r?\n/g, ' ').replace(/`\r?\n/g, ' ');
}

// ---------------------------------------------------------------------------
// Step 2: recurse into shell-wrapper bodies (BUG-045 and its general form)
// ---------------------------------------------------------------------------

const WRAPPER_PATTERNS = [
  // bash -c '...' / sh -c "..." / zsh|dash|ksh -c ... (any flags before -c)
  /(?:^|[;&|(\n])\s*(?:bash|sh|zsh|dash|ksh)(?:\.exe)?\s+(?:-\S+\s+)*-c\s+(?:"((?:[^"\\]|\\.)*)"|'([^']*)')/gi,
  // PowerShell -Command "..." (accepts -Command or the shortest unambiguous
  // prefix in real pwsh, but this guard only needs to recognise the common
  // spelling — see header on the wrapper list not being exhaustive)
  /(?:^|[;&|(\n])\s*(?:powershell|pwsh)(?:\.exe)?\s+(?:-\S+\s+)*-command\s+(?:"((?:[^"\\]|\\.)*)"|'([^']*)')/gi,
  // cmd /c "..."
  /(?:^|[;&|(\n])\s*cmd(?:\.exe)?\s+\/c\s+(?:"((?:[^"\\]|\\.)*)"|'([^']*)')/gi,
];

/** Returns an array of candidate command texts to scan for a git invocation:
 * the (normalized) input itself, plus the bodies of any recognised
 * "run this string as a command" wrappers, recursed to MAX_WRAPPER_DEPTH.
 * Order matters: the outer text is checked first (index 0), so a direct
 * invocation is found before any nested one. */
function gatherScanTexts(cmd, depth) {
  const normalized = normalizeContinuations(cmd);
  const texts = [normalized];
  if (depth >= MAX_WRAPPER_DEPTH) return texts;
  for (const pattern of WRAPPER_PATTERNS) {
    const re = new RegExp(pattern.source, pattern.flags);
    let m;
    while ((m = re.exec(normalized)) !== null) {
      // m[1] is the DOUBLE-quoted capture (`"((?:[^"\\]|\\.)*)"`), which
      // preserves its escaping literally (`\"`, `\\`) rather than the real
      // characters the shell would actually hand to the nested command —
      // this is the gap the Tester found: a correctly-escaped nested wrapper
      // (`bash -c "bash -c \"git commit ... --author=...\""`) recurses only
      // ONE level, because the inner WRAPPER_PATTERNS regex looks for a
      // literal `"` and the still-escaped body has `\"` (backslash then
      // quote) there instead. Unescaping the double-quoted capture before
      // recursing — turning `\"` back into `"` and `\\` back into `\` — is
      // what makes the SAME wrapper-detection regex fire again on the next
      // level down, achieving the MAX_WRAPPER_DEPTH the header/ASM-229
      // already claims, instead of silently stopping at depth 1.
      // m[2] (SINGLE-quoted capture, `'([^']*)'`) is never escaped by real
      // shells inside single quotes, so it is used as-is — unescaping it
      // would be wrong, not merely unnecessary.
      const body = m[1] !== undefined ? unescapeDoubleQuoted(m[1]) : m[2];
      if (body) texts.push(...gatherScanTexts(body, depth + 1));
    }
  }
  return texts;
}

/** Reverses the escaping a real shell applies inside a double-quoted
 * argument (`\"` -> `"`, `\\` -> `\`, `\x` -> `x` for any other escaped
 * character) so a captured wrapper body reads the way the nested command
 * actually receives it, not the way it was typed at the outer shell. Mirrors
 * exactly what WRAPPER_PATTERNS' own capture grammar (`(?:[^"\\]|\\.)*`)
 * allowed through: every backslash there was already paired with the
 * following character as a single escaped unit, so dropping the backslash
 * and keeping that character is a full, correct unescape — not a heuristic. */
function unescapeDoubleQuoted(s) {
  return s.replace(/\\(.)/g, '$1');
}

// ---------------------------------------------------------------------------
// Step 3-5: locate `git <verb>`, parsing (not regexing) global options
// ---------------------------------------------------------------------------

// Boundary-anchored git executable token: `git`, `git.exe`, `git.cmd`, or any
// of those preceded by a path (`/usr/bin/git`, `C:\...\git.exe`, or a
// SPACE-CONTAINING Windows install path only reachable quoted —
// `"C:\Program Files\Git\bin\git.exe"` — Git for Windows' actual default
// install location; found live-executing by a sibling-guard round, see
// ASM-author-guard-quoted-path-token below). The left-hand boundary class is
// `^`, a shell separator (`;&|(\n`), OR plain whitespace (`\s` — BUG-079).
// v1/v2 deliberately left plain whitespace OUT of this class, reasoning that
// it was "a generic word-boundary" and would reopen BUG-050's false-positive
// shape (matching "git" inside prose). That reasoning no longer holds and,
// per BUG-079 (P0, live-verified), left an ordinary single leading wrapper
// word — `sudo git commit ...`, `env git commit ...`, `time`, `nice`,
// `command`, `xargs -I{} ...`, or literally any other one-word wrapper this
// guard cannot enumerate — putting a plain space (not a separator character)
// immediately before "git", so the ENTIRE match failed and
// findCommitInvocation() returned null: total silent non-detection, not a
// narrowed one. The prose concern is a non-issue today because it is handled
// by a DIFFERENT, more precise mechanism than the boundary class ever was:
// buildQuoteMask() (BUG-043, see below) tells the scan loop whether the
// matched "git" token sits inside an outer quoted string (real prose, e.g.
// `echo "please git commit later"` or a `-m "... git ..."` message) and
// skips it there regardless of what character preceded the boundary. Adding
// `\s` to the boundary class only lets the token-matching START at an
// ordinary word gap; it does not change what counts as "real" vs "prose" —
// that distinction is still entirely the quote-mask's job, unchanged by this
// fix. The token itself still requires an exact `git`/`git.exe`/`git.cmd`
// word (or a path ending in one) immediately after the boundary, with a
// mandatory trailing `(?=\s)` — so this still cannot turn "mygit", "digit",
// or "gitlint" into a false match; only a real standalone "git" word,
// wherever it sits after whitespace, start-of-text, or a shell separator.
//
// Between the boundary and the token an optional run of inline env-var
// assignments is allowed — `GIT_AUTHOR_NAME=x GIT_AUTHOR_EMAIL=y git commit`
// is exactly BUG-035's original shape, and without this the whole command
// would simply fail to match (no boundary character directly precedes
// "git"), which would silently ALLOW it rather than deny it — carried
// forward unchanged from v1.
//
// The token itself is ONE capturing group (group 1) with three alternatives,
// tried in this order:
//   1. A double-quoted path+filename: `"...[\/]git(.exe|.cmd)?"` — the WHOLE
//      quoted string is the token, including its own quote characters. This
//      is what lets a space-containing install path be matched at all (an
//      unquoted alternative structurally cannot contain a space and still be
//      one shell word).
//   2. The same, single-quoted.
//   3. An unquoted path+filename, where the path prefix may itself contain
//      spaces (`[^"'<>|&;()\n]*`, NOT `\s`-excluding) — because on Windows,
//      an UNQUOTED command line with spaces in it is not simply "invalid
//      syntax that fails safely." Windows' own CreateProcess (which cmd.exe
//      and other launchers use when given a raw, unquoted command string)
//      resolves an ambiguous unquoted path by trying progressively longer
//      prefixes at each space boundary as a candidate executable path —
//      `C:\Program.exe`, then `C:\Program Files\Git.exe`, then
//      `C:\Program Files\Git\bin\git.exe`, and so on — and runs whichever
//      prefix first resolves to a real file, treating the remainder of the
//      line as that program's arguments. This is the same OS-level ambiguity
//      behind the classic "unquoted service path" vulnerability class
//      (CWE-428), and it is exactly what let a sibling-guard round verify
//      `C:\Program Files\Git\bin\git.exe commit --author=...` executing
//      UNQUOTED, with no shell-level quoting at all, on this project's own
//      Windows host, against Git for Windows' actual default install path.
//      Excluding whitespace from the unquoted prefix (the pre-round-4 shape)
//      would have kept this guard blind to exactly that. Still excluded from
//      the prefix: quote characters (would break quoted-arg detection
//      downstream) and shell metacharacters `<>|&;()` and newline (those end
//      a shell word/command unambiguously even when unquoted).
// Must be followed by whitespace (so a bare trailing "git" or "github" never
// matches, and so the quoted-path form's closing quote is genuinely the end
// of that shell word, not the middle of a longer one). Callers use
// `match.index + match[0].length` as the position right after the token,
// which is what parseGitInvocation() needs; group 1 alone is what
// findCommitInvocation() uses to tell the quoted-path shape apart from the
// unquoted one when deciding what buildQuoteMask's mask means at this match
// (see the comment at that call site — a quoted PATH is legitimate shell
// syntax, not prose, so it needs a different quote-mask check than a bare
// "git" sitting inside someone else's quotes does).
const GIT_TOKEN_RE =
  /(?:^|[;&|(\n]|\s)(?:\s*(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S+))*\s*("(?:[^"]*[\\/])?git(?:\.(?:exe|cmd))?"|'(?:[^']*[\\/])?git(?:\.(?:exe|cmd))?'|(?:[^"'<>|&;()\n]*[\\/])?git(?:\.(?:exe|cmd))?)(?=\s)/gi;

// ---------------------------------------------------------------------------
// Quote-state tracking for GIT_TOKEN_RE (BUG-043, this guard's instance)
// ---------------------------------------------------------------------------
//
// buildQuoteMask() (and its heredoc helpers, matchHeredocHeader() /
// findHeredocBodyEnd()) moved to claude-quote-mask.js (BUG-123 round 6) and
// is imported at the top of this file. This is what GIT_TOKEN_RE's boundary
// class (`[;&|(\n]`) was missing on its own: that class exists to catch a
// REAL git invocation hidden after a shell separator, but a regex has no
// notion of "inside a string literal" by itself, so `(` inside an ordinary
// quoted sentence — e.g. a BOW comment like `"...(git commit --author=... is
// the bypass BUG-035 fixed)"` — matched the boundary class exactly as if it
// were real shell syntax, then found `git commit` right after it and, having
// no `-m`/`-F`/heredoc to point to as the message source, denied ordinary
// prose (BUG-043, filed against the four sibling guards sharing this regex
// shape). See claude-quote-mask.js's own header for the escape-awareness
// history (BUG-077/BUG-078) and why it is now required here rather than
// defined here.

/** Parses the run of recognised global git options starting at `pos` in
 * `text` (right after the git token): `-C <dir>` and `-c <key>=<value>`
 * (repeatable, any order), then the subcommand word. Returns
 * { overrides, verbWord, verbStart, verbEnd } or null if no subcommand word
 * follows. `overrides` maps lowercased config keys (e.g. "user.email") to
 * their raw values, from `-c` only (BUG-044).
 *
 * BUG-044 round 2 (attacker "Corvid", REJECT): `-c`'s value is scanned with
 * `consumeShellToken()` (walk to the first position that is both unquoted
 * AND whitespace, per a `buildQuoteMask()` mask of the WHOLE text) rather
 * than a bare `(\S+)`/fully-pre-quoted-alternation regex — a real shell
 * treats `user.email="fake attacker <fake@evil.com>"` as ONE token (the
 * unquoted `user.email=` prefix runs straight into the quoted remainder with
 * no gap), and only a mask-aware walk gets that boundary right instead of
 * truncating at the first space inside the open quote. Once the token's true
 * end is known, `dequoteShellToken()` strips its embedded quote characters/
 * escapes to recover the shell's own view of the value before the `=` split.
 * `-C <dir>`'s value is a plain unquoted directory argument in every
 * existing test/repro for this guard, so it keeps its own quote-aware token
 * scan (consistency with `-c`, and so a quoted/spaced `-C` path is not
 * silently mis-consumed either) without needing value capture. */
function parseGitInvocation(text, pos) {
  const overrides = {};
  const mask = buildQuoteMask(text);
  let i = pos;
  for (;;) {
    const cHead = /^\s+-c(\s+)/.exec(text.slice(i));
    if (cHead) {
      const valueStart = i + cHead[0].length;
      const valueEnd = consumeShellToken(text, valueStart, mask);
      if (valueEnd !== -1) {
        const kv = dequoteShellToken(text.slice(valueStart, valueEnd));
        const eq = kv.indexOf('=');
        if (eq > 0) overrides[kv.slice(0, eq).toLowerCase()] = kv.slice(eq + 1);
        i = valueEnd;
        continue;
      }
      break; // unparseable -c value (unterminated quote / empty) — stop the option run
    }
    const capCHead = /^\s+-C(\s+)/.exec(text.slice(i));
    if (capCHead) {
      const valueStart = i + capCHead[0].length;
      const valueEnd = consumeShellToken(text, valueStart, mask);
      if (valueEnd !== -1) {
        i = valueEnd;
        continue;
      }
      break;
    }
    const longOpt = /^\s+(?:--git-dir=\S+|--work-tree=\S+)/.exec(text.slice(i));
    if (longOpt) {
      i += longOpt[0].length;
      continue;
    }
    // BUG-224 round-4 bypass #3: benign VALUELESS global options before the
    // verb (`git -p commit`, `git --no-pager commit`) used to fall out of
    // this loop unconsumed, so the verb-word regex below hit the option's
    // leading `-` and returned null — total non-recognition, and every
    // downstream check (author identity here, GR#23's verdict gate in
    // claude-destructive-guard.js) was skipped. These options only affect
    // pager/output/pathspec-matching behaviour — none of them can change
    // WHAT a commit stages or WHO authors it — so consuming them is safe and
    // makes the verb resolve normally (a `git -p commit` is now gated like a
    // plain `git commit`). Deliberately NOT consumed here: any option taking
    // a value we don't parse (`--config-env=...`, `--exec-path=...`) — those
    // still fall through to the null return, which callers adopting BUG-232's
    // fail-closed posture treat as "could not parse", never "not git".
    // Boundary lookahead (?=\s) so `--no-pagerx` (not a real option) does not
    // match as a prefix. `-p`/`-P` are case-distinct real git options
    // (paginate / no-pager).
    const benignOpt =
      /^\s+(?:-p|-P|--paginate|--no-pager|--no-replace-objects|--no-optional-locks|--no-advice|--no-lazy-fetch|--literal-pathspecs|--glob-pathspecs|--noglob-pathspecs|--icase-pathspecs|--bare)(?=\s)/.exec(
        text.slice(i)
      );
    if (benignOpt) {
      i += benignOpt[0].length;
      continue;
    }
    break;
  }
  const ws = /^\s+/.exec(text.slice(i));
  if (!ws) return null;
  i += ws[0].length;
  const word = /^[A-Za-z][A-Za-z-]*/.exec(text.slice(i));
  if (!word) return null;
  return {
    overrides,
    verbWord: word[0],
    verbStart: i,
    verbEnd: i + word[0].length,
  };
}

/** Resolves `word` to a known commit verb via git alias config, if it is not
 * already one. Reads `git config --get alias.<word>` and follows one-word
 * alias chains up to MAX_ALIAS_DEPTH with cycle protection (BUG-047). Only
 * the alias target's own LEADING WORD is used to classify the verb — an
 * alias body's own flags/overrides are not re-parsed (see header: NOT
 * HANDLED). Returns the resolved verb string (which may or may not be in
 * KNOWN_COMMIT_VERBS) or the original word if no alias exists / on error. */
function resolveAlias(word, depth, seen, overrides) {
  return resolveAliasDetailed(word, depth, seen, overrides).verb;
}

/** BUG-224 round-4 bypass #1 (shell-escape alias). The exact resolution walk
 * resolveAlias() has always done, but ALSO reporting whether any alias target
 * in the chain was a shell-escape body (leading `!`) — git hands such a body
 * to the SHELL verbatim, so its leading word says nothing reliable about what
 * runs (`!status; git commit -a` runs a commit even though its leading word
 * resolves to the innocuous `status`). resolveAlias()'s legacy behaviour —
 * strip `[!\s]*`, classify by the leading word — is PRESERVED verbatim in
 * `.verb` (this function is its single implementation; the wrapper above
 * keeps every existing caller byte-identical, GR#3), while `.shellEscape`
 * gives fail-closed consumers (claude-destructive-guard.js's BUG-232 sweep)
 * the honest signal the leading-word heuristic launders away. Returns
 * { verb, shellEscape }. */
function resolveAliasDetailed(word, depth, seen, overrides) {
  if (KNOWN_COMMIT_VERBS.has(word)) return { verb: word, shellEscape: false };
  if (depth >= MAX_ALIAS_DEPTH) return { verb: word, shellEscape: false };
  if (seen.has(word)) return { verb: word, shellEscape: false }; // cycle guard
  seen.add(word);
  let target;
  // BUG-231: an inline `-c alias.<word>=...` override (parsed into `overrides`
  // by parseGitInvocation, keys lowercased) takes precedence over persistent
  // config. Previously only persistent config was read, so `git -c
  // alias.x='commit -a' x` was never recognised as a commit invocation at all.
  const inlineKey = `alias.${word.toLowerCase()}`;
  if (overrides && Object.prototype.hasOwnProperty.call(overrides, inlineKey)) {
    target = overrides[inlineKey];
  } else {
    try {
      target = git(['config', '--get', `alias.${word}`]);
    } catch {
      return { verb: word, shellEscape: false }; // no such alias — leave as-is (caller decides safety)
    }
  }
  if (!target) return { verb: word, shellEscape: false };
  const isShellEscape = target.trim().startsWith('!');
  const m = /^[!\s]*([A-Za-z][A-Za-z-]*)/.exec(target.trim());
  if (!m) return { verb: word, shellEscape: isShellEscape };
  const rest = resolveAliasDetailed(m[1], depth + 1, seen, overrides);
  return { verb: rest.verb, shellEscape: isShellEscape || rest.shellEscape };
}

/** Finds the first `git <commit-creating-verb>` invocation across the given
 * command text and any recognised wrapper bodies. Returns
 * { text, verb, overrides, prefixEnd, suffixStart } or null. `text` is the
 * (possibly nested) string the invocation was found in — flags belong to
 * that string, not necessarily the outer command. */
function findCommitInvocation(cmd) {
  // BUG-232 refactor: this is now a THIN FILTER over scanGitInvocations() —
  // the one scanner both guards share — rather than its own scan loop.
  // Return shape and first-match semantics are byte-identical to the
  // pre-refactor implementation (the regression corpus in the test file
  // proves it): the first scanned invocation, in gatherScanTexts()/
  // left-to-right order, whose RESOLVED verb is commit-creating.
  for (const entry of iterGitInvocations(cmd)) {
    if (entry.parsed && KNOWN_COMMIT_VERBS.has(entry.resolved)) {
      return {
        text: entry.text,
        verb: entry.resolved,
        verbWord: entry.verbWord, // original word — callers detect aliasing (BUG-224)
        overrides: entry.overrides,
        prefixEnd: entry.verbEnd, // env-var prefix search bound: up to & incl. verb word
        suffixStart: entry.verbEnd,
      };
    }
    // Not a commit-creating verb (or rebase, status, etc.) — keep scanning
    // the rest in case of `git status; git commit ...`-shaped chains where
    // an earlier `git` token is a red herring.
  }
  return null;
}

/** True when a GIT_TOKEN_RE match `m` in `text` is PROSE (inside someone
 * else's quoted region) rather than a real invocation, per `quoteMask`.
 *
 * BUG-043 (this guard's instance) / ROUND4-3 (quoted-path token,
 * sibling-guard finding): the boundary class matched, but whether that is
 * real shell syntax or prose depends on the SHAPE of the token (group 1)
 * that GIT_TOKEN_RE actually found:
 *
 *   - UNQUOTED token (`git`, `git.exe`, `/usr/bin/git`, ...): if this bare
 *     text is itself sitting inside someone else's quoted argument, the
 *     boundary character before it is necessarily inside that same quoted
 *     region too (quote state only changes on an actual quote character,
 *     and there is none between the boundary and the token — only the
 *     optional env-var-assignment run, which is itself quote-balanced when
 *     real). So checking the position of "git" alone is sufficient to know
 *     the whole match was prose — unchanged from BUG-043's original fix.
 *   - QUOTED-PATH token (`"C:\...\git.exe"` / `'...'`): the quote
 *     characters here are THE TOKEN's own syntax, not prose-quoting — a
 *     real shell strips exactly this pair and executes the path inside.
 *     Checking the position of "git" inside it would always read as
 *     "inside a quote" and wrongly skip every legitimately quoted path as
 *     if it were prose. What actually matters is whether the token's OWN
 *     opening quote character is itself already inside some OUTER quoted
 *     region (nested/prose) — that is the character immediately BEFORE the
 *     token starts, since buildQuoteMask always marks a quote's own opening
 *     character as "inside" the region it just opened (so checking the
 *     opening quote's own position would always read true and tell us
 *     nothing). */
function isProseGitToken(text, quoteMask, m) {
  const token = m[1];
  const tokenStart = m.index + (m[0].length - token.length);
  const isQuotedPathToken = token[0] === '"' || token[0] === "'";
  return isQuotedPathToken
    ? tokenStart > 0 && quoteMask[tokenStart - 1]
    : quoteMask[tokenStart + token.toLowerCase().lastIndexOf('git')];
}

/** Scans forward from `start` for the first UNQUOTED shell boundary
 * character (`;`, newline, `|`, `&`, `)`), per `mask`. Returns the substring
 * from `start` up to (not including) that boundary — the rest of the git
 * invocation's own command segment. */
function unquotedTail(text, mask, start) {
  for (let i = start; i < text.length; i++) {
    if (mask[i]) continue;
    const c = text[i];
    if (c === ';' || c === '\n' || c === '|' || c === '&' || c === ')') {
      return text.slice(start, i);
    }
  }
  return text.slice(start);
}

/** BUG-232: finds EVERY git invocation (any verb, any spelling GIT_TOKEN_RE
 * recognises, including inside recognised wrapper bodies) in `cmd` — the
 * single scanner this file's findCommitInvocation() and fail-closed
 * consumers (claude-destructive-guard.js's unrecognised-verb sweep, its
 * `git add` pathspec union) are all built on, so there is no second
 * hand-maintained token regex anywhere for this one to drift from (ASM-360's
 * proven same-day-drift class, closed by construction).
 *
 * Returns an array of entries, in gatherScanTexts()/left-to-right order:
 *   parsed:true  — { text, parsed, verbWord, verbStart, verbEnd, suffixStart,
 *                    overrides, resolved, shellEscapeAlias, tail }
 *                  `resolved` is resolveAlias()'s legacy leading-word answer;
 *                  `shellEscapeAlias` is true when ANY alias target in the
 *                  resolution chain was a shell-escape (`!...`) body, whose
 *                  leading word proves nothing about what actually runs —
 *                  fail-closed consumers must treat it as unclassifiable.
 *                  `tail` is the invocation's own argument segment (from
 *                  after the verb to the next unquoted `;`/newline/`|`/`&`/
 *                  `)`), ready for tokenize().
 *   parsed:false — { text, parsed, afterToken, tail } — a REAL git token was
 *                  found but no subcommand word could be parsed after it
 *                  (an unrecognised global option such as `--config-env=...`
 *                  or `--exec-path=...`, or a verbless `git --version`).
 *                  `tail` here starts right after the git token itself.
 *                  Callers with a fail-closed posture treat this as "could
 *                  not classify", never "not git".
 * A git token that is only PROSE inside a quoted argument is excluded
 * entirely (isProseGitToken). An empty array means `cmd` contains no real
 * git invocation at all. */
function scanGitInvocations(cmd) {
  return Array.from(iterGitInvocations(cmd));
}

/** Serializes the `alias.*` half of an invocation's `-c` overrides into a
 * stable cache-key suffix — only alias overrides can change what
 * resolveAliasDetailed() answers for a given word, so two invocations whose
 * alias overrides match may share one resolution result. */
function aliasOverridesKey(overrides) {
  const keys = Object.keys(overrides || {})
    .filter((k) => k.startsWith('alias.'))
    .sort();
  return keys.map((k) => `${k}=${overrides[k]}`).join(' ');
}

/** Lazy core behind scanGitInvocations()/findCommitInvocation() — a
 * GENERATOR, deliberately (BUG-224 Destructive round-5 REJECT). The first
 * array-building version of this scan eagerly alias-resolved EVERY git token
 * in the command before findCommitInvocation() could look at the first one —
 * and resolveAliasDetailed() spawns a synchronous `git config --get
 * alias.<word>` subprocess (~15ms) for every word not already in
 * KNOWN_COMMIT_VERBS. Cost was linear in git-token count with no early exit:
 * a `git commit -m x; ` followed by 3000 `git status; ` tokens took ~43s
 * INSIDE the PreToolUse hook that fires on every Bash/PowerShell call — an
 * attacker-triggerable (or just paste-a-CI-script-triggerable) hook freeze.
 * The pre-refactor findCommitInvocation() returned from inside its own scan
 * loop, paying nothing past the first commit-verb match; yielding entries
 * one at a time restores exactly that profile for the first-match consumer,
 * while full-scan consumers (scanGitInvocations) additionally get:
 *
 *   - a PER-SCAN MEMO of alias resolutions keyed on (verbWord, alias.*
 *     overrides) — `git status` repeated N times costs ONE config subprocess,
 *     not N. Unique unknown words still cost one lookup each (a full-scan
 *     consumer genuinely has to classify every one); a fail-closed consumer
 *     that wants a hard ceiling should cap UNIQUE lookups per scan and treat
 *     overflow as ambiguous/deny — never silently stop scanning, which would
 *     be a bypass (token N+1 could be the real commit).
 *   - a FRESH regex instance per text (never the shared GIT_TOKEN_RE with its
 *     mutable lastIndex), so two interleaved lazy scans cannot corrupt each
 *     other's position state. */
function* iterGitInvocations(cmd) {
  const aliasMemo = new Map();
  const candidates = gatherScanTexts(cmd, 0);
  for (const text of candidates) {
    const re = new RegExp(GIT_TOKEN_RE.source, GIT_TOKEN_RE.flags);
    const quoteMask = buildQuoteMask(text);
    let m;
    while ((m = re.exec(text)) !== null) {
      if (isProseGitToken(text, quoteMask, m)) continue;
      const afterToken = m.index + m[0].length;
      const inv = parseGitInvocation(text, afterToken);
      if (!inv) {
        yield {
          text,
          parsed: false,
          afterToken,
          tail: unquotedTail(text, quoteMask, afterToken),
        };
        continue;
      }
      const memoKey = `${inv.verbWord} ${aliasOverridesKey(inv.overrides)}`;
      let detail = aliasMemo.get(memoKey);
      if (!detail) {
        detail = resolveAliasDetailed(inv.verbWord, 0, new Set(), inv.overrides);
        aliasMemo.set(memoKey, detail);
      }
      yield {
        text,
        parsed: true,
        verbWord: inv.verbWord,
        verbStart: inv.verbStart,
        verbEnd: inv.verbEnd,
        suffixStart: inv.verbEnd,
        overrides: inv.overrides,
        resolved: detail.verb,
        shellEscapeAlias: detail.shellEscape,
        tail: unquotedTail(text, quoteMask, inv.verbEnd),
      };
    }
  }
}

// ---------------------------------------------------------------------------
// Env-var override extraction (prefix) — unchanged approach from v1
// ---------------------------------------------------------------------------

function extractEnvOverrides(prefix) {
  const out = {};
  for (const name of ENV_VAR_NAMES) {
    const posix = new RegExp(`\\b${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|(\\S+))`, 'g');
    const pwsh = new RegExp(`\\$env:${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|(\\S+))`, 'gi');
    let last = null;
    for (const re of [posix, pwsh]) {
      let m;
      re.lastIndex = 0;
      while ((m = re.exec(prefix)) !== null) {
        last = m[1] ?? m[2] ?? m[3] ?? '';
      }
    }
    if (last !== null) out[name] = last;
  }
  return out;
}

// ---------------------------------------------------------------------------
// Step 7: quote-aware tokenizer for the suffix (BUG-050)
// ---------------------------------------------------------------------------

/** Splits `suffix` into shell-like argument tokens, respecting single and
 * double quotes (unescaped whitespace is the separator; quote characters
 * themselves are stripped from the returned token) and — BUG-082 — treating
 * a heredoc BODY as opaque, exactly as buildQuoteMask() already does for the
 * GIT_TOKEN_RE scan. Good enough for this guard's one job — telling "this
 * word is the --author flag" from "this word is inside the -m message
 * string, or inside a piped-in -F - heredoc message body" — without being a
 * full shell grammar (see header: NOT a general shell parser).
 *
 * BUG-082: when a `<<[-]word` heredoc header is found (via
 * matchHeredocHeader(), the same recogniser buildQuoteMask() uses), the
 * header text itself is kept as ordinary characters (it was already being
 * scanned as such before this fix, and it never matches `--author`), but
 * everything from there through the body's terminator line
 * (findHeredocBodyEnd()) is skipped entirely — not accumulated into `cur`,
 * not split into tokens. A real shell never interprets heredoc BODY content
 * as command-line flag syntax; a piped-in commit message (`-F -`) mentioning
 * "--author=<...>" as prose is exactly that body content, so it must never
 * reach extractAuthorFlag()/hasFlag() as if it were a real token. Tokenizing
 * resumes normally right after the terminator line, so anything genuinely
 * outside the heredoc (a real flag positioned elsewhere on the same command
 * line) is still tokenized and still checked — this is a heredoc-body
 * exemption, not a blanket "ignore everything after a heredoc starts". */
/** BUG-165: shared scanning core for tokenize(), extracted so the "tokenize
 * a header line's own trailing remainder" step (BUG-163) no longer works by
 * having tokenize() call ITSELF recursively.
 *
 * `allowBodySkip` selects which of the two heredoc semantics is legal at
 * this scan's position:
 *   - true  (fresh scan, or resuming after a real body was skipped): a
 *     matched `<<word` genuinely owns a heredoc BODY, found for real via
 *     findHeredocBodyEnd() against `text`'s actual newlines, and the loop
 *     jumps past it — exactly the original BUG-082/BUG-163 behaviour.
 *   - false (scanning an already-isolated header-line remainder): the
 *     remainder passed in is, by construction (see the `allowBodySkip: true`
 *     branch below — it is sliced up to the position of `text`'s next real
 *     "\n", never past it), newline-free. findHeredocBodyEnd() can never
 *     find a real body to skip inside newline-free text (its own
 *     `text.indexOf('\n', pos)` guard degenerates to "no body, swallow
 *     nothing"), so a `<<word` found in THIS mode is provably always just
 *     literal characters — it is appended to `cur` atomically (so internal
 *     whitespace like `<<  EOF` still lands in one token, matching the old
 *     behaviour) and scanning simply continues in the SAME loop. This mode
 *     therefore never needs to look for a body and never recurses further.
 *
 * Because mode `false` never calls back into mode `true`, and mode `true`
 * calls mode `false` at most once per header line (for that line's own
 * remainder) before continuing its own loop, total call depth is capped at
 * 2 regardless of how many `<<word`-shaped markers appear on one header
 * line — BUG-165's exact fixture (~7000+ markers) previously recursed
 * ~7000+ deep here and blew the native call stack (RangeError: Maximum call
 * stack size exceeded); now it is a single flat pass over the remainder. */
function scanTokens(text, allowBodySkip) {
  const tokens = [];
  let cur = '';
  let quote = null;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quote) {
      if (c === quote) {
        quote = null;
      } else {
        cur += c;
      }
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      continue;
    }
    if (c === '<' && text[i + 1] === '<') {
      const header = matchHeredocHeader(text, i);
      if (header) {
        cur += text.slice(i, header.afterHeader);

        if (!allowBodySkip) {
          // Literal-only mode (see header comment): no body to skip, just
          // resume scanning right after the matched header span.
          i = header.afterHeader - 1; // loop's i++ lands exactly after it
          continue;
        }

        const end = findHeredocBodyEnd(
          text,
          header.afterHeader,
          header.word,
          header.stripLeadingTabs
        );
        // BUG-163: bash allows trailing command-line arguments AFTER the
        // heredoc delimiter word but still on its OWN header line — the
        // heredoc BODY always starts on the next physical line regardless
        // (`cmd <<EOF --author="Fake <fake@evil.com>"` really does put
        // `--author=...` in argv). findHeredocBodyEnd()'s own internal
        // `text.indexOf('\n', pos)` (re-derived here, not re-invented — same
        // "first newline after the header" fact it already computes for
        // itself) marks where the opaque body actually begins; everything
        // from `header.afterHeader` up to that newline is still the
        // header's own line and must be tokenized as ordinary text, exactly
        // like any other argument on the command line, BEFORE jumping past
        // the body. Previously this span was silently dropped (never
        // appended to `cur`, never tokenized), which is what let a forged
        // --author flag placed here go completely undetected.
        const firstNewline = text.indexOf('\n', header.afterHeader);
        const headerLineEnd = firstNewline === -1 ? text.length : firstNewline;
        const remainder = text.slice(header.afterHeader, headerLineEnd);
        if (remainder !== '') {
          // BUG-165: was `tokenize(remainder)` (a genuine recursive call
          // into this same function once per marker on the header line).
          // Now a single non-recursive scan in literal-only mode — see the
          // header comment for why this is provably equivalent.
          const remainderTokens = scanTokens(remainder, false).tokens;
          if (remainderTokens.length) {
            if (/^\s/.test(remainder) || cur === '') {
              // Remainder starts with whitespace (the normal case — a space
              // between the delimiter word and the first real argument), or
              // `cur` is already empty: the remainder's tokens are all
              // independent of whatever `cur` holds so far.
              if (cur !== '') {
                tokens.push(cur);
                cur = '';
              }
              // BUG-169: was `tokens.push(...remainderTokens)` — spreading the
              // remainder's tokens as individual call arguments has its own
              // unbounded-JS ceiling independent of recursion depth (V8 caps
              // arguments per call at ~125,000-135,000 on this build), so a
              // header line with enough markers still threw the same
              // RangeError BUG-165 was filed to eliminate. `tokens` is
              // mutated in place and returned by reference elsewhere in this
              // function, so a plain loop (not reassignment/concat) is the
              // correct fix here.
              for (const t of remainderTokens) tokens.push(t);
            } else {
              // No whitespace between the delimiter word and the remainder
              // (e.g. a quoted delimiter running straight into more text) —
              // the remainder's first token is a continuation of `cur`,
              // same as ordinary character-by-character accumulation would
              // produce.
              cur += remainderTokens[0];
              for (let ri = 1; ri < remainderTokens.length; ri++) {
                tokens.push(remainderTokens[ri]);
              }
            }
          }
        }
        i = end - 1; // loop's i++ lands exactly at `end`
        continue;
      }
    }
    if (/\s/.test(c)) {
      if (cur !== '') {
        tokens.push(cur);
        cur = '';
      }
      continue;
    }
    cur += c;
  }
  if (cur !== '') tokens.push(cur);
  return { tokens, cur };
}

function tokenize(suffix) {
  return scanTokens(suffix, true).tokens;
}

/** Pull `--author=<value>` / `--author <value>` out of the ARGUMENT TOKENS
 * that follow the verb — never out of the raw suffix text, and never out of
 * a token that is itself the value of `-m`/`--message` (BUG-050: that value
 * is a quoted, human-authored string, not flag syntax, even if it happens to
 * contain the substring "--author="). Returns:
 *   - null                 — no --author flag present
 *   - { raw, email }       — flag present, email extracted from "<...>"
 *   - { raw, email: null } — flag present but no "<email>" could be found
 *     (unverifiable — caller fails closed on this).
 */
function extractAuthorFlag(suffix) {
  // BUG-165: test-only escape hatch, same pattern as
  // CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR in claude-author-identity.js — makes
  // this throw synthetically so main()'s try/catch around this call site
  // (added for BUG-165) can be exercised without needing a real stack
  // overflow.
  if (process.env.CLAUDE_AUTHOR_GUARD_FORCE_TOKENIZE_ERROR === '1') {
    throw new RangeError('CLAUDE_AUTHOR_GUARD_FORCE_TOKENIZE_ERROR forced failure (test-only escape hatch)');
  }
  const tokens = tokenize(suffix);
  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    if (tok === '-m' || tok === '--message') {
      i++; // skip the message VALUE token entirely — never inspected
      continue;
    }
    if (/^--message=/.test(tok)) continue; // message inline; nothing to skip
    let raw = null;
    if (tok === '--author') {
      raw = tokens[i + 1] !== undefined ? tokens[i + 1] : null;
    } else if (/^--author=/.test(tok)) {
      raw = tok.slice('--author='.length);
    }
    if (raw !== null) {
      const emailMatch = /<([^<>]+)>/.exec(raw);
      return { raw, email: emailMatch ? emailMatch[1] : null };
    }
  }
  return null;
}

/** Whole-token match only — tokenizes `suffix` itself, so `--amend-something`
 * never matches `--amend` (kept as a string-in contract, same as v1, so
 * callers/tests don't need to pre-tokenize). */
function hasFlag(suffix, flag) {
  return tokenize(suffix).includes(flag);
}

// ---------------------------------------------------------------------------
// Main — DEMOTED (see header). Detection logic (steps 1-7 above) is
// UNCHANGED; only what happens with a positive detection changed: every
// former call site of the old blocking function is now either allow() (no
// opinion, or an internal error — AC-8, fail OPEN) or advise() (a
// non-blocking warning — AC-9).
// ---------------------------------------------------------------------------

function main() {
  if (process.env.CLAUDE_DISABLE_AUTHOR_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(readStdin() || '{}');
  } catch {
    allow(); // unparsable hook input is not this guard's call to make
  }

  const cmd = String((payload.tool_input || {}).command || '');
  const invocation = findCommitInvocation(cmd);
  if (!invocation) allow(); // no git commit-creating verb found anywhere

  const prefix = invocation.text.slice(0, invocation.prefixEnd);
  const suffix = invocation.text.slice(invocation.suffixStart);

  const isAmend = invocation.verb === 'commit' && hasFlag(suffix, '--amend');
  const hasResetAuthor = hasFlag(suffix, '--reset-author');

  const envOverrides = extractEnvOverrides(prefix);
  // BUG-165: extractAuthorFlag() (via tokenize()) previously had no
  // try/catch here, unlike identity.deriveSanctioned() a few lines below —
  // any throw from tokenizing (this bug's RangeError, or any future one)
  // crashed the hook with a raw uncaught stack trace instead of honoring
  // this guard's own documented fail-OPEN contract (AC-8). Same posture as
  // the deriveSanctioned() catch below: swallow, allow.
  let authorFlag;
  try {
    authorFlag = extractAuthorFlag(suffix);
  } catch {
    allow();
  }

  // -c user.email / -c user.name (BUG-044): the identity THIS invocation
  // would produce if nothing else overrides it — a tier below an explicit
  // env var or --author, above plain config. Never added to the sanctioned
  // set itself (see header item 4) — only checked against it.
  const cEmail = invocation.overrides['user.email'] || null;

  // AC-8: any throw from the shared sanctioned-identity module (or from
  // identity.configuredEmail() below, called via the SAME shared module
  // reference — never a locally reimplemented copy) is swallowed here,
  // silently, exit 0. This is the file's fail-OPEN posture — the inverse of
  // githooks/commit-msg, which fails closed on the identical throw.
  let sanctioned;
  try {
    sanctioned = identity.deriveSanctioned();
  } catch {
    allow();
  }

  if (sanctioned.size === 0) {
    advise(
      '⚠️ AUTHOR GUARD (advisory only, FEAT-045): could not derive ANY ' +
        'sanctioned identity (no git config user.email, no qualifying ' +
        'history, no CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES). This is a ' +
        'WARNING, not a block — set `git config user.email` if this is ' +
        'unexpected. The real control is githooks/commit-msg, which fails ' +
        'closed on this same condition at commit time.'
    );
  }

  const problems = [];

  function checkEmail(email, field) {
    if (!email) {
      problems.push(
        `${field} override was present but no "<email>" could be parsed ` +
          `from it — unverifiable.`
      );
      return;
    }
    if (!sanctioned.has(email.trim().toLowerCase())) {
      // Echoing the OFFENDING value back is fine (and expected — the
      // operator already typed it, it is not a secret); BUG-051 was about
      // enumerating the SANCTIONED set, never about the value under test.
      problems.push(
        `${field} "${email}" is not a sanctioned identity for this repo.`
      );
    }
  }

  /** Config-tier email for this invocation: -c user.email if present on this
   * command, else the process's own git config (via the shared module). */
  function effectiveConfigEmail() {
    return cEmail || identity.configuredEmail();
  }

  // --- COMMITTER: env override, else -c user.email, else config. Always
  // checked, on every known commit-creating verb (each of them derives the
  // committer fresh from config/env/-c — none of them inherit it). ---
  if (envOverrides.GIT_COMMITTER_EMAIL !== undefined) {
    checkEmail(envOverrides.GIT_COMMITTER_EMAIL, 'GIT_COMMITTER_EMAIL');
  } else {
    checkEmail(effectiveConfigEmail(), 'the committer (from git config / -c user.email)');
  }

  // --- AUTHOR: explicit override (flag or env) always checked. For `commit`
  // specifically, --amend without --reset-author and without an explicit
  // override inherits the prior (already-vetted) commit's author and is not
  // re-checked (git's own semantics — only commit has this). Every other
  // verb/case falls back to -c/config, same as an ordinary commit — see
  // header on why this is a safe simplification for cherry-pick/revert/am/
  // merge rather than a real re-verification of an inherited author. ---
  if (authorFlag !== null) {
    checkEmail(authorFlag.email, '--author');
  } else if (envOverrides.GIT_AUTHOR_EMAIL !== undefined) {
    checkEmail(envOverrides.GIT_AUTHOR_EMAIL, 'GIT_AUTHOR_EMAIL');
  } else if (!isAmend || hasResetAuthor) {
    checkEmail(effectiveConfigEmail(), 'the author (from git config / -c user.email)');
  }
  // else: amend, no override, no --reset-author — author inherited from HEAD,
  // already checked when that commit was created.

  if (problems.length) {
    // BUG-051: name the field and the count, never the sanctioned addresses
    // themselves — this warning flows into transcripts/logs, and this repo's
    // history was deliberately rewritten to keep the real operator address
    // off it. The operator can check `git config user.email` directly.
    advise(
      `⚠️ AUTHOR GUARD (advisory only, FEAT-045) — commit identity looks ` +
        `unsanctioned for verb "git ${invocation.verb}" (${problems.length}). ` +
        `This is a WARNING, not a block — the real control is now ` +
        `githooks/commit-msg (fail-closed, at commit time):\n\n` +
        problems.map((p) => `  - ${p}`).join('\n') +
        `\n\nThis machine derives ${sanctioned.size} sanctioned ` +
        `identit${sanctioned.size === 1 ? 'y' : 'ies'} (git config user.email ` +
        `+ trunk history + CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES) — addresses ` +
        `withheld deliberately (this repo is public). Check your own ` +
        `\`git config user.email\` to see what this machine would use.`
    );
  }

  allow();
}

if (require.main === module) {
  try {
    main();
  } catch {
    // AC-8: fail OPEN on any uncaught internal error — no output, exit 0.
    allow();
  }
} else {
  module.exports = {
    KNOWN_COMMIT_VERBS,
    // AC-4 back-compat: these are the SAME functions the shared module
    // exports (not copies) — existing callers of guard.<name>() observe the
    // shared module's behaviour, including any THRESHOLDS mutation, exactly
    // as if they had required claude-author-identity.js directly.
    HISTORY_THRESHOLD: identity.THRESHOLDS.HISTORY_THRESHOLD,
    // ASM-226: HISTORY_SCAN_LIMIT is the resource CEILING; the per-run cap
    // is derived from the repo's commit count by deriveScanLimit() (also
    // re-exported below) — see claude-author-identity.js.
    HISTORY_SCAN_LIMIT: identity.THRESHOLDS.HISTORY_SCAN_LIMIT,
    THRESHOLDS: identity.THRESHOLDS,
    normalizeContinuations,
    gatherScanTexts,
    unescapeDoubleQuoted,
    buildQuoteMask,
    findCommitInvocation,
    scanGitInvocations,
    iterGitInvocations,
    parseGitInvocation,
    resolveAlias,
    resolveAliasDetailed,
    extractEnvOverrides,
    tokenize,
    extractAuthorFlag,
    hasFlag,
    configuredEmail: identity.configuredEmail,
    trunkBranch: identity.trunkBranch,
    deriveScanLimit: identity.deriveScanLimit,
    historyEmails: identity.historyEmails,
    extraIdentities: identity.extraIdentities,
    deriveSanctioned: identity.deriveSanctioned,
  };
}
