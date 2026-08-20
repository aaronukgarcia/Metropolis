# ASM Backlog Disposition Map — FEAT-084

> **RE-BASELINE 2026-08-17 (sitrep action A6, Aaron-approved lane).** This supersedes the 2026-08-14 snapshot's COUNTS (330 open → now **1204 open**). It does NOT supersede the 2026-08-14 per-item fold content for the original population — that content is preserved verbatim in Appendix A below and remains the authoritative fold text for the 324 prior-mapped items still open.
>
> This is a PLAN-ONLY pass: no ASM was closed, no BOW status changed, no acceptance doc edited. Execution is batched below and gated per the standing FEAT-084/FEAT-085 approval.

## Approved-amendments record (carried forward, still in force)

**Aaron-approved amendments (2026-08-14) — FEAT-084/FEAT-085 verdict "APPROVED WITH AMENDMENTS A1–A4":**
1. (A1) Perf-gate soundness ASMs re-bucketed to FIX — **now executed**: all 4 FIX items (ASM-353/355/374/375) have since closed with the C6 cluster.
2. (A2) Balance-number-regime placeholders re-bucketed to CONFIRM-AND-CLOSE, closing citing the standing blanket ruling (placeholder + directional test + row-by-row at the M2 balance pass) — do NOT re-interview Aaron. This re-baseline extends A2 to the whole new population as class **CC-BAL** (batch 1 below).
3. (A3) ASM-150 removed (DONE — GR#22 scrub, e3c2dbb, ACCEPT verdict).
4. (A4) Plans posted on the item + approved before execution (head-dev gate) — this document is that plan for the re-baselined population.

**Aaron's standing directive (unchanged):** never bare-close — fold each assumption's content into a spec/acceptance improvement or user story, THEN close.

## New baseline

- **Open ASM items as of 2026-08-17: 1204** (open 1204 / done 253 / cancelled 1). Growth since the 2026-08-14 baseline: 330 → 1204, driven by the BA/destructive waves (609 created 2026-08-16 alone, 184 on 2026-08-17). ~95% have zero comments; the 55 commented items are all from the original baseline.
- **Prior-map reconciliation:** 324 of the 329 prior-mapped items are still open. Closed since: the 4 FIX items (ASM-353/355/374/375, C6 cluster) and 1 CC item. Their approved buckets are carried forward unchanged.
- **No open ASM has an mkey** — owning module was derived heuristically (see convention below).

## Taxonomy (extends the approved 2026-08-14 legend)

| Class | Meaning | Disposition |
|---|---|---|
| **CC-BAL** | Balance-number class — questions a player-felt number/curve/rate | AUTO-CLOSE citing the balance-number regime (amendment A2; citation text below). No Aaron re-interview. |
| **CC** | Confirm-and-close — an implementer/BA decision already made and documented in the ASM | Light fold: one confirming line into the owning module's acceptance doc (or doc.go header), then close. |
| **SF** | Spec-fold — names a concrete spec/AC gap, drift, or amendment | Substantive fold into `docs/planning/acceptance/<module>.md` (target file = owning module), then close. |
| **ST** | Story — feature-shaped: needs a new BOW item, registration (/register-guid, GR#25 edge), or error-layer ruling | Convert to a BOW item / Bill ruling, then close the ASM referencing it. |
| **DUP** | Duplicate/superseded — same question as another ASM or already ruled | Close citing the ruling/canonical ASM. |
| **AD** | Genuine Aaron decision — money/architecture/gameplay forks | Stays OPEN; goes to Aaron directly (short list below). |
| **UNK** | Unattributable/unclear — no derivable owning module AND no classification signal | Manual triage batch (see batch 6). |

(The 2026-08-14 FIX class is retired — all 4 members closed.)

**Confidence convention.** Classification at this scale is heuristic. Confidence per row: **high** = prior-map carry-forward, curated by hand, or strong keyword signature; **med** = single clear keyword signature; **low** = default-bucketed (no strong signal — landed in CC as "implementation decision logged" by elimination, or UNK). 353 of 1204 rows are low-confidence. Rule for execution: **a batch agent must re-read each ASM before closing it and may re-classify — this map routes work, it does not authorize blind closes.** Low-confidence concentration: the CC default bucket and the `unattributed` module rows. Module attribution is also heuristic (mkey column is empty everywhere): derived from module keys/paths in title+description, referenced item codes' (SEC/FEAT/MOD/BUG) mkeys, and unique module-name words — expect ~5-10% misattribution at the tails; the per-batch re-read corrects it.

## Class counts (new baseline, 2026-08-17)

| Class | Count | of which prior-map |
|---|---|---|
| CC-BAL (balance auto-close) | **112** | 51 |
| CC (confirm-and-close, light fold) | **660** | 196 |
| SF (spec-fold) | **306** | 34 |
| ST (story/registration) | **41** | 30 |
| DUP (duplicate/superseded) | **1** | 0 |
| AD (Aaron decision) | **16** | 13 |
| UNK (unattributable/unclear) | **68** | 0 |
| **Total open** | **1204** | 324 |

Only 1 mechanical duplicate was found (explicit same-fork citation). Near-duplicate *themes* recur constantly (error-range claims, copy-guard conventions, SEC finite-guard scoping) but each instance binds to a different module's spec, so they fold separately — the right dedupe is the shared-standard folds noted in the execution plan, not item-level closes.

## The Aaron decision list (class AD — 16 items baseline, goes to Aaron directly; **2 RATIFIED as shipped since baseline — ASM-1451, ASM-1452 — leaving 14 pending**)

The 13 approved 2026-08-14 AD items (all still open), plus 3 curated from the new population (marked NEW; **ASM-1451 and ASM-1452 since RATIFIED as shipped — see §4 below**).

### 1. Architecture ownership / boundaries (6)
- **ASM-281** — Call-edge direction inferred from spec prose may not match the intended GR#20 contract direction (needs per-candidate architect ruling before master-plan edits).
- **ASM-306** — Which module owns the 'goods' conservation stock: market vs logistics (or both).
- **ASM-453** — Go game process → metro BOW MariaDB for in-game defect logging: driver vs local queue-file (shared FEAT-065+066 decision).
- **ASM-470** — harness.replay.Recorder is NOT durable (buffers in memory): incremental-flush vs rescope to periodic Save() vs declare dev-only.
- **ASM-489** — External commodity prices: single static floor/ceiling per product vs dynamic supply/demand (dynamic shelved to future-dev?).
- **ASM-460** — Death-condition warning: structural trigger-path gate vs weaker temporal-correlation check (AC-29 strength).

### 2. Tax & borrowing instrument design (5)
- **ASM-413** — Secured-loan collateral forfeiture on default is unspecified by FEAT-057.
- **ASM-414** — Revenue-share base: city-wide vs single-facility.
- **ASM-415** — UK-today instruments (VAT/import/corp/PAYE): engine.tax panel vs engine.fiscal whole-economy view.
- **ASM-416** — Zone-class rate/relief overrides generalised to every instrument vs policies.json discount.
- **ASM-875** (NEW) — engine.tax AC set vs FEAT-056's committed six-instrument ceiling: grow the data toward the full §39 panel, trim the ACs to six, or ship six now + levies later. (ASM-823 is the same fork — closes as DUP when this is ruled.)

### 3. Crisis-taxonomy edges (2)
- **ASM-299** — Terror attack + storm-surge-damage: the two lowest-confidence crisis candidates.
- **ASM-346** — C5 storm-surge "damage-to-occupied-cells" gated on feat.disasters emitting a distinguishable damage event.

### 4. Gameplay-feel / strategy (3 baseline → 1 pending; ASM-1451/1452 RATIFIED as shipped)
- **ASM-461** — Death warning must be a proactive push alert (ui.alerts), not just a passive F7/F2 pane.
- **ASM-1451** (NEW — **RATIFIED as shipped, Aaron; resolved**) — engine.roads AC-4 upgrade compatibility: the ruling is **any-to-any with rung-distance cost scaling** — any rung of the class ladder may convert directly to any other rung (gravel→motorway jumps legal), with cost scaled by rung-distance; the step-through-adjacent alternative was ruled out. Folded into `engine.roads.md` AC-4/AC-19.
- **ASM-1452** (NEW — **RATIFIED as shipped, Aaron; resolved**) — Civic-building naming: the **toponym+type fallback is correct** (shipped behaviour); the 'notable deceased citizen' eligibility/ranking rule is future work tracked as **FEAT-213** (out of scope). The engine.citizens edge only becomes relevant when FEAT-213 lands. Folded into `engine.roads.md` AC-10.

## Auto-close list (class CC-BAL — batch 1, 112 items)

**Citation text to use on each close** (per amendment A2; adapted from the approved 2026-08-14 wording):

> Closed citing the standing balance-number regime (Aaron's blanket ruling, 2026-08-13): player-felt numbers ship as placeholders with directional tests; a delegated proposal plus Aaron's row-by-row approval happens at the M2 balance pass (MOD-036 harness). One line recording the placeholder is folded into the owning module's acceptance doc. Do not re-interview Aaron per-number.

Each close also writes the one-line placeholder note into the owning module's acceptance doc (the light fold that satisfies the never-bare-close directive). Codes below; the 13 med-confidence codes must be verified as genuinely balance-shaped (not soundness bugs) during the batch — amendment A1's lesson.

**High-confidence (99):**

ASM-005, ASM-132, ASM-133, ASM-134, ASM-135, ASM-137, ASM-202, ASM-209, ASM-211, ASM-234, ASM-241, ASM-269, ASM-278, ASM-288, ASM-291, ASM-292, ASM-293, ASM-294, ASM-295, ASM-307, ASM-308, ASM-309, ASM-310, ASM-311, ASM-312, ASM-313, ASM-314, ASM-316, ASM-317, ASM-318, ASM-319, ASM-321, ASM-322, ASM-323, ASM-325, ASM-327, ASM-329, ASM-330, ASM-331, ASM-333, ASM-335, ASM-440, ASM-448, ASM-450, ASM-459, ASM-490, ASM-494, ASM-532, ASM-552, ASM-553, ASM-564, ASM-589, ASM-600, ASM-639, ASM-646, ASM-673, ASM-683, ASM-697, ASM-703, ASM-774, ASM-784, ASM-804, ASM-849, ASM-890, ASM-953, ASM-954, ASM-1009, ASM-1012, ASM-1016, ASM-1024, ASM-1032, ASM-1036, ASM-1047, ASM-1049, ASM-1099, ASM-1103, ASM-1137, ASM-1145, ASM-1162, ASM-1170, ASM-1171, ASM-1189, ASM-1194, ASM-1195, ASM-1216, ASM-1224, ASM-1229, ASM-1237, ASM-1279, ASM-1291, ASM-1304, ASM-1315, ASM-1326, ASM-1333, ASM-1342, ASM-1364, ASM-1382, ASM-1411, ASM-1423

**Medium-confidence — verify balance-shape first (13):**

ASM-863, ASM-971, ASM-1004, ASM-1041, ASM-1107, ASM-1234, ASM-1235, ASM-1254, ASM-1295, ASM-1428, ASM-1432, ASM-1457, ASM-1458

## Per-module fold table (modules with ≥3 open ASMs)

Owning module ⇒ target acceptance file `docs/planning/acceptance/<module>.md` (tooling/guards fold into their script headers or `docs/golden-rules-detail.md` where no acceptance file exists).

| Module | Total | CC-BAL | CC | SF | ST | DUP | AD | UNK |
|---|---|---|---|---|---|---|---|---|
| engine.invariant | 26 | 0 | 19 | 4 | 1 | 0 | 2 | 0 |
| engine.wellbeing | 25 | 4 | 12 | 9 | 0 | 0 | 0 | 0 |
| engine.citizens | 24 | 0 | 12 | 10 | 1 | 0 | 1 | 0 |
| tooling | 23 | 0 | 22 | 0 | 1 | 0 | 0 | 0 |
| engine.freight | 21 | 1 | 12 | 8 | 0 | 0 | 0 | 0 |
| engine.refuse | 20 | 3 | 5 | 11 | 1 | 0 | 0 | 0 |
| engine.dispatch | 18 | 2 | 6 | 7 | 2 | 0 | 1 | 0 |
| feat.megafacilities | 18 | 0 | 7 | 9 | 2 | 0 | 0 | 0 |
| engine.core | 17 | 3 | 7 | 6 | 1 | 0 | 0 | 0 |
| engine.logistics | 17 | 2 | 8 | 7 | 0 | 0 | 0 | 0 |
| ui.diagrams | 17 | 1 | 13 | 1 | 2 | 0 | 0 | 0 |
| engine.mining | 16 | 3 | 7 | 3 | 3 | 0 | 0 | 0 |
| engine.education | 16 | 3 | 8 | 5 | 0 | 0 | 0 | 0 |
| engine.finance | 15 | 2 | 4 | 7 | 2 | 0 | 0 | 0 |
| engine.firms | 15 | 2 | 7 | 6 | 0 | 0 | 0 | 0 |
| engine.chemicals | 15 | 2 | 8 | 5 | 0 | 0 | 0 | 0 |
| ui.dash | 14 | 0 | 13 | 1 | 0 | 0 | 0 | 0 |
| data.catalogue | 13 | 1 | 10 | 2 | 0 | 0 | 0 | 0 |
| engine.maintenance | 13 | 2 | 6 | 4 | 1 | 0 | 0 | 0 |
| feat.checkpoint | 13 | 1 | 9 | 3 | 0 | 0 | 0 | 0 |
| engine.unlocks | 12 | 1 | 8 | 2 | 1 | 0 | 0 | 0 |
| engine.policies | 11 | 4 | 4 | 2 | 1 | 0 | 0 | 0 |
| foundation.data | 10 | 0 | 7 | 3 | 0 | 0 | 0 | 0 |
| engine.build | 10 | 3 | 6 | 1 | 0 | 0 | 0 | 0 |
| engine.season | 10 | 5 | 3 | 2 | 0 | 0 | 0 | 0 |
| ui.widgets | 10 | 1 | 9 | 0 | 0 | 0 | 0 | 0 |
| engine.news | 10 | 1 | 7 | 2 | 0 | 0 | 0 | 0 |
| engine.projections | 10 | 1 | 5 | 4 | 0 | 0 | 0 | 0 |
| feat.refinery | 10 | 1 | 6 | 3 | 0 | 0 | 0 | 0 |
| tool.fileclaimguard | 10 | 0 | 9 | 1 | 0 | 0 | 0 | 0 |
| harness.synth | 9 | 0 | 7 | 2 | 0 | 0 | 0 | 0 |
| feat.saveux | 9 | 0 | 6 | 3 | 0 | 0 | 0 | 0 |
| engine.tax | 9 | 0 | 3 | 3 | 0 | 1 | 2 | 0 |
| engine.farming | 9 | 3 | 2 | 3 | 1 | 0 | 0 | 0 |
| foundation.num | 9 | 0 | 6 | 2 | 1 | 0 | 0 | 0 |
| tool.sync | 8 | 0 | 7 | 1 | 0 | 0 | 0 | 0 |
| int.solver | 8 | 0 | 5 | 3 | 0 | 0 | 0 | 0 |
| engine.social | 8 | 2 | 5 | 1 | 0 | 0 | 0 | 0 |
| engine.capexport | 8 | 1 | 2 | 4 | 1 | 0 | 0 | 0 |
| engine.leisure | 8 | 1 | 4 | 3 | 0 | 0 | 0 | 0 |
| tool.codebaseviz | 8 | 0 | 8 | 0 | 0 | 0 | 0 | 0 |
| engine.defence | 7 | 0 | 6 | 1 | 0 | 0 | 0 | 0 |
| engine.world | 7 | 0 | 5 | 2 | 0 | 0 | 0 | 0 |
| feat.metricsdash | 7 | 0 | 5 | 0 | 1 | 0 | 1 | 0 |
| engine.fdi | 7 | 0 | 3 | 4 | 0 | 0 | 0 | 0 |
| engine.services | 7 | 0 | 2 | 5 | 0 | 0 | 0 | 0 |
| feat.minetypes | 7 | 1 | 3 | 3 | 0 | 0 | 0 | 0 |
| feat.containerport | 7 | 0 | 6 | 1 | 0 | 0 | 0 | 0 |
| feat.farmtypes | 7 | 1 | 2 | 3 | 1 | 0 | 0 | 0 |
| engine.rail | 7 | 0 | 4 | 3 | 0 | 0 | 0 | 0 |
| engine.spaceport | 7 | 1 | 2 | 4 | 0 | 0 | 0 | 0 |
| harness.headless | 7 | 1 | 4 | 2 | 0 | 0 | 0 | 0 |
| harness.replay | 6 | 0 | 2 | 3 | 0 | 0 | 1 | 0 |
| feat.extraction | 6 | 1 | 3 | 2 | 0 | 0 | 0 | 0 |
| engine.coastal | 6 | 3 | 2 | 1 | 0 | 0 | 0 | 0 |
| engine.destination | 6 | 0 | 2 | 4 | 0 | 0 | 0 | 0 |
| data.unlocktrees | 6 | 0 | 5 | 1 | 0 | 0 | 0 | 0 |
| feat.facilitypermits | 6 | 0 | 5 | 1 | 0 | 0 | 0 | 0 |
| data.errors | 6 | 0 | 5 | 1 | 0 | 0 | 0 | 0 |
| engine.staffing | 6 | 1 | 1 | 3 | 1 | 0 | 0 | 0 |
| engine.census | 6 | 0 | 5 | 1 | 0 | 0 | 0 | 0 |
| engine.airport | 6 | 0 | 3 | 3 | 0 | 0 | 0 | 0 |
| feat.commoditymarket | 6 | 0 | 2 | 4 | 0 | 0 | 0 | 0 |
| engine.accelerator | 6 | 0 | 4 | 2 | 0 | 0 | 0 | 0 |
| engine.crime | 6 | 1 | 2 | 3 | 0 | 0 | 0 | 0 |
| ui.screen.debug | 5 | 0 | 4 | 1 | 0 | 0 | 0 | 0 |
| engine.consumption | 5 | 0 | 3 | 2 | 0 | 0 | 0 | 0 |
| engine.roads | 5 | 0 | 0 | 4 | 0 | 0 | 1 | 0 |
| engine.attract | 5 | 1 | 4 | 0 | 0 | 0 | 0 | 0 |
| engine.tourism | 5 | 2 | 2 | 1 | 0 | 0 | 0 | 0 |
| feat.helper | 5 | 0 | 4 | 0 | 1 | 0 | 0 | 0 |
| tool.bow | 5 | 0 | 4 | 1 | 0 | 0 | 0 | 0 |
| feat.devmode | 5 | 0 | 3 | 0 | 2 | 0 | 0 | 0 |
| data.buildings | 5 | 0 | 3 | 2 | 0 | 0 | 0 | 0 |
| feat.decommission | 5 | 0 | 5 | 0 | 0 | 0 | 0 | 0 |
| int.serializer | 5 | 0 | 5 | 0 | 0 | 0 | 0 | 0 |
| ui.screen.proj | 5 | 0 | 3 | 0 | 2 | 0 | 0 | 0 |
| engine.traffic | 5 | 0 | 4 | 1 | 0 | 0 | 0 | 0 |
| feat.pharmacampus | 5 | 0 | 3 | 2 | 0 | 0 | 0 | 0 |
| tool.worktreeguard | 5 | 0 | 5 | 0 | 0 | 0 | 0 | 0 |
| cloud.azure | 5 | 1 | 1 | 3 | 0 | 0 | 0 | 0 |
| engine.worklife | 5 | 2 | 2 | 1 | 0 | 0 | 0 | 0 |
| engine.tunnels | 4 | 1 | 1 | 2 | 0 | 0 | 0 | 0 |
| engine.market | 4 | 1 | 1 | 2 | 0 | 0 | 0 | 0 |
| data.taxinstruments | 4 | 0 | 2 | 1 | 0 | 0 | 1 | 0 |
| tool.versionguard | 4 | 0 | 4 | 0 | 0 | 0 | 0 | 0 |
| tool.astgate | 4 | 0 | 4 | 0 | 0 | 0 | 0 | 0 |
| feat.factorytypes | 4 | 1 | 2 | 1 | 0 | 0 | 0 | 0 |
| tool.bowautoref | 4 | 0 | 2 | 2 | 0 | 0 | 0 | 0 |
| tool.pingcheck | 4 | 0 | 2 | 2 | 0 | 0 | 0 | 0 |
| tool.agentstop | 4 | 0 | 3 | 1 | 0 | 0 | 0 | 0 |
| tool.authoridentity | 4 | 0 | 4 | 0 | 0 | 0 | 0 | 0 |
| feat.compositionroot | 4 | 0 | 3 | 1 | 0 | 0 | 0 | 0 |
| tool.destructiveguard | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| balance.harness | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| ui.alerts | 3 | 1 | 1 | 0 | 0 | 0 | 1 | 0 |
| tool.quotemask | 3 | 0 | 2 | 1 | 0 | 0 | 0 | 0 |
| tool.authorguard | 3 | 0 | 1 | 0 | 2 | 0 | 0 | 0 |
| int.protocol | 3 | 0 | 0 | 2 | 1 | 0 | 0 | 0 |
| feat.resourcedeposits | 3 | 0 | 1 | 2 | 0 | 0 | 0 | 0 |
| foundation.registry | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| engine.airunits | 3 | 0 | 1 | 2 | 0 | 0 | 0 | 0 |
| engine.fuel | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| tool.precommitcheck | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| tool.codenamecontentscan | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| tool.reflection | 3 | 0 | 2 | 1 | 0 | 0 | 0 | 0 |
| tool.startup | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| tool.gitcommittrigger | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| tool.planchecker | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 |
| tool.secretchecker | 3 | 1 | 1 | 1 | 0 | 0 | 0 | 0 |
| cloud.netpolicy | 3 | 0 | 2 | 1 | 0 | 0 | 0 | 0 |
| data.containerport | 3 | 2 | 0 | 1 | 0 | 0 | 0 | 0 |
| engine.spiral | 3 | 0 | 0 | 3 | 0 | 0 | 0 | 0 |
| _other modules (<3 each)_ | 97 | 10 | 59 | 20 | 5 | 0 | 3 | 0 |
| _unattributed_ | 211 | 23 | 83 | 32 | 3 | 0 | 2 | 68 |

## Story list (class ST — 41 items)

- ASM-188 (unattributed) — git commit -C/-c (reuse another commit's author) is not handled by this guard version
- ASM-214 (data.georef) — AC-13/AC-22 could not be fully closed: no network access to real OS Terrain 50 data/licence file, and AC-22 needs a data/georef.js
- ASM-220 (engine.finance) — engine.consumption's new AC-20 (household utility billing) is scoped as expose-only (a queryable billed amount), not a ledger post
- ASM-224 (tooling) — author-guard checks author/committer for cherry-pick/revert/am/merge by falling back to config/env/-c when no explicit override is
- ASM-228 (unattributed) — alias resolution (BUG-047 fix) only reads the alias target's own leading word; if the alias body itself contains further -c overri
- ASM-230 (unattributed) — git commit -C <commit> / -c <commit> (author/message REUSE flags) remain unhandled, carried forward from v1's ASM-188
- ASM-360 (tool.authorguard) — Guard does not adopt author guard's full quote-aware suffix tokenizer / KNOWN_COMMIT_VERBS -- uses a LOCAL, divergent GIT_TOKEN_RE
- ASM-363 (tool.authorguard) — isCommitInvocation() reuse: findCommitInvocation() stops at FIRST known-commit verb, may miss a chained later commit
- ASM-372 (engine.core) — Reachability guard is a stopgap; the real fix is a runtime HookCount accessor, not more static analysis
- ASM-381 (engine.dispatch) — project .gitignore does not yet list .scratch/, so the tool's own output directory is only excluded by claude-scratch.js's own har
- ASM-382 (engine.capexport) — capexport<->prison edge landed in c36778b but no AC in either file exercises it
- ASM-417 (engine.policies) — Zone-scoped POLICY application is a 4th ResolveScope kind not yet in engine.policies.md's committed ACs -- flagged for Bill, not r
- ASM-444 (feat.debugmode) — FEAT-067 proposed module key feat.weathermode (no code.json entry exists)
- ASM-445 (feat.devmode) — FEAT-065: propose module key feat.devmode (no key registered yet)
- ASM-451 (feat.metricsdash) — FEAT-066 module key proposed as feat.metricsdash (not yet registered in code.json)
- ASM-454 (feat.helper) — Helper module-key split: engine.helper (contract/registry) + feat.helper (panel UI), escalated not decided
- ASM-472 (tool.syncmsg) — tool.syncmsg module-key proposal for FEAT-069's directed-message schema/commands, escalated not decided
- ASM-473 (tool.looparm) — tool.looparm module-key proposal for FEAT-070's standing-loop auto-arm schema/commands, escalated not decided
- ASM-477 (feat.devmode) — feat.metricsdash reuses FEAT-065's feedback-inbox/import mechanism verbatim (AC-8), but claude-devfeedback-import.js hardcodes cod
- ASM-484 (engine.dispatch) — Secret guard's camelCase entropy exemption (BUG-189) still allows ~15pct residual evasion for adversarial letters-only secrets -- 
- ASM-485 (int.protocol) — BLD-1/2/4/7 need Buy/Zone/Build/Demolish protocol kinds; int.protocol vocabulary closed, out of this item's scope
- ASM-500 (engine.finance) — Decommission liability books as a distinct balance-sheet liability/provision account in engine.finance
- ASM-506 (engine.unlocks) — feat.facilitypermits to engine.unlocks edge is absent from code.json; non-purchase routes depend on it
- ASM-514 (feat.megafacilities) — feat.megafacilities inherits permit and decommission gates via buildings.json catalogue plus FEAT-053 and FEAT-054, with no direct
- ASM-527 (ui.screen.proj) — ui.screen.proj error codes provisional: U registry layer exhausted by Sprint-8 wave
- ASM-528 (ui.screen.proj) — ui.screen.proj rate-outlook drill target names finance.baseRate.cycle though source unresolved
- ASM-534 (ui.diagrams) — chrome claims U900-U999 (codes MET-U901..U904); U-layer contended by in-flight dash/diagrams
- ASM-543 (ui.diagrams) — ui.diagrams error code MET-U900 sits in U900-U999 which ui.alerts also claims
- ASM-547 (engine.mining) — engine.mining claims E950-E999; feat.skeleton narrowed E900-E999 to E900-E949
- ASM-571 (engine.farming) — Cross-module transfer (RegisterTransfer) is out of scope for BUG-067 fix
- ASM-611 (engine.invariant) — AC-11 invariant registration is blocked on BUG-058 (no engine.freight to engine.invariant edge in code.json); doc.go states the bl
- ASM-648 (engine.staffing) — KPI source mapping and GDP defined as finance-ledger aggregation; unfilled-jobs needs an out-of-seven staffing edge
- ASM-707 (feat.spaceport) — feat.spaceport (FEAT-097) lands as a feature entry; package path assumed internal/engine/spaceport/ and inbound surface SpaceportA
- ASM-835 (engine.mining) — engine.mining.md predates FEAT-048 scope expansion (resources-design-brief sections 1-3,5,7; new market+finance edges)
- ASM-836 (engine.refuse) — farming/mining/dispatch/refuse Escalations still point at BUG-058 (closed) for edges that remain absent
- ASM-842 (engine.maintenance) — Re-key maintenance/staffing/airunits acceptance files from superseded FEAT-089/090/095 to modules MOD-072/073/074
- ASM-843 (feat.megafacilities) — feat.airport extends heathrow_class_international_airport while feat.megafacilities AC-2 requires it stay byte-equivalent
- ASM-881 (foundation.num) — FEAT-135 mkey NULL; file named feat.secureprimitives.md; foundation.num unregistered in code.json
- ASM-1005 (feat.farmtypes) — feat.farmtypes error range G1600-G1699 (engine.crime took G1500-G1599)
- ASM-1155 (engine.mining) — refinery blight-class half of AC-7 blocked on BUG-058
- ASM-1328 (engine.citizens) — engine.comms defines a local Sector enum mirroring engine.citizens' five buckets rather than importing citizens, because engine.ci

## Duplicate list (class DUP — 1 item)

- ASM-823 — same fork as ASM-875 (engine.tax AC set vs FEAT-056); fold spec-drift note into engine.tax.md when 875 is ruled

## Unattributable list (class UNK — 68 items, manual-triage batch)

Mostly implementer-decision logs whose prose names no module; expect nearly all to re-home into CC/SF once attributed.

ASM-604, ASM-616, ASM-619, ASM-626, ASM-633, ASM-634, ASM-645, ASM-659, ASM-660, ASM-661, ASM-662, ASM-663, ASM-664, ASM-781, ASM-794, ASM-808, ASM-895, ASM-896, ASM-945, ASM-958, ASM-961, ASM-973, ASM-979, ASM-983, ASM-987, ASM-994, ASM-1000, ASM-1050, ASM-1051, ASM-1052, ASM-1085, ASM-1091, ASM-1094, ASM-1098, ASM-1115, ASM-1185, ASM-1203, ASM-1217, ASM-1220, ASM-1238, ASM-1242, ASM-1253, ASM-1258, ASM-1262, ASM-1263, ASM-1273, ASM-1274, ASM-1282, ASM-1283, ASM-1289, ASM-1302, ASM-1307, ASM-1310, ASM-1311, ASM-1363, ASM-1365, ASM-1366, ASM-1374, ASM-1377, ASM-1381, ASM-1384, ASM-1389, ASM-1394, ASM-1397, ASM-1401, ASM-1413, ASM-1418, ASM-1455

## Execution plan (batched; execution is NOT this wave)

Sizing assumes one Sonnet lane processes ~30-50 light closes or ~15-25 substantive folds per dispatch. Every batch: agent re-reads each ASM, re-verifies class and module, writes the fold, then closes via `claude-bow.js done <code> --note` with the fold destination cited. No `git` restore commands ever (GR#24); comments/closes via claude-bow.js only.

| Batch | Scope | Size | Why this order |
|---|---|---|---|
| **1** | **CC-BAL auto-close** — 99 high-conf + 13 verify-first | **112** | Biggest cheap win; zero-interview by standing ruling; one line per module doc. ~3 lanes. |
| **2** | **Prior-map CC fold+close** — the 196 prior CC items using the ready-made one-line preserve texts in Appendix A | 196 | Fold text already written and approved 2026-08-14; mechanical. ~4-5 lanes. |
| **3** | **New CC by module** — descending module size (Appendix B roster); shared-standard folds first: copy-guard standard, error-range/registry claims, SEC finite-guard scoping conventions each get ONE standard paragraph their items cite | ~464 | Per-module batching keeps each lane inside one acceptance file; the three shared standards collapse dozens of near-duplicate folds. ~10-12 lanes across waves. |
| **4** | **SF spec-folds by target acceptance file** (Appendix B roster; prior 34 use Appendix A text) | 306 | Substantive spec amendments; GR#25 applies — any fold that would add a cross-module dependency goes through Architect edge-registration first. ~12-15 lanes. |
| **5** | **ST conversions** — 41 items → new BOW items / Bill rulings (register-guid ×6, error-layer reallocations ×5, master-plan edge amendments ×~8, guard-gap bugs ×~6, spec re-keys, rest) | 41 | Needs Bill/lead attention per item; batch by theme. |
| **6** | **UNK triage** — manual module attribution + reclassification (most look CC-shaped) | 68 | Cheap once eyes-on; feeds back into batches 3/4. |
| **7** | **AD** — the 16-item Aaron list, one sitting | 16 | Stays open until Aaron rules; rulings then cascade DUP/SF closes (e.g. ASM-823). |

DUP (1 item, ASM-823) closes inside batch 7's cascade.

**Standing risks noted for executors:**
- The classifier's module attribution is heuristic and the mkey column is empty — when the re-read shows a different owner, fold there and say so in the close note.
- Amendment A1's lesson applies to batch 1's med-confidence rows: a "placeholder" that is actually a soundness gap is a FIX/bug, not a balance close.
- New ASMs keep arriving while batches run (~180/day during heavy waves). Re-pull the open set at each batch start; this map covers ASMs created ≤ 2026-08-17.

---

# Appendix B — Execution rosters (new-population codes by module)

## SF items by target acceptance file

- unattributed (32): ASM-053, ASM-069, ASM-074, ASM-077, ASM-089, ASM-190, ASM-601, ASM-653, ASM-975, ASM-992, ASM-996, ASM-1023, ASM-1044, ASM-1054, ASM-1059, ASM-1067, ASM-1095, ASM-1105, ASM-1108, ASM-1118, ASM-1212, ASM-1225, ASM-1255, ASM-1300, ASM-1319, ASM-1323, ASM-1327, ASM-1348, ASM-1368, ASM-1379, ASM-1420, ASM-1422
- engine.refuse (11): ASM-078, ASM-1045, ASM-1057, ASM-1058, ASM-1068, ASM-1070, ASM-1077, ASM-1090, ASM-1092, ASM-1111, ASM-1117
- engine.citizens (10): ASM-273, ASM-827, ASM-891, ASM-968, ASM-998, ASM-1039, ASM-1056, ASM-1160, ASM-1330, ASM-1344
- feat.megafacilities (9): ASM-512, ASM-684, ASM-708, ASM-716, ASM-816, ASM-867, ASM-868, ASM-905, ASM-1181
- engine.wellbeing (9): ASM-999, ASM-1021, ASM-1106, ASM-1110, ASM-1119, ASM-1126, ASM-1129, ASM-1165, ASM-1343
- engine.freight (8): ASM-608, ASM-686, ASM-830, ASM-908, ASM-969, ASM-1087, ASM-1174, ASM-1269
- engine.finance (7): ASM-155, ASM-767, ASM-770, ASM-818, ASM-941, ASM-965, ASM-1153
- engine.logistics (7): ASM-191, ASM-610, ASM-840, ASM-1022, ASM-1069, ASM-1267, ASM-1324
- engine.dispatch (7): ASM-428, ASM-585, ASM-586, ASM-655, ASM-754, ASM-756, ASM-832
- engine.core (6): ASM-066, ASM-067, ASM-100, ASM-826, ASM-1213, ASM-1320
- engine.firms (6): ASM-609, ASM-699, ASM-970, ASM-1175, ASM-1325, ASM-1332
- engine.chemicals (5): ASM-487, ASM-702, ASM-706, ASM-828, ASM-930
- engine.services (5): ASM-638, ASM-807, ASM-1314, ASM-1318, ASM-1347
- engine.education (5): ASM-1079, ASM-1168, ASM-1177, ASM-1240, ASM-1429
- engine.roads (4): ASM-208, ASM-652, ASM-1453, ASM-1454
- engine.destination (4): ASM-326, ASM-1018, ASM-1073, ASM-1426
- engine.fdi (4): ASM-488, ASM-698, ASM-1136, ASM-1167
- engine.maintenance (4): ASM-588, ASM-802, ASM-1227, ASM-1322
- engine.projections (4): ASM-614, ASM-1071, ASM-1367, ASM-1369
- feat.commoditymarket (4): ASM-696, ASM-700, ASM-704, ASM-866
- engine.invariant (4): ASM-825, ASM-841, ASM-1184, ASM-1407
- engine.spaceport (4): ASM-1130, ASM-1139, ASM-1141, ASM-1166
- engine.capexport (4): ASM-1316, ASM-1345, ASM-1349, ASM-1361
- int.solver (3): ASM-073, ASM-844, ASM-845
- engine.mining (3): ASM-210, ASM-691, ASM-1442
- engine.leisure (3): ASM-315, ASM-1064, ASM-1065
- engine.staffing (3): ASM-587, ASM-637, ASM-809
- engine.tax (3): ASM-595, ASM-597, ASM-1219
- feat.farmtypes (3): ASM-676, ASM-1006, ASM-1048
- engine.farming (3): ASM-679, ASM-833, ASM-1008
- foundation.data (3): ASM-847, ASM-939, ASM-1445
- harness.replay (3): ASM-855, ASM-856, ASM-1341
- cloud.azure (3): ASM-918, ASM-919, ASM-922
- feat.refinery (3): ASM-928, ASM-1152, ASM-1260
- feat.minetypes (3): ASM-1002, ASM-1003, ASM-1037
- engine.rail (3): ASM-1028, ASM-1270, ASM-1276
- engine.crime (3): ASM-1034, ASM-1043, ASM-1055
- engine.airport (3): ASM-1147, ASM-1148, ASM-1164
- engine.spiral (3): ASM-1232, ASM-1233, ASM-1435
- feat.saveux (3): ASM-1334, ASM-1335, ASM-1388
- feat.checkpoint (3): ASM-1339, ASM-1340, ASM-1373
- engine.consumption (2): ASM-136, ASM-1128
- engine.market (2): ASM-486, ASM-607
- engine.news (2): ASM-518, ASM-556
- int.protocol (2): ASM-531, ASM-535
- engine.policies (2): ASM-565, ASM-942
- feat.opexintegration (2): ASM-590, ASM-1226
- data.catalogue (2): ASM-599, ASM-1317
- harness.synth (2): ASM-615, ASM-853
- engine.unlocks (2): ASM-635, ASM-1446
- data.buildings (2): ASM-636, ASM-1149
- engine.airunits (2): ASM-641, ASM-938
- engine.tunnels (2): ASM-690, ASM-705
- tool.bowautoref (2): ASM-718, ASM-916
- tool.pingcheck (2): ASM-724, ASM-947
- feat.resourcedeposits (2): ASM-811, ASM-909
- engine.shopping (2): ASM-817, ASM-1104
- engine.world (2): ASM-820, ASM-1038
- engine.season (2): ASM-821, ASM-1101
- harness.headless (2): ASM-848, ASM-860
- foundation.num (2): ASM-872, ASM-1001
- feat.pharmacampus (2): ASM-927, ASM-1163
- feat.extraction (2): ASM-1040, ASM-1424
- engine.accelerator (2): ASM-1123, ASM-1124
- tool.sync (1): ASM-009
- ui.screen.debug (1): ASM-093
- ui.core (1): ASM-530
- ui.dash (1): ASM-538
- ui.diagrams (1): ASM-544
- data.unlocktrees (1): ASM-598
- data.market (1): ASM-612
- tool.bow (1): ASM-721
- tool.statusline (1): ASM-727
- tool.memoryprefetch (1): ASM-729
- tool.quotemask (1): ASM-771
- tool.secretchecker (1): ASM-773
- tool.trailerchecker (1): ASM-776
- tool.versionchecker (1): ASM-778
- foundation.repo (1): ASM-791
- future.slots (1): ASM-792
- foundation.errors (1): ASM-812
- engine.tourism (1): ASM-824
- feat.compositionroot (1): ASM-887
- tool.fileclaimguard (1): ASM-901
- cloud.netpolicy (1): ASM-920
- feat.secureprimitives.md (1): ASM-924
- tool.agentstop (1): ASM-925
- feat.channeltunnel (1): ASM-926
- feat.helicopters.md (1): ASM-940
- feat.citycensus.md (1): ASM-943
- tool.reflection (1): ASM-946
- engine.build (1): ASM-966
- data.taxinstruments (1): ASM-976
- feat.factorytypes (1): ASM-989
- feat.securehelpers (1): ASM-995
- feat.containerport (1): ASM-1029
- data.containerport (1): ASM-1033
- engine.traffic (1): ASM-1042
- engine.prison (1): ASM-1053
- feat.facilitypermits (1): ASM-1151
- engine.census (1): ASM-1169
- engine.worklife (1): ASM-1172
- data.errors (1): ASM-1245
- engine.social (1): ASM-1410
- engine.households (1): ASM-1430
- engine.coastal (1): ASM-1431
- engine.defence (1): ASM-1440

## CC items by module

- unattributed (83): ASM-126, ASM-145, ASM-168, ASM-185, ASM-187, ASM-218, ASM-225, ASM-227, ASM-253, ASM-287, ASM-290, ASM-337, ASM-342, ASM-345, ASM-348, ASM-373, ASM-384, ASM-388, ASM-390, ASM-424, ASM-429, ASM-432, ASM-433, ASM-434, ASM-466, ASM-508, ASM-509, ASM-510, ASM-517, ASM-522, ASM-550, ASM-551, ASM-554, ASM-555, ASM-558, ASM-566, ASM-568, ASM-569, ASM-570, ASM-572, ASM-573, ASM-577, ASM-596, ASM-602, ASM-605, ASM-617, ASM-620, ASM-640, ASM-654, ASM-658, ASM-874, ASM-884, ASM-960, ASM-967, ASM-978, ASM-1046, ASM-1084, ASM-1183, ASM-1207, ASM-1210, ASM-1246, ASM-1259, ASM-1261, ASM-1272, ASM-1284, ASM-1288, ASM-1292, ASM-1293, ASM-1306, ASM-1309, ASM-1321, ASM-1351, ASM-1354, ASM-1356, ASM-1375, ASM-1378, ASM-1380, ASM-1393, ASM-1398, ASM-1414, ASM-1416, ASM-1448, ASM-1456
- tooling (22): ASM-186, ASM-229, ASM-341, ASM-344, ASM-350, ASM-351, ASM-366, ASM-367, ASM-368, ASM-378, ASM-380, ASM-389, ASM-425, ASM-582, ASM-624, ASM-625, ASM-657, ASM-755, ASM-766, ASM-894, ASM-944, ASM-1395
- engine.invariant (19): ASM-205, ASM-282, ASM-437, ASM-567, ASM-593, ASM-647, ASM-814, ASM-1093, ASM-1120, ASM-1200, ASM-1275, ASM-1278, ASM-1281, ASM-1294, ASM-1305, ASM-1370, ASM-1386, ASM-1396, ASM-1415
- ui.diagrams (13): ASM-279, ASM-536, ASM-537, ASM-539, ASM-540, ASM-541, ASM-618, ASM-621, ASM-622, ASM-623, ASM-852, ASM-861, ASM-864
- ui.dash (13): ASM-280, ASM-521, ASM-546, ASM-591, ASM-606, ASM-627, ASM-628, ASM-629, ASM-631, ASM-862, ASM-869, ASM-1433, ASM-1444
- engine.citizens (12): ASM-181, ASM-242, ASM-644, ASM-795, ASM-819, ASM-822, ASM-1063, ASM-1100, ASM-1102, ASM-1204, ASM-1244, ASM-1290
- engine.freight (12): ASM-251, ASM-667, ASM-669, ASM-682, ASM-1007, ASM-1146, ASM-1256, ASM-1268, ASM-1271, ASM-1277, ASM-1286, ASM-1303
- engine.wellbeing (12): ASM-712, ASM-810, ASM-956, ASM-1062, ASM-1121, ASM-1122, ASM-1127, ASM-1187, ASM-1243, ASM-1247, ASM-1385, ASM-1400
- data.catalogue (10): ASM-138, ASM-496, ASM-511, ASM-692, ASM-693, ASM-694, ASM-1010, ASM-1248, ASM-1251, ASM-1436
- ui.widgets (9): ASM-252, ASM-422, ASM-478, ASM-520, ASM-545, ASM-643, ASM-859, ASM-1449, ASM-1450
- feat.checkpoint (9): ASM-443, ASM-1336, ASM-1337, ASM-1350, ASM-1352, ASM-1353, ASM-1355, ASM-1371, ASM-1372
- tool.fileclaimguard (9): ASM-898, ASM-899, ASM-900, ASM-902, ASM-903, ASM-904, ASM-962, ASM-963, ASM-988
- engine.education (8): ASM-231, ASM-815, ASM-1060, ASM-1072, ASM-1081, ASM-1140, ASM-1198, ASM-1236
- engine.logistics (8): ASM-235, ASM-1026, ASM-1027, ASM-1089, ASM-1096, ASM-1186, ASM-1331, ASM-1357
- engine.unlocks (8): ASM-258, ASM-493, ASM-497, ASM-505, ASM-507, ASM-513, ASM-1223, ASM-1329
- tool.codebaseviz (8): ASM-793, ASM-803, ASM-893, ASM-937, ASM-949, ASM-950, ASM-951, ASM-991
- engine.chemicals (8): ASM-839, ASM-990, ASM-1156, ASM-1157, ASM-1190, ASM-1202, ASM-1285, ASM-1298
- foundation.data (7): ASM-082, ASM-201, ASM-221, ASM-462, ASM-498, ASM-769, ASM-846
- harness.synth (7): ASM-170, ASM-172, ASM-385, ASM-457, ASM-549, ASM-858, ASM-1439
- engine.core (7): ASM-173, ASM-200, ASM-336, ASM-526, ASM-650, ASM-813, ASM-1383
- engine.firms (7): ASM-245, ASM-838, ASM-964, ASM-1252, ASM-1358, ASM-1376, ASM-1419
- engine.news (7): ASM-270, ASM-271, ASM-603, ASM-1209, ASM-1231, ASM-1421, ASM-1425
- tool.sync (7): ASM-356, ASM-732, ASM-734, ASM-741, ASM-1191, ASM-1299, ASM-1359
- engine.mining (7): ASM-503, ASM-672, ASM-674, ASM-1188, ASM-1264, ASM-1265, ASM-1266
- feat.megafacilities (7): ASM-515, ASM-516, ASM-713, ASM-876, ASM-877, ASM-929, ASM-1138
- engine.defence (6): ASM-026, ASM-061, ASM-383, ASM-1201, ASM-1205, ASM-1434
- feat.saveux (6): ASM-260, ASM-261, ASM-262, ASM-263, ASM-339, ASM-441
- engine.dispatch (6): ASM-338, ASM-370, ASM-371, ASM-420, ASM-584, ASM-986
- engine.build (6): ASM-561, ASM-562, ASM-780, ASM-831, ASM-977, ASM-1447
- feat.containerport (6): ASM-675, ASM-687, ASM-1011, ASM-1083, ASM-1088, ASM-1097
- feat.refinery (6): ASM-701, ASM-1173, ASM-1287, ASM-1296, ASM-1308, ASM-1313
- engine.maintenance (6): ASM-806, ASM-889, ASM-936, ASM-1228, ASM-1280, ASM-1301
- foundation.num (6): ASM-982, ASM-985, ASM-1017, ASM-1019, ASM-1030, ASM-1206
- int.solver (5): ASM-083, ASM-594, ASM-797, ASM-805, ASM-854
- engine.world (5): ASM-427, ASM-438, ASM-1427, ASM-1438, ASM-1441
- feat.metricsdash (5): ASM-452, ASM-476, ASM-857, ASM-878, ASM-897
- data.unlocktrees (5): ASM-481, ASM-482, ASM-574, ASM-575, ASM-576
- feat.facilitypermits (5): ASM-499, ASM-865, ASM-1031, ASM-1131, ASM-1154
- feat.decommission (5): ASM-501, ASM-1075, ASM-1179, ASM-1182, ASM-1312
- int.serializer (5): ASM-525, ASM-632, ASM-651, ASM-717, ASM-798
- data.errors (5): ASM-578, ASM-1112, ASM-1362, ASM-1404, ASM-1408
- engine.census (5): ASM-642, ASM-1159, ASM-1199, ASM-1214, ASM-1346
- engine.projections (5): ASM-649, ASM-1399, ASM-1402, ASM-1403, ASM-1405
- tool.worktreeguard (5): ASM-750, ASM-751, ASM-752, ASM-753, ASM-931
- engine.refuse (5): ASM-879, ASM-1020, ASM-1113, ASM-1114, ASM-1116
- engine.social (5): ASM-1391, ASM-1392, ASM-1406, ASM-1409, ASM-1412
- engine.attract (4): ASM-246, ASM-613, ASM-1109, ASM-1211
- ui.screen.debug (4): ASM-255, ASM-421, ASM-524, ASM-1193
- feat.helper (4): ASM-379, ASM-456, ASM-474, ASM-1390
- tool.versionguard (4): ASM-426, ASM-748, ASM-749, ASM-932
- tool.bow (4): ASM-430, ASM-436, ASM-912, ASM-913
- tool.astgate (4): ASM-431, ASM-435, ASM-873, ASM-883
- engine.finance (4): ASM-502, ASM-768, ASM-1221, ASM-1437
- engine.traffic (4): ASM-592, ASM-957, ASM-1086, ASM-1161
- engine.rail (4): ASM-685, ASM-1074, ASM-1076, ASM-1082
- engine.policies (4): ASM-782, ASM-783, ASM-955, ASM-1239
- tool.authoridentity (4): ASM-788, ASM-789, ASM-790, ASM-911
- harness.headless (4): ASM-800, ASM-850, ASM-851, ASM-885
- engine.accelerator (4): ASM-981, ASM-1134, ASM-1158, ASM-1197
- engine.leisure (4): ASM-1061, ASM-1066, ASM-1078, ASM-1080
- tool.destructiveguard (3): ASM-193, ASM-340, ASM-362
- engine.season (3): ASM-203, ASM-239, ASM-579
- balance.harness (3): ASM-264, ASM-265, ASM-266
- engine.tax (3): ASM-283, ASM-563, ASM-1215
- feat.extraction (3): ASM-349, ASM-405, ASM-492
- feat.devmode (3): ASM-447, ASM-449, ASM-935
- data.buildings (3): ASM-495, ASM-668, ASM-1015
- ui.screen.proj (3): ASM-529, ASM-583, ASM-630
- foundation.registry (3): ASM-557, ASM-882, ASM-993
- engine.airport (3): ASM-666, ASM-984, ASM-1150
- feat.minetypes (3): ASM-670, ASM-671, ASM-906
- feat.pharmacampus (3): ASM-695, ASM-1132, ASM-1135
- engine.fuel (3): ASM-714, ASM-1257, ASM-1297
- tool.precommitcheck (3): ASM-730, ASM-731, ASM-948
- tool.codenamecontentscan (3): ASM-736, ASM-737, ASM-742
- tool.startup (3): ASM-744, ASM-746, ASM-747
- tool.gitcommittrigger (3): ASM-760, ASM-761, ASM-915
- tool.planchecker (3): ASM-762, ASM-763, ASM-914
- tool.agentstop (3): ASM-786, ASM-787, ASM-910
- feat.compositionroot (3): ASM-880, ASM-886, ASM-1417
- engine.consumption (3): ASM-959, ASM-1025, ASM-1180
- engine.fdi (3): ASM-1133, ASM-1178, ASM-1196
- feat.georef (2): ASM-092, ASM-146
- harness.replay (2): ASM-149, ASM-442
- plan.pipeline (2): ASM-197, ASM-656
- engine.coastal (2): ASM-324, ASM-1142
- tool.quotemask (2): ASM-357, ASM-772
- data.taxinstruments (2): ASM-418, ASM-423
- feat.farmtypes (2): ASM-677, ASM-907
- engine.farming (2): ASM-678, ASM-1250
- feat.factorytypes (2): ASM-680, ASM-681
- engine.spaceport (2): ASM-709, ASM-1144
- engine.tourism (2): ASM-710, ASM-1143
- feat.particleaccelerator (2): ASM-711, ASM-715
- tool.bowautoref (2): ASM-719, ASM-917
- tool.bowrefcheck (2): ASM-720, ASM-722
- tool.pingcheck (2): ASM-723, ASM-725
- tool.prepushcheck (2): ASM-733, ASM-735
- tool.codenamepatterns (2): ASM-739, ASM-740
- tool.reflection (2): ASM-743, ASM-745
- tool.dispatchlog (2): ASM-758, ASM-759
- tool.pushverify (2): ASM-764, ASM-765
- cloud.netpolicy (2): ASM-799, ASM-934
- feat.commoditymarket (2): ASM-829, ASM-1176
- engine.services (2): ASM-834, ASM-837
- legacy.appskeleton (2): ASM-921, ASM-923
- engine.worklife (2): ASM-952, ASM-1125
- data.tax_instruments (2): ASM-972, ASM-1222
- engine.destination (2): ASM-1014, ASM-1249
- engine.crime (2): ASM-1035, ASM-1208
- engine.fiscal (2): ASM-1218, ASM-1241
- engine.capexport (2): ASM-1338, ASM-1360
- tool.codenameguard (1): ASM-199
- data.seasonal (1): ASM-222
- ui.keys (1): ASM-254
- tool.authorguard (1): ASM-359
- engine.market (1): ASM-377
- tool.secretguard (1): ASM-396
- ui.screen.map (1): ASM-463
- feat.debugmode (1): ASM-467
- tool.sprintgate (1): ASM-483
- data.commoditymarket (1): ASM-491
- data.decommission (1): ASM-504
- harness.stub (1): ASM-519
- engine.save (1): ASM-523
- ui.alerts (1): ASM-533
- ui.core (1): ASM-542
- feat.resourcedeposits (1): ASM-548
- ui.screen.demo (1): ASM-559
- ui.screen.build (1): ASM-560
- feat.deathservices (1): ASM-580
- feat.deathwave (1): ASM-581
- foundation.errors (1): ASM-665
- feat.channeltunnel (1): ASM-688
- engine.tunnels (1): ASM-689
- tool.statusline (1): ASM-726
- tool.memoryprefetch (1): ASM-728
- tool.codenamediff (1): ASM-738
- engine.debug (1): ASM-757
- tool.secretchecker (1): ASM-775
- tool.trailerchecker (1): ASM-777
- tool.versionchecker (1): ASM-779
- feat.activitylog (1): ASM-785
- cloud.gpu (1): ASM-796
- cloud.azure (1): ASM-801
- ui.screen.services (1): ASM-870
- feat.securehelpers (1): ASM-871
- feat.maintenance (1): ASM-888
- engine.airunits (1): ASM-892
- tool.codenamehook (1): ASM-933
- engine.households (1): ASM-974
- ui.screen.districts (1): ASM-980
- feat.faith (1): ASM-997
- feat.hobbies (1): ASM-1013
- ui.screen.trade (1): ASM-1192
- engine.staffing (1): ASM-1230
- engine.checkpoint (1): ASM-1387
- ui.screen.menu (1): ASM-1443

---

# Appendix A — Prior disposition map (2026-08-14, APPROVED WITH AMENDMENTS A1–A4)

> **Counts in this appendix are SUPERSEDED** by the 2026-08-17 re-baseline above. The per-item bucket assignments and one-line fold texts remain AUTHORITATIVE for the 324 prior-mapped items still open (the 4 FIX items ASM-353/355/374/375 and 1 CC item have closed since — do not re-close). Batch 2 executes directly from these tables.

### Per-module disposition table

### tooling / guards (author, destructive, secret, astgate, planning, codename)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-185 | CC | — | Sanctioned identity = union(config email, trunk emails ≥3×, env list); config trusted, history needs ≥3 to block self-grandfathering. |
| ASM-186 | CC | — | New-contributor extension is an operator env var, not a committed file (mirrors CLAUDE_DISABLE_*_GUARD). |
| ASM-187 | CC | — | `git rebase` out of scope by construction (no git-commit porcelain invocation to match). |
| ASM-188 | ST | BUG — author-guard `-C`/`-c` reuse flags unhandled | Flags pull an arbitrary other commit's author; unhandled → fabricated-identity slip. |
| ASM-224 | ST | BUG — cherry-pick/am true-author not inspected | Guard falls back to config/env/-c; the picked commit's real inherited author is invisible to a text hook. |
| ASM-226 | CC | — | HISTORY_SCAN_LIMIT (2000) hardcoded; superseded by ASM-577/578 (now derived from repo commit count, ceiling 2000). |
| ASM-227 | CC | — | Deny reasons withhold ALL sanctioned addresses (field name+count only) — BUG-042 history justifies zero disclosure. |
| ASM-228 | ST | BUG — alias body `-c`/wrapper not re-parsed | `alias.ci=!git -c user.email=x commit` resolves to `!git`, missed by KNOWN_COMMIT_VERBS. |
| ASM-229 | CC | — | Wrapper list (bash/sh/zsh/dash/ksh/pwsh/cmd) covers this env; new wrapper added on evidence. |
| ASM-230 | ST | BUG — `-C`/`-c` reuse flags (carried fwd from ASM-188) | Same gap re-confirmed; needs `<rev>` author resolution. |
| ASM-350 | CC | — | buildQuoteMask is a toggle approximating a shell lexer, not sound; documented structural limit, fail-closed. |
| ASM-357 | CC | — | Path-prefix widening covers env-var/8.3/relative/UNC shapes; residual: command-substitution + renamed/symlinked binary. |
| ASM-344 | CC | — | Round-4 fixed backslash-outside-quote + heredoc parity; residual "not a full lexer" claim logged. |
| ASM-345 | CC | — | unescapeDoubleQuoted relies on WRAPPER_PATTERNS capture grammar (\. pairs); lone trailing backslash unreachable. |
| ASM-351 | CC | — | Unterminated heredoc swallows to EOF as inert (shell wouldn't reach past it either); documented. |
| ASM-225 | CC | — | KNOWN_COMMIT_VERBS includes `merge` (same config/env derivation as commit). |
| ASM-366 | CC | — | Node-authored commit-msg hook execution on Windows git not verified; AC-12 install test catches it. |
| ASM-577 | CC | — | History-scan cap derived from `git rev-list --count HEAD`, capped 2000, env-overridable. |
| ASM-578 | CC | — | Failed derivation degrades fail-open to the 2000 ceiling (not a registry error — FEAT-045 AC-8 fail-open). |
| ASM-193 | CC | — | destructive-guard scope = plain `git commit` only (lead-accepted; re-examine when merge-with-new-code first occurs). |
| ASM-340 | CC | — | Same scope narrowing restated for destructive-guard. |
| ASM-341 | CC | — | process.cwd() (not __dirname) — test-harness isolation; wrong-cwd degrades to "deny all", not allow. |
| ASM-348 | CC | — | Alias resolving to literal `commit` treated as commit (risk points toward over-deny, safe). |
| ASM-349 | CC | — | GIT_TOKEN_RE quoted-path tolerance covers executable prefix only; suffix extraction via extractMessage. |
| ASM-359 | CC | — | isCommitInvocation = literal `'commit'`, deliberately NOT author-guard's KNOWN_COMMIT_VERBS set. |
| ASM-360 | ST | FEAT — single source-of-truth GIT_TOKEN_RE + cross-file parity test | Two local divergent regex variants will drift; false-negative (gate silently exempts) risk. |
| ASM-362 | CC | — | Env-var path override (CLAUDE_DESTRUCTIVE_GUARD_*_PATH) is test-only seam, defaults to real siblings. |
| ASM-363 | ST | BUG — findCommitInvocation stops at first known verb | `git cherry-pick X; git commit …` misses the later real commit. |
| ASM-342 | CC | — | bow_destructive_verdicts stores classes/findings as comma-joined VARCHAR; fine at current volume. |
| ASM-356 | CC | — | buildQuoteMask copied 4× (lead ACCEPTED: guards must stay independently loadable); drift test exists. |
| ASM-367 | CC | — | discoverCopies scans source-pattern (not hardcoded list); misses renamed copies (documented). |
| ASM-368 | CC | — | CRLF heredoc case = cross-copy agreement only; promote to golden when BUG-081 lands. |
| ASM-425 | CC | — | AC-3 non-regression count scales with live discoverCopies (2 files), not stale 5. |
| ASM-424 | CC | — | BUG-091 fixture drops trailing quote to isolate backslash boundary from ASM-351. |
| ASM-432 | CC | — | SEC-021 exemption boundary = lowercase+digit segments only, hyphen/underscore-split. |
| ASM-433 | CC | — | SEC-021 base64/hex fixtures are BA-authored placeholders, not real credentials. |
| ASM-396 | CC | — | BUG-088 checker-module filenames left to junior (AC-B5 header doc makes name irrelevant). |
| ASM-405 | CC | — | claude-plan-checker hashFiles uses ASCII space separator (was NUL); hash has no fixed expected value. |
| ASM-484 | ST | FEAT — secret-guard second detection layer | camelCase entropy exemption leaves ~15% adversarial evasion (~1000× worse than SEC-021's ~1/7000). |
| ASM-381 | ST | chore — add `.scratch/` to `.gitignore` | Tool's output dir only excluded by its own hardcoded filter; git sees it as untracked noise. |
| ASM-382 | ST | AC — prison-places export (engine.capexport↔engine.prison) | Edge landed in c36778b with zero consuming AC; needs a new AC to re-arm. |
| ASM-384 | CC | — | push-verify defaults (60s/30min/5s/3) are CLI-overridable guesses, not measured. |
| ASM-388 | CC | — | Settle strategy = 2-consecutive-poll count stability (not fixed settle time). |
| ASM-389 | CC | — | Junction-directory reparse-point detection; bare file-symlink variant left unverified/out of scope. |
| ASM-390 | CC | — | SETTLE_FLOOR_MS=3000 unmeasured; overridable. |
| ASM-378 | CC | — | Scratch timestamp folders: local time, colon-free HHMMSS (Windows-illegal colons). |
| ASM-379 | CC | — | gitignore honouring delegated to `git status --porcelain -uall`, no hand-rolled parser. |
| ASM-380 | CC | — | CLI shape = subcommand (`snapshot`), unknown subcommand → usage+exit 1. |
| ASM-383 | CC | — | `gh run list --commit` sole source; fails loud (exit 2) if flag renamed. |
| ASM-385 | CC | — | Exit 2 collapsed for all could-not-verify causes; stderr distinguishes subtype. |
| ASM-430 | CC | — | BUG-090 `--desc-file` scoped to note+detail flags only. |
| ASM-431 | CC | — | Shell-char warning kept advisory (Bill may want hard gate — P1). |
| ASM-436 | CC | — | `--note-file` ported to depend/ref/done/destructive (comment has no --note). |
| ASM-483 | CC | — | FEAT-061 check-2 deliberately defers FEAT-062 runAudit reuse (scope/cost mismatch; logged). |
| ASM-197 | CC | — | tool.* guard deps=[plan.pipeline] blanket convention; one-line fix if wrong. |
| ASM-199 | CC | — | New tool.* items typed 'feature', seq from gaps, P1/P2 split — Bill can re-set. |
| ASM-281 | AD | — | Call-edge DIRECTION inferred from prose may be backwards; needs per-candidate architect ruling before master-plan edit. |
| ASM-282 | CC | — | Shared-specRef heuristic (174 false-positive pairs) not load-bearing; move to structured collaborations field. |
| ASM-306 | AD | — | Which module owns the registered 'goods' conservation stock — market vs logistics (or both). |
| ASM-462 | CC | — | foundation.data registered at Go package `internal/foundation/data/`, not the `data/` JSON dir. |
| ASM-463 | CC | — | Test-only fixture imports NOT registered as call edges (correctly decoupled). |
| ASM-557 | CC | — | feat.debugmode stale foundation.registry edge removed (consumed by ui.screen.debug, not engine debug). |
| ASM-205 | CC | — | astgate can't derive "helper reached via guarded caller"/"scalar accessor" as safe; live findings hand-triaged. |
| ASM-435 | CC | — | SEC-048 prefers errs.New/F700-799 conversion over exemption comment (real CI gate). |
| ASM-437 | CC | — | SEC-048 correlation IDs minted inline at 3 sites, not threaded through Run. |
| ASM-457 | CC | — | astgate ratchet keys findings by exact violation message text (stable identifier). |
| ASM-466 | CC | — | Stale AND fabricated allowlist entries both hard-fail (fix = code + allowlist removal same commit). |
| ASM-420 | CC | — | BUG-053 already AST-fixed in place; re-homing into astgate optional (split as follow-up if Bill prefers narrow). |

### int.protocol / transport / solver / serialize / errs / registry

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-009 | SF | int.protocol.md — shutdown contract | Close() blocks via RWMutex until in-flight senders finish; invalidated if any future sender becomes blocking under RLock. |
| ASM-026 | CC | — | NUL-byte deferral now logged as this ASM; Go os layer fails closed on both GOOS (sound). |
| ASM-149 | CC | — | ReadShard byte bound is a per-caller parameter (16MiB replay / 0 metctl) per SEC-033 lesson. |
| ASM-061 | CC | — | cmd/metctl main.go:74 `%s` on FormatVersion safe only via ParseSemVer Atoi gate; verified empirically, now logged. |
| ASM-074 | SF | copy-guard standard | errs.Logger copy-guard uses plain sentinel (not errs.New) to avoid sink recursion; rejected Log() → in-memory ring. |
| ASM-126 | CC | — | SEC-033 flood budget 500ms ≈19× measured 26.6ms; tripwire not SLA; re-verify at real scale. |
| ASM-069 | SF | copy-guard standard | SetStatus guard lives in setStatusLocked (the sole mu.Lock site), not the delegating SetStatus. |
| ASM-073 | SF | copy-guard standard | solver.Registry Register/SetFailoverHook now return error; MET-F400 first free F4xx code. |
| ASM-559 | CC | — | MaxRequestPayloadBytes = 1 MiB (~4 orders over any reference payload; matches ui 1MiB bound). |
| ASM-560 | CC | — | Buy/Zone/Build/Demolish use single-cell CellRef; multi-cell = one command per cell. |
| ASM-561 | CC | — | ZoneType/BuildingType are opaque engine-defined strings resolved engine-side. |
| ASM-562 | CC | — | Demolish payload carries no cost; compensation engine-computed and returned in result. |
| ASM-485 | ST | FEAT — int.protocol Buy/Zone/Build/Demolish Kinds | BLD-1/2/4/7 assume purchase/zone/build/demolish command vocabulary; protocol has only 8 skeleton kinds. |
| ASM-100 | SF | harness.replay.md — premature-close | EnginePlayer tracks via results counter, not literal channel-close-ordering (owns its Commands channel). |
| ASM-145 | CC | — | maxFixtureDecodedBytes=16MiB (~3 orders over the 13KB real fixture); re-derive if a larger fixture appears. |
| ASM-146 | CC | — | maxPatchWireBytes=2× maxGridBudgetBytes (150MB); chosen wire-overhead multiplier. |
| ASM-426 | CC | — | SEC-040 exemption comment placed in gen/main.go's existing header doc block. |

### engine.world (MOD-017)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-214 | ST | feat — close AC-22: wire data/georef.json to terrain50 tiles | Data+OGL licence committed (81f471c); only the sanctioned georef.json edit remains. |
| ASM-208 | SF | engine.world.md — offmap junction | M20/J13 placement is a heightmap-variance heuristic, not real OS Open Roads; treat CellLocal as placeholder. |
| ASM-209 | CC | — (close citing balance-number regime) | Tile price base 10000 + linear factors = placeholder, not tuned economy. |
| ASM-211 | CC | — (close citing balance-number regime) | Slope CostMultipliers (1.0/1.4/2.2/+Inf) are Sprint-3 placeholders. |
| ASM-290 | CC | — | SEC-043 headline test pins one corridor/escarpment band; identity-gutting regression already covered by assertion 5. |
| ASM-428 | SF | engine.world.md AC-28 | WorldAPI guard list (11 methods) must be re-derived live at fix time, not trusted from this doc. |
| ASM-429 | CC | — | SEC-043 does not reproduce on current tree (assertion 5 already catches it); verify-only Tester pass. |
| ASM-434 | CC | — | Curve-pinning uses 2 control points (0.75/0.375) — minimum closing BUG-065's two counterexamples. |
| ASM-438 | CC | — | IsProspected/GeologyBaseline/OffMapConnections gained error return (mirrors core.Clock); no consumer exists yet. |
| ASM-427 | CC | — | World copy-guard field `self`/ErrWorldCopied mirrors Engine pattern verbatim (astgate name-match). |
| ASM-210 | SF | engine.world.md AC-6 | Geology modelled per-tile (2km) not per-cell (10m) to keep Cell core ~30 bytes. |
| ASM-291 | CC | — (close citing balance-number regime) | Geology pocket probabilities (h%5/7/11) are an unreviewed build-time choice, not real Kent proportions. |

### engine.tax / FEAT-056 (data/tax_instruments.json)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-283 | CC | — | tax_instruments.json filename ACCEPTED (lead ruling; naming-by-convention). |
| ASM-287 | CC | — | Per-instrument bearer sets ACCEPTED (universal taxonomy would force fake categories). |
| ASM-415 | AD | — | UK-today instruments (VAT/import/corp/PAYE) live in engine.tax panel vs engine.fiscal whole-economy view — fork. |
| ASM-416 | AD | — | zoneOverrides generalised to every instrument (tax relief) vs policies.json discount — fork. |
| ASM-417 | ST | amend engine.policies.md AC-9 ResolveScope | Zone-class is a 4th scope kind (citywide/district/road today); flagged for Bill. |
| ASM-418 | CC | — | VAT/import/corp/PAYE bearer-category sets are BA-invented (extends ASM-287 per-instrument precedent). |
| ASM-423 | CC | — | 'Blue 2' resolved as mechanic-shape citation only, no literal parity claim. |
| ASM-565 | SF | data/tax_instruments.json (FEAT-056 worked example) | businessRates carries the industrial EV-van zoneOverrides discount (0.7/0.85). |
| ASM-563 | CC | — | Instrument category vocabulary (vat=consumption, paye=income, etc.) is descriptive tag, not behavioural. |
| ASM-564 | CC | — (close citing balance-number regime) | Bearer pass-through directions are developer-chosen standard tax incidence (player-felt). |

### feat.checkpoint / feat.saveux / feat.metricsdash / feat.devmode / feat.weathermode / feat.helper / syncmsg / looparm

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-440 | CC | — (close citing balance-number regime) | MaxRetainedForks (N) left as unset balance placeholder pending Aaron's approval. |
| ASM-442 | CC | — | Superseded by ASM-470 (Recorder durability risk confirmed real). |
| ASM-470 | AD | — | Recorder NOT durable (buffers in memory, short-session only) — FEAT-064 needs flush/rescope/dev-only ruling. |
| ASM-441 | CC | — | Checkpoints = sibling package, not a 4th SaveKind in feat.saveux. |
| ASM-443 | CC | — | Fork-tree pruning per abandoned BRANCH (not raw bundle count), ancestor-preserving. |
| ASM-260 | CC | — | FEAT-011 atomic-promote save design (stage outside root, promote after ValidateBundle) is BA's mechanism-agnostic call. |
| ASM-261 | CC | — | SaveKind/provenance metadata kept in feat.saveux sidecar, not int.serializer.Header. |
| ASM-262 | CC | — | Single-save-in-flight concurrency response (queue vs reject) left open — either acceptable. |
| ASM-263 | CC | — | Save exclusion-allowlist is opt-out (fail-loud drift test), not opt-in. |
| ASM-452 | CC | — | FEAT-066 in-game vs CLI resolved by Bill (ASM-476): out-of-band CLI ACCEPTED. |
| ASM-453 | AD | — | Go game process can't reach metro BOW MariaDB today; resolve once for FEAT-065+066 (driver vs queue-file). |
| ASM-451 | ST | register-guid — feat.metricsdash | Module key not registered; feat.* chosen over tool.* (Go package, not root tooling). |
| ASM-476 | CC | — | BILL RULING: out-of-band CLI is FEAT-066's v1 surface. |
| ASM-477 | ST | fix — claude-devfeedback-import.js parametrised attribution | Importer hardcodes feat.devmode; FEAT-066 notes misattribute (Bill: not done until fixed). |
| ASM-444 | ST | register-guid — feat.weathermode | Proposed key; no code.json entry. |
| ASM-445 | ST | register-guid — feat.devmode | Proposed key; no code.json entry. |
| ASM-447 | CC | — | In-game feedback maps to claude-bow.js `add bug` (no dedicated feedback type). |
| ASM-448 | CC | — (close citing balance-number regime) | FEAT-067 event multiplier/grant/subsidy/tax uplift = balance-regime placeholders. |
| ASM-449 | CC | — | Console-open enable reuses SourcePalette (no new EnableSource). |
| ASM-450 | CC | — (close citing balance-number regime) | Easy-mode extra-money + high-tax levers assumed coupled as one package; Aaron may want independent toggles. |
| ASM-467 | CC | — | AC-DM3 enable-trigger branch unreachable via RequireConsole (real gate forecloses it). |
| ASM-454 | ST | register-guid — engine.helper + feat.helper split | Escalated; contract/registry (engine) vs panel UI (feat) split proposed. |
| ASM-456 | CC | — | GameStateView minimal/extensible interface; no concrete field set pinned yet. |
| ASM-474 | CC | — | Helper v1: no panel/protocol wiring; description sourced from ProjectConsequence.Summary. |
| ASM-472 | ST | register-guid — tool.syncmsg | Proposed key; no code.json entry. |
| ASM-473 | ST | register-guid — tool.looparm | Proposed key; LOOP_STALE_MS=72h default is a placeholder. |

### engine.finance / borrowing / decommission / megafacilities / facilitypermits

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-460 | AD | — | Death-warning = structural trigger-path gate (AC-29) vs weaker correlation check — Bill to confirm strength. |
| ASM-461 | AD | — | Death warning must be proactive push alert (ui.alerts), not just F7/F2 pane — player-feel. |
| ASM-413 | AD | — | Secured-loan collateral forfeiture on default unspecified by FEAT-057 — money/design. |
| ASM-414 | AD | — | Revenue-share base (city-wide vs single-facility) left configurable; Aaron may want one shape. |
| ASM-499 | CC | — | Decommission accrual caller (permit/build) unbuilt; feature exposes accrual surface only. |
| ASM-500 | ST | amend engine.finance.md ledger account taxonomy | Liability needs a provision/liability account type not explicitly named. |
| ASM-501 | CC | — | Liability indexes with facility growth (answer = yes; monotonic non-decreasing). |
| ASM-502 | CC | — | Liability feeds CreditRating debt exposure, not the monthly-obligation set. |
| ASM-503 | CC | — | Discharge invoked by engine.mining Reclaim (unbuilt); feature owns surface only. |
| ASM-504 | CC | — | data/decommission.json (unregistered, convention-following). |
| ASM-506 | ST | master-plan amendment — feat.facilitypermits→engine.unlocks edge | Registry gap; non-purchase permit routes need it (BUG-058 family). |
| ASM-505 | CC | — | XP permit route reads engine.unlocks points, no fourth currency. |
| ASM-507 | CC | — | Milestone route = §4 expansion-permit allowance generalised via engine.unlocks tier. |
| ASM-508 | CC | — | All three permit routes available for any large facility unless data restricts. |
| ASM-509 | CC | — | Expansion re-engages permit gate at each data-sourced size threshold. |
| ASM-510 | CC | — | Large-facility size = data-sourced per-class size tier. |
| ASM-511 | CC | — | Permit gate = size gate layered on buildings.json unlock gate, not a new unlock field. |
| ASM-512 | SF | feat.megafacilities.md | Expert-workforce gate reads engine.education research-points (not raw skilled-citizen count). |
| ASM-513 | CC | — | feat.megafacilities owns the numeric gate; engine.unlocks stays out of research-point gating. |
| ASM-514 | ST | master-plan amendment — megafacilities→permits/decommission edges | Inherits gates via catalogue+FEAT-053/054, no direct call edge registered. |
| ASM-515 | CC | — | Gate code homed in internal/engine/mining per code.json (plan-grouping). |
| ASM-516 | CC | — | Gate params in data/megafacilities.json; catalogue extends buildings.json in place. |
| ASM-517 | CC | — | Felixstowe-class port sits above container_terminal at end-game milestone (M11/M12). |

### engine.invariant / BUG-067 stock API

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-566 | CC | — | RegisterStock snapshot = net tracked delta (not level); keeps RunSuite pure. |
| ASM-567 | CC | — | RegisterStock adds reg + StockName (name ≠ stock). |
| ASM-568 | CC | — | Term funcs = niladic closures evaluated at Check time (not SnapshotProvider builder). |
| ASM-569 | CC | — | Violation.Terms = one signed map (ins positive, outs negative). |
| ASM-570 | CC | — | Zero-term registration allowed (degenerates to Closing−Opening==0). |
| ASM-571 | ST | FEAT — cross-module RegisterTransfer primitive | Out of BUG-067 scope; needs distinct primitive + Bill/Aaron tick-alignment ruling. |

### engine core / season / invariant / projections / spiral / attract / market / logistics / consumption / traffic

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-005 | CC | — (close citing balance-number regime) | Pacing constant is a Go var, not GR#15 data-sourced; deferred to MOD-036 balance harness (FEAT-030 debt). |
| ASM-053 | SF | copy-guard standard | advanceOneDailyTick (8th mu.Lock site) unguarded — single sealed call site, documented. |
| ASM-200 | CC | — | Month index 0 = January; calendar month = mod 12 (documented in seasonal.json meta). |
| ASM-203 | CC | — | data/seasonal.json edited outside owned path — AC-10/AC-18 explicit sanction, not a STOP case. |
| ASM-201 | CC | — | healthWaveModifier stored non-negative, negated by SeasonAPI (schema forbids negative). |
| ASM-202 | CC | — (close citing balance-number regime) | 5 seasonal curves' magnitudes (harvest/construction/leisure/health-wave/intake-month) are plausible v1 placeholders. |
| ASM-221 | CC | — | Seasonal peaks step at month boundaries (not interpolated) — documented choice. |
| ASM-222 | CC | — | schoolIntakeGateThreshold=0.5 now load-enforced (exactly-one-month, MET-E504). |
| ASM-231 | CC | — | Only schoolIntakeGate gets load-time shape validation; other 7 curves intentionally unenforced. |
| ASM-155 | SF | engine.invariant.md | Balance identity is untyped int64 (no unit enforcement); reassess when engine.finance ACs written. |
| ASM-459 | CC | — (close citing balance-number regime) | MinWarningLeadMonths (insolvency + ghost-city) independent placeholder values. |
| ASM-234 | CC | — (close citing balance-number regime) | Logistics lead times/buffers/slot counts/shelf life unpinned balance data (shape-only ACs). |
| ASM-235 | CC | — | Junction queue text render = UI's job; engine.logistics exposes queryable state only. |
| ASM-239 | CC | — | '>5 game-years' = >60 months (12-month calendar year). |
| ASM-241 | CC | — (close citing balance-number regime) | Blight-spread rate + decay thresholds data-sourced, untuned (M2). |
| ASM-242 | CC | — | Ghost-city historic peak read from engine.attract (transitive), not direct citizens edge. |
| ASM-245 | CC | — | S6 work step uses interim employment rule pending engine.firms/market (flagged placeholder). |
| ASM-246 | CC | — | S6 scenario test lives in engine.attract package (black-box, headless). |
| ASM-191 | SF | engine.market.md scope | Market owns capacity-bounded availability query; live logistics ledger belongs to engine.logistics. |
| ASM-377 | CC | — | MOD-020 ruling2: guarded all 3 pointer derefs (Price/ExportPrice/Availability) with MET-E605. |
| ASM-190 | SF | engine.market.md AC-6 | Waste needs a distinct negative-commodity price path (ExportPrice accessor or documented negative-price convention). |
| ASM-218 | CC | — | Spillback fixture = ≥3 links / ≥2 junctions, downstream queue undersized (minimum distinguishing topology). |
| ASM-220 | ST | master-plan amendment — engine.consumption inbound=engine.finance | AC-20 expose-only billing avoids unregistered finance call; edge must be registered once. |

### data.catalogue / buildings.json (FEAT-010)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-082 | CC | — | id regex `^[a-z][a-z0-9_.-]{2,63}$` illustrative; accept-or-replace. |
| ASM-132 | CC | — (close citing balance-number regime) | 'Junction controls' (4 tiers, one row) modelled as ONE family entry, not 4 SKUs. |
| ASM-133 | CC | — (close citing balance-number regime) | N-tier chain rows split into one BuildingEntry per named tier. |
| ASM-134 | CC | — (close citing balance-number regime) | sewage_works_medium (M6/~10M/~50k m³/d) is interpolated, not spec-stated. |
| ASM-135 | CC | — (close citing balance-number regime) | ~37 flat-list supplement entries have empty costRaw/capacityRaw + unlock='unspecified' — data gap. |
| ASM-136 | SF | data.catalogue.md / engine.consumption review | consumptionRef assigned only where occupancy maps to consumption.json's 17 classes. |
| ASM-137 | CC | — (close citing balance-number regime) | blightClass assignments are qualitative spec reading (only 2 of ~9 spec-literal). |
| ASM-138 | CC | — | types.go BuildingEntry skeleton replaced per its own TODO(FEAT-010) invite. |

### harness.synth / balance.harness / perf (perfci, push-verify)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-083 | CC | — | MaxSyntheticCitizens reuses int.solver A9 (20-30M) as hard cap. |
| ASM-092 | CC | — | SEC-009 grid ceiling from S1.3 memory budget (150MB halved); ~5.4× real tile, safe. |
| ASM-173 | CC | — | MinMeasurableDuration=5ms (re-derived against 1M-citizen jitter; re-check on CI before S3 gate). |
| ASM-181 | CC | — | 10M-citizen budget uses spec's relative 10% regression + ≤2.5GB shard-memory (no invented absolute ms). |
| ASM-168 | CC | — | DefaultIdleTimeout=2s / DefaultDimAfterUses=5 documented, overrideable. |
| ASM-170 | CC | — | Preset sprawl=0.5 / shape=grid — least-arbitrary convenience defaults. |
| ASM-172 | CC | — | synth gridSideFor/radial/organic formulas = invented trig-free approximations (harness-only). |
| ASM-264 | CC | — | balance.harness seeds-per-config left to tuner judgment (scenario-file param). |
| ASM-265 | CC | — | Closed failure-cause taxonomy (5 causes) BA-invented, AC-3 requirement is distinguishability. |
| ASM-266 | CC | — | Retries (if any) additive — original failure record retained. |
| ASM-336 | CC | — | AST-Ident scan blind spots (runtime-concat/reflect/_test) accepted residual; runtime accessor is real fix. |
| ASM-337 | CC | — | PerfResult.Measured checked at AppendResult write boundary (not construction-time). |
| ASM-338 | CC | — | Uniform corrupt-line recovery (lead ACCEPTED; raise P3 to differentiate torn-tail vs mid-file corruption). |
| ASM-353 | FIX | bug-plan C6 cluster | Regressed=true run still AppendResult'd as new baseline (BUG-071 left unconditional append) — a genuine regression must never become baseline. |
| ASM-355 | FIX | bug-plan C6 cluster | BUG-074 removes Scanner per-line token cap entirely — unbounded-read risk in the perf results reader. |
| ASM-370 | CC | — | Reachability = name-only (over-approximation, false-positives only). |
| ASM-372 | ST | MOD — runtime HookCount accessor (engine.core) | Static scans are stopgap; real fix needs ownership to touch engine/core/headless. |
| ASM-373 | CC | — | CumulativeRegressionThreshold=2× step (20%), chosen multiplier. |
| ASM-375 | FIX | bug-plan C6 cluster | -accept-regression escape hatch wired only through perf-1m-probe (workflow_dispatch), not perf-smoke — gate must never carry a silent accept path. |
| ASM-374 | FIX | bug-plan C6 cluster | ImplausibleReason rejects strictly-negative only; zero-valued records can slip past and be trusted as a baseline. |
| ASM-339 | CC | — | perf-1m-probe queue-not-cancel (never discard in-flight measurement). |
| ASM-371 | CC | — | x/tools/cha not worth the dependency (doesn't close the function-value gap). |

### UI screens (proj/trade/demo/ticker/menu/districts/build) + ui.dash + ui.diagrams + ui.alerts/chrome

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-527 | ST | Bill — assign V-layer for F-screens | U-layer exhausted; V000-V099 claimed for proj; Bill blesses V-layer for other F-screens. |
| ASM-528 | ST | resolve finance vs fiscal drill source | rate-outlook drill target `finance.baseRate.cycle` — source unresolved (engine.finance vs fiscal). |
| ASM-529 | CC | — | proj mirrors widgets' unexported plotSeries/brailleLine normalisation + alignment test. |
| ASM-253 | CC | — | F7 default forecast horizon N = implementer default (mechanism unaffected). |
| ASM-251 | CC | — | F5 warehouse buffer-policy controls = percentage-of-capacity slider default. |
| ASM-252 | CC | — | F6 Saturday-hours view = stacked bar over ui.widgets primitives (no bespoke chart). |
| ASM-254 | CC | — | F9 archive search reuses ui.keys '/' NameIndex (substring, n/N). |
| ASM-255 | CC | — | F10 new-game form = seed + debug-flag only (per BOW parenthetical). |
| ASM-258 | CC | — | F3 unlock badge convention (locked/unlocked/in-progress) UI choice. |
| ASM-288 | CC | — (close citing balance-number regime) | District bundle-conflict pairs are Aaron's/M2 balance content (declared in policies.json). |
| ASM-518 | SF | ui.screen.ticker.md | f9.* wire schemas package-local (engine.news unbuilt); add drift test when MOD-043 ships. |
| ASM-519 | CC | — | SF-3 drives screen directly (stub has no f9 view). |
| ASM-520 | CC | — | Ticker scroll implemented locally (no shared ui.widgets primitive). |
| ASM-521 | CC | — | Drill-through = DrillTargets pair list (ui.dash OPEN). |
| ASM-522 | CC | — | Archive search case-insensitive substring; empty query matches nothing. |
| ASM-556 | SF | ui.screen.ticker.md | Drill target ViewName `news.event` (EntityID = event ID); reconcile when MOD-043 lands. |
| ASM-523 | CC | — | F10 save-root enumeration injected (BundleLister); engine.save owns layout. |
| ASM-524 | CC | — | Menu actions issued as protocol.DebugPayload with fixed Op strings (no dedicated Kinds yet). |
| ASM-525 | CC | — | Save-slot fields derived from Header (CreatedAtTick/GameMonth/WorldSeed/DebugTouched) only. |
| ASM-526 | CC | — | F10 subscribes to 'f10.session' view (schema v1, screen's own choice). |
| ASM-478 | CC | — | ui.screen.demo doc defect (phantom ui.widgets.Pyramid) — doc refresh done; extract only on 2nd need. |
| ASM-530 | SF | ui.alerts.md / ui.core.md | chrome consumes own Effects seam for nav/pause (ui.core has neither API). |
| ASM-531 | SF | ui.alerts.md | chrome carries Alert.Crisis locally; protocol Event.Crisis (FEAT-042) unbuilt. |
| ASM-532 | CC | — (close citing balance-number regime) | Three-tier scheme (Info/Warning/Critical→TokenSelection/Warning/Danger) BA-chosen. |
| ASM-533 | CC | — | Tie-break = oldest-first by Alert.Tick, then ascending ID (deterministic). |
| ASM-534 | ST | Bill — reconcile U-layer (chrome vs dash/diagrams) | chrome claimed U900-U999 starting at MET-U901 (diagrams holds MET-U900). |
| ASM-535 | SF | ui.alerts.md | Jump target = opaque Target string; figures on 'chrome.topbar' view; convert to TargetRef when FEAT-042 lands. |
| ASM-538 | SF | ui.dash.md | DrillTarget{ViewName,EntityID} self-contained; reconcile with protocol.TargetRef at FEAT-042. |
| ASM-542 | CC | — | ui.dash Navigator interface seam (ui.core has no navigation API). |
| ASM-543 | ST | Bill — reconcile U-layer (diagrams vs alerts) | MET-U900 registered under ui.diagrams while ui.alerts claims U900-U999. |
| ASM-544 | SF | ui.dash.md | DiagramHit seam mirrors ui.diagrams Hit (field names differ); reconcile + register edge. |
| ASM-545 | CC | — | Mini-map via widgets.Heatmap, alert-list via widgets.Border (no dedicated widgets). |
| ASM-546 | CC | — | Layout-profile JSON carries top-level `name` for menu LoadLayoutProfile. |
| ASM-279 | CC | — | diagrams layered tie-break = stable sort by caller ID. |
| ASM-536 | CC | — | Equal-rank nodes ordered by SourceID. |
| ASM-537 | CC | — | Cyclic chain flattens to one rank with left-side loops (Kahn detect). |
| ASM-539 | CC | — | Network grid mode = raw X,Y translated to origin. |
| ASM-540 | CC | — | Tube-map line order = node slice order; edges validated not drawn. |
| ASM-541 | CC | — | Sankey band = round(amount/stageTotal × bandMaxWidth). |
| ASM-280 | CC | — | MOD-038 shipped layout = F1 Overview right-rail only; F2/F4/F8 out of scope. |

### engine systems cluster (education/social/refuse/dispatch/news/extcommute/fuel/farming/capexport/fdi/rail/leisure/mining/tourism/chemicals/tunnels/coastal/cafe/destination/crime/defence/disasters/firms/comms/parking)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-292 | CC | — (close citing balance-number regime) | education drift/attainment/research-point magnitudes placeholder (M2). |
| ASM-293 | CC | — (close citing balance-number regime) | social caseload rates + provision capacity placeholder (M2). |
| ASM-294 | CC | — (close citing balance-number regime) | refuse bin-capacity/waste-per-capita/contamination curves placeholder (M2). |
| ASM-295 | CC | — (close citing balance-number regime) | dispatch outcome curves/fire-spread/air-ambulance threshold placeholder (M2). |
| ASM-269 | CC | — (close citing balance-number regime) | news salience weight table across 5 categories = build-time data. |
| ASM-270 | CC | — | LLM fact-lock = exact match on names/numbers/dates (loosest defensible default). |
| ASM-271 | CC | — | News archive retains full history, no pruning (v1). |
| ASM-273 | SF | engine.extcommute.md | Assumes employmentState supports an off-map-pool variant; verify at dispatch. |
| ASM-278 | CC | — (close citing balance-number regime) | Alert priority-tier scheme/colours/tie-break are defaults, not spec-mandated. |
| ASM-307 | CC | — (close citing balance-number regime) | Fuel strategic-reserve days-of-cover + EV-share-by-era curve not spec-fixed. |
| ASM-308 | CC | — (close citing balance-number regime) | BDI decline-faster-than-recovery asymmetric rates (no spec ratio). |
| ASM-309 | CC | — (close citing balance-number regime) | Capacity-export contract terms/penalties/growth rate not spec-fixed. |
| ASM-310 | CC | — (close citing balance-number regime) | FDI prospect cadence + bid win-probability curve unspecified. |
| ASM-311 | CC | — (close citing balance-number regime) | Rail fleet-size maintenance ratio unspecified. |
| ASM-312 | CC | — (close citing balance-number regime) | Organic = 1.0× baseline; conventional '+30-40%' is the full delta. |
| ASM-313 | CC | — (close citing balance-number regime) | Nitrate-runoff rate + pollinator-collapse threshold not numeric. |
| ASM-314 | CC | — (close citing balance-number regime) | leisure venue capacity/novelty-decay/events magnitudes placeholder. |
| ASM-315 | SF | engine.leisure.md | Unmet-taste-demand query = per-district taste-gap vector (BA-invented shape). |
| ASM-316 | CC | — (close citing balance-number regime) | Blight noise dBA-falloff + subsidence-radius magnitudes data-sourced. |
| ASM-317 | CC | — (close citing balance-number regime) | Per-site extraction output rates (t/day) not spec-numbered. |
| ASM-318 | CC | — (close citing balance-number regime) | Tourism bed counts + bed-tax rate placeholder. |
| ASM-319 | CC | — (close citing balance-number regime) | Reputation-fragility lag = fixed N-month (~12), unspecified. |
| ASM-321 | CC | — (close citing balance-number regime) | Chemicals leak-probability + make-or-buy margin balance values. |
| ASM-322 | CC | — (close citing balance-number regime) | Tunnels TBM learning-curve decay + hyperloop capex/prestige magnitude. |
| ASM-323 | CC | — (close citing balance-number regime) | Coastal arrival frequency/caseworker throughput/hotel-requisition multiplier placeholder. |
| ASM-324 | CC | — | Coastal status-pipeline duration = configurable multi-month range (tunable, GR#15). |
| ASM-325 | CC | — (close citing balance-number regime) | Cafe vitality-index term weights data-driven placeholders. |
| ASM-326 | SF | engine.destination.md | Regional-draw split: destination supplies portfolio inputs, tourism owns the draw number. |
| ASM-327 | CC | — (close citing balance-number regime) | Gang removal thresholds + respawn window placeholder. |
| ASM-329 | CC | — (close citing balance-number regime) | Grant win-rate curve/mandate compensation/refusal penalty placeholder. |
| ASM-330 | CC | — (close citing balance-number regime) | Disaster precursor lead-window + frequency/severity distributions placeholder. |
| ASM-331 | CC | — (close citing balance-number regime) | Firms founding-probability weights/superlinear exponent/rate-cycle sensitivity/angel-boost. |
| ASM-333 | CC | — (close citing balance-number regime) | Comms e-commerce-share/remote-work weights/drain curve unpinned. |
| ASM-335 | CC | — (close citing balance-number regime) | Parking footprint-per-space/charge elasticity/cruising multiplier/autonomy shrinkage unpinned. |
| ASM-299 | AD | — | Terror attack + storm-surge-damage are the two lowest-confidence crisis candidates (flagged for Aaron). |
| ASM-346 | AD | — | C5 storm-surge "damage-to-occupied-cells" gated on feat.disasters confirming a distinguishable event. |

### commoditymarket / unlocks / resourcedeposits / decommission-data / external_world

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-486 | SF | feat.commoditymarket.md scope | International market = feature-owned price surface (not an engine.market registry extension). |
| ASM-487 | SF | feat.commoditymarket.md | Named archetypes = data-defined ChemAPI chain stages (not bespoke facility registry). |
| ASM-488 | SF | feat.commoditymarket.md | Pharma campus = manufacture-side only; FDI bid stays in engine.fdi. |
| ASM-489 | AD | — | Always-on floor/ceiling = single static world price per product (dynamic market shelved to future-dev). |
| ASM-490 | CC | — (close citing balance-number regime) | Export-side world prices/ratios/costs/capex unpinned balance data. |
| ASM-491 | CC | — | data/commoditymarket.json (unregistered, convention-following). |
| ASM-492 | CC | — | Parker-class mines excluded (extraction-ladder content, not this feature). |
| ASM-493 | CC | — | Unlock-tree ID scheme: 12 category slugs + node-id prefixes. |
| ASM-494 | CC | — (close citing balance-number regime) | dpCost/prereqTier placeholder = node tier (disclosed v1 shape, M2 tuning). |
| ASM-495 | CC | — | Sewage-works tier = §4 tier 5 (over catalogue M4), upgrade path folded. |
| ASM-496 | CC | — | Child-benefit node at tier 4 (no spec gate; grouped with elder-care). |
| ASM-497 | CC | — | Gas-network content in Water & Gas tree. |
| ASM-498 | CC | — | No transitional category field (loader TODO awaits full loader). |
| ASM-481 | CC | — | (Bill ACCEPT) Every category covers all 13 tiers with explicit kind:none no-op nodes. |
| ASM-482 | CC | — | (Bill ACCEPT) 13-entry-per-category floor = 156 total. |
| ASM-548 | CC | — | Deposit loader self-contained (no foundation.data edge registered). |
| ASM-549 | CC | — | Deposit shuffle uses local splitmix64 (world-gen convention, not det.Stream). |
| ASM-550 | CC | — | Chalk = world GeologyNone for uranium exclusion. |
| ASM-551 | CC | — | Geology-not-derived maps to ErrGeologyNotProspected (caller must prospect). |
| ASM-552 | CC | — (close citing balance-number regime) | data/deposits.json values = directional placeholders (Aaron row-by-row). |
| ASM-554 | CC | — | coalfield coverageFloor = tile-level (not cell-level). |
| ASM-553 | CC | — (close citing balance-number regime) | Fictional resource slot named `arcana` (placeholder; real name is Aaron's call). |
| ASM-555 | CC | — | DepositAt false = no-deposit-or-not-shuffled (not an error). |
| ASM-547 | ST | Bill — reconcile E-layer (engine.mining vs feat.skeleton) | engine.mining claimed E950-E999 by narrowing feat.skeleton to E900-E949; reallocate if wrong. |
| ASM-572 | CC | — | externalRail gated to tier 5 (era-5 unlock tier). |
| ASM-573 | CC | — | capacityByEra: non-empty, strictly-increasing era, non-negative capacity. |
| ASM-574 | CC | — | Unlock nodes require specRef/description/dpCost/prereqTier. |
| ASM-575 | CC | — | Tier coverage = each tier present ≥1 per tree. |
| ASM-576 | CC | — | Category count derived from meta.categories (name bijection). |
| ASM-558 | CC | — | NamingCorpus.Validate structural-only; 40-name floor stays a test assertion (not production Validate). |

### ui.core / keys / demo / map / screens-debug / stub (copy-guard + sanitiser cluster)

| ASM | Bucket | Destination / title | One-line content to preserve |
|---|---|---|---|
| ASM-077 | SF | SEC-011.md — reject-vs-sanitise policy scope | Pattern #4 reject-not-sanitise scoped to identity/path data, NOT display text (policy). |
| ASM-078 | SF | BUG-017.md — warn-vs-reject policy | BUG-017 warn (not reject) on shell-output-looking text in claude-bow.js (policy). |
| ASM-421 | CC | — | SEC-011 sanitiser replaces non-printable with U+FFFD (not strip) — preserves cell alignment. |
| ASM-422 | CC | — | Sanitiser enforced in core.Buffer.Set (single choke point). |
| ASM-066 | SF | copy-guard standard | StubEngine.World() left unguarded (immutable post-construction); add immutability regression test. |
| ASM-067 | SF | copy-guard standard | Locked helpers unguarded — single pre+post-checked call sites. |
| ASM-089 | SF | Joiner/OnceCloser helper | Run's cancel-then-join-then-close contract is doc-only; fold into shared helper. |
| ASM-093 | SF | copy-guard standard | Screen/MapScreen guard every exported method touching a receiver field; TailEntry sole exception. |

---

### Flat AARON-DECISION list (13 items — stays OPEN), grouped by theme

### 1. Architecture ownership / boundaries (6)

- **ASM-281** — call-edge DIRECTION inference may be backwards (needs per-candidate architect ruling before master-plan edit).
- **ASM-306** — which module owns the 'goods' conservation stock (market vs logistics vs both).
- **ASM-453** — Go game process → metro BOW MariaDB (driver vs local queue-file); shared FEAT-065+066 decision.
- **ASM-470** — FEAT-064 durability: Recorder incremental-flush vs rescope to periodic Save() vs dev-only.
- **ASM-489** — static vs dynamic world price surface (always-on floor/ceiling now).
- **ASM-460** — death-warning structural trigger-path gate vs correlation-only (AC-29 strength).

### 2. Tax & borrowing instrument design (4)

- **ASM-413** — secured-loan collateral forfeiture on default (unspecified by FEAT-057).
- **ASM-414** — revenue-share base (city-wide vs single-facility).
- **ASM-415** — UK-today instruments in engine.tax panel vs engine.fiscal whole-economy view.
- **ASM-416** — zone-class overrides generalised to every instrument (tax relief) vs policies.json.

### 3. Crisis-taxonomy edges (2)

- **ASM-299** — terror attack + storm-surge-damage (two lowest-confidence candidates).
- **ASM-346** — C5 storm-surge "damage-to-occupied-cells" gated on feat.disasters emitting a distinguishable damage event.

### 4. Gameplay-feel (1)

- **ASM-461** — death warning must be proactive push alert, not just a passive pane.

---

### FIX bucket (4 items — perf-gate soundness, aligns with bug-plan C6 cluster)

- **ASM-353** — a genuine Regressed=true run still gets AppendResult'd as the new baseline; a real regression must never become baseline.
- **ASM-355** — BUG-074 removed bufio.Scanner's per-line token cap entirely — unbounded-read risk in the perf results reader.
- **ASM-374** — ImplausibleReason (BUG-085) rejects strictly-negative only; zero-valued fields can slip and be trusted as a baseline.
- **ASM-375** — `-accept-regression` escape hatch wired only through perf-1m-probe, not perf-smoke — the gate must never carry a silent accept path.

---

### Key cross-cutting observations for Phase 2

1. **The balance-number regime now absorbs ~55 former AARON items** (re-bucketed to CC). Phase 2 closes these by writing a single line on each module's acceptance doc ("numbers are balance-regime placeholders — placeholder + directional test + Aaron's row-by-row approval at the M2 balance pass, MOD-036") and retiring the ASM row. **Do NOT re-interview Aaron for these** — the standing blanket ruling already covers them.

2. **The FIX set (4) is a distinct perf-gate soundness cluster** (ASM-353/355/374/375) that belongs with the bug-plan C6 cluster, not the confirm-and-close sweep. Phase 2 should dispatch these as fix work, not fold them.

3. **The STORY set (30) is the real net-new engineering work** — error-layer reallocation (527/534/543/547), module-key registration (444/445/451/454/472/473), master-plan call-edge amendments (220/506/514), int.protocol extension (485), and residual guard gaps (188/224/228/230/360/363/484).

4. **The SPEC-FOLD set (34)** clusters into two written standards — the **copy-guard mechanism-design standard** (009/053/066/067/069/073/074/089/093) and **"reconcile-seam-when-dependent-module-lands"** notes (530/531/535/538/544/518/556/273/428) — plus the two policy-scope folds (077→SEC-011.md, 078→BUG-017.md).

5. **ASM-150 is closed** (GR#22 scrub, e3c2dbb, ACCEPT verdict) — removed from this map entirely; do not re-dispatch or re-close it.
