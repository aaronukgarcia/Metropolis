# CLAUDE.md - Metropolis Project Brief

> **Last updated:** 2026-08-12 (GR#23 now MECHANICAL: claude-destructive-guard.js blocks code-bearing commits without an accepted verdict in bow_destructive_verdicts — record via `node claude-bow.js destructive <code> --verdict accept|reject ...`. Perf gate live+required per-push. Commit-ready-list protocol + oversight/worker loops in force — see Dev-Team Process below)
> **Read this entire file at the start of every session.**

---

## 🚀 HOW TO START A SESSION (do this before anything else)

**Every Claude Code window must be launched via the `metro` command** (on PATH):

```powershell
metro
```

This runs `metro.bat`, which:
1. Sets `CLAUDE_IDENTITY=Bill` (preferred slot for the primary window)
2. Changes to `E:\git\Metropolis` and starts `claude --add-dir "E:\AI\Memory\source"`

Session coordination is fully self-contained: `claude-sync.js` is a MariaDB port of the Prix Six permit system, backed by this project's own `metro` database (`sync_permits` / `sync_activity` / `sync_file_claims` / `sync_window_map` tables). No Firebase, no shared state with Prix Six.

The `SessionStart` hook then runs checkin automatically and tells you your identity.

---

## ⚠️ GOLDEN RULES — INVIOLABLE, NON-NEGOTIABLE

These rules are inherited from Prix Six and apply to every piece of code written and every response given in this project. No exceptions. No shortcuts. Ever.

| Rule | Summary |
|------|---------|
| #1 | Aggressive Error Trapping — log, type, correlation ID, selectable display |
| #2 | Version Discipline — **Metropolis profile (2026-08-08):** app version = git describe via ldflags + milestone tags; BOW `[mkey]` ref required on engine/UI/data commits; root tooling exempt; verify after every push |
| #3 | Single Source of Truth — no duplication without validation |
| #4 | Identity Prefix — every response starts with `bill>`, `bob>`, or `ben>` |
| #5 | Verbose Confirmations — explicit, timestamped, version-numbered confirmations |
| #6 | GUID Documentation — read comments before changing code, update GUID versions and code.json |
| #7 | Registry-Sourced Errors — every error MUST be created from the error registry, no exceptions |
| #8 | Prompt Identity Enforcement — "who" check, violation logging to Vestige memory, scorekeeping |
| #9 | Shell Preference — Microsoft PowerShell first, then CMD, then bash if needed |
| #10 | Dependency Update Discipline — check for updates on any dependency encountered during bug fixes or feature builds |
| #11 | Pre-Commit Security Review — mandatory security threat modeling before every commit |
| #12 | Dependency & Completeness Check — never mark complete until ALL dependents implemented; never feature without backup, never backup without restore test |
| #13 | Complete All Identified Issues — when user lists N failures, fix ALL N; never "TODO" or "lower priority" them |
| #14 | Memory Recall at Task Start — query Vestige before composing commits, replying to bug reports, or starting a new task type |
| #15 | Validators Derive From Data — expected counts/values must come from data files or runtime queries, never hardcoded constants |
| #16 | Type-Safe Storage Boundaries — never trust TS types about stored data; coerce via `safeX()` helpers |
| #17 | Silent Failure Detection — every service with a user-visible status field MUST have automated freshness monitoring, and every monitoring FAILURE must also write a registry error |
| #18 | Migration Dead-Code Audit — when eliminating a collection/field/feature, audit for orphaned readers/validators in the SAME commit |
| #19 | Deploy Bundling — every commit changing deployable functions MUST end with the deploy command bundling ALL pending function changes |
| #20 | Contract-First, Stub-Forever — modules consume each other ONLY via registered interfaces (GUIDs in code.json); every module keeps a passing stub for life; `internal/ui → internal/engine` imports lint-banned |
| #21 | Red Determinism Gate Stops the Line — any determinism CI failure is auto-P0; nothing else merges until green; revert first, diagnose after |
| #22 | Codename Discipline — the reference title is **'Blue'** and only 'Blue'. Its real name and abbreviations never appear in git: not in code, data, docs, plans, comments, commit messages, or branch names |
| #23 | **Nothing Is Committed Un-Attacked** — every code-bearing commit requires a recorded Destructive verdict on its BOW item(s). Tester PASS proves the criteria hold; only the Destructive proves the code survives someone actively trying to break it. No exceptions for "small", "obvious", "inherited", or "the lead wrote it". **Proportionality tier (Aaron, 2026-08-13, "we are not building NASA code", FEAT-077):** commits whose diff is docs-only (`*.md`) or test-only (`*.test.js`/`*_test.go`) need Tester-level verification only, no Destructive verdict; engine/UI/data code, guards/hooks, and anything in the commit/push path stay full-tier |
| #24 | **No Code Left Behind** (Aaron, 2026-08-13, after a Destructive agent's `git checkout --` destroyed 211 lines of uncommitted work). Three mechanical duties: **(a) Never destroy the working tree** — `git checkout -- <path>`/`.`, `git checkout -f`, `git restore` (non-`--staged`), `git reset --hard`/`--keep`, `git clean -f/-d/-x`, and `git stash` (push/save) are BANNED for everyone but the operator; `claude-worktree-guard.js` blocks them fail-closed. To undo a change, use a scratch copy (`cp f f.bak; …; mv f.bak f`), never a git command that reverts to HEAD. **(b) Commit early, commit often** — never let uncommitted work accumulate; snapshot before dispatching any can-fail work; a can-fail mutation cycle NEVER uses git to restore. **(c) Every commit is pushed the same session** — a green commit that only lives locally is one bad reset from gone; the startup summary's "NOT SYNCED: N ahead" is a P1 to clear, not a note. Push via `/push` (safe-push: verify tree → push → verify noreply authorship). Small incremental pushes are SAFER than a backlog — each gets its own CI run |

> **Full implementation patterns, code templates, and compliance checklists:** `docs/golden-rules-detail.md`
> (Carried over verbatim from Prix Six — Firebase-specific examples apply once Metropolis has its own stack; adapt as the architecture solidifies.)

---

## 🚨 MANDATORY: Session Coordination Protocol

Same protocol as Prix Six (Bill/Bob/Ben slots, 5-min TTL permits, wake recovery, human-only force-evict), but backed by the `metro` MariaDB database instead of Firestore — Metropolis sessions can never collide with Prix Six sessions.

### Session Start — handled by the SessionStart hook

`claude-startup.js` runs `node claude-sync.js checkin` automatically and prints your identity **plus the METROPOLIS STARTUP SUMMARY**: the Book of Work state (which doubles as the metro MariaDB health check — if the BOW summary printed, the DB answered), a Vestige availability check, and the git sync state (dirty files, ahead/behind origin).

Your first response must confirm all of it — identity, hooks, BOW summary, Vestige (live `mcp__vestige__search` worked), and git sync:

```
bill> Good morning, I'm Bill on branch main. BOW: 3 open (1 P1). Vestige live. Git synced. No conflicts detected.
```

If the summary shows git NOT SYNCED or a Vestige problem, surface that to the user immediately. If the summary is missing, run `node claude-bow.js startup-summary` manually.

### 🛑 GOLDEN RULE #4: Identity Prefix — EVERY SINGLE RESPONSE

**EVERY response MUST start with your assigned name prefix** (`bill> `, `bob> `, or `ben> `). Every 5 responses, mentally verify you are still using it. If you drop it: add it immediately and apologise.

### Permit auto-renewal, polling, session end

- Permits have a 5-minute TTL; the `PostToolUse` hook (`claude-ping-check.js`) auto-renews — no manual pings needed.
- Poll coordination state every ~30 seconds / few messages: `node claude-sync.js read`
- When the user says goodnight / end session: `node claude-sync.js checkout --session $env:CLAUDE_SESSION_ID` then sign off gracefully.
- Full command reference: see `claude-sync.js` header comments.

**If you need to modify a file in a NO-TOUCH ZONE, STOP and ask first.**

---

## 👥 Dev-Team Process (MANDATORY for build work — Aaron-directed, 2026-08-08)

Bill (Fable 5) is the **lead: architecture, briefs, final review of test-clean work only, commits, BOW state**. All build work is delegated to subagents on **Sonnet** (save Fable tokens for lead judgement):

```
Bill brief → BA acceptance criteria (docs/planning/acceptance/<mkey>.md, BEFORE dev dispatch)
          → Jnr developer builds to criteria
          → Tester: PASS/FAIL vs criteria ONLY — never fixes; FAIL bounces to the SAME junior (loop)
          → Documentation pass (.md only)
          → Bill final architectural review → commit "[type]: ... [mkey]" → BOW ref + done
```

**Commit-ready-list protocol + loops (Aaron, 2026-08-12, IN FORCE):** the lead is NEVER a blocker. Pipeline-complete work posts its Destructive verdict + evidence **on the BOW item** (`node claude-bow.js destructive ...` — feed prose is NOT a recorded verdict), then the team moves to the next queue item — the answer to "load up more or hold?" is **always load up, never hold**. The lead sweeps and commits in batches on a self-paced `/loop` (oversight sweep every ~25min); worker windows run their own `/loop 15m` so no session ever idles between waves waiting for a human prompt. **Utilisation is measured mechanically (FEAT-076):** every Agent dispatch/stop is logged to `sync_dispatch_events`; `node claude-sync.js util` reports per-hour lanes vs the 12-lane target (`util --now` for the live count, `util --set-target N` to change it). **Queue priority (Aaron, 2026-08-13):** game code beats framework — lanes go to S3/engine/UI/data items first; tooling/guard work is capped at background-lane levels while game work is unblocked. We are not building NASA code (GR#23 proportionality tier, FEAT-077).

The cadence is **pipelined across sprints** (v1.4): coders build sprint N while BAs (plural allowed, disjoint sprint ownership) write user stories + acceptance criteria for N+1…N+3, the Tester clears N as it lands, and Bill reviews/commits N and freezes sprint gates. BA / Tester / Documentation are **persistent agents** — reuse them via follow-up messages, don't respawn per item. Basic errors must never reach Bill. Additionally an **independent QA agent** (never talks to the other agents, reports ONLY to Bill) audits the pipeline itself: re-verifies samples of Tester evidence, checks code.json/BOW/plan for drift, Golden Rules compliance, and spot-checks code quality (error trapping, inline docs, naming, data types, capitalisation) — advisory, at least once per wave. Full role mandates: `docs/planning/dev-team-process.md`.

## 📋 Book of Work (BOW)

The BOW is the **single source of truth for planned/active work**: modules, features, bugs, interfaces. It lives in the `metro` MariaDB (`bow_items` / `bow_dependencies` / `bow_comments` / `bow_git_refs`) and is driven entirely through `claude-bow.js` — never raw SQL for writes. Every item has a GUID, short code (`MOD-001`/`FEAT-001`/`BUG-001`/`INT-001`), priority `P0`–`P3`, status, dependency links (cycle-checked; `done` refuses while dependencies are open — GR#12), comments that may carry example code, and git commit refs.

- View/manage: `/bow` skill, or `node claude-bow.js list | show <CODE> | add | comment | depend | ref | set | done`
- **After committing work tracked by an item:** `node claude-bow.js ref <CODE> <hash>` then `done <CODE> --note "..."`
- New work discovered mid-task gets a BOW item immediately.
- The checkin startup summary shows the top of the BOW every session.

---

## What is Metropolis?

A **deterministic city-simulation game in Go** with a tcell TUI: persistent individual citizens (Option B — no culls ever, up to 100M at adaptive fidelity), real OS Terrain 50 Kent geography (Folkestone start tile), a two-layer clock, and contract-first modules (GR#20) behind a protocol-only UI/engine split. The full design is `docs/METROPOLIS-MASTER-v2.1.md` (SSOT: `master-plan-v2.1.json` → `tools/plan/generate.js` → `code.json` + BOW). Build order is sprints S0–S11 (`docs/planning/sprint-plan-v1.md`); as of 2026-08-14 **S0–S2 are closed and S3's core has landed** (engine.world, engine.citizens with Destructive ACCEPT, engine.season; BUG-034's 1M perf gate is live+required and closed). **The active milestone is FEAT-083 "Baseline One"** (Aaron, 2026-08-14: "it's a game, not NASA code" — the loop must RUN: citizens consume, money moves, you build, migration responds, watchable via cmd/metropolis on the real engine): spine order finance (MOD-022) → build (MOD-026) → consumption → attract → households, then **FEAT-082 composition root** (the keystone — today every runnable path is zero-phase-hook StubEngine; nothing is wired to the tick). Traffic/roads/logistics get coarse approximations for baseline one; screens/services/unlocks and deeper systems wait. Bug backlog executes per the approved FEAT-085 cluster plan and FEAT-084 ASM close-out (both head-dev-gated: plans posted on the item + approved before execution; verdict 2026-08-14 APPROVED WITH AMENDMENTS A1–A4).

**Human developer:** Aaron. All architectural decisions go through Aaron.

---

## Environment

- **Platform:** Windows
- **Node:** `C:\Program Files\nodejs\node.exe`
- **Project root:** `E:\git\Metropolis`
- **Launcher:** `metro.bat` (in `C:\Users\aarongarcia\AppData\Local\Microsoft\WindowsApps`, on PATH)
- **Database:** MariaDB 12.2, database `metro` on localhost:3306 (root, no password). Client: `"C:\Program Files\MariaDB 12.2\bin\mysql.exe"`. Charset utf8mb4. Created 2026-08-08. Tables: `project_meta` (project facts, `status` row = online, `dispatch_target_lanes` = utilisation target) + `sync_permits`/`sync_activity`/`sync_file_claims`/`sync_window_map`/`sync_dispatch_events` (session coordination + FEAT-076 agent dispatch/stop log) + `bow_items`/`bow_dependencies`/`bow_comments`/`bow_git_refs` (Book of Work). Override connection via `METRO_DB_HOST/PORT/USER/PASSWORD/NAME` env vars.
- **MCPs:** configured user-level in `C:\Users\aarongarcia\.claude.json` (Vestige memory, GitHub, MS 365, etc.) — available automatically in every session
- **Docker:** installed, engine 29.6.2, **Linux** containers. **CLI is NOT on PATH** — invoke by full path: `& "C:\Program Files\Docker\Docker\resources\bin\docker.exe"`. ⚠️ **Flaky under load, but usable (2026-08-09):** the first real workload (`go build ./...` in `golang:1.25`) killed the daemon outright — `unexpected EOF`, Docker Desktop's process gone, WSL distro `Stopped`. A Tester then recovered it with **two restart cycles** (it dropped once more mid-pull, succeeded on the second) and ran a full Linux test pass successfully. So: **expect to restart the daemon, budget a retry, and treat a Linux run as a bonus rather than a gate** — but it does work. Cause of the drops unknown; resource limits are the obvious suspect. *Why we want it working:* it would give a **second GOOS locally** — every "passes locally, fails on CI" defect this project has had (BUG-004's CRLF/gofmt, the never-green-CI saga) came from testing on Windows only. Target command once it's stable: `docker run --rm -v ${PWD}:/src -w /src golang:1.25 go test ./...`
- **WSL:** installed, but the **only** distro is `docker-desktop` (Docker's internal utility VM). There is **no general-purpose Linux distro**, so `wsl <command>` is not a route to arbitrary Linux tooling unless one is installed. Use Docker for Linux work, or the Bash tool (Git Bash) for POSIX shell.

---

## Hooks (inherited from Prix Six)

Configured in `.claude/settings.json`; scripts live in the project root:

| Hook | Script | Purpose |
|------|--------|---------|
| PreToolUse (Bash) | `claude-version-guard.js` | GR#2 Metropolis profile (FEAT-002): denies rogue hand-maintained version files, warns on engine commits; fail-open hygiene guard |
| PreToolUse (Bash) | `claude-pre-commit-check.js` | Blocks Co-Authored-By trailers in commits |
| PreToolUse (Bash) | `claude-pre-push-check.js` | Blocks pushes with unbundled function deploys (GR#19) |
| PreToolUse (Bash+PS) | `claude-codename-guard.js` | GR#22 — blocks the reference title's real name reaching git |
| PreToolUse (Bash+PS) | `claude-author-guard.js` | BUG-035 — blocks a commit whose author/committer is not a sanctioned identity (derived at runtime from git config ∪ trunk history ∪ operator env list, never hardcoded) |
| PreToolUse (Bash+PS) | `claude-destructive-guard.js` | GR#23/FEAT-040 — fail-closed: blocks a code-bearing commit whose `[mkey]`-resolved BOW item(s) lack an accepted verdict in `bow_destructive_verdicts`; record verdicts with `node claude-bow.js destructive <code> --verdict ...` |
| UserPromptSubmit | `claude-memory-prefetch.js` | GR#14 Vestige recall reminder |
| SessionStart | `claude-startup.js` | Auto checkin + identity assignment |
| PreCompact | (inline echo) | Preserves identity + Golden Rules context across compaction |
| PostToolUse | `claude-ping-check.js` | DHCP-style permit auto-renewal |
| PostToolUse (Bash) | `claude-reflection.js` | Post-action Golden Rules reflection |
| statusLine | `claude-statusline.js` | Identity/status display |

> **Note:** `claude-version-guard.js` was retargeted from the Prix Six two-file layout to the Metropolis Go profile on 2026-08-09 (FEAT-002): app version comes from `git describe` via ldflags (M0-ENG §3), so the guard now denies commits introducing rogue hand-maintained version files and warns on engine commits. `[mkey]` enforcement is MOD-007's scope.

---

## Skills (.claude/commands) — inherited from Prix Six

**Process skills** (`/commit`, `/bump`, `/bye`, `/rca`, `/diagnose`, `/health-check`, `/security-audit`, `/silent-failures`, `/memory-hygiene`, `/audit`, `/danger`, `/upgrade`) are project-agnostic and usable now. **Metropolis-native:** `/bow` (metro BOW via `claude-bow.js`), `/sprint` (sprint board + ready-to-build), `/update` (Aaron's "update" command — sync skills/hooks/memory/Vestige/BOW/CLAUDE.md with reality), `/codejson-audit` (plan-drift + registry consistency), `/register-guid` (master-plan registration flow), `/new-error` (MET-xxx registry codes) — all 2026-08-08. **Retired 2026-08-08 (Aaron-approved, recoverable from git):** the 13 Prix-Six Firebase skills (`/openf1`, `/check-race-data`, `/bot-status`, `/cc`, `/fn-status`, `/deploy`, `/rules-deploy`, `/iam-check`, `/new-secret`, `/new-collection`, `/feedback`, `/triage-errors`, `/fs`) plus `/sync-codejson`, whose job (hand-registering GUIDs into code.json) is impossible by design now that code.json is generated from the master plan. `/health-check` gains determinism-gate/perf-CI/ready-queue checks when those exist (BOW-tracked).

---

## Git Discipline

| Branch | Purpose |
|--------|---------|
| `main` | Production-ready code only — never commit directly once CI/CD exists |
| `develop` | Integration branch for features |
| `feature/*` | Individual feature work |

Commit message format: `[type]: brief description` — types: `feat`, `fix`, `refactor`, `docs`, `chore`, `test`.
**`[mkey]` tag convention (Aaron, 2026-08-11):** always tag the most specific key — the feature key when one exists (e.g. `[data.catalogue]`), the module key only when no feature key applies.
**No Co-Authored-By trailers** (enforced by hook).

Remote: **public** GitHub repository `aaronukgarcia/Metropolis` (public since 2026-08-10).

**Merge policy: `gh pr merge --rebase` ONLY — never squash.** GitHub builds squash commits server-side from the account's public email, which leaked the real address onto public main twice on 2026-08-10 (BUG-042); rebase merges preserve the branch commits' noreply authorship verbatim. After every merge, verify: `git log origin/main --format='%ae' | sort -u` → exactly the noreply address. Durable fix pending: Aaron flips GitHub Settings → Emails → "Keep my email addresses private" + "Block command line pushes that expose my email".

---

## Versioning

App version = `git describe` via ldflags + milestone tags (GR#2 Metropolis profile, M0-ENG §3) — never hand-maintained version files; `claude-version-guard.js` denies any commit introducing one (FEAT-002; MOD-001's app skeleton was cancelled 2026-08-08). Root `package.json` exists only for hook-script dependencies (`mysql2` for claude-sync) and is version-guard-exempt.

---

## Compacting / Context Recovery

When you compact the conversation, you **must**:
1. Re-read this entire `CLAUDE.md` file
2. Run `node claude-sync.js read` to check coordination state
3. Inform the user you are caught up with the instructions

**Mid-cycle recovery (session death during dev-team work):** read `docs/planning/checkpoint.md` (the RM-maintained heavy checkpoint), `node claude-bow.js list --by-seq` + the in-progress items' comments, and `git log -10`. Reconstruct the team board from the checkpoint, re-dispatch in-flight work with fresh agents (old agent transcripts are gone — the checkpoint is written to be self-sufficient), never redo committed work. Standing orders from Aaron live in the checkpoint's "Standing orders" section.

---

*This file is the single source of truth for project context. Keep it updated.*
