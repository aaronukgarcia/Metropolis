package mapscreen

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Terrain glyphs — the foreground rune drawn for each terrain string the
// "f1.viewport" schema can carry, overridden by an overlay glyph when the
// cell also carries a road or building (cellStyleAndRune below). An
// unrecognised/empty terrain string (a not-yet-known cell, or a future
// terrain kind this package hasn't been taught) falls back to blankGlyph
// rather than guessing.
//
// TWO vocabularies are recognised, deliberately:
//
//   - Folkestone-64's four handcrafted Sprint-1 fixture bands
//     (internal/engine/stub/fixture.go's TerrainKind: shore/shelf/
//     motorway/escarpment). Kept because internal/engine/stub still
//     serves exactly those strings, and this package's fixture-driven
//     tests still assert them.
//   - engine.world's five REAL surface kinds (internal/engine/world's
//     types.go Surface: grass/woodland/water/shingle/rock), added for
//     BUG-323. compose's "f1.viewport" view publishes Surface.String()
//     verbatim, so without these entries every real cell would fall
//     through to blankGlyph and the map would stay blank even with a
//     correctly registered, non-empty view — the same bug wearing a
//     different hat. The mapping lives here, on the CONSUMER side,
//     rather than having the engine re-label its terrain into the older
//     fixture vocabulary: the engine publishes what it holds, and the
//     renderer is taught to read it.
const (
	glyphShore      = '~'
	glyphShelf      = '.'
	glyphMotorway   = '='
	glyphEscarpment = '^'
	blankGlyph      = ' '

	// engine.world Surface glyphs (BUG-323). glyphWater reuses shore's
	// '~' and glyphRock reuses escarpment's '^' — the same ground drawn
	// the same way under the two vocabularies' different names; grass,
	// woodland and shingle get their own distinct runes.
	glyphGrass    = '.'
	glyphWoodland = '%'
	glyphWater    = '~'
	glyphShingle  = ':'
	glyphRock     = '^'

	// Overlay glyphs (AC-4's two-layer contract: these replace only the
	// foreground rune, never the background terrain colour).
	glyphRoad     = '+'
	glyphBuilding = '#'

	// cursorGlyph/staleGlyph are drawn over whatever terrain/overlay glyph
	// would otherwise occupy that cell.
	staleGlyphOn  = '●'
	staleGlyphOff = '○'
)

// terrainToken maps a "f1.viewport" terrain string to the ui.widgets
// semantic Token whose palette colour paints that band's background
// (AC-4: switching terrain never touches the foreground glyph, and vice
// versa — the two data layers are independent). Tokens are chosen for
// visual distinctiveness, not literal semantic accuracy (widgets.Palette
// has no dedicated "terrain" tokens, and inventing new ones is out of
// this item's scope): shore reads as water, shelf as open/grassy
// (money-green), the motorway corridor as a caution amber, and the
// escarpment as the same grey-purple widgets.Heatmap's ramp already uses
// for "high"/rugged terrain-adjacent readings elsewhere.
//
// BUG-323 extends the same reasoning to engine.world's real Surface
// vocabulary: water reads as water, grass and woodland as open/green
// (money-green — the palette has one green; the GLYPH is what separates
// them, exactly as AC-4's two independent layers intend), shingle as a
// caution amber (the same token the motorway band uses — a shoreline
// band, visually distinct from both green and blue), rock as the same
// grey-purple as the escarpment it is the real-vocabulary name for.
func terrainToken(terrain string) (widgets.Token, bool) {
	switch terrain {
	case "shore":
		return widgets.TokenWater, true
	case "shelf":
		return widgets.TokenMoney, true
	case "motorway":
		return widgets.TokenWarning, true
	case "escarpment":
		return widgets.TokenDecay, true
	// engine.world Surface vocabulary (BUG-323).
	case "water":
		return widgets.TokenWater, true
	case "grass", "woodland":
		return widgets.TokenMoney, true
	case "shingle":
		return widgets.TokenWarning, true
	case "rock":
		return widgets.TokenDecay, true
	default:
		return 0, false
	}
}

