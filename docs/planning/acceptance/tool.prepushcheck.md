BOW code: FEAT-110

# Acceptance criteria — tool.prepushcheck (FEAT-110)

**BOW code:** FEAT-110 (`tool.prepushcheck`)
**Module key:** tool.prepushcheck (GUID 53c61c25-b2ac-4ac5-ad15-31f41d9ca9b7)
**Spec refs:** GR#19 (Deploy Bundling — every commit changing deployable
functions must end with the bundled deploy command); M0-ENG §5 (hooks);
SEC-002/SEC-004 (no shell-string interpolation of git-derived values — the
`spawnSync` argv-array pattern this hook applies repo-wide); SEC-012 (the
"main"/"origin" substring gate that made this check a no-op on any other
remote/branch).
**Date:** 2026-08-16 (written after the fact — archaeology; the code shipped
already)
**Status:** retrospective
**Package under test:** `claude-pre-push-check.js` (repo root, Node.js) — a
`PreToolUse` hook wired on both the `Bash` and `PowerShell` matchers in
`.claude/settings.json`.
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. This item's own
gates: `node --check claude-pre-push-check.js`; SG-6 (no Co-Authored-By).
**There is no test file for this hook** — see ASM-735 and Section F (the ACs
below specify the tests to be written).

## What this guard actually guarantees (read from the code, not assumed)

The check answers one question: *do any commits about to be pushed touch
`functions/`, and if so, does each such commit's message bundle a
`firebase deploy --only functions:` line?* Missing the deploy line is a DENY.
Everything else — non-push commands, force-pushes, unresolved destinations,
nothing-to-push, commits that don't touch `functions/`, internal errors — is an
ALLOW (exit 0). It is fail-open on internal error but fail-closed on an actual
positive detection, which the header states plainly: "a hygiene reminder, not a
security gate."

### A. Engagement — which commands it even evaluates

