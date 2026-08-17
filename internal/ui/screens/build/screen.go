package build

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SendCommandFunc issues one protocol.Command toward the engine. Screen
// never holds a protocol.Transport itself — mirrors ui.screen.trade's,
// ui.screen.map's and ui.screen.proj's convention exactly (SF-1/SF-4):
// the caller owns the transport and the CorrelationID-to-SubscriptionID
// bookkeeping, and hands the resulting SubscriptionID back to this screen
// via BindSubscription.
type SendCommandFunc func(protocol.Command) error

// Screen is F3, the Land & Construction screen: see doc.go for the full
// package contract. The zero Screen is not ready to use; construct with
// New.
//
// Concurrency: every exported method locks mu, so ApplyDelta (called from
// the delta-applying goroutine) and the accessor/command methods (called
// from the render/input goroutine) may run concurrently (SF-9).
//
// Copy safety (SEC-020): mu is a sync.Mutex VALUE — a struct copy
// `s2 := *s` gets its own, independent lock — while subs (map) and the
// per-surface slices/pointers are reference types a copy ALIASES. self
// (below) plus checkNotCopied (copyguard.go) reject every exported method
// call made on such a copy before mu is ever touched, mirroring
// trade.Screen.self exactly (GR#3).
type Screen struct {
	mu sync.Mutex

	// self holds the address New gave this Screen at construction
	// (self.Store(s), set once, at the end of New, never stored to again).
	// See copyguard.go's checkNotCopied for the full rationale.
	self atomic.Pointer[Screen]

	correlationID string

	// subs maps a bound SubscriptionID to the view name it was bound to
	// (BindSubscription) — the lookup ApplyDelta uses to reject an unknown/
	// stale SubscriptionID (SF-7). This screen owns exactly one view
	// (ViewSubscriptionName), so in practice this map holds at most one
	// entry at a time, but the routing is the same general shape as a
	// multi-view screen so a future second F3 view needs no structural
	// change.
	subs map[protocol.SubscriptionID]string

	// stale mirrors ui.core's per-subscription staleness flag for this
	// screen's single view (SetStale).
	stale bool

	// haveData is false until the first "f3.build" patch has been applied.
	haveData bool

	// Per-sub-surface state. Each sub-surface is independent: a patch that
	// carries zones but omits the queue updates zones and marks the queue
	// unavailable (haveQueue=false) — SF-7's "data that has become
	// unavailable shows a clear no-longer-available state, not stale".
	zones     []ZoneInfo
	haveZones bool

	queue     []BuildOrder
	haveQueue bool

	catalogue     []CatalogueEntry
	haveCatalogue bool

	landPrice     *LandPriceView
	haveLandPrice bool

	demolition     *DemolitionView
	haveDemolition bool
}

// New constructs an empty Screen (no data applied yet). correlationID is
// used for this screen's own registry-sourced log entries (malformed
// patches, unknown subscriptions — GR#1) and as the CorrelationID on the
// Subscribe command Subscribe sends; pass errs.NewCorrelationID() if the
// caller has no more specific ID to thread through.
func New(correlationID string) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
	}
	// Stored exactly once, here, before s is returned to any caller — no
	// goroutine can have a reference to s to race this Store against
	// (SEC-016; see copyguard.go's self doc comment).
	s.self.Store(s)
	return s
}

// Subscribe sends the "f3.build" Subscribe command via send (SF-1). It
// does not block on or read any CommandResult/Delta — that is the
// caller's transport-owning responsibility, per SendCommandFunc's doc
// comment.
func (s *Screen) Subscribe(send SendCommandFunc) error {
	// SEC-020: no mu.Lock() below (correlationID never changes after
	// construction), but Subscribe still reads receiver fields, so it
	// still gets the guard — mirrors trade.Screen.Subscribe exactly.
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: ViewSubscriptionName},
	}
	return send(cmd)
}

// BindSubscription records that id (the SubscriptionID the engine
// allocated in response to a prior Subscribe call) belongs to this
// screen's view. ApplyDelta uses this binding to validate incoming Deltas.
func (s *Screen) BindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.subs[id] = ViewSubscriptionName
}