func terrainGlyph(terrain string) rune {
	switch terrain {
	case "shore":
		return glyphShore
	case "shelf":
		return glyphShelf
	case "motorway":
		return glyphMotorway
	case "escarpment":
		return glyphEscarpment
	// engine.world Surface vocabulary (BUG-323).
	case "grass":
		return glyphGrass
	case "woodland":
		return glyphWoodland
	case "water":
		return glyphWater
	case "shingle":
		return glyphShingle
	case "rock":
		return glyphRock
	default:
		return blankGlyph
	}
}

// minimapHeight is the fixed height, in rows, of the minimap summary
// strip reserved at the bottom of the rect Render is given.
const minimapHeight = 1

// Render draws the current grid/offset/cursor/staleness state into buf
// within rect. It is a pure function of MapScreen's state (AC-10): the
// same state renders identically across repeated calls, and nothing here
// samples the wall clock. Render also records rect's viewport sub-area
// as the screen's current viewport size (SetViewportSize) so subsequent
// Pan/MoveCursor calls clamp against what was actually last drawn.
//
// SEC-020 / render-path rejection (ASM-015): this Render is a
// *MapScreen method and touches mu/grid directly (unlike ui.screen.
// debug's package-level Render func, which takes a Snapshot value and
// never touches *Screen at all — see that package's Collect for the
// equivalent decision). On a struct-copied receiver it draws NOTHING
// and returns — buf is left exactly as the caller passed it in, same as
// the `buf == nil || rect empty` early-out just above. That is a
// deliberate choice among the three a 60Hz render loop could be handed
// (an error it ignores / a blank-this-frame no-op / a panic):
//   - an error return would change this method's signature, forcing
//     every caller (today and future) to add handling for a case that,
//     on the documented construction path (NewMapScreen, one owner), is
//     not supposed to be reachable in production at all;
//   - a panic would take the whole UI process down for what a render
//     loop calls 60 times a second — for a render path specifically,
//     "this frame is unchanged" is a strictly better failure mode than
//     "the process is gone";
//   - so: log (checkNotCopied's errs.New already leaves a
//     registry-sourced MET-U101 trail, GR#7) and draw nothing — the
//     screen freezes on its last real frame rather than corrupting or
//     crashing, and the trail is there for the Destructive agent or an
//     operator to find even though this signature has nothing to return
//     it through (same posture as debug.Screen.LastToggleError's sibling
//     case, IsOn in internal/engine/debug/state.go, and this package's
//     own Collect-equivalent above).
func (m *MapScreen) Render(buf *core.Buffer, rect core.Rect) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Render"}); err != nil {
		return
	}

	viewportRect, minimapRect := splitRect(rect)

	m.mu.Lock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Render"}); err != nil {
		m.mu.Unlock()
		return
	}
	m.viewportW, m.viewportH = viewportRect.W, viewportRect.H
	m.clampOffsetLocked()
	m.clampCursorLocked()
	snap := m.snapshotLocked()
	m.mu.Unlock()

	drawViewport(buf, viewportRect, snap, m.palette)
	// AC-4: the active overlay paints ONLY the background layer, after
	// terrain/road/building have already painted both layers — never the
	// foreground glyph (paintOverlay never touches Rune; see its own doc
	// comment, overlay_data.go). Production always sources values via
	// overlayLiveValue, which reports have=false for every one of AC-3's
	// ten overlays today (overlayBlockedReason) — so this call is a no-op
	// in production until a real overlay lands, by construction rather
	// than a special case.
	paintOverlay(buf, viewportRect, snap, snap.activeOverlay, overlayLiveValue, 0, 1, widgets.DefaultHeatRamp(m.palette))
	if minimapRect.H > 0 {
		drawMinimap(buf, minimapRect, snap, m.palette)
	}
	drawStalenessDot(buf, rect, snap.stale, m.palette)
}

// splitRect divides rect into the main viewport area and the minimap
// strip reserved at its bottom (doc.go's layout description). A rect too
// short to spare a minimap row renders viewport-only.
func splitRect(rect core.Rect) (viewport, minimap core.Rect) {
	if rect.H <= minimapHeight {
		return rect, core.Rect{}
	}
	viewport = core.Rect{X: rect.X, Y: rect.Y, W: rect.W, H: rect.H - minimapHeight}
	minimap = core.Rect{X: rect.X, Y: rect.Y + viewport.H, W: rect.W, H: minimapHeight}
	return viewport, minimap
}

