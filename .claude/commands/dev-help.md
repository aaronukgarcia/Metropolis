# /dev-help — parallel dev-team survival guide (Ben / Bill / any coder session)

When a coder session (or Aaron) types `/dev-help`, print this guidance, adapted to the caller's assigned lane if known. Full authority: `docs/planning/parallel-coder-brief.md` (read it in full once per session; this skill is the quick reference).

## The one rule that keeps everyone safe
**You share ONE git checkout with other live sessions.** Stage only your exact file paths; never run a git command that reverts the tree. Concretely:
- BANNED always: `git checkout -- <path>` / `checkout .` / `checkout -f`, `git reset --hard|--keep`, `git restore` (non-`--staged`), `git clean -f/-d/-x`, `git stash`, `git add -A` / `add .` / `add -u` / `commit -am`.
- To undo your own change: `cp file file.bak` first, restore from the copy — never from git.
- Files you didn't create that show modified/untracked in `git status` are **another session's live work — frozen, invisible, not yours to fix or clean**.

## Lanes (edit ONLY inside yours)
- **Ben:** `internal/engine/{traffic,parking,dispatch,staffing,cafe,shopping,tunnels}/`
- **Bill:** `internal/engine/{tourism,extcommute,prison,policies,destination,fuel,airunits}/` (7 modules — don't drop policies/destination/fuel)
- **Bev:** `internal/foundation/integration/`, `internal/engine/compose/`, held tooling files.
- No-go for coders: `internal/foundation/integration/`, `internal/engine/compose/`, `code.json`, `docs/planning/master-plan-v2.1.json`, `tools/plan/*`, `data/errors.json` except via `/new-error` in YOUR module's own MET-range (F900–F919 belongs to foundation.integration), the other coder's lane, held tooling (`claude-sync.js`, `claude-startup.js`, `tools/bow-server.js`, `bow-ui-template.html`, `.gitignore`).

## Build a REAL module (not a stub)
1. Read `docs/planning/acceptance/engine.<name>.md` fully; implement every AC with real logic (no `return 5.0 // placeholder` as the deliverable).
2. GR#15: player-felt numbers live in `data/<module>.json` with a disclosure, never Go literals. GR#21: `det.NewStream(...)` for randomness; no `time.Now()`/`rand`/map-range on tick paths. GR#20: keep the `checkNotCopied()` copy-guard on public entry points; consume dependencies only via registered `code.json` interfaces (test-stub them). GR#25: a new cross-module edge = STOP and flag Aaron/Bev; never hand-add.
3. Error codes: `/new-error` — never hand-edit `data/errors.json` or reuse another module's range. New Go files with error codes MUST be registered or `internal/foundation/errs`'s source-scan gate fails the whole tree.
4. Tests must assert the AC's observable behaviour and be able to FAIL (break the logic, see red, fix). A test asserting a constant equals itself is a banned self-fulfilling test.

## Pipeline per module (GR#23 — nothing commits un-attacked)
```
build → own tests green → Destructive round (attack: determinism, bounds, nil,
concurrency, does it MEET the AC) → node claude-bow.js destructive <CODE>
--verdict accept --attacker "<you>" --note "..." → git add <exact paths> →
git commit -m "feat: ... [engine.<name>]" → node claude-bow.js ref <CODE> <hash>
→ done <CODE> → push same session → merge only on green CI
```
Verify before commit: `gofmt -l`, `go build ./internal/engine/<name>/...`, `go vet`, `go test -race -count=2 ./internal/engine/<name>/...`. Never `go build ./...` (others' in-flight work may not compile — expected, not yours).

## Coordination cribs
- Start: `node claude-sync.js checkin` → `node claude-sync.js claim internal/engine/<name>` per module → poll `node claude-sync.js read` every few actions.
- Before starting an item: `git log -10` + `node claude-bow.js show <CODE>` (has anyone touched it?).
- Message the lead: `node claude-sync.js message "<text>" --to Bev` (or Bill/Bob/Ben).
- Stuck / spec needs a new edge / gate blocks you: STOP and ask Aaron/Bev — never bypass a guard, never widen an allowlist, never disable a hook.

## Common traps (learned the hard way this week)
- CI's astgate copyguard ratchet flags every NEW method on a guarded type: add a real `checkNotCopied` guard for public entry points; `accepted-findings.json` entries only for true internal helpers (mirror existing precedent).
- The secret-guard's entropy check false-positives on 25+ char mixed-case field names — surface it; add a precise `exact` allowlist entry, never widen a pattern.
- The destructive-guard can't read `-F`/heredoc commit messages — use `-m` with the `[mkey]` inline; and it blocks the whole compound `add && commit`, so re-run with `;`.
- `go run` launders exit codes — build the binary and run it when an exit code matters.
