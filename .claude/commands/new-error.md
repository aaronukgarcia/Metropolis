---
description: Create a new error code in the Metropolis error registry (data/errors.json) — GR#7: every error is registry-sourced, no exceptions
allowed-tools: Read, Edit, Bash(node:*)
---

# /new-error — register a new error code via automated tools

GR#7: **every** error anywhere in the system is constructed from the registry (data/errors.json). Ad-hoc error construction is strictly banned.

## Fast Registering One-Liner

To bypass the 193KB `errors.json` manual editing and avoid merge conflicts on a parallel wave:

1. **Run the Code Generator Tool:**
   Use the automatic error generator to register the new code, allocate its range reservation, and generate its Go constant in one step:
   ```bash
   node tools/plan/add-error.js --layer <layer> --msg "Your descriptive error template with {context} fields" --severity <fatal|error|warn> --module <module_key>
   ```
   (Layers: `F` foundation, `P` protocol, `E` engine, `U` ui, `T` tooling. Example: `E` for engine).

2. **Entry shape generated:** Code (`MET-<layer><NNN>`), severity, message template, remediation hint, and owning module key.

3. **Verify:**
   Check that the constant is cleanly generated inside `internal/foundation/errs/registry.go` and runs green.
