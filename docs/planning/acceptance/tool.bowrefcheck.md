BOW code: FEAT-118

# Acceptance criteria — BOW git-ref integrity check (FEAT-118, `tool.bowrefcheck`)

**BOW code:** FEAT-118 (P2, open) — "BOW git-ref integrity check."
**BOW mkey / GUID:** `tool.bowrefcheck` / `3ef12808-4e54-439e-b5a3-a9158bb26aa7` (code.json); parent item `tool.bow` / MOD-007.
**Spec refs:** M0-ENG §4 (The Book of Work) / §5 ("A commit-msg hook validates the ref exists in BoW and auto-comments the commit hash onto the entity"); `docs/planning/acceptance/tool.bow.md` (17 ACs — this hook is the PreToolUse enforcement half of that parent item's two-hook pair).
**Date:** 2026-08-16
**Status:** **RETROSPECTIVE** — code already committed (`781f3ca`, "feat: BOW-git integration — [mkey] ref enforcement on engine commits + auto-ref of hashes"); this file documents the shipped contract and specifies the test coverage that must exist (which, as of writing, does not — see ASM-720).
**Package under test:** `claude-bow-ref-check.js` (repo root, layer tooling). A **PreToolUse** hook (Bash + PowerShell matchers), placed after `claude-secret-guard.js` and before `claude-pre-push-check.js` in `.claude/settings.json`.

## What this hook is (read first)

`claude-bow-ref-check.js` is the enforcement half of the `tool.bow` commit-msg pairing: before a `git commit` lands, if the staged change touches `cmd/`, `internal/`, or `data/`, the commit message must carry at least one `[mkey]`/`[CODE-NNN]` tag that resolves to a live BOW item. Its sibling `claude-bow-autoref.js` (FEAT-117) then records the landed hash after the fact.

The contract is defined by three properties AC'd below: (1) it decides **enforced vs exempt** by inspecting staged files (`git diff --cached --name-only`), never by parsing the command string for path hints; (2) it resolves tags through claude-bow.js's own canonical `findItemByRef`, never a bespoke query (BUG-003); (3) its posture is **fail-open by design** — a transient infrastructure failure (DB down, unparseable stdin, unextractable message) warns or silently allows, it never denies; only two things are genuinely denied: an enforced-path commit with zero `[mkey]` tags, or a tag the (successfully-reached) BOW says does not exist.

## User stories

- As **Bill (lead)**, I need every commit touching `cmd/`, `internal/`, or `data/` to carry a valid `[mkey]` reference to an existing BOW item, so nothing engine/UI/data lands without a traceable work-item link (M0-ENG §5, mechanised).
- As **a developer fixing a typo in a `.md` file**, I need commits that don't touch those trees to pass through silently, so trivial doc/tooling commits aren't forced to invent a spurious BOW link.
- As **a developer in an emergency or during a DB outage**, I need the hook to fail open with a visible warning (or a documented escape hatch), so a transient infrastructure problem can never brick a legitimate commit repo-wide.

## Acceptance criteria

### Behaviour

- **AC-1 (syntax).** `node --check claude-bow-ref-check.js` exits 0.
- **AC-2 (intercept scope — commits only).** The hook acts only when the command matches the shell-boundary regex `/(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/` (same discipline as `claude-version-guard.js`, avoiding false hits on "git commit" inside quoted content). Any non-commit command is allowed silently — exit 0, no stdout. Check: feed `git status` / `echo` on stdin; assert exit 0 and zero bytes on stdout.
- **AC-3 (`--amend` is skipped).** A command containing `--amend` is allowed silently (exit 0), mirroring `claude-bow-autoref.js`'s amend skip — amends are left to a manual `ref` on both halves.
- **AC-4 (escape hatch — `CLAUDE_DISABLE_BOW_REF=1`, ASM-721).** When the environment variable `CLAUDE_DISABLE_BOW_REF` is `1`, the hook bypasses entirely and allows silently, before any stdin parse or git inspection. Check: with the var set, an enforced-path commit message carrying zero tags is still allowed (exit 0, no output).
- **AC-5 (enforced paths — staged files, never command-string hints).** The hook determines whether the commit touches `cmd/`, `internal/`, or `data/` by running `git diff --cached --name-only` and testing each staged path against `ENFORCED_PATH_RE = /^(cmd|internal|data)\//` — it never infers the path from the command string or message. Check: a test stages a file under `internal/` and asserts the hook activates; a test whose command string mentions `internal/` but whose staged diff is empty (or docs-only) asserts the hook does not activate.
- **AC-6 (exempt commits are silent).** If no staged file matches `ENFORCED_PATH_RE`, the hook allows silently with **no output at all** (not even a warning). Check: a docs-only (`docs/...`) staged change produces exit 0 and zero stdout bytes.
- **AC-7 (message extraction — `-m`/`--message` only).** The hook extracts the commit message body from `-m "..."`, `-m '...'`, `--message=...`, and `--message '...'` flags (handling escaped quotes within), joining multiple `-m` flags with newlines (git's own behaviour: each becomes a paragraph). If no `-m`/`--message` flag can be parsed at all (commit-from-file `-F`, heredoc body, interactive editor flow, or a quoting shape the regex can't parse), `extractMessage` returns null.
- **AC-8 (unextractable message → warn-and-allow).** On an enforced-path commit where `extractMessage` returned null, the hook **warns and allows** (never denies), telling the user it cannot verify because it only understands `-m`/`--message` shapes and asking them to confirm the message carries a valid `[mkey]` themselves.
- **AC-9 (zero tags → deny).** On an enforced-path commit whose extracted message carries zero `[tag]`s, the hook **denies** with a clear message citing M0-ENG §5's convention, showing the required shape (`git commit -m "[tool.bow] ..."` / `"[MOD-007] ..."`), pointing at `node claude-bow.js list`/`show`, and naming the `CLAUDE_DISABLE_BOW_REF=1` bypass.
- **AC-10 (canonical lookup, no drift).** Each extracted tag is resolved via claude-bow.js's own `findItemByRef(db, tag)` — `guid = ?` exact, `UPPER(code) = UPPER(?)`, or `mkey = ?` exact — imported directly, never a bespoke reimplementation (BUG-003). Check: `grep -n "findItemByRef" claude-bow-ref-check.js` shows it is required from `./claude-bow.js` and is the only BOW-lookup path.
- **AC-11 (unknown tag → deny, with best-effort suggestions — ASM-722).** A tag that does not resolve against the (successfully-reached) BOW is **denied**, with a message naming each unknown tag. The `nearMisses` `LIKE '%tag%'` suggestions are appended to the denial as advisory UX ("Did you mean: ...") but are **never** load-bearing — a tag with a near-miss is denied exactly like a tag with no near-miss; the pass/fail decision is `findItemByRef`'s alone. Check: a message with one unknown tag and one valid tag is still denied, and the unknown tag (not the valid one) is the one named in the denial.
- **AC-12 (all tags resolved → silent allow).** If every tag resolves, the hook allows silently (exit 0, no output).
- **AC-13 (malformed tags are denied at lookup, not skipped as "not a tag").** `TAG_RE = /\[([^\[\]\n]+)\]/g` is deliberately unopinionated about tag shape, so a malformed `[not-a-real-mkey format]` is still extracted and then denied at the BOW-lookup step as unknown — it is never silently ignored. Check: an enforced-path message containing only `[this is not a code]` is denied (not allowed as if it had no tags).

### Fail-open posture (deliberate, lead-confirmed — parent AC-10/AC-12)

Unlike `claude-plan-guard.js`/`claude-secret-guard.js` (fail-CLOSED, because they gate *content* problems), this hook gates *traceability* and is fail-open, mirroring `claude-version-guard.js`: a transient infrastructure failure must never brick unrelated commits repo-wide. Only AC-9 (zero tags) and AC-11 (unknown tag against a live BOW) genuinely deny.

- **AC-14 (unparseable stdin → silent allow).** A `JSON.parse` failure on stdin (after BOM strip) allows silently — this hook never denies over a plumbing hiccup.
- **AC-15 (`git diff --cached` failure → silent allow).** If staged files can't be determined (e.g. outside a repo, or the git call errors), the hook allows silently.
- **AC-16 (DB unreachable → warn-and-allow).** If `connectReadOnly()` throws (dead DB), the hook **warns and allows**, naming the tags it could not validate and telling the user to confirm them manually once the DB is back (or run `node claude-bow.js ref <code> <hash>`).
- **AC-17 (BOW-lookup throw → warn-and-allow).** A query error after a successful connect is treated the same as DB-unreachable — warn-and-allow (the fail-open posture covers any BOW-lookup failure, not just the initial connect).
- **AC-18 (last resort — even an internal bug cannot deny).** The top-level `main().catch(...)` exits 0, so an unexpected throw anywhere in the hook cannot brick a commit.
- **AC-19 (deny/warn JSON contract).** A deny is emitted as `{ hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: "..." } }` on stdout; a warn is the same shape with `permissionDecision: "allow"`; both then `process.exit(0)` — the hook signals denial via the JSON body, never via a nonzero exit code (matching `claude-plan-guard.js`/`claude-version-guard.js`). Check: a denied commit produces exit code 0 with a stdout JSON whose `permissionDecision` is `"deny"`.

### Tests

- **AC-20 (scratch DB, subprocess contract — ASM-720).** The script ships with **no `module.exports`** (unlike `claude-bow-autoref.js`), so its helpers (`extractMessage`, `extractTags`, `nearMisses`) are module-private and there is no unit-test seam. Test coverage is therefore **subprocess-level**: spawn `node claude-bow-ref-check.js`, write the PreToolUse JSON `{ tool: "Bash", tool_input: { command: "..." } }` to stdin, and assert the stdout `permissionDecision` JSON and exit code. All BOW lookups run against a scratch DB via `METRO_DB_*` (claude-db.js reads env at call time), never `metro`. Check: test setup sets `METRO_DB_NAME=<scratch>` and asserts the real `metro` DB is untouched.
- **AC-21 (intercept + exempt).** A non-commit command → exit 0, no output; a docs-only staged change (scratch repo or mocked git) → exit 0, no output.
- **AC-22 (zero-tag deny).** Enforced-path staged + a message with no `[tag]` → deny JSON naming the requirement.
- **AC-23 (unknown-tag deny).** Enforced-path staged + a message whose tag is absent from the scratch BOW → deny JSON naming exactly that tag.
- **AC-24 (valid-tag allow).** Enforced-path staged + a message whose tag resolves in the scratch BOW → exit 0, no output.
- **AC-25 (fail-open cases).** Unparseable stdin, `git diff --cached` failure, DB unreachable (`METRO_DB_PORT` pointed at a closed port), and a lookup throw → all allow (exit 0), with warn JSON produced for the DB/lookup cases and silence for the plumbing cases.
- **AC-26 (escape hatch).** `CLAUDE_DISABLE_BOW_REF=1` with an enforced-path zero-tag command → exit 0, no output.

## Out of scope

- The post-commit auto-ref half of the `tool.bow` pair — that is `claude-bow-autoref.js` (FEAT-117, `docs/planning/acceptance/tool.bowautoref.md`).
- Extracting messages from `-F` / heredoc / editor flows — by design these warn-and-allow (AC-8) rather than being parsed.
- Unit-level tests on `extractMessage`/`extractTags`/`nearMisses` — not possible without exporting them (see AC-20 and Escalation 2); the subprocess contract is fully testable regardless.

## Escalations

1. **Escape-hatch name differs from the parent doc's provisional name (ASM-721).** `tool.bow.md` AC-13 provisionally named the bypass `CLAUDE_DISABLE_BOW_REF_CHECK=1`; the shipped name is `CLAUDE_DISABLE_BOW_REF=1`. The shipped name is authoritative for this file — flagged for Bill to either update `tool.bow.md` AC-13 to the shipped name or confirm the drift is acceptable, so a future reader of the parent doc isn't pointed at a dead env var.
2. **No `module.exports` (ASM-720).** `claude-bow-autoref.js` exports its core logic for direct testing; `claude-bow-ref-check.js` exports nothing, so its helpers are private and untestable at unit level. Not a defect (the subprocess contract in AC-20 fully covers it), but if Bill wants the two hooks to have symmetric test seams, exporting `extractMessage`/`extractTags`/`nearMisses` would be a small follow-up.

## Assumptions logged (process v1.7)

- **ASM-720** — retrospective posture + missing test file, and the no-`module.exports` fact: coverage must be subprocess/stdin-stdout, not unit-level.
- **ASM-721** — the escape-hatch variable is `CLAUDE_DISABLE_BOW_REF=1` (shipped), authoritative over `tool.bow.md` AC-13's provisional `CLAUDE_DISABLE_BOW_REF_CHECK=1`.
- **ASM-722** — `nearMisses` suggestions are best-effort UX and never affect the allow/deny verdict (strict-resolve-then-deny, no fuzzy matching).

- **ASM-913 (FEAT-084 CC fold).** claude-bow-ref-check.js header line 5 still cites BOW mkey tool.bow / MOD-007 (parent) while the module key is tool.bowrefcheck per code.json (re-keying drift).
