package prison

// Registry error codes for engine.prison (MOD-056). Range: G4300-G4399,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The E layer (E000-E999) is fully exhausted and the G layer's blocks
// through G4200-G4299 (engine.fuel) were claimed by earlier engine
// modules, so engine.prison opens G4300-G4399 under BUG-234's
// three-to-four-digit widening. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G43" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always
// current — no prior MET-G43xx code existed either place. Every code
// below IS registered in data/errors.json with real severity/module/
// message/remedy fields (GR#7); the internal/foundation/errs source-scan
// test guards against this ever drifting out of sync.
const (
	// ErrPrisonDataInvalid: data/prison.json could not be loaded or failed
	// schema validation (missing file, malformed JSON, an out-of-range
	// base rate or regime effect, an invalid category set). Wraps the
	// underlying foundation/data error so callers see both this package's
	// correlation ID and, via errors.Unwrap, the original cause.
	ErrPrisonDataInvalid = "MET-G4300"

	// ErrUnknownCitizen: Admit was called for a citizen ID not present in
	// the injected CitizensAPI existence predicate (AC-10). No placeholder
	// inmate record is created — the admission is rejected outright.
	ErrUnknownCitizen = "MET-G4301"

	// ErrUnregisteredCategory: Admit was called with a prison category not
	// present in data/prison.json's "categories" set (AC-10). Never a
	// silently-created placeholder category.
	ErrUnregisteredCategory = "MET-G4302"

	// ErrAlreadyReleased: Release was called for an inmate already released
	// (or never admitted) (AC-11). Never a silent no-op.
	ErrAlreadyReleased = "MET-G4303"

	// ErrInvalidRegimeFunding: a regime-funding command carried an amount
	// outside its documented valid range (negative, or a funding line not
	// one of the three programmes) (AC-11). Never silently clamped.
	ErrInvalidRegimeFunding = "MET-G4304"

	// ErrCopiedValue: a method was called on a struct-copied *PrisonAPI
	// (SEC-020 family, mirroring engine.crime/engine.capexport).
	ErrCopiedValue = "MET-G4305"

	// ErrInvalidAdmission: an Admit command carried a structurally invalid
	// admission (non-positive sentence length, empty offence class, an
	// unknown offence class) (GR#16). Never a silently-clamped record.
	ErrInvalidAdmission = "MET-G4306"

	// ErrSlowFuseRejected: a rehab-spend funding-increase command did not
	// satisfy the Slow-Fuse pre-submission check (AC-9 interim — no
	// FuseYears tag in the documented [5,15] range, or no locally-computed
	// projected-consequence value) (BUG-058 block).
	ErrSlowFuseRejected = "MET-G4307"

	// ErrInvalidReentrySupport: a re-entry support value was set outside
	// its valid [0,1] range (AC-5). Never silently clamped.
	ErrInvalidReentrySupport = "MET-G4308"
)
