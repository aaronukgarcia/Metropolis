package save

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-485: LoadLatest's own internal per-candidate Load call was
// zero-option (see this file's sibling load_seedcheck_test.go for the
// BUG-479 fix at Manager.Load's own layer), so a differently-seeded
// autosave — restored into LIVE participants exactly like Load itself —
// was accepted as "the latest valid save" with no check at all. The fix
// (load.go's LoadLatest) forwards an opts ...LoadOption parameter
// straight through to the internal per-candidate m.Load call, so a
// caller that now passes WithExpectedWorldSeed gets the same refusal
// Load itself has had since BUG-479. This file proves that both ways:
// the option refuses a mismatch, and — the prove-can-fail half — the
// pre-fix zero-option call shape still succeeds against the exact same
// mismatched autosave, so the refusal is attributable to the seed check
// specifically, not some unrelated validation.

// buildAutosaveUnderSeed writes a single autosave bundle (sequence 1)
// under root, saved with the given world seed, so LoadLatest has exactly
// one candidate to consider.
func buildAutosaveUnderSeed(t *testing.T, root string, seed int64) {
	t.Helper()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "alpha", Score: 3.5})
	mgr := NewManager(root, []Participant{widgets}, "bug485-autosave-save")
	ctx := Context{WorldSeed: seed, CreatedAtTick: 7, GameMonth: 1, AppVersion: "test-build"}
	if err := mgr.Autosave(ctx); err != nil {
		t.Fatalf("Autosave: %v", err)
	}
}

// TestLoadLatest_SeedMismatch_Refused is BUG-485's headline case for
// save.LoadLatest: the sole candidate autosave was saved under seed 42;
// a caller expecting seed 43 must get ErrSaveSeedMismatch — treated
// exactly like a corrupted candidate (skipped, recorded in SkipInfo) —
// and, because there is only one candidate, ErrNoValidSaveFound overall,
// never a silent restore into the caller's live participants.
func TestLoadLatest_SeedMismatch_Refused(t *testing.T) {
	root := t.TempDir()
	buildAutosaveUnderSeed(t, root, 42)

	sentinel := widget{ID: 999, Name: "sentinel-untouched", Score: -1}
	loadWidgets := newWidgetParticipant(sentinel)
	loadMgr := NewManager(root, []Participant{loadWidgets}, "bug485-loadlatest-mismatch")

	_, _, skipped, err := loadMgr.LoadLatest(WithExpectedWorldSeed(43))
	if err == nil {
		t.Fatal("LoadLatest(WithExpectedWorldSeed(43)) against a sole seed-42 autosave succeeded, want ErrNoValidSaveFound (the one candidate refused on seed mismatch)")
	}
	if !errors.Is(err, &errs.E{Code: ErrNoValidSaveFound}) {
		t.Fatalf("LoadLatest error = %v, want code %s", err, ErrNoValidSaveFound)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %d entries, want exactly 1 (the mismatched candidate)", len(skipped))
	}
	if !errors.Is(skipped[0].Reason, &errs.E{Code: ErrSaveSeedMismatch}) {
		t.Fatalf("skipped[0].Reason = %v, want code %s", skipped[0].Reason, ErrSaveSeedMismatch)
	}
	if got := loadWidgets.State(); len(got) != 1 || got[0] != sentinel {
		t.Fatalf("participant state changed on a refused LoadLatest: got %+v, want untouched sentinel %+v", got, []widget{sentinel})
	}
}

// TestLoadLatest_SeedMismatch_ProveCanFail is the prove-can-fail
// companion: calling LoadLatest with NO LoadOption at all — the
// pre-BUG-485 shape, and still LoadLatest's default today — against the
// exact same mismatched autosave succeeds and silently restores it. This
// is what proves the refusal above is the seed check doing its job, not
// some other difference between the two calls.
func TestLoadLatest_SeedMismatch_ProveCanFail(t *testing.T) {
	root := t.TempDir()
	buildAutosaveUnderSeed(t, root, 42)

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets}, "bug485-loadlatest-provecanfail")

	if _, _, _, err := loadMgr.LoadLatest(); err != nil {
		t.Fatalf("LoadLatest with no seed option (pre-BUG-485 shape) failed: %v — the mismatch test's refusal cannot be attributed to the seed check specifically unless this succeeds", err)
	}
	if got := loadWidgets.State(); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("participant state did not load: got %+v", got)
	}
}

// TestLoadLatest_SeedMatch_Succeeds proves WithExpectedWorldSeed on
// LoadLatest is a real comparison, not an unconditional refusal: a
// matching expected seed loads normally.
func TestLoadLatest_SeedMatch_Succeeds(t *testing.T) {
	root := t.TempDir()
	buildAutosaveUnderSeed(t, root, 42)

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets}, "bug485-loadlatest-match")

	if _, _, skipped, err := loadMgr.LoadLatest(WithExpectedWorldSeed(42)); err != nil {
		t.Fatalf("LoadLatest(WithExpectedWorldSeed(42)) against a matching seed-42 autosave failed: %v", err)
	} else if len(skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", skipped)
	}
	if got := loadWidgets.State(); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("participant state did not load: got %+v", got)
	}
}
