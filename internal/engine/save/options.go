package save

// loadOptions is the resolved set of Load/LoadAt behaviour switches a
// LoadOption mutates (BUG-479). Unexported: callers only ever build one
// indirectly, by passing the functional-option constructors below to
// Manager.Load.
type loadOptions struct {
	// checkWorldSeed is true once WithExpectedWorldSeed has been passed.
	// A Load call with no LoadOption at all leaves this false, so it
	// performs NO seed check -- the pre-BUG-479 behaviour is preserved
	// for every caller that does not opt in (checkpoint.Manager today;
	// LoadLatest's own zero-option internal Load calls). BUG-479's fix
	// lives at the compose.Composition layer (save_wire.go), which is
	// the one caller that actually knows "the loading composition's own
	// seed" and passes it via this option on every Load/LoadAt.
	checkWorldSeed bool

	// expectedWorldSeed is the loading composition's own world seed,
	// only meaningful when checkWorldSeed is true.
	expectedWorldSeed int64

	// allowMismatch is BUG-479's explicit reseed escape hatch (set by
	// AllowSeedMismatch): when true, a WithExpectedWorldSeed mismatch is
	// tolerated (the bundle loads as normal) instead of being refused
	// with ErrSaveSeedMismatch. Never the default -- a deliberate
	// reseed (e.g. FEAT-1972079897's rules-change replay) must ask for
	// this explicitly on every call it applies to.
	allowMismatch bool

	// checkGameMode is true once WithExpectedGameMode has been passed
	// (FEAT-143, AC-5). A Load call with no LoadOption at all leaves this
	// false, performing NO game-mode check -- the pre-FEAT-143 behaviour
	// is preserved for every caller that does not opt in.
	checkGameMode bool

	// expectedGameMode is the loading session's own locked
	// gameinit.GameInit.Mode() string, only meaningful when
	// checkGameMode is true. There is deliberately no
	// AllowGameModeMismatch escape hatch mirroring AllowSeedMismatch:
	// AC-5 requires a genuine mode mismatch to ALWAYS fail closed, with
	// no re-mode-on-load path at all -- re-moding a session is a
	// new-game decision, never a load-time one. A bundle with NO
	// recorded mode is NOT automatically a mismatch, though -- see
	// WithExpectedGameMode's own doc comment below for the round-2 lead
	// ruling (2026-09-05) that replaced the original blanket-rejection
	// rule with a real migration path for pre-FEAT-143 saves.
	expectedGameMode string
}

// LoadOption customises one Manager.Load or Manager.LoadAt(-layer) call.
// The zero set of options (no LoadOption passed) is IDENTICAL to Load's
// pre-BUG-479 behaviour: no seed check at all. Options only ever ADD
// checks/opt-ins; there is no way to widen behaviour beyond "no seed
// check", which keeps every existing zero-option caller's semantics
// unchanged after this type was introduced.
type LoadOption func(*loadOptions)

// WithExpectedWorldSeed instructs Load to refuse the bundle at dir unless
// its header's WorldSeed equals seed (BUG-479): loading a bundle into a
// differently-seeded composition silently diverges every seed-derived
// stateless draw (attract hash draws, det.Stream per-draw RNG) from the
// saved trajectory, with no error, unless something enforces this. Pass
// the loading composition's own seed as seed.
//
// On mismatch, Load returns ErrSaveSeedMismatch (carrying both seeds)
// BEFORE any registered Participant's Handler is invoked -- the header is
// fully decoded, and the mismatch checked, ahead of the per-shard load
// loop (see load.go), so a refused load leaves the composition
// byte-for-byte untouched. Combine with AllowSeedMismatch to permit the
// mismatch instead of refusing (a deliberate reseed).
//
// A bundle whose header predates the WorldSeed field decodes that field
// as its zero value (encoding/json leaves an absent JSON key at the Go
// zero value), which this check treats exactly like any other numeric
// mismatch: refused unless it happens to equal seed, or AllowSeedMismatch
// is also passed. That is the intended fail-closed behaviour for a
// missing/legacy seed header (BUG-479 AC).
func WithExpectedWorldSeed(seed int64) LoadOption {
	return func(o *loadOptions) {
		o.checkWorldSeed = true
		o.expectedWorldSeed = seed
	}
}

