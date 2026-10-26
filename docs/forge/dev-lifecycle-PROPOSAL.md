# Dev lifecycle with separated agent roles — proposal v0.4 (Aaron, 2026-09-24; v0.3 2026-10-18; v0.4 2026-10-22: default for all non-lash-up projects, lead never codes, branch trees, section 12)

v0.3 (SAFE-117..122, Aaron's go 2026-10-18): tiers decide the model before dispatch (6a), one brief shape with a token cap and a 10-12 line reply (6b), and three mechanical gates in `/lifecycle verify`: revision-bound verdict, owned-paths scope, and the trivial class (7). Token use is measured in `dispatch_usage` (8). Ideas from thebpandey/lanes (MIT), re-authored; no code copied.

Sources: Aaron's brief of 2026-09-24 (spec, AC, assumption review, build, test, security test, done; testers never change code, devs never change AC), and `E:\projects\Photos\PLAN.md` v0.2.0, the reference instance. Packaged as the forge skill `/lifecycle`, the plan template `lifecycle-PLAN-TEMPLATE.md`, a commit-msg role guard, and golden rule GGR65, so every project gets it through `/new` and `/new --upgrade`.

## 1. Gate zero: is this a lash-up?

The first question on any build, asked by `/new` and by `/lifecycle plan` through AskUserQuestion, before anything else. Only Aaron's answer counts; an agent never decides it. The answer is stored as intake answer I0.

- **Lash-up (yes):** prototype, throw-away, working as fast as possible. No plan template, no AC, no assumption review, no testers. The always-on rules still hold: GGR16 cost gate, GGR38 no secrets in git, GGR64 no AI attribution, GGR12 push every session. The repo and code.json carry `"mode": "lashup"` and the README says PROTOTYPE, NOT FOR REUSE. Promoting a lash-up means a fresh `/lifecycle plan` that uses the prototype as reference material, never a relabel.
- **Not a lash-up (the default):** everything below is mandatory. Skipping any part needs Aaron's explicit answer, recorded as a `lifecycle_event` row.

## 2. Intake interview

Straight after gate zero, in AskUserQuestion batches of four or fewer. Answers go to `harvest/answers/<slug>.json` and the project store. The generic set, extended per project:

| # | Question | Decides |
|---|---|---|
| I1 | Refactor or green field? For a refactor, where is the existing code and is it backed up offsite? | entry path; SAFE exposure |
| I2 | Product name, module-key prefix, any external ids | every key, repo name, BOW code |
| I3 | Repository location and private GitHub remote | git discipline, CI, forge registration |
| I4 | Disk, build time and toolchain budget | feasibility of M0 |
| I5 | Least-privilege service identity for each store (DB user, API key scope) | security baseline |
| I6 | Test data: a sampled, sanitised copy so no gate ever touches the live store | tester targets (GGR41) |
| I7 | External budgets and rate limits (tokens, requests per second, monthly caps) | limiter modules and T4 numbers |
| I8 | UI or behaviour parity with what exists: exact, or may it simplify? | scope |
| I9 | Which existing scripts or features are carried forward, which are retired | scope |
| I10 | Definition of done for the whole product, and its exit test | final milestone |
| I11 | Who maintains it: skill level, framework familiarity, comment language | comment contract bar for T1 |

## 3. The plan document (required sections)

Every non-lash-up project has a `PLAN.md` in its root built from `lifecycle-PLAN-TEMPLATE.md`. Required sections, as in the Photos plan:

1. **Objective** and end state in one paragraph.
2. **Honest planning response**: what is expensive, what is blocked today, what transfers and what does not, effort stated plainly.
3. **Decisions taken** (D1..Dn) with the consequence each bakes in.
4. **Error-handling standard**: the four pillars (log, registered code, correlation id, copyable display) in the project's language, codes generated from one registry file (GGR1, GGR2, GGR5).
5. **Checkpoints and intake answers**.
6. **Architecture**, contract-first (GGR48): module keys `<layer>.<name>` that are also the BOW key and the code.json key; universal edges written once.
7. **Type, memory and security baseline** for the language: compiler flags, static analysis, sanitizers, banned APIs, trust-boundary validation, no elevation, network surface, least privilege, data integrity. This list is T1's mechanical gate list and T4's attack catalogue seed.
8. **Milestones** with exit tests. M0 is always the riskiest dependency, time-boxed, with a written fallback. A blocked external stops only its own milestone unless code.json says another depends on it.
9. **code.json and BOW** conventions.
10. **Roles** table (section 6 below) with models and budgets.
11. **Risks**.
12. **Next actions**.

Entry paths share everything from `ac_review` onwards:

- **Refactor** (baseline: Photos, Python to C++/Qt digiKam plugin): modules are derived from the existing code; every AC states behaviour parity with the old module and names the parity test (e.g. "reproduces run 209's plan exactly"). The old code must have an offsite copy before M0.
- **Green field:** code.json is generated from PLAN.md first (the plan is its source of truth, GGR53), then BOW items are decomposed from that graph.

## 4. Acceptance criteria format

Every BOW item's AC has: title; module key; Given/When/Then lines numbered AC-1..n, each with a measurable outcome; the error codes it may raise; the security baseline items it must satisfy, by number; any cross-module call as a registered code.json edge (GGR51); for a refactor, the parity test; and as its last line **Assumptions: none**. A requirement also carries a mandatory `Paths:` line (comma-separated files, or `dir/**` for a directory, relative to the git toplevel) naming what the developer owns, and a `Spec: <file>#<requirement id>` line when it lives in a shared spec file: the AC block is hashed from the first `- AC-1` line under the heading containing that id to the next heading, read from the default branch tip (SAFE-324).

## 5. The lifecycle (state machine and checkpoints)

```
draft --CP-spec--> ac_review
ac_review --CP-review: assumption found--> rejected(assumptions) --> draft   (bounce 1, 2; bounce 3 = BLUE, Aaron decides)
ac_review --CP-review: none--> accepted                                       (AC hash frozen)
accepted --CP-dev--> in_dev
in_dev --developer: "reject, assumes X"--> rejected(assumptions)             (AC untouched)
in_dev --CP-commit--> test_quality       (T1)
test_quality    --> test_registry        (T2)
test_registry   --> test_functional      (T3)
test_functional --> test_security        (T4)
test_security   --> verdict              (GGR15, independent non-author attacker)
verdict --/lifecycle verify: every gate has a PASS row, AC hash unchanged--> done
any defect --> in_dev; the failed gate and every later gate re-run
```

| Checkpoint | Evidence required |
|---|---|
| CP-0 | M0 exit test passed, or the fallback taken and recorded as a decision |
| CP-spec | every cross-module call is a registered edge; inputs, outputs, errors, security items and numbers written; "Assumptions: none" present |
| CP-review | the reviewer's answer to "what must be true for this AC to be satisfiable that it does not state?" Any answer rejects with the list |
| CP-dev | item is `accepted`; per function, the four questions answered: what can fail, which code, how the user copies it, is it logged |
| CP-T1 quality | comment contract met (module header block, doc comment on every public function, density check); names match code.json; compiler, static analysis and sanitizer runs clean; GGR54 code facets scored with rationale |
| CP-T2 registry and logic | code.json matches the code (GUIDs, edges, error profile, no drift); each AC-n walked against the code on paper |
| CP-T3 functional | one executed test with output per AC-n, against the test data (I6), plus the parity test for a refactor |
| CP-T4 security | the attack catalogue run and each attack recorded as run or not run: rate limits, malformed and oversized input, path traversal, injection, privilege escalation, secrets in logs, concurrency, resource exhaustion |
| CP-verdict | GGR15 destructive verdict by an identity that is neither the author nor T4 |
| CP-commit | registry audit (GUIDs, refs, version drift) and the five security questions (GGR17); a YES without a mitigation stops the commit |
| CP-milestone | demo to Aaron, go or no-go recorded, GGR54 score for the milestone |
| CP-session | resume from the store at start; `/update` sweep at end |

Invariants in every state: the AC hash at `done` equals the hash at `accepted`; only `/lifecycle verify` moves an item to `done`, and only on evidence rows; no role ever declares done.

## 6. Roles

| Role | Writes | Never writes | Model and budget (GGR58) |
|---|---|---|---|
| Architect (lead, or Aaron) | PLAN.md, BOW items, AC, code.json edges, assumption answers | source, tests | lead's model |
| AC Reviewer | assumption comments, accept or reject | AC prose, source | Opus |
| Developer | source, the unit tests it needs to build | AC, acceptance tests, defect comments | Sonnet; Opus from the second bounce |
| T1 Quality | defect comments, T1 evidence | source, AC | Sonnet |
| T2 Registry and logic | defect comments, T2 evidence | source, AC | Sonnet |
| T3 Functional | acceptance tests, defect comments, T3 evidence | source, AC | Sonnet |
| T4 Security | attack log, defect comments | source, AC | Opus |
| Verdict | the verdict row | anything else | Opus; never the author, never T4 |

Every dispatch carries a BUDGET line (minutes, tokens, half-budget checkpoint, minimum deliverable) and the role's model. Developers answer AC only with "reject, assumes X". Testers report defects only.

### 6a. Tiers: difficulty picks the model before dispatch (SAFE-120)

Every BOW item gets a tier at decomposition, recorded in its detail and on its dispatch rows (`usage-ledger.js tag --tier`). The tier refines the role table above; where they disagree, the dearer model wins.

| Tier | Signals (any one is enough) | Model | Typical roles |
|---|---|---|---|
| T0 structural | counts, hashes, formats, lint, unchanged-file checks | no agent: a script | gates in section 7 |
| T1 routine | one file or one unit, clear checks, clerical, cold reads, QA passes | Haiku (Sonnet if it fails once) | T1 quality, QA, clerical |
| T2 moderate | multi-file judgement, cross-module change, unfamiliar code | Sonnet | developer, T2, T3, integration |
| T3 complex | security or trust boundary (hooks, guards, secrets scanners, auth), legal meaning, a hard defect, round 2+, final verdict | Opus | AC reviewer, T4, verdict, developer from bounce 2 |

- In doubt between two tiers, take the higher. Re-tier upward after two FIX verdicts on one item; never downgrade mid-flight.
- Claude models only. External model relays are out: they are paid APIs and the code leaves the machine (cost gate).
- **Batch small units.** Every dispatch pays a fixed start-up cost before it does any work. Baseline 2026-10-18 (951 background agents since September): the cheapest run reported 36k tokens, runs under 10 tool calls averaged 64k, and tokens then grow by about 1.4k per tool call. Work expected to take under about 15 tool calls (one chapter's cold read, one file's lint review) goes several units to a dispatch, not one.
- The GGR58 gate (`claude-agent-budget.js` + `claude-dispatch-guard.js`, reference implementation SitRep, forge `hook.sitrep.claude-agent-budget`) enforces the role/model table and ceilings mechanically where a project has it installed.

### 6b. The dispatch brief (SAFE-121)

One shape for every lifecycle dispatch, template `lifecycle-BRIEF-TEMPLATE.md`: role, round and tier, each on its own line, with the lifecycle role mapped to a GGR58 `ROLE:` value (the template's table; Verdict and AC Reviewer go as `hostile` with a MODEL-REASON for Opus); the GGR58 BUDGET line; the task in one paragraph; inputs (base SHA, the files to read, prior evidence); owned paths; checks; a stop condition; and the report contract. Workers write full evidence to a report file in the project's temp folder and return **10-12 lines** (outcome, checks with PASS/FAIL, files changed, tokens used, anything deferred). The lead reads the full report only when a decision needs it. Workers read the owned files, their callers and their tests, and do not survey the repo.

## 7. Enforcement (gates, not memory, GGR47)

- **Branch guard** in the global commit-msg hook chain (`~/.git-hooks/lifecycle-role-guard.js`, beside the GGR64 trailer guard; canonical source `githooks/lifecycle-role-guard.js`, same code as `lifecycle-check.js guard`): the role is the BRANCH SUFFIX (`lc/<CODE>/<role>`), never `LIFECYCLE_ROLE` or the identity, because agents inherit the lead's identity. In a lifecycle project at or after `guard_from` a commit that touches a guarded path (classes in `lifecycle.json`) is allowed only on the role branch whose role may write that class, as a `lifecycle-merge: <CODE> <role>` merge on `lc/<CODE>/main`, or on the default branch as a `--no-ff` merge `lifecycle-verify: <CODE>` whose second parent is the tip of `lc/<CODE>/main` and whose verdict file says CLEAN for THE REVISION. Refusal is exit 3 (`LC_E_DIRECT_SOURCE`, `LC_E_ROLE`, `LC_E_BRANCH`, `LC_E_DETACHED`, `LC_E_NOT_VERIFY`, `LC_E_NO_VERDICT`, `LC_E_EVIL_MERGE`); any crash or unreadable `lifecycle.json` fails open (`LC_W_GUARD_CRASH`, `LC_W_MODE_FILE`, I8). Deliberate bypass (`--no-verify`, push of `lc/X:main`) is detected, not prevented, by `lifecycle-check.js audit`.
- **AC freeze check**: `/lifecycle verify` compares the AC hash with the one stored at `accepted`.
- **Dispatch guard** (exists for GGR58): refuses a brief with no BUDGET line or a model outside the role table.
- **Registry audit** (GGR53): refuses a commit with undocumented GUIDs, broken refs or version drift.
- **Language gates** from the plan's baseline section, run in CI (GGR14).
- **Revision-bound verdict** (SAFE-117): every tester and verdict output ends with the block below. `node E:\projects\63-SAFE\tools\lifecycle-check.js verdict <review-file> <head>` fails unless REVISION is the full SHA of <head> and VERDICT is CLEAN. THE REVISION of a requirement is the tip of its developer branch `lc/<CODE>/developer`; testers and the verdict role work at that commit and a gate PASS counts only at it. A review of an older revision does not count; the head moved, so it is re-reviewed.
  ```
  TASK: <BOW code>
  REVISION: <full 40-character SHA reviewed>
  REVIEWER: <identity, model>
  SCOPE: <the SCOPE: line from the scope gate>
  CHECKS: <command: PASS|FAIL|not run>, one per check
  FINDINGS: none | numbered: P1|P2|P3 | file:line | failure scenario | fix direction
  VERDICT: CLEAN | FIX        (any P1/P2, scope FAIL or failed check = FIX)
  ```
  Only the reviewer writes that file. Known limit: a lead could forge one; the cross-check is the state store: `lifecycle-check.js audit` accepts a verify merge only when `lifecycle_event` holds a `done` row by role `verify` and PASS rows (CP-T1..T4, CP-verdict) at that revision, and `lifecycle-state.js dispatch` records a `CP-dispatch` row per agent launch.
- **Owned-paths scope** (SAFE-118): dispatch writes the paths the role may change to `<project temp>/lifecycle/<BOW>/owned.txt` (one per line, trailing `/` owns a directory). `lifecycle-check.js scope <base> <head> <owned.txt>` fails on any changed path outside it, or on uncommitted work when head is the worktree. Complements the role guard, which checks the role, not the task.
- **Trivial class** (SAFE-119): `lifecycle-check.js classify <base> <head>` prints TRIVIAL only for added or modified plain prose files (`.md`, `.rst`, `.adoc`, mode 100644; never `.txt` or `.mdx`, which can be build inputs), 40 changed lines or fewer, and none of CLAUDE*.md, SKILL.md, AGENTS.md, GGR.md, LICENSE, SECURITY or anything under `.claude/`, `.github/`, `githooks/`, `schema/`, `harness/`, `skills/`, `commands/`, `agents/`, `docs/forge/`. The verdict gate needs exactly one REVISION and one VERDICT line. A TRIVIAL item skips the four testers and the verdict dispatch; verify still runs the scope gate and records the classify output as evidence (checkpoint `CP-trivial`). Everything else is REVIEW.

## 8. Data

Reuse the Metropolis BOW tables through forge: `bow_items`, `bow_dependencies`, `bow_comments` (defects and assumptions, tagged with role and checkpoint), `bow_git_refs`. Add two:

- `ac_freeze(bow_code, sha256, frozen_at, frozen_by)`
- `lifecycle_event(id, bow_code, from_state, to_state, checkpoint, by_identity, role, evidence_ref, at, result, revision, legacy, project, session_id, identity, epic)` (written only by `tools/lifecycle-state.js`; a project without the table uses the append-only `docs/lifecycle/state.jsonl` of the MAIN worktree)

Generated views: queue by state, items over budget, assumption bounces at 2, AC hash mismatches.

A third table measures cost (SAFE-122): `dispatch_usage` in `schema/lifecycle.sql`, one row per dispatched agent with model, tier, role, tokens, tool calls, duration and acceptance. `node E:\projects\63-SAFE\tools\usage-ledger.js harvest` fills it from Claude Code transcripts (background agents); `add` records a foreground dispatch from the numbers its result reported; `report --by model|project|tier|month` gives count, total, median and p90. Tokens are as reported, never estimated.

## 9. The skill `/lifecycle`

- `plan`: gate zero, intake, PLAN.md from the template, code.json, BOW decomposition, draft AC, AC Reviewer dispatch.
- `status`: queue by state, over-budget items, bounce-2 assumptions waiting for Aaron, hash mismatches.
- `dispatch <BOW>`: reads `lifecycle-state.js state`, picks the next role, opens the worktree (`lifecycle-wt.js open`), records the `CP-dispatch` row and ALWAYS ends in an Agent launch (ROLE, worktree path, BUDGET line, model by role, owned paths, the frozen brief of 6b). The caller never edits files in a worktree.
- `verify <BOW>`: `lifecycle-check.js verify <CODE>` prints the evidence table (classify, scope, gates at THE REVISION, verdict, AC hash); on PASS it merges the role branches into `lc/<CODE>/main` (`lifecycle-merge: <CODE> <role>`), then `lc/<CODE>/main` into the default branch with `--no-ff` and `lifecycle-verify: <CODE>` (no AI trailer), and moves to `done` through `lifecycle-state.js` as role `verify`; the only path to `done`.

## 10. GGR65

"Separation of duties in the dev lifecycle, unless Aaron calls it a lash-up": every build starts by asking Aaron whether it is a lash-up. If not, architecture, AC review, development, the four testers and the destructive verdict are separate identities; none edits another's artefacts; AC is frozen at acceptance; done is a verify transition backed by evidence rows, never a declaration. Applies to every code project.

## 11. Baseline gaps to close in the Photos plan before M0

The Photos plan is the reference instance and already has sections 2 to 6 in project form. To match this lifecycle it still needs: gate zero recorded as intake answer I0; the AC freeze; the bounce cap; the role guard; a verdict state separate from T4; the roles table with models and budgets; `/lifecycle verify` as the only path to done; the unit-test and acceptance-test ownership split; and an offsite copy of `D:\photo-manager`, which is a git repo with no remote and outside the SAFE register.

## 12. v0.4 additions (Aaron, 2026-10-22): default everywhere, lead never codes, branch trees

**12.1 Default.** The lifecycle is the DEFAULT for every project that Aaron has not marked a lash-up (gate zero, section 1). There is no opt-in: forge stamps `lifecycle.json` mode `lifecycle` on assemble and on `upgrade`, `/new` asks only the lash-up question, and GGR65 v3 / GGR83 v2 carry the rule into every project's GGR.md. A project already running is brought in by `forge upgrade <slug> --apply --only ggr` (rule text) plus its next `/lifecycle plan`.

**12.2 Vocabulary.** Epic = a BOW parent item (milestone). Requirement = a BOW item under an epic (one module or one capability). Acceptance criterion = AC-n inside the requirement, Given/When/Then, measurable, ending "Assumptions: none" only after review. The state machine in section 5 is unchanged; "rejected(assumptions)" is the loop Aaron describes: the reviewer (or a developer, "reject, assumes X") rejects with the list of assumptions, the ARCHITECT closes each by editing the requirement, editing an AC, or adding a new requirement, and re-offers it; bounce 3 goes to Aaron.

**12.3 The lead never codes** (SitRep ruling 2026-09-23, now general). The lead writes requirements, ACs and answers assumption questions, directs and decides; it does not write or patch source, tests, config or docs-as-code. A defect found by the lead is bounced to a developer role as a defect comment, not fixed. A one-line change is still a developer dispatch (batch small units, 6a). The branch guard (section 7) refuses any commit of source, tests, AC or `lifecycle.json` mode/classes on the default branch of a lifecycle project once `guard_from` is reached, whoever the identity is.

**12.4 Branch trees, concurrent roles.**
```
main
 └─ lc/<CODE>/main              integration branch of one requirement (created at CP-dev from the default-branch tip)
     ├─ lc/<CODE>/developer     developer worktree: source + unit tests only (alias dev)
     ├─ lc/<CODE>/t1 .. t4      tester worktrees: write ONLY review files (docs/reviews/<CODE>/tN.md); t3 also tests/acceptance/<CODE>/
     └─ lc/<CODE>/verdict       verdict worktree: writes only the verdict file
```
`<CODE>` is upper case. (git cannot hold a branch `lc/<CODE>` beside `lc/<CODE>/<role>`, hence `/main`.)
- One git worktree per role per requirement (`node tools/lifecycle-wt.js open <CODE> <role>`; path `<project>/temp/wt/<CODE>-<role>`, `$TEMP/wt` for a project in a cloud-synced folder), so roles on DIFFERENT requirements, and the testers of the same requirement once dev has committed, run in parallel without touching one another's files.
- Testers and the verdict role get a branch created AT the developer's tip (never detached); their output records that REVISION (section 7). A new developer commit invalidates every later gate (existing rule).
- Ownership files (`lifecycle-check.js scope <base> <head> <owned-file>`) are generated per role from the requirement: dev owns its `Paths:`, a tester owns only its review path; a role that touches anything else fails the scope gate.
- Only `/lifecycle verify` merges: the role branches into `lc/<CODE>/main` (subject `lifecycle-merge: <CODE> <role>`), then `lc/<CODE>/main` into the default branch with `--no-ff` (subject `lifecycle-verify: <CODE>`), after every gate has a PASS row and the AC hash equals the frozen hash. The guard refuses a merge result that differs from the merged branch in a guarded path (`LC_E_EVIL_MERGE`); another requirement's verify merge in between is fine. Worktrees are removed after merge with `lifecycle-wt.js close` (clean ones only); branch deletion is Aaron's.
- Independent requirements are developed concurrently up to the dispatch ceiling in 6a; a requirement that depends on another waits for that one to reach `done`.

**12.5 Model tiers unchanged** (6a): cheap models for clerical and checks, Sonnet to develop and test, Opus for AC review, security test and verdict.
