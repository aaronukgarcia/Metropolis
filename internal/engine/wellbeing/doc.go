// Package wellbeing is the §18 Physical & Mental Health module (MOD-034):
// two 0-100 per-citizen tracks whose value is a conserved sum of causal
// driver contributions — never a flat, hand-pushed number. Each driver is a
// deterministic term that accumulates over ticks; the track total is the
// exact sum of the baseline plus every driver's delta, and each delta can be
// drilled through to the named cause that produced it.
//
// Module key: engine.wellbeing (see code.json; GUID 486813c7-b04b-4510-b05f-6148da76ef8e)
// Spec ref:   §18 (Wellbeing — Physical & Mental Health: "two per-citizen
// 0-100 state tracks"; physical drivers — age curve, healthcare
// access/quality/queue time, diet/fresh-food share, active travel, pollution
// exposure, sport×physicality; mental drivers — commute time nonlinear past
// 45min, job-ambition mismatch, green space within 400m, leisure-fit,
// crowding, isolation = sociability×community access, noise, financial
// stress = rent>35%, unemployment duration; "Wellbeing = f(physical, mental,
// satisfaction) is the headline city stat"; downstream effects mortality/
// productivity/satisfaction/emigration); §42 (Leisure Time & Exploration —
// the leisure-fit vs personal taste-weights coupling and the discretionary-
// hours budget whose commute component feeds mental health via §19.3).
//
// # The fifteen drivers
//
// Every driver below is an exported field on the physical or mental
// attribution result (AC-1's "driver-decomposed scores; every driver
// drill-through"), each with a one-line causal description:
//
// Physical (PhysicalAttribution):
//
//  1. AgeCurve — physical health declines with age along the data age curve.
//  2. HealthcareAccess — better healthcare access/quality/queue raises
//     physical health.
//  3. Diet — a higher fresh-food share raises physical health.
//  4. ActiveTravel — a higher active-travel mode share raises physical
//     health.
//  5. PollutionExposure — more pollution at the home cell lowers physical
//     health.
//  6. SportParticipation — sport venue access × physicality raises physical
//     health.
//
// Mental (MentalAttribution):
//
//  7. CommuteTime — door-to-door commute time, nonlinear (steeper) past 45
//     minutes, lowers mental health.
//  8. JobAmbitionMismatch — the gap between ambition and the career level of
//     the current job lowers mental health.
//  9. GreenSpace400m — green space within 400m raises mental health.
//  10. LeisureFit — leisure-fit vs personal taste weights raises mental
//     health.
//  11. Crowding — persons per room (overcrowding) lowers mental health.
//  12. Isolation — the product (1 − sociability) × (1 − community venue
//     access) lowers mental health; both factors are load-bearing.
//  13. Noise — noise exposure lowers mental health.
//  14. FinancialStress — a rent burden at or above the §18 35% threshold
//     lowers mental health (a threshold, not a smooth penalty).
//  15. UnemploymentDuration — longer unemployment duration lowers mental
//     health (a duration curve, not a boolean).
//
// # Additive identity and isolation (AC-2 / AC-3)
//
// For every attribution query over an accepted config and finite inputs,
// Total == Baseline + Σ(driver.Delta) exactly. The identity is exact because
// Validate bounds every coefficient below maxCoefficient, so no in-domain
// driver product overflows float64; the one runtime input deliberately left
// unbounded — crowding's persons-per-room — is saturated finite by satFinite
// before it reaches the sum, and a single saturated delta still sums finitely
// with the bounded baseline and the other deltas, so Total stays the literal
// sum. satFinite on the final sum is a defensive backstop against a future
// unbounded input, not a clamp that fires for any accepted config. That
// identity alone is NOT proof of real causality — a fabricated post-hoc
// proportional split of a
// single blended score can also be made to sum correctly. The check that
// actually distinguishes real attribution is isolation: each driver's delta
// is a pure function of ITS OWN input (plus the loaded weights and the
// month), so perturbing exactly one driver's input moves exactly that
// driver's delta — every other driver's delta is byte-identical — and
// Total's change equals exactly the perturbed delta's change.
//
// # The accumulator, not a stored field (AC-18)
//
// The tracks are reconstructed on demand as a deterministic function of
// (worldSeed, citizenID, month, driver inputs), never stored as a durable
// per-citizen attribution history. engine.citizens fixes the hot record at
// ~250B and the cold shard at 60-100B/citizen with no headroom for fifteen
// per-driver deltas; storing them durably would add 6-12GB at 100M citizens.
// So attribution lives only in this package: live for HOT citizens, and via
// the same hash(worldSeed, id, month)-bound reconstruct-on-inspect pattern
// engine.citizens' life-writing already contracts for WARM/COLD citizens.
// Do NOT reinvent durable per-citizen attribution storage.
//
// # Determinism and safety (GR#21, GR#16, GR#7)
//
// This package never reads the wall clock; the only source of variation is
// the counter-based hash stream (foundation/det) keyed by (worldSeed,
// citizenID, month), which is bit-identical across runs and platforms. Every
// error is registry-sourced (MET-G21xx, this package's claimed sub-range —
// see errors.go), and every numeric input is validated finite and in-domain
// before it can reach the conserved sum.
package wellbeing
