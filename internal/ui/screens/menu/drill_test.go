package menu

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
)

// TestDrillTargets_RegistersDocumentedFigures is SF-5's check (MEN's
// drill-through, consumed not reimplemented): every SF-2-documented figure
// this screen displays — the five current-session figures (seed/tick/month/
// pause/speed, rendered as the single "current session" line) and one entry
// per save slot's name/timestamp/sim-date/summary — is registered into the
// drill-through source list, and each target is a canonical dash.DrillTarget
// with a grammar-valid, real ViewName (not a dead end or a fabricated
// non-view). Resolvability of the save-slot destination itself is asserted
// separately by TestDrillTargets_SaveSlotResolvesToRegisteredViewport.
func TestDrillTargets_RegistersDocumentedFigures(t *testing.T) {
	entries := []SaveEntry{
		{Name: "autosave-001", Summary: "seed 42 · month 5 · tick 400 · debug off"},
		{Name: "milestone-spring", Summary: "seed 42 · month 3 · tick 120 · debug off"},
	}
	session := Session{WorldSeed: 42, Tick: 400, GameMonth: 5, Paused: false, Speed: 1}

	targets := DrillTargets(entries, session)

	if len(targets) != len(entries)+1 {
		t.Fatalf("DrillTargets produced %d targets for %d entries + session, want %d",
			len(targets), len(entries), len(entries)+1)
	}

	// The five current-session figures all render into the one "current
	// session" line and drill to the one f10.session view (whole view).
	sess := targets[0]
	if sess.ViewName != ViewSession {
		t.Errorf("menu.session ViewName = %q, want %q", sess.ViewName, ViewSession)
	}
	if sess.EntityID != "" {
		t.Errorf("menu.session EntityID = %q, want empty (whole view)", sess.EntityID)
	}

	// Each save slot's name/timestamp/sim-date/summary figures drill to the
	// registered F1 viewport a loaded save lands on — the same whole-view
	// destination for every slot (the specific bundle is the Load action's
	// path, not a DrillTarget sub-entity), never a shared hardcoded
	// "serializer.bundle" scope.
	for i, e := range entries {
		tgt := targets[i+1]
		if tgt.ViewName != drillViewSaveSlot {
			t.Errorf("save slot %q ViewName = %q, want %q", e.Name, tgt.ViewName, drillViewSaveSlot)
		}
		if tgt.EntityID != "" {
			t.Errorf("save slot %q EntityID = %q, want empty (whole f1.viewport view)", e.Name, tgt.EntityID)
		}
	}

	// SF-5's "not a dead end": every registered figure names a non-empty,
	// grammar-valid view (navigation/dead-end detection itself is MOD-038's
	// job — this screen only produces the pairs).
	for _, tgt := range targets {
		if tgt.ViewName == "" {
			t.Errorf("drill target (entity %q) ViewName is empty (a dead end)", tgt.EntityID)
		}
		if err := protocol.ValidateViewName(tgt.ViewName); err != nil {
			t.Errorf("drill target (entity %q) ViewName = %q is not grammar-valid: %v", tgt.EntityID, tgt.ViewName, err)
		}
	}
}

// TestDrillTargets_SaveSlotResolvesToRegisteredViewport is the
// resolvability half of SF-5's "not a dead end" (ASM-651): it proves a save
// slot's drill destination actually exists — that it is the registered F1
// map viewport (the view ui.screen.map subscribes to) and is resolvable
// through ui.dash's public DrillTarget/resolver machinery — rather than
// merely being a grammar-valid string. This fails against the pre-fix
// "serializer.bundle" name, which is neither an F-screen key nor an
// engine-domain noun and is absent from code.json and the master plan.
func TestDrillTargets_SaveSlotResolvesToRegisteredViewport(t *testing.T) {
	entries := []SaveEntry{{Name: "autosave-001"}}
	targets := DrillTargets(entries, Session{})

	if len(targets) != len(entries)+1 {
		t.Fatalf("DrillTargets produced %d targets, want %d", len(targets), len(entries)+1)
	}
	slot := targets[1] // targets[0] is the session figure

	// Resolvable: the destination must be the exact view F1 actually
	// subscribes to (a real, code.json-registered view), not a fabricated
	// scope. This is the drift-test shape (weakness pattern #2): menu holds
	// the literal, the test imports the real source and asserts agreement.
	if slot.ViewName != mapscreen.ViewSubscriptionName {
		t.Fatalf("save slot ViewName = %q, want the registered F1 view %q (a fabricated non-view is a dead end)",
			slot.ViewName, mapscreen.ViewSubscriptionName)
	}

	// Naming-doc rule (internal/protocol/subscription.go): segment 1 must
	// be an F-screen key (f1..f12) or an engine-domain noun. "f1" is an
	// F-screen key; "serializer" is neither.
	seg, _, ok := strings.Cut(slot.ViewName, ".")
	if !ok || !fScreenKey(seg) {
		t.Errorf("save slot ViewName %q segment 1 %q is not an F-screen key per int.protocol's view-naming scheme", slot.ViewName, seg)
	}

	// Usable: the target is a valid, resolvable dash.DrillTarget (whole
	// view — f1.viewport has no bundle-named sub-entity).
	if slot.EntityID != "" {
		t.Errorf("save slot EntityID = %q, want empty (whole f1.viewport view)", slot.EntityID)
	}
	if _, err := dash.NewDrillTarget(slot.ViewName, string(slot.EntityID)); err != nil {
		t.Errorf("save slot target (%q, %q) is not a valid dash.DrillTarget: %v", slot.ViewName, slot.EntityID, err)
	}
	res := dash.NewMapResolver()
	res.Mark(slot)
	if !res.Resolve(slot) {
		t.Errorf("save slot target (%q, %q) did not resolve through dash.MapResolver (a dead end)", slot.ViewName, slot.EntityID)
	}
}

// fScreenKey reports whether seg is an F-screen key (f1..f12), the
// screen-scoped segment-1 form of int.protocol's view-naming scheme
// (internal/protocol/subscription.go). int.protocol exposes no exported
// helper for this, so the test mirrors the rule.
func fScreenKey(seg string) bool {
	if len(seg) < 2 || seg[0] != 'f' {
		return false
	}
	n := 0
	for _, r := range seg[1:] {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n >= 1 && n <= 12
}
