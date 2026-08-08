---
description: Project health check — version sync, BOW status, rules deployment, branch state, memory freshness
allowed-tools: Bash(node:*), Bash(git:*), Bash(grep:*), Bash(npx firebase-tools:*)
---

## Context

- package.json version: !`node -e "try{console.log(require('./app/package.json').version)}catch(e){console.log('NOT FOUND')}" 2>/dev/null`
- version.ts value: !`grep -o "APP_VERSION = \"[^\"]*\"" app/src/lib/version.ts 2>/dev/null || echo "NOT FOUND"`
- Current branch: !`git branch --show-current`
- Git status: !`git status --short`
- Last 3 commits: !`git log --oneline -3`
- firestore.rules last commit: !`git log --oneline -1 -- app/src/firestore.rules`

## Your task

Run a full project health check. Check every item below and produce a single clean status report. This is a read-only diagnostic — do not fix anything unless asked.

---

### CHECK 1 — Version sync (Golden Rule #2)

Compare the two version values in Context above. They must be identical strings.

- ✅ Match: report both values
- ❌ Mismatch: flag — run `/bump` to fix

---

### CHECK 2 — Uncommitted changes

From git status in Context:
- Any staged changes? (ready to commit)
- Any unstaged changes to tracked files? (may be accidental)
- Any untracked files that look like they should be committed (scripts/, docs/, src/)?

Flag anything that looks like in-progress work that shouldn't be left hanging.

---

### CHECK 3 — BOW open items

Pull open BOW items from the metro MariaDB:

```bash
node claude-bow.js list
```

Report P0/P1 items by name (including any ⛓ blocked-on-dependency markers); give count only for P2/P3. Flag any item stuck `in_progress` or `blocked` with no recent comment/git ref — that is silent-stall territory.

---

### CHECK 4 — Firestore rules deployment status

Check when `firestore.rules` was last modified in git vs what's in the file now:

```bash
git diff HEAD -- app/src/firestore.rules | wc -l
```

If output > 0: rules have local uncommitted changes — flag as ⚠️ undeployed.
If output = 0: rules in git match working copy — check when last deployed.

Also check if rules were recently changed but not deployed:
```bash
git log --oneline -5 -- app/src/firestore.rules
```

Report: when the rules were last committed, and whether there are uncommitted local changes.

**Note:** We cannot programmatically check when Firebase last received the rules — this is a known gap. Flag if the most recent rules commit is after the most recent App Hosting deploy in git log.

---

### CHECK 5 — Branch hygiene

- Is this `main`? (production)
- Are there any local branches that look stale (feature branches not merged)?
- Is `main` ahead of or behind remote?

```bash
git branch -v
git log --oneline origin/main..HEAD
```

---

### CHECK 6 — Pending one-time setup items

Check for known pending setup tasks:

```bash
# CRON_SECRET — has it been set?
npx firebase-tools apphosting:secrets:describe CRON_SECRET --project studio-6033436327-281b1 2>&1 | head -3
```

