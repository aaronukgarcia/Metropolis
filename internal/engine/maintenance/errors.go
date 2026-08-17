package maintenance

// Registry error codes for engine.maintenance (MOD-072). Range: G3200-G3299,
// claimed here per the Sprint-1 convention (per-module error subranges are
// claimed at build time by the owning module, not pre-allocated in a master
// table).
//
// The G layer's blocks G000-G3199 were all claimed before this module landed
// (G000-G099 … engine.fiscal G3100-G3199, engine.spaceport G3000-G3099), so
// engine.maintenance opens G3200-G3299 under BUG-234's
// three-digit-to-three-or-four-digit code-format widening. Checked against
// data/errors.json's "ranges.reserved" table AND `grep -rn "MET-G32"
// internal/ cmd/` before claiming, per BUG-008's lesson that the table alone
// is not always current — no prior MET-G32xx four-digit code existed either
// place. Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrMaintenanceDataInvalid: data/maintenance.json could not be loaded or
	// failed this package's schema validation (a class missing its rate, a
	// non-positive lifetime, a malformed class key, a non-positive cost
	// figure, or fewer than two classes). Load-time — never a silent default
	// substitution that would mask a data-authoring bug (AC-12).
	ErrMaintenanceDataInvalid = "MET-G3200"

	// ErrUnknownClass: an operation named a class key that has no entry in
	// the loaded data/maintenance.json (AC-11).
	ErrUnknownClass = "MET-G3201"

	// ErrUnknownStructure: an operation named a structure reference that was
	// never registered (AC-11).
	ErrUnknownStructure = "MET-G3202"

	// ErrNegativeBudget: SetDailyBudget was called with a negative engineer-
	// day budget (AC-11).
	ErrNegativeBudget = "MET-G3203"

	// ErrNegativeAge: AdvanceMonth was called with a negative month advance,
	// which would drive an instance's age negative (AC-11).
	ErrNegativeAge = "MET-G3204"

	// ErrInvalidInput: a numeric input was outside its documented domain
	// (e.g. a zero structure id, a duplicate structure id, a non-positive job
	// cost) (AC-11).
	ErrInvalidInput = "MET-G3205"

	// ErrCopiedValue: a MaintenanceAPI method was called on a struct-copied
	// value (SEC-020 family).
	ErrCopiedValue = "MET-G3206"
)
