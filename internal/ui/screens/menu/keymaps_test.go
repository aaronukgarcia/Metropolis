package menu

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

func newTestGrammar(correlationID string) *keys.KeyGrammar {
	return keys.NewKeyGrammar(
		keys.ClockFunc(func() time.Time { return time.Time{} }),
		time.Minute, // idle timeout — irrelevant here, no timeout is driven
		3,           // dim-after-uses — irrelevant here
		correlationID,
	)
}

// TestSelectKeymap_SurfacesUiKeysRejection is MEN-3's binding check: this
// screen is a consumer of ui.keys' validated-load path — a keymap entry
// binding to an unregistered action is surfaced as ui.keys' own
// keys.KeymapEntryError (rejected per-entry), while the valid bindings in
// the same profile still load. The screen does not swallow the report and
// does not re-implement validation.
func TestSelectKeymap_SurfacesUiKeysRejection(t *testing.T) {
	g := newTestGrammar("corr-grammar")
	if err := g.Register([]string{"b"}, keys.Action{Name: "build", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatalf("Register(b): %v", err)
	}

	km, err := keys.ParseKeymap([]byte(`{
		"version": 1,
		"bindings": {"b": "b", "x": "z z z"}
	}`))
	if err != nil {
		t.Fatalf("ParseKeymap: %v", err)
	}

	s := New("corr-keymap")
	rejected, err := s.SelectKeymap(km, g)
	if err != nil {
		t.Fatalf("SelectKeymap: %v", err)
	}

	// The rejection report is ui.keys' own type, verbatim — not swallowed,
	// not re-derived by this screen.
	if len(rejected) != 1 {
		t.Fatalf("SelectKeymap() rejected = %+v, want exactly 1 entry (x -> z z z)", rejected)
	}
	if rejected[0].PhysicalKey != "x" || rejected[0].MnemonicPath != "z z z" {
		t.Errorf("rejected[0] = %+v, want {PhysicalKey:x MnemonicPath:\"z z z\"}", rejected[0])
	}
	if _, ok := interface{}(rejected[0]).(keys.KeymapEntryError); !ok {
		t.Fatalf("rejected[0] type = %T, want keys.KeymapEntryError (surfaced verbatim)", rejected[0])
	}

	// The valid binding still loaded (AC-11b: per-entry rejection, rest of
	// the profile loads), and this screen held the selected keymap.
	held, have := s.SelectedKeymap()
	if !have || held == nil {
		t.Fatalf("SelectedKeymap() have = %v, want true after a SelectKeymap", have)
	}
	if held.Bindings["b"] != "b" {
		t.Errorf("held keymap binding \"b\" = %q, want \"b\" (the valid binding must still load)", held.Bindings["b"])
	}
}

// TestLoadKeymapFile_RoundTripAndRejection drives the file path: a profile
// file with one bad binding surfaces the rejection while the rest loads,
// and a clean profile loads with an empty rejection report.
func TestLoadKeymapFile_RoundTripAndRejection(t *testing.T) {
	g := newTestGrammar("corr-grammar2")
	if err := g.Register([]string{"b"}, keys.Action{Name: "build", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "keymap.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"bindings":{"b":"b","y":"q q"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New("corr-keymap-file")
	rejected, err := s.LoadKeymapFile(path, g)
	if err != nil {
		t.Fatalf("LoadKeymapFile: %v", err)
	}
	if len(rejected) != 1 || rejected[0].PhysicalKey != "y" {
		t.Fatalf("LoadKeymapFile rejected = %+v, want exactly [{y q q}]", rejected)
	}

	// Save the held keymap and reload it — a clean round-trip (MEN-3's
	// "save" action).
	out := filepath.Join(dir, "out.json")
	if err := s.SaveKeymapFile(out); err != nil {
		t.Fatalf("SaveKeymapFile: %v", err)
	}
	km2, err := keys.ParseKeymap(mustReadFile(t, out))
	if err != nil {
		t.Fatalf("re-parsing saved keymap: %v", err)
	}
	if km2.Bindings["b"] != "b" {
		t.Errorf("saved keymap binding b = %q, want b", km2.Bindings["b"])
	}
}

// TestSaveKeymapFile_NoSelectionFails is MEN-7's keymap half: with no
// keymap loaded/selected, saving fails with a registry error (GR#7), not
// a silent empty write.
func TestSaveKeymapFile_NoSelectionFails(t *testing.T) {
	s := New("corr-keymap-noselect")
	err := s.SaveKeymapFile(filepath.Join(t.TempDir(), "x.json"))
	if err == nil {
		t.Fatalf("SaveKeymapFile() returned nil with no keymap selected")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return b
}

// TestLoadKeymapFile_ErrorsAreRegistrySourced is SEC-212's keymap half: a
// read or parse failure on LoadKeymapFile's READ path must return a
// registry-sourced *errs.E (ErrProfileReadFailed) — never a raw
// *os.PathError or keys parse error — matching SaveKeymapFile's write-path
// discipline (ErrProfileWriteFailed) and ui.keys' own MET-U302 wrapping.
func TestLoadKeymapFile_ErrorsAreRegistrySourced(t *testing.T) {
	g := newTestGrammar("corr-km-read")
	s := New("corr-km-read")

	// Read failure: a nonexistent path surfaces ErrProfileReadFailed, not
	// a raw *os.PathError, and selects nothing.
	if _, err := s.LoadKeymapFile(filepath.Join(t.TempDir(), "missing.json"), g); err == nil {
		t.Fatal("LoadKeymapFile(missing) returned nil error, want ErrProfileReadFailed")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrProfileReadFailed {
		t.Fatalf("LoadKeymapFile(missing) error = %T %v, want *errs.E with code %s", err, err, ErrProfileReadFailed)
	}
	if _, have := s.SelectedKeymap(); have {
		t.Fatal("LoadKeymapFile(missing) selected a keymap; want none")
	}

	// Parse failure: malformed JSON surfaces ErrProfileReadFailed, not the
	// raw json error from keys.ParseKeymap.
	path := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadKeymapFile(path, g); err == nil {
		t.Fatal("LoadKeymapFile(malformed) returned nil error, want ErrProfileReadFailed")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrProfileReadFailed {
		t.Fatalf("LoadKeymapFile(malformed) error = %T %v, want *errs.E with code %s", err, err, ErrProfileReadFailed)
	}
}

// TestSelectKeymap_NilArgsRejected is SEC-079's regression: a nil keymap or
// nil grammar must be rejected with a registry-sourced error (fail-closed),
// never a nil-pointer panic inside keys.ApplyKeymap. Against the unguarded
// code both calls panic; this test also asserts that no keymap was selected
// on the rejected path.
func TestSelectKeymap_NilArgsRejected(t *testing.T) {
	g := newTestGrammar("corr-nil-g")
	km, err := keys.ParseKeymap([]byte(`{"version":1,"bindings":{"b":"b"}}`))
	if err != nil {
		t.Fatal(err)
	}

	s := New("corr-nil-km")
	if _, err := s.SelectKeymap(nil, g); err == nil {
		t.Fatal("SelectKeymap(nil, g) returned nil error, want ErrNilKeymapOrGrammar")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrNilKeymapOrGrammar {
		t.Fatalf("SelectKeymap(nil, g) error = %v, want *errs.E with code %s", err, ErrNilKeymapOrGrammar)
	}
	if _, have := s.SelectedKeymap(); have {
		t.Fatal("SelectKeymap(nil, g) selected a keymap; want none")
	}

	s2 := New("corr-nil-g2")
	if _, err := s2.SelectKeymap(km, nil); err == nil {
		t.Fatal("SelectKeymap(km, nil) returned nil error, want ErrNilKeymapOrGrammar")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrNilKeymapOrGrammar {
		t.Fatalf("SelectKeymap(km, nil) error = %v, want *errs.E with code %s", err, ErrNilKeymapOrGrammar)
	}
	if _, have := s2.SelectedKeymap(); have {
		t.Fatal("SelectKeymap(km, nil) selected a keymap; want none")
	}
}