// renderSnapshot is an immutable copy of everything Render needs, taken
// under mu so the actual drawing (drawViewport/drawMinimap/
// drawStalenessDot) can run lock-free — keeping the critical section
// (and therefore any contention with a concurrent ApplyPatch, AC-11)
// small and bounded.
type renderSnapshot struct {
	width, height    int
	grid             []cellData
	offsetX, offsetY int
	viewportW        int
	viewportH        int
	cursorX, cursorY int
	stale            bool
	activeOverlay    Overlay
}

func (m *MapScreen) snapshotLocked() renderSnapshot {
	// grid is copied, not aliased: applySparseLocked mutates elements of
	// m.grid in place under mu, so a concurrent lock-free reader (the
	// draw* functions below, deliberately run outside mu to keep the
	// critical section small) must never hold a reference to the live
	// slice — that would be exactly the kind of data race AC-11's -race
	// check exists to catch.
	//
	// BUG-331 optimization: the grid only changes on ApplyPatch (which calls
	// applyFullLocked/applySparseLocked and sets gridDirty=true). Render is
	// called many times per frame without grid changes, so caching the grid
	// snapshot between changes eliminates the per-frame full-copy at real
	// tile size (40K cells = 2.5MB). The other snapshot fields (offset,
	// cursor, stale, overlay) change frequently and are always recomputed.
	var grid []cellData
	if m.gridDirty {
		grid = make([]cellData, len(m.grid))
		copy(grid, m.grid)
		m.cachedGridSnapshot = grid
		m.gridDirty = false
	} else {
		grid = m.cachedGridSnapshot
	}

	return renderSnapshot{
		width:         m.width,
		height:        m.height,
		grid:          grid,
		offsetX:       m.offsetX,
		offsetY:       m.offsetY,
		viewportW:     m.viewportW,
		viewportH:     m.viewportH,
		cursorX:       m.cursorX,
		cursorY:       m.cursorY,
		stale:         m.stale,
		activeOverlay: overlayOrder[m.overlayIdx],
	}
}

// cellAt returns the grid cell at absolute grid coordinates (x, y), or
// the zero cellData (Known: false) if out of range or not yet known.
func (s renderSnapshot) cellAt(x, y int) cellData {
	if x < 0 || x >= s.width || y < 0 || y >= s.height {
		return cellData{}
	}
	return s.grid[y*s.width+x]
}

// drawViewport paints the visible grid window (snap.offsetX/Y,
// viewportW x viewportH) into rect, then overlays the cursor highlight.
func drawViewport(buf *core.Buffer, rect core.Rect, snap renderSnapshot, palette widgets.Palette) {
	for row := 0; row < rect.H; row++ {
		for col := 0; col < rect.W; col++ {
			gx, gy := snap.offsetX+col, snap.offsetY+row
			c := snap.cellAt(gx, gy)
			style, r := cellStyleAndRune(c, palette)
			buf.Set(rect.X+col, rect.Y+row, r, style)
		}
	}

	// Cursor highlight (AC-5's seam: this is purely visual — Inspect is
	// called independently by whatever later binds Enter).
	if snap.cursorX >= 0 && snap.cursorX < rect.W && snap.cursorY >= 0 && snap.cursorY < rect.H {
		x, y := rect.X+snap.cursorX, rect.Y+snap.cursorY
		existing := buf.Get(x, y)
		buf.Set(x, y, existing.Rune, palette.SelectionStyle(existing.Style))
	}
}

// cellStyleAndRune computes cell c's two-layer style (AC-4): background
// colour from its terrain band, foreground rune from terrain or, if
// present, the road/building overlay glyph (a building takes visual
// priority over a road, since Folkestone-64 never places both on the
// same cell in practice, but a building is the more specific feature
// when it does).
func cellStyleAndRune(c cellData, palette widgets.Palette) (tcell.Style, rune) {
	if !c.Known {
		return tcell.StyleDefault, blankGlyph
	}

	style := tcell.StyleDefault
	if tok, ok := terrainToken(c.Terrain); ok {
		style = style.Background(palette.Color(tok))
	}

	r := terrainGlyph(c.Terrain)
	switch {
	case c.Building != "":
		r = glyphBuilding
	case c.Road != "":
		r = glyphRoad
	}
	return style, r
}

