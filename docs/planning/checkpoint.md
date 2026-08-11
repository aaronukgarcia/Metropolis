# HEAVY CHECKPOINT — session bounce point

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
