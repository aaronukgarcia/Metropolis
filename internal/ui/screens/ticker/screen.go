package ticker

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SendCommandFunc issues one protocol.Command toward the engine. Screen
// never holds a protocol.Transport itself — mirrors
// internal/ui/screens/demo's SendCommandFunc convention exactly
// (SF-1/SF-4): the caller owns the transport and the
// CorrelationID-to-SubscriptionID bookkeeping, and hands the resulting
// SubscriptionID back to this screen via BindSubscription.
type SendCommandFunc func(protocol.Command) error

// Screen is F9, the Ticker & History screen: see doc.go for the full
// package contract. The zero Screen is not ready to use; construct with
// New.
//
// Concurrency: every exported method locks mu, so ApplyDelta (called from
// the delta-applying goroutine) and the Render*/accessor/search methods
// (called from the render goroutine) may run concurrently (SF-9).
//
// Copy safety (SEC-020): mu is a sync.Mutex VALUE — a struct copy
// `s2 := *s` gets its own, independent lock — while subs/stale (maps) and
// ticker/bulletin/annual/archive/searchMatches (slices) are reference
// types a copy ALIASES. self (below) plus checkNotCopied (copyguard.go)
// reject every exported method call made on such a copy before mu is ever
// touched, mirroring demo.Screen.self/MapScreen.self exactly (GR#3).
type Screen struct {
	mu sync.Mutex

	// self holds the address New gave this Screen at construction
	// (self.Store(s), set once, at the end of New, never stored to
	// again). See copyguard.go's checkNotCopied for the full rationale.
	self atomic.Pointer[Screen]

	correlationID string

	// subs maps a bound SubscriptionID to the view name it was bound to
	// (BindSubscription) — the lookup ApplyDelta uses to route (and to
	// reject an unknown/stale SubscriptionID per SF-7).
	subs map[protocol.SubscriptionID]string

	// stale mirrors ui.core's per-subscription staleness flag, keyed by
	// view name (SetStale).
	stale map[string]bool

	// f9.ticker: the rolling window of atomic events, newest last, in the
	// engine's order.
	haveTicker bool
	ticker     []Story

	// f9.bulletin: the current month's 3–5 salience-ranked stories plus
	// the month they belong to. The 3–5 selection and salience ranking
	// are the engine editor's job (out of scope); this screen renders
	// whatever the engine selected, ordered by Rank.
	haveBulletin  bool
	bulletinMonth int64
	bulletin      []BulletinStory

	// f9.annual: the year-in-numbers + biggest-story review.
	haveAnnual bool
	annual     AnnualReview

	// f9.archive: the full searchable history, append-only, in engine
	// (chronological) order. This is the single store TIK-6 names as the
	// epilogue's data source — there is no second copy anywhere in this
	// package; search and the epilogue-source path both read it via the
	// same Archive() accessor.
	haveArchive bool
	archive     []Story

	// archiveStalled is true once an f9.archive patch has been dropped
	// because its full-snapshot payload exceeded maxPatchWireBytes — the
	// city's atomic-event history outgrew this screen's wire ceiling, so
	// the last-known-good archive is frozen. Surfaced via ArchiveStalled()
	// and a distinct render banner, never a silent freeze (SEC-072/GR#17).
	// Cleared again if a later patch fits under the ceiling and applies.
	archiveStalled bool

	// scrollStep is the deterministic ticker-scroll position (TIK-1),
	// advanced by AdvanceScroll and read by Ticker via scrollPosition
	// (scroll.go). Never wall-clock-derived (SF-8).
	scrollStep int

	// search state (TIK-4/TIK-8), see search.go.
	searchQuery   string
	searchMatches []Story
	searchPos     int
	searchActive  bool
}

