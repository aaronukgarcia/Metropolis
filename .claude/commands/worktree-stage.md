---
description: Create and manage local Git worktrees and clean staging states safely without utilizing banned destructive commands
allowed-tools: Bash, Read
---

# /worktree-stage — use Git worktrees and staging hunks safely without breaking Golden Rule #24

Use this skill when you need to safely switch branches, restore files, or isolate changes. Golden Rule #24 bans all destructive Git commands on the active tree (`git checkout --`, `git restore`, `git reset --hard`, `git clean`, `git stash`) to protect parallel developer work. This command guides you to safe worktree and branch-isolation strategies.

## Safe Git Workflows

### 1. File Reversion / Re-tracking
If you need to undo a local file change:
- **DO NOT** run `git checkout -- <file>` or `git restore`.
- **DO** use a scratch copy backup cycle:
  ```bash
  cp file.go file.go.bak
  # ... perform testing ...
  mv file.go.bak file.go
  ```

### 2. Switching Branches / Segregating Work
If you must switch branches while having uncommitted changes:
- **DO NOT** run `git stash` or `git checkout -f <branch>`. This will sweep away or corrupt other agents' active work.
- **DO** create a temporary local worktree in a separate directory:
  ```bash
  git worktree add ../temp-worktree-branch feature/some-other-branch
  cd ../temp-worktree-branch
  # Perform work in isolated tree, then commit and push
  cd -
  git worktree remove ../temp-worktree-branch
  ```

### 3. Stage Hunks Verbosely
When preparing commits, do not stage files wholesale (`git add .`). Review and stage specific hunks to avoid capturing concurrent work:
```bash
git diff -p <file> # Review diffs
git add -p <file>  # Stage specific safe hunks
```
