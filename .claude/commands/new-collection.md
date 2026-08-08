---
description: Checklist for adding a new Firestore collection — rules, docs, code.json, deploy
allowed-tools: Bash(grep:*), Bash(git diff:*), Bash(npx firebase-tools:*)
---

## Context

- Collection name from arguments: $ARGUMENTS
- Current firestore.rules: !`grep -n "match /" app/src/firestore.rules | tail -30`
- docs/COLLECTIONS.md exists: !`test -f docs/COLLECTIONS.md && echo "YES" || echo "NO — create it"`

## Your task

A new Firestore collection is being added. Work through every gate in order before any code is written or deployed.

**Collection name:** $ARGUMENTS (if not provided, ask the user for the collection name before proceeding)

---

### GATE 1 — Define the collection

Answer these questions before writing any code:

1. **Purpose:** What does this collection store? (one sentence)
2. **Schema:** What are the key fields? (name, type, example value)
3. **Access pattern:** Who reads it? Who writes it? (signed-in users / admin only / public / server only)
4. **Sensitivity:** Does it contain PII, secrets, or user-generated content?
5. **Lifetime:** Is data permanent, or should it expire/be deleted? (e.g. temp data, rate-limit windows)

Present these answers for confirmation before continuing.

---

### GATE 2 — Add Firestore security rule

Open `app/src/firestore.rules` and add a rule block. The rule MUST follow this pattern:

```javascript
// GUID: COLLECTION_NAME_RULES-001-v01
// [Intent] [One sentence: what this collection stores and why the access model is correct]
// [Inbound Trigger] [What service/component reads or writes this collection]
// [Downstream Impact] [What breaks if the rule is wrong — e.g. "silent [] return in getX()"]
match /COLLECTION_NAME/{docId} {
  allow read: if CONDITION;   // e.g. isSignedIn(), isAdmin(), true (public), false (server-only)
  allow write: if CONDITION;
}
```

**Access model guidance:**
| Data type | Read | Write |
|-----------|------|-------|
| Public reference data (e.g. official_teams) | `isSignedIn()` | `isAdmin()` |
| User's own data | `isSignedIn() && request.auth.uid == userId` | same |
| Admin-only | `isAdmin()` | `isAdmin()` |
| Server-only (written by Admin SDK) | `false` | `false` |
| Rate limit / ephemeral | `isSignedIn()` | `isSignedIn()` |

**Never use `allow read, write: if true;`** — flag this and refuse to proceed if someone suggests it.

After adding the rule, verify it compiles:
```bash
npx firebase-tools firestore:rules --project studio-6033436327-281b1 2>&1 | head -5
```

---

### GATE 3 — Update docs/COLLECTIONS.md

Add an entry for the new collection in `docs/COLLECTIONS.md`. Format:

```markdown
## `collection_name`

| Field | Purpose |
|-------|---------|
| Created by | [module or script] |
| Read by | [module or component] |
| Retention | [permanent / expires after X / manual cleanup] |

**Schema:**
| Field | Type | Description |
|-------|------|-------------|
| fieldName | string | ... |

**Access rule:** [describe who can read/write and why]

**Gotchas:**
- [any silent failure modes, e.g. "client SDK returns [] on permission denied — check rule before debugging data"]
```

---

### GATE 4 — Add to code.json variables section

Add the collection name string as a `collection-name` variable in `code.json`:

```json
{
  "name": "collection_name",
  "type": "collection-name",
  "value": "collection_name",
  "description": "One sentence purpose",
  "definedIn": "path/to/file-that-first-uses-it.ts",
  "usedIn": []
}
```

---

### GATE 5 — Deploy the rules

Run `/rules-deploy` to push the updated rules to production.

**Do not skip this.** The collection rule is not live until deployed. App Hosting build does not deploy rules.

---

### GATE 6 — Add a BOW item if needed

If the collection requires any follow-up work (e.g. backfill existing data, add index, set up TTL), create a BOW item via `/bow add`.

---

### Confirm

```
bill> ✅ New collection checklist complete — [collection_name]
     Rule added: ✅ [access model summary]
     docs/COLLECTIONS.md: ✅
     code.json variables: ✅
     Rules deployed: ✅
     BOW item: [created ID or "none needed"]
```
