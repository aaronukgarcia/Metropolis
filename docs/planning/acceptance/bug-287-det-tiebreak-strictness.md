# BUG-287 — det ApplyBarrier / SumInShardOrder lose determinism on key ties

**Bug (confirmed live 2026-09-01):** three sites in the determinism layer use an **unstable
`sort.Slice`** and silently tolerate tied sort keys, so the applied/summed order depends on the
input slice order — which is exactly the goroutine-scheduling-dependent thing the `det` package
exists to eliminate. `MergeInOrder` (shard.go) is the sibling that does this correctly: it is
**deliberately strict** — it detects duplicate/out-of-range shards and returns a registry-sourced
`*errs.E` rather than silently producing a plausible-but-WRONG result. This fix makes the three
permissive sites mirror that strictness.

**Sites:**
1. `internal/foundation/det/barrier.go` — `ApplyBarrier`: canonical key is `(Shard, Sequence)`;
   two messages with the SAME pair sort into submission order (unstable) → nondeterministic apply
   order. A shard emitting MULTIPLE messages with DIFFERENT sequences is legal and expected; only
   a duplicate `(Shard, Sequence)` is a contract violation.
2. `internal/foundation/det/sum.go` — `SumInShardOrder`: sorts by `Shard` only; two `ShardFloat`
   with the same `Shard` tie → unstable order → different sums (float64 add is non-associative).
   **No production callers exist** (grep-verified — dead except tests), so making it strict is
   zero-risk to callers.
3. `internal/engine/core/phase.go` — `runPhaseForHookFast` (BUG-269 single-shard fast path),
   line ~326: `sort.Slice(effects, ... Sequence < Sequence)` — the (Shard,Sequence) sort
   degenerated to Sequence-only for one shard; two effects with the same `Sequence` tie the same
   way. This path MUST stay byte-identical to `ApplyBarrier` (phase_test.go:450 asserts exactly
   that), so it must reject the same duplicate class ApplyBarrier now rejects.

## Design (authoritative — mirrors MergeInOrder; I have decided the API shape, do not re-litigate)

### AC-1 — ApplyBarrier becomes strict
New signature: `func ApplyBarrier[T any](correlationID string, messages []Message[T], apply func(T)) error`.
Behaviour, in this order:
- Range-check each `Shard` ∈ `[0, NumShards)`; on violation return `errs.New(ErrShardOutOfRange,
  correlationID, {"shard": m.Shard})` (reuse the existing MET-F200 code — same semantic).
- Detect a duplicate `(Shard, Sequence)` pair (e.g. a `map[[2]int]struct{}` or an adjacent-equal
  check after the canonical sort). On the FIRST duplicate return `errs.New(ErrBarrierDuplicate,
  correlationID, {"shard": s, "sequence": q})` — a NEW registry code (see AC-5).
- Otherwise sort by canonical `(Shard, Sequence)` ascending (keep the existing comparator) and
  apply, then return `nil`.
Do NOT apply ANY message if an error is detected (fail before the first `apply` call — a partial
application is exactly the plausible-but-wrong state MergeInOrder refuses). This means running the
validation pass BEFORE the apply loop.

