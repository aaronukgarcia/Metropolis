---
description: Root cause analysis for a reported bug — trace the full request path, find the first broken link, produce a clear report. Works for any "X is broken" user report.
allowed-tools: Bash(grep:*), Bash(node:*), Read, Glob
---

## Context

- ARGUMENTS: $ARGUMENTS  ← the reported symptom (e.g. "PIN reset emails not arriving", "scores not saving")
- Source root: `app/src/`
- Key config files: `firebase.json`, `app/apphosting.yaml`, `functions/index.js`

---

## What is this skill?

This is the exact methodology used to find and confirm BUG-PIN-001 (2026-03-04) — every PIN reset email silently lost since the feature was built — in under 10 minutes from a vague user report ("pins can not be reset"). The same path works for any "X is not working" report where no error is visible to the user.

**The core insight:** When something produces no error and no result, the bug is almost always a *consumer that doesn't exist* — a write going to a dead queue, a call going to an unconfigured service, or a silent catch that returns success regardless. You find it by tracing the data path from the user action all the way to the final destination and asking "does the thing that receives this actually exist?"

---

## STEP 0 — Route the symptom (WRONG SKILL = WRONG BUG)

This skill traces **data paths** and finds **missing consumers**. It is the wrong tool for
"X looks wrong / is missing ON A PAGE". Route first:

| Symptom shape | Skill |
|---|---|
| "No result and no error" after an action (email, write, message) | **/rca** (this skill) |
| "The page shows X but it should show Y" / "X is missing from the page" | **/diagnose** — and get a SCREENSHOT first |

**The evidence hierarchy (2026-07-06 lesson — the "missing" British GP):** if your server-side
checks say the data is present but the user says the screen is wrong, BOTH are true — the bug is
in the presentation layer (rendering, labelling, column headers). Do NOT use a passing data check
to overrule what the user sees; ask for a screenshot and reconcile the two. That episode burned
three deploys (cache theory ×2, then the actual fix: the GP column was labelled "R9" not "GP").
One screenshot would have ended it in the first reply.

---

## STEP 1 — Anchor to the symptom

Turn the user report into a concrete action:

> "PIN reset emails not arriving" → user clicks "Send Reset Email" → something should send an email.

Write it as: **`[user action] → [expected side effect]`**

If the symptom is vague, ask one question: *"What does the user do, and what are they expecting to happen?"* Do not start investigating until you can write that sentence. If the expected side effect is **something visible on a page**, also ask for a screenshot — then go to /diagnose.

**If the report is a PX-9001 (uncaught client error) from error_logs: do NOT trust the `route` field.** It names where the error *boundary rendered*, not where the fault lives — an exception thrown during a route transition is attributed to the *destination* page. BUG-WELCOME-001 (2026-07-29) is the canonical trap: "route: /welcome" pointed at an innocent page; the stack showed `ee.destroy` — the Pit Wall's Pixi teardown crashing as the redirect unmounted it mid-init. Anchor to the **stack trace**, not the route. A minified frame like `xx.destroy`/`xx.remove` during navigation means: look at what was being *unmounted*, not what was being mounted.

---

## STEP 2 — Find the handler

Search for the route, function, or component that owns the user action:

```bash
grep -rn "reset.pin\|pin.reset\|forgot.pin" app/src --include="*.ts" --include="*.tsx" -i -l
```

Adapt the search terms to the symptom. Use `-l` first (files only) then read the ones that look like the entry point (API routes first, then server actions, then client components).

**Rule:** Start with the server-side handler. Client components usually delegate. The bug is almost always on the server.

---

## STEP 3 — Read the handler end-to-end

Read the entire route file. Do not skip. Look for the moment it produces the expected side effect — the email send, the Firestore write, the external call.

While reading, answer these questions at every significant step:

| Question | Why it matters |
|----------|---------------|
| What does this write to / call? | Identifies the downstream target |
| Does the response depend on whether that worked? | Silent success is the classic failure mode |
| Is there a catch block that swallows errors? | Golden Rule #1 violation = invisible failures |
| Does this require an external service or extension? | Services can be unconfigured or absent |

