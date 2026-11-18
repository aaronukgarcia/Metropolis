package main

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// BUG-1220 Part B: every screen names its own action keys on a hint row.
//
// The hint row sits directly above the status bar (the bottom row). screens
// are inset from the bottom by chromeHintRows so the row never covers screen
// content (F3's own "keys:" line used to sit under the status bar and was
// never visible). Each hint lists only keys that really work: the tests feed
// every advertised key into the live grammars.
const chromeHintRows = 1

// screenHintMap, screenHintBuild... are the hint texts, one per live screen.
const (
	screenHintMap      = "F1 MAP  arrows move cursor | y buy cell | b build dwelling here | c clear notice"
	screenHintFinance  = "F2 FINANCE  view only, no action keys | F1: y buy cell | F3: b queue build"
	screenHintBuild    = "F3 BUILD  n/p choose | b queue at the F1 map cursor (move it on F1)"
	screenHintServices = "F4 SERVICES  view only, no action keys yet (no service is live in baseline one)"
	screenHintMenu     = "F10 MENU  saves list, view only"
	screenHintDebug    = "F12 DEBUG  view only"
)

// screenHint returns the hint row text for the active screen ("" for an
// unknown id).
func screenHint(id core.ScreenID) string {
	switch id {
	case screenIDMap:
		return screenHintMap
	case screenIDFinance:
		return screenHintFinance
	case screenIDBuild:
		return screenHintBuild
	case screenIDServices:
		return screenHintServices
	case screenIDMenu:
		return screenHintMenu
	case screenIDDebug:
		return screenHintDebug
	}
	return ""
}

// hintBarDraw draws the active screen's hint on row h-2 (just above the status
// bar). Terminals too short to inset the screens (see screenContentRect) get
// no hint row, so nothing is painted over their content.
func hintBarDraw(w *skeletonWiring) core.DrawFunc {
	style := tcell.StyleDefault.Dim(true)
	return func(back *core.Buffer, _ *core.ViewModels) {
		cw, ch := back.Size()
		if cw <= 0 || !hintRoom(ch) {
			return
		}
		y := ch - 1 - chromeHintRows
		for x := 0; x < cw; x++ {
			back.Set(x, y, ' ', style)
		}
		x := 0
		for _, r := range screenHint(w.screens.ActiveID()) {
			if x >= cw {
				break
			}
			back.Set(x, y, r, style)
			x++
		}
	}
}

// hintRoom reports whether a buffer of height h leaves room for the hint row
// without eating the screens' content (the same threshold screenContentRect
// uses to reserve it).
func hintRoom(h int) bool {
	return h-chromeReservedRows-1-chromeHintRows >= 3
}
