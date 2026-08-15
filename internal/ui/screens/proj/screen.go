package proj

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SendCommandFunc issues one protocol.Command toward the engine. Screen
// never holds a protocol.Transport itself — mirrors ui.screen.map's and
// ui.screen.demo's convention exactly (SF-1/SF-4): the caller owns the
// transport and the CorrelationID-to-SubscriptionID bookkeeping, and
// hands the resulting SubscriptionID back to this screen via
// BindSubscription.
type SendCommandFunc func(protocol.Command) error

// Screen is F7, the Projections screen: see doc.go for the full package
// contract. The zero Screen is not ready to use; construct with New.
//
// Concurrency: every exported method locks mu, so ApplyDelta (called
// from the delta-applying goroutine) and the accessor methods (called
// from the render goroutine) may run concurrently (SF-9).
//
// Copy safety (SEC-020): mu is a sync.Mutex VALUE — a struct copy
// `s2 := *s` gets its own, independent lock — while subs (map) and
// curves/crossings (slices) are reference types a copy ALIASES. self
// (below) plus checkNotCopied (copyguard.go) reject every exported method
// call made on such a copy before mu is ever touched, mirroring
// MapScreen.self/demo.Screen.self exactly (GR#3).
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
	// multi-view screen so a future second F7 view needs no structural
	// change.
	subs map[protocol.SubscriptionID]string

	// stale mirrors ui.core's per-subscription staleness flag for this
	// screen's single view (SetStale).
	stale bool

	// haveData is false until the first "f7.projections" patch has been
	// applied.
	haveData      bool
	horizonMonths int
	curves        []Curve
	crossings     []Crossing
	rateOutlook   *RateOutlook // nil until the first patch carrying it
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

// Subscribe sends the "f7.projections" Subscribe command via send
// (SF-1). It does not block on or read any CommandResult/Delta — that is
// the caller's transport-owning responsibility, per SendCommandFunc's doc
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
// screen's view. ApplyDelta uses this binding to validate incoming
// Deltas.
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
// subsequently-arriving stale Delta for it is treated as unknown
// (SF-7) rather than accidentally still routed.
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
// MET-V002 — never applied, never a panic (SF-7). A malformed patch is
// logged via MET-V001 and dropped; the screen's data keeps its
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

	curves := make([]Curve, len(p.Curves))
	for i, wc := range p.Curves {
		curves[i] = curveFromWire(wc)
	}
	crossings := make([]Crossing, len(p.Crossings))
	for i, wx := range p.Crossings {
		crossings[i] = crossingFromWire(wx)
	}
	var rate *RateOutlook
	if p.RateOutlook != nil {
		r := rateFromWire(*p.RateOutlook)
		rate = &r
	}

	s.mu.Lock()
	s.horizonMonths = p.HorizonMonths
	s.curves = curves
	s.crossings = crossings
	s.rateOutlook = rate
	s.haveData = true
	s.mu.Unlock()
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

// Curves returns the current demand/supply curves (PRJ-1), in the order
// the engine sent them (a fixed producer order, deterministic per GR#21).
// The returned slice and every nested series are deep copies — the caller
// owns them and cannot corrupt the Screen's stored state (SEC-062).
// haveData is false until the first "f7.projections" patch has been
// applied.
func (s *Screen) Curves() (curves []Curve, haveData bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Curves"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Curves"}); err != nil {
		return nil, false
	}
	out := make([]Curve, len(s.curves))
	for i := range s.curves {
		out[i] = cloneCurve(s.curves[i])
	}
	return out, s.haveData
}

// Crossings returns the current contracted-vs-internal demand crossing
// charts (PRJ-3), in the order the engine sent them. The returned slice
// and both nested series are deep copies — the caller owns them and cannot
// corrupt the Screen's stored state (SEC-062).
func (s *Screen) Crossings() (crossings []Crossing, haveData bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Crossings"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Crossings"}); err != nil {
		return nil, false
	}
	out := make([]Crossing, len(s.crossings))
	for i := range s.crossings {
		out[i] = cloneCrossing(s.crossings[i])
	}
	return out, s.haveData
}

// RateOutlook returns the §45 national base-rate outlook curve (PRJ-4).
// The returned series are deep copies — the caller owns them and cannot
// corrupt the Screen's stored state (SEC-062). ok is false until the first
// patch carrying rateOutlook has been applied (or if the view never
// supplies one); haveData is this screen's overall data flag.
func (s *Screen) RateOutlook() (rate RateOutlook, ok, haveData bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RateOutlook"}); err != nil {
		return RateOutlook{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RateOutlook"}); err != nil {
		return RateOutlook{}, false, false
	}
	if s.rateOutlook == nil {
		return RateOutlook{}, false, s.haveData
	}
	return cloneRateOutlook(*s.rateOutlook), true, s.haveData
}

// HorizonMonths returns the forecast horizon N the view reported
// (PRJ-2), read from "f7.projections" horizonMonths — never a hardcoded
// literal (GR#15). ok is false until the first patch has been applied.
func (s *Screen) HorizonMonths() (months int, ok bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HorizonMonths"}); err != nil {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HorizonMonths"}); err != nil {
		return 0, false
	}
	return s.horizonMonths, s.haveData
}

