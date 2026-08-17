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
	// list (SF-7/MEN-7). The rendered message carries neither the save
	// root path nor the raw cause (which may embed it) — the cause stays on
	// the wrapped error for errors.Is/As/Unwrap only (SEC-224/GR#1).
	ErrSaveListFailed = "MET-U604"

	// ErrProfileWriteFailed: writing a keymap or dashboard-layout profile
	// file failed (keymaps.go/layouts.go share this one code — GR#3, one
	// definition for the same "persist a JSON profile to disk" failure).
	// The rendered message carries neither the profile path nor the raw
	// cause — the path stays in Ctx and the cause on the wrapped error for
	// diagnostics only, never in Display() (SEC-224/GR#1).
	ErrProfileWriteFailed = "MET-U605"

	// ErrLayoutEditorUnavailable: OpenLayoutEditor could not open the
	// F10 → layouts editor — either no ui.dash layout editor is wired
	// (WithLayoutEditor) or no layout profile has been loaded/selected.
	// The entry point reports "unavailable" rather than silently no-op'ing
	// or invoking the editor with no profile (MEN-7/MEN-4/SEC-213).
	ErrLayoutEditorUnavailable = "MET-U606"

	// ErrNilKeymapOrGrammar: SelectKeymap was called with a nil keymap or
	// nil grammar. A programming error at the call site (LoadKeymapFile
	// parses the profile before selecting, so nil cannot arrive via that
	// path) — rejected fail-closed with a registry error rather than a
	// nil-pointer panic inside ui.keys' ApplyKeymap (SEC-079/GR#1).
	ErrNilKeymapOrGrammar = "MET-U607"

	// ErrProfileReadFailed: reading a keymap or dashboard-layout profile
	// file failed (keymaps.go/layouts.go share this one code — GR#3, one
	// definition for the same "read a JSON profile from disk" failure),
	// mirroring ErrProfileWriteFailed (MET-U605) for writes. The read path
	// returns this registry-sourced error (correlation ID + log) rather
	// than a raw *os.PathError or json error (SEC-212/GR#7/GR#1); the
	// rendered message also carries neither the profile path nor the raw
	// cause (SEC-224/GR#1).
	ErrProfileReadFailed = "MET-U608"

	// ErrSaveListEntryReadFailed: reading a single save bundle's header
	// (serialize.ReadHeader) failed during Refresh — corrupt header.json,
	// an unreadable file, or an incompatible format version. The slot is
	// skipped (one bad slot must not hide every other save) and the
	// failure is recorded in SaveListErrors as this registry-sourced error
	// (correlation ID + wrapped cause) rather than serialize's raw
	// *fmt.wrapError/*os.PathError, which would otherwise leak the save
	// root's absolute filesystem path with no correlation ID and no
	// registry code (SEC-218/GR#7/GR#1). Distinct from ErrSaveListFailed
	// (MET-U604), which covers enumeration of the root itself.
	ErrSaveListEntryReadFailed = "MET-U609"
)
