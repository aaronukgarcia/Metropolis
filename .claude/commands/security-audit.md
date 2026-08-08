---
description: Full repo security audit — scan for secrets, hardcoded values, committed sensitive files, untracked local config, gitignore gaps, and stale service account keys. Produces SEC-NNN findings ready to add to the BOW (claude-bow.js).
allowed-tools: Bash(node:*), Bash(git:*), Bash(grep:*), Bash(find:*)
---

## Context

- Repo root: `E:\git\prix6`  ← **verify with `git rev-parse --show-toplevel` before scanning — a stale path here once made every grep run against nothing (same failure class as the v3.1.8 version-guard incident)**
- Project root: `E:\git\prix6\03.Current`
- Firebase project: `studio-6033436327-281b1`
- Service account: `firebase-adminsdk-fbsvc@studio-6033436327-281b1.iam.gserviceaccount.com`
- gcloud CLI: `"C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd"`

---

## Your task

Run a systematic security audit of the Prix Six repository. Work through each step below in order. Collect all findings, then present them as a structured report at the end.

---

## STEP 0 — Validate the scanners (planted positive)

**An empty grep is only evidence if the pattern is proven to match.** Before trusting any "no hits" result below, verify each credential pattern against synthetic positives:

```bash
cd "E:\git\prix6"
# Confirm you are at the real repo root (guards against stale-path drift):
git rev-parse --show-toplevel

# Planted positives — every line MUST produce a match; if any pattern fails, FIX THE PATTERN before proceeding:
printf 'x AIzaSyFAKE1234567890abcdefghijklmnopqr x' | grep -c "AIzaSy"
printf -- '-----BEGIN PRIVATE KEY-----' | grep -c "BEGIN PRIVATE KEY\|BEGIN RSA PRIVATE KEY\|BEGIN EC PRIVATE KEY"
printf 'ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE' | grep -c "ghp_\|github_pat_\|gho_"
printf 'AccountKey=FAKEBASE64FAKEBASE64FAKEBASE64==' | grep -c "AccountKey="
printf 'clientSecret: "FAKEFAKEFAKE123"' | grep -c "clientSecret\s*[=:]\s*['\"][^'\"]\{10,\}"
```

Any pattern that fails its planted positive would have reported CLEAN while blind. Record in the final report that STEP 0 passed.

---

## STEP 1 — Scan tracked files for credential patterns

Run these greps against git-tracked files only (`git ls-files` to scope the search):

```bash
# From repo root
cd "E:\git\prix6"

# 1a. Firebase/Google API keys (AIzaSy...)
git ls-files | xargs grep -l "AIzaSy" 2>/dev/null

# 1b. Private key / PEM blocks
git ls-files | xargs grep -l "BEGIN PRIVATE KEY\|BEGIN RSA PRIVATE KEY\|BEGIN EC PRIVATE KEY" 2>/dev/null

# 1c. GitHub personal access tokens (ghp_, github_pat_)
git ls-files | xargs grep -rn "ghp_\|github_pat_\|gho_" 2>/dev/null

# 1d. Hardcoded project ID used as a fallback (not in config files)
git ls-files -- "*.ts" "*.tsx" "*.js" | xargs grep -rn "|| 'studio-6033436327-281b1'\||| \"studio-6033436327-281b1\"" 2>/dev/null

# 1e. Hardcoded connection strings / credentials patterns
git ls-files -- "*.ts" "*.tsx" "*.js" "*.json" | xargs grep -rn "password\s*=\s*['\"][^'\"]\+['\"]\|secret\s*=\s*['\"][^'\"]\+['\"]" 2>/dev/null | grep -v "node_modules\|test\|spec\|example\|placeholder\|YOUR_\|<"

# 1f. Azure / MS credentials hardcoded
git ls-files | xargs grep -rn "clientSecret\s*[=:]\s*['\"][^'\"]\{10,\}" 2>/dev/null
```

**Classification:**
- `NEXT_PUBLIC_FIREBASE_*` values in `apphosting.yaml` — **NOISE** (intentionally public, safe to commit)
- `apphosting.yaml` AZURE_TENANT_ID / AZURE_CLIENT_ID — **LOW** (semi-public identifiers, not secrets)
- Any `secret:` directive in `apphosting.yaml` — **OK** (using Key Vault, not plaintext)
- Actual key material (`AIzaSy...`, PEM blocks, tokens) in non-config files — **CRITICAL**

