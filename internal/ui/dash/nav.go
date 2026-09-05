package dash

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Navigator is the navigation seam a Dashboard calls through when a
// drill target resolves. This package does NOT implement navigation
// (that is the screen/pane-focus machinery of the UI composition layer,
// ui.core and the F-screen layer) — it registers what Enter should
// navigate to and calls Navigate to perform the jump (AC-6). The
// concrete implementation is supplied by the caller that owns screen
// focus.
type Navigator interface {
	// Navigate jumps to target's source. A non-nil error is surfaced to
	// the caller (the jump failed); nil means the jump was accepted.
	Navigate(target DrillTarget) error
}

// Resolver reports whether a DrillTarget's source still exists at drill
// time. It is the seam that turns int.protocol's
// stale-subscription/vanished-entity case into AC-9's "no longer
// available" state: before navigating, Dashboard.Drill asks the resolver
// whether the target's view (and entity, when set) is live.
type Resolver interface {
	// Resolve reports whether target's subscription/entity is currently
	// live (true) or has vanished (false).
	Resolve(target DrillTarget) bool
}

// MapResolver is a simple Resolver backed by a set of live drill targets
// (a map keyed by the target's ViewName+EntityID). It is convenient for
// tests and small composition roots; a real screen would implement
// Resolver directly against its live view-model store (ui.core.ViewStore).
// Mark and Resolve are safe for concurrent use.
type MapResolver struct {
	mu   sync.RWMutex
	live map[string]bool

	// self holds the address NewMapResolver gave this MapResolver at
	// construction (self.Store(m), set once, at the end of NewMapResolver,
	// never stored to again). checkNotCopied compares a receiver against
	// self to detect a struct copy (SEC-020): `m2 := *m` is legal,
	// unsafe-free Go, but mu is a sync.RWMutex VALUE — the copy m2 gets
	// its OWN, independently-zeroed mu — while m2.live (a map, a reference
	// type) still ALIASES m.live. An unrejected copy is therefore a second
	// lock domain that can read/mutate the SAME map as the original under
	// the mistaken belief that holding its own mu is exclusive access —
	// exactly SEC-020's "two locks, one referent" shape.
	//
	// atomic.Pointer[MapResolver], not a plain *MapResolver, for the same
	// reason SEC-016 forced Engine.self's type: a plain, unsynchronized
	// field read done lock-free, concurrently with a struct copy that
	// touches the whole struct's memory as one operation, has no defined
	// result in the Go memory model unless the read is itself a properly
	// synchronized operation. Store happens exactly once, in
	// NewMapResolver, before any goroutine can have a reference to m to
	// race against; every subsequent Load is a single lock-free atomic
	// read requiring nothing else — not mu, nothing a copy could have
	// captured mid-lock.
	self atomic.Pointer[MapResolver]
}

