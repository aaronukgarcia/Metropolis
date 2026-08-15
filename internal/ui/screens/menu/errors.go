package menu

// Registry error codes for ui.screen.menu (FEAT-021). This package claims
// U600-U699 (see data/errors.json's "U600-U699" reserved-range entry) —
// checked against that table AND `grep -rn "MET-U6" internal/ cmd/` before
// claiming, per BUG-008's lesson that the reserved-range table alone is
// not always current. U000-U099/U100-U199/U200-U299/U300-U399/U400-U499/
// U500-U599 already belong to ui.core/ui.screen.map/ui.screen.debug/
// ui.keys/feat.devmode/ui.screen.demo. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7).
const (
	// ErrMalformedPatch: a patch for this screen's f10.session view failed
	// to decode — invalid JSON, an unrecognised schemaVersion, or an
	// oversized wire payload. Logged and dropped (SF-7); the session view
	// keeps its last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-U601"

	// ErrUnknownSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription (SF-7's
	// "delta for an unknown/stale subscription is dropped and logged,
	// never applied or causing a panic").
	ErrUnknownSubscription = "MET-U602"

	// ErrUnrecognisedView: Subscribe was called with a view name other
	// than the one this screen owns. A programming error at the call
	// site, not a runtime data condition.
	ErrUnrecognisedView = "MET-U603"

	// ErrSaveListFailed: enumerating the save root (the injected
	// BundleLister, or the default walk) failed with an I/O error. The
	// save browser renders "unavailable" rather than a stale or empty
	// list (SF-7/MEN-7).
	ErrSaveListFailed = "MET-U604"

	// ErrProfileWriteFailed: writing a keymap or dashboard-layout profile
	// file failed (keymaps.go/layouts.go share this one code — GR#3, one
	// definition for the same "persist a JSON profile to disk" failure).
	ErrProfileWriteFailed = "MET-U605"

	// ErrLayoutEditorUnavailable: OpenLayoutEditor was invoked while no
	// ui.dash layout editor is wired (WithLayoutEditor). The F10 → layouts
	// entry point reports "unavailable" rather than silently no-op'ing
	// (MEN-7/MEN-4).
	ErrLayoutEditorUnavailable = "MET-U606"

	// ErrNilKeymapOrGrammar: SelectKeymap was called with a nil keymap or
	// nil grammar. A programming error at the call site (LoadKeymapFile
	// parses the profile before selecting, so nil cannot arrive via that
	// path) — rejected fail-closed with a registry error rather than a
	// nil-pointer panic inside ui.keys' ApplyKeymap (SEC-079/GR#1).
	ErrNilKeymapOrGrammar = "MET-U607"
)
