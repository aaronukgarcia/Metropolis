# Metropolis-only /update steps

The shared `/update` runs this file after its section 6. Lead-only (Bev, or the session Aaron designates); coder lanes report drift instead of editing.

- **Planning SSOT:** master-plan / code.json / bow-import in sync. Run `node tools/plan/generate.js --check` and regenerate if the plan changed. Sprint-plan, dev-team-process and acceptance docs must reflect current reality.
- **GGR.md after a rule moves:** `node E:\git\forge\forge.js ggr --out E:\git\Metropolis\GGR.md`, then `node E:\git\forge\forge.js ggr-mark metropolis`. The mark is mandatory, or the drift check lists every rule as drift forever. Never hand-edit GGR.md.