// drawMinimap paints a one-row horizontal strip summarising the full
// grid's X extent into rect (rect.H is always minimapHeight), with the
// segment corresponding to the current viewport's X range drawn in
// reverse video (AC-2: "the minimap's indicator rectangle moves
// correspondingly" as the viewport pans). Y is deliberately not
// represented (a "strip", not a 2D minimap, per this item's brief) —
// each strip cell instead samples the dominant terrain of its column
// band across all Y, giving a recognisable silhouette of the fixture.
func drawMinimap(buf *core.Buffer, rect core.Rect, snap renderSnapshot, palette widgets.Palette) {
	if snap.width <= 0 || snap.height <= 0 {
		for col := 0; col < rect.W; col++ {
			buf.Set(rect.X+col, rect.Y, blankGlyph, tcell.StyleDefault)
		}
		return
	}

	viewStart, viewEnd := minimapViewportSpan(rect.W, snap)

	for col := 0; col < rect.W; col++ {
		gx0, gx1 := minimapColumnRange(col, rect.W, snap.width)
		terrain := dominantTerrain(snap, gx0, gx1)
		style, r := cellStyleAndRune(cellData{Terrain: terrain, Known: terrain != ""}, palette)
		if col >= viewStart && col < viewEnd {
			style = palette.SelectionStyle(style)
		}
		buf.Set(rect.X+col, rect.Y, r, style)
	}
}

// minimapColumnRange maps minimap column col (of stripW columns) to the
// half-open grid-X range [start, end) it summarises.
func minimapColumnRange(col, stripW, gridW int) (start, end int) {
	if stripW <= 0 {
		return 0, 0
	}
	start = col * gridW / stripW
	end = (col + 1) * gridW / stripW
	if end <= start {
		end = start + 1
	}
	return start, end
}

// minimapViewportSpan maps the current viewport's grid-X range to the
// minimap strip's column range, for the reverse-video indicator.
func minimapViewportSpan(stripW int, snap renderSnapshot) (start, end int) {
	if stripW <= 0 || snap.width <= 0 {
		return 0, 0
	}
	start = snap.offsetX * stripW / snap.width
	viewEndX := snap.offsetX + snap.viewportW
	if viewEndX > snap.width {
		viewEndX = snap.width
	}
	end = viewEndX * stripW / snap.width
	if end <= start {
		end = start + 1
	}
	if end > stripW {
		end = stripW
	}
	return start, end
}

// dominantTerrain returns the most common Terrain string across grid
// column range [gx0, gx1) (all Y), or "" if no known cell is found in
// that range. Ties break toward the terrain seen first (row-major scan
// order) — deterministic, never map-iteration-order-dependent.
func dominantTerrain(snap renderSnapshot, gx0, gx1 int) string {
	counts := make(map[string]int)
	order := make([]string, 0, 4)
	best := ""
	bestCount := 0
	for y := 0; y < snap.height; y++ {
		for x := gx0; x < gx1 && x < snap.width; x++ {
			c := snap.cellAt(x, y)
			if !c.Known {
				continue
			}
			if _, seen := counts[c.Terrain]; !seen {
				order = append(order, c.Terrain)
			}
			counts[c.Terrain]++
		}
	}
	for _, t := range order {
		if counts[t] > bestCount {
			best, bestCount = t, counts[t]
		}
	}
	return best
}

// drawStalenessDot paints UI-SPEC §1's status-bar staleness indicator at
// rect's top-right cell — a filled dot (TokenDanger) when the subscribed
// view is stale (SetStale(true)), a hollow one otherwise. Always drawn
// last so it is never overwritten by the viewport/minimap draws above.
func drawStalenessDot(buf *core.Buffer, rect core.Rect, stale bool, palette widgets.Palette) {
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	x, y := rect.X+rect.W-1, rect.Y
	if stale {
		buf.Set(x, y, staleGlyphOn, palette.Style(widgets.TokenDanger))
		return
	}
	buf.Set(x, y, staleGlyphOff, tcell.StyleDefault)
}
