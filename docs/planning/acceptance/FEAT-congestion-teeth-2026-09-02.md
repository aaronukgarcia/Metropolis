# FEAT: Congestion Teeth — Traffic Penalty on Wellbeing & Income

**Ruling:** Q100057 (A1, Aaron approved).
**Rationale:** BUG-531 — sustained road saturation currently triggers only auto-widening SPEND with no felt consequence (no commute-time, wellbeing, or income penalty). Players build 5-tier motorways with no gameplay friction beyond cost. Baseline One needs congestion to BITE.

---

## Problem Statement

Today:
- `lineUsageOf()` (data.ts:1835) computes saturation = usage / capacity ∈ [0,1]
- A saturated road (saturation ≥ ROAD_SATURATION_THRESHOLD ≈ 0.8) triggers `evaluateRoadMonitors()` to auto-widen (engine.ts:873)
- **But:** zero penalty flows to commute time, wellbeing, or income; a tier-5 motorway at saturation has only a red tint, no gameplay consequence

Aaron's verdict: congestion must impose a **real penalty** — sustained saturation should lower wellbeing (via commute-time drag) and/or reduce income/productivity, making network capacity a **felt resource**, not an invisible spend sink.

---

## Solution Design

### 1. Define Sustained Congestion

A road/motorway line is **sustainably congested** when:
- Its saturation (from `lineUsageOf()`) has **exceeded CONGESTION_PENALTY_THRESHOLD** for **CONGESTION_SUSTAINED_TICKS or more consecutive ticks**, where:
  - `CONGESTION_PENALTY_THRESHOLD` = ⚠️ PLACEHOLDER — likely 0.7–0.85 (slightly below auto-widen trigger, so congestion bites before auto-scale kicks in)
  - `CONGESTION_SUSTAINED_TICKS` = ⚠️ PLACEHOLDER — likely 30–60 ticks (1–2 months watchable, long enough to feel sustained but not noise)

**Implementation layer:** This is a **TS dogfood-sim feature** (webconsole/src/sim/engine.ts + data.ts); Go engine convergence is a later phase.

**Data structure:** Extend `LineUsage` (data.ts:1802) or create a new aux table tracking each line's sustained congestion window. Track:
- `lastSaturation`: the line's saturation last tick
- `sustainedSinceTick`: when the current sustained-congestion window opened (null if currently below threshold)
- `isSustained`: boolean; true if `sustainedSinceTick != null && tick - sustainedSinceTick >= CONGESTION_SUSTAINED_TICKS`

### 2. Penalty Model: Wellbeing Commute-Time Factor

**Primary mechanism:** Sustained congestion lowers **wellbeing** via a new **"Commute Time"** or **"Traffic Congestion"** part.

