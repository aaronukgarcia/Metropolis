# /bot-status — Billceleration autonomous team status

One-shot health + activity view of the Billceleration AI team (v3.7.0+). Shows the kill switch, the GR#17 heartbeat, slot/dedup state, the last picks with their rationale, recent decision log, and the next scheduled slots. Read-only — never writes anything.

## Context

- The bot's runtime SSOT: `admin_configuration/billceleration` `{ uid, enabled, disclosureSentAt }` — `enabled` is the kill switch (10-min cache in the app; flips take effect without deploy).
- Heartbeat: `admin_configuration/billcelerationStatus` `{ lastRunAt, state, detail, correlationId }` — written on EVERY tick including no-ops. The tick fires at :10 :25 :40 :55 Europe/London (`billcelerationTick`). **Older than 2h = the tick chain is dead** (function, route, or scheduler) — same bound as /health-check CHECK 11.
- Slot/dedup state: `admin_configuration/billcelerationState` `{ lastDailyDate, finalDone: {raceKey: true}, lastPick }`.
- Decision history: `billceleration_log` (raceId, mode daily/final, session gp/sprint, picks, rationale, selfDoubt, fallbackUsed).
- Slots: daily 06:55–07:25 London on Sun/Thu/Fri/Sat (hot-news days, in a race week); final call in the last 60 min before `qualifyingTime` (race_schedule collection).

## Your task

Run this script from the repo root (`E:\git\prix6\03.Current`) with plain node:

```javascript
const { initializeApp, getApps, cert } = require('firebase-admin/app');
const { getFirestore } = require('firebase-admin/firestore');
const path = require('path');
if (!getApps().length) initializeApp({ credential: cert(path.resolve('service-account.json')) });
const db = getFirestore();

(async () => {
  const [cfg, status, state] = await Promise.all([
    db.collection('admin_configuration').doc('billceleration').get(),
    db.collection('admin_configuration').doc('billcelerationStatus').get(),
    db.collection('admin_configuration').doc('billcelerationState').get(),
  ]);
  const c = cfg.data() || {}, s = status.data() || {}, st = state.data() || {};

  console.log('=== CONFIG ===');
  console.log('enabled:', c.enabled === true ? '✅ ON' : '🛑 OFF (kill switch)', '| uid:', c.uid || 'NOT PROVISIONED');

  console.log('\n=== HEARTBEAT (GR#17) ===');
  if (!s.lastRunAt) { console.log('❌ NO STATUS DOC — tick has never fired'); }
  else {
    const ageMin = (Date.now() - new Date(s.lastRunAt).getTime()) / 60000;
    console.log(ageMin < 120 ? '✅' : '❌ STALE', 'last tick', ageMin.toFixed(0) + 'min ago |', s.state, '|', s.detail);
    if (s.state === 'error') console.log('⚠️ last run ERRORED — correlationId', s.correlationId);
  }

  console.log('\n=== SLOT STATE ===');
  console.log('lastDailyDate:', st.lastDailyDate || '(never)', '| finalDone:', JSON.stringify(st.finalDone || {}));
  if (st.lastPick) {
    console.log('lastPick:', st.lastPick.raceId, '(' + st.lastPick.mode + ')', '→', (st.lastPick.picks || []).join(', '));
  }

  console.log('\n=== RECENT DECISIONS (billceleration_log) ===');
  const log = await db.collection('billceleration_log').orderBy('at', 'desc').limit(5).get();
  log.docs.forEach((d) => {
    const x = d.data();
    console.log('-', x.at?.toDate?.()?.toISOString?.(), '|', x.raceId, x.mode + '/' + x.session,
      x.fallbackUsed ? '⚠️ fallback:' + x.fallbackUsed : '✅ picker',
      '| P1-P6:', (x.picks || []).join(','));
    console.log('  rationale:', (x.rationale || '').slice(0, 140));
    console.log('  selfDoubt:', (x.selfDoubt || '').slice(0, 140));
  });
  if (log.empty) console.log('(no entries yet)');

  console.log('\n=== NEXT SLOTS ===');
  const sched = await db.collection('race_schedule').get();
  const now = Date.now();
  const next = sched.docs.map((d) => d.data())
    .filter((r) => r.qualifyingTime && new Date(r.qualifyingTime).getTime() > now)
    .sort((a, b) => new Date(a.qualifyingTime).getTime() - new Date(b.qualifyingTime).getTime())[0];
  if (!next) { console.log('no upcoming race — off-season'); }
  else {
    const dl = new Date(next.qualifyingTime).getTime();
    console.log('next race:', next.name, '| quali closes:', next.qualifyingTime, '(' + ((dl - now) / 3600000).toFixed(1) + 'h)');
    console.log('final-call window:', new Date(dl - 3600000).toISOString(), '→', next.qualifyingTime);
    // Next daily slot: next Sun/Thu/Fri/Sat at 06:55 London (only fires if a race is within 7 days).
    // Skip today if the 06:55-07:25 window has already passed (London wall-clock).
    const fmt = new Intl.DateTimeFormat('en-GB', { timeZone: 'Europe/London', weekday: 'short' });
    const lonMins = (() => { const p = new Intl.DateTimeFormat('en-GB', { timeZone: 'Europe/London', hour: 'numeric', minute: 'numeric', hour12: false }).formatToParts(new Date(now)); const g = (t) => Number(p.find((x) => x.type === t)?.value || 0); return g('hour') * 60 + g('minute'); })();
    for (let d = lonMins >= 7 * 60 + 25 ? 1 : 0; d <= 7; d++) {
      const day = new Date(now + d * 86400000);
      if (['Sun', 'Thu', 'Fri', 'Sat'].includes(fmt.format(day))) {
        console.log('next daily slot: ' + fmt.format(day), day.toISOString().slice(0, 10), '06:55 Europe/London',
          dl - day.getTime() <= 7 * 86400000 ? '(race in range — will fire)' : '(race out of 7-day range — will no-op)');
        break;
      }
    }
  }
  process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
```

Then report with the identity prefix, leading with the health verdict:

```
bill> 🤖 Billceleration status
     Kill switch: ✅ ON | Heartbeat: ✅ Nmin ago (state) | Last decision: <race> <mode> [picker|fallback]
     Next: daily slot <day> 06:55 London / final call <window>
```

## Escalation

- Heartbeat ❌ stale (>2h) → check `gcloud functions logs read billcelerationTick --gen2 --project=studio-6033436327-281b1 --region=europe-west2 --limit=10` (silent-OOM diagnostic per /health-check CHECK 11). Remember structured logs land in **jsonPayload**, not textPayload.
- `state: error` repeatedly → read `billcelerationStatus.detail` + the matching `billceleration_log` entry; the slot claim auto-releases so the next in-window tick retries.
- Frequent `fallbackUsed` → the Gemini picker is failing validation; run `npx tsx --tsconfig app/tsconfig.scripts.json app/scripts/test-billceleration.ts --dry` from repo root to see raw output.
- Standings rank: `GOOGLE_APPLICATION_CREDENTIALS=<service-account.json> npx tsx --tsconfig app/tsconfig.scripts.json app/scripts/check-bot-standings.ts`.
- Emergency stop: set `admin_configuration/billceleration.enabled = false` (takes ≤10 min, no deploy).
```
