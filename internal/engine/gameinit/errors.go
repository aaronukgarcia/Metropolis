package gameinit

// Registry error codes for feat.gameinit. Range: G5410-G5419, claimed via
// `node tools/plan/add-error.js claim-range feat.gameinit --size 10
// --layer G` (BUG-273's allocator). Every code below IS registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7
// -- no code below was hand-minted), guarded against drift by
// internal/foundation/errs' source-scan test.
const (
	// ErrGameInitCopied: a *GameInit method was called on a struct copy of
	// the value New/Load/LoadDefault returned (SEC-020 family, mirroring
	// deathservices.ErrDeathServicesCopied / finance.ErrCopiedValue).
	ErrGameInitCopied = "MET-G5410"

	// ErrGameInitDataInvalid: data/gameinit.json is missing, malformed, or
	// fails its own schema validation. Rejected at load time rather than
	// falling back to a silently-invented default (GR#15).
	ErrGameInitDataInvalid = "MET-G5411"

	// ErrUnknownGameMode: New/Load was asked to construct a GameInit with
	// a Mode value that is neither ModeReal nor ModeUnlimited (AC-1's
	// "exactly one of two modes" invariant). Also returned when a save
	// bundle's declared mode string (routed through feat.saveux, never
	// read directly by this package) fails to parse as a known Mode.
	ErrUnknownGameMode = "MET-G5412"

	// ErrModeLocked: a call reached [GameInit.SetGameMode] (or any other
	// mode-changing surface this package exposes) after construction
	// (AC-3, GR#12). The mode is captured once at New/Load time and is
	// never mutated afterwards -- this is the one, single rejecting entry
	// point every settings/dev-console mode-change attempt funnels
	// through, and it never touches the locked field.
	ErrModeLocked = "MET-G5413"

	// ErrLegacyGameModeStamped (BUG-737 round-2 lead ruling, 2026-09-05):
	// a WARN-severity, non-fatal registry event -- a persist-layer city
	// that predates FEAT-143 (a durably recorded world seed, but no
	// gamemode.json and no ModeEpoch marker -- see internal/persist's
	// Store contract doc) is having its mode established for the FIRST
	// time via the one-time migration path, rather than refused. Raised
	// by internal/engine/compose's checkGameMode (snapshot.go) -- never
	// returned as a failing error, only constructed so errs.New's own
	// auto-log (foundation/errs' logEntry, called for every registered
	// code regardless of severity) makes the migration visible, never
	// silent.
	ErrLegacyGameModeStamped = "MET-G5420"

	// NOTE: MET-G5421 was reserved for a save-bundle-side legacy warning
	// but is UNUSED here -- internal/engine/save cannot import this
	// package (no feat.saveux -> feat.gameinit reverse edge; see
	// savewire.go's doc comment), so that warning is
	// save.ErrLegacyGameModeAssumedReal (MET-E1000, save/errors.go)
	// instead, a code feat.saveux owns and raises itself. G5421 stays
	// reserved (harmless, data/errors.json) rather than unclaimed.
)
