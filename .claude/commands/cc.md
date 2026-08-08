---
description: Analyse a Prix Six consistency check report — pull from Firestore, triage issues, apply fix playbooks
allowed-tools: Bash(node:*)
---

## Your task

Analyse the consistency check report with correlation ID: **$ARGUMENTS**

If no correlation ID was provided in $ARGUMENTS, ask the user to paste one before proceeding.

---

## STEP 1 — Pull the report from Firestore

The report lives in the `consistency_reports` collection (NOT `error_logs`).

Run this to retrieve it:

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
db.collection('consistency_reports').where('correlationId', '==', 'CORRELATION_ID').get().then(snap => {
  if (snap.empty) { console.log('NOT FOUND — check the ID'); process.exit(0); }
  snap.forEach(doc => {
    const d = doc.data();
    const byCategory = {};
    const bySeverity = { error: [], warning: [], info: [] };
    (d.issues || []).forEach(i => {
      if (!byCategory[i.category]) byCategory[i.category] = [];
      byCategory[i.category].push(i);
      (bySeverity[i.severity] || (bySeverity.info)).push(i);
    });
    console.log('=== SUMMARY ===');
    console.log(JSON.stringify(d.summary, null, 2));
    console.log('Total issues:', d.issues.length);
    console.log('By severity:', JSON.stringify({ errors: bySeverity.error.length, warnings: bySeverity.warning.length, info: bySeverity.info.length }));
    console.log('By category:', JSON.stringify(Object.fromEntries(Object.entries(byCategory).map(([k,v]) => [k, v.length]))));
    console.log('\\n=== ERRORS ===');
    bySeverity.error.forEach(i => console.log('[ERROR]', i.entity, '/', i.field, '\\n ', i.message, '\\n  Details:', JSON.stringify(i.details)));
    console.log('\\n=== WARNINGS ===');
    bySeverity.warning.forEach(i => console.log('[WARN]', i.entity, '/', i.field, '\\n ', i.message, '\\n  Details:', JSON.stringify(i.details)));
    console.log('\\n=== INFO (grouped) ===');
    Object.entries(byCategory).forEach(([cat, items]) => {
      const infoItems = items.filter(i => i.severity === 'info');
      if (infoItems.length) console.log(cat + ': ' + infoItems.length + ' info items');
    });
  });
  process.exit(0);
}).catch(e => { console.error(e); process.exit(1); });
"
```

Replace `CORRELATION_ID` with the actual ID from $ARGUMENTS.

---

## STEP 2 — Present the structured report

Format the output as:

```
## Consistency Check — [correlationId]

### Summary
| Checks | Passed | Warnings | Errors |
|--------|--------|----------|--------|
| N      | N      | N        | N      |

### 🔴 Errors ([N]) — must fix before season
...

### 🟡 Warnings ([N]) — investigate and resolve
...

