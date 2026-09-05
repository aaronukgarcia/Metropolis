package compose

import (
	"context"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079852 increment 4 remainder — this file covers the pieces the
// existing feat_1972079852_inc4_journaler_test.go (exactly-once + order,
// rejected exclusion, two-boot determinism, Deps override seam) did not:
// JournalStatus's two facets (entry count, persist-halt passthrough),
// determinism across pool sizes, and a real replay-equivalence proof
// through the composed engine + harness/replay's own EnginePlayer — i.e.
// "replaying the journal from the same genesis reproduces the engine's
// state byte-identically", proved via the real composition rather than a
// bare core.Engine.

// journalTestSeq is the fixed accepted-command sequence every test in
// this file drives: a speed change, an advance, a pause/resume, and
// another speed change — enough to move Clock().Tick()/Speed()/Paused()
// away from their zero-value defaults so a replay that silently no-opped
// would be caught.
func journalTestSeq(t *testing.T) []protocol.Command {
	t.Helper()
	return []protocol.Command{
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "c1", Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "c2", Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 3}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "c3", Kind: protocol.KindPause, Payload: protocol.PausePayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "c4", Kind: protocol.KindResume, Payload: protocol.ResumePayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "c5", Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 4}},
	}
}

// TestJournalStatus_ReportsEntryCountForDefaultRecorder proves JournalStatus
// tracks the default *replay.Recorder's Len() as commands are accepted —
// the entries facet of the GR#17 status surface.
func TestJournalStatus_ReportsEntryCountForDefaultRecorder(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	if st := comp.JournalStatus(); !st.EntriesKnown || st.Entries != 0 {
		t.Fatalf("JournalStatus() before any command = %+v, want EntriesKnown=true Entries=0", st)
	}

	for _, cmd := range journalTestSeq(t) {
		if result := e.HandleCommand(cmd); !result.Accepted {
			t.Fatalf("HandleCommand(%s): rejected, error = %+v", cmd.Kind, result.Error)
		}
	}

	st := comp.JournalStatus()
	if !st.EntriesKnown {
		t.Fatal("JournalStatus().EntriesKnown = false, want true (default journaler is *replay.Recorder)")
	}
	if st.EntriesErr != nil {
		t.Fatalf("JournalStatus().EntriesErr = %v, want nil", st.EntriesErr)
	}
	want := len(journalTestSeq(t))
	if st.Entries != want {
		t.Fatalf("JournalStatus().Entries = %d, want %d", st.Entries, want)
	}
	if st.PersistHalted {
		t.Fatalf("JournalStatus().PersistHalted = true, want false (no PersistStore configured, nothing can fail)")
	}
}

// TestJournalStatus_NonRecorderJournalerReportsUnknown proves a
// Deps.CommandJournaler override that is NOT a *replay.Recorder (e.g. a
// test spy, or a future alternative implementation) is reported honestly
// as EntriesKnown=false rather than a guessed/zero count silently passed
// off as real (the GR#17 "never fake a status field" discipline).
func TestJournalStatus_NonRecorderJournalerReportsUnknown(t *testing.T) {
	spy := &spyComposeJournaler{}
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: spy})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if result := e.HandleCommand(journalTestSeq(t)[0]); !result.Accepted {
		t.Fatalf("HandleCommand: rejected, error = %+v", result.Error)
	}
	st := comp.JournalStatus()
	if st.EntriesKnown {
		t.Fatalf("JournalStatus().EntriesKnown = true for a non-Recorder journaler, want false (Entries=%d would be a guess)", st.Entries)
	}
	if len(spy.observed) != 1 {
		t.Fatalf("spy.observed = %d, want 1 (sanity: the override itself must still be receiving commands)", len(spy.observed))
	}
}

