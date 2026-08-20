package airunits

// Registry error codes for engine.airunits (MOD-074). Range: G4900-G4999,
// claimed here at build time per the Sprint-1 convention (per-module error
// subranges are claimed by the owning module, not pre-allocated in a master
// table). The E layer (E000-E999) is fully exhausted and the G layer's blocks
// through G4000-G4099 (engine.policies) were claimed by earlier engine
// modules; G4900-G4999 is the next free four-digit block under BUG-234's
// three-to-four-digit code-format widening. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G49" internal/ cmd/` before
// claiming, per BUG-008's lesson — no prior MET-G49xx code existed either
// place.
//
// NOTE: these codes are defined here and REPORTED for registration, but are
// not yet present in data/errors.json (this package's build was scoped not to
// edit the shared registry). The package's own tests exercise them against a
// local testdata/errors.json fixture via METROPOLIS_ERRORS_PATH; once the
// lead registers the codes below in data/errors.json, errs.New resolves them
// from the real registry and the same assertions hold unchanged.
const (
	// ErrAirunitsDataInvalid: data/helicopters.json could not be loaded or
	// failed schema validation (a type missing its cost, a negative
	// running-cost component, an unrecognised type key, a role defined
	// without its effect). Load-time — never a silent default substitution
	// that would mask a data-authoring bug (AC-12).
	ErrAirunitsDataInvalid = "MET-G4900"

	// ErrUnknownUnitType: an operation named a unit-type key with no entry in
	// data/helicopters.json's "units" set (AC-11).
	ErrUnknownUnitType = "MET-G4901"

	// ErrUnknownUnit: an operation named a chopper ID that was never
	// purchased/registered (AC-11). Assigning a pilot to, dispatching, or
	// querying a nonexistent chopper returns this — never a silently-created
	// placeholder chopper.
	ErrUnknownUnit = "MET-G4902"

	// ErrInsufficientFunds: a purchase command could not settle its capital
	// cost against the finance seam (AC-11). No chopper is created and no
	// finance state is mutated on this path.
	ErrInsufficientFunds = "MET-G4903"

	// ErrNoPilot: a flight/dispatch command was issued for a chopper with no
	// trained pilot assigned (AC-5, AC-11). "No pilot, no flight" is
	// mechanical, not a flavour note.
	ErrNoPilot = "MET-G4904"

	// ErrUnqualifiedPilot: AssignPilot was called with a citizen the staffing
	// seam reports is not a trained pilot (AC-5, AC-11). The chopper's pilot
	// and state are left unchanged.
	ErrUnqualifiedPilot = "MET-G4905"

	// ErrGroundedDispatch: a dispatch command was issued for a chopper that is
	// out-of-service (maintenance, pilot removal, or weather grounding)
	// (AC-11). Never a silently-accepted no-op.
	ErrGroundedDispatch = "MET-G4906"

	// ErrMilestoneLocked: a purchase command was issued before the type's
	// data-loaded unlock milestone (AC-3). Choppers are a prestige/late-game
	// asset, mechanically gated, not a hope.
	ErrMilestoneLocked = "MET-G4907"

	// ErrWeatherGrounded: a dispatch command was issued while the world seam
	// reports adverse weather (wind at or above the data-loaded grounding
	// threshold) (AC-7, AC-11).
	ErrWeatherGrounded = "MET-G4908"

	// ErrCopiedValue: an AirUnitsAPI method was called on a struct-copied
	// value, not the one New constructed (SEC-020 family).
	ErrCopiedValue = "MET-G4909"

	// ErrInvalidInput: a numeric command input was outside its documented
	// domain (a negative amount, a negative month advance, a negative weather
	// wind speed) (GR#16, AC-11).
	ErrInvalidInput = "MET-G4910"

	// ErrPilotAlreadyAssigned: AssignPilot was called with a pilot already
	// assigned to a DIFFERENT chopper (AC-5, MOD-074 r1). One pilot may never
	// be live on two choppers at once, so the assignment is rejected
	// fail-closed and nothing mutates. Release the pilot from their current
	// chopper (RemovePilot) before reassigning.
	ErrPilotAlreadyAssigned = "MET-G4911"
)
