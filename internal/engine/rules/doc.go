// Package rules — Metropolis module engine.rules
// GUID 1846fbfe-0500-440e-ba52-2df435a6acd2 · FEAT-9995385612 · spec docs/planning/acceptance/feat-9995385612-rule-versions.md
//
// Package rules is the engine's rule-version registry: one integer that
// says which rules a player's COMMANDS were judged by, so that a save made
// under old rules replays under those rules while new play gets the new
// ones. It is a leaf package: it imports nothing from the engine.
//
// # What gets a rule version (Aaron's ruling, 2026-11-18)
//
// A rule version is recorded ONLY for a change to what a player's command
// does: whether the command is accepted or refused, and what it costs.
// Examples: the duplicate-build refusal (version 1, BUG-1224), density
// stacking (a later version). It is NOT recorded for the daily simulation formulas (the formulas the
// simulation runs every day): migration weights, tax, charges, prices and
// the like. Those simply replace the old formulas; an old city restores
// from its latest snapshot and carries on under the new formulas. Never
// add a registry entry, a constant or a version branch for a daily
// formula. If a change does not alter whether a command is accepted or
// what it costs, it is not a rule version.
//
// # How a rule change is made
//
//  1. Add one [Rule] to the registry (registry.go) and one named [Version]
//     constant for it (rules.go). Number is the next integer; [Current]
//     becomes that number.
//  2. At the single place where the command's behaviour changes, branch
//     with [Has]: if rules.Has(active, rules.TheConstant) { new } else { old }.
//     Keep the old branch. The second argument is always a named constant,
//     never an integer literal (the registry tests scan internal/ and cmd/
//     and fail on a literal).
//  3. Obtain active through [Active]; a module with no getter gets
//     [Current]. Do not read [Current] directly outside the composition,
//     save/restore, replay and command-line code (registry test).
//  4. Record an old-journal fixture at Number-1 (the registry entry names
//     its path) and mark the entry [StatusWired].
//
// An entry is [StatusPendingWiring] from the moment it is registered until
// a call site uses its constant. The registry tests tolerate an unused
// constant only while the entry is pending-wiring and names the item that
// will wire it; they fail once a call site exists but the entry still says
// pending, and fail once the entry says wired but no call site exists.
//
// # Versions in saves
//
// Version 0 is "before this feature": every journal and snapshot recorded
// before it carries no version and means 0. A version this build does not
// know (greater than [Current]) is refused, never clamped: see [Classify]
// and the registry error codes in errors.go (MET-G5610 to MET-G5615).
package rules
