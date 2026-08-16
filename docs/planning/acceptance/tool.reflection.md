BOW code: FEAT-112

# Acceptance criteria — tool.reflection (FEAT-112)

**BOW code:** FEAT-112 (`tool.reflection`)
**Module key:** tool.reflection (GUID b475594b-c35c-4049-af82-2da246683c82)
**Spec refs:** GR#1 (Aggressive Error Trapping / Golden Rules self-check);
GR#14 (Memory Recall at Task Start — the reflection prompt is a forced
memory/Vestige recall point); M0-ENG §5 (hooks); `docs/planning/checkpoint.md`
(the reflection prompt's "save learnings / update BOW" items feed the
checkpoint).
**Date:** 2026-08-16 (written after the fact — archaeology; the code shipped
already)
**Status:** retrospective
**Package under test:** `claude-reflection.js` (repo root, Node.js) — a
`PostToolUse` hook wired on both the `Bash` and `PowerShell` matchers in
`.claude/settings.json`.
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. This item's own
gates: `node --check claude-reflection.js`; SG-6 (no Co-Authored-By).
**There is no test file for this hook** — see Section F (the ACs specify the
tests to be written).

## What this guard actually guarantees (read from the code, not assumed)

Unlike the two PreToolUse hooks above, this hook **never denies anything** — it
is a `PostToolUse` hook, so it fires *after* the command has already run, and
its stdout becomes part of the conversation context, not a harness decision. Its
contract is: *after a successful `git commit`, emit a Golden-Rules reflection
prompt; otherwise stay silent.* It is entirely fail-open (any parse error → exit
0) and has no escape hatch (there is nothing to escape — it cannot block).

### A. Engagement — when it evaluates a command at all

- **AC-1.** It fires after every Bash (and PowerShell) command by wiring, but
  only proceeds to inspect output when the command text contains **both** the
  substring `commit` and the substring `git` — a plain `command.includes()`
  pair, not a boundary-anchored regex, not quote-mask aware. Check:
  `grep -n "includes('commit')\|includes('git')" claude-reflection.js` matches;
  a passing test asserts `git commit -m x` proceeds and `git status` /
  `npm install` / `git push` do not.
