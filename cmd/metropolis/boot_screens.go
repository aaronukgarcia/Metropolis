package main

import (
	"os"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
	buildscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/build"
	debugscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
	menuscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/menu"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// ui.screen.build (F3) / ui.screen.menu (F10) / ui.screen.debug (F12):
// wire-tui-screens increment. These three screens were built and tested
// but unreachable from the binary (the "built but not wired" class).
// Everything here follows the boot.go registration pattern exactly:
// ScreenID constants, screen struct fields, primeScreenSubscription (F3
// only; menu and debug need no engine view), Register, fKeyGlobal on
// w.chromeGrammar, RegisterResultHandler.

const (
	screenIDBuild core.ScreenID = "build"
	screenIDMenu  core.ScreenID = "menu"
	screenIDDebug core.ScreenID = "debug"
)

// saveDirEnv names the environment variable that overrides the directory
// the F10 save browser lists. Default is "saves" relative to the working
// directory (read-only listing; nothing is created).
const saveDirEnv = "METROPOLIS_SAVE_DIR"

// extraScreens holds the three increment screens plus F3's input state.
type extraScreens struct {
	buildCorrID string
	build       *buildscreen.Screen
	buildGrm    *keys.KeyGrammar
	// selected is the F3 catalogue cursor (index into the published
	// catalogue); atomic because input and draw run on different goroutines.
	selected atomic.Int32

	subID atomic.Value // protocol.SubscriptionID learned by the f3.build prime

	menu  *menuscreen.Screen
	debug *debugscreen.Screen
}

// newExtraScreens constructs the three screens. Pure construction, no I/O.
func newExtraScreens(w *skeletonWiring) *extraScreens {
	x := &extraScreens{buildCorrID: errs.NewCorrelationID()}
	x.build = buildscreen.New(x.buildCorrID)
	root := os.Getenv(saveDirEnv)
	if root == "" {
		root = "saves"
	}
	x.menu = menuscreen.New(errs.NewCorrelationID(), menuscreen.WithSaveRoot(root))
	// F12 is debug-gated by design (M0-ENG section 3, debug AC-10): panes
	// draw only when feat.debugmode is on. The flag is the REAL
	// w.debugState, so this screen turns on exactly when debug mode does.
	x.debug = debugscreen.NewScreen(w.registry, errs.NewCorrelationID(),
		// Lazy: w.debugState is assigned later in bootCore than this call.
		debugscreen.WithDebugFlag(func() bool { return w.debugState != nil && w.debugState.IsOn() }))
	return x
}

// registerExtraScreens registers the three screens, F3's key grammar, the
// F3 result handling and the F3/F10/F12 global keys. Called once, right
// after F1/F2/F4 are bound, so w.screens and w.chromeGrammar exist.
func registerExtraScreens(w *skeletonWiring, transport protocol.Transport) error {
	x := w.extra

	// F3 sends: a FRESH CorrelationID per command (router.RegisterResultHandler
	// holds one pending result per ID, and BuildOn reuses the screen's fixed
	// ID), registered BEFORE the send so a rejection reaches ApplyResult.
	sendBuild := func(cmd protocol.Command) error {
		cmd.CorrelationID = protocol.CorrelationID(errs.NewCorrelationID())
		w.router.RegisterResultHandler(cmd.CorrelationID, x.build)
		return transport.SendCommand(cmd)
	}

	x.buildGrm = keys.NewKeyGrammar(nil, 0, 0, w.correlationID)
	step := func(d int32) func(keys.ActionArgs) {
		return func(keys.ActionArgs) {
			cat, _ := x.build.Catalogue()
			if len(cat) == 0 {
				return
			}
			n := int32(len(cat))
			x.selected.Store(((x.selected.Load()+d)%n + n) % n)
		}
	}
	binds := []struct {
		key  keys.Key
		name string
		run  func(keys.ActionArgs)
	}{
		{keys.KeyRune('n'), "Next building (n)", step(+1)},
		{keys.KeyRune('p'), "Previous building (p)", step(-1)},
		{keys.KeyRune('b'), "Queue build at map cursor (b)", func(keys.ActionArgs) {
			cat, _ := x.build.Catalogue()
			if len(cat) == 0 {
				return
			}
			i := int(x.selected.Load())
			if i < 0 || i >= len(cat) {
				i = 0
			}
			cx, cy := w.mapScreen.CursorPos()
			_ = x.build.BuildOn(sendBuild, protocol.CellRef{X: cx, Y: cy}, cat[i].ID)
		}},
	}
	for _, b := range binds {
		if err := x.buildGrm.RegisterGlobal(b.key, keys.Action{Name: b.name, Run: b.run}); err != nil {
			return errs.Wrap(codeBootFailure, w.correlationID, err, map[string]any{"component": "ui.keys.RegisterGlobal", "key": b.key.Token()})
		}
	}

	entries := []core.ScreenEntry{
		{ID: screenIDBuild, Draw: buildDrawFunc(x, func() bool { return w.router.SubscriptionStale(x.buildSubID()) }), Grammar: x.buildGrm},
		{ID: screenIDMenu, Draw: menuDrawFunc(x.menu)},
		{ID: screenIDDebug, Draw: debugDrawFunc(x.debug)},
	}
	for _, e := range entries {
		if err := w.screens.Register(e); err != nil {
			return errs.Wrap(codeBootFailure, w.correlationID, err, map[string]any{"component": "ui.core.ScreenRegistry.Register", "id": string(e.ID)})
		}
	}

	fKey := func(id core.ScreenID, name string, before func()) keys.Action {
		return keys.Action{Name: name, Run: func(keys.ActionArgs) {
			if before != nil {
				before()
			}
			_ = w.screens.Activate(id, w.chromeGrammar)
		}}
	}
	fkeys := []struct {
		key    string
		id     core.ScreenID
		name   string
		before func()
	}{
		{"F3", screenIDBuild, "Switch to Build (F3)", nil},
		// Refresh on activation (a keypress, never in Draw: BUG-602).
		{"F10", screenIDMenu, "Switch to Menu (F10)", func() { _ = x.menu.Refresh() }},
		{"F12", screenIDDebug, "Switch to Debug (F12)", nil},
	}
	for _, f := range fkeys {
		if err := w.chromeGrammar.RegisterGlobal(keys.Key{Special: f.key}, fKey(f.id, f.name, f.before)); err != nil {
			return errs.Wrap(codeBootFailure, w.correlationID, err, map[string]any{"component": "ui.keys.RegisterGlobal", "key": f.key})
		}
	}
	return nil
}

// buildSubID returns the SubscriptionID primeScreenSubscription bound for
// f3.build (zero value when unbound; SubscriptionStale then reports false).
func (x *extraScreens) buildSubID() protocol.SubscriptionID {
	v, _ := x.subID.Load().(protocol.SubscriptionID)
	return v
}

// buildDrawFunc lays out F3: header, zones + catalogue (left), queue
// (right), then the engine's rejection reason (AC-B4) on the last row.
func buildDrawFunc(x *extraScreens, staleFn func() bool) core.DrawFunc {
	style := tcell.StyleDefault
	return func(back *core.Buffer, _ *core.ViewModels) {
		r := screenContentRect(back)
		if r.H <= 0 {
			return
		}
		bs := x.build
		bs.SetStale(staleFn())
		buildscreen.RenderHeader(back, core.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, bs.HaveData(), bs.Stale(), style)

		body := core.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 2}
		col, row := body.W/2, body.H/2
		zones, haveZ := bs.Zones()
		buildscreen.RenderZones(back, core.Rect{X: body.X, Y: body.Y, W: col, H: row}, zones, haveZ, style)
		cat, haveC := bs.Catalogue()
		buildscreen.RenderCatalogue(back, core.Rect{X: body.X, Y: body.Y + row, W: col, H: body.H - row}, cat, haveC, style)
		q, haveQ := bs.Queue()
		buildscreen.RenderQueue(back, core.Rect{X: body.X + col, Y: body.Y, W: body.W - col, H: body.H}, q, haveQ, style)

		last := r.Y + r.H - 1
		line := "keys: n/p select, b build at map cursor"
		if i := int(x.selected.Load()); i >= 0 && i < len(cat) {
			line += "  [selected: " + cat[i].Name + "]"
		}
		if reason := bs.RejectedReason(); reason != "" {
			line = "REJECTED: " + reason
		}
		for i, ch := range line {
			if i >= r.W {
				break
			}
			back.Set(r.X+i, last, ch, style)
		}
	}
}

