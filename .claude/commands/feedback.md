---
description: Query user feedback and bug reports from Firestore — view open items, generate fix acknowledgements, mark resolved
allowed-tools: Bash(node:*)
---

## Your task

Query the Prix Six `feedback` Firestore collection and work through the steps below.

If an argument was passed (e.g. `/feedback BG-008` or `/feedback bug`), use it to filter results. Otherwise show all open/new items.

---

### STEP 1 — Pull feedback from Firestore

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

const snap = await db.collection('feedback')
  .orderBy('createdAt', 'desc')
  .limit(50)
  .get();

snap.forEach(doc => {
  const d = doc.data();
  const date = d.createdAt?.toDate?.()?.toISOString?.()?.slice(0,10) ?? '?';
  console.log(`[${d.referenceId || doc.id}] ${d.status?.toUpperCase()} | ${d.type} | ${date} | ${d.teamName}`);
  console.log(`  "${d.text}"`);
  console.log(`  email: ${d.userEmail || 'none'} | notify: ${d.notifyOnFix}`);
  console.log('');
});
```

Write to a temp file and run with `node`. Show results grouped by status: `new` first, then `reviewed`, then `resolved`.

---

### STEP 2 — Summarise open items

Present a clean table of all `new` and `reviewed` items:

```
| Ref   | Type    | Team                  | Date       | Summary                          |
|-------|---------|-----------------------|------------|----------------------------------|
| BG-008| bug     | British Racing Greenie| 2026-03-03 | Teams page not showing predictions|
```

Flag any items where `notifyOnFix: true` — these users are waiting for an email when resolved.

---

### STEP 3 — If a specific reference was requested

If the user passed a reference ID (e.g. `BG-008`) or is resolving a specific bug:

**A — Generate acknowledgement text** for use in a CHANGELOG entry or comms:

Include:
- Thank the reporter by team name
- Brief plain-English description of what was wrong
- What was fixed and in which version
- Tone: warm, clear, non-technical — this goes to real users

**B — Mark as resolved in Firestore** (ask user to confirm first):

```javascript
await db.collection('feedback').doc('DOCUMENT_ID').update({
  status: 'resolved',
  resolvedAt: admin.firestore.FieldValue.serverTimestamp(),
  resolvedInVersion: 'vX.Y.Z',
  resolutionNote: 'Fixed in vX.Y.Z — [one-line description]'
});
```

**C — If `notifyOnFix: true`** — remind the user that this reporter opted in to be notified.
Ask: "Do you want to send them an email confirming the fix?" If yes, use the Graph API email pattern (same as `email-restored-scores.js`) to send a brief note.

---

### STEP 4 — Report

```
bill> 📬 Feedback summary — [date]
     ─────────────────────────────────
     New:      N items
     Reviewed: N items
     Resolved: N items (lifetime)
     ─────────────────────────────────
     Notify-on-fix pending: N users
     Next action: [recommendation]
```
