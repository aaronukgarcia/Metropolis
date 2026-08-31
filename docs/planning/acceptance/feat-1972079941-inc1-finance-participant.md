# FEAT-1972079941 inc1 — finance save.Participant (serialization pilot)

**Feature:** per-module state+RNG serialization — the foundation for real game save/load, durable
snapshots (unblocks the deferred FEAT-1972079936 Phase-1 inc3), and engine-authoritative
convergence (Phase 3). Today `save.DefaultParticipants = []` is EMPTY; no module serializes.
**inc1** = implement + prove the pattern on ONE clean module: **finance** (RNG-free, already
covered by `compose.StateDigest`).

## The pivotal finding (de-risks the whole epic)
The engine RNG (`internal/foundation/det` Philox `det.Stream`) is **STATELESS**: every consumer
builds a fresh `det.NewStream(worldSeed, entityID, month, purpose)`, draws, and discards — there
is **no mutable RNG cursor to serialize**. The reproducible-future inputs are (worldSeed, month)
[already in the save-bundle header] + entityID [in the records]. So per-module serialization is
**DATA-ONLY**. (finance has no RNG at all — confirmed no `foundation/det` import.)

## The contract (from `internal/engine/save/participant.go:23-37`)
A `save.Participant` implements: `Kind() string` (stable unique shard label), `Source()
serialize.RecordSource` (fresh pull-iterator OUT, one `Record{Kind,Data}` at a time, `ok=false`
at exhaustion), `Handler() serialize.RecordHandler` (fresh sink IN, one record at a time). Data
is opaque JSON written verbatim — byte-determinism depends on emitting stable bytes. Worked
reference: `internal/engine/save/fixture_test.go` (widget/gadget participants + the field-parity
drift test).

## Design (authoritative) — all in `internal/engine/finance/` (edge: engine.finance→int.serializer, registered a6293cb)

### AC-1 — finance implements save.Participant (in-package)
A `financeParticipant` (or `FinanceAPI` itself) implementing `Kind()="finance"`, `Source()`,
`Handler()`. `Source()` snapshots the live finance state under the finance lock at call time,
then yields records; `Handler()` rebuilds state under the lock. Serialize the **FULL ledger** so
`RecomputeMoneyStock` reconciles post-load (AC-5): `accounts` (id→role+balance), the `role` map,
the append-only `txns` + `tickTxns`, the id counters (`nextTxID/nextLoanID/nextFirmID/nextInvestID`),
`moneyStock/openingStock/trackedDelta`, `month`, `creditLines`+`totalCreditLine`, `loans`+
`totalDebt`+`missedPayments`, `insolvencyMonths`/`gameOver`, `firms`, `investments`. (Immutable
config loaded from data files is NOT serialized.)

### AC-2 — domain↔wire projections + field-parity drift test (MANDATORY)
Each serialized domain type gets an explicit wire projection with `json` tags (never marshal the
domain struct directly — mirror `widgetWire`). Add the reflective **field-parity drift test**
(mirror `TestWidgetWireFieldsMatchWidget`): every exported field of each serialized domain type
has a wire counterpart, so a future field addition that isn't serialized fails the build. This is
the `participant.go:50-53` obligation every real Participant's package inherits.

### AC-3 — determinism of the emitted bytes (GR#21)
`Source()` must emit records in a DETERMINISTIC order — map-backed state (accounts, creditLines,
loans, firms, investments) iterated in SORTED-key order, never raw map range. Two saves of the
same state produce byte-identical shards (mirror `save/determinism_test.go`
`TestSaveManual_ByteDeterminism` for the finance participant).

### AC-4 — streaming (no full-buffer)
`Source`/`Handler` process one record at a time (the append-only txns log can be large) — do not
materialize the whole record set before the first yield, nor buffer a whole shard on load.

### AC-5 — the round-trip determinism test (the bar)
Mirror `compose/persistjournal_test.go` `TestPersistJournal_RoundTripDeterministic`, at the
finance+save seam: (a) drive a `FinanceAPI` through a deterministic sequence (wages/tax/opex/loan
postings over several months) → capture `pre := digest`; (b) `save.NewManager(root,
[]Participant{financeParticipant}, cid).SaveManual(...)`; (c) `Load` into a FRESH `FinanceAPI`;
(d) assert the reloaded finance's state == pre (compare via `compose.StateDigest` if the test can
build a minimal composition, OR via finance's own accessors: every `AccountBalance`, the
`MoneyStock` triple, `RecomputeMoneyStock`, `Lines`); (e) continue identical postings on both and
assert they stay equal. **RecomputeMoneyStock must reconcile post-load** (proves the txns log
round-tripped). Prove-can-fail: mutate one reloaded account/txn → the comparison diverges.

### AC-6 — do NOT wire into live DefaultParticipants yet
inc1 proves the Participant standalone (the test constructs the Manager with the finance
participant directly). Registering all modules' participants into `save.DefaultParticipants` /
the composition root's live save flow is a LATER increment (it would add a save→finance edge);
keep inc1 to the single registered `engine.finance→int.serializer` edge. Do NOT edit
master-plan/code.json (the edge is already registered).

## Gates (as CI runs them)
`gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./internal/engine/finance/... ./internal/engine/save/... ./internal/foundation/serialize/... -race -count=2`, FULL `go test ./...`, `golangci-lint run ./...` @ v2.5.0, astgate `TestRun_LiveTree` green.

## Non-negotiables
- Serialization is DATA-ONLY (no RNG cursor — confirm finance has no det.Stream). If ANY
  long-lived `*det.Stream` with a live counter is found in finance, STOP and report (it would
  break the data-only premise).
- The field-parity drift test is mandatory (GR#3/AC-2).
- Deterministic sorted-key emission (GR#21); byte-identical re-save.
- RecomputeMoneyStock reconciles post-load.
- Only the registered `engine.finance→int.serializer` edge; no master-plan/code.json edits, no
  save→finance edge (no live DefaultParticipants wiring this inc).
- Independent Destructive round (attacker ≠ author) before commit (GR#23): attack byte-determinism
  (map order), the field-parity test's teeth, round-trip losslessness incl. the txns log +
  RecomputeMoneyStock reconciliation, and a prove-can-fail mutation.
