package proj

import "fmt"

// Registry error codes for ui.screen.proj (FEAT-019). This package claims
// V000-V099 (see data/errors.json's "V000-V099" reserved-range entry) —
// the U error-registry layer (U000-U999) is exhausted by the Sprint-8 wave
// (ui.core U000, map U100, debug U200, keys U300, devmode U400, demo U500,
// menu U600, ticker U700, dash U800, alerts U900), so ui.screen.proj opens
// a second UI-layer block. Checked against the table AND
// `grep -rn "MET-V" internal/ cmd/` before claiming, per BUG-008's lesson
// that the reserved-range table alone is not always current — no prior
// MET-V code existed either place. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7).
const (
	// ErrMalformedPatch: an "f7.projections" patch failed to decode —
	// invalid JSON, an unrecognised schemaVersion, or an oversized wire
	// payload. Logged and dropped (SF-7/PRJ-6); the screen's data keeps
	// its last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-V001"

	// ErrUnknownSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription, or was
	// bound and since unsubscribed (SF-7's "delta for an unknown/stale
	// subscription is dropped and logged ... never applied or causing a
	// panic").
	ErrUnknownSubscription = "MET-V002"
)

// ErrScreenCopied (MET-V003) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following ui.screen.
// map's/ui.screen.debug's/ui.screen.demo's precedent of keeping a copy-
// guard's own error constant local to copyguard.go rather than this file.

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch (wire.go) can report; both feed MET-V001's
// {cause} template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) — the registry-sourced error is the
// MET-V001 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.demo's errPatchTooLarge/
// errUnsupportedSchemaVersion convention (malformed.go).
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
