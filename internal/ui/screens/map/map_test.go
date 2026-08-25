package mapscreen_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/engine/stub"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// --- fixture helpers (test-only stub import, per this package's doc.go) ---

// fullPatchJSON builds a "f1.viewport" v1 full-snapshot patch, covering
// every cell of w, using only stub's exported types — the sanctioned
// test-only way to generate fixture JSON without duplicating
// StubEngine's unexported patch-building internals.
func fullPatchJSON(t *testing.T, w *stub.World) json.RawMessage {
	t.Helper()
	cells := make([]stub.ViewportCell, 0, w.Width*w.Height)
	for y := 0; y < w.Height; y++ {
		for x := 0; x < w.Width; x++ {
			c := w.Cells[y][x]
			cells = append(cells, stub.ViewportCell{
				X: c.X, Y: c.Y,
				Terrain: string(c.Terrain), Elevation: c.Elevation,
				Road: c.Road, Building: c.Building,
			})
		}
	}
	return marshalPatch(t, stub.ViewportPatch{
		SchemaVersion: 1,
		Full:          true,
		Origin:        stub.Point{X: 0, Y: 0},
		Extent:        stub.Extent{Width: w.Width, Height: w.Height},
		Cells:         cells,
	})
}

// sparsePatchJSON builds a "f1.viewport" v1 sparse patch carrying only
// cells.
func sparsePatchJSON(t *testing.T, cells []stub.ViewportCell) json.RawMessage {
	t.Helper()
	return marshalPatch(t, stub.ViewportPatch{
		SchemaVersion: 1,
		Full:          false,
		Origin:        stub.Point{X: 0, Y: 0},
		Extent:        stub.Extent{Width: stub.FixtureWidth, Height: stub.FixtureHeight},
		Cells:         cells,
	})
}

func marshalPatch(t *testing.T, p stub.ViewportPatch) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return raw
}

func newTestScreen(t *testing.T) *mapscreen.MapScreen {
	t.Helper()
	return mapscreen.NewMapScreen("test-correlation", widgets.DefaultPalette)
}

// --- AC-1/AC-2/AC-4: full snapshot renders terrain, roads, buildings ---

func TestApplyPatch_FullSnapshot_RendersKnownCells(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))

	buf := core.NewBuffer(20, 20)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 20, H: 20})

	waterColor := widgets.DefaultPalette.Color(widgets.TokenWater)

	// Plain shore terrain (y=0 is shore band, no road/building at x=10).
	got := buf.Get(10, 0)
	wantStyle := tcell.StyleDefault.Background(waterColor)
	if got.Rune != '~' || got.Style != wantStyle {
		t.Fatalf("plain shore cell (10,0) = %+v, want rune '~' style %+v", got, wantStyle)
	}

	// Building overlay glyph: Folkestone Harbour Arm at (5,3), still shore band.
	got = buf.Get(5, 3)
	if got.Rune != '#' || got.Style != wantStyle {
		t.Fatalf("building cell (5,3) = %+v, want rune '#' style %+v", got, wantStyle)
	}

	// Road overlay glyph: Sandgate Road at y=5, x in [8,42], still shore band.
	got = buf.Get(8, 5)
	if got.Rune != '+' || got.Style != wantStyle {
		t.Fatalf("road cell (8,5) = %+v, want rune '+' style %+v", got, wantStyle)
	}
}

// AC-2/AC-6 (sparse-update correctness): applying a sparse patch changes
// only the cell(s) it lists.
func TestApplyPatch_SparseUpdate_OnlyChangedCellChanges(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))

	rect := core.Rect{X: 0, Y: 0, W: 40, H: 40}
	before := core.NewBuffer(40, 40)
	m.Render(before, rect)

	// (30,30) is motorway band (28<=y<36); flip it to escarpment with a
	// different elevation so the change is visually detectable.
	m.ApplyPatch(sparsePatchJSON(t, []stub.ViewportCell{
		{X: 30, Y: 30, Terrain: "escarpment", Elevation: 99},
	}))

	after := core.NewBuffer(40, 40)
	m.Render(after, rect)

	diffs := 0
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			b, a := before.Get(x, y), after.Get(x, y)
			if b != a {
				diffs++
				if x != 30 || y != 30 {
					t.Errorf("unexpected diff at (%d,%d): before=%+v after=%+v", x, y, b, a)
				}
			}
		}
	}
	if diffs != 1 {
		t.Fatalf("got %d changed cells, want exactly 1 (the patched cell)", diffs)
	}
}

