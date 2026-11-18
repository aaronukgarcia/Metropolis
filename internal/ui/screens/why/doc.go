// Package why implements F5, the WHY screen (FEAT-9995385616 lane A3): the
// panel that says why people come to the city and why they leave.
//
// Module key: ui.screen.why (see code.json)
// Spec refs:  docs/planning/acceptance/reason-panel-criteria.md (section 2e
// and C21..C27), plus Aaron's rulings of 2026-11-18 on FEAT-9995385616
// (ten lever lines, safety and environment as two extra modifier lines,
// reputation as a momentum / pride line, one reserved homelessness row).
//
// # View subscription
//
// The screen subscribes to exactly one view, "f5.why" (ViewSubscriptionName).
// The shape of every patch is the one Go type Patch in types.go, with a
// golden fixture under testdata/. The engine lane publishes that shape; this
// package never imports the engine packages (GR#20).
//
// # Honest states
//
//   - Nothing received (the engine does not serve the view yet) and a patch
//     with known=false both draw "no month closed yet" and list every lever
//     name with no figures. Nothing is invented.
//   - A malformed, oversized or contract-breaking patch is refused with a
//     registry error (MET-V980) and the last good frame stays on screen.
//
// # Layout
//
// One line per lever (name, bar, value, word, signed points, why-text), the
// two modifiers, the momentum / pride line, the wellbeing line, TOTAL, the
// arrivals line and the reserved homelessness row. Columns are cut as the
// terminal narrows: the why-text first, then the bars, then the word is
// shortened; the name, the word and the signed points survive. When rows run
// out the lowest-priority lines go first (see render.go) and the ten levers
// and TOTAL are kept as long as possible. The panel is a pure function of
// the patch and the rect: no wall clock, no locale, no map range (GR#21).
//
// The points on screen add up: the listed points, the two modifiers, the
// momentum line and the wellbeing line sum to the net on the TOTAL line,
// exactly, in tenths (see allocateTenths in render.go).
package why