// AllowSeedMismatch is BUG-479's explicit opt-in for a deliberate reseed
// (e.g. the FEAT-1972079897 rules-change replay case, restoring a bundle
// under new rules with an intentionally different seed): it permits a
// WithExpectedWorldSeed mismatch to load anyway, instead of Load refusing
// it with ErrSaveSeedMismatch. Never pass this reflexively -- it is the
// escape hatch, not the default; the default (WithExpectedWorldSeed alone,
// or no options at all pre-BUG-479-style) always refuses a mismatch or
// performs no check at all, respectively.
func AllowSeedMismatch() LoadOption {
	return func(o *loadOptions) {
		o.allowMismatch = true
	}
}

// WithExpectedGameMode instructs Load to refuse the bundle at dir unless
// its Meta.GameMode equals mode (FEAT-143 AC-5): loading a save whose
// declared mode differs from the session's own locked mode would silently
// re-mode the session (letting a Real-mode save resurrect into an
// Unlimited session, or vice versa) with no error unless something
// enforces this. Pass the loading session's own gameinit.GameInit.Mode()
// string (via GameModeWire()) as mode.
//
// On a genuine mismatch (both sides non-empty and different), Load
// returns ErrGameModeMismatch (carrying both mode strings). There is
// deliberately no AllowGameModeMismatch escape hatch: unlike a
// world-seed reseed, a deliberate re-mode is a new-game decision, never
// a load-time opt-in.
//
// BUG-737 round-2 lead ruling (2026-09-05, REPLACING the original
// "absent mode is always rejected" text): a bundle whose Meta predates
// FEAT-143 (or was otherwise written with no mode recorded) decodes
// GameMode as the empty string. The original design treated that as a
// mismatch against ANY expected mode, which broke every save bundle
// written before FEAT-143 shipped with no migration path at all. The
// corrected rule: an empty bundle GameMode loads ONLY when mode ==
// "real" (the conservative default) -- see load.go's own doc comment
// for the exact three-way split, and ErrLegacyGameModeAssumedReal
// (errors.go) for the non-fatal WARN this path raises. Loading the same
// empty-mode bundle against mode == "unlimited" (or any other non-empty,
// non-"real" value) still refuses -- an absent mode is never treated as
// "matches unlimited" (the original false-pass-risk note: silently
// treating an absent mode as unlimited would let an old save silently
// unlock money -- this survives intact for that one direction).
//
// mode == "" is ALSO always refused (FEAT-143 round finding P2-A) --
// even against a bundle whose own recorded GameMode is itself "". This
// package deliberately does not import feat.gameinit (no such reverse
// edge is registered) so it cannot call gameinit.ParseMode to validate
// mode against the two known enum values, but an empty expected mode is
// unconditionally a caller error: it is exactly the value
// gameinit.GameInit.GameModeWire() returns when its SEC-020 copy-guard
// trips on a copied *GameInit, so treating "" as "no expectation" (or as
// matching an equally-empty legacy bundle) would silently turn AC-5's
// fail-closed check into a no-op precisely when the caller most needed
// it to fire. Load refuses with ErrGameModeMismatch whenever mode == ""
// and this option was passed, regardless of the bundle's own GameMode.
func WithExpectedGameMode(mode string) LoadOption {
	return func(o *loadOptions) {
		o.checkGameMode = true
		o.expectedGameMode = mode
	}
}

// resolveLoadOptions applies every opt in order and returns the resolved
// loadOptions. A later option can override an earlier one of the same
// kind (last-write-wins), matching Go's usual functional-option
// convention -- callers are expected to pass each option at most once.
func resolveLoadOptions(opts []LoadOption) loadOptions {
	var lo loadOptions
	for _, opt := range opts {
		opt(&lo)
	}
	return lo
}
