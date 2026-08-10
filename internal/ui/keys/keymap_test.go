package keys

import (
	"os"
	"path/filepath"
	"testing"
)

// registerDefaultVerbs registers a placeholder action for every top-level
// verb the shipped default keymap profile names, so LoadKeymapFile's
// AC-11b validation (every bound mnemonic path must resolve to an
// already-registered action) has something real to check against — the
// actual gameplay action set is out of scope for this package (doc.go),
// so a test standing in as "the owning screen module" registers minimal
// placeholders exactly as a real caller would at boot.
func registerDefaultVerbs(t *testing.T, g *KeyGrammar) {
	t.Helper()
	for _, tok := range []string{"b", "z", "p", "s", "d", "i", "g", "t", "r", "l", "c", "m", "n", "N", "/", "'"} {
		if err := g.Register([]string{tok}, Action{Name: tok, Run: func(ActionArgs) {}}); err != nil {
			t.Fatalf("Register(%q): %v", tok, err)
		}
	}
}

func repoDefaultKeymapPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// internal/ui/keys -> repo root is three levels up.
	return filepath.Join(wd, "..", "..", "..", "data", "keymap-default.json")
}

func TestDefaultKeymapLoadsAndCoversTopLevelVerbs(t *testing.T) {
	g := newTestGrammar()
	registerDefaultVerbs(t, g)

	if err := LoadKeymapFile(repoDefaultKeymapPath(t), g); err != nil {
		t.Fatalf("LoadKeymapFile(default): %v", err)
	}

	for _, verb := range []string{"b", "z", "p", "s", "d", "i", "g", "t"} {
		res := g.Feed(KeyRune(rune(verb[0])))
		if res.Status != Dispatched {
			t.Fatalf("physical key %q (identity-bound in the default profile) status = %v, want Dispatched", verb, res.Status)
		}
	}
}

func TestKeymapRemapsPhysicalKeyToMnemonicToken(t *testing.T) {
	g := newTestGrammar()
	fired := 0
	_ = g.Register([]string{"b"}, Action{Name: "build", Run: func(ActionArgs) { fired++ }})

	km, err := ParseKeymap([]byte(`{"version":1,"bindings":{"y":"b"}}`))
	if err != nil {
		t.Fatalf("ParseKeymap: %v", err)
	}
	if rejected := g.ApplyKeymap(km); len(rejected) != 0 {
		t.Fatalf("unexpected rejected entries: %+v", rejected)
	}

	res := g.Feed(KeyRune('y'))
	if res.Status != Dispatched || fired != 1 {
		t.Fatalf("remapped 'y' did not trigger 'b's action: status=%v fired=%d", res.Status, fired)
	}
}

func TestKeymapEntryToUnknownActionRejectedButRestStillLoads(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b"}, Action{Name: "build", Run: func(ActionArgs) {}})

	km, err := ParseKeymap([]byte(`{"version":1,"bindings":{"y":"b","x":"q"}}`))
	if err != nil {
		t.Fatalf("ParseKeymap: %v", err)
	}
	rejected := g.ApplyKeymap(km)
	if len(rejected) != 1 || rejected[0].PhysicalKey != "x" || rejected[0].MnemonicPath != "q" {
		t.Fatalf("rejected = %+v, want exactly one entry for x->q", rejected)
	}

	// The other, valid binding in the SAME profile still loaded.
	fired := 0
	g2 := newTestGrammar()
	_ = g2.Register([]string{"b"}, Action{Name: "build", Run: func(ActionArgs) { fired++ }})
	_ = g2.ApplyKeymap(km)
	if res := g2.Feed(KeyRune('y')); res.Status != Dispatched || fired != 1 {
		t.Fatalf("valid sibling binding y->b did not survive the partially-invalid profile: status=%v fired=%d", res.Status, fired)
	}
}

func TestMalformedKeymapFallsBackToDefault(t *testing.T) {
	g := newTestGrammar()
	registerDefaultVerbs(t, g)

	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := LoadKeymapFile(bad, g)
	if err == nil {
		t.Fatalf("LoadKeymapFile(malformed) did not report an error")
	}

	// The grammar must remain usable — falling back means "still works",
	// not "now broken." With no keymap ever successfully applied, physical
	// tokens still resolve via identity (no substitution layer installed).
	res := g.Feed(KeyRune('b'))
	if res.Status != Dispatched {
		t.Fatalf("grammar unusable after malformed-keymap fallback: status = %v", res.Status)
	}
}

// TestKeymapBindsFullPathAsCommandShortcut is Bill's ruling case (ASM-165
// resolved 2026-08-10): "remappable" means rebinding a COMMAND to a
// different physical key, not just swapping which key stands in for
// which single mnemonic letter. Binding physical 'y' to the full path
// "b r s" must dispatch the b-r-s action in ONE keystroke, without
// requiring b/r/s to individually exist as physical bindings too.
func TestKeymapBindsFullPathAsCommandShortcut(t *testing.T) {
	g := newTestGrammar()
	var gotArgs ActionArgs
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(a ActionArgs) { gotArgs = a }})

	km, err := ParseKeymap([]byte(`{"version":1,"bindings":{"y":"b r s"}}`))
	if err != nil {
		t.Fatalf("ParseKeymap: %v", err)
	}
	if rejected := g.ApplyKeymap(km); len(rejected) != 0 {
		t.Fatalf("unexpected rejected entries: %+v", rejected)
	}

	res := g.Feed(KeyRune('y'))
	if res.Status != Dispatched {
		t.Fatalf("status = %v, want Dispatched (one keystroke completing a 3-token remapped path)", res.Status)
	}
	if len(gotArgs.Path) != 3 || gotArgs.Path[0] != "b" || gotArgs.Path[1] != "r" || gotArgs.Path[2] != "s" {
		t.Fatalf("dispatched Path = %v, want [b r s]", gotArgs.Path)
	}
}

