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

	// The codes below are the general blight model + extraction siting half
	// of engine.mining (MOD-046 core, AC-11). All in the E950-E999 reserved
	// range, all registered in data/errors.json (GR#7).

	// ErrSitingNotPermitted: an extraction siting attempt was rejected
	// because the land is ungeology-gated (the type's geology class does not
	// match the tile's revealed pocket), unprospected, out of extent, or the
	// site key is already in use. Loud rejection, never a silent no-op (AC-11).
	ErrSitingNotPermitted = "MET-E956"

	// ErrBlightProfileInvalid: a blighting-object registration carried an
	// out-of-extent location, a non-positive noise radius, or a negative/
	// non-finite visual profile. Loud rejection (AC-11).
	ErrBlightProfileInvalid = "MET-E957"

	// ErrAlreadyReclaimed: a Reclaim command named a site that was already
	// reclaimed. Loud rejection (AC-11 — double-reclaim).
	ErrAlreadyReclaimed = "MET-E958"

	// ErrBlightDataInvalid: data/mining.json could not be loaded or failed
	// schema validation (missing file, malformed JSON, a non-positive
	// falloff exponent / occlusion scale / grow-in delay / capacity days, a
	// missing class-profile entry, a non-finite or out-of-domain value). The
	// blight model does NOT proceed with silent defaults (GR#15).
	ErrBlightDataInvalid = "MET-E959"

	// ErrUnknownBlightKey: a query/command named an object or site key absent
	// from the registry. Loud rejection, never a silent zero.
	ErrUnknownBlightKey = "MET-E960"

	// ErrBlightCopied: a *BlightAPI method was called on a struct copy
	// (SEC-020 family, mirroring ErrDepositMapCopied).
	ErrBlightCopied = "MET-E961"

	// ErrBlightQueryOutOfBounds: a query named a TileCoord/CellLocal outside
	// the expansion extent. Loud rejection (GR#1).
	ErrBlightQueryOutOfBounds = "MET-E962"

	// ErrReclaimBlocked: a Reclaim command named an option this package does
	// not implement — notably the landfill-void outcome, which is BLOCKED
	// because no engine.refuse↔engine.mining edge exists (see doc.go).
	ErrReclaimBlocked = "MET-E963"

	// ErrSiteExhausted: an Extract/CloseSite command named a site that is
	// exhausted, reclaimed, or already closed — no further output.
	ErrSiteExhausted = "MET-E964"

	// ErrExtractionInvalid: an Extract command carried a non-positive or
	// non-finite tonnes amount.
	ErrExtractionInvalid = "MET-E965"
)
