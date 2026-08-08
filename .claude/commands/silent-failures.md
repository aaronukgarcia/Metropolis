---
description: Scan for silent failure patterns — catch blocks that swallow errors, functions that return empty on permission denied
allowed-tools: Bash(grep:*)
---

## Context

- Source root: `app/src/`

## Your task

Scan the codebase for silent failure patterns — places where errors are caught and discarded, returning empty values instead of surfacing the problem. These patterns cause bugs that look like data issues but are actually permission or network failures.

**Lesson learned (BUG-PC-003, 2026-03-03):** `getOfficialTeams()` caught a Firestore permission denied error and returned `[]`. Team Lens showed no drivers for 3 teams. Zero logs, zero toasts, zero indication anything was wrong. Root cause took 30 minutes to find.

---

### SCAN 1 — Empty catch blocks and swallowed errors

```bash
grep -rn "catch.*{}" app/src --include="*.ts" --include="*.tsx"
grep -rn "catch.*return \[\]" app/src --include="*.ts" --include="*.tsx"
grep -rn "catch.*return null" app/src --include="*.ts" --include="*.tsx"
grep -rn "catch.*return {}" app/src --include="*.ts" --include="*.tsx"
grep -rn "catch.*return false" app/src --include="*.ts" --include="*.tsx"
grep -rn "catch.*return 0" app/src --include="*.ts" --include="*.tsx"
grep -rn "\.catch(() => {})" app/src --include="*.ts" --include="*.tsx"
grep -rn "\.catch(_ => {})" app/src --include="*.ts" --include="*.tsx"
```

---

### SCAN 2 — Firebase/Firestore calls with no error handling

```bash
grep -rn "\.get()" app/src --include="*.ts" --include="*.tsx" | grep -v "\.then\|await\|catch\|try"
grep -rn "getDoc\|getDocs\|collection\|query" app/src --include="*.ts" --include="*.tsx" | grep -v "catch\|try\|error" | grep -v "//\|import"
```

---

### SCAN 3 — Functions that return empty arrays (potential silent failures)

```bash
grep -rn "return \[\]" app/src --include="*.ts" --include="*.tsx"
grep -rn "return \[\]" app/src/lib --include="*.ts"
grep -rn "return \[\]" app/src/firebase --include="*.ts"
```

---

### SCAN 4 — Dev-only error logging (silenced in production)

```bash
grep -rn "NODE_ENV.*development" app/src --include="*.ts" --include="*.tsx" -A 2 | grep -i "console\|log\|error"
```

These patterns mean errors appear in dev but are completely silent in production.

---

### Triage and classify each finding

For every match found, classify it as one of:

| Class | Meaning | Action |
|-------|---------|--------|
| **INTENTIONAL** | Deliberate resilience (e.g. analytics init, non-critical background ops) | Document why — add comment `// intentional: [reason]` |
| **RISKY** | Silent failure that could mask a real bug (e.g. auth functions, data fetches) | Add error logging — at minimum `console.error` in dev, `logError()` for server-side |
| **BUG** | Confirmed silent failure — something that has or will cause a wrong UI state | Create BOW item + fix |

---

### Report format

```
bill> 🔇 Silent Failure Scan complete

INTENTIONAL (acceptable):
  - [file:line] — [why it's ok]

RISKY (should log errors):
  - [file:line] — [what it silences, recommended fix]

BUG (must fix):
  - [file:line] — [what it causes, BOW item created: ID]

Summary: N intentional, N risky, N bugs
Recommended next action: [e.g. "Add error logging to 3 risky patterns"]
```

If no findings, report:
```
bill> ✅ No silent failure patterns found
```
