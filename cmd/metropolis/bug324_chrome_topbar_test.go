package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	chromescreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/chrome"
)

// BUG-324's composition-root proof set.
//
// The lesson of this bug family, made mechanical: a test that asserts
// "the view is registered" or "the subscription was accepted" would
// have passed on every broken version of this binary. So every
// assertion below is on RENDERED CONTENT — the actual glyphs in row 0
// of a real buffer, drawn through the real composeDraw, over a real
// bootCore. The empty-bar regression is the specific thing being
// guarded against, not registration.

// bootForChromeTest boots the real composition root and returns it.
func bootForChromeTest(t *testing.T) *skeletonWiring {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("bug324-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	return w
}

// renderAt draws the full composed frame (active screen + global
// chrome) into a fresh w x h buffer, exactly as runInteractive's
// RenderLoop does.
func renderAt(w *skeletonWiring, cols, rows int) *core.Buffer {
	back := core.NewBuffer(cols, rows)
	composeDraw(w)(back, &core.ViewModels{})
	return back
}

// row returns one row of a buffer as a string, trailing blanks kept
// (callers TrimRight when they want the text).
func row(b *core.Buffer, y int) string {
	cols, _ := b.Size()
	var sb strings.Builder
	for x := 0; x < cols; x++ {
		c := b.Get(x, y)
		if c.Rune == 0 {
			sb.WriteRune(' ')
			continue
		}
		sb.WriteRune(c.Rune)
	}
	return sb.String()
}

// freezeClock stops the simulation so that two frames drawn a moment
// apart are byte-comparable.
//
// Necessary since BUG-322's tick driver merged: bootCore starts it, and
// its FIRST act is a KindResume, so a freshly booted binary is a MOVING
// picture — the bottom status line legitimately changes between two
// draws (this test caught exactly that: "PAUSED" in the first frame,
// "RUNNING" in the second, with no chrome involved). Pausing removes the
// only live input rather than weakening the comparison to "rows the
// clock does not touch".
func freezeClock(t *testing.T, w *skeletonWiring) {
	t.Helper()

	// Wait for the driver's OWN boot-time Resume to land first. It is
	// issued from the driver's goroutine, so a Pause sent before it wins
	// the race is simply undone a moment later — which is exactly how
	// this helper failed when it was written the naive way (the world
	// came back RUNNING and kept ticking).
	awaitStatus(t, w, "RUNNING")

	res := sendAndAwaitResult(t, w, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("bug324-freeze"),
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	})
	if !res.Accepted {
		t.Fatalf("Pause rejected: %+v", res.Error)
	}
	// The status delta lands on the pump/router goroutines, so wait for
	// the bar itself to report the pause rather than assuming it has.
	awaitStatus(t, w, "PAUSED")
}

