package districts

import "fmt"

// Registry error codes for ui.screen.districts (FEAT-022). This package
// claims V600-V699 (data/errors.json's reserved-range table — auto-claimed
// via `node tools/plan/add-error.js claim-range ui.screen.districts --size
// 100 --layer V`, the lowest free 100-wide V block after ui.screen.
// services' V500-V599). Every code below is registered in data/errors.json
// with real severity/module/message/remedy fields (GR#7).
const (
	// ErrMalformedPatch: an "f8.districts" patch failed to decode -- invalid
	// JSON, an unrecognised schemaVersion, or an oversized wire payload.
	// Logged and dropped (AC-10 sibling / SF-7 convention); the screen's
	// data keeps its last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-V600"

	// ErrStaleSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription, or was
	// bound and has since been unsubscribed (AC-10: dropped and logged,
	// never applied, never a panic).
	ErrStaleSubscription = "MET-V601"

	// ErrInvalidDistrictMultiplier: a SetDistrictMultiplier call's raw
	// value was non-finite or negative -- caught before it ever reached
	// the engine (mirrors internal/engine/tax/tax.go's SetDistrictMultiplier
	// validation locally so a bad request never crosses the wire; the
	// engine's own SEC-098 rate-cap rejection, for a value that IS finite
	// and >=0 but still rejected engine-side, surfaces separately via
	// ApplyResult/TaxRejectedReason, AC-9).
	ErrInvalidDistrictMultiplier = "MET-V603"
)

// ErrScreenCopied (MET-V602) is declared in copyguard.go, next to
// checkNotCopied -- the one thing that produces it -- following
// ui.screen.services'/ui.screen.finance's precedent of keeping a
// copy-guard's own error constant local to copyguard.go rather than this
// file.

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch (wire.go) can report; both feed MET-V600's
// {cause} template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) -- the registry-sourced error is the
// MET-V600 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.services' identical convention.
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
