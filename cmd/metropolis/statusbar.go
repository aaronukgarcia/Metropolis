package main

import (
	"encoding/json"
	"strconv"
	"sync"

	"github.com/gdamore/tcell/v2"

	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// statusBar is BUG-322's "you can SEE that time is moving" half.
//
// The bug's third symptom was that a frozen simulation and a broken binary
// look identical: the engine.status view has been registered by NewEngine
// since FEAT-208 and the subscription pump has been running in this binary
// since then too, but nothing in the process ever subscribed to it, so its
// tick/month/speed/paused figures never reached a screen. bootCore now
// primes a real "engine.status" subscription through the SAME
// primeScreenSubscription handshake finance and services already use, binds
// it into router, and every subsequent delta lands in ApplyDelta below.
//
// # Why a one-line overlay and not ui.screens/chrome
//
// The obvious candidate was to register internal/ui/screens/chrome, whose
// top bar carries date/cycle/speed/money/population/rating. It was rejected
// for this fix, deliberately and narrowly:
//
//   - chrome renders from its OWN view, "chrome.topbar" (chrome.go's
//     ViewChrome), and "chrome.topbar" is NOT in compose's
//     viewRegistrationOrder — only "f4.services", "f2.finance" and
//     engine.core's built-in "engine.status" are registered. A chrome
//     subscription would be rejected by the engine exactly like
//     mapScreen's "f1.viewport" is today, so registering chrome would put a
//     PERMANENTLY EMPTY top bar on screen: the same "looks broken" failure
//     mode, with more machinery behind it.
//   - chrome's figures include money, population and credit rating, which
//     means registering it properly requires a new engine-side view
//     (money/population/rating projections) — a compose.Wire change and its
//     own acceptance criteria, not a P0 clock fix.
//   - BUG-322's brief also asserts chrome already owns a Space-to-pause
//     binding at chrome.go:128-140. It does not: those lines are
//     PauseCommand, a helper that RETURNS the command a Space binding would
//     send. chrome's only registered key is '!' (bind.go's RegisterBang).
//     There was no Space binding anywhere in the tree to reuse, so this fix
//     binds Space itself — see bootCore's registerClockKeys.
//
// So the honest scope line: wiring chrome up for real is out of scope and
// stays open (it needs its view registered engine-side first). What this
// does instead is put the ALREADY-REGISTERED engine.status figures on
// screen, on one line, on every screen, which is what "the player can see
// that time is moving" actually requires.
type statusBar struct {
	mu   sync.Mutex
	have bool
	view enginecore.EngineStatusView
}

func newStatusBar() *statusBar { return &statusBar{} }

// ApplyDelta records the latest engine.status patch. Signature and
// fire-and-forget error posture match finance/services' own ApplyDelta, so
// it can be handed straight to primeScreenSubscription and to
// router.BindSubscription.
//
// A malformed patch leaves the previous snapshot in place rather than
// blanking the bar: this is a display surface, and showing the last known
// good figures is strictly better than showing nothing while the engine
// keeps running. It cannot be silent-failure-by-omission (GR#17) because the
// bar's own staleness is visible — a Tick that stops advancing on screen is
// exactly the symptom this whole item exists to make observable.
func (s *statusBar) ApplyDelta(d protocol.Delta) {
	var v enginecore.EngineStatusView
	if err := json.Unmarshal(d.Patch, &v); err != nil {
		return
	}
	s.mu.Lock()
	s.view = v
	s.have = true
	s.mu.Unlock()
}

// Snapshot returns the last applied engine.status view and whether one has
// ever arrived.
func (s *statusBar) Snapshot() (enginecore.EngineStatusView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view, s.have
}

// Line renders the status text. Kept separate from Draw so a test can assert
// the exact string without a Buffer.
func (s *statusBar) Line() string {
	v, ok := s.Snapshot()
	if !ok {
		return "TICK -  MONTH -  SPEED -  (waiting for engine.status)  " + statusBarKeyHelp
	}
	state := "RUNNING"
	if v.Paused {
		state = "PAUSED"
	}
	return "TICK " + strconv.FormatInt(v.Tick, 10) +
		"  MONTH " + strconv.FormatInt(v.Month, 10) +
		"  SPEED " + strconv.Itoa(v.Speed) + "x" +
		"  " + state +
		"  " + statusBarKeyHelp
}

// statusBarKeyHelp names the clock keys registerClockKeys binds. Written
// once, here, so the on-screen prompt can never drift from what is actually
// bound (GR#3) — bootCore's registration and this string are asserted
// against each other in the tests.
const statusBarKeyHelp = "[Space] pause/resume  []] faster  [[] slower  [F1/F2/F4] screens  [q] quit"

// statusBarDraw returns a core.DrawFunc that renders the status line on the
// LAST row of the buffer, on top of whatever the active screen drew.
//
// Bottom row rather than top: every existing screen (map/finance/services)
// draws its own headings at y=0, and overwriting a heading would trade one
// missing signal for another. It is drawn AFTER the active screen's own
// DrawFunc (run.go composes them in that order), so it always wins the last
// row regardless of what the screen put there.
//
// Contract-clean: this only writes to back, which is core.DrawFunc's one
// requirement (render.go — "Draw callbacks must only write to back").
func statusBarDraw(s *statusBar) core.DrawFunc {
	style := tcell.StyleDefault.Reverse(true)
	return func(back *core.Buffer, _ *core.ViewModels) {
		w, h := back.Size()
		if w <= 0 || h <= 0 {
			return
		}
		y := h - 1
		line := s.Line()
		x := 0
		for _, r := range line {
			if x >= w {
				break
			}
			back.Set(x, y, r, style)
			x++
		}
		for ; x < w; x++ {
			back.Set(x, y, ' ', style)
		}
	}
}