Report if CRON_SECRET is configured (Hot News Feed cron won't fire without it).

---

### CHECK 7 — Secret IAM bindings (prevent silent build failures)

This is the check that would have caught the 2026-03-03 build failure. Extract all secrets from apphosting.yaml and verify each has IAM access granted to the App Hosting SA.

```bash
grep "secret:" app/apphosting.yaml | grep -v "#" | awk '{print $2}' | sort -u
```

For each secret returned, run:
```bash
npx firebase-tools apphosting:secrets:describe SECRET_NAME --project studio-6033436327-281b1 2>&1 | head -5
```

Classify each as ✅ (bound) / ⚠️ (exists, no access) / ❌ (missing entirely).

If ANY secret is ⚠️ or ❌ — flag as CRITICAL. Every App Hosting build will fail until fixed. Run `/iam-check` for full diagnosis and fix.

---

### CHECK 8 — error_logs count

A high error_log count is a silent signal that something is wrong or that noise is accumulating.

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
db.collection('error_logs').orderBy('timestamp', 'desc').limit(5).get().then(snap => {
  console.log('error_logs total (approx from recent):', snap.size);
  snap.forEach(d => {
    const data = d.data();
    console.log(' ', data.errorCode, '@', data.context?.route, '|', (data.error||'').slice(0,60));
  });
  process.exit(0);
}).catch(e => { console.error(e); process.exit(1); });
"
```

Classify:
- ✅ 0 docs — clean
- ⚠️ 1–10 docs — worth a `/triage-errors` to check for real bugs
- ❌ 11+ docs — run `/triage-errors` now; may indicate a noise storm or real systematic error

---

### CHECK 9 — Tailwind v4 CSS variable syntax

After the v3→v4 migration, CSS custom property references inside Tailwind arbitrary values must use parenthesis syntax `(--var)` not bracket syntax `[--var]`. Bracket syntax silently produces invalid CSS (e.g. `width: --sidebar-width` instead of `width: var(--sidebar-width)`).

Scan for any remaining v3-style patterns:

```bash
grep -rn "\[--[a-zA-Z-]*\]" app/src --include="*.tsx" --include="*.ts" --include="*.css"
grep -rn "theme(spacing\." app/src --include="*.tsx" --include="*.ts"
```

- ✅ No matches: clean
- ❌ Matches found: those classes will silently produce invalid CSS — fix by changing `[--var]` → `(--var)` and `theme(spacing.X)` → the resolved px/rem value

Also confirm `@tailwind` directives are gone (v3 only):
```bash
grep -rn "@tailwind" app/src --include="*.css"
```

---

### CHECK 10 — Standings calculation health (manual reminder)

The cumulative standings compute (used by /standings, the results email, and the admin health monitor) has a runtime RAG probe at `/api/admin/health/standings` (admin auth required). It catches the all-zeros bug pattern that broke the results email silently for ~6 weeks before being reported.

This check cannot be automated from a script (admin token required), so flag it as a manual reminder in the report.

Recommend: open admin → Health tab and confirm the **Standings Calculation** card is green. If amber, expand the Standings Diagnostic panel below the cards for the warnings list and top-5 sample.

Report: `STANDINGS PROBE: 📋 manual check — open admin → Health → Standings Calculation`

---

### CHECK 11 — Function-written status field freshness

Scheduled Cloud Functions write timestamps to `backup_status/latest`. If a function silently OOMs or otherwise dies before its status-write call, the timestamp goes stale — the dashboard reads "Last X: 6 weeks ago" but the function appears ACTIVE in `gcloud functions list`. This check catches the silent-OOM pattern that hid the runRecoveryTest crash for 6+ weeks before v3.1.5.

Pull `backup_status/latest` and compare each `lastXTimestamp` against its expected schedule + grace period:

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

(async () => {
  const snap = await db.collection('backup_status').doc('latest').get();
  if (!snap.exists) { console.log('STATUS DOC: ❌ MISSING'); return; }
  const d = snap.data();
  const now = Date.now();
  const checks = [
    { field: 'lastBackupTimestamp',          name: 'dailyBackup',           maxAgeH: 25 },  // runs daily 02:00 UTC
    { field: 'lastSmokeTestTimestamp',       name: 'runRecoveryTest',       maxAgeH: 8 * 24 },  // runs Sundays 04:00 UTC
    { field: 'lastRetentionRunTimestamp',    name: 'applyBackupRetention',  maxAgeH: 36 },  // runs daily 03:30 UTC
    { field: 'lastOffsiteMirrorTimestamp',   name: 'offsiteBackupMirror',   maxAgeH: 26 },  // runs daily 03:00 UTC — encrypted Azure mirror (FEAT-BACKUP-OFFSITE-001, v3.25.0)
    { field: 'lastRecoveryCleanupTimestamp', name: 'cleanupRecoveryProject', maxAgeH: 8 * 24 },  // runs Sundays 05:00 UTC (split from runRecoveryTest, v3.13.0 — fb-functions 7.3.0 540s cap)
  ];
  for (const c of checks) {
    const ts = d[c.field]?._seconds ?? d[c.field]?.seconds;
    if (!ts) { console.log(c.name.padEnd(25) + ' ⚠️  no timestamp — function may not be deployed'); continue; }
    const ageH = (now - ts * 1000) / 3_600_000;
    const status = ageH < c.maxAgeH ? '✅' : '❌';
    console.log(c.name.padEnd(25) + ' ' + status + ' last run ' + ageH.toFixed(1) + 'h ago (max ' + c.maxAgeH + 'h)');
  }
  process.exit(0);
})();
```

**Billceleration heartbeat (added v3.7.0):** the `/api/cron/billceleration` route writes `admin_configuration/billcelerationStatus` on EVERY tick — including no-op states (`idle-offseason`, `idle-waiting`, `disabled`) — so a tight 2h bound works year-round. The `state` field explains WHY the bot was idle; the timestamp proves the tick chain (billcelerationTick → route) is alive.

```javascript
(async () => {
  const snap = await db.collection('admin_configuration').doc('billcelerationStatus').get();
  if (!snap.exists) { console.log('billcelerationTick'.padEnd(25) + ' ⚠️  no status doc — function may not be deployed'); return; }
  const d = snap.data();
  const ageH = (Date.now() - new Date(d.lastRunAt).getTime()) / 3_600_000;
  const status = ageH < 2 ? '✅' : '❌';   // ticks every 15 min; 2h = 8 missed ticks
  console.log('billcelerationTick'.padEnd(25) + ' ' + status + ' last run ' + ageH.toFixed(1) + 'h ago (max 2h) — state: ' + d.state + (d.state === 'error' ? ' ⚠️ ' + d.detail : ''));
})();
```

If any function is ❌ stale, run the silent-OOM diagnostic:

```bash
"C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" functions logs read <name> --gen2 --project=studio-6033436327-281b1 --region=europe-west2 --limit=20
```

Look for `Memory limit of N MiB exceeded with M MiB used`. If found, bump `memory:` in `functions/index.js` and `firebase deploy --only functions:<name>`.

Report: `FN STATUS: ✅ all 4 timestamps fresh` or `FN STATUS: ❌ <name> stale Nh — likely silent OOM`

**Health-failure → error_logs cross-check (Aaron's rule, 2026-07-26 / GR#17 amendment):** every
health/monitoring FAILURE must ALSO write a registry error to `error_logs` — the status doc alone
is not enough, because error_logs is the single place the admin looks (BUG-SMOKE-001 stayed
invisible for 4 months precisely because failures only touched `backup_status`). So for every
`FAILED` status or ❌ stale timestamp found above, query `error_logs` for a matching entry
(correlationId or `[PX-70xx]` code within the same window). A failure WITHOUT a matching
error_logs entry is a DOUBLE fault: report the function as non-compliant and wire it to the
`writeErrorLog` helper in `functions/index.js` (BACKUP_FUNCTIONS-032). History of this exact
trap: 2026-03-22→07-26, `runRecoveryTest` OOM'd in cleanup (limit(500).get() loaded full
replay_chunks docs) BEFORE any status write; fixed v3.12.2 with refs-only `recursiveDelete`,
status-before-cleanup ordering, and 2GiB on both smoke functions. Run this check WEEKLY (Monday)
— the 8-day smoke bound only protects anyone if someone actually looks.

---

### CHECK 12 — Cloud Functions deploy status (added v3.1.7)

Cloud Functions are deployed manually, not by App Hosting on push. Each function commit has a "REQUIRES MANUAL DEPLOY" note in its message body — but it's easy to forget the actual `firebase deploy` step. Functions can sit in code unreleased for days/weeks (today's session had 4 pending across two commits).

