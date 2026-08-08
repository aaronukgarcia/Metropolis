---
description: Register a new GUID block in code.json for newly written code — keeps the registry current as you code (GR#6). Also checks GR#1 error trapping, GR#3 SSOT, GR#7 registry errors, GR#11 security.
allowed-tools: Bash(node:*), Bash(grep:*)
---

## Context

- Recent git diff (new/modified files): !`git diff --name-only HEAD 2>/dev/null | head -20`
- Staged changes (new code): !`git diff --cached --stat 2>/dev/null | head -20`
- Current code.json total_guids: !`node -e "try{console.log(require('./code.json').total_guids+' GUIDs')}catch(e){console.log('ERROR')}" 2>/dev/null`

## Your task

Register one or more new GUID blocks in `code.json` to reflect code that was just written or modified. This fulfils Golden Rule #6.

The user will tell you (or it is clear from context):
- Which file the new code is in
- What function/component/constant was added or modified

---

### STEP 1 — Identify the file and determine the GUID prefix

Ask the user if not clear from context: "Which file and function/block are you registering?"

Once you have the file path, determine the GUID prefix using this mapping convention:

| Path pattern | Prefix example |
|---|---|
| `app/src/app/(app)/admin/_components/Foo.tsx` | `ADMIN_FOO` |
| `app/src/app/(app)/predictions/` | `PAGE_PREDICTIONS` |
| `app/src/app/api/some-route/route.ts` | `API_SOME_ROUTE` |
| `app/src/lib/foo.ts` | `LIB_FOO` |
| `app/src/components/Foo.tsx` | `COMPONENT_FOO` |
| `app/src/firebase/foo.ts` | `FIREBASE_FOO` |
| `app/src/contexts/foo.tsx` | `CONTEXT_FOO` |
| `functions/index.js` | `FUNCTIONS` |
| `whatsapp-worker/src/foo.ts` | `WHATSAPP_FOO` |

---

### STEP 2 — Find existing GUIDs in that file

```bash
grep -n "GUID:" <filePath>
```

List the GUIDs already in the file. The next sequential number for a new block is `max_existing + 1`.

If the file has no GUIDs yet, start at `-000`.

---

### STEP 3 — Determine version number

Check code.json for any existing entries for the same GUID (the block may have been registered before under an older version):

```bash
node -e "const d=require('./code.json'); const g=d.guids.filter(x=>x.guid.startsWith('PREFIX')); g.forEach(x=>console.log(x.guid,'v'+x.version));"
```

If the GUID is new: version = 1.
If updating an existing GUID: version = current + 1.

---

### STEP 4 — Show the comment to add to the source file

Present the exact comment block the user should add (or add it directly):

```typescript
// GUID: PREFIX_NAME-NNN-v01
// [Intent] <what this code does and why it exists>
// [Inbound Trigger] <what calls/triggers this — event, route, user action>
// [Downstream Impact] <what this affects — Firestore writes, state changes, API calls>
```

Ask the user to confirm the intent/trigger/impact if not clear from the code, or draft it from context.

---

### STEP 4b — Golden Rules compliance check on the new code (GR#1, GR#7, GR#11)

Before registering, read the new code block and answer these questions. Flag any failures — do NOT silently skip.

**GR#1 — Error Trapping:** Does the new code have proper error handling?
- Any `async` function or Firestore call must have try/catch
- Caught errors must use `logError()` with a correlation ID (not just `console.error`)
- User-facing errors must be selectable/copyable — no raw `alert()` or plain text

**GR#7 — Registry-Sourced Errors:** Does the new code throw or return any errors?
- All errors displayed to users MUST use `ERRORS.KEY` from `error-registry.ts`
- Grep: `grep -n "message:" <filePath>` — any hardcoded message strings are a violation
- Grep: `grep -n "PX-[0-9]" <filePath> | grep -v "ERRORS\."` — raw PX codes outside the registry are a violation

**GR#11 — Security (mini-check, 3 questions):**
1. Does this code handle user input? → Must be validated/sanitised
2. Does this code touch auth, sessions, or tokens? → Must not weaken existing controls
3. Does this code expose a new API surface? → Must have auth guard checked

If any GR check fails: **STOP and flag to the user before registering the GUID.** A well-documented bad pattern is worse than an undocumented one.

If all pass: note `GR#1 ✅  GR#7 ✅  GR#11 ✅` in the final confirmation.

---

### STEP 5 — Create the code.json entry

Build the new GUID entry object. Determine:

- `guid`: `PREFIX_NAME-NNN`
- `version`: as determined in Step 3
- `logic_category`: one of `ORCHESTRATION | VALIDATION | TRANSFORMATION | SECURITY | DATA_ACCESS | UI | CONFIGURATION | ERROR_HANDLING`
- `description`: one sentence — what it does and why
- `location.filePath`: relative path from project root
- `location.functionName`: the exported function/class/const name (or null for anonymous blocks)
- `callChain.calls`: GUIDs this code directly calls (check the imports and function calls in the code)
- `callChain.calledBy`: GUIDs that call this code (check who uses this — grep if needed)
- `dependencies`: external GUID dependencies (Firebase hooks, context providers, etc.)

**GR#3 — Check for duplicate GUID before inserting (Single Source of Truth):**
```bash
node -e "const d=require('./code.json'); console.log(d.guids.find(g=>g.guid==='PREFIX_NAME-NNN') ? 'DUPLICATE ❌ already exists' : 'Unique ✅');"
```
If the GUID already exists, bump the version number and update the existing entry rather than adding a second one.

Then add to code.json using Node (use PowerShell-compatible node invocation):

```javascript
const fs = require('fs');
const data = JSON.parse(fs.readFileSync('code.json', 'utf8'));

const newEntry = {
  guid: "PREFIX_NAME-NNN",
  version: 1,
  logic_category: "ORCHESTRATION",
  description: "...",
  dependencies: [],
  location: { filePath: "...", functionName: "..." },
  callChain: { calledBy: [], calls: [] },
  created: new Date().toISOString(),
  lastUpdated: new Date().toISOString()
};

// Verify no duplicate
if (data.guids.find(g => g.guid === newEntry.guid)) {
  console.error('DUPLICATE GUID — update existing entry instead');
  process.exit(1);
}
data.guids.push(newEntry);
data.total_guids = data.guids.length;
fs.writeFileSync('code.json', JSON.stringify(data, null, 2));
console.log('Registered:', newEntry.guid, '— total:', data.total_guids);
```

---

### STEP 6 — Verify

```bash
node -e "require('./code.json'); console.log('code.json: valid JSON')"
node -e "const d=require('./code.json'); const e=d.guids.find(g=>g.guid==='PREFIX_NAME-NNN'); console.log('Registered:', JSON.stringify(e, null, 2));"
```

---

### STEP 7 — Confirm

```
bill> ✅ GUID registered: PREFIX_NAME-NNN v1
     File: app/src/...
     Function: functionName
     code.json total: N GUIDs
     GR#1 ✅  GR#3 ✅  GR#6 ✅  GR#7 ✅  GR#11 ✅
```

If any Golden Rule check failed, replace the relevant `✅` with `❌ [reason]` and do NOT proceed until the issue is resolved.

If multiple blocks are being registered in the same session, repeat Steps 1–6 for each.
