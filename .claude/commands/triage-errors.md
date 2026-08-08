---
description: Pull recent error_logs from Firestore, classify noise vs real bugs, group by pattern, recommend action
allowed-tools: Bash(node:*)
---

## Your task

Triage the Prix Six error log. Pull recent `error_logs` entries from Firestore, classify each as
**noise** or **real**, group by pattern, and recommend action for each group.

Optional argument in $ARGUMENTS:
- A number (e.g. `50`) — how many recent errors to pull (default: 30)
- An error code (e.g. `PX-9002`) — filter to that code only
- A route (e.g. `/dashboard`) — filter to that route only
- `all` — pull last 100

---

## STEP 1 — Pull recent error_logs

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

const LIMIT = 30; // adjust from \$ARGUMENTS if provided

db.collection('error_logs')
  .orderBy('timestamp', 'desc')
  .limit(LIMIT)
  .get().then(snap => {
    console.log('Total pulled:', snap.size);
    snap.forEach(doc => {
      const d = doc.data();
      console.log(JSON.stringify({
        id: doc.id,
        errorCode: d.errorCode,
        error: d.error,
        stack: (d.stack || '').split('\n')[0],
        route: d.context?.route,
        userAgent: d.context?.userAgent,
        type: d.context?.type,
        timestamp: d.context?.timestamp || d.timestamp,
        correlationId: d.correlationId,
      }));
    });
    process.exit(0);
  }).catch(e => { console.error(e); process.exit(1); });
"
```

Adjust `LIMIT` based on $ARGUMENTS (default 30, `all` → 100, number → that number).
If filtering by error code or route, add a `.where()` clause before `.limit()`.

---

## STEP 2 — Classify each error

For every error in the output, apply these rules in order. First match wins.

### NOISE — suppress, no action needed

| Signal | Classification | Why |
|--------|---------------|-----|
| `userAgent` matches `/bot\|crawl\|spider\|bingbot\|googlebot/i` | **Bot noise** | Crawlers have no auth session — all their errors are expected |
| `error` contains `performance/invalid attribute value` | **PERF-001 noise** | Tailwind class strings in Firebase Performance putAttribute — already filtered in GlobalErrorLogger |
| `error` contains `Failed to fetch dynamically imported module` or `ChunkLoadError` or `Loading chunk` | **Chunk noise** | Stale build chunk after deploy — auto-reloads silently since v2.0.19 |
| `error` is exactly `"Load failed"` and `context.type` is `unhandledrejection` | **Chunk noise (Safari)** | iOS Safari reports dynamic import chunk failures as `TypeError: "Load failed"` — no message, no name. Fixed in v2.0.19. |
| `context.action` is `global_crash` and `error` contains `ChunkLoadError` or `Loading chunk` | **Chunk noise (boundary)** | React error boundary caught chunk error before window handlers — fixed in v2.0.19, global-error.tsx now reloads silently |
| `error` contains `analytics` and (`403` or `Failed to fetch`) | **Analytics noise** | Firebase Analytics API key restriction or config 403 — non-critical, suppressed at source since v1.99.3 |
| `route` is `/login` or `/about` and `error` contains `Failed to fetch` | **Auth-page noise** | Public pages making unauthenticated Firebase calls — usually analytics or performance SDK init |
| `error` contains `Connection to Indexed Database server lost` | **IndexedDB noise** | Safari iOS browser-env issue — Firebase SDK IDB reset or private browsing. Unactionable. Filtered at source since v2.0.32 (`isIndexedDBError` in GlobalErrorLogger). If appearing despite filter, a page is initialising Firebase SDK unnecessarily — check which route and remove the SDK init. `/signup` was the culprit in BUG-ERR-003. |
| `errorCode` is `PX-9002` and `error` contains `Indexed Database` | **IndexedDB noise** | Same as above — PX-9002 is the NETWORK_ERROR code used as fallback for all unhandledrejections |

### REAL — investigate and act

| Signal | Classification | Investigation hint |
|--------|---------------|-------------------|
| `errorCode` is `PX-4006` | **Permission denied** | Check Firestore rules for the collection. Check if the user's auth token was valid at call time. |
| `error` contains `Unexpected end of JSON input` | **Empty response body** | Find the fetch call that calls `.json()` without checking `res.ok` first. Check for 204/empty 200 responses. |
| `error` contains `Failed to fetch` with a real browser UA (not bot) | **Real network failure** | Check which API or external endpoint. Could be OpenF1, Graph API, or a cold-start timeout. |
| `errorCode` is `PX-2xxx` | **Scoring error** | Check `race_results` and `predictions` collections for the race/user in context. |
| `errorCode` is `PX-5xxx` | **Auth / login error** | Check `login_attempts` and `user_logons`. May be a brute-force signal. |
| Same error + same route from many distinct `correlationId`s | **Systematic bug** | This is affecting all users on that route — prioritise immediately. |
| `stack` references a specific file in `app/src/` | **Traceable error** | Read that file and find the throw site. |

### AMBIGUOUS — needs context

| Signal | How to resolve |
|--------|---------------|
| `userAgent` is empty or `unknown` | May be a server-side error logged via `/api/log-client-error` without a browser context |
| `route` is `unknown` | Error occurred outside a page navigation — check `type` (error vs unhandledrejection) and stack |
| No stack trace | Error was a string rejection, not an Error object — look at `error` message for clues |
| `context.action` is `global_crash` | Came from `global-error.tsx` (React error boundary), NOT `GlobalErrorLogger`. React caught the error before window event handlers fired. Check if `error` message is chunk-related first — if so, classify as NOISE (fixed v2.0.19). If not, it's a genuine React render crash — check the stack for the component that threw. |
| `context.action` is `client_crash` | Came from `app/(app)/error.tsx` or `app/error.tsx` — React error boundary inside the `(app)` group. Same pattern as `global_crash` but scoped to the app subtree. |
| `route` looks innocent (a simple page with no logic) | **The route names where the error boundary LANDED, not where the fault lives.** An exception during a route transition is attributed to the destination page. Classify by the STACK, not the route — a minified `.destroy`/`.remove` frame means the fault is in whatever was being unmounted (BUG-WELCOME-001: "route /welcome" was actually the Pit Wall's Pixi teardown racing its own init, fixed v3.23.1). |

---

### Note: 60-second deduplication (v2.0.32+)

GlobalErrorLogger deduplicates — same error message + route logs at most once per 60 seconds. So if you see N docs of the same error, it ran for N minutes, not N separate incidents. A count of 20 identical errors = the issue persisted for ~20 minutes, not 20 page loads.

---

## STEP 3 — Group and count patterns

After classifying, group errors by their **pattern** — same (errorCode + error message prefix + route).

For each group output:
```
[CODE] PX-XXXX — "message prefix..." @ /route
  Count: N | Classification: NOISE / REAL / AMBIGUOUS
  User-agents: [list unique UAs, truncated]
  First seen: [timestamp] | Last seen: [timestamp]
  Recommendation: [see playbooks below]
