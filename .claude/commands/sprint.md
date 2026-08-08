# /sprint — what to build next (Metropolis sprint board)

Show the current state of the sprint-structured Book of Work and what is ready to build right now. The build order lives in the metro BOW (`sprint` + `seq` fields, loaded from `docs/planning/master-plan-v2.1.json`); the rationale is `docs/planning/sprint-plan-v1.md`.

## Steps

1. **Ready-to-build view** (the only legitimate place to pick up work — M0-ENG §6.2: never start a blocked item):
   ```powershell
   node claude-bow.js ready
   ```
2. **Current sprint context** — full build order with status:
   ```powershell
   node claude-bow.js list --by-seq
   ```
3. Report to the user:
   - The lowest-numbered sprint that still has open items (= the current sprint), its open/in-progress/done counts, and its exit criteria from `docs/planning/sprint-plan-v1.md`.
   - The top ready items in seq order with their spec refs (`node claude-bow.js show <code>` for detail).
   - Anything `blocked` and what unblocks it.
4. If asked to start an item: `node claude-bow.js set <code> --status in_progress`, read its `spec_ref` sections of `docs/METROPOLIS-MASTER-v2.1.md` IN FULL first (working agreement §6.1), and check its inbound/outbound contracts in `code.json`.

## Rules

- Work strictly from `ready`; priority then seq order within the sprint.
- A sprint is done only when every item passes its Definition of Done (M0-ENG §6.3) — including stub maintenance and the determinism gate staying green.
- New work discovered mid-sprint gets a BOW item immediately (add with `--sprint`/`--seq` placing it honestly, then regenerate is NOT needed for ad-hoc items — only master-plan items live in the plan file).
