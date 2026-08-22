---
description: Safe-push the current branch to origin (GR#24) — verify authorship + tree, push, confirm sync. Small, frequent pushes beat a backlog.
allowed-tools: Bash(git *), Bash(gh *), Bash(node *)
---

## Context

- Sync state: !`git status -sb | head -1`
- Unpushed commits: !`git rev-list --count @{u}..HEAD 2>/dev/null || git rev-list --count origin/main..HEAD 2>/dev/null || echo "?"`
- Authorship of unpushed commits: !`git log @{u}..HEAD --format='%ae%n%ce' 2>/dev/null | sort -u`

## Your task

Push the current branch's unpushed commits to origin, safely, per **GR#24 "No Code Left Behind" (c): every commit is pushed the same session.** A green commit that only lives locally is one bad reset from gone.

Steps:

1. **Nothing to push?** If the unpushed count is 0, say "already synced" and stop.

2. **Authorship gate (BUG-042).** Every unpushed commit's author/committer email MUST be the noreply address (`…@users.noreply.github.com`). If any other address appears (a real email), STOP and surface it — do not push. GitHub squash-merges re-author server-side, so this project pushes/rebase-merges only; a leaked real email on public `main` cannot be withdrawn.

3. **Size check.** A handful of commits → push directly (step 5). A large backlog (roughly 20+ commits, or anything that has never seen CI) → prefer the **validation-branch flow**: push the tip to a throwaway `wave/<date>-validation` branch, open a PR so CI (incl. the required `perf-1m-probe`) runs, and only fast-forward `main` to the exact SHA once green — this preserves commit hashes so `bow_git_refs` stay valid, and never lands an unvalidated wall of commits on public `main`. (This is the flow used for the 2026-08-13 78-commit wave.)

4. **Local sanity (fast).** If the working tree has staged/committed code changes not yet validated this session, run the cheap gates that fit the disk/time budget: `go build ./...` and the relevant `node --test <files>`. Skip only if the commits were already validated this session.

5. **Push.** `git push origin HEAD:main` (or the current branch). `main` is branch-protected — a fast-forward push from the CLI is the sanctioned path (never a squash-merge). Then verify: `git rev-list --count origin/main..main` must be `0`, and re-confirm `git log origin/main --format='%ae%n%ce' | sort -u` is exactly the noreply address — BOTH fields, never `%ae` alone (BUG-353: a server-side rebase leaks via the COMMITTER field, which an `%ae`-only check cannot see).

6. **Confirm.** Report the pushed range and the new sync state. If CI is watchable (`gh pr checks` / `gh run watch`), note it; do not block on it for a small direct push, but flag if a required check later goes red.

Guardrails: never `--force`/`--force-with-lease` to `main` without an explicit human instruction; never squash-merge (rebase/fast-forward only, BUG-042); if the push is rejected (non-fast-forward), fetch, rebase local commits onto the updated `origin/main`, re-run the authorship gate, and retry — never resolve it by discarding local commits.
