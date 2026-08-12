BOW code: none — see Escalation A

# Acceptance criteria — tool.authorguard (archaeology)

**Module key:** tool.authorguard
**BOW code:** none currently owns the build. `git log` shows the guard shipped
across three commits (511fe70, 96d6fe3 "feat: commit-identity guard...
[tool.authorguard]", c3aa536) tagged only with the mkey, never a `[FEAT-xxx]`/
`[BUG-xxx]` code. BUG-035 ("Agent left a stray commit with a fabricated author
on local main") is the incident that motivated the build and is still **open**
— it was never closed against the fix. ASM-364 records the same gap from the
other direction ("code.json has no tool.committhook key; FEAT-045 points at
tool.authorguard"). See Escalation A.
**Spec refs:** GR#2 (Version/Identity Discipline); GR#15 (Validators Derive
From Data); BUG-035; BUG-042 (why the repo's history was rewritten in the
first place — the thing a fabricated author would permanently undo).
**Date:** 2026-08-11 (written after the fact — this is archaeology, not a
brief BAs wrote before dispatch)
**Status:** active
**Package under test:** `claude-author-guard.js` (repo root, Node.js).
**Standard gates:** Node, not Go — SG-1/2/4/7 (see README.md) do not apply.
This item's own gates: `node --check claude-author-guard.js`;
`node claude-author-guard.test.js` (or the project's Node test runner)
passing; SG-6 (no Co-Authored-By).

## Why this file exists, and why it is written last-to-first

This is the highest-cost gap of the twelve found in the code.json↔acceptance
audit. The guard has been through **four rounds** (v1 → BUG-044..052 →
ROUND4-1/2/3 → BUG-077..084, all visible in the header comments and the BOW)
and **fifteen live bypasses/false-positives found by Destructive agents**,
and at no point did a criteria file exist for a developer or Destructive
agent to build or attack *against*. Every one of those fifteen findings was
discovered by a human/agent reasoning about the code directly, not by a
check failing against a stated contract. This file states the contract now,
after the fact, so the *next* round has something to be judged against
instead of starting from the code again.

## What this guard actually guarantees (read from the code, not assumed)

### A. Engagement — when the guard evaluates a command at all

- **AC-1.** The guard evaluates identity ONLY when `findCommitInvocation()`
  locates a `git <verb>` invocation, where `<verb>` is a member of
  `KNOWN_COMMIT_VERBS = {commit, cherry-pick, revert, am, merge}` (source:
  `claude-author-guard.js:203`), found either directly in the command text or
  inside a recognised shell-wrapper body (`bash -c`, `sh -c`, `zsh -c`,
  `dash -c`, `ksh -c`, `powershell -Command`, `pwsh -Command`, `cmd /c`,
  recursed to `MAX_WRAPPER_DEPTH = 4`). Check: a passing test asserts
  `findCommitInvocation('git status')` returns `null`; a passing test asserts
  `findCommitInvocation('git commit -m "x"')` returns a non-null match; a
  passing test asserts a `bash -c "git commit -m x"` wrapper is also matched.
  **What a false pass looks like:** a test that only checks the direct-command
  case would also pass a build that silently dropped wrapper recursion —
  the wrapper case must be asserted separately.
- **AC-2.** `git rebase` is deliberately NOT intercepted (header: "already-
  vetted author/committer chain passing through it is unaffected" —
  ASM-author-guard-rebase-scope). Check: a passing test asserts
  `findCommitInvocation('git rebase main')` returns `null`. **Divergence
  note:** this is a scope decision, not a gap discovered by attack — no
  BUG-xxx currently disputes it. Recorded here so a future Destructive round
  has to argue against a stated position, not guess at an implicit one.
- **AC-3.** Plumbing verbs that also create commits — `commit-tree`,
  `fast-import`, `stash store` — are NOT recognised (header: "structural
  limit... `fast-import` takes a data stream on stdin this guard cannot
  inspect from the command string at all"). Check: a passing test asserts
  `findCommitInvocation('git commit-tree ...')` returns `null`, with the test
  comment citing this as a stated non-goal, not an oversight.

### B. Identity derivation (GR#15 — must be derived, never hardcoded)

- **AC-4.** The sanctioned identity set is the union of exactly three
  sources: (1) `git config user.email`, unconditional; (2) emails appearing
  `>= HISTORY_THRESHOLD` (3) times as author OR committer within the most
  recent `HISTORY_SCAN_LIMIT` (2000) commits of the trunk branch; (3)
  `CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES`, operator-set env var. Check: a
  passing test with a fixture repo (2 commits from `a@x.com`, 3 from
  `b@x.com`) asserts `historyEmails()` includes `b@x.com` and excludes
  `a@x.com` — the check must fail on an off-by-one (threshold `>` instead of
  `>=`, or scanning `author` only and not `committer`).
- **AC-5.** History scanning is capped at 2000 commits (BUG-052's fix — was
  unbounded). Check: `grep -n "HISTORY_SCAN_LIMIT" claude-author-guard.js`
  shows it passed as `--max-count` to `git log`; a passing test on a fixture
  history longer than the cap asserts the git invocation used
  `--max-count=2000` (not merely that the function returns *some* answer,
  which an unbounded scan would also do on a small fixture — the check must
  use a fixture large enough, or assert the invocation arguments directly,
  to actually distinguish capped from uncapped).
- **AC-6.** `-c user.email=...` / `-c user.name=...` parsed out of the
  invocation itself is READ as the identity the invocation would produce and
  checked against the sanctioned set — it is never itself ADDED to the
  sanctioned set (header item 4; this is what closes BUG-044). Check: a
  passing test with `git -c user.email=evil@x.com commit -m x` (evil@x.com
  not in the derived sanctioned set) asserts DENY; a second passing test
  re-runs the SAME scenario a second time and asserts the second call is
  STILL denied — proving the first denied call did not accidentally get
  `evil@x.com` added to the sanctioned set as a side effect (this is the
  shape of check AC-4 of `tool.destructiveguard.md` uses for the analogous
  append-only claim — a lazy implementation that grows its "known" set to
  suppress repeat noise would pass a single-call test and fail this one).

### C. What is checked, per verb

- **AC-7.** For every recognised commit-creating verb, the COMMITTER identity
  is always checked fresh from (in order) `GIT_COMMITTER_EMAIL` env override,
  else `-c user.email`, else `git config user.email` — never inherited.
  Check: a passing test asserts a fixture with a sanctioned config email but
  an unsanctioned `GIT_COMMITTER_EMAIL` env override is DENIED.
- **AC-8.** For `git commit --amend` specifically, with no explicit author
  override and no `--reset-author`, the AUTHOR is NOT re-checked (git's own
  semantics: it inherits HEAD's already-vetted author). Every other verb, and
  every other case of `commit`, checks the author fresh the same way as the
  committer. Check: a passing test asserts `git commit --amend -m x` with no
  author flags is allowed regardless of the *current* config email (simulate
  by setting an unsanctioned config email and asserting the amend still
  passes, isolating "inherited, not re-checked" from "happens to pass because
  config was fine"); a second passing test asserts
  `git commit --amend --reset-author -m x` under the same unsanctioned config
  IS denied — proving `--reset-author` actually re-arms the check rather than
  merely being accepted as a flag.
- **AC-9.** `--author`/`--author=` is read from quote-aware TOKENS of the
  suffix, explicitly skipping the token that is the *value* of `-m`/
  `--message` (BUG-050's fix — the false-positive case). Check: a passing
  test asserts `git commit -m "see --author=<fake@x.com> in the bug report"`
  is ALLOWED (the string appears only inside the message value); a passing
  test asserts `git commit --author="Fake <fake@x.com>" -m x` (fake@x.com
  unsanctioned) IS denied — both halves are required, since a check that only
  tests the second would also pass a build that never learned the
  message-value exclusion in the first place (i.e. it would fail on the first
  test, revealing the regression).
- **AC-10.** An `--author`/env override present but with no `<email>`
  extractable from it is treated as unverifiable and fails closed (denied)
  rather than silently skipped. Check: a passing test asserts
  `git commit --author="not-an-email-shape" -m x` is DENIED with a message
  naming the field as unparseable, not merely "not sanctioned".

### D. Parsing robustness (the four rounds' actual content)

- **AC-11.** The git executable token match requires quoted OR unquoted
  paths, including Windows paths containing spaces resolved via progressive
  CreateProcess-style prefix matching (ROUND4-3, ASM-quoted-path-token) —
  `C:\Program Files\Git\bin\git.exe commit --author=...` UNQUOTED is
  recognised. Check: a passing test asserts this exact string is recognised
  as a real invocation (this was found LIVE-executing on this project's own
  host, per the header — the check exists specifically because a narrower
  token pattern silently missed it).
- **AC-12.** Quote-state tracking (`buildQuoteMask`) treats content inside a
  well-formed heredoc body as inert (never scanned as a candidate git
  invocation), and resumes the SURROUNDING quote state correctly after the
  heredoc terminator (BUG-078's fix). Check: a passing test asserts a
  `git commit -F - <<'EOF' ... it's fine ... EOF` heredoc containing a lone
  apostrophe inside the body does NOT flip quote state and hide a REAL
  `git commit --author=<fake>` that follows the heredoc in the same command
  string.
- **AC-13 (known open gap — not this guard's contract yet).** CRLF-terminated
  heredocs (`EOF\r` header line) are NOT recognised by
  `findHeredocBodyEnd`'s exact-string terminator match (BUG-081, open) — a
  CRLF heredoc is treated as unterminated and swallows a real, subsequent
  `git commit` invocation as inert, which is a **false ALLOW**, the guard's
  worst failure direction. This AC is written to be RED today (fails,
  correctly): a check that already passes here would be lying about BUG-081's
  status. Do not "fix" this file to make it pass without also closing
  BUG-081 — that would be exactly the "check drifted from the rule" defect
  process v1.9 exists to prevent.
- **AC-14 (known open gap).** A bare word prefix before `git`
  (`sudo`/`env`/`time`/`nice`/`command`/`xargs`/any wrapper not in the
  recognised list) defeats the boundary-anchor requirement entirely — total
  non-detection, ALLOWs a fabricated author (BUG-079, open). Also written to
  be RED today for the same reason as AC-13.

### E. Disclosure and fail-closed posture

- **AC-15.** A deny reason never prints the sanctioned email list — only the
  FIELD name and the COUNT of sanctioned identities (BUG-051's fix). Check:
  `grep -n "sanctioned identit" claude-author-guard.js` shows the message
  uses `sanctioned.size`, never iterates the set into the string; a passing
  test asserts the deny reason for a fixture with a KNOWN sanctioned email
  does not contain that email anywhere in the output text.
- **AC-16.** Any internal error (thrown exception anywhere in `main()`)
  results in DENY via the top-level `try/catch`, never a silent allow. Check:
  a passing test forces `deriveSanctioned()` (or any helper) to throw and
  asserts the process denies rather than exits 0 with no output.
- **AC-17.** `CLAUDE_DISABLE_AUTHOR_GUARD=1` read from `process.env` only —
  never from text inside the proposed command string (same shape as
  `tool.destructiveguard`'s AC-18, and for the identical reason: a
  `PreToolUse` hook evaluates before the proposed command's own shell exists,
  so inline `VAR=value` text in that command is invisible to the hook judging
  it). Check: a passing test sets the var in the TEST PROCESS'S env and
  asserts allow; a passing test puts the literal text
  `CLAUDE_DISABLE_AUTHOR_GUARD=1` inside the simulated command string (real
  process env unset) and asserts the commit is STILL evaluated normally —
  proving the disable cannot be self-granted from within the command it
  gates.

## Out of scope (stated, not silently absent)

- `git commit -C <commit>` / `-c <commit>` (author/message reuse flags) —
  ASM-188, still unhandled, logged again in the guard's own header.
- Command substitution (`` `...` ``/`$(...)`) reaching a nested `git`
  invocation — not defended anywhere in the current parser; not previously
  logged as an ASM under this exact name, so logged fresh in this report.
- A committed, versioned config file for extra identities — the extension
  path is an operator-set env var only (ASM-186).

## Escalations

- **A. No BOW item owns this build.** The three commits that shipped this
  guard tag only `[tool.authorguard]`, never a BOW code, and BUG-035 (the
  incident that motivated it) is still open. This means: (1) the dispatch
  guard's own "criteria already exist, don't re-commission" check (AC-3 of
  `tool.dispatchguard`) has nothing to key off for this module beyond this
  file now existing; (2) there is no closed record anywhere that says
  "this build was reviewed and accepted" — the four rounds of fixes happened
  entirely outside the BA→dev→Tester→Destructive pipeline this project
  otherwise runs. Recommend Bill either retroactively opens and closes a
  FEAT item referencing 511fe70/96d6fe3/c3aa536, or explicitly rules that
  guard-hook builds shipped as direct lead commits are exempt from the
  pipeline (and says so in `dev-team-process.md`, since right now the
  process document doesn't carve out that exception — it just silently
  didn't happen here). Not resolved by this BA; flagged for Bill.
- **B. AC-13/AC-14 are deliberately written RED.** A future implementer must
  not edit this file to make them pass without a corresponding code fix
  landing for BUG-081/BUG-079 — see the note inline at each AC.
