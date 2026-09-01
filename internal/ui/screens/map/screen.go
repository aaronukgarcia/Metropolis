package mapscreen

import (
	"encoding/json"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// correlationSuffixRe matches one trailing " (correlation: <id>)" segment
// of an errs.E-rendered Display string — see dedupeCorrelationSuffix.
var correlationSuffixRe = regexp.MustCompile(`\s\(correlation: [^)]*\)`)

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

	// Caching optimization for BUG-331: the grid is copied to break aliasing
	// on every snapshotLocked call, but the grid only changes on ApplyPatch
	// calls (which set gridDirty=true). Render calls snapshotLocked many
	// times without the grid changing, so caching the grid snapshot between
	// changes saves ~2.5MB per Render at real tile size (40K cells). The
	// other snapshot fields (offsetX, offsetY, cursor, stale, overlay) change
	// frequently, so only the grid itself is cached; snapshotLocked rebuilds
	// the full renderSnapshot on every call but reuses cachedGridSnapshot
	// when gridDirty is false.
	cachedGridSnapshot []cellData
	gridDirty          bool
	// cachedHasKnown mirrors cachedGridSnapshot: whether the cached grid
	// holds at least one Known cell (BUG-330). Recomputed only when the
	// grid is rebuilt (gridDirty), so Render's EMPTY-view detection costs
	// nothing per-frame. A grid with a non-zero extent but no known cell
	// (a full patch that listed no cells) is still "empty" for the
	// player's purposes — an all-blank viewport — which is exactly the
	// state BUG-330's placeholder must cover, not just the no-snapshot case.
	cachedHasKnown bool

	offsetX, offsetY     int // viewport pan origin, in grid coordinates
	viewportW, viewportH int // last known visible viewport size (Render/SetViewportSize)

	cursorX, cursorY int // cursor position, relative to the current viewport

	stale bool

	// overlayIdx indexes overlayOrder (overlay.go) — the AC-3 overlay
	// cycle's current position. Zero-valued (overlayOrder[0],
	// OverlayOwnership) on a freshly constructed MapScreen, same as every
	// other never-explicitly-set field.
	overlayIdx int

	// seenUnrecognisedTerrain deduplicates BUG-334's MET-U100 log: the set
	// of distinct unrecognised terrain surface strings that have already
	// been logged once. Without it, a 40,000-cell grid of one unknown
	// surface would emit 40,000 identical warn lines every time the grid
	// rebuilt — the cure worse than the disease. Guarded by mu (only
	// touched under lock, from snapshotLocked's grid-rebuild path); nil
	// until the first unrecognised surface is seen, and a nil map reads
	// cleanly for the membership test.
	seenUnrecognisedTerrain map[string]bool

	// subs is the set of SubscriptionIDs bound to this screen (BUG-323),
	// mirroring ui.screen.finance/ui.screen.services' identical field.
	// Populated by BindSubscription; consulted by ApplyDelta so a delta
	// belonging to somebody else's subscription is dropped-and-logged
	// rather than applied. Nil until the first BindSubscription call — a
	// nil map reads cleanly, so an ApplyDelta before any bind is an
	// "unknown subscription" log, never a panic.
	subs map[protocol.SubscriptionID]string

	// buildNotice is BUG-490's fix: the last KindBuy/KindBuild CommandResult
	// this screen was told about, surfaced as visible text (render.go's
	// drawBuildNotice) rather than only the registry-sourced log entry
	// router.ErrRouteMiss leaves when nobody is listening. Empty when the
	// last known result was an Accept, a dismissed rejection
	// (DismissBuildNotice, BUG-493 item 3), or none has arrived yet — see
	// ApplyResult's doc comment for the full rationale. Stored VERBATIM as
	// the registry's own Display text, deduplicated of any repeated
	// trailing "(correlation: ...)" segment (dedupeCorrelationSuffix,
	// BUG-493 item 2) — the render-time width truncation (drawBuildNotice)
	// is a SEPARATE concern (item 1) and never mutates this field.
	buildNotice string
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
		gridDirty:     true, // Initial snapshot will copy the (empty) grid
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

// BindSubscription records subscriptionID as belonging to this screen's
// "f1.viewport" subscription (BUG-323) — the same contract
// ui.screen.finance/ui.screen.services' identically-named methods carry,
// so cmd/metropolis' primeScreenSubscription can prime and bind F1
// exactly the way it already primes F2 and F4.
func (m *MapScreen) BindSubscription(id protocol.SubscriptionID) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	if m.subs == nil {
		m.subs = make(map[protocol.SubscriptionID]string)
	}
	m.subs[id] = ViewSubscriptionName
}

// UnbindSubscription forgets a previously bound subscription.
func (m *MapScreen) UnbindSubscription(id protocol.SubscriptionID) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	delete(m.subs, id)
}

