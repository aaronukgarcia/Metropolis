# HEAVY CHECKPOINT — session bounce point

**Rebuilt by RM, refresh #4, 2026-08-10, at Bill's direction after he flagged the board as badly stale (a narrow serial queue ran while RM/BAs/Docs/QA sat idle — his own callout, Aaron backed it). Full detail and reasoning for everything below lives in `docs/build-log.md` — read that first, this file is the state summary + resume procedure. HEAD is `f12c8ac`, pushed, working tree has active uncommitted work from the current dev wave (see §2). A fresh session recovers from THIS + `docs/build-log.md` + `node claude-bow.js list --by-seq` + `git log -15`.**

## 1. Where we are

- **Sprint 0**: contract-freeze review still pending Aaron (`docs/design/README.md`); cloud half resolved (Azure confirmed, see §6).
- **Sprint 1: CLOSED.** All ten items shipped, including the walking skeleton (FEAT-006, `ad1c308`) as the exit gate. Full commit list in `docs/build-log.md` §1.
- **CI: fixed twice.** First, BUG-004 (CRLF vs `gofmt` on Windows runners — invisible locally, failed *every* CI run since commit #1) + BUG-005 (scheduling-dependent test) — `d5c9c19`, `76e1961`, first green run `31314318798`. Then **BUG-021**, Bill raised against himself: watched a run *start*, never confirmed it *finished*, two more commits landed on top, `main` was red for three commits. Fixed `8833e52`. `golangci-lint` now in the Tester baseline (`f12c8ac`) — it's a CI-blocking job nothing local matched, so a lint error walked past everyone. **Branch protection on `main` is still not configured** — the one open half of BUG-021/BUG-006's DoD.
- **A full security sweep**: three Destructive agents (a new v1.8 pipeline stage), 19/19 built modules scanned initially, now **25 findings total, 8 open** (`node claude-bow.js weakness`). A five-round concurrency chain (SEC-003→014→016→018→019) found the same root cause five times, each round by a verifier, never the fixer; Aaron ruled **all nine** copyable-mutex-bearing structs repo-wide get runtime guards (SEC-020), no tiering. Waves 1-2 done (`InProcTransport`, `StubEngine`, `debug.State` under re-attack); **wave 3 in flight now** (last of the nine types).
- **Fresh finding this morning: SEC-024 (P0), unassigned.** `serialize.Header.DebugTouched` is a plain exported bool — any `*Header` holder can clear it directly, bypassing every SEC-020 guard. Found re-attacking wave 2. Not part of the current dev wave. **Top priority, see §4.**
- **Sprint 2: has not started.** Zero movement across the whole security-focused session. BA-2 is keeping its criteria refreshed to `active` so it's ready the instant it opens. `MOD-013 harness.replay` is dep-ready (`INT-001`/`INT-002` both done) and is the keystone item.
- **Process is now v1.8**: Destructive agent stage, v1.7 assumption-logging rule (+ v1.7.1 fast path, v1.7.2 fixing-a-fix), four weakness patterns, `git stash` banned for non-leads, golangci-lint in the Tester baseline. Full text: `docs/planning/dev-team-process.md`.

## 2. Working tree state (uncommitted, live dev wave)

`git status` shows modified: `data/errors.json`, `internal/engine/debug/{cheats,errors,fidelity,inspector,state}.go`, `internal/engine/stub/{codes,engine}.go`, `internal/foundation/registry/registry.go`, `internal/protocol/subscription.go`; untracked: `internal/engine/debug/copyguard.go` (+`_test.go`), `internal/engine/stub/bug020_test.go`, `internal/engine/stub/sec020_test.go`. This is the **current 4-junior wave's in-progress work** (registry / solver+errs / protocol SeqTracker+SEC-023 / ui.screens+SEC-009) plus Destructive-2's `debug.State`/`StubEngine` re-attack material — **do not `git stash`, `git checkout --`, `git reset --hard`, or `git clean`** (banned for non-leads since the VERSION-fixture incident; leads commit by explicit pathspec only). If this session bounces, treat these as in-progress deliveries to verify against their briefs, not to redo from scratch — check BOW comments on the four wave-3 items first.

## 3. Team (live now, refresh #4)

Per `docs/planning/dev-team-process.md` v1.8 (caps: 4 dev / 2 tester / BA uncapped-disjoint / 1 docs / 1 QA / 1 RM; **Destructive has no documented cap** — 3 running today, flagged to Bill as a doc gap, not a breach):

- **Dev-1..4** (cap 4/4): `foundation/registry`; `foundation/solver`+`foundation/errs`; `internal/protocol` (SeqTracker+SEC-023); `internal/ui/screens` (both screens+SEC-009). All SEC-020 wave 3 — the last wave for the nine-type scope.
- **Tester-1, Tester-2** — both idle, cleared their queues. Should be fed wave-3 items incrementally as each completes, not batched.
- **Destructive-1, Destructive-3** — idle. **Destructive-2** — busy, re-attacking `debug.State` then `StubEngine` next.
- **BA-1** — writing criteria for the security backlog (first time security fixes get BA criteria instead of ad-hoc briefs).
- **BA-2** — refreshing Sprint 2 criteria to `active`; Sprint 2 not started.
- **Documentation, QA** — idle; Bill dispatching QA next.
- **RM** — this file + team-board.md, refresh #4.

**RM flagged to Bill this refresh: 6 of 11 active-role agents were idle with no dependency reason** (both Testers, 2 of 3 Destructives, Docs, QA-pre-dispatch) while 4 devs + 1 Destructive worked. Full proposed assignments for each idle agent are on `team-board.md`.

## 4. RM's ranked recommendation for after wave 3 (full detail + rationale: `team-board.md`)

1. **SEC-024 (P0)** — assign immediately, does not need to wait for wave 3. Highest-priority open item on the board, currently touching nobody's queue.
2. **Let wave 3 land** — closes the SEC-020 nine-type scope, Aaron's ruling satisfied.
3. **SEC-011 (P1)** — terminal-escape injection, cross-cutting (`ui.core`/`ui.widgets`/`ui.screen.debug`), same root cause as the already-fixed SEC-022. BA-1 already drafting its criteria.
4. **Branch protection on `main`** — BUG-021's remaining DoD item, config-only, parallel-friendly.
5. **Open Sprint 2 in parallel** (`MOD-013 harness.replay`) once 1-3 land — don't wait for the remaining P2/P3 backlog (SEC-021/025, BUG-011/012/013/014/016/017/018/019/020); run those alongside Sprint 2, not ahead of it.

**RM's honest read on the security/Sprint-2 balance** (Bill asked directly): the spend to date was justified — SEC-001 (path traversal), the SEC-003→019 chain (fatal crash class, invisible to every test), BUG-007 (live panic path), and five hook bypasses were all real, not theoretical. But the balance is now due to shift: once SEC-024 + wave 3 + SEC-011 land, there is no remaining P0/P1 justification for holding Sprint 2 fully paused. That's "one more focused wave," not "keep going indefinitely."

## 5. Standing orders & rulings from Aaron (STILL IN FORCE)

- Dev-team pipeline **v1.8** mandatory: BA criteria → Sonnet junior → Tester pass/fail-never-fixes → **Destructive attacks it, may reject → Docs (.md-only) → Bill final review → commit**. Saturation rule; heavy checkpointing; staging-area discipline; `git stash` banned for non-leads (v1.5.1 addendum); v1.6 Second-Tester independence; **v1.7 assumption-logging + reciprocal rejection duties + mandatory spawn briefing block** (read from `dev-team-process.md`, don't reconstruct from memory); **v1.8 Destructive agent stage + four weakness patterns**.
- **v1.7 in one line:** log an `ASM-` item (with `--code-path`/`--codejson`) for anything you decided that the spec/criteria/brief didn't decide for you; devs reject asks resting on unlogged BA assumptions; Testers FAIL work carrying unlogged assumptions even on all-criteria-PASS; the lead's own rulings count too.
- **v1.8 in one line:** after the Tester PASSes, a Destructive agent attacks the work (input validation, bounds, type confusion, encapsulation, insecure call-ability, concurrency, resource exhaustion, error-path disclosure) and may reject it back to the same junior; findings are `SEC-` BOW items with a mandatory weakness class; `node claude-bow.js weakness` flags any class recurring 3+ times as a teaching signal, not just a ticket count.
- **SEC-020, Aaron's ruling:** all nine copyable-mutex-bearing structs get the full pipeline — the tiered "guard the severe ones, document the rest" option was recommended and explicitly overruled.
- **SECOND TESTER, v1.6** (still in force): 2 independent Testers, disjoint items, never communicate, one item never gets two verdicts.
- **CLOUD DECISION, 2026-08-09: Azure, confirmed, until otherwise agreed.** Existing garcia.ltd Azure estate reusable (storage account `garcialtdstorage`, RG `garcia`, region `uksouth`; ACR `prixsixacr`; Container Apps env `prixsix-env`, scale-to-zero) — full detail on BOW item **MOD-069**, cite via `node claude-bow.js show MOD-069`. **Key ruling: Metropolis Blob saves get their OWN container — never reuse `whatsapp-session`.**
- **Interim CI control, superseded by BUG-021's lesson:** "after any push, run `gh run list` and eyeball it" was written after BUG-006 and failed within hours because *watching a run start* was mistaken for *confirming it finished*. The control now means: wait for and read the completed result, not the launch. Branch protection (mechanical enforcement) is still pending — see §4 rank 4.
- **Docker/WSL reality (2026-08-09, `docs/build-log.md` §11):** Docker Desktop works (Linux containers, x86_64) but its CLI isn't on PATH — invoke by full path. WSL's only distro is `docker-desktop` (Docker's internal VM) — not a route to general Linux tooling. Docker is now the way to get a genuine Linux CI-equivalent run locally, directly relevant after BUG-004 proved "passes locally" was never evidence about CI.
- "update" (bare word) = run /update skill. Tile decision: option (a) artistic compression (in data/georef.json). Go confirmed over C#. MOD-001 cancelled; metro BOW is the project BOW.
- OPEN question to Aaron: the contract freeze (OD f32-vs-f64, duplicate correlation-ID generators — `docs/build-log.md` §9). Nothing else outstanding.

## 6. Cold-resume procedure

1. `metro` launch → checkin prints BOW summary (metro DB health).
2. **Read `docs/build-log.md` first** — it carries the reasoning, this file carries the state. Then this file + `node claude-bow.js list --by-seq` + `git log -15 --oneline`.
3. `git status` — expect the uncommitted wave-3 work described in §2. Do NOT stash/reset/clean it. If it's gone, the four wave-3 items bounce back to fresh devs against their original briefs (check BOW comments for exact scope).
4. Re-spawn Tester-1/Tester-2, Destructive-1/2/3, BA-1/BA-2, Docs, QA per §3. **Every spawn uses the v1.7 mandatory briefing block from `dev-team-process.md`** — do not paraphrase it from memory.
5. Do NOT redo committed work (git log + BOW `done` status = truth). Hooks are LIVE on both Bash and PowerShell — every commit needs a valid `[mkey]` tag when touching cmd/internal/data.
6. Dispatch SEC-024 (P0, unassigned) before anything else new — see §4.
7. Do not dispatch or estimate FEAT-031 (F1 overlay cycle) before ASM-006's renderer-additive-layer spike has actually happened, even though it looks dependency-ready — this was flagged in a prior refresh and remains true.
