# HEAVY CHECKPOINT — session bounce point

**Rebuilt by Bill, refresh #5, 2026-08-10 evening, ahead of a deliberate session bounce.**
**HEAD is `c81a4ce` on `main` (pushed, CI green). Working branch is `feature/pre-s3-gates`, pointed at the same commit, carrying uncommitted work from two finished agents plus Aaron's own in-progress files (§2).**

A fresh session recovers from **this file + `docs/build-log.md` + `node claude-bow.js list --by-seq` + `git log -15`**. This file is written to be self-sufficient: the agent transcripts from this session are gone.

---

## 1. Where we are

**Sprints 0, 1 and 2 are all CLOSED.** Sprint 3 (citizens — the plan's riskiest model) is next and is deliberately blocked; see §4.

| Sprint | State |
|---|---|
| S0 contracts | 15/15 ✅ |
| S1 walking skeleton | 10/10 ✅ — closed by MOD-015 `harness.headless` |
| S2 test rigs & perf CI | 6/6 ✅ — MOD-011, MOD-013, MOD-014, MOD-016, MOD-019 |
| S3–S11 | 0 of 62. Not started. |

Roughly 12 of 69 modules built. 107 commits. 239 Go files, 110 of them tests.

**The repo is PUBLIC** (`github.com/aaronukgarcia/Metropolis`) and `main` is **branch-protected**: required checks `build-test-vet` + `determinism-gate` + `lint` (strict/up-to-date), PR required at 0 approvals, linear history, no force pushes, no deletions. `enforce_admins` is deliberately **false** so GR#21's revert-first path survives a broken CI — with one human, an unfixable red gate is worse than a bypassable one.

**Direct pushes to `main` no longer work.** Branch + PR for everything, including the lead.

---

## 2. Uncommitted work — READ THIS FIRST

`feature/pre-s3-gates` at `c81a4ce`. Twelve files. **Three of them are Aaron's, not the team's.**

**Aaron's — DO NOT TOUCH, DO NOT COMMIT WITHOUT ASKING:**
- `docs/planning/acceptance/engine.citizens.md`
- `docs/planning/acceptance/engine.season.md`
- `docs/planning/acceptance/engine.world.md`

These are his S3 criteria. He held this commit deliberately so the BUG-036 author amend stayed a clean single-commit operation (§3). He will commit them himself, via branch + PR, on the corrected tip.

**Junior-F, BUG-034 perf gate — built, verified, NOT committed:**
- `internal/harness/synth/{doc,limits,perf}.go` (modified), `phasehooks.go` + `phasehooks_test.go` (new)
- `.github/workflows/ci.yml` — adds a `workflow_dispatch`-only `perf-1m-probe` job

**Junior-G, BUG-035 author guard — built, verified, NOT committed:**
- `claude-author-guard.js`, `claude-author-guard.test.js` (new), `.claude/settings.json` (wires it into the Bash + PowerShell PreToolUse chains)

Both packages pass `gofmt`/`vet`/`golangci-lint`/`go test -race`. Neither has been committed because the S2 merge and the BUG-036 amend took precedence.

---

## 3. What happened this session, and the checkpoints that were verified

Chronologically, with what was actually confirmed rather than assumed.

**Security backlog closed out.** SEC-030/031/032/033/034/037/038/039/041 all landed. Highlights worth carrying:
- **SEC-033** — the error ring's coalescing bound was justified by a stale number (9 guarded types when there were 14) *and* the wrong population entirely (the ring serves all 84 registry codes, not just guarded types). Ruled: **delete the bound, not raise it.** `push()` now coalesces through a `Code`-keyed map, exact and O(1). The index evicts in step with the ring so it cannot become the unbounded resource it replaced — verified at exactly 25 entries after a 10,000-push flood.
- **SEC-034** — the SEC-032 regression test **could not fail**. It called `runtime.Gosched()` to park the waiter, which structurally *closed* the race window it claimed to open. Replaced with a differential test carrying its own pre-fix loop and a two-way handshake: **241/500 pre-fix, 0/500 fixed.** Three drafts were inert at 0/500 before one bit.
- **SEC-037** — `Save()` wrote a valid gzip+SHA256 fixture containing **zero records** and returned `nil`, because `Records()` returns nil on a copied Recorder and `Save` never checked. Silent data loss reported as success, in code committed the same morning. Fixed at source (the ambiguity), not in `Save`.
- **SEC-036** — `RunCommandLoop` returned nothing, so a clean shutdown and a dying transport were indistinguishable. **Fourth instance of that pattern** (BUG-020, SEC-026, SEC-032, this).

**GR#22 enacted — codename discipline.** The reference title is `'Blue'` and only 'Blue'. 41 direct occurrences swept, then the expansion-pack identifiers renamed as indirect identification. `claude-codename-guard.js` enforces it, assembling its patterns from fragments so the guard never holds a forbidden literal.

**History rewritten and the repo published.** `git-filter-repo` scrubbed the reference terms and the real committer email across all history; verified by walking **every commit**, not just HEAD. 115 of 116 BOW git refs remapped; the one that didn't (`f9a460c` on INT-002) never resolved before the rewrite either — an auto-ref hook recorded a hash it never verified.

**BUG-036 — the correction that mattered.** The rewrite scrubbed history but **not the local git config**, so the very next commit republished `aaron@garcia.ltd` onto the now-public tip. Found by Junior-G deriving the sanctioned identity from repo data rather than trusting the brief. Fixed this evening, on Aaron's direction: protection relaxed → author *and* committer amended → `--force-with-lease` → protection restored.

**Verified after the fact, not assumed:** `git log --format='%ae'` and `'%ce'` across all 107 commits each return exactly one address. Tree diff `77a59f4..c81a4ce` empty (metadata-only amend). Protection confirmed back to the approved configuration.

**S2 shipped as PR #1** — first PR through the protected branch, all four checks green including the new `perf-smoke` job.

---

## 4. What is left, in priority order

**BUG-034 (P0) — blocks S3.** The perf gate runs at 2,000 citizens, not the 1M preset. A gate calibrated at smoke scale will not catch a regression that only appears at a million, which is the entire reason MOD-016 exists.

The round-trip still to run:
1. Commit + PR + merge the `feature/pre-s3-gates` work (§2)
2. `gh workflow run` the `perf-1m-probe` job — it is `workflow_dispatch`-only and the workflow must be on the default branch before it can be dispatched
3. Record the **runner-measured** numbers as the baseline. A locally-measured baseline is worthless for a gate running on GitHub's hardware — that mismatch is the same local-vs-CI divergence that kept CI red for this project's entire early history
4. Re-derive the noise floor against real CI jitter

Junior-F built the whole mechanism and **correctly refused to fabricate CI numbers** — it cannot dispatch a workflow without a commit, and juniors do not commit. Local sanity check only (explicitly *not* the baseline): 1M citizens / 3 months on the dev box ≈ 8.3s generation, ~13ms tick, ~50MB peak.

**Carry this caveat forward:** `engine.core` has **zero phase hooks**, so a 1M run today measures world *generation*, not simulation. Generation and tick costs are recorded separately and every result carries the phase-hook count, precisely so nobody quotes a "1M-citizen tick cost" that is walking-skeleton overhead wearing a simulation label.

**Then:** BUG-035's author guard commits with the same PR. Aaron's S3 criteria land on the corrected tip.

**Open and unstarted, roughly by value:**
- **BUG-024** — the mechanical AST gate replacing the hand-sweep. Criteria written (`docs/planning/acceptance/tool.astgate.md`, 15 ACs). Must enumerate **package-level functions taking a guarded type as an argument**, not just receiver methods — that blind spot cost nine manual enumerations by four agents.
- **BUG-029 (P2)** — the secret guard's entropy check flags word-segmented identifiers on length. **Four allowlist exceptions now exist solely to work around it.** The false positives are cheap; the permanent exceptions they leave behind are not. Fix: measure entropy per *segment*, never raise the threshold.
- **engine.invariant.md** carries ~13 ACs whose only check is that a test with a matching *name* exists — satisfiable while asserting nothing. Reported by BA-7, not rewritten, because its AC numbers are cited in landed code.
- **BUG-032** — file ownership cannot be disjoint across a breaking signature change. Best fix: give breaking changes their own dispatch, ahead of the work that needs them.
- P0s: FEAT-013, MOD-017, MOD-018 (dep-blocked on BUG-034), MOD-020.

---

## 5. Standing orders and hard-won rules

- **Prove every regression test can fail against the unfixed code.** On this project an inert test is the *default* outcome, not the exception. Three drafts of one test scored 0/500 against known-buggy code.
- **Never assert an upper bound on wall-clock in a CI-gating test** (BUG-031). A 100ms ceiling blew at 707ms on a shared runner and turned `main` red on a correct fix. Count work, not time.
- **Concurrency tests are deterministic, not probable.** Construct the state; do not race for the timing.
- **An acceptance criterion's CHECK must be able to fail** (dev-team-process v1.9). Three criteria files in one wave had checks that drifted from their rules; one let a security control be satisfied while not existing.
- **Assumptions are logged or the work is rejected** (v1.7). Every ruling in §3 came from an agent escalating rather than guessing.
- **File ownership is transferred, never duplicated** (v1.6.1). Now partly mechanical via `claude-dispatch-guard.js`.
- **Never `git add` a shared file by path during a multi-agent wave** (BUG-030). Review the diff; `data/errors.json` routinely carries three agents' additive ranges at once.
- **Banned for all non-lead agents:** `git stash`, `git reset --hard`, `git checkout --`, `git clean`, branch switches, commits, pushes.
- **An agent verifying git behaviour must state the exact revision it expects the repo to be at when it finishes** (BUG-035). A cleanup claim should name the end state, not list the steps taken.

---

## 6. Tooling built this session

| Tool | Purpose |
|---|---|
| `claude-dispatch-guard.js` | PreToolUse on the **Agent** tool. Denies unknown BOW codes, mkey mismatches, criteria that already exist, and file-ownership overlaps against live `sync_file_claims`. Fail-**open**. |
| `claude-codename-guard.js` | GR#22. Blocks staged content, commit messages and branch names. Fail-**closed**. Patterns assembled from fragments. |
| `claude-author-guard.js` | BUG-035. Rejects unsanctioned author/committer, including `--author=` and `GIT_AUTHOR_*`. Fail-**closed**. **Built, not yet committed.** |
| `/brief` skill | Compose dispatches from looked-up facts, not memory. |
| `tools/health/check.js` | Determinism gate, `-race` presence, CI conclusion vs HEAD sha, BOW queue, git sync, **branch-protection still on**. |

**The author guard is not a control over the operator's own identity** — it unions the configured identity into the sanctioned set by design. It stops fabricated authors. The durable control for BUG-036's class is the **gitconfig**, now set to the noreply address.
