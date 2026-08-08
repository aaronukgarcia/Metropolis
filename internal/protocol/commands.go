package protocol

// This file is the v1 command vocabulary: one Kind constant and one
// payload struct per command, plus the registry that makes decoding
// table-driven (codec.go). The set below is deliberately the
// skeleton-era minimum — enough for H-STUB, H-REPLAY, and the TUI core
// (MOD-008/009/012/013) to have something real to speak against. It is
// NOT the final gameplay vocabulary (no BuyLand, no SetBudget yet); those
// arrive as their owning engine modules go real, following the extension
// rule below.
//
// # Extension rule (v1.1 and beyond, additive)
//
// Adding a new command in a MINOR bump:
//  1. Add a new Kind constant below, named exactly like the command
//     (PascalCase, matching the payload struct name minus "Payload").
//  2. Add its payload struct, implementing CommandPayload via a
//     commandKind() method that returns the new constant.
//  3. Register it in commandRegistry.
//  4. Do NOT remove or renumber existing Kind constants or reuse a
//     retired one's string value — old fixtures and replay logs (H-REPLAY,
//     MOD-013) must keep decoding.
// A command whose *meaning* changes incompatibly (not just gains fields)
// is a new Kind (e.g. "AdvanceTicksV2"), never a silent redefinition of
// an existing one — see docs/design/protocol.md.
//
// Adding a FIELD to an existing payload struct is additive and safe as
// long as it has a JSON zero value that means "old behaviour" (Go's
// encoding/json already gives missing fields their zero value on
// decode); removing or repurposing a field is a breaking change and
// needs a new Kind by the same rule.

// Command Kind constants for the v1 vocabulary.
const (
	KindAdvanceTicks  Kind = "AdvanceTicks"
	KindSetSpeed      Kind = "SetSpeed"
	KindPause         Kind = "Pause"
	KindResume        Kind = "Resume"
	KindSubscribe     Kind = "Subscribe"
	KindUnsubscribe   Kind = "Unsubscribe"
	KindInspectEntity Kind = "InspectEntity"
	KindDebug         Kind = "Debug"
)

// AdvanceTicksPayload requests the engine advance the simulation by N
// ticks (GDD §3's logistics day-ticks) before yielding control back —
// used by headless harnesses (H-SYNTH, H-REPLAY) and by the debug
// single-step control. N must be positive; the engine, not this package,
// enforces that (see Command.Validate's doc comment on scope).
type AdvanceTicksPayload struct {
	N int64 `json:"n"`
}

func (AdvanceTicksPayload) commandKind() Kind { return KindAdvanceTicks }

// SetSpeedPayload sets the running simulation speed multiplier (GDD §3:
// 1/2/3, plus 8 in debug builds). Pausing is its own command (Pause), not
// Speed=0, because Pause/Resume is a distinct, more urgent control path
// (e.g. bound directly to Space in UI-SPEC §3) that must not be confused
// with "speed zero."
type SetSpeedPayload struct {
	Speed int `json:"speed"`
}

func (SetSpeedPayload) commandKind() Kind { return KindSetSpeed }

// PausePayload pauses the simulation clock. Carries no fields — Pause is
// idempotent (pausing an already-paused world is a no-op Accept, not an
// error).
type PausePayload struct{}

func (PausePayload) commandKind() Kind { return KindPause }

// ResumePayload resumes the simulation clock at its previously set speed.
// Carries no fields, idempotent, mirrors PausePayload.
type ResumePayload struct{}

func (ResumePayload) commandKind() Kind { return KindResume }

// SubscribePayload opens a view subscription: from this point on, while
// the subscription is live, the engine pushes Delta messages carrying
// that view's patches (subscription.go; UI-SPEC §6). ViewName follows the
// naming scheme documented in subscription.go. Params are
// view-defined key/value strings (e.g. a viewport's origin/extent, a
// junction ID) — kept as strings, not a typed union, so new views never
// require a protocol change to add a parameter.
type SubscribePayload struct {
	ViewName string            `json:"viewName"`
	Params   map[string]string `json:"params,omitempty"`
}

func (SubscribePayload) commandKind() Kind { return KindSubscribe }

// UnsubscribePayload closes a previously opened subscription by ID. Once
// acknowledged (CommandResult.Accepted), the engine stops computing and
// pushing deltas for it — "deltas flow only for live subscriptions"
// (UI-SPEC §6) applies the instant Unsubscribe is processed, not when the
// UI stops reading them.
type UnsubscribePayload struct {
	SubscriptionID SubscriptionID `json:"subscriptionId"`
}

func (UnsubscribePayload) commandKind() Kind { return KindUnsubscribe }

// InspectEntityPayload requests the engine life-write / detail-resolve a
// single entity (GDD §5.2's cold-citizen reconstruction is the canonical
// example, but any inspectable entity — road, building, junction — uses
// the same command). EntityRef is an opaque, engine-defined reference
// string (e.g. a typed ID like "citizen:482913" or "junction:14"); this
// package does not parse or validate its internal structure.
type InspectEntityPayload struct {
	EntityRef string `json:"entityRef"`
}

func (InspectEntityPayload) commandKind() Kind { return KindInspectEntity }

// DebugPayload is the escape hatch for debug-mode-only operations (GDD
// M0-ENG §3's F12 info panel: force-unlock a milestone tier, toggle a
// module's stub/real registry flag, force a determinism-gate snapshot,
// etc.). Op names the operation; Args are op-defined key/value strings.
// Debug commands are still versioned, correlated, and registry-error-able
// like any other command — "debug" describes what they DO, not a
// relaxation of the envelope contract.
type DebugPayload struct {
	Op   string            `json:"op"`
	Args map[string]string `json:"args,omitempty"`
}

func (DebugPayload) commandKind() Kind { return KindDebug }

// commandRegistry maps every known Kind to a factory that returns a
// fresh, zero-valued pointer to that Kind's payload type. codec.go uses
// it to make decoding table-driven: an unrecognized Kind is a typed
// UnknownKindError, never a panic or a silent zero-value command.
//
// The factory returns a pointer (*T) rather than T so encoding/json has
// an addressable value to unmarshal into; DecodeCommand dereferences it
// before storing it in Command.Payload, so Command.Payload always holds
// a value type (T), matching the struct literals callers construct by
// hand (e.g. Command{Payload: AdvanceTicksPayload{N: 10}, ...}).
var commandRegistry = map[Kind]func() CommandPayload{
	KindAdvanceTicks:  func() CommandPayload { return &AdvanceTicksPayload{} },
	KindSetSpeed:      func() CommandPayload { return &SetSpeedPayload{} },
	KindPause:         func() CommandPayload { return &PausePayload{} },
	KindResume:        func() CommandPayload { return &ResumePayload{} },
	KindSubscribe:     func() CommandPayload { return &SubscribePayload{} },
	KindUnsubscribe:   func() CommandPayload { return &UnsubscribePayload{} },
	KindInspectEntity: func() CommandPayload { return &InspectEntityPayload{} },
	KindDebug:         func() CommandPayload { return &DebugPayload{} },
}

// KnownKinds returns the registered command Kinds, for diagnostics and
// tests (e.g. asserting every Kind round-trips). Order is unspecified.
func KnownKinds() []Kind {
	kinds := make([]Kind, 0, len(commandRegistry))
	for k := range commandRegistry {
		kinds = append(kinds, k)
	}
	return kinds
}
