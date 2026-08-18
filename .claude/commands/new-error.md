---
description: Create a new error code in the Metropolis error registry (data/errors.json) — GR#7: every error is registry-sourced, no exceptions
allowed-tools: Read, Edit, Bash(node:*)
---

# /new-error — register a new error code via automated tools

GR#7: **every** error anywhere in the system is constructed from the registry (data/errors.json). Ad-hoc error construction is strictly banned.

## The Three Commands: claim-range -> add -> check

`tools/plan/add-error.js` (BUG-273) is the single allocator for the shared
`data/errors.json` registry. It exists so nobody hand-picks a number range
by eyeballing the file — every worktree runs the same tool against its own
copy, and the tool itself finds the next free block by scanning both the
existing codes AND the existing reservations (`ranges.reserved`). This is
what stops the class of collision BUG-273 was filed for (two worktrees
independently "reserving" the same G4300-G4399 block by hand on the same
day).

1. **Claim a range for your module** (only needed once per module/mkey —
   skip straight to step 2 if your mkey already owns a reservation):
   ```bash
   node tools/plan/add-error.js claim-range <mkey> [--size 100] [--layer X] [--dry-run]
   ```
   The layer letter is inferred from the mkey prefix (`foundation.*` -> F,
   `engine.*` -> G, `ui.*` -> V, `harness.*` -> H — the E/U layers are
   fully exhausted so new engine/ui ranges land in their overflow letters,
   per the existing `ranges.reserved` entries). `feat.*` mkeys are
   ambiguous (some live in the engine G layer, at least one lives in the
   ui U/V layer) — pass `--layer <LETTER>` explicitly for those. Prints
   the allocated range (e.g. `G4400-G4499`) and records it in
   `ranges.reserved`.

2. **Register the code** once you know which range it belongs to:
   ```bash
   node tools/plan/add-error.js add MET-G4400 --mkey <mkey> --name <ErrName> \
     --template "Your descriptive error template with {context} fields" \
     --remedy "..." --severity <fatal|error|warn>
   ```
   Refuses to run if the code is malformed, already taken, or falls
   outside a range reserved for your mkey (it will name the actual owner
   if someone else already claimed that block). Inserts the entry into
   `data/errors.json` via a targeted splice — the rest of the file is
   untouched byte-for-byte.

3. **Lint the whole registry** (CI-able, exit 1 on violation):
   ```bash
   node tools/plan/add-error.js check
   ```
   Catches duplicate codes, codes with no owning reservation, and
   overlapping reservations.

Every subcommand supports `--dry-run` to preview without writing.

### Worktree rebase-first warning

`check` also compares your local `data/errors.json` reservations against
`origin/main`'s and prints a **WARNING** (never a failure) if origin/main
has reservations your local file doesn't. If you see that warning, **pull
main before running `claim-range`** — claiming against a stale view of the
registry is exactly how two worktrees end up fighting over the same block.

### After registering

Verify the constant is cleanly generated inside
`internal/foundation/errs/registry.go` and runs green.
