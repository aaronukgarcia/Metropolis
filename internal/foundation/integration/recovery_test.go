package integration

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/checkpoint"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// --- fixtures --------------------------------------------------------

// accumulatorState is the tiny domain type recovery_test.go's fixture
// save.Participant checkpoints — standing in for a real engine module's
// state (mirrors internal/engine/save/fixture_test.go's widget/gadget
// fixtures, restated here rather than imported since save's fixtures are
// package-private test-only types).
type accumulatorState struct {
	Sum int64 `json:"sum"`
}

// accumulatorParticipant is a fixture save.Participant wrapping a single
// int64 accumulator — deliberately the simplest possible non-trivial
// state, so the crash-recovery test's "byte-identical" assertion is easy
// to state precisely (an exact integer, not a fuzzy comparison).
type accumulatorParticipant struct {
	mu  sync.Mutex
	sum int64
}

func newAccumulatorParticipant() *accumulatorParticipant {
	return &accumulatorParticipant{}
}

func (p *accumulatorParticipant) Kind() string { return "accumulator" }

func (p *accumulatorParticipant) Add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sum += n
}

func (p *accumulatorParticipant) Sum() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sum
}

func (p *accumulatorParticipant) Source() serialize.RecordSource {
	p.mu.Lock()
	snapshot := p.sum
	p.mu.Unlock()

	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted {
			return serialize.Record{}, false, nil
		}
		emitted = true
		data, err := json.Marshal(accumulatorState{Sum: snapshot})
		if err != nil {
			return serialize.Record{}, false, err
		}
		return serialize.Record{Kind: "accumulator", Data: data}, true, nil
	}
}

func (p *accumulatorParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		var s accumulatorState
		if err := json.Unmarshal(rec.Data, &s); err != nil {
			return err
		}
		p.mu.Lock()
		p.sum = s.Sum
		p.mu.Unlock()
		return nil
	}
}

// applyBuyToAccumulator is the fixture "engine apply" function both the
// live-processing side and Recover's replay side use — a real engine
// would dispatch on cmd.Kind and mutate real module state; this fixture
// only understands KindBuy, adding its cell's X coordinate to the
// accumulator, which is enough to make replay order and exactly-once
// application observable and byte-precise.
func applyBuyToAccumulator(participant *accumulatorParticipant) func(protocol.Command) error {
	return func(cmd protocol.Command) error {
		buy := cmd.Payload.(protocol.BuyPayload)
		participant.Add(int64(buy.Cell.X))
		return nil
	}
}

func fixtureCtx(tick int64) save.Context {
	return save.Context{WorldSeed: 7, CreatedAtTick: tick, GameMonth: tick / 12, AppVersion: "test-build"}
}

// --- tests -------------------------------------------------------------