// AC-2: panning changes the rendered viewport origin, clamped at fixture edges.
func TestPan_ClampsAtFixtureEdges(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))
	m.SetViewportSize(10, 8)

	m.Pan(1000, 1000)
	if x, y := m.Offset(); x != w.Width-10 || y != w.Height-8 {
		t.Fatalf("Offset() after huge positive pan = (%d,%d), want (%d,%d)", x, y, w.Width-10, w.Height-8)
	}

	m.Pan(-5000, -5000)
	if x, y := m.Offset(); x != 0 || y != 0 {
		t.Fatalf("Offset() after huge negative pan = (%d,%d), want (0,0)", x, y)
	}

	m.Pan(3, 2)
	if x, y := m.Offset(); x != 3 || y != 2 {
		t.Fatalf("Offset() after in-bounds pan = (%d,%d), want (3,2)", x, y)
	}
}

func TestMoveCursor_ClampsToViewport(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))
	m.SetViewportSize(5, 5)

	m.MoveCursor(100, 100)
	x, y := m.CursorPos()
	if x != 4 || y != 4 {
		t.Fatalf("CursorPos() after huge positive move = (%d,%d), want (4,4)", x, y)
	}

	m.MoveCursor(-100, -100)
	x, y = m.CursorPos()
	if x != 0 || y != 0 {
		t.Fatalf("CursorPos() after huge negative move = (%d,%d), want (0,0)", x, y)
	}
}

// AC-5: Inspect returns a known fixture cell's attributes.
func TestInspect_KnownFixtureCell(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))

	res := m.Inspect(5, 3)
	if !res.Found {
		t.Fatal("Inspect(5,3) Found = false, want true")
	}
	if res.Building != "Folkestone Harbour Arm" {
		t.Fatalf("Inspect(5,3).Building = %q, want %q", res.Building, "Folkestone Harbour Arm")
	}
	if res.Terrain != "shore" {
		t.Fatalf("Inspect(5,3).Terrain = %q, want %q", res.Terrain, "shore")
	}
}

// AC-9: a cell outside the known snapshot (never covered, e.g. before
// any full snapshot) surfaces Found=false rather than stale/corrupted data.
func TestInspect_UnavailableCell(t *testing.T) {
	m := newTestScreen(t) // no ApplyPatch call at all
	res := m.Inspect(0, 0)
	if res.Found {
		t.Fatalf("Inspect(0,0) on an empty screen: Found = true, want false: %+v", res)
	}

	w := stub.GenerateFolkestone64()
	m.ApplyPatch(fullPatchJSON(t, w))
	res = m.Inspect(1000, 1000)
	if res.Found {
		t.Fatalf("Inspect(1000,1000) out of range: Found = true, want false: %+v", res)
	}
}

// AC-8/AC-9 posture: malformed patches are dropped, logged, never crash,
// and never partially apply.
func TestApplyPatch_MalformedJSON_LoggedSkipNoCrash(t *testing.T) {
	m := newTestScreen(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ApplyPatch panicked on malformed JSON: %v", r)
		}
	}()
	m.ApplyPatch(json.RawMessage(`{not valid json`))

	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("state changed after malformed patch: %+v", res)
	}
}

func TestApplyPatch_UnsupportedSchemaVersion_LoggedSkip(t *testing.T) {
	m := newTestScreen(t)
	raw := marshalPatch(t, stub.ViewportPatch{SchemaVersion: 2, Full: true, Extent: stub.Extent{Width: 4, Height: 4}})
	m.ApplyPatch(raw)

	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("state changed after unsupported-schemaVersion patch: %+v", res)
	}
}

func TestApplyPatch_SparseBeforeSnapshot_LoggedSkip(t *testing.T) {
	m := newTestScreen(t)
	m.ApplyPatch(sparsePatchJSON(t, []stub.ViewportCell{{X: 0, Y: 0, Terrain: "shore"}}))

	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("state changed after sparse-before-snapshot patch: %+v", res)
	}

	// A subsequent full snapshot must still apply cleanly (dropping the
	// bad patch must not have corrupted anything).
	w := stub.GenerateFolkestone64()
	m.ApplyPatch(fullPatchJSON(t, w))
	if res := m.Inspect(0, 0); !res.Found {
		t.Fatalf("full snapshot after a dropped sparse patch failed to apply: %+v", res)
	}
}

