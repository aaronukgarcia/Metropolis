# ASM-1286 — close the SEC-160/136 residual in freight ContainerPort

**Origin:** ASM-1286 (from chemicals FEAT-084 fold). The chemicals module CLOSED the SEC-160 copy-
guard class locally; the same shape was left unfixed in `internal/engine/freight/containerport.go`
and flagged for a separate freight dispatch. **Partial-stale check (Bev, 2026-09-01):** the three
`Wire*` methods and `Tiers()` DO now call `checkNotCopied()` — but they **swallow** its error: the
`Wire*` methods are `void` (no-op on a copied port) and `Tiers()` returns a bare `nil` sentinel.
That is exactly the SEC-136 swallow the ASM names — the guard fires but the caller can never see it.

**The canonical convention (mirror it EXACTLY):** every other module's wired API surfaces the copy
guard by returning an error — `internal/engine/chemicals/refinery.go` (the reference that closed the
class), `airport/airport.go`, `census/census.go`. refinery's `WireFreight/WirePermit/... ` are
`func(...) error`; its query methods `Chem()/Facility()/Facilities()` return `(..., error)` and the
doc explicitly states a struct-copied value is "rejected ... rather than returning a plausible empty
slice (SEC-136 sentinel class)". ContainerPort must match this convention.

## Scope (authoritative)
`internal/engine/freight/containerport.go` + its test files ONLY. ContainerPort has **no production
caller** of its Wire* methods (grep-verified: every non-test caller is a freight `_test.go`), so the
signature changes touch only freight tests — low blast radius.

### AC-1 — Wire* methods surface the guard
`WireRail`, `WirePermit`, `WireDecommission`: change from `void` to `error`. On a copied port return
`c.checkNotCopied("WireRail")`'s error (i.e. `ErrCopiedValue`); otherwise take the lock, assign, and
return `nil`. Mirror refinery.go:349-408 line-for-line (guard → lock → assign → `return nil`).

### AC-2 — every guarded QUERY method surfaces the guard (no nil/zero sentinel)
Audit EVERY exported ContainerPort method that currently calls `checkNotCopied` and returns a bare
value on failure (start with `Tiers()`; also inspect each RLock/Lock method — the ones at approx
lines 196/210/238/265/279/314/331/375/394). For each, change the signature to add a trailing
`error` and return the guard error instead of the nil/zero sentinel, mirroring refinery's
`Facilities() ([]FacilityProfile, error)` / `Facility() (FacilityProfile, error)`. If a method
already returns an `error`, just ensure it returns the guard error (many may already be correct —
only the swallowers change). List every method you changed and every one you left (with why).

### AC-3 — callers updated
Update all freight `_test.go` call sites to handle the new returns (`if err := cp.WireRail(r); err
!= nil { t.Fatalf(...) }`, and destructure the query-method error). No production caller exists;
confirm that by grep and state it. Do NOT introduce a production wiring in this item.

### AC-4 — the guard is now OBSERVABLE (the teeth — prove-can-fail)
Add/extend a security test (mirror `chemicals/security_test.go`) proving that a **struct-copied**
ContainerPort's `WireRail`/`WirePermit`/`WireDecommission` and each newly-erroring query method
return `ErrCopiedValue` (assert by registry code via `errors.Is`, not string). This is the whole
point: before this fix the error was unobservable (swallowed); the test must go RED if the method
reverts to swallowing (delete the `return err` → the copied-port test sees `nil`). Also keep a
positive test: a normal (non-copied) port wires + queries with `nil` error.

### AC-5 — contract/registry
`feat.containerport` appears in code.json (4 refs). Determine whether code.json records METHOD
SIGNATURES for it or only edges/GUIDs (it almost certainly records only edges — astgate checks the
import graph + structural rules, not return types). If astgate `TestRun_LiveTree` stays green with
the signature changes, nothing in master-plan/code.json needs editing — say so. If astgate or a
registered signature DOES complain, STOP and report the exact contract entry to Bev (lead/architect
owns master-plan regen). Do NOT edit master-plan-v2.1.json or code.json yourself.

## Gates (as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`,
`go test ./internal/engine/freight/... -race -count=2`, FULL `go test ./...`,
`golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- Mirror the refinery convention exactly (error-returning Wire*, error-returning guarded queries,
  no nil/zero sentinel on copy). Consistency with the module that closed the class is the bar.
- No production caller may be invented; freight tests are the only call sites to update.
- Every new/changed security test prove-can-fail (RED if the guard is re-swallowed); assert by
  registry code, never message string. No vacuous tests (BUG-230).
- No master-plan/code.json edits — report if astgate or a contract complains.
- Independent Destructive round (attacker ≠ author, GR#23) before commit: attack whether ANY guarded
  method still swallows (returns a plausible zero/nil on a copied port), the prove-can-fail teeth,
  and full gates rerun independently.
