package destination

// Registry error codes for engine.destination (MOD-061). Range: G4600-G4699,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The E layer (E000-E999) is fully exhausted and the G layer's blocks
// through G4500-G4599 (engine.extcommute) were claimed by earlier engine
// modules, so engine.destination opens G4600-G4699 under BUG-234's
// three-to-four-digit widening. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G46" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always
// current — no prior MET-G46xx code existed either place.
const (
	// ErrInvalidSite: placement named an unrecognised archetype kind, a
	// non-finite or under-minimum site footprint, an empty reclamation site
	// key, or an out-of-range BDI input. Rejected outright, never a
	// silently-clamped or silently-defaulted placement (AC-10/GR#16).
	ErrInvalidSite = "MET-G4600"

	// ErrUnknownDestination: a query named a destination id this API has
	// never placed. Never a silently-created zero entry (AC-10).
	ErrUnknownDestination = "MET-G4601"

	// ErrMalformedConfig: data/destination.json could not be loaded or
	// failed schema validation (missing job count, negative footprint, an
	// out-of-range draw/blight coefficient). Wraps the underlying
	// foundation/data error; never a silent default figure (AC-11/GR#15).
	ErrMalformedConfig = "MET-G4602"

	// ErrNotReclamationSite: a mega-mall placement named a site the
	// engine.mining blight model does not report as an exhausted,
	// not-yet-reclaimed extraction pit (§32, AC-3). Never accepted with a
	// cosmetic reclamation skin.
	ErrNotReclamationSite = "MET-G4603"

	// ErrCopiedValue: a method was called on a struct-copied *DestAPI,
	// not the one New constructed (SEC-020 family).
	ErrCopiedValue = "MET-G4604"

	// ErrDependencyMissing: an operation that needs a registered outbound
	// dependency (tourism for regional draw, mining for reclamation/blight,
	// parking for demand) was issued before that seam was wired. Fail
	// closed, never a silent zero (GR#17).
	ErrDependencyMissing = "MET-G4605"
)
