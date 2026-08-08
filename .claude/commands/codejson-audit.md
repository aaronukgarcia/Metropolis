---
description: Audit code.json integrity — plan drift, BOW/GUID consistency, and (once code exists) source-header GUID coverage
allowed-tools: Bash(node:*), Bash(git:*)
---

## Context

- Plan check: !`node tools/plan/generate.js --check`

## Your task

Audit the Metropolis code registry. **code.json is GENERATED** from `docs/planning/master-plan-v2.1.json` by `tools/plan/generate.js` — it is never hand-edited (GR#3, GR#6, GR#20). The audit therefore checks *consistency between the three stores*, not hand-maintained content:

1. **Plan validity** — `node tools/plan/generate.js --check` must pass (unique keys/seqs, acyclic deps, every item spec-referenced).
2. **Drift** — regenerate and diff: run `node tools/plan/generate.js`, then `git diff --stat code.json tools/plan/bow-import.json`. Any diff means someone changed the plan without regenerating (or hand-edited an output) — report it and commit the regeneration. (`tool.planguard` will automate this at commit time.)
3. **BOW mirror** — spot-check ~5 items: `node claude-bow.js show <mkey>` must show the same guid/guid_in/guid_out/seq/sprint as the code.json entry.
4. **Source coverage** (once `cmd/`/`internal/` exist) — every Go package listed in code.json has a header comment carrying its module GUID and spec ref (GR#6); every source directory maps to a code.json entry. Report orphans both ways.

Report findings as a table (check → pass/fail → detail), fix what is mechanical (regeneration), and raise BOW bugs for anything structural.
