package mapscreen

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// cellData is this screen's own working-copy of one grid cell, built
// entirely from "f1.viewport" wireCell payloads — never from an
// internal/engine type (AC-1).
type cellData struct {
	Terrain   string
	Elevation int
	Road      string
	Building  string
	// Known is false for a grid slot that has never been covered by an
	// applied full or sparse patch (e.g. before the first full snapshot
	// arrives, or a coordinate outside the last full snapshot's extent).
	// Inspect surfaces Known==false as its AC-9 "no longer available"
	// state — this schema has no cell-deletion message, so "vanished" is
	// modelled as "not currently covered by known snapshot data" (noted
	// as a Sprint-1 simplification in this package's doc.go).
	Known bool
}

// SendCommandFunc issues one protocol.Command toward the engine.
// MapScreen never holds a protocol.Transport itself — "the screen does
// not own the transport" (this item's dispatch brief) — Subscribe hands
// its Command to this callback instead, letting the caller (feat.skeleton
// in production, a fake in tests) own the actual transport plumbing and
// the CorrelationID-to-SubscriptionID bookkeeping that
// internal/engine/stub/engine.go's handleSubscribe doc comment describes
// (the SubscriptionID a caller learns is the one carried on the
// Subscribe's first correlated Delta — this package never needs to know
// its own SubscriptionID: the caller is expected to route only Deltas
// belonging to this screen's subscription into ApplyPatch).
type SendCommandFunc func(protocol.Command) error

// InspectResult is Inspect's return value: the F1 "enter to inspect"
// seam (AC-5) — it returns data, it does not render a popup (that is a
// later item's concern per this package's doc.go).
type InspectResult struct {
	// Found is false when (x, y) is outside the last known snapshot's
	// extent, or no snapshot has been applied yet (AC-9's "no longer
	// available" case). Every other field is the zero value when Found
	// is false.
	Found bool
	X, Y  int

	Terrain   string
	Elevation int
	// Road is "" when the cell has no named road segment.
	Road string
	// Building is "" when the cell has no named building.
	Building string
}

// MapScreen is F1: see doc.go for the full package contract.
//
// Concurrency: ApplyPatch is expected to be called from the delta-
// applying goroutine (T-VIEWS' caller) while Render runs on T-RENDER's
// goroutine; every exported method locks mu, so the two can safely run
// concurrently (AC-11's "-race clean" requirement) at the cost of a
// single mutex acquisition per call — the grid is at most 64x64 cells in
// Sprint 1 (Folkestone-64), so this is not a hot-path allocation or
// contention concern.
type MapScreen struct {
	mu sync.Mutex

	// self is set once, at the end of NewMapScreen, to the pointer
	// NewMapScreen itself returns — never reassigned after that.
	// checkNotCopied (copyguard.go) compares a receiver against self to
	// detect a struct copy (SEC-020). Lock-free (atomic.Pointer), so
	// checkNotCopied can run BEFORE mu is ever touched (SEC-016's
	// pre-lock-ordering requirement — see copyguard.go for the full
	// rationale).
	self atomic.Pointer[MapScreen]

	correlationID string
	palette       widgets.Palette

	haveSnapshot  bool
	width, height int // last full snapshot's extent
	grid          []cellData

	offsetX, offsetY     int // viewport pan origin, in grid coordinates
	viewportW, viewportH int // last known visible viewport size (Render/SetViewportSize)

	cursorX, cursorY int // cursor position, relative to the current viewport

	stale bool
}

// NewMapScreen constructs an empty MapScreen (no snapshot applied yet).
// correlationID is used for this screen's own registry-sourced log
// entries (malformed patches — AC-8/AC-9 posture) and as the
// CorrelationID on the Subscribe command Subscribe sends; pass
// errs.NewCorrelationID() if the caller has no more specific ID to
// thread through. palette supplies the terrain/overlay colours (AC-4's
// two-layer contract, reusing ui.widgets rather than hardcoding colour).
func NewMapScreen(correlationID string, palette widgets.Palette) *MapScreen {
	m := &MapScreen{
		correlationID: correlationID,
		palette:       palette,
	}
	// Stored once, last, before m ever escapes to a caller — see the
	// self field's doc comment and copyguard.go for why this is what
	// makes checkNotCopied work.
	m.self.Store(m)
	return m
}

// Subscribe sends the "f1.viewport" Subscribe command via send (AC-1).
// It does not block on or read any CommandResult/Delta — that is the
// caller's transport-owning responsibility, per SendCommandFunc's doc
// comment.
func (m *MapScreen) Subscribe(send SendCommandFunc) error {
	// SEC-020: no mu.Lock() below (correlationID never changes after
	// construction), but Subscribe still reads a receiver field, so it
	// still gets the guard — see the enumeration in sec020_test.go for
	// the one exported method deliberately excluded and why.
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(m.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: ViewSubscriptionName},
	}
	return send(cmd)
}

