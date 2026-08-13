package demo

import (
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SendCommandFunc issues one protocol.Command toward the engine. Screen
// never holds a protocol.Transport itself — mirrors
// internal/ui/screens/map's SendCommandFunc/MapScreen.Subscribe
// convention exactly (SF-1/SF-4): the caller owns the transport and the
// CorrelationID-to-SubscriptionID bookkeeping, and hands the resulting
// SubscriptionID back to this screen via BindSubscription.
type SendCommandFunc func(protocol.Command) error

// Screen is F6, the Demographics screen: see doc.go for the full package
// contract. The zero Screen is not ready to use; construct with New.
//
// Concurrency: every exported method locks mu, so ApplyDelta (called
// from the delta-applying goroutine) and the Render*/accessor methods
// (called from the render goroutine) may run concurrently (SF-9).
//
// Copy safety (SEC-020, BOW FEAT-018 Destructive round, Widowmaker
// REJECT): mu is a sync.Mutex VALUE — a struct copy `s2 := *s` gets its
// own, independent lock — while subs/stale/typologies (maps) and
// ageMonths/personality/hoursByActivity/leisureTaste/typologyOrder
// (slices) are reference types a copy ALIASES. self (below) plus
// checkNotCopied (copyguard.go) reject every exported method call made
// on such a copy before mu is ever touched, mirroring
// MapScreen.self/debug.Screen.self exactly (GR#3).
type Screen struct {
	mu sync.Mutex

	// self holds the address New gave this Screen at construction
	// (self.Store(s), set once, at the end of New, never stored to
	// again). See copyguard.go's checkNotCopied for the full rationale.
	self atomic.Pointer[Screen]

	correlationID string

	// subs maps a bound SubscriptionID to the view name it was bound to
	// (BindSubscription) — the lookup ApplyDelta uses to route (and to
	// reject an unknown/stale SubscriptionID per SF-7/DEMO-9).
	subs map[protocol.SubscriptionID]string

	// stale mirrors ui.core's per-subscription staleness flag, keyed by
	// view name (SetStale).
	stale map[string]bool

	havePopulation bool
	ageMonths      []AgeBucket
	personality    []TraitBucket

	haveLeisure     bool
	hoursByActivity []ActivityHours
	leisureTaste    []TasteBucket

	haveHousing bool
	// typologyOrder preserves first-seen order (deterministic render
	// order, GR#21) independent of Go's randomised map iteration.
	typologyOrder []string
	typologies    map[string]TypologyRow

	haveCommute bool
	commute     CommuteFigures
}

// New constructs an empty Screen (no data applied yet). correlationID is
// used for this screen's own registry-sourced log entries (malformed
// patches, unknown subscriptions — GR#1) and as the CorrelationID on
// every Subscribe command Subscribe sends; pass errs.NewCorrelationID()
// if the caller has no more specific ID to thread through.
func New(correlationID string) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
		stale:         make(map[string]bool),
		typologies:    make(map[string]TypologyRow),
	}
	// Stored exactly once, here, before s is returned to any caller — no
	// goroutine can have a reference to s to race this Store against
	// (SEC-016; see copyguard.go's self doc comment).
	s.self.Store(s)
	return s
}

// Subscribe sends a Subscribe command for view via send (SF-1). view must
// be one of ViewPopulation/ViewLeisure/ViewHousing/ViewCommute — any
// other value is rejected with MET-U502 and no command is sent.
func (s *Screen) Subscribe(view string, send SendCommandFunc) error {
	// SEC-020: no mu.Lock() below (correlationID never changes after
	// construction), but Subscribe still reads receiver fields, so it
	// still gets the guard — mirrors MapScreen.Subscribe exactly.
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	if !knownViews[view] {
		return errs.New(ErrUnrecognisedView, s.correlationID, map[string]any{"view": view})
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: view},
	}
	return send(cmd)
}

// SubscribeAll issues Subscribe for all four views this screen owns, in
// a fixed deterministic order (population, leisure, housing, commute) —
// the convenience form a composition root calls once at screen
// construction. Returns the first error encountered, if any; earlier
// Subscribe calls that already succeeded are not rolled back (mirrors
// how a partial multi-Subscribe failure is handled elsewhere — the
// caller decides whether to retry the specific failed view).
func (s *Screen) SubscribeAll(send SendCommandFunc) error {
	// SEC-020: checked here too, not only inside the per-view Subscribe
	// calls it makes — an entry point exported directly to callers (a
	// copy could be called on SubscribeAll itself) needs its own guard,
	// mirrored from the same reasoning as MapScreen.Subscribe.
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SubscribeAll"}); err != nil {
		return err
	}
	for _, v := range []string{ViewPopulation, ViewLeisure, ViewHousing, ViewCommute} {
		if err := s.Subscribe(v, send); err != nil {
			return err
		}
	}
	return nil
}

