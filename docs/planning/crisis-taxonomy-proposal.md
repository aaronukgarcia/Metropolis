BOW codes: FEAT-013, ASM-276, ASM-277

# Crisis Taxonomy Proposal — candidate list for Aaron's approval

**Author:** BA-Crisis (Ben's team)
**Date:** 2026-08-11
**Decides:** which engine-side conditions carry the crisis tag that `ui.alerts` (FEAT-013) uses to auto-pause (per ASM-277). This is a **proposal**, not a spec. Aaron approves or cuts rows; nothing here is binding until he does.
**Consumed by:** whichever BA/engineer next carries the crisis tag into `int.protocol`'s `Event`/`Delta` schema (ASM-276) and the owning engine systems (`engine.spiral`, `engine.finance`, `engine.defence`/police, `engine.weather`/disasters — module names approximate, several don't exist yet).
**Does not edit:** `docs/planning/acceptance/ui.alerts.md` (owned by another BA) — this proposal is written to be consistent with its ACs (cited throughout), not to duplicate or override them.

---

## 1. What "crisis" means here (the test every row is held to)

Per `ui.alerts.md` AC-6, crisis ≠ priority tier. A P0 alert ("Loan payment due") must not auto-pause; that is the spec's own example of the failure mode this document exists to prevent.

The working definition I've used to sort every candidate below:

> **Crisis = the player would materially change what they do in the next few seconds if told right now, and the cost of not telling them right now is irreversible or compounds fast enough that catching it next tick is meaningfully worse than catching it this tick.**

Two spec pillars sharpen this:

