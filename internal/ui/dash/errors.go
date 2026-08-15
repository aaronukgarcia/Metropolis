package dash

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// Error-code sub-range for ui.dash: MET-U800..U899, claimed from
// data/errors.json's ranges.reserved table. The U-layer sub-ranges are
// taken through U700-U799 (ui.core U000, ui.screen.map U100,
// ui.screen.debug U200, ui.keys U300, feat.devmode U400, ui.screen.demo
// U500, ui.screen.menu U600, ui.screen.ticker U700), so U800-U899 is the
// next free U-layer sub-range. Checked against the table AND a live
// source scan (grep -rn "MET-U8" internal/ cmd/) before claiming, per
// BUG-008's lesson that the table alone is not always current — no prior
// MET-U8xx code existed either place.
const (
	// codeDrillUnavailable is raised when a DrillTarget's resolved
	// subscription/entity no longer exists at drill time (int.protocol's
	// stale-subscription/vanished-entity case) — AC-9.
	codeDrillUnavailable = "MET-U800"

	// codeMalformedProfile is raised when a saved layout-profile JSON
	// blob is malformed or corrupt; LoadProfile falls back to the shipped
	// default layout for the screen alongside this error — AC-10.
	codeMalformedProfile = "MET-U801"

	// codeTileNeedsDrill is raised when a tile is constructed or added
	// with a zero/empty DrillTarget (AC-4: the drill target is required,
	// with no zero-value/optional path).
	codeTileNeedsDrill = "MET-U802"

	// codeInvalidViewName is raised when a DrillTarget's ViewName does
	// not match int.protocol's view-name grammar (protocol.ValidateViewName).
	codeInvalidViewName = "MET-U803"

	// codeUnknownTile is raised when an editor/navigation operation
	// (RemoveTile, MoveTile, Drill) names a tile ID not present in the
	// layout.
	codeUnknownTile = "MET-U804"

	// codeMapResolverCopied is raised when a MapResolver method is called
	// on a struct copy of the value NewMapResolver returned (SEC-020): mu
	// is a sync.RWMutex VALUE (a copy gets its own, independent lock)
	// while live (a map) is a reference type a copy ALIASES, so an
	// unrejected copy is a second lock domain racing the original over
	// the same map. Rejected fail-closed before mu is ever touched.
	codeMapResolverCopied = "MET-U805"

	// codeDashboardCopied is raised when a Dashboard method is called on a
	// struct copy of the value NewDashboard returned (SEC-020): mu is a
	// sync.RWMutex VALUE (a copy gets its own, independent lock) while
	// layout.tiles (a slice) is a reference type a copy ALIASES, so an
	// unrejected copy is a second lock domain racing the original over
	// the same backing array. Rejected fail-closed before mu is ever
	// touched.
	codeDashboardCopied = "MET-U806"
)

// corr mints a correlation ID for registry-sourced errors raised on a
// validation path that has no more specific caller-supplied ID to thread
// through (GR#1).
func corr() string { return errs.NewCorrelationID() }
