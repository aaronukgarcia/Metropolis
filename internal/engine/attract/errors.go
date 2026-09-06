package attract

// Registry error codes for engine.attract (MOD-029). Range: G700-G799,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed by eleven earlier engine modules, and the G layer's earlier
// blocks belong to engine.citizens (G000-G099), engine.projections
// (G100-G199), engine.finance (G200-G299), engine.consumption
// (G300-G399), engine.logistics (G400-G499), engine.build (G500-G599),
// and engine.households (G600-G699); G700-G799 is the next free engine
// sub-range, checked against data/errors.json's "ranges.reserved" table
// AND `grep -rn "MET-G7" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards
// against drift.
//
// (The BA's AC-10 wording asks for a "MET-E-range code"; the E layer was
// exhausted before this module landed, so — exactly like engine.citizens'
// G000-G099 and engine.households' G600-G699 before it — engine.attract
// opens its codes in the G-layer second block. Same registry-sourced
// guarantee, current convention.)
const (
	// ErrInvalidWeights: a §11 weight is outside its documented range
	// (finite, [0,1], summing to 1) or non-finite. Rejected rather than
	// silently defaulting to an unweighted or zero-weighted term (AC-10).
	ErrInvalidWeights = "MET-G700"

	// ErrConfigInvalid: a non-weight config field (migration rate,
	// reputation rates, A_world baseline) is non-finite, out of range, or
	// fails the asymmetry rule (fallRate must exceed riseRate — US-2).
	ErrConfigInvalid = "MET-G701"

	// ErrCopiedValue: an AttractAPI method was called on a struct-copied
	// value, not the one New constructed (SEC-020-class, mirroring
	// engine.households' MET-G604 / engine.build's MET-G505).
	ErrCopiedValue = "MET-G702"

	// ErrDependencyMissing: a term/migration query was invoked before the
	// citizens/finance/households dependency was wired. Never a silent
	// no-op (GR#1/GR#17).
	ErrDependencyMissing = "MET-G703"

	// ErrInvalidTermInput: a pushed §11 term value is non-finite or outside
	// its documented [0,100] range, or a monetary housing input is negative
	// (FEAT-086). Rejected with no state change.
	ErrInvalidTermInput = "MET-G704"

	// ErrInvalidCapacity: a migration capacity input (housing vacancy,
	// junction arrival throughput) is negative. Rejected with no state
	// change (FEAT-086).
	ErrInvalidCapacity = "MET-G705"

	// ErrWorldPoolMissing: the §4 world-pool seam (the A_world comparison
	// baseline) is absent — a missing/unloadable A_world, distinguishable
	// from a genuine zero (AC-11). The engine refuses to construct rather
	// than silently defaulting to A_world = 0.
	ErrWorldPoolMissing = "MET-G706"

	// ErrInvalidMonth: a migration command carried a negative simulation
	// month. Rejected rather than wrapped into a migrant birth-month or a
	// hash-stream key (FEAT-086).
	ErrInvalidMonth = "MET-G707"

	// ErrMigrantCounterImplausible: a decoded attract.meta record's
	// nextMigrantID exceeds migrantCounterCeiling (participant.go) — BUG-380
	// round finding P1 (opus-round-bug380): applyLoadRecord's tenure-map
	// backfill loop used to run i=2..nextMigrantID straight off the save
	// with no validation, so a corrupt/hostile nextMigrantID (e.g. 1<<40)
	// drove an unbounded map allocation/hang before any other check ran.
	// Refused BEFORE the backfill loop ever executes — see
	// attack_bug380_round_test.go's
	// TestAttack380_CorruptNextMigrantIDDrivesDecodeAllocation for the
	// attack this closes.
	ErrMigrantCounterImplausible = "MET-G708"
)
