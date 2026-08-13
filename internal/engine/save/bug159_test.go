package save

import (
	"os"
	"strings"
	"testing"
)

// TestSaveManual_RejectsNameCollidingWithReplacedMarker is BUG-159's
// exact reproduction fixture: a manual save name that happens to
// contain bundle.go's internal ".replaced-stage-<random>" crash-
// recovery marker must be rejected outright, not silently accepted and
// written to disk (where it would then be permanently hidden by List's
// isReplacedSiblingName filter).
func TestSaveManual_RejectsNameCollidingWithReplacedMarker(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	err := mgr.SaveManual(fixtureContext(1, 0), "backup.replaced-stage-DEADBEEF01")
	if err == nil {
		t.Fatalf("SaveManual with a marker-colliding name returned nil error, want ErrReservedSaveName (MET-E816)")
	}
	if !strings.Contains(err.Error(), ErrReservedSaveName) {
		t.Fatalf("SaveManual error = %v, want it to wrap ErrReservedSaveName (MET-E816)", err)
	}

	// Nothing must have been written to disk — the rejection happens
	// before any filesystem call.
	if _, statErr := os.Stat(manualDir(root, "backup.replaced-stage-DEADBEEF01")); !os.IsNotExist(statErr) {
		t.Fatalf("manual save dir exists after a rejected SaveManual call (stat err=%v), want nothing written", statErr)
	}
}

// TestSaveManual_OrdinaryNameStillWorks confirms BUG-159's fix does not
// collaterally reject legitimate, unrelated manual save names — only
// names that actually contain the internal marker text.
func TestSaveManual_OrdinaryNameStillWorks(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	for _, name := range []string{"backup", "My Save 1", "before-boss-fight", "replaced", "stage-1"} {
		if err := mgr.SaveManual(fixtureContext(1, 0), name); err != nil {
			t.Fatalf("SaveManual(%q) = %v, want success (this name does not contain the reserved marker)", name, err)
		}
		if _, statErr := os.Stat(manualDir(root, name)); statErr != nil {
			t.Fatalf("SaveManual(%q) succeeded but manual dir is missing: %v", name, statErr)
		}
	}
}

// TestSaveManual_ReservedNameRejectionPreventsUnrelatedSlotDeletion is
// BUG-159's second finding: because the colliding name is now rejected
// AT CREATION TIME, the "save to slot backup deletes an unrelated real
// save named backup.replaced-stage-<suffix>" data-loss scenario can no
// longer occur — the dangerous name never makes it onto disk in the
// first place for reapDisplacedSiblings to later glob-match.
func TestSaveManual_ReservedNameRejectionPreventsUnrelatedSlotDeletion(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	// The attack precondition -- a save literally named with the
	// colliding suffix -- can no longer be created at all.
	if err := mgr.SaveManual(fixtureContext(1, 0), "backup.replaced-stage-DEADBEEF01"); err == nil {
		t.Fatalf("SaveManual with the colliding name succeeded, want rejection (precondition for the data-loss scenario must be unreachable)")
	}

	// Save to the real, unrelated slot "backup" — this used to be the
	// step that triggered reapDisplacedSiblings' glob match and deleted
	// the (never-created, in the fixed world) colliding-named save.
	if err := mgr.SaveManual(fixtureContext(2, 0), "backup"); err != nil {
		t.Fatalf("SaveManual(%q) = %v, want success", "backup", err)
	}

	// Nothing named with the colliding suffix exists to have been
	// deleted, and the real "backup" slot is intact.
	if _, statErr := os.Stat(manualDir(root, "backup.replaced-stage-DEADBEEF01")); !os.IsNotExist(statErr) {
		t.Fatalf("colliding-named save exists on disk (stat err=%v), want it never created", statErr)
	}
	if _, statErr := os.Stat(manualDir(root, "backup")); statErr != nil {
		t.Fatalf("real 'backup' slot missing after save: %v", statErr)
	}
}