// ApplyPatch decodes raw as an "f1.viewport" wire patch and applies it to
// the internal grid: a full patch (re)initialises the grid at its
// declared extent; a sparse patch updates only the cells it lists,
// leaving every other cell as it was (AC-6 in the wider acceptance doc;
// this item's "sparse-update application correctness" test).
//
// A malformed patch (invalid JSON, unrecognised schemaVersion, or a
// sparse patch arriving before any full snapshot) is logged via a
// registry-sourced error (MET-U100, GR#7) and dropped — ApplyPatch never
// panics and never partially applies a bad patch (mirrors ui.core
// views.go's AC-9 posture for malformed Deltas).
func (m *MapScreen) ApplyPatch(raw json.RawMessage) {
	// SEC-020: checked before decodeWirePatch even runs — decodeWirePatch
	// touches no receiver state, so there is nothing to protect there,
	// but doing the identity check first (rather than after a wasted
	// decode) means a copy is rejected as cheaply as every other guarded
	// method, not just eventually.
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyPatch"}); err != nil {
		return
	}

	p, err := decodeWirePatch(raw)
	if err != nil {
		m.logMalformed(err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyPatch"}); err != nil {
		return
	}

	if p.Full {
		m.applyFullLocked(p)
		return
	}
	if !m.haveSnapshot {
		// logMalformed only reads m.correlationID, which never changes
		// after construction, so calling it while mu is held is safe (no
		// re-entrant lock, no deadlock).
		m.logMalformed(errSparseBeforeSnapshot)
		return
	}
	m.applySparseLocked(p)
}

func (m *MapScreen) applyFullLocked(p wirePatch) {
	w, h := p.Extent.Width, p.Extent.Height
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	// SEC-009: bound the factors BEFORE they are ever multiplied
	// together — see limits.go's maxGridSide doc comment for why
	// checking w*h against maxGridCells directly, without this
	// per-dimension check first, would let the multiplication itself
	// overflow before the comparison ever ran. Rejected, never clamped
	// (clamping would silently render a truncated grid and hide the
	// attack, same posture ApplyPatch's other malformed-patch paths
	// already take) — this function returns before touching
	// m.width/m.height/m.grid/m.haveSnapshot at all, so the grid keeps
	// its last-known-good state exactly as decodeWirePatch's malformed-
	// patch contract promises.
	if w > maxGridSide || h > maxGridSide {
		m.logMalformed(errExtentTooLarge(w, h, maxGridSide, maxGridCells))
		return
	}
	cells := w * h // safe: both factors are bounded by maxGridSide above
	if cells > maxGridCells {
		m.logMalformed(errExtentTooLarge(w, h, maxGridSide, maxGridCells))
		return
	}
	grid := make([]cellData, cells)
	for _, c := range p.Cells {
		if c.X < 0 || c.X >= w || c.Y < 0 || c.Y >= h {
			continue
		}
		grid[c.Y*w+c.X] = cellData{
			Terrain:   c.Terrain,
			Elevation: c.Elevation,
			Road:      c.Road,
			Building:  c.Building,
			Known:     true,
		}
	}
	m.width, m.height = w, h
	m.grid = grid
	m.haveSnapshot = true
	m.clampOffsetLocked()
	m.clampCursorLocked()
}

func (m *MapScreen) applySparseLocked(p wirePatch) {
	for _, c := range p.Cells {
		if c.X < 0 || c.X >= m.width || c.Y < 0 || c.Y >= m.height {
			continue
		}
		m.grid[c.Y*m.width+c.X] = cellData{
			Terrain:   c.Terrain,
			Elevation: c.Elevation,
			Road:      c.Road,
			Building:  c.Building,
			Known:     true,
		}
	}
}

func (m *MapScreen) logMalformed(cause error) {
	_ = errs.New("MET-U100", m.correlationID, map[string]any{
		"cause": cause.Error(),
	})
}

// SetStale surfaces ui.core's per-subscription staleness flag
// (core.ViewModels.Stale) as a visible indicator cell (render.go) —
// UI-SPEC §1's "staleness dot". The caller (whoever owns the ViewStore)
// is expected to call this once per render tick with
// vm.Stale[thisScreensSubscriptionID].
func (m *MapScreen) SetStale(stale bool) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	m.mu.Lock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		m.mu.Unlock()
		return
	}
	m.stale = stale
	m.mu.Unlock()
}

// SetViewportSize records the visible viewport size (in grid cells) used
// to clamp Pan and the cursor. Render also calls this from the rect it
// is given, so a caller that only ever renders through Render does not
// need to call this directly; it exists so Pan's clamping behaviour is
// testable independent of any rendering call.
func (m *MapScreen) SetViewportSize(w, h int) {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetViewportSize"}); err != nil {
		return
	}
	m.mu.Lock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetViewportSize"}); err != nil {
		m.mu.Unlock()
		return
	}
	m.viewportW, m.viewportH = w, h
	m.clampOffsetLocked()
	m.clampCursorLocked()
	m.mu.Unlock()
}