// TestJournalStatus_PersistHaltSurfacesFromEngine proves JournalStatus's
// persist-halt facet is the SAME e.PersistHalted() the engine itself
// latches (BUG-472) — not a second, independently-tracked flag (GR#3). A
// journaler whose ObserveCommand fails halts the engine; JournalStatus
// must see that halt through the read-only accessor, with the exact code
// and correlation ID PersistHalted() reports.
func TestJournalStatus_PersistHaltSurfacesFromEngine(t *testing.T) {
	failing := &failingComposeJournaler{}
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: failing})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	// This command's OWN side effects (SetSpeed) already applied before
	// journalAccepted runs and fails — see commands.go's journalAccepted
	// doc comment ("why the FAILING command itself is still rejected").
	result := e.HandleCommand(journalTestSeq(t)[0])
	if result.Accepted {
		t.Fatal("HandleCommand with a failing journaler: accepted, want rejected (BUG-472 HALT+SURFACE)")
	}

	wantCode, wantCorrID, wantOK := e.PersistHalted()
	if !wantOK {
		t.Fatal("e.PersistHalted() ok=false after a failing journaler observed a command — the engine itself never latched")
	}

	st := comp.JournalStatus()
	if !st.PersistHalted {
		t.Fatal("JournalStatus().PersistHalted = false, want true")
	}
	if st.PersistHaltCode != wantCode || st.PersistHaltCorrelationID != wantCorrID {
		t.Fatalf("JournalStatus() halt identity = (%q, %q), want the engine's own (%q, %q) — GR#3 second-source-of-truth drift",
			st.PersistHaltCode, st.PersistHaltCorrelationID, wantCode, wantCorrID)
	}
}

type failingComposeJournaler struct{}

func (f *failingComposeJournaler) ObserveCommand(cmd protocol.Command) error {
	return &journalFailureErr{}
}

type journalFailureErr struct{}

func (e *journalFailureErr) Error() string {
	return "journal_wire_test: simulated durable-append failure"
}

// TestJournalStatus_PersistWrappedRecorderReportsKnownCount is BUG-740's
// fix proof: on the production path (Deps.PersistStore set), Wire wraps the
// default *replay.Recorder in persistCommandJournaler
// (persistjournal.go) before calling e.SetCommandJournaler, so
// c.state.journaler is a *persistCommandJournaler, never a bare
// *replay.Recorder. JournalStatus must chase that ONE level of wrapping
// (persistCommandJournaler.Inner()) and still report the real Recorder's
// count — EntriesKnown=true, EntriesErr=nil, Entries equal to the number of
// accepted commands — rather than falling back to EntriesKnown=false
// exactly where durability matters (attack_journalstatus_round_test.go's
// TestAttackJournalStatus_PersistWrappedJournaler documented the pre-fix
// gap; this test pins the honest post-fix answer).
func TestJournalStatus_PersistWrappedRecorderReportsKnownCount(t *testing.T) {
	mem := persist.NewMemStore()
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{PersistStore: mem})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	seq := journalTestSeq(t)
	for _, cmd := range seq {
		if r := e.HandleCommand(cmd); !r.Accepted {
			t.Fatalf("HandleCommand(%s): rejected, error = %+v", cmd.Kind, r.Error)
		}
	}

	st := comp.JournalStatus()
	if !st.EntriesKnown {
		t.Fatal("JournalStatus().EntriesKnown = false for the persist-wrapped Recorder, want true (BUG-740)")
	}
	if st.EntriesErr != nil {
		t.Fatalf("JournalStatus().EntriesErr = %v, want nil", st.EntriesErr)
	}
	if want := len(seq); st.Entries != want {
		t.Fatalf("JournalStatus().Entries = %d, want %d", st.Entries, want)
	}
	if st.PersistHalted {
		t.Fatalf("JournalStatus().PersistHalted = true, want false (MemStore never fails here)")
	}
}

// TestJournalStatus_PersistWrappedNonRecorderStillUnknown proves the chase
// stops at exactly one level: a Deps.CommandJournaler override that is
// itself NOT a *replay.Recorder (e.g. a test spy) still reports
// EntriesKnown=false even once wrapped by persistCommandJournaler — the fix
// finds the real Recorder when one exists, it never invents one.
func TestJournalStatus_PersistWrappedNonRecorderStillUnknown(t *testing.T) {
	spy := &spyComposeJournaler{}
	mem := persist.NewMemStore()
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: spy, PersistStore: mem})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if r := e.HandleCommand(journalTestSeq(t)[0]); !r.Accepted {
		t.Fatalf("HandleCommand: rejected, error = %+v", r.Error)
	}
	st := comp.JournalStatus()
	if st.EntriesKnown {
		t.Fatalf("JournalStatus().EntriesKnown = true for a persist-wrapped non-Recorder override, want false (Entries=%d would be a guess)", st.Entries)
	}
	if len(spy.observed) != 1 {
		t.Fatalf("spy.observed = %d, want 1 (sanity: the override itself must still be receiving commands)", len(spy.observed))
	}
}

