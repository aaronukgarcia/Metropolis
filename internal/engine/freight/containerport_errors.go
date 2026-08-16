package freight

// Registry error codes for feat.containerport (FEAT-099), the deep-sea
// container-terminal tier that shares the internal/engine/freight package
// with engine.freight (MOD-047). Range: G2000-G2099, claimed here per
// docs/planning/acceptance/README.md's "Conventions ratified during Sprint 1"
// (per-module error subranges are claimed at build time by the owning
// module/feature, not pre-allocated in a master table). The G layer's blocks
// through G1900-G1999 were all claimed by the time this feature landed
// (engine.freight G1000-G1099, engine.spiral G1100, engine.services G1200,
// engine.tax G1300, engine.firms G1400, engine.crime G1500, engine.farming
// G1600, engine.rail G1700, engine.education G1800, engine.refuse G1900), so
// feat.containerport opens G2000-G2099 (checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G20" internal/ cmd/` before
// claiming, per BUG-008's lesson — no prior MET-G20xx code existed either
// place). Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the internal/foundation/errs
// source-scan test guards against drift.
const (
	// ErrContainerPortBuildRejected: Build was refused — no permit authority
	// wired / permit not granted (AC-7, permit-gated via feat.facilitypermits),
	// below the tier's data-driven milestone gate, a non-upgrade (downgrade or
	// repeat) of an already-built terminal, a day-one decommission liability
	// that cannot be recorded (feat.decommission unbuilt), or an intermodal
	// transfer through an unregistered intermodal point. Never a panic, a
	// silent no-op, a downgrade of activeTier, or a partially-created terminal
	// (AC-8).
	ErrContainerPortBuildRejected = "MET-G2000"

	// ErrContainerPortUnknownTier: a tier query or Build named a port-tier
	// key that is not one of the loaded data/containerport.json tiers
	// (cargo_port_small / container_terminal / deep_sea_terminal). Query- or
	// build-time, never a silently-created zero-value tier (AC-8).
	ErrContainerPortUnknownTier = "MET-G2001"

	// ErrContainerPortDataInvalid: data/containerport.json could not be
	// loaded or failed schema validation (missing file, malformed JSON, a
	// tier missing its key/name/disclosure, a non-positive berth count, a
	// non-positive crane rate/operating hours/customs capacity, a
	// non-positive ship tonnage, a negative jobs/milestone/cost figure, an
	// unknown deepSeaTier key, or a port-tier ladder that is not strictly
	// ascending — deep_sea_terminal must sort strictly above container_terminal
	// on the (milestone, cost) key). Load is all-or-nothing: no partial ladder,
	// no inverted ordering, and no silent default substitution (AC-9). Distinct
	// from ErrContainerPortBuildRejected.
	ErrContainerPortDataInvalid = "MET-G2002"
)
