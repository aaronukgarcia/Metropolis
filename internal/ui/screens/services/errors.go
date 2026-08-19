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
