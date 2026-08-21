---
description: Bill's standing loop ritual — checkin, read queue, fetch, work the tiers, never idle
---

# /bill-loop — Bill's standing loop ritual

Run this EVERY loop tick (the `/loop 15m` prompt is just `/bill-loop`). Do ALL steps, in order, every time:

1. `node claude-sync.js checkin --name Bill` (always the flag, from E:\git\Metropolis).
2. `node claude-sync.js read` — process EVERY unread message; reply to any ask immediately.
3. `git -C /e/git/metropolis-bob fetch origin` — fetch BEFORE anything; never build against a stale ref.
4. Read `E:\git\metropolis-status\bev-to-bill.md` — Bev's live orders (the top "Current queue" section supersedes everything below it).
5. Write a heartbeat line to `E:\git\metropolis-status\bill.status.md` (timestamp + one-line state).
6. **WORK THE TIERS — NEVER IDLE.** If a lane is free, dispatch. Pull the next item, in order: Tier B (bugs) → Tier C (features) → Tier D (ICD-stubs-then-build, the default well) → Tier E (verification proposals). If an item awaits a round, pull the next edge-clean item — there is always something.
7. Reply to Bev via `node claude-sync.js message "<text>" --to Bev` at each wave boundary.

NEVER: wait idle for Bev · build against a stale ref · skip the fetch · forget the heartbeat · edit code.json / master-plan / data/errors.json · self-verdict · use a banned git command (checkout --/reset --hard/restore/clean/stash) · `done --force`.

New-build flow (Tier C/D): build → message Bev "ready for round" → she rounds + records verdict → then commit exact paths + push (the destructive-guard blocks the commit until the verdict lands). Docs-only `*.md` and test-only commits pass without a verdict.
