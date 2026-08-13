BOW code: FEAT-028

# Acceptance criteria — tool.secretguard (FEAT-028)

**BOW code:** FEAT-028
**Spec refs:** GR#11 (Pre-Commit Security Review, `CLAUDE.md` line 42); GR#15 (Validators Derive From Data, line 46); M0-ENG §5 (Git — repository & conventions, hygiene/hooks, `docs/METROPOLIS-MASTER-v2.1.md` lines 985-990); `claude-plan-guard.js` (`tool.planguard`, the shipped sibling hook whose deny-JSON convention, fail-closed posture, escape hatch, and BOM-tolerant/commit-scoped-fail-closed stdin parsing this item must match).
**Date:** 2026-08-08
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** `claude-secret-guard.js` (repo root, per `node claude-bow.js show FEAT-028`'s `path:`)
**Standard gates:** see `README.md` for the general convention; this is a **Node.js hook script, not a Go package** — SG-1/SG-2/SG-4/SG-7 (go build/vet/test/determinism-grep) do not apply. This item's own gates are AC-1 through AC-3 below (no new npm dependency, `node --check` syntax validity, and the item's own test suite), plus SG-5 (forbidden-touch) and SG-6 (no Co-Authored-By) unchanged.

## User stories

- As **Bill** (GR#11), I need every `git commit` to be scanned for staged secrets and hardcoding smells mechanically, so security review no longer depends on a human catching it by eye every time.
- As **a developer**, I need the metro DB's documented localhost/root/no-password default and GUID/UUID literals to never trip the guard, so legitimate, already-sanctioned patterns don't block routine commits.
- As **a developer in an emergency**, I need `CLAUDE_DISABLE_SECRET_GUARD=1` as a documented, deliberate escape hatch, so a false positive can never brick a session (mirrors `tool.planguard`'s `CLAUDE_DISABLE_PLAN_GUARD`).
- As **any non-commit Bash command** (`git status`, `npm install`, …), I need to run unaffected by this hook even when its stdin is malformed, so a hook-input hiccup never blocks unrelated work — the lesson `tool.planguard` already learned and hardcoded (commit-scoped fail-closed blast radius).

## Scope

`claude-secret-guard.js`: a `PreToolUse` hook on `Bash` that, for `git commit` commands only, scans `git diff --cached` for private-key blocks, API-key/token patterns, passwords in URLs/connection strings, high-entropy string literals, and GR#15 hardcoding smells; denies with per-line evidence unless every match is allowlisted; wired into `.claude/settings.json` immediately after `claude-plan-guard.js`.

## Acceptance criteria

### Functional

- **AC-1.** `claude-secret-guard.js` exists at the repo root and is valid, syntactically-checkable Node.js. Check: `node --check claude-secret-guard.js` exits 0.
- **AC-2.** It is wired into `.claude/settings.json`'s `PreToolUse` → `Bash` hooks array, positioned **after** `claude-plan-guard.js` and before `claude-pre-push-check.js` (matching the brief's "settings.json wiring after the plan guard"). Check: `grep -n "claude-plan-guard.js\|claude-secret-guard.js\|claude-pre-push-check.js" .claude/settings.json` shows the three in that relative order within the same hooks array.
- **AC-3.** No new npm dependency is introduced: `package.json`'s `dependencies` is unchanged from before this item (still exactly `mysql2`), and `claude-secret-guard.js` uses only Node.js built-in modules. Check: `git diff --cached -- package.json` (at the junior's commit time) shows no new dependency entries, and `grep -n "require(" claude-secret-guard.js` lists only stdlib modules (`fs`, `path`, `crypto`, `child_process`, etc.) — no bare package-name `require()`.
- **AC-4.** Non-`git commit` Bash commands exit 0 immediately with no stdout — matching `claude-plan-guard.js`'s behaviour. Check: a test harness feeds `{"tool":"Bash","tool_input":{"command":"git status"}}` on stdin and asserts exit code 0, empty stdout.
- **AC-5.** For a `git commit` command, the hook inspects **staged content only**, via `git diff --cached` (or an equivalent staged-scope diff/show), never the working-tree unstaged diff and never full-file contents of untracked files. Check: `grep -n "diff --cached\|--staged" claude-secret-guard.js` matches, and a test stages a clean file while leaving an unstaged secret-bearing edit in the same file, then asserts the commit is allowed (the unstaged secret is invisible to the scan).
- **AC-6.** Detects private-key blocks: a staged diff line containing `-----BEGIN ... PRIVATE KEY-----` (RSA/EC/OPENSSH/generic) is flagged. Check: a passing test stages a fixture private-key block and asserts the hook denies.
- **AC-7.** Detects common API-key/token patterns (e.g. `AKIA[0-9A-Z]{16}`, `sk-[A-Za-z0-9]{20,}`, `ghp_[A-Za-z0-9]{36}`, `xox[baprs]-`, generic `[a-zA-Z0-9_-]{32,}` bearer-token-shaped assignments to a variable named like `token`/`key`/`secret`) is flagged. Check: passing tests cover at least 3 distinct pattern families and assert denial for each.
- **AC-8.** Detects passwords in URLs or connection strings (`scheme://user:password@host`, e.g. `mysql://`, `postgres://`, `mongodb(+srv)://`, `redis://`, generic `://.*:.*@`) — flagged **unless** the exact string matches an allowlist entry (AC-11). Check: passing tests cover at least 2 distinct connection-string schemes and assert denial when the password segment is non-trivial (not the documented sanctioned default).
- **AC-9.** Detects high-entropy string literals (Shannon entropy above a documented threshold, over a minimum length to avoid false positives on short tokens) as a heuristic "looks like a secret" signal, separate from the pattern-based checks above. Check: a passing test stages a random 32+ char base64-ish literal and asserts it is flagged as high-entropy; a passing test stages an ordinary English sentence/identifier of similar length and asserts it is NOT flagged.
- **AC-10.** GR#15 hardcoding-smell check: staged code in validator-shaped context (a comparison/assertion against a literal number or string where the surrounding code reads as "expected count/value", e.g. `assert(count === 45)`, `if (total != 1200)`, a bare numeric literal compared against a runtime-computed count) is flagged as a hardcoding smell distinct from the secret-detection categories above. Check: a passing test stages a fixture snippet matching this shape and asserts it is flagged with a category distinguishable from "secret" (e.g. `category: "hardcoding"` in the evidence output), and a passing test stages an equivalent snippet that instead reads the expected value from a data file/config (the GR#15-compliant form) and asserts it is NOT flagged.
- **AC-11.** A committed, documented allowlist file exists (e.g. `data/secret-guard-allowlist.json` or `.secretguardignore` — junior's choice, but it must be a tracked, reviewable file, not an env var or gitignored local file) containing at minimum: (a) the documented metro DB default connection string (localhost/root/no-password, as described in `CLAUDE.md`'s Environment section), and (b) a pattern class for GUID/UUID literals (matching the `[0-9a-f]{8}-[0-9a-f]{4}-...` canonical form used throughout `code.json`/BOW GUIDs) so they are never flagged as high-entropy secrets. Check: `go`... n/a (this is JS) — check: the allowlist file exists, is valid JSON (or documented format), is tracked in git (not `.gitignore`d), and a passing test stages the exact metro DB default connection string plus a sample GUID literal and asserts both are allowed (no denial) specifically because of the allowlist match (verified by a test case that removes/empties the allowlist and re-asserts the same inputs ARE flagged, proving the allowlist — not an accidental pattern gap — is what suppresses them).
- **AC-12.** Allowlist matching is precise, not fuzzy: an allowlist entry matches only the specific documented string/pattern class, not any string that merely contains a substring of it (e.g. allowlisting the exact metro DB default connection string must not also allowlist `mysql://root:realpassword@production-host/metro`). Check: a passing test asserts a similar-but-different connection string (same scheme/user, different host or a non-empty password) is still flagged.
- **AC-13.** Fail-closed deny includes per-line evidence: the denial reason names the file, the line number(s), and the detection category (private-key / api-key / connection-string-password / high-entropy / hardcoding-smell) for every match, not just an aggregate "secrets found" message. Check: a passing test with 2 distinct findings in 2 different files asserts the deny reason enumerates both, each with file+line+category.
- **AC-14.** `CLAUDE_DISABLE_SECRET_GUARD=1` bypasses the guard entirely (same posture as `tool.planguard`'s `CLAUDE_DISABLE_PLAN_GUARD`) — check: `grep -n "CLAUDE_DISABLE_SECRET_GUARD" claude-secret-guard.js` matches, and a passing test sets the env var, stages an obvious private-key fixture, and asserts the commit is allowed (exit 0, no deny output).

### Error handling

- **AC-15.** Unparseable stdin JSON does **not** deny non-commit-looking input — the fail-closed posture is scoped to commits only (the lead-review lesson from `tool.planguard`'s BOM incident): a raw-substring fallback checks whether the unparsed input text contains `git commit`; if not, exit 0 immediately. Check: `grep -n "git commit" claude-secret-guard.js` shows this fallback substring check present in the catch/parse-failure path (mirroring `claude-plan-guard.js` lines 96-109), and a passing test feeds garbage (non-JSON) stdin with no `git commit` substring and asserts exit 0.
- **AC-16.** Unparseable stdin JSON that **does** contain `git commit` as a raw substring denies (fail-closed) with a clear message that input was unparseable — matching `claude-plan-guard.js`'s exact fallback behaviour. Check: a passing test feeds garbage stdin containing the substring `git commit` and asserts a deny with a message naming the parse failure.
- **AC-17.** BOM-tolerant stdin parsing: a UTF-8 BOM (`﻿`) prepended to stdin (as PowerShell pipes do) is stripped before `JSON.parse`, matching `claude-plan-guard.js`'s `input.replace(/^﻿/, '')`. Check: `grep -n "\\\\uFEFF" claude-secret-guard.js` matches, and a passing test prepends a BOM to valid JSON stdin and asserts the hook parses it correctly (does not fall into the unparseable-input path).
- **AC-18.** An internal error during scanning (e.g. `git diff --cached` itself fails — not in a repo, git not on PATH) results in a DENY for a `git commit` command, fail-closed by design (mirroring `claude-plan-guard.js`'s top-level try/catch → deny), with the error message attached. Check: a passing test mocks/forces the `git diff --cached` call to fail and asserts a `git commit` command is denied with the underlying error surfaced.
- **AC-19.** Deny output uses the exact `hookSpecificOutput` JSON convention: `{ hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: "..." } }`, written to stdout, process exits 0 (not a non-zero exit — denial is communicated via the JSON payload, per the existing hooks' convention). Check: `grep -n "hookSpecificOutput\|permissionDecision" claude-secret-guard.js` matches the same shape as `claude-plan-guard.js`'s `deny()` helper.

### Determinism & safety

- **AC-20.** Scanning the same staged diff twice (no changes between runs) produces identical verdicts and identical evidence output — no randomness, no wall-clock-dependent detection logic. Check: a passing test runs the scan function twice against the same fixture diff and asserts byte-identical output.
- **AC-21.** The guard never modifies the working tree, the index, or any file — it is read-only with respect to the repository (unlike `claude-plan-guard.js`, which deliberately regenerates files; this guard has no equivalent write step). Check: a passing test snapshots `git status --porcelain` before and after invoking the guard against a fixture and asserts no change.

### Documentation

- **AC-22.** `claude-secret-guard.js` carries a header comment in the same style as `claude-plan-guard.js`: BOW mkey (`tool.secretguard`), spec refs (GR#11, GR#15, M0-ENG §5), a behaviour summary, an explicit fail-closed-posture note scoped to commit-only, and the escape-hatch env var name.
- **AC-23.** The allowlist file documents its own format inline (a comment block or adjacent `.md`) so a future contributor can add a new sanctioned pattern correctly (what fields are required, whether entries are exact-string or regex, how GUID-class entries differ from literal-string entries).

## Out of scope

- Any secret-scanning of files outside `git diff --cached`'s staged scope (e.g. scanning the whole working tree, or historical commits) — that is a separate, heavier audit (`/security-audit` skill territory), not this hook's job.
- Automatic remediation (stripping/redacting the secret from the diff, auto-rotating credentials) — this item only denies with evidence; fixing is the developer's job.
- A configurable severity/threshold UI — the entropy threshold and pattern list are code/data-file constants for v1, not a runtime-tunable setting.
- CLAUDE.md's hooks table being updated to list this new hook — that belongs to the Documentation role's pass, not this item's junior.

## Escalations

- None at draft time. This item's criteria were written before dispatch per normal pipeline order; no spec/brief conflict found. One judgement call flagged for Bill's awareness rather than as a conflict: AC-19 mandates the `hookSpecificOutput` deny convention with `process.exit(0)` (matching `claude-plan-guard.js`) rather than a non-zero process exit code, since that is how the existing hook family communicates denial to the harness — the BOW item description didn't specify this explicitly, but deviating from the shipped sibling's convention would be inconsistent with "same posture as tool.planguard" in the brief.

---

# BOW code: BUG-088

# Acceptance criteria — BUG-088 remediation (four guards' shared `GIT_COMMIT_RE`/`isRealGitCommit` false-negative class)

**BOW code:** BUG-088 (P0)
**Filed under:** `docs/planning/acceptance/tool.secretguard.md` per `code.json:tool.secretguard`
(BUG-088's primary tag), covering the remediation of **all four** files it names —
`claude-pre-commit-check.js`, `claude-secret-guard.js`, `claude-version-guard.js`,
`claude-plan-guard.js` — not secret-guard alone. `claude-plan-guard.js`'s own
acceptance file (`tool.planguard.md`) is owned by a different BA and is **not**
touched by this section; the plan-guard remediation criteria live here instead,
per this item's explicit filing instruction.
**Spec refs:** BUG-088 (this item, GIT_COMMIT_RE boundary false-negative, ONE
finding across four files); `docs/planning/acceptance/tool.committhook.md`
(FEAT-045, the precedent this section follows — hook-point selection method,
AC numbering/rigor style, fail-open/fail-closed framing); `claude-author-guard.js`
(current, demoted/advisory state — read in full before building); ASM-386 (the
commit-msg/pre-commit verb-coverage gap for cherry-pick/revert/am, live-verified
on this machine's git 2.55.0.windows.3); GR#3 (Single Source of Truth); GR#15
(Validators Derive From Data); GR#23 (Nothing Is Committed Un-Attacked — the
Destructive verdict this item's own build will need before it can be committed).
**Date:** 2026-08-11
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** four existing hook scripts (repo root, unchanged file
identities — see AC-14 for what "unchanged" means here); a new commit-msg
dispatch integration point (see Section C — this section does **not** create or
edit `githooks/commit-msg` itself, FEAT-045's file; it specifies the checker
modules a follow-on integration step wires into it); four new "checker" modules
extracted from the four guards' existing payload-inspection logic (new files,
junior's naming, one per domain — see AC-6).
**Standard gates:** Node.js — `node --check` on every changed/added `.js` file;
this item's own test suite; SG-6 (no Co-Authored-By). Forbidden-touch:
`githooks/commit-msg`, `claude-committhook-install.js`, `claude-author-guard.js`,
`claude-author-guard.test.js`, `claude-author-identity.js`, `claude-startup.js`,
`.claude/settings.json`, `docs/planning/acceptance/tool.committhook.md`,
`docs/planning/acceptance/tool.planguard.md`, `docs/planning/checkpoint.md` — all
FEAT-045's/other BAs'/Bill's files, per this item's brief. **This means the
actual wiring of the new checker modules into `.git/hooks/commit-msg` is
explicitly OUT of this dispatch's file set** — see the Escalations section.

## User stories

- As **Bill**, I need the same false-negative class that beat `claude-author-guard.js`
  fifteen times to stop being live against its four siblings, so a public repo's
  secret-leak and registry-drift protections do not silently no-op behind `env `,
  a shell wrapper, or `git.exe`, the exact way `claude-author-guard.js` did.
- As **a developer whose secret-bearing commit would previously have slipped
  through** any of the fifteen already-catalogued bypass shapes, I need
  `claude-secret-guard.js`'s scan to run **unconditionally** at the moment of
  commit, regardless of how `git commit` was invoked, so a public repo's worst
  documented outcome in this class (BUG-088's own framing) cannot recur.
- As **a future maintainer of these four checks**, I need each check's actual
  payload-inspection logic (the part that decides pass/fail) kept exactly where
  it already lives and already works — this item fixes the unreliable *trigger*
  that decides whether the check runs at all, not the checks themselves, which
  were never the defect BUG-088 found.
- As **an operator who already accepted FEAT-045's residual gaps** (ASM-386's
  cherry-pick/revert/am non-firing, `--no-verify`'s native bypass), I need this
  item's design to inherit those same, already-disclosed limitations rather than
  silently re-promise coverage FEAT-045 itself does not yet have.

## The architectural distinction this section rests on (read before the ACs)

`claude-author-guard.js`'s bypasses were fatal in **two** independent ways: the
*trigger* (`isRealGitCommit`, deciding whether the guard engages at all) was
parsed from the proposed command string, and the *payload* (the author/committer
identity itself — `--author`, `-c user.email=`, an env var) was **also** parsed
from that same unreliable command string. Fixing the trigger alone would not
have fixed identity detection, because the payload was equally unsound; FEAT-045
therefore had to move to a hook that resolves identity via `git var
GIT_AUTHOR_IDENT`/`GIT_COMMITTER_IDENT` — git's own resolution, not a re-parse.

Auditing the four BUG-088 guards against this same two-part question, live in
this repo's own source (see `claude-secret-guard.js`, `claude-version-guard.js`,
`claude-plan-guard.js`, `claude-pre-commit-check.js`, all read in full for this
BA pass), finds an asymmetry that changes the fix's shape per check:

| Guard | Trigger source | Payload source | Payload sound? |
|---|---|---|---|
| `claude-secret-guard.js` | `isRealGitCommit()` — command text | `git diff --cached` (real git state, read from disk via `spawnSync`) | **Yes** |
| `claude-plan-guard.js` | `isRealGitCommit()` — command text | `code.json`/`bow-import.json` on disk, regenerated via `tools/plan/generate.js` and hash-compared | **Yes** |
| `claude-version-guard.js` | `isRealGitCommit()` — command text | `git diff --cached --name-only` + per-file `git diff --cached -- <path>` (real git state) | **Yes** |
| `claude-pre-commit-check.js` | `isRealGitCommit()` — command text | `-m`/`--message` values, `-F`/`--file` targets, heredoc bodies — **all extracted from the same command-text string**, plus three documented residual extraction gaps (bare `git commit` with no message source, `-F -` fed by a plain pipe) | **No — same class as identity** |

**What this means for the fix:** for the first three, BUG-088's finding is
entirely a **trigger** defect — the check, once it runs, is already reading real
git/filesystem state and is trustworthy. Moving the trigger to a hook point that
fires unconditionally (no text-parsing engage decision at all) closes the finding
completely for those three, and their existing scan/diff/hash logic is reused
essentially unchanged. `claude-pre-commit-check.js` is a **different, harder**
case: it is `claude-author-guard.js`'s twin, not its sibling — both trigger and
payload need to move, because the payload itself cannot be trusted independent
of the trigger's own text-parsing.

## Acceptance criteria

### A. Hook point — chosen and evidence-verified, not received wisdom

- **AC-B1. The enforcing hook point for all four checks is `commit-msg`**,
  matching FEAT-045's own choice for identity, not a second, different hook
  point invented for this item. Live evidence gathered for this file (throwaway
  repo, never this project's tracked history, deleted after): a real `commit-msg`
  hook script that shells out to `git diff --cached --name-only` and
  `git diff --cached` (no arguments beyond scope) was installed and invoked
  during a real, non-interactive `git commit` that staged a modified tracked
  file and a new untracked-then-staged file; its stderr output showed **both**
  files by name and the **full, correct added-line diff content** for both —
  proving a `commit-msg` hook sees exactly the staged state a `pre-commit` hook
  would, which is the specific claim the brief asked to be verified rather than
  assumed (a `commit-msg` hook is late enough that "the diff might already be
  gone" was the reasonable-sounding but wrong assumption this AC rules out). A
  second, separate throwaway-repo run confirmed `commit-msg` receives the
  **final, fully composed commit message** as a real file at `$1` (observed
  resolving to `.git/COMMIT_EDITMSG`), correct and complete, regardless of
  whether the message was supplied via `-m`, `-F`, a heredoc, or (implicitly)
  an editor — this is what AC-D1 below builds on for the trailer check. Check:
  a passing test, run against a real throwaway repo, installs the real hook
  script under test at `.git/hooks/commit-msg`, stages a fixture matching each
  of the four checks' positive-detection fixtures in turn (a secret-shaped
  literal, a plan-drift condition, a hand-maintained version file, a
  Co-Authored-By trailer), and asserts each is caught **regardless of which
  bypass shape from BUG-088's own list (`env git commit`, `git.exe commit`,
  a `bash -c` wrapper, a quoted full path, an inline `VAR=val` prefix) invoked
  the commit** — proving the fix is trigger-reliability, not merely
  "the check still works when called directly."
  **What a lazy implementation looks like:** re-testing only the plain
  `git commit -m x` shape (which already worked before this item, since the
  bypass only ever affected the *other* shapes) — passes every AC that only
  exercises the unbypassed form, silently leaves BUG-088's actual reported
  defect (the bypass shapes) unverified. This AC's per-bypass-shape loop is
  what rejects that.
- **AC-B2. The known verb-coverage gap (ASM-386) is inherited, not re-solved,
  and is stated plainly in every checker module's header.** `commit-msg` does
  not fire for `git cherry-pick`/`git revert`/`git am` on this machine's git
  2.55.0.windows.3 (verified three independent ways per ASM-386's own comment
  thread — not re-verified again here, since re-litigating an already
  live-confirmed finding would waste a build cycle FEAT-045 already paid for).
  This item's design does **not** attempt to close that gap — see Out of scope
  and the Escalations section for why, and for what changes if Aaron accepts
  Bill's pre-push-backstop recommendation on FEAT-045. Check: each of the four
  new checker modules' header comments states, by name, that cherry-pick/
  revert/am bypass this enforcement point entirely on this project's git
  version, citing ASM-386 — reviewed by eye (prose completeness against a
  named list, same as `tool.committhook.md` AC-10's check style).

### B. One enforcing mechanism (GR#3), not four independently-wired checks

- **AC-B3. Each of the four checks' payload-inspection logic is extracted into
  its own standalone, requireable "checker" module** (`claude-secret-checker.js`,
  `claude-plan-checker.js`, `claude-version-checker.js`,
  `claude-trailer-checker.js` — or equivalent names, junior's choice, but one
  module per domain, not one merged file covering all four; merging unrelated
  domains into a single file would itself violate GR#3's "single source of
  truth" by forcing a change to one domain's logic to touch a file three other
  domains also depend on). Each module exports a single check function taking
  no arguments beyond what it needs to read from disk/git (no command-string
  argument anywhere in any of the four modules' signatures — the whole point of
  this item is that none of them need one any more) and returning a structured
  result (findings array, or a single deny/allow verdict — junior's choice per
  domain, matching what each existing guard already returns). This mirrors
  `tool.committhook.md`'s AC-4 (one shared module for identity, required by
  both layers) applied per-domain rather than globally, because these four
  domains are not one shared concern the way sanctioned-identity is — forcing
  them into one module would create exactly the kind of accidental coupling
  GR#3 warns against. Check: `require(...)` of each new checker module from a
  test file resolves and returns a result without touching `process.stdin` or
  invoking `main()` in the corresponding original guard file (same
  `require.main === module` isolation pattern all four existing guards already
  use for testability) — a passing test proves this for all four.
- **AC-B4. The four checkers do not duplicate the boundary-parsing machinery
  they are replacing.** None of the four new checker modules contains a copy of
  `GIT_COMMIT_RE`, `buildQuoteMask`, `isRealGitCommit`, or any of the
  wrapper/heredoc/quote-tracking logic those four functions pull in — that
  machinery existed **only** to decide whether to engage, and a `commit-msg`
  hook has no engage decision to make (git itself only invokes it for a real
  commit-creating verb it recognises). Check:
  `grep -rn "buildQuoteMask\|GIT_COMMIT_RE\|isRealGitCommit" claude-secret-checker.js claude-plan-checker.js claude-version-checker.js claude-trailer-checker.js`
  (or the junior's actual filenames) finds **zero** matches across all four —
  not "present but unused," genuinely absent, because a present-but-dead copy
  is exactly the kind of orphaned machinery GR#18 (Migration Dead-Code Audit)
  exists to catch, and leaving it in "just in case" would misrepresent to a
  future reader that trigger-parsing is still part of this design. **What a
  lazy implementation looks like:** copying the existing guard file wholesale
  into the new checker module name and just skipping the `isRealGitCommit()`
  call at the top — passes a shallow "does the checker work" test while
  leaving ~150 lines of now-pointless quote/wrapper/heredoc parsing per file,
  a maintenance trap for whoever next touches "BUG-088's siblings." The grep
  above rejects that directly.
- **AC-B5. The four checker modules are designed to be invoked from ONE
  physical git hook** (`commit-msg` — there is no such thing as two
  `commit-msg` scripts; git invokes exactly one file at that path), consistent
  with FEAT-045's existing `githooks/commit-msg`. This section does **not**
  implement that invocation itself (forbidden-touch, see header) — it defines
  the checker modules' call contract precisely enough that a follow-on
  integration dispatch can require() all four (plus FEAT-045's existing
  identity check) from a shared dispatcher loop without further design
  decisions. Check: each checker module's exported function signature and
  return shape is documented in its header comment in enough detail that a
  reviewer can write the dispatcher's calling code from the header alone,
  without reading the module's implementation — reviewed by eye (prose
  completeness, not grep-checkable, same as `tool.committhook.md`'s AC-5).

### C. Fail-open vs fail-closed — decided per check, not inherited wholesale from identity

**The rule this table encodes:** identity's fail-open PreToolUse / fail-closed
hook split was a decision about *false-positive cost vs missed-detection cost*
for **that specific check** (an identity false-positive has no cheap remedy;
a session blocked by one cost real time twice already). That reasoning does not
transfer mechanically to four checks with different remedy shapes and different
consequences of a miss:

| Check | PreToolUse fate | Hook-layer internal-error posture | Why |
|---|---|---|---|
| `claude-secret-guard.js` | **Stays blocking (fail-closed), NOT demoted** | **Fail-closed** | A secret false positive has a cheap, documented remedy (`claude-secret-guard.allow.json`, AC-11/AC-12 of this file's FEAT-028 section, already shipped) — unlike identity, whose false positives had no equivalent. A missed secret on a public repo is this whole item's own "worst outcome in the class" framing. Payload is already sound (table above); only the trigger was broken, and the trigger fix does not require demoting the check itself. |
| `claude-plan-guard.js` | **Stays blocking (fail-closed), NOT demoted** | **Fail-closed** | Its check is a deterministic regenerate-and-hash-compare — a "false positive" here is not a heuristic misfire, it is real drift; there is close to no false-positive cost to weigh against. Registry integrity (GR#3/GR#6) is the same severity class as identity. |
| `claude-version-guard.js` | **Stays exactly as-is (unchanged by this item)** | **Fail-open on internal errors** (unchanged from its current, already-documented posture) — **but still DENIES on an actual positive detection** (a real hand-maintained version file staged); "fail-open" here describes only the internal-error path, not detection results, same distinction its current header already draws | This is the one check that must **not** get identity's fail-closed answer, deliberately: it is explicitly documented (see the file's own header) as "a hygiene check, not a security gate," and inheriting fail-closed-on-error here would raise its blast radius on a hook bug beyond what the check's own severity justifies. |
| `claude-pre-commit-check.js` | **Demoted to advisory-only, matching `claude-author-guard.js` exactly** | **Fail-closed** | Table above: this is the one check whose *payload*, not only its trigger, came from unreliable command-text parsing (three documented extraction gaps: bare `git commit`, `-F -` via a plain pipe). It is architecturally `claude-author-guard.js`'s twin, and gets the same demotion for the same reason — moving to `commit-msg` does not just fix a trigger, it also replaces the extraction machinery with something categorically more reliable (the final composed message, read directly, per AC-B1's live evidence), so the PreToolUse copy's own detection is no longer worth trusting as a blocking control once the hook exists. |

- **AC-C1.** `claude-secret-guard.js` and `claude-plan-guard.js`'s existing
  PreToolUse behaviour is **unchanged by this item** except for whatever the
  junior's refactor into a shared checker module requires mechanically (see
  AC-B3 — the guard file itself now calls into the extracted checker rather
  than inlining the scan, but observable behaviour at the PreToolUse layer is
  identical). Check: the existing test suites for both files
  (`claude-secret-guard.test.js`... — junior confirms actual filename —
  and any plan-guard test fixtures) pass unmodified in their assertions (only
  their `require()` target may change if the refactor moves code between
  files), proving no PreToolUse-observable behaviour regressed.
- **AC-C2.** `claude-version-guard.js`'s existing PreToolUse behaviour is
  **unchanged by this item**, same proof method as AC-C1.
- **AC-C3.** `claude-pre-commit-check.js`'s PreToolUse layer is rewritten to
  the exact demotion shape `claude-author-guard.js` already established
  (`tool.committhook.md` Section C, AC-6 through AC-9, reused here rather than
  reinvented): no code path emits a blocking `permissionDecision` value; every
  path exits 0; a positive trailer-shaped detection becomes a non-blocking
  advisory (`permissionDecision: "allow"` with a reason, or a stderr-only
  warning) rather than a deny. Check: the same grep/test-inversion method
  `tool.committhook.md`'s AC-6/AC-7 already specify, applied to
  `claude-pre-commit-check.js`: `grep -n "permissionDecision.*['\"]deny['\"]"
  claude-pre-commit-check.js` and the `"ask"` equivalent both find zero
  matches; the existing test file's deny-shaped assertions are inverted (not
  deleted) to assert exit 0 for the same fixture strings, so a future
  regression back to blocking is caught by name, not silently unnoticed.
  **False-pass warning:** deleting rather than inverting the old assertions
  would pass a shallow "grep for deny finds nothing" check while leaving no
  regression test for the exact fifteen-bypass-class fixtures this guard
  already has — the check explicitly requires inversion, not deletion, for
  the same reason `tool.committhook.md` AC-7 does.

### D. The new commit-msg checks — what each actually inspects

- **AC-D1. Trailer checker reads `$1` directly, drops all extraction
  machinery.** `claude-trailer-checker.js` (or equivalent name) reads the
  message file path passed as the hook's first argument, scans its full text
  for `Co[- ]Authored[- ]By\s*:` (the existing `TRAILER_RE`, unchanged), and
  returns a deny/allow verdict — with `extractMFlagMessages`,
  `extractFileFlagPaths`, `extractHeredocBodies`, `MSG_FLAG_RE`, `FILE_FLAG_RE`,
  and `HEREDOC_RE` **not** carried into the new module (AC-B4's dead-code rule
  applies here specifically — this is the concrete instance of it). Check: a
  passing test writes a fixture message file to disk (simulating `$1`), calls
  the checker with that path, and asserts detection for a trailer present
  anywhere in the file's text — a single test replaces what used to require
  three separate extraction-path test suites (`-m`, `-F`, heredoc), because
  `commit-msg` collapses all three into "read the one file." A second passing
  test asserts the three previously-catalogued residual gaps (bare `git
  commit` with no message source, `-F -` via a plain pipe) **no longer exist**
  as gaps at all — there is no such thing as "couldn't find the message" at
  this hook point, since git always writes `COMMIT_EDITMSG` before invoking
  `commit-msg`, regardless of which flag supplied it.
- **AC-D2. Secret checker's payload logic is unchanged, only relocated.**
  `claude-secret-checker.js` exports the existing `runScan`/`scanLine`/
  allowlist-matching pipeline from `claude-secret-guard.js`, verbatim in
  behaviour (same detectors, same entropy threshold, same allowlist file,
  same redaction), reachable without going through `isRealGitCommit`. Check:
  a passing test stages each of the FEAT-028 section's existing positive-
  detection fixtures (private key, API key pattern, connection string,
  high-entropy literal, hardcoding smell) in a throwaway repo, invokes the
  new checker directly (no hook plumbing), and asserts identical findings
  (same categories, same file/line) to what `claude-secret-guard.js`'s
  existing `runScan()` produces against the same fixture — proving relocation,
  not reimplementation, which is the failure mode AC-B4's "copy and skip the
  trigger" lazy-implementation warning also applies to here: a subtly
  reimplemented scan that "mostly" matches would pass a shallow smoke test
  while silently narrowing coverage.
- **AC-D3. Version checker's payload logic is unchanged, only relocated.**
  `claude-version-checker.js` exports `claude-version-guard.js`'s existing
  hand-maintained-file / hardcoded-semver detection (`isHandMaintainedVersionFile`,
  `stagedDiffHasHardcodedSemver`, the exemption pattern list), same proof
  method as AC-D2 (fixture parity test between old and new).
- **AC-D4. Plan checker's payload logic is unchanged, only relocated.**
  `claude-plan-checker.js` exports `claude-plan-guard.js`'s existing
  regenerate-and-hash-compare logic (`hashFiles`, the `generate.js --check`
  and full-regenerate `spawnSync` calls), same proof method as AC-D2. One
  documented divergence, stated in the module's header rather than left
  implicit: at `commit-msg` time the regeneration side-effect (rewriting
  `code.json`/`bow-import.json` on disk) happens **after** the commit's tree
  is already fixed (unlike at `pre-commit` time, where the same side-effect
  happens before `git write-tree`) — a `commit-msg`-time regeneration can
  refresh files that will **not** be part of THIS commit even if the check
  denies, exactly matching `tool.committhook.md` AC-10's own "before vs at"
  disclosure for identity, applied to this check's specific write behaviour.
  Check: header comment present, reviewed by eye against this specific claim.

### E. Cross-check consistency

- **AC-E1. All four checkers use the same deny/allow-and-reason shape** so a
  future dispatcher (Section B) can treat them uniformly: each exported check
  function returns (not prints) a result object distinguishing "clean," "found
  problems" (with an evidence array/string), and "internal error" (with the
  error), leaving the actual `hookSpecificOutput`/stdout/exit-code emission to
  the caller (the PreToolUse guard file for the unchanged three, or the future
  commit-msg dispatcher). Check: a passing test asserts each of the four
  checkers' return shape has the same three-state discriminant (e.g. a
  `status` field with the same three literal values across all four modules),
  not four independently-shaped return objects that a caller has to
  special-case per domain.

### F. Error handling — matches Section C's per-check table, not one blanket answer

- **AC-F1.** Each checker module's own internal-error handling matches its
  row in Section C's table: `claude-secret-checker.js` and
  `claude-plan-checker.js` propagate an internal error as their "internal
  error" result state (never silently downgraded to "clean"); `claude-
  version-checker.js` treats an internal error (e.g. a `git diff` invocation
  failure) the same way its current guard already does — logged/surfaced, not
  silently swallowed, but not itself a positive detection; `claude-trailer-
  checker.js` treats an unreadable `$1` message file as an internal error, not
  as "no trailer found." Check: a passing test per module forces its
  underlying git/fs call to throw and asserts the checker's return `status` is
  the internal-error state, never silently coerced to "clean" — this is the
  same "a guard that returns a sentinel has not finished the job" lesson
  `dev-team-process.md`'s weakness pattern #6 already states, applied here as
  a check rather than left as prose only.

## Out of scope

- **Wiring the four new checker modules into `.git/hooks/commit-msg` itself.**
  That file, its tracked canonical source, and the install/verify machinery
  are FEAT-045's forbidden-touch set for this dispatch (see header). This
  section defines the checker modules' call contract precisely enough for that
  wiring to be a mechanical follow-on, not a design decision — see
  Escalations.
- **Closing ASM-386's cherry-pick/revert/am gap.** Inherited from FEAT-045
  unchanged (AC-B2). If Aaron accepts Bill's pre-push-backstop recommendation
  on FEAT-045 (open at time of writing — see ASM-386's comment thread), this
  item's checks — **especially the secret checker, given the severity BUG-088
  itself names** — should very likely extend the same pre-push mechanism, but
  designing that extension now, ahead of Aaron's ruling on the mechanism
  itself, would very likely need to be redone; flagged as an escalation rather
  than designed here.
- **Closing `git commit --no-verify`.** Same native, hook-invisible-by-
  construction bypass `tool.committhook.md` AC-15 already discloses for
  identity; unchanged and un-closeable from any hook, by any of these four
  checks either.
- **Changing any of the four checks' detection LOGIC** (thresholds, patterns,
  the allowlist format, the plan-drift comparison method, the hand-maintained-
  file path list). AC-D2/D3/D4 require the existing logic to move and be
  reused, not to change.
- **A paired CI check for any of the four.** Not raised by the brief; not
  assumed here either, matching `tool.committhook.md`'s explicit exclusion of
  a paired CI authorship check for the same local-only reasoning.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **The four new checker-module filenames are the junior's choice** (this
  file specifies one-module-per-domain and a call-contract shape, not exact
  names) — P3, low risk, since AC-B5 requires the header documentation that
  makes the exact name irrelevant to the follow-on integration dispatch.
- **Whether `claude-version-checker.js`'s "fail-open on internal error, but
  still deny on positive detection" posture is expressible cleanly inside the
  same three-state return shape AC-E1 mandates for all four checkers** was not
  fully worked through at the design level — the three-state discriminant
  (clean/found-problems/internal-error) plus a **caller-side** decision about
  what to DO with "internal-error" (deny for three checkers, allow-with-a-
  warning for this one) should resolve it without a fourth state, but a
  junior hitting friction here should escalate rather than silently add a
  fourth state that breaks AC-E1's uniformity.
- **The live `commit-msg`/`git diff --cached` verification in AC-B1 was run
  only on this machine's git 2.55.0.windows.3**, the same version ASM-386's
  own finding is scoped to — not cross-checked against a different git
  version or OS. If this project's CI or a contributor's machine runs a
  materially different git, the `git diff --cached`-inside-`commit-msg`
  behaviour this whole section relies on should be re-verified there before
  trusting it blindly, same caution ASM-386 already logged for its own
  finding.

## Escalations

- **The commit-msg wiring gap (Section B/Out-of-scope) needs Bill's
  coordination, not a unilateral decision by either this file or FEAT-045's
  owner.** Once both this item's checker modules and FEAT-045's identity hook
  exist, someone needs to extend `githooks/commit-msg` into the multi-checker
  dispatcher AC-B5 assumes — that touches a file both items' forbidden-touch
  lists protect, so it is neither BA's file to resolve; flagging for Bill to
  either assign as a follow-on dispatch or fold into FEAT-045's own remaining
  work.
- **ASM-386's pending pre-push-backstop ruling (batched to Aaron, per the BOW
  comment thread) directly affects this item's eventual completeness,
  especially for the secret checker.** If Aaron approves the two-layer
  design (`commit-msg` fast-path + `pre-push` complete backstop), BUG-088's
  own severity framing ("on a public repo this is the worst outcome in the
  whole class") argues the secret checker should be the **first** of the four
  extended to the pre-push layer, ahead of plan/version/trailer, once that
  mechanism exists — flagging this priority ordering for Bill/Aaron now so it
  is not re-derived from scratch later.
- **Whether this section's four-checker-module refactor itself needs its own
  Destructive-agent pass before commit (GR#23)** is asked explicitly rather
  than assumed: this is a security-control refactor on a public repo, the
  exact shape of change GR#23 exists for, and the brief did not separately
  remind the junior/Tester of that gate the way it's spelled out in
  `dev-team-process.md` — noting it here so it is not missed at review time.

---

# BOW code: BUG-091

# Acceptance criteria — BUG-091 remediation (quote-mask-drift.test.js corpus gap: no case for a backslash immediately before a closing single quote)

**BOW code:** BUG-091 (P1)
**Filed under:** `docs/planning/acceptance/tool.secretguard.md` per
`code.json:tool.secretguard` (BUG-091's own tag). The code under attack —
`quote-mask-drift.test.js` and `buildQuoteMask` — is author-guard-adjacent
machinery, not secret-guard's own logic, but this file is already the
established home for cross-guard `buildQuoteMask`/trigger-parsing drift work:
the BUG-088 section immediately above this one covers the identical shared
function across all of `claude-secret-guard.js`, `claude-plan-guard.js`,
`claude-version-guard.js`, and `claude-pre-commit-check.js`. Filing BUG-091
here follows that precedent rather than opening a third acceptance file for
the same machinery; see Escalations for the one wrinkle this session's
refactor introduces.
**Spec refs:** BUG-091 (this item, full text via `node claude-bow.js show
BUG-091` — the Destructive-8 finding this section responds to); BUG-076
(the drift-test's own origin, filed against the same corpus-completeness
question this item extends); ASM-351 (unterminated-heredoc swallow-to-EOF,
the declared fail-safe this item's fixture deliberately avoids re-exercising
— see the design note below); `dev-team-process.md`'s "An acceptance
criterion's CHECK must be able to fail" rule (v1.9, BUG-033) and its
"a value duplicated across a module boundary needs a drift test" weakness
pattern #2 — both directly on point for a corpus-completeness bug.
**Date:** 2026-08-11
**Status:** active — normal pipeline order (criteria written before junior
dispatch)
**Package under test:** `quote-mask-drift.test.js` (repo root) — a corpus
addition only. **No production file (`claude-author-guard.js`,
`claude-pre-commit-check.js`, or any other guard) is touched by this item**:
BUG-091's own finding is that the *current* implementation is already
correct (single quotes do not support backslash-escaping, matching real
shell semantics); the defect is that the test corpus cannot see a regression
in that correctness. Building against these criteria means adding one golden
case, not changing detection logic.
**Standard gates:** Node.js — `node --check quote-mask-drift.test.js`; the
file's own test suite (`node --test quote-mask-drift.test.js`); no new npm
dependency; SG-6 (no Co-Authored-By). Forbidden-touch: every file
`quote-mask-drift.test.js` treats as a discovered copy (currently
`claude-author-guard.js` and `claude-pre-commit-check.js` — see the note on
current copy count below) and every checker module (`claude-secret-
checker.js`, `claude-plan-checker.js`, `claude-version-checker.js`,
`claude-trailer-checker.js`) — this item adds a test case, it does not touch
any file the case exercises.

## Current copy count (read before building — the file's own header is stale)

`quote-mask-drift.test.js`'s header and `KNOWN_COPIES_AS_OF_2026_08_11`
constant both describe **five** discovered copies. Verified live against
this session's actual repo state (via the test file's own `discoverCopies()`
logic, run read-only, no file touched) for this BA pass: **only two** files
now contain a literal `function buildQuoteMask(` declaration —
`claude-author-guard.js` and `claude-pre-commit-check.js`. The other three
(`claude-secret-guard.js`, `claude-plan-guard.js`, `claude-version-guard.js`)
lost their copies as a side effect of this session's BUG-088 remediation
(Section B/AC-B4 above): those three guards no longer parse a trigger at all
at the PreToolUse layer, so they have nothing left to mask, and none of the
four new `commit-msg` checker modules carries a copy either (AC-B4 requires
that absence explicitly). This is **not** this item's defect to fix — the
drift test's `discoverCopies()` mechanism is dynamic by design (see the
test file's own header, "judgement call #1") and will correctly run the new
corpus case against whatever it finds, five files or two. But it does change
what "confirm the 9 existing cases still pass" (AC-3 below) actually proves
right now: with two copies instead of five, the golden-case tests this item
must leave green number 2×9 = 18 (plus the two cross-copy tests), not 5×9 =
45. **AC-3 is written against the live discovered set, whatever its size is
at build time, not against the stale five-file header** — a build that
"fixes" the header's `KNOWN_COPIES_AS_OF_2026_08_11` list to match reality is
welcome (it would silence the file's own informational inventory-drift
alarm) but is explicitly out of scope for THIS item (see Out of scope) and
must not be required to pass these criteria.

## Acceptance criteria

### The new corpus case

- **AC-1. A tenth golden case is added to `GOLDEN_CASES` in
  `quote-mask-drift.test.js`, exercising a backslash immediately before a
  closing single quote**, asserting the current (correct) behaviour: inside
  a single-quoted region, a backslash is an ordinary masked character with
  no escape effect, so the very next `'` closes the quote there — it is not
  consumed as an escaped pair the way `\"` is inside double quotes. Use this
  exact fixture (verified empirically against the live `claude-author-
  guard.js` implementation for this BA pass, output below):

  ```js
  {
    name: 'BUG-091: backslash immediately before a closing single quote does NOT escape it (single quotes take no escapes)',
    // Shell text: echo 'it\'s fine git commit
    // The quoted region is exactly the 5 characters 'it\' -- the backslash
    // does not escape the closing quote (real shell semantics: single
    // quotes support NO escape character at all, unlike double quotes).
    // The quote therefore closes right there, and everything after --
    // including the real "git commit" -- is unmasked (visible/detected),
    // not swallowed as prose.
    ...build(
      seg('echo ', false),
      seg("'it\\'", true),
      seg('s fine git commit', false)
    ),
  },
  ```

  This fixture is a genuine shell mistake (someone tries to write the
  contraction "it's" inside single quotes using a backslash, which does not
  work in a real shell) followed by a real `git commit` invocation, so the
  case matters for the same reason BUG-043's prose case matters: it decides
  whether a real invocation is seen or hidden. It deliberately contains only
  two quote characters (not three), so the case tests the backslash-escape
  boundary in isolation, without also exercising the unrelated unbalanced-
  quote swallow-to-EOF behaviour ASM-351 already covers — conflating the two
  would leave it ambiguous which mechanism a future failure was pointing at.
  Check: the case is present in `GOLDEN_CASES` with a `text`/`mask` pair
  produced via the file's existing `seg`/`build` helpers (matching every
  other entry's convention, not hand-computed indices), and
  `node --test quote-mask-drift.test.js` passes it for every discovered
  copy plus the belt-and-braces cross-copy-agreement test for this case
  (AC-3 covers what "passes" must mean for existing cases; this case must
  pass the same way).
  **What a lazy implementation looks like:** adding a case whose text has NO
  backslash directly adjacent to the closing quote (e.g. `'it is fine'`), or
  one where the backslash sits outside any quoted region (that shape is
  already BUG-077's case) — either satisfies "a corpus case that mentions
  backslash and single quotes" by name while leaving the actual boundary
  BUG-091 named (backslash immediately before the closing delimiter, inside
  an already-open single-quoted region) untested. The check above requires
  the specific text given, not merely "a new case involving single quotes
  and backslashes."

### The regression-proof requirement

- **AC-2. The new case is demonstrated, not assumed, to catch the exact
  regression class BUG-091 names**: a mutant `buildQuoteMask` that widens
  the double-quote-only backslash-escape condition
  (`quote === '"' && c === '\\'`) to also cover single quotes
  (e.g. `(quote === '"' || quote === "'") && c === '\\'`) must produce a
  DIFFERENT mask than the AC-1 fixture's golden expectation, and that
  divergence must land inside the `git commit` substring specifically (not
  merely "some byte differs somewhere") — proving the corpus case would
  catch the dangerous direction of the regression (a real `git commit`
  becoming hidden as masked prose), not just any difference. This mirrors
  `dev-team-process.md`'s "an AC's check must be able to fail" standard
  (v1.9) and the "prove the drift test can actually fail" step weakness
  pattern #2 already requires for every duplicated-value drift test in this
  project. Check: a passing test (either inside `quote-mask-drift.test.js`
  itself as an explicitly-labelled self-check, or recorded as a one-time
  verification note in the item's BOW comments/commit — junior's choice, but
  it must be reproducible from the report, not asserted from memory) runs
  BOTH the real `buildQuoteMask` and a literal mutant matching the widened
  condition above against the AC-1 fixture's text, and asserts (a) the two
  masks differ, and (b) the mutant's mask has the `git commit` substring's
  index range set to masked/`true` while the real implementation's mask has
  that same range set to unmasked/`false`. For this BA pass, this was
  verified empirically ahead of dispatch (read-only, via a throwaway script
  outside the repo, no live file touched, same method BUG-091's own filer
  used): against `echo 'it\'s fine git commit`, the real implementation
  yields `git commit` fully unmasked (`0000000000`) and the mutant yields it
  fully masked (`1111111111`) — confirmed divergent, confirmed in the
  dangerous direction.
  **False-pass warning:** a check that only asserts "the two masks are not
  byte-identical" would be satisfied by a mutant that diverges somewhere
  harmless (e.g. a one-character difference inside the already-quoted `'it`
  region, which changes nothing about detection). The check must pin the
  divergence to the `git commit` substring's masked/unmasked state, because
  that is the only divergence BUG-091 says matters — a case that "can fail"
  against an irrelevant mutant but not against the named one is decoration
  wearing the shape of coverage, exactly the trap `dev-team-process.md`'s
  v1.9 rule exists to close.

### Non-regression of the existing corpus

- **AC-3. All 9 existing `GOLDEN_CASES` entries, the inventory test, the
  discovery-count test, the CRLF cross-copy test, and the belt-and-braces
  cross-copy-agreement test for each existing case still pass unmodified**
  — this is a corpus ADDITION, not a rewrite; no existing case's `name`,
  `text`, or `mask` construction changes, and no existing assertion helper
  (`seg`, `build`, `maskToString`, `firstDivergingIndex`, `loadMaskFn`,
  `discoverCopies`, `collectJsFiles`) is altered. Check:
  `git diff -- quote-mask-drift.test.js` shows only an addition (new array
  entry in `GOLDEN_CASES`, appended after the existing 9 — no other line in
  the file changed, confirmed by a diff review, not merely a rerun), and
  `node --test quote-mask-drift.test.js` is fully green — for the discovered
  copy set at build time (2 files as of this BA pass — see "Current copy
  count" above; this AC's pass count scales with whatever `discoverCopies()`
  finds, it does not hardcode 5×9=45 or 2×9=18). **What a lazy
  implementation looks like:** reordering or "cleaning up" an existing case
  while adding the new one — passes a shallow "still green" check while
  making a future `git blame`/diff review unable to tell the corpus addition
  from an unrelated edit. The diff-shape check above rejects that.

## Out of scope

- **Refreshing `KNOWN_COPIES_AS_OF_2026_08_11` and the header's "five
  copies" narrative to match the current two-file reality.** Genuinely
  useful (see "Current copy count" above) but not what BUG-091 asks for, and
  bundling it risks obscuring the one-line corpus addition this item is
  actually about inside an unrelated documentation refresh. Flagged as a
  natural follow-on, not assumed into this item's scope.
- **Any change to `buildQuoteMask` itself, in either of the two files that
  still carry it.** BUG-091's own finding is that current behaviour is
  already correct; this item proves the test can see a regression, it does
  not modify the thing being regression-tested.
- **Consolidating the two remaining copies into a shared module.** Out of
  scope for the same reason ASM-356 already gives for not doing this
  generally (a PreToolUse guard must still emit a decision when a shared
  dependency is broken) — not reopened by this item.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- The AC-1 fixture text deliberately differs from BUG-091's own descriptive
  example (`echo 'it\'s fine' git commit`, three quote characters) by
  dropping the trailing quote, isolating the backslash-before-closing-quote
  boundary from the unrelated unbalanced-quote swallow-to-EOF behaviour
  (ASM-351) that a third quote character would also exercise in the same
  case. The brief explicitly allowed "construct a real, meaningful test
  string" rather than mandating the literal example — logged because a
  reasonable builder could have instead transcribed the three-quote example
  verbatim, which would still catch BUG-091's regression but would also
  make the case's failure output ambiguous between two different mechanisms
  if it ever failed for an unrelated reason.
- AC-3's pass-count scales with the live discovered copy count (2, not the
  file's own documented 5) rather than being pinned to a literal number —
  logged because the discrepancy between the test file's header and the
  actual repo state (caused by this session's BUG-088 refactor removing
  three of the five copies) is exactly the kind of thing a developer or
  Tester could reasonably read as "the brief is wrong" and bounce on, when
  it is instead the correct, dynamic behaviour of `discoverCopies()` working
  as designed.

## Escalations

- **File-location check, per the brief's instruction to flag if a different
  acceptance file looks clearly more appropriate.** Considered and rejected:
  `buildQuoteMask` is author-guard-adjacent code, but the BUG-088 section
  immediately above this one already established `tool.secretguard.md` as
  the filing home for cross-guard drift/trigger-parsing work on this exact
  function (its own header explains why — BUG-088 is itself tagged
  `code.json:tool.secretguard` despite touching four files, none of them
  exclusively secret-guard's). BUG-091 carries the same `code.json:
  tool.secretguard` tag per `node claude-bow.js show BUG-091`. Filing here
  is consistent with that precedent, not a mismatch — no escalation raised
  on this point, but recording that it was checked rather than assumed.
- **The stale `KNOWN_COPIES_AS_OF_2026_08_11` list (Out of scope, above) is
  worth a follow-on BOW item** so the file's own informational drift alarm
  (`test('inventory: ...')`) stops firing for a reason everyone already
  understands (BUG-088's refactor, not a new undocumented copy). Not filed
  here — flagging for Bill to decide whether it is worth its own BUG code or
  folds into BUG-088's/FEAT-045's documentation pass.

---

# BOW code: SEC-021

# Acceptance criteria — SEC-021 remediation (high-entropy detector flags descriptive hyphenated correlation IDs)

**BOW code:** SEC-021 (P3)
**Spec refs:** SEC-021 (this item, full text via `node claude-bow.js show
SEC-021`); GR#1 (Aggressive Error Trapping — a correlation ID required on
every error/command, the convention this false positive punishes); GR#15
(Validators Derive From Data); SEC-015 (the same failure shape this item is
explicitly named as a repeat of — a guard that cries wolf on ordinary code
degrading into a rubber stamp); `dev-team-process.md`'s weakness pattern #4
table (`SEC-015 / SEC-021 | an identifier, a test literal | a heuristic's
verdict | false positives that train people to bypass`) and pattern #3
("fix the class, not the demonstrated instance"); BUG-029 (open, same root
cause in a sibling dressing — word-segmented lowercase identifiers clearing
the entropy floor on length alone — five allowlist entries in
`claude-secret-guard.allow.json` are explicitly held open pending "when the
heuristic learns to recognise word-segmented lowercase identifiers, DELETE
this entry"; see the note on BUG-029 under Escalations for why this item
does not close it outright even though the fix largely overlaps).
**Date:** 2026-08-12
**Status:** active — normal pipeline order (criteria written before junior
dispatch)
**Package under test:** `claude-secret-checker.js` (the entropy detector's
current home post-BUG-088 — `shannonEntropy`, `TOKEN_SHAPE_RE`,
`looksHighEntropy`, `ENTROPY_THRESHOLD`/`ENTROPY_MIN_LENGTH`, read in full
for this BA pass) and `claude-secret-guard.allow.json` (the three interim
`allowedPatterns` entries this item removes: `test-phase-bogus-injected`,
`test-corr-sec014-original-still-works`, `test-corr-sec018-original-still-
works`). `claude-secret-guard.js` itself is untouched — it calls into the
checker unchanged (BUG-088 AC-C1's already-established posture).
**Standard gates:** Node.js — `node --check claude-secret-checker.js`; the
checker's own test suite (`node --test claude-secret-checker.test.js`), plus
`claude-secret-guard.test.js` if any of its fixtures exercise the same
literals; no new npm dependency (stdlib only, matching FEAT-028 AC-3); SG-6
(no Co-Authored-By). Forbidden-touch for this BA pass: this file only —
`claude-secret-checker.js` and `claude-secret-guard.allow.json` are junior-
dispatch targets, read-only for this criteria pass (per this item's brief).

## User stories

- As **any developer following GR#1's correlation-ID convention**, I need a
  descriptive hyphenated identifier (`bogus-injected-phase`,
  `sec014-original-still-works`) to commit cleanly on first try, so following
  the project's own mandated convention does not routinely trigger a security
  denial that has to be argued past.
- As **Bill**, I need the entropy heuristic to stop degrading into a rubber
  stamp (SEC-015's failure mode) by training people to allowlist-on-reflex,
  so the day a real credential gets pasted next to a descriptive ID, the
  guard still has teeth.
- As **the guard itself**, I need to keep catching a real high-entropy secret
  after this fix — narrowing the detector to stop a false positive must not
  quietly open a false negative in the same motion.

## The chosen heuristic, and why (read before the ACs)

SEC-021 names three acceptable approaches: a Shannon-entropy floor, a
dictionary-word-ratio test, or a mixed-character-class requirement. This
item chooses a **combination of segment structure and character-class
mix**, not a bare threshold retune, for a reason specific to this
codebase's own evidence:

- `claude-secret-guard.allow.json`'s own accumulated comments (the
  `git-head-ref`, `data/buildings.json`, `bug095-fixture` entries — BUG-029's
  open trail) already diagnose the false-positive class precisely: **length
  clears the entropy floor while per-character entropy stays low**, because
  the string is really several short English/identifier *words* joined by a
  separator, not one random blob. Retuning `ENTROPY_THRESHOLD` upward (pure
  floor-tuning) is exactly the "widen a pattern" failure mode SEC-021's own
  description forbids — it would just move which length of hyphenated ID
  next clears the bar, not fix the shape confusion.
- A pure dictionary-word-ratio test (checking each segment against an
  English wordlist) is rejected as the sole mechanism: it requires bundling
  or hand-maintaining a wordlist (this file's own FEAT-028 gate, still
  binding on the checker, requires stdlib-only, no new dependency), and it
  would not correctly classify `sec014-original-still-works`'s first segment
  (`sec014` is not an English word) without a fragile alphanumeric-ID
  carve-out — at which point the "dictionary" part is doing no real work.
- **The chosen rule instead asks what STRUCTURE distinguishes the two
  shapes, not what VOCABULARY does**: split the candidate on `-`/`_`
  boundaries. If every resulting segment is composed **only of lowercase
  letters and/or digits** (no uppercase letter, no `+`/`/`/`=` anywhere in
  any segment), the candidate is a word-shaped identifier and is **exempt**
  from the high-entropy flag, regardless of its whole-string Shannon
  entropy. A candidate that is a single contiguous run (no `-`/`_` at all)
  — the shape of a base64 blob, a hex digest, a bearer token — is
  **untouched by this exemption** and still goes through the existing
  entropy/length check exactly as today. A candidate that IS
  hyphen/underscore-segmented but has an uppercase letter or a base64 symbol
  in some segment (e.g. a connection string or a mixed-case token that
  happens to contain a literal `-`) is also **not** exempt.
- This is a **mixed-character-class requirement, scoped by segment
  structure** — approach 3 from the item, refined so it does not
  accidentally exempt a real secret that happens to contain a hyphen. It
  needs no wordlist, no new dependency, and it is falsifiable against both
  named literals and a real secret shape (AC-1, AC-4 below).

**Fixture pairs this AC set is built from:**

| Candidate | Segments (split on `-`/`_`) | Every segment lowercase+digit only? | Verdict required |
|---|---|---|---|
| `bogus-injected-phase` | `bogus`, `injected`, `phase` | yes | NOT flagged |
| `sec014-original-still-works` | `sec014`, `original`, `still`, `works` | yes | NOT flagged |
| `sec018-original-still-works` | `sec018`, `original`, `still`, `works` | yes | NOT flagged |
| a realistic AWS-secret-key-shaped literal, e.g. `wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY` (base64-alphabet, contiguous, no hyphen, mixed case — a **fabricated example shape**, never a real credential) | none (`_`/`-` absent → treated as one segment) | n/a — not segmented, exemption does not apply | **still flagged** |
| a realistic hex-token-shaped literal, e.g. `9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b` (40 lowercase-hex chars, contiguous, no hyphen) | none | n/a — not segmented | **still flagged** |
| a mixed-case hyphenated token, e.g. `sk-LiVe-Ax7Qz9Km2Rp8Vt4Wy6` (contains uppercase inside a hyphen-delimited segment) | `sk`, `LiVe`, `Ax7Qz9Km2Rp8Vt4Wy6` | no (`LiVe`/`Ax7Qz9Km2Rp8Vt4Wy6` contain uppercase) | **still flagged** — exemption does not apply because a segment fails the lowercase+digit-only test |

## Acceptance criteria

### The heuristic fix

- **AC-1. The high-entropy detector distinguishes credential-shaped strings
  from lowercase-hyphenated word sequences using the segment rule above**:
  a candidate that (a) contains at least one `-` or `_`, and (b) has every
  resulting segment composed only of `[a-z0-9]` (no uppercase, no
  `+`/`/`/`=`), is exempt from `looksHighEntropy`'s flag regardless of its
  Shannon entropy or length. A candidate failing either condition — no
  separator at all, or any segment containing an uppercase letter or a
  base64 symbol — is evaluated by the existing, unchanged entropy/length
  logic. Check: a passing test in `claude-secret-checker.test.js` asserts,
  in one table-driven case covering the fixture-pair table above,
  `looksHighEntropy('bogus-injected-phase') === false`,
  `looksHighEntropy('sec014-original-still-works') === false`,
  `looksHighEntropy('sec018-original-still-works') === false`, AND
  `looksHighEntropy(<the fabricated base64-shaped fixture>) === true`,
  `looksHighEntropy(<the fabricated hex-shaped fixture>) === true`,
  `looksHighEntropy(<the fabricated mixed-case hyphenated fixture>) ===
  true` — all six assertions in the same test run, so a fix that only
  handles the "don't flag" half (and quietly breaks the "still flag" half)
  cannot pass partially unnoticed.
  **What a lazy implementation looks like:** widening `ENTROPY_THRESHOLD`
  upward until the three named literals stop clearing it (pure floor
  retuning) — this is the exact "do not fix by widening a pattern" failure
  mode SEC-021's own description forbids, and it would satisfy a check that
  only tests the three named literals while leaving every OTHER
  lowercase-hyphenated identifier of similar or greater length still
  misclassified as a secret above the new, higher bar (it just moves which
  length starts false-positiving). The fabricated-secret rows in this AC's
  check are also a trap for a different lazy fix: exempting *any* string
  containing a `-` at all (ignoring case/symbol content) would wrongly
  exempt the mixed-case `sk-LiVe-...` fixture, which the check explicitly
  requires to remain flagged.
  **False-pass warning:** a check that only asserts the three named literals
  return `false` (and never constructs a fourth, never-allowlisted
  hyphenated identifier, nor a real-secret-shaped fixture) cannot
  distinguish "the general shape is now handled" from "the three exact
  strings were special-cased" — see AC-2's False-pass warning for the
  allowlist-removal-specific form of this same trap.

### The allowlist removal

- **AC-2. The three interim exact-literal `allowedPatterns` entries —
  `test-phase-bogus-injected`, `test-corr-sec014-original-still-works`,
  `test-corr-sec018-original-still-works` — are removed from
  `claude-secret-guard.allow.json`**, and a test proves they are no longer
  NEEDED, not merely that they were deleted: with the three entries absent
  from the allowlist (either because they were genuinely deleted in this
  commit, or — for a test that must keep passing independent of allowlist
  file state — by loading the allowlist, asserting the three ids are absent
  from `allowedPatterns`, and separately calling `looksHighEntropy` directly
  on the three literals with no allowlist consultation at all, matching how
  `looksHighEntropy` is actually invoked upstream of allowlist matching),
  all three literals are still allowed through end-to-end (`runScan`/
  `checkSecrets` against a fixture diff containing each literal returns no
  finding for it) **because the improved heuristic itself exempts them**,
  not because a surviving allowlist entry catches them. Check: (a) a test
  asserts `claude-secret-guard.allow.json`'s `allowedPatterns` array
  contains no entry with `id` equal to any of the three retired ids —
  `grep -n "test-phase-bogus-injected\|test-corr-sec014-original-still-
  works\|test-corr-sec018-original-still-works" claude-secret-guard.allow
  .json` finds zero matches; (b) a passing end-to-end test stages a fixture
  diff containing all three literals (in a file NOT covered by any
  `allowedPaths` glob) and asserts `runScan`/`checkSecrets` reports no
  high-entropy finding for any of them, run against the allowlist file in
  its post-removal state.
  **What a lazy implementation looks like:** deleting the three
  `allowedPatterns` entries but leaving the allowlist's `allowedPaths` glob
  set unchanged, or leaving a fourth, differently-worded allowlist entry
  that happens to still cover the same three literals by a different
  mechanism (e.g. a new regex pattern crafted to match exactly those three
  strings) — technically satisfies "the three entries are removed" while
  actually just renaming the same allowlist-dependency this item exists to
  eliminate. The check's part (b) — asserting the literals pass with the
  allowlist in its real, fully-post-removal state, not a mocked "pretend
  it's empty" state — is what catches a disguised replacement entry.
  **False-pass warning:** the item's own description explicitly forbids
  "allowlisting a prefix" as a non-fix; a check that only re-tests the
  three EXACT literals (never a fourth, structurally-identical, never-
  allowlisted hyphenated ID) would pass a build that quietly added
  `"test-*"`-shaped prefix allowlisting instead of fixing the heuristic —
  the general-shape assertions required by AC-1 (a fourth literal never
  named in any allowlist entry, e.g. `foo-bar-baz-example-literal`, also
  returning `false` from `looksHighEntropy`) are what closes this gap; a
  Tester or Destructive agent should treat "AC-2 passes but AC-1's
  never-allowlisted fourth-literal case was skipped" as an automatic FAIL
  regardless of how clean AC-2's own evidence looks in isolation.

### Regression proof — a real secret shape is still caught

- **AC-3. After the fix, a realistic high-entropy secret is demonstrated,
  not assumed, to still be flagged.** Construct fixtures matching two
  distinct real-secret shapes never resembling any actual credential (base64
  API-key shape and hex-token shape — the two rows already given in the
  fixture-pair table above, or equivalent fabricated strings of the same
  shape), stage each inside a realistic surrounding line (e.g. an assignment
  to a variable named `apiKey`/`token`, matching FEAT-028 AC-9's existing
  "ordinary code around it" framing) in a throwaway fixture diff, and assert
  `runScan`/`checkSecrets` reports a `high-entropy` finding for each — not
  merely that `looksHighEntropy()` returns `true` in isolation (AC-1 already
  covers that at the unit level; this AC covers the end-to-end path a real
  commit would take, since AC-1 passing and the overall scan still finding
  it are logically separate claims once a segment-based exemption is added
  upstream of the entropy check). Check: a passing test in
  `claude-secret-checker.test.js` stages both fixtures, runs the full
  `runScan`/`checkSecrets` path (not `looksHighEntropy` directly), and
  asserts a `high-entropy` category finding with the correct file/line for
  each, using genuinely fabricated values (documented in the test as
  fabricated, per this project's existing convention of never staging a
  real-looking credential without saying so).
  **What a lazy implementation looks like:** only re-running the PRE-existing
  entropy fixture already present in `claude-secret-checker.test.js` (the
  one AC-D2 of the BUG-088 section above already exercises) without adding a
  fixture whose shape specifically probes the new segment-exemption logic's
  boundary (e.g. a secret that happens to contain a stray `-`, like a
  hyphen inserted into an otherwise-random token) — passes trivially because
  the old fixture likely never had a hyphen in it to begin with, so it never
  exercises the new code path's negative case at all.
  **False-pass warning:** a check that asserts only "the fixture is flagged"
  without also asserting the flagged category is specifically
  `high-entropy` (not some other category the scan happens to also catch it
  under, e.g. if the fixture is accidentally shaped like an API-key regex
  match too) would pass even if the entropy path itself silently stopped
  firing and a different detector coincidentally caught the same fixture —
  masking a regression in the exact function this item modifies.

## Out of scope

- **BUG-029's other allowlist entries** (`data/buildings.json`,
  `internal/foundation/data/buildings_test.go`, `bug095-fixture`,
  `git-head-ref`) — same root cause, explicitly NOT required to be removed
  by this item, which is scoped to the three literals SEC-021's own
  description names. If the chosen heuristic happens to also cover these
  (plausible — they are the same word-segmented-lowercase-identifier shape),
  removing them is a natural follow-on but is BUG-029's item to close, not
  this one's; see Escalations.
- **Any change to `ENTROPY_THRESHOLD` or `ENTROPY_MIN_LENGTH`'s numeric
  values.** The fix is structural (segment/character-class shape), not a
  threshold retune — SEC-021 explicitly forbids the latter as a non-fix.
- **A full dictionary-word-ratio implementation.** Considered and rejected
  above (bundling/maintaining a wordlist conflicts with the stdlib-only
  constraint FEAT-028 AC-3 already established); not reopened here.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- The exemption rule's exact segment/character-class boundary (lowercase +
  digit only, no uppercase, no `+`/`/`/`=`, at least one `-`/`_` present) is
  this BA's specific instantiation of SEC-021's "mixed character class"
  suggestion; SEC-021's own text does not specify the exact classes or
  the segment-splitting mechanism. Logged because a reasonable builder could
  instead choose a slightly different boundary (e.g. also exempting a lone
  digit-only segment differently, or treating `_` and `-` differently) that
  would still satisfy AC-1's six-fixture table while disagreeing with this
  BA's table on an untested edge case; what breaks if this specific
  boundary is wrong: an edge-case identifier neither this file's fixtures
  nor the junior's own tests happen to cover could go either way without
  anyone noticing until a real instance recurs.
- The fabricated secret-shaped fixtures in AC-1/AC-3 (the base64-alphabet
  and hex-token example strings) are BA-authored placeholders, not values
  copied from any real system — logged per this project's standing
  practice of never staging a real-looking credential without saying so
  explicitly; what breaks if this assumption is wrong: none, this is a
  disclosure logged out of caution, not a load-bearing technical claim.

## Escalations

- **Whether BUG-029 should be folded into this item or stay separate** is
  flagged rather than decided: this item's fix, if it lands as designed,
  will very likely also satisfy BUG-029's five open entries' own stated
  removal condition ("DELETE this entry when BUG-029 lands"), but SEC-021's
  description scopes this item to the three named literals only, and BUG-029
  covers different file paths (`data/buildings.json`, a `.go` test file,
  `refs/remotes/origin/HEAD`) this BA pass has not re-verified against the
  chosen rule's exact boundary (e.g. `refs/remotes/origin/HEAD` splits on
  `/`, not `-`/`_` — the chosen rule as specified does NOT exempt it, since
  its segments are separated by `/`, a character this rule does not treat
  as a boundary). Recommend Bill decide, once SEC-021 lands, whether to
  re-scope BUG-029's remaining entries against the shipped heuristic rather
  than assume they are automatically closed by it.
