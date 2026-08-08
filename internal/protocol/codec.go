package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// # Deterministic marshalling
//
// This package relies on encoding/json's documented behaviour: a struct
// is marshalled to a JSON object whose key order matches the struct's
// FIELD declaration order (encoding/json does not sort or reorder
// struct fields). That means the byte-for-byte JSON output of Command,
// CommandResult, Event, and Delta is stable across runs and across
// machines for a given Go version, which is what H-REPLAY fixtures
// (MOD-013) and any hash-based fixture comparison rely on. It is NOT
// stable across map[string]string fields (Go randomizes map iteration
// order, and encoding/json sorts map keys before emitting them, so those
// ARE stable too, in fact — but do not rely on map field ordering
// matching insertion order). If a fixture hash ever needs to survive a
// Go version bump, re-verify this guarantee against that version's
// encoding/json release notes before trusting it blindly.

// ErrUnknownCommandKind is the sentinel wrapped by UnknownKindError.
// Callers that only care "was the kind unknown" can use errors.Is(err,
// ErrUnknownCommandKind); callers that want the offending Kind use
// errors.As(err, &UnknownKindError{}).
var ErrUnknownCommandKind = errors.New("protocol: unknown command kind")

// UnknownKindError is returned by DecodeCommand when the wire envelope's
// Kind is not registered in commandRegistry (commands.go). It is a typed
// error specifically so callers can distinguish "malformed JSON" from
// "well-formed envelope, unrecognized kind" (e.g. a newer UI talking to
// an older engine build) without string-matching an error message.
type UnknownKindError struct {
	Kind Kind
}

func (e *UnknownKindError) Error() string {
	return fmt.Sprintf("protocol: unknown command kind %q", e.Kind)
}

// Is makes errors.Is(err, ErrUnknownCommandKind) work for any
// *UnknownKindError.
func (e *UnknownKindError) Is(target error) bool {
	return target == ErrUnknownCommandKind
}

// wireCommand is the on-the-wire shape of a Command: identical to
// Command except Payload is raw JSON, deferred until Kind tells us which
// concrete type to decode it into (commandRegistry). Field order matches
// Command's for the determinism note above.
type wireCommand struct {
	ProtocolVersion string          `json:"protocolVersion"`
	CorrelationID   CorrelationID   `json:"correlationId"`
	IssuedAtTick    Tick            `json:"issuedAtTick"`
	Kind            Kind            `json:"kind"`
	Payload         json.RawMessage `json:"payload"`
}

// EncodeCommand marshals cmd to its wire JSON form. It does not call
// cmd.Validate() first — callers that want validation-before-encode
// should call it explicitly; EncodeCommand's job is purely serialization,
// so an intentionally-invalid Command can still be encoded (e.g. for a
// test fixture exercising the receiving side's validation).
func EncodeCommand(cmd Command) ([]byte, error) {
	return json.Marshal(cmd)
}

// DecodeCommand unmarshals data into a Command, table-driven off
// commandRegistry via wireCommand.Kind. It returns *UnknownKindError
// (wrapping ErrUnknownCommandKind) for an unrecognized Kind, never a
// panic — decoding attacker- or network-supplied bytes must always fail
// as a typed error. It does NOT call Command.Validate(); callers that
// need envelope-level guarantees (non-empty CorrelationID, matching
// ProtocolVersion) call Validate() on the result themselves, so
// DecodeCommand stays usable for fixtures/tests that intentionally probe
// invalid envelopes.
func DecodeCommand(data []byte) (Command, error) {
	var wire wireCommand
	if err := json.Unmarshal(data, &wire); err != nil {
		return Command{}, fmt.Errorf("protocol: decode command envelope: %w", err)
	}

	factory, known := commandRegistry[wire.Kind]
	if !known {
		return Command{}, &UnknownKindError{Kind: wire.Kind}
	}

	payload := factory()
	if len(wire.Payload) > 0 {
		if err := json.Unmarshal(wire.Payload, payload); err != nil {
			return Command{}, fmt.Errorf("protocol: decode payload for kind %q: %w", wire.Kind, err)
		}
	}
	// payload is a pointer (see commandRegistry's doc comment); dereference
	// so Command.Payload holds the same value-typed CommandPayload a
	// caller constructing a Command by hand would use.
	value := derefCommandPayload(payload)

	return Command{
		ProtocolVersion: wire.ProtocolVersion,
		CorrelationID:   wire.CorrelationID,
		IssuedAtTick:    wire.IssuedAtTick,
		Kind:            wire.Kind,
		Payload:         value,
	}, nil
}

// derefCommandPayload dereferences the pointer commandRegistry factories
// return into the value type stored on Command.Payload. Implemented as a
// type switch (rather than reflection) so it stays a compile-time
// exhaustiveness check: adding a new Kind to commands.go without adding
// its case here is a bug caught by DecodeCommand's round-trip tests, not
// a silent reflection fallback.
func derefCommandPayload(p CommandPayload) CommandPayload {
	switch v := p.(type) {
	case *AdvanceTicksPayload:
		return *v
	case *SetSpeedPayload:
		return *v
	case *PausePayload:
		return *v
	case *ResumePayload:
		return *v
	case *SubscribePayload:
		return *v
	case *UnsubscribePayload:
		return *v
	case *InspectEntityPayload:
		return *v
	case *DebugPayload:
		return *v
	default:
		// Unreachable as long as commandRegistry and this switch are kept
		// in sync (codec_test.go's TestKnownKindsRoundTrip catches drift).
		// Returning p unchanged (still the pointer) is safer than a panic
		// in library code that decodes untrusted input.
		return p
	}
}

// EncodeCommandResult, EncodeEvent, and EncodeDelta marshal their
// argument directly: none of these three types has a polymorphic field,
// so encoding/json's default struct marshalling is sufficient and no
// wire-shadow type is needed (contrast wireCommand above, which exists
// solely to defer Payload's decode).
func EncodeCommandResult(r CommandResult) ([]byte, error) { return json.Marshal(r) }
func EncodeEvent(e Event) ([]byte, error)                 { return json.Marshal(e) }
func EncodeDelta(d Delta) ([]byte, error)                 { return json.Marshal(d) }

// DecodeCommandResult, DecodeEvent, and DecodeDelta are the matching
// decoders. Like DecodeCommand, they do not call their type's Validate()
// method — callers opt into envelope validation explicitly.
func DecodeCommandResult(data []byte) (CommandResult, error) {
	var r CommandResult
	if err := json.Unmarshal(data, &r); err != nil {
		return CommandResult{}, fmt.Errorf("protocol: decode command result: %w", err)
	}
	return r, nil
}

func DecodeEvent(data []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return Event{}, fmt.Errorf("protocol: decode event: %w", err)
	}
	return e, nil
}

func DecodeDelta(data []byte) (Delta, error) {
	var d Delta
	if err := json.Unmarshal(data, &d); err != nil {
		return Delta{}, fmt.Errorf("protocol: decode delta: %w", err)
	}
	return d, nil
}
