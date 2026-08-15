package menu

// Destructive regression tests (FEAT-021 attack round). The keymap/layout
// profile-management surface must not hand out, or hold by reference, the
// caller's mutable profile: a defensive copy belongs at the boundary
// (GR#16 type-safe storage boundaries), matching this package's own
// SaveEntries/SettingsSchema/SettingValues accessors and the sibling
// demo.Screen accessors (Population/Personality/Typologies/... all copy).
//
// These tests assert the DESIRED invariant. Against the code as built they
// FAIL: SelectedKeymap/SelectedLayoutProfile return the screen's live
// internal pointer, so a caller mutation corrupts the screen's state, and
// SelectKeymap stores the caller's raw (rejected-bindings-included) profile
// so SaveKeymapFile re-persists bindings ui.keys refused to apply.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

func TestSelectedKeymap_DoesNotAliasInternalState(t *testing.T) {
	g := newTestGrammar("corr-alias")
	if err := g.Register([]string{"b"}, keys.Action{Name: "build", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatal(err)
	}
	km, err := keys.ParseKeymap([]byte(`{"version":1,"bindings":{"b":"b"}}`))
	if err != nil {
		t.Fatal(err)
	}
	s := New("corr-alias")
	if _, err := s.SelectKeymap(km, g); err != nil {
		t.Fatal(err)
	}

	got, have := s.SelectedKeymap()
	if !have {
		t.Fatal("SelectedKeymap() have = false")
	}
	// A caller mutating the returned value must NOT change the screen's state.
	got.Bindings["injected"] = "x"

	again, _ := s.SelectedKeymap()
	if _, ok := again.Bindings["injected"]; ok {
		t.Fatalf("SelectedKeymap() returns the screen's LIVE internal map: a caller mutation leaked into the screen's state (encapsulation leak)")
	}
}

func TestSelectedLayoutProfile_DoesNotAliasInternalState(t *testing.T) {
	s := New("corr-alias-layout")
	s.SelectLayoutProfile(&LayoutProfile{Name: "x", Data: []byte(`{"name":"x"}`)})

	got, have := s.SelectedLayoutProfile()
	if !have {
		t.Fatal("SelectedLayoutProfile() have = false")
	}
	got.Data[0] = '!'

	again, _ := s.SelectedLayoutProfile()
	if again.Data[0] == '!' {
		t.Fatalf("SelectedLayoutProfile() returns the screen's LIVE internal profile: a caller mutation leaked into the screen's state (encapsulation leak)")
	}
}

func TestSelectKeymap_SelectedExcludesRejectedBindings(t *testing.T) {
	g := newTestGrammar("corr-rejected")
	if err := g.Register([]string{"b"}, keys.Action{Name: "build", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatal(err)
	}
	km, err := keys.ParseKeymap([]byte(`{"version":1,"bindings":{"b":"b","x":"z z z"}}`))
	if err != nil {
		t.Fatal(err)
	}
	s := New("corr-rejected")
	rejected, err := s.SelectKeymap(km, g)
	if err != nil {
		t.Fatalf("SelectKeymap: %v", err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %+v, want exactly 1", rejected)
	}

	held, _ := s.SelectedKeymap()
	if _, ok := held.Bindings["x"]; ok {
		t.Fatalf("the screen's selected keymap still holds the REJECTED binding %q: SaveKeymapFile will silently re-persist a binding ui.keys refused to apply (SSOT/GR#3 divergence)", "x")
	}
}