// New constructs an empty Screen (no data applied yet). correlationID is
// used for this screen's own registry-sourced log entries (malformed
// patches, unknown subscriptions, missing event IDs — GR#1) and as the
// CorrelationID on every Subscribe command Subscribe sends; pass
// errs.NewCorrelationID() if the caller has no more specific ID to thread
// through.
func New(correlationID string) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
		stale:         make(map[string]bool),
	}
	// Stored exactly once, here, before s is returned to any caller — no
	// goroutine can have a reference to s to race this Store against
	// (SEC-016; see copyguard.go's self doc comment).
	s.self.Store(s)
	return s
}

// Subscribe sends a Subscribe command for view via send (SF-1). view must
// be one of ViewTicker/ViewBulletin/ViewAnnual/ViewArchive — any other
// value is rejected with MET-U702 and no command is sent.
func (s *Screen) Subscribe(view string, send SendCommandFunc) error {
	// SEC-020: no mu.Lock() below (correlationID never changes after
	// construction), but Subscribe still reads receiver fields, so it
	// still gets the guard — mirrors demo.Screen.Subscribe exactly.
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

// SubscribeAll issues Subscribe for all four views this screen owns, in a
// fixed deterministic order (ticker, bulletin, annual, archive) — the
// convenience form a composition root calls once at screen construction.
// Returns the first error encountered, if any; earlier Subscribe calls
// that already succeeded are not rolled back (mirrors demo.Screen.
// SubscribeAll).
func (s *Screen) SubscribeAll(send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SubscribeAll"}); err != nil {
		return err
	}
	for _, v := range []string{ViewTicker, ViewBulletin, ViewAnnual, ViewArchive} {
		if err := s.Subscribe(v, send); err != nil {
			return err
		}
	}
	return nil
}

// BindSubscription records that id (the SubscriptionID the engine
// allocated in response to a prior Subscribe(view, ...) call) belongs to
// view. ApplyDelta uses this binding to route/validate incoming Deltas.
// view must be one of ViewTicker/ViewBulletin/ViewAnnual/ViewArchive —
// any other value is rejected with MET-U702 (ErrUnrecognisedView) and no
// binding is recorded, mirroring Subscribe's validation (SEC-073): a
// bound-to-bogus view would otherwise be silently dropped by ApplyDelta's
// routing switch with no registry error. Rebinding an id to a different
// known view simply overwrites the previous binding.
func (s *Screen) BindSubscription(view string, id protocol.SubscriptionID) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return err
	}
	if !knownViews[view] {
		return errs.New(ErrUnrecognisedView, s.correlationID, map[string]any{"view": view})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return err
	}
	s.subs[id] = view
	return nil
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

// ApplyDelta routes delta to the view its SubscriptionID is bound to and
// applies its Patch. A Delta for an unbound (unknown/stale)
// SubscriptionID is dropped and logged via MET-U701 — never applied,
// never a panic (SF-7).
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

	// SEC-020 enumeration note: applyTicker/applyBulletin/applyAnnual/
	// applyArchive (below) are unexported and reachable only from here,
	// through this already-guarded ApplyDelta — never directly by any
	// external holder of a *Screen, copy or not (mirrors demo.Screen's
	// identical precedent for its applyPopulation/... helpers).
	switch view {
	case ViewTicker:
		s.applyTicker(delta.Patch)
	case ViewBulletin:
		s.applyBulletin(delta.Patch)
	case ViewAnnual:
		s.applyAnnual(delta.Patch)
	case ViewArchive:
		s.applyArchive(delta.Patch)
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

// --- f9.ticker --------------------------------------------------------

func (s *Screen) applyTicker(raw json.RawMessage) {
	var p wireTickerPatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewTicker, err)
		return
	}
	events := make([]Story, 0, len(p.Events))
	for _, w := range p.Events {
		st, ok := s.checkStory(ViewTicker, w)
		if !ok {
			continue
		}
		events = append(events, st)
	}
	s.mu.Lock()
	s.ticker = events
	s.haveTicker = true
	s.mu.Unlock()
}

