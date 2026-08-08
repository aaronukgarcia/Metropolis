---
description: Register new code in the Metropolis registry — new modules go in the master plan, code files carry their module's GUID header (GR#6, GR#20)
allowed-tools: Bash(node:*), Read, Edit
---

## Your task

Register code in the Metropolis registry. Registration flows **through the master plan**, never by hand-editing code.json:

1. **New module/feature/interface** (not yet in the plan):
   - Add the item to `docs/planning/master-plan-v2.1.json` (key, type, seq — gapped tens, sprint, priority, milestone, layer, title, desc, specRef, path, deps, calls, inbound name/format/pattern).
   - `node tools/plan/generate.js` → mints its GUIDs (module + inbound + outbound), recomputes forward/reverse pointers.
   - `node claude-bow.js import tools/plan/bow-import.json` → BOW row appears.
   - Commit plan + regenerated outputs together.
   - *Ad-hoc work that is NOT a code module* (bugs, process tasks) goes straight into the BOW with `claude-bow.js add` — no plan entry.

2. **New source file for an existing module** (once Go code exists): the file's package header comment must carry the owning module's GUID, key and spec ref from code.json, e.g.
   ```go
   // Package traffic — Metropolis module engine.traffic
   // GUID f048ef78-9446-4fdb-845e-500fda1f2743 · spec §19, §51, A4 · see code.json
   ```

3. **While registering, check the code being registered** (GR compliance): GR#1 error trapping via `errs.New(code, correlationId, ctx)` only; GR#7 error codes exist in `data/errors.json`; GR#15 no hardcoded expected values; GR#20 no imports bypassing registered interfaces.

Confirm with the item's mkey, GUID, and BOW code.
