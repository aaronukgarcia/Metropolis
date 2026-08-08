// Package protocol defines the Engine<->UI protocol: commands, events,
// deltas, and view subscriptions. It is the seam between the UI
// process-domain and the engine domain (in-process channels v1, gRPC by
// config flag later) — the same contract in both cases.
//
// Module key: int.protocol (see code.json)
// Spec ref:   §15; UI-SPEC §6; V.2.1
package protocol