// Ticker returns the current rolling ticker events, newest last, in the
// engine's order. haveTicker is false until the first "f9.ticker" patch
// has been applied — the render path uses that to show TIK-7's "no news
// feed" state rather than an empty scroll.
func (s *Screen) Ticker() (events []Story, haveTicker bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Ticker"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Ticker"}); err != nil {
		return nil, false
	}
	out := make([]Story, len(s.ticker))
	copy(out, s.ticker)
	return out, s.haveTicker
}

// AdvanceScroll advances the deterministic ticker-scroll position by
// delta steps (TIK-1's "ticker scroll" motion, driven by the caller's
// render tick, never the wall clock — SF-8). Negative delta scrolls
// backward; the step is clamped so it never goes negative (scrollPosition
// wraps it forward through the list).
func (s *Screen) AdvanceScroll(delta int) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "AdvanceScroll"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "AdvanceScroll"}); err != nil {
		return
	}
	s.scrollStep += delta
	if s.scrollStep < 0 {
		s.scrollStep = 0
	}
}

// ScrollStep returns the current deterministic ticker-scroll position.
func (s *Screen) ScrollStep() int {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ScrollStep"}); err != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ScrollStep"}); err != nil {
		return 0
	}
	return s.scrollStep
}

// --- f9.bulletin -------------------------------------------------------

func (s *Screen) applyBulletin(raw json.RawMessage) {
	var p wireBulletinPatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewBulletin, err)
		return
	}
	stories := make([]BulletinStory, 0, len(p.Stories))
	for _, w := range p.Stories {
		st, ok := s.checkStory(ViewBulletin, w.wireStory)
		if !ok {
			continue
		}
		stories = append(stories, BulletinStory{Story: st, Salience: w.Salience, Rank: w.Rank})
	}
	s.mu.Lock()
	s.bulletinMonth = p.Month
	s.bulletin = stories
	s.haveBulletin = true
	s.mu.Unlock()
}

// Bulletin returns the current month's bulletin stories, sorted by the
// engine editor's Rank ascending (tie-broken by EventID for a
// deterministic order even if the engine sent equal ranks — GR#21), plus
// the month they belong to. haveBulletin is false until the first
// "f9.bulletin" patch has been applied.
func (s *Screen) Bulletin() (stories []BulletinStory, month int64, haveBulletin bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Bulletin"}); err != nil {
		return nil, 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Bulletin"}); err != nil {
		return nil, 0, false
	}
	out := make([]BulletinStory, len(s.bulletin))
	copy(out, s.bulletin)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].EventID < out[j].EventID
	})
	return out, s.bulletinMonth, s.haveBulletin
}

// --- f9.annual ---------------------------------------------------------

func (s *Screen) applyAnnual(raw json.RawMessage) {
	var p wireAnnualPatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewAnnual, err)
		return
	}
	review := AnnualReview{Year: p.Year}
	for _, n := range p.Numbers {
		review.Numbers = append(review.Numbers, AnnualNumber(n))
	}
	if p.BiggestStory != nil {
		if st, ok := s.checkStory(ViewAnnual, *p.BiggestStory); ok {
			review.BiggestStory = st
			review.HasBiggest = true
		}
	}
	s.mu.Lock()
	s.annual = review
	s.haveAnnual = true
	s.mu.Unlock()
}

// Annual returns the current annual review (year in numbers + biggest
// story). haveAnnual is false until the first "f9.annual" patch has been
// applied.
func (s *Screen) Annual() (review AnnualReview, haveAnnual bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Annual"}); err != nil {
		return AnnualReview{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Annual"}); err != nil {
		return AnnualReview{}, false
	}
	review = s.annual
	review.Numbers = make([]AnnualNumber, len(s.annual.Numbers))
	copy(review.Numbers, s.annual.Numbers)
	return review, s.haveAnnual
}

// --- f9.archive --------------------------------------------------------