// ApplyDelta applies one routed protocol.Delta belonging to this
// screen's bound subscription (BUG-323), delegating the actual decode
// and grid update to ApplyPatch. A delta whose SubscriptionID is not
// bound to this screen — or is bound to some other view name — is
// dropped with a registry-sourced log entry (GR#7) and never applied:
// the map must never render another view's payload as terrain.
//
// This is the method cmd/metropolis' router hands F1's deltas to, and
// the method primeScreenSubscription calls directly for the very first
// delta (the one consumed during priming, before router.Run starts).
func (m *MapScreen) ApplyDelta(delta protocol.Delta) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	m.mu.Lock()
	view, ok := m.subs[delta.SubscriptionID]
	m.mu.Unlock()
	if !ok || view != ViewSubscriptionName {
		_ = errs.New("MET-U100", m.correlationID, map[string]any{
			"cause":          "delta for an unknown or unbound subscription",
			"subscriptionID": string(delta.SubscriptionID),
		})
		return
	}
	m.ApplyPatch(delta.Patch)
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
	m.gridDirty = true // Grid changed; snapshot must regenerate on next Render
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
	m.gridDirty = true // Grid changed; snapshot must regenerate on next Render
}

func (m *MapScreen) logMalformed(cause error) {
	_ = errs.New("MET-U100", m.correlationID, map[string]any{
		"cause": cause.Error(),
	})
}

// noteUnrecognisedTerrainLocked logs an unrecognised terrain surface string
// through the MET-U100 path AT MOST ONCE per distinct string over this
// screen's lifetime (BUG-334). The caller must hold m.mu.
//
// The dedup is the whole point: terrainGlyph now draws a VISIBLE marker for
// any surface it doesn't recognise, but a 40,000-cell grid all carrying the
// same unknown surface must still leave exactly ONE log line, not 40,000 —
// so the first sighting of each distinct string logs, and every later
// sighting (this grid or any future one) is a set-membership no-op. This is
// deliberately NOT a per-cell call: snapshotLocked drives it once per grid
// rebuild (see there), and even within one rebuild the set collapses the
// repeats.
func (m *MapScreen) noteUnrecognisedTerrainLocked(terrain string) {
	if m.seenUnrecognisedTerrain[terrain] {
		return
	}
	if m.seenUnrecognisedTerrain == nil {
		m.seenUnrecognisedTerrain = make(map[string]bool)
	}
	m.seenUnrecognisedTerrain[terrain] = true
	_ = errs.New("MET-U100", m.correlationID, map[string]any{
		"cause":   "unrecognised terrain surface " + strconv.Quote(terrain) + " rendered as the unknown-terrain marker (BUG-334)",
		"terrain": terrain,
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

// ApplyResult surfaces the outcome of a KindBuy/KindBuild command issued
// from this screen's own keyboard path (BUG-362's 'y'/'b' map keys,
// registerMapBuildKeys in cmd/metropolis/boot.go) — BUG-490's fix,
// hardened by BUG-493.
//
// Before this method existed, those keys sent their command fire-and-
// forget with no ResultReceiver registered at all (boot.go's own prior
// doc comment on registerMapBuildKeys said exactly that: "the
// CommandResult left to ui/router's own ErrRouteMiss accounting"), so a
// REJECTED command — insufficient funds, tile not owned, unknown zone —
// produced nothing the player could see: no tile change (correctly —
// nothing happened), no message, no sound, nothing. Aaron's own dogfood
// report (BUG-490) named exactly this: "queueing a build via the screen
// has no effect the player can observe". The player-visible half of the
// fix is this method plus drawBuildNotice (render.go); the boot.go half
// registers this screen as the ResultReceiver for those two commands'
// CorrelationIDs before sending, mirroring services.Screen.ApplyResult's
// existing wiring for the F4 funding slider.
//
// On a rejection (Accepted == false, Error != nil), the engine's own
// registry-sourced ErrorRef.Display (GR#1/GR#7 — never a message this
// screen invents) is stored — deduplicated of a repeated trailing
// "(correlation: ...)" segment via dedupeCorrelationSuffix (BUG-493 item
// 2, the BUG-267-class double-wrap this session's Phase-3 finance bridge
// independently found and fixed the same way, internal/converge/
// finance_ab_actions.go's stripCorrelationSuffix — GR#3, one canonical
// fix reused rather than reinvented) — and rendered until the next
// result arrives, a subsequent Accept clears it, or the player presses
// the dismiss key (DismissBuildNotice, BUG-493 item 3).
//
// On an accept, any previously-shown rejection is cleared — a later
// successful command must not leave a stale error on screen (mirrors
// finance.Screen.ApplyResult's loanRejectedReason and
// services.Screen.ApplyResult's fundingRejectedReason, both cleared
// symmetrically on their own Accept branch).
//
// A REJECTED result carrying a nil Error is protocol-malformed
// (protocol.ErrRejectedResultMissingError's own contract — nothing on
// this delivery path enforces that today) and, before BUG-493, was
// treated exactly like an Accept: the notice was silently wiped with no
// trace at all, indistinguishable from success to the player and
// invisible to any operator (GR#1/GR#17 gap). BUG-493 item 4 closes
// that: the notice is still cleared (a malformed result is not "kept
// showing a possibly-stale rejection" either), but a registry-sourced
// MET-U102 warn is logged first, so the gap is diagnosable rather than
// silent.
//
// This method does NOT attempt to distinguish which of 'y'/'b' (or which
// cell) a given result belongs to — CommandResult carries only
// CorrelationID/Tick/Accepted/Error (protocol/envelope.go), no payload
// echo — so "one live notice at a time, last result wins" is the whole
// contract, same information-theoretic limit services.Screen documents
// for its own ApplyResult.
func (m *MapScreen) ApplyResult(res protocol.CommandResult) {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	if res.Accepted {
		m.buildNotice = ""
		return
	}
	if res.Error == nil {
		// BUG-493 item 4: a REJECTED result with no ErrorRef is malformed
		// (protocol.ErrRejectedResultMissingError) — log it via the
		// registry (GR#1/GR#7) before clearing, rather than silently
		// wiping the notice as if this were an ordinary Accept.
		_ = errs.New("MET-U102", m.correlationID, map[string]any{
			"cause": "ApplyResult received a rejected CommandResult with Error == nil",
		})
		m.buildNotice = ""
		return
	}
	m.buildNotice = dedupeCorrelationSuffix(res.Error.Display)
}

// BuildNotice returns the text ApplyResult last surfaced for a rejected
// KindBuy/KindBuild command — "" when the last known result was an
// accept, the notice has been dismissed (DismissBuildNotice), or no
// result has arrived yet. Exported so a test (or a future second
// renderer) can assert on it without decoding the drawn buffer.
func (m *MapScreen) BuildNotice() string {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BuildNotice"}); err != nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BuildNotice"}); err != nil {
		return ""
	}
	return m.buildNotice
}

// DismissBuildNotice clears a currently-showing build rejection notice
// (BUG-493 item 3) without waiting for a subsequent command result.
//
// Why a dismiss key rather than a wall-clock or tick-based timeout:
// Render's own doc comment documents this package's AC-10 purity
// contract — "the same state renders identically across repeated calls,
// and nothing here samples the wall clock" — so a time.Now()-driven
// expiry inside Render (or anything Render reads) would be a direct
// regression of that invariant, and this package carries no tick-count
// field today (MapScreen never observes protocol.Tick at all — Wiring
// one in is a bigger change than this bug's scope). A dismiss key is
// deterministic, trivially testable, and mirrors the explicit-clear
// precedent already in this codebase (internal/ui/screens/chrome's
// Chrome.ResolveAlert: "the next delta reporting a resolved underlying
// condition drops the alert, rather than leaving a dead entry the player
// must dismiss" — here there is no such delta to key off, so the
// player's own keypress is the resolving signal instead). Bound to the
// 'c' map key in cmd/metropolis/boot.go's registerMapBuildKeys
// ("clear notice") — 'y'/'b' are taken by Buy/Build, and Esc/'q' are the
// walking skeleton's process-lifecycle quit keys (cmd/metropolis/
// run.go's isQuitInput), not available for a screen-local dismiss.
// Idempotent: dismissing an already-empty notice is a silent no-op, not
// an error.
func (m *MapScreen) DismissBuildNotice() {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "DismissBuildNotice"}); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "DismissBuildNotice"}); err != nil {
		return
	}
	m.buildNotice = ""
}

