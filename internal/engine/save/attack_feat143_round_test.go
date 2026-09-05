package save

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAttackFEAT143_EmptyExpectedModeIsAnEscapeHatch is the round's
// central save-side finding. WithExpectedGameMode does not validate its
// argument, so WithExpectedGameMode("") sets checkGameMode=true with an
// empty expected value — which then MATCHES a pre-FEAT-143, mode-less
// bundle. The caller believes it opted into the fail-closed check and it
// silently passes. This is reachable in production: the documented
// composition-root call is
// save.WithExpectedGameMode(gi.GameModeWire()), and GameModeWire()
// returns "" whenever the SEC-020 copy guard trips (its error is
// swallowed).
func TestAttackFEAT143_EmptyExpectedModeIsAnEscapeHatch(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "attack-corr")

	// A pre-FEAT-143 bundle: Context.GameMode never set.
	ctx := fixtureContext(1, 0)
	if err := mgr.SaveManual(ctx, "legacy"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	meta, err := ReadMeta(manualDir(root, "legacy"))
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.GameMode != "" {
		t.Fatalf("precondition: want an absent GameMode, got %q", meta.GameMode)
	}

	_, _, err = mgr.Load(manualDir(root, "legacy"), WithExpectedGameMode(""))
	if err == nil {
		t.Errorf("FEAT143 finding (P2, fail-open): Load of a MODE-LESS bundle with WithExpectedGameMode(\"\") SUCCEEDED. The option accepts an empty expected mode, so a caller that opted into AC-5's fail-closed check gets no check at all against exactly the legacy bundles AC-5 names. Reachable via the documented compose call save.WithExpectedGameMode(gi.GameModeWire()) when the copy guard makes GameModeWire() return \"\". Fix: reject a non-parsing/empty mode at option-construction or load time")
	} else {
		t.Logf("WithExpectedGameMode(\"\") correctly refused: %v", err)
	}
}

// TestAttackFEAT143_UnknownModeStringAccepted probes whether the load
// check validates the bundle's mode string at all, or only compares it.
// A hand-edited bundle carrying garbage loads clean provided the session
// asks for the same garbage.
func TestAttackFEAT143_UnknownModeStringAccepted(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "attack-corr")

	ctx := fixtureContext(1, 0)
	ctx.GameMode = "godmode"
	if err := mgr.SaveManual(ctx, "garbage"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	if _, _, err := mgr.Load(manualDir(root, "garbage"), WithExpectedGameMode("godmode")); err != nil {
		t.Logf("unknown mode string refused at load: %v", err)
		return
	}
	t.Logf("FEAT143 attack (P3, informational): the save package treats GameMode as an opaque string — it neither validates the written value nor the expected one against the two known modes, so a bundle written with a bogus mode round-trips cleanly. Acceptable given save must not import gameinit, but the composition root is then the ONLY place the enum is enforced")
}

// TestAttackFEAT143_MismatchRefusalLeavesParticipantsUntouched is the
// BUG-689 precedent check: a refused load must leave every participant
// byte-identical. Digest before and after across all three refusal
// shapes (mismatch, absent mode, reverse mismatch).
func TestAttackFEAT143_MismatchRefusalLeavesParticipantsUntouched(t *testing.T) {
	root := t.TempDir()
	writer := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 7, Name: "saved", Score: 99})}, "attack-corr")
	ctx := fixtureContext(5, 1)
	ctx.GameMode = "real"
	if err := writer.SaveManual(ctx, "src"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	digest := func(p *widgetParticipant) string {
		b, err := json.Marshal(p.State())
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(b)
	}

	for _, expected := range []string{"unlimited", "godmode", "REAL", "real "} {
		live := newWidgetParticipant(widget{ID: 1, Name: "live", Score: 1}, widget{ID: 2, Name: "live2", Score: 2})
		mgr := NewManager(root, []Participant{live}, "attack-corr")
		before := digest(live)
		_, _, err := mgr.Load(manualDir(root, "src"), WithExpectedGameMode(expected))
		if err == nil {
			t.Fatalf("Load with expected mode %q succeeded against a %q bundle", expected, "real")
		}
		if after := digest(live); after != before {
			t.Fatalf("expected=%q: refused load MUTATED the participant: before=%s after=%s", expected, before, after)
		}
	}
}

