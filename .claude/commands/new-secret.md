---
description: Add a new secret to the Prix Six app — creates in Secret Manager, updates apphosting.yaml, grants App Hosting access, optionally Cloud Functions. Prevents silent build failures.
allowed-tools: Bash(npx firebase-tools:*), Bash(node:*), Read, Edit
---

## Context

- ARGUMENTS: $ARGUMENTS  ← secret name (e.g. `CRON_SECRET`) or leave blank to be prompted
- Current secrets in apphosting.yaml: !`grep "secret:" app/apphosting.yaml | grep -v "#" | awk '{print $2}' | sort -u`
- App Hosting backend: `prixsix`
- Project ID: `studio-6033436327-281b1`

## Your task

Walk through every step of adding a new secret to the app. **Never skip or reorder steps** — the grant-access step is critical and has caused silent 4-hour build failures when missed.

**RULE — no plaintext secret values outside Secret Manager.** Never inline a secret value (connection string, account key, SAS token) into:
- `.claude/settings.local.json` permission-allowlist entries — write allowlist patterns that read the value from env at runtime instead (precedent: SEC-008, an Azure storage account key embedded in an allowlist entry and spread into permission strings)
- inline Node scripts, commit messages, BOW notes, or claude-sync log messages
- any file in the working tree, tracked or not — gitignore protects the repo, not the disk

---

### STEP 0 — Confirm the secret name

If ARGUMENTS is blank, ask: "What is the secret name?" (use UPPER_SNAKE_CASE).

Check if it already exists in apphosting.yaml (from context above). If it does, stop and say it's already registered — use `/iam-check` to verify its access instead.

---

### STEP 1 — Create the secret in Secret Manager

```bash
npx firebase-tools apphosting:secrets:set SECRET_NAME --project studio-6033436327-281b1
```

This command is **interactive** — it will prompt for the secret value. Do NOT run it non-interactively. Tell the user:

> "The Firebase CLI will now prompt you for the secret value. Paste it in and press Enter."

Wait for confirmation that the secret was created before proceeding.

Alternatively, if the user already has the value and wants non-interactive creation, use gcloud:
```bash
# Generate a random value (for secrets like CRON_SECRET):
node -e "const {randomBytes} = require('crypto'); console.log(randomBytes(32).toString('hex'));"
# Then:
printf '%s' 'THE_SECRET_VALUE' | "C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" secrets create SECRET_NAME --project=studio-6033436327-281b1 --replication-policy=automatic --data-file=-
```

---

### STEP 2 — Add the secret to apphosting.yaml

Read `app/apphosting.yaml` and add the new env block in the appropriate section. Use the pattern:

```yaml
  - variable: SECRET_NAME
    secret: SECRET_NAME
```

Place it logically (group with related secrets, e.g. WhatsApp secrets together, Graph secrets together).

---

### STEP 3 — Grant access to the App Hosting backend (CRITICAL — never skip)

```bash
npx firebase-tools apphosting:secrets:grantaccess SECRET_NAME --backend prixsix --project studio-6033436327-281b1
```

Expected output: `Successfully set IAM bindings on secret SECRET_NAME.`

**Why this is critical:** If skipped, EVERY subsequent App Hosting build will fail at the preparer step with `fah/misconfigured-secret`. The build log shows the error but the App Hosting dashboard silently continues serving the previous build. No obvious alert. This is exactly what happened with CRON_SECRET (2026-03-03 — 4 hours of stale deployments).

---

### STEP 4 — Cloud Functions (conditional)

Ask: "Does this secret need to be available in Cloud Functions as well?"

If yes:
```bash
npx firebase-tools functions:secrets:set SECRET_NAME --project studio-6033436327-281b1
```

Note: Cloud Functions secrets are separate from App Hosting secrets. Setting one does NOT set the other.

---

### STEP 5 — Verify

```bash
npx firebase-tools apphosting:secrets:describe SECRET_NAME --project studio-6033436327-281b1
```

Confirm:
- Secret exists (version 1+, ENABLED)
- `prixsix` backend is listed under access grants

---

### STEP 6 — Confirm to user

```
bob> ✅ Secret registered: SECRET_NAME
     Secret Manager: version 1, ENABLED
     apphosting.yaml: added
     App Hosting grant: ✅ prixsix backend has access
     Cloud Functions: ✅ set  /  ⬜ not needed

     Next: commit the apphosting.yaml change and push to trigger a build.
     Run /iam-check to verify all secrets are bound before pushing.
```

---

**Run `/iam-check` after any secret change** to verify the full binding state before pushing to main.