// NewMapResolver returns an empty resolver (nothing live).
func NewMapResolver() *MapResolver {
	m := &MapResolver{live: make(map[string]bool)}
	// Stored exactly once, here, before m is returned to any caller — no
	// goroutine can have a reference to m to race this Store against (see
	// self's doc comment above).
	m.self.Store(m)
	return m
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other MapResolver value (SEC-020, mirroring MapScreen.checkNotCopied —
// internal/ui/screens/map/copyguard.go — and World.checkNotCopied —
// internal/engine/world/grid.go). Deliberately lock-free — a single
// atomic.Pointer.Load, requiring nothing else, not m.mu — so it is safe
// and correct to call BEFORE m.mu is ever touched.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`m2 := *m` while another goroutine has
// m.mu.Lock()'d) — acquiring, or even attempting to acquire, a copy's
// own mu in that state can block forever, since nothing will ever
// Unlock() that specific copy's address. A guard placed AFTER the lock
// can never run for that attack; rejecting the copy here, before Lock()
// is ever called, means that hang path is never reached at all.
//
// A nil m.self.Load() (a MapResolver constructed as a bare
// `MapResolver{}` or `new(MapResolver)` rather than via NewMapResolver,
// so self was never stored) is treated the same as a mismatch and
// rejected the same way — every documented construction path is
// NewMapResolver, so an unset self is itself a misuse this same error
// correctly names.
func (m *MapResolver) checkNotCopied(correlationID string, ctx map[string]any) error {
	if m.self.Load() != m {
		return errs.New(codeMapResolverCopied, correlationID, ctx)
	}
	return nil
}

// Mark registers target as live.
func (m *MapResolver) Mark(target DrillTarget) {
	if err := m.checkNotCopied(corr(), map[string]any{"method": "Mark"}); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Defence-in-depth re-check under the lock (cheap — one more atomic
	// load — mirrors MapScreen/WorldAPI's pre+post pattern).
	if err := m.checkNotCopied(corr(), map[string]any{"method": "Mark"}); err != nil {
		return
	}
	m.live[target.ViewName+"\x00"+string(target.EntityID)] = true
}

// Resolve implements Resolver.
func (m *MapResolver) Resolve(target DrillTarget) bool {
	if err := m.checkNotCopied(corr(), map[string]any{"method": "Resolve"}); err != nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkNotCopied(corr(), map[string]any{"method": "Resolve"}); err != nil {
		return false
	}
	return m.live[target.ViewName+"\x00"+string(target.EntityID)]
}

// Dashboard is one screen's dashboard: a Layout plus the navigation and
// live-state seams it drills through, with a mutex so the layout editor
// (mutating) and the render path (reading) are safe to run concurrently
// (AC-13: the editor's save path running alongside a live render).
type Dashboard struct {
	mu     sync.RWMutex
	layout Layout
	nav    Navigator
	live   Resolver

	// self holds the address NewDashboard gave this Dashboard (stored once,
	// at the end of NewDashboard, never stored to again). checkNotCopied
	// compares a receiver against self to reject a struct copy (SEC-020),
	// mirroring MapResolver.self — see its doc comment for the full
	// rationale (mu is a sync.RWMutex VALUE while layout.tiles is a slice
	// a copy ALIASES; atomic.Pointer, not a plain *Dashboard, for the same
	// SEC-016 lock-free-read reason).
	self atomic.Pointer[Dashboard]
}

// NewDashboard constructs a dashboard for the given layout. nav may be
// nil (drilling then returns a loud error rather than panicking); live
// may be nil (treated as "everything is live" — the zero resolver, so a
// dashboard without a resolver never falsely reports a vanished entity).
func NewDashboard(l Layout, nav Navigator, live Resolver) *Dashboard {
	// Clone l so the dashboard owns its tiles backing array (SEC-092): a
	// caller keeping `l` and calling l.RemoveTile/AddTile/MoveTile must not
	// reach d.layout.tiles, or a second mutation path into the dashboard's
	// layout would silently corrupt it.
	d := &Dashboard{layout: l.clone(), nav: nav, live: live}
	// Stored exactly once, here, before d is returned to any caller — no
	// goroutine can have a reference to d to race this Store against.
	d.self.Store(d)
	return d
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Dashboard value (SEC-020, mirroring MapResolver.checkNotCopied /
// MapScreen.checkNotCopied / World.checkNotCopied). Deliberately
// lock-free — a single atomic.Pointer.Load, requiring nothing else, not
// d.mu — so it is safe and correct to call BEFORE d.mu is ever touched.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held — acquiring a copy's own mu in that state can
// block forever, since nothing will ever Unlock() that specific copy's
// address. Rejecting the copy here, before Lock()/RLock() is ever called,
// means that hang path is never reached at all.
//
// A nil d.self.Load() (a Dashboard constructed as a bare `Dashboard{}` or
// `new(Dashboard)` rather than via NewDashboard, so self was never stored)
// is treated the same as a mismatch and rejected the same way — every
// documented construction path is NewDashboard.
func (d *Dashboard) checkNotCopied(correlationID string, ctx map[string]any) error {
	if d.self.Load() != d {
		return errs.New(codeDashboardCopied, correlationID, ctx)
	}
	return nil
}

// Layout returns a copy of the dashboard's current layout. The returned
// Layout is defensive (its tiles slice has its own backing array), so
// editing it does not mutate the dashboard. On a struct-copied receiver it
// fails closed to an empty layout (SEC-020).
func (d *Dashboard) Layout() Layout {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Layout"}); err != nil {
		return Layout{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Layout"}); err != nil {
		return Layout{}
	}
	return d.layout.clone()
}

// SetLayout replaces the dashboard's layout (the editor's "reload a
// profile" path).
func (d *Dashboard) SetLayout(l Layout) {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "SetLayout"}); err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "SetLayout"}); err != nil {
		return
	}
	d.layout = l.clone()
}

// AddTile appends a tile under the editor lock.
func (d *Dashboard) AddTile(t Tile) error {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "AddTile"}); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "AddTile"}); err != nil {
		return err
	}
	return d.layout.AddTile(t)
}

