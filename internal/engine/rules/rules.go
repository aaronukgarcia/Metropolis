package rules

import "sync/atomic"

// Version is a rule version: how player commands were judged when a save
// was made. 0 means "before rule versions existed" (today's recorded
// bytes). Each rule change adds exactly one number.
type Version int

// The named rule constants. One per registry entry; the name is what call
// sites pass to [Has], never a literal. See the package documentation for
// what qualifies: only changes to what a player's command does (accepted or
// refused, and what it costs), never a daily simulation formula.
const (
	// RefuseOccupiedCell (version 1, BUG-1224): a second build order on a
	// cell that already holds a queued or standing structure is refused
	// with the cell-in-use error and not charged. Before this version it was accepted
	// and charged.
	RefuseOccupiedCell Version = 1

	// LayRoad (version 2, FEAT-9995385628): a Build order whose building type
	// is "road" is accepted, charged as materials, and on completion makes
	// the cell a road cell. Before this version it was refused as an unknown
	// zone type (MET-G501).
	LayRoad Version = 2
)

// Current is the live rule version: the number of the highest WIRED registry
// entry, never a pending one. It is the version that stamps journal frames
// and snapshots and that live play runs at; replay of recorded history runs
// at the version recorded with it. A rule registered as pending wiring is
// not in force, so it cannot raise Current: a frame stamped with a number
// whose rule the engine does not enforce would later replay WITH the rule
// once it is wired. Whoever wires a rule flips its entry to wired AND raises
// Current in the same commit as the call site (the registry test enforces
// Current == the highest wired entry, and that the first pending entry is
// exactly Current+1). Rule 2 (FEAT-9995385628, build module) is wired, so Current is 2.
const Current Version = 2

// testCurrent is the test-only override of [Effective] (see
// SetCurrentForTest in testhelper.go). Zero means no override.
var testCurrent atomic.Int64

// Effective is the live rule version the engine actually uses: [Current]
// in production, or the value a test pinned with SetCurrentForTest. Code
// that needs "the current version" outside a unit test reads Effective (the
// same files that may read Current); the bounds checks below use it so a
// test can stand in for a future build that has wired a rule.
func Effective() Version {
	if o := testCurrent.Load(); o > 0 {
		return Version(o)
	}
	return Current
}

// OldestSupported is the oldest version this build can still replay.
// Raising it is the retirement mechanism: a snapshot or journal frame
// older than it is refused. It is a variable only so tests can exercise
// the refusal; production code never assigns it.
var OldestSupported Version = 0

// Has reports whether the rule introduced at version introduced is in
// force when the active version is active. introduced must be a named
// constant from this package.
func Has(active, introduced Version) bool {
	return active >= introduced
}

// Active returns the version a module should judge commands by: the
// getter's answer, or [Current] when the module was built without a getter
// (a unit test of the bare module, or live play with nothing injected).
func Active(getter func() Version) Version {
	if getter == nil {
		return Effective()
	}
	return getter()
}

// IsKnown reports whether v is a version this build has a registry entry
// for (or version 0). A version above [Current] is a save from a newer
// build and must be refused, never clamped.
func IsKnown(v Version) bool {
	return v >= 0 && v <= Effective()
}

// Support is the verdict of [Classify].
type Support int

// The verdicts. Callers map each refusal to its registry code
// (ErrSavedUnderNewerRules and so on, errors.go).
const (
	// SupportOK: the version can be replayed by this build.
	SupportOK Support = iota
	// SupportTooNew: v > Current. Refuse, code MET-G5610.
	SupportTooNew
	// SupportTooOld: v < OldestSupported. Refuse, code MET-G5611.
	SupportTooOld
	// SupportInvalid: v < 0. A malformed stamp, code MET-G5613.
	SupportInvalid
)

// Classify says whether this build can replay history recorded at version
// v. It is the one check lane code calls before applying any state.
func Classify(v Version) Support {
	switch {
	case v < 0:
		return SupportInvalid
	case v > Effective():
		return SupportTooNew
	case v < OldestSupported:
		return SupportTooOld
	}
	return SupportOK
}