// awaitStatus blocks until the live status line contains want, or fails
// the test. Polling rather than assuming: every status figure arrives
// asynchronously through the pump and router goroutines.
func awaitStatus(t *testing.T, w *skeletonWiring, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.statusBar.Line(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the status bar never reported %q within 5s; last line: %q", want, w.statusBar.Line())
}

// TestBUG324_ChromeIsConstructedAndSubscribed is the cheap wiring
// check. It is deliberately NOT the proof — see the file header — but a
// nil chromeUI would make every content assertion below fail with a
// confusing message rather than a clear one.
func TestBUG324_ChromeIsConstructedAndSubscribed(t *testing.T) {
	w := bootForChromeTest(t)
	if w.chromeUI == nil {
		t.Fatal("bootCore did not construct the global chrome (w.chromeUI is nil) — the game has no top bar")
	}
	if got := w.chromeUI.Figures(); got == (chromescreen.Figures{}) {
		t.Fatalf("chrome's figures are the ZERO value immediately after bootCore — the chrome.topbar subscription delivered nothing, so the bar would render blank. Figures: %+v", got)
	}
}

// TestBUG324_TopRowCarriesRealFigures is the core proof: the top row of
// a real 100x24 frame contains the real figures, at boot, before any
// tick.
//
// Every substring asserted here is a label or a value that can only
// appear if a live chrome.topbar delta actually reached the screen —
// the zero Figures would render "... | cycle 0/30 | money 0 |
// pop 0 | rating " (note the empty date and empty rating), so the
// date/rating/population assertions are the ones that cannot be
// satisfied by an empty bar.
func TestBUG324_TopRowCarriesRealFigures(t *testing.T) {
	w := bootForChromeTest(t)
	back := renderAt(w, 100, 24)
	top := strings.TrimRight(row(back, 0), " ")

	if top == "" {
		t.Fatal("row 0 of the rendered frame is entirely blank — this is the empty-top-bar regression BUG-324 exists to prevent")
	}

	fig := w.chromeUI.Figures()
	for _, want := range []string{
		fig.Date,   // e.g. "Jan Y1" — empty on a blank bar
		"cycle ",   // the labelled clock cycle
		"money ",   // the labelled money
		"pop ",     // the labelled population
		fig.Rating, // e.g. "1000/1000" — empty on a blank bar
		"pop " + strconv.FormatInt(fig.Population, 10), // the REAL live citizen count
		"money " + strconv.FormatInt(fig.Money, 10),    // the REAL live treasury, whole pounds
	} {
		if want == "" {
			t.Errorf("expected a non-empty figure to assert on, got empty (figures: %+v)", fig)
			continue
		}
		if !strings.Contains(top, want) {
			t.Errorf("top row does not contain %q\n  top row: %q\n  figures: %+v", want, top, fig)
		}
	}

	// Population and money must be REAL, not plausible-looking zeros:
	// the composition seeds 64 citizens and a 10-pound treasury before
	// the engine ever ticks.
	if fig.Population <= 0 {
		t.Errorf("Figures.Population = %d, want the live seeded citizen count (>0)", fig.Population)
	}
	if fig.Money <= 0 {
		t.Errorf("Figures.Money = %d, want the live treasury in whole pounds (>0)", fig.Money)
	}
}

// TestFEAT216_SpeedIsOnTheBottomBarOnly is the lead's ruling made
// mechanical: the two persistent bars split by subject, so the SPEED
// figure appears on the machine-state bar (bottom) and nowhere else.
// Before the ruling both bars printed it, in two disagreeing formats
// ("speed 1" against "SPEED 1x RUNNING") — one fact, two spellings.
//
// The top-row assertion is a substring check on the rendered glyphs, not
// on the Figures value: the field is deliberately still populated (the
// publisher still sources a real multiplier, so Figures() stays
// truthful) — what must be absent is the PRINTED figure.
func TestFEAT216_SpeedIsOnTheBottomBarOnly(t *testing.T) {
	w := bootForChromeTest(t)
	back := renderAt(w, 100, 24)

	top := strings.ToLower(strings.TrimRight(row(back, 0), " "))
	if strings.Contains(top, "speed") {
		t.Errorf("the top bar still prints speed — FEAT-216 puts machine state on the bottom bar only\n  top row: %q", row(back, 0))
	}

	bottom := row(back, 23)
	if !strings.Contains(bottom, "SPEED ") {
		t.Errorf("the bottom bar lost its SPEED figure — FEAT-216 moves speed here, it does not delete it\n  bottom row: %q", bottom)
	}
}

// TestBUG324_TopBarOverlaysTheActiveScreen proves chrome is GLOBAL
// chrome (Aaron's F9 ruling) rather than a screen of its own: whatever
// the active screen drew at row 0, the bar wins that row, and the
// screen's own body below row 0 is left alone.
//
// The baseline is deliberately screen + status line, NOT the screen
// alone: composeDraw carries two overlays since BUG-322's tick driver
// merged, and comparing against a status-less frame would attribute the
// bottom row's difference to chrome. This baseline isolates chrome's
// contribution to exactly one row.
func TestBUG324_TopBarOverlaysTheActiveScreen(t *testing.T) {
	w := bootForChromeTest(t)
	freezeClock(t, w)

	// The active screen + the bottom status line, but NO chrome.
	withoutChrome := core.NewBuffer(100, 24)
	w.screens.ActiveDraw()(withoutChrome, &core.ViewModels{})
	statusBarDraw(w.statusBar)(withoutChrome, &core.ViewModels{})

	composed := renderAt(w, 100, 24)

	if row(withoutChrome, 0) == row(composed, 0) {
		t.Errorf("row 0 is identical with and without the chrome overlay — the bar is not being drawn on top\n  got: %q", row(composed, 0))
	}
	// Rows 1..23 must be byte-identical: chrome is given exactly one row
	// (chromeTopBarRows), so it must not paint the screen's body and it
	// must not disturb the status line either.
	for y := 1; y < 24; y++ {
		if row(withoutChrome, y) != row(composed, y) {
			t.Fatalf("chrome overlay modified row %d, which does not belong to it\n  without chrome: %q\n  composed:       %q", y, row(withoutChrome, y), row(composed, y))
		}
	}
}

// TestBUG324_ScreensAreInsetSoNoHeadingIsLost is the lead's ruling on
// this fix's independent round, made mechanical: chrome owns row 0, so
// every registered screen's own content now starts at row 1 and no
// screen loses its identifying heading to the bar.
//
// The round's finding was concrete — on F2 and F4 the heading is the
// ONLY identifying line, so a bar over row 0 left the player looking at
// vital signs above the single word "unavailable". So the assertion is
// on rendered glyphs, per screen: row 0 of the SCREEN's own output must
// be blank (nothing to lose), and the text that was previously at row 0
// must be present in the composed frame at row 1.
//
// It is table-driven over ALL THREE registered screens on purpose: a
// per-screen exception is exactly how a uniform treatment drifts back
// apart, and this test fails the moment one draw func stops going
// through screenContentRect.
func TestBUG324_ScreensAreInsetSoNoHeadingIsLost(t *testing.T) {
	w := bootForChromeTest(t)
	freezeClock(t, w)

	for _, id := range []core.ScreenID{screenIDMap, screenIDFinance, screenIDServices} {
		if err := w.screens.Activate(id, nil); err != nil {
			t.Fatalf("Activate(%s): %v", id, err)
		}

		screenOnly := core.NewBuffer(100, 24)
		w.screens.ActiveDraw()(screenOnly, &core.ViewModels{})

		if got := strings.TrimSpace(row(screenOnly, 0)); got != "" {
			t.Errorf("screen %q still draws on row 0 (%q) — that row belongs to the global chrome, so this content would be painted over", id, got)
		}

		firstRow := strings.TrimRight(row(screenOnly, 1), " ")
		if strings.TrimSpace(firstRow) == "" {
			t.Errorf("screen %q draws nothing on row 1 — the inset moved its content down but the first content row is empty, so the screen has no visible heading at all", id)
			continue
		}

		composed := renderAt(w, 100, 24)
		if got := strings.TrimRight(row(composed, 1), " "); got != firstRow {
			t.Errorf("screen %q lost its first content row in the composed frame\n  screen-only row 1: %q\n  composed row 1:    %q", id, firstRow, got)
		}
	}
}

// TestBUG324_TopBarSurvivesAScreenSwitch proves the bar is not tied to
// the initially-active screen: it is still there after switching to
// finance and to services. A per-screen bar would be exactly the
// not-global-chrome mistake.
func TestBUG324_TopBarSurvivesAScreenSwitch(t *testing.T) {
	w := bootForChromeTest(t)
	fig := w.chromeUI.Figures()

	for _, id := range []core.ScreenID{screenIDFinance, screenIDServices, screenIDMap} {
		if err := w.screens.Activate(id, nil); err != nil {
			t.Fatalf("Activate(%s): %v", id, err)
		}
		top := strings.TrimRight(row(renderAt(w, 100, 24), 0), " ")
		if !strings.Contains(top, fig.Date) || !strings.Contains(top, "pop "+strconv.FormatInt(fig.Population, 10)) {
			t.Errorf("with %q active, the top bar lost its figures\n  top row: %q", id, top)
		}
	}
}

// TestBUG324_TopBarIsLive_CycleAdvancesWithTheClock proves the bar is a
// LIVE subscription, not a one-shot snapshot taken at boot: advancing
// the engine's clock changes what row 0 renders. A bar that only ever
// showed its priming delta would pass every other test in this file and
// still be frozen on screen.
func TestBUG324_TopBarIsLive_CycleAdvancesWithTheClock(t *testing.T) {
	w := bootForChromeTest(t)
	// Freeze first: the real-time tick driver is running (BUG-322), so
	// the explicit AdvanceTicks below must be the ONLY thing that moves
	// the clock, or the after-assertion is not attributable to it.
	//
	// The baseline is READ, not assumed to be genesis: by the time the
	// driver's boot Resume has landed and the Pause has taken, the world
	// may legitimately be a few ticks in. Asserting "cycle 0/30" here
	// would have been a clock-speed-dependent test.
	freezeClock(t, w)

	before := strings.TrimRight(row(renderAt(w, 100, 24), 0), " ")
	startCycle := w.chromeUI.Figures().ClockCycle
	wantCycle := "cycle " + strconv.Itoa(startCycle+3) + "/30"
	if startCycle+3 >= 30 {
		t.Skipf("the frozen baseline is cycle %d/30, too close to the month boundary for a +3 advance to stay in the same month", startCycle)
	}

	res := sendAndAwaitResult(t, w, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("bug324-live"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 3},
	})
	if !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}

	// The delta arrives on the pump/router goroutines, so poll rather
	// than assume it has landed by the time the result came back.
	deadline := time.Now().Add(3 * time.Second)
	var after string
	for time.Now().Before(deadline) {
		after = strings.TrimRight(row(renderAt(w, 100, 24), 0), " ")
		if strings.Contains(after, wantCycle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("top row never advanced to %q after AdvanceTicks{N:3} — the bar is not live\n  before: %q\n  after:  %q", wantCycle, before, after)
}

// TestBUG324_DegenerateGeometryDoesNotPanic re-verifies the independent
// round's own geometry attacks against the composed frame, now that the
// screens are inset: a terminal with no room for the chrome row, no room
// for anything at all, or a zero dimension must degrade to drawing
// nothing rather than panicking or indexing off the end of a buffer.
//
// 1x1 and 3x1 are the interesting ones — they are exactly the sizes at
// which the inset's remaining height goes to zero (h - chromeTopBarRows
// == 0), and where both overlays target the same single row.
func TestBUG324_DegenerateGeometryDoesNotPanic(t *testing.T) {
	w := bootForChromeTest(t)

	for _, size := range []struct{ cols, rows int }{
		{0, 0}, {1, 1}, {3, 1}, {100, 0}, {0, 24}, {1, 2},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("composeDraw panicked at %dx%d: %v", size.cols, size.rows, r)
				}
			}()
			back := core.NewBuffer(size.cols, size.rows)
			composeDraw(w)(back, &core.ViewModels{})
		}()
	}

	// The inset itself must never hand a screen a negative-height rect,
	// whatever the geometry.
	for _, size := range []struct{ cols, rows int }{{0, 0}, {1, 1}, {3, 1}, {100, 0}} {
		r := screenContentRect(core.NewBuffer(size.cols, size.rows))
		if r.H < 0 || r.W < 0 || r.Y < 0 {
			t.Errorf("screenContentRect(%dx%d) = %+v, want no negative dimension", size.cols, size.rows, r)
		}
		if r.Y+r.H > size.rows {
			t.Errorf("screenContentRect(%dx%d) = %+v, extends past the buffer's %d rows", size.cols, size.rows, r, size.rows)
		}
	}
}

