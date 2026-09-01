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