### AC-2 — SumInShardOrder becomes strict
New signature: `func SumInShardOrder(correlationID string, values []ShardFloat) (float64, error)`.
- Range-check each `Shard` ∈ `[0, NumShards)` → `ErrShardOutOfRange` (MET-F200).
- Detect duplicate `Shard` → `errs.New(ErrShardDuplicate, correlationID, {"shard": s})` (reuse the
  existing MET-F202 code — identical semantic to MergeInOrder's duplicate-shard).
- Otherwise sort by `Shard` ascending and sum, return `(sum, nil)`.
(One float contribution per shard is the faithful reading of "fixed shard order" summation; a
shard needing multiple contributions must pre-aggregate before calling.)

### AC-3 — runPhaseForHookFast rejects the same duplicate class
The inline `sort.Slice(effects, ...Sequence...)` path must reject two effects sharing a `Sequence`
(the single-shard degenerate of ApplyBarrier's duplicate rule). `runPhaseForHookFast` already
returns `error`, so return `errs.New(ErrBarrierDuplicate, correlationID, {"shard": 0, "sequence":
q})` on a duplicate Sequence, BEFORE applying any effect. Keep the existing sort for the valid
(all-distinct) case. This preserves the phase_test.go:450 "fast path matches ApplyBarrier's
canonical order" guarantee AND extends it to the duplicate case (both now error instead of
diverging).

### AC-4 — the one caller updated
`internal/foundation/integration/executor.go:60` `det.ApplyBarrier(messages, in.ApplyMessage)` →
`if err := det.ApplyBarrier(correlationID, messages, in.ApplyMessage); err != nil { return
in.Zero(), err }` — mirror the MergeInOrder propagation immediately above it (the comment there
about propagating a det registry `*errs.E` unchanged applies verbatim). `correlationID` is already
in scope. No other production caller exists for either function (grep-verify and state so).

### AC-5 — the one new error code
Add exactly ONE new code `ErrBarrierDuplicate = "MET-F203"` in `internal/foundation/det/errors.go`
(MET-F203 is free — grep-verified), doc-commented in the ErrShardDuplicate style ("ApplyBarrier /
the single-shard fast path was handed two messages with the same (Shard, Sequence)"). Register it
through the SANCTIONED flow only: `node tools/plan/add-error.js` claim-range → add → check (BUG-309
overlap-slip: verify no collision after adding). Do NOT hand-edit errors.json. Reuse MET-F200 /
MET-F202 as specified — no other new codes.

### AC-6 — tests (the teeth — each MUST prove-can-fail)
For every new test, first confirm it goes RED against the OLD (unfixed) behaviour by scratch-mutation.
- **barrier_test.go**: (a) two `Message` with identical `(Shard, Sequence)`, different payloads,
  fed in BOTH slice orders → returns `ErrBarrierDuplicate`, applies NOTHING (record apply order via
  a closure; assert it stays empty). Prove the OLD code applied them in submission order (order-
  dependent) — i.e. the test must fail if strictness is removed. (b) out-of-range shard →
  `ErrShardOutOfRange`. (c) VALID: distinct `(Shard, Sequence)` in scrambled input → deterministic
  canonical apply order, `nil` error (regression-guards the happy path still works).
- **sum_test.go**: (a) duplicate `Shard` → `ErrShardDuplicate`. (b) out-of-range → `ErrShardOutOfRange`.
  (c) VALID distinct shards scrambled → deterministic sum, `nil`. Include a case where the OLD unstable
  order would have produced a DIFFERENT float sum (two same-shard values whose add-order matters), now
  rejected.
- **phase_test.go**: a SingleShardHook emitting two effects with the SAME `Sequence` → the fast path
  returns `ErrBarrierDuplicate` and applies nothing. Confirm the existing "fast path matches
  ApplyBarrier" test (line ~450) still passes for the distinct case.
- Assert error identity via the registry code (`errs.Code(err) == ErrBarrierDuplicate` or the
  package's existing assertion helper — match how sum/barrier/shard tests already inspect codes),
  never by string-matching the message.

## Gates (exactly as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`,
`go test ./internal/foundation/det/... ./internal/foundation/integration/... ./internal/engine/core/... -race -count=2`,
FULL `go test ./...`, `golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- Mirror MergeInOrder's strictness precisely: validate FULLY before producing ANY output; a partial
  apply/sum on an invalid input is a FAIL.
- Exactly one new error code (MET-F203); reuse MET-F200/MET-F202 as specified; sanctioned add-error
  flow only.
- Do NOT edit master-plan-v2.1.json / code.json. If astgate or a registered `foundation.det`
  contract signature complains about the changed function signatures, STOP and report to Bev (the
  lead/architect owns the registration; changing a signature inside the existing integration→det
  edge should not add an edge, but a contract signature entry may need a lead-side regen).
- Every test prove-can-fail (RED against old behaviour, GREEN after). No vacuous tests (BUG-230).
- Independent Destructive round (attacker ≠ author, GR#23) before commit: attack the validate-before-
  apply ordering (can a duplicate slip through if apply runs first?), the prove-can-fail teeth, the
  fast-path/ApplyBarrier alignment (do they still agree on a scrambled VALID input?), and the
  error-code identity.
