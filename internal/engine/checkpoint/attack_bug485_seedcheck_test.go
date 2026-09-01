package checkpoint

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-485: checkpoint.Manager.Load/Revert/recoverAfterLoad restored a
// checkpoint bundle straight into LIVE participants with NO check that
// the bundle's WorldSeed matched this Manager's own composition seed —
// unlike compose.Composition.Load/LoadAt, which BUG-479 already guards.
// This file proves the fix (checkpoint.go's expectedWorldSeed field,
// given at NewManager construction and threaded into every saveMgr.Load
// call this package makes) for all three named restore paths.
//
// mismatchedSeedCheckpoint builds a checkpoint bundle named "seed-drift"
// directly under root, saved with a WorldSeed that deliberately does NOT
// match matchingSeed — standing in for "a bundle from a differently-
// seeded composition", the exact shape BUG-479/485 close.
func mismatchedSeedCheckpoint(t *testing.T, root string, matchingSeed int64) {
	t.Helper()
	// A throwaway Manager whose OWN expectedWorldSeed matches the bundle
	// it writes (matchingSeed+1) -- CreateCheckpoint's ctx.WorldSeed is
	// what lands in the bundle header, independent of the Manager's own
	// expectedWorldSeed, which only gates Load/Revert. Using a Manager
	// whose expectedWorldSeed also equals the bundle's seed just avoids
	// entangling this fixture with the very check under test.
	writer := NewManager(root, []save.Participant{newMemParticipant("widget")}, "bug485-fixture-writer", matchingSeed+1)
	ctx := save.Context{WorldSeed: matchingSeed + 1, CreatedAtTick: 10, GameMonth: 1, AppVersion: "test-build"}
	if _, err := writer.CreateCheckpoint(ctx, "seed-drift", ""); err != nil {
		t.Fatalf("fixture CreateCheckpoint: %v", err)
	}
}

// TestManagerLoad_SeedMismatch_Refused is BUG-485's headline case for
// checkpoint.Manager.Load: a Manager whose own expectedWorldSeed is 42
// refuses to Load a checkpoint bundle saved under a different seed (43),
// with ErrSaveSeedMismatch, and the live participant is left untouched.
func TestManagerLoad_SeedMismatch_Refused(t *testing.T) {
	root := t.TempDir()
	mismatchedSeedCheckpoint(t, root, 42) // bundle saved under seed 43

	sentinel := entry{ID: 999, Name: "sentinel-untouched", Score: -1}
	live := newMemParticipant("widget", sentinel)
	m := NewManager(root, []save.Participant{live}, "bug485-load-mismatch", 42)

	_, _, err := m.Load("seed-drift")
	if err == nil {
		t.Fatal("Manager.Load against a seed-43 bundle from a seed-42 Manager succeeded, want ErrSaveSeedMismatch")
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("Load error = %v, want code %s", err, save.ErrSaveSeedMismatch)
	}
	if got := live.state(); len(got) != 1 || got[0] != sentinel {
		t.Fatalf("participant state changed on a refused Load: got %+v, want untouched sentinel %+v", got, []entry{sentinel})
	}
}

// TestManagerLoad_SeedMatch_Succeeds proves the check is a real
// comparison: a Manager whose expectedWorldSeed genuinely matches the
// bundle loads normally.
func TestManagerLoad_SeedMatch_Succeeds(t *testing.T) {
	root := t.TempDir()
	mismatchedSeedCheckpoint(t, root, 42) // bundle saved under seed 43

	live := newMemParticipant("widget")
	m := NewManager(root, []save.Participant{live}, "bug485-load-match", 43)

	if _, _, err := m.Load("seed-drift"); err != nil {
		t.Fatalf("Manager.Load against a matching seed-43 bundle failed: %v", err)
	}
}

