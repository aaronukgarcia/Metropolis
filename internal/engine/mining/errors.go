package mining

// Registry error codes for engine.mining (MOD-046 / feat.resourcedeposits
// FEAT-049). Range: E950-E999, declared in data/errors.json's
// "ranges.reserved" table. Every code below IS registered there with real
// severity/module/message/remedy fields (GR#7) — see that file's
// "E950-E999" reserved-range entry and its "codes" section.
//
// Range-claim note (ASM — see dispatch report): the E layer's 000-899
// blocks were all claimed by ten earlier engine modules by the time this
// package landed, and the code format is hard-capped at three digits
// (^MET-[A-Z]\d{3}$, enforced by internal/foundation/errs/registry.go),
// so a new E block could only be carved from the tail of feat.skeleton's
// E900-E999 reservation. feat.skeleton ships exactly one code (MET-E900),
// so its reservation was narrowed to E900-E949 and E950-E999 was claimed
// for this module — flagging for Bill/Aaron in the dispatch report, since
// any reallocation of a reserved range is their call.
const (
	// ErrDepositDataInvalid: data/deposits.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, a negative
	// count, an inverted depth band, an out-of-taxonomy resource key, a
	// non-positive curve shape, an out-of-range co-location factor). The
	// shuffle does NOT proceed with silent defaults or a partially
	// populated map (AC-11).
	ErrDepositDataInvalid = "MET-E950"

	// ErrGeologyNotDerived: the deposit shuffle was invoked against a tile
	// whose engine.world geology has not been derived (revealed by
	// prospecting) yet — PocketGeology returned ErrGeologyNotProspected.
	// Distinct from ErrDepositDataInvalid; proceeding would place deposits
	// against zero-value geology and silently produce a geology-blind map
	// (AC-12).
	ErrGeologyNotDerived = "MET-E951"

	// ErrDepositQueryOutOfBounds: a deposit query named a TileCoord outside
	// the expansion extent or a CellLocal outside the tile-local domain.
	// Returned as an error rather than a silent "no deposit" (GR#1).
	ErrDepositQueryOutOfBounds = "MET-E952"

	// ErrDepositMapCopied: a *DepositMap method was called on a struct copy
	// of the value NewDepositMap returned (SEC-020 family, mirroring
	// engine.world's ErrWorldCopied). checkNotCopied (shuffle.go) rejects
	// every such call before m.mu is touched — mu is a sync.RWMutex VALUE
	// while placed is an aliased map, so a copy is a second, independent
	// lock racing the original over the same map.
	ErrDepositMapCopied = "MET-E953"

	// ErrMineTypeDataInvalid: data/minetypes.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, a missing or
	// non-positive footprint/output rate, a negative jobs count, an unknown
	// blight-class value, an inverted depth band, a dangling geology or
	// deposit class ref, a field of the wrong JSON type). The catalogue does
	// NOT proceed with silent defaults or a partially-populated result
	// (feat.minetypes AC-7).
	ErrMineTypeDataInvalid = "MET-E954"

	// ErrUnknownMineType: a mine-type resolve named a key absent from the
	// loaded catalogue. Returned as an error rather than a silent
	// default-substituted parameter set (feat.minetypes AC-7).
	ErrUnknownMineType = "MET-E955"
)
