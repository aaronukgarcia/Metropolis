---
description: SAFE health check — last nightly run, archives, repos, services, tidy, Aaron's queue, git sync, secret scan, Vestige pointer, agent tokens. Read-only. (shared edition, SAFE-239: one skill for every project; SAFE-wide checks run against E:/projects/63-SAFE and a project's own checks go in .claude/health-extra.md. Metropolis/SitRep keep their own tools/health/check.js edition.)
allowed-tools: Bash(node:*), Bash(git:*), Bash(ls:*), Bash(tail:*)
---

## Context

- Branch and sync: !`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then git status -sb | head -1; else echo "not a git repository (SAFE-239)"; fi`
- Last 3 commits: !`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then git log --oneline -3 2>/dev/null || git status -sb; fi`
- BOW: !`if [ -f tools/bow-summary.js ]; then node tools/bow-summary.js --limit 5 2>&1; else echo "BOW: tools/bow-summary.js is not installed in this project (node E:/git/forge/forge.js upgrade <slug> --apply installs it); read the BOW with SQL instead (SAFE-215, SAFE-226)"; fi`

## Your task

A **read-only** diagnostic: report, do not fix (open a BOW item for anything that needs work). Run each check and paste its real output; never describe what a check would say. Every line of the final report is PASS, WARN, FAIL or UNCONF (could not reach a verdict); never fold UNCONF into PASS.

SAFE_HOME is E:/projects/63-SAFE. Every SAFE script and file named below is read from there, whichever project this window is in.

1. **Status** — `node E:/projects/63-SAFE/safe-status.js` (prints the STATUS.md text to stdout without touching the published copy; exit 1 means findings, 2 fatal). Its Findings are the FAIL/WARN list; Waiting on Aaron leads the report.
2. **Last nightly** — newest `E:\SAFE\logs\nightly_*.log`: date, `safe-backup exit`, and any stream marked FAILED. Older than 30 h = FAIL. `ALERT.md` present in the RECAL OneDrive `Documents\SAFE` folder or `SAFE-ALERT.md` on the Desktop = FAIL.
3. **Tidy** — `TIDY.md` beside STATUS.md is from the last 30 h; quarantine days past retention are listed for Aaron, not acted on.
4. **Git** — this repo clean and pushed (`git status -sb` shows no ahead/behind); a project with no repository reports UNCONF with the reason, not FAIL.
5. **Secret scan** — gitleaks resolves (on PATH or under `%LOCALAPPDATA%\Microsoft\WinGet\Packages\Gitleaks.Gitleaks_*`); `~/.git-hooks/pre-push` equals `E:/projects/63-SAFE/githooks/pre-push` and `~/.git-hooks/gitleaks.toml` equals `E:/projects/63-SAFE/githooks/gitleaks.toml` (a missing config blocks every push: FAIL); any `BLOCK` or `NOSCANNER` line in `~/.claude/secret-scan.log` in the last 7 days is listed.
6. **Vestige** — `mcp__vestige__memory_status` answers; pointer index row count (`SELECT COUNT(*) FROM safe.vestige_pointer`) and the age of the newest `vestige_pointer_build` row (older than 7 days = WARN, refresh at `/update`).
7. **Agent tokens** — `node E:/projects/63-SAFE/tools/usage-ledger.js report --by project --since <7 days ago>`; newest `dispatch_usage.at` older than 2 days = WARN (nightly harvest not running).
8. **This project's own checks** — if .claude/health-extra.md exists in this project, read it and run what it says (a database connection, a workbench status, a tool check). Add each as a report line "PASS|WARN|FAIL|UNCONF  extra:<name>  evidence". Absent means no line, not a failure.

## Report

```
<prefix> Health — <project> <date time>   (<prefix> = this window's reply prefix from CLAUDE.md)
OVERALL: OK | ATTENTION | FAIL | UNCONFIRMED
Waiting on Aaron: <codes, or none>
<one line per check: PASS|WARN|FAIL|UNCONF  check  evidence>
Next: <the single most useful action>
```
