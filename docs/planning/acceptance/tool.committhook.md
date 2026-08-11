BOW code: FEAT-045

# Acceptance criteria — tool.committhook (FEAT-045)

**BOW code:** FEAT-045
**code.json:** FEAT-045's registry entry currently points at `tool.authorguard` (the
existing PreToolUse guard's key) rather than a dedicated key for the new git-native
hook this item introduces. **This file is filed under `tool.committhook` per the
lead's assignment; see the Escalations section — the registry has no `tool.committhook`
module entry yet and needs one registered (or `tool.authorguard`'s `path` field needs
to become plural) before this item can carry a clean single-module GUID.**
**Spec refs:** FEAT-045 (Aaron's ruling, 2026-08-11 — quoted throughout below);
BUG-035 (original incident); BUG-079/080/081/082 (round-4 live bypasses of the
PreToolUse guard that forced the ruling); ASM-350 (why the PreToolUse guard's
command-string parsing cannot be made sound); GR#2 (Version/Identity Discipline);
GR#15 (Validators Derive From Data); `docs/planning/dev-team-process.md`
§"An acceptance criterion's CHECK must be able to fail" (v1.9).
**Date:** 2026-08-11
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** a new git-native hook, installed at `.git/hooks/commit-msg`
(new; **not** `pre-commit` — see AC-1 for why) sourced from a tracked canonical copy
(new; location TBD by junior, e.g. `githooks/commit-msg`, since `.git/hooks/` itself
is never version-controlled); an install/verify script (new); `claude-author-guard.js`
(existing — demoted to advisory, fail-open; this is a rewrite of its decision/output
path, not its command-parsing internals, which are untouched); a new shared module
carrying the sanctioned-identity derivation, extracted out of `claude-author-guard.js`
so both layers require the same code (new — see AC-9).
**Standard gates:** Node.js — `node --check` on every changed/added `.js` file;
`.git/hooks/commit-msg` itself is a POSIX shell or Node script depending on the
junior's implementation choice (either is fine; if Node, `node --check` applies to it
too). SG-6 (no Co-Authored-By). Forbidden-touch: `claude-author-guard.js`,
`claude-author-guard.test.js`, the new hook script and its tracked canonical copy,
the new shared sanctioned-identity module and its test file, the new install/verify
script and its test file, `.claude/settings.json` (only to add an install-check call
if the junior wires one into a session hook — see AC-16), and this item's own test
fixtures. No other file may be touched.

## User stories

- As **Bill**, I need a fabricated commit author to be impossible to land on this
  machine regardless of how the proposed shell command is phrased, so the four
  rounds of `claude-author-guard.js` bypasses (BUG-044..052, BUG-079..082) stop
  being a security control this project depends on for real protection.
- As **an agent whose command is refused**, I need the PreToolUse layer's refusal
  to become a warning, not a block, so a false positive in a text-parsing guard
  never again costs a session the way BUG-043/BUG-082 did — the lead has been
  blocked twice in one day by this guard, once while documenting its own fix.
- As **a future contributor to the sanctioned-identity logic**, I need one place
  that defines "sanctioned," required by both layers, so a fix to the derivation
  can't silently diverge between the advisory and the enforcing copy.
- As **an operator on a fresh clone or after a `.git` directory is rebuilt**, I need
  a way to know the enforcing hook is actually installed, because `.git/hooks/` is
  never checked in and a hook that silently isn't there protects nothing.

## Scope

Three deliverables under one BOW item (parallel to `tool.destructiveguard`'s
two-deliverable structure, extended to three because installation/survival is its
own real surface, not a footnote):

