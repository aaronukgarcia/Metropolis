package ticker

import "fmt"

// Registry error codes for ui.screen.ticker (FEAT-020). This package
// claims U700-U799 (see data/errors.json's "U700-U799" reserved-range
// entry) — checked against that table AND `grep -rn "MET-U7" internal/
// cmd/` before claiming, per BUG-008's lesson that the reserved-range
// table alone is not always current. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields
// (GR#7).
const (
	// ErrMalformedPatch: a patch for one of this screen's four views
	// (f9.ticker/f9.bulletin/f9.annual/f9.archive) failed to decode —
	// invalid JSON, an unrecognised schemaVersion, or an oversized wire
	// payload. Logged and dropped (SF-7); the affected view's data keeps
	// its last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-U700"

	// ErrUnknownSubscription: ApplyDelta was called with a
	// protocol.Delta whose SubscriptionID was never bound via
	// BindSubscription, or was bound to a view since unsubscribed
	// (SF-7's "delta for an unknown/stale subscription is dropped and
	// logged ... never applied or causing a panic").
	ErrUnknownSubscription = "MET-U701"

	// ErrUnrecognisedView: Subscribe was called with a view name other
	// than the four this screen owns. A programming error at the call
	// site, not a runtime data condition.
	ErrUnrecognisedView = "MET-U702"

	// ErrMissingEventID: a story in an otherwise well-formed f9.* patch
	// carried an eventId that is empty after trimming (an empty string, or
	// whitespace-only — SEC-076) — TIK-5's structural "no hallucinated
	// news" control. The story is rejected (never rendered), not repaired
	// with a placeholder ID: a story with no backing event is exactly the
	// plausible-looking string with nothing behind it that TIK-5 exists to
	// refuse.
	ErrMissingEventID = "MET-U703"

	// ErrArchiveStopped: an f9.archive patch arrived whose full-snapshot
	// payload exceeded this screen's wire ceiling (maxPatchWireBytes), so
	// the last-known-good archive is frozen and surfaced as stopped — never
	// a silent freeze (SEC-072, GR#17). The archive carries the city's
	// whole atomic-event history (spec 29.4), which outgrows the per-patch
	// ceiling long before a "century of monthly bulletins" would.
	ErrArchiveStopped = "MET-U705"
)

// ErrScreenCopied (MET-U704) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following
// ui.screen.map's/ui.screen.demo's precedent of keeping a copy-guard's
// own error constant local to copyguard.go rather than this file.

// errPatchTooLargeError is decodeWirePatch's oversized-wire rejection
// (SEC-039 discipline): a f9.* patch whose raw byte size exceeds
// maxPatchWireBytes, rejected BEFORE json.Unmarshal ever runs. It is a
// distinct type (not a plain fmt.Errorf) so applyArchive can tell "the
// archive outgrew the wire ceiling" — a permanent, surfaced stop
// (SEC-072) — apart from a transient malformed patch (bad JSON or an
// unsupported schemaVersion), which is merely dropped as MET-U700.
type errPatchTooLargeError struct {
	gotBytes int
	maxBytes int
}

func (e errPatchTooLargeError) Error() string {
	return fmt.Sprintf("ticker: f9.* patch is %d bytes, exceeding the wire-size ceiling of %d bytes — rejected before decoding", e.gotBytes, e.maxBytes)
}

func errPatchTooLarge(gotBytes, maxBytes int) error {
	return errPatchTooLargeError{gotBytes: gotBytes, maxBytes: maxBytes}
}

// errUnsupportedSchemaVersion is decodeWirePatch's error for a f9.*
// patch whose schemaVersion this package doesn't understand.
func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("ticker: unsupported f9.* schemaVersion %d (want %d)", got, wireSchemaVersion)
}
