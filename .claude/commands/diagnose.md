---
description: Deep diagnostic for visual/rendering bugs — find the divergence point, trace data top-down, confirm root cause before touching code. Born from the 2026-03-24 replay-no-cars incident (6 wrong pushes).
allowed-tools: Bash(grep:*), Bash(find:*), Bash(node:*), Read, Glob, Grep, Agent
---

## Context

- ARGUMENTS: $ARGUMENTS  ← the reported symptom (e.g. "replay no cars", "zoom 2 blank", "scores not updating")
- Source root: `app/src/`
- This skill exists because on 2026-03-24, six consecutive speculative fixes were pushed to main for a "no cars in replay" bug. Each was wrong. The actual root cause — a useRef mutated inside useMemo — was found by working top-down from the data pipeline, not bottom-up from the rendering layer.

---

## RULE ZERO — DO NOT WRITE CODE YET

This skill is a DIAGNOSTIC. You are not allowed to edit, write, or commit any files until you reach Step 7 and the user confirms the root cause. If you feel the urge to "just try a quick fix", stop. That impulse is the exact failure mode this skill exists to prevent.

---

## STEP 1 — Search for existing analysis

Before you investigate ANYTHING, check if someone already diagnosed this:

```bash
# Search for RCA, bug, analysis, or postmortem docs
find . -iname "*bug*" -o -iname "*rca*" -o -iname "*root-cause*" -o -iname "*analysis*" -o -iname "*postmortem*" | grep -v node_modules | grep -v .git
```

Also check:
- `docs/` folder for any recently added files
- `memory/` folder for project memories about this area
- Git log for recent commits mentioning the symptom keywords

If you find an existing analysis, **READ IT FIRST**. It may already contain the answer. Report it to the user before proceeding.

---

## STEP 2 — Write the divergence statement

Something works. Something doesn't. Write both:

