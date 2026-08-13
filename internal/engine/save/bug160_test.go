package save

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveManual_RejectsPathTraversalName is BUG-160's exact reproduction
// fixture: a manual save name containing ".." components must be
// rejected outright, before any filesystem write, and nothing must be
// written anywhere on disk -- including OUTSIDE the configured save
// root, which is the whole point of this bug (worse in kind than
// BUG-159's within-tree mis-filing).
func TestSaveManual_RejectsPathTraversalName(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	escapeName := "../../evil-escaped-" + filepath.Base(root)
	err := mgr.SaveManual(fixtureContext(1, 0), escapeName)
	if err == nil {
		t.Fatalf("SaveManual(%q) returned nil error, want ErrUnsafeSaveName (MET-E817)", escapeName)
	}
	if !strings.Contains(err.Error(), ErrUnsafeSaveName) {
		t.Fatalf("SaveManual(%q) error = %v, want it to wrap ErrUnsafeSaveName (MET-E817)", escapeName, err)
	}

	// Nothing must exist inside the configured root...
	if _, statErr := os.Stat(manualDir(root, escapeName)); !os.IsNotExist(statErr) {
		t.Fatalf("manual save dir exists inside root after a rejected SaveManual call (stat err=%v), want nothing written", statErr)
	}
	// ...and nothing must have escaped it either: the exact location
	// BUG-160's live repro wrote to (two levels up from root/manual,
	// i.e. root's own parent's parent) must not exist.
	escapedPath := filepath.Join(filepath.Dir(filepath.Dir(root)), "evil-escaped-"+filepath.Base(root))
	if _, statErr := os.Stat(escapedPath); !os.IsNotExist(statErr) {
		t.Fatalf("BUG-160 escape path %q exists on disk (stat err=%v), want nothing written outside root", escapedPath, statErr)
	}
}

// TestSaveManual_RejectsUnsafeNameEdgeCases exercises every edge case
// BUG-160's fix direction called out: bare ".."/".", empty name, a name
// with an embedded separator mid-string, Windows drive-letter-absolute
// paths, and UNC-shaped paths. All must be rejected with the typed
// ErrUnsafeSaveName error, and nothing may be written to disk for any of
// them.
func TestSaveManual_RejectsUnsafeNameEdgeCases(t *testing.T) {
	cases := []string{
		"..",
		".",
		"",
		"sub/dir",
		"sub\\dir",
		"/leading-slash",
		"\\leading-backslash",
		"trailing-slash/",
		"trailing-backslash\\",
		"/",
		"\\",
		"C:\\evil",
		"C:evil",
		`\\server\share\evil`,
		"embedded\x00null",
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
			mgr := NewManager(root, []Participant{widgets}, "test-corr")

			err := mgr.SaveManual(fixtureContext(1, 0), name)
			if err == nil {
				t.Fatalf("SaveManual(%q) returned nil error, want ErrUnsafeSaveName (MET-E817)", name)
			}
			if !strings.Contains(err.Error(), ErrUnsafeSaveName) {
				t.Fatalf("SaveManual(%q) error = %v, want it to wrap ErrUnsafeSaveName (MET-E817)", name, err)
			}

			// Confirm nothing at all landed under root (staging or
			// otherwise) as a result of the rejected call.
			entries, readErr := os.ReadDir(root)
			if readErr != nil {
				t.Fatalf("os.ReadDir(root) after rejected SaveManual(%q) failed: %v", name, readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("root directory has %d entries after rejected SaveManual(%q), want 0: %v", len(entries), name, entries)
			}
		})
	}
}

// TestSaveManual_UnsafeNameCheckRunsBeforeReservedMarkerCheck confirms
// the two entry-point checks are independent and both fire regardless
// of ordering: a name that is BOTH unsafe (contains a separator) AND
// would also contain the BUG-159 reserved marker text still gets
// rejected -- as ErrUnsafeSaveName, since that check runs first.
func TestSaveManual_UnsafeNameCheckRunsBeforeReservedMarkerCheck(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	name := "../backup.replaced-stage-DEADBEEF01"
	err := mgr.SaveManual(fixtureContext(1, 0), name)
	if err == nil {
		t.Fatalf("SaveManual(%q) returned nil error, want rejection", name)
	}
	if !strings.Contains(err.Error(), ErrUnsafeSaveName) {
		t.Fatalf("SaveManual(%q) error = %v, want it to wrap ErrUnsafeSaveName (MET-E817) since the path-traversal check runs first", name, err)
	}
}

// TestSaveManual_OrdinaryNameStillWorksAfterBUG160Fix confirms the new
// entry-point check does not collaterally reject legitimate manual save
// names -- only names that are actually unsafe to join into a path.
func TestSaveManual_OrdinaryNameStillWorksAfterBUG160Fix(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	for _, name := range []string{"backup", "My Save 1", "before-boss-fight", "save-2026", "a"} {
		if err := mgr.SaveManual(fixtureContext(1, 0), name); err != nil {
			t.Fatalf("SaveManual(%q) = %v, want success (this is an ordinary, safe name)", name, err)
		}
		if _, statErr := os.Stat(manualDir(root, name)); statErr != nil {
			t.Fatalf("SaveManual(%q) succeeded but manual dir is missing: %v", name, statErr)
		}
	}
}