// RemoveTile removes a tile under the editor lock.
func (d *Dashboard) RemoveTile(id string) error {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "RemoveTile"}); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "RemoveTile"}); err != nil {
		return err
	}
	return d.layout.RemoveTile(id)
}

// MoveTile reorders a tile under the editor lock.
func (d *Dashboard) MoveTile(id string, to int) error {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "MoveTile"}); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "MoveTile"}); err != nil {
		return err
	}
	return d.layout.MoveTile(id, to)
}

// Save marshals the current layout to profile JSON under the editor lock
// (AC-13: safe to run concurrently with Render).
func (d *Dashboard) Save() ([]byte, error) {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Save"}); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Save"}); err != nil {
		return nil, err
	}
	return Marshal(d.layout)
}

// Drill fires Enter on the selected element: it resolves the element's
// DrillTarget against the live-state resolver and, if live, calls
// through to navigation (AC-6). elementID selects within an aggregate
// tile — "" means the tile itself (the whole-view target); "row:N" and
// "hit:N" select a specific table row or diagram hit-test entry.
//
// A target whose resolved subscription/entity no longer exists returns
// a registry-sourced "no longer available" error (MET-U600) rather than
// crashing or silently no-oping (AC-9). An unknown tile ID returns
// MET-U604. A nil navigator returns a loud error rather than panicking.
func (d *Dashboard) Drill(tileID, elementID string) error {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Drill"}); err != nil {
		return err
	}
	d.mu.RLock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Drill"}); err != nil {
		d.mu.RUnlock()
		return err
	}
	target, ok := d.resolveTarget(tileID, elementID)
	d.mu.RUnlock()
	if !ok {
		return errs.New(codeUnknownTile, corr(), map[string]any{
			"tileId":    tileID,
			"elementId": elementID,
		})
	}

	if d.live != nil && !d.live.Resolve(target) {
		return errs.New(codeDrillUnavailable, corr(), map[string]any{
			"viewName": target.ViewName,
			"entityId": target.EntityID,
		})
	}
	if d.nav == nil {
		return errs.New(codeDrillUnavailable, corr(), map[string]any{
			"viewName": target.ViewName,
			"entityId": target.EntityID,
			"reason":   "no navigator configured",
		})
	}
	return d.nav.Navigate(target)
}

// resolveTarget maps (tileID, elementID) to the element's DrillTarget.
// It runs under the caller's read lock.
func (d *Dashboard) resolveTarget(tileID, elementID string) (DrillTarget, bool) {
	for _, t := range d.layout.tiles {
		if t.id != tileID {
			continue
		}
		switch elementID {
		case "":
			return t.drill, true
		default:
			return d.resolveElement(t, elementID)
		}
	}
	return DrillTarget{}, false
}

// resolveElement maps an aggregate element ID ("row:N"/"hit:N") to its
// DrillTarget.
func (d *Dashboard) resolveElement(t Tile, elementID string) (DrillTarget, bool) {
	// Parse the "kind:N" prefix. Only table rows and diagram hits are
	// addressable elements; anything else is not found (loud, not a
	// silent fall-through to the tile target).
	switch t.kind {
	case KindTable:
		if t.table == nil {
			return DrillTarget{}, false
		}
		idx, ok := parseElementIndex(elementID, "row:")
		if !ok || idx < 0 || idx >= len(t.table.Rows) {
			return DrillTarget{}, false
		}
		return t.table.Rows[idx].Drill, true
	case KindDiagram:
		if t.diagram == nil {
			return DrillTarget{}, false
		}
		idx, ok := parseElementIndex(elementID, "hit:")
		if !ok || idx < 0 || idx >= len(t.diagram.Hits) {
			return DrillTarget{}, false
		}
		return t.diagram.Hits[idx].Drill, true
	default:
		return DrillTarget{}, false
	}
}

// parseElementIndex parses an aggregate element ID of the form
// "prefixN" (e.g. "row:" + "7" -> 7). It returns ok=false for anything
// that is not exactly that shape, so a malformed/foreign element ID is a
// loud not-found, never a silent wrong element.
func parseElementIndex(elementID, prefix string) (int, bool) {
	if !strings.HasPrefix(elementID, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(elementID[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}
