# Metropolis — Full Code-Base Review & Recommendations

> **Date:** 2026-08-23 · **Reviewer:** opencode session (read-only sweep + three parallel survey agents)
> **Scope:** Go engine/UI/foundation/harness/cmd (~253k LOC across ~1,400 files), root Node guard tooling (37 scripts), CI, plan/data pipeline, docs.
> **Method:** read-only; nothing was modified except this file.

---

## Executive summary

The code base is in unusually good shape for its size. The mechanical gates that exist
(determinism gate, AST copy-guard, error-source scan, depguard UI/engine split, perf ratchet,
units-lint) genuinely run in CI and are argued in-line in the code. The systemic risks found
are almost all of one shape: **they live where the mechanical gates don't look** — the
determinism gate never runs against the composed engine, two packages paint error codes in a
form the scanner regex can't see, and six scripts in the commit/push path have zero tests.

Top five actions, ranked:

| # | Action | Severity |
|---|--------|----------|
| 1 | Point the determinism gate at the **composed** engine (compose.Wire hook set), not bare-core | HIGH |
| 2 | Fix scanner-evading hand-painted MET codes in `parking`/`tunnels`; registry-source them | HIGH |
| 3 | Add tests to the six untested commit/push-path Node scripts | MED |
| 4 | Rewrite public README; scrub local paths / Windows identity from docs (fix forward) | MED |
| 5 | Add a Linux GOOS leg to CI (Node job already proves Linux runners work) | MED |

---

## 1. Go code — findings

### 1.1 [HIGH] Determinism gate does not cover the composed engine

`detgate.RunGate` has no callers outside detgate's own tests, and those run it on a bare
`core.NewEngine` — **zero phase hooks**. Production runs compose's 9-module hook set
(traffic/citizens/build/attract/crime/leisure/refuse/services/firms), including hash-stream
emigration and month advances. CI therefore hash-compares an engine shape nobody ships.

- Evidence: `internal/engine/detgate/gate_test.go:23-24` — the comment "engine.core has zero
  registered PhaseHooks today" is stale post-FEAT-082.
- Risk: any nondeterministic ordering bug in any composed hook ships green.
- **Recommendation:** add a CI leg (or extend `TestDeterminismGate`) that builds via
  `compose.Wire` and hashes the same seed/months/runs triple against the composed hook set.
  This is the single highest-value change in this review.

### 1.2 [HIGH] Hand-painted error codes escape the GR#7 mechanical gate

- `internal/engine/parking/api.go` — 13 sites (`MET-E_PARKING_01`, etc., lines ~82–431)
- `internal/engine/tunnels/api.go` — 6 sites (`MET-E_TUNNEL_xx`, lines ~39–147)

These codes are absent from `data/errors.json`, carry no correlation ID/module/severity, and
**bypass the source scanner** because its regex requires digits immediately after the layer
letter (`foundation/errs/source_scan_test.go:83`), so `MET-E_PARKING_01` doesn't match.
Same collision class BUG-008 was built to stop.

**Recommendation:** register real MET codes for both packages via `tools/plan/add-error.js`,
convert all 19 sites to `errs.New/Wrap`. Optionally harden the scanner with a second pattern
catching underscore-style painted codes so they fail the build instead of hiding.

### 1.3 [MED] Bare `fmt.Errorf` on a production load path

`internal/engine/build/zone.go:139–203` — seven bare errors in zone load/validation with no
registry codes at all. Convert to `errs.New` with registered codes.

### 1.4 [MED] Silent mid-tick swallow of migration failure

`internal/engine/compose/compose.go:1486–1489` — when `attractHook.ApplyEffect` fails, the
hook logs MET-G801 and returns; the tick continues, so `peopleDelta` / `netMigration`
silently miss that month's applied change and the invariant checker sees a clean-but-wrong
ledger. Logged is not surfaced.

**Recommendation:** propagate into the phase result (or record a run-verdict-visible flag)
so a failed migration month cannot pass silently inside an otherwise green tick.

### 1.5 [MED] `mintMigrantID` returns 0 instead of failing on copy-guard violation

`internal/engine/attract/migration.go:268–276` — a copied-API call silently mints id 0;
safety depends on birthMigrant's second check two calls later instead of failing at the mint.
Fail loudly at the mint site.

### 1.6 [MED] Emigration structurally blind to migrants

`internal/engine/compose/compose.go:1637–1643` — `residentIDs` enumerates only
`[1, nextCitizenID)`, excluding the `[2^62, …)` migrant range. Documented limitation, but the
decline branch's hazard model never sees exactly the population migration adds, growing each
month. Schedule closure or an explicit invariant asserting the blind spot is acceptable.

### 1.7 [LOW-MED] Dead-weight noop slots contradict compose's own policy

