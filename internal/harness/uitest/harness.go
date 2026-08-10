package uitest

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	uicore "github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// nopWriter is a core.ScreenWriter that does nothing — Harness never
// needs a real terminal to receive the diffed cells; it only needs
// core.Flush's side effect of reconciling back into front so Capture()
// reads a caught-up buffer.
type nopWriter struct{}

func (nopWriter) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {}
func (nopWriter) Show()                                                                  {}

// Harness constructs ui.core's headless plumbing — an InputLoop reading
// from an injectable event source, a ViewStore, and (once AttachFixture
// is called) a ViewsLoop consuming a harness.replay fixture — and drives
// it with scripted key sequences (AC-1). It owns its own back/front
// Buffer pair and a caller-supplied set of DrawFuncs, so Render()+
// Capture() exercises the exact same core.Flush path a real screen's
// render loop would.
//
// Zero value is NOT ready for use (SEC-020-class); construct with
// NewHarness. Harness holds a sync.Mutex alongside reference fields
// (buffers, channels, a WaitGroup) — see doc.go's "Copy-safety" section
// for the self-identity guard this implies.
type Harness struct {
	mu      sync.Mutex
	stopped bool

	correlationID string
	draws         []uicore.DrawFunc
	onKey         func(uicore.InputMsg)

	src   *chanEventSource
	input *uicore.InputLoop
	views *uicore.ViewStore

	back  *uicore.Buffer
	front *uicore.Buffer

	stopCh chan struct{}
	wg     sync.WaitGroup

	pump *fixturePlayback

	// self holds the address NewHarness gave this Harness at
	// construction. Mirrors harness.replay's Recorder.self exactly (see
	// that field's doc comment for the full SEC-016 ordering rationale):
	// atomic.Pointer, not a plain field, so the identity check is
	// race-safe and can run BEFORE mu is ever touched.
	self atomic.Pointer[Harness]
}

// defaultCols/defaultRows size a new Harness's buffers to ui.core's own
// documented minimum terminal size (UI-SPEC §1), so a caller's DrawFuncs
// see the same dimensions they would in the real minimum-supported
// terminal, not an arbitrary test-only size.
const (
	defaultCols = uicore.MinCols
	defaultRows = uicore.MinRows
)

// NewHarness constructs a ready-to-use Harness. onKey, if non-nil, is
// invoked synchronously on the harness's own T-INPUT goroutine
// (uicore.InputLoop.OnDelivered's documented contract) after every
// injected key is translated — the seam a caller wires a screen's own
// key-handling logic through (ui.keys, MOD-011, is not yet built; a
// caller of this package supplies whatever key-to-effect logic it is
// testing directly, same as core.RenderLoop's own draws parameter
// pattern). draws are invoked, in order, by Render().
func NewHarness(correlationID string, onKey func(uicore.InputMsg), draws ...uicore.DrawFunc) *Harness {
	if onKey == nil {
		onKey = func(uicore.InputMsg) {}
	}
	h := &Harness{
		correlationID: correlationID,
		draws:         draws,
		onKey:         onKey,
		src:           newChanEventSource(),
		views:         uicore.NewViewStore(),
		back:          uicore.NewBuffer(defaultCols, defaultRows),
		front:         uicore.NewBuffer(defaultCols, defaultRows),
		stopCh:        make(chan struct{}),
	}
	h.input = uicore.NewInputLoop(h.src, eventBuffer)
	h.input.OnDelivered(h.onKey)

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.input.Run(h.stopCh)
	}()

	// Stored exactly once, here, before h is returned to any caller — no
	// goroutine can have a reference to h to race this Store against
	// (mirrors NewRecorder/NewEnginePlayer — see self's doc comment).
	h.self.Store(h)
	return h
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Harness value. Deliberately lock-free so it is safe to call
// BEFORE mu is ever touched (SEC-016 — see Recorder.checkNotCopied's
// doc comment for the full ordering rationale this mirrors).
func (h *Harness) checkNotCopied() error {
	if h.self.Load() != h {
		return errs.New(codeHarnessCopied, h.correlationID, nil)
	}
	return nil
}

