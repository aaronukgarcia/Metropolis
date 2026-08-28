// Package viewgate is a build-time (go test) source-scanning gate that
// enforces the "one view registry" doctrine mechanically (FEAT-231
// increment V1).
//
// The doctrine: every view a screen or tile subscribes to / drills to
// MUST be a member of the single registered set the composition root
// publishes — compose.viewRegistrationOrder (RegisteredViewNames). Today
// that set is enforced only at RUNTIME (engine/core's Subscribe rejects
// an unregistered ViewName, and a rejected Subscribe silently renders a
// blank screen — the project's dominant "built but not wired" defect
// class). This package adds a STATIC gate that fails CI: an unregistered
// screen-scoped view-name string literal anywhere under internal/ or cmd/
// fails `go test`, before it can ship as a blank screen.
//
// It is deliberately its OWN package (never `compose`): it is a pure
// source scanner that recovers the registered set by PARSING compose's
// source (viewRegistrationOrder + its backing string consts) rather than
// importing compose at runtime, so it introduces no engine-layer import
// cycle and does not collide with tests a parallel lane may be adding to
// the compose package itself. It mirrors internal/foundation/errs's
// source_scan_test.go (the BUG-008 MET-code registry gate) in shape.
//
// The single non-test file in this package is this doc.go: the gate
// itself, its pure helpers, and its negative controls all live in
// viewgate_test.go, and the accepted (known-unregistered, pending-wiring)
// view names live in the accepted-views.json ratchet beside it.
package viewgate
