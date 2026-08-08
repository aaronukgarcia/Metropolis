---
description: Verify race_results and scores collections — SSOT-001 compliance, Title-Case ID format, which races have results vs pending
allowed-tools: Bash(node:*)
---

## Context

SSOT-001 eliminated the `scores` collection — scores are computed at read time from
`race_results + predictions`. The `scores` collection must always be empty.

`race_results` doc IDs must always be Title-Case e.g. `"Australian-Grand-Prix-GP"` (from
`generateRaceId()`). Lowercase IDs are a bug — direct Firestore lookups will miss them.

Optional argument in $ARGUMENTS:
- A race name fragment (e.g. `australia`) — show detailed doc for that race
- `all` — list all race_results docs with their driver fields

---

## STEP 1 — Pull race_results and scores

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

async function run() {
  const [resultsSnap, scoresSnap] = await Promise.all([
    db.collection('race_results').orderBy('__name__').get(),
    db.collection('scores').limit(5).get(),
  ]);

  console.log('=== race_results ===');
  console.log('Count:', resultsSnap.size);
  resultsSnap.forEach(doc => {
    const d = doc.data();
    const id = doc.id;
    const isCorrectCase = /^[A-Z][a-z]/.test(id); // Title-Case check
    const flag = isCorrectCase ? '✅' : '❌ WRONG CASE';
    console.log(' ', flag, id, '|', d.driver1, d.driver2, d.driver3, d.driver4, d.driver5, d.driver6);
  });

  console.log('');
  console.log('=== scores (SSOT-001: must be empty) ===');
  console.log('Count:', scoresSnap.size, scoresSnap.size === 0 ? '✅ CLEAN' : '❌ NOT EMPTY — SSOT-001 VIOLATION');
  if (scoresSnap.size > 0) {
    scoresSnap.forEach(doc => console.log('  ', doc.id, JSON.stringify(doc.data()).slice(0, 80)));
  }
}
run().catch(console.error);
" 2>/dev/null
```

---

## STEP 2 — Cross-reference against schedule

After pulling the results, compare against the 2026 F1 season races:

Australian GP, Chinese GP, Japanese GP, Bahrain GP, Saudi Arabian GP, Miami GP,
Emilia Romagna GP, Monaco GP, Spanish GP, Canadian GP, Austrian GP, British GP,
Belgian GP, Hungarian GP, Dutch GP, Italian GP, Azerbaijan GP, Singapore GP,
United States GP, Mexico City GP, São Paulo GP, Las Vegas GP, Qatar GP, Abu Dhabi GP

For each race that has passed (based on today's date):
- ✅ Has a `race_results` doc
- ⚠️ Missing — results not yet entered

For each race that hasn't passed yet:
- ✅ No results doc (correct — pre-race)
- ⚠️ Has a results doc prematurely (may be from `_simulate_season.js` — will lock predictions)

---

## STEP 3 — Detailed view (if $ARGUMENTS contains a race name)

If a race name fragment was provided, pull that specific doc and show all fields:

```javascript
// Example: pull Australian GP
db.collection('race_results').doc('Australian-Grand-Prix-GP').get().then(doc => {
  if (!doc.exists) {
    console.log('NOT FOUND — check Title-Case format');
    return;
  }
  console.log(JSON.stringify(doc.data(), null, 2));
});
```

Note: always use the exact Title-Case ID. Never `.toLowerCase()` on the doc ID.

---

## STEP 4 — Final report

```
bill> Race Data Check — [date]

  race_results: N doc(s)
  ✅ Australian-Grand-Prix-GP | VER HAM LEC NOR PIA SAI
  [list all docs]

  ID format: ✅ all Title-Case / ❌ [list bad IDs]

  scores (SSOT-001): ✅ empty / ❌ N docs — VIOLATION

  Season coverage:
  ✅ [race] — results entered
  ⚠️ [race] — PAST, no results yet (pit lane may still show 'open' for this race)
  🔒 [race] — future, no results (correct)
```

### Common fixes

**Scores collection not empty:**
Results are now computed at read time — the `scores` docs are stale from before SSOT-001.
Delete them via the `/danger` skill then a batch-delete script.

**Wrong-case race ID in race_results:**
The doc was created with a lowercase ID — direct lookups from the app will miss it.
Use the `/danger` skill to: (1) read the bad doc, (2) write a new doc with correct Title-Case ID,
(3) delete the bad doc.

**Past race missing results:**
Enter via the admin panel Results tab. Until results are entered, the pit lane stays open
for that race and standings won't show that round.

**Future race has a result doc:**
Likely left over from `_simulate_season.js`. Delete it — it locks predictions for that race.
Use: `db.collection('race_results').doc('RACE-ID').delete()`
