// Package core is the TUI renderer core: cell-buffer diff, decoupled
// input/render loops, and capability probe. tcell screen access is
// single-goroutine (T-RENDER); this package owns that goroutine
// (M0-ENG §1.1).
//
// Per Golden Rule #20 (Contract-First, Stub-Forever), packages under
// internal/ui must consume the engine ONLY via internal/protocol —
// importing internal/engine directly from here is lint-banned
// (see .golangci.yml).
//
// Module key: ui.core (see code.json)
// Spec ref:   UI-SPEC §1, §5; M0-ENG §1.1
package core