```

---

## STEP 4 — Apply fix playbooks

### Playbook: New noise pattern identified

If a noise pattern is appearing that GlobalErrorLogger doesn't yet filter:

1. Identify the distinguishing signal (error message fragment, UA pattern, or route+message combo)
2. Add a new filter function to `GlobalErrorLogger.tsx`:
   ```typescript
   // GUID: COMPONENT_GLOBAL_ERROR_LOGGER-00N-v01
   function isXxxNoise(error: any): boolean {
     const message = error?.message || error?.toString() || '';
     return message.includes('your-distinguishing-string');
   }
   ```
3. Call it in both `handleError` and `handleRejection` before `logErrorToServer`
4. Bump GUID versions on affected blocks
5. Run `/commit` — bump version

### Playbook: Missing `.catch()` on a Promise chain

Symptoms: "Unexpected end of JSON input" or "Failed to fetch" from a real user UA on a specific route.

1. Find which component renders on that route
2. Search for `fetch(` calls in that component
3. Check: is the call inside `try { } catch { }`? If not, that's the gap
4. Check: does it call `.json()` before checking `res.ok`? Add `.ok` guard:
   ```typescript
   if (!res.ok) throw new Error(`HTTP ${res.status}`);
   const data = await res.json();
   ```
5. Check: is the call fire-and-forget (not awaited, no `.catch()`)? Add `.catch(() => {})` if non-critical

### Playbook: Firestore permission denied (PX-4006)

1. Identify the collection from the error context or stack
2. Read `app/src/firestore.rules` — find the `match` block for that collection
3. Check whether the rule requires `isSignedIn()`, `isAdmin()`, or `isOwner()`
4. Reproduce: was the user's session valid at the time? Check the timestamp against `user_logons`
5. If rules are wrong → fix and deploy `firestore.rules` via `firebase deploy --only firestore:rules`
6. If session was stale → add a token refresh before the critical write (or move to server API + Admin SDK)

### Playbook: Systematic error (same error, many users, same route)

This is P1 — fix before next race.

1. Get the full list of affected correlationIds
2. Reproduce locally: `npm run dev`, navigate to that route
3. Check browser DevTools Network tab for the failing request
4. Fix at source — don't just filter it from the logger

---

## STEP 5 — Final output

```
bill> Error Log Triage — [N] errors pulled

  🔇 NOISE ([N]) — no action needed
     [list noise groups with counts]

  🔴 REAL ([N]) — action required
     [list real error groups with recommended playbook]

  🟡 AMBIGUOUS ([N]) — needs investigation
     [list ambiguous groups]

  Recommended next action: [clear directive or "error log is clean"]
```

If any REAL errors exist, offer to tackle them immediately using the relevant playbook.
If new noise patterns are found that GlobalErrorLogger doesn't yet filter, offer to add filters.
