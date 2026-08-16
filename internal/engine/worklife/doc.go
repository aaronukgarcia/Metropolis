// Package worklife is the work-schedule & work-life balance module
// (MOD-080): every placed job carries a TIME pattern (core-hours / shift /
// any-time), every employed worker commutes on a schedule-shaped rush, and
// the working week is a policy lever (a varied ~40h default vs a 996
// 9-to-9/6-day option that earns more but costs wellbeing).
//
// Module key: engine.worklife (see code.json; GUID 04a588d4-700d-490e-beb0-d3dbc9a66651)
// Spec ref:   §5.1 (the citizen employmentState this schedule describes);
// §18 (wellbeing — where the 996 cost lands); §19.3 (commute accounting);
// §21 (off-map job pools); §26 (shared staffing pools — the shift model
// must not break the "a nurse shortage is one shortage everywhere"
// precedent); §42 (leisure time — the 168-hour discretionary-hours budget
// that makes work hours a wellbeing/leisure policy); §52 (policies — the
// home of the 996 toggle); §54 (Public Service Pie staffing ratios); §5.2
// (determinism).
// Design:     Aaron 2026-08-16 design (BOW FEAT-137): time patterns
// (9-5 core-hours/teacher model, 24x7 shift/nurse model, any-time),
// schedule-driven commute, a 40h-vs-996 working-week policy, and the
// overwork trade.
//
// # Balance-number regime (standing rule, ASM-953)
//
// The hours-per-week figures (default ~40h, 996 = 72h), the 996 wage
// multiplier, and the overwork wellbeing weight are PLACEHOLDERS living in
// data/worklife.json, each carrying a disclosure comment naming it pending
// Aaron's balance pass. This package hardcodes no schedule hour, wage rate,
// or happiness weight: every such figure is read from data or from the
// active working-week policy via the PoliciesAPI seam. Tests assert
// direction/structure only (996 hours > default; 996 wage > default; 996
// wellbeing < default), never a pinned total.
//
// # Three boundaries (what this module does NOT own)
//
//  1. The policy lives in engine.policies (MOD-064): worklife consumes the
//     active working-week effect through the PoliciesAPI seam, it never
//     defines the toggle, its cost, or its scope (AC-8).
//  2. Commute assignment lives in engine.traffic (MOD-023, deferred):
//     worklife produces the time-shaped per-hour demand (AC-6/AC-7);
//     traffic consumes it and computes the real rush/congestion (AC-7).
//  3. The wellbeing cost routes through engine.wellbeing (MOD-034):
//     worklife feeds an overwork/work-life input through the WellbeingAPI
//     seam (§42 discretionary-hours/leisure-fit, ASM-956); wellbeing owns
//     the happiness arithmetic (AC-12).
//
// # The tick is a simulation hour (GR#21)
//
// worklife's tick is an absolute simulation-hour index (0, 1, 2, ...): the
// hour of day is tick%24, the day of week is (tick/24)%7 (0 = Monday), and
// the week index is tick/(24*7). The "day"/"hour" below is always this
// simulation tick — never the wall clock. The fixed 24/7 clock structure
// lives in clock.go as named constants and is the ONLY hour-shaped numeric
// literal in this package's non-test source; every schedule figure (pattern
// hours, rotation windows, weekly hours, wage/wellbeing coefficients) is
// data, never a Go literal.
package worklife
