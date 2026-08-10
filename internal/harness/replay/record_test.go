package replay

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func cmdFixture(id string) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(id),
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
}

// TestRecorderObserveStrictArrivalOrder is AC-1: every Observe* call
// appends in the order it was made.
func TestRecorderObserveStrictArrivalOrder(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < 5; i++ {
		if err := r.ObserveCommand(cmdFixture(string(rune('a' + i)))); err != nil {
			t.Fatalf("ObserveCommand: %v", err)
		}
	}
	recs := r.Records()
	if len(recs) != 5 {
		t.Fatalf("got %d records, want 5", len(recs))
	}
	for i, rec := range recs {
		want := string(rune('a' + i))
		cmd, err := protocol.DecodeCommand(rec.Data)
		if err != nil {
			t.Fatalf("DecodeCommand[%d]: %v", i, err)
		}
		if string(cmd.CorrelationID) != want {
			t.Errorf("record[%d] correlation = %q, want %q — arrival order not preserved", i, cmd.CorrelationID, want)
		}
	}
}

// TestRecorderOrderEnforcedCannotReorderCapturedRecords is AC-1b's named
// check: Recorder's exported surface (Observe*, Records, Len) offers no
// mutator capable of reordering or splicing already-captured records —
// Records returns a copy (proven by
// TestRecorderRecordsIsDefensiveCopy below), so even a caller that
// mutates its own copy cannot touch the Recorder's real sequence, and
// there is no InsertAt/exported slice field to call instead. This test
// exists primarily so its name is greppable per the acceptance
// criteria's check
// (`grep -rn "func Test.*[Oo]rder.*[Ee]nforc|func Test.*[Cc]annotReorder"`);
// the substantive proof is TestRecorderRecordsIsDefensiveCopy and
// TestRecorderObserveStrictArrivalOrder above.
func TestRecorderOrderEnforcedCannotReorderCapturedRecords(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < 3; i++ {
		if err := r.ObserveCommand(cmdFixture(string(rune('a' + i)))); err != nil {
			t.Fatalf("ObserveCommand: %v", err)
		}
	}
	before := r.Records()

	// Attempt to "reorder" via the only exported handle a caller has —
	// mutating the returned copy — then re-fetch and confirm the
	// Recorder's own sequence is untouched.
	before[0], before[2] = before[2], before[0]

	after := r.Records()
	orig, err := protocol.DecodeCommand(after[0].Data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if orig.CorrelationID != "a" {
		t.Fatalf("Recorder's captured order was changed via a caller-side copy mutation: record[0] correlation = %q, want %q", orig.CorrelationID, "a")
	}
}

// TestRecorderRecordsIsDefensiveCopy is AC-1b: mutating the returned
// slice must never affect the Recorder's own captured state.
func TestRecorderRecordsIsDefensiveCopy(t *testing.T) {
	r := NewRecorder()
	if err := r.ObserveCommand(cmdFixture("only")); err != nil {
		t.Fatalf("ObserveCommand: %v", err)
	}
	recs := r.Records()
	recs[0].Kind = "tampered"

	fresh := r.Records()
	if len(fresh) != 1 {
		t.Fatalf("Recorder state changed by mutating a returned copy: got %d records, want 1", len(fresh))
	}
	if fresh[0].Kind != string(KindCommand) {
		t.Errorf("Recorder's own record was mutated via the returned copy: Kind = %q", fresh[0].Kind)
	}
}

// TestRecorderConcurrentObserveNeverCorruptsOrDuplicates is AC-1b's
// concurrency requirement: two goroutines racing to submit must never
// silently corrupt or duplicate the captured sequence — every record
// that lands must be traceable to exactly one Observe* call, and the
// total count must equal the number of calls made, under any scheduling.
// Deterministic in what it ASSERTS (never depends on which goroutine
// wins the race, only on the invariant holding regardless) — the
// "construct the state, don't race for the timing" rule from
// docs/planning/dev-team-process.md v1.8.
func TestRecorderConcurrentObserveNeverCorruptsOrDuplicates(t *testing.T) {
	r := NewRecorder()
	const perGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < perGoroutine; i++ {
			_ = r.ObserveCommand(cmdFixture("a"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < perGoroutine; i++ {
			_ = r.ObserveCommand(cmdFixture("b"))
		}
	}()
	wg.Wait()

	recs := r.Records()
	if len(recs) != 2*perGoroutine {
		t.Fatalf("got %d records, want %d — a concurrent Observe was lost or duplicated", len(recs), 2*perGoroutine)
	}
	var aCount, bCount int
	for _, rec := range recs {
		cmd, err := protocol.DecodeCommand(rec.Data)
		if err != nil {
			t.Fatalf("DecodeCommand: %v (a corrupted record proves the append was not properly serialised)", err)
		}
		switch cmd.CorrelationID {
		case "a":
			aCount++
		case "b":
			bCount++
		default:
			t.Fatalf("unexpected correlation ID %q — record corrupted by the race", cmd.CorrelationID)
		}
	}
	if aCount != perGoroutine || bCount != perGoroutine {
		t.Fatalf("aCount=%d bCount=%d, want %d each — some Observe calls were lost", aCount, bCount, perGoroutine)
	}
}

// TestRecorderCorrelationIDPreservedVerbatim is AC-7: CorrelationID
// round-trips unchanged through capture and decode.
func TestRecorderCorrelationIDPreservedVerbatim(t *testing.T) {
	r := NewRecorder()
	want := protocol.NewCorrelationID()
	res := protocol.CommandResult{CorrelationID: want, Accepted: true}
	if err := r.ObserveResult(res); err != nil {
		t.Fatalf("ObserveResult: %v", err)
	}
	recs := r.Records()
	var got protocol.CommandResult
	if err := json.Unmarshal(recs[0].Data, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CorrelationID != want {
		t.Errorf("CorrelationID = %q, want %q", got.CorrelationID, want)
	}
}
