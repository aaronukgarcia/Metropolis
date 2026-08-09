// Package debug implements feat.debugmode (FEAT-008): the runtime
// debug-mode switch and its unlock set (§14 Debug Mode; M0-ENG §3
// "Debug mode & the Info Panel — a first-class feature").
//
// Module key: feat.debugmode (see code.json)
// Spec ref:   §14 (docs/METROPOLIS-MASTER-v2.1.md lines 257-260);
//
//	M0-ENG §3 (docs/METROPOLIS-MASTER-v2.1.md lines 853-865)
//
// # Debug is a runtime switch, not a build flavour
//
// Release builds always carry this package; a freshly constructed State
// (NewState, no options) defaults to off — AC-2. Three enable paths
// converge on the same State.IsOn() read: the --debug flag, config, and
// the `:debug on` palette command, distinguished only by the
// EnableSource passed to Enable (state.go) — AC-1. Wiring the actual
// flag/config/palette parsing to Enable is a caller concern (cmd/metctl,
// a future config loader, ui.core's palette) outside this package's
// scope; this package only guarantees the three paths land in the same
// place once they call Enable.
//
// # The sticky DebugTouched contract — READ THIS BEFORE TOUCHING Enable/Disable
//
// Enabling debug at any point in a session sticky-flags the active save
// header via int.serializer's Header.TouchDebug() (AC-3): once a save
// has been debug-touched, M0-ENG §3 requires it stay flagged FOREVER —
// specifically so a debug-touched save can never silently re-enter clean
// balance data. This is this package's entire reason for existing
// (AC-15). Disable therefore, BY DESIGN, never calls any Header method
// (AC-4) — it only flips the in-process IsOn() bit back off. A future
// contributor "fixing" Disable to also clear DebugTouched, or adding any
// other path that clears it, would silently reopen the exact hygiene
// hole this package exists to close. Header itself enforces the
// direction at the type level too (TouchDebug/MergeDebugTouched only
// ever OR true in — see internal/foundation/serialize/header.go) but the
// invariant is restated here because it is the single most important
// fact about this package.
//
// # AC-12: Enable must not lie about success
//
// If the sticky flag's write-through fails to persist (the injected
// PersistFunc reports an error), Enable returns a registry-sourced error
// and IsOn() stays false. Reporting "debug on" while the sticky flag
// silently failed to record would itself be the hygiene violation this
// package exists to prevent — so the enable path is refused, not
// downgraded to a warning.
//
// Note the asymmetry this produces deliberately: Header.TouchDebug()
// itself is still called, in memory, before the persist check runs
// (state.go's Enable), so the header's in-process DebugTouched bit ends
// up true even on this failure path, while State.on never flips and the
// session stays off. That is the correct bias, not a bug: a save
// wrongly marked debug-touched is a nuisance (a false positive); a
// debug-touched save wrongly marked clean corrupts balance data
// permanently (a false negative). When in doubt about which way an
// enable-path failure should lean, lean toward over-flagging.
//
// # Everything else is a gate over an injected seam
//
// This package does not own cheats' domain effects, entity resolution,
// the fidelity dial's real radius/cost model, the console, or fixture
// record/replay — every one of those is out of scope here (owned
// respectively by engine.finance / engine.unlocks (MOD-032, not yet
// built), a future world/rendering module, ui.core, and
// harness.replay/MOD-013 (Sprint 2, not yet built)), and none of them
// are wired into engine.core's command dispatch yet either (see
// commands.go's handleDebug TODO). What this package guarantees: with
// debug off, every one of those capabilities is rejected with a clear
// registry-sourced error, never a silent no-op and never a panic
// (AC-9/AC-11); with debug on, the caller-supplied implementation is
// invoked/exposed, and for cheats, audited (AC-6).
//
// # Determinism (GR#21, AC-13, AC-14)
//
// Nothing in this package reads the wall clock directly (grep for the
// stdlib wall-clock call across internal/engine/debug/*.go, excluding
// _test.go, returns no matches); the one legitimate timestamp this
// package produces — a
// cheat-used audit entry — is stamped via the injectable Clock
// (WithClock), exactly like foundation/errs.SetClock. Toggling debug
// on/off, and the act of invoking InvokeCheat itself, never touch any
// tick/world state this package doesn't own — only a cheat's own
// injected CheatEffect legitimately changes state, and that change is
// always logged (AC-6), never retroactive to already-committed history
// (AC-13).
package debug
