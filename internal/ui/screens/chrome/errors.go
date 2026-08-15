package chrome

// Registry error codes for ui.alerts (FEAT-013). This package owns
// U950-U999 (see data/errors.json's "U950-U999" reserved-range entry).
//
// RANGE SPLIT (lead arbitration, 2026-08-14): the U900-U999 block was
// claimed concurrently by ui.diagrams (MET-U900 dangling edge, MET-U905
// copied Engine) and this package (chrome's four codes below). The lead
// split it non-overlappingly — ui.diagrams owns U900-U949, ui.alerts
// (this package) owns U950-U999 — and chrome's four codes re-numbered
// from 901-904 to MET-U950..U953. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7).
const (
	// ErrAlertMissingTarget: an Alert (or a crisis-tagged alert) was
	// constructed without a non-empty jump target — AC-3's "cannot be
	// constructed without a DrillTarget" discipline and AC-11's required
	// rejection. A targetless alert is refused at construction (NewAlert)
	// and again at the stack boundary (AddAlert), never silently entered
	// onto the stack.
	ErrAlertMissingTarget = "MET-U950"

	// ErrAlertMissingID: an Alert was constructed without a non-empty
	// identifier. The ID is the resolution key (AC-12) and, for
	// crisis-tagged alerts, the stable per-instance crisis identity AC-8's
	// edge-triggered dedupe is keyed on (FEAT-042 AC-25b).
	ErrAlertMissingID = "MET-U951"

	// ErrChromeCopied: a *Chrome method was called on a struct copy of the
	// value NewChrome returned (SEC-020 wave). checkNotCopied rejects it
	// before it does anything else — see copyguard.go.
	ErrChromeCopied = "MET-U952"

	// ErrMalformedPatch: a chrome figures delta patch failed to decode —
	// invalid JSON or an unrecognised schemaVersion. Logged and dropped;
	// the top bar keeps its last-known-good figures (same posture as
	// ui.screen.map's MET-U100).
	ErrMalformedPatch = "MET-U953"
)
