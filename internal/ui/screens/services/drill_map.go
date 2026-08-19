package services

import "github.com/aaronukgarcia/Metropolis/internal/ui/dash"

// coverageJumpView is the drill-through destination view name SVC-3's
// coverage-map jump names: the F1 map viewport ("f1.viewport"), the
// registered F-screen-key view ui.screen.map actually subscribes to
// (mapscreen.ViewSubscriptionName) — mirrors ui.screen.menu's
// drillViewSaveSlot precedent (menu/render.go) exactly: this screen holds
// the literal so it need not import ui.screen.map from production code
// (SF-1 stays scoped to protocol-only consumption of the ENGINE, but a
// cross-screen literal is still checked for drift — see drill_map_test.go,
// which imports mapscreen in a _test.go file only, the sanctioned
// exception menu's drill_test.go already establishes, and asserts this
// constant equals mapscreen.ViewSubscriptionName).
const coverageJumpView = "f1.viewport"

// CoverageJumpTarget returns SVC-3's coverage-map jump target for
// serviceID: a canonical dash.DrillTarget naming a real, registered view
// (coverageJumpView) so the plumbing is not a fabricated non-view (GR#3).
//
// BLOCKED (tripwire, SVC-3): the target does not currently RESOLVE.
// ui.screen.map's own AC-3 (per-service coverage overlay, part of its
// documented "overlay cycle: ownership, land value, zoning, ...") was
// explicitly deferred out of scope at FEAT-005's Sprint 1 dispatch (see
// internal/ui/screens/map/doc.go's "Scope" section: "real terrain/
// citizens/traffic overlays populate once those engine modules land") and
// has not landed since — ui.screen.map never dash.MapResolver.Mark()s a
// per-service coverage entity, so Enter on this target is presently a
// dead end through no fault of this screen. This screen implements no
// second coverage renderer of its own (the AC's explicit instruction) and
// invents no map-side overlay semantics — it registers the real target
// name and waits for ui.screen.map's AC-3 to make it live. Flagged for
// Bill as a BUG-058-adjacent candidate: SVC-3 cannot be closed until
// ui.screen.map's AC-3 lands.
func CoverageJumpTarget(serviceID string) dash.DrillTarget {
	return dash.DrillTarget{ViewName: coverageJumpView, EntityID: "coverage." + serviceID}
}