---

## STEP 4 — Verify every downstream consumer

For **each Firestore collection write** in the handler, ask: *who reads this collection and acts on it?*

Check in this order:

**A — Firebase Extensions:**
```bash
cat firebase.json | grep -i "extension\|trigger\|smtp\|mail"
```
If nothing comes back, no Firebase extensions are installed. Any `mail` collection write is a dead queue.

**B — Cloud Functions triggers:**
```bash
grep -n "onDocumentCreated\|onDocumentWritten\|onChange\|collection('mail')" functions/index.js
```
If no function listens to the collection, nothing consumes it.

**C — Scheduled cron routes:**
```bash
grep -rn "collection_name" app/src/app/api/cron --include="*.ts" -l
```

**For each external API call**, check the environment variables it depends on are present in `apphosting.yaml`:
```bash
grep "VARIABLE_NAME" app/apphosting.yaml
```

---

## STEP 5 — Identify the collection or call that has no consumer

This is the bug. State it precisely:

> `reset-pin/route.ts` writes to the `mail` collection (line 178).
> `firebase.json` has no extensions block — Firebase Trigger Email is not installed.
> `functions/index.js` has no trigger on `mail`.
> **Consumer does not exist. Every write is silently dropped.**

The pattern is always the same:
- Code writes/calls something ✅
- Code returns success ✅ (it wrote successfully — to a dead queue)
- Nothing on the other end reads it ❌
- User sees no result, no error

---

## STEP 6 — Confirm with a secondary check (the "why did no one notice?" question)

Ask: *why didn't this surface in logs?*

```bash
# Check for any error handling that would have caught a failure
grep -n "catch\|logError\|logTracedError" app/src/app/api/YOUR_ROUTE/route.ts
```

If the route catches all errors but the *write itself succeeded* (to a dead collection), there is nothing to catch. The route is correct in isolation. The bug is architectural — a dependency on infrastructure that was never deployed.

This is why `/silent-failures` won't catch it. The code has no silent failure — the code is working. The *system* has a gap.

---

## STEP 7 — Produce the bug report

State clearly:

1. **What the user sees:** no result, no error
2. **What the code does:** succeeds at writing to X
3. **What X does with it:** nothing — no consumer
4. **How long this has been broken:** since the feature was committed (check git log for the file)
5. **Why it was invisible:** route returns success because writing to Firestore succeeded
6. **Fix direction:** either build the consumer, or bypass the queue and call the real service directly

```
bill> 🔴 RCA: [Symptom]

Root cause: [file:line] writes to [collection/service] which has no consumer.
  - [Evidence A: firebase.json check]
  - [Evidence B: functions/index.js check]

Duration: Broken since [commit hash / date] — [N days/weeks]
Visibility: Zero — route returns success on every call regardless of delivery

Fix: [one sentence — bypass the dead queue OR build the consumer]
```

---

## The PIN reset case study (BUG-PIN-001, 2026-03-04)

This is the exact path that found the bug:

**Report:** "pins can not be reset" (3 words, no error code, no log)

**Step 1:** User clicks "Send Reset Email" → should receive an email with a 6-digit PIN.

**Step 2:**
```bash
grep -rn "reset.pin" app/src -i -l
# → app/src/app/api/auth/reset-pin/route.ts
```

**Step 3:** Read `reset-pin/route.ts` top to bottom. Found at line 178:
```ts
await db.collection('mail').add({
  to: normalizedEmail,
  message: { subject: mailSubject, html: mailHtml },
});
```
Question: *who reads the `mail` collection?*

**Step 4:**
```bash
cat firebase.json | grep -i "extension\|mail"
# → (no output)

grep -n "collection('mail')" functions/index.js
# → (no output)
```
No extension. No function. Dead queue confirmed.

**Step 5:** Consumer does not exist. Every PIN reset since day one wrote a doc that sat in Firestore forever.

**Step 6:** Route returns `success: true` because `db.collection('mail').add()` succeeded. Firestore happily accepted the write. Nothing to catch.