---

## STEP 2 — Check for committed sensitive files or directories

```bash
cd "E:\git\prix6"

# 2a. Service account JSON files
git ls-files | grep -i "service.account\|serviceaccount\|service_account"

# 2b. Environment files that slipped through
git ls-files | grep -E "\.env$|\.env\."

# 2c. Browser profile / automation session data
git ls-files | grep -iE "puppeteer|playwright|\.wwebjs|chromium|chrome.profile"

# 2d. Credentials or key files
git ls-files | grep -iE "credential|\.p8$|\.pem$|\.key$|\.pfx$|\.p12$"

# 2e. Auth session data
git ls-files | grep -iE "session|auth.cache|\.wwebjs"

# 2f. Credential template files that contain real values (check content, not just name)
git ls-files | grep -i "credential\|template\|onboarding" | head -20
```

For any hit in 2f, check if the file contains real values vs placeholder text (`[GET FROM AARON]`, `YOUR_KEY_HERE`, etc.).

---

## STEP 2B — Scan UNTRACKED local config for secrets (working-dir hygiene)

`git ls-files` covers tracked files only — but secrets also leak into gitignored local config, where they are one `git add -f`, one backup, or one screen-share away from exposure. **Precedent: SEC-008 (2026-08-02)** — a full Azure storage account key sat in `.claude/settings.local.json` inside a Bash permission-allowlist entry; the tracked-only audit run that same day reported CLEAN and missed it.

```bash
cd "E:\git\prix6"

# 2B-a. Enumerate untracked + ignored files that commonly hold credentials
git ls-files --others --exclude-standard | grep -iE "\.env|settings.*\.json|credential|\.key$|\.pem$"
git ls-files --others --ignored --exclude-standard | grep -viE "node_modules|\.next|dist|build" | head -40

# 2B-b. Grep the known local-config locations with the STEP 1 patterns
grep -n "AIzaSy\|AccountKey=\|BEGIN PRIVATE KEY\|ghp_\|clientSecret\|SharedAccessSignature\|sig=" 03.Current/.claude/settings.local.json 2>/dev/null
grep -rn "AIzaSy\|AccountKey=\|BEGIN PRIVATE KEY\|ghp_" 03.Current/app/.env* 03.Current/.env* 2>/dev/null
```

**Classification:** findings here are NOT repo leaks (verify with `git ls-files <file>` + `git check-ignore -v <file>` and say so in the report) — they are **MEDIUM working-dir hygiene**: rotate the credential, rewrite the config to read from env at runtime, purge the plaintext. If the file IS tracked, escalate to **CRITICAL** (repo leak — rotate immediately).

---

## STEP 3 — Audit .gitignore for gaps

```bash
cd "E:\git\prix6"
cat .gitignore
```

Check for these common gaps — flag as **MISSING** if not covered:

| Pattern | Should be ignored |
|---------|------------------|
| `**/service-account*.json` | ✅ Service account keys |
| `**/.env` / `**/.env.*` | ✅ Environment files |
| `**/node_modules/` | ✅ Dependencies |
| `**/.wwebjs_auth/` | ✅ WhatsApp session |
| `**/.puppeteer*` / `**/playwright-*` | ✅ Browser automation profiles |
| `**/*.pem` / `**/*.key` / `**/*.p8` | ✅ Key files |
| `**/credentials*.json` / `**/*-credentials*` | ✅ Credential files |

Also check: are there any `!` (allowlist) entries that override the above and expose sensitive files?

---

## STEP 4 — Check for hardcoded values in source files

```bash
cd "E:\git\prix6\03.Current"

# 4a. Project ID used as a non-config hardcoded fallback
grep -rn "studio-6033436327-281b1" app/src/ --include="*.ts" --include="*.tsx" --include="*.js" | grep -v "node_modules"

# 4b. Any Firebase storageBucket, databaseURL hardcoded (vs env vars)
grep -rn "\.firebaseapp\.com\|\.appspot\.com\|firebasestorage\.googleapis\.com" app/src/ --include="*.ts" --include="*.tsx" | grep -v "NEXT_PUBLIC\|process\.env"

# 4c. Any Azure tenant/client IDs hardcoded in source (apphosting.yaml is OK)
grep -rn "70edfb9b\|ea123d4d" app/src/ --include="*.ts" --include="*.tsx" | grep -v "node_modules"
```

