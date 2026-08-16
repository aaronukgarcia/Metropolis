// Package leisure implements §42 Leisure Time & Exploration and the §5.1
// leisure-taste-weight machinery for the Metropolis engine. It is
// code.json's engine.leisure module (GUID d7d01c42-212d-48f4-b9b9-984bf33bd1ce,
// "venue patronage personality-weighted; unmet taste demand queryable").
//
// # The 168-hour weekly budget (the load-bearing core)
//
// §42's own formula is `168 − work − sleep − chores − commute`. This package
// computes that budget per citizen as a DETERMINISTIC, dynamic split — never
// a flat constant:
//
//   - work (time at a firm) and education (time at a school) are the two
//     halves of the productive obligation, drawn from a per-life-stage data
//     table (child/student/employed/unemployed/retired) in data/leisure.json;
//   - sleep and chores are per-life-stage data baselines;
//   - commute is engine.traffic's real door-to-door figure (§19.3), read
//     through the registered engine.leisure → engine.traffic edge, so an
//     hour saved on the network is genuinely an hour available for leisure;
//   - the residual discretionary time is split between leisure (going out)
//     and rest (staying home) by the citizen's own taste weights — a
//     homebody rests more, a social/novelty-seeking citizen goes out more.
//
// The trade-offs are captured directionally: overtime (SetOvertimeHours)
// reduces discretionary leisure time — the "overtime harms wellbeing" half —
// while generating OvertimeWage — the "overtime generates wages" half.
// Leisure fits reduce stress and consume time; the stress half is wellbeing's
// scoring of the pushed LeisureFit driver, not this package's.
//
// # Venue patronage and novelty
//
// A citizen's leisure hours are allocated across the seven going-out venue
// categories (sport, arts, nightlife, nature, community, gaming, dining) in
// proportion to their personality-derived taste weights (§5.1), reduced by
// an access-time penalty when a category is not reachable within available
// evening/weekend capacity (AC-3). Per-citizen per-venue "freshness" decays
// with repeated visits, faster for novelty-seeking citizens, and the decayed
// freshness reduces future patronage probability (AC-4). Opening or
// refurbishing a venue resets that freshness for matching citizens (AC-5).
//
// # Outbound contracts (GR#20)
//
// This package consumes engine.citizens (the citizen record: personality,
// derived taste weights, life stage), engine.traffic (commute + access time +
// crowd-transport trip demand), and engine.wellbeing (the pushed LeisureFit
// driver) — through their registered contracts alone. engine.traffic and
// engine.wellbeing are not yet built, so both are consumed as local contract
// shapes (TrafficAPI/WellbeingAPI) that the composition root wires, with
// tests injecting fakes — the same GR#20 contract-first, stub-forever pattern
// engine.education uses for its traffic edge.
//
// # Two things that share the name "leisure-fit" (for Bill)
//
// AC-9's citywide LeisureFitAggregate (venue mix vs a would-be-migrant
// personality distribution, §11) and AC-3/AC-10's per-citizen leisure-fit
// (venue mix vs one citizen's own taste weights, §18) are deliberately two
// different accessors because §11 and §18 define genuinely different things
// that happen to share a name. Collapsing them is an interface change, not a
// drift.
//
// # Remaining unregistered edges (BUG-058)
//
//   - The citywide leisureFit aggregate (AC-9) is consumed by engine.attract
//     only as a PUSHED value pending BUG-058: code.json registers no
//     engine.attract → engine.leisure edge, so a caller pushes
//     LeisureFitAggregate's result into AttractAPI.TermInputs rather than
//     engine.attract calling here directly (engine.attract.md AC-3's
//     documented workaround).
//   - The seasonal leisure-mix behaviour (AC-8) is BLOCKED pending the same
//     class of registry gap: code.json registers no engine.leisure →
//     engine.season edge, so this package imports no engine.season package
//     at all and never duplicates a summer/winter curve (GR#3/GR#15). Until
//     that edge lands, AC-8 stays unarmed.
//   - work/education baselines are DATA inputs, not live calls to
//     engine.firms/engine.education, because code.json registers no such
//     outbound edges for this module.
package leisure
