# HEAVY CHECKPOINT — session bounce point

## REFRESH #8 — Bill, 2026-08-12 morning (read this section first; refresh #7 and older below)

**HEAD `b819c53`, pushed, CI green (run 31583407877, all 5 jobs, watched to completion).** The perf gate is LIVE, REQUIRED, and HONEST — the defining change since refresh #7.

### Landed since refresh #7 (commits 5d7b130 → b819c53)
- **`5d7b130`** — the whole perf-gate chain: BUG-071 (3 exit codes), BUG-083 (replay-frozen baseline + cumulative anchor), BUG-073/085/096 (read-boundary provenance), BUG-074 (no scanner cap), BUG-095 (git-committed acceptance registry `perf-accepted-regressions.json`), BUG-097/057/102, BUG-101 interim. Destructive-10 ACCEPT recorded on BUG-034. Poisoned 44ms baseline discarded (v2 cache generation + v1 entries deleted).
- **`303d3ac` + `5756db7`** — BUG-110: perfci's exit codes never survived `go run` (**go run exits 1 for ANY nonzero child — run built binaries when the exit code is a contract**). First diagnosis (pwsh) was wrong, disproved live, corrected on record.
- **`5bfc381`** — **perf-1m-probe is a required per-push merge check (4 of 4)**. Exit 1/2 block merges NOW; exit 3 warns until MOD-018's first real tick work flips it to hard-fail (mandatory recorded condition: ci.yml header, BUG-034, ASM-352). First real 1M baselines: ~6.7s wall, ~44MB peak, PerMonthTick 488–926µs (sub-floor, walking skeleton).
- **`b819c53`** — BUG-064/065: World gets the Engine-pattern copy-guard (MET-E406); compressV tests proven able to fail. ±0.8pp residual named + accepted by lead ruling.

