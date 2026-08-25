# Dispatch brief — FEAT-042: int.protocol additive amendment (entity drill addressing + crisis signal)

**Dispatch:** Bill (RM/BA), 2026-08-21. **Lane:** E:\git\metropolis-pr73 (branch feat/042-protocol-amend-2, based on origin/main).
**Item:** FEAT-042 (P0, AARON APPROVED). **mkey tag:** `[int.protocol]`.
**Acceptance authority (READ FULLY FIRST):** `docs/planning/acceptance/int.protocol.md` — the `# FEAT-042` section (AC-19 through AC-36). AC-1–AC-18 are the frozen v1 record — DO NOT touch them.

## The amendment (narrower than the BOW title — ASM-305)
Whole-entity drill targets ("opens that junction") ALREADY work under the frozen `ViewName` grammar — that part is a DOCUMENTATION fix (AC-19), not a wire change. The genuine new wire capability:
1. **Sub-entity addressing** — address an element INSIDE an already-open view's patch (a ledger line, a diagram arrow). New additive type `EntityID` (string-based, exported, distinct from existing fields) + `TargetRef{ViewName string; EntityID EntityID}` (exactly two JSON-tagged exported fields). AC-20/AC-21/AC-22/AC-23.
2. **Crisis signal** — `Event` gains one additive `omitempty` bool `Crisis`, independently settable from `Severity` (AC-24/AC-25/AC-25b).

## The hard rules (frozen-contract surgery — read AC-26..AC-31 before ANY edit)
- **AC-28: ProtocolVersion STAYS `"1.0"`. Do NOT bump it.** `Validate` does exact string equality — a bump breaks every recorded fixture.
- **AC-26: byte-determinism proof.** Capture golden JSON fixtures for `Command`/`Delta`/`Event` at the pre-amendment state FIRST (before your struct edits — copy the current output), check them in, then re-marshal under your changes and diff. All new fields must be `omitempty` so zero-value marshalling is byte-identical.
- **AC-31: eight consumer packages must keep compiling with NO source change** (ui.core, ui.widgets, ui.screen.map, ui.screen.debug, harness.stub, harness.headless, harness.replay, engine.core). If `go build ./...` fails, it means a positional struct literal existed — fix it by converting to a keyed literal in the SAME commit and CALL IT OUT in your report (per the Escalations note, that's the desired failure mode, not something to hide).
- **AC-30: `go list -deps ./internal/protocol/...` shows no `internal/engine` or `internal/ui` import.**
- **AC-32: any new validation error = new `MET-P0xx` code from the protocol reserved range** (check `internal/protocol/codes.go` + `data/errors.json` `ranges.reserved` + live grep `MET-P[0-9]` in internal/ cmd/) — register via `node tools/plan/add-error.js` from E:\git\Metropolis (main checkout), then `check`.
- **AC-29: a pre-amendment-shaped replay fixture (no new keys) loads cleanly with new fields at zero values.**
- **AC-33/34: no `time.Now`, no map-iteration-order into wire bytes.**
- **AC-22:** if any consumer defines its own parallel struct instead of reusing `TargetRef`, a reflection-based field-parity test is required there — but OUT OF SCOPE here unless one exists already; this lane only ships the protocol-side types.

## Scope guardrails
- ONLY `internal/protocol/` + the golden fixtures + errors registry additions + doc comments. NO consumer-package changes except the mechanical positional→keyed conversion AC-31 forces (call it out).
- NO view patch schemas (out of scope per the AC). NO crisis taxonomy decision (ASM-277 is Aaron's).
- GR#15: no invented numbers. GR#7: registry codes only via the tool. GR#21: determinism.
- NEVER banned git commands (checkout --/reset --hard/restore/clean/stash); undo via scratch copy.
- Do NOT commit, do NOT push. Build + test + report.

## Test discipline (RED→GREEN per test, proven)
Each regression/compatibility test must be shown RED before the fix and GREEN after (scratch-copy mutation, never git-revert). Cover at minimum:
- `TestValidateEntityID` (AC-20, valid+invalid)
- `TestTargetRefJSONRoundTrip` (AC-21 field-for-field)
- `TestCrisisIndependentOfSeverity` (AC-24 two-sided: Info+Crisis:true, Critical+Crisis:false)
- `TestCrisisAbsentDefaultsFalse` (AC-25)
- `TestGoldenBytesUnchanged` (AC-26 — the byte-diff against pre-amendment fixtures)
- `TestProtocolVersionStill10` (AC-28)
- `TestPreAmendmentReplayLoads` (AC-29)
- `TestNoEngineOrUIImport` (AC-30)

## Report back
1. The exact new types (EntityID, TargetRef) + Crisis field with their doc comments.
2. Golden fixture approach + the byte-diff evidence (AC-26).
3. Any positional-literal fix forced by AC-31 (package + file).
4. The MET-P0xx code minted + the free-code check you ran.
5. Full test list: RED→GREEN proof per test (1-2 lines), `go test ./internal/protocol/... -race -count=1`, `go test ./...`, `go vet ./...`, `gofmt -l`, golangci errcheck.
6. `git status --short` in the lane (only intended changes).
7. Any deviations + why.
