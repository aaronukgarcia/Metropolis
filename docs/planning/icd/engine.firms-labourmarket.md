# ICD: Firms Labour-Market Aggregate (Vacancy vs Workforce)

> **What this is:** an Interface Control Document for the `engine.firms` vacancy-vs-workforce aggregate query — the surface that makes FEAT-167's `JobAvailability` migration term computable from real firm + citizen state instead of the flat `baselineOneTermValue = 50.0` placeholder documented in `docs/planning/icd/engine.attract-terms.md` §3. Authored against `docs/planning/icd/TEMPLATE.md` (Integration Engine proposal §2/§5/§7/§8). A dev builds the aggregate against this document, not against a conversation.

---

## 1. Identity

- **GUID:** `df06bec1-ea6c-456d-9d06-690cb18fa2a0` — `engine.firms`' own registered `code.json` module GUID. Standing in as this surface's identity GUID: the aggregate is a set of new query methods ON the already-registered `FirmsAPI` (inbound guid `f6c47094-fae7-4d93-8267-653b15cc1a2a`), not a new module, so the module's own GUID is the correct identity.
- **Name:** `firms.labourmarket.vacancy`
- **Owning module (mkey):** `engine.firms`
- **code.json edge ref(s):**
  - **Already registered (reused, no action needed):** `engine.firms → engine.citizens` (edge guid `fd65035a-1204-45e0-bdb7-5b9196c77b4f`, `CitizensAPI` inbound guid `35078084-e4a5-4c34-b9ee-6b0352b76fe3`) — the workforce side reads `CitizensAPI.TotalPopulation` over this already-wired edge (via `SetCitizens`).
  - **NOT YET REGISTERED — pending GR#25 registration before the aggregate may be cited as live input to migration:** `feat.compositionroot → engine.firms` (`engine.firms`'s inbound `consumers` are comms/fdi/freight/pharmacampus only; `feat.compositionroot`'s outbound `calls` omit `engine.firms`). This edge is required for the composition root to read `LabourMarket()` back out and feed the `JobAvailability` term. The aggregate itself is fully buildable and testable in isolation before this edge lands.

---

## 2. Purpose

