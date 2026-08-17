package crime

// Registry error codes for engine.crime (MOD-042). Range: G1500-G1599,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed by eleven earlier engine modules, and the G layer's earlier
// blocks belong to engine.citizens (G000-G099) through engine.firms
// (G1400-G1499); G1500-G1599 is the next free engine sub-range, checked
// against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G15" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards
// against drift.
//
// (The BA's AC-14 wording asks for a "MET-E-range code"; the E layer was
// exhausted before this module landed, so — exactly like engine.citizens'
// G000-G099 and engine.households' G600-G699 before it — engine.crime
// opens its codes in the G-layer second block. Same registry-sourced
// guarantee, current convention.)
const (
	// ErrConfigInvalid: a crime.json field is malformed, non-positive, or
	// violates a structural ordering rule. Rejected rather than silently
	// defaulting a balance figure (GR#15).
	ErrConfigInvalid = "MET-G1500"

	// ErrUnregisteredDistrict: a generation/justice query or command named
	// a district the API has never been told about (no AdvanceMonth or
	// RegisterDistrict introduced it). Never a silently-created zero-value
	// district entry (AC-14).
	ErrUnregisteredDistrict = "MET-G1501"

	// ErrNoConstabularyHQ: a citywide strategy-mix command was issued
	// while no Constabulary Headquarters is built. A station or Divisional
	// HQ alone does not unlock the citywide mix (AC-10).
	ErrNoConstabularyHQ = "MET-G1502"

	// ErrInvalidMix: the three strategy-mix weights do not sum to the
	// documented total. Rejected outright, never silently renormalised
	// (AC-15).
	ErrInvalidMix = "MET-G1503"

	// ErrInvalidDecapitation: a decapitation command named a gang ID that
	// does not exist. Never silently ignored (AC-15).
	ErrInvalidDecapitation = "MET-G1504"

	// ErrCopiedValue: a CrimeAPI method was called on a struct-copied
	// value, not the one New constructed (SEC-020-class).
	ErrCopiedValue = "MET-G1505"

	// ErrPrisonIntakeMissing: VerifyPrisonIntake was called before a
	// PrisonIntake ledger was wired. The cross-check is against an
	// independent ledger, never this module's own say-so (AC-12).
	ErrPrisonIntakeMissing = "MET-G1506"

	// ErrInvalidDistrictInput: a district driver/justice input is
	// non-finite or negative. Rejected with no state change (GR#16).
	ErrInvalidDistrictInput = "MET-G1507"
)
