---
description: Create a new error code in the Prix Six error registry — Golden Rule #7 compliant
allowed-tools: Bash(grep:*), Bash(node:*)
---

## Context

- Existing error codes (last 20): !`grep -n "PX-[0-9]" app/src/lib/error-codes.ts | tail -20`
- Highest current code: !`grep -o "PX-[0-9]*" app/src/lib/error-codes.ts | sort -t- -k2 -n | tail -1`
- Error registry structure: !`head -40 app/src/lib/error-registry.ts 2>/dev/null || echo "check app/src/lib/error-registry.ts"`

## Your task

A new error needs to be added to the Prix Six error system. Every error in this application MUST originate from the registry — no hardcoded strings, no raw PX codes outside the registry.

---

### STEP 1 — Identify the error details

Determine (from context or ask the user):

- **What module** is this error in? (Auth, Predictions, Scoring, Teams, etc.)
- **What scenario** causes this error?
- **What should the user see?** (short, clear, non-technical message)
- **What severity?** (critical / error / warning / info)
- **Is it user-facing** (needs a display message) or internal-only?

---

### STEP 2 — Assign the next PX code

Look at the highest existing code from the context above. The next code is highest + 1.

Format: `PX-NNNN` (zero-padded to 4 digits, e.g. PX-2003)

---

### STEP 3 — Add to error-codes.ts

Add the new entry to `app/src/lib/error-codes.ts` following the existing pattern:

```typescript
// [MODULE] — [Brief description]
KEY_NAME: {
  code: 'PX-NNNN',
  message: 'User-facing message here',
  severity: 'error',    // critical | error | warning | info
  module: 'ModuleName'
}
```

Place it in the correct module section. If the module section doesn't exist, create it with a comment header.

---

### STEP 4 — Add to error-registry.ts

Add the corresponding entry in `app/src/lib/error-registry.ts` following the existing pattern so it's accessible as `ERRORS.KEY_NAME`.

---

### STEP 5 — Show usage pattern

Display the correct usage pattern for the new error so it can be dropped straight into code:

```typescript
// Throwing a traced error
import { ERRORS } from '@/lib/error-registry';
import { TracedError } from '@/lib/traced-error';

throw new TracedError(ERRORS.KEY_NAME, correlationId, {
  context: 'additional context here'
});

// Logging without throwing
import { logError } from '@/lib/error-codes';
await logError(ERRORS.KEY_NAME, correlationId, db, { userId });
```

---

### STEP 6 — Confirm

```
bill> ✅ Error PX-NNNN created
     Key: ERRORS.KEY_NAME
     Message: "User-facing message"
     Module: ModuleName | Severity: error
     GR#7 ✅ — registry-sourced, no hardcoded strings
```
