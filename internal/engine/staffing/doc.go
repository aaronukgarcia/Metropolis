// Package staffing implements the skill pool matched to per-service demand module (MOD-073).
//
// Key: engine.staffing
// Cites: §26 Emergency & Care Dispatch Model, §54 The Fiscal Circuit, §5.1 the citizen record,
// §27 The Educational Lifecycle, and A6 Off-map job pools.
//
// # One-Role-Per-Citizen Conservation Rule (AC-9):
//
// A citizen can be assigned to exactly one staffing role at any given time (e.g. nurse,
// teacher, or engineer). Assigning a citizen to a second role automatically vacates
// their previous role, preventing the labour pool from being double-counted across
// distinct demand sources.
//
// # Directional & Untuned Economics (AC-2/AC-10):
//
// The per-object operator demand ratios and the off-map contractor premiums/capacities are
// configured strictly as directional, untuned placeholders. All actual ratios remain
// completely un-hardcoded in Go source code, read dynamically from external data files
// pending full system-wide balance pass iterations.
package staffing
