---
description: Batch-register undocumented GUIDs from source files into code.json — the catch-up skill for the registry backlog
allowed-tools: Bash(node:*), Bash(grep:*), Read, Edit
---

## Context

- Undocumented GUID count (from last audit): !`node -e "try{const r=require('./audit-report.json');const u=r.findings.filter(f=>f.type==='UNDOCUMENTED_GUID');const byFile={};for(const x of u){byFile[x.filePath]=(byFile[x.filePath]||0)+1;}const sorted=Object.entries(byFile).sort((a,b)=>b[1]-a[1]);console.log('Total: '+u.length+' undocumented GUIDs across '+sorted.length+' files');console.log('Top 5:');sorted.slice(0,5).forEach(([f,n])=>console.log(' '+n+' '+f));}catch{console.log('No audit-report.json — run /codejson-audit first');}" 2>/dev/null`
- Current code.json total_guids: !`node -e "try{console.log(require('./code.json').total_guids+' GUIDs')}catch(e){console.log('ERROR')}" 2>/dev/null`

## Your task

Work through undocumented GUIDs from source files and register them in code.json. These are GUID-tagged blocks that exist in source but are absent from the registry.

If `audit-report.json` is more than 30 minutes old, run `/codejson-audit` first to refresh the findings.

---

### STEP 1 — Pick the target file

From the undocumented GUID list above, pick the file with the most undocumented GUIDs (or the file the user specifies).

Ask the user: "I'll start with `<file>` which has N undocumented GUIDs. Proceed, or do you want a different file?"

---

### STEP 2 — Read the file and extract all undocumented GUID blocks

Read the entire target file. For each `// GUID: PREFIX-NNN` line that is NOT already in code.json:

1. Extract the GUID name (strip the `-vNN` suffix)
2. Read the comment block immediately below the GUID line (lines starting with `//` up to the first non-comment line)
3. Read the function/const/class definition that follows the comment block
4. Identify: function name, what it does, what it calls

Build a draft entry for each undocumented GUID:

```json
{
  "guid": "PREFIX_NAME-NNN",
  "version": <version from -vNN suffix or 1 if not present>,
  "logic_category": "<inferred from code>",
  "description": "<extracted from // [Intent] comment or inferred>",
  "dependencies": [],
  "location": {
    "filePath": "<relative path>",
    "functionName": "<extracted function/const name or null>"
  },
  "callChain": {
    "calledBy": [],
    "calls": []
  },
  "created": "<today ISO date>",
  "lastUpdated": "<today ISO date>"
}
```

**Description priority:**
1. Use `// [Intent]` comment if present
2. Use the JSDoc/inline comment above the function
3. Infer from the function name and code — be specific, not generic

**logic_category mapping:**
- Auth, security, validation → `VALIDATION` or `SECURITY`
- Firestore reads → `DATA_ACCESS`
- Firestore writes, mutations → `DATA_ACCESS`
- React components, UI rendering → `UI`
- API route handlers → `ORCHESTRATION`
- Error handling, logging → `ERROR_HANDLING`
- Config, constants → `CONFIGURATION`
- Data transformation, mapping → `TRANSFORMATION`

---

### STEP 3 — Present entries for approval

Show each draft entry in a compact summary format before adding:

```
GUID: PREFIX-NNN  (v1)
  File: app/src/...
  Function: functionName
  Category: ORCHESTRATION
  Description: "What it does in one sentence"
  Add to code.json? [Y/skip/edit]
```

If the user says `edit`, update the description before adding.
If the user says `skip`, note it and move on.
If the user says `all` or `y`, add all remaining without asking.

---

### STEP 4 — Batch-add approved entries

For each approved entry, append to code.json's `guids` array. Do this in a single Node.js operation to avoid multiple file reads/writes:

```javascript
const fs = require('fs');
const data = JSON.parse(fs.readFileSync('code.json', 'utf8'));
const newEntries = [ /* approved entries array */ ];
data.guids.push(...newEntries);
data.total_guids = data.guids.length;
fs.writeFileSync('code.json', JSON.stringify(data, null, 2));
console.log('Added', newEntries.length, 'entries. New total:', data.total_guids);
```

Verify JSON is valid:
```bash
node -e "require('./code.json'); console.log('valid')"
```

---

### STEP 5 — Update audit-report.json

Re-run the audit for just the summary (not full scan).
**GR#9 — PowerShell first:**
```powershell
node scripts/audit-code-json.js --pretty 2>$null | Out-File audit-report.json -Encoding utf8
```
Fallback (bash):
```bash
node scripts/audit-code-json.js --pretty 2>/dev/null > audit-report.json
```

Report the new undocumented count vs. starting count.

---

### STEP 6 — Ask to continue

```
bill> ✅ Registered N GUIDs from <file>
     Remaining undocumented: N total across N files
     Next highest: <next file> (N GUIDs)
     Continue with that file? [Y/N]
```

Repeat from Step 1 if the user says yes.

---

### Notes

- **Don't invent callChain entries** — leave `calls` and `calledBy` as `[]` unless you can see a direct invocation in the code. Bad refs are worse than empty arrays.
- **Version from comment** — if the source says `// GUID: FOO-001-v03`, use `version: 3` not `1`
- **Batch efficiently** — read the whole file once, extract all blocks, then do one write to code.json
- **Skip `// GUID: ... REMOVED` blocks** — if the comment says REMOVED or the code below is deleted/commented out, don't register it
- **GR#3 SSOT** — before batch-inserting, verify none of the GUIDs already exist in code.json (the dupe guard in the Node script catches this, but check consciously)

---

### Golden Rule flags — note as you read (GR#1, GR#7)

While reading each file to extract GUID blocks, keep an eye open for violations. Do NOT silently pass over them — flag as a side-note before moving on.

**GR#1 flag** — if you see `catch(e) { console.error(e) }` or an empty catch with no `logError()` call, note:
```
⚠️  GR#1 concern in <GUID>: catch block does not call logError() — flag for fix
```

**GR#7 flag** — if you see a hardcoded error message string or raw `PX-NNNN` outside `ERRORS.`:
```
⚠️  GR#7 concern in <GUID>: hardcoded error string — should use ERRORS.KEY from error-registry.ts
```

These are not blockers for the sync itself — the code already exists and `/sync-codejson` is a documentation pass, not a code review. But the flags belong in the session so they can be fixed. Assign them to the book-of-work if the user wants.