`world` and `market` register `noopHook`s occupying PhaseProduction / PhaseConsumptionShortfall
slots (`compose.go:220,243`, defined at `compose.go:1076–1092`), while `compose/doc.go:137–139`
declares "a stub that occupies a phase slot and does nothing is dead weight, not a composition."
Either implement or remove the slot occupation.

### 1.8 [LOW] Duplicated phase-name list mirrored into UI

`monthlyPhaseOrder` is hand-copied into `ui/screens/debug/phase.go`; drift is caught only by a
CI test importing the engine (`debug/determinism_test.go`), not by the compiler — a rename
lands as a red main run. Acceptable under the sanctioned test-import ruling, but consider a
generated constant shared via protocol.

### 1.9 [LOW] Hardcoded behavioural placeholders without BOW refs in-code

`shopping/api.go:110` (`OnlineDeliveryShare: 0.15 // TODO/STUB`), `cafe/api.go:359`,
10+ `TODO-SPEC` sizing guesses in `foundation/solver/sizing.go` / `problems.go`.
Each commented, none carries a BOW key in-code. Add `[BUG-xxxx]`/`[FEAT-xxxx]` refs so a
grep finds the owning item.

### 1.10 [LOW] Coverage asymmetry in later-wave modules

freight 5.1k LOC / 4 test files (has `conservation.go`!), refuse 4.3k/3, spiral 2.7k/2,
policies 3.5k/5, tourism/farming/fuel/tax/prison/fdi/spaceport/destination/households/
extcommute ~5–6 files / 1 test each — far below the estate norm. Prioritise freight first
(conservation-relevant).

---

## 2. What is genuinely strong (keep doing this)

- **Error infrastructure:** all errors flow through `foundation/errs.New/Wrap` (code,
  correlation ID, module, template); unregistered codes degrade loudly to MET-F003; an AST
  source scan mechanically verifies every raised code is registered and range-owned.
- **Determinism hygiene:** no wall clock / rand on sim paths (citizens even source-scans for
  `rand.New`); counter-based hash streams (`det.NewStream(seed,id,month,...)`); fixed slices
  everywhere maps would leak iteration order; int64 micropound money, floats routed through
  `foundation/num` saturation/guard choke points.
- **Concurrency:** seal-before-first-tick lock-free phase reads, deterministic lowest-shard
  error selection, SEC-020 atomic copy-guard enforced by the AST gate, `-race` on everything
  including the determinism leg. No leaks or unsynchronised state smells found.
- **Composition root discipline:** single `compose.Wire`, fixed registration-order slice,
  citizen-ID namespace contract defended three ways (the FEAT-169 fix).
- **UI/engine split is real:** depguard rule runs in CI; every live ui→engine import is in a
  `_test.go` file; several UI packages self-scan their own source as belt-and-braces.
- **Plan pipeline:** `tools/plan/generate.js --check` passes live (168 modules, acyclic);
  GUID carry-over, atomic writes, validate-before-write; `code.json` has both claimed
  top-level sections (`units`, `conventions`); all 55 `data/*.json` parse cleanly; OS Terrain
  50 licence file present.

---

## 3. Node guard tooling — findings

