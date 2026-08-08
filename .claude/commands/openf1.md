---
description: Diagnose PubChat / OpenF1 timing state — what session OpenF1 is returning, what's in Firestore, and whether isBetweenRaces / isWaitingForNewSession should be true right now
allowed-tools: Bash(node:*), Bash(curl:*), mcp__firebase__firestore_get_document
---

## Context

PubChat has three display states driven by two booleans:

| State | Condition | Display |
|-------|-----------|---------|
| Active session | `!isBetweenRaces && !isWaitingForNewSession` | Leaderboard tabs |
| Between races | `isBetweenRaces` | "PubChat next opens at FP1" + session schedule |
| Waiting for data | `isWaitingForNewSession` | Amber "FP1 underway, waiting for OpenF1" panel |

**`isBetweenRaces`** = stored session started >6h ago AND next qualifying is in the future AND FP1 hasn't started yet.

**`isWaitingForNewSession`** = FP1 has started AND stored Firestore data predates FP1.

**`fp1Date` formula:**
- Normal weekend: `qualifyingTime - 24h`
- Sprint weekend: `qualifyingTime - 4h` (only 1 practice session on sprint weekends — FP1 is same day as Sprint Qualifying)

**`qualifyingTime` on sprint weekends** = Sprint Qualifying time (NOT main qualifying).
Sprint weekend session order: FP1 → SQ → S → Q → GP

OpenF1 `session_key=latest` lags by up to 30+ minutes after a session ends. It continues
returning the previous race's data between race weekends until FP1 of the next race starts
and appears in the API.

---

## STEP 1 — Check OpenF1 `session_key=latest`

```bash
curl -s "https://api.openf1.org/v1/sessions?session_key=latest" | node -e "
const chunks = [];
process.stdin.on('data', c => chunks.push(c));
process.stdin.on('end', () => {
  const data = JSON.parse(chunks.join(''));
  if (!data.length) { console.log('No data returned'); return; }
  const s = data[0];
  console.log('OpenF1 session_key=latest:');
  console.log('  session_key   :', s.session_key);
  console.log('  session_name  :', s.session_name);
  console.log('  meeting_name  :', s.meeting_name);
  console.log('  location      :', s.location);
  console.log('  date_start    :', s.date_start);
  console.log('  date_end      :', s.date_end);
  console.log('  year          :', s.year);
  const start = new Date(s.date_start);
  const hoursSince = (Date.now() - start.getTime()) / (1000 * 60 * 60);
  console.log('  hours since start:', hoursSince.toFixed(1), hoursSince > 6 ? '(>6h — would trigger isBetweenRaces check)' : '(<6h — session considered active)');
});
"
```

---

## STEP 2 — Check Firestore `app-settings/pub-chat-timing`

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

async function run() {
  const doc = await db.doc('app-settings/pub-chat-timing').get();
  if (!doc.exists) { console.log('pub-chat-timing: NOT FOUND'); return; }
  const d = doc.data();
  console.log('Firestore pub-chat-timing:');
  console.log('  sessionName  :', d.session?.sessionName);
  console.log('  meetingName  :', d.session?.meetingName);
  console.log('  location     :', d.session?.location);
  console.log('  dateStart    :', d.session?.dateStart);
  console.log('  fetchedBy    :', d.fetchedBy);
  console.log('  updatedAt    :', d.updatedAt?.toDate?.()?.toISOString() ?? d.updatedAt);
  if (d.session?.dateStart) {
    const start = new Date(d.session.dateStart);
    const hoursSince = (Date.now() - start.getTime()) / (1000 * 60 * 60);
    console.log('  hours since dateStart:', hoursSince.toFixed(1));
  }
  console.log('  driver count :', d.drivers?.length ?? 0);
}
run().catch(console.error);
" 2>/dev/null
```

---

## STEP 3 — Compute isBetweenRaces / isWaitingForNewSession right now

Read `app/src/lib/data.ts` to get the 2026 race schedule, then compute:

```javascript
node -e "
// Paste the RaceSchedule array here or derive from data.ts
// For a quick inline check, use the known schedule:
const now = new Date();
console.log('Now (UTC):', now.toISOString());
console.log('Now (local):', now.toLocaleString('en-GB'));

// Find next race (first with qualifyingTime in future)
// Paste qualifying times from data.ts as needed