// SendKeys parses script (keyscript.go's DSL) and injects the resulting
// key events into the harness's input stream, in order (AC-1, AC-2). A
// malformed token fails the whole call (MET-H100) and injects nothing.
func (h *Harness) SendKeys(script string) error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	events, err := ParseScript(script)
	if err != nil {
		return err
	}
	return h.SendKeyEvents(events)
}

// SendKeyEvents injects already-built key events directly, for callers
// that need finer control than the DSL affords (AC-1's "or equivalent").
func (h *Harness) SendKeyEvents(events []*tcell.EventKey) error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	for _, ev := range events {
		h.src.inject(ev)
	}
	return nil
}

// AttachFixture wires a harness.replay fixture as the harness's Delta
// (and Result/Event) source, in place of a live protocol.Transport
// (AC-3): it starts a ViewsLoop (ui.core's own T-VIEWS) consuming the
// fixture's canned stream and publishing to the harness's ViewStore, so
// DrawFuncs passed to NewHarness see exactly the ViewModels a real
// engine's Deltas would have produced. May be called at most once per
// Harness.
func (h *Harness) AttachFixture(f replay.Fixture) error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	h.mu.Lock()
	if h.pump != nil {
		h.mu.Unlock()
		return errs.New(codeFixturePlaybackReadOnly, h.correlationID, map[string]any{"cause": "AttachFixture called twice on the same Harness"})
	}
	h.mu.Unlock()

	player, err := replay.NewUIPlayer(f)
	if err != nil {
		return err
	}
	pump := newFixturePlayback(player, h.stopCh)
	vloop := uicore.NewViewsLoop(pump, h.views, h.correlationID)

	h.mu.Lock()
	h.pump = pump
	h.mu.Unlock()

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		vloop.Run(h.stopCh)
	}()
	return nil
}

// AwaitDeltas blocks until an attached fixture (AttachFixture) has
// forwarded at least want deltas to the consuming ViewsLoop, or its
// stream is exhausted first, or timeout elapses. It is driven by real
// completion signals — fixturePlayback.DeltasSeen()'s count and the
// exhausted channel closing — polled on a short, bounded interval, never
// a fixed sleep guessing how long delivery takes (GR#21; doc.go's
// "Determinism" section).
//
// A stream that closes with fewer than want deltas forwarded is AC-3b's
// "fixture exhausted before the scripted sequence's expected effects
// were all observed" — reported as the distinct MET-H101, never
// conflated with a clean, fully-observed run.
//
// "Forwarded" alone is not enough to prove an effect landed (forwarding
// a delta and ViewsLoop finishing its apply are separate events on
// separate goroutines) — AwaitDeltas additionally waits for
// transport.go's pump-appended barrier delta to appear in the
// ViewStore, which (see pump's doc comment) IS a hard proof every prior
// delta already applied, given ViewsLoop's strictly sequential
// single-goroutine consumption.
func (h *Harness) AwaitDeltas(want int, timeout time.Duration) error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	h.mu.Lock()
	pump := h.pump
	h.mu.Unlock()
	if pump == nil {
		return errs.New(codeFixtureExhausted, h.correlationID, map[string]any{
			"want": want, "got": 0, "cause": "AwaitDeltas called with no fixture attached",
		})
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()

	// exhaustedCh is nilled out after its one useful firing so the select
	// below never busy-spins re-selecting an already-closed channel while
	// waiting on barrierObserved() to catch up (a nil channel case is
	// simply never selectable — the standard Go idiom for "stop watching
	// this channel").
	exhaustedCh := pump.Exhausted()

	for {
		seen := pump.DeltasSeen()
		if seen >= int64(want) && h.barrierObserved() {
			return nil
		}
		select {
		case <-exhaustedCh:
			exhaustedCh = nil
			if seen := pump.DeltasSeen(); seen < int64(want) {
				return errs.New(codeFixtureExhausted, h.correlationID, map[string]any{
					"want": want, "got": seen,
				})
			}
			// Exhausted with enough deltas forwarded: the barrier itself is
			// sent as the very last item on this same channel, so it may
			// not have been applied yet at the instant Exhausted() fired —
			// loop back around to keep polling barrierObserved().
		case <-deadline.C:
			return errs.New(codeFixtureExhausted, h.correlationID, map[string]any{
				"want": want, "got": pump.DeltasSeen(), "cause": "timed out before settling",
			})
		case <-poll.C:
		}
	}
}

