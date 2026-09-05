// Package protocol defines the Engine<->UI protocol: commands, events,
// deltas, and view subscriptions. It is the seam between the UI
// process-domain and the engine domain (in-process channels v1, gRPC by
// config flag later) — the same contract in both cases (M0-ENG §1.1: "No
// shared memory between domains — channels/protocol only, even
// in-process. This is what keeps the gRPC flip a config change.").
//
// This package is neutral ground: it imports nothing from internal/engine
// or internal/ui, and nothing in either of those packages may be imported
// here. Engine and UI both depend on protocol; protocol depends on
// neither of them (M0-ENG §1.1, GDD §15).
//
// # Files
//
//   - envelope.go    — the versioned envelope: ProtocolVersion, Tick,
//     CorrelationID, Kind, Command, CommandPayload, CommandResult, ErrorRef.
//   - commands.go    — the v1 command vocabulary (typed payload structs)
//     and the Kind -> payload-factory registry that makes decoding
//     table-driven.
//   - deltas.go      — Delta: the state-patch message pushed to live view
//     subscriptions.
//   - events.go      — Event: discrete, causally-tagged occurrences
//     (distinct from the continuous Delta stream).
//   - subscription.go — the view-subscription contract: SubscriptionID
//     allocation, view-name scheme, and per-subscription Seq tracking
//     (gap detection).
//   - transport.go   — the Transport interface and its only v1
//     implementation, InProcTransport (Go channels). Comments document
//     the 1:1 gRPC mapping; no gRPC code or dependency lives here.
//   - codec.go       — JSON encode/decode (encoding/json only) of every
//     envelope type, built on the commands.go registry.
//   - entity.go      — FEAT-042: EntityID and TargetRef, the sub-entity
//     addressing pair (a ledger line, a diagram arrow) inside an
//     already-open view's patch.
//
// # FEAT-042 (additive amendment to the frozen v1 contract)
//
// FEAT-042 extends this package with entity-level drill addressing
// (EntityID/TargetRef, entity.go) and an explicit crisis signal
// (Event.Crisis, events.go). Both are purely additive: every new field is
// omitempty-tagged and every new type is exported alongside the existing
// vocabulary, never replacing it. AC-26 proves marshalling is
// byte-identical to the pre-amendment schema when the new fields are at
// their zero value; AC-28 proves ProtocolVersion stays "1.0" (an exact
// string-equality check in Command.Validate — see envelope.go — makes a
// version bump actively wrong for an additive change, not merely
// unnecessary); AC-29 proves a v1-recorded fixture with none of the new
// keys still replays cleanly under this code. Neither addition
// introduces a hand-maintained JSON wire-mirror struct (FEAT-042 AC-27) —
// if a future change ever does, that same commit must add a
// reflection-based field-parity test modeled on
// TestHeaderWireFieldsMatchHeader (internal/foundation/serialize). See
// docs/planning/acceptance/int.protocol.md's "FEAT-042" section for the
// full AC-19..AC-36 range, and its own AC-36 note that AC-1..AC-18 above
// remain the byte-for-byte v1 freeze record this amendment does not
// touch.
//
// # Rules this package must not break
//
//   - No wall-clock time anywhere (no time.Now, no time-based IDs).
//     IssuedAtTick and every Tick field are simulation time, minted by the
//     engine's phase pipeline — never the wall clock (M0-ENG §1.1: "Nothing
//     in the engine ever calls wall-clock time for logic. Simulation time
//     is the only time.").
//   - Every Command carries a non-empty CorrelationID, minted by the
//     initiating side (code.json conventions.errorHandling.correlation).
//     It is validated here, not assumed.
//   - Standard library only. No gRPC, no protobuf, no third-party codec.
//
// Module key: int.protocol (see code.json)
// Spec ref:   §15; UI-SPEC §1, §6; M0-ENG §1.1; V.2.1
// Design doc: docs/design/protocol.md (freeze-review page for INT-001)
package protocol
