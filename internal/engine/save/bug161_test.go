package save

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveManual_RejectsC0ControlCharacters is BUG-161's exact
// reproduction fixture: each of the specific bypass characters the
// report called out (tab, newline, BEL 0x07, ESC 0x1b, backspace 0x08)
// must be rejected individually with the typed ErrUnsafeSaveName
// (MET-E817), not some other error and not a raw OS error surfaced
// late from writeBundleLocked's promote-rename step.
func TestSaveManual_RejectsC0ControlCharacters(t *testing.T) {
	cases := map[string]string{
		"tab":       "evil\tname",
		"newline":   "evil\nname",
		"BEL":       "evil\x07name",
		"ESC":       "evil\x1bname",
		"backspace": "evil\x08name",
	}

	for label, name := range cases {
		t.Run(label, func(t *testing.T) {
			root := t.TempDir()
			widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
			mgr := NewManager(root, []Participant{widgets}, "test-corr")

			err := mgr.SaveManual(fixtureContext(1, 0), name)
			if err == nil {
				t.Fatalf("SaveManual(%q) returned nil error, want ErrUnsafeSaveName (MET-E817)", name)
			}
			if !strings.Contains(err.Error(), ErrUnsafeSaveName) {
				t.Fatalf("SaveManual(%q) error = %v, want it to wrap ErrUnsafeSaveName (MET-E817), not a raw OS error", name, err)
			}

			assertNothingWrittenUnder(t, root, name)
		})
	}
}

// TestSaveManual_RejectsWhitespaceOnlyName confirms a name that is
// empty after trimming whitespace -- or consists entirely of
// whitespace -- is rejected with ErrUnsafeSaveName, before any
// filesystem I/O.
func TestSaveManual_RejectsWhitespaceOnlyName(t *testing.T) {
	cases := []string{" ", "   ", "\t\t", "\t \t", "\n", " \t\n "}

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

			assertNothingWrittenUnder(t, root, name)
		})
	}
}

// TestSaveManual_RejectsOverlongName confirms a name well past
// maxSaveNameLen (255) is rejected with ErrUnsafeSaveName rather than
// being allowed to reach writeBundleLocked and fail late with a raw OS
// error about an invalid filename.
func TestSaveManual_RejectsOverlongName(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	name := strings.Repeat("a", 5000)
	err := mgr.SaveManual(fixtureContext(1, 0), name)
	if err == nil {
		t.Fatalf("SaveManual(<5000-char name>) returned nil error, want ErrUnsafeSaveName (MET-E817)")
	}
	if !strings.Contains(err.Error(), ErrUnsafeSaveName) {
		t.Fatalf("SaveManual(<5000-char name>) error = %v, want it to wrap ErrUnsafeSaveName (MET-E817)", err)
	}

	assertNothingWrittenUnder(t, root, name)

	// A name right at the boundary (exactly maxSaveNameLen) must still
	// be accepted -- confirms the bound is the documented 255, not some
	// smaller accidental cutoff.
	boundaryRoot := t.TempDir()
	boundaryMgr := NewManager(boundaryRoot, []Participant{newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})}, "test-corr")
	boundaryName := strings.Repeat("b", maxSaveNameLen)
	if err := boundaryMgr.SaveManual(fixtureContext(1, 0), boundaryName); err != nil {
		t.Fatalf("SaveManual(<%d-char name>) = %v, want success (exactly at maxSaveNameLen)", maxSaveNameLen, err)
	}
}

// TestSaveManual_RejectionHappensBeforeAnyFilesystemIO confirms the
// core of BUG-161: for every bypass class, nothing is written to
// .staging/ or anywhere else on disk under root -- the rejection must
// happen before writeBundleLocked's real filesystem I/O, not merely
// eventually error out after doing that I/O.
func TestSaveManual_RejectionHappensBeforeAnyFilesystemIO(t *testing.T) {
	names := []string{
		"evil\x07name",
		"evil\tname",
		"evil\nname",
		"evil\x1bname",
		"evil\x08name",
		"   ",
		"\t\t",
		strings.Repeat("z", 5000),
	}

	for _, name := range names {
		root := t.TempDir()
		widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
		mgr := NewManager(root, []Participant{widgets}, "test-corr")

		if err := mgr.SaveManual(fixtureContext(1, 0), name); err == nil {
			t.Fatalf("SaveManual(%q) returned nil error, want rejection", name)
		}

		assertNothingWrittenUnder(t, root, name)
	}
}

// TestSaveManual_OrdinaryNamesStillWorkAfterBUG161Fix confirms the
// broadened check does not collaterally reject legitimate manual save
// names -- including names with spaces in the middle, which must
// remain valid (only whitespace-ONLY names are rejected).
func TestSaveManual_OrdinaryNamesStillWorkAfterBUG161Fix(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	names := []string{
		"backup",
		"My Save 1",
		"before-boss-fight",
		"save-2026",
		"a",
		"  padded but not empty  ", // leading/trailing spaces, non-whitespace content
		"multi word save name here",
	}
	for _, name := range names {
		if err := mgr.SaveManual(fixtureContext(1, 0), name); err != nil {
			t.Fatalf("SaveManual(%q) = %v, want success (this is an ordinary, safe name)", name, err)
		}
		if _, statErr := os.Stat(manualDir(root, name)); statErr != nil {
			t.Fatalf("SaveManual(%q) succeeded but manual dir is missing: %v", name, statErr)
		}
	}
}

// assertNothingWrittenUnder confirms root contains zero entries after a
// rejected SaveManual call for name -- in particular, no ".staging/"
// directory or partially-written bundle, which is exactly what
// BUG-161 reported reaching disk before the late promote-rename
// failure.
func assertNothingWrittenUnder(t *testing.T, root, name string) {
	t.Helper()
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("os.ReadDir(root) after rejected SaveManual(%q) failed: %v", name, readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("root directory has %d entries after rejected SaveManual(%q), want 0: %v", len(entries), name, entries)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".staging")); !os.IsNotExist(statErr) {
		t.Fatalf("SaveManual(%q) left a .staging directory behind (stat err=%v), want none", name, statErr)
	}
}
