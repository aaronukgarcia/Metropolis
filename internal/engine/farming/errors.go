package farming

// Registry error codes for the shared engine.farming package (MOD-045) and
// feat.farmtypes (FEAT-104). Range: G1600-G1699, declared in data/errors.json's
// "ranges.reserved" table. Every code below IS registered there with real
// severity/module/message/remedy fields (GR#7) — see that file's
// "G1600-G1699" reserved-range entry and its "codes" section.
//
// Range-claim note (ASM — see dispatch report): the E layer (E000-E999) was
// fully claimed by eleven earlier engine modules, and the G layer's
// three-digit blocks plus the G1000-G1599 four-digit blocks were claimed by
// engine.citizens through engine.crime by the time this package landed.
// G1600-G1699 is the next free four-digit block under BUG-234's
// three-to-four-digit code-format widening. Flagging for Bill/Aaron: any
// reallocation of a reserved range is their call.
const (
	// ErrFarmTypeDataInvalid: data/farmtypes.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, a missing or
	// out-of-taxonomy "types" key, a missing/non-positive footprint, a
	// missing chain commodity or destination, an unknown soil-band/terrain/
	// chain-destination enum value, a negative stocking density, a stocking
	// table on a non-livestock type, or a field of the wrong JSON type —
	// GR#16). The catalogue never proceeds with silent defaults or a
	// partially populated set (AC-8).
	ErrFarmTypeDataInvalid = "MET-G1600"

	// ErrUnknownFarmType: a farm-type key was resolved that is not one of the
	// five §31 facility categories (arable, livestock, orchard, market
	// garden, vineyard). Returned rather than a silent default substitution
	// (AC-8).
	ErrUnknownFarmType = "MET-G1601"
)
