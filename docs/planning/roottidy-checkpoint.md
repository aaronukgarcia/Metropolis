# FEAT-2326609811 — Repo-root tidy: CHECKPOINT & PLAN

**Goal:** declutter the repo root so the README surfaces on the GitHub landing page (like 13-FibonacciRadar). Move the 86 `claude-*` files out of root into `harness/`, keep everything working, land, and bounce the session.

**Why a checkpoint:** landing this move breaks the live session's hooks (they resolve `node claude-X.js` from the project root), so a session **bounce (`metro` relaunch) is mandatory after landing**. A fresh session must be able to resume from here.

## Where the work lives
- **Worktree:** `E:\git\Metropolis\.claude\worktrees\roottidy` — branch `roottidy`, cut from main `ad7639f` (green CI).
- The main checkout (`E:\git\Metropolis`) is UNTOUCHED until the final land, so the current session stays healthy.
- Aaron's directive 2026-10-06: do it carefully, cross-check via lower-tier agents, capture evidence, tick items off. (This lifts the 2026-09-11 single-thread order for this task.)

## Design decisions
- All 86 `claude-*.{js,json}` → `harness/`. Cross-requires `require('./claude-X')` stay valid (moved together).
- **Root shims** for `claude-bow.js` + `claude-sync.js` only (referenced 327× / 47× in docs/skills/CLAUDE.md/SessionStart — far too many to rewrite). Shims re-exec `harness/claude-<x>.js` preserving argv/stdio/exit.
- `.claude/settings.json`: 41 hook commands `node claude-X.js` → `node harness/claude-X.js` (cwd stays project root, so this resolves).
- `process.cwd()` usages are SAFE (invocation cwd = project root, independent of file location) — do NOT change.