// (a)/(d) THE CRASH-RECOVERY TEST: append+apply commands to the WAL
// exactly as a live tick driver would (Append BEFORE apply — the seam
// wal.go's header comment documents), checkpoint partway, append+apply
// MORE commands afterward (never checkpointed), "crash" (discard all live
// state), Recover from the checkpoint + WAL, and assert the rebuilt
// accumulator is byte-identical to a reference value computed as if the
// crash never happened.
func TestRecover_CrashRecovery_ByteIdenticalToPreCrashState(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-crash-recovery"

	// --- "pre-crash" run: live participant + live WAL --------------------
	live := newAccumulatorParticipant()
	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	applyLive := applyBuyToAccumulator(live)

	// Phase 1: 5 commands, each appended to the WAL BEFORE being applied
	// — exactly what a live tick driver does every tick (wal.go's "seam"
	// note).
	const beforeCheckpoint = 5
	for i := 0; i < beforeCheckpoint; i++ {
		cmd := buyCmd(corrID, i+1) // cells 1..5
		tick := int64(i + 1)
		if _, err := wal.Append(tick, cmd); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
		if err := applyLive(cmd); err != nil {
			t.Fatalf("applyLive: %v", err)
		}
	}
	sumAtCheckpoint := live.Sum() // 1+2+3+4+5 = 15

	mgr := checkpoint.NewManager(checkpointRoot, []save.Participant{live}, corrID)
	cp, err := mgr.CreateCheckpoint(fixtureCtx(int64(beforeCheckpoint)), "cp-partway", "")
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	// Phase 2: MORE commands are appended to the WAL (durably logged,
	// fsync'd) but never checkpointed before the crash — this is exactly
	// the gap Recover must close: state the checkpoint does not know
	// about, but the WAL does. Ticks continue past beforeCheckpoint so
	// Recover's tick > checkpoint-tick filter includes every one of them.
	const afterCheckpoint = 4
	var pending []protocol.Command
	for i := 0; i < afterCheckpoint; i++ {
		cmd := buyCmd(corrID, 100+i) // cells 100,101,102,103
		tick := int64(beforeCheckpoint + i + 1)
		if _, err := wal.Append(tick, cmd); err != nil {
			t.Fatalf("Append(pending %d): %v", i, err)
		}
		if err := applyLive(cmd); err != nil { // the live run DOES apply these — it just never gets to checkpoint them
			t.Fatalf("applyLive(pending): %v", err)
		}
		pending = append(pending, cmd)
	}

	// The reference "had it not crashed" value: checkpoint sum plus every
	// pending command's contribution, applied in the same order.
	wantSum := sumAtCheckpoint
	for _, cmd := range pending {
		wantSum += int64(cmd.Payload.(protocol.BuyPayload).Cell.X)
	}
	if got := live.Sum(); got != wantSum {
		t.Fatalf("test setup error: live sum = %d, want %d", got, wantSum)
	}

	// --- "crash": live state (the *accumulatorParticipant and the *WAL's
	// in-memory bookkeeping) is simply abandoned here — never referenced
	// again. Only checkpointRoot and walRoot's ON-DISK contents survive,
	// exactly as a real process crash would leave them. ---

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{
		CheckpointRoot: checkpointRoot,
		WALRoot:        walRoot,
		Participants:   []save.Participant{recovered},
		CorrelationID:  corrID,
	}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if !result.HadCheckpoint {
		t.Fatal("result.HadCheckpoint = false, want true")
	}
	if result.CheckpointID != cp.ID {
		t.Fatalf("result.CheckpointID = %q, want %q", result.CheckpointID, cp.ID)
	}
	if result.CheckpointTick != beforeCheckpoint {
		t.Fatalf("result.CheckpointTick = %d, want %d", result.CheckpointTick, beforeCheckpoint)
	}
	if result.ReplayedCount != afterCheckpoint {
		t.Fatalf("result.ReplayedCount = %d, want %d", result.ReplayedCount, afterCheckpoint)
	}

	// THE assertion: recovered state is byte-identical (here: an exact
	// integer equality, the simplest possible "byte-identical" claim for
	// this fixture's state shape) to the pre-crash reference.
	if got := recovered.Sum(); got != wantSum {
		t.Fatalf("recovered sum = %d, want %d (pre-crash reference)", got, wantSum)
	}

	// Sanity: the checkpoint ALONE (no replay) would NOT already equal
	// the pre-crash value — proves the replay actually contributed.
	if sumAtCheckpoint == wantSum {
		t.Fatal("test setup error: checkpoint-only sum already equals the pre-crash reference — the replay contribution would be untestable")
	}
}

