package citizens

// Registry error codes for engine.citizens (MOD-018). Range: G000-G099,
// declared in data/errors.json's "ranges.reserved" table. Every code below
// IS registered there with real severity/module/message/remedy fields
// (GR#7); the internal/foundation/errs source-scan test guards against
// this ever drifting out of sync.
//
// Range-claim note (flagged for Bill/Aaron — a registry-schema extension,
// not a routine claim): the E layer is FULLY exhausted. By the time this
// module landed, eleven earlier engine modules had claimed every E
// sub-range — engine.core (E000-E099), engine.detgate (E100-E199),
// feat.debugmode (E200-E299), engine.invariant (E300-E399), engine.world
// (E400-E499), engine.season (E500-E599), engine.market (E600-E699),
// engine.helper (E700-E799), feat.saveux (E800-E899), feat.skeleton
// (E900-E949, already narrowed once for mining), and engine.mining
// (E950-E999, carved from feat.skeleton's tail). The code format is
// hard-capped at three digits (^MET-[A-Z]\d{3}$, enforced by
// internal/foundation/errs/registry.go), so no E code remains. engine.
// citizens therefore opens a second engine block under the next free
// single letter, G (V is the UI-second-block precedent; F is foundation,
// H is harness). If Bill/Aaron prefer to reallocate an existing E
// sub-range instead of a new layer letter, that is their call.
const (
	// ErrInvalidBirthMonth: a citizen record was constructed with a
	// birthMonth outside the representable range [0, MaxInt16] — negative
	// (before the world's epoch, month 0), or so large it would overflow
	// the int16 delta the cold store encodes the age in (int16(40000)
	// wraps to -25536). Rejected outright (AC-13/GR#16) rather than
	// silently clamped or wrapped — a wrapped birth month would corrupt
	// the age derivation every later system reads.
	ErrInvalidBirthMonth = "MET-G001"

	// ErrPersonalityAxisOutOfRange: a personality axis fell outside the
	// documented 0-100 range. Rejected rather than silently clamped
	// (AC-13) — a clamped axis is a corrupted record, not a corrected one.
	ErrPersonalityAxisOutOfRange = "MET-G002"

	// ErrUnknownHousehold: a citizen's householdId references a household
	// that does not exist. Rejected rather than silently orphaned
	// (AC-13) — an orphaned member would corrupt household composition
	// and the overcrowding/rent-burden derivations (AC-12).
	ErrUnknownHousehold = "MET-G003"

	// ErrAPICopied: a *CitizensAPI method was called on a struct copy of
	// the value NewCitizensAPI returned (SEC-020 family, mirroring
	// engine.world's ErrWorldCopied). checkNotCopied rejects every such
	// call before c.mu is touched — mu is a sync.RWMutex VALUE while the
	// hot/warm/household maps and the cold shard array are reference types
	// a copy ALIASES, so a copy is a second, independent lock racing the
	// original over the same referents.
	ErrAPICopied = "MET-G004"

	// ErrShardIndexOutOfRange: a cold shard index fell outside
	// [0, foundation/det.NumShards). Returned rather than an out-of-bounds
	// slice panic (GR#1) — the 256-shard count is a fixed determinism
	// invariant, never relaxed.
	ErrShardIndexOutOfRange = "MET-G005"

	// ErrAttainmentOutOfRange: a citizen's quality-weighted education
	// attainment score fell outside the int16 range the cold store encodes
	// it in. Rejected outright (AC-13/GR#16) rather than silently narrowed
	// to int16 — an int16(40000) would wrap to -25536 and corrupt the
	// record.
	ErrAttainmentOutOfRange = "MET-G006"

	// ErrFieldOutOfRange: a scalar/enum field fell outside its documented
	// contract range at the hot→cold boundary (satisfaction components,
	// schooling months, sex, health band, education stage, employment
	// state/sector, child count). Rejected outright rather than silently
	// narrowed — a bare int8(sat=200) wraps to -56, and an out-of-domain
	// enum is a data error, not a clampable number (AC-13/GR#16).
	ErrFieldOutOfRange = "MET-G007"

	// ErrFertilityDataInvalid: data/fertility.json (FEAT-160's childbearing
	// balance config) is missing, malformed, or fails its own schema
	// validation (a missing unit/disclosure, a non-finite value, an
	// out-of-order age window). Rejected at load time rather than falling
	// back to a silently-invented default (GR#15) — mirrors
	// engine.census's ErrCensusDataInvalid precedent.
	ErrFertilityDataInvalid = "MET-G008"

	// ErrFertilityBirthRejected: a fertility-driven birth's constructed
	// Citizen record failed ValidateCitizen (e.g. a config-derived
	// out-of-range BirthMonth). This should never occur for a well-formed
	// month/config, but is logged loudly rather than silently dropped or
	// allowed to corrupt the cold store (GR#1) — the birth is skipped for
	// that couple this month rather than crashing the monthly pass.
	ErrFertilityBirthRejected = "MET-G009"

	// ErrDuplicateCitizenID (FEAT-169 cross-module ID-collision finding,
	// destructive-review REJECT): a LifeEventBirth command's Citizen.ID
	// already exists in this CitizensAPI's cold or hot store.
	// ApplyLifeEventCommand rejects it outright rather than silently
	// appending a second row under the same id (which would ALIAS two
	// logically-distinct citizens — invisible to TotalPopulation's
	// row-count-based conservation view, since the row count would still
	// balance while one citizen's identity silently overwrote another's on
	// every subsequent per-id lookup). This is DEFENSE IN DEPTH: the real
	// fix is the disjoint id-range convention documented in doc.go's "Live
	// tick wiring" section (compose seeds/migrants [1, 2^62), attract
	// migrants [2^62, 2^63), fertility children [2^63, ...)) plus the
	// Wire-time assertion compose runs against it; this check is the
	// last-resort catch if that convention is ever violated by a future
	// caller.
	ErrDuplicateCitizenID = "MET-G010"
)
