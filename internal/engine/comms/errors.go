package comms

// Registry-sourced error codes (GR#7) for engine.comms, claimed in
// data/errors.json's ranges.reserved table under G3300-G3399 — the next
// free four-digit G-layer block after engine.maintenance's G3200-G3299 (the
// E layer is fully exhausted and the G layer's three-digit blocks plus
// G1000-G3299 were all claimed by earlier engine modules, so engine.comms
// opens G3300-G3399 under BUG-234's three-to-four-digit widening).
// Checked against this table AND `grep -rn "MET-G33" internal/ cmd/` before
// claiming, per BUG-008's lesson — no prior MET-G33xx code existed either
// place.
const (
	// ErrDataInvalid: data/comms.json failed to load or validate (the
	// six-era ladder, gate values, sectors, eCommerce/post/drain blocks).
	ErrDataInvalid = "MET-G3300"

	// ErrCopiedValue: a method was called on a struct-copied *CommsAPI
	// (SEC-020 family, mirroring engine.firms/engine.logistics).
	ErrCopiedValue = "MET-G3301"

	// ErrInvalidEra: an era value outside the six-era ladder was passed to
	// a capability-gate query or an era advance (AC-10). No zero-value era
	// state is ever created by the rejection.
	ErrInvalidEra = "MET-G3302"

	// ErrEraSkip: an era advance skipped an intermediate era (AC-2's
	// monotonic, one-step-at-a-time ladder).
	ErrEraSkip = "MET-G3303"

	// ErrNotUnlocked: the target era's milestone tier has not been reached
	// (or no milestone gate is wired — fail closed, AC-4/SEC-095 shape).
	ErrNotUnlocked = "MET-G3304"

	// ErrNoFirmRef: a fulfilment-centre registration was attempted with no
	// engine.firms reference wired (AC-7/AC-10).
	ErrNoFirmRef = "MET-G3305"

	// ErrLogisticsNotWired: a delivery movement was resolved with no
	// engine.logistics reference wired (AC-8).
	ErrLogisticsNotWired = "MET-G3306"

	// ErrServicesNotWired: postal infrastructure was registered or queried
	// with no engine.services reference wired (US-5).
	ErrServicesNotWired = "MET-G3307"

	// ErrOutOfRange: a numeric input fell outside its documented domain —
	// wealth/counterplay outside [0,1], or a negative letter/parcel volume
	// (AC-11). Rejected, never silently clamped.
	ErrOutOfRange = "MET-G3308"

	// ErrNonFinite: a NaN or ±Inf value reached a numeric boundary
	// (SEC-093 — rejected before any ordered range check).
	ErrNonFinite = "MET-G3309"

	// ErrUnknownSector: a sector outside the five documented buckets was
	// passed to a sector-aware query (AC-4).
	ErrUnknownSector = "MET-G3310"

	// ErrFulfilmentNotRegistered: a fulfilment-centre query (or an
	// infrastructure precondition) found no registered fulfilment centre.
	ErrFulfilmentNotRegistered = "MET-G3311"
)