Architecture is better than expected: DB access centralised in `claude-db.js` (with 4s
connect timeout so a dead DB can't hang a commit); git-command recognition deliberately reused
from `claude-author-guard.js` rather than re-hand-rolled; fail-open vs fail-closed posture is
explicit per-file header and consistent with CLAUDE.md claims. Tests are extraordinary in the
critical paths (destructive-guard: 98 KB source / 185 KB tests).

Gaps:

1. **Six scripts have zero tests**, several sitting directly in the commit/push path:
   `claude-pre-push-check.js` (GR#19 gate), `claude-bow-ref-check.js` (PreToolUse),
   `claude-bow-autoref.js` (PostToolUse DB writer), `claude-ping-check.js` (permit renewal —
   a bug here self-lockouts windows), `claude-memory-prefetch.js`, `claude-reflection.js`.
   Also `tools/bow-server.js`. **Recommendation:** bring the two commit-path guards up to
   tested standard first; per GR#23 these are full-tier code.
2. **ASM-386 (already P0 in BOW):** cherry-pick/revert/am bypass both git hooks on this
   environment (sequencer verbs don't fire commit-msg). Confirmed self-declared in
   `githooks/commit-msg` header. Keep it P0; a wrapper alias or server-side check is the
   eventual close.
3. **No `npm test` script** — package.json has no scripts section at all; everyone must
   remember bare `node --test`. Add `"test": "node --test"` and use it in ci.yml.
4. Root debris: delete `metctl.exe`, `astinfo.exe` (both ignored build artifacts), `nul`,
   `~$status.md`. Decide a home for tracked root-level `status.md` (an audit report dated
   2026-08-16 — belongs in `docs/planning/` or should be removed; CLAUDE.md says status
   reports live outside every checkout).
5. `settings.json` lists identical guard sets twice for Bash and PowerShell matchers — pure
   duplication, drift-prone when adding the next guard.

---

## 4. CI / build — findings

Five jobs exist: build+vet+test(-race)+gofmt, determinism-gate, golangci-lint (pinned 2.5.0),
node-test on ubuntu against MariaDB service container, perf smoke + REQUIRED 1M probe.
Well-annotated with BUG archaeology. Gaps:

1. **Go testing is Windows-only.** Every historical "passes locally, fails on CI" defect was
   CRLF/gofmt-class — exactly what a Linux GOOS leg catches. The node-test job already uses
   ubuntu-latest, so the runner cost is known-good. **Recommendation:** add a Linux
   `go test ./...` job; Docker flakiness makes the local route unreliable.
2. Perf baselines live in the Actions cache (LRU-evictable, prune-to-3). Documented tradeoff,
   but cache loss = silent baseline reset. Consider committing a small seed baseline to git.
3. No scheduled (cron) run; everything is push/PR-triggered. A weekly full-suite cron would
   catch dependency rot (GR#10) between waves.
4. go.mod is exemplary: Go 1.25, one direct dep (tcell v2.13.10), nothing deprecated.
   `.golangci.yml` is sane; the depguard glob was verified to actually match (two prior silent
   failures honestly documented in its header).

---

## 5. Data / plan pipeline — findings

- All 55 `data/*.json` parse cleanly; terrain tiles + OS licence present; georef.json carries
  honest provenance ("NOT yet cross-checked") — matches open ASM-214.
- Minor naming inconsistency: snake_case (`attract_terms.json`) vs concatenation
  (`external_world.json`, `capexport.json`) vs kebab (`keymap-default.json`). Cheap to
  standardise next time a file is touched; not worth churn on its own.
- `generate.js` reports collaborations declared for only 16/168 modules — the BUG-058
  drift-gate's own coverage limit. Worth a BOW item to raise collaboration coverage before
  more cross-module systems land.

---

## 6. Docs / public-repo hygiene — findings

README.md is stale and leaky for a public repo: calls the project "Private", explains the
`metro` Claude launcher to visitors, says "architecture TBD" despite a 149 KB master doc, and
names the predecessor project. Additionally, pushed docs contain local paths
(`E:\git\...`, Google Drive paths), the real Windows account identity
(`AzureAD\aarongarcia` in sitrep files), and hardcoded user paths.

**Recommendations (fix forward — history rewriting is banned by BUG-353 policy):**
1. Rewrite README.md: describe the game (deterministic city-sim, Go, tcell, persistent
   citizens), drop launcher/Prix Six/private paths.
2. Generalise `AzureAD\aarongarcia` / `C:\Users\...` occurrences in sitrep*.md forward.
3. Stamp stale artifacts with STALE banners (house pattern from checkpoint.md):
   `sprint-plan-v1.md` (zero status annotations), `build-log.md`, `team-board.md`
   (still shows Bob active). Refresh `parallel-coder-brief.md` §0a with Bro's lane.
4. No live secrets found anywhere — secret-guard allowlist approach is working.

---

## 7. Session/repo-state observations (2026-08-23)

- Main checkout is on branch `feat/167-attract-live` (not main) with **18 uncommitted changes**
  spanning guards AND engine files (`compose.go`, `synth_terrain.go`, author/destructive
  guards) and **8 commits ahead of origin**. Per GR#24(c)/GR#26 this is the most urgent
  operational item in this review: push or land the branch, and decide each dirty file
  (commit / discard-by-owner) before more work stacks on it.
- Slot table in CLAUDE.md says main checkout = Bev on main; reality differs. Either CLAUDE.md
  or the checkout should be reconciled so the startup summary stops normalising drift.

---

## Appendix A — prioritised action list

| Pri | Item | Where |
|-----|------|-------|
| P1 | Composed-engine determinism coverage in CI | detgate/compose |
| P1 | Registry-source parking/tunnels painted codes; harden scanner regex | errs, parking, tunnels |
| P1 | Clear 18-file dirty tree + 8-ahead branch on main checkout | repo ops |
| P2 | Tests for pre-push-check + bow-ref-check (then remaining four) | root tooling |
| P2 | README rewrite + identity/path scrub (fix forward) | README, docs |
| P2 | Linux GOOS CI leg | ci.yml |
| P2 | build/zone.go bare errors → registry codes | engine/build |
| P2 | Surface migration ApplyEffect failure beyond log | compose |
| P3 | npm test script; settings.json matcher dedup | package.json, .claude |
| P3 | mintMigrantID fail-loud; emigrant-range closure decision | attract, compose |
| P3 | Later-wave module test coverage wave (freight first) | engine/* |
| P3 | noopHook dead-weight cleanup; placeholder BOW refs in-code | compose, shopping/cafe/solver |
| P3 | Root debris deletion; status.md rehoming; data filename convention note | root, data |
| P3 | Weekly cron CI run; committed perf seed baseline | ci.yml |

*End of review.*