- **AC-1.** Only a real `git push` invocation engages, for **any** remote and
  **any** refspec (SEC-012 fix — no longer gated on the substrings "main" or
  "origin" appearing in the command text). Engagement is boundary-anchored
  (`GIT_PUSH_RE` — start-of-string or immediately after `;`/`&`/`|`/`(`/
  newline) and tolerates the `git -C <dir> push` global-flag form. Check:
  `grep -n "GIT_PUSH_RE" claude-pre-push-check.js` shows the anchored pattern; a
  passing test asserts `git push upstream release` and `git push backup master`
  both engage (the two shapes SEC-012's old substring gate skipped), and
  `git status` / `npm install` do not.
- **AC-2.** A non-`git push` command exits 0 immediately with no stdout. Check:
  a test feeds `{"tool":"Bash","tool_input":{"command":"git status"}}` and
  asserts exit 0, empty stdout.
- **AC-3.** A force-push is exempt, checked as real tokens (`-f`, `--force`,
  `--force-with-lease`, or `--force-with-lease=<ref>`) — not a
  substring-with-trailing-space hack. Check: a passing test asserts
  `git push -f`, `git push --force-with-lease`, and `git push -f` as a *bare
  trailing* token (the shape the pre-SEC-012 version missed) all exit 0 without
  running the deploy-line check.

### B. Destination resolution — the actual remote/branch, not text-sniffed

- **AC-4.** The destination remote and refspec are parsed from the command's
  own positional arguments (`git push [<remote>] [<refspec>]`) via a
  quote-aware tokenizer (`tokenize()`), with flags and their value-arguments
  (`-o`/`--push-option`, `--repo`, `--receive-pack`) skipped; a
  `local:remote` refspec resolves the destination branch to the part after the
  colon. Check: `grep -n "tokenize\|parsePushTarget" claude-pre-push-check.js`
  matches; a passing test asserts `git push origin feature:main` resolves branch
  `main` (not `feature:main`), and a quoted `git push origin "feat ure"` does
  not split on the embedded space.
- **AC-5.** When the remote or branch is omitted, the current branch's tracked
  upstream is resolved via `git rev-parse --abbrev-ref --symbolic-full-name
  @{u}`, and split into remote/branch on the first `/` — never inferred from
  substrings in the command text. Check: a passing test (in a fixture repo with
  a set upstream) asserts `git push` resolves to the tracked remote/branch.
- **AC-6.** All git invocations use `spawnSync` with an argv **array**
  (`shell:false`) — no template-literal string that a shell re-parses, so a
  git-derived value (branch name, commit hash) can never be interpreted as shell
  syntax. Check: `grep -n "spawnSync" claude-pre-push-check.js` shows array-argv
  calls only, and `grep -n "execSync(" claude-pre-push-check.js` finds zero
  call sites (the sole literal mention of `execSync` is a comment recording
  that this pattern *replaced* execSync, which is expected — the binding claim
  is that no git-derived value is ever passed through a shell, not that the
  word never appears in a comment).
- **AC-7.** If the destination cannot be determined (no upstream, ambiguous
  refspec), the hook exits 0 without blocking. Check: a passing test with no
  upstream set and a bare `git push` asserts exit 0.

### C. The pending-commit scan and the deny condition

- **AC-8.** The commits about to be pushed are resolved as
  `git log <remote>/<branch>..HEAD` (commits on HEAD not on the destination
  ref), with no output treated as "nothing to push" → exit 0. Check: a passing
  test in a fixture repo with no ahead-commits asserts exit 0 with no deny.
- **AC-9.** A commit "touches functions/" iff any of its changed files
  (`git show <hash> --name-only --pretty=format:`) starts with the literal
  prefix `functions/`. Check: a passing test stages a commit that touches
  `functions/src/index.js` and one that touches only `docs/x.md`, and asserts
  only the first is considered.
- **AC-10.** The deploy line is detected by the regex
  `/firebase\s+deploy\s+--only\s+functions/i` against the commit's full message
  body (`git show <hash> --pretty=format:%B --no-patch`), case-insensitive.
  Check: a passing test asserts a commit touching `functions/` whose message
  contains `firebase deploy --only functions:...` is ALLOWED, and one whose
  message omits it is DENIED.
- **AC-11.** A commit touching `functions/` whose message omits the deploy line
  produces a DENY: stdout JSON `{ hookSpecificOutput: { hookEventName:
  "PreToolUse", permissionDecision: "deny", permissionDecisionReason: <reason> } }`,
  process exit 0 (denial travels in the JSON payload, per the hook family's
  convention — not a non-zero exit). The reason names the offending commits
  (short hash + subject) and the resolved `remote/branch` target, and instructs
  the fix (amend with the bundled deploy command) or the escape hatch. Check:
  `grep -n "permissionDecision" claude-pre-push-check.js` shows the `"deny"`
  literal; a passing test in a fixture repo with a fabricated
  `functions/`-touching commit asserts deny output naming that commit's short
  hash.

### D. Fail-open on internal error

- **AC-12.** Any parse error or git failure (malformed stdin JSON, a git
  invocation returning non-zero, an unexpected exception) results in a silent
  exit 0 — the outer `try`/`catch`, and the `git()` helper returning `null`
  that is then treated as "skip." Check: `grep -n "catch" claude-pre-push-check.js`
  shows the outer handler exiting 0; a passing test feeds non-JSON stdin and
  asserts exit 0, and a passing test forces the `git` helper to fail (not in a
  repo) and asserts exit 0. **False-pass warning:** a check that only asserts
  "doesn't crash" would pass a build that silently *blocks* on a git hiccup —
  the assertions must pin exit 0 with no stdout, not merely a clean exit.
- **AC-13.** The header states the fail-open posture and its rationale
  ("hygiene reminder, not a security gate") by name, so a future reader does
  not assume this hook is a security control that should fail closed. Check:
  reviewed by eye.

### E. Escape hatch

- **AC-14.** `CLAUDE_DISABLE_PUSH_CHECK=1` read from `process.env` bypasses the
  hook entirely (exit 0 before any processing). Check: `grep -n
  "CLAUDE_DISABLE_PUSH_CHECK" claude-pre-push-check.js` matches; a passing test
  sets the var and asserts a `functions/`-touching fixture exits 0 with no deny.
  The deny reason itself documents this hatch as the operator acknowledgment
  path (e.g. pure-docs commits where no function code actually changed).

### F. Bypass coverage — stated, not glossed

- **AC-15 (residual gap).** `GIT_PUSH_RE`'s boundary class does not handle a
  bare-word prefix before `git` (`env git push`, `sudo git push`, `git.exe
  push`) — the same BUG-088 trigger-parsing family that beat the commit-verb
  guards. Because this hook is fail-open hygiene (a miss means a push proceeds
  without the deploy-reminder, not that a secret/identity control is defeated),
  the severity is lower, but the gap is real and is documented here rather than
  silently ignored. Check: reviewed by eye against the SEC-012 header note.
- **AC-16 (dormancy).** GR#19 is inherited from Prix Six's Firebase stack;
  Metropolis is a Go repository with no `functions/` directory, so the deny
  path is currently **unexercisable** in this repo (no commit can touch
  `functions/`). The check is retained for contract fidelity, not as a live
  control. See ASM-733. Check: `ls functions/` does not exist in this repo; the
  ACs above therefore exercise the deny path against a throwaway fixture repo,
  never against Metropolis's own tree.

### G. Tests (none exist — to be written)

- **AC-17.** A test file is created (junior's name, e.g.
  `claude-pre-push-check.test.js`) that covers at minimum: engagement (push vs
  non-push vs force-push), destination parsing (positional remote/refspec,
  `local:remote`, quoted values, upstream fallback), the pending-commit scan,
  the `functions/` prefix match, the deploy-line regex (allow on match, deny on
  miss), the deny JSON shape, the two escape hatches (env var and force-push),
  and fail-open on malformed stdin / git failure. The deny path must be proven
  able to fail (a mutant that drops the `functions/` prefix check, or that
  returns "clean" on a git failure, must make a test red). Check:
  `node --test claude-pre-push-check.test.js` is green, and the test corpus
  includes an explicit mutant/negative case per `dev-team-process.md`'s "an AC's
  check must be able to fail" rule. See ASM-735.
- **AC-18.** The tests exercise the hook via spawned subprocesses
  (`spawnSync(process.execPath, [scriptPath], { input: JSON.stringify(...) })`)
  feeding the stdin JSON the hook expects, matching the pattern already used by
  `claude-pre-commit-check.test.js` — not by `require()`ing the file (this hook
  has no `require.main === module` guard and runs its stdin listener at load).
  Check: reviewed by eye / the test passes without hanging on stdin.

## Out of scope (stated, not silently absent)

- Any paired CI deploy-bundling check — this is a local PreToolUse reminder, not
  a server-side gate (GR#19's local-hook scope).
- Migrating the hook off the Prix Six `functions/` prefix onto a Metropolis
  deployable surface (e.g. a Go binary deploy step). That is a re-scope of
  GR#19 itself for the Go profile, not this file's retrospective contract — see
  ASM-733.
- Closing the bare-word-prefix engagement gap (AC-15) — a hygiene-reminder
  severity issue, not worth a hardening round until the check governs something
  Metropolis actually deploys.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-733.** GR#19 targets Firebase `functions/` deploys, inherited from Prix
  Six; Metropolis is a Go repo with no `functions/` directory, so the deny path
  is currently unexercisable here. The criteria document the contract as
  inherited, not as a live deny path. What breaks if this is wrong: none — this
  is a scoping disclosure so a Tester knows the deny path must be proven against
  a throwaway fixture repo, not Metropolis's own tree.
- **ASM-735.** No test file exists for this hook. The ACs (AC-17/AC-18)
  specify the tests to be written, including a throwaway repo with a fabricated
  `functions/`-touching commit to exercise the deny path. What breaks if this is
  wrong: nothing today — but a future change to this hook is currently
  unprotected by any regression test, which is exactly the gap this doc closes
  by naming it.

## Escalations

- **The GR#19 "functions/" prefix is a Prix Six-ism that has no referent in
  Metropolis.** The hook is dead weight in this repo today (ASM-733). Flagging
  for Bill/Aaron: either re-scope GR#19 to a Metropolis deployable surface (in
  which case this hook is re-targeted and its tests become load-bearing) or
  retire the hook explicitly, rather than leaving an unexercisable deny path
  silently shipping. Deciding this is an architecture call, not a criteria
  call — this file documents the current contract either way.
- **No test suite means the AC-C17/AC-C18 test build is the actual work this
  item still owes.** The criteria above are retrospective; closing FEAT-110
  properly requires the test file AC-17/AC-18 specify, since a guard with no
  regression test is a guard a future refactor can silently break. Flagging so
  the Tester/Destructive pass knows "documented" is not the same as "verdicted."