// TestBUG324_MalformedPatchKeepsLastKnownGood proves the display
// degrades to the last good figures rather than blanking when a patch
// arrives that does not decode — the round's injection attacks, run
// through the REAL delta sink this binary wires (chromeDeltaSink), not
// through chrome's decoder directly.
func TestBUG324_MalformedPatchKeepsLastKnownGood(t *testing.T) {
	w := bootForChromeTest(t)
	freezeClock(t, w)

	good := w.chromeUI.Figures()
	if good == (chromescreen.Figures{}) {
		t.Fatal("no good figures to defend — the priming delta never landed")
	}

	sink := chromeDeltaSink{c: w.chromeUI}
	for _, patch := range []string{
		``,
		`{`,
		`null`,
		`[]`,
		`"not an object"`,
		`{"schemaVersion":999,"figures":{"date":"HACKED","clockCycle":0,"speed":0,"money":0,"population":0,"rating":""}}`,
		`{"figures":{"date":"HACKED"}}`,
		`{"schemaVersion":1,"figures":"HACKED"}`,
		// BUG-324 round-2 (attacker finding): structurally-valid patches with
		// EMPTY or PARTIAL figures content — the pre-r2 decoder accepted
		// these and replaced the whole Figures value, blanking the bar to
		// plausible zeros. The content guard (wire.go decodeFiguresPatch)
		// must keep the last-known-good figures for each of these too.
		`{"schemaVersion":1,"figures":{}}`,
		`{"schemaVersion":1}`,
		`{"schemaVersion":1,"figures":null}`,
		`{"schemaVersion":1,"figures":{"date":"HACKED"}}`,
		`{"schemaVersion":1,"figures":{"money":999,"population":42}}`,
	} {
		sink.ApplyDelta(protocol.Delta{Patch: []byte(patch)})
		if got := w.chromeUI.Figures(); got != good {
			t.Errorf("patch %q changed the figures\n  before: %+v\n  after:  %+v", patch, good, got)
		}
		top := strings.TrimRight(row(renderAt(w, 100, 24), 0), " ")
		if strings.Contains(top, "HACKED") {
			t.Errorf("patch %q reached the rendered bar: %q", patch, top)
		}
		if strings.TrimSpace(top) == "" {
			t.Errorf("patch %q blanked the bar — a malformed patch must leave the last known good figures on screen", patch)
		}
	}
}