// --- defensive copies (SEC-062 / GR#16) ------------------------------
//
// Curves/Crossings/RateOutlook hand their state back to callers as
// snapshots: the outer slice is copied AND every nested slice is given a
// fresh backing array, so a caller that mutates, sorts, truncates or
// rewrites a returned series owns only the copy and cannot corrupt the
// Screen's stored state. The pre-fix accessors copied the outer slice but
// aliased History/Projection/ConfidenceUpper/ConfidenceLower/Thresholds/
// Markers (and Crossing's/RateOutlook's series) — mutating curves[0].
// History[0] through the returned handle changed the screen (SEC-062).
// cloneSlice is the same make+copy defensive-copy convention ui.screen.
// demo's accessors use, generalised over the element type because proj's
// domain structs carry nested slices (demo's do not).

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

// cloneCurve returns a deep copy of c: every slice field is reallocated.
// Key/Label/Status/UnavailableReason are value types (strings are
// immutable) and need no copying.
func cloneCurve(c Curve) Curve {
	c.History = cloneSlice(c.History)
	c.Projection = cloneSlice(c.Projection)
	c.ConfidenceUpper = cloneSlice(c.ConfidenceUpper)
	c.ConfidenceLower = cloneSlice(c.ConfidenceLower)
	c.Thresholds = cloneSlice(c.Thresholds)
	c.Markers = cloneSlice(c.Markers)
	return c
}

// cloneCrossing returns a deep copy of x: both series are reallocated.
func cloneCrossing(x Crossing) Crossing {
	x.InternalDemand = cloneSlice(x.InternalDemand)
	x.ContractedCapacity = cloneSlice(x.ContractedCapacity)
	return x
}

// cloneRateOutlook returns a deep copy of r: both series are reallocated.
func cloneRateOutlook(r RateOutlook) RateOutlook {
	r.History = cloneSlice(r.History)
	r.Projection = cloneSlice(r.Projection)
	return r
}

// --- wire -> domain conversion ---------------------------------------

func curveFromWire(w wireCurve) Curve {
	c := Curve{
		Key:               w.Key,
		Label:             w.Label,
		Status:            decodeStatus(w.Status),
		UnavailableReason: w.UnavailableReason,
		History:           w.History,
		Projection:        w.Projection,
		ConfidenceUpper:   w.ConfidenceUpper,
		ConfidenceLower:   w.ConfidenceLower,
	}
	for _, wt := range w.Thresholds {
		c.Thresholds = append(c.Thresholds, Threshold(wt))
	}
	for _, wm := range w.Markers {
		c.Markers = append(c.Markers, DecisionMarker(wm))
	}
	if c.UnavailableReason == "" && c.Status != StatusAvailable {
		// An unavailable/unlocked curve with no producer-supplied reason
		// still gets a human-readable default rather than a blank line
		// (PRJ-6's "per the reason" — a missing reason is itself shown as
		// "no reason supplied", never a blank).
		if c.Status == StatusNotUnlocked {
			c.UnavailableReason = "forecasting tier not yet unlocked"
		} else {
			c.UnavailableReason = "source view has not delivered data"
		}
	}
	return c
}

func crossingFromWire(w wireCrossing) Crossing {
	x := Crossing{
		Key:                w.Key,
		Label:              w.Label,
		Status:             decodeStatus(w.Status),
		UnavailableReason:  w.UnavailableReason,
		InternalDemand:     w.InternalDemand,
		ContractedCapacity: w.ContractedCapacity,
		CrossingMonth:      w.CrossingMonth,
	}
	if x.UnavailableReason == "" && x.Status != StatusAvailable {
		if x.Status == StatusNotUnlocked {
			x.UnavailableReason = "forecasting tier not yet unlocked"
		} else {
			x.UnavailableReason = "source view has not delivered data"
		}
	}
	return x
}

func rateFromWire(w wireRateOutlook) RateOutlook {
	r := RateOutlook{
		Status:            decodeStatus(w.Status),
		UnavailableReason: w.UnavailableReason,
		History:           w.History,
		Projection:        w.Projection,
	}
	if r.UnavailableReason == "" && r.Status != StatusAvailable {
		if r.Status == StatusNotUnlocked {
			r.UnavailableReason = "forecasting tier not yet unlocked"
		} else {
			r.UnavailableReason = "source view has not delivered data"
		}
	}
	return r
}

// --- error trapping (GR#1/GR#7) ---------------------------------------

// logMalformed/logUnknownSubscription are unexported and only ever called
// from ApplyDelta, which already guard-validates the receiver on the way
// in. They still call checkNotCopied themselves: astgate's syntactic,
// no-call-graph scan (internal/foundation/astgate) cannot see that
// reachability relationship, so an unguarded method here would surface as
// a NEW ratchet violation rather than the already-accepted demo.Screen
// shape. The guard is lock-free (one atomic pointer load) and returns nil
// on the only path that reaches it — cheap defense-in-depth, not a second
// lock domain.
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