// TestJournalStatus_PersistWrappedCopiedRecorderStillSurfacesErr proves the
// copied-Recorder case (SEC-037) survives the chase too: wrapping a
// struct-copied *replay.Recorder in persistCommandJournaler must still
// surface EntriesKnown=true + EntriesErr!=nil, never a silent zero.
func TestJournalStatus_PersistWrappedCopiedRecorderStillSurfacesErr(t *testing.T) {
	orig := replay.NewRecorder()
	copied := recorderByteCopy(orig)
	mem := persist.NewMemStore()
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: copied, PersistStore: mem})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	st := comp.JournalStatus()
	if !st.EntriesKnown {
		t.Fatalf("EntriesKnown=false for a persist-wrapped *replay.Recorder receiver, want true")
	}
	if st.EntriesErr == nil {
		t.Fatalf("EntriesErr=nil for a persist-wrapped struct-copied Recorder — SEC-037 silent-zero regression (Entries=%d)", st.Entries)
	}
}

// TestJournalStatus_PersistHaltPassthroughUnchangedWithPersistStore proves
// the fix did not disturb the OTHER facet of JournalStatus: the persist-halt
// passthrough still reads verbatim off e.PersistHalted() even when
// Deps.PersistStore is configured (the failing journaler here is wrapped by
// persistCommandJournaler too, since it is Deps.CommandJournaler with
// PersistStore also set).
func TestJournalStatus_PersistHaltPassthroughUnchangedWithPersistStore(t *testing.T) {
	failing := &failingComposeJournaler{}
	mem := persist.NewMemStore()
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: failing, PersistStore: mem})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	result := e.HandleCommand(journalTestSeq(t)[0])
	if result.Accepted {
		t.Fatal("HandleCommand with a failing journaler: accepted, want rejected (BUG-472 HALT+SURFACE)")
	}

	wantCode, wantCorrID, wantOK := e.PersistHalted()
	if !wantOK {
		t.Fatal("e.PersistHalted() ok=false after a failing journaler observed a command — the engine itself never latched")
	}

	st := comp.JournalStatus()
	if !st.PersistHalted {
		t.Fatal("JournalStatus().PersistHalted = false, want true")
	}
	if st.PersistHaltCode != wantCode || st.PersistHaltCorrelationID != wantCorrID {
		t.Fatalf("JournalStatus() halt identity = (%q, %q), want the engine's own (%q, %q) — GR#3 second-source-of-truth drift",
			st.PersistHaltCode, st.PersistHaltCorrelationID, wantCode, wantCorrID)
	}
}

// TestWire_JournalerDeterministicAcrossPoolSizes generalises the existing
// TestWire_JournalerDeterministicAcrossBoots (which fixes PoolSize(1)) to
// prove the journal's captured bytes are identical regardless of the
// engine's internal citizen-pool parallelism — GR#21's "map range + early
// break" class of nondeterminism would show up here as pool-size-dependent
// journal content even though the commands sent are identical.
func TestWire_JournalerDeterministicAcrossPoolSizes(t *testing.T) {
	run := func(poolSize int) [][]byte {
		e := core.NewEngine(core.WithWorldSeed(11), core.WithPoolSize(poolSize))
		comp, err := Wire(e, nil)
		if err != nil {
			t.Fatalf("Wire(poolSize=%d): %v", poolSize, err)
		}
		rec := comp.Journaler().(*replay.Recorder)
		for _, cmd := range journalTestSeq(t) {
			if result := e.HandleCommand(cmd); !result.Accepted {
				t.Fatalf("poolSize=%d HandleCommand(%s): rejected, error = %+v", poolSize, cmd.Kind, result.Error)
			}
		}
		records, err := rec.Records()
		if err != nil {
			t.Fatalf("Records(): %v", err)
		}
		out := make([][]byte, len(records))
		for i, r := range records {
			out[i] = r.Data
		}
		return out
	}

	baseline := run(1)
	for _, pool := range []int{1, 4, 20} {
		got := run(pool)
		if len(got) != len(baseline) {
			t.Fatalf("poolSize=%d: %d records, want %d", pool, len(got), len(baseline))
		}
		for i := range got {
			if string(got[i]) != string(baseline[i]) {
				t.Fatalf("poolSize=%d record %d differs from poolSize=1 baseline:\n  got:  %x\n  want: %x", pool, i, got[i], baseline[i])
			}
		}
	}
}

