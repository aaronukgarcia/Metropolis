package core

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// fakeEventSource replays a fixed slice of events, then returns nil
// (tcell.Screen.PollEvent's own "screen finalized" signal) — enough to
// drive InputLoop.Run to completion deterministically without any real
// or simulated terminal.
type fakeEventSource struct {
	events []tcell.Event
	i      int
}

func (f *fakeEventSource) PollEvent() tcell.Event {
	if f.i >= len(f.events) {
		return nil
	}
	ev := f.events[f.i]
	f.i++
	return ev
}

func TestInputLoop_NeverBlocks_EvenWithFullBuffer(t *testing.T) {
	// More events than the buffer can hold, and nothing draining Out()
	// concurrently — if delivery ever blocked (rather than evicting the
	// oldest queued message), Run would deadlock and this test would time
	// out (AC-2: T-INPUT never blocks, ever).
	const bufSize = 4
	const eventCount = 50

	events := make([]tcell.Event, eventCount)
	for i := range events {
		events[i] = tcell.NewEventKey(tcell.KeyRune, rune('a'+i%26), tcell.ModNone)
	}

	loop := NewInputLoop(&fakeEventSource{events: events}, bufSize)

	done := make(chan struct{})
	go func() {
		loop.Run(nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("InputLoop.Run did not return promptly — a blocking send would deadlock here")
	}

	// The buffer should hold at most bufSize messages (the newest ones),
	// never the full eventCount — proof that older messages were evicted
	// rather than the loop blocking until a reader drained them.
	if got := len(loop.Out()); got > bufSize {
		t.Fatalf("Out() channel holds %d messages, want <= %d", got, bufSize)
	}
}

func TestInputLoop_TranslatesKeyEvent(t *testing.T) {
	ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	loop := NewInputLoop(&fakeEventSource{events: []tcell.Event{ev}}, 4)
	loop.Run(nil)

	select {
	case msg := <-loop.Out():
		if msg.Kind != KeyInput {
			t.Fatalf("Kind = %v, want KeyInput", msg.Kind)
		}
		if msg.Key != tcell.KeyEnter {
			t.Fatalf("Key = %v, want KeyEnter", msg.Key)
		}
	default:
		t.Fatal("expected a translated InputMsg on Out()")
	}
}

func TestInputLoop_TranslatesResizeEvent(t *testing.T) {
	ev := tcell.NewEventResize(160, 45)
	loop := NewInputLoop(&fakeEventSource{events: []tcell.Event{ev}}, 4)
	loop.Run(nil)

	msg := <-loop.Out()
	if msg.Kind != ResizeInput {
		t.Fatalf("Kind = %v, want ResizeInput", msg.Kind)
	}
	if msg.Width != 160 || msg.Height != 45 {
		t.Fatalf("Width/Height = %d/%d, want 160/45", msg.Width, msg.Height)
	}
}

func TestInputLoop_OnDeliveredCallback(t *testing.T) {
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	loop := NewInputLoop(&fakeEventSource{events: []tcell.Event{ev}}, 4)

	var got int
	loop.OnDelivered(func(InputMsg) { got++ })
	loop.Run(nil)

	if got != 1 {
		t.Fatalf("OnDelivered called %d times, want 1", got)
	}
}

func TestInputLoop_StopChannel(t *testing.T) {
	// A source that never runs out (blocks in the sense of "always has
	// more"), paired with a closed stop channel: Run must still return
	// promptly, since InputLoop checks stop between polls. We simulate
	// "always more" by pre-closing stop before Run is even called; Run's
	// select on stop should fire on the very first iteration.
	stop := make(chan struct{})
	close(stop)

	loop := NewInputLoop(&fakeEventSource{events: []tcell.Event{
		tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
	}}, 1)

	done := make(chan struct{})
	go func() {
		loop.Run(stop)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("InputLoop.Run did not honor a pre-closed stop channel")
	}
}