**Time to root cause: ~8 minutes.**

**Fix:** Replaced `mail.add()` with `sendEmail()` — the same Graph API call used by every other email in the app. 3 files changed. Build deployed as v2.0.6.

---

## The SECOND failure class: the abandoned producer (BUG-ROAST-001, 2026-07-20)

The missing-consumer pattern (above) is not the only way to get "no result, no error". The mirror image exists: **the producer itself silently never ran to completion** — because it was post-response fire-and-forget work on Cloud Run.

**Report:** "LREG submitted, my second team submitted, only the second got a WhatsApp alert."

**How it differs from missing-consumer:** every consumer existed and worked (the queue, the worker, the group). The write that feeds them *never happened* — and, critically, **no catch block fired either**, because the code didn't fail; Cloud Run throttled CPU to ~zero after the HTTP response and the orphaned async block simply froze mid-await and stopped being scheduled.

**The tell-tale signature (memorise this):**
1. Expected side-effect missing (no queue doc at all — not PENDING, not FAILED, absent)
2. **Zero error lines** even though every failure path in the block logs — search proved the block never errored
3. Request log shows a clean 200 in ~1s
4. **Intermittent, correlated with traffic**: works when the instance is busy (other requests keep CPU alive), loses on quiet instances. Check `httpRequest` logs: same instance, and note whether a *later* request "revived" a stalled pipeline (the Billceleration roast took 81s response→queue-write, un-frozen by an unrelated cron hit).

**Diagnostic steps that worked:**
```bash
# request logs: status, latency, instance — were both requests healthy?
gcloud logging read 'resource.type="cloud_run_revision" AND httpRequest.requestUrl:"THE_ROUTE"' --freshness=3h --format=json
# then prove the block never errored (its failure modes all console.error):
gcloud logging read 'resource.type="cloud_run_revision" AND textPayload:"non-fatal"' --freshness=3h
```
⚠️ Structured `console.log(JSON.stringify(...))` lands in **jsonPayload, not textPayload** — grep BOTH or you'll get false negatives. And run gcloud from PowerShell with the filter in a single-quoted string; Git Bash mangles filters containing colons.

**Fix pattern (v3.7.2):** await fast work (<300ms Firestore ops) before the response; for slow work (AI, aggregates), awaited-write a durable task doc + `onDocumentCreated` trigger + internal CRON_SECRET route so the pipeline runs inside a real request with full CPU and a visible `PENDING→PROCESSING→DONE/ERROR` status.

**Prevention grep** when reviewing any route:
```bash
grep -rn "(async () => {" app/src/app/api --include="*.ts" -A2 | grep -B2 "})();"
```
Any unawaited IIFE doing more than milliseconds of work before the route returns is this bug waiting for a quiet Monday.

---

## For Garth — how to create your first skill with Bertie

A skill is a markdown file in `.claude/commands/`. When you type `/skill-name` in a Claude Code session, the file is loaded and Bertie follows it as a set of instructions.

**To create this skill (or any skill):**

1. Open a Claude Code session in the project directory
2. Type: `write me a skill called rca that...` and describe what you want it to do
3. Bertie will write the file to `.claude/commands/rca.md`
4. From that point on, type `/rca` in any session and Bertie runs the RCA playbook

**Good skills have three things:**

| Thing | Why |
|-------|-----|
| A concrete trigger — what the user types/says | So Bertie knows when to use it |
| Step-by-step instructions, not goals | "grep for X, then read Y" beats "investigate the issue" |
| A case study or worked example | Shows Bertie exactly what "done" looks like |

**Skills worth building next:**

- `/trace-email` — trace any email type through the full stack: provider call → API route → sendEmail → email_logs. Answers "did this email go out and why?"
- `/check-collection` — given a Firestore collection name, find every reader and writer in the codebase and every trigger in functions/index.js
- `/new-route` — checklist for adding a new API route: auth guard, CSRF if needed, error logging, GUID, apphosting.yaml secrets check

The best skills capture something you've just had to figure out the hard way. Write the skill immediately after you solve the problem — before you forget the path.
