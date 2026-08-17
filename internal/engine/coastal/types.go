package coastal

// CellCoord is a world-grid cell coordinate. It is engine.coastal's LOCAL
// view of a cell — a plain (X, Y) pair so this package never imports
// engine.world (which is NOT a registered outbound edge of engine.coastal,
// GR#20/code.json). The composition root translates world's TileCoord into
// this shape when wiring the shore source (ASM-207).
type CellCoord struct {
	X int
	Y int
}

// RescueOutcome is the recorded rescue response for one arrival event
// (AC-4): whether a rescue was mounted, and — when coastguard/lifeboat
// capacity was insufficient for the batch — the shortfall magnitude. A
// fully-resourced rescue has CapacityShortfall=false and ShortfallPeople=0,
// so "shortfall" is always distinguishable from "rescue happened".
type RescueOutcome struct {
	// Responded is true when a rescue was mounted (a coastguard/lifeboat
	// response, even a partial one, is still a response).
	Responded bool
	// CapacityShortfall is true when the month's arrivals exceeded the
	// wired rescue capacity (AC-4).
	CapacityShortfall bool
	// ShortfallPeople is the number of arrivals the rescue capacity could
	// not cover (size - remaining capacity), zero when fully resourced.
	ShortfallPeople int64
}

// ArrivalEvent is one small-boat arrival event on a shore cell (AC-2). It
// is generated only by the scheduled [CoastalAPI.Advance] path — there is
// no exported creation command — and carries the factual figures the ticker
// reports (AC-12).
type ArrivalEvent struct {
	// ID is the deterministic event id (drawn from the world-seed hash
	// stream), unique within the module.
	ID uint64
	// Month is the simulation month the boat arrived.
	Month int64
	// Cell is the shore cell the event was placed on.
	Cell CellCoord
	// Size is the number of people aboard.
	Size int64
	// Rescue is the recorded coastguard/lifeboat response (AC-4).
	Rescue RescueOutcome
}

// CaseID is the stable identity of one pipeline case (one person moving
// through the §30 status pipeline).
type CaseID uint64

// CaseStage is the §30 status-pipeline stage of a case: processing
// (awaiting a caseworker or in progress), granted (a citizen), or
// not-granted (managed departure).
type CaseStage uint8

const (
	// CaseProcessing is a case that has not yet resolved (waiting for a
	// caseworker, or already assigned but inside its months-long duration).
	CaseProcessing CaseStage = iota
	// CaseGranted is a case resolved as granted — a citizen record was
	// created through engine.citizens (AC-6).
	CaseGranted
	// CaseNotGranted is a case resolved as not-granted — a managed
	// departure cost was recorded (AC-7).
	CaseNotGranted
)

// Case is the queryable record of one pipeline case (AC-1's per-case
// query). Its terminal state is never deleted: a granted case keeps its
// CitizenID, a not-granted case keeps its DepartureCost (AC-7).
type Case struct {
	ID           CaseID
	ArrivalID    uint64
	Month        int64 // arrival month
	ResolveMonth int64 // 0 = not yet assigned a caseworker
	Stage        CaseStage
	// DepartureCost is the managed-departure cost recorded when a
	// not-granted case resolves (AC-7). Zero otherwise.
	DepartureCost int64
	// CitizenID is the citizen created when a granted case resolves
	// (AC-6). Zero otherwise.
	CitizenID uint64
}

// AdvanceResult is the summary of one [CoastalAPI.Advance] month — the
// factual figures the caller (and the ticker) report. It is a value, so a
// caller can never mutate the module's internal state through it.
type AdvanceResult struct {
	Month                int64
	Arrivals             int   // arrival events generated this month
	NewCases             int64 // people/cases created this month
	ResolvedGranted      int64 // cases resolved granted this month
	ResolvedNotGranted   int64 // cases resolved not-granted this month
	HotelRequisitionCost int64 // hotel-requisition cost recorded this month (AC-5)
	DepartureCost        int64 // managed-departure cost recorded this month (AC-7)
	SatisfactionFriction float64
	Backlog              int64 // cases still awaiting a caseworker after this month
}

// ShoreSource supplies shore-cell membership (§30, §2.1). engine.world owns
// the geography; engine.coastal consumes it through this seam because
// code.json registers NO engine.coastal → engine.world edge (GR#20), and
// engine.world exports no per-cell shore classifier today. The composition
// root wires world's classifier here; tests inject a fake. The shore cells
// are the hand-authored piecewise-linear coastline approximation (ASM-207),
// whose accuracy limits this module inherits.
type ShoreSource interface {
	// ShoreCells returns the current shore cells, in a deterministic order
	// (the caller must not rely on mutation of the returned slice).
	ShoreCells() []CellCoord
	// IsShore reports whether c is classified as shore by the wired world
	// model (AC-14's validation authority).
	IsShore(c CellCoord) bool
}

// ShoreSourceFunc adapts a single classifier to [ShoreSource] for tests and
// one-line wiring; ShoreCells returns an empty set (such a source can
// validate cells but supplies no candidates — useful for AC-14's
// malformed-cell test).
type ShoreSourceFunc func(c CellCoord) bool

// IsShore implements [ShoreSource].
func (f ShoreSourceFunc) IsShore(c CellCoord) bool { return f(c) }

// ShoreCells implements [ShoreSource] with an empty set.
func (f ShoreSourceFunc) ShoreCells() []CellCoord { return nil }