// AC-1: Subscribe sends a well-formed Subscribe command for "f1.viewport"
// via the caller-provided sender callback, never touching a transport
// itself.
func TestSubscribe_SendsWellFormedSubscribeCommand(t *testing.T) {
	m := newTestScreen(t)

	var got protocol.Command
	err := m.Subscribe(func(cmd protocol.Command) error {
		got = cmd
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if got.Kind != protocol.KindSubscribe {
		t.Fatalf("Kind = %q, want %q", got.Kind, protocol.KindSubscribe)
	}
	payload, ok := got.Payload.(protocol.SubscribePayload)
	if !ok {
		t.Fatalf("Payload type = %T, want protocol.SubscribePayload", got.Payload)
	}
	if payload.ViewName != mapscreen.ViewSubscriptionName {
		t.Fatalf("ViewName = %q, want %q", payload.ViewName, mapscreen.ViewSubscriptionName)
	}
	if payload.ViewName != "f1.viewport" {
		t.Fatalf("ViewSubscriptionName = %q, want literal %q", payload.ViewName, "f1.viewport")
	}
	if err := protocol.ValidateViewName(payload.ViewName); err != nil {
		t.Fatalf("ValidateViewName(%q) = %v, want nil", payload.ViewName, err)
	}
	if got.CorrelationID == "" {
		t.Fatal("CorrelationID is empty")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Command.Validate() = %v, want nil (command must be sendable as-is)", err)
	}
}

func TestSubscribe_PropagatesSenderError(t *testing.T) {
	m := newTestScreen(t)
	wantErr := protocol.ErrTransportClosed
	err := m.Subscribe(func(protocol.Command) error { return wantErr })
	if err != wantErr {
		t.Fatalf("Subscribe() error = %v, want %v", err, wantErr)
	}
}

// UI-SPEC §1 staleness dot.
func TestRender_StalenessIndicatorCell(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))

	rect := core.Rect{X: 0, Y: 0, W: 10, H: 10}
	staleX, staleY := rect.X+rect.W-1, rect.Y

	fresh := core.NewBuffer(10, 10)
	m.Render(fresh, rect)
	freshCell := fresh.Get(staleX, staleY)

	m.SetStale(true)
	stale := core.NewBuffer(10, 10)
	m.Render(stale, rect)
	staleCell := stale.Get(staleX, staleY)

	if freshCell == staleCell {
		t.Fatalf("staleness indicator cell did not change: fresh=%+v stale=%+v", freshCell, staleCell)
	}
	dangerColor := widgets.DefaultPalette.Color(widgets.TokenDanger)
	if fg, _, _ := staleCell.Style.Decompose(); fg != dangerColor {
		t.Fatalf("stale indicator foreground = %v, want danger colour %v", fg, dangerColor)
	}
}

// AC-2: the minimap's viewport indicator moves as the viewport pans.
//
// Render always derives the viewport (and therefore minimap-strip) size
// from the rect it is given (render.go's doc comment) — SetViewportSize
// only matters for callers that never render, e.g. the Pan/MoveCursor
// clamp tests above. This test deliberately uses a rect narrower than
// Folkestone-64's 64-cell width (16 of 64) so there is room to pan and
// the minimap's 4-grid-columns-per-strip-cell binning has a real span to
// move across.
func TestRender_MinimapIndicatorMovesWithPan(t *testing.T) {
	w := stub.GenerateFolkestone64() // 64x64
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))

	const stripW = 16 // rect.W; grid width 64 -> 4 grid columns per strip cell
	rect := core.Rect{X: 0, Y: 0, W: stripW, H: 9}
	minimapY := rect.H - 1

	reverseAt := func(buf *core.Buffer, x int) bool {
		_, _, attrs := buf.Get(x, minimapY).Style.Decompose()
		return attrs&tcell.AttrReverse != 0
	}

	before := core.NewBuffer(rect.W, rect.H)
	m.Render(before, rect) // establishes viewportW=16 from rect; offset 0 -> highlighted strip cols [0,4)
	if !reverseAt(before, 0) {
		t.Fatal("minimap column 0 not highlighted at initial offset 0")
	}
	if reverseAt(before, 10) {
		t.Fatal("minimap column 10 unexpectedly highlighted at initial offset 0")
	}

	m.Pan(32, 0) // offsetX=32 (clamped max is 64-16=48) -> highlighted strip cols [8,12)
	after := core.NewBuffer(rect.W, rect.H)
	m.Render(after, rect)
	if reverseAt(after, 0) {
		t.Fatal("minimap column 0 still highlighted after panning away from it")
	}
	if !reverseAt(after, 10) {
		t.Fatal("minimap column 10 not highlighted after panning to it")
	}
}

