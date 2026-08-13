package debug

// Registry error codes for feat.debugmode (FEAT-008). Per project
// convention (see internal/engine/core/errors.go's E000-E099 claim on
// the same "E — engine" layer of data/errors.json's ranges.reserved
// table), this package claims E200-E299 as its own block. (Originally
// claimed E100-E199; renumbered before first Tester pass — that block
// turned out to already be claimed and used, in source only, by
// internal/engine/detgate (feat.detgate, commit 47de5d0), which had
// never been registered in data/errors.json. Lesson applied going
// forward: check `grep -rn "MET-<prefix>" internal/ cmd/` in addition to
// the registry before claiming a range, since source can be ahead of
// the registry.) Every code below IS registered in data/errors.json
// with real severity/module/message/remedy fields (GR#7) — see that
// file's "E200-E299" reserved-range entry and its "codes" section.
const (
	// ErrDebugRequired: a debug-gated capability (8x speed, a cheat,
	// the entity inspector, the fidelity dial, the console, fixture
	// record/replay controls) was requested while IsOn() == false
	// (AC-9, AC-11). The {capability} context names which one.
	ErrDebugRequired = "MET-E200"

	// ErrNoHeaderConfigured: Enable was called before WithHeader wired
	// a save header to sticky-flag. Enabling without a header to touch
	// would silently skip the AC-3 sticky-flag contract, so this is
	// refused rather than treated as a no-op.
	ErrNoHeaderConfigured = "MET-E201"

	// ErrEnablePersistFailed: the injected PersistFunc reported a
	// failure writing the sticky DebugTouched flag through; Enable does
	// not report success in this case (AC-12).
	ErrEnablePersistFailed = "MET-E202"

	// ErrUnknownEnableSource: Enable was called with an EnableSource
	// outside {flag, config, palette}. Defensive: every call site in
	// this codebase uses one of the three named constants, so this
	// should be unreachable outside a caller bug.
	ErrUnknownEnableSource = "MET-E203"

	// ErrNilCheatEffect: InvokeCheat was called with a nil CheatEffect
	// — this package never has a default domain effect to fall back to
	// (it doesn't own cheats' domain effects, see doc.go).
	ErrNilCheatEffect = "MET-E204"

	// ErrCheatEffectFailed: a cheat's injected CheatEffect returned an
	// error; the cheat is not recorded as used (AC-6 only audits
	// successful invocations — a failed effect never happened).
	ErrCheatEffectFailed = "MET-E205"

	// ErrEntityLookupNotConfigured: InspectEntity was called before
	// WithEntityLookup wired a resolver.
	ErrEntityLookupNotConfigured = "MET-E206"

	// ErrEntityLookupFailed: the injected EntityLookup returned an
	// error resolving the requested ref.
	ErrEntityLookupFailed = "MET-E207"

	// ErrEntityMarshalFailed: the resolved entity value could not be
	// JSON-marshalled (e.g. it carries a chan/func field).
	ErrEntityMarshalFailed = "MET-E208"

	// ErrFidelityDialNotConfigured: FidelityDial was called before
	// WithFidelityDial wired an implementation.
	ErrFidelityDialNotConfigured = "MET-E209"

	// codeCheatUsed: the audit-log entry recorded via errs.New for
	// every successfully invoked cheat (AC-6: "cheats must be visible
	// in the record, not silent"), mirroring the in-memory
	// State.CheatLog() entry into the standard NDJSON log sink at warn
	// severity. Deliberately unexported — it is a log-line code, not a
	// failure any caller branches on; State.CheatLog() is the supported
	// programmatic audit surface.
	codeCheatUsed = "MET-E210"

	// ErrStateCopied: a *State method was called on a struct copy of the
	// value NewState returned (SEC-020 wave 2). State's mu is a
	// sync.Mutex VALUE (a copy gets its own, independent lock) while
	// header (*serialize.Header) and cheatLog (slice) are reference types
	// a copy ALIASES — so an unrejected copy is a second lock domain
	// racing the original to mutate the SAME save header's sticky
	// DebugTouched hygiene flag. Every method that touches mu or a shared
	// field checks for this before doing so (checkNotCopied, copyguard.go)
	// and rejects rather than proceeding; see that file's doc comment for
	// the pre-lock ordering requirement (SEC-016).
	ErrStateCopied = "MET-E211"

	// ErrFeedbackInboxNotConfigured: SubmitFeedback (feedback.go, FEAT-065
	// AC-DM8) was called before WithFeedbackInbox wired a directory. No
	// invented default path — the inbox location is a deployment/config
	// concern the caller owns.
	ErrFeedbackInboxNotConfigured = "MET-E212"

	// ErrFeedbackInboxUnwritable: the configured feedback inbox directory
	// could not be created/written (os.MkdirAll failed) — e.g. permission
	// denied, path collides with a non-directory file.
	ErrFeedbackInboxUnwritable = "MET-E213"

	// ErrFeedbackWriteFailed: writing (or renaming into place) the
	// per-submission JSON record failed. Never partially written into the
	// final path — the temp-file-then-rename in SubmitFeedback means a
	// reader polling the inbox never observes a half-written file even on
	// this failure path.
	ErrFeedbackWriteFailed = "MET-E214"

	// ErrFeedbackMarshalFailed: the FeedbackRecord could not be
	// JSON-marshalled. Defensive only — every field is a plain string/
	// int64/bool, so this should be unreachable outside a future field
	// addition introducing an unmarshalable type.
	ErrFeedbackMarshalFailed = "MET-E215"
)