// BindSubscription records that id (the SubscriptionID the engine
// allocated in response to a prior Subscribe(view, ...) call) belongs to
// view. ApplyDelta uses this binding to route/validate incoming Deltas.
// Rebinding an id to a different view (e.g. after unsubscribe/
// resubscribe) simply overwrites the previous binding.
func (s *Screen) BindSubscription(view string, id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.subs[id] = view
}

// UnbindSubscription forgets id (e.g. after Unsubscribe) so a
// subsequently-arriving stale Delta for it is treated as unknown
// (SF-7/DEMO-9) rather than accidentally still routed.
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

// ApplyDelta routes delta to the view its SubscriptionID is bound to and
// applies its Patch. A Delta for an unbound (unknown/stale)
// SubscriptionID is dropped and logged via MET-U501 — never applied,
// never a panic (SF-7/DEMO-9).
func (s *Screen) ApplyDelta(delta protocol.Delta) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		s.mu.Unlock()
		return
	}
	view, ok := s.subs[delta.SubscriptionID]
	s.mu.Unlock()
	if !ok {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}

	// SEC-020 enumeration note: applyPopulation/applyLeisure/applyHousing/
	// applyCommute (below) are unexported and reachable only from here,
	// through this already-guarded ApplyDelta — never directly by any
	// external holder of a *Screen, copy or not (they are unexported, so
	// only this package can call them, and this package only ever calls
	// them on the same s ApplyDelta already validated). Mirrors debug.
	// Screen's collectRegistry/collectErrorTail/collectPhaseSeries/
	// collectBoW precedent (internal/ui/screens/debug/screen.go) exactly:
	// an unexported helper behind an already-guarded entry point does not
	// need its own redundant guard.
	switch view {
	case ViewPopulation:
		s.applyPopulation(delta.Patch)
	case ViewLeisure:
		s.applyLeisure(delta.Patch)
	case ViewHousing:
		s.applyHousing(delta.Patch)
	case ViewCommute:
		s.applyCommute(delta.Patch)
	}
}

// SetStale surfaces ui.core's per-subscription staleness flag for view
// (UI-SPEC §1's "staleness dot"). The caller is expected to call this
// once per render tick per bound view.
func (s *Screen) SetStale(view string, stale bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.stale[view] = stale
}

// Stale reports whether view is currently marked stale. Defaults to
// false for a view never touched by SetStale.
func (s *Screen) Stale(view string) bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	return s.stale[view]
}

// --- f6.population --------------------------------------------------

func (s *Screen) applyPopulation(raw json.RawMessage) {
	var p wirePopulationPatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewPopulation, err)
		return
	}

	ages := make([]AgeBucket, len(p.AgeMonths))
	for i, a := range p.AgeMonths {
		ages[i] = AgeBucket(a)
	}
	traits := make([]TraitBucket, len(p.Personality))
	for i, t := range p.Personality {
		traits[i] = TraitBucket(t)
	}

	s.mu.Lock()
	s.ageMonths = ages
	s.personality = traits
	s.havePopulation = true
	s.mu.Unlock()
}

// Population returns the current population pyramid rows, sorted by
// MonthAge ascending (deterministic render order regardless of the wire
// order the engine happened to send — GR#21). havePopulation is false
// until the first "f6.population" patch has been applied.
func (s *Screen) Population() (ages []AgeBucket, havePopulation bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Population"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Population"}); err != nil {
		return nil, false
	}
	out := make([]AgeBucket, len(s.ageMonths))
	copy(out, s.ageMonths)
	sort.Slice(out, func(i, j int) bool { return out[i].MonthAge < out[j].MonthAge })
	return out, s.havePopulation
}

// Personality returns the current personality-trait distribution
// (DEMO-7), in the order the engine sent it (already a small, named-
// category enumeration — no re-sort imposed here, unlike Population's
// dense month-age series).
func (s *Screen) Personality() (traits []TraitBucket, havePopulation bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Personality"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Personality"}); err != nil {
		return nil, false
	}
	out := make([]TraitBucket, len(s.personality))
	copy(out, s.personality)
	return out, s.havePopulation
}

// --- f6.leisure -------------------------------------------------------

func (s *Screen) applyLeisure(raw json.RawMessage) {
	var p wireLeisurePatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewLeisure, err)
		return
	}

	hours := make([]ActivityHours, len(p.HoursByActivity))
	for i, h := range p.HoursByActivity {
		hours[i] = ActivityHours(h)
	}
	taste := make([]TasteBucket, len(p.LeisureTaste))
	for i, t := range p.LeisureTaste {
		taste[i] = TasteBucket{Taste: t.Taste, Weight: t.Weight}
	}

	s.mu.Lock()
	s.hoursByActivity = hours
	s.leisureTaste = taste
	s.haveLeisure = true
	s.mu.Unlock()
}

