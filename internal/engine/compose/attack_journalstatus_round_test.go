package compose

import (
	"context"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Independent destructive round (opus-round-journalstatus-inc4b) against
// compose_journal.go's JournalStatus + journal_wire_test.go's proofs.

// A1: JournalStatus hammered from 8 goroutines while commands arrive and
// the engine ticks. -race must be clean and Entries must be monotone
// non-decreasing per observer (no torn read of the Recorder's slice/len).
func TestAttackJournalStatus_ConcurrentReadsMonotone(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(7), core.WithPoolSize(4))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: accepted commands (each one appends to the Recorder) plus
	// real tick advances.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			speed := []int{1, 2, 4}[i%3]
			cmd := protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion,
				CorrelationID:   protocol.NewCorrelationID(),
				Kind:            protocol.KindSetSpeed,
				Payload:         protocol.SetSpeedPayload{Speed: speed},
			}
			if r := e.HandleCommand(cmd); !r.Accepted {
				t.Errorf("SetSpeed rejected: %+v", r.Error)
				break
			}
			adv := protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion,
				CorrelationID:   protocol.NewCorrelationID(),
				Kind:            protocol.KindAdvanceTicks,
				Payload:         protocol.AdvanceTicksPayload{N: 1},
			}
			if r := e.HandleCommand(adv); !r.Accepted {
				t.Errorf("AdvanceTicks rejected: %+v", r.Error)
				break
			}
		}
		close(stop)
	}()

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			last := -1
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := comp.JournalStatus()
				if !st.EntriesKnown {
					t.Errorf("goroutine %d: EntriesKnown=false for the default Recorder", id)
					return
				}
				if st.EntriesErr != nil {
					t.Errorf("goroutine %d: EntriesErr = %v", id, st.EntriesErr)
					return
				}
				if st.Entries < last {
					t.Errorf("goroutine %d: Entries went DOWN: %d after %d (torn read)", id, st.Entries, last)
					return
				}
				last = st.Entries
				if st.PersistHalted {
					t.Errorf("goroutine %d: PersistHalted=true with no persist store", id)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	final := comp.JournalStatus()
	if final.Entries != 600 {
		t.Fatalf("final Entries = %d, want 600 (300 SetSpeed + 300 AdvanceTicks)", final.Entries)
	}
}

// recorderByteCopy produces a struct-copied *replay.Recorder via a raw memcpy
// through unsafe.Pointer, mirroring citizensByteCopy in
// internal/engine/citizens/copyguard_test.go: a literal `cp := *r` is flagged
// by go vet's copylocks check, which CI runs bare (nolint directives only bind
// golangci-lint, not `go vet ./...`).
func recorderByteCopy(r *replay.Recorder) *replay.Recorder {
	cp := new(replay.Recorder)
	*(*[unsafe.Sizeof(replay.Recorder{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(replay.Recorder{})]byte)(unsafe.Pointer(r))
	return cp
}

// A2a: a struct-COPIED Recorder injected as the journaler. SEC-037 says
// Len() must REFUSE rather than report 0; JournalStatus must surface that
// as EntriesKnown=true + EntriesErr!=nil (we-have-a-recorder-that-refused),
// never as a silent 0.
func TestAttackJournalStatus_CopiedRecorderSurfacesErrNotZero(t *testing.T) {
	orig := replay.NewRecorder()
	copied := recorderByteCopy(orig) // deliberate SEC-037 copy under attack (raw memcpy: go vet copylocks runs bare in CI)
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: copied})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	st := comp.JournalStatus()
	if !st.EntriesKnown {
		t.Fatalf("EntriesKnown=false for a *replay.Recorder receiver, want true")
	}
	if st.EntriesErr == nil {
		t.Fatalf("EntriesErr=nil for a struct-copied Recorder — SEC-037 silent-zero regression (Entries=%d)", st.Entries)
	}
	t.Logf("copied-Recorder status = EntriesKnown=%v Entries=%d EntriesErr=%v", st.EntriesKnown, st.Entries, st.EntriesErr)
}

// A2b: the PRODUCTION persist path. When Deps.PersistStore is set, Wire
// wraps the Recorder in persistCommandJournaler, so c.state.journaler is
// NOT a *replay.Recorder. Document what the status surface says there.
func TestAttackJournalStatus_PersistWrappedJournaler(t *testing.T) {
	mem := persist.NewMemStore()
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{PersistStore: mem})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	for _, cmd := range journalTestSeq(t) {
		if r := e.HandleCommand(cmd); !r.Accepted {
			t.Fatalf("HandleCommand(%s): %+v", cmd.Kind, r.Error)
		}
	}
	st := comp.JournalStatus()
	t.Logf("PERSIST-WRAPPED status = EntriesKnown=%v Entries=%d halted=%v (5 commands were journaled)",
		st.EntriesKnown, st.Entries, st.PersistHalted)
	if st.EntriesKnown {
		t.Logf("wrapper is unwrapped by JournalStatus")
	} else {
		t.Logf("FINDING: the production persist path reports EntriesKnown=false; the GR#17 count is blind exactly where durability matters")
	}
	// Honesty bar (what this test ENFORCES): never a known-but-wrong count.
	if st.EntriesKnown && st.Entries != len(journalTestSeq(t)) {
		t.Fatalf("EntriesKnown=true but Entries=%d, want %d — a WRONG count is worse than unknown", st.Entries, len(journalTestSeq(t)))
	}
}

