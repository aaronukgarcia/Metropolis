---
description: Cloud Functions deploy + last-fired status — list every exported function, cross-reference with gcloud, surface pending deploys and silent OOMs
allowed-tools: Bash(grep:*), Bash(node:*), PowerShell(*)
---

## Context

- Source functions: !`grep -E "^exports\\.[a-zA-Z0-9_]+ = on(Schedule|Call|Request)" functions/index.js | sed 's/exports\\.\\([a-zA-Z0-9_]*\\) =.*/\\1/' | sort -u`
- Most recent commit touching functions: !`git log -1 --pretty=format:"%h %ai %s" -- functions/index.js`

## Your task

Map every Cloud Function in source against (a) what's deployed in production, (b) when each last fired, (c) whether the deploy is current with the source code. Output a single table with action items.

---

### STEP 1 — Source vs deployed diff

Source functions are in the Context above. Now query gcloud for deployed:

```powershell
& "C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" `
  functions list --v2 --project=studio-6033436327-281b1 `
  --format="value(name,state)" 2>&1 | Where-Object { $_ -match "ACTIVE" }
```

Diff:
- **In source AND deployed** → ✅ check freshness in Step 2
- **In source NOT deployed** → ⚠ pending — surface the deploy command
- **Deployed NOT in source** → ⚠ stale (someone removed from code without un-deploying); manual cleanup may be needed

---

### STEP 2 — Last-fired freshness per function

For functions that write to `backup_status/latest`, use the same query as `/health-check` CHECK 11:

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

(async () => {
  const snap = await db.collection('backup_status').doc('latest').get();
  if (!snap.exists) { console.log('STATUS DOC MISSING'); return; }
  const d = snap.data();
  const now = Date.now();
  const checks = [
    { field: 'lastBackupTimestamp',         name: 'dailyBackup',          maxAgeH: 25 },
    { field: 'lastSmokeTestTimestamp',      name: 'runRecoveryTest',      maxAgeH: 192 },
    { field: 'lastRetentionRunTimestamp',   name: 'applyBackupRetention', maxAgeH: 36 },
  ];
  for (const c of checks) {
    const ts = d[c.field]?._seconds ?? d[c.field]?.seconds;
    if (!ts) { console.log(`${c.name.padEnd(25)} ⚠️  no timestamp — likely undeployed`); continue; }
    const ageH = (now - ts * 1000) / 3_600_000;
    const status = ageH < c.maxAgeH ? '✅' : '❌';
    console.log(`${c.name.padEnd(25)} ${status} last ${ageH.toFixed(1)}h ago (max ${c.maxAgeH}h)`);
  }
  process.exit(0);
})();
```

Write to a temp file and run via `node`.

For functions WITHOUT a status field (e.g. `manualBackup`, `expireStaleLogons`, `processEmailQueue`, etc.), check Cloud Functions logs:

```powershell
& "C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" `
  functions logs read <fn-name> --gen2 --region=europe-west2 `
  --project=studio-6033436327-281b1 --limit=1 `
  --format="value(timestamp)"
```

Compare timestamp to most-recent commit touching `functions/index.js`. If logs predate the commit, the deploy is stale.

For functions with `manualBackup`-style on-demand triggers, "last fired" being long ago is acceptable; flag only as informational.

---

### STEP 3 — Compose deploy command

For every function flagged "pending deploy" or "stale deploy", build the bundled command:

```
firebase deploy --only functions:<name1>,functions:<name2>,...
```

Audit recent commits for prior `REQUIRES MANUAL DEPLOY` notes that haven't been confirmed:

```bash
git log --oneline -10 -- functions/index.js
```

If any prior commit's listed deploy doesn't have evidence of having run (logs predate it, status field stale), include those function names in the command.

---

### STEP 4 — Final report

Format as a table:

```
bill> Cloud Functions status — YYYY-MM-DD HH:MM UTC
     ─────────────────────────────────────────────────────────────────
     Function                  | Deployed | Last fired | Source vN
     ─────────────────────────────────────────────────────────────────
     dailyBackup               | ✅       | 14h ago    | matches
     manualBackup              | ✅       | 12d ago    | matches (on-demand)
     runRecoveryTest           | ✅       | 26d ago    | ⚠ source bumped to 1GiB v3.1.5, deploy still 512MiB
     applyBackupRetention      | ⚠ no    | never      | committed v3.1.2, never deployed
     ...
     ─────────────────────────────────────────────────────────────────
     Action: firebase deploy --only functions:runRecoveryTest,functions:applyBackupRetention
```

Honest about uncertainty — if logs are absent for a function, say "no recent invocations" and surface the source-vs-deploy version as best-guess from commit messages.
