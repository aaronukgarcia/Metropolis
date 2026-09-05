package save

import "testing"

// TestGameMode_DeclaredOnSave (AC-4): the GameMode field is written into
// Meta on the initial save and every subsequent save (SaveManual,
// Autosave, Milestone all funnel through writeBundle, so one test per
// trigger path proves all three).
func TestGameMode_DeclaredOnSave(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	ctx := fixtureContext(10, 1)
	ctx.GameMode = "real"
	if err := mgr.SaveManual(ctx, "mode-check"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	meta, err := ReadMeta(manualDir(root, "mode-check"))
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.GameMode != "real" {
		t.Fatalf("manual save meta.GameMode = %q, want %q", meta.GameMode, "real")
	}

	autosaveCtx := fixtureContext(20, 2)
	autosaveCtx.GameMode = "unlimited"
	if err := mgr.Autosave(autosaveCtx); err != nil {
		t.Fatalf("Autosave: %v", err)
	}
	autoMeta, err := ReadMeta(autosaveDir(root, 0))
	if err != nil {
		t.Fatalf("ReadMeta(autosave): %v", err)
	}
	if autoMeta.GameMode != "unlimited" {
		t.Fatalf("autosave meta.GameMode = %q, want %q", autoMeta.GameMode, "unlimited")
	}

	milestoneCtx := fixtureContext(30, 3)
	milestoneCtx.GameMode = "real"
	if err := mgr.Milestone(milestoneCtx, Tier{Number: 1, Name: "Hamlet"}); err != nil {
		t.Fatalf("Milestone: %v", err)
	}
	msMeta, err := ReadMeta(milestoneDir(root, Tier{Number: 1, Name: "Hamlet"}))
	if err != nil {
		t.Fatalf("ReadMeta(milestone): %v", err)
	}
	if msMeta.GameMode != "real" {
		t.Fatalf("milestone meta.GameMode = %q, want %q", msMeta.GameMode, "real")
	}
}

// TestGameMode_SaveLoadRoundTrip (AC-4): a save/load round-trip preserves
// the mode verbatim through Manager.Load's returned Meta.
func TestGameMode_SaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	ctx := fixtureContext(1, 0)
	ctx.GameMode = "unlimited"
	if err := mgr.SaveManual(ctx, "roundtrip"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	_, meta, err := mgr.Load(manualDir(root, "roundtrip"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if meta.GameMode != "unlimited" {
		t.Fatalf("Load meta.GameMode = %q, want %q", meta.GameMode, "unlimited")
	}
}

// TestGameMode_LoadEnforcesMatch (AC-5): loading a save whose Meta.GameMode
// differs from the current session's expected mode FAILS closed with
// ErrGameModeMismatch, and never mutates any registered participant.
func TestGameMode_LoadEnforcesMatch(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "alpha", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	ctx := fixtureContext(1, 0)
	ctx.GameMode = "real"
	if err := mgr.SaveManual(ctx, "real-save"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	// Load into a FRESH participant so a wrongly-applied load would be
	// observable — the widget state must stay at its zero value if the
	// mode check correctly refuses before any Handler runs.
	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets}, "test-corr")

	_, _, err := loadMgr.Load(manualDir(root, "real-save"), WithExpectedGameMode("unlimited"))
	if err == nil {
		t.Fatalf("Load with mismatched expected mode succeeded, want ErrGameModeMismatch")
	}
	if len(loadWidgets.State()) != 0 {
		t.Fatalf("Load refused for a mode mismatch but still mutated the widget participant: %+v", loadWidgets.State())
	}
}

// TestGameMode_LoadEnforcesMatch_ReverseDirection is the symmetric case
// (AC-5's "and vice-versa"): an unlimited-mode save loaded by a real-mode
// session is refused too.
func TestGameMode_LoadEnforcesMatch_ReverseDirection(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	ctx := fixtureContext(1, 0)
	ctx.GameMode = "unlimited"
	if err := mgr.SaveManual(ctx, "unlimited-save"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	if _, _, err := mgr.Load(manualDir(root, "unlimited-save"), WithExpectedGameMode("real")); err == nil {
		t.Fatalf("Load with mismatched expected mode (unlimited save, real session) succeeded, want ErrGameModeMismatch")
	}
}

// TestGameMode_LoadAcceptsMatch proves the check is not simply always-fail
// — a matching mode loads successfully.
func TestGameMode_LoadAcceptsMatch(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	ctx := fixtureContext(1, 0)
	ctx.GameMode = "real"
	if err := mgr.SaveManual(ctx, "matching"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	if _, _, err := mgr.Load(manualDir(root, "matching"), WithExpectedGameMode("real")); err != nil {
		t.Fatalf("Load with matching expected mode failed: %v", err)
	}
}

// TestGameMode_AbsentModeAssumedRealNeverUnlimited (BUG-737 round-2 lead
// ruling, 2026-09-05 — REPLACES the original AC-5 text this test's name
// used to be TestGameMode_AbsentModeRejected for): a save written with
// NO mode field (a pre-FEAT-143 bundle, GameMode == "") loads ONLY into
// the conservative REAL session (with a non-fatal ErrLegacyGameModeAssumedReal
// WARN, never silent) — the original blanket-rejection rule broke every
// save bundle written before FEAT-143 shipped, with no migration path at
// all. Loading the SAME absent-mode bundle into an UNLIMITED session
// still refuses — an absent mode is never treated as "matches unlimited"
// (the original false-pass-risk note survives for this direction).
func TestGameMode_AbsentModeAssumedRealNeverUnlimited(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	// fixtureContext's GameMode is left at its zero value ("") —
	// simulating a pre-FEAT-143 save that never set Context.GameMode.
	ctx := fixtureContext(1, 0)
	if err := mgr.SaveManual(ctx, "no-mode"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	meta, err := ReadMeta(manualDir(root, "no-mode"))
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.GameMode != "" {
		t.Fatalf("precondition: expected an absent GameMode, got %q", meta.GameMode)
	}

	if _, _, err := mgr.Load(manualDir(root, "no-mode"), WithExpectedGameMode("real")); err != nil {
		t.Fatalf("Load of a mode-less save with WithExpectedGameMode(\"real\") refused, want the legacy-migration ACCEPT: %v", err)
	}
	if _, _, err := mgr.Load(manualDir(root, "no-mode"), WithExpectedGameMode("unlimited")); err == nil {
		t.Fatal("Load of a mode-less save with WithExpectedGameMode(\"unlimited\") succeeded, want ErrGameModeMismatch — an absent mode must never be treated as matching unlimited")
	}
}

// TestGameMode_ZeroOptionLoadUnaffected proves the zero-option Load
// behaviour is completely unchanged: a mode-less OR mode-carrying save
// loads fine with no WithExpectedGameMode passed at all — the pre-
// FEAT-143 caller (any Load site that never opts in) sees byte-for-byte
// identical behaviour.
func TestGameMode_ZeroOptionLoadUnaffected(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	ctx := fixtureContext(1, 0)
	ctx.GameMode = "real"
	if err := mgr.SaveManual(ctx, "no-opt-in"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	if _, _, err := mgr.Load(manualDir(root, "no-opt-in")); err != nil {
		t.Fatalf("zero-option Load failed: %v", err)
	}
}