Today no module can answer "how many jobs are open, and how does that compare to the people who could fill them?" — `FirmsAPI` exposes lifecycle/credit/founding queries but no vacancy or labour-demand-vs-workforce figure, `CitizensAPI` exposes no unemployment/working-age aggregate, and `MarketAPI` has no labour concept at all (FEAT-167 discovery). This surface adds the missing signal in `engine.firms`: total vacancies (Σ each firm's headroom to its stage band), workforce (from `CitizensAPI.TotalPopulation`), and the `VacancyRatePerMille` ratio that migration maps onto `JobAvailability`.

---

## 3. Inputs

| Source | Shard-state read | Type |
|---|---|---|
| `engine.firms` (own `firms map[FirmID]*firmState`) | Each firm's `Stage` and `Staff []uint64` roster length, plus the `data/firms.json` stage `minStaff` floors the band ceilings derive from (read-only under `mu.RLock`) | internal `Firm`/`stageConfig` state (never mutated by the aggregate) |
| `engine.citizens` (`CitizensAPI`) | `TotalPopulation(correlationID)` — the labour-supply side | `int` (already-wired registered edge) |

The vacancy side is computed **inside** `engine.firms` from its own roster state and its own data file (GR#15: band ceilings derive from `data/firms.json`, never Go literals). The workforce side is the single already-registered citizens read. `engine.market` is **not** consumed by this surface — market has no labour concept, so the "+market" qualifier is intentionally not exercised (see §12).

---

## 4. Outputs

| Effect | Target consumer / edge | Type |
|---|---|---|
| `TotalVacancies() int64` | read by the composition root (edge pending, §1) | Σ `max(0, bandCeiling(stage) − len(Staff))` |
| `LabourMarket()` (`TotalVacancies`, `Workforce`, `VacancyRatePerMille`) | read by the composition root to derive `JobAvailability` (edge pending, §1) | value struct |

`VacancyRatePerMille = 0` when `Workforce == 0`, else `clamp(vacancies × 1000 / workforce)` — integer arithmetic, division-by-zero guarded, never NaN/Inf. No output mutates any conserved stock: this is a dimensionless query result, not a resource/money/population transfer (no `invariant` accumulator is fed).

---

## 5. Update Class

**T1.** Job availability is a migration-response *input*, not the conservation-tracked population delta — the same reasoning `docs/planning/icd/engine.attract-terms.md` §5 applies to the five attract terms. The aggregate has no tick of its own; the caller samples it once per month (`attractHook` cadence) or on demand. A value one tick late violates no invariant, so it is batchable/coalescible, never queued past the same month's use.

---

## 6. Shard Scope

**Single-shard (shard 0), matching the eventual consumer.** The aggregate is a whole-city read over a single `FirmsAPI` with no per-shard structure of its own; the consumer that drives it (the composition root's `attractHook`) is already `SingleShard() == true` (shard 0 only). When the `LabourMarket()` read is wired there, it inherits that contract — one call per month, not once per shard.

---

## 7. Determinism Guarantee

Firms are iterated in **ascending `FirmID` order** (never Go map iteration order over the `firms` map on the aggregate path), and `VacancyRatePerMille` is a pure integer fold of `(vacancies, workforce)` in fixed source order (GR#21). `Workforce` is `CitizensAPI.TotalPopulation`, itself deterministic (AC-17 `PopulationHash`-verified). **No wall-clock time is read anywhere in this surface** — every value derives from sim state (firm rosters, citizens population), never `time.Now()`.

---

## 8. Error / Registry Codes

`engine.firms` owns `MET-G1400`–`MET-G1499` (`errors.go`). Codes this surface can surface: `MET-G1409` (`ErrDependencyMissing`) if `LabourMarket()` is called before `SetCitizens` — never a zero `Workforce` silently read as "no jobs" (GR#17); `MET-G1403` (`ErrCopiedValue`) if called on a struct-copied `*FirmsAPI`.

---

## 9. Resilience Behaviour

In-process, always-connected — the degenerate `integration.LocalReconnectHooks` case. Both inputs are pure reads (firm roster state + a citizens aggregate), deterministic given identical inputs, so a bare retry fails identically and the correct "retry" is "fix the caller bug," not a backoff loop. No queue, no remote dispatch (T1, in-process). A crash is recovered by the existing checkpoint/replay path; the aggregate recomputes from roster + population each call and holds no recoverable state of its own.

---

## 10. Monitoring Signals

**Status:** derived from whether `LabourMarket()` returns an error this month (up) vs propagates one (degraded, logged per GR#1/GR#17). **Throughput/liveness:** `TotalVacancies`/`VacancyRatePerMille` are the natural "did the economy open jobs" signal — pipe them alongside `netMigration` in the composition root's read-only accessor surface (mirroring the attract-terms ICD §10). **Queue depth:** n/a (T1, no queue). **Peak load:** the fold is linear in registered firms, watched against the BUG-034 perf gate.

---

## 11. Required Tests

- **Vacancy formula:** a Startup with 1 staff and a Medium with 10 staff yield `TotalVacancies` equal to the headroom computed from the **loaded** `data/firms.json` floors — and mutating a floor in the fixture changes the result with no code change (must be able to FAIL — a hardcoded `Σ(5−len)` build fails the mutation).
- **Workforce live query:** wiring a `CitizensAPI` with known population makes `LabourMarket().Workforce` equal it; raising population tracks the field.
- **Ratio directionality:** adding a firm with vacancies (workforce held) raises `VacancyRatePerMille`; raising workforce (vacancies held) lowers it.
- **Division-by-zero guard:** `Workforce == 0` ⇒ `VacancyRatePerMille == 0`, no NaN/Inf.
- **Unwired dependency:** `LabourMarket()` before `SetCitizens` returns `MET-G1409`, not a zero-value return.
- **Determinism equivalence:** identical construction ⇒ byte-identical `TotalVacancies`/`LabourMarket` across worker counts.
- **AC-coverage:** `docs/planning/acceptance/engine.firms.md` AC-21…AC-27 are spec-lint-clean for this surface.

---

## 12. Change Control

Additive-only: a later revision may ADD a field to `LabourMarket` (e.g. an `Unemployed` denominator once `CitizensAPI` grows an unemployment aggregate) without a version bump provided no existing field's type or semantics change. Any REMOVAL or semantic change to the `VacancyRatePerMille` formula or the band-ceiling derivation requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected code.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **Consumer edge gap (GR#25):** `feat.compositionroot → engine.firms` does not exist in `code.json`; it must be registered before the composition root may read `LabourMarket()` to drive `JobAvailability` (§1). The aggregate itself is buildable and testable in isolation first.
2. **Workforce proxy:** `Workforce = TotalPopulation()` is a labour-*supply proxy* — `CitizensAPI` exposes no working-age/unemployment aggregate, so the ratio is vacancies-per-resident, not vacancies-per-seeker. Refinable later by a citizens-side unemployment/working-age aggregate (a follow-up BOW item, not invented here).
3. **Enterprise ceiling:** §45 gives Enterprise no upper band ("250+"), so `bandCeiling(Enterprise)` needs either a data-declared ceiling (new `data/firms.json` field) or a documented "unbounded → 0 vacancies" rule; AC-21 requires the ceiling be data-sourced either way (balance-number regime).
4. **"+market" is not exercised:** `engine.market` has no labour concept, so this surface uses only firms (vacancies) + citizens (workforce); the existing `engine.firms → engine.market` edge is untouched by it.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-19 | Initial ICD — vacancy-vs-workforce labour-market aggregate for FEAT-167 `JobAvailability` (no new outbound edge; workforce proxy + Enterprise ceiling flagged). |
