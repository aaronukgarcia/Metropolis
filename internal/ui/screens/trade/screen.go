package trade

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SendCommandFunc issues one protocol.Command toward the engine. Screen
// never holds a protocol.Transport itself — mirrors ui.screen.map's and
// ui.screen.proj's convention exactly (SF-1/SF-4): the caller owns the
// transport and the CorrelationID-to-SubscriptionID bookkeeping, and
// hands the resulting SubscriptionID back to this screen via
// BindSubscription.
type SendCommandFunc func(protocol.Command) error

// Screen is F5, the Trade & Logistics screen: see doc.go for the full
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
// MapScreen.self/demo.Screen.self/proj.Screen.self exactly (GR#3).
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
	// multi-view screen so a future second F5 view needs no structural
	// change.
	subs map[protocol.SubscriptionID]string

	// stale mirrors ui.core's per-subscription staleness flag for this
	// screen's single view (SetStale).
	stale bool

	// haveData is false until the first "f5.trade" patch has been applied.
	haveData bool

	// Per-sub-surface state. Each sub-surface is independent: a patch that
	// carries contracts but omits the port updates contracts and marks the
	// port unavailable (havePort=false) — SF-7's "data that has become
	// unavailable shows a clear no-longer-available state, not stale".
	contracts     []ImportContract
	haveContracts bool

	junctions     []JunctionQueue
	haveJunctions bool

	warehouse     []WarehouseCommodity
	haveWarehouse bool

	port     *PortState
	havePort bool

	balance     *BalanceOfTradeView
	haveBalance bool

	safety     []SafetyCorridor
	haveSafety bool
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

// Subscribe sends the "f5.trade" Subscribe command via send (SF-1). It
// does not block on or read any CommandResult/Delta — that is the
// caller's transport-owning responsibility, per SendCommandFunc's doc
// comment.
func (s *Screen) Subscribe(send SendCommandFunc) error {
	// SEC-020: no mu.Lock() below (correlationID never changes after
	// construction), but Subscribe still reads receiver fields, so it
	// still gets the guard — mirrors MapScreen.Subscribe exactly.
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
// MET-V101 — never applied, never a panic (SF-7). A malformed patch is
// logged via MET-V100 and dropped; the screen's data keeps its
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

	if p.Contracts != nil {
		s.contracts = contractsFromWire(*p.Contracts)
		s.haveContracts = true
	} else {
		s.contracts = nil
		s.haveContracts = false
	}
	if p.Junctions != nil {
		s.junctions = junctionsFromWire(*p.Junctions)
		s.haveJunctions = true
	} else {
		s.junctions = nil
		s.haveJunctions = false
	}
	if p.Warehouse != nil {
		s.warehouse = warehouseFromWire(*p.Warehouse)
		s.haveWarehouse = true
	} else {
		s.warehouse = nil
		s.haveWarehouse = false
	}
	if p.Port != nil {
		port := portFromWire(*p.Port)
		s.port = &port
		s.havePort = true
	} else {
		s.port = nil
		s.havePort = false
	}
	if p.Balance != nil {
		balance := balanceFromWire(*p.Balance)
		s.balance = &balance
		s.haveBalance = true
	} else {
		s.balance = nil
		s.haveBalance = false
	}
	if p.Safety != nil {
		s.safety = safetyFromWire(*p.Safety)
		s.haveSafety = true
	} else {
		s.safety = nil
		s.haveSafety = false
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

// HaveData reports whether any "f5.trade" patch has been applied yet.
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

// Contracts returns the current import contracts (TRD-1), in the order the
// engine sent them (a fixed producer order, deterministic per GR#21). The
// returned slice is a copy — the caller owns it and cannot corrupt the
// Screen's stored state. have is false until a patch has delivered the
// contracts sub-surface.
func (s *Screen) Contracts() (contracts []ImportContract, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Contracts"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Contracts"}); err != nil {
		return nil, false
	}
	return cloneContracts(s.contracts), s.haveContracts
}

// Junctions returns the current junction queue live view (TRD-2), in the
// engine's order. The returned slice and every nested Approaches slice are
// deep copies. have is false until a patch has delivered the junctions
// sub-surface.
func (s *Screen) Junctions() (junctions []JunctionQueue, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Junctions"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Junctions"}); err != nil {
		return nil, false
	}
	return cloneJunctions(s.junctions), s.haveJunctions
}

// Warehouse returns the current per-commodity warehouse stock/buffer rows
// (TRD-3), in the engine's order. The returned slice is a copy. have is
// false until a patch has delivered the warehouse sub-surface.
func (s *Screen) Warehouse() (rows []WarehouseCommodity, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Warehouse"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Warehouse"}); err != nil {
		return nil, false
	}
	return cloneWarehouse(s.warehouse), s.haveWarehouse
}

// Port returns the current port panel state (TRD-4). have is false until a
// patch has delivered the port sub-surface (the port is then "unavailable",
// distinct from an unlocked==false "not yet unlocked" state). The returned
// value is a copy.
func (s *Screen) Port() (port PortState, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Port"}); err != nil {
		return PortState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Port"}); err != nil {
		return PortState{}, false
	}
	if s.port == nil {
		return PortState{}, false
	}
	return *s.port, s.havePort
}

