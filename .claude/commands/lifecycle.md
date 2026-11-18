---
name: lifecycle
description: Plan and drive a code project through the separated-role dev lifecycle (spec v0.4). Always asks first whether it is a lash-up. Usage: /lifecycle plan [scope] | status | dispatch <BOW> | verify <BOW>. Spec: docs/forge/dev-lifecycle-PROPOSAL.md; plan template: docs/forge/lifecycle-PLAN-TEMPLATE.md.
---

# /lifecycle

Read `docs/forge/dev-lifecycle-PROPOSAL.md` (v0.4) first (installed by forge; canonical copies of the spec, `lifecycle-PLAN-TEMPLATE.md` and `schema/lifecycle.sql` are in `E:\projects\63-SAFE`). Tools, all run from the project's checkout with `T=E:/projects/63-SAFE/tools`: `node $T/lifecycle-state.js` (the ONE writer of the state machine), `node $T/lifecycle-wt.js` (branch tree and worktrees), `node $T/lifecycle-check.js` (classify, scope, verdict, guard, audit, verify). Guard: the branch-based commit-msg guard (`~/.git-hooks/lifecycle-role-guard.js`) reads `lifecycle.json` in the git toplevel; the role of a commit is its BRANCH SUFFIX (`lc/<CODE>/<role>`), nothing in the environment or identity counts. Rules in force: GGR7 identity, GGR15 destructive verdict, GGR41 test isolation, GGR47 gates not memory, GGR48 contract-first, GGR51 graph-driven AC, GGR53 code.json, GGR54 quality bar, GGR58 dispatch budget, GGR64 no AI attribution, GGR65 separation of duties.

The lead (this session) writes requirements, ACs and answers; it NEVER edits source, tests, config or files inside a role worktree. Everything that changes code is a dispatch.

## Requirement format
Each BOW requirement has: title, module key, `Spec: <file>#<requirement id>` (when it sits in a shared spec file), AC-1..n in Given/When/Then form ending "Assumptions: none", and a mandatory **`Paths:`** line (files, or `dir/**`, relative to the git toplevel) naming what the developer owns. No `Paths:` line, no dispatch (`LC_E_NO_PATHS`).

## plan [scope]
0. **Gate zero.** AskUserQuestion: "Is this a lash-up (prototype, throw-away, just get it working)?" This is the FIRST question and the only mode question; only Aaron's answer counts. Store it as I0.
   - Yes: write `lifecycle.json` in the git toplevel with `"mode": "lashup"`, `"mode_source": "aaron"`, set the same mode in code.json, add PROTOTYPE, NOT FOR REUSE to the README, stop. Only the always-on rules apply.
   - No (or no answer): write `lifecycle.json` in the git toplevel with `"mode": "lifecycle"`, `"mode_source": "aaron"`, `default_branch` and the path classes (source, unit_tests, acceptance_tests, ac, exclude) and keep code.json `mode` equal to it (the guard does not read code.json). `guard_from` is set only by the owning project's session after the file is committed. Nothing below may be skipped without Aaron's explicit answer, logged in `lifecycle_event`.
1. **Intake** I1 to I11 (spec section 2) in batches of four or fewer; store in `harvest/answers/<slug>.json`. If I1 says refactor and the existing code has no offsite copy, raise it to SAFE before anything else.
2. **PLAN.md** from `lifecycle-PLAN-TEMPLATE.md`, every required section filled. If a PLAN.md exists, diff it against the template and list the missing sections instead of overwriting.
3. **code.json**: refactor, derive modules from the existing code; green field, generate from PLAN.md. Refuse to continue without it (GGR53).
4. **BOW decomposition** (spec section 12.2): one item per module key, plus a parent epic per milestone. Insert as `draft`.
5. **AC** per item in the spec section 4 format with the `Paths:` line, ending "Assumptions: none". Cross-module calls must be registered edges.
6. **AC Reviewer**: offer EACH requirement to the AC Reviewer (an Agent launch as in dispatch, Opus, BUDGET line). It may only write assumption comments and the accept or reject transition, through `lifecycle-state.js move`. Bounce 3 goes to Aaron.
7. Report the queue.