- **Per-line congestion score:** For each road class line in sustained congestion, a penalty factor: `1.0 - (saturation - CONGESTION_PENALTY_THRESHOLD) / (1.0 - CONGESTION_PENALTY_THRESHOLD)` ∈ [1.0, 0]; saturated (1.0) → 0, below threshold → 1.0 (linear damping).
- **City-wide penalty:** Aggregate the penalty across all road/motorway lines (e.g., MIN of all active lines' factors, or AVERAGE of sustained lines). ⚠️ Aggregation strategy TBD — Aaron's call (MIN is harshest, AVERAGE is gentler).
- **Wellbeing impact:** Add a new **"Traffic/Commute"** part to `wellbeingOf()` (engine.ts:3764) valued as `part(congestionFactor)` using the existing `blend()` and `part()` helpers. This part is averaged with all other wellbeing parts (line 3788) at equal weight (or ⚠️ PLACEHOLDER weight if Aaron wants to tune its influence separately).

**Secondary mechanism (optional, Aaron's call):** Income penalty.
- If the lead decides congestion should also penalize **income**, apply a small multiplier to business/freight/office income flows (computeFlows, engine.ts:604–608), similar to the brownout incomeFactor:
  - `incomeFactor = 1.0 - congestionFactor * CONGESTION_INCOME_K`, where `CONGESTION_INCOME_K` = ⚠️ PLACEHOLDER (likely 0.05–0.15, so a fully congested city loses 5–15% of business income).
  - Apply **after** brownout so the two penalties don't double-charge a single shortage.

**Determinism (GR#21):**
- Sustained-congestion window is a pure function of the line's saturation history; no Date/Math.random.
- No early breaks in line iteration (mirrors `lineUsageOf`'s strict spec-id ordering, data.ts:1915).

**Boundedness:**
- Congestion factor is always ∈ [0, 1]; applied as a multiplier or part-input, never a spike.
- Cannot spiral — even a city at tier-5 saturation bottoms out at a fixed, recoverable penalty, never a death spiral.

---

## Acceptance Criteria

### AC-1: Sustained Congestion is Measurable
**When:** A single road class line (e.g., `road`) exceeds CONGESTION_PENALTY_THRESHOLD saturation for CONGESTION_SUSTAINED_TICKS or more.  
**Then:** The line's `isSustained` flag is true, and its congestion factor reflects the saturation excess.  
**Failing scenario:** A line at saturation 0.8 for 60 ticks reads `isSustained` = false, or congestion factor = 1.0 (no penalty).

### AC-2: Wellbeing Has a Traffic/Commute Part
**When:** A city has one or more sustainably congested lines.  
**Then:** `wellbeingOf()` returns a "Traffic/Commute" or "Congestion" part in its `parts[]` array valued between 0–100, using the city's aggregate congestion factor via `part(congestionFactor)`.  
**Failing scenario:** wellbeingOf() returns 11 parts (missing the new part), or the part's value is hardcoded (not responsive to congestion state).

### AC-3: Wellbeing Overall Drops When Congestion Sustained
**When:** A city transitions from uncongested to sustainably congested.  
**Then:** `wellbeingOf().overall` decreases (all else equal), inversely proportional to the aggregate congestion factor.  
**Failing scenario:** Wellbeing overall remains at 80 before and after a motorway hits 90% saturation for 60 ticks.

### AC-4: Uncongested Network Has Zero Penalty
**When:** All road/motorway lines have saturation < CONGESTION_PENALTY_THRESHOLD OR the city is below CONGESTION_SUSTAINED_TICKS elapsed since the last drop below threshold.  
**Then:** All lines' `isSustained` = false; congestion factors = 1.0; the Traffic/Commute wellbeing part = ~100 (no penalty).  
**Failing scenario:** A brand-new city or a city with 2-lane roads at 40% saturation reads a reduced wellbeing part.

### AC-5: Widening a Congested Road Relieves the Penalty
**When:** A sustainably congested road is auto-upgraded (or manually widened, AC-6 future).  
**Then:** The upgraded line's saturation drops below CONGESTION_PENALTY_THRESHOLD; its `isSustained` flag clears; the congestion factor → 1.0; wellbeing overall increases.  
**Failing scenario:** A tier-4 road at 90% saturation upgrades to tier-5, but saturation stays 90% and wellbeing doesn't improve.

### AC-6: Congestion Factor is Bounded [0, 1]
**When:** Any line reaches maximum saturation (e.g., tier-5 motorway with demand > capacity).  
**Then:** The congestion factor is ≤ 1.0; wellbeing parts are never < 0; no spiral or explosion.  
**Failing scenario:** Congestion factor = 1.5 (exceeds bound), or wellbeing part = -50.

### AC-7: Deterministic Penalty
**When:** Two identical save states (same buildings, population, road network, tick count with saturation history) are replayed.  
**Then:** Both render the identical congestion factors and wellbeing parts (byte-identical). Replayed a third time with mutations (e.g., widened one road), the new penalty is deterministically reproducible.  
**Failing scenario:** Two runs of the same state compute different congestion factors, or the penalty varies across replays.

### AC-8: Penalty Visible to Player
**When:** A city has sustainably congested lines.  
**Then:** The Traffic/Commute wellbeing part is displayed in the Wellbeing panel (or HUD), and its colour/icon/label clearly signals congestion. Optionally, the saturation panel (line usage overlay) can annotate sustained congestion lines (e.g., "⚠️ sustained").  
**Failing scenario:** Wellbeing overall drops but no UI label explains why, or the Traffic/Commute part is computed but never displayed.

### AC-9: (Optional) Income Penalty on Congestion
**If Aaron rules yes for secondary income penalty:**  
**When:** A city has sustainably congested lines and `CONGESTION_INCOME_K > 0`.  
**Then:** Business/Freight/Office Tax inflows are reduced by `incomeFactor = 1.0 - congestionFactor * CONGESTION_INCOME_K`.  
**Failing scenario:** A congested city's Business Tax remains constant while wellbeing drops.

---

## Open Questions for Aaron

1. **Penalty target:** Wellbeing only (AC-1 through AC-8), or wellbeing **plus** income (AC-9)?
   - **BA recommendation:** Wellbeing first; income is a secondary knob if balance needs tuning.

2. **Sustained window length:** How many ticks for congestion to be felt "sustained"?
   - **Candidates:** 30 ticks (1 month, noisy), 60 ticks (2 months, medium), 120 ticks (4 months, sluggish).
   - **BA recommendation:** 60 ticks — long enough that a temporary traffic spike doesn't sting, but short enough that a city can feel relief within a couple of game-months once widened.

3. **Congestion-penalty threshold:** At what saturation should the penalty begin to bite?
   - **Candidates:** 0.70 (bite early), 0.80 (near the auto-widen threshold), 0.90 (only bite at crisis).
   - **BA recommendation:** 0.75 — below the auto-widen trigger (~0.80), so the player feels congestion *before* auto-scale, not after.

4. **Penalty severity curve:** Linear ramp from threshold to 1.0, or steeper (quadratic)?
   - **BA recommendation:** Linear (deterministic, no surprise curves); Aaron can tune the constant multiplier (`CONGESTION_INCOME_K`) to set the slope.

5. **Aggregation across lines:** If multiple road classes are congested, how should the city-wide congestion factor combine them?
   - **Candidates:** MIN (the worst line drags everyone), AVERAGE (blended), MAX (only the worst matters).
   - **BA recommendation:** AVERAGE of sustainably congested lines; if no lines are sustained, factor = 1.0.

6. **Income penalty magnitude (if AC-9):** What CONGESTION_INCOME_K value?
   - **Candidates:** 0.05 (5% max loss), 0.10 (10%), 0.20 (20%).
   - **BA recommendation:** 0.10 (10% — noticeable but not game-ending; a tier-5 congested motorway costs the player ~10% of business income).

---

## Implementation Notes

### Files to modify (TS dogfood layer):
- **webconsole/src/sim/engine.ts:** Add `computeCongestionFactors(s: SimState, tick: number)` function tracking sustained windows; wire into `wellbeingOf()` to add the new part.
- **webconsole/src/sim/data.ts:** Extend `LineUsage` interface with `sustainedSinceTick?: number` and `isSustained: boolean`; update `lineUsageOf()` to populate these fields. OR create an aux tracking structure.

### Constants to define (balance placeholders):
- `CONGESTION_PENALTY_THRESHOLD` (default ~0.75)
- `CONGESTION_SUSTAINED_TICKS` (default ~60)
- `CONGESTION_INCOME_K` (default ~0.10, if income penalty enabled)

### No Go-engine changes required for inc1:
Baseline One targets the TS dogfood sim. Go engine convergence (FEAT-1972079XXX inc2 or later) can mirror the same logic once the architecture stabilizes.

---

## Overlap & Sequencing Notes

- **BUG-567** (motorway junctions / on/off ramps): Independent; junctions are topology, congestion is dynamics. Sequence either way; no blocking dependency.
- **Auto-widening (current):** Congestion teeth enhance auto-widening by making saturation *felt* before the player manually builds wider roads. No code conflict; additive.
- **Brownout income penalty (line 603, engine.ts):** Apply congestion income penalty *after* brownout so both penalties are visible independently and never double-charge a single root cause.