// Key formula checks:
// fp1Date (normal): qualifyingTime - 24h
// fp1Date (sprint): qualifyingTime - 4h
// isBetweenRaces: storedDateStart > 6h ago AND nextQualifying > now AND fp1 not started
// isWaitingForNewSession: fp1 started AND storedDateStart < fp1Date
"
```

Then manually compute using the values from Steps 1 and 2:

1. **`hoursSinceStoredSession`** = `(now - pub-chat-timing.session.dateStart)` in hours
2. **`nextQualifyingInFuture`** = is next race's `qualifyingTime` > now?
3. **`fp1Date`** = `nextRace.qualifyingTime - 4h` (sprint) or `- 24h` (normal)
4. **`fp1AlreadyStarted`** = `fp1Date <= now`
5. **`storedDataPreDatesFP1`** = `pub-chat-timing.session.dateStart < fp1Date`

---

## STEP 4 — Check upcoming sessions for next race

```javascript
node -e "
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

// Read static schedule from data.ts and compute next race
// (copy RaceSchedule array or call findNextRace equivalent inline)
// Then print:
//   next race name, hasSprint flag
//   qualifyingTime (= Sprint Qualifying on sprint weekends)
//   sprintTime (sprint race, if applicable)
//   raceTime
//   fp1Date = qualifyingTime - (hasSprint ? 4h : 24h)
//   fp1AlreadyStarted

const now = new Date();
console.log('Current time:', now.toISOString());
// Add schedule inline if needed for offline diagnosis
" 2>/dev/null
```

---

## STEP 5 — Final diagnosis report

```
bill> OpenF1 / PubChat Diagnosis — [timestamp]

OPENF1 (session_key=latest):
  Session    : [session_name] · [meeting_name] · [location]
  Started    : [date_start] ([X]h ago)
  Status     : [Active / >6h old — stale]

FIRESTORE (app-settings/pub-chat-timing):
  Session    : [sessionName] · [location]
  dateStart  : [dateStart] ([X]h ago)
  Fetched by : [auto / admin-fetch]
  Drivers    : [N]

NEXT RACE:
  Name       : [race name]
  Sprint?    : [Yes / No]
  fp1Date    : [datetime] ([in X hours / X hours ago])
  qualTime   : [datetime] ([in X hours])
  raceTime   : [datetime]

STATE EVALUATION:
  hoursSinceStored    : [X]h  [>6? ✅/❌]
  nextQualifyingFuture: [✅/❌]
  fp1AlreadyStarted   : [✅/❌]
  storedPreDatesFP1   : [✅/❌]

  isBetweenRaces        = [TRUE / FALSE]  — expected display: [between-races panel]
  isWaitingForNewSession= [TRUE / FALSE]  — expected display: [amber waiting panel]
  Active session        = [TRUE / FALSE]  — expected display: [leaderboard tabs]

VERDICT:
  PubChat should be showing: [leaderboard / between-races / waiting for data]
  Actual issue (if any)    : [description]
  Fix                      : [trigger refresh via /api/live/refresh-timing / wait for OpenF1 to update / code change needed]
```

---

## Common issues and fixes

**OpenF1 still returning previous race after it ended:**
Normal — `session_key=latest` lags until the next session starts. Nothing to fix.
PubChat correctly detects `fp1AlreadyStarted` and switches to `isWaitingForNewSession`.

**PubChat stuck showing "between races" when it shouldn't be:**
Check `fp1Date` formula. Sprint weekends: `qualifyingTime - 4h` (same day). Normal: `qualifyingTime - 24h`.
If `fp1AlreadyStarted = true` but `isBetweenRaces` is still true — the `fp1AlreadyStarted` check is missing or broken in `LiveTimingClient.tsx`.

**PubChat showing "waiting for data" when FP1 hasn't started:**
`fp1Date` is too early. Likely the sprint formula used `- 2d` instead of `- 4h`.
Fix: check `getNextTracksideLabel()` and `getUpcomingSessions()` in `LiveTimingClient.tsx`.

**Firestore `pub-chat-timing` is very stale (days old):**
The refresh cron or auto-refresh is not running. Check Cloud Functions (`refreshHotNews`),
or trigger manually via the admin Fetch Timing button, or POST to `/api/live/refresh-timing`.

**`session_key=latest` returns a session from a different year:**
OpenF1 API issue — rare. Check `s.year` field. If wrong year, the session schedule comparison
will break. Report as OpenF1 upstream issue; no code fix needed.