// barrierObserved reports whether transport.go's synthetic barrier
// delta has reached the harness's ViewStore — see pump's doc comment
// for why its presence proves every real delta sent before it has
// already been applied.
func (h *Harness) barrierObserved() bool {
	vm := h.views.Front()
	_, ok := vm.Tick[barrierSubID]
	return ok
}

// RunScript is SendKeys followed by AwaitDeltas(wantDeltas, timeout) —
// the common case of "drive this script, then wait for its expected
// effects to land" (AC-3b). Pass wantDeltas <= 0 to skip the wait (no
// fixture attached, or the script's effects are not delta-observable).
func (h *Harness) RunScript(script string, wantDeltas int, timeout time.Duration) error {
	if err := h.SendKeys(script); err != nil {
		return err
	}
	if wantDeltas <= 0 {
		return nil
	}
	return h.AwaitDeltas(wantDeltas, timeout)
}

// SetDraws replaces the DrawFuncs Render() invokes — the harness-level
// equivalent of a real UI switching which screen is on top (used by
// TestLatencyScreenSwitch to time exactly that transition).
func (h *Harness) SetDraws(draws ...uicore.DrawFunc) error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.draws = draws
	return nil
}

// Render performs one draw+flush cycle: every registered DrawFunc writes
// into the back buffer given the current ViewStore snapshot, then
// core.Flush reconciles back into front (AC-4). Returns the FlushStats
// core.Flush reported, for latency/allocation-sensitive callers.
func (h *Harness) Render() (uicore.FlushStats, error) {
	if err := h.checkNotCopied(); err != nil {
		return uicore.FlushStats{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkNotCopied(); err != nil {
		return uicore.FlushStats{}, err
	}
	vm := h.views.Front()
	for _, d := range h.draws {
		d(h.back, vm)
	}
	return uicore.Flush(nopWriter{}, h.back, h.front), nil
}

// Capture renders the front buffer (the state Render()'s most recent
// Flush call reconciled to) into a human-diffable plain-text grid — one
// line per row, one rune per cell (AC-4, AC-5). See doc.go's "Cell-buffer
// capture scope" note for why only Rune, not Style, is captured.
func (h *Harness) Capture() (string, error) {
	if err := h.checkNotCopied(); err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkNotCopied(); err != nil {
		return "", err
	}
	return renderBufferText(h.front), nil
}

// renderBufferText is Capture's pure formatting step, split out so
// snapshot_test.go can exercise it directly without a full Harness.
func renderBufferText(b *uicore.Buffer) string {
	w, hh := b.Size()
	out := make([]rune, 0, (w+1)*hh)
	for y := 0; y < hh; y++ {
		for x := 0; x < w; x++ {
			out = append(out, b.Get(x, y).Rune)
		}
		out = append(out, '\n')
	}
	return string(out)
}

// Stop shuts the harness down: stops T-INPUT and any attached fixture's
// T-VIEWS loop, and waits for every goroutine NewHarness/AttachFixture
// started to exit. Idempotent — safe to call more than once, and safe to
// defer immediately after NewHarness.
func (h *Harness) Stop() {
	if err := h.checkNotCopied(); err != nil {
		return
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.mu.Unlock()

	close(h.stopCh)
	h.src.close()
	h.wg.Wait()
}