// AC-10: rendering is a pure function of (grid, offset, cursor, staleness).
func TestRender_Deterministic(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))
	m.SetViewportSize(15, 12)
	m.Pan(4, 6)
	m.MoveCursor(2, 3)

	rect := core.Rect{X: 0, Y: 0, W: 20, H: 20}
	a := core.NewBuffer(20, 20)
	b := core.NewBuffer(20, 20)
	m.Render(a, rect)
	m.Render(b, rect)

	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				t.Fatalf("non-deterministic render at (%d,%d): %+v vs %+v", x, y, a.Get(x, y), b.Get(x, y))
			}
		}
	}
}

// AC-11: no data race between the delta-applying goroutine (ApplyPatch)
// and the render path (Render), run concurrently -race.
func TestConcurrent_ApplyPatchAndRender_NoRace(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))
	m.SetViewportSize(16, 16)

	full := fullPatchJSON(t, w)
	sparse := sparsePatchJSON(t, []stub.ViewportCell{{X: 1, Y: 1, Terrain: "shelf", Elevation: 5}})

	var wg sync.WaitGroup
	const iterations = 200

	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				m.ApplyPatch(full)
			} else {
				m.ApplyPatch(sparse)
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := core.NewBuffer(20, 20)
		rect := core.Rect{X: 0, Y: 0, W: 20, H: 20}
		for i := 0; i < iterations; i++ {
			m.Render(buf, rect)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.Pan(1, -1)
			m.MoveCursor(1, -1)
			m.SetStale(i%2 == 0)
			_ = m.Inspect(i%64, (i*3)%64)
		}
	}()
	wg.Wait()
}

// --- AC-3: overlay cycle -------------------------------------------

// TestActiveOverlay_DefaultsToFirstInOrder: a freshly constructed
// MapScreen starts on overlayOrder's first entry (ownership) — no
// separate "no overlay" state exists (doc.go's Overlay cycle section),
// per AC-3's ten-overlay list having no described null/off member.
func TestActiveOverlay_DefaultsToFirstInOrder(t *testing.T) {
	m := newTestScreen(t)
	if got, want := m.ActiveOverlay(), mapscreen.OverlayOwnership; got != want {
		t.Fatalf("ActiveOverlay() on a fresh MapScreen = %v, want %v", got, want)
	}
}

// TestCycleOverlay_Forward_ReturnsToStartAfterNSteps is AC-3's core
// assertion: "cycling through all overlays returns to the starting
// overlay after N steps (N = overlay count)" — forward direction ("o").
// N is FEAT-031's ten plus FEAT-1972079851's appended eleventh ("power").
func TestCycleOverlay_Forward_ReturnsToStartAfterNSteps(t *testing.T) {
	m := newTestScreen(t)
	start := m.ActiveOverlay()

	seen := map[mapscreen.Overlay]bool{start: true}
	const n = 11 // ten AC-3 overlays + OverlayPower; asserted separately below
	for i := 0; i < n; i++ {
		got := m.CycleOverlay(true)
		if i < n-1 {
			seen[got] = true
		} else if got != start {
			t.Fatalf("CycleOverlay(true) step %d (last of %d) = %v, want back to start %v", i+1, n, got, start)
		}
	}
	if len(seen) != n {
		t.Fatalf("forward cycle visited %d distinct overlays before returning to start, want exactly %d (no repeats, no skips)", len(seen), n)
	}
}

// TestCycleOverlay_Reverse_ReturnsToStartAfterNSteps is AC-3's same
// assertion in the reverse direction ("O").
func TestCycleOverlay_Reverse_ReturnsToStartAfterNSteps(t *testing.T) {
	m := newTestScreen(t)
	start := m.ActiveOverlay()

	seen := map[mapscreen.Overlay]bool{start: true}
	const n = 11
	for i := 0; i < n; i++ {
		got := m.CycleOverlay(false)
		if i < n-1 {
			seen[got] = true
		} else if got != start {
			t.Fatalf("CycleOverlay(false) step %d (last of %d) = %v, want back to start %v", i+1, n, got, start)
		}
	}
	if len(seen) != n {
		t.Fatalf("reverse cycle visited %d distinct overlays before returning to start, want exactly %d (no repeats, no skips)", len(seen), n)
	}
}