// TestBUG324_RenderTheFrame prints the real 100x24 frame so a human can
// read the bar exactly as a player would. It asserts nothing beyond the
// bar being non-blank — the assertions live in the tests above; this
// one exists so the visual evidence is reproducible on demand
// (`go test ./cmd/metropolis -run TestBUG324_RenderTheFrame -v`) rather
// than being a screenshot pasted into a report and never checkable
// again.
func TestBUG324_RenderTheFrame(t *testing.T) {
	w := bootForChromeTest(t)
	freezeClock(t, w)

	// Every registered screen, so the capture shows the inset doing its
	// job on all three rather than only on whichever boots active.
	for _, id := range []core.ScreenID{screenIDMap, screenIDFinance, screenIDServices} {
		if err := w.screens.Activate(id, nil); err != nil {
			t.Fatalf("Activate(%s): %v", id, err)
		}
		back := renderAt(w, 100, 24)

		var sb strings.Builder
		sb.WriteString("\n" + string(id) + ":\n+" + strings.Repeat("-", 100) + "+\n")
		for y := 0; y < 24; y++ {
			sb.WriteString("|" + row(back, y) + "|\n")
		}
		sb.WriteString("+" + strings.Repeat("-", 100) + "+\n")
		t.Log(sb.String())

		if strings.TrimSpace(row(back, 0)) == "" {
			t.Fatalf("row 0 is blank with %q active", id)
		}
	}
}
