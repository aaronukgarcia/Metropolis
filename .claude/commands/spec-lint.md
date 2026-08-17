---
description: Audit the acceptance criteria markdown files against code.json to detect Prose-vs-Graph dependency violations (Golden Rule #25)
allowed-tools: Bash, Read
---

# /spec-lint — audit the specification estate for graph and interface compliance

Use this command to verify that your acceptance criteria specifications strictly adhere to Golden Rule #25 (no speculative, unregistered dependencies in prose).

## Execution

Run the static linter:
```bash
node tools/plan/spec-lint.js
```

## Findings Handled

- **[SPEC-LINT-001] Graph Violation:** The prose cites a dependency on an external module key, but no such outbound edge is registered in `code.json`.
- **[SPEC-LINT-002] Interface Mismatch:** The prose references a Go API method or symbol, but the target package on disk does not export or contain that symbol.

If any findings are reported, the BAs must coordinate with the Architect to either register the outbound edges in `code.json` or adjust the criteria to match the physical Go package interfaces before dispatching.