// UnbindSubscription forgets id (e.g. after Unsubscribe) so a
// subsequently-arriving stale Delta for it is treated as unknown (SF-7)
// rather than accidentally still routed.
func (s *Screen) UnbindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	delete(s.subs, id)
}

// ApplyDelta applies delta's Patch to this screen's view state, provided
// delta's SubscriptionID is bound to ViewSubscriptionName. A Delta for an
// unbound (unknown/stale) SubscriptionID is dropped and logged via
// MET-V201 — never applied, never a panic (SF-7). A malformed patch is
// logged via MET-V200 and dropped; the screen's data keeps its
// last-known-good state.
func (s *Screen) ApplyDelta(delta protocol.Delta) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.mu.Lock()
	view, ok := s.subs[delta.SubscriptionID]
	s.mu.Unlock()
	if !ok {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}
	if view != ViewSubscriptionName {
		// Should be unreachable — BindSubscription only ever stores this
		// screen's single view — but a defensive guard keeps an unexpected
		// binding from routing a patch to the wrong place.
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}

	p, err := decodeWirePatch(delta.Patch)
	if err != nil {
		s.logMalformed(err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if p.Zones != nil {
		s.zones = zonesFromWire(*p.Zones)
		s.haveZones = true
	} else {
		s.zones = nil
		s.haveZones = false
	}
	if p.Queue != nil {
		s.queue = queueFromWire(*p.Queue)
		s.haveQueue = true
	} else {
		s.queue = nil
		s.haveQueue = false
	}
	if p.Catalogue != nil {
		s.catalogue = catalogueFromWire(*p.Catalogue)
		s.haveCatalogue = true
	} else {
		s.catalogue = nil
		s.haveCatalogue = false
	}
	if p.LandPrice != nil {
		lp := landPriceFromWire(*p.LandPrice)
		s.landPrice = &lp
		s.haveLandPrice = true
	} else {
		s.landPrice = nil
		s.haveLandPrice = false
	}
	if p.Demolition != nil {
		d := demolitionFromWire(*p.Demolition)
		s.demolition = &d
		s.haveDemolition = true
	} else {
		s.demolition = nil
		s.haveDemolition = false
	}
	s.haveData = true
}

// SetStale surfaces ui.core's per-subscription staleness flag for this
// screen's single view (UI-SPEC §1's "staleness dot"). The caller is
// expected to call this once per render tick with vm.Stale[this screen's
// subscription ID].
func (s *Screen) SetStale(stale bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		s.mu.Unlock()
		return
	}
	s.stale = stale
	s.mu.Unlock()
}

// Stale reports whether this screen's view is currently marked stale.
// Defaults to false until SetStale has been called.
func (s *Screen) Stale() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	return s.stale
}

// HaveData reports whether any "f3.build" patch has been applied yet.
func (s *Screen) HaveData() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	return s.haveData
}

// Zones returns the current §34 zone catalogue (BLD-2), in the order the
// engine sent them (a fixed producer order, deterministic per GR#21). The
// returned slice is a copy — the caller owns it and cannot corrupt the
// Screen's stored state. have is false until a patch has delivered the
// zones sub-surface.
func (s *Screen) Zones() (zones []ZoneInfo, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Zones"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Zones"}); err != nil {
		return nil, false
	}
	return cloneZones(s.zones), s.haveZones
}

// Queue returns the current build queue (BLD-3), in the engine's order.
// The returned slice is a copy. have is false until a patch has delivered
// the queue sub-surface.
func (s *Screen) Queue() (orders []BuildOrder, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Queue"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Queue"}); err != nil {
		return nil, false
	}
	return cloneQueue(s.queue), s.haveQueue
}

// Catalogue returns the current building catalogue (BLD-5), in the
// engine's order. The returned slice is a copy. have is false until a
// patch has delivered the catalogue sub-surface.
func (s *Screen) Catalogue() (entries []CatalogueEntry, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Catalogue"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Catalogue"}); err != nil {
		return nil, false
	}
	return cloneCatalogue(s.catalogue), s.haveCatalogue
}

// LandPrice returns the current land-purchase price figure (BLD-1). have
// is false until a patch has delivered the landPrice sub-surface (the
// price is then "unavailable", distinct from a price of zero). The
// returned value is a copy.
func (s *Screen) LandPrice() (price LandPriceView, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LandPrice"}); err != nil {
		return LandPriceView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LandPrice"}); err != nil {
		return LandPriceView{}, false
	}
	if s.landPrice == nil {
		return LandPriceView{}, false
	}
	return *s.landPrice, s.haveLandPrice
}