// Balance returns the current balance-of-trade breakdown (TRD-5). have is
// false until a patch has delivered the balance sub-surface. The returned
// value and its nested slices are deep copies.
func (s *Screen) Balance() (balance BalanceOfTradeView, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Balance"}); err != nil {
		return BalanceOfTradeView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Balance"}); err != nil {
		return BalanceOfTradeView{}, false
	}
	if s.balance == nil {
		return BalanceOfTradeView{}, false
	}
	return cloneBalance(*s.balance), s.haveBalance
}

// Safety returns the current pipeline-vs-truck safety corridors (TRD-6),
// in the engine's order. have is false until a patch has delivered the
// safety sub-surface — which, per the BUG-058 candidate, is not yet a
// registered outbound edge, so the render path shows "unavailable" when
// have is false (SF-7/TRD-8). The returned slice is a copy.
func (s *Screen) Safety() (corridors []SafetyCorridor, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Safety"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Safety"}); err != nil {
		return nil, false
	}
	return cloneSafety(s.safety), s.haveSafety
}

// --- wire -> domain conversion ---------------------------------------

func contractsFromWire(ws []wireContract) []ImportContract {
	out := make([]ImportContract, 0, len(ws))
	for _, w := range ws {
		out = append(out, ImportContract{
			ID:                             w.ID,
			Commodity:                      w.Commodity,
			TermMonths:                     w.TermMonths,
			MonthsRemaining:                w.MonthsRemaining,
			CancellationPenaltyMicropounds: w.CancellationPenaltyMicropounds,
			PricePerUnitMicropounds:        w.PricePerUnitMicropounds,
			Status:                         decodeContractStatus(w.Status),
		})
	}
	return out
}

func junctionsFromWire(ws []wireJunction) []JunctionQueue {
	out := make([]JunctionQueue, 0, len(ws))
	for _, w := range ws {
		j := JunctionQueue{JunctionID: w.JunctionID, Label: w.Label}
		for _, wa := range w.Approaches {
			j.Approaches = append(j.Approaches, JunctionApproach{
				ApproachID:  wa.ApproachID,
				Cargo:       decodeCargo(wa.Cargo),
				TruckCount:  wa.TruckCount,
				WaitSeconds: wa.WaitSeconds,
			})
		}
		out = append(out, j)
	}
	return out
}

func warehouseFromWire(ws []wireWarehouse) []WarehouseCommodity {
	out := make([]WarehouseCommodity, 0, len(ws))
	for _, w := range ws {
		out = append(out, WarehouseCommodity(w))
	}
	return out
}

func portFromWire(w wirePort) PortState {
	return PortState(w)
}

func ledgerFromWire(w *wireLedger) TradeLedgerView {
	if w == nil {
		return TradeLedgerView{}
	}
	var byCommodity []TradeFlow
	var byArtery []TradeFlow
	if w.ByCommodity != nil {
		byCommodity = make([]TradeFlow, 0, len(w.ByCommodity))
		for _, f := range w.ByCommodity {
			byCommodity = append(byCommodity, TradeFlow(f))
		}
	}
	if w.ByArtery != nil {
		byArtery = make([]TradeFlow, 0, len(w.ByArtery))
		for _, f := range w.ByArtery {
			byArtery = append(byArtery, TradeFlow(f))
		}
	}
	return TradeLedgerView{ByCommodity: byCommodity, ByArtery: byArtery}
}

func balanceFromWire(w wireBalance) BalanceOfTradeView {
	return BalanceOfTradeView{
		Imports: ledgerFromWire(w.Imports),
		Exports: ledgerFromWire(w.Exports),
	}
}

func safetyFromWire(w wireSafety) []SafetyCorridor {
	out := make([]SafetyCorridor, 0, len(w.Corridors))
	for _, c := range w.Corridors {
		out = append(out, SafetyCorridor(c))
	}
	return out
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

func cloneContracts(cs []ImportContract) []ImportContract { return cloneSlice(cs) }

func cloneJunctions(js []JunctionQueue) []JunctionQueue {
	out := cloneSlice(js)
	for i := range out {
		out[i].Approaches = cloneSlice(out[i].Approaches)
	}
	return out
}

func cloneWarehouse(ws []WarehouseCommodity) []WarehouseCommodity { return cloneSlice(ws) }

func cloneBalance(b BalanceOfTradeView) BalanceOfTradeView {
	b.Imports.ByCommodity = cloneSlice(b.Imports.ByCommodity)
	b.Imports.ByArtery = cloneSlice(b.Imports.ByArtery)
	b.Exports.ByCommodity = cloneSlice(b.Exports.ByCommodity)
	b.Exports.ByArtery = cloneSlice(b.Exports.ByArtery)
	return b
}

func cloneSafety(cs []SafetyCorridor) []SafetyCorridor { return cloneSlice(cs) }

// --- error trapping (GR#1/GR#7) ---------------------------------------

// logMalformed/logUnknownSubscription are unexported and only ever called
// from ApplyDelta, which already guard-validates the receiver on the way
// in. They still call checkNotCopied themselves: astgate's syntactic,
// no-call-graph scan cannot see that reachability relationship, so an
// unguarded method here would surface as a NEW ratchet violation rather
// than the already-accepted demo.Screen/proj.Screen shape. The guard is
// lock-free (one atomic pointer load) and returns nil on the only path
// that reaches it — cheap defense-in-depth, not a second lock domain.
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
