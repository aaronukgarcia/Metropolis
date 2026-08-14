# ASM Backlog Disposition Map — FEAT-084 Phase 1

> **SNAPSHOT 2026-08-14.** Before Phase 2 execute, RE-PULL `bow_items` + `bow_destructive_verdicts` and reconcile — this session has already closed several ASMs and recorded Destructive verdicts on 6+ fixes since this map was produced; do NOT close-already-closed or re-dispatch-verdicted work.

This is a READ-ONLY disposition map. No ASM was closed, no BOW item was created, no acceptance doc or data file was edited. Phase 2 (the actual folds/closes) is gated behind a budget reset and is out of scope for this pass.

**Aaron-approved amendments (2026-08-14) applied to this map:** (1) perf-gate soundness ASMs re-bucketed to FIX; (2) the balance-number-regime placeholders re-bucketed to CONFIRM-AND-CLOSE (close citing the standing blanket ruling — placeholder + directional test + row-by-row at the M2 balance pass — do NOT re-interview); (3) ASM-150 removed (now DONE — GR#22 scrub, committed e3c2dbb, ACCEPT verdict).

## Summary

- **Total open ASM items found: 330** (task premise said ~332). Additionally 247 ASM are `DONE` and 1 is `CANCELLED` (ASM-480). Of the 330 open, **ASM-150 was closed in-session** (GR#22 scrub e3c2dbb, ACCEPT verdict) and is removed from this map — **329 remain in disposition**.
- Bucket legend: **CC** = CONFIRM-AND-CLOSE · **SF** = SPEC-FOLD · **ST** = STORY · **FIX** = FIX (perf-gate soundness, bug-plan C6 cluster) · **AD** = AARON-DECISION.

### Bucket counts

| Bucket | Count |
|---|---|
| CONFIRM-AND-CLOSE (CC) | 248 |
| SPEC-FOLD (SF) | 34 |
| STORY (ST) | 30 |
| FIX (perf-gate soundness) | 4 |
| AARON-DECISION (AD) | 13 |
| **Total in disposition** | **329** |

---

## Per-module disposition table

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

## Flat AARON-DECISION list (13 items — stays OPEN), grouped by theme

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

## FIX bucket (4 items — perf-gate soundness, aligns with bug-plan C6 cluster)

- **ASM-353** — a genuine Regressed=true run still gets AppendResult'd as the new baseline; a real regression must never become baseline.
- **ASM-355** — BUG-074 removed bufio.Scanner's per-line token cap entirely — unbounded-read risk in the perf results reader.
- **ASM-374** — ImplausibleReason (BUG-085) rejects strictly-negative only; zero-valued fields can slip and be trusted as a baseline.
- **ASM-375** — `-accept-regression` escape hatch wired only through perf-1m-probe, not perf-smoke — the gate must never carry a silent accept path.

---

## Key cross-cutting observations for Phase 2

1. **The balance-number regime now absorbs ~55 former AARON items** (re-bucketed to CC). Phase 2 closes these by writing a single line on each module's acceptance doc ("numbers are balance-regime placeholders — placeholder + directional test + Aaron's row-by-row approval at the M2 balance pass, MOD-036") and retiring the ASM row. **Do NOT re-interview Aaron for these** — the standing blanket ruling already covers them.

2. **The FIX set (4) is a distinct perf-gate soundness cluster** (ASM-353/355/374/375) that belongs with the bug-plan C6 cluster, not the confirm-and-close sweep. Phase 2 should dispatch these as fix work, not fold them.

3. **The STORY set (30) is the real net-new engineering work** — error-layer reallocation (527/534/543/547), module-key registration (444/445/451/454/472/473), master-plan call-edge amendments (220/506/514), int.protocol extension (485), and residual guard gaps (188/224/228/230/360/363/484).

4. **The SPEC-FOLD set (34)** clusters into two written standards — the **copy-guard mechanism-design standard** (009/053/066/067/069/073/074/089/093) and **"reconcile-seam-when-dependent-module-lands"** notes (530/531/535/538/544/518/556/273/428) — plus the two policy-scope folds (077→SEC-011.md, 078→BUG-017.md).

5. **ASM-150 is closed** (GR#22 scrub, e3c2dbb, ACCEPT verdict) — removed from this map entirely; do not re-dispatch or re-close it.