// Pan shifts the viewport origin by (dx, dy) grid cells, clamped so the
// viewport never scrolls past the last known snapshot's edges (AC-2's
// "panning changes the rendered viewport origin ... correspondingly").
// Panning before any snapshot or viewport size is known is a no-op
// (offset stays clamped to 0,0 by clampOffsetLocked).
func (m *MapScreen) Pan(dx, dy int) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Pan"}); err != nil {
		return
	}
	m.mu.Lock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Pan"}); err != nil {
		m.mu.Unlock()
		return
	}
	m.offsetX += dx
	m.offsetY += dy
	m.clampOffsetLocked()
	m.mu.Unlock()
}

// Offset returns the current viewport pan origin, in grid coordinates.
// On a struct-copied receiver, fails closed to (0, 0) — the same value
// an un-panned, freshly constructed MapScreen would report — rather than
// reading the copy's own (aliased-construction-time) fields.
func (m *MapScreen) Offset() (int, int) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Offset"}); err != nil {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Offset"}); err != nil {
		return 0, 0
	}
	return m.offsetX, m.offsetY
}

func (m *MapScreen) clampOffsetLocked() {
	m.offsetX = clampOffset(m.offsetX, m.width, m.viewportW)
	m.offsetY = clampOffset(m.offsetY, m.height, m.viewportH)
}

func clampOffset(offset, extent, viewport int) int {
	maxOffset := extent - viewport
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

// MoveCursor shifts the cursor by (dx, dy) cells, clamped to the current
// viewport window (the cursor only ever points at a currently visible
// cell — the "enter to inspect" seam inspects what's on screen).
func (m *MapScreen) MoveCursor(dx, dy int) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "MoveCursor"}); err != nil {
		return
	}
	m.mu.Lock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "MoveCursor"}); err != nil {
		m.mu.Unlock()
		return
	}
	m.cursorX += dx
	m.cursorY += dy
	m.clampCursorLocked()
	m.mu.Unlock()
}

func (m *MapScreen) clampCursorLocked() {
	m.cursorX = clampCursor(m.cursorX, m.viewportW)
	m.cursorY = clampCursor(m.cursorY, m.viewportH)
}

func clampCursor(v, viewport int) int {
	if viewport <= 0 {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > viewport-1 {
		return viewport - 1
	}
	return v
}

// CursorPos returns the cursor's current position, in grid coordinates
// (viewport offset + the cursor's viewport-relative position). Fails
// closed to (0, 0) on a struct-copied receiver, same posture as Offset.
func (m *MapScreen) CursorPos() (int, int) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CursorPos"}); err != nil {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CursorPos"}); err != nil {
		return 0, 0
	}
	return m.offsetX + m.cursorX, m.offsetY + m.cursorY
}

// InspectCursor is Inspect at the cursor's current grid position — the
// convenience form a future Enter-key binding calls.
//
// SEC-020: guarded here too even though it delegates entirely to
// CursorPos and Inspect, both already guarded — checking first means a
// copy is rejected without even calling into either, and keeps this
// method's own entry consistent with every other exported method on
// this type (sec020_test.go's enumeration checks it by name, not just
// via its callees).
func (m *MapScreen) InspectCursor() InspectResult {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "InspectCursor"}); err != nil {
		return InspectResult{Found: false}
	}
	x, y := m.CursorPos()
	return m.Inspect(x, y)
}

// Inspect returns the data known for grid cell (x, y) (AC-5). x and y
// are absolute grid coordinates (the same coordinate space as the
// "f1.viewport" wire schema's cells[].x/y), not viewport-relative. On a
// struct-copied receiver, fails closed to InspectResult{Found: false} —
// reusing the existing "cell not known" result shape (AC-9) rather than
// inventing a second not-found meaning.
func (m *MapScreen) Inspect(x, y int) InspectResult {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Inspect"}); err != nil {
		return InspectResult{Found: false, X: x, Y: y}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Inspect"}); err != nil {
		return InspectResult{Found: false, X: x, Y: y}
	}

	if x < 0 || x >= m.width || y < 0 || y >= m.height {
		return InspectResult{Found: false, X: x, Y: y}
	}
	c := m.grid[y*m.width+x]
	if !c.Known {
		return InspectResult{Found: false, X: x, Y: y}
	}
	return InspectResult{
		Found:     true,
		X:         x,
		Y:         y,
		Terrain:   c.Terrain,
		Elevation: c.Elevation,
		Road:      c.Road,
		Building:  c.Building,
	}
}
