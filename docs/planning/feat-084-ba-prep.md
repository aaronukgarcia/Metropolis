# FEAT-084 — BA Prep: re-baselined fold plan (ASM close-out)

**Author:** Bill (lead lane) · **Date:** 2026-08-17 23:0x (re-baseline) · **Status:** PLAN-ONLY re-baseline. No ASM closed, no BOW status changed, no acceptance file edited by this note. Docs-only, Tester-tier (GR#23 proportionality).

**What this is.** The previous 08-14 plan baselined FEAT-084 at **332 open ASMs**; the 08-17 sitrep claimed **~1,204**; neither matches reality. This note re-baselines the plan against the live BOW, corrects the sitrep, and lays out a fold-cluster plan sized to the true figure — fold-don't-bare-close per Aaron's directive. The already-verified batch-1 verdicts and the wellbeing/citizens/freight fold drafts (Parts A/B of the prior note) are preserved below as ready-to-execute content.

---

## 1. Corrected baseline (measured live, not quoted)

**Sampled 2026-08-17 23:01 (local).** Derived from `node claude-bow.js list` (for the open-item total and the ASM-vs-non-ASM split) plus read-only queries against the `metro` DB (`bow_items` + `bow_comments`) for the two fields the list does not print: comment counts and creation dates.

| Metric | Live value | Sitrep/plan claimed | Verdict |
|---|---|---|---|
| Open BOW items (all types) | **1,374** | — (implied ~1,5xx) | measured |
| Open ASM assumptions | **1,031** | 332 (plan) / ~1,204 (sitrep) | both wrong |
| Open ASMs, zero comments | **972 (94.3%)** | "95%" | ≈ correct |
| Open ASMs, ≥1 comment | **59 (5.7%)** | — | measured |
| Open ASMs with an `mkey` | **0** | — | confirmed |
| Open SEC findings (separate type) | **184** | — | measured |

**Open ASMs by creation date (survivors still open at sample):**

| Created | Open now |
|---|---|
| 2026-08-09 | 5 |
| 2026-08-10 | 39 |
| 2026-08-11 | 58 |
| 2026-08-12 | 51 |
| 2026-08-13 | 92 |
| 2026-08-14 | 0 |
| 2026-08-15 | 82 |
| **2026-08-16** | **560** |
| 2026-08-17 | 172 |

**The 16 Aug spike, reconciled.** 609 ASMs were *created* on 08-16 (560 open + 49 since closed) — this matches the sitrep's "609". The 609-is-open reading was wrong: **560** of that day's ASMs remain open, not 609.

**Two further corrections to the sitrep:**

1. **"~1,204 open ASMs" was already ~14% stale when written.** The true figure is **~1,031** and falling. The 18:08 re-baseline comment logged 1,204; a close-out wave has since closed ~173 of them.
2. **The number is moving during sampling.** Over the ~3 minutes of this re-baseline the open-ASM count fell **1,087 → 1,031** (56 closes), and **174 ASMs closed on 08-17 alone** by 23:01. A close-out is already in flight (Bev's sweep); treat every count here as a timestamped snapshot, not a constant. Any batch executor must re-pull `node claude-bow.js list` at dispatch time.

**Ratio to the 332 baseline:** 1,031 / 332 ≈ **3.1x** out of date (the sitrep's "3.6x" assumed the stale 1,204).

---

## 2. Fold-cluster plan (sized to ~1,000+ open ASMs)

Aaron's directive: **fold, never bare-close** — content lands in an acceptance doc / master plan / user story before the ASM row retires. Grouping is by owning module/mkey so each acceptance file is opened once.

**Bucket targets.** The disposition map (`docs/planning/asm-disposition.md`) was re-pulled at 1,204 rows (bill, 18:08). It must be re-pulled again to the live ~1,031 before batch execution (the 173 already-closed rows drop out, mostly from the CC/SF buckets). Apply these proportions to the live count as planning targets, not asserted totals:

| Bucket | Map rows (1,204) | ~Live target (×0.86) | Action |
|---|---|---|---|
| CC — confirm-and-close (light fold) | 660 | ~570 | one-line rationale close, content already captured elsewhere |
| SF — spec-fold into acceptance doc | 306 | ~260 | add AC/note text, then close citing file#section |
| CC-BAL — balance-regime auto-close | 112 | ~95 | cite the standing balance-number regime |
| UNK — manual triage | 68 | ~58 | route by hand |
| ST — story / registration / Bill ruling | 41 | ~35 | new BOW item or `register-guid` edge |
| AD — Aaron decision list | 16 | ~14 | leave open, consolidate for Aaron |
| DUP — already folded | 1 | ~1 | verify-and-close |

**Execution order (cluster sequence):**

1. **CC-BAL (~95)** — biggest cheap win. Auto-close citing Aaron's balance-number-regime blanket ruling; but the **13 med-confidence items still go through the verify-first verdict** in Part A (below), not blind-close. 3 of 13 are genuinely balance-shaped; the other 10 re-classify (1 is a FIX).
2. **CC (~570)** — confirm-and-close with a specific one-line rationale (never boilerplate-only).
3. **SF (~260)** — grouped by acceptance file; the wellbeing/citizens/freight drafts in Part B are ready now; the rest are routed per `asm-disposition.md`.
4. **ST (~35)** — new BOW items + the GR#25 edge registrations (below) before any cross-module prose is committed.
5. **AD (~14)** — leave open; deliver Aaron the single consolidated decision list.
6. **UNK (~58)** — manual triage, then re-route into 1–5.

**Process guardrails (unchanged from FEAT-084's spec):** one module-cluster at a time on spare/background lanes only (never take lanes from the FEAT-083 spine); respect file claims on acceptance docs; `claude-bow.js` for every BOW write; escalate anything with money/balance/gameplay-feel or a real architecture fork. Every close names WHERE its content landed. Spot-check 10% of closes. Definition of done: open ASMs down to the AD set (~14) only.

---

## 3. Throttle the ASM/SEC generators (create:close)

The backlog is generator-driven, not close-driven. Live create vs close:

- **08-16:** 609 ASMs created vs **1** closed that day — effectively ~600:1.
- **08-17 (by 23:01):** ~184 created (172 still open) vs **174 closed** — the close wave has just caught up (~1.06:1), the first day close has outrun create.

**Action:** pause or rate-limit the ASM/SEC generators (the BA assumption emitters and the security-finding emitter) until the close rate is sustained ≥1 for at least a full day AND the open-ASM backlog is back under a re-baselined cap (e.g. <200). Otherwise the fold programme is shovelling against a tap left open. The 08-16 burst (609 in one day, clustered at 01:00–03:00 and 13:00/23:00) is the specific generator behaviour to stop. Recommend the generators be disabled (not just slowed) during the execute phase — a fresh assumption generated mid-fold can be folded the same wave, but not while the close pipeline is still below parity.

---

## Part A — Batch-1 verify-first verdict (13 med-confidence CC-BAL items)

Amendment A1's lesson applies: a "placeholder" that is actually a soundness gap is a FIX/bug, not a balance close. Each item was re-read against the balance-number-regime test ("is this a player-felt number/curve/rate that ships as a placeholder?").

| ASM | Module | Verdict | Action |
|---|---|---|---|
| ASM-1107 | engine.wellbeing | **BALANCE** ✓ | Keep CC-BAL. Financial-stress hard-step shape + data-sourced 0.35 threshold is a player-felt curve placeholder. Fold one line into `engine.wellbeing.md` AC-6. |
| ASM-1234 | engine.fiscal | **BALANCE** ✓ | Keep CC-BAL. Consecutive-cycles count (default 3 monthly) is a data-defined threshold placeholder. Fold into `engine.fiscal-circuit.md`. |
| ASM-1235 | engine.fiscal | **BALANCE** ✓ | Keep CC-BAL. 18-month runway is a data-defined threshold placeholder (reuses reserve-months machinery). Fold into `engine.fiscal-circuit.md`. |
| ASM-863 | ui.diagrams | NOT balance | **SF** — an unfilled `ASM-xxx` placeholder + an empty `DiagramAPI` inbound-consumers list with no open tracker (a BUG-058-linked gap never re-filed). Documentation/tracking gap, not a number. |
| ASM-971 | engine.firms | NOT balance | **CC** — demand-signal *proxy* (ConsumerGoods availability), not a number. Light fold into `engine.firms.md`: proxy is placeholder, replace when a real aggregate demand signal lands. |
| ASM-1004 | feat.minetypes | NOT balance | **SF + GR#25 flag** — blight ordinal is a cross-module placeholder pending `engine.mining`'s unbuilt `BlightAPI`. Fold the placeholder note; flag the implied feat.minetypes→engine.mining dependency for edge-registration before it can be "reconciled." |
| ASM-1041 | engine.crime | NOT balance | **SF** — precursor *semantics* (consecutive-nonzero streak, not month-over-month rising) is a justified soundness/design decision that must be preserved in the spec, not a balance number. Fold the precursor definition into `engine.crime.md` AC-11. |
| ASM-1254 | engine.fdi | NOT balance | **CC** — test-adapter `RemoveFirm` unwired, returns error (fails loud). Scaffolding placeholder, not a number. Light fold into `engine.fdi` test note / `engine.pharmacampus` AC note. |
| ASM-1295 | engine.maintenance | NOT balance — **SOUNDNESS GAP** | **ST → FIX/BUG item.** Config/data validation leaves `EngineerDaysPerYear`/`LifetimeYears`/cost figures positive-unbounded; a near-MaxInt64 authoring value silently saturates at load time (SEC-117 shape). The bound *value* is a balance-pass decision, but *leaving it unbounded* is a real (P3) load-time silent-saturation gap, not a player-felt placeholder. Needs a BOW item, not a close. |
| ASM-1428 | engine.coastal | MIXED | **SF** — the *shape* (`world conditions` = [0,1] push scalar × data scale, §30 undefined) is a spec-gap modelling decision; the *magnitude* is balance (ASM-323). Fold the shape into `engine.coastal.md`; magnitude rides the balance regime. |
| ASM-1432 | engine.coastal | MIXED | **SF** — per-case-per-month vs one-off requisition is a §30 ambiguity resolution (honest reading), not a number. Fold the interpretation into `engine.coastal.md`; magnitude rides balance (ASM-323). |
| ASM-1457 | engine.coastal | NOT balance | **CC** — friction bound magnitudes (1e6 / 1e18) are GR#15 finite-guard ceilings whose "only job is to keep SatisfactionFriction() finite"; the ASM itself says "neither is a player-felt balance number." Light fold into `engine.coastal` config doc/comment. |
| ASM-1458 | engine.coastal | NOT balance | **CC** — SEC-233/234 ceilings (1e6) are finite-guard bounds to keep int64 conversion + friction saturation deterministic, "GR#15 data-placeholder bound," explicitly not player-felt. Light fold into `engine.coastal` config doc/comment. |

**Headline:** 3 of 13 are genuinely balance-shaped (1107/1234/1235). 10 re-classify. Of those, **ASM-1295 is a real soundness gap needing a FIX item** (the A1-lesson case), 2 (1457/1458) are GR#15 finite-guard bounds already self-described as "not player-felt," and the rest split SF/CC. Two items (ASM-1004, and ASM-1428/1432's shape halves) carry GR#25 cross-module-dependency implications that must go through edge-registration before the fold is committed.

---

## Part B — SF fold drafts (wellbeing / citizens / freight)

Fold legend: **FOLD** = substantive prose into the named file's named AC/section · **CC** = one-line confirm · **ST** = convert to a BOW item / Bill ruling (registration, new command, or edge) · **DUP/ALREADY** = already folded or duplicate. Every entry names the fold destination; the executing agent copies the text in, re-verifies the AC, then closes the ASM with the destination cited.

### engine.wellbeing (9 SF)

1. **ASM-999** (feat.faith) — **FOLD** into `engine.wellbeing.md` AC-1 note / Escalations. *Text:* "Cohesion (faith's neighbourhood-harmony term) is **not** a 16th wellbeing driver; faith feeds a data-placeholder contribution through the community-access surface. If Aaron rules a first-class cohesion driver, AC-1's 15-driver binding list must be amended." (Cross-module wellbeing↔faith; confirm edge in code.json before committing prose.)
2. **ASM-1021** (engine.refuse) — **FOLD** into `engine.wellbeing.md` AC-1b/API surface. *Text:* "`WellbeingAPI` must expose `ReportPollutionExposure` — the refuse→wellbeing outbound edge (GUID da2c5c2a). `engine.refuse` already declares the local seam; the composition root wires the real `WellbeingAPI` when MOD-034 lands. The overflow health consequence crosses this seam, never a refuse-owned health number."
3. **ASM-1106** — **FOLD** into `engine.wellbeing.md` AC-15. *Text:* "The track baseline includes a deterministic hash-bound offset `det.NewStream(worldSeed, citizenID, month, wellbeing.baseline)` in [-3,+3], so the attribution signature depends on (seed, id, month) and the baseline is never flat; bit-identical across runs/platforms."
4. **ASM-1110** — **FOLD** into `engine.wellbeing.md` AC-13 (replace the stale "no sub-range exists yet"). *Text:* "Error range claimed: **MET-G2200..G2299** (engine.wellbeing). G2100-G2199 was taken by engine.leisure. MET-G2200..G2205 are registered in `data/errors.json` under BUG-234."
5. **ASM-1119** — **FOLD** into `engine.wellbeing.md` AC-13. *Text:* "`PersonsPerRoom` carries only a `>=0` validator bound, so the Crowding driver must guard its own overflow (1e308 is treated in-domain). NaN/+Inf config vectors are programmatic-only — `data.Load`'s strict JSON decoder rejects `1e999` and NaN literals."
6. **ASM-1126** (engine.worklife) — **FOLD** into `engine.wellbeing.md` AC-4/AC-1 driver mapping. *Text:* "The worklife overwork (996) input maps to the §42 discretionary-hours/leisure-fit term (`balance = (168-workHours)/168 - wellbeingWeight`), **not** the §18 commute-time driver. worklife pushes via its `WellbeingAPI` seam; engine.wellbeing owns the happiness arithmetic."
7. **ASM-1129** — **FOLD** into `engine.wellbeing.md` AC-2 (correct the "never-clamped/exact" claim). *Text:* "The AC-2 identity (`Total == Baseline + Σdelta`) is exact for every accepted config, but the doc's 'never-clamped/exact' wording is now false: `satFinite` clamps the four modifier products, the headline product, and the unbounded-input saturation boundary. The identity is only breakable via two adversarial MaxFloat64 weights in one track — unreachable with real data."
8. **ASM-1165** — **FOLD** into `engine.wellbeing.md` AC-2/AC-13. *Text:* "`Validate` rejects any weight/slope/age-curve-delta/commute-stress-anchor above `maxCoefficient=1e6` (~1e5× the largest shipped weight 15, ~1e8× the shipped slope 0.01) — rejects nothing legitimate, guarantees no float64 overflow, keeps the identity exact. `satFinite` remains the backstop."
9. **ASM-1343** (engine.social) — **FOLD** into `engine.wellbeing.md` AC-3 (fix the grep to match reality). *Text:* "The exported names are `DriverCrowding`/`DriverFinancialStress` constants and `MentalAttribution.Crowding`/`.FinancialStress` fields — **not** package-level `Crowding`/`FinancialStress`. AC-3's check must match these actual names; a doc-comment alias is not the binding contract."

### engine.citizens (10 SF)

> Cluster-level finding: this "citizens SF" set is mostly **not** citizens-owned spec folds. It is a cluster of cross-module API-surface gaps, three of which need new `CitizensAPI` surface (→ ST). Only ASM-827 is a clean mechanical citizens fold.

1. **ASM-827** — **FOLD (mechanical)** into `engine.citizens.md` header. Fix three line drifts: counter-RNG 824→**826**, citizen-shard 8GB 832→**834**, reference hardware 788→**790**; and `engine.world.md`'s world-cells 4GB 833→**835**.
2. **ASM-273** (engine.extcommute) — **FOLD** into `engine.citizens.md` AC-1 note. *Text:* "`employmentState` must be able to represent an off-map-job variant (which pool, since when) for `engine.extcommute`'s AC-6/9/11. If the field shape cannot, extcommute's dormitory/no-resident-gain/double-assignment ACs need revisiting at dispatch."
3. **ASM-891** (engine.staffing) — **FOLD** into `engine.staffing.md` (cross-module, not citizens). *Text:* "A6 (the only off-map labour clause) governs out-commuting citizens; the in-commuting contractor-premium magnitude and contractor-pool size are unspecified — AC-10 checks direction + finiteness only, never a magnitude."
4. **ASM-968** (engine.firms) — **FOLD** into `engine.citizens.md` AC-1b note. *Text:* "`CitizensAPI` exposes no labour-pool query method; firms' `HireStaff`/`Grow` accept caller-supplied CitizenIDs (composition root obtains them). If CitizensAPI later adds a labour-pool query, `Grow` should consume it directly."
5. **ASM-998** (feat.faith) — **FOLD** into `engine.citizens.md` AC-1 note. *Text:* "The citizen record has **no** affiliation field (MOD-018 done). Faith owns a deterministic affiliation store keyed by citizen id. If Aaron rules affiliation into the citizen record, MOD-018 reopens with a byte-budget review."
6. **ASM-1039** (engine.education) — **ST** → new BOW item (CitizensAPI attainment-write command). The `LifeEventCommand` surface has no command to write `Education.Attainment/Stages/SchoolingMonths`; education holds authoritative attainment in its own pupil record. A citizens command accepting quality/attainment input is the missing cross-module piece. Fold the gap note into `engine.citizens.md` AC-1b, then convert to a BOW item.
7. **ASM-1056** (engine.education) — **ST** → integration gap (citizens life-event-stream subscription). Education's AC-10 names "via citizens life-event stream" but only a manual `RemovePupil(id, reason, month)` exists; the citizens→education departure wiring is an integration assumption, not code. Fold the note; convert to a BOW item.
8. **ASM-1160** (engine.census) — **FOLD** into `engine.citizens.md` AC-1 note. *Text:* "The `Sector` enum is coarse (primary/secondary/tertiary/public); census maps primary/secondary/tertiary→blue-collar, public→white-collar. The finer white-collar split (firm-overhead vs finance vs public-admin) is a master-spec gap, not census-invented."
9. **ASM-1330** (engine.comms) — **FOLD** into `engine.comms.md` (cross-module). *Text:* "AC-4's remote-work slice is sector-aware only; per-citizen personality refinement is deferred to the future traffic consumer (blocked on BUG-058). If personality modulation is required now, comms needs a citizens/personality input + an edge decision."
10. **ASM-1344** (engine.social) — **ST** → new citizens `LifeEventSocial` command kind. Social writes the AC-6 marker via `LifeEventHealth→HealthBand` because no `LifeEventSocial` kind exists. Fold the note into `engine.citizens.md` AC-1b; convert to a BOW item (a dedicated LifeEventSocial kind is a citizens change needing its own dispatch).

### engine.freight (8 SF)

1. **ASM-608** — **FOLD** into `engine.freight.md` AC-10. *Text:* "AC-10's identity (`Produced == ConsumedDownstream + Exported + StorageDelta + InTransitDelta`) holds only when imports are zero. The complete identity is `Produced + Imported == ConsumedDownstream + Exported + StorageDelta + InTransitDelta`, exposed via `ConservationAccount.IsBalanced`. If imports are intended inside the single identity, the account's terms need restructuring."
2. **ASM-686** (feat.containerport) — **FOLD** into `engine.freight.md` AC-3 note. *Text:* "Containerport customs throughput + saturation-smuggling risk reuse engine.freight's customs model (AC-3), extended for the deep-sea tier — not a new local smuggling model (which would duplicate and break the shared saturation-smuggling mechanic)."
3. **ASM-830** — **ALREADY FOLDED / verify+close.** The current `engine.freight.md` header already reads `Status: done`, lists `MOD-020`/`MOD-025` done, and includes `feat.megafacilities` in the consumers list. Re-verify at close time; no new edit expected.
4. **ASM-908** (feat.factorytypes) — **ST** → register-guid. `feat.factorytypes` is absent from code.json (grep confirmed); ASM-680-683/694 anchor to the unregistered key. Register before dispatch: path `internal/engine/freight/`, deps `engine.freight` + `engine.firms`, empty `inbound.consumers`, `outbound.calls`→`engine.freight`. Sibling `feat.extraction` models the pattern.
5. **ASM-969** (engine.firms) — **FOLD** into `engine.freight.md` (chain-input taxonomy note). *Text:* "The firm sector→input-commodity mapping (AC-8) is a placeholder: primary→foodStaples, secondary→constructionMaterials, else→consumerGoods. The full per-sector input bill is engine.freight's job."
6. **ASM-1087** (engine.rail) — **FOLD** into `engine.freight.md` AC-13 note. *Text:* "Intermodal transfers that would saturate either ledger are rejected whole (`ErrRailTransferRejected`, no partial update) via `num.SatAddChecked` on both in[from] and out[to] — matches AC-13 'reject, don't clamp'. (Reject chosen over dwell-gap because the stub is zero-dwell.)"
7. **ASM-1174** (feat.pharmacampus) — **ST** → edge registration. `feat.pharmacampus` outbound must add `engine.freight` (master-plan → generate.js) before wiring `TradeEdge.AddExports` to `FreightAPI.Export`; currently registers only `engine.fdi`/`engine.education`/`engine.firms`.
8. **ASM-1269** (engine.rail) — **FOLD** into `engine.freight.md` modal-cap note. *Text:* "rail's `loadModalCaps` mirrors engine.freight's modal-cap load validation (`min >= 0` and `min <= max`) so a nonsensical `data/freight.json` is rejected at `NewRailAPI` (fail-loud), rather than producing a surface that rejects every transfer."

---

## Part C — Cross-cutting flags (collision + ordering signals, re-baselined)

1. **The disposition map is out of sync with the live count.** `asm-disposition.md` was re-pulled at 1,204 rows (18:08); the live open-ASM count is now ~1,031 and falling. **Re-pull the map against `node claude-bow.js list` at dispatch time** before any batch executes — the ~173 already-closed rows must not be re-folded (and a batch that "closes" an already-closed row is a wasted lane or a wrong edit).
2. **Batch-1 re-classification is material.** The 13 med-confidence items are 3/10, not the assumed balance set; the executor must apply Part A, not the raw CC-BAL roster. **ASM-1295 is a FIX, not a close** — needs a BOW item for unbounded maintenance config bounds (SEC-117 load-time saturation).
3. **GR#25 ordering.** Four folds imply cross-module dependencies that must be edge-registered in `master-plan-v2.1.json` → `generate.js` before the prose is committed: ASM-1004 (minetypes→mining BlightAPI), ASM-908 (feat.factorytypes registration), ASM-1174 (pharmacampus→freight), plus the wellbeing↔faith (ASM-999) and refuse→wellbeing (ASM-1021) seams. None of these are pure prose folds; the batch must not close them as SF without the edge landing first.
4. **"engine.citizens SF" is mis-owned in the disposition map.** 6 of 10 are cross-module (extcommute/staffing/firms/faith/census/comms/social), and 3 are **ST** (new CitizensAPI surface: attainment-write command, life-event-stream subscription, LifeEventSocial kind) that need BOW items, not spec folds. Only ASM-827 is a genuine citizens-owned mechanical fold.
5. **ASM-830 already folded** — the current `engine.freight.md` header is up to date; verify-and-close, no edit.
6. **The generators are still the root cause.** 560 ASMs from 08-16 remain open and 172 from 08-17 (and counting). Throttling (Section 3) is a precondition for this programme to converge — otherwise every fold wave is outpaced by new creates.

*No git mutation performed in this note's production — all prep is in this file. Execution (editing acceptance files + closing ASMs) proceeds per the cluster sequence in Section 2, gated on the disposition re-pull and the generator throttle.*
