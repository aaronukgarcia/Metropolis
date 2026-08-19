package extcommute

// Registry error codes for engine.extcommute (MOD-035). Range: G4800-G4899,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at build
// time by the owning module, not pre-allocated in a master table). The E
// layer (E000-E999) is fully exhausted and the G layer's blocks through
// G4400-G4499 (engine.tourism) were claimed by earlier engine modules, so
// engine.extcommute opens G4800-G4899 under BUG-234's three-to-four-digit
// widening. Checked against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G48" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current — no prior MET-G48xx code
// existed either place.
//
// NOTE (registration pending): these codes are const-declared and used via
// errs.New/Wrap (GR#7), but data/errors.json registration is owned by the
// lead and is NOT performed in this change. Until the codes below are added
// to data/errors.json, errs.New returns its documented MET-F003
// "unregistered code" fallback that still preserves the requested code in
// Ctx["code"]; the package's tests assert these codes via a helper that
// accepts both the registered and the pending-registration form.
const (
	// ErrExtCommuteDataInvalid: data/extcommute.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, a non-positive
	// transport-capacity figure). Wraps the underlying foundation/data error.
	ErrExtCommuteDataInvalid = "MET-G4800"

	// ErrExternalWorldDataInvalid: data/external_world.json could not be
	// loaded or failed schema validation, OR a pool's capacity curve does not
	// cover era 1 (the world's starting era), OR a pool names a transport
	// channel with no capacity entry in data/extcommute.json. Wraps the
	// underlying foundation/data error (AC-15: never a silent zero-capacity
	// or unlimited-capacity default).
	ErrExternalWorldDataInvalid = "MET-G4801"

	// ErrPoolFull: an off-map assignment was requested for a pool whose
	// finite, era-scaled capacity is already exhausted (AC-3/AC-4). The
	// first cap of the two-cap model. Distinct from ErrTransportCapacity and
	// ErrAlreadyOffMap.
	ErrPoolFull = "MET-G4802"

	// ErrTransportCapacity: an off-map assignment was requested for a pool
	// with free slots but no reaching transport leg with available capacity
	// after engine.traffic's congestion (AC-8). The second cap of the
	// two-cap model (A6b/R6(b)). Distinct from ErrPoolFull.
	ErrTransportCapacity = "MET-G4803"

	// ErrAlreadyOffMap: an off-map assignment was requested for a citizen
	// already holding an off-map job (AC-11). Never silently overwrites the
	// first assignment.
	ErrAlreadyOffMap = "MET-G4804"

	// ErrUnknownPool: a command named a pool id not present in
	// data/external_world.json. Never a silently-created placeholder pool.
	ErrUnknownPool = "MET-G4805"

	// ErrUnknownCitizen: an off-map assignment was requested for a citizen id
	// the injected citizens seam reports absent (AC-11/GR#17). Never a
	// fabricated placeholder citizen.
	ErrUnknownCitizen = "MET-G4806"

	// ErrNotOffMapAssigned: a release or assignment query named a citizen not
	// currently holding an off-map job. Never a silent no-op.
	ErrNotOffMapAssigned = "MET-G4807"

	// ErrInvalidEra: an era/tier argument lay outside the milestone ladder
	// (1..13, §4). Rejected rather than silently clamped to a neighbouring
	// tier (GR#16).
	ErrInvalidEra = "MET-G4808"

	// ErrCopiedValue: a method was called on a struct-copied *ExtCommuteAPI
	// (SEC-020 family, mirroring engine.crime / engine.prison).
	ErrCopiedValue = "MET-G4809"

	// ErrInvalidInput: a command carried a structurally invalid numeric input
	// (a negative vacancy count, a non-finite or out-of-range congestion
	// figure) (GR#16). Never silently clamped.
	ErrInvalidInput = "MET-G4810"

	// ErrDependencyNotWired: a stateful operation required a registered
	// dependency seam (engine.citizens, engine.traffic, engine.finance) that
	// the composition root has not wired. Fails closed rather than silently
	// skipping the check (GR#17/GR#20).
	ErrDependencyNotWired = "MET-G4811"

	// ErrNoEligiblePool: SelectPool found no pool with both a free slot and
	// an available reaching transport leg at the given era (AC-16). Never a
	// silently-picked ineligible pool.
	ErrNoEligiblePool = "MET-G4812"
)
