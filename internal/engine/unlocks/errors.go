package unlocks

// Registry error codes for engine.unlocks (MOD-032). Range: G900-G999,
// claimed here at build time per the per-module error-subrange convention
// (README.md "Conventions ratified during Sprint 1"). The E layer
// (E000-E999) is fully claimed by eleven earlier engine modules and the G
// layer's G000-G899 is claimed by engine.citizens, engine.projections,
// engine.finance, engine.consumption, engine.logistics, engine.build,
// engine.households, engine.attract and feat.compositionroot, so
// G900-G999 was the next free G sub-range. Checked against
// data/errors.json's "ranges.reserved" table AND `grep -rn "MET-G9"
// internal/ cmd/` before claiming, per BUG-008's lesson that the table
// alone is not always current — no prior MET-G9xx code existed either
// place. Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrDataInvalid: data/unlock_trees.json could not be loaded or
	// failed foundation.data's schema validation (missing file, malformed
	// JSON, a missing/duplicate category, an unknown node kind, a
	// prereqNodeIds edge that does not resolve, or a cyclic prereq graph).
	// Load-time, wrapped around foundation.data's own *errs.E so callers
	// see one consistent engine.unlocks code (AC-13/GR#7).
	ErrDataInvalid = "MET-G900"

	// ErrUnregisteredGate: a gate check or spend command referenced a
	// node id, milestone tier, or category that is not present in the
	// loaded data/unlock_trees.json (a typo'd node id, an out-of-range
	// tier index). Returned as a typed error, never a silent
	// "not unlocked" false negative indistinguishable from a genuine gate
	// failure (AC-12).
	ErrUnregisteredGate = "MET-G901"

	// ErrInsufficientDP: SpendDevelopmentPoints was asked to spend more
	// Development Points than the current unspent balance holds (AC-7).
	ErrInsufficientDP = "MET-G902"

	// ErrTierPrerequisite: SpendDevelopmentPoints was asked to unlock a
	// node whose declared milestone-tier prerequisite has not been
	// reached yet (AC-7).
	ErrTierPrerequisite = "MET-G903"

	// ErrUnknownOffMapKind: BuyOffMapCapacity/OffMapCapacity was asked
	// for an off-map capacity kind outside the five §22 names (grid, gas,
	// rail, port, water) (AC-9).
	ErrUnknownOffMapKind = "MET-G904"

	// ErrFinanceNotWired: an operation that must post a real ledger
	// transaction (a milestone cash award, an off-map capacity purchase)
	// was attempted with no engine.finance dependency wired (GR#1/GR#17
	// — never a silent no-op).
	ErrFinanceNotWired = "MET-G905"

	// ErrDebugRequired: ForceUnlock was invoked without a debug
	// authorizer wired, or the wired authorizer denied the request.
	// Debug-gated capabilities never run partially or invisibly with
	// debug off (AC-11/M0-ENG §3).
	ErrDebugRequired = "MET-G906"

	// ErrInvalidUnlockTarget: ForceUnlock was given a target that names
	// neither (or both of) a valid milestone tier and a valid tree node,
	// or a tier outside the 1-13 domain (AC-11).
	ErrInvalidUnlockTarget = "MET-G907"

	// ErrCopiedValue: a UnlocksAPI method was called on a struct-copied
	// value, not the one Load/LoadDefault constructed (SEC-020-class).
	ErrCopiedValue = "MET-G908"

	// ErrNegativeAmount: an XP award, Development-Point amount,
	// population, or other quantity was negative where only a
	// non-negative value is meaningful (GR#16).
	ErrNegativeAmount = "MET-G909"

	// ErrNodeAlreadyUnlocked: SpendDevelopmentPoints was asked to unlock
	// a tree node that is already unlocked; the DP balance is left
	// untouched rather than silently drained a second time (GR#1).
	ErrNodeAlreadyUnlocked = "MET-G910"
)