func (s *Screen) applyArchive(raw json.RawMessage) {
	var p wireArchivePatch
	if err := decodeWirePatch(raw, &p); err != nil {
		// SEC-072: an oversized full-snapshot archive patch is not a
		// transient malformed-patch drop — the archive has legitimately
		// outgrown the wire ceiling and every later snapshot will too, so
		// the last-known-good archive is frozen. Surface that distinctly
		// (GR#17) via archiveStalled + a registry-sourced MET-U705 error,
		// rather than silently keeping haveArchive=true forever.
		var tooLarge errPatchTooLargeError
		if errors.As(err, &tooLarge) {
			s.mu.Lock()
			s.archiveStalled = true
			s.mu.Unlock()
			_ = errs.New(ErrArchiveStopped, s.correlationID, map[string]any{
				"view":  ViewArchive,
				"bytes": tooLarge.gotBytes,
				"limit": tooLarge.maxBytes,
			})
			return
		}
		s.logMalformed(ViewArchive, err)
		return
	}
	stories := make([]Story, 0, len(p.Stories))
	for _, w := range p.Stories {
		st, ok := s.checkStory(ViewArchive, w)
		if !ok {
			continue
		}
		stories = append(stories, st)
	}
	s.mu.Lock()
	s.archive = stories
	s.haveArchive = true
	s.archiveStalled = false
	// SEC-074: the search selection was computed against the previous
	// archive and is now stale relative to Archive(); invalidate it rather
	// than let SearchMatchedCount/NextMatch/CurrentMatch serve a snapshot
	// that no longer agrees with the fresh store.
	s.searchQuery = ""
	s.searchMatches = nil
	s.searchPos = -1
	s.searchActive = false
	s.mu.Unlock()
}

// Archive returns the full history archive — the single store TIK-4's
// search indexes and TIK-6 names as the epilogue's data source. There is
// exactly one archive in this package; both the search path (search.go)
// and any epilogue consumer read it through this same accessor, never a
// second copy (GR#3). haveArchive is false until the first "f9.archive"
// patch has been applied.
func (s *Screen) Archive() (stories []Story, haveArchive bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Archive"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Archive"}); err != nil {
		return nil, false
	}
	out := make([]Story, len(s.archive))
	copy(out, s.archive)
	return out, s.haveArchive
}

// ArchiveStalled reports whether the archive has stopped updating: an
// f9.archive patch arrived whose full-snapshot payload exceeded this
// screen's wire ceiling (maxPatchWireBytes), so the last-known-good
// archive is frozen. The render path surfaces this as a distinct banner
// (GR#17 — never a silent freeze). False before any archive patch, false
// while patches keep fitting under the ceiling, and false again if a
// later patch fits and applies.
func (s *Screen) ArchiveStalled() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ArchiveStalled"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ArchiveStalled"}); err != nil {
		return false
	}
	return s.archiveStalled
}

// --- TIK-5 structural guard + error trapping --------------------------

// checkStory converts one wire story to a Story, rejecting (and logging
// via MET-U703) a story whose eventId is empty after trimming — TIK-5's
// structural "no hallucinated news" control: a story with no backing event
// ID (including a whitespace-only one — SEC-076, which would otherwise be
// carried into dash.DrillTarget.EntityID as an untraceable source) is
// refused at the patch boundary, never rendered, even if its prose reads
// plausibly. It reads only s.correlationID (immutable), so it is safe to
// call without mu held.
func (s *Screen) checkStory(view string, w wireStory) (Story, bool) {
	if strings.TrimSpace(w.EventID) == "" {
		_ = errs.New(ErrMissingEventID, s.correlationID, map[string]any{"view": view})
		return Story{}, false
	}
	return Story(w), true
}

// logMalformed/logUnknownSubscription read only s.correlationID (set
// once in New), so they are safe to call without mu held (same posture as
// demo.Screen.logMalformed/logUnknownSubscription — no mutable aliased
// state is touched).
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
