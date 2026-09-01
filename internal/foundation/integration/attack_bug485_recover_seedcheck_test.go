package integration

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/checkpoint"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-485: Recover constructed its checkpoint.Manager (checkpoint.
// NewManager) with no notion of "this recovery's own expected world
// seed" at all, so a checkpoint bundle from a differently-seeded
// composition was rebuilt straight into the caller's live participants
// with zero check — exactly the compose.Composition.Load class of bug
// BUG-479 closed one layer up. The fix threads RecoverPlan.
// ExpectedWorldSeed into checkpoint.NewManager (recovery.go), so
// Recover's own checkpoint load now refuses a mismatch with
// ErrRecoveryCheckpointLoadFailed wrapping save.ErrSaveSeedMismatch,
// exactly like any other checkpoint-load failure this function already
// halts on (see this file's header comment on Recover's two named
// failure phases).

// TestRecover_SeedMismatch_Refused is the headline case: the on-disk
// checkpoint was created under seed 99; RecoverPlan.ExpectedWorldSeed
// names 42. Recover must refuse before ever calling apply, and the
// caller's live participant must be left untouched.
func TestRecover_SeedMismatch_Refused(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-bug485-recover-mismatch"

	// The on-disk checkpoint is written by a Manager whose OWN seed is
	// 99 — standing in for a checkpoint directory that arrived from a
	// differently-seeded composition (e.g. a copied save root).
	foreign := newAccumulatorParticipant()
	foreignMgr := checkpoint.NewManager(checkpointRoot, []save.Participant{foreign}, corrID, 99)
	foreignCtx := save.Context{WorldSeed: 99, CreatedAtTick: 5, GameMonth: 0, AppVersion: "test-build"}
	if _, err := foreignMgr.CreateCheckpoint(foreignCtx, "cp-foreign-seed", ""); err != nil {
		t.Fatalf("fixture CreateCheckpoint: %v", err)
	}

	sentinel := newAccumulatorParticipant()
	sentinel.Add(12345) // stand-in for "this recovery's own live state"

	plan := RecoverPlan{
		CheckpointRoot:    checkpointRoot,
		WALRoot:           walRoot,
		Participants:      []save.Participant{sentinel},
		CorrelationID:     corrID,
		ExpectedWorldSeed: 42,
	}
	_, err := Recover(plan, applyBuyToAccumulator(sentinel))
	if err == nil {
		t.Fatal("Recover against a seed-99 checkpoint with ExpectedWorldSeed 42 succeeded, want ErrRecoveryCheckpointLoadFailed/ErrSaveSeedMismatch")
	}
	if !errors.Is(err, &errs.E{Code: ErrRecoveryCheckpointLoadFailed}) {
		t.Fatalf("Recover error = %v, want code %s", err, ErrRecoveryCheckpointLoadFailed)
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("Recover error = %v, want it to wrap code %s", err, save.ErrSaveSeedMismatch)
	}
	if got := sentinel.Sum(); got != 12345 {
		t.Fatalf("live participant mutated by a refused Recover: sum = %d, want untouched 12345", got)
	}
}

// TestRecover_SeedMatch_Succeeds proves the check is a real comparison:
// a RecoverPlan.ExpectedWorldSeed that genuinely matches the on-disk
// checkpoint recovers normally.
func TestRecover_SeedMatch_Succeeds(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-bug485-recover-match"

	seeded := newAccumulatorParticipant()
	mgr := checkpoint.NewManager(checkpointRoot, []save.Participant{seeded}, corrID, 42)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 5, GameMonth: 0, AppVersion: "test-build"}
	seeded.Add(7)
	if _, err := mgr.CreateCheckpoint(ctx, "cp-matching-seed", ""); err != nil {
		t.Fatalf("fixture CreateCheckpoint: %v", err)
	}

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{
		CheckpointRoot:    checkpointRoot,
		WALRoot:           walRoot,
		Participants:      []save.Participant{recovered},
		CorrelationID:     corrID,
		ExpectedWorldSeed: 42,
	}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover with matching ExpectedWorldSeed(42) failed: %v", err)
	}
	if !result.HadCheckpoint {
		t.Fatal("result.HadCheckpoint = false, want true")
	}
	if got := recovered.Sum(); got != 7 {
		t.Fatalf("recovered sum = %d, want 7", got)
	}
}