1. **The enforcing git hook** (`.git/hooks/commit-msg`, from a tracked canonical
   source file) — reads git's own resolved identity and denies a code-bearing-or-not
   commit (this item's scope is identity only, not code-bearing detection — that is
   `tool.destructiveguard`'s job and stays out of scope here) whose author or
   committer is not sanctioned.
2. **A shared sanctioned-identity module**, extracted from `claude-author-guard.js`,
   required by both the hook and the guard.
3. **The demoted PreToolUse guard** (`claude-author-guard.js`, rewritten decision
   path) plus **an installation/survival check** proving the hook is actually in
   place and reporting loudly when it is not.

## Acceptance criteria

### A. Hook point — which one, and the evidence it sees the final identity

- **AC-1. The enforcing hook is `commit-msg`, not `pre-commit` and not
  `prepare-commit-msg`.** The rule this pins down: the chosen hook point must be
  the one that (a) can read the fully-resolved author **and** committer identity —
  including a `--author` command-line override and a `-c user.email=…`/`-c
  user.name=…` config override, neither of which are literal file content — and
  (b) fires for every one of this project's already-enumerated commit-creating verbs
  (`commit`, `cherry-pick`, `revert`, `am`, `merge` — the same set
  `claude-author-guard.js`'s `KNOWN_COMMIT_VERBS` already tracks), not merely plain
  `git commit`. Live evidence gathered against real git 2.55 on this machine, in
  throwaway repos, before this file was written: `git var GIT_AUTHOR_IDENT` and
  `git var GIT_COMMITTER_IDENT`, run from inside `pre-commit`, `prepare-commit-msg`,
  `commit-msg`, and `post-commit` hooks, all four correctly reflected a `--author`
  override and a distinct `GIT_COMMITTER_NAME`/`GIT_COMMITTER_EMAIL` override on
  this git version — so *identity resolution alone* does not distinguish the three
  pre-commit-family hooks here (a materially different result from received
  git-hook lore that `pre-commit` can't see `--author`; that lore may be
  version-dependent and is not what this repo's git does). **What distinguishes
  them is verb coverage**, verified live: a real `git merge --no-ff` merge commit
  fired `prepare-commit-msg` and `commit-msg` but did **not** fire `pre-commit` at
  all. A guard installed only at `pre-commit` would therefore never see a merge
  commit's identity — a silent, structural gap for one of the five enumerated
  verbs, discovered the same way BUG-079 discovered "sudo git commit": by actually
  running it, not by reading the manual. `commit-msg` is chosen because it is the
  latest point that (i) still runs before the commit object is written (verified:
  see AC-4) and (ii) is common to all five verbs. Check: a passing test, run
  against a real throwaway repo for **each** of the five `KNOWN_COMMIT_VERBS`
  verbs in turn (plain commit, cherry-pick, revert, am, and a no-ff merge),
  installs only the real hook script under test at `.git/hooks/commit-msg` and
  asserts it is invoked (e.g. by having it touch a marker file) for every one of
  the five. **What a lazy implementation looks like:** installing at `pre-commit`
  because it is the more commonly-known hook name, and testing only plain `commit`
  — passes every AC that only exercises `commit`, silently drops merge-commit
  coverage. This AC's per-verb loop is what rejects that, because the merge case
  in the loop fails for a `pre-commit`-based implementation by construction (git
  itself does not invoke `pre-commit` for that verb — confirmed above), not by any
  weakness of the test.
- **AC-2. The check reads git's own resolution, not any parsed text.** The hook's
  body determines the commit's author and committer by invoking `git var
  GIT_AUTHOR_IDENT` and `git var GIT_COMMITTER_IDENT` (both — BUG-035's original
  finding was that author and committer are separate fields and checking one is a
  hole; that finding carries over unchanged) and extracting the email from each
  resolved identity string — never by reading `GIT_AUTHOR_EMAIL`/`GIT_COMMITTER_EMAIL`
  env vars directly (those are one *input* to the resolution, not the resolution
  itself, and are absent when the override came from `--author` or `-c` rather
  than an env var), and never by re-parsing the proposed shell command in any form
  — there is no shell command visible to a `commit-msg` hook to parse; it receives
  only a path to the commit-message file as `$1`. Check:
  `grep -n "git var GIT_AUTHOR_IDENT" <hook-file>` and `grep -n "git var
  GIT_COMMITTER_IDENT" <hook-file>` both match; `grep -n "GIT_AUTHOR_EMAIL\|GIT_COMMITTER_EMAIL"
  <hook-file>` finds no direct env-var read used as the identity source (the names
  may appear only in comments). **False-pass warning:** a grep for `git var` alone
  would pass an implementation that calls it but then falls back to a raw env-var
  read when the `git var` output doesn't parse — the binding part is that the
  extracted email always comes from the `git var` output string, which a passing
  test proves by setting `--author` **without** any matching `GIT_AUTHOR_EMAIL` env
  var and confirming the hook still denies the fabricated address (a fallback-to-env
  implementation would see no env var, find nothing to deny, and wrongly allow).
- **AC-3. The check can actually fail** — the requirement that a hook which runs
  and always exits 0 is indistinguishable from no hook at all. This is proven the
  same way BUG-079/080/081/082 were proven: by executing a real commit with a
  fabricated identity in a real, throwaway (never this repo, never a repo with a
  tracked remote) git repository and observing the actual outcome, not by
  inspecting the hook's source for a `deny`-shaped branch. Check: a passing test,
  in a throwaway repo with the real hook installed, stages a file, runs `git
  commit -m x --author="Fabricated Author <fabricated@example.invalid>"` (a
  synthetic, obviously-fake address that cannot collide with any sanctioned
  identity — never a real address, and never `test@test.com`, BUG-035's own
  fabricated string, to avoid any future grep conflating fixture noise with a real
  incident) as a real subprocess, and asserts **both** a non-zero exit code **and**
  `git log --oneline | wc -l` unchanged from before the attempt (no commit object
  was created — exit code alone is not proof if the implementation exits non-zero
  for an unrelated reason after already writing the object, which `commit-msg`
  cannot do by construction since it runs before object creation, but the test
  proves the outcome rather than trusting the mechanism). A second passing test
  runs the identical sequence with the *current* sanctioned identity (real `git
  config user.email` on the test fixture) and asserts a zero exit and exactly one
  new commit — proving the check can also **pass**, which matters equally: a hook
  that denies unconditionally is just as indistinguishable from a broken control
  as one that always allows. **What a lazy implementation looks like:** a hook
  that greps its own `$1` message-file argument for the word "fabricated" — passes
  a test built carelessly around that literal string, denies nothing about the
  actual author/committer. This AC's check rejects it because the fixture's
  fabricated identity is asserted via `--author`, not via any special word in the
  commit message text, and a second test using a message that literally contains
  the word "fabricated" with a **sanctioned** author must still be asserted to
  pass — add that as a third fixture in the same test to close this specific
  false-pass path.

### B. The sanctioned set — kept verbatim, shared, not duplicated

- **AC-4. Sanctioned-identity derivation is extracted into one shared module**,
  required by both the new hook and `claude-author-guard.js` — not reimplemented
  in the hook and not left duplicated between the two files. The derivation logic
  itself is unchanged from `claude-author-guard.js`'s existing three sources (read
  its header before writing code, per GR#6): (1) `git config user.email`, local
  then global, trusted unconditionally; (2) emails appearing at or above
  `HISTORY_THRESHOLD` (3) times as author or committer in the trunk's own history,
  scanned over `HISTORY_SCAN_LIMIT` (2000) most-recent commits; (3)
  `CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES`, an operator-set env var. Check:
  `grep -n "require(" <hook-file>` and `grep -n "require(" claude-author-guard.js`
  both resolve to the same shared module path; a passing test changes
  `HISTORY_THRESHOLD` (or an equivalent runtime-configurable value) in the shared
  module's fixture and asserts the change is observed identically through **both**
  the hook's and the guard's exported entry points, proving one codepath rather
  than two copies that happen to agree today. **What a lazy implementation looks
  like:** a shared module that both files `require`, but where the hook
  additionally hardcodes a fallback sanctioned list "in case the module is
  missing" — passes the `require` grep, reintroduces exactly the drift risk this
  AC exists to close. Check rejects it: a passing test deletes/breaks the shared
  module (or monkeypatches it to throw) and asserts the hook does **not** fall
  back to any embedded list — it must fail per AC-13 (fail-closed on internal
  error), not silently use a second, undeclared source of truth.
- **AC-5. Why config is trusted unconditionally and why history alone would be
  wrong here is restated in the shared module's header**, not re-derived from
  first principles by whoever reads the new hook file — the same reasoning
  `claude-author-guard.js`'s header already documents (BUG-036: a frequency-only
  derivation from this repo's own rewritten history would have sanctioned the
  wrong address and bricked every legitimate commit, because `git-filter-repo`
  rewrites history but does not touch local config). Check: header comment present
  in the shared module, reviewed by eye (prose quality is not grep-checkable, and
  this AC says so rather than pretending a keyword match proves it) — but the
  presence of the *specific claim* "history alone would sanction the wrong
  address" (or equivalent) is checkable by grep as a floor, not a substitute for
  the eye review.

### C. The demotion — advisory, fail-open, and what "advisory" means for output

- **AC-6. The PreToolUse guard never denies.** No code path in
  `claude-author-guard.js` may emit `permissionDecision: "deny"` (nor `"ask"`,
  which is also blocking — it stops the agent and waits on a human) for any
  input, including a command string that is a verbatim, byte-for-byte match of
  every one of the fifteen live bypasses already recorded against it
  (BUG-044..052, BUG-079..082). Check: `grep -n "permissionDecision.*['\"]deny['\"]"
  claude-author-guard.js` and `grep -n "permissionDecision.*['\"]ask['\"]"
  claude-author-guard.js` both find **zero** matches anywhere in the file (not
  "zero reachable from the entry point" — zero, full stop, so there is no dead
  branch left half-migrated that a later edit could accidentally reconnect).
  **False-pass warning:** this grep alone would pass an implementation that still
  blocks by a different mechanism — throwing an uncaught exception with a non-zero
  process exit, or writing to stdout a string the harness happens to interpret as
  blocking even without the literal field name. AC-7 and AC-8 close those two
  specific gaps; this AC's grep is a floor, and a reviewer confirming no renamed
  equivalent field exists is required (reviewed by eye) precisely because a grep
  cannot prove a negative against an unknown future field name.
- **AC-7. Every code path exits 0.** A passing test suite runs the guard's full
  existing test corpus (`claude-author-guard.test.js`'s fixtures, unchanged in
  meaning — every command string that used to trigger a `deny`) through the
  rewritten guard and asserts `process.exitCode` (or the observed subprocess exit
  code, matching however the existing test harness already observes it) is `0`
  for every single fixture, with no exceptions, including the fixtures that
  previously asserted a deny. Check: the existing test file's assertions on
  `deny`-shaped output are inverted — replaced with assertions that the same input
  now exits 0 — rather than deleted, so a future regression that reintroduces a
  block on one of the fifteen already-catalogued bypass strings is caught by name.
  **What a lazy implementation looks like:** deleting the deny-path test
  assertions instead of inverting them, so nothing in the suite would notice a
  regression back to blocking. Check rejects it by requiring the same fixture
  strings to still appear in the test file, now asserting the opposite outcome —
  a reviewer diffing the fixture list against the pre-demotion file (both are in
  git history) confirms no fixture silently disappeared.
- **AC-8. Internal errors also exit 0** (the fail-open half of "fail open," not
  just the identity-detected half) — a metro DB read failure, an unreadable git
  config, a `git log` invocation error, an unparseable stdin JSON, or any
  uncaught exception in the shared sanctioned-identity module all result in
  `allow()`/exit 0, the exact inverse of `claude-author-guard.js`'s current
  top-level `try`/`catch` → `deny()` wrapper. Check: `grep -n "catch" <file>`
  shows the guard's outer error handler calling `allow()` (or falling through to
  a default exit-0 path), not `deny()`; a passing test forces the shared module's
  history-scan call to throw and asserts the guard exits 0 with no
  `permissionDecision` output at all (silence, not even an advisory message, is
  an acceptable outcome for an internal-error path — see AC-9 for what "advisory"
  requires when it *does* have something to say).
- **AC-9. "Advisory" means the guard may still say something, but only as
  information, never as a decision the harness could act on as a block.** When
  the guard's existing detection logic (unchanged internals — the regex/tokenizer
  machinery ASM-350 already describes as unsound-by-construction stays exactly as
  unsound as it is; demotion changes what happens with a positive detection, not
  the detection quality itself) would previously have produced a `deny`, the
  advisory path instead either (a) emits `permissionDecision: "allow"` with a
  `permissionDecisionReason` carrying the warning text, or (b) writes a plain
  warning to stderr and exits 0 with no `hookSpecificOutput` at all — either shape
  is acceptable; both are non-blocking by the harness's own contract (`"allow"`
  proceeds without pausing; stderr-only output with exit 0 and no
  `hookSpecificOutput` is treated as silent allow). What is **not** acceptable is
  routing the warning through any field or exit code the harness could interpret
  as pausing or refusing. Check: a passing test feeds a known-bypass fixture
  string (one of the fifteen) and asserts the observed output matches shape (a) or
  (b) above and, either way, that the harness-facing result is allow (same
  assertion mechanism as AC-7); a second passing test asserts the warning text
  itself is non-empty for that fixture (proving "advisory" isn't silently
  degraded to "nothing at all" for the interesting case — a guard that warns about
  nothing is not advisory, it is absent) — the two tests together are what
  distinguish "advisory" from both "still blocks" (AC-7 catches that) and "does
  nothing" (this AC's second half catches that).

### D. The gap between the two layers — stated, not glossed over

- **AC-10. The header of the demoted `claude-author-guard.js` (and/or a short
  shared design note, junior's choice of location, but it must exist somewhere a
  future reader will find it from either file) states plainly what stops being
  caught before it starts.** Required content, not illustrative: a PreToolUse
  guard refuses **before** a commit is attempted — the agent never runs the
  command. A `commit-msg` hook refuses **at** the moment of commit — after `git
  add`, after the message is composed, after any pre-commit tooling (linters,
  formatters wired to `pre-commit`) has already run against the (still
  uncommitted) change. The cost of the later catch: nothing is lost (the index and
  working tree are untouched by a `commit-msg` rejection — verified live in this
  file's own research, see the throwaway-repo evidence cited in AC-1/AC-3) but the
  developer/agent does the message-composition work again. What is genuinely no
  longer caught pre-flight: an agent that would have been warned off attempting
  the commit at all now always reaches git; the **only** remaining hard stop for a
  Bash/PowerShell-mediated commit is the `commit-msg` hook itself. A path that
  bypasses the PreToolUse-mediated tools entirely (a non-Bash/PowerShell commit
  path, or `git commit --no-verify`, which is git's own native, always-available
  hook bypass and this item does not attempt to close it — see Out of scope) was
  never covered by the PreToolUse layer regardless of this change and is not a
  new gap. Check: reviewed by eye against this AC's required-content list (not
  grep-checkable — prose completeness against a list is a human judgement, and
  this AC says so rather than pretending a keyword match proves it); the reviewer
  confirms each of the three required points (before-vs-at, nothing-lost, and the
  `--no-verify` native-bypass caveat) is present, not merely that *a* paragraph
  about the gap exists.

### E. Installation and survival

- **AC-11. The canonical hook source is a tracked file in the repository**, not
  something that exists only inside `.git/hooks/` (which is never committed —
  confirmed: `.git/` is excluded from version control by git's own design, so a
  hook that exists only there cannot survive a fresh clone). Check: `git ls-files`
  includes the canonical source path; `.git/hooks/commit-msg` itself is correctly
  **absent** from `git ls-files` (proving the distinction between the tracked
  source and its untracked installed copy is real, not merely asserted).
- **AC-12. An install step copies (or symlinks, junior's choice, state which and
  why in the header — a symlink is simpler but Windows symlink creation needs
  elevated privileges or Developer Mode by default, which is a real deployability
  concern on this project's own documented environment; a copy needs an explicit
  "did the source change and the copy go stale" check instead) the tracked source
  into `.git/hooks/commit-msg` and makes it executable.** Check: a passing test
  runs the install step against a throwaway repo with no `.git/hooks/commit-msg`
  present, then asserts the file now exists, is executable (POSIX) or otherwise
  invocable by git on this platform, and its content matches the tracked source
  byte-for-byte (or, if a symlink, resolves to it).
- **AC-13. A verify/survival check reports, distinctly, three states**: hook
  present and matching the tracked source (healthy); hook present but not
  matching (stale — the tracked source moved on and the installed copy didn't,
  e.g. after a `git pull` that changed the canonical file); hook absent entirely
  (unprotected). "Silently unprotected" is the failure mode named in the brief —
  the check must not collapse "absent" and "healthy" into the same silence. Check:
  a passing test deletes `.git/hooks/commit-msg` in a throwaway repo and asserts
  the verify check's output explicitly names the "absent" state (not merely a
  non-zero exit code with no distinguishing text — AC-14 needs the human-visible
  distinction, an exit code alone does not give a session-start summary anything
  to print); a passing test corrupts the installed copy (appends a byte) and
  asserts the check names "stale," not "absent" and not "healthy"; a passing test
  runs the check immediately after a real install and asserts "healthy."
  **What a lazy implementation looks like:** a check that only asks "does a file
  exist at `.git/hooks/commit-msg`," treating any existing file (including one a
  local operator hand-edited into uselessness, or an unrelated leftover script
  from before this item existed) as healthy. Check rejects it: the "stale" fixture
  above specifically constructs an existing-but-wrong file and requires the check
  to distinguish it from "healthy," which an existence-only check cannot do.
- **AC-14. The verify check's "absent" and "stale" states are surfaced somewhere
  a human will actually see them without asking** — this project already has a
  session-start summary (`claude-startup.js` / `node claude-bow.js
  startup-summary`, per `CLAUDE.md`'s "MANDATORY: Session Coordination Protocol")
  that prints unconditionally at the start of every session; the verify check
  from AC-13 is wired into that summary (or an equivalent already-unconditional
  surface — junior's choice if a better one exists, but it must not be a surface
  that requires the operator to remember to run a separate command). Check:
  `grep -n` for the verify-check's invocation inside the session-start script's
  source, or an equivalent trace showing it runs unconditionally rather than
  on-demand; a passing test simulates a missing hook and asserts the session-start
  output (captured, not just the function's return value) contains the
  "absent"/unprotected wording. **False-pass warning:** wiring the check into a
  skill or slash command (e.g. `/health-check`) that a human has to remember to
  invoke would pass a shallow "is it wired in somewhere" grep while failing the
  actual requirement ("a hook nobody notices is missing protects nothing" — a
  check nobody runs is exactly that); the check's location must be traced to an
  unconditional startup path, not merely to *a* file that mentions it.
- **AC-15 (residual gap, stated not solved).** `git commit --no-verify` bypasses
  `commit-msg` entirely — this is git's own native, always-available escape
  hatch, present for every hook on every git repository, and this item does not
  attempt to close it (doing so is not possible from a hook, by construction: the
  hook simply is not invoked). The header must state this plainly, matching the
  "local-only, no paired CI check" scope Aaron accepted for this ruling — the
  hook's guarantee is "a commit that goes through normal `git commit` cannot
  fabricate an identity," not "no commit on this machine can ever fabricate an
  identity by any means." Check: header comment present (reviewed by eye).

### F. Error handling — fail-closed, unlike the layer it backstops

- **AC-16.** Unlike the demoted PreToolUse guard (Section C, fail-open), the
  `commit-msg` hook is **fail-closed** — an internal error (git invocation
  failure, the shared sanctioned-identity module throwing, an unreadable commit
  message file) denies the commit. The header must state this contrast by name so
  a future reader does not assume the hook inherited the guard's new fail-open
  posture. Check: header comment present (reviewed by eye); a passing test forces
  the shared module's history scan to throw and asserts the hook's exit code is
  non-zero and no commit is created (same before/after commit-count assertion
  method as AC-3).

## Out of scope

- Code-bearing detection (which paths a commit touches) — that is
  `tool.destructiveguard`'s scope (`FEAT-040`, already shipped), not this item's.
  This hook fires on identity alone, for every commit, regardless of what's
  staged.
- A paired CI authorship check — explicitly ruled out by Aaron
  ("local-only scope is accepted — no paired CI authorship check").
- Closing `git commit --no-verify` as a bypass — not possible from a hook by
  construction; see AC-15.
- Any change to the sanctioned-identity derivation's *logic* (thresholds, sources,
  matching-by-email-only) — AC-4/AC-5 require the existing logic to move and be
  shared, not to change.
- `git rebase` — excluded from both layers, same reasoning
  `claude-author-guard.js`'s header already gives (it replays already-vetted
  commits through internal plumbing rather than any of the enumerated porcelain
  verbs) and unchanged by this item.
- `commit-tree`, `fast-import`, `stash store` — same low-level-plumbing exclusion
  already logged against the PreToolUse guard (`ASM-author-guard-plumbing-verbs-unhandled`),
  unchanged; a `commit-msg` hook additionally cannot see `fast-import`'s stdin
  data stream at all, a structural limit independent of this item.

## Assumptions

Logged via `node claude-bow.js add assumption`, per the brief's requirement:

- **The registry key mismatch (`tool.committhook` vs the registered
  `tool.authorguard`)** — P0, code-path `code.json`, since a commit implementing
  this item needs a real `[mkey]` tag to satisfy GR#2/BUG-042-era commit-message
  discipline and none currently exists for the new hook as a distinct module.
- **Cherry-pick/revert/`am` hook-firing behavior was not independently re-verified
  live this session** (unlike plain `commit` and `merge`, both of which were) — P1,
  because AC-1's per-verb test loop is what actually closes this, not my own
  research; logging so the Tester knows this specific claim in AC-1's prose rests
  on general git documentation plus BUG-075's warning about unverified claims,
  not on a live repro I obtained here (a `cherry-pick` repro in my own testing hit
  an unrelated script bug and I did not chase it down further given the time
  budget) — if wrong, AC-1's per-verb test would fail immediately and visibly,
  which is exactly what that AC is for.
- **Whether `.git/hooks/commit-msg` receiving the message-file path as `$1` is
  itself sufficient for a Node-script hook on Windows without a shebang-interpreting
  shell wrapper** (git for Windows normally handles this via its bundled sh.exe,
  but this item's install step (AC-12) needs to pick a shape that actually
  executes on this machine) was not verified against a Node-authored hook
  specifically (only POSIX shell hooks were used in this file's live research) —
  P2, code-path the new hook script, because the junior building it should verify
  this directly rather than trusting my shell-only tests to generalize.

## Escalations

- **The code.json key mismatch (see header and Assumptions) should go back to
  Ben/Aaron before commit**, not resolved unilaterally by this BA file: either
  register a new `tool.committhook` module entry in the master plan (cleanest —
  the git hook is a genuinely new artifact, distinct in kind from the PreToolUse
  guard it backstops, and conflating them under one key means one module entry
  now describes two files with two different fail-open/fail-closed postures)
  or extend `tool.authorguard`'s existing entry to cover both paths explicitly.
  Flagging, not deciding — this is a registry-structure call, not a criteria call.
- **AC-1's finding that `pre-commit` silently skips merge commits, verified live
  against real git 2.55 on this machine, contradicts the BOW item's own
  pre-filled `Code:` field (`.git/hooks/pre-commit`)** — worth Aaron/Ben seeing
  directly rather than only inferring it from this file's AC-1, since it changes
  which literal file the junior creates.
