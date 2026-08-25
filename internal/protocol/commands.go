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

	// Gameplay command kinds (added per the additive extension rule in the
	// header): the build-screen vocabulary ui.screen.build (FEAT-015) needs
	// to issue land purchase, zoning, construction, and demolition. Each
	// carries only command INTENT — what/where/which — so a future engine
	// module (engine.build) can act on it; accept/reject, pricing and
	// compensation are the engine's, out of this package's scope. Do NOT
	// route gameplay intent through KindDebug's Op/Args — Debug is the
	// debug-mode escape hatch (F12 panel), not a gameplay catch-all.
	KindBuy      Kind = "Buy"
	KindZone     Kind = "Zone"
	KindBuild    Kind = "Build"
	KindDemolish Kind = "Demolish"

	// KindSetFunding is F4's (ui.screen.services) per-service funding-slider
	// command (FEAT-208 increment 3, ASM-1193's own "prefer a real Kind over
	// the Debug fallback long-term" ruling, made concrete here as the first
	// instance): promotes services.set-funding off protocol.KindDebug's
	// no-op escape hatch (whose only handler, engine.core's handleDebug, is
	// a documented placeholder that never inspects Op) onto a first-class,
	// registered Kind that reaches compose.handleGameplay exactly like
	// Buy/Zone/Build/Demolish do. Added per the additive extension rule
	// above — a new Kind, not a repurposed one.
	KindSetFunding Kind = "SetFunding"
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

// CellRef addresses one map cell by its (x, y) grid coordinates. It is
// the protocol's cell-addressing primitive for the gameplay commands
// (Buy, Zone, Build, Demolish) that target a specific cell. Coordinates
// are zero-based grid offsets, matching the {x, y} cell shape the
// f1.viewport patch already uses (internal/engine/stub/viewport.go) — the
// wire shape is repeated here, not imported, because this package is
// neutral ground (doc.go) and may not depend on engine or UI types.
// Bounds (negative / out-of-grid) and ownership are NOT validated here —
// they are the receiving engine module's job, same as every other
// payload's internal invariants (Command.Validate's scope, envelope.go).
type CellRef struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// BuyPayload requests the purchase of one map cell (§7: "player buys
// before building"; §13-F3 land purchase). It carries only WHICH cell —
// the price is computed and charged by the engine (engine.finance's
// LandPrice, consumed by engine.build per its AC-9), never by this
// package, and any outcome is returned in the CommandResult, not carried
// here. A multi-cell parcel purchase is expressed as one Buy command per
// cell (the UI's choice — ui.screen.build BLD-1's "cell(s)/parcel").
type BuyPayload struct {
	Cell CellRef `json:"cell"`
}

func (BuyPayload) commandKind() Kind { return KindBuy }

// ZonePayload zones one cell into one of §34's eight land types
// (Dwelling, Shop, Office, Entertainment, Farming, Manufacturing, Heavy
// Industry, Mining). ZoneType is an opaque, engine-defined string resolved
// against engine.build's zone catalogue (loaded from buildings.json) —
// deliberately a string, not an enum, so this neutral package never has to
// know the catalogue and adding a zone type is a data edit, not a protocol
// change (same rationale as InspectEntityPayload.EntityRef and
// DebugPayload.Op).
type ZonePayload struct {
	Cell     CellRef `json:"cell"`
	ZoneType string  `json:"zoneType"`

	// Density is the FEAT-199 zoning density level the player paints:
	// 0 = engine default/unspecified (omitted on the wire), 1..5 = the
	// ladder data/zoning.json declares per zone family. Like ZoneType,
	// the per-family min/max are DATA (GR#15) — this package carries the
	// level opaquely; the receiving engine side validates it against the
	// catalogue. Additive field per this file's extension rule: a sender
	// that never sets it produces byte-identical v1 JSON.
	Density int `json:"density,omitempty"`
}

func (ZonePayload) commandKind() Kind { return KindZone }

// BuildPayload requests construction of a catalogue building on one cell
// (§13-F3 build queue). BuildingType is an opaque, engine-defined string
// resolved against engine.build's buildings.json catalogue, exactly as
// ZonePayload.ZoneType is resolved against the zone catalogue — the
// materials bill, labour and lead time are the engine's, not carried here.
type BuildPayload struct {
	Cell         CellRef `json:"cell"`
	BuildingType string  `json:"buildingType"`
}

func (BuildPayload) commandKind() Kind { return KindBuild }

// DemolishPayload requests demolition of the structure on one cell
// (§13-F3, §12's costed recovery). It carries only WHICH cell — the
// compensation figure is computed by the engine (engine.finance, per
// engine.build's AC-7) and returned in the CommandResult, not carried
// here; a Demolish against a cell with no structure is the engine's
// rejection to make, not this package's.
type DemolishPayload struct {
	Cell CellRef `json:"cell"`
}

func (DemolishPayload) commandKind() Kind { return KindDemolish }

// SetFundingPayload requests a funding-level change for one registered
// service (F4's per-service slider, §26/§54). ServiceID is an opaque,
// engine-defined identifier resolved against engine.services' registered
// instance roster — deliberately a string, not an enum, mirroring
// ZonePayload.ZoneType/BuildPayload.BuildingType's rationale exactly (this
// neutral package never has to know the service catalogue). Level is the
// engine's own [0,1] funding-level fraction (internal/engine/services/
// api.go's ServicesAPI.SetFunding contract) — the UI's display-domain
// rescale (e.g. 0-1000, a percentage) happens screen-side, before the
// command is ever built (internal/ui/screens/services/screen.go's
// normalizeFundingLevel), so this payload always carries the already-
// rescaled [0,1] fraction, never a raw slider position. Range/finite
// validation is the engine's job (ServicesAPI.SetFunding hard-rejects
// non-finite or out-of-[0,1] values), not this package's — same scope
// discipline every other payload's internal invariants follow
// (Command.Validate's doc comment).
type SetFundingPayload struct {
	ServiceID string  `json:"serviceId"`
	Level     float64 `json:"level"`
}

func (SetFundingPayload) commandKind() Kind { return KindSetFunding }

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
	KindBuy:           func() CommandPayload { return &BuyPayload{} },
	KindZone:          func() CommandPayload { return &ZonePayload{} },
	KindBuild:         func() CommandPayload { return &BuildPayload{} },
	KindDemolish:      func() CommandPayload { return &DemolishPayload{} },
	KindSetFunding:    func() CommandPayload { return &SetFundingPayload{} },
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
