BOW code: FEAT-046

# Acceptance criteria — tool.codenameguard (FEAT-046)

**BOW code:** FEAT-046
**code.json:** `tool.codenameguard` (GUID `7ece8624-f4d5-4883-b359-f39ec5a86a1c`, already
registered). FEAT-046's BOW Key field is `tool.codenamehook` — a key with **no**
`code.json` entry (ASM-933). The parent citation matches `code.json`; the BOW key is
an orphaned alias, not a second module. See AC-12.
**Spec refs:** FEAT-046 (this item's own description, quoted throughout below); GR#22
(Codename Discipline — the whole point of this item); GR#3 (Single Source of Truth — no
duplication without validation); GR#15 (Validators Derive From Data — no hardcoded
pattern list separate from the canonical one); FEAT-045 / `tool.committhook.md`
(explicit precedent this item follows — house style, rigor bar, and the hook-point
research this file's AC-1 builds directly on); ASM-386 (commit-msg/pre-commit verb
coverage findings on this machine's git); BUG-061 (analysis naming file-content-paste as
the likeliest GR#22 slip route — the reason this item exists).
**Date:** 2026-08-11
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** a new git-native hook, installed at `.git/hooks/commit-msg` —
**the same hook slot FEAT-045 already occupies for identity checking** (see AC-8; this
is a real, live coexistence problem, not a hypothetical one — `githooks/commit-msg`
already exists in this working tree, built by FEAT-045, currently unmerged pending one
residual-risk sign-off); a new shared pattern-source module extracted from
`claude-codename-guard.js`'s existing `PATTERNS`/fragment-assembly/`scan()` logic,
required by both the new hook and the existing guard (new — see AC-4); a small addition
to the tracked canonical commit-msg source and its install step (existing files, owned
by FEAT-045/Bob's claim — **this BA file does not touch them**, but the ACs below state
what the eventual build must do to them, since the coexistence requirement is a fact
about this item's own scope, not optional).
**Forbidden-touch (this BA file only):** `claude-codename-guard.js`,
`docs/planning/acceptance/tool.committhook.md`, `internal/harness/synth/**`,
`.github/workflows/ci.yml`, `perf-accepted-regressions.json`,
`docs/planning/checkpoint.md`, `githooks/**`, `claude-author-guard.js`,
`claude-author-identity.js`, `claude-committhook-install.js`, `claude-startup.js`,
`.claude/settings.json`, any other acceptance file. (The eventual junior dispatch is
**not** bound by this list in the same way — `claude-codename-guard.js` in particular
must be edited by that dispatch per AC-4/AC-6; this list only constrains what the BA who
wrote this file was permitted to touch while writing it.)

## User stories

- As **Bill**, I need the forbidden reference title to be caught even when it arrives as
  pasted file content rather than command text, so the PreToolUse guard's blind spot
  (BUG-061: content pasted from the BOW into a doc, landing via plain `git add` +
  `git commit -m "..."` with nothing incriminating in the command itself) is closed the
  same way FEAT-045 closed the identity-spoofing blind spot — by inspecting what is
  actually being committed, not what was typed.
- As **a future contributor to the GR#22 pattern set**, I need one place that defines
  "forbidden," required by both the PreToolUse guard and the commit-msg content scan, so
  a pattern added to catch a new bypass can't silently apply to only one layer.
- As **an operator whose commit is denied**, I need the denial to explain what matched
  and how to fix it, the same clarity the existing PreToolUse guard already gives —
  losing that clarity would make the backstop layer worse than the layer it backs up.
- As **an operator relying on this project going public**, I need this control to fail
  closed on its own internal errors, because a codename leak into a public repo cannot
  be withdrawn — the same reasoning `claude-codename-guard.js`'s own header already
  gives for its current fail-closed posture, and the opposite of the fail-open posture
  FEAT-045 gave the demoted identity guard for a different reason (identity checks have
  a cheap human-seconds cost when wrong; a codename leak does not).

## Scope

Two deliverables under one BOW item:

1. **A shared GR#22 pattern-source module**, extracted from `claude-codename-guard.js`'s
   existing `PATTERNS` array and fragment-assembly helpers (e.g. `SAN_FRAN()`), required
   by both `claude-codename-guard.js` (rewritten to `require()` it, behavior unchanged)
   and the new commit-msg content scan. The pattern *values* and fragment-assembly
   *technique* are unchanged by this item — only their location moves (parallel to how
   FEAT-045's AC-4 required `claude-author-guard.js`'s sanctioned-identity derivation to
   move into a shared module without changing its logic).
2. **A commit-msg content scan** that reads `git diff --cached` (added lines only, same
   discipline as the existing guard's own diff scan) and denies the commit on any
   pattern hit, fail-closed on its own internal errors, coexisting at the same hook slot
   FEAT-045's identity check already occupies (AC-8) rather than replacing it.

The existing PreToolUse `claude-codename-guard.js` is **retained, not demoted** — it
keeps denying, keeps failing closed, and keeps its own three checks (command text,
branch name, staged diff) exactly as today. This item adds a second, later-firing layer;
it does not weaken the first one.

## Acceptance criteria

### A. Hook point — verified live, not assumed

- **AC-1. The content-scanning check is installed at `commit-msg`, not `pre-commit`.**
  Live evidence gathered for this file (throwaway repo, real git, out-of-repo scratch
  directory, never this repo): a `pre-commit` and a `commit-msg` hook were installed
  together in the same throwaway repo, each running `git diff --cached --unified=0` and
  reporting the diff's length. For a plain `git commit`, both hooks fired and both
  observed byte-identical staged-diff content (117 characters, matching the one staged
  line). For a real `git merge --no-ff` bringing in a second branch's commit, **only
  `commit-msg` fired** — `pre-commit` produced no output at all, confirming ASM-386's
  finding independently for the content-scanning use case specifically (ASM-386's own
  live testing was about identity resolution via `git var`, not about `git diff
  --cached` visibility; this file's testing closes that gap for the content-scan
  question directly rather than inferring it by analogy). `commit-msg`'s
  `git diff --cached` output during the merge correctly reflected the incoming branch's
  content (124 characters, matching the merge's one added line) — proving the diff seen
  at `commit-msg` is the real incoming content, not a stale or empty view, for the verb
  `pre-commit` cannot see at all. Since a merge can introduce forbidden content exactly
  as easily as a plain commit (GR#22 does not carve out an exemption for merge commits),
  `pre-commit` is structurally insufficient here for the same reason FEAT-045's AC-1
  rejected it for identity. Check: a passing test, run against a real throwaway repo,
  installs only the real content-scan hook under test at `.git/hooks/commit-msg` and
  asserts it fires (e.g. by having it touch a marker file) for both a plain `git commit`
  and a real `git merge --no-ff`. **What a lazy implementation looks like:** installing
  at `pre-commit` because the BOW item's own pre-filled `Code:` field says
  `claude-codename-guard.js` and the item's title says "pre-commit hook" informally —
  and testing only plain `commit`, silently dropping merge-commit content coverage. This
  AC's merge-case test rejects that by construction, the same way FEAT-045's AC-1 merge
  test does.
- **AC-2. The scan reads the staged diff via `git diff --cached`, added lines only —
  never the working tree, never unstaged changes.** This mirrors the existing PreToolUse
  guard's own diff-scanning discipline exactly (scanning whole files would block an
  unrelated fix in a file that still carries an occurrence elsewhere; scanning removed
  lines would block the very commit that deletes a violation). Check: `grep -n
  "diff --cached"` in the new hook's source finds the invocation; a passing test stages
  a file containing a fragment-assembled positive pattern (AC-9) on one line and a clean
  line elsewhere, commits, and asserts the denial names only the added-line occurrence —
  not a false trigger from context lines. A second passing test modifies a file that
  already has a **pre-existing, committed** violation elsewhere in the same file
  (constructed via a bypass such as `--no-verify` in the test fixture setup, never via a
  real unguarded commit) so only unrelated lines are staged, and asserts the commit is
  **not** denied — proving the scan is diff-scoped, not whole-file-scoped.
- **AC-3. The check can actually fail.** Proven the same way FEAT-045's AC-3 and the
  existing PreToolUse guard's own design proves it: a passing test, in a throwaway repo
  with the real hook installed, stages a file whose added content contains a
  fragment-assembled positive (AC-9 — never a real literal), commits, and asserts both a
  non-zero exit code **and** the commit count unchanged from before the attempt (no
  commit object was created — `commit-msg` runs before object creation, but the test
  proves the outcome, not the mechanism, matching this project's verification
  standards). A second passing test runs the identical sequence with clean content and
  asserts a zero exit and exactly one new commit — proving the check can also **pass**.
  **What a lazy implementation looks like:** a hook that greps the commit **message**
  text for a keyword unrelated to the actual diff scan (e.g. the literal word
  "forbidden") — passes a carelessly-built test, denies nothing about the actual staged
  content. This AC's check rejects it because the fixture's positive is asserted via
  staged file content with an unrelated, clean commit message, and a companion fixture
  with a message that literally contains an unrelated flagged-sounding word (e.g.
  "blocked") but clean staged content must still be asserted to **pass**.

### B. Pattern sharing — one source, not two hand-maintained copies

- **AC-4. The GR#22 pattern set is extracted into one shared module**, required by both
  the new commit-msg hook and `claude-codename-guard.js` — not reimplemented in the hook
  and not left duplicated between the two files. The pattern values and the
  fragment-assembly technique (why the forbidden strings are never joined as literals in
  source — see `claude-codename-guard.js`'s own header) are unchanged, only relocated.
  Check: `grep -n "require("` in the new hook's source and in the rewritten
  `claude-codename-guard.js` both resolve to the same shared module path; a passing test
  adds one new pattern to the shared module's fixture (or, if the module is not
  fixture-parameterizable, temporarily monkeypatches its exported pattern list in a test
  double) and asserts the change is observed identically through **both** the hook's and
  the guard's exported scan entry points — proving one codepath, not two copies that
  happen to agree today. **What a lazy implementation looks like:** a shared module that
  both files `require`, but where the new hook additionally hardcodes one or two
  patterns "for defense in depth" or "in case the module changes" — passes the `require`
  grep, reintroduces exactly the drift risk this AC exists to close (GR#3). Check
  rejects it: a passing test breaks/monkeypatches the shared module to export an empty
  pattern list and asserts the hook denies **nothing** (not even the previously-caught
  fragment-assembled positive) — proving no undeclared second source of truth exists.
  Combined with AC-5's fail-closed requirement, "the module is broken" and "the module
  says nothing is forbidden" must be distinguishable: the former denies the commit
  (AC-5), the latter allows it — a passing test for each proves the hook tells them
  apart rather than treating any non-throwing empty-list result as an internal error.
- **AC-5. A future edit to one copy and not the other is mechanically caught** — this is
  the drift test GR#3 requires, stated as its own criterion rather than folded silently
  into AC-4's grep. Check: a passing test asserts that `claude-codename-guard.js` and
  the new hook's source **do not** independently define a pattern literal, fragment, or
  regex matching any of the shared module's exported patterns — e.g. by asserting
  neither file's own source (excluding the `require` line and comments) contains a
  `new RegExp(` or a bare pattern-fragment `const` that duplicates a shared-module
  export's fragments. This is deliberately stricter than AC-4's "both resolve to the
  same require path" grep, because that grep alone would pass a file that requires the
  shared module **and** also defines a second, unused-but-present duplicate list that a
  future edit could accidentally wire back in (the same "lazy implementation" shape
  FEAT-045's AC-4 named for the sanctioned-identity module).
- **AC-6. The retained PreToolUse guard's externally observable behavior is unchanged
  by the extraction.** Since `claude-codename-guard.js` must be edited to consume the
  shared module (AC-4), a regression risk exists that the extraction subtly changes what
  it catches. Check: a passing test suite (new, since none currently exists for this
  file — confirmed via `ls claude-codename-guard*` finding no `.test.js` counterpart)
  exercises the guard's existing three scan surfaces (command text, branch name, staged
  diff) with fragment-assembled positive and negative fixtures (AC-9's discipline
  applies here too) both **before** conceptually and **after** the extraction, asserting
  identical `deny`/`allow` outcomes and identical `permissionDecision` shape (still
  `deny`, never demoted to `allow`-with-warning) for every fixture. **False-pass
  warning:** a test suite written only against the post-extraction file would pass
  trivially without proving anything was preserved — the check is that the same fixture
  set is asserted to behave the same way the file's own header already describes
  (denies, fails closed, disable only via `CLAUDE_DISABLE_CODENAME_GUARD=1`), which a
  reviewer confirms by reading the header claims against the new test assertions, not by
  trusting the test file's existence alone.

### C. Fail-closed — the opposite posture of the demoted identity guard

- **AC-7.** The commit-msg content scan is **fail-closed**: an internal error (a `git
  diff --cached` invocation failure, the shared pattern module throwing or failing to
  load, an unreadable index) denies the commit. This is stated explicitly because it is
  the **opposite** of FEAT-045's demoted `claude-author-guard.js` (fail-open) — a future
  reader must not assume this hook inherited that posture by proximity (same hook slot,
  same era of work). The header must name this contrast explicitly, the same way
  `githooks/commit-msg`'s own header names its contrast with the demoted identity guard.
  Check: header comment present (reviewed by eye) stating fail-closed and naming why
  (content-security check, not an identity check — a codename leak into a public repo
  cannot be withdrawn, unlike an identity mismatch which costs a human seconds to
  correct); a passing test forces the shared pattern module's load/require to throw (or
  monkeypatches the scan function to throw) and asserts the hook's exit code is
  non-zero and no commit is created (same before/after commit-count method as AC-3).

### D. The retained layer and the gap between the two layers — stated, not glossed over

- **AC-8. The two layers coexist at the shared `commit-msg` hook slot without either
  silently disabling the other.** `commit-msg` is a single named hook — git invokes at
  most one script there — and FEAT-045 already occupies it for identity checking
  (`githooks/commit-msg`, unmerged pending sign-off but present in this working tree).
  This item's content scan must not require overwriting that slot with a second,
  unrelated script that silently drops identity checking, nor may it require the
  operator to choose one control over the other. Required shape: the codename content
  scan is invoked from the same occupied `commit-msg` slot as an additional check
  alongside the identity check (the exact wiring — both checks called in sequence from
  one canonical script, or an equivalent dispatcher — is the junior's implementation
  choice, out of this BA's file-ownership to prescribe since it touches
  `githooks/commit-msg`, but the **outcome** is not optional). Check: a passing test, in
  a throwaway repo with **both** controls installed together, proves **all three**
  outcomes independently: (a) a commit with a sanctioned identity and clean content is
  allowed; (b) a commit with a sanctioned identity and forbidden content
  (fragment-assembled, AC-9) is denied by the codename check; (c) a commit with an
  unsanctioned identity and clean content is denied by the identity check. **What a
  lazy implementation looks like:** a build that installs its own `commit-msg` script,
  silently overwriting FEAT-045's, so identity checking stops working the moment
  FEAT-046 lands — passes any test that only exercises codename scanning in isolation.
  This AC's outcome-(c) test rejects it specifically because it is run against the
  **combined** installed state, not against FEAT-046's hook alone.
- **AC-9. Verification uses the real hook contract, fragment-assembled positives, and
  proven negative controls — never a literal forbidden string anywhere in test fixtures,
  source, or fixtures-as-data.** Matching this project's own GR#22 discipline (the
  guard's own header: writing the forbidden literal into git to search for it would
  itself be the violation) and the item's own required-behavior text ("payloads through
  the real hook contract incl. fragment-assembled positives in staged content, negative
  controls that must pass, and prove the detector can fail"). Required, concretely:
  - **(a)** every positive test fixture assembles its forbidden content at test-runtime
    from fragments (the same technique `claude-codename-guard.js`'s own source uses for
    its `PATTERNS`), never as a whole literal typed anywhere — including inside string
    fixtures, JSON test-data files, or code comments explaining the fixture. Check: `git
    grep` (or equivalent) over the new test files for the assembled literal forms
    returns no match — the same class of check this repo's own `claude-codename-guard.js`
    exists to run on every commit, applied here to its own test suite as a build-time
    self-check the Tester runs.
  - **(b)** at least one negative control per scan surface (staged diff — AC-2) using
    ordinary technical prose that is superficially similar to a forbidden fragment but
    does not match (mirroring the existing guard's own documented ambiguity handling —
    the bare two-letter abbreviation case) must be asserted to **pass** (commit allowed).
  - **(c)** the detector's ability to fail is proven per AC-3, not assumed from AC-2's
    positive assertions alone — a distinct test, not a re-read of the same assertion.
  Check (overall): a Tester or reviewer confirms (a)/(b)/(c) each have a distinct,
  independently-runnable test, not one combined test asserting several things where a
  single wrong assertion could mask a missing case.
- **AC-10. Known, disclosed limitation: `cherry-pick`, `revert`, and `am` are
  structurally uncovered by this item, same as FEAT-045.** ASM-386's finding (neither
  `pre-commit` nor `commit-msg` fires for these three verbs on this machine's git) was
  established for identity checking but is a property of git's hook dispatch, not of
  what a given `commit-msg` script does — it applies identically to content scanning.
  This item does not attempt to close that gap (doing so is not possible from a
  `commit-msg` or `pre-commit` hook by construction — the hook is not invoked). The
  header must state this plainly: this hook's real guarantee is "content committed via
  `git commit` or `git merge` cannot introduce a GR#22 violation," not "...via any
  commit-creating verb." Check: header comment present (reviewed by eye) naming the
  three uncovered verbs by name and citing ASM-386, not merely gesturing at "some
  limitations exist."
- **AC-11. The commit-message body itself (as composed via an interactive editor, not
  passed as a `-m` argument) is out of scope for this item's new hook and that gap is
  named, not silently left implicit.** This item's required behavior text scopes the
  content scan to the staged diff (`git diff --cached`) specifically; the PreToolUse
  guard's existing command-text scan already covers `-m`/heredoc message arguments that
  appear in the Bash/PowerShell command string, but cannot see a message composed
  interactively in an editor (no command-string content to scan in that case). The new
  hook, as scoped by this item, does not add message-body scanning either — it inspects
  the diff, not the `commit-msg` hook's own `$1` argument. The result: an editor-composed
  commit message that types the forbidden reference title directly (rather than pasting
  content that lands in a file) is covered by neither layer today. Check: header comment
  present (reviewed by eye) stating this specific gap; **this is flagged as an
  Escalation below**, not resolved by adding message-body scanning unilaterally, since
  expanding scope is a call for the lead/Aaron, not a criteria decision.
- **AC-12 (ASM-933 — FEAT-046 BOW key `tool.codenamehook` is an orphaned alias).**
  FEAT-046's BOW Key field is `tool.codenamehook`; `code.json` holds `tool.codenameguard`
  and does **not** hold `tool.codenamehook`. This file's parent citation is the
  `code.json` key. The BOW key is an orphaned alias, not a second module — no new key
  is invented here. Check: `node claude-bow.js show FEAT-046` Key field equals
  `tool.codenamehook`; `grep -n "\"key\": \"tool.codenamehook\"" code.json` is empty;
  `grep -n "\"key\": \"tool.codenameguard\"" code.json` matches. **False-pass:**
  grepping `codename` in this file would always pass.

## Out of scope

- Command-text scanning, branch-name scanning — already covered by the retained
  PreToolUse `claude-codename-guard.js` (AC-6); this item does not duplicate that
  surface at the commit-msg layer.
- Commit-message body content composed interactively (not via `-m`) — see AC-11,
  escalated below.
- Any change to the GR#22 pattern *values* or the fragment-assembly *technique* — AC-4/
  AC-5 require the existing patterns to move and be shared, not to change.
- A paired CI content check — not requested by this item; if wanted, it is a separate
  BOW item (this item's own scope is local-only, matching the `commit-msg` hook's
  reach).
- Closing `git commit --no-verify` as a bypass — not possible from a hook by
  construction, same reasoning as FEAT-045's AC-15.
- `cherry-pick`/`revert`/`am` coverage — AC-10, disclosed limitation, not solved here.
- `rebase`, `commit-tree`, `fast-import`, `stash store` — same low-level-plumbing
  exclusion already logged against the PreToolUse guard and against FEAT-045's hook,
  unchanged.

## Assumptions

Logged via `node claude-bow.js add assumption`, per the brief's requirement:

- **The exact wiring by which the codename content scan and FEAT-045's identity check
  coexist inside the single `commit-msg` hook slot (one combined canonical script vs. a
  dispatcher that calls two separate check modules) is left to the junior's
  implementation choice** rather than prescribed by this BA file, since prescribing it
  would mean editing/designing `githooks/commit-msg`'s contents, which is FEAT-045's
  claimed file and outside this BA's ownership. AC-8's outcome-based check (all three
  combined-scenario tests must pass) is written so it holds regardless of which wiring
  shape the junior picks. If wrong — i.e. if some wiring shape can satisfy AC-8's tests
  while still leaving a real coexistence gap I haven't anticipated — that would surface
  as a Tester or Destructive finding against AC-8, not as a silent gap, because AC-8's
  checks exercise the actually-installed combined state rather than either hook's logic
  in isolation.
- **Whether `.git/hooks/commit-msg` can practically host two independent Node-based
  check modules without a measurable performance or reliability cost on this machine
  (module load time, subprocess spawn overhead for two `git diff`/`git var` calls per
  commit) was not measured for this file** — P2, code-path the new hook script, since
  this BA file's live research measured hook *firing* and diff *visibility*, not
  combined-hook latency; if wrong (i.e. if this is meaningfully slow), the impact is a
  developer-experience complaint, not a correctness gap, and is cheaply fixable later
  (e.g. combining both `git diff`/`git var` calls into one subprocess round-trip).

```
node claude-bow.js add assumption "Codename-scan/identity-check commit-msg wiring shape left to junior's choice, AC-8 checks the combined outcome not the wiring" --priority P2 --code-path "githooks/commit-msg" --codejson "tool.codenameguard" --desc "This BA file cannot prescribe githooks/commit-msg's contents (FEAT-045's claimed file); AC-8 is written outcome-first so it holds under any wiring shape. If a wiring shape satisfies AC-8's tests while leaving a real gap, that surfaces as a Tester/Destructive finding against AC-8, not silently."

node claude-bow.js add assumption "Combined-hook latency (identity check + codename scan in one commit-msg invocation) not measured by this BA file" --priority P2 --code-path "githooks/commit-msg" --codejson "tool.codenameguard" --desc "Live research here measured hook firing and diff visibility, not combined-hook performance. If slow, it is a UX complaint fixable by combining subprocess calls, not a correctness defect."
```

## Escalations

- **AC-11's gap (editor-composed commit-message bodies scanning neither layer) should
  go to Bill/Aaron before this item's build is considered complete**, not resolved
  unilaterally here: either accept it as a stated, disclosed limitation (parallel to
  AC-10's verb gap) or fold message-body scanning into this item's scope (the hook
  already has `$1` available at zero extra subprocess cost, so the technical lift is
  small — the open question is whether it belongs in this item or a follow-up).
  Flagging, not deciding — this is a scope call, not a criteria call.
- **AC-8's coexistence requirement is worth Bill/Aaron seeing directly**, since it
  is a real, live conflict discovered while writing this file (`githooks/commit-msg`
  already exists, unmerged, claimed by FEAT-045/Bob) rather than a hypothetical risk —
  the two items' build order matters: whichever lands second must not silently regress
  the first, and today neither item's own scope text names the other's occupation of
  the same hook slot.
- **Whether this item's commit-msg content scan should also become part of ASM-386's
  already-ruled pre-push-as-complete-backstop direction** (Bill's ruling on FEAT-045,
  recorded 2026-08-11: "commit-msg stays as the fast-path for commit+merge, and the
  COMPLETE identity backstop moves to the pre-push hook verifying ... across the whole
  push range") is explicitly **not** assumed here — a pre-push content scan over the
  full push range would additionally catch content introduced via `cherry-pick`/
  `revert`/`am` (which, unlike identity, DO produce real commit objects with real diffs
  by the time of push, even though the commit-msg hook never saw them at creation time),
  which would close AC-10's gap more completely than the identity backstop closes its
  analogous gap. This is a materially attractive follow-up but is **not** in this item's
  required-behavior text (which asks for "a git pre-commit hook that scans the STAGED
  DIFF" — a commit-time, not push-time, control), so it is raised here as a suggestion
  for Bill/Aaron's consideration rather than built into these criteria unilaterally.
