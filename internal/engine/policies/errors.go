package policies

// Registry error codes for engine.policies (MOD-064). Range: G4000-G4099,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed by eleven earlier engine modules and the G layer's G000-G3999
// was claimed by engine.citizens … engine.roads before this module landed,
// so engine.policies is the next G-layer claimant. Checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G40" internal/ cmd/` before claiming, per BUG-008's
// lesson — no prior MET-G40xx code existed either place. Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against drift.
const (
	// ErrPoliciesDataInvalid: data/policies.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, schema
	// violation, unknown policy key, non-finite coefficient delta, unknown
	// scope kind, unknown combination rule, unknown tax-move mode). The
	// library never falls back to a partial or default-substituted set
	// (GR#15/GR#7).
	ErrPoliciesDataInvalid = "MET-G4000"

	// ErrUnknownPolicy: a query or mutation referenced a policy key that is
	// not one of the entries loaded from data/policies.json. Never a
	// zero-value policy silently treated as valid.
	ErrUnknownPolicy = "MET-G4001"

	// ErrPolicyAlreadyActive: Enact was called for a policy that is already
	// active in an identical scope (AC-13). Rejected, never a silent no-op
	// and never a duplicate enactment.
	ErrPolicyAlreadyActive = "MET-G4002"

	// ErrUnknownScope: scope resolution (ResolveScope/Enact) named a
	// DistrictID or road that is not registered. Never a resolved-to-empty-
	// set false success (AC-13).
	ErrUnknownScope = "MET-G4003"

	// ErrUnknownDistrict: a district query (District/RenameDistrict) named
	// a DistrictID that does not exist.
	ErrUnknownDistrict = "MET-G4004"

	// ErrUnknownRoad: a road scope named a RoadID that is not registered.
	ErrUnknownRoad = "MET-G4005"

	// ErrEnactmentNotFound: Repeal (or drift bookkeeping) referenced an
	// enactment ID that is not active.
	ErrEnactmentNotFound = "MET-G4006"

	// ErrScopeMismatch: a policy's declared scope kind does not match the
	// concrete scope target it was enacted/resolved against (e.g. a
	// citywide policy given a district target, or a district policy given
	// a road target).
	ErrScopeMismatch = "MET-G4007"

	// ErrNonFiniteDelta: a coefficient delta decoded from data or submitted
	// was NaN or ±Inf. A non-finite delta would poison the combination and
	// projection arithmetic (GR#16).
	ErrNonFiniteDelta = "MET-G4008"

	// ErrFinanceNotWired: an operation that posts cost/opex ran before
	// SetFinance (GR#17). Never a silently-skipped monetary posting.
	ErrFinanceNotWired = "MET-G4009"

	// ErrProjectionsNotWired: a preview/enactment operation ran before
	// SetProjections (GR#17). Never a silently-skipped model update.
	ErrProjectionsNotWired = "MET-G4010"

	// ErrTaxNotWired: a policy carrying a tax coefficient move was enacted
	// before SetTax (GR#17).
	ErrTaxNotWired = "MET-G4011"

	// ErrCopiedValue: a PoliciesAPI method was called on a struct-copied
	// value (SEC-020-class).
	ErrCopiedValue = "MET-G4012"

	// ErrMonthRegression: AdvanceMonth was called with a month earlier than
	// the current simulation month. Distinct from the unknown-scope and
	// checkpoint errors that previously shared a code with this.
	ErrMonthRegression = "MET-G4013"

	// ErrCheckpointPrecedesCurrentMonth: Checkpoint was called with a month
	// earlier than the current simulation month.
	ErrCheckpointPrecedesCurrentMonth = "MET-G4014"

	// ErrPreviewRangeInverted: a preview was requested with toMonth before
	// fromMonth.
	ErrPreviewRangeInverted = "MET-G4015"

	// ErrEmptyDistrictName: CreateDistrict was called with an empty name.
	ErrEmptyDistrictName = "MET-G4016"

	// ErrEmptyDistrictCells: CreateDistrict was called with no cells (a
	// scope that resolves to nothing would be a resolved-to-empty-set false
	// success, AC-13).
	ErrEmptyDistrictCells = "MET-G4017"

	// ErrEmptyRoadID: RegisterRoad was called with an empty RoadID.
	ErrEmptyRoadID = "MET-G4018"

	// ErrEmptyRoadEdges: RegisterRoad was called with an empty edge set.
	ErrEmptyRoadEdges = "MET-G4019"

	// ErrRoadAlreadyRegistered: RegisterRoad was called for a RoadID that is
	// already registered. Rejected, never silently overwritten.
	ErrRoadAlreadyRegistered = "MET-G4020"

	// ErrEmptyRenameName: RenameDistrict was called with an empty name for a
	// district that exists.
	ErrEmptyRenameName = "MET-G4021"
)
