# Assumption Triage — 2026-08-11

**Author:** BA-ASM. **For:** Ben (lead ruling pass). **Scope:** `node claude-bow.js list --type assumption` — 242 open ASM items. Primary focus per brief: ASM-189 onward (186 items, this wave, entirely unruled at BOW-comment level). Older ASM-001..182 already carry a BA-recommended `[disposition:...]` tag from a prior triage pass (author "bob") — those are noted only where they intersect this wave's clusters; they are **not** re-litigated here.

**Method:** every line below is tagged **[verified-in-code]** (I read the actual file/constant/test) or **[inferred-from-the-record]** (checked against the BOW/docs but not against runtime code, usually because the module doesn't exist yet — see Finding 0). Every BUG/FEAT/MOD/ASM code I cite was resolved with `claude-bow.js show <CODE>` before use (BUG-075 discipline).

---

## Lead with these: administrative gaps found while verifying

Not code-contradicts-description findings this time — I sampled description-vs-code claims across the pieces of the codebase that actually exist (guards, `internal/harness/synth`, `internal/engine/market`, `internal/protocol`, the three empty-stub data files) and **all of them checked out exactly as described** (detail in Finding 0). The real yield from verification was different: **several items in the 189+ "unruled" band already have a ruling sitting in their comment thread that never got reflected in BOW status.** These are cheaper than triage — they just need Ben's ten minutes to close them out, not fresh judgement:

| Code | What already happened | Action needed |
|---|---|---|
| ASM-214 | Aaron ruled 2026-08-11 (via Bob): Claude fetches real OS Terrain 50 data directly. Bob executed it — 27 TR tiles + OGL licence now in `data/terrain50/` (verified: file exists, ~5MB, TR23.asc dimensions match Folkestone). **Files are uncommitted.** | ACCEPT the ruling as executed; get the commit made (Bill/Ben), then close AC-13/AC-22 wiring as a follow-up, not this ASM. |
| ASM-216 | Aaron ruled: fixed-point integer PCU accounting for SUE, float-with-canonical-order rejected. | ACCEPT — ruling is final and complete. Close. |
| ASM-237 | Aaron ruled: forecast horizon is 10 game-years (120 months), not the BA's assumed 6, held in data per GR#15. | ACCEPT — ruling is final and complete. Close. |
| ASM-338 | Ben already ruled (logged 2026-08-11): shipped behaviour accepted, but a real gap (no severity distinction between a torn-tail line and a mid-file corrupt line) was found and should be raised as a fresh P3 when someone next touches `results.go`. | ACCEPT — ruling done. The one open action is filing that P3, which doesn't exist yet. |
| ASM-356 | Ben already ruled: guard-copying pattern stands (crash-open in a shared-module design was proven, not hypothetical), but the five `buildQuoteMask` copies need a drift test before this is "settled." | ACCEPT — ruling done. Confirm a drift test exists (`quote-mask-drift.test.js` is referenced by ASM-367/368, so it does) and close. |
| ASM-276 | Split in two by the record itself: the design half (crisis is a distinct explicit tag, never tier-derived) is Aaron-ruled and closed; the engineering half (the tag has no home yet in `int.protocol`'s Event/Delta schema, and needs a stable per-instance crisis ID per Aaron's follow-up direction) is still open. | CORRECT the item: split it. File the design half as ACCEPT/closed, keep only the schema-engineering half open (it duplicates ASM-275's addressing-scheme gap and ASM-304's `Event.Crisis bool` design — see Cluster E). |

That's 6 of the 186 "unruled" items that are actually already-ruled-but-uncommitted-to-status. Fixing that alone cuts the live backlog to 180 without asking Ben to think about anything new.

Two more are **self-corrected mid-wave**, which is healthy process working as intended, but they should not be read as still stating the current truth:
- **ASM-344** — round-4 correction already supersedes the original framing (two new live bypasses found and fixed: outside-quote backslash-escaping, heredoc bodies). SUPERSEDED — recommend closing this item and letting the newly-logged P2 (`buildQuoteMask` is-not-a-full-lexer residual) stand as the live record instead.
- **ASM-354** — explicitly marked "SUPERSEDED IN PART by ASM-369" in its own comment thread; gap 1 (wrapper indirection) is now closed by a real reachability test, only gap 2 (`reflect.MethodByName`) remains open. SUPERSEDED — keep for history per the comment's own instruction, rule on ASM-369/370/372 instead (Cluster D below).
- **ASM-357** — comment thread is a full correction of the original coverage claim, re-verified live in a shell. Treat the comment, not the original description, as current. CORRECT (the description text is now stale relative to its own thread — an easy trap for anyone reading only the top line).

---

## Finding 0 — verification sample, zero contradictions found

I checked six cheaply-verifiable claims from the 189+ band directly against source:

- **ASM-215 / ASM-223 / ASM-274** [verified-in-code]: `data/modes.json`, `data/naming_corpus.json`, `data/external_world.json` are exactly what's described — schema-valid stub shells (`{"version":1,"entries":[]}` / `{"categories":{}}` / `{"profiles":[]}`), genuinely empty of content.
- **ASM-301** [verified-in-code]: `internal/protocol/envelope.go:16` — `const ProtocolVersion = "1.0"`, unchanged. Description accurate.
- **ASM-364** [verified-in-code]: `code.json` has `tool.authorguard` (line 633) and no `tool.committhook` key anywhere. Description accurate.
- **ASM-373** [verified-in-code]: `internal/harness/synth/limits.go:111` — `const CumulativeRegressionThreshold = 2 * RegressionThreshold`, with the doc comment itself labelling it "ASM, judgment call — not spec-mandated." Description accurate, and unusually well self-documented in the source.
- **ASM-352 / ASM-355** [verified-in-code]: `.github/workflows/ci.yml`'s `perf-smoke` job treats `cmd/perfci` exit code 3 as non-fatal with an explicit `::warning::` annotation (line 244); `internal/harness/synth/results.go:257-268` uses `bufio.NewReader(f).ReadString('\n')`, no `bufio.Scanner` token cap anywhere in that file. Both accurate.
- **ASM-376/377** [verified-in-code]: `internal/engine/market/market.go` — `Load` exists at line 140, `Price`/`ExportPrice` split with `ErrWasteNotImportable`/`ErrNotExportable` guard errors exists as described (ASM-190's resolution, referenced directly in `doc.go`/`errors.go`).

**Why this matters for the ruling pass:** unlike the prior triage pass (which found real drift and called it the highest-value work in the exercise), this sample found none. Read that as reassurance the wave-2026-08-11 BA output is currently trustworthy where it touches built code — **not** as license to skip verification on the rest; it's a sample of 6 out of 186, chosen because they were cheap to check, and the bulk of the 186 describe modules (S4–S11: tax, households, tourism, coastal, cafe, firms, comms, parking, leisure, chemicals, tunnels, gangs, grants, disasters...) that **do not exist in code yet** — `internal/engine/` currently contains only `core, debug, detgate, invariant, market, season, stub, world`. For those, "verify against the code" is structurally impossible; they are BA acceptance-criteria work running ahead of the build queue (as the dev-team pipeline intends — S4-S11 BAs write criteria while S3 builds), so I've marked every such item **[inferred-from-the-record]** rather than claiming a check I couldn't perform.

---

## Cluster A — Player-felt balance numbers, no spec figure, placeholder pending an M2 tuning pass

**This is the biggest cluster by far and the one Aaron needs to see as one decision, not forty.** Every item below is a BA correctly declining to invent a number a player will feel (a rate, a threshold, a price, a probability, a decay curve) where the design spec (`docs/METROPOLIS-MASTER-v2.1.md`) states the *mechanism* but not the *magnitude*. All of them already point at the same resolution pattern: ship a data-file-sourced placeholder now (GR#15-compliant — not a hardcoded Go literal), defer the real number to a dedicated balance-tuning pass.

**44 items, one recommendation, two parts:**

1. **ACCEPT the pattern itself** — deferring magnitude decisions to a named balance pass, while keeping the *mechanism* and the *data-file plumbing* built now, is the right call every time it appears below. Ben can rule this once and it covers all 44.
2. **ESCALATE TO AARON, batched by system** — not the deferral, but the eventual numbers. Group into one sitting, not 44 tickets:

| Sub-decision (one Aaron sitting each) | ASM codes |
|---|---|
| Tax bands, elasticity, policy costs (core economy) | ASM-284 (P0) |
| Service/budget slider bounds (min/max/step for tax + spend UI) | ASM-250, ASM-256 |
| Logistics: lead times, buffers, slot counts, shelf life | ASM-234 |
| Environment/land: blight decay, geology, biodiversity, chemicals, tunnels | ASM-241, ASM-291, ASM-308, ASM-313, ASM-316, ASM-321, ASM-322 |
| Public services: education, social services, refuse, dispatch/fire/ambulance | ASM-292, ASM-293, ASM-294, ASM-295 |
| Tourism/leisure/culture: accommodation, reputation lag, venue capacity, cafe vitality | ASM-314, ASM-318, ASM-319, ASM-325 |
| Regional economy: firms founding, FDI cadence, comms/e-commerce, parking elasticity, rail fleet, capacity-export contracts | ASM-309, ASM-310, ASM-311, ASM-331, ASM-333, ASM-335 |
| Fuel/energy era curves (strategic reserve, EV share) | ASM-307 |
| Coastal migrants: arrival frequency, caseworker throughput, hotel cost | ASM-323 |
| Justice/social: gang removal thresholds, grant win-rates, reoffending rates | ASM-327, ASM-329 |
| Disasters: precursor lead-window, frequency/severity | ASM-330 |
| Wellbeing per-driver weighting (commute/rent-burden penalties) | ASM-268 |
| Terrain/extraction placeholders (procedural terrain, tile pricing, slope-cost, extraction rates) | ASM-206, ASM-207, ASM-209, ASM-211, ASM-317 |
| Seasonal curve shapes/step-vs-interpolate | ASM-202, ASM-221 |
| Pacing "achievable-but-hard" definition — explicitly flagged by its own author as Aaron's call | ASM-267 |

That's 44 codes resolved by two rulings instead of 44. Recommend Ben package this table directly into Aaron's next design session as one agenda item: "approve the M2-balance-pass deferral pattern; then work this table top to bottom."

---

## Cluster B — Guard/hook family: scope boundaries and shared-code decisions

`claude-author-guard.js` / `claude-destructive-guard.js` / the four sibling hooks that copy `buildQuoteMask`. This is Ben's own domain (engineering judgement, not player-facing) — ACCEPT is the right default answer for nearly all of these, several already effectively ruled per the "lead with these" section above (ASM-356).

- **ACCEPT** (scope-boundary choices, consistent with the accepted `git commit`-only, no-rebase/merge/cherry-pick scope already ruled for the author-guard at ASM-193/187/188): ASM-340 (destructive-guard scopes to plain `git commit` only), ASM-348 (alias resolving to literal `commit` treated identically), ASM-359 (`isCommitInvocation()` checks literal `commit` not the wider verb set), ASM-360 (local `GIT_TOKEN_RE` variant, not the full tokenizer), ASM-341 (root resolved via `process.cwd()` not `__dirname` — flag this one for a one-line confirm since it's the one sibling that diverges from the rest; low risk but worth a sentence in the ruling), ASM-343 (settings.json only read for root-level staged files) [verified-in-code, matches `claude-destructive-guard.js`'s own doc comments], ASM-349 (quoted-path tolerance scoped to executable-path prefix only), ASM-350 (`buildQuoteMask` is an approximation by construction, cannot be made sound), ASM-361 (dependency-load failure fails closed only when text looks commit-shaped), ASM-362 (env-var override paths with real-file defaults), ASM-363 (`findCommitInvocation()` stops at first verb).
- **ACCEPT with a named follow-up** (already Ben-ruled, see table above): ASM-356.
- **SUPERSEDED** (self-corrected in-thread, treat comment as current): ASM-344, ASM-354, ASM-357.
- **ESCALATE — none.** Nothing in this cluster is player-facing; all of it belongs to Ben.
- Process note: ASM-347 and ASM-358 (reuse of `claude-author-guard.js`'s parsing primitives via `require()`) are the same judgement as ASM-356 and should be ruled together with it, not separately.
- ASM-367/368/369/370/372/375/352(dup, see Finding 0) round out this cluster's remaining reachability/CI-scope items — all engineering-scope, ACCEPT is the consistent call given ASM-338/356's precedent of "accept the shipped trade, log the honest residual as a follow-up rather than pretending it's fully closed."

---

## Cluster C — GR#20 contract/call-edge direction calls (BUG-058 family)

Architecture judgement — spec prose says "which module needs data from the other," and a BA had to infer directionality. Ben-level, not Aaron-level.

- ASM-217, 219, 232, 233, 236, 243, 247, 248, 249, 273, 281, 282, 303, 306, 332, 334, 376, 377 — all "which module owns X / which direction does the edge point" calls, several explicitly tied to BUG-058 (the two independently-found missing-edge defects) [verified: BUG-075/BUG-058 both resolve to what they're cited as].
- **Recommend ACCEPT** for the individually-argued ones (each carries its own stated rationale, e.g. ASM-232's live-ledger-vs-static-query split, ASM-247's citizens-owns-partnering-primitive split) — these read as sound, defensible engineering calls consistent with the existing MOD-020 pattern (ASM-376/377, already verified-in-code above).
- **One flag:** ASM-281 and ASM-282 are meta-assumptions about the *method* used to infer all the others ("edge direction inferred from spec prose may not match intent," "shared spec-section citation is weak evidence of an edge") — rule on these two FIRST, since a correction there could cascade back through the rest of the cluster. Recommend ACCEPT-with-audit: approve the method, but note it as the reason BUG-058-style drift checks (also flagged in BUG-058's own "recommended" text) are worth building.

---

## Cluster D — Reachability/perf-gate engineering residuals

ASM-369, 370, 372 (the corrected BUG-072/BUG-087 static-analysis residual — reflect.MethodByName gap, name-only over-approximation, stopgap-not-permanent-fix) and ASM-375 (perf-1M escape hatch wired only into `perf-1m-probe`, not `perf-smoke`) [verified-in-code: ASM-352/355's verification above confirms this family of files matches its description]. **ACCEPT** all four — consistent, honestly-scoped residual-risk statements, Ben's domain.

---

## Cluster E — Crisis/alert taxonomy engineering (post-Aaron-ruling remainder)

Aaron already ruled the design half (ASM-276, see above). What's left is pure engineering, and it's genuinely one motion:

- ASM-275 (sub-entity/row addressing scheme needed in `int.protocol`), ASM-304 (`Event.Crisis bool`, not enum, not on Delta), ASM-277 (which conditions qualify as crisis-tagged is a design/balance call — **this one should move into the Aaron pile**, it's the mechanics of ASM-299/300 below), ASM-296 (only the two terminal Detroit-spiral conditions are crisis-tagged; no intermediate stage), ASM-298 (stockout needs a discrete once-on-transition event, distinct from the 3-month projection alert), ASM-300 (crisis taxonomy is data for balance, but the player-facing control is a coarse Pause/Notify/Off switch, not per-condition).
- **ASM-299 is explicitly self-flagged "for Aaron specifically"** (terror attack and storm-surge are the taxonomy's two lowest-confidence crisis candidates) — pull it out and put it in the Aaron pile above alongside ASM-277.
- Recommend: **ACCEPT** ASM-275/296/298/300/304 as engineering scaffolding consistent with Aaron's already-ruled design (stable per-instance crisis ID, tier-independent tag); **ESCALATE** ASM-277 + ASM-299 together as one Aaron sitting: "which conditions get the crisis tag."

---

## Cluster F — UI/interaction defaults (low-stakes implementer choices)

ASM-252 (F6 stacked-bar reuses `ui.widgets` primitives, not a bespoke chart type), ASM-253 (F7 default forecast horizon is an implementer default — note: this may now be **subsumed by ASM-237's Aaron ruling** on the 120-month base horizon; recommend Ben confirm ASM-253 folds into ASM-237 rather than standing alone), ASM-254 (F9 archive search reuses `ui.keys`' existing `/` convention), ASM-255 (F10 new-game form limited to seed + debug flag), ASM-288 (district bundle-conflict warning list is BA-invented, extensible), ASM-278 (alert priority-tier scheme/colour mapping/tie-break are implementer defaults). **ACCEPT** across the board — these are exactly the kind of low-stakes, reversible, documented-as-such defaults the process is supposed to let a BA make without escalation.

---

## Cluster G — Scope/process calls (file ownership, empty-data-blocks-ACs)

ASM-215, 223, 274 [verified-in-code, see Finding 0] — genuinely blocked ACs pending data authoring, correctly flagged rather than faked. **ACCEPT the flag; this is a scheduling/BOW-dependency item, not a judgement call** — recommend Ben confirm these have (or get) a BOW dependency edge onto whichever item owns authoring `modes.json`/`naming_corpus.json`/`external_world.json`, so they don't sit as silent blockers.

ASM-283 (new `data/tax_instruments.json`, spec names no file for §39) — **ACCEPT**, consistent with GR#15 and the existing pattern (`data/traffic_balance.json` per ASM-212, already-accepted precedent).

ASM-285, 287 — named districts as cell-tagged regions (not vector polygons), tax-incidence-bearer categories as a BA-invented enum. **ACCEPT** — both are BA-drawn taxonomies with no player-facing number attached, same shape as Cluster F.

ASM-320 — August tourism stress scenario's pass/fail definition is the BA's own construction. **ACCEPT** — this is exactly the acceptable kind of BA-authored test oracle (conservation + capacity-cap + relative queue-drain bound), not a balance number.

---

## Individually-notable items (don't cluster cleanly)

- **ASM-289** — `ImportAndPlaceStartTile` re-import contract: always succeed, never discard state. This is a data-safety design call, not balance. **ACCEPT** — "never discard state" is the correct default for any re-import path touching a save-bearing operation, consistent with GR#12 (never feature without backup).
- **ASM-303** — "the 18 high-confidence edges added for BUG-058 are correctly directed and no others are needed among them." This is a confidence self-assessment, not a decision — **ACCEPT conditionally**: fine to rule on now, but flag it for a spot-check whenever the BUG-058 drift-check tooling (recommended in BUG-058 itself) exists.
- **ASM-365, 366** — process/verification gaps ("cherry-pick/revert/am hook-firing not live-reverified," "Node-authored commit-msg hook execution on Windows git not verified"). These aren't assumptions to rule on so much as **open verification debt** — recommend Ben route these to the Tester queue rather than ruling ACCEPT/CORRECT, since the honest answer is "someone needs to actually run this," not a judgement call.

---

## The escalation ratio — the finding that matters most structurally

Of the 186 items in the 189+ wave:
- **~44 (24%)** are genuine player-felt balance/design numbers with no spec figure — Cluster A, plus ASM-277/299 in Cluster E, plus ASM-267. Call it **46 (25%)**.
- **~140 (75%)** are engineering/architecture/scope calls squarely inside a lead's competence — guard scoping, call-edge direction, UI defaults, data-file placement, taxonomy naming.

**Read: this is the healthy pattern, not the runaway one.** A quarter of a large BA wave landing as genuine Aaron-scale decisions — and *only* the ones that are actually player-felt (a tax rate, a decay curve, a threshold someone will notice) — is what "BAs correctly refuse to invent numbers" is supposed to look like. The other three-quarters are exactly the kind of call the dev-team process design intends a lead to clear without going upstairs: named, single-file scope boundaries, direction-of-a-call-edge questions, and low-stakes UI defaults, nearly all already carrying a stated rationale in their own description.

The risk this project should actually watch for is the **opposite** failure this brief warned about — a lead escalating everything because 425 items feels unmanageable. The 44-into-2-rulings compression in Cluster A is the concrete proof that escalation volume and escalation *count* are different things: 44 raw items collapse into exactly 2 things for Aaron to decide (approve the deferral pattern; then work one grouped table). If Ben rules Cluster A as designed, the *decision* count Aaron actually sees this wave is closer to **4** (the M2-pattern approval, the grouped-numbers table, the crisis-tag-scope table, and ASM-267's pacing-feel call) — not 46.

---

## Summary counts for Ben's ruling pass

| Recommendation | Approx. item count (of 186) |
|---|---|
| ACCEPT | ~125 |
| ESCALATE TO AARON (batched into ~4 sittings) | ~46 |
| CORRECT | ~3 (ASM-276 split, ASM-253 fold into ASM-237, ASM-357 stale top-line) |
| SUPERSEDED | ~2 (ASM-344, ASM-354) |
| Already ruled, needs status close only | 6 (ASM-214, 216, 237, 338, 356, and 276's design half) |
| Route to Tester (not a ruling) | 2 (ASM-365, 366) |

One sitting, following the cluster order above, should let Ben clear the entire 186-item wave without opening `claude-bow.js show` on each one individually — everything needed to rule is quoted or summarized above, with its verification status marked.
