package spiral

// Registry error codes for engine.spiral (MOD-030).
//
// engine.spiral owns the G1100-G1199 sub-range (data/errors.json
// ranges.reserved). The E layer (E000-E999) is fully claimed by eleven
// earlier engine modules, the G layer's three-digit namespace (G000-G999)
// is fully claimed through engine.unlocks (G900-G999), and the four-digit
// blocks opened by BUG-234's format widening were taken in turn by
// engine.freight (G1000-G1099) — G1100-G1199 is the next free G-layer
// engine block. Checked against data/errors.json's ranges.reserved table
// AND `grep -rn "MET-G11" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current — no prior MET-G11xx
// code existed either place. Every code below IS registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7).
const (
	// ErrInvalidScenario: a scripted-shock scenario definition is
	// malformed — an unrecognised shock type, or a shock target that names
	// a nonexistent/out-of-bounds cell/tile (AC-11). Ctx always carries
	// "field" naming the malformed field (shockType / shockTarget) so the
	// failure names exactly what was wrong, and nothing is silently
	// ignored or substituted: the scenario is rejected outright, never
	// run with a defaulted shock.
	ErrInvalidScenario = "MET-G1100"

	// ErrNoDecayToRecover: a recovery command (AC-5) was issued against a
	// cell with no decay state — nothing to demolish/invest in/relieve.
	// Rejected via CommandResult/ErrorRef (AC-12) rather than silently
	// succeeding, so the player can never "recover" a healthy district for
	// free.
	ErrNoDecayToRecover = "MET-G1101"

	// ErrGhostCityNoWarning: the ghost-city dual threshold (population
	// below 10% of a historic peak that exceeded 50,000) was reached but
	// engine.projections' WarningLedger carries no qualifying
	// MarginToGhostCity entry recorded at least MinWarningLeadMonths before
	// this month (AC-15(b), AC-17). The death condition therefore cannot
	// fire — a typed, registry-sourced rejection, never a silent game-over
	// with no warning on record (FEAT-068).
	ErrGhostCityNoWarning = "MET-G1102"

	// ErrCopiedValue: a *DecayAPI method was called on a struct-copied
	// value, not the one New constructed (SEC-020-class hazard: a copy gets
	// its own, independently-zeroed mu while still ALIASING the decay map,
	// event log and population history).
	ErrCopiedValue = "MET-G1103"

	// ErrInvalidMonth: a month argument is negative — before the world's
	// epoch (month 0 = genesis). Rejected rather than folding a negative
	// month into the append-only history, which would corrupt the
	// population provider and the epilogue's timeline.
	ErrInvalidMonth = "MET-G1104"

	// ErrDependencyMissing: an operation that requires a wired dependency
	// (engine.finance for the insolvency death condition, engine.projections
	// for the ghost-city warning gate) was attempted before that dependency
	// was set via SetFinance/SetProjections. Registry-sourced, never a
	// silent no-op or a nil-pointer panic (GR#1).
	ErrDependencyMissing = "MET-G1105"

	// ErrSpiralConfigInvalid: the embedded spiral.json failed to unmarshal
	// or failed validation (a non-positive coefficient, an unreachable
	// blight frontier). Should be unreachable in a built binary, but fails
	// loudly (registry-sourced) rather than defaulting, matching
	// engine.projections' embedded-config convention.
	ErrSpiralConfigInvalid = "MET-G1106"

	// ErrNegativePopulation: a month's population input was negative. A
	// negative population is a caller bug, rejected at the AdvanceMonth
	// boundary BEFORE it can reach the death evaluator — where
	// float64(-N) < 10%-of-peak would otherwise read as "below threshold"
	// and fire a spurious ghost-city game-over (SEC-087, GR#16: never let
	// a wrapped/negative input silently read as a valid signal).
	ErrNegativePopulation = "MET-G1107"
)
