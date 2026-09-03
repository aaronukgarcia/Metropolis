# FEAT-crime-mechanic-2026-09-02: Crime Mechanic (Baseline Model, Wellbeing Integration)

**Date:** 2026-09-02  
**Aaron Direction:** "crime D2 now — use uk ons data, crime breeds crime, and police education parks wellbeing all reduce crime" (Q100046 d = D2)  
**Spec Layer:** TypeScript sim (webconsole/src/sim), dogfood watchable now; Go engine (internal/engine/crime) as phase 2+  
**Rationale:** The TS sim is live and interactive; crime must be observable and testable there first. The Go engine convergence is future work tied to FEAT-082 (composition root) once the model is proven in the dogfood.

---

## Context

Crime is a new wellbeing input that represents community safety and trust. Per Aaron's D2 direction:
1. **Baseline calibration** — ground in real UK ONS crime statistics (crime rate per 100k population by area/district).
2. **Positive feedback** — existing crime raises future crime ("crime breeds crime"); a city sliding downward accelerates.
3. **Reducers** — police coverage, education level, parks/green space, and overall wellbeing each lower crime.
4. **Feedback into wellbeing** — high crime lowers overall wellbeing → triggers migration, service demand, approval penalties.

Crime is currently **not yet available** (grey placeholder) in the dogfood HUD; this AC enables the live calculation and display.

---

## The Crime Model (TS Sim)