// A4a: persist halt is STICKY across further commands and ticks, and the
// status surface never masks it (halted=false while the engine is halted).
func TestAttackJournalStatus_HaltIsStickyAndNeverMasked(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: &failingComposeJournaler{}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if r := e.HandleCommand(journalTestSeq(t)[0]); r.Accepted {
		t.Fatal("first command accepted, want rejected (BUG-472 HALT+SURFACE)")
	}
	first := comp.JournalStatus()
	if !first.PersistHalted || first.PersistHaltCode == "" || first.PersistHaltCorrelationID == "" {
		t.Fatalf("first status = %+v, want halted with a real code+correlation", first)
	}
	// Drive many further commands (incl. tick advances) and re-check.
	for i := 0; i < 50; i++ {
		e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.NewCorrelationID(),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: 1},
		})
		st := comp.JournalStatus()
		if !st.PersistHalted {
			t.Fatalf("iteration %d: PersistHalted=false — halt MASKED after latch", i)
		}
		if st.PersistHaltCode != first.PersistHaltCode || st.PersistHaltCorrelationID != first.PersistHaltCorrelationID {
			t.Fatalf("iteration %d: halt identity drifted to (%q,%q), want (%q,%q) — a fresh code per rejection",
				i, st.PersistHaltCode, st.PersistHaltCorrelationID, first.PersistHaltCode, first.PersistHaltCorrelationID)
		}
		code, corr, ok := e.PersistHalted()
		if !ok || code != st.PersistHaltCode || corr != st.PersistHaltCorrelationID {
			t.Fatalf("iteration %d: GR#3 drift — engine (%q,%q,%v) vs status (%q,%q,%v)",
				i, code, corr, ok, st.PersistHaltCode, st.PersistHaltCorrelationID, st.PersistHalted)
		}
	}
}

// A4b: halt observed CONCURRENTLY — once any observer has seen halted=true,
// no later observation may see false.
func TestAttackJournalStatus_HaltNeverUnlatchesUnderConcurrency(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: &failingComposeJournaler{}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			seen := false
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := comp.JournalStatus()
				if st.PersistHalted {
					seen = true
				} else if seen {
					t.Errorf("goroutine %d: halted went true -> false", id)
					return
				}
			}
		}(g)
	}
	for i := 0; i < 200; i++ {
		e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.NewCorrelationID(),
			Kind:            protocol.KindSetSpeed,
			Payload:         protocol.SetSpeedPayload{Speed: 2},
		})
	}
	close(stop)
	wg.Wait()
}

// A3a: re-verify the inc4 claim that a REJECTED command is never journaled,
// using JournalStatus's own count as the witness (not the Recorder directly).
func TestAttackJournalStatus_RejectedCommandNotCounted(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	before := comp.JournalStatus().Entries
	bad := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 9999},
	}
	if r := e.HandleCommand(bad); r.Accepted {
		t.Fatalf("SetSpeed(9999) accepted — fixture no longer produces a rejection")
	}
	worse := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.Kind("not.a.real.kind"),
	}
	if r := e.HandleCommand(worse); r.Accepted {
		t.Fatalf("unknown kind accepted")
	}
	if got := comp.JournalStatus().Entries; got != before {
		t.Fatalf("Entries = %d after two REJECTED commands, want %d — rejected commands are being journaled", got, before)
	}
	// And an accepted one still lands.
	if r := e.HandleCommand(journalTestSeq(t)[0]); !r.Accepted {
		t.Fatalf("accepted-command sanity: %+v", r.Error)
	}
	if got := comp.JournalStatus().Entries; got != before+1 {
		t.Fatalf("Entries = %d after one accepted command, want %d", got, before+1)
	}
}

// A3b: replay-equivalence STRENGTH. journal_wire_test's proof compares only
// Clock().Tick()/Speed()/Paused(). Compare the full observable StateDigest
// of both compositions after replay — if the clocks match but the digests
// differ, the shipped proof is vacuous.
func TestAttackReplay_StateDigestEquivalenceNotJustClock(t *testing.T) {
	const seed = 42
	source := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	sourceComp, err := Wire(source, nil)
	if err != nil {
		t.Fatalf("Wire(source): %v", err)
	}
	sourceRec := sourceComp.Journaler().(*replay.Recorder)
	for _, cmd := range journalTestSeq(t) {
		r := source.HandleCommand(cmd)
		if !r.Accepted {
			t.Fatalf("source HandleCommand(%s): %+v", cmd.Kind, r.Error)
		}
		if err := sourceRec.ObserveResult(r); err != nil {
			t.Fatalf("ObserveResult: %v", err)
		}
	}
	records, err := sourceRec.Records()
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	player, err := replay.NewEnginePlayer(replay.Fixture{Name: "attack-inline", Records: records})
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}
	target := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	targetComp, err := Wire(target, nil)
	if err != nil {
		t.Fatalf("Wire(target): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	loopDone := make(chan error, 1)
	go func() { loopDone <- target.RunCommandLoop(ctx, player) }()
	cmp, err := player.Replay(ctx)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !cmp.Matched {
		t.Fatalf("result mismatches: %v", cmp.Diffs)
	}
	cancel()
	<-loopDone

	srcDigest := sourceComp.StateDigest()
	tgtDigest := targetComp.StateDigest()
	if srcDigest != tgtDigest {
		t.Fatalf("FINDING (proof vacuity): clocks match but StateDigest differs\n  source: %x\n  target: %x", srcDigest, tgtDigest)
	}

	// Non-vacuity: the digest must actually MOVE off a fresh composition's
	// value, or "equal digests" would prove nothing.
	fresh := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	freshComp, err := Wire(fresh, nil)
	if err != nil {
		t.Fatalf("Wire(fresh): %v", err)
	}
	if freshComp.StateDigest() == srcDigest {
		t.Fatalf("VACUOUS: a never-advanced composition has the same StateDigest as one that ran the sequence — the digest does not observe this workload")
	}
}
