package roads

// Registry-sourced error codes (GR#7) for engine.roads, claimed in
// data/errors.json's ranges.reserved table under G3900-G3999 — the next
// free four-digit G-layer block after engine.defence's G3800-G3899 (the E
// layer is fully exhausted and G000-G3899 were all claimed by earlier
// engine modules, so engine.roads opens G3900-G3999 under BUG-234's
// three-to-four-digit widening). Checked against this table AND
// `grep -rn "MET-G39" internal/ cmd/` before claiming, per BUG-008's
// lesson — no prior MET-G39xx code existed either place. Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields; the internal/foundation/errs source-scan test guards
// against drift.
const (
	// ErrRoadsDataInvalid: data/roads.json could not be loaded or failed
	// this package's schema validation (a class missing its rate, fewer
	// than the eleven §51 rungs, a non-positive lane count/width, a speed
	// limit outside speedMin..speedMax). Load-time — never a silent default.
	ErrRoadsDataInvalid = "MET-G3900"

	// ErrCopiedValue: a RoadsAPI method was called on a struct-copied value
	// (SEC-020 family, mirroring engine.comms/engine.firms).
	ErrCopiedValue = "MET-G3901"

	// ErrRoadNotFound: an upgrade/roadworks/query named a RoadID that was
	// never added.
	ErrRoadNotFound = "MET-G3902"

	// ErrNodeNotFound: an AddRoad command referenced a NodeID that was
	// never added.
	ErrNodeNotFound = "MET-G3903"

	// ErrInvalidClass: a RoadClass outside the eleven-rung ladder reached a
	// class-aware boundary.
	ErrInvalidClass = "MET-G3904"

	// ErrIncompatibleUpgrade: an ApplyUpgradeCommand target was rejected by
	// the documented compatibility rule.
	ErrIncompatibleUpgrade = "MET-G3905"

	// ErrFootprintObstructed: widening a road's footprint would overlap an
	// occupied (zoned or structured) cell the road does not already own —
	// purchase/demolition must clear the cell first (AC-5).
	ErrFootprintObstructed = "MET-G3906"

	// ErrWorldNotWired: a widening check (or any footprint query) requires
	// engine.world but none is wired. The operation fails closed rather than
	// fabricating a footprint answer (GR#17/GR#20).
	ErrWorldNotWired = "MET-G3907"

	// ErrInvalidRoadworks: a roadworks schedule was malformed — a negative
	// month/duration, a non-positive phase, overlapping phases, or a phase
	// opening more lanes than the road has.
	ErrInvalidRoadworks = "MET-G3908"

	// ErrUnknownObjectKind: a NameFor request used an ObjectKind outside the
	// five documented kinds (AC-13).
	ErrUnknownObjectKind = "MET-G3909"

	// ErrSpeedLimitOutOfBounds: a SetSpeedLimitCommand named a KPH outside
	// the road class's speedMin..speedMax bounds.
	ErrSpeedLimitOutOfBounds = "MET-G3910"

	// ErrMonthRegression: Advance was called with a target month earlier
	// than the current simulated month (time must not run backwards).
	ErrMonthRegression = "MET-G3911"

	// ErrInvalidInput: a numeric/identity input was outside its documented
	// domain (a zero RoadID/NodeID, a negative repair amount, an empty
	// rename, a KindRoad passed to NameFor where NameRoad is required).
	ErrInvalidInput = "MET-G3912"

	// ErrCorpusLoadFailed: data/naming_corpus.json could not be loaded or
	// validated through foundation.data (AC-13) — never a silent
	// empty/placeholder name that could ship visible to the player.
	ErrCorpusLoadFailed = "MET-G3913"

	// ErrNodeReferenced: AddNode tried to move (re-register at a different
	// position) a node that a road already references. Moving it would desync
	// the road's stored footprint from its endpoints (SEC-236), so it is
	// rejected rather than silently overwritten.
	ErrNodeReferenced = "MET-G3914"
)
