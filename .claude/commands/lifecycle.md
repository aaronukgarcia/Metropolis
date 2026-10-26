---
name: lifecycle
description: Plan and drive a code project through the separated-role dev lifecycle. Always asks first whether it is a lash-up. Usage: /lifecycle plan [scope] | status | dispatch <BOW> | verify <BOW>. Spec: docs/forge/dev-lifecycle-PROPOSAL.md; plan template: docs/forge/lifecycle-PLAN-TEMPLATE.md.
---

# /lifecycle

Read `docs/forge/dev-lifecycle-PROPOSAL.md` first (installed by forge; canonical copies of the spec, `lifecycle-PLAN-TEMPLATE.md` and `schema/lifecycle.sql` are in `E:\projects\63-SAFE`). Role guard: `~/.git-hooks/lifecycle-role-guard.js`, driven by `lifecycle.json` in the repo root and `LIFECYCLE_ROLE` on each dispatch. Rules in force: GGR7 identity, GGR15 destructive verdict, GGR41 test isolation, GGR47 gates not memory, GGR48 contract-first, GGR51 graph-driven AC, GGR53 code.json, GGR54 quality bar, GGR58 dispatch budget, GGR64 no AI attribution, GGR65 separation of duties.

## plan [scope]
0. **Gate zero.** AskUserQuestion: "Is this a lash-up (prototype, throw-away, just get it working)?" Store the answer as I0.
   - Yes: set `"mode": "lashup"` in code.json and in `lifecycle.json` at the repo root, add PROTOTYPE, NOT FOR REUSE to the README, stop. Only the always-on rules apply.
   - No: write `lifecycle.json` at the repo root with `"mode": "lifecycle"` and the path classes (source, ac, acceptance_tests, unit_tests) so the role guard can act, then continue. Nothing below may be skipped without Aaron's explicit answer, logged in `lifecycle_event`.
1. **Intake** I1 to I11 (proposal section 2) in batches of four or fewer; store in `harvest/answers/<slug>.json`. If I1 says refactor and the existing code has no offsite copy, raise it to SAFE before anything else.
2. **PLAN.md** from `lifecycle-PLAN-TEMPLATE.md`, every required section filled. If a PLAN.md exists (as in Photos), diff it against the template and list the missing sections instead of overwriting.
3. **code.json**: refactor, derive modules from the existing code; green field, generate from PLAN.md. Refuse to continue without it (GGR53).
4. **BOW decomposition**: one item per module key, plus a parent epic per milestone. Insert as `draft`.
5. **AC** per item in the proposal section 4 format, ending "Assumptions: none". Cross-module calls must be registered edges.
6. **AC Reviewer** dispatch per item (Opus, BUDGET line). It may only write assumption comments and the accept or reject transition. Bounce 3 goes to Aaron.
7. Report the queue.

## status
Items by state, items over budget, bounce-2 assumptions waiting for Aaron, AC hash mismatches, milestones blocked by an external.

## dispatch <BOW>
Pick the next role for the item's state from the roles table and its tier from proposal 6a (in doubt, the higher tier; two FIX verdicts on the item move it up a tier). Refuse if the identity may not write what the role needs. Batch units expected to take under about 15 tool calls into one dispatch. Write the owned paths to `<project temp>/lifecycle/<BOW>/owned.txt`, then send the brief from `docs/forge/lifecycle-BRIEF-TEMPLATE.md` with every line filled: ROLE/ROUND/TIER, the GGR58 BUDGET line, BOW text, AC with its hash, inputs with the base SHA, owned paths, checks, stop condition, report contract (10-12 line reply, full evidence on disk). Record in `lifecycle_event`. When the agent returns, record its tokens from the Agent result (never estimated): `node E:/projects/63-SAFE/tools/usage-ledger.js add --item <BOW> --role <role> --tier <T> --model <m> --tokens <n> --tool-uses <n> --duration-ms <n>`.

## verify <BOW>
Rerun every gate for the item's current state and print the evidence table. From the repo, with `L=E:/projects/63-SAFE/tools/lifecycle-check.js`:
1. `node $L classify <base> <head>`. TRIVIAL: only the scope gate and CP-trivial are needed (no testers, no verdict dispatch). REVIEW: the full chain below.
2. `node $L scope <base> <head> <owned.txt>` must PASS.
3. CP-T1 to CP-T4 each have a PASS row, and for every tester review file and the verdict file `node $L verdict <review.md> <head>` must PASS (the review is for this exact head and says CLEAN).
4. The verdict is by a non-author, non-T4 identity, and the AC hash equals the frozen hash.

Move the item on only when each gate has a PASS row, with the gate output as `evidence_ref`. This is the only path to `done`.
