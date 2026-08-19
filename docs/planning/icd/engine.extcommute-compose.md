# ICD: engine.extcommute → Composition Root (seam adapters)

> Interface Control Document per `docs/planning/icd/TEMPLATE.md`. THIN ICD (FEAT-207): the seams and the off-map employment mirror already exist — this documents the composition-root adapters that map the real `CitizensSeam`/`TrafficSeam`/`FinanceSeam` onto the real `engine.citizens`/`engine.traffic`/`engine.finance` APIs, and the identity EmploymentState map. Authored BEFORE Bev builds the wiring.

---

## 1. Identity

- **GUID:** `39bb5039-2f59-4e32-9cb5-d7f899a05d3a` *(`engine.extcommute`'s own module GUID — the dedicated `feat.compositionroot → engine.extcommute` edge is NOT yet registered in code.json; this stands in until GR#25 registration lands, see §12 Open Decision 1)*
- **Name:** `extcommute.compose.adapters`
- **Owning module (mkey):** `engine.extcommute`
- **code.json edge ref(s):** `engine.extcommute`'s own outbound edges are registered and live: `engine.citizens` (`35078084-e4a5-4c34-b9ee-6b0352b76fe3`), `engine.traffic` (`df65414a-b7cd-4003-b25d-0772bf9049d5`), `engine.finance` (`d6a475f0-a86c-46db-883e-a48effe4205b`), `foundation.data` (`8c6bb3b0-a8a9-444b-9c93-86250b3d0a41`). The **`feat.compositionroot → engine.extcommute`** edge is NONE YET — `feat.compositionroot`'s `outbound.calls[]` does not list `engine.extcommute`; the composition root cannot construct/adapt it until that edge is registered (GR#25).

---

## 2. Purpose

`engine.extcommute` (MOD-035, the module itself ACCEPTED) consumes `engine.citizens`/`engine.traffic`/`engine.finance` exclusively through the contract-first `CitizensSeam`/`TrafficSeam`/`FinanceSeam` interfaces, wired by the composition root via `SetCitizensSeam`/`SetTrafficSeam`/`SetFinanceSeam` — proven only against package-local test stubs. No composition-root adapter exists, so `extcommute`'s assign/release employment flip, transport cap, and fiscal postings cannot run against the real modules, and `engine.extcommute.md` AC-6/AC-7 (the dormitory-arithmetic identity) stay stub-verifiable. This integration builds the three adapters in `compose.Wire`, wires the world seed, and lands the end-to-end proof that an off-map assign moves the citizen's own bucket without changing conservation.

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `engine.extcommute` (`ExtCommuteAPI`) | Constructed via `extcommute.Load`/`LoadDefault`; its three seams are dependency-inversion interfaces it calls, not state it reads from compose | `*extcommute.ExtCommuteAPI` (opaque; wired with `SetCitizensSeam`/`SetTrafficSeam`/`SetFinanceSeam` + `SetSeed`) |
| `engine.citizens` (`CitizensAPI`) | `CitizenAt(id, cid) (Citizen, bool)` for `CitizenExists`; `TotalPopulation(cid)` for the seam's population probe; `ApplyLifeEventCommand(LifeEventCommand{Kind: LifeEventEmployment, ...})` for the employment write | `*citizens.CitizensAPI` |
| `engine.traffic` (`TrafficAPI`) | No congestion method exists (see §12 Open Decision 2) — the `TrafficSeam.Congestion(channel)` adapter is a shape mismatch, not a direct read | `*traffic.TrafficAPI` |
| `engine.finance` (`FinanceAPI`) | `Post(Transaction{Entries: []Entry{Account, Side, Amount, Category}})` + `OpenAccount` + `LinesByCategory` — no verb-matching single-call surface (see §12 Open Decision 3) | `*finance.FinanceAPI` |

The **identity map** is the core of this integration: `extcommute.EmploymentState` is a `uint8` whose constants are numerically **equal** to `citizens.EmploymentState` (`EmploymentUnemployed = 3`, `EmploymentOffMap = 5`, plus `None/Student/Employed/Retired = 0/1/2/4` — `extcommute/types.go:85-94`). The adapter's employment write is therefore a direct numeric cast: `citizens.EmploymentState(state)` is the identity function, no translation table.

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| `CitizensSeam.ApplyLifeEventEmployment(id, state)` → citizen's `Employment.State` flips to `EmploymentOffMap` on assign / `EmploymentUnemployed` on release (sector always `SectorNone`) | `citizens`' hot/cold record, via the existing `ApplyLifeEventCommand(LifeEventEmployment)` path — no new `CitizensAPI` method (ICD `engine.citizens-offmap.md` §4) | `citizens.Employment{State, Sector}` |
| `FinanceSeam.RecordOffMapWage`/`RemoveOffMapWage`/`RecordWageLeakage` → finance double-entry postings (income-tax-eligible off-map wage; wage-leakage category) | `finance` ledger (`Post(Transaction)`), tagged with `Category` strings | `finance.Transaction` |
| `TrafficSeam.Congestion(channel)` → per-channel congestion fraction fed into `extcommute`'s transport cap | `extcommute`'s own `transportAvailable` second-cap check | `float64` (in `[0,1]`) |
| **No conservation-accumulator effect.** An off-map assign is a coarse-state relabel of a still-resident citizen; `simState.peopleDelta`/`moneyDelta` need no new term (ICD `engine.citizens-offmap.md` §4 "no conservation-accumulator effect" ruling) | n/a | n/a |

---

## 5. Update Class

**T1** — employment-state flips are command-driven (`Assign`/`Release`/`InCommute`, player-triggered, not a per-tick pass), and they have no conservation-accumulator effect (§4). There is no "must land this exact tick" requirement; both mutations happen synchronously inside the same command call.

---

## 6. Shard Scope

**Single-citizen, not shard-scoped.** `ApplyLifeEventCommand(LifeEventEmployment)` operates on exactly one `CitizenID` per call (`citizens/registry.go:414-430`, routed through citizens' own `det.ShardForEntity`). `extcommute`'s commands carry a single `CitizenID` (`AssignCommand`/`ReleaseCommand`). There is no batch/all-shards path; the cold-pass shard fan-out is untouched.

---

## 7. Determinism Guarantee

The adapter introduces no new stochastic draw. The employment write is an explicit command with a pure numeric cast (`citizens.EmploymentState(state)`), and `extcommute`'s own `SelectPool` tie-break uses `det.NewStream(worldSeed, 0, month, "extcommute.select")` (counter-based, seeded) — deterministic given `(worldSeed, tick, command log)`. `SetSeed` must be wired to the engine's world seed at `Wire` time so `SelectPool` is reproducible. The compose adapter's own seam methods are pure function closures with no map iteration or shared mutable state. **No wall-clock time is read anywhere in this integration** — every mutation is command-driven, and the seams carry no time-of-day input.

---

## 8. Error / Registry Codes

- **`engine.extcommute`** owns `MET-G4800`–`MET-G4899`: `ErrDependencyNotWired` (`MET-G4811`) is the fail-closed result of any nil seam; `ErrPoolFull`/`ErrTransportCapacity`/`ErrAlreadyOffMap`/`ErrUnknownCitizen`/`ErrNotOffMapAssigned`/`ErrNoEligiblePool`/`ErrInvalidInput`/`ErrCopiedValue`/`ErrUnknownPool`/`ErrInvalidEra` cover the command rejection surface (see `extcommute/errors.go`).
- **`engine.citizens`** surfaces `MET-G007` (`ErrFieldOutOfRange`) for an out-of-domain employment value, and `MET-G004` (`ErrAPICopied`) on a copied handle — both already handled by the adapter's call target.
- **`engine.finance`** surfaces its own ledger codes on a rejected `Post` (overdraft / unbalanced transaction) — the adapter propagates them wrapped under `ErrDependencyNotWired`.
- **`ErrModuleFailed`** (compose's own code) for a construction failure of `extcommute.Load`/`LoadDefault` in `Wire`.

---

## 9. Resilience Behaviour

In-process, always-connected. `extcommute` fails closed on a nil seam (`ErrDependencyNotWired` — GR#17/GR#20), never silently skips a cap or an employment write. `Assign` already implements the compensating-rollback discipline (post wage → flip employment → commit map; on citizens failure, `RemoveOffMapWage` rolls the wage back, and on rollback failure both errors are joined — `extcommute.go:398-427`), so a seam failure mid-assign leaves no store changed. No retry/backoff: every failure is deterministic given the same inputs; the correct remedy is a caller/seam bug fix. Catch-up: none — crash recovery is the existing checkpoint/replay path.

---

## 10. Monitoring Signals

**Status:** the three seams' nil-wiring is observable at construction (a nil seam + a command that needs it → `ErrDependencyNotWired`), not silent. **Cross-check signal:** `count(citizens with EmploymentOffMap) == Σ_pool extcommute.FilledSlots(pool)` — the AC-6/AC-7 identity's cross-module consistency check, surfaced as a test (§11) and available to a future audit. **Throughput:** `FilledSlots`/`Assignment` remain the authoritative pool-occupancy signal (single source of truth, GR#3).

---

## 11. Required Tests

- **End-to-end unblock (the FEAT-207 deliverable):** a compose-level test that constructs the real modules, wires the adapters, issues an `Assign`, and asserts (a) the citizen's own `Employment.State` moves to `EmploymentOffMap`, (b) `TotalPopulation`/`PopulationHash` conservation is unchanged apart from the state byte, and (c) the transport cap (`TrafficSeam`) was consulted and the fiscal posting (`FinanceSeam`) was recorded.
- **Identity-map conformance:** a test asserting `extcommute.EmploymentState` and `citizens.EmploymentState` agree on every constant value (0..5), so the numeric cast can never silently mislabel — the guard against a future renumbering on either side.
- **Fail-closed seams:** a test driving `Assign`/`Release` with each seam nil and asserting `ErrDependencyNotWired` with the naming `dependency` — never a silent skip.
- **AC-coverage:** the FEAT-207 acceptance criteria file must exist and pass `tools/plan/spec-lint.js` clean before the Go wiring is built.

---

## 12. Change Control

Additive-only: a later ICD revision may ADD an Input/Output without a version bump provided no existing field's type or semantics changes; any REMOVAL or semantic change to an existing Input/Output/Update-Class/Determinism guarantee requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected integration code.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **The `feat.compositionroot → engine.extcommute` code.json edge does not exist.** It must be registered (GR#25) before `compose.Wire` imports `internal/engine/extcommute`, and §1's GUID swapped to the real edge GUID once it lands. FEAT-207's own BOW description names this ("register any missing compositionroot->extcommute edge").
2. **`TrafficSeam.Congestion(channel)` has no matching `TrafficAPI` surface.** `extcommute/types.go` requires `Congestion(channel string) (float64, error)`, but `engine.traffic` exposes `LinkTravelTime`, `CommuteHours`, `AccessMinutes`, `CommuteMinutes`, `ActiveTravelShare`, `AddTripDemand`/`RegisterTrip`/`AddDemand`, `AdvanceTick`, `DailyAssignment` — **no `Congestion` method**. The adapter must either (a) return a documented free-flow placeholder (`0.0`) until traffic grows a per-channel congestion query, or (b) derive congestion from `LinkTravelTime`/`DailyAssignment` v/c — a build-time decision Bev must rule on, not left implicit. This is a seam-shape gap, not a missing edge (the `extcommute → traffic` edge is registered).
3. **`FinanceSeam`'s five verbs have no verb-matching `FinanceAPI` surface.** `extcommute/types.go` requires `RecordOffMapWage`/`RemoveOffMapWage`/`RecordBusinessRates`/`RecordCorpShare`/`RecordWageLeakage`, but `engine.finance` exposes a double-entry `Post(Transaction{Entries: []Entry{Account, Side, Amount, Category}})`, `OpenAccount`, `Lines`, `LinesByCategory`. The adapter must translate the semantic verbs into `Post` transactions with the right `Category` tags (income-tax-eligible wage vs wage-leakage), and `RecordBusinessRates`/`RecordCorpShare` must remain never-called proof stubs (AC-12). Another seam-shape gap, not a missing edge.
4. **Off-map assign/release command routing is out of scope.** This ICD wires the seams; it does not add `Assign`/`Release`/`InCommute` to the protocol command vocabulary or route them through `handleGameplay` — that is a later gameplay-surface item.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-19 | Initial ICD (FEAT-207) |