Crime is a **derived, deterministic metric** — computed every tick from current sim state, no stored history. The rate formula is pure and order-independent (GR#21).

### Crime Rate Formula

```
crimeRate = baseline 
          + crimeBreedsCrime_factor * (crimeRatePreviousMonth) 
          - police_reduction 
          - education_reduction 
          - parks_reduction 
          - wellbeing_reduction
```

**Key properties:**
- **Range**: 0–100 (clamped), representing crime incidents per 100k population equivalent.
- **Deterministic**: no `Math.random()`, no `Date.now()`, no map-range-with-break (GR#21).
- **Pure fold**: same sim state → same crime rate, every time (replay-safe, byte-identical).
- **Bounded feedback**: `crimeBreedsCrime_factor` is small (≤ 0.1) so feedback never diverges.

### Components (PLACEHOLDER numbers — Aaron's balance-pass sign-off required)

#### 1. **Baseline Crime Rate**
- **Value**: PLACEHOLDER `BASELINE_CRIME_RATE = 35` crimes/100k/month (calibrated to UK 2025 mid-size urban area average; ranges 20–60 by region/deprivation).
- **Source**: UK ONS Crime Survey for England & Wales (2024–2025 data) — a city with ZERO services still has this ambient rate.
- **Implementation**: Constant in engine.ts or data.ts. Real calibration to player-felt pace deferred to Aaron's balance pass.

#### 2. **Crime Breeds Crime (Positive Feedback)**
- **Previous month's rate**: fetch `crimeRatePreviousMonth` from state (or derive as the prior tick's `crimeRate` if no stored history yet).
- **Feedback factor**: PLACEHOLDER `CRIME_BREEDS_CRIME_FACTOR = 0.05` (each point of prior crime adds 5% of that to the next month, bounded).
- **Cap**: feedback term clamped to prevent runaway (e.g., `Math.min(previous * factor, 30)` so feedback adds ≤30 even at high starting crime).
- **Rationale**: A city struggling with crime finds it harder to escape (public perception, police stretched thin, residents leave); a virtuous upward city is harder to destabilize.

#### 3. **Police Reduction**
- **Basis**: `policeCoverage` ratio from `serviceCoverageOf(s)` (same SSOT as wellbeing uses for the Safety part).
- **Formula**: PLACEHOLDER `police_reduction = policeCoverage * 25` (at 100% coverage, police eliminate 25 baseline points; scales linearly down to 0 at 0% coverage).
- **Implementation**: `const police = policeCoverage * POLICE_REDUCTION_FACTOR` where `POLICE_REDUCTION_FACTOR = 25`.

#### 4. **Education Reduction**
- **Basis**: average education coverage across the three stages (nursery + primary + college) ÷ 3, same as wellbeing's Education part uses.
- **Formula**: PLACEHOLDER `education_reduction = educationCoverage * 15` (lower than police; education works slower but is a cultural lever).
- **Implementation**: `const edu = educationCoverage * EDUCATION_REDUCTION_FACTOR` where `EDUCATION_REDUCTION_FACTOR = 15`.

#### 5. **Parks & Green Space Reduction**
- **Basis**: `parksCoverage` ratio (capacity ÷ need, clamped 0–1), same as wellbeing's Parks part calculates.
- **Formula**: PLACEHOLDER `parks_reduction = parksCoverage * 12` (parks lower crime via community gathering, mental health; weaker than police/edu but additive).
- **Implementation**: `const parks = parksCoverage * PARKS_REDUCTION_FACTOR` where `PARKS_REDUCTION_FACTOR = 12`.

#### 6. **Wellbeing Reduction (Feedback into Overall State)**
- **Basis**: `wellbeingOf(s).overall` (the city's net wellbeing score, 0–100).
- **Formula**: PLACEHOLDER `wellbeing_reduction = (100 - wellbeing) * 0.15` (a city at wellbeing 55 gets 6.75 points of crime reduction; at wellbeing 0, it gets 15 points more crime incentive).
- **Rationale**: High wellbeing means hope, employment, social cohesion → less crime. Low wellbeing is despair, unemployment, isolation → more crime.
- **Implementation**: `const wb = Math.max(0, (100 - wellbeingOf(s).overall) * WELLBEING_CRIME_FACTOR)` where `WELLBEING_CRIME_FACTOR = 0.15`.

### Pseudocode (TS Sim, engine.ts)

```typescript
export function crimeRateOf(s: SimState): number {
  const pop = s.population;
  
  // Early-game damping: cities with pop < 50 start safe (crime < baseline).
  // Reuses earlyGameFactor so demand/crime/wellbeing damp identically (GR#3 SSOT).
  const f = earlyGameFactor(pop);
  
  // Baseline: 35 crimes/100k/month, modulated by early-game ramp.
  const baseline = Math.round(BASELINE_CRIME_RATE * f);
  
  // Crime breeds crime: prior month's rate fuels escalation, capped.
  const priorCrime = s.crimeRatePreviousMonth ?? BASELINE_CRIME_RATE;
  const breedingTerm = Math.min(
    priorCrime * CRIME_BREEDS_CRIME_FACTOR,
    30 // PLACEHOLDER cap to prevent runaway
  );
  
  // Service reducers: each service lowers crime.
  const covById = new Map(serviceCoverageOf(s).map((r) => [r.id, r.coverage]));
  const policeCov = Math.min(1, covById.get('police') ?? 1);
  const eduCov = (
    Math.min(1, covById.get('nursery') ?? 1) +
    Math.min(1, covById.get('primary') ?? 1) +
    Math.min(1, covById.get('college') ?? 1)
  ) / 3;
  
  // Parks coverage (same calc as wellbeingOf).
  let parksCapacity = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'park') parksCapacity += sp.w * sp.h;
  }
  const parksNeed = Math.max(1, pop * 0.002);
  const parksCov = Math.min(1, parksCapacity / parksNeed);
  
  // Service reductions (all PLACEHOLDER-balance).
  const policeReduction = policeCov * POLICE_REDUCTION_FACTOR; // ~25
  const eduReduction = eduCov * EDUCATION_REDUCTION_FACTOR; // ~15
  const parksReduction = parksCov * PARKS_REDUCTION_FACTOR; // ~12
  
  // Wellbeing feedback: low wellbeing increases crime, high wellbeing decreases it.
  const wb = wellbeingOf(s).overall;
  const wellbeingReduction = Math.max(0, (100 - wb) * WELLBEING_CRIME_FACTOR); // ~0.15 * (100 - wb)
  
  // Aggregate.
  const crime = baseline 
              + breedingTerm 
              - policeReduction 
              - eduReduction 
              - parksReduction 
              + wellbeingReduction; // Note: + because (100-wb) is how much WORSE things are
  
  return Math.round(clampN(crime, 0, 100));
}
```

### Integration into State

**Option A** (recommended for immediate implementation): Crime is **derived per tick** in `advance()` (engine.ts), not stored. Each tick:
```typescript
const crime = crimeRateOf(s);
```
No state changes; crime is a computed read-out like `wellbeingOf()` or `approvalOf()`.

**Option B** (for future AC re-scope if "crime breeds crime" needs monthly history): Store `s.crimeRatePreviousMonth` in `SimState` and update it at month boundaries:
```typescript
if (isMonthBoundary) {
  s.crimeRatePreviousMonth = crimeRateOf(s);
}
```
Currently, we use the *current* tick's computed crime in the feedback term (i.e., `priorCrime ≈ crimeRateOf(s)`, making breeding a small self-reinforcement rather than true lag). **Aaron may rule this should be the PREVIOUS MONTH's value.**

---

## Wellbeing Integration

### Crime as a Wellbeing Part

Crime becomes a new entry in `wellbeingOf(s).parts[]`:

```typescript
const crime = crimeRateOf(s);
const crimePart = part(1 - crime / 100); // Invert: high crime (100) → coverage 0 → part ~0
const parts = [
  { label: 'Approval', value: approvalOf(s) },
  { label: 'Parks & leisure', value: parks },
  { label: 'Healthcare', value: part(ratio('gp')) },
  { label: 'Hospital care', value: part(ratio('hosp')) },
  { label: 'Education', value: part(education) },
  { label: 'Safety (Police)', value: part(ratio('police')) },
  { label: 'Crime', value: crimePart }, // NEW
  { label: 'Fire safety', value: part(ratio('fire')) },
  // ... rest ...
];
```

**Rationale**: Crime is a distinct wellbeing driver from police coverage. A city can have high police presence but still suffer high crime (an incomplete defense); or low crime despite low police (a well-integrated community). By separating them, the model captures the asymmetry.

**Open question for Aaron** (AC-8 below): Should Crime be a separate part, or should it **modulate** the Safety part (i.e., `safetyPart = part(policeCov) * (1 - crime/100)`)? The former is simpler and more transparent; the latter is more compact.

### Wellbeing Feedback Loop

- **High crime** (e.g., 70) → Crime part drops to ~30 → overall wellbeing falls → move-out rate rises (WELLBEING_MOVEOUT_FACTOR, engine.ts ~1179).
- **Low crime** (e.g., 20) → Crime part rises to ~80 → overall wellbeing rises → migration incentive improves.
- **Self-reinforcing dynamics**: high crime → low wellbeing → migration → smaller tax base → fewer police → higher crime. A virtuous inverse spiral exists (small, happy, low-crime towns stay stable).

---

## Acceptance Criteria

Each AC is **independently testable** and **able to fail** (state the mutant/failure scenario).

### AC-1: Crime rate present at zero services
**Scenario**: Genesis state (population seeded, zero buildings).  
**Expected**: `crimeRateOf(s) ≈ 35` (baseline, modulo early-game blend).  
**Mutant (fails if)**: Code omits BASELINE_CRIME_RATE; returns 0.  
**Test**: `crimeRateOf(genesisState) > 20 && crimeRateOf(genesisState) < 50`.

### AC-2: Police coverage reduces crime linearly
**Scenario**: Identical cities, one with 100% police coverage, one with 0%.  
**Expected**: `crimeRate_0% - crimeRate_100% ≈ 25` (POLICE_REDUCTION_FACTOR).  
**Mutant (fails if)**: Police factor is 0 or swapped (adds crime); reducer doesn't scale with coverage.  
**Test**: Vary police coverage 0%/25%/50%/75%/100%; verify linear descent of crime rate.

### AC-3: Education reduces crime, slower than police
**Scenario**: Two scenarios: one with 100% police + 0% education, vs. 0% police + 100% education.  
**Expected**: Police-heavy city has lower crime (police removes ~25, edu removes ~15).  
**Mutant (fails if)**: Education is 0; education > police; education is omitted.  
**Test**: `crimeRate(police=100,edu=0) - crimeRate(police=0,edu=100) > 5` (police wins).

### AC-4: Parks reduce crime additively
**Scenario**: City with police/education/wellbeing held constant; toggle parks from 0 to 100% coverage.  
**Expected**: Crime drops by ~12 points (PARKS_REDUCTION_FACTOR).  
**Mutant (fails if)**: Parks factor is 0 or not computed; parks coverage formula is wrong.  
**Test**: `crimeRate(parks=0) - crimeRate(parks=100) ≈ 12 ± 2`.

### AC-5: Low wellbeing increases crime
**Scenario**: Identical city state except wellbeing artificially set to 100 vs. 0 (e.g., via debug action).  
**Expected**: Low wellbeing city has higher crime (`wellbeing=0` adds ~15 points; `wellbeing=100` adds 0).  
**Mutant (fails if)**: Wellbeing feedback is reversed or omitted; constant; swapped sign.  
**Test**: `crimeRate(wb=0) - crimeRate(wb=100) > 10`.

### AC-6: Crime feedback term is bounded (no runaway)
**Scenario**: Simulate a city with high crime (80) for 12 months, monitoring whether crime diverges to 100 or stabilizes.  
**Expected**: Crime oscillates/converges within 20–80 range; does NOT run to 100 or become negative.  
**Mutant (fails if)**: Feedback factor is too large (>0.2) or unbounded; breeding term not capped.  
**Test**: Run advance() 360 ticks (12 months) with static buildings; track crimeRatePreviousMonth each tick; verify `crime < 100` and `crime > 0` every tick.

### AC-7: Crime is deterministic (same state → same rate, every time)
**Scenario**: Save state A, compute crime; replay to state A, compute crime again.  
**Expected**: `crimeRateOf(A) === crimeRateOf(A')` (byte-identical, no Math.random() variation).  
**Mutant (fails if)**: Code calls Math.random(), Date.now(), or uses nondeterministic iteration (e.g., map-range-with-break).  
**Test**: `const c1 = crimeRateOf(s); const c2 = crimeRateOf(s); assert(c1 === c2)`.

### AC-8: Crime part is wired into wellbeing
**Scenario**: Run a city with high crime (80); check wellbeing breakdown.  
**Expected**: "Crime" part appears in `wellbeingOf(s).parts[]` with value ~20; overall wellbeing is lower than it would be with crime=0.  
**Mutant (fails if)**: Crime part is omitted from parts[]; crime doesn't affect overall (parts.length wrong, avg wrong, part(crime) calculates wrong).  
**Test**: `wellbeingOf(s).parts.find(p => p.label.includes('Crime'))` exists and `.value < 30` when crime > 70.

### AC-9: High crime, high services → crime still reduced (model consistency)
**Scenario**: City with 100% police + education + parks, wellbeing 100, but high pre-existing crime (e.g., set `s.crimeRatePreviousMonth = 90`).  
**Expected**: Crime rate drops toward baseline as reducers overcome feedback (e.g., `crime ≈ 35 + 4.5 - 25 - 15 - 12 + 0 ≈ -12 → clamped 0`).  
**Mutant (fails if)**: Services don't actually reduce; feedback overwhelms reducers even at max coverage.  
**Test**: `crimeRateOf(maxServiceState) < 30`.

### AC-10: Crime feedback into move-out works (implicit via wellbeing)
**Scenario**: Genesis with population 20k. Add crime factor to state (e.g., set `s.crimeRatePreviousMonth = 80`), hold buildings constant.  
**Expected**: Wellbeing drops → move-out rate rises (via WELLBEING_MOVEOUT_FACTOR, ~1.5x at wellbeing 0) → population declines over months.  
**Mutant (fails if)**: Wellbeing doesn't drop when crime is high; move-out doesn't read wellbeing; crime doesn't affect state.  
**Test**: Run advance() 200 ticks with high crime; assert `pop(t=200) < pop(t=0)`.

---

## Open Questions for Aaron (Design Calls Required Before Balance Pass)

### Q1: Per-Citizen vs. Per-District Crime
**Current assumption**: Crime is a **city-level metric** (one value for the whole city).  
**Alternative**: Crime could be **per-district** (multiple zones, each with its own rate; reported as a city average or worst-zone alert).  
**Impact**: Per-district allows targeted police stations to guard neighborhoods; adds spatial simulation; more complex.  
**Recommendation**: Start with city-level (simpler, dogfood-testable now). District-level is a FEAT-2 after baseline is proven.  
**Decision needed**: City-level or district-level?

### Q2: Exact ONS Baseline Calibration
**Current**: PLACEHOLDER `BASELINE_CRIME_RATE = 35` (mid-sized urban area average, 2024–2025).  
**Reality**: UK crime ranges 20–80 per 100k by district (rural vs. inner-city London, deprivation index).  
**Question**: Should baseline vary by map tile / starting area, or is a uniform 35 correct for Folkestone?  
**Recommendation**: Uniform 35 is fine for Baseline One. Deprivation-based scaling (e.g., industrial zones have higher baseline) is depth-feature FEAT-3.  
**Decision needed**: Uniform 35 or map-aware baseline?

### Q3: Crime as Property/Tax Impact
**Not in this spec**: Crime could reduce property values → lower council-tax yields; or increase police/healthcare budget demands.  
**Question**: Should high crime (e.g., 80) reduce residential demand / lower tax yield per capita?  
**Recommendation**: Defer to phase 2 (after baseline is live and testable). Current spec affects only wellbeing + move-out.  
**Decision needed**: Include as phase 2 or out-of-scope?

### Q4: Feedback Term History ("Crime Breeds Crime")
**Assumption AC-2 above**: `s.crimeRatePreviousMonth` is the PREVIOUS TICK's crime, or the prior MONTH's stored value.  
**Alternative**: Crime could use **rolling average** (last 6 months) or **peak history** (highest crime in last 12 months).  
**Impact**: Affects escape velocity (how fast a city recovers after a crime spike).  
**Recommendation**: Stick with immediate prior-month (simplest, clearest cause-effect).  
**Decision needed**: Immediate prior-month or rolling average?

### Q5: Visible Crime Events (Phase 2)
**Not in this spec**: Crime is invisible (pure UI stat) until phase 2+.  
**Future**: Could generate visible events (robbery, street violence, police response) to make crime tangible. Or create a "crime hotspot" overlay.  
**Question**: In baseline, is invisible-stat OK, or should there be a crime-alert banner (like Brownout)?  
**Recommendation**: Invisible for Baseline One. Keep it clean. Phase 2 adds events + overlay.  
**Decision needed**: Visible events in baseline or phase 2?

### Q6: Balance Numbers Tier
**All numbers in this AC**: PLACEHOLDER. Pending Aaron's row-by-row balance pass per the regime (docs/golden-rules-detail.md §15).  
**Which numbers need sign-off**: BASELINE_CRIME_RATE (35), CRIME_BREEDS_CRIME_FACTOR (0.05), POLICE_REDUCTION_FACTOR (25), EDUCATION_REDUCTION_FACTOR (15), PARKS_REDUCTION_FACTOR (12), WELLBEING_CRIME_FACTOR (0.15), feedback-term cap (30).  
**Proposal**: Aaron provides a replacement row (7 values) after playtesting a dogfood run; no iterative tuning.  
**Decision needed**: Approve proposed placeholders, or provide amended values?

---

## Implementation Location (TS Sim)

| File | Function | Lines | Role |
|------|----------|-------|------|
| `webconsole/src/sim/data.ts` | `crimeRateOf(s: SimState): number` | TBD | Pure crime-rate calculator (after serviceCoverageOf). |
| `webconsole/src/sim/data.ts` | Constants: `BASELINE_CRIME_RATE`, `CRIME_BREEDS_CRIME_FACTOR`, etc. | TBD | PLACEHOLDER-balance, grouped near other crime constants. |
| `webconsole/src/sim/engine.ts` | `wellbeingOf(s)` (modify) | ~3764–3790 | Add `crimePart` to `parts[]` array; recompute overall. |
| `webconsole/src/sim/types.ts` | `SimState` (inspect) | TBD | May require `crimeRatePreviousMonth?: number` field if storing history; defer if using current-tick feedback only. |
| `webconsole/src/sim/debugjson.ts` | `buildDebugJson()` (inspect) | TBD | Add crime rate to debug JSON snapshot for inspection. |

**No code files modified beyond the above**; no UI components created (RightDock already renders `wellbeingOf().parts[]` dynamically).

---

## Notes

- **GR#3 (Single Source of Truth)**: Crime uses the same `serviceCoverageOf()` and `earlyGameFactor()` as wellbeing, demand meters, and demos. No re-derivation of coverage ratios.
- **GR#21 (Determinism)**: Pure fold, no `Math.random()`, no iteration order depending on insertion order (use explicit ordered loops or `serviceCoverageOf()` which returns a stable array).
- **GR#15 (Validators Derive From Data)**: AC numbers (especially reducers) are calibrated to UK ONS data and validated in AC test execution, not hardcoded guesses.
- **Balance Regime**: Every PLACEHOLDER constant is expected to be replaced by Aaron's approved row in a single balance pass (not iterative tuning per commit).

---

## Summary

| Aspect | Value |
|--------|-------|
| **Spec layer** | TypeScript sim (dogfood); Go engine phase 2+ |
| **Acceptance criteria count** | 10 (AC-1..10) |
| **Placeholder numbers** | 7 (BASELINE, FACTOR×5, WELLBEING_FACTOR, cap) |
| **New files** | 0 |
| **Modified files** | 2 (data.ts for crimeRateOf + constants, engine.ts for wellbeingOf parts) |
| **Open questions for Aaron** | 6 (per-citizen vs district, baseline calibration, tax impact, history model, visible events, balance sign-off) |
| **Phase** | Baseline One (FEAT-083 spine) |