Hardcoded non-secret identifiers are **LOW** severity — flag them but they're not critical. Hardcoded fallbacks to production IDs are **MEDIUM** — they should use env vars with no fallback.

---

## STEP 5 — Check service account key age

```bash
"C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" iam service-accounts keys list \
  --iam-account=firebase-adminsdk-fbsvc@studio-6033436327-281b1.iam.gserviceaccount.com \
  --project=studio-6033436327-281b1 \
  --format="table(name.basename():label=KEY_ID,validAfterTime:label=CREATED,validBeforeTime:label=EXPIRES)"
```

**Classification:**
- Keys older than 90 days → **MEDIUM** (rotate soon)
- Keys older than 180 days → **HIGH** (rotate now)
- More than 2 active keys → **MEDIUM** (investigate — old keys should be deleted after rotation)
- System-managed keys (created by Google, not user-created) → **NOISE** (ignore)

---

## STEP 6 — Classify and report

After running all steps, compile findings using this format:

```
bill> Security Audit — [date]

  🔴 CRITICAL ([N]) — immediate action required
     SEC-NNN: [finding] @ [file/location]
       Risk: [what could go wrong]
       Fix: [specific action]

  🟠 HIGH ([N]) — fix before next deploy
     SEC-NNN: [finding]
       Risk: [...]
       Fix: [...]

  🟡 MEDIUM ([N]) — fix this sprint
     SEC-NNN: [finding]
       Risk: [...]
       Fix: [...]

  🟢 LOW ([N]) — informational
     SEC-NNN: [finding]
       Risk: [...]
       Fix: [...]

  ✅ NOISE ([N]) — no action needed
     [list noise items with one-line explanation]

  Overall posture: [CLEAN / NEEDS ATTENTION / CRITICAL]
  Scanner validation: [STEP 0 passed — all planted positives matched / FAILED on: ...]
  Negative results: [for each clean area, state METHOD: "pattern X over scope Y — 0 hits"]
  Recommended next action: [clear directive]
```

**A CLEAN posture may only be declared if STEP 0 passed and every negative result names its pattern + scope.** An empty grep with an unvalidated pattern or a stale path is not evidence of absence — it is evidence you didn't look.

Number findings sequentially. If previous SEC findings exist in the BOW (`node claude-bow.js list --all` — look for titles starting `SEC-`), pick up from the next available number.

---

## STEP 7 — Offer to act

For each CRITICAL or HIGH finding:
- Offer to fix immediately (git rm --cached, remove fallback, rotate key)
- Offer to add findings to the BOW as bug items: `node claude-bow.js add bug "SEC-NNN: <summary>" --priority P0` (P0 for CRITICAL, P1 for HIGH), detail in `--desc` or a follow-up `comment`

For MEDIUM findings:
- Offer to add to the BOW for tracking: `node claude-bow.js add bug "SEC-NNN: <summary>" --priority P2`

---

## Known noise — do not flag these

These are confirmed safe and should be classified as NOISE automatically:

| Pattern | Why it's safe |
|---------|--------------|
| `NEXT_PUBLIC_FIREBASE_API_KEY=AIzaSy...` in `apphosting.yaml` | Firebase web API key is intentionally public — security is via Firestore rules, not key secrecy |
| `AZURE_TENANT_ID` / `AZURE_CLIENT_ID` in `apphosting.yaml` | Semi-public OAuth identifiers — not secrets |
| `studio-6033436327-281b1` in `apphosting.yaml` / `firebase.json` / `.firebaserc` | Config files — expected location |
| `GARTH-CREDENTIALS-TEMPLATE.md` | Removed from git tracking in v1.99.5 |
| `app/scripts/.puppeteer-test-profile/` | Removed from git tracking in v1.99.5 |
| System-managed service account keys | Created and managed by Google — not user-rotatable |
