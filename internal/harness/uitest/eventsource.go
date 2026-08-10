package uitest

import "github.com/gdamore/tcell/v2"

// chanEventSource is a core.EventSource backed by a channel this package
// controls directly — the headless stand-in for a real terminal's input
// stream (which core.InputLoop would otherwise poll via tcell.Screen).
// Harness.SendKeys/SendKeyEvents inject events by writing to events;
// InputLoop.Run (T-INPUT) reads them out via PollEvent, translating each
// into an InputMsg exactly as it would for a real terminal (AC-1).
//
// events is buffered generously (see newChanEventSource) so SendKeys
// never blocks the calling (test) goroutine on T-INPUT's own pace —
// UI-SPEC §1's "never blocks, ever" applies to T-INPUT's read side, not
// to a test harness's synchronous injection call, but a generous buffer
// keeps the two goroutines decoupled regardless.
type chanEventSource struct {
	events chan tcell.Event
}

// eventBuffer is chanEventSource's channel capacity — large enough that
// a single scripted sequence (a handful to a few dozen keys) never fills
// it before T-INPUT drains it.
const eventBuffer = 256

func newChanEventSource() *chanEventSource {
	return &chanEventSource{events: make(chan tcell.Event, eventBuffer)}
}

// PollEvent implements core.EventSource. It returns nil once close has
// been called and every already-queued event has been drained — the
// same "PollEvent returns nil on shutdown" contract core.InputLoop.Run
// documents for a real tcell.Screen.
func (s *chanEventSource) PollEvent() tcell.Event {
	ev, ok := <-s.events
	if !ok {
		return nil
	}
	return ev
}

// inject enqueues ev for PollEvent to deliver. Called only before close.
func (s *chanEventSource) inject(ev tcell.Event) {
	s.events <- ev
}

// close signals shutdown: PollEvent returns nil once every already-
// queued event has drained, which unblocks InputLoop.Run (T-INPUT)
// deterministically rather than leaving it parked in a blocking receive
// forever.
func (s *chanEventSource) close() {
	close(s.events)
}