// Demolition returns the current demolition-compensation figure (BLD-4).
// have is false until a patch has delivered the demolition sub-surface.
// The returned value is a copy.
func (s *Screen) Demolition() (d DemolitionView, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Demolition"}); err != nil {
		return DemolitionView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Demolition"}); err != nil {
		return DemolitionView{}, false
	}
	if s.demolition == nil {
		return DemolitionView{}, false
	}
	return *s.demolition, s.haveDemolition
}

// --- wire -> domain conversion ---------------------------------------

func zonesFromWire(ws []wireZone) []ZoneInfo {
	out := make([]ZoneInfo, 0, len(ws))
	for _, w := range ws {
		out = append(out, ZoneInfo{
			Zone:             w.ID,
			Name:             w.Name,
			Materials:        w.Materials,
			Labour:           w.Labour,
			BaseLeadTimeDays: w.BaseLeadTimeDays,
		})
	}
	return out
}

func queueFromWire(ws []wireBuildOrder) []BuildOrder {
	out := make([]BuildOrder, 0, len(ws))
	for _, w := range ws {
		out = append(out, BuildOrder{
			ID:                 w.ID,
			Cell:               w.Cell,
			Zone:               w.Zone,
			MaterialsBillTotal: w.MaterialsBillTotal,
			MaterialsDrawn:     w.MaterialsDrawn,
			MaterialsRemaining: w.MaterialsRemaining,
			LabourRemaining:    w.LabourRemaining,
			LeadTimeRemaining:  w.LeadTimeRemaining,
			Status:             decodeBuildOrderStatus(w.Status),
		})
	}
	return out
}

func catalogueFromWire(ws []wireCatalogueEntry) []CatalogueEntry {
	out := make([]CatalogueEntry, 0, len(ws))
	for _, w := range ws {
		out = append(out, CatalogueEntry{
			ID:          w.ID,
			Name:        w.Name,
			Section:     w.Section,
			CostRaw:     w.CostRaw,
			CapacityRaw: w.CapacityRaw,
			Notes:       w.Notes,
			Unlock:      decodeUnlockState(w.UnlockState),
		})
	}
	return out
}

func landPriceFromWire(w wireLandPrice) LandPriceView {
	return LandPriceView(w)
}

func demolitionFromWire(w wireDemolition) DemolitionView {
	return DemolitionView(w)
}

// --- defensive copies (SEC-062 / GR#16) ------------------------------

// cloneSlice returns a copy of s whose backing array is owned by the
// caller. nil stays nil, so a "no data" series round-trips as nil rather
// than a fabricated empty slice.
func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

func cloneZones(zs []ZoneInfo) []ZoneInfo     { return cloneSlice(zs) }
func cloneQueue(os []BuildOrder) []BuildOrder { return cloneSlice(os) }
func cloneCatalogue(es []CatalogueEntry) []CatalogueEntry {
	return cloneSlice(es)
}

// --- error trapping (GR#1/GR#7) ---------------------------------------

// logMalformed/logUnknownSubscription are unexported and only ever called
// from ApplyDelta, which already guard-validates the receiver on the way
// in. They still call checkNotCopied themselves: astgate's syntactic,
// no-call-graph scan cannot see that reachability relationship, so an
// unguarded method here would surface as a NEW ratchet violation rather
// than the already-accepted trade.Screen shape. The guard is lock-free
// (one atomic pointer load) and returns nil on the only path that reaches
// it — cheap defense-in-depth, not a second lock domain.
func (s *Screen) logMalformed(cause error) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logMalformed"}); err != nil {
		return
	}
	_ = errs.New(ErrMalformedPatch, s.correlationID, map[string]any{
		"view":  ViewSubscriptionName,
		"cause": cause.Error(),
	})
}

func (s *Screen) logUnknownSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logUnknownSubscription"}); err != nil {
		return
	}
	_ = errs.New(ErrUnknownSubscription, s.correlationID, map[string]any{
		"subscriptionId": string(id),
	})
}
