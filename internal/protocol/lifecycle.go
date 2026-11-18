package protocol

import (
	"encoding/json"
	"fmt"
)

// lifecycle.go -- FEAT-9995385597 increment 1, section A (AC-P1..AC-P3):
// the server lifecycle notifications server.waking, server.draining and
// server.hibernating, their params types, and the capability tokens that
// gate them. Contract first (GR#20): this file only DEFINES the wire shape;
// the server emitting them (waking order, SIGTERM drain, hibernate) lands in
// later increments, and the TS mirror lives in webconsole/src/sim/wire.ts.
//
// # No wire-version bump (AC-P1, ASM-1599)
//
// CurrentWireVersion stays 1.0. Every new behaviour is opt-in per
// connection through the three capability flags below: a client that does
// not negotiate lifecycle.X keeps today's handshake order and never
// receives an unknown method (AC-P3), so nothing here can break an older
// client and a version bump would be noise. The flags are fine-grained per
// Aaron ruling 3 (see capability.go).
//
// # Determinism (GR#21)
//
// Nothing in this file is read by engine code. ReconnectAfterMs is a
// host-side hint for the browser's timer; wall-clock never reaches the
// engine.

// Capability tokens for the lifecycle notifications (AC-P1).
const (
	// CapLifecycleWaking: the server may send server.waking and replies to
	// the handshake before the city has loaded.
	CapLifecycleWaking Capability = "lifecycle.waking"
	// CapLifecycleDraining: the server may send server.draining.
	CapLifecycleDraining Capability = "lifecycle.draining"
	// CapLifecycleHibernating: the server may send server.hibernating.
	CapLifecycleHibernating Capability = "lifecycle.hibernating"
	// CapCommandDedup: the server dedups commands on (clientSessionId,
	// correlationId) so a client may safely re-send an in-flight command
	// (AC-R9). Declared by the server ONLY once FEAT-9995385594 dedup is
	// live: it must NOT be in wsserver's default capability set in this
	// increment (asserted by a wsserver test).
	CapCommandDedup Capability = "command.dedup"
)

// Lifecycle notification method names (no-id JSON-RPC notifications, the
// same framing as the result/event/delta methods in wsserver).
const (
	MethodServerWaking      = "server.waking"
	MethodServerDraining    = "server.draining"
	MethodServerHibernating = "server.hibernating"
)

// LifecycleMethodCapability maps a lifecycle notification method to the
// capability a connection must have negotiated for the server to send it
// (AC-P3). ok is false for any other method.
func LifecycleMethodCapability(method string) (c Capability, ok bool) {
	switch method {
	case MethodServerWaking:
		return CapLifecycleWaking, true
	case MethodServerDraining:
		return CapLifecycleDraining, true
	case MethodServerHibernating:
		return CapLifecycleHibernating, true
	}
	return "", false
}

// WakePhase is one step of a city wake (AC-W4). The set is closed and
// ordered; only WakeQueued (first) and WakeReady (last) are mandatory, the
// intermediate phases are reported only where the loader can observe them.
// WakeFailed is the terminal failure phase (AC-W6).
type WakePhase string

// The closed set of wake phases.
const (
	WakeQueued    WakePhase = "queued"
	WakeFetching  WakePhase = "fetching"
	WakeDecoding  WakePhase = "decoding"
	WakeIndexing  WakePhase = "indexing"
	WakeReplaying WakePhase = "replaying"
	WakeReady     WakePhase = "ready"
	WakeFailed    WakePhase = "failed"
)

// DrainReason says why the server is draining (AC-P2). A meter stop is a
// drain, never a hibernation. "hidden" is NOT a server reason: the Page
// Visibility disconnect is a client-initiated close with no server frame.
type DrainReason string

// The closed set of drain reasons.
const (
	DrainDeploy   DrainReason = "deploy"
	DrainShutdown DrainReason = "shutdown"
	DrainLimit    DrainReason = "limit"
	DrainBudget   DrainReason = "budget"
)

// HibernateReason says why the server is hibernating the city (AC-P2).
// "operator" is reserved for a future operator command.
type HibernateReason string

// The closed set of hibernate reasons.
const (
	HibernateIdle     HibernateReason = "idle"
	HibernateOperator HibernateReason = "operator"
)

// WakingParams is server.waking's params (AC-P2). Code and Ctx are only
// meaningful with phase "failed": Code is the registry code (for example
// MET-P030) and Ctx its context. Pct is 0..100 and monotonic
// non-decreasing within one wake.
type WakingParams struct {
	Phase WakePhase      `json:"phase"`
	Pct   int            `json:"pct"`
	Code  string         `json:"code,omitempty"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}

// DrainingParams is server.draining's params (AC-P2). ReconnectAfterMs is a
// floor for the client's next reconnect attempt (AC-D4, AC-R2).
type DrainingParams struct {
	ReconnectAfterMs int         `json:"reconnectAfterMs"`
	Reason           DrainReason `json:"reason"`
}

// HibernatingParams is server.hibernating's params (AC-P2).
type HibernatingParams struct {
	Reason HibernateReason `json:"reason"`
}

// Validate reports whether p is a well-formed waking message.
func (p WakingParams) Validate() error {
	switch p.Phase {
	case WakeQueued, WakeFetching, WakeDecoding, WakeIndexing, WakeReplaying, WakeReady, WakeFailed:
	default:
		return fmt.Errorf("protocol: server.waking phase %q is not one of the closed set", p.Phase)
	}
	if p.Pct < 0 || p.Pct > 100 {
		return fmt.Errorf("protocol: server.waking pct %d out of range 0..100", p.Pct)
	}
	if p.Phase == WakeReady && p.Pct != 100 {
		return fmt.Errorf("protocol: server.waking ready must carry pct 100, got %d", p.Pct)
	}
	return nil
}

// Validate reports whether p is a well-formed draining message.
func (p DrainingParams) Validate() error {
	if p.ReconnectAfterMs < 0 {
		return fmt.Errorf("protocol: server.draining reconnectAfterMs %d is negative", p.ReconnectAfterMs)
	}
	switch p.Reason {
	case DrainDeploy, DrainShutdown, DrainLimit, DrainBudget:
		return nil
	}
	return fmt.Errorf("protocol: server.draining reason %q is not one of deploy|shutdown|limit|budget", p.Reason)
}

// Validate reports whether p is a well-formed hibernating message.
func (p HibernatingParams) Validate() error {
	switch p.Reason {
	case HibernateIdle, HibernateOperator:
		return nil
	}
	return fmt.Errorf("protocol: server.hibernating reason %q is not one of idle|operator", p.Reason)
}

// DecodeWakingParams decodes and validates a server.waking params object.
// Unknown fields are tolerated (forward compatibility, like every other
// wire struct in this package); a type mismatch or invalid value is an error.
func DecodeWakingParams(raw []byte) (WakingParams, error) {
	var p WakingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return WakingParams{}, fmt.Errorf("protocol: decode server.waking params: %w", err)
	}
	return p, p.Validate()
}

// DecodeDrainingParams decodes and validates a server.draining params object.
func DecodeDrainingParams(raw []byte) (DrainingParams, error) {
	var p DrainingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return DrainingParams{}, fmt.Errorf("protocol: decode server.draining params: %w", err)
	}
	return p, p.Validate()
}

// DecodeHibernatingParams decodes and validates a server.hibernating params object.
func DecodeHibernatingParams(raw []byte) (HibernatingParams, error) {
	var p HibernatingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return HibernatingParams{}, fmt.Errorf("protocol: decode server.hibernating params: %w", err)
	}
	return p, p.Validate()
}