> **WORKS:** [what renders / what data is correct]
> **BROKEN:** [what doesn't render / what data is wrong]

Example from the replay bug:
> **WORKS:** Track outline renders. Race table shows driver data (P1: ANT). Replay progress bar advances.
> **BROKEN:** Zero car dots on the track map at any zoom level.

The divergence point — where the working path and the broken path split — is where the bug lives. Everything before the split is innocent. Do NOT investigate it.

---

## STEP 3 — Map the data path (not the rendering path)

Starting from the data source, trace forward through every transformation until the visual output. Write it as a chain:

```
[Data Source] → [Transform 1] → [Transform 2] → ... → [Visual Output]
```

For the replay bug, this was:
```
replayPlayer.replayDrivers (React state)
  → castReplayToLive() (type cast)
  → activeDrivers (useMemo)
  → PitWallTrackMap props
  → PixiTrackApp.setData()
  → InterpolationSystem.onDriversUpdate(drivers, virtualTimeDeltaMs)
  → InterpolationSystem.interpolate()
  → carLayer.update(interpolated, bounds, w, h)
  → projectToCanvas() → sprite.position.set(px, py)
```

Now mark the divergence point from Step 2. The table reads `activeDrivers` directly. The track map reads via `InterpolationSystem`. The divergence is at `InterpolationSystem`.

**CRITICAL:** Do not start from the rendering layer and work backwards. Start from the data and work forwards. The rendering layer is almost never the bug — it's a dumb projector. The data pipeline is where logic, filters, and transformations can silently kill the signal.

---

## STEP 4 — Read the code at the divergence point

Read the function/system at the divergence point end-to-end. For every conditional, filter, or early return, ask:

| Question | What it catches |
|----------|----------------|
| What value does this receive at runtime? | Upstream computation bugs (useMemo, useCallback) |
| What does it do when the value is null/undefined/0? | Fallback paths that silently change behaviour |
| Does this filter/reject data? Under what conditions? | Spike filters, validation, dedup logic |
| Is this called inside a render phase? Does it mutate state? | React 18 double-render anti-patterns |
| Does this depend on timing (Date.now, intervals, RAF)? | Wall time vs virtual time mismatches |

Do NOT skim. Read every line. The bug is often a single expression that evaluates differently than you'd expect.

---

## STEP 5 — Trace the suspicious value upstream

By now you should have a hypothesis about which value is wrong. Trace it backwards to where it's computed:

```
visual output ← interpolate() ← onDriversUpdate(virtualTimeDeltaMs) ← setData(opts) ← React prop ← useMemo ← ???
```

Read the computation. Check for:

- **React anti-patterns:** Ref mutations in useMemo/render phase. React 18 double-invokes these.
- **Stale closures:** useCallback/useMemo with missing dependencies capturing old values.
- **Reference equality traps:** `opts.drivers !== this.drivers` fails when React returns the same memoized reference.
- **Type coercion:** `null` vs `undefined` vs `0` behaving differently in `!=` vs `!==` checks.
- **Timing assumptions:** Wall clock vs virtual clock vs RAF timestamp.

---

## STEP 6 — Prove the hypothesis WITHOUT writing code

You must be able to explain the full causal chain:

```
[Root cause at line X] → [produces wrong value Y] → [downstream system Z sees Y] → [Z behaves incorrectly] → [symptom the user sees]
```

Every link must be airtight. If you can't explain one link, your hypothesis is incomplete — keep digging.

**Validation checklist:**
- [ ] Does this explain ALL symptoms (not just some)?
- [ ] Does this explain why it USED to work (what changed)?
- [ ] Does this explain the divergence (why X works but Y doesn't)?
- [ ] Can you predict what a console.log at the divergence point would show?

If all four are yes, present the RCA to the user.

---

## STEP 7 — Present the RCA and get confirmation

```
bill> 🔍 DIAGNOSIS: [Symptom]

Divergence: [what works] vs [what doesn't]
Split point: [where the paths diverge]

Root cause: [file:line] — [one sentence description]

Causal chain:
  1. [Root cause produces wrong value]
  2. [Downstream system receives wrong value]
  3. [System behaves incorrectly because of wrong value]
  4. [User sees symptom]

Why it used to work: [what changed — commit, version, React upgrade, etc.]

Proposed fix: [one sentence — what to change and why]
```

**Wait for user confirmation before writing any code.**

---

## STEP 8 — Fix, verify, push (ONE commit)

Only after confirmation:
1. Write the fix
2. `npm run build` — must compile clean
3. Explain to the user exactly what the fix changes
4. Commit and push — ONE commit, ONE build

---

## Headless browser probes against prod (recipes proven 2026-07-29, BUG-WELCOME-001)

When the Chrome extension is unavailable, Puppeteer + system Chrome
(`executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe'` — the puppeteer cache
has no browser installed) can drive the real deployed app. Three recipes that took real debugging
to get right — do not rediscover them:

**Signing in headlessly:** mint a custom token for `billceleration-bot` (Admin SDK, modular API),
then in the page: `page.setBypassCSP(true)`, inject the Firebase *compat* SDK from gstatic, and
`initializeApp(config)` **as the DEFAULT app — no name argument**. A named app persists auth under
a different indexedDB key and the site's own SDK never sees the session (you bounce to /login and
it looks like the token failed). Config values are the NEXT_PUBLIC_* entries in apphosting.yaml.
The bot has `lateJoinerAcknowledged: true` so it cannot land on /welcome.

**Reproducing unmount bugs:** `page.goto()` **cannot do it** — a goto is a full browser navigation
that discards the page wholesale; React unmount effects never run, so teardown-path bugs are
invisible and a "verification" passes vacuously. Trigger CLIENT-SIDE navigation instead: stay
inside the SPA and `click()` real sidebar `<a>` elements. First run of the BUG-WELCOME-001 verify
used goto and "proved" the unfixed code safe — worthless.

**Widening race windows:** `Emulation.setCPUThrottlingRate` via a CDP session (`rate: 4`) stretches
async init so short dwells reliably land mid-initialisation on a headless, GPU-less Chrome.

**Closing the loop:** assert BOTH sides — `page.on('pageerror')` count is zero AND no new rows in
`error_logs` (query `timestamp >= run start`) with the relevant code. Client silence alone can just
mean your probe never exercised the path — which is exactly what the goto mistake looked like.

**Clickable-text traps:** match nav links by exact intent, not loose regex — `/start|play/i` once
matched the sidebar's "Getting Started" link and navigated the probe away from the page under test.

---

## The replay-no-cars case study (2026-03-24)

**Report:** "no cars in replay mode" (screenshot showed track outline but zero car dots)

**Step 1:** No existing RCA docs found (at the time — `replay-bug.md` was created later by Gemini).

**Step 2:**
> WORKS: Track outline, race table, replay progress bar, weather data
> BROKEN: Car dots — zero visible at any zoom level

**Step 3:** Data path:
```
replayPlayer → activeDrivers → PitWallTrackMap → PixiTrackApp.setData()
  → InterpolationSystem.onDriversUpdate(drivers, virtualTimeDeltaMs)
  → InterpolationSystem.interpolate() → carLayer.update()
```
Table reads `activeDrivers` directly (works). Track map reads via `InterpolationSystem` (broken). **Divergence: InterpolationSystem.**

**Step 4:** Read InterpolationSystem.onDriversUpdate(). Found the spike filter:
```ts
const timeDeltaS = virtualTimeDeltaMs != null
  ? Math.max(0.1, virtualTimeDeltaMs / 1000)
  : Math.max(0.1, (now - this.snapshotTimestamp) / 1000);
const maxDist = MAX_PLAUSIBLE_SPEED_MPS * timeDeltaS + GPS_JITTER_MARGIN_M;
```
If `virtualTimeDeltaMs` is undefined, falls back to wall time. At 4x replay speed, wall time delta is ~60ms → maxDist ≈ 30m. Actual car movement ≈ 45m. Every update rejected as spike. Cars frozen.

**Step 5:** Traced `virtualTimeDeltaMs` upstream to PitWallClient.tsx:
```ts
const virtualTimeDeltaMs = useMemo(() => {
  const delta = currentElapsed - prevReplayElapsedMsRef.current;
  prevReplayElapsedMsRef.current = currentElapsed; // ❌ ref mutation in render phase
  return delta > 0 ? delta : undefined;
}, [isReplayMode, replayPlayer.elapsedMs]);
```
React 18 double-invokes useMemo. First pass: delta=500, mutates ref. Second pass: delta=0, returns undefined.

**Step 6:** Causal chain:
1. useMemo mutates ref → React double-render → delta=0 → returns undefined
2. InterpolationSystem receives undefined → falls back to wall time
3. Wall time delta too small → spike filter rejects all position updates
4. All 20 cars permanently frozen at spawn

Explains ALL symptoms: track draws (bypasses interpolation), table works (reads raw state), cars frozen (interpolation rejects everything).

**Step 7:** Presented RCA. Confirmed.

**Step 8:** Split useMemo (read-only) + useEffect (writes ref post-commit). One commit. One build. Fixed.

**What went wrong the first 6 times:** Started from the rendering layer (bloom filter, z-ordering, coordinate projection) instead of the data pipeline. Each wrong fix was a guess that didn't explain all symptoms. Never discarded the wrong hypothesis — kept patching it.

---

## Backend / infra / dependency variant (added 2026-06-12 after the WhatsApp + PX-3101 circling)

RULE ZERO still applies. The data-path framing maps onto backend bugs too — but a few extra heuristics catch the failure modes that wasted a whole session of redeploy-and-hope:

1. **A green health endpoint does NOT prove health.** Health/status endpoints are often *unauthenticated* (e.g. the WhatsApp worker's `/health` skips the HMAC check). A 200 there proves the service is *up*, not that *auth/the secret/the data path* works. **Always exercise the actual authenticated/affected path** (`/status`, not `/health`).

2. **Prove the root cause at the SOURCE, not by redeploying.** Before changing config or pushing: reproduce the dependency's behaviour directly. Examples that ended the circling — compute the expected `HMAC(secret, payload)` yourself and call the dependency directly (proved the worker was fine, the app's secret was stale); run the library in isolation to capture the *real* error string (`GenkitError: Unknown action type…` revealed a version skew, not the protobufjs vuln I'd assumed). "Redeploy and see" is not diagnosis.

3. **When a dependency change breaks something, suspect VERSION SKEW across a coupled family first.** `npm audit fix` bumped `genkit`/`@genkit-ai/core` to 1.37 but left the `@genkit-ai/google-genai` plugin at 1.33 → all AI broke. Plugin/runtime pairs must match. The prod build skips type validation, so re-test the affected feature after *any* dep change.

4. **All-identical-but-still-wrong → suspect a stale binding/cache.** Every stored copy of `WHATSAPP_APP_SECRET` matched, yet the running app signed with a different value: App Hosting served a stale secret across deploys. When the stored truth is consistent but runtime disagrees, the bug is in *resolution/caching*, not the value.

5. **Don't trust your own "fix verified" if you didn't test the real path.** I called an `npm audit fix` "safe" without re-testing AI and reintroduced PX-3101. Verify the user-facing behaviour, not just the build.
