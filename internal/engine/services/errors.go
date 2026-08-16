package services

// Registry error codes for engine.services (MOD-033). Range: G1200-G1299,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The E layer (E000-E999) is fully exhausted — eleven earlier engine
// modules (engine.core/detgate/debug/invariant/world/season/market/
// helper/saveux/skeleton/mining) claimed every E sub-range before this
// module landed, so engine modules open a second block under the G layer
// (the registry's documented engine-overflow letter). Every G000-G999
// sub-range and the first three four-digit blocks were claimed by the time
// this module landed — engine.unlocks took G900-G999, engine.freight
// G1000-G1099, and engine.spiral G1100-G1199 in the same wave — so
// engine.services opens G1200-G1299, which BUG-234's 2026-08-14
// code-format widening (three digits → three-or-four) explicitly makes
// valid. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G12" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always
// current — no prior MET-G12xx code existed in either place. Every code
// below IS registered in data/errors.json with real severity/module/
// message/remedy fields (GR#7); the internal/foundation/errs source-scan
// test guards against this ever drifting out of sync.
const (
	// ErrServiceDataInvalid: data/services.json could not be loaded or
	// failed this package's schema validation (missing file, malformed
	// JSON, a non-positive staffing-pool/pie field, an empty staffing
	// pool member list, or a dangling kind.benchmark reference). Load-time
	// (AC-11).
	ErrServiceDataInvalid = "MET-G1200"

	// ErrUnknownServiceKind: RegisterService was called with a ServiceKind
	// that has no KindDef registered — the extensible-kind registry holds
	// the built-in §10 kinds plus whatever synthetic kinds callers have
	// registered, and anything else is rejected rather than silently
	// treated as "a service that exists but is empty" (AC-11).
	ErrUnknownServiceKind = "MET-G1201"

	// ErrServiceNotRegistered: a query (Capacity, FundingLevel, Quality,
	// CoverageRadius, Demand, UpgradePath, GrossWageCost, NetFiscalCost)
	// or command (SetFunding, UpdateDemand, Upgrade) was issued against a
	// ServiceID that was never registered. Query-time, never a zero-value
	// capacity/quality silently returned as "the service exists but is
	// empty" (AC-11).
	ErrServiceNotRegistered = "MET-G1202"

	// ErrInvalidFunding: SetFunding was called with a level outside the
	// valid [0,1] range (negative or above 100%). Rejected with this
	// typed error, never silently clamped without the caller knowing a
	// clamp occurred (AC-12).
	ErrInvalidFunding = "MET-G1203"

	// ErrNotUnlocked: SetFunding was called for a service whose enabling
	// building's §4 milestone tier has not been reached — the tier-gate
	// check routes through the injected UnlockGate (the seam engine.unlocks
	// implements), never a locally-duplicated milestone table (AC-7).
	ErrNotUnlocked = "MET-G1204"

	// ErrUnknownStaffingPool: SetPoolStaff or AllocateStaffing referenced a
	// staffing-pool id that is not declared in data/services.json's
	// staffingPools table. Query-time, never a silently-returned empty
	// allocation for a pool that does not exist (AC-4).
	ErrUnknownStaffingPool = "MET-G1205"

	// ErrUpgradeUnavailable: Upgrade was called on a service already at the
	// final step of its upgrade path (or whose path is empty). Rejected
	// rather than silently leaving the ceiling unchanged (AC-9).
	ErrUpgradeUnavailable = "MET-G1206"

	// ErrDuplicateService: RegisterService was called with a ServiceID that
	// is already registered. A duplicate would let the second registration
	// silently overwrite the first's capacity/funding/upgrade state, so it
	// is rejected instead (GR#3: no silent last-write-wins).
	ErrDuplicateService = "MET-G1207"

	// ErrCopiedValue: a ServicesAPI method was called on a struct-copied
	// value (SEC-020 family). A copied *ServicesAPI would alias its
	// mutex/maps across two values, so the copy guard rejects the call
	// rather than let a torn read/write pass silently.
	ErrCopiedValue = "MET-G1208"

	// ErrNonFiniteInput: a command handler (SetFunding, UpdateDemand,
	// UpdateStaffing, SetPoolStaff) was handed a NaN or ±Inf float where
	// only a finite simulation-state value is meaningful. Rejected at the
	// boundary rather than stored, so NaN can never propagate into the
	// quality/staffing arithmetic and collapse silently (SEC-093).
	ErrNonFiniteInput = "MET-G1209"

	// ErrFiscalOverflow: NetFiscalCost's gross×incomeRate intermediate
	// product overflowed int64, so the income-tax clawback cannot be
	// computed honestly. Surfaced rather than silently returning a net
	// that reads ≈gross at a 100% rate (SEC-094).
	ErrFiscalOverflow = "MET-G1210"
)
