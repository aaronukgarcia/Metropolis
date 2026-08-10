---
description: Project health check — determinism gate, CI health, BOW ready queue, git sync, Vestige (Metropolis profile)
allowed-tools: Bash(node:*), Bash(git:*), Bash(gh:*)
---

## Context

- Current branch: !`git branch --show-current`
- Git status: !`git status --short`
- Last 3 commits: !`git log --oneline -3`

## Your task

Run a full project health check. This is a **read-only diagnostic** — do not fix anything unless
asked. `tools/health/check.js` (module key `tool.healthcheck`, FEAT-027) does the heavy lifting:
it derives everything it reports from a live source at run time (GR#15 — no hardcoded job names,
counts, or "should be green" assumptions), and it is the file you'd edit if a check needs to
change. Run it and paste its real output — do not describe what it would say:

```bash
node tools/health/check.js
```

Exit code: `1` = at least one `[FAIL]`, `2` = no FAIL but at least one genuine `[UNCONF]`, `0` =
clean or only determined `[WARN]`s. Read its report top to bottom; it already covers:

1. **CI workflow structure** — jobs parsed live from `.github/workflows/ci.yml`.
2. **Determinism gate presence + result (GR#21)** — a red determinism gate is auto-P0 and blocks
   every other merge. The script surfaces this unmissably as its own `DETERMINISM GATE` section,
   and is explicit about whether the result is *confirmed for the current HEAD commit* or
   genuinely `[UNCONF]` (stale — the run it found was for a different SHA — or the run hasn't
   finished yet). `[UNCONF]` is a distinct axis from `[WARN]`: `[WARN]` means the check determined
   a real, not-clean state (e.g. dirty git tree); `[UNCONF]` means the check could not reach a
   verdict at all. Never report a determinism-gate result the script marked `[UNCONF]` as if it
   were a clean PASS — that exact failure mode (a run started, never confirmed finished, main red
   for three commits) is BUG-021 in the BOW, and is also why the script's own `OVERALL` line keeps
   FAIL / UNCONFIRMED / ATTENTION NEEDED / OK as four distinct outcomes rather than folding
   "not clean" and "don't know" together.
3. **`-race` flag presence (SEC-028)** — confirms `-race` is still on every `go test` step in CI.
   This flag was missing for the project's entire history until 2026-08-09, and the whole
   copy-guard (SEC-003..SEC-020) chain was unverified because of it — a FAIL here is not cosmetic.
4. **build-test-vet / lint status** — from the latest completed run's per-job breakdown.
5. **perf-CI job presence** — reports honestly that no perf-CI job exists yet in `ci.yml`. Per
   `docs/planning/sprint-plan-v1.md`, that job is expected to land with `harness.synth` (H-SYNTH,
   Sprint 2) — its current absence is `INFO`, not `FAIL`. If the script's `perf-CI job presence`
   section ever flips to reporting a job found, or you independently learn `harness.synth`/MOD-016
   has landed while the job is still absent, treat that as new information worth a BOW check
   (`node claude-bow.js show MOD-016`).
6. **Git sync state** — branch, uncommitted files (staged/unstaged/untracked), and ahead/behind
   counts. The default branch is never assumed to be `main` — it's read live via
   `gh api repos/:owner/:repo --jq .default_branch` (falling back to the git remote's
   `origin/HEAD` if `gh` is unavailable). Reporting now distinguishes states that used to be
   collapsed into one misleading "NOT SYNCED" warning (BUG-031's pattern — the same tool bounced
   once already for training the reader to skim a warning that fired on every normal state):
   - **Feature branch, no upstream** — this is the required workflow once the default branch is
     protected (all work lands via PR). Reported plainly as normal, alongside how far the branch
     has diverged from `origin/<default>` (unpushed/divergent commit counts) so a *genuine* problem
     (e.g. way behind main) is still visible.
   - **Feature branch, with upstream** — reports ahead/behind against the upstream itself AND
     separately against `origin/<default>`, since "in sync with my own branch" and "in sync with
     main" are different questions and only the second tells you whether a merge will hurt.
   - **Default branch** — unchanged expectation of having an upstream; being ahead of
     `origin/<default>` is flagged `[WARN]` because direct pushes to a protected default branch are
     refused — those commits need a PR.
   - Uncommitted files are reported as before (`[WARN]` if any).
7. **Branch protection on the default branch** — confirms it's actually still ON via
   `gh api repos/:owner/:repo/branches/<default>/protection`. A 404 means protection is OFF and is
   reported `[FAIL]`, not `[WARN]` — a silently-removed control that direct pushes now succeed
   against is exactly the kind of drift nobody notices (GR#17-adjacent). Repo/owner comes from
   `gh`'s own `:owner/:repo` resolution (reads the git remote), never a hardcoded repo name.
8. **BOW ready queue** — P0 items with no open dependencies, and total ready count, via
   `node claude-bow.js ready`. Also checks `node claude-bow.js list --status blocked` for anything
   stuck.

### Vestige availability (the one thing the script cannot check)

`tools/health/check.js` is a Node script and cannot call MCP tools. Confirm Vestige live yourself:

```
mcp__vestige__search query="metropolis health check" limit=1
```

- ✅ Returns a result set (even empty) without error: Vestige is live.
- ❌ Tool call errors: Vestige is down — flag it, this breaks GR#14 (memory recall at task start)
  project-wide, not just for this check.

### GR#17 — a monitoring FAILURE must not just print

If the script's `OVERALL` line reads `FAIL` (not `UNCONFIRMED`), don't stop at printing the
report. For each `[FAIL]` section:

1. Check whether a BOW item already tracks it (`node claude-bow.js list` / `show <code>` — search
   by the section's subject, e.g. "determinism gate", "-race").
2. If nothing tracks it, file one immediately so the failure has a registry-visible record instead
   of living only in this run's terminal output:
   ```bash
   node claude-bow.js add bug "<what failed, exact section text>" --priority P0 \
     --code-path ".github/workflows/ci.yml" --codejson "tool.healthcheck" \
     --desc "<what tool.healthcheck found, and why it's P0/P1 — cite GR#21/SEC-028 if applicable>"
   ```
   A determinism-gate FAIL is always P0 (GR#21, non-negotiable). A missing/removed `-race` flag is
   P0 (SEC-028 precedent — it hid a real concurrency defect chain project-wide). Other FAIL
   sections use judgement, default P1.
3. Never file a duplicate for a FAIL already tracked — comment on the existing item
   (`node claude-bow.js comment <CODE> "..."`) instead.
4. `UNCONFIRMED` (e.g. a run still in progress, or the latest run is for a different commit than
   HEAD) is not itself a FAIL — say so plainly, and re-run the check once the run completes rather
   than filing a bug against an unfinished result.

---

### Final health report

Present as a clean dashboard, e.g.:

```
bill> Metropolis Health Check — [date]
     ─────────────────────────────────────────
     Determinism gate:   ✅ green for HEAD <sha> / ❌ RED (P0) / ⚠ unconfirmed (stale or in-progress)
     -race flag:         ✅ present in all go-test jobs / ❌ missing from <job>
     build-test-vet:     ✅ / ❌
     lint:                ✅ / ❌
     perf-CI:             📋 not yet wired (expected pre-harness.synth) / ✅ present & green / ❌ present & red
     Git sync:            ✅ clean, in sync / 📋 feature branch, no upstream (normal) / ⚠ N uncommitted / ⚠ ahead of origin/<default>
     Branch protection:   ✅ ON for <default> / ❌ OFF (404) / ❓ unconfirmed
     BOW P0 ready:        N items — [list] / ✅ none
     BOW blocked:         ✅ none / ⚠ N items
     Vestige:              ✅ live / ❌ down
     ─────────────────────────────────────────
     Overall: ✅ HEALTHY / ⚠️ ATTENTION NEEDED (determined, not clean) / ❓ UNCONFIRMED (could not verdict) / ❌ ISSUES FOUND (P0 filed: <codes>)
     Next recommended action: [one line]
```
