---
description: Destructive Firestore operation protocol — always query first, explain downstream effects, confirm twice before deleting anything
allowed-tools: Bash(node:*)
---

## Context

- Current date/time: !`date`
- Project: studio-6033436327-281b1
- Service account: E:\GoogleDrive\Papers\03-PrixSix\03.Current\service-account.json

## Your task

A destructive Firestore operation has been requested. This skill enforces the mandatory safety protocol. **Do not skip any step, even if the user has already said "yes go ahead".**

The lesson that created this protocol (2026-02-25): race_results and 100 test users were deleted without asking about real players' scores. Standings still showed a full season because scores are computed and stored separately. Scope was assumed incorrectly.

---

### STEP 1 — Query first, show counts

Before touching anything, query Firestore and report:

- Exact document count in every collection that will be touched
- Sample of 3-5 documents (IDs and key fields only — no PII) so the user can confirm these are the right docs
- Any subcollections that exist under documents in the target collection

Use the inline Node.js pattern:
```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
// query the relevant collection(s) here
```

---

### STEP 2 — Map the downstream effects

Identify and explain every downstream effect of the deletion:

- What **other collections** derive data from this one? (e.g. deleting `race_results` does NOT delete `scores` — they are computed separately)
- What **UI behaviour** will change immediately after deletion?
- What **compute/scoring processes** might produce wrong results if run after deletion?
- Are there any **subcollections** that will be orphaned?

Present this as a clear list the user can read and understand.

---

### STEP 3 — Ask for explicit confirmation

Present a summary:

```
⚠️  DESTRUCTIVE OPERATION SUMMARY

Collections to be deleted: [list]
Document count: [N] documents
Downstream effects: [list from step 2]

This CANNOT be undone.

Question 1: Shall I proceed with deleting the above?
Question 2: Do you also want to delete the derived/computed data listed above? [list it]
```

Wait for explicit answers to BOTH questions before proceeding.

---

### STEP 4 — Execute and confirm

Only after receiving explicit confirmation for both questions:

1. Perform the deletion in batches of ≤ 500 documents (Firestore batch limit)
2. Report count of documents deleted from each collection
3. If derived data deletion was also confirmed, handle that separately and report

Final confirmation:
```
bill> ✅ Deletion complete
     [Collection]: N docs deleted
     [Derived collection]: N docs deleted (if applicable)
     Downstream effect: [reminder of what changed]
```

---

## Deleting user accounts — additional protocol (added 2026-07-28)

Account deletion is the highest-stakes case, because the Firebase Auth record goes with it and a real person loses their sign-in. Everything above applies, plus:

**1. Back up before deleting, always.** Write every user document and auth record to a JSON file outside the repo — `E:/tmp/prix6-purged-accounts-<date>.json` — so the decision is reversible. Never commit these; they contain personal data.

**2. Guard the script, not just the conversation.** Have the deletion script itself refuse to proceed on any account that fails a precondition, so a mistyped uid cannot run unchecked. The gates that earned their place:
- team name must match the one you expect for that uid
- account must have **zero predictions** (if it has any, it is not dormant)
- `isAdmin !== true` unless admin deletion was explicitly authorised
- **admin count must not reach zero** — count remaining admins before deleting one. This is a lockout you cannot undo from inside the app.

**3. Surface admin status before asking.** In the 2026-07-28 purge, one of four "never participated" accounts turned out to be an admin (`adrian.hilder@gmail.com`). Aaron initially chose to keep it, then authorised deletion separately once he knew. Never let an admin deletion happen as a side effect of a bulk instruction.

**4. Clean up what the app's own route misses.** `/api/admin/delete-user` handles Auth, the users doc, presence and `leagues.memberUserIds` — but **not the `team_names` sentinel**. Left behind, the team name stays permanently reserved and nobody can reuse it. Delete `team_names/<teamName lowercased>` too.

**5. Write an `audit_logs` entry per account** recording the uid, team name, reason, who authorised it, whether it was an admin, and the backup path.

**6. Verify afterwards — do not trust the script's own output.** Re-query: user doc gone, auth record gone (`getUser` throws), name sentinel released, league membership updated, and the retained accounts still intact. See `/verify-recovery` for why the code running is not the same as the database committing.
