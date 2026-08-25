# Dispatch brief — policies temporal-order code + combineMultiplicative clamp (BUG-300 + routed finding)

**Dispatch:** Bill (RM/BA), 2026-08-21. **Lane:** E:\git\metropolis-pr71 (branch fix/policies-tempord-clamp, based on origin/main @ 760cfbb).
**Item:** BUG-300 (open) + the routed combineMultiplicative finding from Bev's queue (Tier 0.4).
**mkey tags for commits:** `[engine.policies]` (both findings are engine.policies estate).

## Authority
- BOW: `node claude-bow.js show BUG-300` (from E:\git\Metropolis).
- Acceptance: `docs/planning/acceptance/engine.policies.md` (or feat.policy.md) — the drift/preview ACs.

## The two findings

### 1. BUG-300 — temporal-order errors report ErrUnknownScope (GR#7 registry semantics)
Three call sites misuse `ErrUnknownScope` (a scope-lookup code) for *temporal-order* errors:
- `internal/engine/policies/drift.go` `AdvanceMonth`: `month < currentMonth` → returns ErrUnknownScope with scope:"month regression"
- `internal/engine/policies/drift.go` checkpoint: checkpoint month precedes current month → same misuse
- `internal/engine/policies/preview.go` `computePreview`: `toMonth < fromMonth` → ErrUnknownScope with scope:"inverted preview range"

**Fix:** mint ONE dedicated temporal-order code via `tools/plan/add-error.js` (main checkout, from E:\git\Metropolis):
```
node tools/plan/add-error.js add MET-G40xx --mkey engine.policies --name ErrInvalidTemporalOrder --template "..." --remedy "..."
```
Claim in the policies range (G4000-G4099; the file's header notes G4013-G4099 reserved/unclaimed). Use the NEXT free code in range — do NOT reuse G4013/G4014 blindly; check `data/errors.json` and the errors.go header comment for what is actually claimed. Wire the code at all three sites with the same shape (`errs.New(ErrInvalidTemporalOrder, ...)` keeping the correlation/scope-ish fields as context). No code path may fall back to ErrUnknownScope for a temporal-order failure.

**Regression tests (per site, all three):**
- `TestAdvanceMonthRegression` — AdvanceMonth(month < current) returns the new code, not ErrUnknownScope.
- `TestCheckpointPrecedesCurrent` — drift checkpoint before current month returns the new code.
- `TestPreviewImpactRangeInverted` — PreviewImpactRange to < from returns the new code.
Each asserts the registry code (GR#7 assertion per BUG-100 convention: code matches AND no partial state was created).

### 2. Routed finding — combineMultiplicative unbounded compounding (balance regime)
`combineMultiplicative` (combine.go) computes ∏(1+delta_i)−1 with NO bound. Stacking many policies (or a single large data delta) can produce arbitrarily large combined effects. 

**Fix (data-declared bound + clamp + disclosure):**
- Add a data-declared max combined |delta| to `data/policies.json` `meta` (e.g. `"maxCombinedAbsDelta": 2.0` — a placeholder value with a `$comment`/disclosure line naming it a placeholder pending Aaron's balance pass per ASM-284/ASM-286 regime; GR#15 — never a Go literal).
- `combineMultiplicative` clamps the result to `[-maxCombinedAbsDelta, +maxCombinedAbsDelta]` reading the bound from the loaded meta (pass it in or store it on the API — do NOT hardcode).
- Document the clamp in combine.go's doc comment (the disclosure: combined effects are bounded at the data-declared value; the bound is a balance placeholder).

**Regression test:**
- `TestCombinedEffectClamped` — a fixture/construction whose raw multiplicative product exceeds the bound asserts the returned combined delta equals exactly ±bound (not the raw product); a second test loads a *different* bound from a mutated fixture and asserts the clamp moves with the data (data-driven proof, same shape as AC-4-style reuse-horizon tests elsewhere).

## Constraints (house rules, all apply)
- GR#7: every error code via `tools/plan/add-error.js` from the MAIN checkout — never hand-edit data/errors.json. `add` then `check`.
- GR#15: no balance numbers in Go literals; the bound is data-sourced.
- GR#21: determinism — the clamp is a pure function of (deltas, bound); no map iteration in combination order.
- gofmt + LF + golangci errcheck clean before you report. `go test ./internal/engine/policies/... -race -count=1` green, then `go test ./...` for the touched package's dependents.
- NEVER use banned git commands (checkout --/reset --hard/restore/clean/stash). Undo via scratch copy.
- Do NOT commit. Build + test + report. The lead/round protocol handles verdict + commit.

## Report back
- The exact MET-G code minted and the three wired call sites.
- The data/policies.json meta addition + combine.go clamp signature.
- Full test list with results (each test named + green), and the `go test ./internal/engine/policies/...` + `go vet` + `gofmt -l` results.
- `git status --short` in the lane showing ONLY intended changes (policies + data/policies.json + add-error output in main checkout).
