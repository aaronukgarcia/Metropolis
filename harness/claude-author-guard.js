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
  heredocBodyRange,
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
  /(?:^|[;&|(\n])\s*(?:bash|sh|zsh|dash|ksh)(?:\.exe)?\s+(?:-\S+\s+)*-c\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi,
  // PowerShell -Command "..." (accepts -Command or the shortest unambiguous
  // prefix in real pwsh, but this guard only needs to recognise the common
  // spelling — see header on the wrapper list not being exhaustive)
  /(?:^|[;&|(\n])\s*(?:powershell|pwsh)(?:\.exe)?\s+(?:-\S+\s+)*-command\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi,
  // cmd /c "..."
  /(?:^|[;&|(\n])\s*cmd(?:\.exe)?\s+\/c\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi,
  // eval "..." / eval '...' — the shell builtin that runs its string argument
  // as a command (bash/zsh/sh/dash/ksh). Same wrapper class as bash -c: the
  // quoted string is REAL command text the scanner must descend into. BUG-332
  // r6 F7 (r5 REJECT): `eval "git add internal/engine/evil.go && git commit
  // -m \"docs: tidy\""` hid both verbs inside a string no wrapper pattern
  // recognised → the inner commit was never scanned → silent ALLOW. One edit
  // here makes BOTH commit-detection (findCommitInvocation) and add-detection
  // (findGitAddInvocations) descend into eval bodies, since both funnel
  // through gatherScanTexts().
  /(?:^|[;&|(\n])\s*eval\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi,
  // iex "..." / Invoke-Expression "..." — PowerShell's eval equivalent
  // (alias + full cmdlet; same wrapper class — this guard's hook runs on the
  // PowerShell lane too).
  /(?:^|[;&|(\n])\s*(?:iex|invoke-expression)\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi,
  // BUG-332 r17 (r16 attacker F2): string-executing language runtimes in
  // code-exec mode (`python -c '…'`, `perl -e`, `php -r`, `ruby -e`,
  // `node -e`) — the code string is REAL command text the scanner must
  // descend into, same wrapper class as bash -c. Without this, a commit verb
  // inside the string is prose and invisible to recognition. The run-flag
  // cluster (`-c`/`-e`/`-r`/`-m`/`-M`/`-p`/`--eval`) is a single dash-word,
  // so `(?:-\S+\s+)*` skips any other flags first (mirrors the shell -c
  // pattern above; STRING_EXECUTOR_RUN_FLAGS is the source for the list).
  // BUG-332 r18 (r17 attacker F2): the run-flag match now accepts a SHORT-FLAG
  // CLUSTER containing c/e/r/p/m (`-ne`, `-pe`, `-MMIME::Base64` is skipped as
  // a leading flag and the `-ne`/`-e` after it is the run flag) — the r16
  // exact-word alternation let combined `perl -ne '…'` through.
  /(?:^|[;&|(\n])\s*(?:python|python2|python3|perl|php|ruby|node)(?:\.exe)?\s+(?:-\S+\s+)*(?:-[A-Za-z]*[cerpm][A-Za-z]*|--eval)\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi,
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
  // BUG-332 r7: wrapper bodies the boundary-anchored WRAPPER_PATTERNS above
  // cannot reach — `! eval "..."`, `builtin eval "..."`, `else eval "..."`,
  // `time bash -c "..."` (the r6 F10-F12 spellings; the `(?:^|[;&|(\n])`
  // anchors never matched the reserved word / prefix builtin before the
  // wrapper word). The lexer recognises the wrapper word at COMMAND position
  // and extracts the run-string it executes, recursed the same way.
  for (const body of wrapperBodiesFromWords(normalized)) {
    if (body) texts.push(...gatherScanTexts(body, depth + 1));
  }
  // BUG-332 r9 (r8 attacker F4): heredoc bodies fed to a SHELL are command
  // text the shell executes (`sudo bash <<'EOF' … git add … EOF`), but
  // buildQuoteMask masks them opaque so the lexer never splits the git words
  // inside. Extract the body of every shell-fed heredoc and recurse into it.
  for (const body of heredocBodiesFromWords(normalized)) {
    if (body) texts.push(...gatherScanTexts(body, depth + 1));
  }
  // BUG-332 r10 (r9 attacker F4b): pipe-fed shell text — `echo "git add x &&
  // git commit" | bash` has no wrapper and no heredoc, yet bash EXECUTES the
  // piped text. Extract the verbatim emitter's quoted arguments when a shell
  // is the pipe target (see pipeFedShellBodies for the honest emitter scope).
  for (const body of pipeFedShellBodies(normalized)) {
    if (body) texts.push(...gatherScanTexts(body, depth + 1));
  }
  // BUG-332 r12 (r11 attacker NEW-1): herestring bodies fed to a shell —
  // `bash <<< "git add evil.go && git commit"` — are command text the shell
  // executes from STDIN (a `<<<` redirection the lexer masks as a quoted
  // prose word). Extract every shell-fed herestring operand and recurse.
  for (const body of herestringBodiesFromWords(normalized)) {
    if (body) texts.push(...gatherScanTexts(body, depth + 1));
  }
  // De-duplicate: the regex and lexer paths can both surface the SAME body
  // (a `; eval "..."` is both boundary-anchored and command-position) and two
  // identical wrapper bodies are identical to scan. Keeping the first
  // occurrence preserves the "outer text at index 0" contract — the outer
  // text is scanned before any nested body, so a direct invocation is still
  // found first.
  return [...new Set(texts)];
}

/** Reverses the escaping a real shell applies inside a double-quoted
 * argument so a captured wrapper body reads the way the nested command
 * actually receives it, not the way it was typed at the outer shell. Real
 * bash inside double quotes treats ONLY `\"`, `\\`, `\$`, and `` \` `` as
 * escape sequences; every other `\X` is the literal two characters `\X`.
 * (BUG-332 r14, r13 attacker F2: the previous `\\(.)`→`$1` unescape
 * collapsed EVERY `\X` pair — including a `%s\n` printf format's `\n` into
 * `n` — which turned the format into `%sn`, a string evalConstantPrintf
 * rejects, hiding the payload a constant-printf emitter would have exposed.
 * Keeping `\n` literal is also what a real shell does.) Mirrors exactly what
 * WRAPPER_PATTERNS' own capture grammar allowed through: every backslash
 * that was already a `\\.` escape unit is reversed here, while a backslash
 * that arrived inside a `$()`/backtick span is preserved. */
function unescapeDoubleQuoted(s) {
  return s.replace(/\\(["\\$`])/g, '$1');
}

// ---------------------------------------------------------------------------
// BUG-332 r7: ONE real shell-word lexer (the structural replacement for the
// boundary-set regexes)
// ---------------------------------------------------------------------------
//
// r6's root cause is ASM-350 restated: three boundary-set regexes each
// enumerate a finite "a command can start here" class — CWD_CHANGE_CMD_RE in
// the destructive guard, WRAPPER_PATTERNS' anchors here, and GIT_TOKEN_RE's
// boundary — and a shell starts a command in far more ways than any boundary
// enumeration can cover: reserved words (`else`, `!`, `if`, `while`), prefix
// builtins (`builtin`, `command`, `time`, `exec`), env-prefix assignments
// (`X=1`), and quote-splitting (`c"d"` → `cd`, `g"it"` → `git`). The fix is
// ONE lexer that walks command text the way a real shell does and answers
// "which words begin a simple command, at what nesting depth, dequoted how" —
// every boundary-sensitive consumer below derives its answer from this single
// tokenization instead of its own finite list.
const MAX_SHELL_NEST = 64;

/** Words that change the working directory for subsequent commands. */
const CWD_COMMAND_WORDS = new Set(['cd', 'chdir', 'pushd', 'popd', 'set-location', 'sl']);

/** Shell reserved words that structure a compound command — the word AFTER
 * one of these is still a command, not an argument. `in` is deliberately NOT
 * here: in `case $x in cd)` the `cd)` is a case PATTERN, not a command, and
 * dropping `in` from the keyword set keeps `case` bodies from false-flagging
 * (the `for … in …; do` loop still gets its `do` boundary from the `;`). */
const SHELL_KEYWORD_WORDS = new Set([
  'if', 'then', 'else', 'elif', 'fi', 'while', 'until', 'do', 'done',
  'for', 'case', 'esac', 'function', 'select', 'time', 'coproc', '!',
]);

/** Prefix builtins that RUN what follows as a command IN THE CURRENT SHELL —
 * `command`, `builtin`, `exec` (`time` lives in SHELL_KEYWORD_WORDS). A
 * following `cd` genuinely changes the working directory (`builtin cd /tmp`),
 * and a following `bash -c`/`eval` still executes its run-string, so the NEXT
 * WORD is classified as a command. */
const SHELL_PREFIX_WORDS = new Set(['command', 'builtin', 'exec']);

/** Prefix wrappers that run what follows as a command in a SUBPROCESS —
 * `sudo`, `doas`, `nice`, `stdbuf`, `setsid`, `xargs`, `timeout`, `env`,
 * `nohup`. The command word after one is still real (`sudo git commit`,
 * `env bash -c "…"`), but it sits at the END of the prefix's ARGUMENT RUN
 * (`sudo -u root bash -c "…"`, `timeout 10 bash -c "…"`, `env -i bash -c
 * "…"`), and a `cd` there cannot change the guard's working directory (a
 * subprocess never alters its parent shell). BUG-332 r8: the r7 REJECT was
 * the r6 F13 total-bypass class returning by a trivial rename — the r7
 * prefix set omitted this whole family, so `sudo bash -c "cd internal/engine
 * && git add evil.go && git commit -m 'docs: tidy'"` was silently ALLOWED
 * (the inner run-string was never extracted, and the inner git verbs were
 * prose-masked). These words therefore get an ARGUMENT-RUN model in
 * scanShellWordsAt: every word up to the next separator is an argument of the
 * prefix (commandStart=false, inPrefixArgs=true), and wrapperBodiesFromWords
 * scans that run for shell `-c` / eval run-strings — a value-taking flag
 * (`sudo -u root`, `timeout 10`, `env -u VAR`) cannot hide the wrapper. */
const SHELL_SUBPROCESS_PREFIX_WORDS = new Set(['nohup', 'env', 'sudo', 'doas', 'nice', 'stdbuf', 'setsid', 'xargs', 'timeout', 'exec']);

/** True when `text[i]` is a shell word separator — the character ends the
 * current word (or starts a new one) rather than belonging inside it. `$(`
 * starts a substitution, so `$` followed by `(` is a separator; `{` is a
 * group separator except when `readShellWord` already consumed it as part of
 * a `${...}` expansion (checked there, before this is reached). */
function isShellWordSeparator(text, i) {
  const c = text[i];
  if (c === ';' || c === '\n' || c === '|' || c === '&' ||
      c === '(' || c === ')' || c === '`' || c === '<' || c === '>' ||
      c === '{' || c === '}') return true;
  return c === '$' && text[i + 1] === '(';
}

/** Finds the `}` closing a `${...}` parameter expansion that opened at
 * `open` (nested `{`/`}` counted). Returns -1 when unterminated. */
function findMatchingBrace(text, open) {
  let depth = 1;
  for (let k = open + 1; k < text.length; k++) {
    if (text[k] === '\\') { k++; continue; }
    if (text[k] === '{') depth++;
    else if (text[k] === '}') { depth--; if (depth === 0) return k; }
  }
  return -1;
}

/** Finds the backtick closing a `` ` `` substitution that opened at `start`
 * (backslash escapes skipped). Returns -1 when unterminated (caller treats
 * the rest as opaque — fail-safe). */
function findBacktickEnd(text, start) {
  for (let j = start + 1; j < text.length; j++) {
    if (text[j] === '\\' && j + 1 < text.length) { j++; continue; }
    if (text[j] === '`') return j;
  }
  return -1;
}

/** Reads ONE shell word starting at `i` — the shell's DEQUOTED view,
 * concatenating quote-split fragments (`c"d"` → `cd`, `g"it"` → `git`),
 * resolving backslash escapes, and keeping `$VAR`/`${...}` expansion inside
 * the word. Stops at whitespace or a shell separator (see
 * isShellWordSeparator), or end of text. Returns `{ value, end }`. */
function readShellWord(text, i, mask) {
  let value = '';
  let j = i;
  let prevChar = '';
  const len = text.length;
  while (j < len) {
    const c = text[j];
    if (mask[j]) {
      // A quoted region inside the word — consume the WHOLE region and append
      // its dequoted content. dequoteShellToken strips the quote characters
      // and resolves `\"`→`"` escapes, so `"it"` in `g"it"` becomes `it`.
      let k = j;
      while (k < len && mask[k]) k++;
      value += dequoteShellToken(text.slice(j, k));
      prevChar = text[k - 1] || prevChar;
      j = k;
      continue;
    }
    if (/\s/.test(c)) break;
    if (c === '\\' && j + 1 < len) { value += text[j + 1]; prevChar = c; j += 2; continue; }
    // `${VAR}` / `${VAR:-default}` stays INSIDE the word (`{` right after
    // `$`); a bare `{` at a word boundary is a group separator.
    if (c === '{' && prevChar === '$') {
      const close = findMatchingBrace(text, j);
      if (close === -1) { value += text.slice(j); j = len; break; }
      value += text.slice(j, close + 1);
      prevChar = '}';
      j = close + 1;
      continue;
    }
    if (isShellWordSeparator(text, j)) break;
    value += c;
    prevChar = c;
    j++;
  }
  return { value, end: j };
}

/** True when a dequoted shell word is the git executable token — `git`,
 * `git.exe`, `git.cmd`, or path-prefixed (`/usr/bin/git`, `C:\...\git.exe`) —
 * the same shapes GIT_TOKEN_RE's token group covers, but quote-split-tolerant
 * because the lexer has already concatenated `g"it"` → `git`. */
function isGitExecutableWord(value) {
  const v = value.toLowerCase();
  return v === 'git' || v === 'git.exe' || v === 'git.cmd' ||
    v.endsWith('/git') || v.endsWith('\\git') ||
    v.endsWith('/git.exe') || v.endsWith('\\git.exe') ||
    v.endsWith('/git.cmd') || v.endsWith('\\git.cmd');
}

/** A tokenizer-found git word is a REAL invocation when its start is unquoted
 * (`git`, `g"it"`), or when it is a fully-quoted word AT COMMAND POSITION
 * (`"git" commit` — a real shell runs it). A quoted word at ARGUMENT position
 * (`-m "git"`, `echo "git"`) is a message/flag value, never a command. */
function isRealGitWord(w) {
  return !w.prose || w.commandStart;
}

/** Any shell executable that can take a run-string flag — bare names, a
 * path-prefixed variant (`/bin/bash`, `C:\...\pwsh.exe`), or a `.exe`-suffixed
 * Windows spelling (`powershell.exe`, `cmd.exe`). BUG-332 r9 (r8 attacker
 * F1): the closed-list form was bypassed by naming ANY shell the list missed —
 * `sudo ash/fish/tcsh/csh/mksh/bash4 -c "…"` was silently ALLOWED because
 * `isShellExecutableWord('ash')` was false and the run-string body was never
 * extracted. The decision is now a CLASS test: any sh-family or Windows-shell
 * basename, with an optional digit version suffix (`bash4`, `sh5` — a real
 * versioned build is still a run-string shell), so no future name can extend
 * the set.
 *
 * BUG-332 r10 (r9 attacker F1): the r9 class still omitted run-string shells
 * a real box can carry — OpenBSD's `osh`, Debian's `posh`, the standalone
 * `sash`, `nu` (nushell), the restricted `rbash`, and the Plan-9/ES/Oil
 * family (`rc`, `es`, `oil`), plus `xonsh`/`elvish`. Each executes its run
 * string exactly like bash; an attacker naming one at COMMAND position (not
 * behind `sudo`) slipped past the r9 class. They are now members of the same
 * class. The class stays the gate at COMMAND position (a plain argument to a
 * non-shell — `echo bash -c "hi"` — must never be treated as a shell); what
 * changed in r10 is the SCAN inside the gate (see wrapperBodiesFromWords). */
const SHELL_EXECUTABLE_RE = /^(sh|bash|ash|dash|ksh|zsh|fish|tcsh|csh|mksh|pdksh|yash|rbash|sash|osh|posh|nu|xonsh|elvish|oil|es|rc|busybox|pwsh|powershell|cmd)(\d*)?$/;
function isShellExecutableWord(value) {
  const base = value.toLowerCase().replace(/\\/g, '/').split('/').pop();
  const stem = base.endsWith('.exe') ? base.slice(0, -4) : base;
  return SHELL_EXECUTABLE_RE.test(stem);
}

// BUG-332 r17 (r16 attacker F2): STRING-EXECUTING LANGUAGE RUNTIMES in code-exec
// mode (`python -c`, `python -m`, `perl -e`, `php -r`, `ruby -e`, `node -e`,
// `node -p`, `node --eval`). Each executes a STRING literal as code, so a
// commit-shaped payload inside it is invisible to the raw-text scan exactly
// like a `bash -c "…"` run-string — same wrapper class, different interpreter.
// They are NOT shells (no shebang semantics), which is why SHELL_EXECUTABLE_RE
// does not carry them; they live in their own class so the two are never
// conflated. GR#3: these two regexes + WRAPPER_PATTERNS are the only places a
// run-string interpreter can be named.
const STRING_EXECUTOR_RE = /^(python|python2|python3|perl|php|ruby|node)(\.exe)?$/;
const STRING_EXECUTOR_RUN_FLAGS = new Set(['-c', '-e', '-r', '-m', '-M', '-p', '--eval']);

/** True when `v` is a STRING-EXECUTOR RUN FLAG — one of the exact spellings in
 * STRING_EXECUTOR_RUN_FLAGS, OR a single-dash SHORT-FLAG CLUSTER containing
 * `c`/`e`/`r`/`p`/`m` (`-ne`, `-pe`, `-ap`, `-lpe`, `-mfoo`). Perl (and GNU
 * getopt runtimes generally) combine short flags, so `-ne` is `-n -e` and
 * EXECUTES the next argument as a string — the r16 exact-word match
 * (STRING_EXECUTOR_RUN_FLAGS.has(v)) missed combined `-ne`/`-pe` and let the
 * code-exec mode through. `-an` (autosplit + loop, no code-exec letter) stays
 * out. BUG-332 r18 (r17 attacker F2). */
function hasStringExecutorRunFlag(v) {
  if (STRING_EXECUTOR_RUN_FLAGS.has(v)) return true;
  if (/^-[A-Za-z]+$/.test(v)) return /[cerpm]/i.test(v);
  return false;
}

/** Basename of a command word (path and .exe stripped), or '' for empty. */
function commandBasename(value) {
  const base = (value || '').toLowerCase().replace(/\\/g, '/').split('/').pop();
  return base.endsWith('.exe') ? base.slice(0, -4) : base;
}

/** BUG-332 r19 (attacker F3): a DECODER-STAGE command word normalised the way
 * isGitExecutableWord normalises the git token — path prefix stripped,
 * case-folded, and a trailing Windows `.exe`/`.cmd` suffix removed — so
 * `base64.exe -d`, `/usr/bin/base64.exe --decode` and `BASE64.CMD -d` are the
 * SAME deterministic decoders their bare POSIX spellings are. Without this,
 * isDeterministicDecoderStage matched bare names only and
 * `echo QUJD | base64.exe -d | bash` evaded the invisible-commit backstop
 * entirely. */
function decoderStageBasename(value) {
  const base = String(value || '').toLowerCase().replace(/\\/g, '/').split('/').pop();
  return base.replace(/\.(?:exe|cmd)$/, '');
}

/** True when `value` is a string-executing runtime name (`python`, `php`, …). */
function isStringExecutorWord(value) {
  return STRING_EXECUTOR_RE.test(commandBasename(value));
}

/** True when `words[i]` is a string-executing runtime in CODE-EXEC mode — a
 * run flag (`-c`/`-e`/`-r`/`-m`/`-M`/`-p`/`--eval`) among its first few
 * words. `python script.py` runs a FILE (no string), so it is deliberately
 * NOT code-exec and stays out — a runtime only becomes the wrapper class when
 * it executes an inline string. */
function isStringExecutorInvocation(words, i) {
  if (!isStringExecutorWord(words[i].value)) return false;
  for (let j = i + 1; j < Math.min(words.length, i + 4); j++) {
    const v = words[j].value;
    if (!v.startsWith('-')) return false; // first non-flag word: a script/file, not a string
    if (hasStringExecutorRunFlag(v)) return true;
  }
  return false;
}

/** True when a word is a shell RUN-STRING flag — the flag that makes the NEXT
 * word command text a real shell executes, rather than an argument. Exact
 * spellings `-c` (the POSIX dash-family option), `/c` and `/k` (cmd),
 * `-command`/`--command` (powershell/pwsh), OR a single-dash short-flag
 * cluster that CONTAINS `c` (`-ci`, `-icf`, `-lci` — the r8 attacker's F2:
 * bash executes all of these as `-c`, but the r8 form required the cluster to
 * END in `c`, so `bash -ci "…"` was ALLOWED). A cluster containing `o`/`O` is
 * a VALUE-TAKING option (`-oc`, `-o`, `-On` consume an argument, not a run
 * string) and is deliberately NOT a run flag — that is
 * isShellValueTakingFlag's territory. */
function isRunFlag(value) {
  const flag = value.toLowerCase();
  if (flag === '-c' || flag === '/c' || flag === '/k' ||
      flag === '-command' || flag === '--command') return true;
  return flag.length > 2 && flag[0] === '-' && flag[1] !== '-' &&
    flag.includes('c') && !/[oO]/.test(flag);
}

/** True when a shell flag CONSUMES ITS OWN VALUE — the next word is the
 * option's argument, not the next flag (`-o noclobber`, `-O extglob`,
 * `--rcfile x`, `--init-file x`, `--emulate sh`). The r8 attacker's F3 used a
 * value-taking flag to park a non-flag word between the shell and the run flag
 * (`bash -O extglob -c "…"` — runStringBodyAt stopped at `extglob` and never
 * reached the `-c`). Any single-dash cluster containing `o`/`O` is
 * value-taking (`-oc`), matching isRunFlag's exclusion of the same clusters. */
function isShellValueTakingFlag(value) {
  const flag = value.toLowerCase();
  if (flag === '-o' || flag === '+o' || flag === '--rcfile' ||
      flag === '--init-file' || flag === '--emulate') return true;
  return flag.length > 2 && flag[0] === '-' && flag[1] !== '-' &&
    /[oO]/.test(flag);
}

/** The run-string argument of a shell-wrapper word at `i` (`bash -c "…"`,
 * `cmd /c "…"`, `powershell -Command "…"`) — the body a real shell EXECUTES
 * as command text — or null when no run flag follows. Flags between the shell
 * word and the run flag are tolerated (`bash -l -c "…"`), including a combined
 * short-flag cluster CONTAINING `c` (`bash -lc "…"` — `-c` compacted into the
 * cluster, r9's isRunFlag), and value-taking flags are SKIPPED WITH THEIR
 * VALUES (`bash -O extglob -c "…"`, `bash -o noclobber -c "…"`, `bash
 * --rcfile x -c "…"` — the r8 attacker's F3), but the scan stops at the first
 * non-flag, non-value word before a run flag (`bash foo -c "…"` runs `foo`,
 * not a run string). */
function runStringBodyAt(words, i) {
  const n = words.length;
  for (let j = i + 1; j < n; j++) {
    const f = words[j];
    if (isRunFlag(f.value)) {
      const body = words[j + 1];
      return body ? body.value : null;
    }
    if (isShellValueTakingFlag(f.value)) { j++; continue; }
    if (!/^[-/]/.test(f.value)) return null;
  }
  return null;
}

/** Returns the "run this string as a command" bodies the boundary-anchored
 * WRAPPER_PATTERNS cannot reach — a wrapper word at COMMAND position after a
 * reserved word / prefix builtin / `!` (`else eval "..."`, `! eval "..."`,
 * `builtin eval "..."` — the r6 F10-F12 spellings, none of which match the
 * `(?:^|[;&|(\n])` anchors) OR a wrapper word inside a SUBPROCESS prefix's
 * argument run (`sudo bash -c`, `env -i bash -c` — the r7 r8 finding: such a
 * word is commandStart=false yet still EXECUTES its run-string). For the eval
 * family the body is quoted-only (matching WRAPPER_PATTERNS' grammar): an
 * UNQUOTED `eval git commit` is already visible to the outer scan —
 * `git`/`commit` are separate words in `text` — so only a quote-wrapped body
 * hides its contents. For the shell -c family the run string is the SINGLE
 * argument after the run flag, quoted or quote-split (`bash -c gi"t commit"`
 * fuses two words the outer scan cannot see separately, so its body is
 * extracted regardless of prose). A plain argument to a NON-prefix command
 * (`echo bash -c "hi"`) is neither commandStart nor inPrefixArgs and stays
 * invisible. */
function wrapperBodiesFromWords(text) {
  const words = scanShellWords(text);
  const bodies = [];
  const n = words.length;
  for (let i = 0; i < n; i++) {
    const w = words[i];
    if (!w.commandStart && !w.inPrefixArgs) continue;
    const lower = w.value.toLowerCase();
    if (lower === 'eval' || lower === 'iex' || lower === 'invoke-expression') {
      const body = words[i + 1];
      if (body && body.prose) {
        bodies.push(body.value);
        bodies.push(...maybeConstantBody(text, body));
      }
      continue;
    }
    if (w.inPrefixArgs) {
      // BUG-332 r9 (r8 attacker F1/F2/F3): a word inside a SUBPROCESS prefix's
      // argument run (`sudo -u root bash -ci "…"`) is command text the prefix
      // EXECUTES, so the WHOLE RUN is scanned for run-string flags and every
      // flag's body extracted. One mechanism closes all three findings: no
      // shell-name gate (F1: `sudo ash/fish/bash4 -c "…"`), no cluster-shape
      // gate (F2: `-ci`/`-icf`/`-lci`), and value-taking flags before the run
      // flag can no longer hide it (F3: `sudo bash -O extglob -c "…"`). The
      // run is the contiguous inPrefixArgs stretch (a subprocess-prefix
      // argument run ends at the next separator, which the lexer marks by
      // clearing inPrefixArgs). `i` is the run's FIRST word: the prefix word
      // itself is inPrefixArgs=false, so the first inPrefixArgs word is where
      // the run begins and the whole run is consumed in one pass.
      let last = i;
      for (let j = i; j < n && words[j].inPrefixArgs; j++) {
        last = j;
        if (isRunFlag(words[j].value)) {
          const body = words[j + 1];
          if (body && body.inPrefixArgs && body.value) {
            bodies.push(body.value);
            bodies.push(...maybeConstantBody(text, body));
          }
        }
      }
      i = last;
      continue;
    }
    if (isShellExecutableWord(w.value)) {
      // BUG-332 r10 (r9 attacker F1): a COMMAND-position shell's run-string
      // is found by the SAME whole-run scan as the inPrefixArgs path, not
      // runStringBodyAt — which stopped at the first non-flag, non-value word,
      // so a shell dispatched through an applet dispatcher (`busybox ash -c
      // "…"` — `ash` is a non-flag word) or any subcommand word between the
      // shell and the run flag hid the body. Every word up to the next COMMAND
      // boundary is scanned for a run-string flag and each body extracted. The
      // gate here is still the shell CLASS on the command word (`echo` is not a
      // shell, so `echo bash -c "hi"` stays invisible); only the scan inside
      // the gate is un-gated, mirroring the inPrefixArgs path. Known
      // over-broad tradeoff, accepted fail-closed: `bash foo -c "git add x"`
      // (bash runs the SCRIPT foo, making `-c` a positional param, not a run
      // flag) is also extracted — the guard cannot distinguish an applet
      // subcommand from a script filename, and over-blocking a rare script-arg
      // spelling is the safe direction for a P0 bypass guard.
      for (let j = i + 1; j < n; j++) {
        const f = words[j];
        if (f.commandStart) break; // next command — this shell's run is over
        if (isRunFlag(f.value)) {
          const body = words[j + 1];
          if (body && body.value) {
            bodies.push(body.value);
            bodies.push(...maybeConstantBody(text, body));
          }
        }
      }
    }
  }
  return bodies;
}

/** BUG-332 r11 (r10 attacker MINOR-1): a wrapper run-string body that is a
 * CONSTANT command substitution (`bash -c "$(printf 'git add evil.go')"` —
 * the shell first runs printf, then executes its output as commands) carries
 * the emitted arguments as ADDITIONAL command text. The run-string's leading
 * quote (`"$(…)"`) is skipped before the substitution is recognised; a
 * non-substitution body adds nothing. This is gated to WRAPPER BODIES ONLY —
 * a constant substitution inside a plain argument (`echo "$(printf 'x')"`)
 * prints data and is never unwrapped (no false positive). */
function maybeConstantBody(text, w) {
  const extra = [];
  let p = w.start;
  while (p < text.length && /\s/.test(text[p])) p++;
  if (p < text.length && (text[p] === '"' || text[p] === "'")) p++;
  const args = constantSubstitutionFrom(text, p);
  if (args) extra.push(...args);
  return extra;
}

/** If `text` at `start` begins a command substitution (`$(…)` or `` `…` ``)
 * whose content is a CONSTANT emitter, returns the emitted arguments as
 * command text; else null. */
function constantSubstitutionFrom(text, start) {
  if (start + 1 >= text.length) return null;
  const c0 = text[start];
  let inner = null;
  if (c0 === '$' && text[start + 1] === '(') {
    let depth = 0;
    for (let p = start + 2; p < text.length; p++) {
      const c = text[p];
      if (c === '(') depth++;
      else if (c === ')') { if (depth === 0) { inner = text.slice(start + 2, p); break; } depth--; }
    }
    if (inner === null) return null;
  } else if (c0 === '`') {
    const close = text.indexOf('`', start + 1);
    if (close === -1) return null;
    inner = text.slice(start + 1, close);
  } else return null;
  return constantEmitterArgs(inner);
}

/** If `s` is a CONSTANT command substitution's inner text — exactly one
 * `echo`/`printf` with only LITERAL quoted arguments and no variables, pipes,
 * jobs, or nesting — returns the emitted arguments as the text the
 * substitution produces. `printf` is evaluated ONLY when its format is a
 * statically-knowable constant (`%s`/`%%` plus a fixed escape set — see
 * evalConstantPrintf); `%d`, width/flags, positional args, or a format that
 * repeats over extra values is a real transform the guard cannot evaluate, so
 * it stays an honest limitation (GR#23 tripwire honesty). Anything dynamic
 * returns null. */
function constantEmitterArgs(s) {
  const m = /^\s*(echo|printf)\b([\s\S]*)$/.exec(s);
  if (!m) return null;
  const name = m[1];
  const rest = m[2];
  const args = [];
  const n = rest.length;
  let i = 0;
  while (i < n) {
    const c = rest[i];
    if (/\s/.test(c)) { i++; continue; }
    if (c === '"' || c === "'") {
      const q = c;
      let j = i + 1, val = '';
      if (q === '"') {
        while (j < n && rest[j] !== '"') {
          if (rest[j] === '\\' && j + 1 < n) { val += rest[j] + rest[j + 1]; j += 2; continue; }
          val += rest[j]; j++;
        }
        if (j >= n) return null; // unterminated
        val = unescapeDoubleQuoted(val);
      } else {
        while (j < n && rest[j] !== "'") { val += rest[j]; j++; }
        if (j >= n) return null;
      }
      // a `$` or backtick inside ANY quoted arg is a variable/nested
      // substitution: printf expands it before emitting, and the output
      // re-expands in the wrapper shell — dynamic, not constant.
      if (/\$|`/.test(val)) return null;
      args.push(val);
      i = j + 1;
      continue;
    }
    if (name === 'echo') {
      const fm = /^-[eEn]+$/.exec(rest.slice(i)); // echo flags, any position
      if (fm) { i += fm[0].length; continue; }
      if (rest.startsWith('--', i)) { i += 2; continue; }
    }
    // BUG-332 r13 (r12 attacker NEW-C): an UNQUOTED integer literal is a
    // constant printf VALUE (`printf '%*s' 3 'git add evil.go'` — the
    // width/precision operand is commonly written unquoted, real commit
    // 4370c48/df28d64). A pure integer is constant and can never carry a
    // command payload, so it is safely pushed as a value; any other unquoted
    // token (an operator, a nested substitution, a bare command word) stays
    // dynamic. The integer must be a WHOLE token: the char after it (if any)
    // must be whitespace/end — a glued `3"git"`/`3foo` is a single unquoted
    // word, not an integer value.
    const numTok = /^-?[0-9]+/.exec(rest.slice(i));
    if (numTok) {
      const after = rest[i + numTok[0].length];
      if (after === undefined || /\s/.test(after)) {
        args.push(numTok[0]);
        i += numTok[0].length;
        continue;
      }
    }
    // any other unquoted token — a real shell operator (`|;&<>\n`), a nested
    // substitution, or a format string — makes the emitter dynamic.
    return null;
  }
  if (name === 'printf') {
    // BUG-332 r12 (r11 attacker NEW-4): a CONSTANT format string is a
    // statically-evaluable transform — `printf '%s\n' 'git add evil.go'`
    // emits `git add evil.go\n`, which the wrapper shell then executes. Only
    // `%s`/`%%` and a fixed escape set are evaluated; `%d`, width/flags,
    // positional args, an unknown escape, or a format needing MORE values than
    // given stays an honest declared boundary (a transform the guard cannot
    // evaluate, GR#23 tripwire honesty).
    const emitted = evalConstantPrintf(args[0], args.slice(1));
    if (emitted === null) return null;
    return [emitted];
  }
  return args.length ? args : null;
}

/** Statically evaluates `printf` whose format `format` and value `values` are
 * all CONSTANT (no `$`/backtick — constantEmitterArgs' quoted-argument scan
 * guarantees this). Supported: the `%s` and `%b` conversions with flags,
 * width (literal or `*`), and precision (literal or `*`); `%%`; and the
 * escapes `\n` `\t` `\r` `\\` `\"` `\'`. Anything else — `%d`, positional
 * `%1$s`, an unknown escape, or extra values the format does not consume
 * (real printf repeats the format for them, which IS statically knowable but
 * deliberately kept as a declared boundary) — returns null. Returns the exact
 * string printf would emit, or null when out of the evaluated subset.
 *
 * BUG-332 r13 (r12 attacker NEW-C): width is a MINIMUM, so a payload longer
 * than its width is emitted VERBATIM — `printf '%10s'`, `printf '%*s' 3`,
 * `printf '%-s'` (real commits 4370c48/df28d64) all preserve the payload and
 * are therefore same-output conversions, not boundaries. Precision
 * TRUNCATES, so a `%.5s` of a long payload is genuinely shortened (honest);
 * a negative `*` width means left-justify to its absolute value and a
 * negative `*` precision means no truncation (both are what real printf
 * does). */
function evalConstantPrintf(format, values) {
  let out = '';
  let vi = 0;
  const consumeValue = () => {
    if (vi >= values.length) return null;
    return values[vi++];
  };
  for (let i = 0; i < format.length; i++) {
    const c = format[i];
    if (c === '\\') {
      const e = format[i + 1];
      if (e === undefined) return null;
      if (e === 'n') { out += '\n'; i++; continue; }
      if (e === 't') { out += '\t'; i++; continue; }
      if (e === 'r') { out += '\r'; i++; continue; }
      if (e === '\\') { out += '\\'; i++; continue; }
      if (e === '"') { out += '"'; i++; continue; }
      if (e === "'") { out += "'"; i++; continue; }
      return null; // unknown escape — declared boundary
    }
    if (c === '%') {
      const d = format[i + 1];
      if (d === undefined) return null;
      if (d === '%') { out += '%'; i++; continue; }
      let p = i + 1;
      let flags = '';
      while (p < format.length && '-+# 0'.includes(format[p])) { flags += format[p]; p++; }
      let width = null; // >= 0 literal, or -1 = `*` (consume next value)
      if (format[p] === '*') { width = -1; p++; }
      else if (/[0-9]/.test(format[p] || '')) {
        let num = '';
        while (/[0-9]/.test(format[p] || '')) { num += format[p]; p++; }
        width = Number(num);
      }
      let prec = null; // >= 0 literal, or -1 = `*` (consume next value)
      if (format[p] === '.') {
        p++;
        if (format[p] === '*') { prec = -1; p++; }
        else if (/[0-9]/.test(format[p] || '')) {
          let num = '';
          while (/[0-9]/.test(format[p] || '')) { num += format[p]; p++; }
          prec = Number(num);
        } else prec = 0; // bare `.` = precision zero
      }
      const conv = format[p];
      if (conv !== 's' && conv !== 'b') return null; // %d etc — declared boundary
      if (width === -1) {
        const wv = consumeValue();
        if (wv === null || !/^-?[0-9]+$/.test(String(wv))) return null;
        if (Number(wv) < 0) { width = -Number(wv); flags += '-'; } // -W = left-justify
        else width = Number(wv);
      }
      if (prec === -1) {
        const pv = consumeValue();
        if (pv === null || !/^-?[0-9]+$/.test(String(pv))) return null;
        prec = Number(pv);
        if (prec < 0) prec = null; // negative precision = no precision
      }
      const raw = consumeValue();
      if (raw === null) return null; // %s/%b with no value — boundary
      let value = conv === 'b' ? evalBString(raw) : raw;
      if (prec !== null) value = value.slice(0, prec);
      if (width !== null) {
        const pad = width - value.length;
        if (pad > 0) value = flags.includes('-') ? value + ' '.repeat(pad) : ' '.repeat(pad) + value;
      }
      out += value;
      i = p;
      continue;
    }
    out += c;
  }
  if (vi !== values.length) return null; // extra values → format repeats
  return out;
}

/** printf `%b` escape processing on a VALUE (as opposed to `%s`, which leaves
 * them literal): `\a \b \f \n \r \t \v \\` plus octal escapes `\0nnn`/`\nnn`.
 * Unknown escapes are kept literal (bash printf leaves them untouched), which
 * matches bash's behaviour for the values constantEmitterArgs admits (no
 * `$`/backtick). A backslash-free payload — the r13 attacker's NEW-C case —
 * is returned verbatim. */
function evalBString(s) {
  let out = '';
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c !== '\\') { out += c; continue; }
    const e = s[i + 1];
    if (e === undefined) { out += '\\'; break; }
    switch (e) {
      case 'a': out += '\x07'; i++; break;
      case 'b': out += '\b'; i++; break;
      case 'f': out += '\f'; i++; break;
      case 'n': out += '\n'; i++; break;
      case 'r': out += '\r'; i++; break;
      case 't': out += '\t'; i++; break;
      case 'v': out += '\v'; i++; break;
      case '\\': out += '\\'; i++; break;
      case '"': out += '"'; i++; break;
      case "'": out += "'"; i++; break;
      case '0': case '1': case '2': case '3': case '4':
      case '5': case '6': case '7': {
        let oct = e;
        let k = i + 2;
        while (k < s.length && /[0-7]/.test(s[k]) && oct.length < 3) { oct += s[k]; k++; }
        out += String.fromCharCode(parseInt(oct, 8));
        i = k - 1;
        break;
      }
      // BUG-332 r14 (r13 attacker F3): real bash `printf %b` also decodes
      // `\xHH` (1-2 hex digits, byte value), `\uHHHH` (1-4), and `\UHHHHHHHH`
      // (1-8). The r12 %b support stopped at octal + the named escapes, so a
      // FULLY hex-encoded payload (`printf '%b' '\x67\x69\x74…'`) stayed
      // literal and the emitted command was never recognised. Bash leaves a
      // `\x` with NO hex digit literal, matching the default branch below.
      case 'x': {
        let hex = '';
        let k = i + 2;
        while (k < s.length && /[0-9a-fA-F]/.test(s[k]) && hex.length < 2) { hex += s[k]; k++; }
        if (!hex) { out += '\\x'; i++; break; }
        out += String.fromCharCode(parseInt(hex, 16));
        i = k - 1;
        break;
      }
      case 'u': {
        let hex = '';
        let k = i + 2;
        while (k < s.length && /[0-9a-fA-F]/.test(s[k]) && hex.length < 4) { hex += s[k]; k++; }
        if (!hex) { out += '\\u'; i++; break; }
        out += String.fromCharCode(parseInt(hex, 16));
        i = k - 1;
        break;
      }
      case 'U': {
        let hex = '';
        let k = i + 2;
        while (k < s.length && /[0-9a-fA-F]/.test(s[k]) && hex.length < 8) { hex += s[k]; k++; }
        if (!hex) { out += '\\U'; i++; break; }
        out += String.fromCodePoint(parseInt(hex, 16));
        i = k - 1;
        break;
      }
      default: out += '\\' + e; i++; break; // unknown escape — literal
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// BUG-332 r9 (r8 attacker F4): shell-fed heredoc bodies are command text
// ---------------------------------------------------------------------------
//
// `sudo bash <<'EOF'` feeds a heredoc body to a shell's STDIN, and a shell
// without a `-c` script executes that body as COMMANDS — so
//
//   sudo bash <<'EOF'
//   git add evil.go
//   git commit -m "docs: tidy"
//   EOF
//
// is a real `git add` + `git commit`, but buildQuoteMask masks the whole body
// opaque (BUG-078: a heredoc body is inert to quote-parsing) and the lexer
// reads the body as ONE prose word, so neither verb is detected — the same
// total-bypass class the run-string wrappers cover, opened a level deeper.
// heredocBodiesFromWords() finds every heredoc header, keeps only those whose
// feeding command is shell-like, and returns each body as its own scan text.

/** Every heredoc header in `text`, in order: { afterHeader, word,
 * stripLeadingTabs, start }. Quote state is tracked exactly as buildQuoteMask
 * does (`$'…'`, `'…'`, `"…"`; a backslash escapes the next char inside double
 * quotes and consumes it outside), so a `<<` inside a quoted string is never a
 * heredoc header. The body of each detected heredoc is skipped, because a `<<`
 * inside a body is body content, not a nested header. */
function findHeredocHeaders(text) {
  const headers = [];
  let quote = null;
  let i = 0;
  const len = text.length;
  while (i < len) {
    const c = text[i];
    if (quote) {
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < len) { i += 2; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      i++;
      continue;
    }
    if (c === '\\' && i + 1 < len) { i += 2; continue; }
    if (c === '$' && text[i + 1] === "'") { quote = 'ansic'; i += 2; continue; }
    if (c === '"' || c === "'") { quote = c; i++; continue; }
    if (c === '<' && text[i + 1] === '<') {
      const header = matchHeredocHeader(text, i);
      if (header) {
        headers.push({ ...header, start: i });
        i = findHeredocBodyEnd(text, header.afterHeader, header.word, header.stripLeadingTabs);
        continue;
      }
    }
    i++;
  }
  return headers;
}

/** The start of the command segment enclosing `pos` — just past the last
 * `;`, newline, `&`, `|` or `(` before it. A heredoc's feeding command is
 * everything since that separator (`sudo bash <<EOF`: the segment is
 * `sudo bash`), which is what heredocFeedsShell() lexes. */
function lastCommandSegmentStart(text, pos) {
  for (let i = pos - 1; i >= 0; i--) {
    if (text[i] === ';' || text[i] === '\n' || text[i] === '&' ||
        text[i] === '|' || text[i] === '(') return i + 1;
  }
  return 0;
}

/** True when the heredoc's body is EXECUTED as command text, so it must be
 * extracted as a scan text. `segment` is the pre-`<<` command; `line` is the
 * whole header line (segment plus anything right of the heredoc).
 *
 * Two independent ways a heredoc body reaches a shell's stdin:
 *
 * 1. The PRE-HEREDOC command IS a shell — a shell executable at command or
 *    inPrefixArgs position (`bash <<'EOF'`, `sudo bash <<EOF`, `xargs -I{}
 *    bash <<EOF`), OR a privilege wrapper in shell-invoking form — `sudo
 *    -s`/`sudo -i` (BUG-332 r10, r9 attacker F4: sudo starts an interactive/
 *    login shell with NO shell word on the line) and `su` / `su <user>` (su
 *    always starts a login shell). Plain `sudo cmd <<EOF` passes the heredoc
 *    to `cmd`, not a shell, so only the -s/-i forms count.
 *
 * 2. A shell RIGHT OF A PIPE on the header line — `cat <<EOF | bash`: the
 *    heredoc body flows through cat's stdout into bash's stdin, which
 *    EXECUTES it (the r9 attacker's F4 pipe variant). A shell on the line
 *    that is NOT a pipe target (`cat <<A; bash <<B`) is a SEPARATE command —
 *    heredoc A still feeds cat, so it is NOT shell-fed. A non-shell pipe
 *    target (`cat <<EOF | grep foo`) transforms the body as data, not
 *    commands, and stays opaque.
 *
 * A heredoc fed to a non-shell with no pipe (`cat <<EOF`, `git add - <<EOF`)
 * is data and stays opaque. */
function heredocFeedsShell(segment, line, pipeStart) {
  const segWords = scanShellWords(segment);
  for (let i = 0; i < segWords.length; i++) {
    const w = segWords[i];
    const v = w.value.toLowerCase();
    const atCmd = w.commandStart || w.inPrefixArgs;
    if (isShellExecutableWord(w.value) && atCmd) return true;
    if (v === 'su' && atCmd) return true; // su runs a login shell
    // sudo is a SUBPROCESS PREFIX, so its own word is neither commandStart nor
    // inPrefixArgs (both are false for the prefix itself) — `i === 0` is the
    // segment-leading sudo (`sudo -s <<EOF`); a later sudo reached only as a
    // prefix argument (`xargs sudo -s <<EOF`) is inPrefixArgs instead.
    if (v === 'sudo' && (i === 0 || atCmd) && sudoRunsShell(segWords, i)) return true;
  }
  // A pipe RIGHT OF the heredoc (`cat <<EOF | bash`) feeds the body onward to
  // a shell — pipeTargetsShell only considers pipes at/after the `<<`, so a
  // pipe in an EARLIER command on the same line (`echo x | bash && cat
  // <<EOF`) can never false-trigger: that heredoc feeds cat, not bash.
  return pipeTargetsShell(line, pipeStart);
}

/** True when a real (unquoted, non-`||`) PIPE at/after `fromPos` on the
 * heredoc's header line feeds the body to a shell. The lexer's quote-mask
 * swallows the heredoc header + body as one opaque run, so scanShellWords
 * cannot see a shell word right of the `<<`; this walks the RAW line with the
 * same quote state buildQuoteMask uses and, at each real pipe at/after the
 * heredoc, asks pipeTargetExecutesShell() whether the pipeline right of it
 * terminates in a shell that runs the piped text — the r9 attacker's `cat
 * <<EOF | bash`, the r10 C2 prefix form (`| sudo bash`), the C3 substitution
 * form (`| xargs -I{} bash -c "{}"`), and the C4 passthrough form (`| cat |
 * bash`). A shell reached only through `&&`/`;`/`||` is a NEW command with a
 * fresh stdin — it is not fed the body and stays out. */
function pipeTargetsShell(line, fromPos) {
  let quote = null;
  for (let i = fromPos; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < line.length) { i++; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      continue;
    }
    if (c === '\\' && i + 1 < line.length) { i++; continue; }
    if (c === '$' && line[i + 1] === "'") { quote = 'ansic'; i++; continue; }
    if (c === '"' || c === "'") { quote = c; continue; }
    if (c === '|') {
      if (line[i + 1] === '|') { i++; continue; } // `||` is OR, not a pipe
      // BUG-332 r15: propagate `'decoder'` — a shell reached THROUGH a known
      // deterministic decoder is shell-fed text the guard cannot see, which
      // the caller must fail closed on.
      const r = pipeTargetExecutesShell(line, i);
      if (r) return r;
    }
  }
  return false;
}

/** True when the pipeline whose right end is the real pipe at `pipePos`
 * (within `slice`) terminates in a shell that EXECUTES the pipe input — either
 * reading it from stdin (`| bash`, `| sudo bash`, `| sudo -s`) or substituting
 * it per-line into a run string (`| xargs -I<ph> bash -c "<ph>"`). Walks right
 * across passthrough filters (`cat`, `tee`, `sed ''`) and subprocess prefixes.
 * Returns `'decoder'` (a shell reached THROUGH a known deterministic decoder —
 * `| base64 -d | bash`; BUG-332 r15) and `true` (a plain shell) both as
 * truthy. A transforming stage (`grep`, `sed 's/…/…/'`, `cat <file>`) stops as
 * an honest limitation — its output is a function of data the guard cannot
 * see. `xargs` WITHOUT a `-I` placeholder is NOT shell-feeding: it turns the
 * input into ARGUMENTS (a script filename), never command text. */
function pipeTargetExecutesShell(slice, pipePos) {
  let pos = pipePos + 1;
  let crossedDecoder = false;
  for (let guard = 0; guard < 8; guard++) {
    const right = slice.slice(pos);
    const words = scanShellWords(right);
    if (!words.length) return false;
    const w0 = words[0];
    const v0 = w0.value.toLowerCase();
    if (isShellExecutableWord(w0.value) && (w0.commandStart || w0.inPrefixArgs))
      return crossedDecoder ? 'decoder' : true;
    if (v0 === 'sudo' && sudoRunsShell(words, 0)) return crossedDecoder ? 'decoder' : true; // sudo -s/-i
    // xargs is a subprocess prefix too, but its shell-feeding test is
    // DIFFERENT: only the -I placeholder form substitutes each line into a
    // command string (`xargs -I{} bash -c "{}"`); plain `xargs bash` turns the
    // input into ARGUMENTS (a script filename), so it must be checked BEFORE
    // the generic prefix-run test.
    if (v0 === 'xargs') {
      // `words` are positions-relative to `right`, so the placeholder's raw
      // continuation must be read from `right`, not the full `slice`.
      return xargsPlaceholder(right, words, 0) != null;
    }
    if (SHELL_SUBPROCESS_PREFIX_WORDS.has(v0)) {
      if (prefixRunFeedsShell(words, 0)) return crossedDecoder ? 'decoder' : true;
      return false; // prefix run with no shell — not shell-fed
    }
    const stageEnd = nextPipePos(right);
    if (isPassthroughFilter(words, 0, stageEnd)) {
      if (stageEnd < 0) return false;
      pos += stageEnd + 1;
      continue;
    }
    // BUG-332 r15 (r14 attacker): a KNOWN DETERMINISTIC DECODER stage
    // (`base64 -d`, `base64 --decode`, `xxd -r -p`, `openssl base64 -d`,
    // `b64 -d`, `base32 -d`, `uudecode`, `basenc --base64 -d`) has stdout that
    // is a PURE FUNCTION of stdin — the piped text reaches the shell DECODED,
    // so this is a real shell-feeding pipe the guard must fail closed on, not
    // a data-dependent transform it cannot see. Continue the walk across it; a
    // shell reached after it flips the return to 'decoder'.
    if (isDeterministicDecoderStage(words, 0, stageEnd, right)) {
      crossedDecoder = true;
      if (stageEnd < 0) return false; // decoder at the end — no shell after it
      pos += stageEnd + 1;
      continue;
    }
    // BUG-332 r16 (r15 attacker F4): once a DECODER has been crossed, an
    // UNRECOGNIZED stage may still feed a shell — keep walking FAIL-CLOSED
    // instead of bailing to false at the first unknown stage. The decoder's
    // output is bytes the guard cannot see; a shell reached after them must
    // deny. The `guard < 8` loop bound keeps the walk finite.
    if (crossedDecoder) {
      if (stageEnd < 0) return false;
      pos += stageEnd + 1;
      continue;
    }
    return false; // transforming emitter / non-shell — honest limitation
  }
  return false;
}

/** True when a subprocess-prefix's argument run (words after `i` while
 * inPrefixArgs) feeds a shell — either a shell executable word (`sudo bash`,
 * `env bash`) or a nested `sudo -s`/`sudo -i` (sudo reading stdin as a
 * script, `env sudo -s`). */
function prefixRunFeedsShell(words, i) {
  for (let k = i + 1; k < words.length; k++) {
    const w = words[k];
    if (w.commandStart) return false; // run ended — prefix ran a non-shell
    if (w.inPrefixArgs === false) return false;
    if (isShellExecutableWord(w.value)) return true;
    if (w.value === 'sudo' && sudoRunsShell(words, k)) return true;
  }
  return false;
}

/** Is `words[first]` (the first word of a pipeline stage ending at `endPos`) a
 * PASSTHROUGH filter — one that copies its stdin to stdout unchanged — so the
 * guard may walk FURTHER LEFT to the verbatim emitter? `cat` with NO operands
 * is a pure stdin copy (any file arg or flag reads a file or transforms);
 * `tee` always echoes stdin to stdout (its destinations are a side copy); `sed`
 * with an EMPTY program is the identity. A transforming stage (`grep`, `sort`,
 * `sed 's/…/…/'`, `cat <file>`, `cat -n`) is an honest limitation — its output
 * is a function of data the guard cannot see — and the walk stops there. */
function isPassthroughFilter(words, first, endPos) {
  const name = words[first].value.toLowerCase();
  if (name === 'cat') {
    for (let k = first + 1; k < words.length; k++) {
      if (words[k].start >= endPos) break;
      return false; // any operand/flag after cat → reads a file or transforms
    }
    return true;
  }
  if (name === 'tee') return true;
  if (name === 'sed') {
    for (let k = first + 1; k < words.length; k++) {
      if (words[k].start >= endPos) break;
      const v = words[k].value;
      if (v === '') continue; // empty program → identity
      if (v.startsWith('-n')) return false; // -n suppresses all output
      if (v.startsWith('-e')) {
        const prog = words[k + 1];
        if (prog && prog.value === '' && prog.start < endPos) return true;
        return false;
      }
      if (v.startsWith('-')) continue;
      return false; // a non-empty program transforms
    }
    return true; // no program → sed copies input unchanged
  }
  return false;
}

/** True when the stage `words[first..endPos)` is a KNOWN DATA-TEXT
 * TRANSFORMER — a filter whose stdout is a REWRITE of its stdin the guard
 * cannot attribute from the raw command text: `sed` with a non-empty program
 * (`sed 's/x/git commit/'`), `awk` with a program (`awk '{print "git
 * commit"}'`), `tr` (always a character map). A transformer feeding a shell
 * means the shell executes bytes the guard cannot see, which may BE a commit —
 * the unattributable indirection the AARON ruling denies. Pure passthrough
 * (`sed ''`, `sed -e ''`) is isPassthroughFilter's territory and stays clear;
 * encode-mode `base64` (a non-decoder, non-transformer stage) also stays clear
 * per the r15 F21i control — scoped to KNOWN transformers, not every unknown
 * stage. BUG-332 r18 (r17 attacker F3). */
function isKnownTextTransformer(words, first, endPos) {
  const name = words[first].value.toLowerCase();
  if (name === 'sed' || name === 'awk' || name === 'tr') return true;
  return false;
}

/** True when the SHORT flag word `v` (single `-`, not `--`) contains the char
 * `ch` — GNU getopt clusters short flags (`-di` = `-d -i`), so a decode flag
 * glued into a cluster (`base64 -di`, `xxd -rp`, `gzip -dc`) is the same
 * decode. BUG-332 r16 (r15 attacker F1): the r15 exact-word match missed
 * clustered flags. */
function flagClusterHas(rest, ch) {
  return rest.some((v) => v.length > 1 && v[0] === '-' && v[1] !== '-' &&
    v.includes(ch));
}

/** Advance `first` past leading `NAME=value` environment-assignment words —
 * `X=1 base64 -d` runs `base64` with X in its env, so the assignment is a
 * prefix wrapper, not the command. BUG-332 r16 (r15 attacker F3). */
function skipEnvAssignmentWords(words, first, endPos) {
  while (first < words.length && words[first].start < endPos &&
         /^[A-Za-z_][A-Za-z0-9_]*=/.test(words[first].value)) first++;
  return first;
}

/** Is the stage `words[first..endPos)` a KNOWN DETERMINISTIC DECODER
 * invocation — one whose stdout is a PURE, invertible function of its stdin?
 * DECODE mode only: `base64` alone ENCODES (its output is base64 text, never
 * the payload) and stays out, as does any data-dependent transform (`grep`,
 * `sed 's/…/…/'`, `cat <file>`, `openssl enc -d -aes-…` keyed ciphers) whose
 * output is a function of data the guard cannot see. A decoder feeding a
 * shell means the shell executes bytes derived from the piped input — the
 * guard cannot see them, so callers FAIL CLOSED (BUG-332 r15). BUG-332 r16
 * (r15 attacker) widens the class: clustered decode flags (F1 `-di`),
 * openssl's `-a` base64 short form (F2), env-prefix wrappers (F3), and
 * DECOMPRESSORS whose stdout is a pure function of stdin (F4 `gzip -d`,
 * `xz -d`, …). */
function isDeterministicDecoderStage(words, first, endPos, text) {
  first = skipEnvAssignmentWords(words, first, endPos);
  if (first >= words.length || words[first].start >= endPos) return false;
  // BUG-332 r19 (attacker F3): decoderStageBasename — the raw word used to be
  // compared verbatim (`base64.exe` ≠ `base64`), letting the Windows
  // executable suffix evade every known-decoder list below.
  const name = decoderStageBasename(words[first].value);
  const rest = [];
  for (let k = first + 1; k < words.length; k++) {
    if (words[k].start >= endPos) break;
    rest.push(words[k].value);
  }
  const has = (f) => rest.indexOf(f) !== -1;
  const clusterD = flagClusterHas(rest, 'd');
  if (name === 'base64' || name === 'b64' || name === 'base32') {
    return has('--decode') || clusterD || flagClusterHas(rest, 'D');
  }
  if (name === 'xxd') return has('--revert') || flagClusterHas(rest, 'r');
  if (name === 'openssl') {
    const sub = rest[0];
    if (sub === 'base64') return clusterD;
    if (sub === 'enc') {
      // `-a`/`-base64` marks base64 mode, `-d` marks decode; BOTH required
      // (`enc -d -aes-256-cbc` is a keyed cipher — the `-aes` word is a
      // cipher name, not the base64 `-a` flag, so `-a` is matched EXACTLY).
      const base64Mark = has('-base64') || has('-a');
      return base64Mark && clusterD;
    }
    return false;
  }
  if (name === 'uudecode') return true; // uudecode always decodes
  if (name === 'basenc') {
    const base = rest.some((v) => v === '--base64' || v === '--base32' ||
      v === '--base16' || v === '--base64url' || v === '--base2' ||
      v === '--base64hex' || v === '--base32hex');
    return base && (has('--decode') || clusterD);
  }
  // BUG-332 r16 F4: decompressors. A FILE operand means it reads a file —
  // honest limitation, data the guard cannot see — so only the stdin→stdout
  // forms count. The `un*`/`*cat` aliases are decode-only spellings; the base
  // tools need a decode flag (`-d`/`--decompress`/`--uncompress`, clustered).
  const DECOMPRESS_ALIAS = new Set(['gunzip', 'zcat', 'unxz', 'xzcat',
    'unzstd', 'zstdcat', 'bunzip2', 'bzcat', 'unlz4', 'lz4cat', 'unlzma',
    'lzcat', 'uncompress']);
  const DECOMPRESS_FLAG = new Set(['gzip', 'xz', 'zstd', 'bzip2', 'lz4',
    'lzma', 'compress']);
  const dec = DECOMPRESS_ALIAS.has(name) ||
    (DECOMPRESS_FLAG.has(name) &&
     (has('--decompress') || has('--uncompress') || clusterD));
  if (dec) {
    // BUG-332 r16 (r15 attacker F4, honest-limitation nuance): a FILE operand
    // (`gzip -d file.gz`) means the decompressor writes to a FILE, never to the
    // pipe — an honest limitation. A REDIRECTION operand (`< file`, `<<< data`)
    // is not a file argument: stdin is redirected but stdout STILL carries the
    // decoded bytes to the pipe, so it is not exempt.
    let fileOperand = false;
    for (let k = first + 1; k < words.length; k++) {
      if (words[k].start >= endPos) break;
      const v = words[k].value;
      if (!v || v.startsWith('-')) continue;
      if (text && isRedirectOperand(text, words[k])) continue;
      fileOperand = true;
    }
    return !fileOperand;
  }
  // BUG-332 r17 (r16 attacker F2a): Windows' certutil base64 decoder —
  // `echo '<b64>' | certutil -decode -f - | bash`. Deterministic stdin→stdout
  // decoder, the same class as base64 -d.
  if (name === 'certutil') {
    return has('-decode') || has('-decodehex');
  }
  // BUG-332 r17 (r16 attacker F2b): a STRING-EXECUTING RUNTIME in code-exec
  // mode as a pipeline stage (`echo '<b64>' | python -c 'print(...decode(
  // sys.stdin.read()))' | bash`, `... | perl -MIME::Base64 -e '...' | bash`).
  // Its stdout may be decoded bytes the shell executes; the guard cannot see
  // them, so a runtime stage in code-exec mode feeding a shell is never
  // legitimate in a commit context (fail-closed, same posture as the
  // decompressors). `python script.py | bash` runs a FILE — not code-exec,
  // no run flag — and stays OUT.
  if (isStringExecutorWord(name)) {
    return rest.some((v) => hasStringExecutorRunFlag(v));
  }
  return false;
}

/** True when the word at `w` is the OPERAND of a shell redirection (`<<< x`,
 * `< file`, `> out`) — the last non-space character before it is `<` or `>` —
 * so it is data fed through stdin/stdout, NOT a command argument. BUG-332 r16
 * (r15 attacker F4): keeps a decompressor's stdin-redirect form
 * (`gzip -d < file | bash` — stdout carries decoded bytes) from being
 * exempted as a "file operand". */
function isRedirectOperand(text, w) {
  let i = w.start - 1;
  while (i >= 0 && /\s/.test(text[i])) i--;
  if (i < 0) return false;
  return text[i] === '<' || text[i] === '>';
}

/** True when the leading command of a `<<<`/`<<` segment is a known
 * deterministic decoder, after unwrapping a subprocess/prefix wrapper
 * (`sudo base64 -d <<< 'x'` decodes just the same). BUG-332 r15. */
function segmentIsDecoder(segWords, segment) {
  if (!segWords.length) return false;
  let first = 0;
  if (SHELL_SUBPROCESS_PREFIX_WORDS.has(segWords[0].value.toLowerCase()) ||
      SHELL_PREFIX_WORDS.has(segWords[0].value.toLowerCase())) {
    let inner = 1;
    while (inner < segWords.length && segWords[inner].value.startsWith('-')) inner++;
    if (inner >= segWords.length) return false;
    first = inner;
  }
  return isDeterministicDecoderStage(segWords, first, segWords[segWords.length - 1].end + 1, segment);
}

/** Position of the next real pipe (`|`, not `||`) in `right`, or -1. */
function nextPipePos(right) {
  let quote = null;
  for (let i = 0; i < right.length; i++) {
    const c = right[i];
    if (quote) {
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < right.length) { i++; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      continue;
    }
    if (c === '\\' && i + 1 < right.length) { i++; continue; }
    if (c === '$' && right[i + 1] === "'") { quote = 'ansic'; i++; continue; }
    if (c === '"' || c === "'") { quote = c; continue; }
    if (c === '|') {
      if (right[i + 1] === '|') { i++; continue; }
      return i;
    }
  }
  return -1;
}

/** The `-I` PLACEHOLDER of an `xargs -I<ph> …` command — `{}` for `xargs -I{}
 * bash -c "{}"`, `%` for `xargs -I% …`. The lexer splits `-I{}` at the `{`
 * GROUP OPERATOR, so the placeholder is recovered from the RAW text: the chars
 * after the `-I` prefix inside the word (`-I%` → `%`) plus any chars after the
 * word's end up to the next whitespace/separator (`-I` + `{}` → `{}`). Null
 * when xargs has no `-I` placeholder — plain `xargs bash` makes the input
 * ARGUMENTS (a script filename), not command text. `words` positions and `text`
 * must be relative to the same string. */
function xargsPlaceholder(text, words, i) {
  for (let k = i + 1; k < words.length; k++) {
    const w = words[k];
    if (w.commandStart || w.inPrefixArgs === false) return null; // run ended
    // BUG-332 r16 (r15 attacker F5): `xargs sh -c` — a BARE `-c` with NO
    // program argument — makes xargs append each piped line AS the command
    // string (`xargs sh -c` → `sh -c <line>`, the line IS the program). A
    // FIXED program after `-c` (`xargs sh -c 'echo hi'`) makes stdin the
    // ARGUMENTS, not command text, so only the bare form is shell-feeding.
    // Returns the sentinel 'shell-c' so pipeFedShellBodies and
    // scanTextHasDecoderFedShell treat the piped text as shell-fed.
    if (isShellExecutableWord(w.value) && !w.prose) {
      const nxt = words[k + 1];
      if (nxt && isRunFlag(nxt.value)) {
        const prog = words[k + 2];
        if (!prog || prog.commandStart || prog.inPrefixArgs === false) return 'shell-c';
      }
    }
    // BUG-332 r12 (r11 attacker NEW-3): GNU xargs long forms of -I.
    // `--replace` bare defaults the placeholder to `{}`; `--replace=STR` uses
    // STR. A SPACE-separated `--replace STR` is NOT the replacement string —
    // GNU's --replace takes an OPTIONAL argument (only `=STR` binds), so `STR`
    // becomes the COMMAND's argv[0] (empirically: `xargs --replace CMD bash -c
    // "CMD"` executes `CMD`, never the piped text). Treating it as a
    // placeholder would be a false positive, so it is deliberately not read.
    if (w.value === '--replace' || w.value === '--replace=') return '{}';
    if (w.value.startsWith('--replace=')) return w.value.slice('--replace='.length);
    // BUG-332 r12 bonus (same class): GNU `-i[replstr]`, the deprecated `-I`
    // shorthand — bare `-i` defaults to `{}`, `-iSTR` uses STR. Unlike -I it
    // takes no separate argument, so only the glued form is read.
    if (w.value === '-i') return '{}';
    if (w.value.startsWith('-i') && w.value.length > 2) return w.value.slice(2);
    if (!w.value.startsWith('-I')) continue;
    let ph = w.value.slice(2);
    // `{`/`}` are GROUP OPERATORS to the lexer, so `-I{}` lexes as `-I` then a
    // `{` boundary — the placeholder continues in the RAW text after the word.
    // A separator or quote ends it; a bare `{`/`}` pair is the placeholder
    // itself, not a boundary (`xargs -I{} bash -c "{}"`). BUG-332 r12 (r11
    // attacker NEW-2): `-I {}` — the placeholder SPACE-separated. -I takes its
    // operand as the NEXT ARGUMENT (POSIX required_argument), so leading
    // whitespace after a BARE `-I` (ph is empty) is skipped before the
    // placeholder is read. A GLUED placeholder (`-I%`) is complete as lexed —
    // the whitespace after it starts the COMMAND, never a continuation.
    let p = w.end;
    if (!ph.length) while (p < text.length && /\s/.test(text[p])) p++;
    while (p < text.length) {
      const c = text[p];
      if (/\s/.test(c) || ';&|()`"\'$'.includes(c)) break;
      ph += c;
      p++;
    }
    return ph.length ? ph : null;
  }
  return null;
}

/** True when the `sudo` word at `i` will run a SHELL from stdin — `sudo -s` /
 * `sudo -i` (interactive / login shell), including a cluster containing them
 * (`-si`, `-is`). `-S` (uppercase, read password from stdin) is deliberately
 * NOT a shell form and stays out. */
function sudoRunsShell(words, i) {
  for (let k = i + 1; k < words.length; k++) {
    const w = words[k];
    if (w.commandStart) return false; // next command — this sudo ran with no -s/-i
    const flag = w.value;
    if (flag.length > 1 && flag[0] === '-' && flag[1] !== '-' &&
        /[si]/.test(flag)) return true;
  }
  return false;
}

/** True when `w` in `text` is a PIPE TARGET — the last non-space character
 * before the word is `|` (`… | bash`). Pipes are separators to the lexer (not
 * words), so the raw text between words carries the signal. BUG-332 r14 (r13
 * attacker F1): `|&` (bash's stderr-merged pipe) is also a real pipe — the
 * `&` after the `|` is part of the pipe operator, never a job-control
 * separator, so `echo "…" |& bash` feeds the shell's stdin the same way. */
function isPipeTarget(text, w) {
  for (let i = w.start - 1; i >= 0; i--) {
    const c = text[i];
    if (/\s/.test(c)) continue;
    if (c === '|') return true;
    if (c === '&' && i - 1 >= 0 && text[i - 1] === '|') return true;
    return false;
  }
  return false;
}

/** The bodies of all shell-fed heredocs in `text`, each a standalone scan
 * text (in text order). */
function heredocBodiesFromWords(text) {
  const bodies = [];
  for (const header of findHeredocHeaders(text)) {
    const segStart = lastCommandSegmentStart(text, header.start);
    const segment = text.slice(segStart, header.start);
    const nl = text.indexOf('\n', header.afterHeader);
    const line = text.slice(segStart, nl === -1 ? text.length : nl);
    // pipeTargetsShell only looks at pipes at/after the heredoc's `<<`
    // (header.start), so a pipe in an earlier command on the same line can
    // never be read as feeding the body — that heredoc feeds its own segment.
    if (!heredocFeedsShell(segment, line, header.start - segStart)) continue;
    const range = heredocBodyRange(text, header);
    if (range && range.end > range.start) bodies.push(text.slice(range.start, range.end));
  }
  return bodies;
}

// ---------------------------------------------------------------------------
// BUG-332 r12 (r11 attacker NEW-1): shell-fed herestring bodies — `bash <<<
// "git add evil.go && git commit"`
// ---------------------------------------------------------------------------
//
// A `<<<` herestring feeds its operand word to the feeding command's STDIN,
// and a shell without a run-string (`bash -c`) executes stdin as COMMANDS — so
//
//   bash <<< "git add evil.go && git commit -m 'docs: tidy'"
//
// is a real `git add` + `git commit`, but the lexer's redirection rules read
// `<<<` as `<<` + `<` and mask the quoted operand as ONE prose word, so
// neither verb is detected — the same total-bypass class as the run-string
// wrappers, the shell-fed heredoc bodies, and the pipe-fed shell text, opened
// one more level deep. herestringBodiesFromWords() finds every `<<<`, keeps
// only those whose feeding command is shell-like, and returns each dequoted
// operand as its own scan text.

/** The bodies of all shell-fed herestrings in `text`, each a standalone scan
 * text (in text order). Heredoc bodies are SKIPPED during the walk — their
 * quotes must not bleed into the outer quote state, and their own `<<<`
 * operators are real command text only when the body itself is shell-fed,
 * which gatherScanTexts catches when it recurses the extracted heredoc body. */
function herestringBodiesFromWords(text) {
  const bodies = [];
  const hdrs = findHeredocHeaders(text);
  const bodyRanges = [];
  for (const h of hdrs) {
    const r = heredocBodyRange(text, h);
    if (r && r.end > r.start) bodyRanges.push(r);
  }
  let quote = null;
  let i = 0;
  const len = text.length;
  while (i < len) {
    if (bodyRanges.length && i === bodyRanges[0].start) {
      i = bodyRanges[0].end;
      bodyRanges.shift();
      continue;
    }
    const c = text[i];
    if (quote) {
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < len) { i += 2; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      i++;
      continue;
    }
    if (c === '\\' && i + 1 < len) { i += 2; continue; }
    if (c === '$' && text[i + 1] === "'") { quote = 'ansic'; i += 2; continue; }
    if (c === '"' || c === "'") { quote = c; i++; continue; }
    if (c === '<' && text[i + 1] === '<' && text[i + 2] === '<') {
      // `[N]<<<` — a DIGIT immediately before the operator — feeds fd N, NOT
      // stdin; only the bare `<<<` feeds stdin, which is what a shell executes
      // as commands. `bash3<<<` (no space) is a command NAME `bash3`, not an
      // fd number, but that is not a shell word either, so skipping is safe.
      if (i > 0 && /[0-9]/.test(text[i - 1])) { i += 3; continue; }
      const segStart = lastCommandSegmentStart(text, i);
      const segment = text.slice(segStart, i);
      const nl = text.indexOf('\n', i);
      const line = text.slice(segStart, nl === -1 ? text.length : nl);
      if (herestringFeedsShell(segment, line, i - segStart)) {
        const body = readHerestringWord(text, i + 3);
        if (body !== null) bodies.push(body);
        // BUG-332 r13 (r12 attacker NEW-D): a CONSTANT command substitution as
        // the herestring operand — `bash <<< "$(printf '%s\n' 'git add
        // evil.go')"` (real commit 2de10ff) — is executed by the receiving
        // shell: the outer shell runs the emitter, then feeds its OUTPUT to
        // the herestring'd shell's stdin. Unwrap it exactly like
        // maybeConstantBody unwraps a wrapper run-string.
        bodies.push(...maybeConstantBody(text, { start: i + 3 }));
      }
      i += 3;
      continue;
    }
    i++;
  }
  return bodies;
}

/** True when the herestring operand at this `<<<` is EXECUTED as command text,
 * so it must be extracted as a scan text. `segment` is the pre-`<<<` command;
 * `line` is the full header line (segment plus anything right of the
 * herestring); `pipeStart` is the offset of the `<<<` within `line`.
 *
 * Two independent ways a herestring operand reaches a shell's stdin:
 *
 * 1. The PRE-HERESTRING command IS a shell — a shell executable at command or
 *    inPrefixArgs position (`bash <<< "…"`, `sudo bash <<< "…"`, `xargs -I{}
 *    bash <<< "…"`), or a privilege wrapper in shell-invoking form — `sudo
 *    -s`/`sudo -i` (BUG-332 r12, r11 attacker NEW-1: sudo starts an
 *    interactive/login shell with NO shell word on the line) and `su` / `su
 *    <user>` (su always starts a login shell).
 *
 * 2. A shell RIGHT OF A PIPE on the header line — `cat <<< "git add" | bash`
 *    (BUG-332 r13, r12 attacker NEW-A, real commit ef4ad5f): the operand
 *    flows through a PASSTHROUGH filter's stdout into the shell's stdin,
 *    which EXECUTES it. A shell on the line that is NOT a pipe target is a
 *    SEPARATE command — the operand still feeds cat, so it is NOT shell-fed.
 *    A non-shell pipe target (`cat <<< "x" | grep foo`) transforms the
 *    operand as data, not commands, and stays opaque.
 *
 * A herestring fed to a non-shell with no pipe (`cat <<< "x"`, `git add -
 * <<< "x"`) is data and stays opaque. Known over-broad tradeoff inherited
 * from the heredoc path, accepted fail-closed: a shell WITH a run-string
 * (`bash -c "echo x" <<< "git add"`) does not read stdin for commands yet is
 * still extracted — an over-block, never a bypass. */
function herestringFeedsShell(segment, line, pipeStart) {
  const segWords = scanShellWords(segment);
  for (let i = 0; i < segWords.length; i++) {
    const w = segWords[i];
    const v = w.value.toLowerCase();
    const atCmd = w.commandStart || w.inPrefixArgs;
    if (isShellExecutableWord(w.value) && atCmd) return true;
    if (v === 'su' && atCmd) return true;
    if (v === 'sudo' && (i === 0 || atCmd) && sudoRunsShell(segWords, i)) return true;
  }
  // The r12 comment claimed "there is NO pipe-right variant because a pipe
  // right of a herestring receives the shell's OUTPUT" — that is only true
  // when the pre-`<<<` command is itself a SHELL; for a passthrough filter
  // the pipe receives the OPERAND, which a shell executes. pipeTargetsShell
  // only considers pipes at/after the `<<<`, so a pipe in an EARLIER command
  // on the same line (`echo x | bash && cat <<< "git add evil.go"`) can never
  // false-trigger: that herestring feeds cat, not bash. Mirrors
  // heredocFeedsShell's pipe-right check exactly.
  // BUG-332 r15 (r14 attacker): a KNOWN DETERMINISTIC DECODER as the
  // pre-`<<<` command piped to a shell — `base64 -d <<< '<b64>' | bash`,
  // `sudo base64 -d <<< '<b64>' | bash`. The operand reaches the shell
  // DECODED (a pure function of stdin); fail closed with 'decoder' so the
  // caller denies. pipeTargetsShell may itself return 'decoder' (a decoder
  // right of the pipe — `cat <<< 'x' | base64 -d | bash`), which the segment
  // check must NOT mask: only a NON-decoder segment falls through to it.
  const pipeRight = pipeTargetsShell(line, pipeStart);
  if (pipeRight && segmentIsDecoder(segWords, segment)) return 'decoder';
  return pipeRight;
}

/** The operand word of a `<<<` herestring starting at `start` (just past the
 * operator), dequoted — the text the shell executes. A quoted operand is read
 * as its WHOLE quoted span (`<<< "git add x && git commit"` — the quotes are
 * part of the redirection, not word-splitting); a `$'…'` ANSI-C string is
 * unescaped; an unquoted operand is a single word to the next whitespace /
 * separator, honouring backslash escapes (`<<< git\ add\ x`). Returns null
 * when no operand follows or the quote is unterminated. */
function readHerestringWord(text, start) {
  let i = start;
  while (i < text.length && /\s/.test(text[i])) i++;
  if (i >= text.length) return null;
  const c = text[i];
  if (c === '"' || c === "'") {
    const q = c;
    let j = i + 1;
    let val = '';
    while (j < text.length) {
      const ch = text[j];
      if (ch === q) break;
      if (q === '"' && ch === '\\' && j + 1 < text.length) { val += ch + text[j + 1]; j += 2; continue; }
      val += ch;
      j++;
    }
    if (j >= text.length) return null; // unterminated
    return q === '"' ? unescapeDoubleQuoted(val) : val;
  }
  if (c === '$' && text[i + 1] === "'") {
    let j = i + 2;
    let val = '';
    while (j < text.length) {
      const ch = text[j];
      if (ch === "'") break;
      if (ch === '\\' && j + 1 < text.length) {
        const e = text[j + 1];
        if (e === 'n') val += '\n';
        else if (e === 't') val += '\t';
        else if (e === 'r') val += '\r';
        else if (e === '\\') val += '\\';
        else val += e;
        j += 2;
        continue;
      }
      val += ch;
      j++;
    }
    if (j >= text.length) return null;
    return val;
  }
  let val = '';
  while (i < text.length && !/\s/.test(text[i])) {
    if (';&|()`"\'$'.includes(text[i])) break;
    if (text[i] === '\\' && i + 1 < text.length) { val += text[i + 1]; i += 2; continue; }
    val += text[i];
    i++;
  }
  return val.length ? val : null;
}

// ---------------------------------------------------------------------------
// BUG-332 r10 (r9 attacker F4): pipe-fed shell text — `echo "git add x" |
// bash`
// ---------------------------------------------------------------------------
//
// `echo 'git add evil.go && git commit' | bash` has NO wrapper (`bash` is a
// pipe target, not `bash -c`) and NO heredoc, yet bash EXECUTES the echoed
// text as commands — a total bypass of both the run-string wrappers and the
// heredoc machinery. pipeFedShellBodies() finds a shell executable word that
// is a PIPE TARGET and extracts the quoted arguments of a VERBATIM emitter
// (echo/printf) in the command that feeds the pipe — echo/printf output their
// arguments unchanged, so those quoted arguments ARE the text bash executes.
// A TRANSFORMING emitter (`grep "git add" f | bash`, `sed 's/…/git add/…' |
// bash`) is deliberately not extracted: its output is a function of file
// contents the guard cannot see, and its quoted arguments are grep's/sed's
// OWN pattern, NOT what bash receives — extracting them would be a false
// positive. That is an honest, documented limitation (GR#23 tripwire
// honesty), not a fabricated one: the transformed output is statically
// unknowable.

/** The command text a pipe-fed shell executes. Four accepted shapes, all
 * walking LEFT from the shell to the VERBATIM emitter (echo/printf) whose
 * quoted arguments ARE the text the shell receives:
 *
 *   (a) a direct shell pipe target: `echo "x" | bash`
 *   (b) a subprocess-prefix pipe target whose run feeds a shell (r10 C2):
 *       `echo "x" | sudo bash`, `echo "x" | doas sh`, `echo "x" | env bash`
 *   (c) an `xargs -I<ph>` pipe target, whose placeholder is remembered (r10 C3):
 *       `echo "x" | xargs -I{} bash -c "{}"` — xargs substitutes each piped
 *       line for `{}`, so the shell executes the ECHOED text, not the literal
 *       placeholder; the emitter is extracted from the xargs pipe chain.
 *   (d) a shell whose run-string body matches a remembered placeholder — the
 *       shell half of (c): bash is an argument (commandStart false after the
 *       lexer's `{` split), so the placeholder body found there must be
 *       re-attached to its xargs emitter to yield real command text.
 *
 * A TRANSFORMING emitter (`grep "git add" f | bash`, `sed 's/…/git add/…' |
 * bash`, `echo "x" | grep foo | bash`, `cat <file> | bash`) is deliberately
 * not extracted: its output is a function of file/data contents the guard
 * cannot see, and its quoted arguments are the transforming tool's OWN
 * pattern, NOT what bash receives — extracting them would be a false
 * positive. That is an honest, documented limitation (GR#23 tripwire
 * honesty), not a fabricated one: the transformed output is statically
 * unknowable. Only pure passthrough stages (`cat`, `tee`, `sed ''` — see
 * isPassthroughFilter) are walked across, because they copy stdin to stdout
 * unchanged, so the verbatim emitter is still the source of the text.
 * `xargs` WITHOUT `-I` is NOT shell-feeding: piped lines become ARGUMENTS (a
 * script filename), not command text. */
function pipeFedShellBodies(text) {
  const bodies = [];
  const words = scanShellWords(text);
  const n = words.length;
  const xargsPlaces = []; // remembered `xargs -I<ph>` emitters, { ph, idx }
  for (let i = 0; i < n; i++) {
    const w = words[i];
    // (a) direct command-position shell that is a pipe target (`| bash`)
    if (w.commandStart && isShellExecutableWord(w.value) && isPipeTarget(text, w)) {
      pipeEmitterToShell(text, words, i, bodies);
      continue;
    }
    // (b) subprocess-prefix pipe target whose run feeds a shell (`| sudo bash`)
    if (w.value !== 'xargs' && !w.commandStart &&
        SHELL_SUBPROCESS_PREFIX_WORDS.has(w.value) &&
        isPipeTarget(text, w) &&
        (prefixRunFeedsShell(words, i) ||
         (w.value === 'sudo' && sudoRunsShell(words, i)))) {
      pipeEmitterToShell(text, words, i, bodies);
      continue;
    }
    // (c) remember an `xargs -I<ph>` pipe target for the placeholder branch
    if (w.value === 'xargs' && isPipeTarget(text, w)) {
      const ph = xargsPlaceholder(text, words, i);
      if (ph) xargsPlaces.push({ ph, idx: i });
      continue;
    }
    // (d) a shell whose run-string body EMBEDS a remembered xargs placeholder —
    //     or, for the r16 F5 class, a shell with a BARE `-c` (`xargs sh -c`)
    //     where the piped line IS the command string.
    if (isShellExecutableWord(w.value) && (w.commandStart || w.inPrefixArgs)) {
      const body = runStringBodyAfter(words, i);
      for (const x of xargsPlaces) {
        if (x.idx >= i) continue;
        if (x.ph === 'shell-c' && shellFollowedByBareC(words, i)) {
          pipeEmitterToShell(text, words, x.idx, bodies);
          break;
        }
        if (body && body.value && runStringEmbedsPlaceholder(body.value, x.ph)) {
          pipeEmitterToShell(text, words, x.idx, bodies);
          break;
        }
      }
    }
  }
  return bodies;
}

/** True when the shell word at `i` is followed by a run flag `-c` (in any
 * `-c` cluster: `-c`, `-ec`, `-exc`) with NO program argument before the run
 * ends — `xargs sh -c`'s receiving shell executes its piped line as commands.
 * BUG-332 r16 (r15 attacker F5). */
function shellFollowedByBareC(words, i) {
  for (let k = i + 1; k < words.length; k++) {
    const w = words[k];
    if (w.commandStart || w.inPrefixArgs === false) return false;
    if (isRunFlag(w.value)) {
      const prog = words[k + 1];
      return !prog || prog.commandStart || prog.inPrefixArgs === false;
    }
    if (w.value.startsWith('-')) continue; // other flags before the -c
    return false; // a non-flag operand — not a bare -c
  }
  return false;
}

/** True when the placeholder `ph` appears as a STANDALONE WORD in the
 * run-string body `body`, so xargs substitution plants the piped line at a
 * position the run-string executes. BUG-332 r13 (r12 attacker NEW-B): the r12
 * exact-equality gate (`bash -c "{}"`) missed a run string that EMBEDS the
 * placeholder — `bash -c "{} && echo harmless"` (real commit 93d5334). The
 * lexer treats `{`/`}` as GROUP OPERATORS, so the placeholder word is never a
 * lexed word; the check is RAW-TEXT word boundaries instead. Over-broad
 * tradeoff accepted fail-closed: a placeholder glued inside a WORD (`echo
 * "not{}"`) stays data and is not counted, but a placeholder at an ARGUMENT
 * position (`echo {}`) IS counted — the guard cannot tell a substitution site
 * from an argument position in general, and over-extraction is the safe
 * direction for a P0 bypass guard. */
function runStringEmbedsPlaceholder(body, ph) {
  if (!ph || typeof body !== 'string' || !body.includes(ph)) return false;
  const esc = ph.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const boundary = '[\\s|;&(){}<>"\'$`\\\\]';
  return new RegExp('(^|' + boundary + ')' + esc + '(?=' + boundary + '|$)').test(body);
}

/** The run-string body word after a shell word at `i` (`bash -c "…"` → the
 * `"…"` word), or null when the shell has no run-string flag before its next
 * command boundary. */
function runStringBodyAfter(words, i) {
  for (let k = i + 1; k < words.length; k++) {
    const w = words[k];
    if (w.commandStart) return null;
    if (isRunFlag(w.value)) {
      const body = words[k + 1];
      return body && body.value ? body : null;
    }
  }
  return null;
}

/** Walk the pipeline LEFT from the pipe target at `targetIdx` to the VERBATIM
 * emitter (echo/printf) whose quoted arguments are the text a pipe-fed shell
 * executes, pushing each as command text. Pipeline stages between the target
 * and the emitter must be pure passthrough (`| cat |`, `| tee |`, `| sed '' |`)
 * or a subprocess prefix wrapping the emitter (`sudo echo "x" | bash`); a
 * transforming stage stops the walk as an honest limitation. Returns true
 * when an emitter was found. */
function pipeEmitterToShell(text, words, targetIdx, bodies) {
  let cur = targetIdx;
  for (let guard = 0; guard < 10; guard++) {
    const pipePos = pipeBefore(text, words[cur].start);
    if (pipePos < 0) return false;
    const stageStart = lastBoundaryBefore(text, pipePos);
    let first = -1;
    for (let j = 0; j < words.length; j++) {
      if (words[j].start >= stageStart && words[j].start < pipePos) { first = j; break; }
    }
    if (first < 0) return false;
    let cmd = first;
    // BUG-332 r16 (r15 attacker F3): leading env assignments prefix-wrap the
    // command (`X=1 echo '…'`, `X=1 base64 -d`) — skip them before any stage
    // check and before the prefix unwrap, which expects a real command word.
    cmd = skipEnvAssignmentWords(words, cmd, pipePos);
    if (cmd >= words.length || words[cmd].start >= pipePos) return false;
    // a leading subprocess/prefix builtin wraps the real command (`sudo echo x`)
    if (SHELL_SUBPROCESS_PREFIX_WORDS.has(words[cmd].value.toLowerCase()) ||
        SHELL_PREFIX_WORDS.has(words[cmd].value.toLowerCase())) {
      let inner = cmd + 1;
      while (inner < words.length && words[inner].start < pipePos &&
             words[inner].value.startsWith('-')) inner++;
      if (inner >= words.length || words[inner].start >= pipePos) return false;
      // env assignments can sit inside the prefix's run too (`sudo X=1 echo x`)
      cmd = skipEnvAssignmentWords(words, inner, pipePos);
      if (cmd >= words.length || words[cmd].start >= pipePos) return false;
    }
    const name = words[cmd].value.toLowerCase();
    if (name === 'echo' || name === 'printf') {
      for (let k = cmd + 1; k < words.length; k++) {
        if (words[k].start > pipePos || words[k].commandStart) break;
        if (words[k].prose && words[k].value) {
          bodies.push(words[k].value);
          // BUG-332 r14 (r13 attacker F2): the piped text a shell executes can
          // be a CONSTANT command substitution inside the emitter's own
          // argument — `echo '$(printf "%s\n" "git add …")' | bash` prints the
          // literal substitution text, and the receiving bash runs it as a
          // script (running the printf, then executing its output). The emitter
          // argument was pushed verbatim but never UNWRAPPED, so the payload
          // stayed hidden inside the printf's masked args. Same self-gating
          // unwrap wrapper bodies and herestring operands already use — a
          // dynamic/unknown substitution adds nothing.
          bodies.push(...maybeConstantBody(text, words[k]));
        }
      }
      return true;
    }
    if (isPassthroughFilter(words, cmd, pipePos)) { cur = cmd; continue; }
    // No verbatim emitter reachable — a transforming/unknown stage (r18: a
    // known data-text transformer sed/awk/tr) means the shell executes bytes
    // this body-collection cannot see. That case is DENIED by the sibling walk
    // emitterChainCrossesDecoder (consulted via hasDecoderFedShell BEFORE this
    // collection matters), so returning false here only means "no verbatim
    // bodies to descend into", never "safe".
    return false;
  }
  return false;
}

// ---------------------------------------------------------------------------
// BUG-332 r15 (r14 attacker): deterministic decoder pipes — `echo '<b64>' |
// base64 -d | bash`
// ---------------------------------------------------------------------------
//
// The r14 attacker's fresh total-bypass class: a KNOWN DETERMINISTIC DECODER
// stage between a shell and the text that feeds it. `echo '<b64>' | base64 -d
// | bash` decodes a constant base64 string and EXECUTES it; the decoder's
// stdout is a PURE FUNCTION of its stdin, so the guard could in principle
// statically decode the constant operand and continue the walk (mirroring
// evalConstantPrintf). It instead FAILS CLOSED: a known decoder feeding a
// shell is never legitimate in a commit context, and the decoded payload may
// BE the commit (`cd internal/engine && git add evil.go && git commit -m x`).
//
// The raw command carries NO git verb (the decoded payload is the commit), so
// isCommitInvocation() ALLOWs it before any scan text is gathered — the deny
// MUST fire at RECOGNITION. claude-destructive-guard.js calls
// hasDecoderFedShell() before the commit check; this section is the detector.
//
// Decoder scope (deterministic TEXT decoders, DECODE mode only): base64
// -d/--decode/-D, b64 -d, base32 -d, xxd -r, openssl base64 -d / openssl enc
// -d -base64, uudecode, basenc --base64/32/16 -d. `base64` WITHOUT a decode
// flag ENCODES (its output is base64 text, not the payload) and stays out, as
// do data-dependent transforms (`grep`, `sed 's/…/…/'`, `cat <file>`) whose
// output the guard genuinely cannot see — those remain the documented honest
// limitation.

/** Walk the pipeline LEFT from the pipe target at `targetIdx`, mirroring
 * pipeEmitterToShell's stage walk, and return true the moment a KNOWN
 * DETERMINISTIC DECODER stage sits between the target and the pipeline's
 * source — the r15 signal. The walk and pipeEmitterToShell share every stage
 * rule (prefix wrappers unwrap, passthrough filters are crossed); if one
 * changes for a future round, change both. */
function emitterChainCrossesDecoder(text, words, targetIdx) {
  let cur = targetIdx;
  for (let guard = 0; guard < 10; guard++) {
    const pipePos = pipeBefore(text, words[cur].start);
    if (pipePos < 0) return false;
    const stageStart = lastBoundaryBefore(text, pipePos);
    let first = -1;
    for (let j = 0; j < words.length; j++) {
      if (words[j].start >= stageStart && words[j].start < pipePos) { first = j; break; }
    }
    if (first < 0) return false;
    let cmd = first;
    // BUG-332 r16 (r15 attacker F3): leading env assignments prefix-wrap the
    // command (`X=1 base64 -d`) — skip them before any stage check and before
    // the prefix unwrap, which expects a real command word.
    cmd = skipEnvAssignmentWords(words, cmd, pipePos);
    if (cmd >= words.length || words[cmd].start >= pipePos) return false;
    // a leading subprocess/prefix builtin wraps the real command (`sudo base64 -d`)
    if (SHELL_SUBPROCESS_PREFIX_WORDS.has(words[cmd].value.toLowerCase()) ||
        SHELL_PREFIX_WORDS.has(words[cmd].value.toLowerCase())) {
      let inner = cmd + 1;
      while (inner < words.length && words[inner].start < pipePos &&
             words[inner].value.startsWith('-')) inner++;
      if (inner >= words.length || words[inner].start >= pipePos) return false;
      // env assignments can sit inside the prefix's run too (`sudo X=1 base64 -d`)
      cmd = skipEnvAssignmentWords(words, inner, pipePos);
      if (cmd >= words.length || words[cmd].start >= pipePos) return false;
    }
    if (isDeterministicDecoderStage(words, cmd, pipePos, text)) return true;
    const name = words[cmd].value.toLowerCase();
    if (name === 'echo' || name === 'printf') return false; // verbatim emitter — no decoder between
    if (isPassthroughFilter(words, cmd, pipePos)) { cur = cmd; continue; }
    // BUG-332 r18 (r17 attacker F3): a KNOWN DATA-TEXT TRANSFORMER between the
    // source and the shell REWRITES the piped text — `echo 'x' | sed
    // 's/x/git commit.../' | bash` executes a commit that exists ONLY inside
    // the sed program, invisible to the raw-text scan (the r15 "honest
    // limitation" return-false was the allow hole). A transform the guard
    // cannot resolve feeding a shell is the unattributable indirection the
    // ruling denies, so a transformer is a fail-closed signal like a decoder.
    // Unknown non-transformer stages (e.g. encode-mode `base64`) stay clear.
    if (isKnownTextTransformer(words, cmd, pipePos)) return true;
    return false; // unknown stage — not a known decoder, passthrough, or transformer
  }
  return false;
}

/** True when a `<<<` herestring's operand reaches a pipe-right shell DECODED —
 * `base64 -d <<< '<b64>' | bash`, `sudo base64 -d <<< '<b64>' | bash`. The
 * pre-`<<<` command is a known deterministic decoder AND the pipe right is a
 * shell (which the operand feeds through the decoder). Mirrors
 * herestringBodiesFromWords' walk (heredoc bodies skipped, quote state
 * tracked) so a `<<<` inside a heredoc body or a quoted string is never read
 * as a herestring. */
function herestringDecoderFeedsShell(text) {
  const hdrs = findHeredocHeaders(text);
  const bodyRanges = [];
  for (const h of hdrs) {
    const r = heredocBodyRange(text, h);
    if (r && r.end > r.start) bodyRanges.push(r);
  }
  let quote = null;
  let i = 0;
  const len = text.length;
  while (i < len) {
    if (bodyRanges.length && i === bodyRanges[0].start) {
      i = bodyRanges[0].end;
      bodyRanges.shift();
      continue;
    }
    const c = text[i];
    if (quote) {
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < len) { i += 2; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      i++;
      continue;
    }
    if (c === '\\' && i + 1 < len) { i += 2; continue; }
    if (c === '$' && text[i + 1] === "'") { quote = 'ansic'; i += 2; continue; }
    if (c === '"' || c === "'") { quote = c; i++; continue; }
    if (c === '<' && text[i + 1] === '<' && text[i + 2] === '<') {
      if (i > 0 && /[0-9]/.test(text[i - 1])) { i += 3; continue; } // `[N]<<<` feeds fd N, not stdin
      const segStart = lastCommandSegmentStart(text, i);
      const segment = text.slice(segStart, i);
      const nl = text.indexOf('\n', i);
      const line = text.slice(segStart, nl === -1 ? text.length : nl);
      if (herestringFeedsShell(segment, line, i - segStart) === 'decoder') return true;
      i += 3;
      continue;
    }
    i++;
  }
  return false;
}

/** True when `text` contains a known deterministic decoder feeding a shell,
 * across all three routes:
 *   (a) pipe-fed shell: `echo '<b64>' | base64 -d | bash`, `printf '%s' '<b64>'
 *       | base64 --decode | bash`, `echo '<hex>' | xxd -r -p | bash`, `echo
 *       '<b64>' | openssl base64 -d | bash`, and the subprocess-prefix / xargs
 *       -I variants — a decoder stage between the shell target and the
 *       pipeline's source;
 *   (b) herestring-fed decoder piped to a shell: `base64 -d <<< '<b64>' |
 *       bash`;
 *   (c) heredoc-fed decoder piped to a shell: `cat <<EOF | base64 -d | bash`.
 * The decoder pipe can hide a level deep inside a wrapper run-string (`bash -c
 * "echo 'x' | base64 -d | bash"`), so the outer text AND every gathered
 * wrapper/heredoc/herestring body are scanned. */
// ---------------------------------------------------------------------------
// BUG-332 r16 (r15 attacker F6/F8): command-substitution and PowerShell
// decoder-to-shell routes
// ---------------------------------------------------------------------------
//
// F6 — `$(echo '<b64>' | base64 -d)` as EXECUTED text. A command substitution
// whose body pipeline ends in a known deterministic decoder produces DECODED
// bytes. When the substitution sits at COMMAND position — the whole program of
// a `bash -c` wrapper, or a pipeline stage (`$(echo B64 | base64 -d) | bash`)
// — a shell executes those decoded bytes, the same total bypass as the pipe
// form with the body hidden behind `$()`. Detection is limited to command
// position on purpose: `echo "$(echo B64 | base64 -d)"` prints the decoded
// text as DATA and stays out (echo's argument is never executed), which is the
// documented false-positive guard.
//
// F8 — PowerShell's base64 command surface: `powershell -EncodedCommand <b64>`
// / `-enc <b64>` decodes its operand as the command; `[Convert]::FromBase64-
// String('…')` feeds `iex`/`invoke-expression` or `& (…)` to run the decoded
// payload. Both execute bytes derived from base64 text the guard cannot see.

/** Position of the `)` closing the `$(` substitution at `open`, balanced and
 * quote-aware (a `)` inside a quote or a nested `$(…)` is not the close), or
 * -1 when unterminated. */
function findSubstitutionClose(text, open) {
  let depth = 1;
  let quote = null;
  let i = open + 2;
  while (i < text.length) {
    const c = text[i];
    if (quote) {
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < text.length) { i += 2; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      i++;
      continue;
    }
    if (c === '\\' && i + 1 < text.length) { i += 2; continue; }
    if (c === '$' && text[i + 1] === "'") { quote = 'ansic'; i += 2; continue; }
    if (c === '"' || c === "'") { quote = c; i++; continue; }
    if (c === '$' && text[i + 1] === '(') { depth++; i += 2; continue; }
    if (c === ')') { if (depth === 1) return i; depth--; i++; continue; }
    i++;
  }
  return -1;
}

/** True when the `$(` at `open` is at COMMAND position — start of text, or
 * preceded (modulo whitespace) by a shell separator `|;&({` — and NOT inside a
 * quoted region / heredoc body (`mask`). A substitution at command position is
 * EXECUTED as (part of) a command; one inside a quoted argument is data. */
function substitutionIsAtCommandPosition(text, open, mask) {
  if (mask[open]) return false;
  let i = open - 1;
  while (i >= 0 && /\s/.test(text[i])) i--;
  if (i < 0) return true; // start of text
  return '|;&({'.includes(text[i]);
}

/** True when the command-substitution body's pipeline ends in a known
 * deterministic decoder — `$(echo '<b64>' | base64 -d)` — so the substitution
 * OUTPUT is decoded bytes. The LAST stage is tested directly first (it may
 * itself be the decoder, or carry a `sudo`/env prefix); if it is a passthrough
 * (`| base64 -d | cat`), a decoder UPSTREAM of it is found by walking left via
 * emitterChainCrossesDecoder; a transforming stage (`| grep foo`) stops the
 * walk as the documented honest limitation. */
function substitutionBodyEndsInDecoder(body) {
  const words = scanShellWords(body);
  if (!words.length) return false;
  let lastStageFirst = 0;
  let hasPipe = false;
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    if (w.commandStart || w.inPrefixArgs) {
      if (i > 0 && pipeBefore(body, w.start) >= 0) hasPipe = true;
      lastStageFirst = i;
    }
  }
  if (isDeterministicDecoderStage(words, lastStageFirst, body.length + 1, body)) return true;
  if (!hasPipe) return false;
  // the last stage is not itself a decoder — one may sit UPSTREAM of it
  // through passthrough stages (`| base64 -d | cat`), which the left-walk finds.
  return emitterChainCrossesDecoder(body, words, lastStageFirst);
}

/** True when `text` holds a command-position substitution whose output is
 * decoded bytes — the F6 signal. Every gathered scan text (the outer command
 * AND each wrapper/heredoc/herestring body) is scanned by the caller, so a
 * `bash -c "$(echo '<b64>' | base64 -d)"` is caught on its extracted body
 * where the substitution sits at command position. */
function commandSubstitutionFeedsShell(text) {
  const mask = buildQuoteMask(text);
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '$' && text[i + 1] === '(' &&
        substitutionIsAtCommandPosition(text, i, mask)) {
      const close = findSubstitutionClose(text, i);
      if (close < 0) continue;
      const body = text.slice(i + 2, close);
      if (substitutionBodyEndsInDecoder(body)) return true;
    }
  }
  return false;
}

/** A long base64-looking token (≥16 base64 chars, optional `=` padding) — the
 * shape of a PowerShell `-EncodedCommand` operand / `FromBase64String`
 * argument. The length bound keeps ordinary flag operands out. */
function isBase64ishToken(v) {
  return v.length >= 16 && /^[A-Za-z0-9+/]+={0,2}$/.test(v);
}

/** True when `text` holds the PowerShell base64-to-invocation surface — the
 * F8 signal: `powershell -EncodedCommand <b64>` / `-enc <b64>`, or a
 * `[Convert]::FromBase64String(…)`/`FromBase64CharArray(…)` call that is
 * invoked (`| iex`, `| invoke-expression`, `& (…)`). */
function powerShellDecoderFeedsShell(text) {
  const words = scanShellWords(text);
  for (let i = 0; i < words.length; i++) {
    if (/^-(?:enc|encodedcommand)$/i.test(words[i].value)) {
      const b64 = words[i + 1];
      if (b64 && !b64.commandStart && isBase64ishToken(b64.value)) return true;
    }
  }
  if (/\[Convert\]::FromBase64(?:String|CharArray)\(/i.test(text)) {
    if (/\b(?:iex|invoke-expression)\b/i.test(text) || /&\s*\(/.test(text)) return true;
  }
  return false;
}

function hasDecoderFedShell(text) {
  for (const t of gatherScanTexts(text, 0)) {
    if (scanTextHasDecoderFedShell(t)) return true;
  }
  return false;
}

function scanTextHasDecoderFedShell(text) {
  const words = scanShellWords(text);
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    // (a) a shell — or a subprocess-prefix-run / xargs -I shell — that is a
    // PIPE TARGET, with a known decoder between it and the pipeline's source.
    if (w.commandStart && isShellExecutableWord(w.value) && isPipeTarget(text, w)) {
      if (emitterChainCrossesDecoder(text, words, i)) return true;
    } else if (w.value !== 'xargs' && !w.commandStart &&
        SHELL_SUBPROCESS_PREFIX_WORDS.has(w.value) && isPipeTarget(text, w) &&
        (prefixRunFeedsShell(words, i) ||
         (w.value === 'sudo' && sudoRunsShell(words, i)))) {
      if (emitterChainCrossesDecoder(text, words, i)) return true;
    } else if (w.value === 'xargs' && isPipeTarget(text, w) &&
        xargsPlaceholder(text, words, i) != null) {
      if (emitterChainCrossesDecoder(text, words, i)) return true;
    }
  }
  // (c) heredoc body through a decoder to a pipe-right shell (`cat <<EOF |
  // base64 -d | bash`) — heredocFeedsShell propagates pipeTargetsShell's
  // 'decoder' tri-state.
  for (const header of findHeredocHeaders(text)) {
    const segStart = lastCommandSegmentStart(text, header.start);
    const segment = text.slice(segStart, header.start);
    const nl = text.indexOf('\n', header.afterHeader);
    const line = text.slice(segStart, nl === -1 ? text.length : nl);
    if (heredocFeedsShell(segment, line, header.start - segStart) === 'decoder') return true;
  }
  // (b) herestring operand through a decoder to a pipe-right shell.
  if (herestringDecoderFeedsShell(text)) return true;
  // BUG-332 r16 (r15 attacker F6): a COMMAND-POSITION command substitution
  // whose body pipeline ends in a known decoder — `bash -c "$(echo '<b64>' |
  // base64 -d)"`, `$(echo '<b64>' | base64 -d) | bash` — the substitution
  // output is decoded bytes a shell executes.
  if (commandSubstitutionFeedsShell(text)) return true;
  // BUG-332 r16 (r15 attacker F8): PowerShell's base64 command surface.
  if (powerShellDecoderFeedsShell(text)) return true;
  // BUG-332 r17 (r16 attacker F2c): a string-executing runtime at COMMAND
  // position in code-exec mode whose code string REACHES A SHELL — `php -r
  // 'system(base64_decode("…"))'`, `python -c 'import os;
  // os.system("git commit …")'` — executes a commit-shaped string with NO
  // pipeline to walk and (for the base64 spelling) no git verb in the raw
  // text. The shell-reach call (system/os.system/subprocess/exec/popen/…)
  // inside the code string is the unattributable indirection the ruling
  // denies: the guard cannot see what it executes.
  if (stringExecutorFeedsShell(text)) return true;
  return false;
}

// A runtime code string REACHES A SHELL when it contains a shell-exec call —
// the r16 attacker's F2c route (`php -r 'system(base64_decode("…"))'`,
// `python -c 'import os; os.system("git commit …")'`). Any of these in an
// inline code string means the code can execute a commit-shaped string the
// guard cannot see. `print(1+1)` and other harmless one-liners carry none.
const SHELL_REACH_RE = /\b(system|popen|passthru|shell_exec|os\.system|os\.popen|subprocess\.(?:run|call|check_call|Popen|popen)|execSync|spawnSync|spawn|fork|exec)\s*\(|\bchild_process\b|`/i;

/** True when a STRING-EXECUTING RUNTIME at command position in code-exec mode
 * has a code string that reaches a shell — `php -r 'system(...)'`,
 * `python -c 'import os; os.system(...)'`, `perl -e 'system(...)'`. Same
 * wrapper-class recognition as WRAPPER_PATTERNS (the runtimes plus a run
 * flag), but instead of recursing into the body for a git verb (the commit is
 * often base64'd, so none exists) it FAILS CLOSED on the shell-reach signal:
 * a runtime that executes a string through a shell is exactly the
 * invisible-commit route the ruling bans. BUG-332 r17 (r16 attacker F2c). */
function stringExecutorFeedsShell(text) {
  const normalized = normalizeContinuations(text);
  // The run-flag alternation matches a short-flag cluster containing c/e/r/p/m
  // (`-ne`, `-pe`) or --eval — BUG-332 r18 (r17 attacker F2): the r16
  // exact-word list missed combined flags (`perl -ne 'system(...)'`).
  const re = /(?:^|[;&|(\n])\s*(?:python|python2|python3|perl|php|ruby|node)(?:\.exe)?\s+(?:-\S+\s+)*(?:-[A-Za-z]*[cerpm][A-Za-z]*|--eval)\s+(?:"((?:\$\([^()]*\)|`[^`]*`|\\.|[^"\\])*)"|'([^']*)')/gi;
  let m;
  while ((m = re.exec(normalized)) !== null) {
    const body = m[1] !== undefined ? unescapeDoubleQuoted(m[1]) : m[2];
    if (body && SHELL_REACH_RE.test(body)) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// AARON RULING 2026-08-23 (Bev on BOW BUG-332): STRUCTURAL ALLOWLIST for
// commit recognition.
//
// The r15 REJECT surfaced 8 NEW decoder-route spellings (base64 -di, openssl
// enc -a, env-prefix, gzip/xz -d, xargs sh -c, command-substitution,
// backslash-newline, PowerShell FromBase64String|iex), proving the denylist
// of bypass spellings is asymptotically incomplete — "a regex is not a shell
// parser". Per the ruling the commit-RECOGNITION layer is flipped to an
// ALLOWLIST: the guard recognises as a commit ONLY the plain benign form — a
// literal `git commit` with ZERO shell indirection anywhere in the command.
// Any commit-shaped command carrying an unattributable shell metacharacter is
// COULD-NOT-EVALUATE and DENIED fail-closed ("false-positive deny is
// recoverable; false-negative allow is the hole"). `&&` / `;` / newline
// separators are NOT indirection (BUG-224's `git add x && git commit` rhythm
// and trailing `&& git push` stay plain). hasDecoderFedShell (above) remains
// the INVISIBLE-commit backstop: a command with NO git verb in its raw text
// (`echo '<b64>' | base64 -d | bash`) cannot be commit-shaped by recognition,
// but its decoded payload could BE a commit — so it still denies at the
// decoder layer before this classifier ever runs.
// ---------------------------------------------------------------------------

// Command-position words that mean the (possibly later) commit runs under
// shell indirection rather than as the plain form: string-executing shells,
// string-executors (eval/iex), and subprocess prefixes (sudo/env/xargs/nohup/
// ...). This is the lexer's own classification (SHELL_SUBPROCESS_PREFIX_WORDS)
// plus the shell/string-executor words WRAPPER_PATTERNS descends into —
// reaching a commit through one of these is exactly the wrapper class the
// ruling bans. The bash lookups `command`/`builtin` are deliberately NOT here:
// they run git directly (bypass-alias, no indirection), and denying them would
// be a pointless false positive.
const INDIRECTION_COMMAND_WORDS = new Set([
  // Shells here are a SUBSET of SHELL_EXECUTABLE_RE for the lexer's prefix
  // walk; the indirection test at the use site ALSO tests isShellExecutableWord
  // (BUG-332 r17 / r16 attacker F4) so no shell name can be missed by this
  // hardcoded list going stale — GR#3 keeps SHELL_EXECUTABLE_RE the one list.
  'bash', 'sh', 'zsh', 'dash', 'ksh', 'ash', 'busybox',
  // Windows shells
  'powershell', 'pwsh', 'cmd',
  // string-executors
  'eval', 'iex', 'invoke-expression',
  // subprocess prefixes (SHELL_SUBPROCESS_PREFIX_WORDS:
  // nohup/env/sudo/doas/nice/stdbuf/setsid/xargs/timeout/exec)
  'nohup', 'env', 'sudo', 'doas', 'nice', 'stdbuf', 'setsid', 'xargs', 'timeout', 'exec',
]);

// An env-assignment at command position (`X=1 git commit`) — the ruling's
// env-prefix class (r15 F3). `env -i` is caught via `env`; a bare `KEY=value`
// prefix is a shell variable the commit runs under, invisible to the guard.
// BUG-332 r17 (r16 attacker F3): also the bash APPEND-assign form
// (`X+=append git commit` — the `+=` operator, missed by the strict `=`) and
// PowerShell's brace-less `$env:X=...` prefix (`${env:X}=` already trips the
// raw `${` substitution scan above). Both set a variable the commit runs
// under, invisible to the guard, so both are env-prefix indirection.
const ENV_ASSIGN_RE = /^[A-Za-z_][A-Za-z0-9_]*(\+?=)/;
const PS_ENV_ASSIGN_RE = /^\$env:[A-Za-z_][A-Za-z0-9_]*=/;

/** TRUE when `command` — already known to contain a literal commit verb —
 * carries ANY shell indirection, per the AARON structural ruling. Three scans:
 *   1. RAW substitution markers — `` ` `` / `$(` / `${` execute even inside
 *      double quotes, so no quoting can attribute them to message text.
 *   2. UNQUOTED pipes/redirections — read off buildQuoteMask, which masks
 *      quoted regions, `$(...)` spans, backtick spans and heredoc bodies
 *      (a `|`/`<`/`>` inside `-m "..."` is message text; one outside is the
 *      shell operating). Heredoc headers (`<<`) are redirection, covered here.
 *   3. COMMAND-POSITION indirection words / env-assign prefixes — at each
 *      UNQUOTED command start (start of input or after `;`/`&`/`|`/`(`/newline),
 *      the leading word. `git` (and any plain command word) is fine; a word in
 *      INDIRECTION_COMMAND_WORDS — bash -c / eval / iex / xargs / env / sudo /
 *      nohup / cmd / powershell … — or an `X=1` env-assign means the commit is
 *      reached through shell indirection. `&&` / `;` / newline separators and
 *      `( )` grouping are NOT indirection.
 */
function hasShellIndirection(command) {
  const normalized = normalizeContinuations(command);
  const mask = buildQuoteMask(normalized);
  if (/`|\$\(|\$\{/.test(normalized)) return true;
  for (let i = 0; i < normalized.length; i++) {
    if (!mask[i] && /[|<>]/.test(normalized[i])) return true;
  }
  // Heredoc/herestring headers (`<<`/`<<<`) at an UNQUOTED position:
  // buildQuoteMask masks the whole heredoc span opaque (header + body), so the
  // `<<` reads masked even though it is real shell redirection. The
  // distinguishing signal is the preceding char — an unquoted heredoc header
  // is preceded by an unmasked separator/space; a `<<` inside `-m "see <<
  // here"` is preceded by a masked char (the enclosing quote region).
  for (let i = 0; i < normalized.length - 1; i++) {
    if (normalized[i] === '<' && normalized[i + 1] === '<') {
      if (i === 0 || !mask[i - 1]) return true;
    }
  }
  // Command-position scan via the lexer's word stream. Three shapes:
  //   (a) a real command word (commandStart) that is a string-executor or a
  //       shell — `bash -c`, `eval`, `iex`, a pipe-target `bash`, and — thanks
  //       to the lexer keeping commandStart true after a reserved word — the
  //       `bash` in `time bash -c` (the r6 F10-F12 spelling).
  //   (b) an env-assign prefix at command position (`X=1 git commit`, r15 F3) —
  //       `echo X=1` (an argument) sits after a commandStart word and is safe.
  //   (c) a subprocess prefix (xargs/env/sudo/nohup/...) that actually opens a
  //       prefix-arg run (its NEXT word is inPrefixArgs) — `xargs sh -c`,
  //       `env git commit`, `sudo -u root bash -c`. `echo env` opens no run.
  // Words inside quoted regions (prose) are message text / quoted args.
  const words = scanShellWords(normalized, mask);
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    if (w.prose) continue;
    const lower = w.value.toLowerCase();
    // BUG-332 r17 (r16 attacker F4): the shell half of the class is tested by
    // isShellExecutableWord — the SAME class test as the wrapper-recursion
    // backstop — NOT the stale subset hardcoded in INDIRECTION_COMMAND_WORDS
    // (which was missing fish/tcsh/csh/mksh/pdksh/yash/rbash/sash/osh/posh/
    // nu/xonsh/elvish/oil/es/rc, letting `fish -c "git commit"` classify
    // 'plain'). GR#3: SHELL_EXECUTABLE_RE is the one shell list.
    // isStringExecutorInvocation adds the r16 F2 language runtimes in
    // code-exec mode (`python -c`, `perl -e`, `php -r`, `ruby -e`, `node -e`).
    const isIndirectionWord = INDIRECTION_COMMAND_WORDS.has(lower) ||
      isShellExecutableWord(w.value) || isStringExecutorInvocation(words, i);
    if (w.commandStart && isIndirectionWord) return true;
    if ((ENV_ASSIGN_RE.test(lower) || PS_ENV_ASSIGN_RE.test(lower)) &&
        (i === 0 || !words[i - 1].commandStart)) return true;
    if (isIndirectionWord && !w.commandStart &&
        (i === 0 || !words[i - 1].commandStart) &&
        words[i + 1] && words[i + 1].inPrefixArgs) return true;
  }
  return false;
}

/** True when a `commit`/`commit.exe` word is the verb of a HIDDEN git
 * executable — a shell variable/expansion reference standing immediately
 * before it (`$GIT commit`, `$(echo $GIT) commit`, `GIT=git; $GIT commit`,
 * `` `G` `` spelled via backticks). F1 (r17 attacker): these carry NO literal
 * `git` token, so findCommitInvocation returns null and classifyCommitShape
 * would classify 'none' → silent allow of a real code-bearing commit. The
 * executable word is a variable the guard cannot resolve — its expansion may
 * BE git — so a commit verb reached that way is the unattributable shell
 * indirection the AARON structural allowlist denies fail-closed. Only the
 * IMMEDIATELY preceding non-prose word counts: `$foo bar commit` stays allowed
 * (the commit's command word is `bar`, not a variable), and `git commit`,
 * `echo commit`, `grep commit f` are untouched. A false-positive deny is
 * recoverable; a false-negative allow is the hole. */
function hasHiddenCommit(command) {
  const normalized = normalizeContinuations(command);
  const mask = buildQuoteMask(normalized);
  const words = scanShellWords(normalized, mask);
  for (let i = 1; i < words.length; i++) {
    if (words[i].prose) continue;
    const lower = words[i].value.toLowerCase();
    if (lower !== 'commit' && lower !== 'commit.exe') continue;
    let j = i - 1;
    while (j >= 0 && words[j].prose) j--;
    if (j < 0) continue;
    if (/[`$]/.test(words[j].value)) return true;
  }
  return false;
}

/** Structural commit-recognition classifier (AARON RULING 2026-08-23).
 * Returns:
 *   { kind: 'none' }              — no commit-creating invocation recognisable
 *                                   → the caller's decoder backstop already
 *                                   ran; otherwise allow silently.
 *   { kind: 'indirect', reason }  — a commit-shaped command carrying shell
 *                                   indirection (or an aliased verb) → DENY
 *                                   fail-closed, never a guess.
 *   { kind: 'plain', invocation } — a literal `git commit` with zero shell
 *                                   indirection → the existing verdict flow
 *                                   proceeds.
 */
function classifyCommitShape(command) {
  const inv = findCommitInvocation(command);
  if (!inv) {
    // BUG-332 r18 (r17 attacker F1): a HIDDEN git executable — `$GIT commit`,
    // `$(echo $GIT) commit`, `GIT=git; $GIT commit` — carries NO literal git
    // token anywhere, so findCommitInvocation returns null and this would
    // classify 'none' (silent allow of a real code-bearing commit). The commit
    // verb's executable word is a shell variable/expansion reference (`$VAR`,
    // `${VAR}`, `$(...)`, backticks) whose value the guard cannot see — it may
    // BE git. Per the structural allowlist's deny-fail-closed posture that is
    // exactly the unattributable shell indirection the ruling bans.
    if (hasHiddenCommit(command)) return { kind: 'indirect', reason: 'hidden-commit' };
    return { kind: 'none' };
  }
  // The plain form is the LITERAL `git commit`. Three shapes, distinguished by
  // the resolved verb (`inv.verb`) vs the literal word typed after git
  // (`inv.verbWord`), which findCommitInvocation sets apart ONLY for aliases:
  //   * ALIAS — the typed word differs from the resolved verb (`git cy`
  //     resolving to `commit -a`, `git mg` to `merge --no-ff`). An alias body
  //     can smuggle staging flags or shell text this guard cannot see from the
  //     command text (BUG-224 round 4) → deny fail-closed.
  //   * REAL non-commit porcelain verb — merge/cherry-pick/revert/am are
  //     genuine git commands, not aliases (verbWord === verb), and are
  //     deliberately OUT of the commit trap's scope ("merge is not commit",
  //     ROUND-4 — blocking them would also break the team's own local merges)
  //     → 'none' allows silently, exactly as isCommitInvocation() always did.
  //   * PLAIN `git commit` — the allowlisted form; only then consult shell
  //     indirection (AARON RULING 2026-08-23).
  if (inv.verbWord !== inv.verb) return { kind: 'indirect', reason: 'alias' };
  if (inv.verb !== 'commit') return { kind: 'none' };
  // BUG-332 r17 (r16 attacker F1 completion): a replay verb with a NO-COMMIT
  // flag BEFORE the literal commit stages content invisible to the verdict
  // flow (which reads the PRE-command index) — deny fail-closed before we even
  // reach the shell-indirection question (the staging is a git verb, not a
  // pipe).
  if (hasReplayNoCommitStaging(command)) return { kind: 'indirect', reason: 'replay-staging' };
  if (hasShellIndirection(command)) return { kind: 'indirect', reason: 'shell-indirection' };
  return { kind: 'plain', invocation: inv };
}

/** True when a command containing a literal `git commit` ALSO runs a replay
 * verb with a NO-COMMIT flag before it — `git cherry-pick -n/-x… <sha>`,
 * `git cherry-pick --no-commit`, `git merge --no-commit <sha>`, `git revert
 * -n <sha>`, `git am --no-commit <patch>`.
 *
 * BUG-332 r17 (r16 attacker F1 completion): the verdict flow reads the
 * PRE-command index (`git diff --cached`) before any of the command runs, so a
 * replay with --no-commit stages code that is INVISIBLE to the guard — the
 * later literal `git commit` then commits un-attacked code silently. Same
 * un-enumerable-staging class as `ambiguousAdd` / `--pathspec-from-file` / a
 * bare pathspec: the staging is done by a verb the guard treats as non-commit
 * (ROUND-4 "merge is not commit"), but the chain ENDS in a real commit of
 * invisible content → fail-closed deny.
 *
 * Verb-aware flag set: `-n` IS --no-commit for cherry-pick/revert (and may
 * appear combined, `-xn`), but for merge `-n` means --no-stat (harmless —
 * auto-commit behaviour unchanged) and am has no short form, so only
 * `--no-commit` counts for merge/am. Only a replay STARTING before the
 * commit's own verb matters — a replay after it cannot pollute its index. */
function hasReplayNoCommitStaging(command) {
  let commitStart = -1;
  const replays = [];
  for (const entry of iterGitInvocations(command)) {
    if (!entry.parsed) continue;
    const v = entry.resolved;
    if (!KNOWN_COMMIT_VERBS.has(v)) continue;
    if (v === 'commit') { if (commitStart < 0) commitStart = entry.verbStart; continue; }
    replays.push(entry);
  }
  if (commitStart < 0) return false;
  const noCommitFlag = (v, tail) => {
    if (v === 'cherry-pick' || v === 'revert') {
      return /(?:^|\s)--no-commit(?:\s|$)|(?:^|\s)-[A-Za-z0-9]*n[A-Za-z0-9]*(?:\s|$)/.test(tail);
    }
    if (v === 'merge' || v === 'am') {
      return /(?:^|\s)--no-commit(?:\s|$)/.test(tail);
    }
    return false;
  };
  for (const e of replays) {
    if (e.verbStart >= commitStart) continue;
    if (noCommitFlag(e.resolved, e.tail || '')) return true;
  }
  return false;
}

/** Position of the real pipe (`|`, not `||`) feeding the word at `pos`, or -1
 * when the word is not a pipe target (a non-space non-pipe char intervenes).
 * BUG-332 r14 (r13 attacker F1): `|&` (stderr-merged pipe) counts — the `&`
 * is the pipe operator's stdout+stderr merge flag, so it returns the `|`. */
function pipeBefore(text, pos) {
  for (let i = pos - 1; i >= 0; i--) {
    const c = text[i];
    if (/\s/.test(c)) continue;
    if (c === '|') return i;
    if (c === '&' && i - 1 >= 0 && text[i - 1] === '|') return i - 1;
    return -1;
  }
  return -1;
}

/** Position just after the last shell-command boundary (`|;&\n({`) before
 * `pos` — the start of the pipeline stage the word at `pos` belongs to. Quote
 * state is tracked the same way buildQuoteMask does so a boundary inside a
 * QUOTED ARGUMENT (`echo "cd /a && b" | bash` — the `&&` is echo's data, not
 * a command separator) can never truncate the stage. */
function lastBoundaryBefore(text, pos) {
  let quote = null;
  for (let i = pos - 1; i >= 0; i--) {
    const c = text[i];
    if (quote) {
      // backward scan: the LAST quote of a region is its closer; the pair
      // before it re-opens, so a quoted region is skipped whole.
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i - 1 >= 0) { i--; continue; }
      if (c === (quote === 'ansic' ? "'" : quote)) quote = null;
      continue;
    }
    if (c === '\\' && i - 1 >= 0) { i--; continue; }
    if (c === '$' && text[i + 1] === "'") { quote = 'ansic'; i--; continue; }
    if (c === '"' || c === "'") { quote = c; continue; }
    if (c === '|' || c === ';' || c === '&' || c === '\n' || c === '(' || c === '{') return i + 1;
  }
  return 0;
}

/** The single lexer — see the section header for the full design rationale.
 * Returns an array of word records, in text order:
 *   { value, start, end, depth, commandStart, prose }
 *   value        — the shell's dequoted view (quote-split fragments
 *                  concatenated: `c"d"` → `cd`, `g"it"` → `git`).
 *   start / end  — the word's span in `text`.
 *   depth        — subshell nesting (`$(` / backtick / `(` increase it, `)`
 *                  decreases). A cwd-change at depth D affects only an add at
 *                  depth >= D — a `cd` inside `$(...)`/backticks/`(...)` can
 *                  never shift an add OUTSIDE it (the r6 F14 over-block).
 *   commandStart — true when this word begins a simple command (after a
 *                  separator, a reserved word, a prefix builtin, or an
 *                  env-prefix assignment), never for an argument, a redirect
 *                  target, or a case pattern.
 *   prose        — true when the word's first character is inside a quoted
 *                  region per `quoteMask` (e.g. `"git"`, `-m "cd /tmp"`).
 * `quoteMask` is authorGuard.buildQuoteMask(text) (or built here); it marks
 * quoted regions and heredoc bodies, which the lexer consumes as opaque runs
 * (a masked run ADJACENT to an unquoted fragment is part of that word —
 * quote-splitting). */
function scanShellWords(text, quoteMask) {
  const mask = quoteMask || buildQuoteMask(text);
  return scanShellWordsAt(text, mask, 0, 0, 0);
}

function scanShellWordsAt(text, mask, i, rec, baseDepth) {
  const words = [];
  if (rec > MAX_SHELL_NEST) return words; // pathological nesting — opaque
  let depth = baseDepth;
  let expectCommand = true;
  let redirectTarget = false;
  // BUG-332 r8: true while scanning a SUBPROCESS prefix's argument run
  // (`sudo -u root bash -c "…"` — everything between the prefix and the next
  // separator is the prefix's argument, never a standalone command; a `cd`
  // there cannot shift the guard's cwd, and the run-string wrapper at the end
  // is still real command text the prefix EXECUTES).
  let prefixArgs = false;
  const len = text.length;

  while (i < len) {
    const c = text[i];
    if (/\s/.test(c)) { i++; continue; }
    // A masked run at a word boundary: a quoted word (possibly quote-split
    // into adjacent unquoted text) or a heredoc body. Quoted words are
    // commands OR arguments — a real shell runs `"git" add` — so classify by
    // command position like any other word; heredoc bodies are argument
    // values, and a quoted word at argument position is a message/flag value
    // (the prose vs commandStart distinction isRealGitWord() relies on).
    if (mask[i]) {
      const w = readShellWord(text, i, mask);
      const lower = w.value.toLowerCase();
      const isEnvAssign = expectCommand && /^[a-z_][a-z0-9_]*=/.test(lower);
      const isPrefix = expectCommand && SHELL_PREFIX_WORDS.has(lower);
      const isSubprocess = expectCommand && SHELL_SUBPROCESS_PREFIX_WORDS.has(lower);
      // BUG-332 r8: a leading-`-` word at command position is a FLAG of the
      // pending command (`command -p bash`, `sudo -n bash`), never a command
      // itself — a real executable is invoked as `./-x`, never bare `-x`.
      const isFlag = expectCommand && !prefixArgs && /^-/.test(w.value);
      const commandStart = expectCommand && !prefixArgs && !isEnvAssign && !isPrefix && !isSubprocess && !isFlag && !redirectTarget;
      words.push({
        value: w.value, start: i, end: w.end, depth,
        commandStart, prose: true, inPrefixArgs: prefixArgs,
      });
      if (isSubprocess) prefixArgs = true;
      if (commandStart) expectCommand = false;
      redirectTarget = false;
      i = w.end;
      continue;
    }
    // Redirection operators — the NEXT word is a redirect target, never a
    // command (`> out`, `2>&1`, `<<EOF`, `<<< "text"`).
    const redir = /^[0-9]*[<>]{1,2}(?:&[0-9]+|-)?/.exec(text.slice(i));
    if (redir) { redirectTarget = true; i += redir[0].length; continue; }
    // Shell separators and group operators — each resets the command position
    // AND ends any subprocess-prefix argument run.
    if (c === ';' || c === '\n' || c === '&' || c === '|') { expectCommand = true; prefixArgs = false; i++; continue; }
    if (c === '$' && text[i + 1] === '(') { depth++; expectCommand = true; prefixArgs = false; i += 2; continue; }
    if (c === '(') { depth++; expectCommand = true; prefixArgs = false; i++; continue; }
    if (c === ')') { if (depth > baseDepth) depth--; expectCommand = true; prefixArgs = false; i++; continue; }
    if (c === '{' || c === '}') { expectCommand = true; prefixArgs = false; i++; continue; }
    if (c === '`') {
      // Backtick substitution: its BODY is real command text at depth+1, so
      // recurse into it (a `cd` / `git` inside still runs, but its depth is a
      // subshell for the F14 rule). Unterminated → swallow to EOF (fail-safe).
      const bodyEnd = findBacktickEnd(text, i);
      if (bodyEnd < 0) break;
      const inner = scanShellWordsAt(
        text.slice(i + 1, bodyEnd), mask.slice(i + 1, bodyEnd), 0, rec + 1, depth + 1);
      for (const w of inner) words.push({ ...w, start: w.start + i + 1, end: w.end + i + 1 });
      prefixArgs = false;
      i = bodyEnd + 1;
      continue;
    }
    if (c === '\\') { i += 2; continue; }
    // A real (unquoted-start) word — the common case.
    const w = readShellWord(text, i, mask);
    const lower = w.value.toLowerCase();
    const isEnvAssign = expectCommand && /^[a-z_][a-z0-9_]*=/.test(lower);
    const isKeyword = expectCommand && SHELL_KEYWORD_WORDS.has(lower);
    const isPrefix = expectCommand && SHELL_PREFIX_WORDS.has(lower);
    const isSubprocess = expectCommand && SHELL_SUBPROCESS_PREFIX_WORDS.has(lower);
    const isFlag = expectCommand && !prefixArgs && /^-/.test(w.value);
    const commandStart = expectCommand && !prefixArgs && !isEnvAssign && !isKeyword && !isPrefix && !isSubprocess && !isFlag && !redirectTarget;
    words.push({
      value: w.value, start: i, end: w.end, depth,
      commandStart, prose: false, inPrefixArgs: prefixArgs,
    });
    if (isSubprocess) prefixArgs = true;
    if (commandStart) expectCommand = false;
    redirectTarget = false;
    i = w.end;
  }
  return words;
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
  //
  // BUG-332 r17 (r16 attacker F1, CRITICAL): first-match-stop let a literal
  // `git commit` LATER in a chain slip — `git cherry-pick -n X && git commit`
  // (and `git merge --no-commit` / `git revert --no-commit` / `git am`
  // followed by `git commit`) returned the FIRST KNOWN_COMMIT_VERBS
  // invocation (the replay verb), which classifyCommitShape maps to 'none' →
  // silent allow of a REAL code-bearing commit (proven end-to-end as commit
  // a4e7dd1 with zero verdict). The AARON structural allowlist proceeds ONLY
  // on the plain benign `git commit` form, so the recogniser now PREFERS a
  // literal `commit` verb anywhere in the chain: if one exists, THAT is the
  // invocation the caller gates on. The first known-verb invocation is the
  // fallback when no literal commit is present (`git cherry-pick <sha>`
  // standalone stays OUT of the trap, ROUND-4 "merge is not commit"). An
  // ALIASED verb that resolves to commit (git cy) is still returned — callers
  // whose guard is the structural layer turn verbWord≠verb into an alias
  // DENY (BUG-224); here the resolved verb is what classifies.
  let firstKnownVerb = null;
  for (const entry of iterGitInvocations(cmd)) {
    if (!entry.parsed || !KNOWN_COMMIT_VERBS.has(entry.resolved)) continue;
    if (entry.resolved === 'commit') {
      // A literal commit verb — the allowlisted form. Prefer it over any
      // earlier replay verb (cherry-pick/merge/revert/am) so the chain is
      // gated on the actual commit, never the replay that precedes it.
      return buildCommitInvocation(entry);
    }
    // Replay verb (cherry-pick/merge/revert/am) — remember as fallback and
    // keep scanning for a LATER literal `git commit`.
    if (firstKnownVerb === null) firstKnownVerb = entry;
  }
  return firstKnownVerb !== null ? buildCommitInvocation(firstKnownVerb) : null;
}

/** Shared builder for the invocation object findCommitInvocation returns. */
function buildCommitInvocation(entry) {
  return {
    text: entry.text,
    verb: entry.resolved,
    verbWord: entry.verbWord, // original word — callers detect aliasing (BUG-224)
    overrides: entry.overrides,
    prefixEnd: entry.verbEnd, // env-var prefix search bound: up to & incl. verb word
    suffixStart: entry.verbEnd,
  };
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
    // BUG-332 r7: the lexer runs once per text, shared by the regex loop's
    // position-dedupe (a token the regex already found must not be re-yielded
    // by the quote-split fallback below) and by the fallback itself.
    const shellWords = scanShellWords(text, quoteMask);
    const covered = new Set();
    let m;
    while ((m = re.exec(text)) !== null) {
      covered.add(m.index + (m[0].length - m[1].length));
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
    // BUG-332 r7: quote-split git spellings (`g"it" commit`, `/usr/"bin"/git`)
    // that GIT_TOKEN_RE's token group cannot recognise — its quote characters
    // sit inside the word, so the regex token class never matches. The lexer
    // concatenates the fragments, so a tokenizer-found git word NOT already
    // covered by the regex loop is a real invocation, parsed identically.
    // Yield only when a verb parses — never a parsed:false entry from this
    // path: a quote-split fragment with no verb is prose, and fabricating an
    // unparseable entry would false-deny innocent messages.
    for (const w of shellWords) {
      if (covered.has(w.start)) continue;
      if (!isGitExecutableWord(w.value)) continue;
      if (!isRealGitWord(w)) continue;
      const inv = parseGitInvocation(text, w.end);
      if (!inv) continue;
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
    hasDecoderFedShell,
    hasShellIndirection,
    classifyCommitShape,
    unescapeDoubleQuoted,
    buildQuoteMask,
    scanShellWords,
    isGitExecutableWord,
    isRealGitWord,
    CWD_COMMAND_WORDS,
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
