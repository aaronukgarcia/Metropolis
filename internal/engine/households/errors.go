package households

// Registry error codes for engine.households (MOD-028). Range: G600-G699,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed, and the G layer's earlier blocks belong to engine.citizens
// (G000-G099), engine.projections (G100-G199), engine.finance (G200-G299),
// engine.consumption (G300-G399), engine.logistics (G400-G499), and
// engine.build (G500-G599); G600-G699 is the next free engine sub-range,
// checked against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G6" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards
// against drift.
const (
	// ErrUnknownHousehold: a query referenced a householdId that is not
	// present via CitizensAPI (AC-10). Rejected loudly, never a nil-panic
	// or a silently-zeroed membership/overcrowding/appeal result.
	ErrUnknownHousehold = "MET-G600"

	// ErrUnknownTypology: an appeal/demand/stock lookup named a typology id
	// that is not one of the loaded HS catalogue entries (AC-10). Rejected
	// loudly, never a silently-zeroed appeal.
	ErrUnknownTypology = "MET-G601"

	// ErrInvalidStock: ReportStock was given a negative built-stock count.
	// A stock count is a dwelling-unit quantity and must be non-negative;
	// a negative value is rejected rather than clamped to zero (GR#16).
	ErrInvalidStock = "MET-G602"

	// ErrTypologyDataInvalid: the HS housing-typology catalogue could not be
	// built — data/buildings.json failed to load/validate, or carried no
	// catalogueSection "HS" entries at all. Never a silent empty-catalogue
	// substitution (GR#15/GR#7).
	ErrTypologyDataInvalid = "MET-G603"

	// ErrCopiedValue: a HouseholdsAPI method was called on a struct-copied
	// value, not the one Load/NewFromBuildings constructed (SEC-020-class,
	// mirroring engine.build's MET-G505 and engine.finance's MET-G204).
	ErrCopiedValue = "MET-G604"

	// ErrDependencyMissing: a query was invoked before engine.citizens was
	// wired via SetCitizens. Never a silent no-op (GR#1/GR#17).
	ErrDependencyMissing = "MET-G605"

	// ErrOrphanedMember: a household's member list references a citizen id
	// that does not resolve via CitizensAPI — the conservation invariant's
	// orphan half (no citizen orphaned, no double-housed) surfaced as a
	// loud error rather than a silently-skipped member.
	ErrOrphanedMember = "MET-G606"

	// ErrInvalidAmount: a rent or income figure was negative. Rent and
	// income are non-negative monetary quantities; a negative value is a
	// caller error, rejected before any ratio is computed (GR#16/FEAT-086).
	ErrInvalidAmount = "MET-G607"
)