## status
`node $T/lifecycle-state.js list` for items by state with bounce counts; for each code `state <CODE>` for AC-hash status and stale gates. Report: items by state, bounce counts, items `blocked(aaron)` (waiting for Aaron), stale gates, AC-hash mismatches, over-budget items, and the open worktrees (`git worktree list` entries under `temp/wt/`).

**Repos with their own branch names (branch_roles, SAFE-450).** A project whose agents commit on `worktree-agent-*` or lanes on `lane/<name>` can add `"branch_roles": {"worktree-agent-*": "developer"}` to `lifecycle.json`: a matching branch gets that role's write rights instead of needing `lc/<CODE>/<role>`. It never applies to the default branch, so reaching main still needs the `lifecycle-verify` merge and a CLEAN verdict; map agent and named lane branches only, not the lead's. Changing the key is a guarded commit, so set it (and the corrected `classes`) before `guard_from`.

## dispatch <BOW>
1. Read the state: `node $T/lifecycle-state.js state <CODE>`. Pick the next role from the item's state (spec section 5/6) and its tier from 6a (in doubt, the higher tier; two FIX verdicts move it up a tier). Refuse a `blocked(aaron)` or `done` item.
2. Open the role's worktree: `node $T/lifecycle-wt.js open <CODE> <role> [--spec <file>#<req>]`. It creates `lc/<CODE>/main` and `lc/<CODE>/<role>` and writes the ownership file `temp/lifecycle/<CODE>/<role>.owned.txt`; a developer open without a `Paths:` line is refused. Batch units expected to take under about 15 tool calls into one dispatch.
3. Record the dispatch: `node $T/lifecycle-state.js dispatch <CODE> --role <role> --agent <id or description> --by <identity> [--revision <sha>]` (a `CP-dispatch` row; not a move). Moves are made by the dispatched role itself with `lifecycle-state.js move`.
4. ALWAYS end in an Agent launch, and never return without one: the brief from `docs/forge/lifecycle-BRIEF-TEMPLATE.md` with every line filled: `ROLE:`, ROUND/TIER, the worktree path, the GGR58 BUDGET line, the model by role, BOW text, AC with its hash, inputs with the base SHA, owned paths, checks, stop condition, report contract (10-12 line reply, full evidence on disk). The dispatched agent does ALL the work in its worktree; the lead only reads the report.
5. When the agent returns, record its tokens from the Agent result (never estimated): `node E:/projects/63-SAFE/tools/usage-ledger.js add --item <BOW> --role <role> --tier <T> --model <m> --tokens <n> --tool-uses <n> --duration-ms <n>`.

## verify <BOW>
From the project's main checkout:
1. `node $T/lifecycle-check.js verify <CODE>` prints the evidence table: state, THE REVISION (tip of `lc/<CODE>/developer`), classify (TRIVIAL needs only the scope gate and CP-trivial), a PASS row per gate CP-T1..T4 and CP-verdict at that revision, the verdict file (CLEAN for that exact revision), and the AC hash against the frozen hash. Any FAIL stops here.
2. `node $T/lifecycle-check.js scope <base> <head> <owned-file>` must PASS for each role branch against its ownership file.
3. Merge, only here: each role branch into `lc/<CODE>/main` with `--no-ff` and subject `lifecycle-merge: <CODE> <role>`, then `lc/<CODE>/main` into the default branch with `git merge --no-ff` and the message `lifecycle-verify: <CODE>` + a blank line + a one-line summary (no AI trailer, GGR64).
4. Move to done through the one writer: `node $T/lifecycle-state.js move <CODE> done --role verify --by <identity> --evidence <verify output file>`.
5. List the role worktrees of the code for removal (`lifecycle-wt.js close <CODE> <role>`; clean ones only). Deleting branches is Aaron's gate.

This is the only path to `done`. A deliberate bypass (`--no-verify`) is caught by `node $T/lifecycle-check.js audit` and reported for Aaron's acknowledgement in `docs/lifecycle/waivers.md`.
