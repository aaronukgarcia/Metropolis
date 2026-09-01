---
description: Reverse-direction code.json edge lint — real Go imports / header edge annotations must be registered in code.json (BUG-482, GR#25)
allowed-tools: Bash(node:*), Read
---

# /edge-lint — catch a real edge missing FROM code.json (the reverse of /codejson-audit)

`tools/plan/codejson-audit.js` only checks the FORWARD direction: every
code.json-declared outbound call edge must be backed by a real Go import
(AC-3). It says nothing about the REVERSE direction — a real Go import, or a
`(edge A->B)` header annotation, between two registered modules that has no
corresponding outbound edge in code.json at all. That is exactly the drift
class BUG-478 found and fixed by hand, and the BUG-478 independent round
proved it is otherwise silent: deleting a real edge from a scratch code.json
left codejson-audit, astgate, spec-lint, and generate.test all unchanged.

## Execution

```bash
node tools/plan/edge-lint.js
```

## Findings handled

- **[EDGE-LINT-001] Missing reverse edge:** a real Go import (via a real
  go/ast parse, same `tools/plan/astinfo` helper codejson-audit uses) or a
  `(edge A->B)` / `(edge A→B)` header annotation between two REGISTERED
  code.json modules has no matching entry in `A`'s `outbound.calls`. Fix:
  register the edge in `master-plan-v2.1.json` and regenerate (GR#25 — the
  BA/Architect edge-registration flow), or, if the import is stale, remove
  it from the Go source.
- **[EDGE-LINT-002] Unknown module key in a header annotation:** a `(edge
  A->B)` annotation names a key (on either side) that does not exist in
  code.json at all — the typo class. Fix: correct the spelling in the Go
  header comment (never hand-edit code.json to match a typo).

## Baseline (`tools/plan/edge-lint-baseline.json`)

The real repo already carried reverse-direction findings that predate this
tool (the 2026-08-17 "292 missing imports" finding was never fully closed).
Every finding is always printed, never silenced — but only a finding NOT
listed in the baseline fails the gate (exit 1). Burning the baseline down is
tracked as its own follow-up work; adding to the baseline is not a way to
make a new finding disappear — treat every addition with the same scrutiny
as a code.json edge registration.

## Exit code

Unlike `codejson-audit.js` (report-only, always exits 0), `edge-lint.js` IS a
gate: exit 1 on any finding not in the baseline, exit 0 otherwise. Wired into
`node --test` automatically via `tools/plan/edge-lint.test.js` (the CI
node-test job runs a bare `node --test` at the repo root, which discovers
every `*.test.js` — no separate CI step needed).

`edge-lint` never hand-edits code.json, the master plan, or Go source.
