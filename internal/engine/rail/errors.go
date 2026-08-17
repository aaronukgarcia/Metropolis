package rail

// Registry error codes for the engine.rail stub (MOD-060's stand-in — see
// doc.go). Range: G1700-G1799, claimed here per docs/planning/acceptance/
// README.md's "Conventions ratified during Sprint 1" (per-module error
// subranges are claimed at build time by the owning module). The G layer's
// blocks through G1600-G1699 were claimed by engine.citizens … engine.crime
// and engine.farming by the time this stub landed, so engine.rail opens
// G1700-G1799 (the range the sibling wave's engine.education entry already
// names for engine.rail) — checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G17" internal/ cmd/` before
// claiming, per BUG-008's lesson — no prior MET-G17xx code existed either
// place. Every code below IS registered in data/errors.json (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrRailTransferRejected: an intermodal transfer declared a non-positive
	// tonnage, an unknown transport mode, a from==to no-op handoff, a tonnage
	// exceeding the intermodal modal cap (the smaller of the source and
	// destination modes' per-movement max from data/freight.json), or a
	// tonnage below a mode's per-movement minimum (sea's 3kt coaster floor —
	// SEC-125). Rejected loudly, never silently ignored or clamped
	// (engine.rail.md AC-3's conservation contract cannot be met by a
	// malformed, over-cap or below-min transfer, and engine.freight.md AC-13
	// mandates "reject, don't clamp").
	ErrRailTransferRejected = "MET-G1700"

	// ErrRailCopiedValue: a RailAPI method was called on a struct-copied
	// *RailAPI (SEC-020 family, mirroring engine.freight's ErrCopiedValue).
	// Always construct exactly one *RailAPI via NewRailAPI and pass its
	// pointer everywhere.
	ErrRailCopiedValue = "MET-G1701"

	// ErrRailDataInvalid: the rail stub could not read the road/rail/sea
	// per-movement modal caps from data/freight.json (missing file, malformed
	// JSON, a mode missing its maxTonnesPerMovement, or a minTonnesPerMovement
	// that is negative or exceeds its max). NewRailAPI fails rather than
	// returning a caps-less surface that would silently accept an over-cap or
	// below-min transfer (GR#7/GR#15).
	ErrRailDataInvalid = "MET-G1702"
)