- **Pillar 3, "Squeezed, never ambushed"** (§1): "the projection engine means the player should almost always be able to see trouble coming... difficulty comes from competing demands... not from surprises." Anything the projection system (F7, the alert stack's own lead-time alerts) already surfaces with lead time is, by the spec's own design intent, **not** a crisis — it's exactly the kind of thing projections exist to convert from ambush into ordinary planning pressure. This is why "Water deficit in 3 months" is excluded below even though water running out is obviously bad.
- **Pillar 6, "Both directions on every dial"** (§1, §12): the Detroit spiral is explicitly **not** a scripted, discrete failure — it's a chain of continuous, reversible-until-the-end stages. This argues against treating any *intermediate* spiral stage as a crisis (see §4 below on the slow-collapse boundary question).

---

## 2. Candidate list

Each row: condition · spec basis · why crisis not merely urgent · cost if it fires wrongly · cost if it fails to fire.

### INCLUDE

| # | Condition | Spec basis | Why crisis, not merely urgent | Cost if fires wrongly (false positive) | Cost if it fails to fire (false negative) |
|---|---|---|---|---|---|
| C1 | **Insolvency game-over trigger** — the 3rd consecutive month of unmet obligations with no available credit (the exact moment `engine.finance`'s `IsInsolvent` fires, per `engine.finance.md` AC-7) | §7 line 210, §12 line 240 ("Death conditions: insolvency"). Spec-grounded, exact. | This is not a warning about insolvency — it's the death condition *actually resolving*. There is nothing left to plan around; the game state itself has changed. Letting the sim run on at 4× past this point is meaningless. | None realistically — this event can only fire once per save (it's terminal), so "wrongly" reduces to "fired at the wrong month," which is an engine bug, not a taxonomy problem. | Player doesn't realize the game has ended; sim keeps advancing post-mortem, confusing history log/epilogue generation (`engine.spiral.md` AC-8 needs a clean trigger moment). |
| C2 | **Ghost-city death-condition trigger** — population crosses below 10% of historic peak, having once exceeded 50,000 (`engine.spiral.md` AC-7's exact two-clause check) | §12 line 240, verbatim. | Same shape as C1: not a warning, the terminal condition itself. The chain that led here (spiral stages) is explicitly *not* crisis-worthy (see §4) — only the final threshold crossing is. | Same as C1 — terminal, fires once. | Player misses the epilogue moment; sim keeps running a "dead" city, and the ghost-city ending (AC-8) has no clean point to anchor the epilogue's generated narrative to. |
| C3 | **Water reserve stockout** — the sim-tick moment stored water stock actually hits zero and a consumption pass has nothing to draw from (distinct from "Water deficit in 3 **months**," which is a projection) | BA-proposed. §6 (aquifer/reservoir stock mechanics) + §8 (JIT shortfall mechanics: "Shortfalls hit consumption → satisfaction, health") give the mechanism; the spec never names this exact moment as crisis-worthy. **No spec basis for the auto-pause tag itself.** | The projection alert ("in 3 months") is exactly what Pillar 3 says should prevent ambush — but the actual stockout, when it happens, is citizens going without water *this tick*, with real health consequences starting immediately (§8). That's a different event from the projection, and the projection having fired earlier doesn't make the arrival non-urgent — plenty of players will have seen the warning and still not fixed it in time, which is precisely when an interrupt earns its keep. | Player who is mid-crisis-response for something else gets yanked away for a shortage that's already priced into consumption/satisfaction math and doesn't need a same-tick decision to arrest (the damage this tick has already happened by the time the tag fires). | Player doesn't notice water is actually gone (as opposed to "projected to run low") until satisfaction/health has been eroding silently for weeks — the "invisible for months, then illness" shape §25 explicitly calls the Detroit on-ramp. |
| C4 | **Successful terror attack event** (post-threat-level-intel) | §28 line 424: "a successful attack is a reputation and mental-health shock — rare, never random-spam, always preceded by visible threat-level intel," plus §3's generic "Crisis events auto-pause" clause. **Spec names the event and its shock character but never says "auto-pause."** Weakest spec grounding of the include list. | Rare by design (spec says so explicitly), discrete, sudden, and carries an immediate reputation/mental-health shock the player would want to see and potentially respond to (funding, liaison policy) right away — the opposite of a slow-burn stat. | Fires at most a handful of times across an entire 80–150 hour run (spec: "rare"), so even a wrong call here is cheap in aggregate — but if it *does* fire on something less severe than "successful attack" (e.g. a foiled attempt), it trains the player to distrust the tag on the rarest, highest-stakes alert in the game, which is the worst place for that to happen. | A genuinely rare, high-impact narrative event gets buried in the ordinary alert stack the one time in a playthrough it matters. |
| C5 | **Storm surge / flood event that actually breaches or damages occupied cells** (as opposed to the generic "storm surge" disasters-lite event pressure named in §10) | §10 line 230 ("disasters-lite... storm surge on the shore... as event pressure on JIT") + storm drains as flood defence (data table, line 1057). **Spec frames storm surge as ongoing JIT event pressure, not a discrete crisis — this row narrows it to a sub-case the spec does not itself distinguish.** Weak-to-moderate grounding; flagged low-confidence. | Sudden onset, weather-driven, not player-triggered, and — in the specific sub-case where it actually damages occupied cells rather than just stressing logistics — carries the same "fast, irreversible-if-unaddressed harm" shape as a fire (§26). | If the tag can't reliably distinguish "surge event fired" from "surge event that actually reached and damaged a home cell," this collapses into the excluded, JIT-pressure-only case below and fires on every coastal weather event, training the player to ignore it fast. | A shore district takes direct property/land-value damage the player doesn't see until the next time they happen to look at that overlay. |

### Least confident of the above

C4 and C5 are the two I'd flag hardest for Aaron's judgement over mine — C4 because the spec gives me the event but not the auto-pause verdict, and C5 because I'm the one drawing a line the spec doesn't draw (I don't have a way to be sure "surge event pressure" and "surge event that damages a cell" are even mechanically distinguishable data the engine will emit as two different things — that's for the owning engine BA to confirm before this row can go live).

---

## 3. Considered and excluded

This section is the more load-bearing half of the document — it's where the boundary actually sits.

| Condition | Why excluded |
|---|---|
| **Water deficit in 3 months** (§13's own example alert) | This is the spec's own worked example of a *projection*, not an emergency — it exists specifically so Pillar 3 ("squeezed, never ambushed") holds. The lead time is the point: the player has three months to act through ordinary planning, not three seconds. Tagging this as crisis would defeat the reason the projection system exists. |
| **School capacity exceeded next September** (§13's own example) | Same shape as water deficit — seasonally gated (§9), months of lead time, exactly the kind of thing the alert stack's ordinary prioritisation (not auto-pause) is for. |
| **Loan payment due** (§13's own example, and `ui.alerts.md` AC-6's explicit non-crisis case) | Already settled by AC-6 itself — P0-worthy, not crisis. Included here only for completeness of the taxonomy. |
| **Junction at 94%** (§13's own example) | Progressive congestion, visible continuously via the traffic overlay and F7 projections; crossing a percentage threshold isn't a discrete event with a "before/after" the player needs redirecting to — it's a standing condition they can check any time. |
| **Aquifer over-abstraction / drought stress degrading yield** (§6) | Gradual degradation over many months, visible in projections and the water overlay, and it's a *policy* problem (abstraction rate, farming regime choices per §31) more than a tick-urgent one. Nothing about the moment of crossing a degradation threshold demands the same-second reaction a stockout (C3) does. |
| **Road closure** (§10 disasters-lite) | Spec explicitly frames this as "event pressure on JIT" alongside storm surge and aquifer drought — i.e., content the logistics system is built to absorb and route around, not a stop-the-sim event. The whole point of the JIT system (§8) is that it handles exactly this class of disruption. |
| **Bin overflow → vermin → illness chain** (§25) | Spec's own words: "invisible for months, then vermin, then illness, then emigration — a classic Detroit on-ramp." Explicitly slow by design, same shape as the spiral itself (see §4) — the whole point is that it's *supposed* to be missable if you're not watching, which is a different design intent from a crisis interrupt. |
| **Detroit-spiral intermediate stage transitions** (emigration onset, tax-base decline, service cuts, attractiveness decline, abandonment onset, blight-spread onset — `engine.spiral.md` AC-2) | See §4 below — this is the single most important exclusion in this document and gets its own section. |
| **Gang formation** (§28, the >24-month threshold) | By definition a 24-month gradual process, visible continuously via crime/deprivation overlays. The threshold crossing is a milestone in an ongoing story the player has had two years of visibility into, not a surprise. |
| **Individual fire/police/ambulance incidents** (§26) | Dispatch is automatic — "nearest available unit assigned" happens without player input, and outcome quality is already a function of response time baked into the sim math before the player could react to a pause. There's no tick-critical lever pausing would hand the player; the actionable version of this problem (chronic under-resourcing, response times degrading) is a funding/capacity decision that belongs in an ordinary alert, not an interrupt, because by the time any single incident is bad enough to notice, the window to have prevented it has already closed. |
| **Coastal arrival (small-boat) events** (§30) | Explicitly a months-long status pipeline with policy sliders, not a same-tick decision. The spec's own framing ("policy sliders with real trade-offs, not right answers") argues for deliberate consideration via the ordinary alert stack, not a snap decision forced by auto-pause. |
| **Deep-mine closure** (§32) | The spec calls this "a scripted-by-you Detroit test" — it's a consequence of the player's *own* prior decision to open (and eventually exhaust or close) the mine. Pillar 3 is about not ambushing the player with things they had no way to see coming; a closure they scheduled or a mine they exhausted fails that test on its face. |

---

## 4. The slow-collapse boundary question, stated explicitly

You asked whether a slow collapse ever deserves an interrupt, and at what threshold. My answer, argued:

**No intermediate stage of the Detroit spiral should be crisis-tagged. Only the two terminal death conditions (C1, C2) should be.**

Reasoning:
- `engine.spiral.md` AC-2 requires each stage transition to be reversible — driven by real, live values (attractiveness score, tax delta), not a scripted countdown, and explicitly *reversible* if the player turns those values around. A collapse that can be reversed is, definitionally, still inside the "competing demands" gameplay loop Pillar 3 describes — it's hard, not an ambush.
- `ui.alerts.md` AC-8 (edge-triggered, not level-triggered) already tells us the *mechanism* can't tolerate a per-stage re-fire cheaply — pausing once per stage transition, six times over a slow multi-year decline, is exactly the "nag until disabled" failure mode the brief warns about. Even if I wanted a stage-level crisis, the interaction contract would make it feel worse, not better.
- The spiral's whole design intent (§12: "no scripted loss") is that the player should be watching this happen via ordinary UI (land-value overlay, decay overlay, F7 projections, the news ticker per §29) across the months it takes to unfold — the ticker literally narrates it ("340 families left for the mainland this month citing rents"). That's the game already telling the story through its normal channels. An auto-pause interrupt would be redundant with, and cruder than, the ticker doing its job.
- The two **terminal** conditions are different in kind, not degree: they are the moment the game state resolves to over. There is no "next stage" for the player to plan around, no reversibility left to exercise. That's exactly the crisis definition in §1 — nothing to do *except* react right now, because right now is when it stopped being a game-in-progress.

**Threshold, stated plainly:** the interrupt belongs at the death-condition check itself (`engine.spiral.md` AC-7's population/peak clause, `engine.finance.md` AC-7's 3-consecutive-months clause) — not at any earlier stage boundary, however severe that stage looks on the overlays.

---

## 5. Tunability recommendation

**Two-layer split, not one setting.**

**Layer 1 — which conditions carry the crisis tag at all: fixed by a data file, not exposed to the player.**

GR#15 ("validators derive from data") and this project's habit of keeping balance-adjacent numbers in data files argue for the *taxonomy itself* — the list in §2 — living in a data file (e.g. `data/crisis-taxonomy.json`) rather than hardcoded per-condition in engine Go, so design/balance passes (and QA, per the dev-team process's independent audit role) can add, remove, or re-threshold candidates without a code change, and so the file itself is the artifact Aaron approves against row-by-row.

But **this is not the same case GR#15 was written for.** GR#15's data-driven doctrine is about validators and expected values — things where getting the number wrong produces a wrong test result, correctable next iteration. A crisis tag that the player can silently misconfigure is different: get it wrong and the *player* doesn't find out until real in-sim damage has already happened (per this file's own opening framing — "a bug the player may not notice until real damage has already happened"). That argues against **player-facing** tunability of the taxonomy itself, even though it argues *for* the taxonomy being data, not code.

**Layer 2 — whether auto-pause is armed at all: a coarse, player-facing settings toggle.**

Some players will want zero interruptions (speedrunners, players who've learned the game and check the alert stack manually) and forcing auto-pause on everyone is its own way to train people to resent and eventually ignore the feature. I'd recommend a single settings toggle with three states — **Pause / Notify only / Off** — applied uniformly to every crisis-tagged condition, not a per-condition checklist. A per-condition settings screen ("auto-pause on insolvency: yes, auto-pause on ghost-city: no") reintroduces exactly the misconfiguration risk Layer 1 argues against, just moved to the player's hands instead of the balance team's.

**Recommendation in one line:** the *taxonomy* (which conditions are crisis-grade) is data-driven but not player-configurable; the *master switch* (does auto-pause fire at all) is a coarse player setting, uniform across all tagged conditions.

---

## 6. Interaction rules

`ui.alerts.md` AC-8/AC-9/AC-10 already specify the UI-side mechanism precisely (edge-triggered per crisis identity, re-arm-safe across manual resume, redirect never silently skipped when already paused). This proposal is written to be consistent with those, not to re-litigate them. What follows is the engine-side framing those ACs need from whatever emits the crisis tag:

- **A crisis firing while already paused:** per `ui.alerts.md` AC-10, the redirect must still fire even though the pause is a no-op. This proposal's only addition: the engine-side crisis identity (the ID `ui.alerts` dedupes on, AC-8) must be **stable across the condition's lifetime**, not regenerated per delta — otherwise AC-8's dedupe can't work regardless of how well the UI side is built. Whoever carries the crisis tag into `int.protocol` needs to own minting one stable ID per underlying crisis instance (e.g. "insolvency-trigger-save-X" fires once, ever, by construction — it's terminal; a hypothetical repeatable crisis type would need a per-instance ID, not a per-condition-type ID).
- **Two crises firing in the same tick:** `ui.alerts.md` AC-5 already gives the stack's tie-break (tier, then documented tie-break). I'd extend that same rule to decide *which* of two same-tick new crisis IDs the camera redirects to first: **redirect to the higher-tier one; if tied, the same deterministic tie-break AC-5 already uses (oldest-first / whatever it settles on) — not arrival order within the delta, which is not guaranteed stable.** This is an assumption (logged below) since neither file states it today.
- **A crisis persisting across many ticks:** covered by AC-8's edge-triggered dedupe — this document's only contribution is C1–C5 each being conditions where the *underlying engine event* is naturally discrete (a threshold crossing, an attack, a stockout moment), not a standing state, so the "persists across many ticks" case should be rare for this taxonomy specifically. The one place it's a real risk is C3 (water stockout) if the engine reports "stock is zero" on every subsequent delta while it stays zero — that's exactly AC-8's dedupe test shape, and the owning engine BA needs to confirm the stockout event is emitted once-on-transition, not once-per-delta-while-true.
- **A crisis resolving before the player acts:** covered by `ui.alerts.md` AC-12 (stale alert removed on the delta reporting resolution). No additional engine-side rule needed beyond AC-12 already existing — the auto-pause and redirect already happened; resolution afterward just clears the alert from the stack, it doesn't need to un-pause or re-redirect anything.

---

## 7. Report to Ben (summary)

**Candidate list in brief:**

| Condition | Verdict | One-line reason |
|---|---|---|
| Insolvency game-over trigger | **Include** | Terminal, spec-named death condition |
| Ghost-city death trigger | **Include** | Terminal, spec-named death condition |
| Water reserve actual stockout (not the 3-month projection) | **Include** | Distinct from the projection alert; real harm starts this tick |
| Successful terror attack | **Include (low confidence)** | Spec names the shock, not the auto-pause verdict |
| Storm surge damaging occupied cells | **Include (low confidence)** | I'm drawing a line the spec doesn't draw; needs engine confirmation it's even a distinguishable event |
| Water deficit in 3 months | Exclude | Spec's own projection example — the lead time is the point |
| School capacity exceeded next September | Exclude | Same — seasonal lead time, ordinary alert |
| Loan payment due | Exclude | Already settled by AC-6 |
| Junction at 94% | Exclude | Continuously visible standing condition, not a discrete event |
| Aquifer over-abstraction/drought | Exclude | Gradual, policy-driven, visible in projections |
| Road closure | Exclude | Spec frames as ordinary JIT event pressure |
| Bin overflow → illness chain | Exclude | Explicitly designed to be slow/missable, same shape as the spiral |
| Detroit-spiral intermediate stages | Exclude | Reversible by design; only the two terminal conditions qualify (see §4) |
| Gang formation | Exclude | 24-month threshold, continuously visible |
| Individual emergency-dispatch incidents | Exclude | Dispatch is automatic; no tick-critical player lever exists at incident time |
| Coastal arrival events | Exclude | Months-long policy pipeline, not a snap decision |
| Deep-mine closure | Exclude | Self-inflicted by the player's own prior choice — not an ambush |

**Tunability recommendation:** the taxonomy itself (which conditions are tagged) should be a data file, not player-configurable per-condition — it's a safety interrupt, not a balance curve, and GR#15's data-driven doctrine argues for data over hardcoding but not for player tunability here. The master on/off (Pause / Notify only / Off) should be a single coarse player setting applied uniformly, not per-condition.

**Least confident rows:** C4 (terror attack) and C5 (storm surge damage) — both flagged low-confidence above; these are where Aaron's judgement matters most, since the spec gives weaker or no explicit auto-pause grounding for either.

**ASM codes filed:** ASM-276 and ASM-277 (pre-existing, cited throughout) plus new assumptions logged against this proposal (see BOW).
