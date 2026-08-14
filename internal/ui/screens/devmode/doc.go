// Package devmode implements the UI half of FEAT-065 (feat.devmode): a
// pause-anywhere dev console overlay, an object-metrics inspection
// surface, and in-game feedback submission — all gated exclusively
// through feat.debugmode's (internal/engine/debug, FEAT-008/FEAT-035)
// existing capability seams.
//
// Module key: feat.devmode (see code.json; GUID 7d49d8f6-f29e-48fe-af76-e67d6b15f501)
// Spec refs:  §14 (docs/METROPOLIS-MASTER-v2.1.md lines 257-260);
//
//	M0-ENG §3; docs/planning/acceptance/feat.devmode.md (this
//	package's own criteria, AC-DM1..AC-DM17); docs/planning/
//	acceptance/feat.debugmode.md (FEAT-008/FEAT-035 — the package
//	this one is a thin consumer of, never a second implementation
//	of).
//
// # A thin consumer, never a second gate
//
// This package owns NO debug-on/off state of its own. Every capability
// (opening the console, inspecting an entity, submitting feedback) is
// wired at construction time (Option functions, console.go) to a
// caller-supplied closure over the real *debug.State — RequireConsole,
// InspectEntity, SubmitFeedback, Enable. Console.Open, Console.Inspect,
// and Console.SubmitFeedback never decide "is debug on" themselves; they
// only call the wired seam and act on its answer. A future contributor
// adding a devmode-local `debugOn bool` field, or any other locally
// cached copy of debug state, would silently diverge from
// debug.State.IsOn() the moment debug is toggled mid-session — exactly
// the failure shape feat.debugmode's own doc.go and feat.devmode.md's
// header warn against. Don't do that; wire another Option instead.
//
// # Why this package never imports internal/engine directly
//
// GR#20 (Contract-First, Stub-Forever) bans internal/ui -> internal/engine
// imports outright (see .golangci.yml's ui-must-not-import-engine
// depguard rule) — the exact same discipline ui.screen.debug (FEAT-007,
// internal/ui/screens/debug) already follows via its own DebugFlagFunc
// seam. This package follows the identical pattern: every debug.State
// method it needs is captured as a plain func value (RequireConsoleFunc,
// EnableFunc, InspectFunc, SubmitFeedbackFunc, PauseFunc, IsPausedFunc,
// DebugTouchedFunc) and injected by whatever composition root
// (cmd/metropolis, or a test) has a real *debug.State to close over —
// this package's own production source never names the debug package.
// _test.go files ARE exempt from the depguard rule (golangci-lint v2
// config, see .golangci.yml's own comment on why) and this package's
// tests take advantage of that to wire real *debug.State values rather
// than hand-rolled fakes, so what's proven here is proven against the
// real gate, not a look-alike.
//
// # Reachability boundary (AC-DM12/AC-DM13)
//
// Every exported action on Console (Open, Inspect, SubmitFeedback)
// either calls into the wired RequireConsoleFunc seam itself or first
// requires Console.Open to have already done so (Console.open tracks
// this; Inspect/SubmitFeedback both reject with ErrConsoleNotOpen,
// MET-U401, before ever touching their own wired seam) — there is no
// exported path into this package that bypasses the gate. Wiring this
// package into the real cmd/metropolis binary, and proving a release
// build (no --debug, no config, no :debug on) rejects all three actions
// end-to-end (AC-DM13), is composition-root work outside this package's
// own file ownership for FEAT-065's dispatch — see the FEAT-065 dispatch
// report's "left undone" section, which escalates this exactly the way
// FEAT-035's own AC-E5 escalated its analogous headless-vs-interactive
// wiring split.
//
// # Determinism (GR#21, AC-DM16)
//
// This package never reads the wall clock (grep -rn "time.Now"
// internal/ui/screens/devmode/*.go, excluding _test.go, returns no
// matches) and never itself advances or perturbs simulation state; the
// pause action is delegated entirely to the wired PauseFunc seam
// (engine.core's own existing pause command, via whatever Transport the
// composition root wires in), and feedback timestamps come from
// feat.debugmode's own injected Clock (feedback.go), never from this
// package.
package devmode
