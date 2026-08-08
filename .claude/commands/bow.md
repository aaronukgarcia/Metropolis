---
description: Book of Work — view open items, mark done, or add new items to the book_of_work Firestore collection
allowed-tools: Bash(node:*), Bash(npx:*)
---

## Context

- Current version: !`node -e "try{console.log(require('./app/package.json').version)}catch(e){console.log('check package.json')}" 2>/dev/null`
- Today: !`node -e "console.log(new Date().toISOString().split('T')[0])"`

## Your task

Manage the `book_of_work` Firestore collection. The BOW is the single source of truth for open bugs, features, and chores.

**ARGUMENTS:** $ARGUMENTS — if provided, treat as a sub-command (see below). If empty, default to VIEW.

---

### Sub-commands

| Argument | Action |
|----------|--------|
| (empty) or `view` | Show all open/monitoring/tbd items grouped by priority |
| `done <ID>` | Mark a single BOW item as done |
| `add` | Create a new BOW item (will prompt for details) |
| `all` | Show ALL items including done/resolved (for audit) |

---

### VIEW — Show open items

Run this Node.js script to pull open items:

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
db.collection('book_of_work')
  .where('status', 'in', ['open', 'tbd', 'monitoring'])
  .get()
  .then(snap => {
    if (snap.empty) { console.log('BOW is clean — no open items.'); return; }
    const byPriority = {};
    snap.docs.forEach(d => {
      const data = d.data();
      const p = data.priority || 99;
      if (!byPriority[p]) byPriority[p] = [];
      byPriority[p].push({ id: d.id, ...data });
    });
    Object.keys(byPriority).sort().forEach(p => {
      console.log(`\nP${p}:`);
      byPriority[p].forEach(item => {
        const title = item.title || item.summary || '(no title)';
        console.log(`  [${(item.status || '').toUpperCase()}] ${item.id} — ${title}`);
      });
    });
    console.log(`\nTotal open: ${snap.size}`);
  });
```

Write this to a temp file and run it with node. Present the output as a clean table grouped by priority.

After displaying, ask: **"Would you like to mark any of these as done, or add a new item?"**

---

### DONE — Mark item as done

When the user provides an ID (e.g. `bow done BUG-PC-003` or `bow done EpiZJiDowkmvv8OpWOmU`):

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
const id = 'ITEM_ID_HERE';
db.collection('book_of_work').doc(id).update({
  status: 'done',
  resolvedAt: new Date().toISOString(),
  resolutionNote: 'RESOLUTION_NOTE_HERE',
}).then(() => console.log('Marked done: ' + id));
```

Ask for a one-line resolution note before marking done (e.g. "Fixed in v2.0.1 — added Firestore rule").

---

### ADD — Create new BOW item

Collect from the user or infer from context:
- `id` — short identifier (e.g. BUG-XX-001, FEAT-XX-001, CHORE-XX-001)
- `title` — one-line description
- `category` — `bug` | `feat` | `chore` | `security`
- `severity` — `critical` | `high` | `medium` | `low`
- `priority` — 1 (do now) to 4 (backlog)
- `status` — default `open`
- `notes` — detail, root cause, suggested fix

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();
db.collection('book_of_work').doc('ITEM_ID').set({
  title: 'TITLE',
  category: 'CATEGORY',
  severity: 'SEVERITY',
  priority: PRIORITY,
  status: 'open',
  notes: 'NOTES',
  createdAt: new Date().toISOString(),
  version: 'CURRENT_VERSION',
});
```

---

### Confirm

```
bill> 📋 BOW — N open items
     P1: [list]
     P2: [list]
     P3: [list]
     Monitoring: [list]
```

or after update:
```
bill> ✅ BOW updated — [ID] marked done
     Resolution: [note]
```