// menuDrawFunc lists the real save slots found at the save root (the last
// Refresh, run on F10 activation). Never touches disk in Draw.
func menuDrawFunc(ms *menuscreen.Screen) core.DrawFunc {
	style := tcell.StyleDefault
	return func(back *core.Buffer, _ *core.ViewModels) {
		r := screenContentRect(back)
		if r.H <= 0 {
			return
		}
		drawLine(back, r, 0, "F10 Menu & saves", style)
		if reason := ms.SaveListUnavailable(); reason != "" {
			drawLine(back, r, 1, "saves unavailable: "+reason, style)
			return
		}
		entries := ms.SaveEntries()
		if len(entries) == 0 {
			drawLine(back, r, 1, "no saves found", style)
			return
		}
		menuscreen.RenderSaves(back, core.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}, entries, style)
	}
}

// debugDrawFunc draws the F12 header always; the panes follow only when
// debug mode is on (the screen's own AC-10 gate), otherwise a notice says
// why the screen is bare.
func debugDrawFunc(ds *debugscreen.Screen) core.DrawFunc {
	style := tcell.StyleDefault
	return func(back *core.Buffer, _ *core.ViewModels) {
		r := screenContentRect(back)
		if r.H <= 0 {
			return
		}
		drawLine(back, r, 0, "F12 Debug", style)
		// Flag check BEFORE Collect: no clock/memstats reads on the off path (BUG-602).
		if !ds.DebugOn() {
			drawLine(back, r, 1, "debug mode is off (enable it from the dev console)", style)
			return
		}
		snap := ds.Collect()
		debugscreen.Render(back, core.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}, snap, widgets.DefaultPalette)
	}
}

// drawLine writes text on row dy of r, clipped to r's width.
func drawLine(back *core.Buffer, r core.Rect, dy int, text string, style tcell.Style) {
	if dy >= r.H {
		return
	}
	for i, ch := range text {
		if i >= r.W {
			return
		}
		back.Set(r.X+i, r.Y+dy, ch, style)
	}
}
