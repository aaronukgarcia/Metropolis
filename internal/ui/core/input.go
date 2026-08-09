package core

import "github.com/gdamore/tcell/v2"

// InputKind discriminates InputMsg.Kind.
type InputKind int

const (
	// KeyInput is a translated *tcell.EventKey.
	KeyInput InputKind = iota
	// MouseInput is a translated *tcell.EventMouse.
	MouseInput
	// ResizeInput is a translated *tcell.EventResize.
	ResizeInput
	// OtherInput covers any tcell.Event this package doesn't specifically
	// model (focus events, interrupts, errors post-Init) — carried
	// through rather than silently dropped, so a consumer that cares can
	// still see it via Raw.
	OtherInput
)

// InputMsg is T-INPUT's translated output: a plain value, safe to pass
// on a channel and to read from any goroutine, unlike a raw tcell.Event
// (which callers should treat as read-only and not hold onto — Raw is
// provided for completeness/logging, not for widgets to depend on).
type InputMsg struct {
	Kind InputKind

	// Key fields, valid when Kind == KeyInput.
	Key  tcell.Key
	Rune rune
	Mod  tcell.ModMask

	// Mouse fields, valid when Kind == MouseInput.
	MouseX, MouseY int
	MouseButtons   tcell.ButtonMask

	// Resize fields, valid when Kind == ResizeInput.
	Width, Height int

	// Raw is the original event, always populated.
	Raw tcell.Event
}

// translate converts a tcell.Event into an InputMsg. It performs no I/O
// and never blocks — the property AC-2 tests.
func translate(ev tcell.Event) InputMsg {
	switch e := ev.(type) {
	case *tcell.EventKey:
		return InputMsg{Kind: KeyInput, Key: e.Key(), Rune: e.Rune(), Mod: e.Modifiers(), Raw: ev}
	case *tcell.EventMouse:
		x, y := e.Position()
		return InputMsg{Kind: MouseInput, MouseX: x, MouseY: y, MouseButtons: e.Buttons(), Mod: e.Modifiers(), Raw: ev}
	case *tcell.EventResize:
		w, h := e.Size()
		return InputMsg{Kind: ResizeInput, Width: w, Height: h, Raw: ev}
	default:
		return InputMsg{Kind: OtherInput, Raw: ev}
	}
}

// EventSource is the subset of tcell.Screen that T-INPUT needs: just
// PollEvent. A real tcell.Screen satisfies this trivially; tests can
// inject any fake source (including tcell.SimulationScreen, or a
// hand-rolled one for AC-2's non-blocking test) without needing a
// running screen.
type EventSource interface {
	PollEvent() tcell.Event
}

// InputLoop is T-INPUT: it polls src.PollEvent() in a loop (Run should
// be started in its own goroutine) and translates each event to an
// InputMsg delivered on Out(). Delivery never blocks: if Out()'s buffer
// is full, the oldest queued InputMsg is evicted to make room for the
// newest (util.go's trySendEvictOldest) — UI-SPEC §1/§5's "<10ms echo,
// never waits on anything" would be violated by a blocking send just as
// surely as by a blocking read, so both directions of this loop are
// non-blocking by construction.
//
// InputLoop never touches a tcell.Screen directly beyond PollEvent
// (M0-ENG §1.1: "T-INPUT only translates events to messages") — it has
// no reference to a Screen, only to the narrower EventSource.
type InputLoop struct {
	src EventSource
	out chan InputMsg
	// onDelivered, if set, is called synchronously after every message is
	// enqueued (whether or not an eviction occurred) — render.go's
	// RenderLoop uses this to trigger an immediate render-on-input
	// without InputLoop needing to know RenderLoop exists.
	onDelivered func(InputMsg)
}

// NewInputLoop constructs an InputLoop reading from src and buffering up
// to bufSize InputMsg values (bufSize < 1 is treated as 1).
func NewInputLoop(src EventSource, bufSize int) *InputLoop {
	if bufSize < 1 {
		bufSize = 1
	}
	return &InputLoop{src: src, out: make(chan InputMsg, bufSize)}
}

// Out returns the channel InputMsg values are delivered on.
func (l *InputLoop) Out() <-chan InputMsg { return l.out }

// OnDelivered registers a callback invoked after each InputMsg is
// enqueued. It must not block or perform screen I/O — it runs on
// InputLoop's own goroutine, and per M0-ENG §1.1 that goroutine must
// never touch the tcell.Screen.
func (l *InputLoop) OnDelivered(fn func(InputMsg)) { l.onDelivered = fn }

// Run polls src for events until it returns nil (tcell.Screen.PollEvent
// returns nil once the screen is finalized — the documented shutdown
// signal) or until stop is closed. Intended to run in its own goroutine.
func (l *InputLoop) Run(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		ev := l.src.PollEvent()
		if ev == nil {
			return
		}
		msg := translate(ev)
		trySendEvictOldest(l.out, msg)
		if l.onDelivered != nil {
			l.onDelivered(msg)
		}
	}
}