### ℹ️ Info ([N]) — review for expected noise
[grouped by category with counts]
```

---

## STEP 3 — Triage using known patterns

Apply this knowledge to classify each issue:

### Known categories and what they mean

| Category | Typical severity | Meaning | Pre-season noise? |
|----------|-----------------|---------|-------------------|
| `team-coverage` | info | User has no predictions for a race | ✅ Yes — normal before Australian GP |
| `standings` | info | No completed race weekends | ✅ Yes — normal pre-season |
| `leagues` | warning | Ghost member IDs / invalid members | ❌ Needs cleanup — see playbook below |
| `scores` | error/warning | Score doc orphaned or mismatched | ❌ Needs investigation |
| `predictions` | warning | Prediction without a matching race | ❌ Check race ID format |
| `users` | warning/error | User doc missing required fields | ❌ Investigate |
| `race_results` | error | Race result references non-existent race | ❌ Data entry error |
| `trophies` | info mostly | Podium awards and the links they render | ✅ Info entries are expected — read below |

### The `trophies` category (added v3.20.0, FEAT-TROPHY-002)

Two checks run under this category: `checkTrophies` validates the awards and every link a trophy renders, and `checkTrophyAssets` renders all 66 trophy drawings to confirm each produces a complete SVG data URI with a real host-nation flag.

**Do not treat info entries here as problems.** The bulk of the output is joint-place reporting, and joint places are *correct*. Prix Six uses competition ranking, so equal points share a place and the next place is skipped — a three-way tie for first awards three golds and no silver or bronze. The check names the tied teams so an auditor can see it was deliberate. As of 2026-07-28 the live data produces 50 trophies and 11 joint-place info entries, including three-way ties for first at Barcelona, Silverstone and Spa. That is a healthy `pass`.

What genuinely matters here:

| Finding | Severity | What it means |
|---------|----------|---------------|
| "not competition-ranked: expected the next place to be N" | error | A tie failed to skip the following place — trophy counts now disagree with the standings tables |
| "link target ... matches no scheduled race" | error | A trophy's `/results?race=` deep link would land nowhere |
| "anchor ... produced by two different races" | error | A Results podium badge would scroll to the wrong trophy |
| "awarded ... with 0 points" | error | Zero-point sessions must award nothing |
| "No trophy artwork for circuit X" | warning | New circuit on the calendar with no `CIRCUITS` row in `lib/trophy-assets.ts` |
| "Scores exist for X but it matches no race in RaceSchedule" | warning | Orphaned score rows — no trophy or link can be built |
| "No trophies awarded for X — no team scored above zero" | info | Legitimate, but worth eyeballing |

### Pre-season noise rule
If ALL issues are:
- `team-coverage` info → "no predictions yet"
- `standings` info → "no completed race weekends"

Then the report is **clean** — no action needed. State this clearly.

---

## STEP 4 — Fix playbooks for actionable issues

### Playbook: League ghost members (`leagues` warning — "invalid member ID(s)")

This is caused by test users being added to a league then deleted, or by a `-secondary` ID being stored instead of a primary user ID.

**Fix procedure (always use `/danger` for the actual write):**

1. Get the league document and its `memberUserIds` array
2. Cross-check every ID against the `users` collection in batches of 30:

```javascript
// Cross-check pattern
const BATCH = 30;
const validIds = new Set();
for (let i = 0; i < memberIds.length; i += BATCH) {
  const batch = memberIds.slice(i, i + BATCH);
  const snap = await db.collection('users').where('__name__', 'in', batch).get();
  snap.docs.forEach(d => validIds.add(d.id));
}
const kept = memberIds.filter(id => validIds.has(id));
const removed = memberIds.filter(id => !validIds.has(id));
```

3. Show the user:
   - List of valid members with display names (verify these are real players)
   - Count of invalid IDs to remove
   - Any suspicious entries (e.g. IDs ending in `-secondary` — these are malformed)
4. Apply `/danger` protocol — confirm before writing
5. Write: `leagueRef.update({ memberUserIds: kept })`
6. Verify with a read-back

**Important:** The league doc ID for the main league is `global` (not the league name "Global League").

---

### Playbook: Orphaned scores (`scores` warning/error)

Scores are keyed as `{normalizedRaceId}_{userId}`. An orphaned score means the race_results or prediction it was computed from no longer exists.

1. Identify the race and user from the score document ID
2. Check if `race_results` has a doc for that race
3. Check if the user has a prediction for that race
4. If both are missing → the score is truly orphaned → safe to delete via `/danger`
5. If race_results exists but score is wrong → recalculate via `scripts/recalculate-scores.ts`

---

### Playbook: Prediction format mismatch (`predictions` warning)

Predictions exist in two formats (historical inconsistency):
- With `-GP` suffix: `Australian-Grand-Prix-GP`
- Without suffix: `Australian-Grand-Prix`

The scoring engine handles both via `normalizeRaceId()`. If the consistency checker flags a prediction as mismatched, check whether it's a genuine orphan or just the known dual-format situation.

Run: `grep -n "normalizeRaceId\|normalizeRaceIdForComparison" app/src/lib/scoring.ts` to verify the dual-format handling is in place.

---

### Playbook: User missing required fields (`users` warning/error)

Required fields vary by user type. Common missing fields:
- `teamName` — needed for standings display
- `primaryTeam` / `secondaryTeam` — needed for league membership checks
- `email` — needed for notifications

Don't auto-fix user records without explicit instruction. Present what's missing and ask.

---

## STEP 5 — Final summary output

Always close with:

```
bill> CC Report [correlationId] — Analysis Complete

  ✅ [N] checks passed
  🔴 [N] errors — [list or "none"]
  🟡 [N] warnings — [list or "none"]
  ℹ️  [N] info items — [pre-season noise / expected / review]

  Action required: [clear next steps or "none — report is clean"]
```