// (b) BUG-1 REPRO: crash in the gap between "accepted for apply" (WAL
// Append succeeds) and "applied" — the OLD design lost this silently
// because the T1 queue segment backing the command was already deleted
// by Drain before apply/checkpoint ever ran. Under the WAL, the entry
// survives regardless of anything else that happened to the (unrelated)
// command copy elsewhere, and Recover replays it correctly.
func TestRecover_Bug1Repro_CrashBetweenAcceptAndApply_EntrySurvives(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-bug1-repro"

	live := newAccumulatorParticipant()
	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}

	// A checkpoint at tick 0 with nothing applied yet (base case).
	mgr := checkpoint.NewManager(checkpointRoot, []save.Participant{live}, corrID)
	if _, err := mgr.CreateCheckpoint(fixtureCtx(0), "cp-base", ""); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	// Accept a command for application: WAL.Append succeeds (durably
	// logged, fsync'd) — and then the process "crashes" in the gap
	// BEFORE the apply step ever runs. live.Add is deliberately never
	// called here — this is exactly the race the destructive review
	// found: something accepted the command but never got to apply it.
	cmd := buyCmd(corrID, 42)
	if _, err := wal.Append(1, cmd); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// --- "crash" here --- live/wal abandoned, never referenced again.

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.ReplayedCount != 1 {
		t.Fatalf("ReplayedCount = %d, want 1 — the entry accepted right before the crash must survive and replay", result.ReplayedCount)
	}
	if got := recovered.Sum(); got != 42 {
		t.Fatalf("recovered sum = %d, want 42 (bug 1's entry must not be silently lost)", got)
	}
}

// (c) BUG-2 REPRO / structural proof: the OLD design's T1 in-memory
// window recycling could leave a legitimate MID-RANGE hole on disk
// (queue.go's peekLocked worked example), which its "absent = end of
// log" replay logic misread as a boundary, missing later pending work
// and double-applying earlier work on the next boot. This test proves
// the WAL structurally cannot exhibit that: append many entries,
// checkpoint (which retains everything, since nothing has been pruned
// yet), and confirm every appended sequence number is present with NO
// gap — the WAL never deletes an individual entry (only Prune's
// whole-slot atomic rebuild ever removes anything, and only for entries
// already captured by a checkpoint), so "absent" can only ever mean the
// unwritten tail, never a hole partway through.
func TestRecover_Bug2Repro_WALIsContiguous_NoMidGapPossible(t *testing.T) {
	walRoot := t.TempDir()
	corrID := "corr-bug2-repro"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}

	const n = 20
	for i := 0; i < n; i++ {
		if _, err := wal.Append(int64(i), buyCmd(corrID, i)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	slot, err := readCurrentSlot(walRoot, corrID)
	if err != nil {
		t.Fatalf("readCurrentSlot: %v", err)
	}
	seqs, err := listWALSeqs(walSlotDir(walRoot, slot), corrID)
	if err != nil {
		t.Fatalf("listWALSeqs: %v", err)
	}
	if len(seqs) != n {
		t.Fatalf("listWALSeqs returned %d entries, want %d — a real gap would silently drop entries from this listing", len(seqs), n)
	}
	for i, seq := range seqs {
		if seq != int64(i) {
			t.Fatalf("seqs[%d] = %d, want %d — the WAL's on-disk entries must be exactly the contiguous range [0, n) with no mid-range hole (bug 2's exact failure mode)", i, seq, i)
		}
	}

	// A cold Recover (no checkpoint at all) must therefore replay EVERY
	// entry, in order, with none missed and none double-applied.
	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: t.TempDir(), WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.ReplayedCount != n {
		t.Fatalf("ReplayedCount = %d, want %d", result.ReplayedCount, n)
	}
	wantSum := int64(0)
	for i := 0; i < n; i++ {
		wantSum += int64(i)
	}
	if got := recovered.Sum(); got != wantSum {
		t.Fatalf("recovered sum = %d, want %d", got, wantSum)
	}
}

// TestRecover_NoBacklog_CheckpointAloneIsSufficient covers the "nothing
// to replay" path: a checkpoint exists, nothing was ever appended to the
// WAL after it, and Recover reports zero replayed entries without error.
func TestRecover_NoBacklog_CheckpointAloneIsSufficient(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-no-backlog"

	live := newAccumulatorParticipant()
	live.Add(42)
	mgr := checkpoint.NewManager(checkpointRoot, []save.Participant{live}, corrID)
	if _, err := mgr.CreateCheckpoint(fixtureCtx(1), "cp-only", ""); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.ReplayedCount != 0 {
		t.Fatalf("ReplayedCount = %d, want 0", result.ReplayedCount)
	}
	if recovered.Sum() != 42 {
		t.Fatalf("recovered sum = %d, want 42 (checkpoint alone)", recovered.Sum())
	}
}

// TestRecover_NoCheckpointYet_ReplaysOnTopOfZeroState covers a fresh
// install that crashed before ever taking a checkpoint: HadCheckpoint is
// false (not an error), and any backlog already in the WAL still replays
// cleanly on top of each Participant's zero-value state.
func TestRecover_NoCheckpointYet_ReplaysOnTopOfZeroState(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-no-checkpoint"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	if _, err := wal.Append(0, buyCmd(corrID, 3)); err != nil {
		t.Fatalf("Append(0): %v", err)
	}
	if _, err := wal.Append(0, buyCmd(corrID, 4)); err != nil {
		t.Fatalf("Append(1): %v", err)
	}

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.HadCheckpoint {
		t.Fatal("HadCheckpoint = true, want false (fresh root)")
	}
	if result.ReplayedCount != 2 {
		t.Fatalf("ReplayedCount = %d, want 2", result.ReplayedCount)
	}
	if recovered.Sum() != 7 { // 3+4
		t.Fatalf("recovered sum = %d, want 7", recovered.Sum())
	}
}

