---
description: Version bump — update package.json and version.ts to stay in sync, then confirm
allowed-tools: Bash(node:*), Bash(grep:*)
---

## Context

- Current version in package.json: !`node -e "try{console.log(require('./app/package.json').version)}catch(e){console.log('NOT FOUND')}" 2>/dev/null`
- Current version in version.ts: !`grep -o "APP_VERSION = '[^']*'" app/src/lib/version.ts 2>/dev/null || echo "NOT FOUND"`
- Last 3 version commits: !`git log --oneline --grep="v[0-9]" -3`

## Your task

Bump the version in BOTH required locations. These must always be identical — a mismatch means the About page shows a different version than what's in the codebase.

---

### STEP 1 — Determine the new version

Versioning scheme: **MAJOR.MINOR.PATCH**

- **PATCH** — bug fixes, minor tweaks, no new features
- **MINOR** — new features, backward compatible
- **MAJOR** — breaking changes or significant new functionality

Based on the current work being committed, determine the appropriate bump level and state the new version number before making any changes.

If the user has specified a version number, use that exactly.

---

### STEP 2 — Update app/package.json

Edit the `"version"` field in `app/package.json` to the new version number.

---

### STEP 3 — Update app/src/lib/version.ts

Edit the `APP_VERSION` constant to match exactly. It must be the same string as in package.json.

---

### STEP 4 — Verify both files match

After editing, read both files and confirm the version strings are identical:

```
bill> ✅ Version bumped to vX.Y.Z
     app/package.json: "version": "X.Y.Z" ✅
     app/src/lib/version.ts: APP_VERSION = 'X.Y.Z' ✅
```

If they don't match, fix immediately before proceeding.

---

### STEP 5 — Reminder

The version will only appear correctly on https://prix6.win/about and https://prix6.win/login **after a successful deploy to main**. Run `/deploy` after pushing to verify.