### Rulings and state (2026-08-11/12)
- **Lead ASM sweep done:** ~55 P0/P1 assumptions ruled; only the dep-held remain (215/223/274/283/287 close via FEAT-047/056; 214 waits on Aaron's terrain licence file).
- **Aaron's interview rulings:** two-layer identity (commit-msg + pre-push backstop — ASM-386; sequencer verbs don't fire hooks); BUG-061 = redact command; BUG-034 = gate immediately (done); FEAT-041 deep-dive dispatched; ASM-267 hard = failure-risk floor; email-privacy flags confirmed ON (BUG-042).
- **Crisis taxonomy FULLY adjudicated (FEAT-013):** explicit Event.Crisis tag; include insolvency+ghost-city (with FEAT-068 mandatory pre-warnings) + water stockout; exclude terror attacks + all 12 exclusion rows incl. every intermediate spiral stage; data-file taxonomy never player-editable per-condition; one Pause/Notify/Off master switch. **ui.alerts is dispatchable.**
- **Six Aaron feature anchors filed with rulings attached:** FEAT-063 Helper (ask-driven panel v1; **standing constraint: every player-action feature registers advisor metadata NOW**), FEAT-064 checkpoints (bounded fork tree), FEAT-065 dev-mode console + feedback→BOW, FEAT-066 metrics dashboard, FEAT-067 weather-mode seed switch, FEAT-068 doom warnings.
- **SEC-021 ruled after a 4-round loop hit an architectural wall** (order-0 entropy can't separate the classes — two independent impossibility proofs on record): ship interim after the hostile-sha256-bundle allowlist fix; BUG-029 becomes the structural second-layer item.
- **BUG-088 escalations ruled:** checker wiring folds into FEAT-045; secret checker extends to pre-push first.

### Process changes IN FORCE
- **Commit-ready-list protocol (Aaron, 2026-08-12): Bill is never a blocker.** Pipeline-complete work posts its verdict+evidence ON THE BOW ITEM, team moves on, Bill sweeps and commits in batches. Feed prose is NOT a recorded verdict.
- **QA audit findings:** `bow_destructive_verdicts` has ZERO rows — GR#23 is prose-only until FEAT-040 (top of Bob's queue). FEAT-060 (BOW prose-dep lint), FEAT-061 (sprint entry gate), FEAT-069 (claude-sync unread-message delivery — filed after Bob's wake missed three standing-order messages).
- **FEAT-062's code.json audit RAN:** findings BUG-103..109 (P0: cmd/metctl + detgate unregistered). Remediation = one coherent registry-correction batch on Bob's queue.

### In flight / awaiting
- **Bob's commit-ready backlog** (FEAT-045, FEAT-059, BUG-088, BUG-090, SEC-048): blocked ONLY on verdicts being posted to the items. Bob's standing orders (3 feed messages, 2026-08-12 09:27–09:33 + URGENT-READ-FIRST) carry the full deep queue A–K.
- **Blocked on Aaron:** terrain50 licence file (he confirmed OS OpenData provenance), FEAT-047/056 proposal approvals when they arrive, crisis-taxonomy data file review at build time.
- **BUG-034 remaining scope:** noise-floor re-derivation from accumulating real-scale runs + the exit-3 hard-fail flip at MOD-018's first real tick work. Then S3 unblocks.

### Recovery procedure (unchanged in shape)
`metro` → checkin → **read the FEED (`node claude-sync.js read`) — step 3 is where Bob's wake failed today** → this file → `node claude-bow.js list --by-seq` + comments on in-flight items → `git log -10`. Never redo committed work; never stash/reset/clean the tree — it carries Bob's team's uncommitted pipeline output.

---


**HEAD `4c01266`, pushed, CI green (run 31473977195, confirmed COMPLETED not merely started).** Running as **Ben**, not Bill — the Bill slot was occupied and all three slots were live in different windows at once, which is a real cross-session file-ownership risk.

### Shipped since refresh #6
- **`engine.season` (MOD-027)** and **`engine.world` (MOD-017)** are committed — the first two Sprint 3 modules through the full pipeline including a Destructive verdict.
- **46 acceptance files committed, covering S6 through S11 COMPLETE**, plus S4/S5 finalised and `tool.destructiveguard`, `feat.protocolv2`, `tool.committhook`. The criteria estate now runs far ahead of the build queue, by Aaron's direction.
- **BUG-058's 18 registry edges** plus a `collaborations` drift gate in `generate.js`.
- **`quote-mask-drift.test.js`** — the control watching five copies of `buildQuoteMask`.

### The three things that matter most on resume
1. **BUG-071/083/095 — the perf gate is NOT a gate and this blocks all of Sprint 3.** It reported success while never gating (every smoke run measured below its own noise floor). The ratchet then proved live: 30 commits at 9% each drifted **13×** with zero signal. The two-threshold fix was then defeated because **`AcceptedRegression` is two struct fields anyone who can write the file can forge** — a forged record made a genuine unregressed run report a 216% regression. BUG-034, MOD-016 open; MOD-018 blocked.
2. **The commit-identity guard is architecturally dead and Aaron has ruled** (FEAT-045): **fifteen live bypasses across four rounds**, ending with `sudo git commit` — any ordinary leading word. Enforcement moves to a **`commit-msg` git hook** (a BA proved empirically that `pre-commit` never fires on `git merge`); the PreToolUse guard is demoted to advisory and must **fail OPEN**. **BUG-088: all four sibling guards share the same flaw** — one finding, four files — so the secret guard, version guard and plan guard are all silently disengaged by the same shapes.
3. **THE CROSS-CUTTING LESSON, worth more than any single fix: a control must be enforced where the fact is CREATED, not where it is RECORDED.** Three controls fell today to the same shape — a parsed command *string*, a self-declared `Measured` bool, a self-declared `AcceptedRegression` bool. Each asks the data to vouch for itself, and whoever writes the data writes the vouching.

### Process findings that changed how we work
- **BUG-075** — a report cited three ASM codes that were never filed, and the lead relayed one into two downstream briefs as fact. A citation that *resolves* passes a naive check while pointing elsewhere. **Verify cited codes resolve to what the sentence claims.**
- **BUG-090** — a Destructive's example commands were executed for real by the shell submitting them (backticks in a `--desc`), landing stray commits; its `git reset --soft` cleanup then discarded a lead commit that had landed on top. Recovered from the reflog. **The recovery rule is now in every brief: if you make a mess, report and stop — do not repair.**
- **Five caveats this session were wider than declared.** Verifying a declared limitation's *width* is now a standing Tester duty.
- **A regression corpus built from fixed defects proves you won't repeat yourself, not that you're safe** (BUG-091).

### Blocked on Aaron
**BUG-061** (GR#22 text in the BOW database — no sanctioned way to redact), **FEAT-041** (traffic numerics deep dive; MOD-023 dependency-blocked), and the crisis-taxonomy proposal's row-by-row verdict.

### In flight at this point
`engine.market` (MOD-020) at Destructive after two Tester passes; BUG-095/096/097 with a junior; the four sibling guards blocked on BUG-088. **BOW is 425 open / 645 total — the ASM backlog is the lead's ruling debt, not the BAs' fault.**

---


**Refresh #6, written by Bill 2026-08-10 late evening, immediately after EVERY running agent was killed simultaneously by a session limit (resets 23:50 Europe/London). HEAD is `1331ddb`, pushed, CI green. The working tree carries a large amount of uncommitted, mostly-verified agent work — see §2, and read it before touching anything.** A fresh session recovers from THIS file + `node claude-bow.js list --by-seq` + the BOW comments on the items named below + `git log -10`. Agent transcripts are gone; this file is written to be self-sufficient without them.

---

## 1. The headline: Golden Rule #23 exists now, and it earned itself the same day

**GR#23 — "Nothing Is Committed Un-Attacked"** was added to `CLAUDE.md` (commit `1331ddb`) at Aaron's direction. Every code-bearing commit requires a recorded Destructive verdict on its BOW item(s). No exception for "small", "obvious", "inherited", or "the lead wrote it" — that last one is the exception that actually got used.

It was added because the lead committed two pieces this morning without the v1.8 Destructive stage, on baseline evidence alone, because they arrived as inherited uncommitted work and looked finished. **Both were then rejected by their Destructives:**

- **BUG-035 author guard** — nine findings (BUG-044…052). Four are trivial bypasses of a live security control on a public repo, most notably `git -c user.email=fake@evil.com commit`: one ordinary command, reproducing the original incident. Root cause: the guard decided whether to engage by regex-matching a shell command string, and a regex is not a shell parser.
- **BUG-034 perf gate** — two of four claims falsified live (BUG-053, BUG-054). The drift guard is defeated by `register := e.RegisterPhaseHook` — a one-line Go method value, added *inside the very package the guard watches*, which is wider than the limitation its author honestly declared.

**Do not weaken GR#23 on the grounds that it is slowing things down.** The evidence that it is necessary is one day old and consists of two P0-adjacent pieces that a Tester-equivalent baseline had already blessed.

## 2. Working tree — uncommitted, and most of it is GOOD work, not debris

**Do NOT `git stash`, `git checkout --`, `git reset --hard`, or `git clean`.** Banned for non-leads; the lead commits by explicit pathspec only.

| Path | What it is | Pipeline state |
|---|---|---|
| `internal/engine/season/` (new) | **MOD-027 `engine.season`** — 18 ACs, 4 files | Tester PASS → Destructive ACCEPT w/ BUG-059 → junior was **mid-fix when killed**. `season.go` already contains `validateSchoolIntakeGateShape`, so the fix is partly or wholly in. **Verify it compiles and its tests pass before assuming either way.** |
| `internal/engine/world/` (new) | **MOD-017 `engine.world`** — P0 scale-risk, 17 src + 12 test files | Tester PASS → Destructive **incomplete** (killed mid-sweep). Junior was mid-bounce on 3 follow-ups (see §4). |
| `internal/foundation/astgate/` (new) | **BUG-024 AST gate** — 8 tests, clean lint | Junior stood down at a clean point under the throttle. Handover in §4. Contains a stray `probe_main_test_helper.go.bak` — scratch, not deliverable. |
| `claude-author-guard.js` + `.test.js` | **v2 rewrite** — regex replaced with a real parser; 42/42 tests | Junior delivered; Tester had verified BUG-044 only when killed. Needs Tester completion, then a Destructive **re-attack** (it was defeated once already). |
| `internal/harness/synth/*` (9 files) | **BUG-053/054 fixes** | Junior-Perf's last message claimed full-repo build/vet/test-race green. **UNVERIFIED BY THE LEAD — treat as a claim.** |
| `docs/planning/acceptance/*.md` (7 modified, 1 new) | S4 criteria (finance/consumption/unlocks/services, 76 ACs), S5 (traffic/roads, 45 ACs), market, and new `tool.destructiveguard.md` (30 ACs) | BA work, complete and ready. **Commit these promptly** — process v1.6.1: agent output existing only in the working tree is one concurrent write from being lost. |
| `master-plan-v2.1.json`, `code.json`, `tools/plan/bow-import.json` | Four `tool.*` keys registered per Aaron's ruling, regenerated | Verified: `generate.js` never touches the BOW DB; only the four new blocks changed, `moduleCount` 96→100. **`bow-import.json` was deliberately NOT imported** — doing so would rewrite every existing BOW row's metadata from the master plan and stomp concurrent drift (ASM-196). |
| `data/errors.json`, `data/seasonal.json` | MET-E400..E405 (world), MET-E500..E503 (season) + seasonal meta | Sanctioned — see the ASM-203 ruling in §5. |

## 3. Aaron's rulings and standing orders from this session (IN FORCE)

- **Agent throttle: 2 devs, 2 BAs, 2 testers concurrently.** Destructives were not capped but keep them proportionate.
- **BA effort stays BEHIND the build queue** — do not plan five sprints ahead. S4 and S5 criteria are done; **S6 and S7 are queued, not cancelled** (both BAs stood down having written nothing; their findings are in §6).
- **Register all four `tool.*` keys** (done) rather than writing a tooling exemption.
- **MOD-016 reopened** (done). A module whose own acceptance line is untrue is not done, even when reopening a closed sprint's gate. It closes when BUG-034 closes.
- **FEAT-041 — traffic numerics: DO NOT DECIDE, DO NOT DISPATCH.** Aaron is deliberately 50/50 and wants a deep-dive review. MOD-023 is dependency-blocked on it. `engine.roads` (MOD-024) is NOT blocked and can proceed.
- Prior standing orders (v1.8 pipeline, second Tester independence, v1.7 assumption logging + reciprocal rejection, Azure, rebase-merge-only) all remain in force — see refresh #4 history and `docs/planning/dev-team-process.md`.

## 4. In-flight work, per agent, at the moment of death

1. **Junior-Season / MOD-027** — fixing **BUG-059** (the "exactly one school intake month" invariant is prose-only; fixtures with two and with zero qualifying months both load happily). Wanted: a `Load`-time validation with a registry-sourced typed error, tests proven red-then-green. Last message referenced editing `validateSchoolIntakeGateShape`'s comment, so the code fix likely landed. **Then MOD-027 is ready to commit.**
2. **Junior-World / MOD-017** — three bounces: (a) strengthen `TestMemoryBudgetRealAllocationMatchesAccounting` to assert real allocated bytes (`runtime.MemStats`) instead of slice lengths — Tester-1 measured **962.8MB actual vs 789.6MB accounted, 22% out**, still inside the 4GB budget so AC-19 passes, but the test could never have caught it; (b) make AC-3's primary test able to fail a gutted `compressV` (it currently stays GREEN when `compressV` is broken to an identity function — only a secondary test catches it); (c) log an ASM for the geology pocket probabilities.
3. **Destructive-2 / MOD-017** — incomplete sweep. **Its unproven lead is the most valuable loose thread in this checkpoint:** `ImportAndPlaceStartTile` does `delete(a.w.tiles, a.w.startCoord)`, deleting the *entire* tile struct rather than only terrain fields — so a re-import of an already-purchased start tile silently wipes `owned`, `sim`, `ownerID`, `geology`, `prospected` with no error. Found by reading, **never run**. Also unattacked: the compression-test gap, the locking convention (a convention, not compiler-enforced — what happens when someone adds the 12th `WorldAPI` method?), and whether `World` is copyable with a silently-broken mutex (SEC-020 family).
4. **Tester-1 / author guard v2** — had confirmed BUG-044 is now denied; eight reproductions still to run. Was also told to check whether the *declared* limitations (ASM-228 alias-body with `-c`, ASM-229 wrapper list, ASM-230 `-C`/`-c <commit>`) are as narrow as declared — an understated caveat is precisely what sank v1.
5. **Junior-Perf / BUG-053+054** — claimed green, unverified.
6. **Destructive-1** — idle, last verdict ACCEPT on MOD-027.

## 5. Lead rulings made this session (recorded because a verbal ruling is an unlogged assumption)

- **ASM-203 ACCEPTED** — Junior-Season's edit to `data/seasonal.json` outside its declared path was sanctioned: AC-10/AC-18 require that file, so the brief's "and nothing else" contradicted the criteria it dispatched against, and **the criteria win**. The defect was in the dispatch. **Standing correction: a dispatch brief must enumerate every path the criteria require, data files included, not just the module directory.**
- **ASM-192/193/194/195 ACCEPTED** on `tool.destructiveguard` — see the two lead-ruling comments on FEAT-040 for the reasoning, especially why append-only verdict history beats a mutable field (the record of what an attacker tried and *failed* to break is evidence, not noise).
- **ASM-096 CLOSED as superseded** — but only after Tester-2 proved the existing drift test can actually fail, four ways. A triage agent had recommended the close on a code read alone. **A test nobody has watched fail is a claim, not a control.**
- **ASM-189 ACCEPTED** (gas as a ninth commodity).

## 6. Findings that outlived their agents — do not lose these

- **BUG-058 (P1) — `code.json` is missing call edges the spec requires.** Found *independently by two different BAs in one wave*, which is what makes it a class defect: `engine.build → engine.season` absent in both directions; `engine.finance → engine.consumption` and `→ engine.invariant` absent, with `engine.invariant`'s inbound contract entirely null. Under GR#20 a developer facing a spec-mandated but unregistered call **has no correct move**, so the rule silently becomes pressure to break it. Nothing mechanically checks this, so the true extent is unknown.
- **BUG-061 (P0) — GR#22 text in the BOW database, AWAITING AARON.** Git is provably clean (history, reflogs, dangling objects, stashes — all zero, and the codename guard was verified to actually catch reintroduction with 19 payloads). But `ASM-150`'s own record and, independently, `MOD-032` carry forbidden text as prose. The BOW is copied outward into checkpoint.md, commit messages and planning docs. **Blocked because `claude-bow.js set` cannot edit a title or description and CLAUDE.md forbids raw SQL for BOW writes — there is currently no sanctioned way to redact a BOW item.** Aaron must choose: a `redact` command, or a one-off sanctioned `UPDATE`.
- **BA-S7's finding:** "300 game-years headless in seconds–minutes" is **not testable as written**. It is a literal wall-clock claim (forbidden in CI here), and it cannot be translated to relative-regression either, because `harness.synth`'s presets are keyed to *citizen count*, not *simulated duration*. That axis does not exist and defining it is a design decision.
- **BA-S6's finding:** the S6 end-to-end scenario needs exactly one owning criteria file — written four times is a GR#3 violation waiting to drift, written zero times is an untested exit gate.
- **ASM-216 (P0):** cross-worker bit-identity is unachievable with per-shard float partial sums. Feeds FEAT-041.
- **Two empty data files block S5 ACs as written:** `data/modes.json`, `data/naming_corpus.json`.
- **The 1M perf probe RAN GREEN on windows-latest** (run 31425416549) — viability established, first-run baseline recorded. **The lead never read the wall-time/peak-memory numbers out of the log**, so no runner-measured figure has been recorded on BUG-034 yet. That is a required step before MOD-016/BUG-034 can close.

## 7. Cold-resume procedure

1. `metro` → checkin prints the BOW summary (also the metro DB health check).
2. Read this file, then `node claude-bow.js list --by-seq`, then the BOW comments on MOD-017, MOD-027, BUG-034, BUG-035, FEAT-040, FEAT-041, BUG-058, BUG-061. Then `git log -10`.
3. `git status` — expect §2's tree. **Do not stash/reset/clean it.**
4. **First action: verify what actually compiles.** `gofmt -l .`, `go build ./...`, `go vet ./...`, `go test -race ./...`. Two juniors died mid-edit; find out which packages are whole before dispatching anything.
5. Then, in order: finish MOD-027 (nearest to commit) → finish the author-guard Tester run and re-attack → Destructive-2's `ImportAndPlaceStartTile` lead → commit the BA criteria files (they are complete and at risk).
6. Re-dispatch with the v1.7 mandatory spawn block from `dev-team-process.md` verbatim — do not paraphrase from memory. Respect Aaron's 2/2/2 throttle.
7. Never redo committed work. Commits + BOW status are the truth.
