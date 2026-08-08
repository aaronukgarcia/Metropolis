---
description: Audit IAM bindings for all secrets in apphosting.yaml — verify App Hosting SA has accessor rights to every secret before building
allowed-tools: Bash(npx firebase-tools:*), Bash(grep:*), Bash(node:*)
---

## Context

- Secrets in apphosting.yaml: !`grep "secret:" app/apphosting.yaml | grep -v "#" | awk '{print $2}' | sort -u`
- App Hosting SA: `firebase-app-hosting-compute@studio-6033436327-281b1.iam.gserviceaccount.com`

## Your task

Verify that every secret referenced in `apphosting.yaml` exists in GCP Secret Manager AND has the correct IAM binding for the App Hosting service account.

**Why this matters:** If any secret is missing or missing its IAM binding, EVERY App Hosting build fails at the preparer step with `fah/misconfigured-secret`. The build log shows the error but the App Hosting dashboard just shows the previous build still serving — no obvious alert. The site goes stale silently.

**Lesson (2026-03-03):** CRON_SECRET was in apphosting.yaml but IAM access was never granted. All builds from v2.0.0 to v2.0.2 failed. Site stuck on v1.99.5 for 4 hours.

---

### STEP 1 — Extract all secrets from apphosting.yaml

The secrets from the context above are the ones to audit. The current list is:
- GRAPH_TENANT_ID
- GRAPH_CLIENT_ID
- GRAPH_CLIENT_SECRET
- GRAPH_SENDER_EMAIL
- WHATSAPP_APP_SECRET
- CRON_SECRET
- OPENF1_USERNAME
- OPENF1_PASSWORD
- AZURE_CLIENT_SECRET

If the grep output above shows a different list (new secrets added), use that list instead.

---

### STEP 2 — Check each secret

For each secret, run:

```bash
npx firebase-tools apphosting:secrets:describe SECRET_NAME --project studio-6033436327-281b1 2>&1
```

Classify the result:

| Result | Status | Meaning |
|--------|--------|---------|
| Shows secret details + backend access granted | ✅ OK | Build will succeed |
| `Error: Secret ... does not exist` | ❌ MISSING | Create it: `firebase apphosting:secrets:set SECRET_NAME` |
| Shows secret but no backend access | ⚠️ NO ACCESS | Grant it: `firebase apphosting:secrets:grantaccess SECRET_NAME --backend prixsix` |
| `PermissionDenied` on the describe itself | ⚠️ CHECK MANUALLY | Run gcloud directly or check GCP console |

Run all 9 checks. Do not stop on first failure — audit all of them.

---

### STEP 3 — Fix any issues found

For each ❌ MISSING secret:
```bash
npx firebase-tools apphosting:secrets:set SECRET_NAME --project studio-6033436327-281b1
```
Then set the value when prompted. Then grant access (step below).

For each ⚠️ NO ACCESS secret:
```bash
npx firebase-tools apphosting:secrets:grantaccess SECRET_NAME --backend prixsix --project studio-6033436327-281b1
```

Re-run the describe check to confirm the fix.

---

### STEP 4 — Trigger a new build if any fixes were made

If any secret was missing access and you just fixed it, the last failed build won't retry automatically. Trigger a new rollout:

```bash
npx firebase-tools apphosting:rollouts:create prixsix --git-branch main --project studio-6033436327-281b1
```

---

### Final report

```
bill> 🔑 IAM Check — apphosting.yaml secrets audit

GRAPH_TENANT_ID:      ✅ / ⚠️ / ❌
GRAPH_CLIENT_ID:      ✅ / ⚠️ / ❌
GRAPH_CLIENT_SECRET:  ✅ / ⚠️ / ❌
GRAPH_SENDER_EMAIL:   ✅ / ⚠️ / ❌
WHATSAPP_APP_SECRET:  ✅ / ⚠️ / ❌
CRON_SECRET:          ✅ / ⚠️ / ❌
OPENF1_USERNAME:      ✅ / ⚠️ / ❌
OPENF1_PASSWORD:      ✅ / ⚠️ / ❌
AZURE_CLIENT_SECRET:  ✅ / ⚠️ / ❌

Overall: ✅ All secrets bound — builds will succeed
      or ❌ N secrets missing access — builds WILL FAIL until fixed
```

**Run this skill whenever:**
- A new secret is added to apphosting.yaml
- Builds are failing at the preparer step
- Starting a new sprint (preventative check)