// TestManagerRevert_SeedMismatch_Refused is BUG-485's headline case for
// Revert: reverting to a checkpoint saved under a foreign seed refuses
// with ErrSaveSeedMismatch and leaves live state untouched (no fork is
// created, no head pointer is moved).
func TestManagerRevert_SeedMismatch_Refused(t *testing.T) {
	root := t.TempDir()

	// The Manager under test creates its OWN checkpoint under its real
	// seed (42) first, establishing a real prior head...
	live := newMemParticipant("widget", entry{ID: 1, Name: "alive", Score: 1})
	m := NewManager(root, []save.Participant{live}, "bug485-revert-mismatch", 42)
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}

	// ...then a FOREIGN checkpoint "B" is written directly to the same
	// root under a different seed (99), standing in for a checkpoint
	// bundle that arrived from a differently-seeded composition (e.g. a
	// copied save directory).
	foreignWriter := NewManager(root, []save.Participant{newMemParticipant("widget")}, "bug485-revert-foreign-writer", 99)
	foreignCtx := save.Context{WorldSeed: 99, CreatedAtTick: 20, GameMonth: 1, AppVersion: "test-build"}
	if _, err := foreignWriter.CreateCheckpoint(foreignCtx, "B", "A"); err != nil {
		t.Fatalf("fixture foreign CreateCheckpoint B: %v", err)
	}

	preRevertHead, err := m.CurrentID()
	if err != nil {
		t.Fatalf("CurrentID before Revert: %v", err)
	}

	if _, err := m.Revert(fixtureContext(30, 1), "B"); err == nil {
		t.Fatal("Revert to a seed-99 checkpoint from a seed-42 Manager succeeded, want ErrSaveSeedMismatch")
	} else if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("Revert error = %v, want code %s", err, save.ErrSaveSeedMismatch)
	}

	if got := live.state(); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("live participant mutated by a refused Revert: got %+v", got)
	}
	postRevertHead, err := m.CurrentID()
	if err != nil {
		t.Fatalf("CurrentID after refused Revert: %v", err)
	}
	if postRevertHead != preRevertHead {
		t.Fatalf("active head moved on a refused Revert: got %q, want unchanged %q", postRevertHead, preRevertHead)
	}
}

// TestManagerRecoverAfterLoad_SeedMismatch_Surfaced closes the third of
// BUG-485's three named restore paths, which the cases above do not
// reach: recoverAfterLoad, the SEC-176 undo that reloads the prior
// active head after a mid-Revert failure. Stripping the
// WithExpectedWorldSeed from this call site alone is invisible to every
// other test in this package (verified by mutation during the BUG-485
// destructive round), so it gets its own regression here.
//
// priorActiveID names a bundle carrying a foreign seed — which, for a
// bundle this Manager itself wrote, indicates on-disk corruption or
// tampering. The contract is that the undo refuses it and surfaces the
// condition via ErrRevertRestoreFailed (never a silent restore of a
// foreign trajectory into live participants).
func TestManagerRecoverAfterLoad_SeedMismatch_Surfaced(t *testing.T) {
	root := t.TempDir()
	live := newMemParticipant("widget", entry{ID: 1, Name: "alive", Score: 1})
	m := NewManager(root, []save.Participant{live}, "bug485-recover-after-load", 42)

	// A prior head bundle carrying seed 99 rather than the Manager's 42.
	foreignCtx := save.Context{WorldSeed: 99, CreatedAtTick: 10, GameMonth: 1, AppVersion: "test-build"}
	if _, err := m.CreateCheckpoint(foreignCtx, "A", ""); err != nil {
		t.Fatalf("fixture CreateCheckpoint A: %v", err)
	}

	cause := errors.New("original-revert-cause")
	err := m.recoverAfterLoad("A", cause)
	if err == nil {
		t.Fatal("recoverAfterLoad reloaded a seed-99 prior head into a seed-42 Manager without error, want ErrRevertRestoreFailed")
	}
	if !errors.Is(err, &errs.E{Code: ErrRevertRestoreFailed}) {
		t.Fatalf("recoverAfterLoad error = %v, want code %s", err, ErrRevertRestoreFailed)
	}
	// The seed mismatch must be the surfaced reason, not swallowed.
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("recoverAfterLoad error %v does not carry %s", err, save.ErrSaveSeedMismatch)
	}
}

// TestManagerLoad_UsesConstructedSeedNotFixtureDefault proves the check
// reads this Manager's REAL constructed seed rather than any constant
// that merely happens to match the package fixtures (which all use 42).
// A hardcoded-42 implementation passes every other test in this file;
// it fails this one.
func TestManagerLoad_UsesConstructedSeedNotFixtureDefault(t *testing.T) {
	root := t.TempDir()
	const oddSeed = 12345

	writer := NewManager(root, []save.Participant{newMemParticipant("widget")}, "bug485-odd-writer", oddSeed)
	ctx := save.Context{WorldSeed: oddSeed, CreatedAtTick: 10, GameMonth: 1, AppVersion: "test-build"}
	if _, err := writer.CreateCheckpoint(ctx, "odd", ""); err != nil {
		t.Fatalf("fixture CreateCheckpoint: %v", err)
	}

	// The fixture-default seed (42) must REFUSE the odd-seeded bundle.
	m42 := NewManager(root, []save.Participant{newMemParticipant("widget")}, "bug485-odd-42", 42)
	if _, _, err := m42.Load("odd"); !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("seed-42 Manager loading a seed-%d bundle: got %v, want %s", oddSeed, err, save.ErrSaveSeedMismatch)
	}
	// The genuinely-matching seed must ACCEPT it.
	mOdd := NewManager(root, []save.Participant{newMemParticipant("widget")}, "bug485-odd-real", oddSeed)
	if _, _, err := mOdd.Load("odd"); err != nil {
		t.Fatalf("seed-%d Manager loading its own bundle failed: %v", oddSeed, err)
	}
}
