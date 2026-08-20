package services

import "fmt"

// Registry error codes for ui.screen.services (FEAT-016). This package
// claims V500-V599 (data/errors.json's "V500-V599" reserved-range entry,
// auto-claimed via `node tools/plan/add-error.js claim-range
// ui.screen.services --size 100 --layer V` — the lowest free 100-wide V
// block after ui.screen.finance's V300-V399 and ui.router's V400-V499).
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7).
const (
	// ErrMalformedPatch: an "f4.services" patch failed to decode — invalid
	// JSON, an unrecognised schemaVersion, or an oversized wire payload.
	// Logged and dropped (SF-7/SVC-7); the screen's data keeps its
	// last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-V500"

	// ErrStaleSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription, or was
	// bound and since unsubscribed (SF-7's "delta for an unknown/stale
	// subscription is dropped and logged ... never applied or causing a
	// panic").
	ErrStaleSubscription = "MET-V501"

	// ErrInvalidFundingRequest: a SetFunding call's rawValue rescaled (via
	// normalizeFundingLevel, screen.go) to a non-finite level, or a level
	// outside the engine's [0,1] funding-level domain
	// (internal/engine/services/api.go:266-292's ServicesAPI.SetFunding
	// hard-rejects level<0 or level>1, never silently clamps) — caught
	// before it ever reached the engine (SVC-8's local-validation half;
	// the engine's own rejection path, for a level that IS in [0,1] but
	// still rejected engine-side (e.g. below a hard floor), is
	// ApplyResult/FundingRejectedReason).
	ErrInvalidFundingRequest = "MET-V503"

	// ErrFundingCommandSendFailed: a SetFunding call's send() itself
	// failed (e.g. protocol.ErrCommandQueueFull, protocol.ErrTransportClosed
	// — the command never reached the transport at all, so no
	// CommandResult will ever arrive for it). Distinct from
	// ErrInvalidFundingRequest (a local pre-check on the VALUE, before
	// send is ever called) and distinct from an engine rejection
	// (ApplyResult/FundingRejectedReason, which requires a real
	// CommandResult to have actually come back) — this is FEAT-208
	// increment 3 destructive round r1's finding F-B part 1: a client-side
	// transport/queue failure must surface as its own, separately labelled
	// class of failure (FundingLocalFailureReason, screen.go), never
	// silently indistinguishable from success.
	ErrFundingCommandSendFailed = "MET-V504"

	// ErrFundingRequestEvicted: pendingFunding hit its documented capacity
	// (fundingPendingCap, screen.go) and the OLDEST outstanding request was
	// evicted to make room — its CommandResult, if it ever arrives, will
	// find no pendingFunding entry and be silently ignored (ApplyResult's
	// existing "not a pending funding command" branch). Sibling of
	// ErrFundingCommandSendFailed (MET-V504): both are LOCAL, client-side
	// failure classes surfaced via FundingLocalFailureReason, never
	// FundingRejectedReason — the engine never adjudicated (or, for an
	// evicted entry, may still be about to adjudicate) this specific
	// request either way (FEAT-208 increment 3 destructive round r2,
	// item 3: pendingFunding previously had no bound at all).
	ErrFundingRequestEvicted = "MET-V505"
)

// ErrScreenCopied (MET-V502) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following
// ui.screen.finance's/ui.screen.trade's precedent of keeping a
// copy-guard's own error constant local to copyguard.go rather than this
// file.

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch (wire.go) can report; both feed MET-V500's
// {cause} template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) — the registry-sourced error is the
// MET-V500 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.finance's/ui.screen.trade's
// errPatchTooLarge/errUnsupportedSchemaVersion convention.
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
