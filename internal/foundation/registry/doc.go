// Package registry is the module registry: real/stub/off status, health,
// and CanToggle — the single mechanism the engine uses to boot (M0-ENG §2
// module stubbing) and that the F12 info panel displays and toggles
// (M0-ENG §3). One registry, two consumers.
//
// Module key: foundation.registry (see code.json)
// Spec ref:   M0-ENG §2; M0-ENG §3
package registry