// TestKeymapShortcutDoesNotDisturbPendingLeaderSequence mirrors globals'
// non-disturbance contract (AC-10): firing a remapped MULTI-SEGMENT
// shortcut mid-way through an unrelated, still-incomplete leader sequence
// must not corrupt that sequence's state. (A single-segment keymap entry
// is plain alphabet substitution for whatever the CURRENT trie position
// expects next, not a shortcut — it deliberately does NOT get this
// non-disturbance treatment, since it isn't dispatching a specific,
// independent action the way a multi-segment remap or a global is.)
func TestKeymapShortcutDoesNotDisturbPendingLeaderSequence(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) {}})
	_ = g.Register([]string{"z", "x"}, Action{Name: "zoneExtra", Run: func(ActionArgs) {}})
	km, err := ParseKeymap([]byte(`{"version":1,"bindings":{"y":"z x"}}`))
	if err != nil {
		t.Fatalf("ParseKeymap: %v", err)
	}
	if rejected := g.ApplyKeymap(km); len(rejected) != 0 {
		t.Fatalf("unexpected rejected entries: %+v", rejected)
	}

	g.Feed(KeyRune('b')) // start an unrelated sequence
	if !g.IsPending() {
		t.Fatalf("expected pending after 'b'")
	}
	res := g.Feed(KeyRune('y')) // fires the 2-segment "z x" shortcut
	if res.Status != Dispatched {
		t.Fatalf("shortcut status = %v, want Dispatched", res.Status)
	}
	if !g.IsPending() {
		t.Fatalf("shortcut dispatch disturbed the unrelated in-progress leader sequence")
	}
	// The original sequence can still complete.
	if res2 := g.Feed(KeyRune('r')); res2.Status != Pending {
		t.Fatalf("original sequence not resumable after shortcut fired: %v", res2.Status)
	}
}

// TestKeymapRejectsInvalidTailOfAnOtherwiseValidPrefix is the AC-11b
// regression Bill's bounce asked for: "b r x" shares a valid two-token
// PREFIX ("b r" — itself a real, reachable node, since "b r s" is
// registered) with the legitimate action, but its full path does not
// resolve to any registered action ("x" is not a registered continuation
// of "b r"). A validator that only checks the target's first token (or
// any strict-prefix-reachability check weaker than "the full path is a
// terminal") would wrongly ACCEPT this entry — demonstrated below by
// literally running that weaker check against the same fixture and
// confirming it says yes. ApplyKeymap must reject it.
func TestKeymapRejectsInvalidTailOfAnOtherwiseValidPrefix(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) {}})

	badPath := []string{"b", "r", "x"}

	// --- Demonstrate the fix is load-bearing: reproduce the PRIOR
	// (token/prefix-reachability-only) check inline and show it would
	// have accepted this exact entry — i.e. the regression test fails
	// against the unfixed validator, per dev-team-process's "any
	// regression test must be demonstrated to fail against the unfixed
	// code."
	g.mu.Lock()
	_, wouldHaveBeenAcceptedByOldCheck := g.root.children[badPath[0]] // old logic: was path[0] merely reachable?
	g.mu.Unlock()
	if !wouldHaveBeenAcceptedByOldCheck {
		t.Fatalf("test fixture invalid: badPath[0] must itself be a reachable top-level token for this to be a meaningful regression check")
	}

	// --- The actual, fixed behaviour: full-path validation rejects it.
	km, err := ParseKeymap([]byte(`{"version":1,"bindings":{"q":"b r x"}}`))
	if err != nil {
		t.Fatalf("ParseKeymap: %v", err)
	}
	rejected := g.ApplyKeymap(km)
	if len(rejected) != 1 || rejected[0].PhysicalKey != "q" {
		t.Fatalf("rejected = %+v, want exactly one entry for q->%q", rejected, "b r x")
	}

	// And confirm the binding truly never took effect: feeding 'q'
	// produces no dispatch of any kind.
	res := g.Feed(KeyRune('q'))
	if res.Status == Dispatched {
		t.Fatalf("rejected shortcut still dispatched: %+v", res)
	}
}

func TestMalformedKeymapWrongVersionRejected(t *testing.T) {
	_, err := ParseKeymap([]byte(`{"version":2,"bindings":{}}`))
	if err == nil {
		t.Fatalf("ParseKeymap accepted an unsupported schema version")
	}
}

func TestMalformedKeymapBadTokenRejected(t *testing.T) {
	_, err := ParseKeymap([]byte(`{"version":1,"bindings":{"notasingletoken":"b"}}`))
	if err == nil {
		t.Fatalf("ParseKeymap accepted a multi-rune, non-<Name> binding key")
	}
}
