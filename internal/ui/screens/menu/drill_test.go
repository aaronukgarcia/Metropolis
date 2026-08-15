package menu

import "testing"

// TestDrillTargets_RegistersDocumentedFigures is SF-5's check (MEN's
// drill-through, consumed not reimplemented): every SF-2-documented figure
// this screen displays — the five current-session figures (seed/tick/month/
// pause/speed, rendered as the single "current session" line) and one entry
// per save slot's name/timestamp/sim-date/summary — is registered into the
// drill-through pair list, and each target names a real source rather than a
// dead end.
func TestDrillTargets_RegistersDocumentedFigures(t *testing.T) {
	entries := []SaveEntry{
		{Name: "autosave-001", Summary: "seed 42 · month 5 · tick 400 · debug off"},
		{Name: "milestone-spring", Summary: "seed 42 · month 3 · tick 120 · debug off"},
	}
	session := Session{WorldSeed: 42, Tick: 400, GameMonth: 5, Paused: false, Speed: 1}

	targets := DrillTargets(entries, session)

	byID := make(map[string]DrillTarget, len(targets))
	for _, tgt := range targets {
		byID[tgt.WidgetID] = tgt
	}

	// The five current-session figures all render into the one "current
	// session" line and drill to the one engine.core.session source.
	sess, ok := byID["menu.session"]
	if !ok {
		t.Errorf("missing drill target for the current-session figures")
	} else if sess.Target != "engine.core.session" {
		t.Errorf("menu.session target = %q, want %q", sess.Target, "engine.core.session")
	}

	// Each save slot's name/timestamp/sim-date/summary figures drill to that
	// slot's own serializer bundle, never a shared or hardcoded target.
	for _, e := range entries {
		wantID := "menu.save." + e.Name
		tgt, ok := byID[wantID]
		if !ok {
			t.Errorf("missing drill target for save slot %q", e.Name)
			continue
		}
		if want := "serializer.bundle." + e.Name; tgt.Target != want {
			t.Errorf("%s target = %q, want %q", wantID, tgt.Target, want)
		}
	}

	// SF-5's "not a dead end": every registered figure names a non-empty
	// target that is not its own widget id (navigation/dead-end detection
	// itself is MOD-038's job — this screen only produces the pairs).
	for _, tgt := range targets {
		if tgt.Target == "" {
			t.Errorf("drill target for %q is empty (a dead end)", tgt.WidgetID)
		}
		if tgt.Target == tgt.WidgetID {
			t.Errorf("drill target for %q points at itself (a dead end)", tgt.WidgetID)
		}
	}
}
