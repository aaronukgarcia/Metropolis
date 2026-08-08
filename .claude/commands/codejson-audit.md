---
description: Audit code.json against the codebase — find missing files, broken refs, undocumented GUIDs, and coverage gaps
allowed-tools: Bash(node:*), Bash(grep:*)
---

## Context

- Audit script exists: !`test -f scripts/audit-code-json.js && echo "YES" || echo "MISSING — cannot run"`
- Current code.json total_guids: !`node -e "try{const d=require('./code.json');console.log(d.total_guids+' GUIDs, variables: '+(d.variables?d.variables.length:'NOT PRESENT'))}catch(e){console.log('ERROR: '+e.message)}" 2>/dev/null`
- Last audit-report.json age: !`node -e "try{const s=require('fs').statSync('audit-report.json');const age=Math.round((Date.now()-s.mtimeMs)/60000);console.log(age+' minutes old ('+new Date(s.mtimeMs).toLocaleTimeString()+')');} catch{console.log('NOT FOUND — will run fresh');}" 2>/dev/null`

## Your task

Run a full structural audit of `code.json` against the actual codebase and triage the findings.

---

### STEP 1 — Run the audit

**GR#9 — Shell preference: PowerShell first on Windows.**
```powershell
node scripts/audit-code-json.js --pretty 2>$null | Out-File audit-report.json -Encoding utf8
```
Fallback (bash/WSL):
```bash
node scripts/audit-code-json.js --pretty 2>/dev/null > audit-report.json
```

If the script is missing, stop and tell the user to check `scripts/audit-code-json.js`.

---

### STEP 2 — Parse and display the summary

Read `audit-report.json` and present the findings as a triage dashboard:

```
╔══════════════════════════════════════════════════╗
║           code.json Audit — [timestamp]          ║
╠══════════════════════════════════════════════════╣
║  Registry: N GUIDs across N source files         ║
╠══════════════╦═══════════╦══════════════════════╣
║  Finding     ║  Count    ║  Priority             ║
╠══════════════╬═══════════╬══════════════════════╣
║  missing_files      N    ║  P1 — fix immediately ║
║  broken_refs        N    ║  P1 — fix immediately ║
║  wrong_file_path    N    ║  P2 — fix this session║
║  guid_not_in_file   N    ║  P2 — add comments    ║
║  fn_name_mismatch   N    ║  P3 — fix when noticed║
║  undocumented       N    ║  P3 — batch catch-up  ║
║  case_flags         N    ║  INFO — review only   ║
╚══════════════╩═══════════╩══════════════════════╝
```

---

### STEP 3 — Triage P1 findings (if any)

If `missing_files > 0`:
- List each missing file path
- For each: does the file still exist under a different name? (suggest the fix)
- Instruction: remove the stale GUID entry or update the filePath

If `broken_refs > 0`:
- List the top 10 broken references grouped by the dangling GUID name
- Instruction: either restore the deleted GUID or sweep callChain arrays to remove it
- Quick fix command: `node -e "const fs=require('fs'); const d=JSON.parse(fs.readFileSync('code.json','utf8')); const all=new Set(d.guids.map(g=>g.guid)); let n=0; for(const g of d.guids){if(g.callChain){g.callChain.calls=(g.callChain.calls||[]).filter(r=>{if(!all.has(r)){n++;return false;}return true;}); g.callChain.calledBy=(g.callChain.calledBy||[]).filter(r=>all.has(r));} g.dependencies=(g.dependencies||[]).filter(r=>all.has(r));} fs.writeFileSync('code.json',JSON.stringify(d,null,2)); console.log('Removed '+n+' broken refs');"`

---

### STEP 4 — Top files needing attention

Show two tables from the audit findings:

**Files with undocumented GUIDs (not in code.json):**
Show the top 10 files sorted by count, with counts. These are candidates for `/sync-codejson`.

**Files with missing GUID comments (in code.json, not in source):**
Show all files with `GUID_NOT_IN_FILE` findings. These need `// GUID: NAME-vNN` comments added.

---

### STEP 4b — GR#3 Single Source of Truth: check for duplicate GUIDs

```bash
node -e "const d=require('./code.json'); const seen=new Set(); const dupes=[]; for(const g of d.guids){if(seen.has(g.guid))dupes.push(g.guid); seen.add(g.guid);} console.log(dupes.length===0?'No duplicate GUIDs ✅':'DUPLICATES ❌: '+dupes.join(', '));"
```

If duplicates exist, flag them as P1 — the registry is not a single source of truth until resolved.

---

### STEP 5 — Variables section health

Check if `variables` key exists in code.json and report:
- Total variables: N (N constants, N env-vars, N collections)
- If missing: run `node scripts/add-variables-to-codejson.js` to populate it

---

### STEP 6 — Verdict and recommended next action

Based on findings, give ONE clear recommendation:

- If P1 findings exist → "Run fixes before any commit. P1 errors undermine the registry."
- If 0 P1, P2 > 10 → "Registry is stable. Use `/sync-codejson` to catch up on undocumented GUIDs."
- If 0 P1, P2 ≤ 10 → "Registry is healthy. Fix the N P2 items manually — quick wins."
- If all 0 → "Registry is clean. Nothing to do."

Confirm:
```
bill> ✅ code.json audit complete — [ISO timestamp]
     GR#3 ✅ (or ❌ N duplicates)
     P1: N missing_files, N broken_refs
     P2: N wrong_file_path, N guid_not_in_file
     P3: N undocumented, N fn_mismatch
     Variables: N (N constants, N env-vars, N collections)
     Verdict: [one line]
```