- **AC-2.** Because the trigger is substring-based, a prose mention of
  "git commit" (e.g. an `echo` of a bug report quoting the phrase) also engages
  the *trigger* — but the success gate (AC-4) means no prompt is emitted unless
  the command's stdout also looks like a successful commit. Documented as the
  current contract, not a defect fixed here (see ASM-745). Check: a passing test
  asserts `echo "git commit is the bypass"` (whose stdout has no `[main `/`
  `[develop `) emits no prompt.

### B. Success gating — only a *successful* commit reflects

- **AC-3.** The commit-success signal is read from `tool_output.stdout`
  (falling back to `tool_output` itself, then to empty string). Check:
  `grep -n "tool_output" claude-reflection.js` matches; a passing test feeds a
  post-commit `tool_output` whose stdout is the git success line
  `[main abc1234] subject` and asserts the prompt is emitted.
- **AC-4.** If `tool_output` is a string **and** it does not contain `[main `
  **and** does not contain `[develop `, the hook exits 0 with no prompt — a
  failed commit (git's own error output) does not reflect. Check: a passing test
  feeds stdout `error: pathspec ...` (no `[main `) and asserts exit 0, empty
  stdout.
- **AC-5 (residual gap).** The success signal is a **branch-name heuristic**: it
  keys on the literal `[main ` or `[develop ` prefix of git's commit summary.
  A successful commit on any other branch (git prints `[<branch> <short-hash>]`)
  does not match, so no prompt is emitted — a false negative. Conversely, a
  non-string `tool_output` (an object with no string stdout) skips the gate
  entirely and always reflects. Documented as the current contract and gap, not
  re-fixed here (see ASM-745). Check: a passing test asserts a successful commit
  on branch `feature/x` (stdout `[feature/x deadbeef] subject`) emits **no**
  prompt, proving the gap is real and understood — this AC is written to
  document today's behaviour, which is RED against a "reflect on every branch"
  ideal by design.

### C. Output contract — non-blocking, exit 0

- **AC-6.** On a successful commit, the hook writes the reflection prompt to
  stdout and exits 0. The prompt is a single `<user-prompt-submit-hook>` block
  covering six numbered sections (Golden Rules check, Memory & learning, User
  relationship, Quality check, Artefacts, Book of Work) and ends with an
  explicit "skip if clean / say clean" line. Check: `grep -n "POST-COMMIT
  REFLECTION\|user-prompt-submit-hook" claude-reflection.js` matches; a passing
  test asserts stdout is non-empty and contains the six section headings.
- **AC-7.** The hook emits **no** `hookSpecificOutput` and **no**
  `permissionDecision` of any kind — a `PostToolUse` hook's stdout is
  context, not a decision, so there is nothing for the harness to act on as a
  block. Check: `grep -n "permissionDecision\|hookSpecificOutput"
  claude-reflection.js` finds zero matches.
- **AC-8.** The prompt content is Prix Six/TypeScript-era (references to
  Puppeteer, `version.ts`, `package.json`, `CHANGELOG.md`, `MEMORY.md`, the
  `/diagnose` skill) and is **stale against the Metropolis Go profile** — the
  contract documented here is *emit a reflection prompt on a successful
  commit*, not the specific question wording. See ASM-743. Check: reviewed by
  eye (the doc's own Assumptions section states the staleness; a Tester must not
  bounce on the wording, since AC-6's check keys on structure, not content).

### D. Fail-open

- **AC-9.** Any parse error or uncaught exception results in a silent exit 0 —
  a `PostToolUse` hook that threw would otherwise pollute the context or (worse)
  be reported as a hook failure. Check: `grep -n "catch" claude-reflection.js`
  shows the outer handler exiting 0; a passing test feeds malformed stdin and
  asserts exit 0, empty stdout.
- **AC-10.** There is **no** BOM-stripping before `JSON.parse` (unlike
  `claude-pre-commit-check.js`/`claude-pre-push-check.js`). A UTF-8 BOM
  prepended to stdin therefore makes `JSON.parse` throw, which the AC-9
  fail-open path silently swallows — a BOM'd invocation is a silent no-op, not a
  crash. Check: `grep -n "FEFF\|uFEFF\|BOM" claude-reflection.js` finds zero
  matches (documented absence); a passing test prepends a BOM to valid JSON and
  asserts exit 0, empty stdout.

### E. Escape hatch

- **AC-11.** There is no disable env var — and none is needed, because the hook
  cannot block. Check: `grep -n "CLAUDE_DISABLE\|process.env" claude-reflection.js`
  finds zero matches (documented absence). This is a stated non-feature, not an
  omission.

### F. Tests (none exist — to be written)

- **AC-12.** A test file is created (junior's name, e.g.
  `claude-reflection.test.js`) covering at minimum: trigger-on (`git commit`)
  vs trigger-off (`git status`, `git push`, an `echo` of prose mentioning
  "git commit" with no success-line in stdout), success gating (`[main `/
  `[develop ` present vs absent), the branch-name false-negative gap (AC-5),
  the non-string `tool_output` always-reflects edge case, the prompt's
  six-section shape, exit 0 with no `permissionDecision` in every case, and
  fail-open on malformed/BOM'd stdin. Each positive case is paired with a
  mutant check proving the fixture distinguishes correct from plausible-wrong
  (e.g. a mutant that emits on any `git commit` regardless of success must make
  the AC-4/AC-5 tests red). Check: `node --test claude-reflection.test.js` is
  green, and the corpus includes the explicit mutant/negative cases.
- **AC-13.** The tests exercise the hook via spawned subprocesses feeding the
  stdin JSON the hook expects (matching `claude-pre-commit-check.test.js`'s
  pattern) — this hook, like the pre-push hook, has no `require.main === module`
  guard and runs its stdin listener at load, so `require()`ing it in-process
  would hang or fire the listener twice. Check: reviewed by eye / the test
  passes without hanging on stdin.

## Out of scope (stated, not silently absent)

- Refreshing the prompt's stale Prix Six/TS wording for the Metropolis Go
  profile — a documentation/content pass, not this hook's contract (ASM-743).
- Making the commit-success signal branch-agnostic (e.g. parsing the `[<branch>
  <hash>]` line instead of hardcoding `[main `/`[develop `) — a genuine fix,
  but it is a *change* to the hook, not documentation of the committed
  behaviour. Flagged in Escalations; not assumed into this retrospective.
- Closing the substring-trigger false-positive (a prose mention of "git commit"
  that also happens to echo a `[main ...]`-shaped line would emit a spurious
  prompt) — same category: a change, not a documentation item.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-743.** The reflection prompt's question content (Puppeteer, `version.ts`,
  `package.json`, `CHANGELOG.md`, `/diagnose`) is Prix Six-era and predates the
  Metropolis Go profile; it is treated as stale-but-retained. The documented
  contract is *emit a prompt on a successful commit*, not the specific wording.
  What breaks if this is wrong: none technically — the hook still prompts — but
  a Tester who reads the wording as part of the contract would bounce on stale
  content that this doc explicitly declares out of scope.
- **ASM-745.** The commit-success detection keys on stdout containing `[main ` or
  `[develop `, a branch-name heuristic: commits on other branches do not trigger
  reflection, and the substring trigger matches prose mentions of "git commit."
  Documented as the current contract and gap, not a defect fixed here. What
  breaks if this is wrong: a future maintainer reading AC-4/AC-5 as "must
  reflect on every branch" would treat today's correct-but-narrow behaviour as a
  bug and re-litigate a documented gap.

## Escalations

- **The branch-name heuristic (AC-5) is the one live defect-shaped thing in this
  hook.** On Metropolis's current single-branch `main` posture it happens to work,
  but the moment `develop`/`feature/*` branches carry real work, successful
  commits on them will silently stop reflecting — exactly the "no learning loop"
  failure this hook was born to prevent (2026-03-24). Flagging for Bill/Aaron as
  a candidate small BUG item: parse git's own `[<branch> <short-hash>]` summary
  instead of hardcoding two branch names.
- **No test suite (ASM-743/AC-12).** Same escalation shape as `tool.prepushcheck.md`:
  closing FEAT-112 properly requires the test file AC-12/AC-13 specify, since a
  prompt that silently stops firing is indistinguishable from a working one to a
  human who isn't watching for it. "Documented" is not "verdicted."