Identify functions in source:

```bash
grep -E "^exports\.[a-zA-Z0-9_]+ = on(Schedule|Call|Request)" functions/index.js \
  | sed 's/exports\.\([a-zA-Z0-9_]*\) =.*/\1/' | sort -u
```

Cross-reference with deployed functions:

```bash
"C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" \
  functions list --v2 --project=studio-6033436327-281b1 --format="value(name,state)" 2>/dev/null \
  | grep ACTIVE | awk '{print $1}' | sort -u
```

Diff the two lists:
- Functions in source but NOT deployed: ⚠ pending manual deploy
- Functions deployed but NOT in source: ⚠ stale deploy (someone removed from code without un-deploying)

For each "in source", check whether the source version matches what's deployed by examining the most recent commit touching `functions/index.js`:

```bash
git log -3 --pretty=format:"%h %s" -- functions/index.js
```

If a recent commit message says `REQUIRES MANUAL DEPLOY: firebase deploy --only functions:<list>` and the listed functions are in `gcloud functions list` ACTIVE, check whether their last invocation log mentions the new code (rough proxy: did they run since the commit timestamp?).

If any pending deploy is detected, surface the exact deploy command so the user can paste it:

```
firebase deploy --only functions:<name1>,functions:<name2>
```

Report: `FN DEPLOY: ✅ all source functions deployed` or `FN DEPLOY: ⚠ N pending — run firebase deploy --only functions:<list>`

---

### Final health report

Present as a clean dashboard:

```
bill> 🏥 Prix Six Health Check — vX.Y.Z — [date]
     ─────────────────────────────────────────
     Version sync:      ✅ / ❌
     Uncommitted work:  ✅ clean / ⚠️ [what's pending]
     BOW P0/P1:         ✅ none / ❌ N items — [list]
     BOW total open:    N items
     Firestore rules:   ✅ committed, no local changes / ⚠️ uncommitted changes
     Branch:            main ✅ / ⚠️ [branch name]
     CRON_SECRET:       ✅ configured / ⚠️ not set — Hot News cron inactive
     Standings probe:   📋 open admin → Health → Standings Calculation
     Fn status fields:  ✅ all 3 fresh / ❌ <name> stale Nh — silent OOM
     Fn deploy state:   ✅ all source fns deployed / ⚠️ N pending deploy
     ─────────────────────────────────────────
     Overall: ✅ HEALTHY / ⚠️ ATTENTION NEEDED / ❌ ISSUES FOUND
     Next recommended action: [one line]
```
