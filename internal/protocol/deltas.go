package protocol

import "encoding/json"

// Delta is the state-patch message the engine pushes for a live view
// subscription (subscription.go; UI-SPEC §6). It is the ONLY message
// type that carries continuous, high-frequency state — everything else
// (Event, CommandResult) is discrete and low-frequency by comparison,
// which is why Delta gets its own drop/stale policy on the transport
// (transport.go) instead of sharing one with events.
type Delta struct {
	// SubscriptionID identifies which live subscription this patch
	// belongs to (subscription.go). A Delta for a subscription that has
	// since been unsubscribed is a bug on the sending side — the engine
	// must stop pushing the instant Unsubscribe is processed (UI-SPEC §6).
	SubscriptionID SubscriptionID `json:"subscriptionId"`

	// Tick is the simulation tick this patch reflects. It is NOT wall
	// time (see Tick's doc comment) — it lets T-VIEWS (M0-ENG §1.1)
	// detect staleness by comparing against the engine's current tick,
	// which is how the UI-SPEC §1 "staleness dot" is driven.
	Tick Tick `json:"tick"`

	// Seq is monotonically increasing PER SUBSCRIPTION, starting at 1 for
	// the first delta pushed after a Subscribe is accepted. It exists so
	// the receiver can detect gaps (a dropped delta under the transport's
	// full-buffer policy, transport.go) independently of Tick, since
	// ticks are not necessarily 1:1 with deltas (a subscription may skip
	// producing a delta on a tick where its view didn't change).
	// SeqTracker in subscription.go implements the gap check.
	Seq uint64 `json:"seq"`

	// Patch is the view's own patch payload, opaque to this package —
	// its schema is versioned per view (view schema v1) by the engine
	// module that owns the view, not by the protocol envelope. Keeping it
	// as json.RawMessage means adding or changing a view's patch shape
	// never requires a protocol-package change; only the view schema
	// itself is versioned, independently of ProtocolVersion.
	Patch json.RawMessage `json:"patch"`

	// CorrelationID echoes the Command that caused this delta, where the
	// causality is direct and traceable (e.g. a Subscribe's very first
	// delta, or a delta produced synchronously within the same tick as a
	// command that mutated the subscribed state). Most deltas are caused
	// by the ordinary passage of simulation time (AdvanceTicks or the
	// free-running clock), not by a single command, so this is commonly
	// empty — omitted from the wire in that case. See envelope.go's
	// CorrelationID doc and docs/design/protocol.md's causality note.
	CorrelationID CorrelationID `json:"correlationId,omitempty"`
}