## CHECKLIST
### A. Path fixes in harness/ (cross-boundary __dirname → `..`)  [IN PROGRESS]
- [ ] A1 `claude-sync.js` :360,:504  `join(__dirname,'.claude')` → `join(__dirname,'..','.claude')`  (permit/identity — CRITICAL)
- [ ] A2 `claude-bow.js` :570  same `.claude` fix  (NOTE: file has a NUL byte at ~190420 — preserve it)
- [ ] A3 `claude-memory-prefetch.js` :42,:47  `.claude` fix
- [ ] A4 `claude-ping-check.js` :101  `.claude` fix
- [ ] A5 `claude-committhook-install.js` :52,:56  `join(__dirname,'githooks')` → `..`
- [ ] A6 `claude-version-checker.js` :78  `const ROOT = __dirname` → `path.join(__dirname,'..')` (ROOT only ever = project root)
### B. Root shims
- [ ] B1 root `claude-bow.js` shim → re-exec `harness/claude-bow.js`
- [ ] B2 root `claude-sync.js` shim → re-exec `harness/claude-sync.js`
### C. settings.json
- [ ] C1 41 refs `node claude-X.js` → `node harness/claude-X.js` (both Bash + PowerShell variants); keep bow/sync resolving via shim OR harness path
### D. External (outside the repo)
- [ ] D1 forge `E:\GoogleDrive\Tools\new\harvest.json`: `"hooks": "claude-*.js"` → `"harness/claude-*.js"`; then `node harness/claude-committhook... ` n/a — run `forge.js harvest --only metropolis` AFTER land
- [ ] D2 `metro.bat` launcher (WindowsApps): check for any `claude-*.js` refs (likely none — it only launches claude)
- [ ] D3 CLAUDE.md: update the one `node claude-committhook-install.js install` reference → `node harness/claude-committhook-install.js install`
### E. VERIFICATION (delegated to agents, evidence captured to temp/roottidy-evidence/)
- [ ] E1 CLIs work via shim: `node claude-bow.js list` + `node claude-sync.js` read op succeed, identity resolves (not null)
- [ ] E2 every hook script loads from harness/ with NO MODULE_NOT_FOUND (run each with benign input)
- [ ] E3 settings.json is valid JSON AND every `node harness/claude-X.js` points to an existing file
- [ ] E4 the moved `claude-*.test.js` pass (requires resolve) — scoped runner
- [ ] E5 no stray root `claude-*` except the 2 shims; no `require('./claude-` left that should be `../`; NUL in claude-bow.js preserved
- [ ] E6 spot-check QA (independent agent): re-verify a sample of the above from scratch, check edit quality
### F. Land + bounce (LAST)
- [ ] F1 commit in worktree (GR#23 verdict handled — guards moved; round or proportionality note)
- [ ] F2 merge roottidy → main (local merge, no gh pr), push, verify %ae/%ce noreply
- [ ] F3 run `forge.js harvest --only metropolis` + backup (reflects new paths)
- [ ] F4 **BOUNCE: Aaron relaunches via `metro`** — new hook paths load
- [ ] F5 post-bounce: confirm SessionStart hook + checkin work (claude-startup→claude-sync via shim), BOW query works, a dummy commit fires the guards from harness/

## RECOVERY (if session dies mid-op)
- The move + any edits are in the worktree on disk (branch `roottidy`), uncommitted until F1. `git -C <worktree> status` shows progress.
- Re-read this file + FEAT-2326609811 BOW item. Resume at the first unchecked box.
- If the session died AFTER land (F2) but before bounce: just relaunch `metro`.
</content>

---
## PROGRESS LOG (2026-10-06)
- **A (path fixes): DONE + verified.** The careful cross-check caught WAY more than the initial 6: 8× `const ROOT = __dirname`, `projectRoot = __dirname` (startup), `repoRoot || __dirname` (committhook), `|| __dirname` (statusline), `[__dirname, os.tmpdir()]` (bow security allowlist), + the original `.claude`/`githooks` lines, + secret-checker's allowlist → sibling `__dirname`. ALL fixed to `..` (project root) except genuine siblings. `node --check` passes on all 16 edited files. NUL in claude-bow.js preserved (2→2). Evidence: temp/roottidy-evidence/A*.txt
- **B (shims): DONE.** Root claude-bow.js + claude-sync.js are CLI re-execs → harness/. Confirmed nothing external requires them as modules (only intra-harness requires, which resolve to the real files). Evidence: B-shim-design.txt
- **C (settings.json): DONE + verified.** 38 hook commands → `node harness/claude-X.js`; valid JSON; all referenced files exist. The 3 non-command `claude-sync.js` refs (a permission allow-rule + a PreCompact echo) correctly target the root shim — left as-is. Evidence: C-*.txt
- **D (external): D2 launcher = no change (metro.bat invokes no claude-*.js). D3 committhook printed-instruction strings → harness/ (3 lines). ../githooks fix proven to resolve to real files. D1 forge harvest.json = POST-LAND (F3): update the glob `claude-*.js`→`harness/claude-*.js` AND the explicit `claude-sync.js`/`claude-bow.js` paths, then `forge.js harvest --only metropolis`.** Evidence: D*.txt
- **E (verification, delegated): V2 hooks = PASS (all load, no path errors). V1 tests+CLI, V3 mechanical, QA spot-check = RUNNING.** Evidence: temp/roottidy-evidence/V*.txt, QA-spotcheck.txt
- **F (land + bounce): PENDING** on E all-green.

---
## QA-FIX ROUND (2026-10-06) — the adversarial spot-check EARNED ITS KEEP
The NUL byte in claude-bow.js made grep skip lines, so the first scan missed several refs; and I only searched harness/tools/scripts for module consumers (missed githooks/). The QA agent + an exhaustive binary-safe re-sweep found and FIXED:
1. claude-bow.js :2051-2053 (docs/acceptance, code.json, sprint-plan), :2210 & :3000 (rootDir||__dirname), :1004 (git cwd) → all `..`. (binary-safe verified)
2. tools/debugsink/server.js:109 + tools/vestige/backfill-rulings.js:226 `require('../../claude-db.js')` → `../../harness/claude-db.js`.
3. githooks/verdict-guard.js resolveSiblingModulePath → harness/ candidates FIRST (root claude-bow.js is now the shim with no exports).
4. githooks/commit-msg resolveIdentityModulePath + resolveCodenameScanModulePath → harness/ candidates FIRST.
5. **SECURITY (GR#23): claude-destructive-guard.js ENFORCED_DIR_RE → added `harness` — otherwise a commit editing the moved guards would skip the destructive-verdict requirement.** Verified: isEnforcedDirPath('harness/claude-destructive-guard.js')===true.
6. .github/workflows/ci.yml:282 `node claude-committhook-install.js install` → `harness/` (committhook is not shimmed). committhook printed instructions also → harness/.
7. githooks/verdict-guard.test.js:26 `require('../claude-bow.js')` → `../harness/claude-bow.js`.
Direct re-verification PASSED: harness modules export the needed fns; verdict-guard + commit-msg resolve to harness (not the shim); harness/ is code-bearing. Re-sweep: zero non-harness root-claude requires remain; node --check green on all edits.
NEXT: full test-run + a fresh adversarial re-hunt (agents R1/R2), then land+bounce.

---
## TEST-SUITE FIX ROUND (2026-10-06) — R2 caught the whole 43-file test suite was broken
The 43 harness/*.test.js were moved uncorrected. ROOT was doing double duty (project-root AND the claude-* script location, same place pre-move). Fixes applied:
- `const ROOT = __dirname` -> `path.join(__dirname,'..')` (project root); `__dirname/githooks|docs|code.json|package.json` -> `..`; `cwd: __dirname` -> `..` (uniform sed).
- SIBLING-SCRIPT refs `path.join(ROOT,'claude-*.js')` and `path.join(ROOT,'ddl-spy*')` -> `__dirname` (scripts moved WITH the tests into harness/).
- DDL-spy spawn: bare 'claude-bow.js' -> path.join(__dirname,'claude-bow.js') so the --require preload reaches the REAL process (the shim would swallow it).
- Moved ddl-spy.fixture.js + quote-mask-drift.test.js into harness/ (root now 20 entries).
PROVEN: the 3 suites R2 showed broken (committhook/codenamehook/version-checker) now PASS; all test files node --check clean.
NEXT: full-suite run (all harness + githooks + tool tests incl DB-backed) as ground truth, then land+bounce.

---
## REGRESSIONS FIXED + READY TO LAND (2026-10-06)
Full-suite run (independent agent, vs baseline ad7639f) found 5 REAL regressions — all test-file "assumes-root-layout" bugs — now FIXED + re-run PASS:
1. author-identity.test.js:23 require('./githooks/...') -> '../githooks/...'
2. pre-push-check.test.js fixture: copy the tool into workDir/harness/ (ROOT=.. now)
3. dispatch-guard.test.js: basename(__dirname) -> basename(join(__dirname,'..'))
4. quote-mask-drift.test.js KNOWN_COPIES -> 'harness/claude-quote-mask.js'
5. bow-recordersession.test.js expected -> normalizeRecorderCwd(join(__dirname,'..'))
Remaining full-suite failures are PRE-EXISTING (fail at baseline too; local-only because TEMP is inside the repo — they pass on CI) + one flaky plan-checker concurrency test. Live .git/hooks reinstalled from main (healthy) and 33 .bak litter files removed.

## EXACT LAND SEQUENCE (bounce required)
1. GR#23: record an ACCEPT verdict for FEAT-2326609811 from an INDEPENDENT cwd (a throwaway worktree / the review agent) — the commit touches the guards (ENFORCED_DIR_RE etc.) so it is code-bearing; the adversarial QA/R2/full-suite rounds are the evidence.
2. In the worktree: `git add -A` then `git commit -m "refactor: move claude-* harness scripts into harness/ ... [FEAT-2326609811]"`. (Guard runs from the still-intact main checkout → passes with the verdict.)
3. LAND as the LAST foreground action, because it breaks this session's hooks the instant main's files move:
   `git -C E:/git/Metropolis merge --no-ff roottidy && git -C E:/git/Metropolis push origin main`  (ONE command — PreToolUse fires before, files still at root; the git-native .git/hooks/pre-push is self-contained so it survives the move). Verify %ae/%ce noreply.
4. IMMEDIATELY BOUNCE: Aaron relaunches `metro`. The new session loads settings.json (now harness/ paths) + the moved scripts cleanly.
5. POST-BOUNCE (new session): (a) confirm SessionStart checkin + BOW + guards work; (b) a dummy commit to prove the guards fire from harness/; (c) forge: update harvest.json `claude-*.js`->`harness/claude-*.js` + explicit paths, run `forge.js harvest --only metropolis` + backup; (d) run /ci-green + confirm CI green; (e) `git worktree remove` the roottidy worktree; (f) confirm the README now surfaces on the GitHub landing page.
