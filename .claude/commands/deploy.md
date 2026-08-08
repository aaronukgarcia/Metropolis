---
description: Post-push build verification — confirm Firebase build succeeded and version is live on both pages
allowed-tools: Bash(git log:*), Bash(git status:*)
---

## Context

- Last commit: !`git log --oneline -1`
- Current branch: !`git branch --show-current`
- Version expected: !`node -e "try{console.log(require('./app/package.json').version)}catch(e){console.log('check package.json')}" 2>/dev/null`

## Your task

A push to `main` has just been made. Work through all verification steps in order.

---

### STEP 1 — Confirm push landed on main

Verify the branch is `main` and the last commit matches what was just pushed. If the branch is not `main`, warn the user — builds only trigger from main.

---

### STEP 2 — Check Firebase App Hosting build status

Use the `mcp__firebase__apphosting_list_backends` tool to check the build status:

- Project: `studio-6033436327-281b1`
- Backend: `prixsix`
- Look for: `reconciling: false` — this means the build has finished
- Note the build ID (format: `build-YYYY-MM-DD-NNN`)

If `reconciling: true`, the build is still in progress. Wait and check again in 60 seconds. Do NOT declare success until `reconciling: false`.

Report: `BUILD ✅ build-YYYY-MM-DD-NNN complete` or `BUILD ⏳ still reconciling — check again shortly`

---

### STEP 3 — Quick anonymous version check via `/api/version`

Fastest verification — `/api/version` is a public endpoint that returns the deployed `APP_VERSION`. No auth required, works cleanly with WebFetch.

Fetch `https://prix6.win/api/version` and confirm the JSON `{ "version": "X.Y.Z" }` matches the expected version from package.json.

This is the primary deploy-verified signal. If this returns the expected version, the build is live and the version constant deployed correctly. /about and /login are authoritative double-checks (next steps) for the user to confirm in a browser.

Report: `API ✅ vX.Y.Z` or `API ❌ shows vX.Y.Z (expected vA.B.C) — build may still be propagating, retry in 60s`

---

### STEP 4 — Verify version on About page

Fetch https://prix6.win/about and confirm the version displayed matches.

Note: /about is auth-gated for the rendered page content. From an automated session this may return shell HTML without the version visible — if so, ask the user to open the page in their browser and confirm. Step 3 (`/api/version`) is the authoritative anonymous check.

Report: `ABOUT ✅ shows vX.Y.Z` or `ABOUT ⚠️ auth-gated — user to confirm in browser` or `ABOUT ❌ shows vX.Y.Z (expected vA.B.C) — cache issue?`

---

### STEP 5 — Verify version on Login page

Fetch https://prix6.win/login and confirm the version displayed matches.

Both pages must show identical versions to the user. A mismatch means the build deployed partially or the version.ts was not updated.

Report: `LOGIN ✅ shows vX.Y.Z` or `LOGIN ⚠️ user to confirm` or `LOGIN ❌ mismatch`

---

### STEP 5B — Cloud Functions deploy verification (added v3.1.7)

Cloud Functions are NOT auto-deployed by App Hosting. If the just-pushed commit's body contains `REQUIRES MANUAL DEPLOY` or `firebase deploy --only functions:`, the user must run that command separately. This step confirms whether they did.

**Check whether functions/ was changed in the just-pushed commit:**

```bash
git show HEAD --stat | grep -E "^ functions/"
```

If `functions/` files ARE in the commit, look at the message for the bundled deploy command:

```bash
git show HEAD --pretty=format:"%B" --no-patch | grep -E "firebase deploy --only functions"
```

**Verify each listed function shows recent activity** (proxy for "deployed"):

For functions writing to `backup_status/latest`, check freshness via the same query as `/health-check` CHECK 11:
- `lastBackupTimestamp` < 25h → dailyBackup deployed and running
- `lastSmokeTestTimestamp` < 8d → runRecoveryTest deployed and running
- `lastRetentionRunTimestamp` < 36h → applyBackupRetention deployed and running

For functions without a status field, check Cloud Functions logs:

```bash
"C:\Program Files (x86)\Google\Cloud SDK\google-cloud-sdk\bin\gcloud.cmd" \
  functions logs read <fn-name> --gen2 \
  --project=studio-6033436327-281b1 --region=europe-west2 --limit=5
```

If the most recent log line predates the commit timestamp, the deploy hasn't been run.

Report:
- `FN ✅ all listed functions deployed and active` — heartbeat / log evidence after commit time
- `FN ⚠️ N functions pending deploy — paste this:` (paste the exact firebase deploy command from the commit body)
- `FN ✅ no functions changed in this commit` — App Hosting deploy is sufficient

If functions DID change but the user hasn't run the deploy yet, **flag this loudly in the Final confirmation** — App Hosting being live does not mean the release is complete.

---

### STEP 5C — Asset verification (added v3.20.2, after BUG-PUBLIC-404)

**A correct version number proves only that the code shipped. It says nothing about whether assets load.**

Learned the hard way on 2026-07-28: v3.20.0 deployed cleanly, `/api/version` reported the new version, the build was green and tsc was clean — and every one of the 85 new trophy images 404'd, so the feature rendered as broken-image placeholders. The same investigation found the site logo had been broken in production since the day it was written, unnoticed.

**Firebase App Hosting does NOT serve `app/public` over HTTP.** Confirmed against the origin directly (`prixsix--studio-6033436327-281b1.europe-west4.hosted.app`), bypassing Cloudflare: `/api/version` answers while `/logo.svg` 404s from that same host. The files *are* deployed — `standings-chart-image.ts` reads `public/fonts/Roboto-Regular.ttf` off disk via `process.cwd()` and works — they are simply never served. `next start` locally serves them at 200, which is exactly why this never shows up in development.

**If the just-pushed commit adds or changes any image, font, or other asset, curl the asset URL itself:**

```bash
# Extract the real hashed asset URL from a rendered page, then fetch it
url=$(curl -s https://prix6.win/login | grep -oE "/_next/static/media/[^\"']+" | head -1)
curl -s -o /dev/null -w "%{http_code}\n" "https://prix6.win$url"
```

Anything referenced by a literal `/foo.svg` path is a **red flag** — grep the diff for `src="/` and confirm those assets are imported through the bundler (emitted to `/_next/static/media/`, which IS served) or generated as `data:` URIs, rather than sitting in `public/`.

Report:
- `ASSETS ✅ no new assets in this commit`
- `ASSETS ✅ /_next/static/media/... returns 200`
- `ASSETS ❌ <url> returns 404 — asset is in public/ and will never be served; import it or inline it`

---

### STEP 6 — Final confirmation

Confirm with your identity prefix:
```
bill> ✅ Deploy verified — vX.Y.Z live
     Build: build-YYYY-MM-DD-NNN ✅
     /api/version: vX.Y.Z ✅
     About page: vX.Y.Z ✅ (or "user to confirm")
     Login page: vX.Y.Z ✅ (or "user to confirm")
     Cloud Functions: ✅ all deployed (or ⚠️ N pending — exact command above)
     Assets: ✅ none added (or "/_next/static/media/... 200")
```

If any step failed:
```
bill> ❌ Deploy issue detected — [what failed]
     Action required: [next step]
```
