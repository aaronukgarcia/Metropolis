package main

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	chromescreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/chrome"
)

// BUG-324: the GLOBAL chrome's render + delta adapters.
//
// Per Aaron's F9 ruling chrome is not an F-key-selected screen of its
// own: it renders OVER the active screen. So it is deliberately NOT
// registered in w.screens (the ScreenRegistry) — it is composed on top
// of whatever ActiveDraw() produced, exactly the way BUG-322's bottom
// status line is composed on top of it. See chromeTopBarDraw.

// chromeTopBarRows is how many buffer rows the global chrome is given.
//
// ONE, deliberately: chrome.Render treats row 0 of its rect as the top
// bar and every subsequent row as the prioritised alert stack, and this
// binary wires no alert SOURCE (nothing engine-side publishes alerts,
// and Effects.Navigator has no drill-through target registry to point
// at yet — see bootCore's chrome construction). Handing chrome the full
// buffer height would therefore reserve rows for a stack that can never
// receive an entry, while giving its Render licence to paint over the
// active screen's body. A height of 1 makes drawAlertStack a no-op by
// construction (its first row is rect.Y+1, already outside the rect)
// rather than by luck. Widening this is the alert wiring's own job.
const chromeTopBarRows = 1

// chromeTopBarDraw returns a core.DrawFunc that renders the global
// chrome's top bar across row 0, on top of whatever the active screen
// drew there.
//
// # How this composes with BUG-322's status line
//
// It COMPLEMENTS it; it does not supersede it. They are different rows
// carrying different facts, split by SUBJECT (FEAT-216, the lead's
// ruling on this fix's independent round):
//
//   - top row (here) = WORLD state: date, clock cycle, money,
//     population, credit rating — when you are and what you have, from
//     "chrome.topbar".
//   - bottom row (BUG-322's statusBarDraw) = MACHINE state: tick,
//     month, speed, running/paused, and the key help — from
//     "engine.status".
//
// Speed used to appear on BOTH, in two disagreeing formats ("speed 1"
// here, "SPEED 1x RUNNING" there). It is machine state, so FEAT-216
// removed it from the top bar only; the bottom bar is untouched. See
// chrome/render.go's topBarString for why the FIELD is kept and kept
// truthful even though this bar no longer prints it.
//
// Row 0 rather than the bottom row is not a free choice — chrome.Render
// defines row 0 of its rect as the top bar, and "top bar" is what the
// item asks for. The round found the real cost of that: map/finance/
// services each drew their own heading at y=0, so the bar covered the
// only line identifying which screen you were looking at. The lead's
// ruling took the durable fix rather than a per-bar compromise —
// **chrome owns row 0, the screens' content starts at row 1** — and
// screenContentRect below is the single place that inset is computed,
// for all three screens at once (a per-screen exception is how a
// treatment like this drifts back apart).
//
// Contract-clean: this only writes to back, which is core.DrawFunc's
// one requirement (ui/core/render.go — "Draw callbacks must only write
// to back").
func chromeTopBarDraw(c *chromescreen.Chrome) core.DrawFunc {
	return func(back *core.Buffer, _ *core.ViewModels) {
		if c == nil || back == nil {
			return
		}
		w, h := back.Size()
		if w <= 0 || h <= 0 {
			return
		}
		rows := chromeTopBarRows
		if rows > h {
			rows = h
		}
		// Blank the row first, in the bar's own style, so the active
		// screen's heading cannot show through in the columns the
		// figures line happens to be shorter than (chrome.Render draws
		// only as many cells as its string is long). Without this the
		// bar would read as its own figures followed by a fragment of
		// whatever the screen underneath printed.
		for x := 0; x < w; x++ {
			back.Set(x, 0, ' ', tcell.StyleDefault)
		}
		c.Render(back, core.Rect{X: 0, Y: 0, W: w, H: rows})
	}
}

// screenContentRect is the rect EVERY registered screen draws itself
// into: the whole buffer inset from the top by the rows the global
// chrome owns (FEAT-216 / the lead's BUG-324 ruling — "chrome owns row
// 0, the screens' own content starts at row 1").
//
// One function, used by mapDrawFunc, financeDrawFunc and
// servicesDrawFunc alike (boot.go), so the treatment is uniform by
// construction: a screen cannot quietly opt out of the inset and get
// its heading painted over again, and widening chromeTopBarRows for the
// alert stack moves all three screens together.
//
// Degenerate geometry is clamped here rather than at three call sites.
// A buffer with no room left below the chrome (h <= chromeTopBarRows —
// the 1x1 and 3x1 terminals the destructive round exercises) yields
// H == 0, which every screen's Render treats as "draw nothing" rather
// than as a negative-height rect to index off the end of. Y is still
// clamped into the buffer in that case so the rect can never name a row
// that does not exist.
//
// The BOTTOM row is deliberately NOT reserved here. BUG-322's status
// line overlays row h-1 after the screen has drawn, exactly as it did
// before this fix, so a screen's last content row is still covered on a
// full-height terminal. That is unchanged, pre-existing behaviour from
// the merged tick-driver branch and is outside the ruling this fix
// implements ("the tick driver's status line keeps the bottom row") —
// recorded here, not silently widened into a second inset.
func screenContentRect(back *core.Buffer) core.Rect {
	w, h := back.Size()
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	y := chromeTopBarRows
	if y > h {
		y = h
	}
	return core.Rect{X: 0, Y: y, W: w, H: h - y}
}

// chromeDeltaSink adapts *chromescreen.Chrome onto the two callbacks
// primeScreenSubscription and router.BindSubscription need
// (func(protocol.SubscriptionID) and func(protocol.Delta)).
//
// The adapter exists because Chrome, unlike finance.Screen and
// services.Screen, has no BindSubscription method and its patch entry
// point takes the raw json.RawMessage rather than a protocol.Delta —
// it never imports internal/protocol for delta plumbing (GR#20's
// screens-own-no-transport seam). Rather than grow Chrome's public API
// for one call site, the composition root adapts it here, which is the
// composition root's job.
type chromeDeltaSink struct{ c *chromescreen.Chrome }

// Bind is the no-op BindSubscription this adapter supplies. Chrome does
// not track its own SubscriptionID: it has no outbound command that
// needs to name the subscription (finance/services need theirs to
// correlate command results; chrome's only outbound command is the
// crisis pause, which carries a correlation ID, not a subscription).
// Recorded as a deliberate no-op rather than left implicit.
func (s chromeDeltaSink) Bind(protocol.SubscriptionID) {}

// ApplyDelta hands the delta's patch to Chrome's own
// ApplyFiguresPatch, which validates the schema version and keeps the
// last-known-good figures on a malformed patch (wire.go). Fire and
// forget, matching finance/services' ApplyDelta signature exactly so it
// can be passed straight to primeScreenSubscription.
func (s chromeDeltaSink) ApplyDelta(d protocol.Delta) { s.c.ApplyFiguresPatch(d.Patch) }
