---
description: Inline Firestore admin — boilerplate Node.js pattern for querying or mutating Firestore from the command line
allowed-tools: Bash(node:*), Bash(powershell.exe:*)
---

## Context

- Working directory: !`pwd`
- Service account present: !`test -f service-account.json && echo "✅ service-account.json found" || echo "❌ NOT FOUND — cd to project root first"`
- Firebase Admin available: !`node -e "require('firebase-admin/app'); console.log('✅ firebase-admin available')" 2>/dev/null || echo "❌ firebase-admin not installed"`
- Current Firestore collections (if needed): run Step 1 below

## Your task

Execute an inline Firestore operation using the correct boilerplate for this project. Choose the right pattern below for the task.

**⚠️ If this is a destructive operation (delete, update, wipe), run `/danger` first.**

---

### PATTERN A — Query / Read (safe)

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

// --- YOUR QUERY HERE ---
db.collection('COLLECTION_NAME').get().then(snap => {
  console.log('Total docs:', snap.size);
  snap.docs.slice(0, 5).forEach(d => console.log(d.id, JSON.stringify(d.data()).substring(0, 200)));
  process.exit(0);
}).catch(e => { console.error(e); process.exit(1); });
"
```

---

### PATTERN B — Batch Delete (destructive — use /danger first)

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

db.collection('COLLECTION_NAME').get().then(snap => {
  if (snap.empty) { console.log('Nothing to delete'); process.exit(0); }
  const batch = db.batch();
  snap.docs.forEach(d => batch.delete(d.ref));
  return batch.commit().then(() => {
    console.log('Deleted', snap.size, 'docs from COLLECTION_NAME');
    process.exit(0);
  });
}).catch(e => { console.error(e); process.exit(1); });
"
```

---

### PATTERN C — Targeted Query (with filter)

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

db.collection('COLLECTION_NAME')
  .where('FIELD', '==', 'VALUE')
  .orderBy('timestamp', 'desc')
  .limit(20)
  .get().then(snap => {
    console.log('Found', snap.size, 'docs');
    snap.forEach(d => console.log(d.id, JSON.stringify(d.data(), null, 2)));
    process.exit(0);
  }).catch(e => { console.error(e); process.exit(1); });
"
```

---

### PATTERN D — List all collections

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
db.listCollections().then(cols => {
  console.log('Collections:', cols.map(c => c.id).join(', '));
  process.exit(0);
}).catch(e => { console.error(e); process.exit(1); });
"
```

---

### Execution notes

- **Working directory must be the project root** (`E:\GoogleDrive\Papers\03-PrixSix\03.Current`) so `path.resolve('service-account.json')` finds the file
- On Windows, wrap in `powershell.exe -Command` if the bash inline node doesn't work
- Batches are limited to 500 writes per commit — loop for larger collections
- TypeScript scripts: `npx ts-node --project app/tsconfig.scripts.json scripts/SCRIPT.ts`

Choose the appropriate pattern, fill in the collection name and logic, run it, and report results to the user.
