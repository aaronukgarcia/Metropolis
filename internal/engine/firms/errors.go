package firms

// Registry error codes for engine.firms (MOD-058). Range: G1400-G1499,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The G layer's three-digit blocks through G1299 are all claimed
// (engine.citizens G000-G099, engine.projections G100-G199,
// engine.finance G200-G299, engine.consumption G300-G399,
// engine.logistics G400-G499, engine.build G500-G599,
// engine.households G600-G699, engine.attract G700-G799,
// feat.compositionroot G800-G899, engine.unlocks G900-G999,
// engine.freight G1000-G1099, engine.spiral G1100-G1199,
// engine.services G1200-G1299); this module's dispatch brief fixed the
// range at G1400-G1499, so G1300-G1399 is left unclaimed (a sibling wave
// item may take it). Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G14" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always
// current — no prior MET-G14xx code existed either place. Every code
// below IS registered in data/errors.json with real severity/module/
// message/remedy fields (GR#7); the internal/foundation/errs
// source-scan test guards against drift.
const (
	// ErrFirmsDataInvalid: data/firms.json could not be loaded or failed
	// this package's schema validation (missing file, malformed JSON, a
	// non-positive or non-monotone stage staff floor, an unknown stage or
	// premise-class slug, a negative founding/credit coefficient, an empty
	// or non-monotone base-rate cycle). Load-time (AC-1).
	ErrFirmsDataInvalid = "MET-G1400"

	// ErrUnknownCitizen: founding or hiring referenced a CitizenID that
	// does not resolve through CitizensAPI (AC-15). Rejected, never a
	// silently-created placeholder citizen.
	ErrUnknownCitizen = "MET-G1401"

	// ErrUnknownFirm: a lifecycle command or query referenced a FirmID not
	// registered in this FirmsAPI (AC-15). Rejected, never a
	// silently-created zero-value firm.
	ErrUnknownFirm = "MET-G1402"

	// ErrCopiedValue: a FirmsAPI method was called on a struct-copied
	// *FirmsAPI (SEC-020-class), mirroring engine.finance/freight/build.
	ErrCopiedValue = "MET-G1403"

	// ErrGrowthBlocked: a growth attempt could not advance the firm — staff
	// roster below the target stage floor (AC-4) or no premises secured
	// (AC-7). Never a silent stage advance.
	ErrGrowthBlocked = "MET-G1404"

	// ErrAlreadyEnterprise: a growth command targeted a firm already at
	// Enterprise (AC-16). Rejected, never silently clamped.
	ErrAlreadyEnterprise = "MET-G1405"

	// ErrInvalidStaffCount: a staff-count query/command carried a
	// non-positive count (AC-16). Rejected, never silently clamped.
	ErrInvalidStaffCount = "MET-G1406"

	// ErrNoPremises: the firm's target-stage premises (right zone class,
	// right size) are not available through engine.build, so growth is
	// blocked and the firm enters the stalled/exit state (AC-7).
	ErrNoPremises = "MET-G1407"

	// ErrCreditDenied: a credit request exceeded the deposit-backed lending
	// capacity (AC-13). Credit cannot be created beyond what the deposit
	// pool supports.
	ErrCreditDenied = "MET-G1408"

	// ErrDependencyMissing: an operation was invoked before a required
	// sibling module (citizens/finance/market/build) was wired (GR#17).
	ErrDependencyMissing = "MET-G1409"

	// ErrInvalidCreditTerms: a credit request carried a non-positive
	// principal. Rejected, never silently clamped to a plausible amount.
	ErrInvalidCreditTerms = "MET-G1410"

	// ErrDuplicateStaff: a hire list contained a CitizenID already on the
	// firm's roster (or duplicated within the list). The roster is a SET of
	// real citizens (AC-4), so a duplicate is rejected, never silently
	// deduped into a smaller headcount.
	ErrDuplicateStaff = "MET-G1411"
)
