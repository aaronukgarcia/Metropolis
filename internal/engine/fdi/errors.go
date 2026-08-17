package fdi

// Registry error codes for engine.fdi (MOD-059) / feat.pharmacampus
// (FEAT-101), the shared internal/engine/fdi package. Range: G2500-G2599,
// declared in data/errors.json's "ranges.reserved" table. Every code below
// IS registered there with real severity/module/message/remedy fields
// (GR#7) — see that file's "G2500-G2599" reserved-range entry and its
// "codes" section.
//
// Range-claim note (BUG-008 discipline): the E layer (E000-E999) is fully
// claimed by eleven earlier engine modules, and the G layer's blocks
// through G2400-G2499 were claimed by earlier engine modules/features
// (engine.citizens … engine.wellbeing, engine.news, engine.accelerator) by
// the time this package landed, so engine.fdi / feat.pharmacampus opens
// G2500-G2599 as the next free four-digit block under BUG-234's
// three-to-four-digit code-format widening. Checked against this table AND
// `grep -rn "MET-G25" internal/ cmd/` before claiming — no prior MET-G25xx
// code existed either place.
const (
	// ErrPharmaDataInvalid: data/pharmacampus.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, a missing or
	// non-positive footprint/output/jobs figure, a negative utility draw,
	// an unrecognised jobsCharacter archetype name, a non-positive bid
	// curve shape, a field of the wrong JSON type). The catalogue does NOT
	// proceed with silent defaults or a partially-populated result
	// (feat.pharmacampus AC-8).
	ErrPharmaDataInvalid = "MET-G2500"

	// ErrUnknownAnchor: an anchor-facility resolve named a key absent from
	// the loaded catalogue. Returned as an error rather than a silent
	// default-substituted parameter set (feat.pharmacampus AC-8).
	ErrUnknownAnchor = "MET-G2501"

	// ErrEducationOutputUnavailable: a pharma bid resolution referenced the
	// city's education-output term but the registered education edge
	// reported the output unavailable (unregistered). The bid does NOT
	// proceed, and no facility/firm/demand state is created
	// (feat.pharmacampus AC-8).
	ErrEducationOutputUnavailable = "MET-G2502"

	// ErrEducationDemandRejected: the won pharma campus could not emit its
	// graduate/research demand through the registered education edge. The
	// win is refused rather than registering a firm with a silently-dropped
	// demand term (feat.pharmacampus AC-8).
	ErrEducationDemandRejected = "MET-G2503"

	// ErrPharmaFirmRegistrationFailed: the anchor firm could not be
	// registered through the engine.firms edge on a bid win. Returned as an
	// error rather than a silently-unregistered anchor (feat.pharmacampus
	// AC-6/AC-8).
	ErrPharmaFirmRegistrationFailed = "MET-G2504"

	// ErrPharmaExportRejected: the won pharma campus could not route its
	// per-day export flow through the registered trade/freight edge. The win
	// is refused rather than a campus whose exports bypass the
	// balance-of-trade surface (feat.pharmacampus AC-6/AC-8).
	ErrPharmaExportRejected = "MET-G2505"
)