// (d) A torn/absent final WAL entry: replay applies every entry actually
// present and returns no error — never a partial apply, never a
// fabricated substitute. Simulated by removing the highest-sequence
// entry file after it was written (wal.go's own doc comment: a crash
// mid-write leaves NO file at the final path at all — "torn" and
// "absent" are the same observable state, which this test exercises
// directly by removing the file that torn write would never have
// produced in the first place).
func TestRecover_TornFinalWALEntry_StopsCleanlyAtLastComplete(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-torn"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	seqs := make([]int64, 0, 3)
	for _, cell := range []int{10, 20, 30} {
		seq, err := wal.Append(int64(cell), buyCmd(corrID, cell))
		if err != nil {
			t.Fatalf("Append(%d): %v", cell, err)
		}
		seqs = append(seqs, seq)
	}

	// Simulate the torn/never-promoted final write: the entry for the
	// highest sequence (cell 30) is removed, exactly as it would never
	// have existed at all had the crash landed mid-write, before
	// os.Rename ever ran.
	slot, err := readCurrentSlot(walRoot, corrID)
	if err != nil {
		t.Fatalf("readCurrentSlot: %v", err)
	}
	if err := os.Remove(walEntryPath(walSlotDir(walRoot, slot), seqs[2])); err != nil {
		t.Fatalf("simulating torn entry: %v", err)
	}

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: unexpected error on a torn final entry: %v", err)
	}
	if result.ReplayedCount != 2 {
		t.Fatalf("ReplayedCount = %d, want 2 (stop before the torn/absent entry)", result.ReplayedCount)
	}
	if recovered.Sum() != 30 { // 10+20, never 30 (the torn entry's own contribution)
		t.Fatalf("recovered sum = %d, want 30 (10+20 only)", recovered.Sum())
	}
}

// TestRecover_GenuineCorruption_IsAnError proves a WAL entry that IS
// present at its final path but fails to decode is treated as a REAL
// error, never silently skipped as if it were a clean torn-write
// boundary — the two must never be confused (wal.go/recovery.go's header
// comments).
func TestRecover_GenuineCorruption_IsAnError(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-corrupt"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	seq, err := wal.Append(1, buyCmd(corrID, 1))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	slot, err := readCurrentSlot(walRoot, corrID)
	if err != nil {
		t.Fatalf("readCurrentSlot: %v", err)
	}
	// Corrupt the promoted entry file directly (bypassing writeWALEntry's
	// atomic path entirely) — this is exactly "something outside this
	// package's own writer touched it".
	if err := os.WriteFile(walEntryPath(walSlotDir(walRoot, slot), seq), []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("corrupting entry: %v", err)
	}

	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	_, err = Recover(plan, applyBuyToAccumulator(recovered))
	if err == nil {
		t.Fatal("expected an error for a genuinely corrupted (present but undecodable) entry, got nil")
	}
	if !containsCode(err, ErrWALReadFailed) {
		t.Fatalf("expected error to carry code %s, got: %v", ErrWALReadFailed, err)
	}
}

