# Meta-tooling reform — proposal

**Date:** 2026-08-16 · **Author:** Bob (Slot 2), on findings from an independent audit (Ben, auditing slot)
**Status:** PROPOSAL for Aaron — split into **Tier A (safe, stand up now)** and **Tier B (Golden-Rule reversals — Aaron's call).**

---

## Provenance

An independent audit logged six findings to the BOW (BUG-243…247, FEAT-135). The audit's recommendations are a mix: three are safe additive improvements, and three propose **reversing Golden Rules** (GR#7, GR#24) that Aaron set for specific, documented reasons. This proposal separates the two and recommends standing up the safe half now while deferring the reversals to Aaron.

---

## The six findings

| Code | Pri | Finding | Recommendation |
|------|-----|---------|----------------|
| BUG-243 | P1 | 35+ custom Node.js guards/hooks create compliance bloat + bypass-debug loops | Consolidate into CI |
| BUG-244 | P1 | `claude-worktree-guard.js` bans `git restore`/`stash`/`reset --hard`, forcing hand-rolled copy cycles | **Decommission it** |
| BUG-245 | P0 | Perf gate's `AcceptedRegression` JSON override is forgeable; baseline measured below noise floor | Hard CI check + signed tolerances |
| BUG-246 | P2 | BA specs: contradictions (ASM-468), phantom citations (BUG-075), prose-vs-graph drift (4 lint findings) | BA quality audit |
| FEAT-135 | P2 | 95 security findings (42 open) from lack of centralized safe primitives | Centralize `safeInt64`/copy-guard wrappers |
| BUG-247 | P2 | `errors.json` bloated to 193KB; adding one code is heavy | **Re-evaluate GR#7** |

---

## Tier A — safe additive (stand up now)

### A1. FEAT-135 — secure-by-default Go helpers *(additive, no reversal)*
Centralize the recurring safety primitives so juniors stop writing insecure code that only the Destructive agent catches:
- `foundation/num`-style **type-safe coercion** (`safeInt64`, bounded/NaN-checked floats) — already partially exists; make it the only sanctioned path.
- **Copy-guard + defensive-copy wrappers** in `foundation/registry` so `checkNotCopied` boilerplate isn't hand-rolled per module (the SEC-066 live-pointer class).
- Mandate use in junior briefs + acceptance criteria (feed it into the `/brief` skill's mandatory block).

This *adds* a library; it removes nothing. **Stand up: BA criteria → build.**

### A2. BUG-245 — harden the perf-gate ledger *(lighter fix than the audit's)*
The audit is right that the `AcceptedRegression` JSON is forgeable. The signed-tolerance idea is heavy; a lighter, integrity-preserving fix:
- Move the accepted-regression ledger from a **locally-editable JSON** to a **git-tracked file reviewed in-PR** (or CI-side), so a local edit can't self-vouch a regression.
- Keep the three-exit-code gate (BUG-071) but ensure the ledger provenance is the commit, not the workstation.
- Re-derive the baseline at real scale (already tracked as BUG-034/ASM-352).

**Stand up: junior fix → Tester → Destructive.**

### A3. BUG-246 — BA quality audit *(already in flight)*
The audit's four drift classes — contradictory ACs, phantom `ASM-` citations, prose-vs-graph drift, stale spec line refs — are exactly what the current **audit + refresh tranche** is resolving. No new dispatch needed; it continues.

---

## Tier B — Golden-Rule reversals (Aaron's decision)

These three are listed faithfully, with the trade-off on each side. **Not actioned without Aaron.**

### B1. BUG-244 — decommission `claude-worktree-guard.js`
- **Why it exists (GR#24):** a Destructive agent's `git checkout --` destroyed **211 lines of uncommitted work**. The guard fail-closes the destructive-git-command family (`checkout --`, `restore`, `reset --hard`, `clean`, `stash`) so that specific loss can't recur.
- **The audit's cost:** hand-rolled `cp f f.bak` cycles, tree-contamination risk, friction.
- **Trade-off:** decommissioning *removes the protection* before a replacement exists. GR#24's own text already prescribes the real fix — *commit early, worktrees, branch staging* — which is a **training/automation** problem, not a guard problem.
- **Recommendation:** keep the guard; add the `/brief`-block training + a worktree helper. Revisit only if Aaron wants the risk back.

### B2. BUG-247 — re-evaluate GR#7 (central `errors.json`)
- **Why it exists (GR#7):** every error registry-sourced → consistent codes, correlation-ID tracking, and the determinism/observability the sim depends on.
- **The audit's cost:** 193KB JSON, per-code edit friction, merge conflicts, "reuse generic error" temptation.
- **Trade-off:** the fix is **tooling to add codes cheaply** (`/new-error` already exists; make it a one-liner) and **package-level ranges** (F400–F499 already scoped per module), *not* abandoning the central registry.
- **Recommendation:** keep GR#7; improve the `/new-error` ergonomics. Aaron to rule whether a package-local declaration (still keyed to a central range) is acceptable.

### B3. BUG-243 — consolidate the 35+ guards into CI
- **Why they exist (GR#22/23/24 + FEAT-040):** codename discipline, "nothing committed un-attacked", worktree protection — each is a mechanical gate Aaron set after a real incident.
- **The audit's cost:** the bypass arms-race (BUG-123 6 rounds, BUG-119 10 rounds) burned tokens.
- **Trade-off:** moving to CI means the protections stop being *fail-closed local* and become *post-push* — the codename/author/destructive leaks would already be public before CI rejects them. That's the whole reason they're pre-commit.
- **Recommendation:** keep local; the real win is the **single-scanner consolidation** (BUG-224's `scanGitInvocations`) so the 35 files share one parser instead of 35. Aaron to rule on any deeper consolidation.

---

## Recommendation

1. **Stand up Tier A now** — FEAT-135 (secure helpers), BUG-245 (perf-gate ledger hardening); BUG-246 is already running.
2. **Hold Tier B for Aaron** — the three reversals each trade a real, incident-earned protection for a real friction cost. The safe middle for each (worktree *training*, `/new-error` ergonomics, single-scanner consolidation) is noted above.

No Golden Rule is reversed by this proposal; the reversals are surfaced for Aaron, not pre-approved.

---

## New custom skills (audit protocol item 3)

The audit asked specifically: "highlight where we can build new custom skills to automate checking, compile standard code templates, and simplify code generation." These are additive and safe — none reverses a Golden Rule.

| Skill | What it automates | Backs |
|-------|-------------------|-------|
| `/ci-green` | One command that runs the *exact* CI surface locally — `gofmt -l .`, `go build ./...`, `go vet ./...`, `golangci-lint run ./...`, `go test ./... -count=1 -race`, `node --test` — so juniors catch red gates before push, not after (the BUG-240/241/242 class). | A, D |
| `/new-error` (one-liner) | Extend the existing skill so it generates the registry entry + the Go constant + the range reservation in one call, removing the 193KB hand-edit friction. | BUG-247 |
| `/astgate-accept` | Guided "review + accept a copy-guard finding" flow — replaces the manual accepted-findings.json edit (the 23-finding exercise this wave did by hand) with a reviewed command. | BUG-243 |
| `/safe-module` template | A new-module code template that pre-wires `checkNotCopied`, registry-sourced errors, and `safeInt64`/NaN-checked coercion so incoming juniors start secure-by-default. | FEAT-135 |
| `/worktree-stage` | Teaches `git worktree` + clean branch staging as the GR#24-safe alternative to banned destructive commands (the training half of B1). | BUG-244 |
| `/brief` (extend) | Add a mandatory "secure-by-default primitives" block so every junior brief names the safe helpers, matching how GR#15/GR#20 are already named. | FEAT-135 |

---

## Implementation plan (next steps)

**Tier A — stand up now (no reversal):**
1. FEAT-135 — BA criteria for the centralized safe-primitives library → build → Tester → Destructive.
2. BUG-245 — junior fix (git-tracked perf ledger, in-PR provenance) → Tester → Destructive.
3. Build `/ci-green` + `/astgate-accept` + `/new-error` ergonomics (tooling, `tool.*` lane).

**Tier B — Aaron's go/no-go (each surfaced with trade-offs in B1/B2/B3 above):**
4. BUG-244 — Aaron decides worktree-guard fate (recommendation: keep, add training).
5. BUG-247 — Aaron rules whether package-local error declaration is acceptable (recommendation: keep GR#7, improve ergonomics).
6. BUG-243 — Aaron rules on guard-consolidation depth (recommendation: single-scanner consolidation, keep pre-commit fail-closed).

No file is modified by this proposal itself; Tier A items are queued in the BOW and stand up through the normal pipeline the moment Aaron gives the word.
