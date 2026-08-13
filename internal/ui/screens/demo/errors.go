package demo

// Registry error codes for ui.screen.demo (FEAT-018). This package claims
// U500-U599 (see data/errors.json's "U500-U599" reserved-range entry) —
// checked against that table AND `grep -rn "MET-U5" internal/ cmd/`
// before claiming, per BUG-008's lesson that the reserved-range table
// alone is not always current. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields
// (GR#7).
const (
	// ErrMalformedPatch: a patch for one of this screen's four views
	// (f6.population/f6.leisure/f6.housing/f6.commute) failed to decode —
	// invalid JSON, an unrecognised schemaVersion, or an oversized wire
	// payload. Logged and dropped (SF-7/DEMO-9); the affected view's data
	// keeps its last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-U500"

	// ErrUnknownSubscription: ApplyDelta was called with a
	// protocol.Delta whose SubscriptionID was never bound via
	// BindSubscription, or was bound to a view since unsubscribed
	// (SF-7/DEMO-9's "delta for an unknown/stale subscription is dropped
	// and logged ... never applied or causing a panic").
	ErrUnknownSubscription = "MET-U501"

	// ErrUnrecognisedView: Subscribe was called with a view name other
	// than the four this screen owns. A programming error at the call
	// site, not a runtime data condition.
	ErrUnrecognisedView = "MET-U502"
)

// ErrScreenCopied (MET-U503) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following ui.screen.
// map's/ui.screen.debug's precedent of keeping a copy-guard's own error
// constant local to copyguard.go rather than this file.
