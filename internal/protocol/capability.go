package protocol

// capability.go — FEAT-1972079936 Phase 0 increment 3 (AC-5): the
// fine-grained capability registry + enforcement lookup.
//
// # Fine-grained, per Aaron ruling 3 (2026-08-31)
//
// The acceptance doc's open question 3 asked whether capabilities should be
// coarse (one flag per feature AREA, e.g. "finance.v2") or fine-grained
// (one flag per individual feature/behaviour), noting it assumed coarse
// pending Aaron's call. Aaron ruled 3: fine-grained -- one flag per
// individual feature, for precise per-connection interop, at the (accepted)
// cost of more bookkeeping than a coarse scheme. This file's registry is
// deliberately a flat set of single-feature tokens, never a
// version-as-featureset shorthand.
//
// # Increments 1/2 built the mechanism; increment 3 is the first real GATE
//
// protocol.IntersectCapabilities (wireversion.go) and the handshake's
// Capabilities field (wsserver/server.go) already computed the negotiated
// (intersected) set from day one -- but nothing actually consulted it to
// refuse a feature. This file is the first ENFORCEMENT point: a command
// Kind can be registered here as requiring a named capability, and
// wsserver's handleCommand (server.go) refuses any such command on a
// connection whose negotiated set does not contain it (MET-P021,
// ErrCapabilityRequired, codes.go), rather than silently serving it or
// relying on the client to have gated itself.
type Capability = string

const (
	// CapDebugCommands gates KindDebug (commands.go): an operator/
	// diagnostic command. Phase 0 has no real production feature that
	// needs gating yet (the acceptance doc's own note: "Phase 0 has no new
	// features gated behind one -- it needs the mechanism"), so this is
	// the ONE illustrative gated Kind this increment ships, proving the
	// enforcement mechanism end-to-end (AC-5's mutation: a client lacking
	// this capability cannot invoke KindDebug; one with it can) without
	// inventing a real feature Phase 0 has no other use for.
	CapDebugCommands Capability = "debug.commands"
)

// kindCapabilityRequirements maps a Command Kind to the single Capability
// token required to invoke it. A Kind absent from this map requires no
// capability at all (the common case -- every pre-existing Kind from
// before Phase 0 keeps working unconditionally). A future phase's real
// gated feature adds a new entry here, never restructures this map's
// shape (mirrors shimRegistry's own "grow, don't restructure" convention,
// wsserver/shim.go).
var kindCapabilityRequirements = map[Kind]Capability{
	KindDebug: CapDebugCommands,
}

// RequiredCapability returns the capability token Kind k requires to be
// invoked, and whether one is required at all. ok is false for every Kind
// not present in kindCapabilityRequirements -- "no capability required,"
// not "capability requirement unknown."
func RequiredCapability(k Kind) (capability Capability, ok bool) {
	capability, ok = kindCapabilityRequirements[k]
	return capability, ok
}

// HasCapability reports whether capability is present in the negotiated
// set. A nil or empty negotiated set correctly reports false for every
// capability (AC-5's empty-intersection case: a client that declared no
// capabilities, or whose declared set shares nothing with the server's,
// gets access to nothing capability-gated).
func HasCapability(negotiated []string, capability string) bool {
	for _, c := range negotiated {
		if c == capability {
			return true
		}
	}
	return false
}
