package save

// Context is the deterministic, simulation-derived information a save
// call needs to build int.serializer's Header — supplied by the caller
// (the future engine.core tick loop) rather than read from any wall
// clock or global state by this package. Every field here is either
// deterministic simulation data or a build identity string; none of it
// is sourced from the wall clock (AC-15).
type Context struct {
	// WorldSeed is the deterministic world seed (§5.3).
	WorldSeed int64

	// CreatedAtTick is the simulation tick at save time — never a
	// wall-clock timestamp.
	CreatedAtTick int64

	// GameMonth is the in-world calendar month counter at save time.
	GameMonth int64

	// AppVersion is the build identity (buildinfo.Version or
	// equivalent) that is taking this save. Callers are responsible for
	// passing this explicitly — this package does not import
	// internal/foundation/buildinfo itself, mirroring int.serializer's
	// own NewHeader's deliberate choice (see save-format.md's "Open
	// questions" #4) to keep this package's tests independent of that
	// package's mutable globals.
	AppVersion string

	// DebugTouched carries forward whether debug mode has ever been
	// enabled this session (§14, M0-ENG §3). Sticky: this package
	// merges it into the header via Header.TouchDebug/MergeDebugTouched
	// (int.serializer's own sticky-flag enforcement, never a plain
	// assignment) so a previously debug-touched save can never come
	// back clean through this package's API either.
	DebugTouched bool

	// GameMode is FEAT-143 (mkey feat.gameinit)'s locked initialization
	// mode string ("real" or "unlimited", from gameinit.GameInit.Mode()/
	// GameModeWire()) — carried straight into this bundle's own Meta
	// sidecar (AC-4) by SaveManual/Autosave/Milestone. This package does
	// not import internal/engine/gameinit (no such reverse edge is
	// registered); GameMode is a plain string exactly like SaveKind's
	// own typed-in-this-package convention. Left empty, a bundle's Meta
	// simply carries no game mode — the pre-FEAT-143 shape, unaffected
	// unless a caller opts into [WithExpectedGameMode] on load.
	GameMode string
}