// dedupeCorrelationSuffix collapses every trailing " (correlation: ...)"
// segment in an externally-sourced, already fully-rendered errs.E
// Display string (errs.go's E.Display(): "[code] msg (correlation: id)")
// down to exactly ONE occurrence (BUG-493 item 2, the BUG-267-class
// double-wrap: "[MET-G804] [MET-E404] ... (correlation: X) (correlation:
// X)" — two stacked error codes from a chained errs.Wrap upstream, each
// contributing its own copy of the same correlation ID).
//
// This reuses the identical pattern internal/converge/
// finance_ab_actions.go's stripCorrelationSuffix independently found and
// fixed for the same defect class this session (Phase-3 finance bridge,
// GR#3 — one canonical fix, not reinvented) but cannot call that function
// directly: stripCorrelationSuffix is written for a caller that is about
// to WRAP the stripped string inside a fresh errs.New (whose own
// Display() will append exactly one new "(correlation: ...)" suffix), so
// it always removes the trailing segment entirely, leaving zero. This
// screen has no such wrapping step — ApplyResult stores the engine's
// Display text as-is for the player to read (never inventing or
// re-wrapping it, per this method's own doc comment) — so the equivalent
// fix here must COLLAPSE repeats down to exactly one rather than strip to
// zero, keeping the first correlation ID found (every occurrence in the
// observed double-wrap shapes carries the same ID, since a single
// ApplyResult call's chain of errs.Wrap calls shares one).
//
// A Display with zero or one "(correlation: ...)" segment is returned
// unchanged.
func dedupeCorrelationSuffix(display string) string {
	matches := correlationSuffixRe.FindAllString(display, -1)
	if len(matches) <= 1 {
		return display
	}
	cleaned := correlationSuffixRe.ReplaceAllString(display, "")
	return cleaned + matches[0]
}
