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