// TestAttackFEAT143_RefusalOrderingIsDeterministicAndSeedFirst pins the
// documented ordering: the world-seed check runs BEFORE the game-mode
// check, so a bundle that is wrong on both reports the seed mismatch.
// The ordering must be stable across repeated runs.
func TestAttackFEAT143_RefusalOrderingIsDeterministicAndSeedFirst(t *testing.T) {
	root := t.TempDir()
	live := newWidgetParticipant(widget{ID: 1, Name: "live", Score: 1})
	mgr := NewManager(root, []Participant{live}, "attack-corr")
	ctx := fixtureContext(11, 3)
	ctx.GameMode = "real"
	if err := mgr.SaveManual(ctx, "both-wrong"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	var first string
	for i := 0; i < 5; i++ {
		_, _, err := mgr.Load(manualDir(root, "both-wrong"),
			WithExpectedWorldSeed(999999),
			WithExpectedGameMode("unlimited"))
		if err == nil {
			t.Fatalf("run %d: Load succeeded with BOTH seed and mode wrong", i)
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("run %d: refusal is nondeterministic: %q vs %q", i, err.Error(), first)
		}
	}
	t.Logf("FEAT143 attack: seed-and-mode-both-wrong refusal is stable across 5 runs and reports: %s", first)
	if !containsStr(first, ErrSaveSeedMismatch) {
		t.Errorf("expected the SEED mismatch (MET-E819) to win, per load.go's check ordering; got %q", first)
	}
	if containsStr(first, ErrGameModeMismatch) {
		t.Errorf("both errors reported in one refusal, which the single-error return shape should make impossible: %q", first)
	}
}

func containsStr(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestAttackFEAT143_HandEditedMetaCannotSmuggleAMode rewrites the meta
// sidecar on disk (the realistic tamper) and asserts the load check reads
// the ON-DISK value, not a cached one.
func TestAttackFEAT143_HandEditedMetaCannotSmuggleAMode(t *testing.T) {
	root := t.TempDir()
	live := newWidgetParticipant(widget{ID: 1, Name: "live", Score: 1})
	mgr := NewManager(root, []Participant{live}, "attack-corr")
	ctx := fixtureContext(1, 0)
	ctx.GameMode = "real"
	if err := mgr.SaveManual(ctx, "tamper"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	dir := manualDir(root, "tamper")
	// Find the meta sidecar.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var metaPath string
	for _, e := range entries {
		if !e.IsDir() && containsStr(e.Name(), "meta") {
			metaPath = filepath.Join(dir, e.Name())
		}
	}
	if metaPath == "" {
		t.Skipf("no meta sidecar found in %v; skipping tamper probe", entries)
	}
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal meta: %v", err)
	}
	m["gameMode"] = "unlimited"
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(metaPath, out, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A real-mode session must now refuse it, and an unlimited-mode
	// session must accept it — proving the check reads the on-disk value.
	if _, _, err := mgr.Load(dir, WithExpectedGameMode("real")); err == nil {
		t.Fatalf("after tampering the sidecar to unlimited, a real-mode Load still succeeded — the check is not reading the on-disk value")
	}
	if _, meta, err := mgr.Load(dir, WithExpectedGameMode("unlimited")); err != nil {
		t.Logf("tampered bundle refused even for the matching mode (checksum coverage of the sidecar): %v", err)
	} else if meta.GameMode != "unlimited" {
		t.Fatalf("meta.GameMode = %q after tamper, want unlimited", meta.GameMode)
	} else {
		t.Logf("FEAT143 attack (P3, informational): the meta sidecar is NOT covered by the bundle checksum — a hand-edited gameMode is accepted verbatim, so AC-5's guarantee is against accidental re-moding, not against a tampered save")
	}
}
