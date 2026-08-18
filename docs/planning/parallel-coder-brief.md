# Parallel Module-Build Briefing (Ben / Bill / any coder session)

**You are one of several Claude sessions working on Metropolis AT THE SAME TIME, in the SAME git checkout (`E:\git\Metropolis`).** Read this whole document before writing any code. It tells you exactly where you may work, where you may NOT, how to build a module properly, and — most importantly — how to not destroy another session's work in the shared tree.

---

## 0a. WORKTREES — YOU NOW HAVE YOUR OWN DIRECTORY (2026-08-18, supersedes shared-checkout working)

Each coder session now works in its **own git worktree** — a private directory + private branch over the same repository, so branches, uncommitted files, and builds no longer collide:

- **Ben:** work in `E:\git\metropolis-ben` (branch `lane/ben`). Your cafe commit is already there; your uncommitted stub modules were copied in.
- **Bill:** work in `E:\git\metropolis-bill` (branch `lane/bill`). Your policies work-in-progress was copied in.
- **Bev:** stays in `E:\git\Metropolis` (the main checkout).

**Rules:** (1) `cd` into YOUR worktree at session start and never work in the main checkout again; verify with `git worktree list` + `git branch --show-current` (must be your `lane/<name>` branch). (2) Commit/push on your lane branch; open a PR per module (or small batch) from `lane/<name>` → `main`; rebase-merge on green. (3) Keep your lane fresh: `git fetch origin main` + `git rebase origin/main` on YOUR OWN branch only (allowed — it's your worktree; still NEVER reset --hard/clean/stash). (4) The Section-0 shared-tree bans still apply inside your worktree for safety habits, and the main checkout is now a NO-GO zone for coders entirely. (5) After confirming your worktree has your files, delete YOUR OWN leftover uncommitted copies from the main checkout (`E:\git\Metropolis\internal\engine\<yours>`) — nobody else will touch them.

## 0b. THE SHARED-TREE RULES (still read them — they built the habits above)

Every session edits the SAME working directory. Other sessions have **uncommitted work sitting in this tree right now**. Therefore:

- **NEVER run any git command that discards or reverts the working tree.** BANNED, always: `git checkout -- <path>` / `git checkout .` / `git checkout -f`, `git reset --hard` / `--keep`, `git restore` (non-`--staged`), `git clean -f/-d/-x`, `git stash`. Any of these can silently delete another session's hours of uncommitted work (GR#24). To undo your own change, copy the file to a `.bak` first and restore from that copy, never from git.
- **NEVER `git add -A`, `git add .`, `git add -u`, or `git commit -am`.** Those stage EVERYONE's uncommitted files, not just yours. **Always stage your exact file paths**, e.g. `git add internal/engine/parking/api.go internal/engine/parking/api_test.go`.
- **Only edit files inside YOUR assigned module directories** (Section 1). If the tree doesn't compile because of someone ELSE's uncommitted work, that is THEIR lane — do NOT "fix" it, do NOT touch their files. Build/test only YOUR packages: `go build ./internal/engine/<yourmodule>/... && go test ./internal/engine/<yourmodule>/...`. (`go build ./...` will fail on others' in-progress work — that's expected and not your problem.)
- **Check in and claim before you start:** `node claude-sync.js checkin`, then `node claude-sync.js claim internal/engine/<module>` for each module before touching it. Poll `node claude-sync.js read` regularly. Check `git log -10` and `node claude-bow.js show <CODE>` before starting a module to be sure no one else has touched it.

---

## 1. WHERE YOU MAY WORK (your lane)

You own a fixed set of engine modules. Work ONLY inside these directories and their acceptance docs:

- **Ben:** `internal/engine/{traffic, parking, dispatch, staffing, cafe, shopping, tunnels}/`
- **Bill:** `internal/engine/{tourism, extcommute, prison, policies, destination, fuel, airunits}/`

Each module has a spec at `docs/planning/acceptance/engine.<name>.md` and a registration in `code.json`. Your job: implement the module's Go package under `internal/engine/<name>/` **to satisfy its acceptance criteria** (see Section 3 — real implementation, not a stub).

## 2. WHERE YOU MAY NOT WORK (no-go zones — never edit)

- `internal/foundation/integration/` — the integration engine (Bev owns it; it is under active construction).
- `internal/engine/compose/` — the composition root. Do NOT wire your module into the tick; that is Bev's integration step, done later.
- `data/errors.json` — you MAY add error codes, but ONLY in YOUR module's assigned MET-range via the `/new-error` skill (never hand-pick a range; e.g. `MET-F900–F919` is `foundation.integration`'s and is off-limits — do not reuse another module's codes).
- `code.json`, `docs/planning/master-plan-v2.1.json`, `tools/plan/*` — the registry/plan (Architect-owned; edits go through `/register-guid`).
- The other coder's 7 modules, and the "held" tooling files (`claude-sync.js`, `claude-startup.js`, `tools/bow-server.js`, `bow-ui-template.html`, `.gitignore`).
- **GR#25:** do NOT add any cross-module call/edge that isn't already registered in `code.json`. If your spec seems to need a new dependency, STOP and flag it to Aaron/Bev — do not hand-write it.

## 3. HOW TO BUILD A REAL MODULE (not a stub)

**A single 45-line file with `return 5.0 // stub placeholder` is a contract STUB, not the module.** GR#20 requires every module to keep a passing stub — that's a fine *starting skeleton* — but the deliverable is the module that actually implements its acceptance criteria.

- Read `docs/planning/acceptance/engine.<name>.md` FULLY and implement each AC with **real logic**, not a hardcoded return. (e.g. `engine.traffic`'s `CommuteHours` must compute from the actual model the spec describes — a shortest-path/assignment/mode-choice result — not `return 5.0`.)
- **GR#15:** every player-felt number (rate, threshold, capacity, cost) lives in a `data/<module>.json` file (add one if needed), NEVER as a Go literal. Placeholders are fine, but they live in data with a disclosure, not `return 5.0`.
- **GR#21 determinism is SACRED:** seeded RNG via `det.NewStream(...)`, never `time.Now()`/`rand` in sim logic, never `for range` over a map on a tick path.
- **Consume dependencies via their registered `code.json` interfaces**, and **test-stub** them in your unit tests. Do NOT construct other real engines.
- **GR#20 copy-guard:** keep the `checkNotCopied()` / `self atomic.Pointer` guard on your API's public entry points (your stubs already have this — good).

## 4. REAL TESTS, NOT VACUOUS ONES

A test that calls `CommuteHours` and asserts it returns `5.0` when the code literally does `return 5.0` proves NOTHING — it's a self-fulfilling test (a banned antipattern here). Your tests must:
- Assert the **acceptance criteria's behavior** (the AC's observable property), not that a constant equals itself.
- Be able to **FAIL** if the logic is wrong — before you trust a green test, break the logic on purpose and confirm the test goes red, then fix it.
- Include a determinism check where the AC involves seeded behavior (same seed ⇒ same result).

## 5. THE PIPELINE — every commit is attacked (GR#23)

"It compiles and my tests pass" is NOT done. **Every code-bearing commit requires a recorded Destructive verdict** (someone actively trying to break your module) before it can commit — the commit is mechanically blocked otherwise.
- After building + your own tests pass, run a Destructive pass on the module (attack: determinism, bounds/overflow, nil, concurrency, does it actually meet the AC or just look like it), then record: `node claude-bow.js destructive <CODE> --verdict accept --attacker "<you>" --note "..."`.
- Only then commit: `git add <your exact paths>; git commit -m "feat: engine.<name> ... [engine.<name>]"` → `node claude-bow.js ref <CODE> <hash>` → `done`.
- Verify first with the cheap gates or the `/ci-green` skill: `gofmt`, `go build ./internal/engine/<name>/...`, `go vet`, `go test -race -count=2 ./internal/engine/<name>/...`.
- **Push every commit the same session** (`/push`) — small PRs per module. Merge only on green CI.

## 6. YOUR CURRENT 7 (Ben) — next step

You've landed the **contract stubs** for traffic/parking/dispatch/staffing/cafe/shopping/tunnels — that's a valid GR#20 skeleton and a fine first step, but they return placeholders and their tests are self-confirming, so they are NOT the acceptance-criteria modules yet. Next: flesh each stub into the real implementation per Section 3–4, one module at a time, each through the Section-5 pipeline. Also: the `MET-F905–F910` codes you added to `data/errors.json` belong to `foundation.integration` (Bev's) — please revert those additions (leave `data/errors.json` alone) and use `/new-error` for any codes YOUR modules need. And please don't edit `internal/foundation/integration/` at all — that's Bev's active work.

---

**Summary:** stay in your 7 module directories; never `git add -A` or any tree-reverting git command (shared tree); build the real acceptance-criteria logic (not stubs) with tests that can fail; every commit goes through a Destructive round; check in + claim + poll to coordinate. Questions or a spec that needs a new edge → ask Aaron/Bev, don't guess.
