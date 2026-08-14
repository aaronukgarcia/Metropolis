// Package registry is the module registry: real/stub/off status, health,
// and CanToggle — the single mechanism the engine uses to boot (M0-ENG §2
// module stubbing) and that the F12 info panel displays and toggles
// (M0-ENG §3). One registry, two consumers: engine.core's boot loop reads
// it to decide which implementation (real or Stub) backs each module key,
// and ui.screen.debug reads/writes the exact same rows to render F12's
// module table and drive its guarded toggles. Nothing forks a second copy
// of this state for a new consumer — it reads this package.
//
// # Mandatory Stub pairing
//
// M0-ENG §2: "every simulation module registers behind an interface with
// a mandatory Stub implementation" — so the engine can boot with any
// real/stub mix from month one, and modules go real one at a time in BOW
// seq order without ever blocking the walking skeleton. Register enforces
// this at runtime: a nil stub is rejected with a registry-sourced error
// (Golden Rule #20), never silently accepted and never a panic.
//
// # Status vs. health
//
// Status (real/stub/off) and health (ok/degraded/error) are orthogonal
// fields (AC-6): a module can be status:real and health:degraded at the
// same time (e.g. a real implementation that is up but failing its own
// internal checks). Status changes only via the guarded toggle
// (Registry.SetStatus), gated on the entry's CanToggle bool declared at
// registration time and a confirm token supplied by the caller — the F12
// UI is expected to obtain that confirmation from the user before
// calling. A successful toggle fires the registry's toggle hook exactly
// once, which is the wiring point for the "Crime module → STUB" ticker
// event (M0-ENG §3) and any other world-event consumer.
//
// # Tick cost ownership
//
// The registry stores each module's last-tick cost (µs) and a 60-sample
// ring buffer for the F12 sparkline; it does not compute or schedule
// anything. engine.core's phase pipeline calls RecordTickCost once per
// module per tick — the registry only stores what it is told.
//
// # Determinism
//
// All listing APIs (List, BootOrder) return entries in a stable order —
// List sorted by key (AC-10), BootOrder in registration order (AC-16) —
// never Go map iteration order (Golden Rule #21). The package holds no
// wall-clock dependency at all: no boot, status, health, or cost value is
// ever a function of time.Now.
//
// Module key: foundation.registry (see code.json; GUID d6460761-184d-4bee-aac7-5f1408242a0c)
// Spec ref:   M0-ENG §2; M0-ENG §3
package registry