// TestRecover_ApplyFailure_HaltsImmediately proves a failing apply halts
// replay at that command rather than continuing on top of a state that
// never actually received it (ErrRecoveryApplyFailed).
func TestRecover_ApplyFailure_HaltsImmediately(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-apply-fail"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	if _, err := wal.Append(1, buyCmd(corrID, 1)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	failingApply := func(protocol.Command) error { return os.ErrInvalid }
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, CorrelationID: corrID}
	_, err = Recover(plan, failingApply)
	if err == nil {
		t.Fatal("expected an error from a failing apply function")
	}
	if !containsCode(err, ErrRecoveryApplyFailed) {
		t.Fatalf("expected error to carry code %s, got: %v", ErrRecoveryApplyFailed, err)
	}
}

// (e) PRUNE-ON-CHECKPOINT: entries whose tick <= the checkpoint's tick
// are pruned, entries with tick > the checkpoint's tick are retained, and
// a Recover run AFTER the prune is still correct (identical to a Recover
// run against the unpruned WAL — pruning must never change the outcome,
// only the disk usage).
func TestWAL_PruneOnCheckpoint_RecoverStillCorrectAfterPrune(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-prune"

	live := newAccumulatorParticipant()
	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	applyLive := applyBuyToAccumulator(live)

	// Ticks 1..5 applied and checkpointed at tick 5.
	for i := 1; i <= 5; i++ {
		cmd := buyCmd(corrID, i)
		if _, err := wal.Append(int64(i), cmd); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
		if err := applyLive(cmd); err != nil {
			t.Fatalf("applyLive: %v", err)
		}
	}
	mgr := checkpoint.NewManager(checkpointRoot, []save.Participant{live}, corrID)
	cp, err := mgr.CreateCheckpoint(fixtureCtx(5), "cp-5", "")
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	// Ticks 6..8 applied but NOT checkpointed.
	for i := 6; i <= 8; i++ {
		cmd := buyCmd(corrID, i)
		if _, err := wal.Append(int64(i), cmd); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
		if err := applyLive(cmd); err != nil {
			t.Fatalf("applyLive: %v", err)
		}
	}

	// Prune at the checkpoint's tick (5): entries 1..5 (tick<=5) pruned,
	// entries 6..8 (tick>5) retained.
	pruneResult, err := wal.Prune(cp.CreatedAtTick)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruneResult.PrunedCount != 5 {
		t.Fatalf("PrunedCount = %d, want 5", pruneResult.PrunedCount)
	}
	if pruneResult.RetainedCount != 3 {
		t.Fatalf("RetainedCount = %d, want 3", pruneResult.RetainedCount)
	}

	slot, err := readCurrentSlot(walRoot, corrID)
	if err != nil {
		t.Fatalf("readCurrentSlot: %v", err)
	}
	seqs, err := listWALSeqs(walSlotDir(walRoot, slot), corrID)
	if err != nil {
		t.Fatalf("listWALSeqs: %v", err)
	}
	if len(seqs) != 3 {
		t.Fatalf("post-prune WAL has %d entries, want 3", len(seqs))
	}

	// A crash "now": Recover from the checkpoint + pruned WAL must still
	// produce the exact pre-crash sum.
	recovered := newAccumulatorParticipant()
	plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
	result, err := Recover(plan, applyBuyToAccumulator(recovered))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.ReplayedCount != 3 {
		t.Fatalf("ReplayedCount = %d, want 3", result.ReplayedCount)
	}
	if got, want := recovered.Sum(), live.Sum(); got != want {
		t.Fatalf("recovered sum = %d, want %d (live, pre-crash reference)", got, want)
	}

	// A repeated Prune at the same tick is a safe, cheap no-op (nothing
	// left at or below tick 5 to prune).
	second, err := wal.Prune(cp.CreatedAtTick)
	if err != nil {
		t.Fatalf("second Prune: %v", err)
	}
	if second.PrunedCount != 0 {
		t.Fatalf("second Prune PrunedCount = %d, want 0", second.PrunedCount)
	}
}