// HoursByActivity returns the current §42 "how your city spends
// Saturday" breakdown (DEMO-4), in the order the engine sent it.
func (s *Screen) HoursByActivity() (hours []ActivityHours, haveLeisure bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HoursByActivity"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HoursByActivity"}); err != nil {
		return nil, false
	}
	out := make([]ActivityHours, len(s.hoursByActivity))
	copy(out, s.hoursByActivity)
	return out, s.haveLeisure
}

// LeisureTaste returns the current leisure-taste weighting distribution
// (DEMO-7).
func (s *Screen) LeisureTaste() (taste []TasteBucket, haveLeisure bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LeisureTaste"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LeisureTaste"}); err != nil {
		return nil, false
	}
	out := make([]TasteBucket, len(s.leisureTaste))
	copy(out, s.leisureTaste)
	return out, s.haveLeisure
}

// --- f6.housing -------------------------------------------------------

func (s *Screen) applyHousing(raw json.RawMessage) {
	var p wireHousingPatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewHousing, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if p.Full {
		seen := make(map[string]bool, len(p.Typologies))
		for _, t := range p.Typologies {
			seen[t.Typology] = true
			if _, known := s.typologies[t.Typology]; !known {
				s.typologyOrder = append(s.typologyOrder, t.Typology)
			}
			s.typologies[t.Typology] = TypologyRow{Typology: t.Typology, Demand: t.Demand, Stock: t.Stock}
		}
		// SF-7/DEMO-9: a typology previously known but absent from this
		// full snapshot is retired mid-game — marked, never silently kept
		// at its last stale Demand/Stock numbers and never deleted (so a
		// player can still see it existed).
		for name, row := range s.typologies {
			if !seen[name] {
				row.Retired = true
				s.typologies[name] = row
			}
		}
	} else {
		for _, t := range p.Typologies {
			if _, known := s.typologies[t.Typology]; !known {
				s.typologyOrder = append(s.typologyOrder, t.Typology)
			}
			s.typologies[t.Typology] = TypologyRow{Typology: t.Typology, Demand: t.Demand, Stock: t.Stock}
		}
	}
	s.haveHousing = true
}

// Typologies returns the current per-typology demand-vs-stock rows
// (DEMO-5), in first-seen order (deterministic — GR#21, independent of
// Go's map iteration order).
func (s *Screen) Typologies() (rows []TypologyRow, haveHousing bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Typologies"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Typologies"}); err != nil {
		return nil, false
	}
	out := make([]TypologyRow, 0, len(s.typologyOrder))
	for _, name := range s.typologyOrder {
		out = append(out, s.typologies[name])
	}
	return out, s.haveHousing
}

// --- f6.commute -------------------------------------------------------

func (s *Screen) applyCommute(raw json.RawMessage) {
	var p wireCommutePatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewCommute, err)
		return
	}

	s.mu.Lock()
	s.commute = CommuteFigures{OutCommuters: p.OutCommuters, InCommuters: p.InCommuters}
	s.haveCommute = true
	s.mu.Unlock()
}

// Commute returns the current in/out commuting-leak figures (DEMO-6).
func (s *Screen) Commute() (figures CommuteFigures, haveCommute bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Commute"}); err != nil {
		return CommuteFigures{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Commute"}); err != nil {
		return CommuteFigures{}, false
	}
	return s.commute, s.haveCommute
}

// --- error trapping (GR#1/GR#7) ---------------------------------------

// SEC-020 enumeration note: logMalformed/logUnknownSubscription (below)
// are unexported and only ever called from within applyPopulation/
// applyLeisure/applyHousing/applyCommute/ApplyDelta — all of which run
// on the same already-guard-validated *s. Neither reads mutable aliased
// state (only s.correlationID, an immutable string set once in New), so
// there is nothing here for a struct copy to corrupt even in principle;
// left unguarded on purpose, same reasoning as debug.Screen.
// collectRegistry's precedent, not silently.
func (s *Screen) logMalformed(view string, cause error) {
	_ = errs.New(ErrMalformedPatch, s.correlationID, map[string]any{
		"view":  view,
		"cause": cause.Error(),
	})
}

func (s *Screen) logUnknownSubscription(id protocol.SubscriptionID) {
	_ = errs.New(ErrUnknownSubscription, s.correlationID, map[string]any{
		"subscriptionId": string(id),
	})
}
