package policies

// Registry error codes for engine.policies (MOD-064). The module owns the
// G4000-G4099 block reserved for it in data/errors.json's ranges.reserved
// table; it raises exactly the codes below, G4000-G4012 (thirteen codes),
// which are the ones registered in the canonical data/errors.json. The
// remaining reserved slots (G4013-G4099) are intentionally unclaimed.
//
// The E layer (E000-E999) was fully claimed by eleven earlier engine
// modules and G000-G3999 was claimed by engine.citizens … engine.roads
// before this module landed, so engine.policies is the next G-layer
// claimant. Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
//
// NOTE (error-range discipline): an earlier draft of this file minted nine
// additional codes (G4013-G4021) for defensive input validations (month
// regression, empty district/road inputs, duplicate road registration,
// etc.). Those codes were never registered in the canonical
// data/errors.json — the module's registered range ends at G4012 — so they
// are removed here and their call sites re-mapped onto the closest
// registered code by meaning: G4003 (unknown/malformed scope) for
// malformed or empty-resolving inputs, G4004 (unknown district) for
// invalid district identity, G4005 (unknown road) for invalid road
// identity.
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
	// DistrictID or road that is not registered, or the input was otherwise
	// malformed (including a month/range regression and an empty district
	// or road cell/edge set, which would resolve to an empty set — a
	// resolved-to-empty-set false success, AC-13). Never a silent no-op.
	ErrUnknownScope = "MET-G4003"

	// ErrUnknownDistrict: a district query (District/RenameDistrict) or a
	// district-shaped input named a DistrictID that does not exist or is
	// empty (no valid district identity).
	ErrUnknownDistrict = "MET-G4004"

	// ErrUnknownRoad: a road-shaped input named a RoadID that is not
	// registered or is empty (no valid road identity).
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
)