// (f) IDEMPOTENT RECOVER: running Recover TWICE against the exact same
// on-disk state (checkpoint + unpruned WAL backlog) produces byte-
// identical results both times, and never double-applies — Recover only
// ever reads, it never prunes or mutates the WAL itself.
func TestRecover_IdempotentAcrossRepeatedRuns(t *testing.T) {
	checkpointRoot := t.TempDir()
	walRoot := t.TempDir()
	corrID := "corr-idempotent"

	seed := newAccumulatorParticipant()
	mgr := checkpoint.NewManager(checkpointRoot, []save.Participant{seed}, corrID)
	if _, err := mgr.CreateCheckpoint(fixtureCtx(0), "cp-seed", ""); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	for i, cell := range []int{5, 6, 7, 8} {
		if _, err := wal.Append(int64(i+1), buyCmd(corrID, cell)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	runOnce := func() (int64, RecoverResult) {
		recovered := newAccumulatorParticipant()
		plan := RecoverPlan{CheckpointRoot: checkpointRoot, WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
		result, err := Recover(plan, applyBuyToAccumulator(recovered))
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		return recovered.Sum(), result
	}

	sum1, result1 := runOnce()
	sum2, result2 := runOnce()

	if sum1 != sum2 {
		t.Fatalf("recovered sums diverged across identical runs: %d vs %d", sum1, sum2)
	}
	if result1 != result2 {
		t.Fatalf("RecoverResult diverged across identical runs: %+v vs %+v", result1, result2)
	}
	// A THIRD run, for good measure — idempotence is not "runs twice OK",
	// it must hold for any repeated call.
	sum3, result3 := runOnce()
	if sum3 != sum1 || result3 != result1 {
		t.Fatalf("a third Recover run diverged: sum=%d result=%+v, want sum=%d result=%+v", sum3, result3, sum1, result1)
	}
}

// (g) DETERMINISM: proven at the unit level here (byte-identical replay
// across repeated runs against fixed on-disk state — see
// TestRecover_IdempotentAcrossRepeatedRuns above, which this test
// reinforces from a "no checkpoint at all" starting point) and at the
// suite level by `go test -race -count=2` (VERIFY step) re-running this
// whole file's tests twice under the race detector.
func TestRecover_DeterminismAcrossRuns_NoCheckpoint(t *testing.T) {
	walRoot := t.TempDir()
	corrID := "corr-determinism-recover"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	for i, cell := range []int{1, 2, 3, 4, 5} {
		if _, err := wal.Append(int64(i), buyCmd(corrID, cell)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	runOnce := func() (int64, RecoverResult) {
		recovered := newAccumulatorParticipant()
		plan := RecoverPlan{CheckpointRoot: t.TempDir(), WALRoot: walRoot, Participants: []save.Participant{recovered}, CorrelationID: corrID}
		result, err := Recover(plan, applyBuyToAccumulator(recovered))
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		return recovered.Sum(), result
	}

	sum1, result1 := runOnce()
	sum2, result2 := runOnce()
	if sum1 != sum2 {
		t.Fatalf("recovered sums diverged across identical runs: %d vs %d", sum1, sum2)
	}
	if result1 != result2 {
		t.Fatalf("RecoverResult diverged across identical runs: %+v vs %+v", result1, result2)
	}
}

// TestReadCurrentSlot_MissingRoot_IsNotAnError proves a WALRoot that was
// never created (no command ever appended) is a valid, non-error "empty
// WAL" outcome, not a filesystem error, and defaults to walSlotA.
func TestReadCurrentSlot_MissingRoot_IsNotAnError(t *testing.T) {
	missing := t.TempDir() + "/never-created"
	slot, err := readCurrentSlot(missing, "corr-missing-root")
	if err != nil {
		t.Fatalf("readCurrentSlot on a missing root: unexpected error: %v", err)
	}
	if slot != walSlotA {
		t.Fatalf("slot = %q, want %q (default for a fresh root)", slot, walSlotA)
	}
}

// TestWAL_Append_AssignsGapFreeMonotonicSequence proves Append's own
// sequence assignment is strictly increasing with no gaps, in call
// order — the structural property this file's header comment and
// TestRecover_Bug2Repro_WALIsContiguous_NoMidGapPossible both rely on.
func TestWAL_Append_AssignsGapFreeMonotonicSequence(t *testing.T) {
	walRoot := t.TempDir()
	corrID := "corr-seq"

	wal, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	for i := 0; i < 10; i++ {
		seq, err := wal.Append(int64(i), buyCmd(corrID, i))
		if err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
		if seq != int64(i) {
			t.Fatalf("Append(%d) returned seq %d, want %d", i, seq, i)
		}
	}
}

// TestWAL_NewWAL_ResumesSequenceAcrossReopen proves a fresh *WAL
// constructed against a root that already has entries continues
// assigning sequence numbers from where the prior instance left off —
// required for gap-free continuity across a process restart (wal.go's
// NewWAL doc comment).
func TestWAL_NewWAL_ResumesSequenceAcrossReopen(t *testing.T) {
	walRoot := t.TempDir()
	corrID := "corr-resume"

	first, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL (first): %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := first.Append(int64(i), buyCmd(corrID, i)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	second, err := NewWAL(walRoot, corrID)
	if err != nil {
		t.Fatalf("NewWAL (second): %v", err)
	}
	seq, err := second.Append(99, buyCmd(corrID, 99))
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if seq != 3 {
		t.Fatalf("seq after reopen = %d, want 3 (continuing from the first instance's 3 appends)", seq)
	}
}

// walByteCopy performs the SEC-020-class attack — a plain WAL struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// save.Manager's managerByteCopy and resilience_test.go's
// connectionByteCopy: a literal `w2 := *w` is legal, unsafe-free Go from
// outside this package too, but go vet's copylocks check statically
// flags it, which this project's VERIFY step requires every package to
// pass. The byte-level copy produces identical runtime semantics (mu's
// bytes copied as-is, self's pointer bytes copied unchanged) without a
// statically-flaggable copy expression.
func walByteCopy(w *WAL) *WAL {
	cp := new(WAL)
	*(*[unsafe.Sizeof(WAL{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(WAL{})]byte)(unsafe.Pointer(w))
	return cp
}

// TestWAL_CopyGuard proves WAL's SEC-020-class copy guard rejects a
// struct copy the same way tierQueue/QueuedTransport/Connection's do.
func TestWAL_CopyGuard(t *testing.T) {
	walRoot := t.TempDir()
	w, err := NewWAL(walRoot, "corr-copy")
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	cp := walByteCopy(w)

	if _, err := cp.Append(1, buyCmd("corr-copy", 1)); err == nil {
		t.Fatal("expected Append on a copied WAL to fail")
	} else if !containsCode(err, ErrWALCopied) {
		t.Fatalf("expected copy-guard error to carry code %s, got: %v", ErrWALCopied, err)
	}
	if _, err := cp.Prune(0); err == nil {
		t.Fatal("expected Prune on a copied WAL to fail")
	} else if !containsCode(err, ErrWALCopied) {
		t.Fatalf("expected copy-guard error to carry code %s, got: %v", ErrWALCopied, err)
	}
}