// TestCycleOverlay_ForwardThenReverse_Cancels: N forward steps then N
// reverse steps must land back exactly where N forward steps started
// (the two directions are true inverses of each other, not just each
// individually cyclic).
func TestCycleOverlay_ForwardThenReverse_Cancels(t *testing.T) {
	m := newTestScreen(t)
	start := m.ActiveOverlay()
	for i := 0; i < 7; i++ {
		m.CycleOverlay(true)
	}
	for i := 0; i < 7; i++ {
		m.CycleOverlay(false)
	}
	if got := m.ActiveOverlay(); got != start {
		t.Fatalf("7 forward + 7 reverse CycleOverlay calls landed on %v, want back at start %v", got, start)
	}
}

// TestCycleOverlay_MatchesDocumentedFEAT031Order pins the cycle order to
// FEAT-031's own list ("ownership, land value, zoning, utilities,
// traffic, pollution, decay, per-service coverage, parking occupancy,
// vitality") followed by FEAT-1972079851's appended eleventh entry
// ("power" — overlay.go documents why it appends at the end): a passing
// test here is what makes a future accidental reordering (e.g. an
// alphabetised overlayOrder) visible as a failure, not just a silent
// behaviour change.
func TestCycleOverlay_MatchesDocumentedFEAT031Order(t *testing.T) {
	m := newTestScreen(t)
	want := []mapscreen.Overlay{
		mapscreen.OverlayOwnership,
		mapscreen.OverlayLandValue,
		mapscreen.OverlayZoning,
		mapscreen.OverlayUtilities,
		mapscreen.OverlayTraffic,
		mapscreen.OverlayPollution,
		mapscreen.OverlayDecay,
		mapscreen.OverlayServiceCoverage,
		mapscreen.OverlayParkingOccupancy,
		mapscreen.OverlayVitality,
		mapscreen.OverlayPower,
	}
	if got := m.ActiveOverlay(); got != want[0] {
		t.Fatalf("ActiveOverlay() = %v, want %v (first in FEAT-031's documented order)", got, want[0])
	}
	for i := 1; i < len(want); i++ {
		if got := m.CycleOverlay(true); got != want[i] {
			t.Fatalf("CycleOverlay(true) step %d = %v, want %v (FEAT-031's documented order)", i, got, want[i])
		}
	}
	// one more step wraps back to the start
	if got := m.CycleOverlay(true); got != want[0] {
		t.Fatalf("CycleOverlay(true) after the full order = %v, want wrap to %v", got, want[0])
	}
}

// --- AC-4 (screen-level): overlay state never affects Render's
// foreground glyphs -------------------------------------------------

// TestRender_CyclingOverlays_NeverChangesForegroundGlyphs is AC-4's
// screen-level invariant, exercised through the real, public Render path
// (the overlay_paint_internal_test.go file proves the underlying
// paintOverlay mechanism's full two-layer contract with synthetic data;
// this test proves Render's wiring of it is correct too): with every
// overlay in AC-3's ten BLOCKED today (overlayLiveValue), cycling through
// all of them must render byte-identical output, foreground AND
// background — the honest current-state consequence of every overlay
// reporting have=false, verified here so a regression that started
// touching glyphs while blocked, or started diverging background for a
// BLOCKED overlay, is caught.
func TestRender_CyclingOverlays_NeverChangesForegroundGlyphs(t *testing.T) {
	w := stub.GenerateFolkestone64()
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, w))
	m.SetViewportSize(20, 20)

	rect := core.Rect{X: 0, Y: 0, W: 20, H: 20}
	first := core.NewBuffer(20, 20)
	m.Render(first, rect)
	firstCells := captureCells(first, 20, 20)

	for i := 0; i < 10; i++ {
		m.CycleOverlay(true)
		buf := core.NewBuffer(20, 20)
		m.Render(buf, rect)
		got := captureCells(buf, 20, 20)
		if !cellsEqual(firstCells, got) {
			t.Fatalf("cycle step %d: render differs from the first overlay's render, even though every AC-3 overlay is BLOCKED (have=false) today — a BLOCKED overlay must render identically to any other BLOCKED overlay", i+1)
		}
	}
}

// captureCells/cellsEqual are this (external, mapscreen_test) package's
// own copies of sec020_test.go's snapshotBuffer/buffersEqual helpers —
// that file lives in the internal `mapscreen` package (white-box tests),
// so its unexported helpers are not visible here; duplicated rather than
// exported solely for test convenience.
func captureCells(buf *core.Buffer, w, h int) []core.Cell {
	out := make([]core.Cell, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out = append(out, buf.Get(x, y))
		}
	}
	return out
}

func cellsEqual(a, b []core.Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