// TestWire_ReplayThroughRealCompositionReproducesEngineState is the
// determinism AC's real proof: the journal recorded through ONE composed
// engine is replayed (harness/replay.EnginePlayer, driving a SECOND
// composed engine's real core.Engine.RunCommandLoop) and the resulting
// Clock (tick/speed/paused) must match exactly. This exercises the actual
// composed engine on both ends, not a bare core.NewEngine() — the
// wired-not-built distinction this whole increment exists to prove.
func TestWire_ReplayThroughRealCompositionReproducesEngineState(t *testing.T) {
	const seed = 42

	// Source: record the sequence through a real composition.
	source := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	sourceComp, err := Wire(source, nil)
	if err != nil {
		t.Fatalf("Wire(source): %v", err)
	}
	sourceRec := sourceComp.Journaler().(*replay.Recorder)
	for _, cmd := range journalTestSeq(t) {
		result := source.HandleCommand(cmd)
		if !result.Accepted {
			t.Fatalf("source HandleCommand(%s): rejected, error = %+v", cmd.Kind, result.Error)
		}
		// EnginePlayer.Replay compares against the FIXTURE's recorded
		// results too (compareResults) — capture those via ObserveResult
		// so the fixture round-trip below is faithful to what a real
		// transport/journal pairing would carry.
		if err := sourceRec.ObserveResult(result); err != nil {
			t.Fatalf("ObserveResult: %v", err)
		}
	}
	sourceClock, err := source.Clock()
	if err != nil {
		t.Fatalf("source.Clock(): %v", err)
	}

	records, err := sourceRec.Records()
	if err != nil {
		t.Fatalf("sourceRec.Records(): %v", err)
	}
	fixture := replay.Fixture{Name: "journal_wire_test-inline", Records: records}

	player, err := replay.NewEnginePlayer(fixture)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}

	// Target: a FRESH composition, same genesis (world seed), fed the
	// recorded commands via the target's own real RunCommandLoop — the
	// same code path a live transport drives, not a hand-rolled loop over
	// HandleCommand.
	target := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	if _, err := Wire(target, nil); err != nil {
		t.Fatalf("Wire(target): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	loopDone := make(chan error, 1)
	go func() {
		loopDone <- target.RunCommandLoop(ctx, player)
	}()

	cmp, err := player.Replay(ctx)
	if err != nil {
		t.Fatalf("player.Replay: %v", err)
	}
	if !cmp.Matched {
		t.Fatalf("replay produced result mismatches against the recorded fixture: %v", cmp.Diffs)
	}
	cancel()
	<-loopDone

	targetClock, err := target.Clock()
	if err != nil {
		t.Fatalf("target.Clock(): %v", err)
	}
	if targetClock.Tick() != sourceClock.Tick() {
		t.Fatalf("replayed Tick() = %d, want %d (source)", targetClock.Tick(), sourceClock.Tick())
	}
	if targetClock.Speed() != sourceClock.Speed() {
		t.Fatalf("replayed Speed() = %v, want %v (source)", targetClock.Speed(), sourceClock.Speed())
	}
	if targetClock.Paused() != sourceClock.Paused() {
		t.Fatalf("replayed Paused() = %v, want %v (source)", targetClock.Paused(), sourceClock.Paused())
	}
}
