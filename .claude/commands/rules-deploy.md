---
description: Deploy Firestore security rules to production — separate from App Hosting, required whenever firestore.rules changes
allowed-tools: Bash(npx firebase-tools:*), Bash(git diff:*), Bash(git log:*), Bash(grep:*)
---

## Context

- Current branch: !`git branch --show-current`
- firestore.rules last changed: !`git log --oneline -3 -- app/src/firestore.rules`
- Staged/unstaged rules changes: !`git diff HEAD -- app/src/firestore.rules | head -40`

## Your task

Deploy Firestore security rules to production. Work through every step in order.

---

### ⚠️ CRITICAL REMINDER BEFORE ANYTHING ELSE

**App Hosting build does NOT deploy Firestore rules.**

When you push to `main`, Firebase App Hosting deploys the Next.js app only. Firestore rules sit in a completely separate Firebase subsystem and require an explicit deploy command. A rules change committed to git is NOT live until this skill is run.

Lesson learned (BUG-PC-003, 2026-03-03): `official_teams` had no rule → default deny → `getOfficialTeams()` silently returned `[]` → Team Lens showed no drivers for 3 teams. The rule was in git but never deployed.

---

### STEP 1 — Review what changed in firestore.rules

Show the diff of the current rules file against the last commit:

```bash
git diff HEAD -- app/src/firestore.rules
```

If there are no changes, check if the user wants to redeploy the current rules anyway (e.g. after a force-reset or to confirm production matches source).

---

### STEP 2 — New collection audit

For every new `match /COLLECTION_NAME/{...}` block added in the diff:

Ask yourself and state the answer for each:
- [ ] Does this collection store user PII? (should require `isSignedIn()` at minimum)
- [ ] Is write access restricted to admin only? (`isAdmin()`)
- [ ] Is there a corresponding entry in `docs/COLLECTIONS.md`?

Flag any collection that has `allow read, write: if true;` — that is a security hole and must NOT be deployed.

---

### STEP 3 — Deploy the rules

Run:

```bash
npx firebase-tools deploy --only firestore:rules --project studio-6033436327-281b1
```

Watch for:
- `rules file app/src/firestore.rules compiled successfully` — compilation passed
- `released rules app/src/firestore.rules to cloud.firestore` — rules are live

If the deploy fails with a compilation error, show the error and stop. Do not attempt workarounds — fix the rules syntax first.

---

### STEP 4 — Confirm

Report with your identity prefix:

```
bill> ✅ Firestore rules deployed
     Compiled: ✅
     Released to cloud.firestore: ✅
     New collections added: [list or "none"]
     Collections audited: [pass/flag for each new one]
```

If any new collection has a security concern:
```
bill> ⚠️ Rules deployed but security review needed:
     [collection name]: [concern]
     Action required: [what to fix]
```
