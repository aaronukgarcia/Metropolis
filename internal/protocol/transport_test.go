package protocol

import (
	"errors"
	"sync"
	"testing"
)

func testCommand(corr CorrelationID) Command {
	return Command{
		ProtocolVersion: ProtocolVersion,
		CorrelationID:   corr,
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         PausePayload{},
	}
}

func TestInProcTransport_CommandSendReceive(t *testing.T) {
	tr := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	defer tr.Close()

	cmd := testCommand("c1")
	if err := tr.SendCommand(cmd); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	select {
	case got := <-tr.Commands():
		if got != cmd {
			t.Fatalf("received command mismatch: got %#v, want %#v", got, cmd)
		}
	default:
		t.Fatal("expected a command to be immediately available on Commands()")
	}
}

func TestInProcTransport_SendCommand_RejectsInvalid(t *testing.T) {
	tr := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	defer tr.Close()

	invalid := testCommand("") // missing correlation ID
	err := tr.SendCommand(invalid)
	if !errors.Is(err, ErrMissingCorrelationID) {
		t.Fatalf("SendCommand(invalid) = %v, want ErrMissingCorrelationID", err)
	}

	select {
	case got := <-tr.Commands():
		t.Fatalf("invalid command must not be enqueued, got %#v", got)
	default:
	}
}

func TestInProcTransport_SendCommand_QueueFull(t *testing.T) {
	tr := NewInProcTransport(1, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	defer tr.Close()

	if err := tr.SendCommand(testCommand("c1")); err != nil {
		t.Fatalf("first SendCommand: %v", err)
	}
	err := tr.SendCommand(testCommand("c2"))
	if !errors.Is(err, ErrCommandQueueFull) {
		t.Fatalf("second SendCommand (queue full) = %v, want ErrCommandQueueFull", err)
	}

	// The first command must still be the one queued (not silently
	// replaced) — commands, unlike outbound messages, are never dropped.
	got := <-tr.Commands()
	if got.CorrelationID != "c1" {
		t.Fatalf("queued command CorrelationID = %q, want %q", got.CorrelationID, "c1")
	}
}

func TestInProcTransport_ResultEventDeltaSendReceive(t *testing.T) {
	tr := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	defer tr.Close()

	if ok := tr.SendResult(CommandResult{CorrelationID: "c1", Tick: 1, Accepted: true}); !ok {
		t.Fatal("SendResult returned false, want true")
	}
	if ok := tr.SendEvent(Event{Kind: "test.event", Tick: 1, Severity: SeverityInfo}); !ok {
		t.Fatal("SendEvent returned false, want true")
	}
	if ok := tr.SendDelta(Delta{SubscriptionID: "sub-1", Tick: 1, Seq: 1}); !ok {
		t.Fatal("SendDelta returned false, want true")
	}

	if r := <-tr.Results(); r.CorrelationID != "c1" {
		t.Fatalf("Results() = %#v, want CorrelationID c1", r)
	}
	if e := <-tr.Events(); e.Kind != "test.event" {
		t.Fatalf("Events() = %#v, want Kind test.event", e)
	}
	if d := <-tr.Deltas(); d.SubscriptionID != "sub-1" {
		t.Fatalf("Deltas() = %#v, want SubscriptionID sub-1", d)
	}
}

// TestInProcTransport_DeltaDropPolicy exercises the documented
// evict-oldest full-buffer policy (transport.go): with no reader
// draining the Deltas channel, sending more deltas than the buffer holds
// must never block, and the freshest delta must be the one still queued
// once the buffer settles ("the last frame stands" per UI-SPEC §1).
func TestInProcTransport_DeltaDropPolicy(t *testing.T) {
	const bufSize = 4
	tr := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, bufSize)
	defer tr.Close()

	const totalSent = 20
	for seq := uint64(1); seq <= totalSent; seq++ {
		if ok := tr.SendDelta(Delta{SubscriptionID: "sub-1", Tick: Tick(seq), Seq: seq}); !ok {
			t.Fatalf("SendDelta(seq=%d) returned false, want true (transport not closed)", seq)
		}
	}

	// Drain whatever is queued; there must be at most bufSize deltas
	// (never more — SendDelta must not have blocked to accumulate a
	// bigger backlog), and the LAST one drained must be the most recent
	// (Seq == totalSent), proving eviction favours freshness.
	var drained []Delta
	for {
		select {
		case d := <-tr.Deltas():
			drained = append(drained, d)
		default:
			goto done
		}
	}
done:
	if len(drained) == 0 {
		t.Fatal("expected at least one delta to remain queued")
	}
	if len(drained) > bufSize {
		t.Fatalf("drained %d deltas, want at most bufSize=%d", len(drained), bufSize)
	}
	last := drained[len(drained)-1]
	if last.Seq != totalSent {
		t.Fatalf("most recently queued delta has Seq=%d, want %d (freshest must survive eviction)", last.Seq, totalSent)
	}
	// And every seq that did survive must be in ascending order (eviction
	// removes from the front, never reorders).
	for i := 1; i < len(drained); i++ {
		if drained[i].Seq <= drained[i-1].Seq {
			t.Fatalf("drained deltas out of order at index %d: %d then %d", i, drained[i-1].Seq, drained[i].Seq)
		}
	}
}

func TestInProcTransport_Close(t *testing.T) {
	tr := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent: a second Close must not panic.
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if err := tr.SendCommand(testCommand("c1")); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("SendCommand after Close = %v, want ErrTransportClosed", err)
	}
	if ok := tr.SendResult(CommandResult{CorrelationID: "c1", Accepted: true}); ok {
		t.Fatal("SendResult after Close returned true, want false")
	}
	if ok := tr.SendEvent(Event{Kind: "x"}); ok {
		t.Fatal("SendEvent after Close returned true, want false")
	}
	if ok := tr.SendDelta(Delta{SubscriptionID: "sub-1"}); ok {
		t.Fatal("SendDelta after Close returned true, want false")
	}

	// Channels must be closed (reads return zero value, ok=false).
	if _, ok := <-tr.Results(); ok {
		t.Fatal("Results() channel not closed after Close")
	}
	if _, ok := <-tr.Events(); ok {
		t.Fatal("Events() channel not closed after Close")
	}
	if _, ok := <-tr.Deltas(); ok {
		t.Fatal("Deltas() channel not closed after Close")
	}
}

// TestInProcTransport_Race drives concurrent producers (engine side) and
// consumers (UI side) plus a concurrent Close, intended to be run with
// -race. It asserts no panics/deadlocks rather than exact message
// counts, since the drop policy makes exact counts under contention
// nondeterministic by design.
func TestInProcTransport_Race(t *testing.T) {
	tr := NewInProcTransport(8, 8, 8, 8)

	var wg sync.WaitGroup

	// UI side: send commands.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = tr.SendCommand(testCommand(CorrelationID("race")))
		}
	}()

	// Engine side: drain commands, send results/events/deltas.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			select {
			case <-tr.Commands():
			default:
			}
			tr.SendResult(CommandResult{CorrelationID: "race", Accepted: true})
			tr.SendEvent(Event{Kind: "race.event", Tick: Tick(i)})
			tr.SendDelta(Delta{SubscriptionID: "sub-race", Tick: Tick(i), Seq: uint64(i + 1)})
		}
	}()

	// UI side: drain results/events/deltas.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			select {
			case <-tr.Results():
			default:
			}
			select {
			case <-tr.Events():
			default:
			}
			select {
			case <-tr.Deltas():
			default:
			}
		}
	}()

	wg.Wait()
	tr.Close()
}
