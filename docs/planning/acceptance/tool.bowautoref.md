BOW code: FEAT-117

# Acceptance criteria — post-commit BOW auto-ref hook (FEAT-117, `tool.bowautoref`)

**BOW code:** FEAT-117 (P2, open) — "Post-commit hook: auto-record commit hash refs onto BOW items."
**BOW mkey / GUID:** `tool.bowautoref` / `b0d29b58-d7d0-411c-9cc1-15bbea5d00ed` (code.json); parent item `tool.bow` / MOD-007.
**Spec refs:** M0-ENG §4 (The Book of Work) / §5 ("...auto-comments the commit hash onto the entity via `bow`"); `docs/planning/acceptance/tool.bow.md` AC-8/AC-9/AC-11/AC-15 (this hook is the PostToolUse half of that parent item's two-hook pair).
**Date:** 2026-08-16
**Status:** **RETROSPECTIVE** — code already committed (`781f3ca`, "feat: BOW-git integration — [mkey] ref enforcement on engine commits + auto-ref of hashes"); this file documents the shipped contract and specifies the test coverage that must exist (which, as of writing, does not — see ASM-718).
**Package under test:** `claude-bow-autoref.js` (repo root, layer tooling). A **PostToolUse** hook (Bash + PowerShell matchers), not a PreToolUse guard — it runs after the tool call has already succeeded and can never block or undo anything.

## What this hook is (read first)

`claude-bow-autoref.js` is the second half of the `tool.bow` commit-msg pairing: `claude-bow-ref-check.js` (FEAT-118) enforces that engine/UI/data commits carry a valid `[mkey]` **before** they land; this hook records the **after** fact — for each `[tag]` in a just-landed commit message that resolves to a live BOW item, it writes a `bow_git_refs` row attaching the real commit hash to that item. It is the ONE sanctioned direct DB **write** among this project's hooks; every other hook (including `claude-bow-ref-check.js`) is read-only.

Three properties define its contract and are AC'd below: (1) it trusts the **working tree** (the real `git log` HEAD), never the tool's captured stdout, for the hash; (2) it resolves tags through claude-bow.js's own canonical `findItemByRef`, never a bespoke query (BUG-003 was exactly this hook reimplementing that lookup with drift); (3) it is **idempotent** and **fail-open-by-construction** — because it runs post-commit, nothing it does can block or roll back.

## User stories

- As **the BOW itself**, I need a landed commit's hash attached to every item its message referenced, so `node claude-bow.js show <code>` always shows its real git history without anyone remembering to run `ref` by hand (M0-ENG §5).
- As **Bill (lead)**, I need the auto-ref to be idempotent and to never touch an item's `status`, so a hook re-invocation or a retried tool call can't double-insert or silently mark something `done`.
- As **a developer committing at a moment the DB is down**, I need the failure to be logged and swallowed, not surfaced as a denial, because the commit has already landed and a missed auto-ref is recoverable later with a manual `ref`.

## Acceptance criteria

### Behaviour

- **AC-1 (syntax).** `node --check claude-bow-autoref.js` exits 0.
- **AC-2 (intercept scope — commits only).** The hook acts only when the just-run command matches the shell-boundary regex `/(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/` (the same boundary discipline as `claude-version-guard.js`, avoiding false hits on "git commit" inside quoted string content). Any non-commit Bash/PowerShell command exits 0 immediately with no DB connection, no log line, and no stdout. Check: feed a `git status` or an echo command on stdin; assert exit 0 and that no `bow_git_refs` row was added and `claude-bow-autoref.log` was not appended to.
- **AC-3 (`--amend` is deliberately skipped — ASM-719).** A command containing `--amend` exits 0 without reading HEAD or writing any ref, because amending rewrites the hash out from under an auto-ref in a way that is ambiguous to record safely; amends are left to a manual `node claude-bow.js ref <code> <hash>`. Check: a `git commit --amend ...` command produces no `bow_git_refs` row and no log entry.
- **AC-4 (working tree is the source of truth).** The hook reads the real, just-landed commit via `git log -1 --format=%H%x1f%B` (hash, unit separator, full message body) — never the tool's captured stdout for the hash. The hash is validated against `/^[0-9a-f]{7,40}$/i`; if HEAD can't be read (not a repo, `git log` fails) it returns null and the hook logs `SKIP: could not read HEAD commit ...` and exits 0. Check: the hash inserted into `bow_git_refs` equals `git rev-parse HEAD`'s value, not whatever the tool's stdout happened to contain.
- **AC-5 (tag extraction).** Every `[tag]` is extracted from the full commit message body (`%B` — subject + body paragraphs), using `TAG_RE = /\[([^\[\]\n]+)\]/g`, supporting multiple tags (a commit may close more than one item). Check: a message `[tool.bow] [MOD-007] ...` yields exactly those two tags.
- **AC-6 (canonical lookup, no drift).** Each tag is resolved via claude-bow.js's own `findItemByRef(db, ref)` — which matches `guid = ?` exact, `UPPER(code) = UPPER(?)`, or `mkey = ?` exact — imported directly, never a bespoke reimplementation of that query (BUG-003). Check: `grep -n "findItemByRef" claude-bow-autoref.js` shows it is required from `./claude-bow.js` and is the only BOW-lookup path.
- **AC-7 (the insert — same statement as `ref`).** For each tag that resolves, the hook INSERTs a `bow_git_refs` row with `(item_guid, commit_hash, branch, note)` = `(item.guid, hash.toLowerCase(), currentBranch(), 'auto-ref (hook)')` — the same `INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note) VALUES (?, ?, ?, ?)` statement claude-bow.js's own `ref` command runs. Check: after a real (or faked) landed commit carrying `[<a real code>]`, `bow_git_refs` contains a row whose `item_guid` is that item's GUID, `commit_hash` is the lowercased hash, and `note` is exactly `auto-ref (hook)`.
- **AC-8 (idempotent — AC-9 of the parent item).** Before inserting, the hook checks for an existing `(item_guid, commit_hash)` row (`refExists`) and skips if present; a concurrent duplicate-key race is handled by swallowing `ER_DUP_ENTRY` / `/duplicate/i` as `skipped-duplicate` (and only that error class — any other error propagates). Check: running the ref step twice for the same hash surfaces no duplicate-row error to the user; the second run returns `skipped-duplicate`.
- **AC-9 (never mutates anything but `bow_git_refs` — parent AC-15).** The hook must not, under any path, change an item's `status` (marking something `done` remains a deliberate `node claude-bow.js done` action). Check: a test snapshot of `bow_items.status` before/after an auto-ref run is byte-identical.
- **AC-10 (unknown tags are skipped, not errors).** A tag that does not resolve to a live BOW item is skipped with a `WARN: HEAD <hash> tag [<tag>] does not resolve ...` log line, no ref written, and no error thrown — a `[typo]` tag must not fail the post-commit step for the tags that did resolve.

### Fail-open posture (the whole point — parent AC-11)

- **AC-11 (always exits 0, never a permissionDecision).** Every code path — including the top-level `main().catch(...)` uncaught-error handler — exits 0. The hook never writes a `permissionDecision`; PostToolUse hooks cannot block. Check: a test that forces an uncaught throw inside `main()` still observes exit code 0.
- **AC-12 (unparseable stdin → silent exit).** A `JSON.parse` failure on stdin (after BOM strip) exits 0 silently — nothing to do, never block anything.
- **AC-13 (DB unreachable → log + exit 0).** If `connectReadWrite()` throws (dead DB), the hook logs `FAIL: DB unreachable for HEAD <hash> — <message>` and exits 0 — the commit has already landed, so a missed auto-ref is a recoverable annoyance, not a reason to fail anything further.
- **AC-14 (a throw during auto-ref → log + exit 0).** Any exception from `autoRefForCommit` (e.g. a query error after connect) is caught, logged with its stack, and the hook exits 0.
- **AC-15 (logging itself can never throw).** `log()` wraps `fs.appendFileSync` in a try/catch with an empty handler, so an unwritable log path or full disk cannot take the hook down.
- **AC-16 (log file is gitignored).** The log is `claude-bow-autoref.log` at repo root, covered by the existing `*.log` gitignore rule — no log data (which embeds commit hashes and error text) reaches git. Check: `git check-ignore claude-bow-autoref.log` reports it ignored.

### Tests

- **AC-17 (scratch DB only, never `metro` — ASM-718).** The test suite runs against a scratch database via the `METRO_DB_*` env override (claude-db.js reads `connectionOptions` at call time), never the real `metro` database, so fixture rows never appear in `node claude-bow.js list`/`show`. Check: test setup uses `METRO_DB_NAME=<scratch>` and asserts the real `metro` `bow_git_refs` row count is unchanged before/after the suite.
- **AC-18 (`extractTags` unit).** The exported `extractTags` correctly handles a single tag, multiple tags, no tags, empty brackets `[]`, and malformed brackets — returning exactly the non-empty trimmed bracket contents.
- **AC-19 (`autoRefForCommit` on a fake commit).** The exported `autoRefForCommit(db, { hash, message, branch })` is exercised directly with a fake hash/subject (no real git commit required — this is why the header exports it): a resolving tag → `{ status: 'inserted' }` with a real row; an unknown tag → `{ status: 'unknown-tag' }` with no row; a duplicate → `{ status: 'skipped-duplicate' }`.
- **AC-20 (`insertRefIdempotent`/`refExists` idempotency + race).** A second `insertRefIdempotent` for the same `(item_guid, hash)` returns `skipped-duplicate`; an injected `ER_DUP_ENTRY` (or `/duplicate/i`-messaged error) also returns `skipped-duplicate` rather than throwing, while an injected non-duplicate error still propagates.
- **AC-21 (`readHeadCommit` null paths).** `readHeadCommit()` returns null when not in a repo or when `git log` fails (the `SKIP` path in `main`), and returns a well-formed `{ hash, message }` when a commit is present.
- **AC-22 (fail-open harness + no-status-mutation).** A test that forces each failure mode — unparseable stdin, a mocked `connect` throw, and a mocked `autoRefForCommit` throw — asserts exit code 0 in every case and that no `bow_items.status` changed (proving AC-9 and AC-11 together, not just one).

## Out of scope

- `--amend` commits — deliberately never auto-ref'd (ASM-719); a manual `node claude-bow.js ref <code> <hash>` is the sanctioned path.
- Marking any item `done` — the hook writes `bow_git_refs` rows only (AC-9); `done` stays a deliberate, separate command.
- Any write to a table other than `bow_git_refs`.
- The PreToolUse enforcement half of the `tool.bow` pair — that is `claude-bow-ref-check.js` (FEAT-118, `docs/planning/acceptance/tool.bowrefcheck.md`).

## Escalations

1. **Header-comment vs `settings.json` ordering drift.** The hook's own header says it "sits in `.claude/settings.json`'s PostToolUse Bash matcher (**after** `claude-reflection.js`)". The live `settings.json` lists `node claude-bow-autoref.js` **before** `node claude-reflection.js` in both the Bash and PowerShell PostToolUse matchers. This does not affect behaviour (both are non-blocking PostToolUse hooks), but the header is stale — flagged for Bill to correct the header (or reorder), since the header is the self-documentation AC-16/AC-17 of the parent item rely on.
2. **Missing test coverage (ASM-718).** The hook shipped with no `claude-bow-autoref.test.js`, despite exporting its core logic precisely to be unit-testable. This file's Tests section is the spec for that coverage; whether it is written as a follow-up test item or folded into the existing `claude-bow.test.js` suite is Bill's call.

## Assumptions logged (process v1.7)

- **ASM-718** — retrospective posture + missing test file: the doc's Tests ACs specify coverage to be written/verified (no suite exists as of `781f3ca`); the intended seam is the exported `autoRefForCommit`/`insertRefIdempotent`/`refExists`/`extractTags`/`readHeadCommit` surface, which the header exports for direct testing without a real git commit.
- **ASM-719** — `--amend` commits are never auto-ref'd (hash-rewrite ambiguity); deliberate, accepted gap, requiring a manual `ref`.

- **ASM-917 (FEAT-084 CC fold).** claude-bow-autoref.js header line 43 says the hook sits after claude-reflection.js, but .claude/settings.json lists claude-bow-autoref.js BEFORE claude-reflection.js in both Bash and PowerShell PostToolUse matchers (ordering drift).
