// Package unlocks is the unlock-economy module (MOD-032): the §22
// four-currency progression spine — XP, the §4 thirteen-tier milestone
// ladder, Development-Point trees, and the money-only "Buy" path — with
// the gate-check query every other module (notably ui.screen.build and
// data.catalogue's unlock field) consumes.
//
// Module key: engine.unlocks (see code.json; GUID
// 50d7fc81-4770-4361-854c-8675fce10299, inbound UnlocksAPI
// 865f4351-b2c5-48fd-aa71-261c24e1ac3c).
// Spec refs: §4 (Population Scale & the Milestone Ladder); §22 (Unlock
// Economy); §23 (Expansion-Content Mapping); GR#15.
//
// # The four currencies
//
// §22 names exactly four, all data-driven per GR#15:
//
//   - XP — earned continuously from four per-source award functions
//     (construction, population, service performance, milestone
//     progress), each with its own documented placeholder rate — never a
//     single opaque gainXP(amount) called ad hoc. See xp.go.
//   - Milestones — the §4 ladder (Wilderness…Centopolis) crossed by
//     population threshold; each crossing grants its signature unlocks
//     (dynamic, see below), an expansion-permit allowance, a cash award
//     posted through engine.finance (US-7), a loan-facility uplift (this
//     API implements finance.MilestoneGate), and Development Points. The
//     ladder's thresholds and names are §4 spec data transcribed
//     verbatim — there is no §24 config file for them (see ladder.go and
//     the logged ASM).
//   - Development Points — spent in the twelve per-category progression
//     trees loaded from data/unlock_trees.json (foundation.data's
//     validated LoadUnlockTrees), so two players at the same tier own
//     different toolkits. See dp.go.
//   - Buy — off-map capacity (grid/gas/rail/port/water tranches)
//     purchased directly with money regardless of points, never touching
//     the DP balance. See buy.go.
//
// # Data-driven gate checks (GR#15)
//
// [UnlocksAPI.IsUnlocked] / [UnlocksAPI.CheckGate] resolve a [Gate]
// (milestone tier, a DP-tree node id, a bare development-point flag, and
// an achievement boolean) against the current state. Everything they
// check derives from data/unlock_trees.json — the twelve category names,
// the node ids and their DP costs and tier prerequisites — never from a
// hardcoded Go switch over node or tier names. [UnlocksAPI.CheckGate]
// is the error-returning form that distinguishes an unregistered
// reference (a typo'd node id or an out-of-range tier, returned as a
// registry-sourced error) from a genuine not-yet-unlocked result
// (returned as false), per AC-12; [UnlocksAPI.IsUnlocked] is the bool
// convenience for callers whose gates are already data-validated (e.g.
// ui.screen.build consuming data.catalogue entries).
//
// # Debug force-unlock (M0-ENG §3)
//
// [UnlocksAPI.ForceUnlock] is the debug-only cheat path (§4's own
// "port testing pre-100k" example). It is gated behind an injected
// debug authorizer and, on success, invokes an injected sticky-flag
// callback the composition root wires to feat.debugmode's
// serialize.Header.DebugTouched write — never usable silently in a
// non-debug build. See force.go.
package unlocks
