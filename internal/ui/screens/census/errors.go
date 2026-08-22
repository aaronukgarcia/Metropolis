package census

import "fmt"

// Registry error codes for ui.screen.census (FEAT-209). This package
// claims V700-V799 (data/errors.json's "V700-V799" reserved-range entry,
// auto-claimed via `node tools/plan/add-error.js claim-range
// ui.screen.census --size 100 --layer V` — the lowest free 100-wide V
// block after ui.screen.districts' V600-699). Every code below is
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7).
const (
	// ErrMalformedPatch: an "f6.census" patch failed to decode — invalid
	// JSON, an unrecognised schemaVersion, or an oversized wire payload.
	// Logged and dropped (AC-11); the screen's data keeps its last-known-
	// good state, never partially applied.
	ErrMalformedPatch = "MET-V700"

	// ErrStaleSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription, or was
	// bound and since unsubscribed (AC-11: "a delta for an unknown/stale
	// subscription is dropped and logged ... never applied or causing a
	// panic").
	ErrStaleSubscription = "MET-V701"

	// ErrKPIUnavailable: a KPI/source query the engine rejected (AC-12,
	// mirroring engine.census's MET-G2701/MET-G2702 — unknown object/
	// unknown key) surfaces as an explicit "unavailable" state on the
	// affected KPI tile, never a silently-rendered zero.
	ErrKPIUnavailable = "MET-V703"

	// ErrBioUnavailable: a citizen-bio query the engine rejected (AC-12,
	// mirroring engine.census's MET-G2701 — unknown object) surfaces as an
	// explicit "unavailable" state on the affected bio pane, never a
	// silently-rendered zero-value bio.
	ErrBioUnavailable = "MET-V704"
)

// ErrScreenCopied (MET-V702) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following
// ui.screen.services'/ui.screen.finance's precedent of keeping a
// copy-guard's own error constant local to copyguard.go rather than this
// file.

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch (wire.go) can report; both feed MET-V700's
// {cause} template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) — the registry-sourced error is the
// MET-V700 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.services'/ui.screen.finance's
// errPatchTooLarge/errUnsupportedSchemaVersion convention.
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
