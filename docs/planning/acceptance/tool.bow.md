BOW code: MOD-007

# Acceptance criteria — tool.bow (MOD-007)

**BOW code:** MOD-007
**Spec refs:** M0-ENG §4 (The Book of Work, `docs/METROPOLIS-MASTER-v2.1.md` lines 867-983, esp. line 989's commit convention: "A commit-msg hook validates the ref exists in BoW and auto-comments the commit hash onto the entity (via `bow`)"); V.2.3 (open item 3, "BoW seed", line 1328 — superseded per the BOW item's own DECIDED note, see below); the BOW item's own description (`node claude-bow.js show tool.bow`): "DECIDED (Aaron, 2026-08-08): the metro BOW (claude-bow.js) IS the project Book of Work... Remaining work: commit-msg validation that [mkey] refs exist in the BOW, auto-comment of commit hashes onto items (extend `claude-pre-commit-check.js` or a commit hook), and the F12 read-only BoW tab (already in `ui.screen.debug`)."
**Date:** 2026-08-09
**Status:** active
**Deliverable:** a repo-root Node.js `PreToolUse`/`PostToolUse` hook pair (matching `claude-plan-guard.js`/`claude-pre-commit-check.js`'s existing style) — **not** the `tools/bow/` + `cmd/bow/` Go paths carried over from the BOW item's stale `path:` field (see Escalations: that field predates the DECIDED note superseding the separate `metropolis_bow` schema, and should be corrected on the BOW item itself, not followed literally).
**Standard gates:** see `README.md`'s general convention; this is a **Node.js hook script, not a Go package** — SG-1/SG-2/SG-4/SG-7 (go build/vet/test/determinism-grep) do not apply, matching `tool.secretguard`'s posture. This item's own gates are AC-1 (`node --check`), SG-5 (forbidden-touch), and SG-6 (no Co-Authored-By).

## User stories

- As **Bill**, I need every commit touching `cmd/`, `internal/`, or `data/` to carry a valid `[mkey]` reference to an existing BOW item, so nothing lands without a traceable work-item link (M0-ENG §5's commit convention, mechanised).
- As **the BOW itself**, I need a landed commit's hash automatically attached to the item(s) it referenced, so `node claude-bow.js show <mkey>` always shows its real git history without anyone remembering to run `ref` by hand.
- As **a developer fixing a typo in a `.md` file**, I need commits that don't touch `cmd/`, `internal/`, or `data/` to be exempt from the `[mkey]` requirement, so trivial doc/tooling commits aren't forced to invent a spurious BOW link.
- As **a developer in an emergency**, I need a documented escape hatch, so a false-negative BOW lookup (e.g. DB temporarily unreachable) can never brick a legitimate commit.

## Scope

Two hook behaviours: (1) a `PreToolUse` check on `git commit` that requires a `[mkey]` tag in the commit message when the staged change touches `cmd/`, `internal/`, or `data/`, and denies if the referenced mkey does not exist in the live BOW; (2) a `PostToolUse` (or a chained step after a successful commit) that auto-runs `node claude-bow.js ref <code> <hash>` for every `[mkey]` found in the just-landed commit message.

## Acceptance criteria

### Functional

- **AC-1.** The hook script(s) are valid, syntactically-checkable Node.js. Check: `node --check <script>.js` exits 0 for each file added.
- **AC-2.** The `PreToolUse` check intercepts only `git commit` commands (mirroring `claude-plan-guard.js`'s `command.includes('git commit')` gate) — non-commit Bash commands exit 0 immediately with no stdout.
- **AC-3.** The check determines whether the commit touches `cmd/`, `internal/`, or `data/` by inspecting the staged changes (`git diff --cached --name-only`), not by parsing the commit message or command string for path hints — a passing test stages a file under `internal/` and asserts the check activates; a passing test stages only a `docs/`-only change and asserts the check is skipped (exempt, per M0-ENG §5's existing docs/tooling exemption carried forward from `legacy.versionguard`'s retarget decision).
- **AC-4.** When active (AC-3), the check parses the commit message (from the `git commit -m "..."` command string, or a `-F`/heredoc body if present) for a `[mkey]` tag — check: `grep -n "\\[.*\\]" <script>.js` or equivalent shows a regex extracting bracketed tags, and a passing test asserts a message containing `[engine.traffic]` extracts `engine.traffic` correctly, including messages with multiple tags (a commit may close more than one item).
- **AC-5.** A commit message with **zero** `[mkey]` tags, touching `cmd/`/`internal/`/`data/`, is **denied** with a clear message explaining the requirement and citing the M0-ENG §5 convention — a passing test asserts this.
- **AC-6.** Each extracted `[mkey]` tag is validated against the **live BOW** (a lookup equivalent to `node claude-bow.js show <mkey>` succeeding) — a passing test with a mocked/injected BOW-lookup function asserts a valid mkey passes and an invalid/unknown mkey is denied, with the denial message naming the specific unknown tag (not just "some tag was invalid").
- **AC-7.** A commit message may reference an mkey using either its dot-separated key (`engine.traffic`) or its short BOW code (`MOD-023`) — matching `claude-bow.js`'s own `requireItem` lookup, which the check should reuse or mirror (spawning `node claude-bow.js show <ref>` and checking its exit code is an acceptable, simple implementation rather than reimplementing BOW lookup logic from scratch — check: the script either shells out to `claude-bow.js` or imports/requires its lookup function directly, not a third bespoke BOW query).
- **AC-8.** After a `git commit` that passed AC-5/AC-6 actually lands (exit code 0 from the real commit), a follow-up step runs `node claude-bow.js ref <code> <hash>` for every validated `[mkey]` tag found, using the real commit's resulting hash (`git rev-parse HEAD` immediately after) — a passing integration test performs a real commit against a disposable BOW test item/fixture and asserts a `bow_git_refs` row (or equivalent, via `node claude-bow.js show <code>`) now references the new commit hash.
- **AC-9.** The auto-`ref` step is idempotent/harmless if run twice for the same commit (e.g. hook re-invocation, retried commit) — a passing test runs the ref step twice for the same hash and asserts no duplicate-row error surfaces to the user (either the underlying `ref` command is naturally idempotent, or the wrapper catches and swallows a duplicate-insert condition specifically, not errors in general).

### Error handling

- **AC-10.** If the BOW/database is unreachable when the `PreToolUse` check runs, the check **fails open** for the commit (allows it through with a warning), not fail-closed — this is a deliberate posture difference from `tool.planguard`/`tool.secretguard`'s fail-closed stance, because a transient DB outage must never block all engine/UI/data commits repo-wide. Check: a passing test mocks the BOW lookup to throw/timeout and asserts the commit is allowed, with a warning message printed (not silently allowed with no signal).
- **AC-11.** If the BOW is unreachable when the `PostToolUse` auto-ref step runs (after the commit already landed), the failure is logged/surfaced but never un-commits or blocks anything further — the commit has already happened; a missed auto-ref is a recoverable annoyance (fixable later with a manual `node claude-bow.js ref`), not a reason to fail a subsequent action.
- **AC-12.** A malformed `[not-a-real-mkey format]` tag (doesn't match the `key`/`CODE` shape at all) is treated the same as an unknown mkey (AC-6) — denied with a clear message, not silently ignored as "not a tag."
- **AC-13.** `CLAUDE_DISABLE_BOW_REF_CHECK=1` (or a name matching the project's existing `CLAUDE_DISABLE_*` convention) bypasses the `PreToolUse` requirement entirely, matching `tool.planguard`/`tool.secretguard`'s escape-hatch posture — a passing test sets the env var and asserts a commit with zero `[mkey]` tags touching `internal/` is still allowed.

### Determinism & safety

- **AC-14.** The `[mkey]`-extraction regex and BOW-lookup logic produce identical verdicts given the same commit message and BOW state across repeated invocations — no randomness, no wall-clock dependence in the decision logic itself (`grep -rn "time.Now" <script>.js` — any match confined to logging/timestamps, never the pass/fail decision).
- **AC-15.** The auto-ref step never mutates anything other than the referenced item(s)' `bow_git_refs` rows — it must not, for example, also silently change an item's `status` to `done` (that remains a deliberate, separate `claude-bow.js done` action per the dev-team process's "nothing is `done` in the BOW without Bill's final review").

### Documentation

- **AC-16.** The hook script(s) carry a header comment in the established house style (matching `claude-plan-guard.js`): BOW mkey (`tool.bow`), spec ref (M0-ENG §4, §5), behaviour summary, the fail-open-not-fail-closed posture note (AC-10, called out explicitly since it deliberately differs from the repo's other guard hooks), and the escape-hatch env var name.
- **AC-17.** `.claude/settings.json`'s hooks section documents (via the script's own header, since settings.json itself carries no comments) where in the `PreToolUse`/`PostToolUse` chains this hook sits relative to `claude-plan-guard.js`, `claude-pre-commit-check.js`, and `claude-secret-guard.js` (if landed) — check: `grep -n "claude-plan-guard.js\|claude-pre-commit-check.js\|claude-secret-guard.js\|<this item's script name>" .claude/settings.json` shows the new hook present in the `PreToolUse`/`PostToolUse` arrays as appropriate.

## Out of scope

- The F12 read-only BoW tab — explicitly already covered by `ui.screen.debug` (`FEAT-007`, its own AC-9), per this item's own BOW description ("already in ui.screen.debug"). Do not duplicate.
- Building a separate `metropolis_bow` MariaDB schema per the original M0-ENG §4 `CREATE TABLE` blocks — explicitly DECIDED against (2026-08-08): the metro BOW (`claude-bow.js`, the `metro` database's `bow_*` tables) already carries all the spec's fields; this item extends the git-integration behaviour only.
- Seeding the BOW from the master plan (`tools/plan/generate.js` + `bow-import.json`) — that's `FEAT-003` (already done) and `tool.planguard`'s drift-detection concern, not this item's.
- Enforcing BOW refs via a `commit-msg` git hook (installed into `.git/hooks/`) rather than a Claude Code `PreToolUse` hook — the brief and the existing sibling hooks (`claude-plan-guard.js` etc.) are all Claude-Code-harness hooks intercepting the `Bash` tool call, not native git hooks; this item follows that established pattern unless Bill specifies otherwise.

## Escalations

1. **BOW item `path:` field is stale.** `node claude-bow.js show tool.bow` currently reports `path: tools/bow/ + cmd/bow/`, which is a leftover from the master plan's original (pre-DECIDED-note) framing of this item as a Go CLI (`cmd/bow`). The item's own DECIDED comment (2026-08-08) supersedes that: the remaining work is a JS hook extending `claude-pre-commit-check.js`/a sibling of `claude-plan-guard.js`, not a Go binary. This file's criteria target the JS-hook deliverable per the DECIDED note and the lead's brief; Bill should correct the BOW item's `path:` field to match (e.g. via `node claude-bow.js set MOD-007 --path <actual path>`) so future readers of `show tool.bow` aren't misled — the BA cannot make that write herself.
2. **Fail-open vs fail-closed posture (AC-10) is a deliberate departure** from `tool.planguard`/`tool.secretguard`'s fail-closed stance, reasoned from GR#3/GR#7's own precedent that a *transient infrastructure* failure (DB down) should not itself block unrelated commits repo-wide, unlike a *content* problem (plan drift, a leaked secret) which genuinely must block. Flagging for Bill's explicit sign-off since it's the one hook in this family that inverts the fail-closed convention — if that reasoning is wrong, AC-10/AC-11 need rewriting to fail-closed instead.
