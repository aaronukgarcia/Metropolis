# FEAT-services-convergence-inc1 — Services/Wellbeing Azure Convergence, Increment 1

**Status:** DRAFT for review. **Author:** BA (docs-only). **Date:** 2026-09-02.
**mkey:** `services.convergence` (no existing feature key registered yet — Architect to confirm/assign at build dispatch).

---

## 1. Context

**Northstar sequence (Aaron, Q100052=A1, 2026-09-02):** after finance, the
service / wellbeing / migration chain is the next domain to converge from the
webconsole's TS simulation onto the authoritative Go engine — "the visible
build → city responds loop". Standing instruction: "keep the eye on the prize
and fine grain on Azure" — this increment stays narrow and does not attempt
the domain flip.

**The finance precedent (FEAT-1972079941 / Phase-3), the recipe to mirror**
(`docs/planning/acceptance/feat-1972079941-inc1-finance-participant.md`,
`internal/engine/finance/participant.go`, `internal/converge/`,
`webconsole/test/converge-fixture-emit.mjs`):

1. The Go domain is authoritative and serializable — the domain type never
   marshals directly; an explicit `*Wire` struct (json tags) plus a
   reflective field-parity drift test keeps the wire shape honest, emitted
   with deterministic sorted-key ordering (GR#21) and streamed one record at
   a time.
2. The domain implements the structural `save.Participant` contract —
   `Kind()` / `Source()` / `Handler()` (`internal/engine/save/participant.go:23-37`)
   — via a `participant.go` in the domain package, without that package
   importing `internal/engine/save` (finance's is
   `internal/engine/finance/participant.go:544-624`). The
   `engine.finance → int.serializer` edge was registered in `code.json` as
   part of that work (present at `code.json:797-798`, `:1055-1056`,
   `:2599-2600`).
3. A parity fixture proves TS-sim vs Go-engine outputs reconcile at defined
   sample points: `internal/converge/` holds the AB harness
   (`compare.go`, `domain.go`, `finance_domain.go`, `fixture.go`,
   `tolerance.go`, `trajectory.go`) plus a canonical TS action list
   (`webconsole/test/converge-fixtures/converge-finance-actions.json`).
   `webconsole/test/converge-fixture-emit.mjs` runs the TS pure
   `initialState`/`reducer` over that action list and emits
   `internal/converge/testdata/finance-webconsole-v1.json`
   (`{domain, samples:[{tick, values}]}`). The fixture is **never
   hand-edited** — a test asserts the committed file byte-matches a fresh
   `emitFixture()` re-run; regeneration is only via an explicit `--write`
   flag. This is the same "no silent drift" discipline as `code.json`
   generation from the master plan.
4. Only once parity holds does the domain flip to being Go-backed in the
   running host. Finance has **not** flipped yet — `DefaultParticipants`
   (`internal/engine/save/participant.go:59`) is still empty; nothing is
   wired into the live save flow. The flip is a later increment.

**Why services is next:** it is the largest single lever on the "city
responds" half of the loop (coverage → wellbeing → migration attractiveness)
and the build→services bridge (907b603 + follow-ups) removed the last
structural blocker — services now receives real build-driven state instead
of being fed by a stub.

**Dependency already in flight — do not duplicate:** BOW `FEAT-2326609743`
("engine.services as a save participant (bridge root item)") is filed and
open (P3) to give `engine.services` its own `participant.go` mirroring
finance's. This spec's part (a) below references that item as its
dependency; it does not re-specify participant-contract internals.

---

## 2. Inc1 scope

**In scope:**

**(a) Services becomes a save participant — dependency, not this spec's build.**
Inc1 assumes `FEAT-2326609743` lands (or lands alongside, coordinated) giving
`internal/engine/services` a `participant.go` with `Kind()`/`Source()`/
`Handler()` and the `engine.services → int.serializer` edge in `code.json`,
mirroring finance's pattern. This spec's ACs for the parity fixture (below)
are **blocked on** that participant existing, because the fixture samples
serialized Go state, not live in-memory-only reads.

**(b) A services/wellbeing parity fixture + emitter, mirroring finance's.**
New `internal/converge/services_domain.go` (parity-harness `Domain`
implementation for services+wellbeing, alongside `finance_domain.go`) and a
new webconsole emitter script analogous to
`converge-fixture-emit.mjs`, sampling:
- TS `serviceCoverageOf(s)` (`webconsole/src/sim/data.ts:2161-2221`) — the 10
  coverage rows (nursery, primary, college, gp, hosp, police, fire,
  cleanwater, waste, power).
- TS `wellbeingOf(s)` (`webconsole/src/sim/engine.ts:3832`) — the composite
  wellbeing score that consumes `serviceCoverageOf` as its sole coverage
  input.
- Go `ServicesAPI.CoverageSummary()` / `CoverageByDistrict()`
  (`internal/engine/services/coverage.go:125,153`) under an equivalent
  scripted scenario (a canonical build/demand action list, analogous to
  `converge-finance-actions.json`), plus whatever `engine.wellbeing`
  currently exposes (`internal/engine/wellbeing/api.go:22`) as the Go-side
  wellbeing read.

**(c) The documented TS-concept → Go-concept mapping table** (Section 3) —
which coverage rows exist on both sides, which diverge and why, and which
divergences are being deliberately carried into inc1 rather than resolved.

**Explicitly NOT in inc1** (deferred to inc2+, gated on parity holding):
- The actual domain flip — services/wellbeing becoming Go-backed in the
  running webconsole host. Exactly as finance has not flipped yet, services
  won't either until this fixture is green and stays green.
- Resolving `BUG-523` (TS money model divergence from Go) — flagged as a
  precondition risk for finance-adjacent numbers feeding into service
  funding, not something this increment fixes.
- Resolving `BUG-519` (approval flow deaf to services/wellbeing) — a
  consumer-side wiring bug, orthogonal to convergence.
- Any new coverage kind, service kind, or wellbeing sub-score. Inc1 only
  instruments and compares what already exists on both sides.

---

## 3. TS-concept → Go-concept mapping table

TS `serviceCoverageOf` rows (`webconsole/src/sim/data.ts:2199-2220`) are
per-service-tier, served-population-based. Go `ServiceKind`
(`internal/engine/services/kind.go:21-37`) is coarser and capacity/funding-
based, with no per-district split on the TS side. This asymmetry is the
central finding the fixture must make explicit rather than paper over.

| TS row (`data.ts`)                  | Go `ServiceKind` (`kind.go`)          | Same granularity? | Notes |
|--------------------------------------|----------------------------------------|--------------------|-------|
| `nursery` (0–4)                      | `education` (`ServiceEducation`)      | No — TS splits 3 tiers, Go has one kind | Deliberate divergence for inc1: fixture compares Go's single `education` capacity against the **sum** of TS nursery+primary+college capacity |
| `primary` (5–15)                     | `education`                            | No (see above) | as above |
| `college` (16–19)                    | `education`                            | No (see above) | as above |
| `gp` (GP clinics)                    | `healthcare` (`ServiceHealthcare`)    | No — TS splits GP vs hospital, Go has one kind | Fixture compares Go `healthcare` against summed TS gp+hosp |
| `hosp` (hospital)                    | `healthcare`                           | No (see above) | as above |
| `police`                             | `police-jail` (`ServicePoliceJail`)   | Yes (1:1) | Go's kind also folds in jail capacity, which has no TS equivalent — noted divergence, not reconciled in inc1 |
| `fire`                               | `fire` (`ServiceFire`)                | Yes (1:1) | Newest row both sides (TS: BUG-526; Go: existing) — best 1:1 candidate, recommended as the first exact-match AC |
| `cleanwater`                         | `water-sewage` (`ServiceWaterSewage`) | No — TS splits clean vs waste water, Go has one kind | Fixture compares Go `water-sewage` against summed TS cleanwater+waste |
| `waste` (sewage)                     | `water-sewage`                         | No (see above) | as above |
| `power`                              | `electricity` (`ServiceElectricity`)  | Yes (1:1), but TS power uses MW capacity/demand while Go coverage uses the generic capacity/demand aggregate — unit mapping must be checked at build time | |
| *(no TS row)*                        | `garbage`, `deathcare`, `elder-care`, `home-care`, `child-benefit`, `public-transport`, `parks-leisure`, `communications`, `districts-policies`, `disasters-lite`, `roads-maintenance` | N/A | Go-only kinds with no current TS coverage-meter equivalent. Out of scope for inc1's parity fixture (nothing to compare against); flag as a known gap, not a defect |

**Wellbeing:** TS `wellbeingOf` (`engine.ts:3832`) blends `serviceCoverageOf`
output with `earlyGameFactor`, parks/education folds, a min(power, clean
water) utilities term, and a brownout penalty — a single composite score, no
named parts. Whether `engine.wellbeing`'s current API exposes an equivalent
composite or only sub-components is an **open question** (Section 5) that
must be answered before the fixture's wellbeing rows are speced in detail —
this document recommends but does not mandate the comparison granularity.

---

## 4. Acceptance criteria

1. **AC-1 (parity fixture exists, generated not hand-written).**
   `internal/converge/testdata/services-webconsole-v1.json` exists,
   produced only by an `emitFixture()`-equivalent script analogous to
   `converge-fixture-emit.mjs`, following the same `{domain,
   samples:[{tick, values}]}` shape as the finance fixture.

2. **AC-2 (no-silent-drift regression).** A committed Go test asserts the
   committed fixture byte-matches a fresh emitter re-run over the same
   canonical action list; the fixture is regenerated only via an explicit
   `--write` flag, exactly mirroring the finance pattern. Hand-editing the
   fixture JSON is a fail on this test.

3. **AC-3 (canonical action list is versioned and reviewed).** A new
   `webconsole/test/converge-fixtures/converge-services-actions.json`
   (or equivalently named) canonical action list exists, driving both the
   TS emitter and the Go-side scripted scenario from the same logical
   inputs (build placements, demand pushes) so the comparison is
   apples-to-apples.

4. **AC-4 (tolerance rules are defined and named).** Each compared field
   declares an explicit tolerance mode:
   - **Exact** for integer/count-like fields (e.g. registered service
     count, population figures feeding `need`).
   - **Banded** for derived/continuous scores (coverage ratios, wellbeing
     composite, `demandIndexOf`-style transforms), with the band width a
     **named constant** in `internal/converge/tolerance.go` (mirroring
     finance's existing tolerance constants), not a magic number inline in
     the comparison.
   Recommendation (Section 5): start with a single named band
   (`ServicesCoverageTolerance` or similar) rather than a per-row band,
   and tighten per-row only if a specific comparison proves noisy.

5. **AC-5 (mapping table completeness).** Every row returned by
   `serviceCoverageOf` (10 rows, Section 3) has an entry in the mapping
   table with an explicit Go-side counterpart OR an explicit "no Go
   equivalent, tracked as gap" marker — no TS row is left unaddressed.
   Every Go `ServiceKind` with no TS row is likewise listed as a known gap.
   This AC is satisfied by Section 3 of this document remaining accurate;
   a future PR that adds/removes a coverage row or ServiceKind without
   updating the table fails review.

6. **AC-6 (determinism, both sides).** The Go-side scripted scenario run
   twice produces byte-identical `CoverageSummary`/`CoverageByDistrict`
   output (GR#21); the TS emitter run twice over the same action list
   produces a byte-identical fixture. Both are provable by running the
   respective harness twice and diffing.

7. **AC-7 (fixture red-proves — fail-open detection).** A test perturbing
   either side breaks the fixture comparison:
   - Perturbing a Go-side coverage value (e.g. flipping a sign, corrupting
     `capacityCeiling()`) makes the parity comparison fail.
   - Perturbing the TS emitter's output (e.g. skipping a coverage row)
     makes the parity comparison fail.
   This proves the fixture is a live gate, not a fixture that would still
   report green with the underlying logic deleted (per the project's
   verification standard: mutate the data, don't grep for it).

8. **AC-8 (dependency gating, not duplication).** This increment's fixture
   work does not implement `save.Participant` for services — it consumes
   whatever `FEAT-2326609743` lands. If that item is not yet landed when
   inc1 build starts, the fixture's Go-side sampling reads `ServicesAPI`
   directly (pre-serialization) as an interim, and a follow-up item is
   filed to re-point the fixture at the serialized `*Wire` form once the
   participant lands — this must not silently diverge into two competing
   "Go truth" sources.

---

## 5. GR#25 graph check

- `engine.finance → int.serializer` is registered (`code.json:797-798`,
  `:1055-1056`, `:2599-2600`) — the edge shape to mirror.
- **No edge currently exists** between `int.serializer` and
  `engine.services` or `engine.wellbeing`. `FEAT-2326609743` (the in-flight
  save-participant lane) is the item expected to register
  `engine.services → int.serializer`; **this spec does not register it** —
  coordinate with the Architect and that lane rather than adding a
  duplicate/conflicting edge. If `engine.wellbeing` also needs to serialize
  (open question, Section 6), that edge is a second registration the
  Architect must add before any prose depends on it (GR#25 — hand-written
  unregistered dependency prose is a fail-closed rejection).
- `engine.attract`/`engine.market`/`engine.consumption → int.serializer`
  edges were previously *missing* despite being declared in code headers
  (`BUG-478`, closed) — the cautionary precedent this spec's Section 4 AC-8
  is designed to avoid repeating for services.
- No new edge is required for the parity-fixture work itself (b/c in
  Section 2): `internal/converge` already has a registered read path to
  `engine.finance` for the existing fixture; extending it to read
  `engine.services`/`engine.wellbeing` needs whatever edge already exists
  from `internal/converge` to those domains checked at build time — flagged
  as a build-time task, not scoped here since it's mechanical registration
  work, not a new architectural dependency.

---

## 6. Open questions (with recommendations)

1. **Parity sample cadence.** Finance's fixture samples at defined ticks
   from a scripted action list. **Recommendation:** reuse the same cadence
   convention (sample after each action, not on a fixed tick interval) so
   the two fixtures stay structurally comparable and reviewers only learn
   one pattern.

2. **Wellbeing 1:1 vs n:m.** TS `wellbeingOf` returns one composite score;
   it is not yet established whether `engine.wellbeing`
   (`internal/engine/wellbeing/api.go:22`) exposes an equivalent single
   composite or only sub-components that would need combining before
   comparison. **Recommendation:** the build phase confirms
   `engine.wellbeing`'s current API surface first; if only sub-components
   exist, compare Go's raw sub-components against a same-formula TS
   recomputation (documented explicitly, not inferred) rather than
   comparing TS's single score against an ad-hoc Go composite invented for
   the fixture.

3. **Acceptable pre-flip divergence.** Given the coarser Go `ServiceKind`
   granularity (Section 3) and open `BUG-523` (TS money divergence
   affecting funding-adjacent numbers), some TS/Go coverage values will
   legitimately differ pre-flip even under otherwise-correct logic.
   **Recommendation:** the fixture's tolerance bands (AC-4) are explicitly
   allowed to be wide for inc1's first pass — the goal of inc1 is
   "comparison exists, is generated, and can fail" (AC-1/2/7), not "TS and
   Go already agree tightly." Tightening bands is inc2+ work once the
   mapping-table gaps (Section 3) are either closed or formally accepted as
   permanent divergences.

---

**Follow-ups to file at build dispatch, not resolved here:**
- Confirm `FEAT-2326609743` scope/timeline with its owner before AC-8's
  interim-vs-final sampling decision is made.
- Register `engine.services → int.serializer` (owned by `FEAT-2326609743`,
  not this spec) and, if Section 6.2 concludes wellbeing needs one,
  `engine.wellbeing → int.serializer` — Architect action, GR#25.

---

## Addendum (2026-09-02, independent round r1 REJECT — remediation record)

The build's first draft (`internal/converge/services_domain.go` v1) was
REJECTed: it never actually READ `engine.services`' own coverage
arithmetic — it re-derived a capacity/demand ratio locally with
hand-authored capacity literals chosen to numerically match the TS
building being compared, which CONCEALED a real, live divergence and made
a hostile source mutation (`coverageRatio()×0.5`) invisible to the whole
suite. This addendum records the three remediation items and the two
findings they surfaced, closing the loop this document's Section 3/6 left
open.

### A1 — the clamp01-vs-unclamped divergence (Section 3 amendment)

`engine.services`' `coverageRatio()` (`internal/engine/services/coverage.go:68-73`)
returns `1.0` when demand is zero, else `clamp01(capacity/demand)` — the
ratio is **capped at 1.0**. TS's `serviceCoverageOf` `row()`
(`data.ts:2291-2292`) computes `need<=0 ? 1 : cap/need` with **no clamp** —
an over-provisioned service can read `40.0` (4000% coverage) on the TS
side where the Go engine would report exactly `1.0` for the identical
capacity/demand pair. This is a genuine, currently-live semantic
difference between the two engines, not a bug in either: TS's unclamped
figure feeds `wellbeingOf`'s `ratio()` helper (`engine.ts`, itself clamped
via `Math.min(1, ...)` at the *call site*, not inside `serviceCoverageOf`),
so the TWO representations already coexist on the TS side today.

**The fixture now makes the divergence explicit rather than papering over
it** (`internal/converge/services_domain.go`,
`webconsole/test/converge-fixture-emit-services.mjs`):
- `"{group}_coverage_x10000"` is the **CLAMPED** ratio on both sides —
  Go's value is the ENGINE's own `CoverageForDistrict(group).CoverageRatio`
  (a first-class read, not a local re-implementation); TS's value is
  `min(1, cap/need)` computed at emission time. This is the field
  `Compare()` checks (both domains' `Contract()`).
- `"{group}_ts_unclamped_coverage_x10000"` is the RAW, unclamped TS ratio
  — TS-only, informational (mirrors the finance fixture's
  income/expenses/net fields), never fed through `Compare()` since Go's
  `ServicesAPI` has no unclamped coverage read to compare it against.

**RED-proof:** because Go's compared field is now the engine's own
`CoverageRatio`, a hostile `coverageRatio()×0.5` source mutation changes
every `"{group}_coverage_x10000"`/`"citywide_coverage_x10000"` value the
pinned self-consistency test
(`TestServicesDomain_EngineReadsPinnedAtTick90`,
`internal/converge/services_domain_test.go`) asserts — the mutation now
fails the suite, where v1's local re-implementation made it invisible.

**Follow-up note (not resolved here):** the eventual domain flip needs an
explicit ruling from Aaron on which semantic wins for the LIVE game —
clamped (a service is "fully covered", no credit for surplus capacity) or
unclamped (surplus capacity is visible, e.g. for a future "sell excess
capacity" mechanic). This spec does not anticipate that ruling; both
representations are captured in the fixture so the decision, whenever
made, has real data to look at.

### A2 — Go-catalogue vs TS-SPECS capacity is now genuinely comparable, and genuinely diverges

Capacity is now sourced via `services.ServiceSpecFromBuilding` from the
REAL `data/buildings.json` catalogue (mirroring
`internal/engine/build/build.go`'s `registerServiceLocked`, the actual
build→services bridge) instead of a hand-authored journal literal. This
surfaced a finding neither engine's own tests would catch in isolation:
**the two catalogues use different units for the same service kind.**
`fire_station` (`data/buildings.json`) is `"4 appliances"` (a truck count);
TS's `fire_post`/`fire_station` (`data.ts`) is `"served=4000"` /
`"served=20000"` (a population-served figure). `clinic`'s Go capacity is
`"150 visits/d"` (a daily RATE); TS's `hea_clinic` is `"served=5000"` (a
population STOCK). These are not comparable numbers today, by design of
each catalogue's own author, and reconciling them is a balance/design
decision (which unit each catalogue SHOULD use), not something this
fixture can or should paper over.

**Consequence:** `TestServicesParity_KnownDivergence_NonEmpty`
(`internal/converge/services_domain_test.go`) asserts the real Go-vs-TS
comparison is non-empty — mirroring `finance_ab_test.go`'s
`TestFinanceAB_KnownDivergence_NonEmpty` honesty-proof pattern exactly.
Every compared field (capacity, and therefore the derived coverage ratio)
genuinely diverges pre-flip; only the population-derived `"{group}_need"`
fields agree today, because both sides deliberately compute demand from
the SAME literal-duplicated population formula (this spec's Section 2
"the fixture's Go-side sampling reads `ServicesAPI` directly" plus a
caller-supplied demand push).

**RED-proof:** a scratch-edit to `data/buildings.json`'s `fire_station`/
`primary_school`/`clinic` `capacityRaw` field changes the LIVE catalogue
value `services_domain.go`'s `loadServicesCatalogue()` reads at `Run()`
time, moving the corresponding `"{group}_capacity"` value away from the
literal `TestServicesDomain_EngineReadsPinnedAtTick90` pins — the suite
goes red. A `capacityCeiling()×0.9` engine mutation is caught by the
education/healthcare legs of the same pinned test (fire's own capacity, 4,
is too small for a 10% mutation to survive integer rounding — noted
honestly in that test's own doc comment rather than silently assumed
sensitive).

### A3 — correction to Section 3's police/power framing

Section 3 lists `police` as "Yes (1:1)" granularity and flags `power`'s
unit mapping as "must be checked at build time," both implying these two
rows are ready to compare today. **They are not, and this is a build-time
finding, not a design gap:** `data/buildings.json` currently has NO entry
carrying the `serviceKind`/`coverageRadius`/`staffingNeed` fields
`registerServiceLocked`'s bridge requires for EITHER a police building or
any power-plant entry (`grep -n '"serviceKind"' data/buildings.json`
returns exactly 8 entries — 4 healthcare, 3 education, 1 fire; `_none_`
tagged `police-jail` or `electricity`). Sourcing a police or power
compared row via `ServiceSpecFromBuilding` (A2's route) is therefore not
possible without inventing a capacity figure — exactly the
hand-authored-literal anti-pattern A1/A2's remediation removes. **Police
and power are NOT compared rows in this increment.** This is a
build-time catalogue-completeness gap (a future increment wiring
`serviceKind` onto a police/power `data/buildings.json` entry unblocks it,
the same way `FEAT-build-services-bridge-2026-09-02` wired
healthcare/education/fire), not a design decision requiring Aaron's
ruling — filed for BOW tracking at build dispatch, not resolved here.

### Kept from the original draft (verified sound, unchanged)

The aggregate semantics (Go's single kind vs the TS multi-row sum for
education/healthcare — Section 3's table itself), the wellbeing deferral
(Section 6.2 — `WellbeingAPI` remains a per-citizen driver-decomposed
engine with no coverage-consuming composite, verified against
`internal/engine/wellbeing/api.go` and unchanged by this remediation), the
named tolerance constants (`internal/converge/tolerance.go`'s
`ServicesCoverageScale`/`ServicesCoverageEpsilon`), the fixture's
determinism and fail-closed shape, and AC-8's interim direct-`ServicesAPI`
sampling (still correct — `FEAT-2326609743` has not landed).
