package menu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestOpenLayoutEditor_UnavailableWhenNotWired is MEN-4/MEN-7's layout
// half: the F10 → layouts entry point, with no ui.dash editor wired,
// reports "unavailable" (ErrLayoutEditorUnavailable) rather than silently
// no-op'ing or fabricating an editor.
func TestOpenLayoutEditor_UnavailableWhenNotWired(t *testing.T) {
	s := New("corr-layout-unwired") // no WithLayoutEditor
	err := s.OpenLayoutEditor()
	if err == nil {
		t.Fatalf("OpenLayoutEditor() returned nil with no editor wired")
	}
	if e, ok := err.(*errs.E); !ok || e.Code != ErrLayoutEditorUnavailable {
		t.Fatalf("OpenLayoutEditor() error = %T %v, want *errs.E with code %s", err, err, ErrLayoutEditorUnavailable)
	}
}

// TestOpenLayoutEditor_InvokesWiredEditor proves the entry point invokes
// the wired ui.dash editor (MEN-4), passing the selected profile.
func TestOpenLayoutEditor_InvokesWiredEditor(t *testing.T) {
	var got *LayoutProfile
	var called bool
	s := New("corr-layout", WithLayoutEditor(func(p *LayoutProfile) error {
		called = true
		got = p
		return nil
	}))
	s.SelectLayoutProfile(&LayoutProfile{Name: "two-pane", Data: []byte(`{"name":"two-pane"}`)})

	if err := s.OpenLayoutEditor(); err != nil {
		t.Fatalf("OpenLayoutEditor(): %v", err)
	}
	if !called {
		t.Fatalf("wired layout editor was not invoked")
	}
	if got == nil || got.Name != "two-pane" {
		t.Fatalf("editor received profile %+v, want name two-pane", got)
	}
}

// TestLayoutProfile_LoadSelectSave round-trips a layout profile through
// the screen's load/select/save management surface (MEN-4).
func TestLayoutProfile_LoadSelectSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "layout.json")
	if err := os.WriteFile(path, []byte(`{"name":"compact","widgets":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New("corr-layout-io")
	p, err := s.LoadLayoutProfile(path)
	if err != nil {
		t.Fatalf("LoadLayoutProfile: %v", err)
	}
	if p.Name != "compact" {
		t.Errorf("loaded profile name = %q, want compact", p.Name)
	}

	sel, have := s.SelectedLayoutProfile()
	if !have || sel == nil || sel.Name != "compact" {
		t.Fatalf("SelectedLayoutProfile() = %+v have=%v, want compact", sel, have)
	}

	out := filepath.Join(dir, "out.json")
	if err := s.SaveLayoutProfile(out, p); err != nil {
		t.Fatalf("SaveLayoutProfile: %v", err)
	}
	if b := mustReadFile(t, out); len(b) == 0 {
		t.Fatalf("saved layout profile is empty")
	}
}

// TestLoadLayoutProfile_ErrorsAreRegistrySourced is SEC-212's layout half:
// a read or parse failure on LoadLayoutProfile's READ path must return a
// registry-sourced *errs.E (ErrProfileReadFailed) — never a raw
// *os.PathError or json error — matching SaveLayoutProfile's write-path
// discipline (ErrProfileWriteFailed).
func TestLoadLayoutProfile_ErrorsAreRegistrySourced(t *testing.T) {
	s := New("corr-layout-read")

	if _, err := s.LoadLayoutProfile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("LoadLayoutProfile(missing) returned nil error, want ErrProfileReadFailed")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrProfileReadFailed {
		t.Fatalf("LoadLayoutProfile(missing) error = %T %v, want *errs.E with code %s", err, err, ErrProfileReadFailed)
	}

	path := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadLayoutProfile(path); err == nil {
		t.Fatal("LoadLayoutProfile(malformed) returned nil error, want ErrProfileReadFailed")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrProfileReadFailed {
		t.Fatalf("LoadLayoutProfile(malformed) error = %T %v, want *errs.E with code %s", err, err, ErrProfileReadFailed)
	}
}

// TestOpenLayoutEditor_NoSelectedProfileDoesNotCallEditor is SEC-213's
// regression: with an editor wired but no layout profile selected,
// OpenLayoutEditor must fail closed with ErrLayoutEditorUnavailable rather
// than invoking the editor with a nil *LayoutProfile (the documented
// SelectedLayoutProfile "(nil, false)" unavailable state).
func TestOpenLayoutEditor_NoSelectedProfileDoesNotCallEditor(t *testing.T) {
	called := false
	s := New("corr-layout-noprofile", WithLayoutEditor(func(p *LayoutProfile) error {
		called = true
		return nil
	}))
	// No SelectLayoutProfile — the selected profile is nil.

	err := s.OpenLayoutEditor()
	if err == nil {
		t.Fatal("OpenLayoutEditor() with no selected profile returned nil, want ErrLayoutEditorUnavailable")
	}
	if e, ok := err.(*errs.E); !ok || e.Code != ErrLayoutEditorUnavailable {
		t.Fatalf("OpenLayoutEditor() error = %T %v, want *errs.E with code %s", err, err, ErrLayoutEditorUnavailable)
	}
	if called {
		t.Fatal("OpenLayoutEditor() invoked the editor with no selected profile (nil *LayoutProfile)")
	}
}
