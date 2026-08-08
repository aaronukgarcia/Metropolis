---
description: Create a new error code in the Metropolis error registry (data/errors.json) — GR#7: every error is registry-sourced, no exceptions
allowed-tools: Read, Edit, Bash(node:*)
---

## Your task

Add a new error code to the Metropolis error registry. GR#7: **every** error anywhere in the system is constructed from the registry — ad-hoc errors are banned (lint-enforced once `foundation.errors` lands, MOD-002).

**Code format:** `MET-<layer><NNN>` — layers: `F` foundation, `P` protocol, `E` engine, `U` ui, `T` tooling. Example: `MET-E042`.

1. **Registry file:** `data/errors.json` (created by `foundation.data`/`foundation.errors` in Sprint 0; until it exists, tooling errors are declared inline in the tool's header comment — see `tools/plan/generate.js` for the current MET-T0xx set, and migrate them into the registry when it lands).
2. **Entry shape:** code, severity, message template, remediation hint, owning module key (must exist in code.json).
3. **Check for an existing code first** — near-duplicates get reused, not multiplied.
4. **Every raise site** must supply a correlation ID and context object (GR#1); errors are logged to NDJSON and stored for review (F12 tail / `metctl errors`), never printed-and-lost.
5. If the error accompanies new code, run `/register-guid` checks on that code too.

Confirm with the new code, its registry entry, and the module it belongs to.
