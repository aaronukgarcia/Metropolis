package services

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

type SendCommandFunc func(protocol.Command) error

// setFundingCommand builds the real, first-class protocol.KindSetFunding
// command (FEAT-208 increment 3, ASM-1193's own "prefer a real Kind over
// the Debug fallback long-term" ruling made concrete) — this screen no
// longer rides protocol.KindDebug's Op/Args escape hatch for SVC-1
// (superseded "services.set-funding" DebugPayload.Op convention; see
// doc.go's SVC-8 gating note for the history).
func setFundingCommand(correlationID string, serviceID string, level float64) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindSetFunding,
		Payload:         protocol.SetFundingPayload{ServiceID: serviceID, Level: level},
	}
}

// pendingFundingRequest is one in-flight SetFunding command's own
// bookkeeping, keyed by the FRESH, per-command protocol.CorrelationID
// SetFunding mints for it (FEAT-208 increment 3 destructive round r1,
// finding F-A: every SetFunding command previously reused the screen's
// single, fixed s.correlationID — the same value Subscribe uses — so two
// commands in flight at once collapsed onto router.RegisterResultHandler's
// ONE pending-result slot per CorrelationID, and the second command's
// CommandResult was silently lost as a route miss, never reaching
// ApplyResult. Each SetFunding call now mints its own ID and is tracked
// here individually until its own, and only its own, CommandResult
// arrives).
//
// No seq/issue-order field (round r2 had one; round r4 removed it —
// see fundingHasOtherOutstandingLocked's doc comment for why "am I the
// newest outstanding" was itself the round r3 bug, and a plain
// per-service outstanding PRESENCE check, no direction, is both simpler
// and correct).
type pendingFundingRequest struct {
	serviceID      string
	attemptedLevel float64
}

// fundingAdjustBaseline is RegisterFundingAdjustKeys'/SetFunding's shared
// documented starting point for a service whose funding level this
// screen has never itself confirmed or attempted (see fundingConfirmed's
// doc comment) — a neutral midpoint placeholder, not a guess at any real
// engine default (ServicesAPI's own default is not read here; SF-1
// forbids importing internal/engine).
const fundingAdjustBaseline = 0.5

// fundingPendingCap bounds s.pendingFunding (FEAT-208 increment 3
// destructive round r2, item 3: previously unbounded — a CommandResult
// that never arrives, e.g. dropped by protocol.Transport's own documented
// evict-oldest contract under load, or an engine that never responds,
// left a permanent entry forever, with no TTL or prune mechanism
// anywhere in this package). A modest, documented cap: this pilot key
// fires at most a small handful of times between renders; 32 outstanding
// requests for ALL services combined is generous headroom for that while
// still bounding worst-case memory growth over a long session. On
// overflow, SetFunding evicts the OLDEST outstanding entry (FIFO via
// pendingFundingOrder) before inserting the new one and records a loud,
// registry-sourced local failure (ErrFundingRequestEvicted, MET-V505 —
// ErrFundingCommandSendFailed's sibling) rather than silently dropping
// it: GR#1 forbids a silent give-up even when giving up is the correct
// choice.
const fundingPendingCap = 32

type Screen struct {
	mu sync.Mutex

	self atomic.Pointer[Screen]

	correlationID         string
	subs                  map[protocol.SubscriptionID]string
	stale                 bool
	haveData              bool
	fundingRejectedReason string

	// fundingLocalFailureReason surfaces a SetFunding call whose send()
	// itself failed (e.g. protocol.ErrCommandQueueFull/ErrTransportClosed
	// from transport.SendCommand) — FEAT-208 increment 3 destructive round
	// r1 finding F-B part 1: previously the adjust closure discarded
	// SetFunding's returned error entirely (`_ = s.SetFunding(...)`), so a
	// command that never even left the client was indistinguishable from
	// one that succeeded — no CommandResult can ever arrive for a command
	// that was never sent, so FundingRejectedReason() alone could never
	// surface this failure mode. Deliberately a SEPARATE field/accessor
	// from fundingRejectedReason (never merged into the same string) so a
	// caller can tell "the ENGINE rejected this" (an authoritative,
	// registry-sourced ErrorRef from a real CommandResult) apart from "this
	// never reached the engine at all" (a local transport/queue failure,
	// MET-V504) — GR#1's "selectable display" requirement applies to
	// telling these two failure classes apart, not just detecting that
	// SOMETHING went wrong.
	fundingLocalFailureReason string

	// fundingConfirmed is this screen's own client-side, per-ServiceID
	// [0,1]-domain OPTIMISTIC DISPLAY tracker (FEAT-208 increment 3
	// destructive round r1 finding F-B part 2, corrected by round r2
	// finding F-C, corrected AGAIN by round r4 — see pendingFundingRequest's
	// and fundingHasOtherOutstandingLocked's doc comments for the full
	// history): SetFunding sets it optimistically, at send time, to the
	// level just requested. Two paths correct it afterward:
	// ApplyResult's accept branch sets it UNCONDITIONALLY to match
	// lastConfirmed (round r4's "accepts are authoritative" fix — round r3's
	// design left this field un-repaired on accept, only writing
	// lastConfirmed, which round r3's own fuzz/permutation attacks proved
	// could leave a stale, un-reverted value behind); a completing
	// rejection/send-failure/eviction reverts it to lastConfirmed[serviceID]
	// via fundingRevertToLastConfirmedLocked, but ONLY when no OTHER request
	// for the same service remains outstanding at all (round r4's
	// direction-agnostic guard — round r3's "newest-outstanding" direction
	// check was itself the bug, see fundingHasOtherOutstandingLocked). This
	// field answers "what should the player see RIGHT NOW" — it is NOT the
	// engine's own ground truth (that is lastConfirmed, below). A service
	// with no entry has never been SetFunding-attempted by this screen
	// instance; fundingBaseline returns fundingAdjustBaseline for that case.
	fundingConfirmed map[string]float64

	// lastConfirmed is this screen's own per-ServiceID record of the last
	// level the ENGINE actually confirmed (an Accepted CommandResult) —
	// updated ONLY by ApplyResult's accept branch (unconditionally, for any
	// matched pendingFunding entry — round r4's "accepts are authoritative"
	// rule), NEVER optimistically, NEVER by a rejection or a local send
	// failure. This is the revert TARGET every completing request's revert
	// path (ApplyResult's rejection branch, SetFunding's send-failure
	// branch, and eviction) shares (FEAT-208 increment 3 destructive round
	// r2 finding F-C, direction-guard corrected by round r4): round r1's
	// design reverted to a per-request "priorLevel" snapshot — what THIS
	// request itself had optimistically overwritten — which is only the
	// true confirmed value for the FIRST rejected link in a chain; a SECOND
	// rejected request in the same chain reverts to the FIRST's own
	// attempted (and itself later-rejected) value, a phantom the engine
	// never confirmed. Deriving the revert target from THIS single,
	// engine-sourced map instead — never a per-request field — means every
	// completing request in a chain reverts to the SAME true ground truth,
	// so the terminal state after any number of overlapping
	// accepts/rejections/failures, completing in ANY order, is always
	// lastConfirmed once every request has resolved
	// (fundingRevertToLastConfirmedLocked's doc comment has the
	// correctness argument). A service with no entry has never had an
	// Accepted SetFunding result; the revert target for that case is
	// fundingAdjustBaseline, matching fundingConfirmed's own unset default.
	lastConfirmed map[string]float64

	// pendingFunding is every currently in-flight SetFunding command this
	// screen has sent and not yet observed a CommandResult for (or evicted
	// — see fundingPendingCap), keyed by each command's own fresh
	// CorrelationID (see pendingFundingRequest's doc comment for the F-A
	// finding this closes). Bounded to fundingPendingCap entries.
	pendingFunding map[protocol.CorrelationID]pendingFundingRequest

	// pendingFundingOrder is pendingFunding's insertion order, oldest
	// first — a plain slice, never derived from a map range (this
	// codebase's GR#21 "never a map range for anything order-sensitive"
	// discipline extended here to a UI-side FIFO, not just simulation
	// state): fundingPendingCap's eviction needs a deterministic "which
	// entry is oldest" answer, and Go map iteration order is explicitly
	// unspecified. Kept in lock-step with pendingFunding: every insert
	// into pendingFunding appends here; every removal (ApplyResult,
	// SetFunding's send-failure path, or eviction itself) removes the
	// matching entry here too.
	pendingFundingOrder []protocol.CorrelationID

	sliders        []ServiceSlider
	haveSliders    bool
	capacityDemand []CapacityDemand
	haveCapacity   bool
	responseTimes  []ResponseTimeStat
	haveResponse   bool
	waitingLists   []WaitingList
	haveWaiting    bool
	pie            *PublicServicePieView
	havePie        bool
}

func New(correlationID string) *Screen {
	s := &Screen{
		correlationID:    correlationID,
		subs:             make(map[protocol.SubscriptionID]string),
		fundingConfirmed: make(map[string]float64),
		lastConfirmed:    make(map[string]float64),
		pendingFunding:   make(map[protocol.CorrelationID]pendingFundingRequest),
	}
	s.self.Store(s)
	return s
}

func (s *Screen) Subscribe(send SendCommandFunc) error {
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

// ApplyResult surfaces a rejected funding-slider change (SVC-8): the
// engine's rejection reason (e.g. below a hard floor) is stored rather
// than the slider silently reverting with no feedback, mirroring
// finance.Screen.ApplyResult's loanRejectedReason pattern exactly.
//
// Dispatch is keyed by MEMBERSHIP in pendingFunding, never by equality
// against a single fixed CorrelationID (FEAT-208 increment 3 destructive
// round r1 finding F-A — see pendingFundingRequest's doc comment for the
// full history: a fixed, reused CorrelationID meant a second in-flight
// SetFunding command's CommandResult could never be told apart from the
// first's at all, and router.RegisterResultHandler's own one-shot
// contract meant it was consumed as a route miss before ever reaching
// here). A CorrelationID with no pendingFunding entry — a foreign result
// (e.g. this screen's own Subscribe accept, which never registers a
// pendingFunding entry), a stale/duplicate delivery, OR a genuinely
// EVICTED request's late-arriving result (fundingPendingCap, round r2 item
// 3) — is ignored, exactly as the old fixed-ID mismatch check intended,
// just checked against the right set now.
//
// Known, documented limitation (round r3's own destructive finding,
// TestAttack_EvictedRequest_LateAcceptedResult_PermanentDivergence,
// intentionally NOT closed here): protocol.CommandResult carries only
// CorrelationID/Tick/Accepted/Error (envelope.go) — no ServiceID, no
// Level. Once a request is evicted from pendingFunding, this screen has
// no way to recover WHICH service or WHAT level a late-arriving result
// (even a genuine Accept) was for; there is nothing this method can act
// on beyond the membership-miss no-op below. Round r4's "accepts are
// authoritative" fix (below) closes every case where the completing
// request is STILL a live pendingFunding entry — an evicted entry is a
// separate, information-theoretic limitation, not a case this branch
// left unhandled by oversight.
func (s *Screen) ApplyResult(res protocol.CommandResult) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}

	pending, ok := s.pendingFunding[res.CorrelationID]
	if !ok {
		return
	}
	delete(s.pendingFunding, res.CorrelationID)
	s.removePendingFundingOrderLocked(res.CorrelationID)
	// A real CommandResult arrived for a command that DID leave the
	// client, so any stale local-send-failure reason from an earlier,
	// unrelated attempt no longer describes the current state (finding
	// F-B part 1).
	s.fundingLocalFailureReason = ""

	if res.Accepted {
		s.fundingRejectedReason = ""
		// Round r4 fix: ACCEPTS ARE AUTHORITATIVE — set BOTH lastConfirmed
		// AND fundingConfirmed unconditionally. Round r3's design only
		// wrote lastConfirmed here, leaving fundingConfirmed to be
		// "repaired" only as a SIDE EFFECT of some LATER completing
		// request's own revert happening to read the (by-then-updated)
		// lastConfirmed value — round r3's own fuzz/permutation attacks
		// (TestFuzz_ResolutionOrder_AtCap, TestNew_
		// AcceptInterleavedAmongRejections) proved that does NOT always
		// happen: an accept resolving while ANOTHER request for the same
		// service is still outstanding could leave fundingConfirmed
		// permanently stale at whatever that other, still-pending
		// request's own optimistic bump last set it to, with nothing ever
		// correcting it once every request finishes resolving (unless the
		// LAST one to resolve happens to be a reject/failure that reverts
		// — which round r3's guard fired inconsistently in the first
		// place, see fundingHasOtherOutstandingLocked's doc comment).
		// Writing fundingConfirmed here too — synchronously, at the exact
		// moment THIS accept is known — removes the dependency on some
		// other request's revert ever running at all.
		s.lastConfirmed[pending.serviceID] = pending.attemptedLevel
		s.fundingConfirmed[pending.serviceID] = pending.attemptedLevel
		return
	}

	if res.Error != nil {
		s.fundingRejectedReason = res.Error.Display
	} else {
		s.fundingRejectedReason = ""
	}
	// Finding F-B part 2 (round r1) / F-C (round r2) / round r4's
	// direction-guard fix: resync fundingConfirmed back to the true
	// engine-confirmed value — see fundingRevertToLastConfirmedLocked's
	// doc comment for why the revert target is lastConfirmed[serviceID],
	// never this request's own priorLevel snapshot (round r1's design),
	// and why the guard checks for ANY other outstanding request for this
	// service, not just a "newer" one (round r3's bug).
	s.fundingRevertToLastConfirmedLocked(pending.serviceID)
}

// fundingHasOtherOutstandingLocked reports whether s.pendingFunding
// contains ANY OTHER entry for serviceID — round r4's fix, replacing
// round r2/r3's fundingIsNewestOutstandingLocked (a seq/issue-order
// comparison that only treated a HIGHER-seq — i.e. more-recently-ISSUED —
// entry as blocking a revert). That directional check was itself round
// r3's destructive finding: an OLDER-issued request that simply hasn't
// resolved YET (lower seq, still genuinely outstanding) was invisible to
// a completing request's "is anything NEWER still outstanding" scan, so a
// completing rejection could revert fundingConfirmed AWAY FROM a
// still-pending sibling request's eventual, possibly-accepted outcome —
// see fundingRevertToLastConfirmedLocked's doc comment for the concrete
// scenario and why this simpler, direction-agnostic presence check is
// both sufficient and correct (a per-service outstanding COUNT, not a
// seq comparison — the "not more machinery" instruction this fix
// followed). Caller must hold s.mu and must have ALREADY removed the
// completing request's own entry from s.pendingFunding before calling
// this, so the scan reflects only what remains outstanding after it.
func (s *Screen) fundingHasOtherOutstandingLocked(serviceID string) bool {
	for _, req := range s.pendingFunding {
		if req.serviceID == serviceID {
			return true
		}
	}
	return false
}

// fundingRevertToLastConfirmedLocked is the ONE revert path every
// completing SetFunding request shares — ApplyResult's rejection branch,
// SetFunding's own send-failure branch, and fundingPendingCap's eviction
// path all call this, rather than each hand-rolling its own revert logic
// (FEAT-208 increment 3 destructive round r2 finding F-D: the send-
// failure branch previously reverted unconditionally, with no
// compare-before-revert guard at all, unlike ApplyResult's — a slow,
// eventually-failing send could stomp a faster, already-succeeded
// command's landed optimistic value). Caller must hold s.mu and must have
// already removed the completing entry from s.pendingFunding (mirroring
// fundingHasOtherOutstandingLocked's own precondition).
//
// # Correctness argument (round r2 finding F-C, "chain of rejections";
// round r4 fix, "accept interleaved among rejections")
//
// Reverting to lastConfirmed[serviceID] (never a per-request priorLevel
// snapshot) — guarded by "is ANY OTHER request for this service still
// outstanding, in either issue-order direction" (fundingHasOtherOutstandingLocked)
// — guarantees that after ANY number of overlapping requests for the same
// service complete, in ANY order, with ANY mix of accept/reject/
// send-failure/eviction outcomes, fundingConfirmed[serviceID] always ends
// up correct once every request has resolved:
//
//   - Whichever request resolves LAST (by real-world completion time, not
//     issue order) will, at the moment IT completes, find s.pendingFunding
//     has nothing else left for this service (every other request already
//     completed and was removed) — so fundingHasOtherOutstandingLocked
//     trivially returns false for it, REGARDLESS of issue order. If that
//     last-to-resolve request is a rejection/failure/eviction, its revert
//     therefore always fires and lands on lastConfirmed — which, by this
//     point, already reflects whichever accept (if any) most recently
//     confirmed (ApplyResult's accept branch, round r4: unconditional, not
//     dependent on THIS revert ever running). If the last-to-resolve
//     request is itself an Accept, ApplyResult's accept branch sets BOTH
//     lastConfirmed AND fundingConfirmed directly — this revert path is
//     not even consulted for that case.
//   - Every EARLIER-resolving request's own revert decision (fire or skip)
//     only affects the INTERMEDIATE state a caller might observe between
//     completions — it can never be the terminal state once every request
//     for the service has resolved, because the last one to resolve always
//     gets the final say per the point above.
//
// Round r3's own design used a DIRECTIONAL guard here (only a
// higher-seq/more-recently-ISSUED outstanding request blocked a revert) —
// that was itself round r3's destructive finding: an OLDER-issued sibling
// request that simply had not resolved yet (lower seq, genuinely still
// outstanding) was invisible to that check, so a completing request could
// revert fundingConfirmed away from a value an EARLIER-issued, still-live
// request would later legitimately confirm — see
// fundingHasOtherOutstandingLocked's own doc comment for the concrete
// trace (A/B/C, B accepted, resolving in an order where a directional scan
// missed B). The fix removes the direction entirely: ANY other outstanding
// request for the service — issued before or after — blocks a revert.
func (s *Screen) fundingRevertToLastConfirmedLocked(serviceID string) {
	if s.fundingHasOtherOutstandingLocked(serviceID) {
		return
	}
	if v, ok := s.lastConfirmed[serviceID]; ok {
		s.fundingConfirmed[serviceID] = v
		return
	}
	s.fundingConfirmed[serviceID] = fundingAdjustBaseline
}

// removePendingFundingOrderLocked removes id from pendingFundingOrder, if
// present (a no-op otherwise — e.g. called defensively from a path that
// isn't certain id was ever inserted). Caller must hold s.mu. A linear
// scan is deliberate and fine: pendingFundingOrder is bounded by
// fundingPendingCap (a small, fixed constant), never proportional to
// anything unbounded.
func (s *Screen) removePendingFundingOrderLocked(id protocol.CorrelationID) {
	for i, existing := range s.pendingFundingOrder {
		if existing == id {
			s.pendingFundingOrder = append(s.pendingFundingOrder[:i], s.pendingFundingOrder[i+1:]...)
			return
		}
	}
}

// evictOldestPendingFundingLocked is fundingPendingCap's overflow path
// (FEAT-208 increment 3 destructive round r2, item 3): removes the
// SINGLE oldest entry in pendingFundingOrder (FIFO — never a map range,
// see pendingFundingOrder's own doc comment for why), reverts
// fundingConfirmed for it exactly like any other completing request
// (fundingRevertToLastConfirmedLocked — the evicted entry is "completing"
// in the sense that this screen is giving up on ever learning its real
// outcome, not that it succeeded or failed engine-side), and records a
// loud, registry-sourced local failure (ErrFundingRequestEvicted,
// MET-V505) so an evicted request is never silently forgotten (GR#1).
// Caller must hold s.mu. A no-op if pendingFundingOrder is empty
// (defensive; unreachable via SetFunding's own call site, which only
// calls this when len(s.pendingFunding) >= fundingPendingCap > 0).
func (s *Screen) evictOldestPendingFundingLocked() {
	if len(s.pendingFundingOrder) == 0 {
		return
	}
	oldestID := s.pendingFundingOrder[0]
	s.pendingFundingOrder = s.pendingFundingOrder[1:]
	evicted, ok := s.pendingFunding[oldestID]
	if !ok {
		return
	}
	delete(s.pendingFunding, oldestID)
	s.fundingRevertToLastConfirmedLocked(evicted.serviceID)
	wrapped := errs.New(ErrFundingRequestEvicted, s.correlationID, map[string]any{
		"id":  evicted.serviceID,
		"cap": fundingPendingCap,
	})
	s.fundingLocalFailureReason = wrapped.Display()
}

// fundingBaseline returns the level RegisterFundingAdjustKeys' adjust
// action (or any other caller wanting to compute a delta against this
// screen's own last-known funding state) should treat as serviceID's
// current confirmed level: the last value SetFunding successfully sent
// and ApplyResult has not since reverted, or fundingAdjustBaseline if
// this screen has never attempted serviceID at all.
func (s *Screen) fundingBaseline(serviceID string) float64 {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "fundingBaseline"}); err != nil {
		return fundingAdjustBaseline
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.fundingConfirmed[serviceID]; ok {
		return v
	}
	return fundingAdjustBaseline
}

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

	s.haveData = true

	if p.Sliders != nil {
		s.sliders = make([]ServiceSlider, len(*p.Sliders))
		for i, sl := range *p.Sliders {
			s.sliders[i] = ServiceSlider(sl)
		}
		s.haveSliders = true
	} else {
		s.haveSliders = false
	}

	if p.CapacityDemand != nil {
		s.capacityDemand = make([]CapacityDemand, len(*p.CapacityDemand))
		for i, c := range *p.CapacityDemand {
			s.capacityDemand[i] = CapacityDemand(c)
		}
		s.haveCapacity = true
	} else {
		s.haveCapacity = false
	}

	if p.ResponseTimes != nil {
		s.responseTimes = make([]ResponseTimeStat, len(*p.ResponseTimes))
		for i, r := range *p.ResponseTimes {
			s.responseTimes[i] = ResponseTimeStat(r)
		}
		s.haveResponse = true
	} else {
		s.haveResponse = false
	}

	if p.WaitingLists != nil {
		s.waitingLists = make([]WaitingList, len(*p.WaitingLists))
		for i, w := range *p.WaitingLists {
			s.waitingLists[i] = WaitingList{
				ID:           w.ID,
				Label:        w.Label,
				CurrentCount: w.CurrentCount,
				TrendHistory: append([]float64(nil), w.TrendHistory...),
			}
		}
		s.haveWaiting = true
	} else {
		s.haveWaiting = false
	}

	if p.PublicServicePie != nil {
		s.pie = &PublicServicePieView{}
		s.pie.Slices = make([]PieSlice, len(p.PublicServicePie.Slices))
		for i, sl := range p.PublicServicePie.Slices {
			s.pie.Slices[i] = PieSlice(sl)
		}
		s.havePie = true
	} else {
		s.havePie = false
	}
}

func (s *Screen) HaveData() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.haveData
}

func (s *Screen) Stale() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stale
}

func (s *Screen) SetStale(v bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stale = v
}

func (s *Screen) FundingRejectedReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "FundingRejectedReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fundingRejectedReason
}

// FundingLocalFailureReason surfaces a SetFunding call whose send() never
// even reached the transport successfully (e.g. the command queue was
// full, or the transport had already closed) — see
// fundingLocalFailureReason's doc comment on the Screen struct for why
// this is deliberately a SEPARATE surface from FundingRejectedReason: the
// engine never adjudicated this command at all, so it is not an engine
// rejection and must not be reported as one (FEAT-208 increment 3
// destructive round r1, finding F-B part 1).
func (s *Screen) FundingLocalFailureReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "FundingLocalFailureReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fundingLocalFailureReason
}

func (s *Screen) Sliders() ([]ServiceSlider, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Sliders"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveSliders {
		return nil, false
	}
	res := make([]ServiceSlider, len(s.sliders))
	copy(res, s.sliders)
	return res, true
}

func (s *Screen) CapacityDemand() ([]CapacityDemand, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CapacityDemand"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveCapacity {
		return nil, false
	}
	res := make([]CapacityDemand, len(s.capacityDemand))
	copy(res, s.capacityDemand)
	return res, true
}

func (s *Screen) ResponseTimes() ([]ResponseTimeStat, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ResponseTimes"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveResponse {
		return nil, false
	}
	res := make([]ResponseTimeStat, len(s.responseTimes))
	copy(res, s.responseTimes)
	return res, true
}

func (s *Screen) WaitingLists() ([]WaitingList, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "WaitingLists"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveWaiting {
		return nil, false
	}
	res := make([]WaitingList, len(s.waitingLists))
	for i, w := range s.waitingLists {
		res[i] = WaitingList{
			ID:           w.ID,
			Label:        w.Label,
			CurrentCount: w.CurrentCount,
			TrendHistory: append([]float64(nil), w.TrendHistory...),
		}
	}
	return res, true
}

// PublicServicePie (SVC-6) — BLOCKED: see doc.go's SVC-6 note. Always
// returns have=false today because nothing sends the wire field yet
// (no engine.fiscal outbound edge is registered for ui.screen.services,
// BUG-058 candidate); the accessor and its render path exist so wiring
// SVC-6 later requires no structural change here.
func (s *Screen) PublicServicePie() (PublicServicePieView, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "PublicServicePie"}); err != nil {
		return PublicServicePieView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.havePie {
		return PublicServicePieView{}, false
	}
	res := PublicServicePieView{Slices: make([]PieSlice, len(s.pie.Slices))}
	copy(res.Slices, s.pie.Slices)
	return res, true
}

// normalizeFundingLevel rescales rawValue (a slider position in the
// slider's own UI display domain, e.g. min..max) into the engine's [0,1]
// funding-level fraction — internal/engine/services/api.go:266-292's
// ServicesAPI.SetFunding hard-rejects any level outside [0,1] (the
// codebase-wide funding-level convention; never a UI-scaled absolute like
// 0-1000). A degenerate min>=max domain has no meaningful fraction and
// returns NaN so the caller rejects rather than silently misreporting 0.
func normalizeFundingLevel(min, max, rawValue float64) float64 {
	if max <= min {
		return math.NaN()
	}
	return (rawValue - min) / (max - min)
}

// SetFunding issues SVC-1's per-service funding-slider change as a real
// protocol.KindSetFunding command (FEAT-208 increment 3 — supersedes the
// earlier protocol.KindDebug "services.set-funding" Op escape hatch,
// ASM-1193's own long-term ruling now landed). sl is the slider being changed (its Min/Max define
// the UI display domain the player sees — the slider MAY still display
// 0-1000 or a percentage) and rawValue is the new position in that same
// display domain. The wire value this method actually sends is rawValue
// rescaled into the engine's [0,1] funding-level fraction via
// normalizeFundingLevel, mirroring internal/engine/services/api.go's
// SetFunding contract exactly — a non-finite, negative, or above-domain
// rawValue (one that rescales to a level outside [0,1]) is rejected
// locally with MET-V503 before ever reaching the engine, never silently
// clamped. An engine-side rejection for a value inside [0,1] (e.g. below a
// hard floor, or ErrNotUnlocked) still surfaces separately via
// ApplyResult/FundingRejectedReason (SVC-8).
//
// Each call mints its OWN fresh protocol.CorrelationID
// (protocol.NewCorrelationID — the same generator every other real
// command path in this codebase mints per-command IDs from) rather than
// reusing s.correlationID (FEAT-208 increment 3 destructive round r1
// finding F-A — see pendingFundingRequest's doc comment for the full
// history) and tracks it in s.pendingFunding, optimistically advancing
// fundingConfirmed[sl.ID] to level before send() is even called so
// RegisterFundingAdjustKeys' next press (or any other caller reading
// fundingBaseline) sees the requested change immediately, matching the
// pre-existing "double-issue idempotency" shape — ApplyResult reconciles
// it for real once the CommandResult arrives (accept: records
// lastConfirmed AND fundingConfirmed unconditionally, round r4; reject:
// reverts via fundingRevertToLastConfirmedLocked, guarded by
// fundingHasOtherOutstandingLocked).
//
// If s.pendingFunding is already at fundingPendingCap, the single OLDEST
// outstanding entry (across every service, FIFO) is evicted first — round
// r2 item 3's bound on unbounded growth — before this new entry is
// inserted.
//
// If send() itself fails (finding F-B part 1 — e.g.
// protocol.ErrCommandQueueFull/ErrTransportClosed: the command never
// reached the transport at all, so no CommandResult will ever arrive),
// the optimistic fundingConfirmed bump and the pendingFunding entry are
// both rolled back immediately (nothing to wait for — the failure is
// already fully known, synchronously) through the SAME
// fundingRevertToLastConfirmedLocked/compare-before-revert path
// ApplyResult's own rejection branch uses (round r2 finding F-D: this
// branch previously reverted unconditionally, with no guard at all,
// unlike ApplyResult's), and the failure is recorded on
// FundingLocalFailureReason (MET-V504, GR#7) — a real, non-nil error is
// ALSO returned so a caller reading SetFunding's own return value learns
// about it either way; RegisterFundingAdjustKeys' Action.Run cannot
// itself return an error (keys.Action's signature), which is exactly why
// FundingLocalFailureReason exists as a polling-safe surface.
func (s *Screen) SetFunding(send SendCommandFunc, sl ServiceSlider, rawValue float64) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetFunding"}); err != nil {
		return err
	}
	level := normalizeFundingLevel(sl.Min, sl.Max, rawValue)
	if math.IsNaN(level) || math.IsInf(level, 0) || level < 0 || level > 1 {
		return errs.New(ErrInvalidFundingRequest, s.correlationID, map[string]any{
			"reason":   "funding level outside the engine's [0,1] domain (internal/engine/services/api.go SetFunding)",
			"id":       sl.ID,
			"rawValue": rawValue,
			"min":      sl.Min,
			"max":      sl.Max,
			"level":    level,
		})
	}

	corrID := protocol.NewCorrelationID()
	s.mu.Lock()
	if len(s.pendingFunding) >= fundingPendingCap {
		s.evictOldestPendingFundingLocked()
	}
	s.pendingFunding[corrID] = pendingFundingRequest{serviceID: sl.ID, attemptedLevel: level}
	s.pendingFundingOrder = append(s.pendingFundingOrder, corrID)
	s.fundingConfirmed[sl.ID] = level
	s.mu.Unlock()

	if err := send(setFundingCommand(string(corrID), sl.ID, level)); err != nil {
		s.mu.Lock()
		delete(s.pendingFunding, corrID)
		s.removePendingFundingOrderLocked(corrID)
		// Round r2 finding F-D / round r4 direction-guard fix: this revert
		// goes through the SAME guard ApplyResult's rejection branch uses
		// (fundingRevertToLastConfirmedLocked — fires only when no OTHER
		// request for this service remains outstanding at all, any issue
		// order) rather than unconditionally overwriting
		// fundingConfirmed[sl.ID] — a slow, ultimately-failing send must
		// never stomp a faster, already-succeeded command's landed
		// optimistic value.
		s.fundingRevertToLastConfirmedLocked(sl.ID)
		wrapped := errs.Wrap(ErrFundingCommandSendFailed, s.correlationID, err, map[string]any{
			"id":    sl.ID,
			"level": level,
			"cause": err.Error(),
		})
		s.fundingLocalFailureReason = wrapped.Display()
		s.mu.Unlock()
		return wrapped
	}
	return nil
}

// RegisterFundingAdjustKeys is SVC-1's input CALL SITE (FEAT-208
// increment 3): the inc2 gating note's own finding was that
// Screen.SetFunding was "never called from anywhere outside this
// package's own screen.go/tests" — this closes that, registering a real,
// production ui.keys action (mirroring
// internal/ui/screens/chrome/bind.go's RegisterBang exactly: Register/
// RegisterGlobal, an Action.Run closure calling straight back into this
// package's own public method, nothing ad hoc) that raises or lowers
// serviceID's funding level by step (the engine's own [0,1] domain,
// api.go's SetFunding contract) each time it fires, clamped to [0,1], and
// sends the result through send exactly as any other caller of
// SetFunding would — the SAME command seam, SAME local-rejection rule
// (MET-V503 for anything outside [0,1]), SAME ApplyResult/
// FundingRejectedReason surfacing (SVC-8) that a hypothetical slider
// widget would use.
//
// Deliberately built on top of SetFunding (a ServiceSlider{Min:0,Max:1}
// degenerate one-to-one display domain — a placeholder identical to the
// server's real slider bounds until the sliders sub-view itself is a
// published fast-follow, doc.go's own gating note) rather than a second,
// domain-bypassing entry point: doc.go's SVC-8 note is explicit that
// closing this gap must not "invent custom/ad-hoc wiring" around the
// established SetFunding contract, and this doesn't — it is the same
// rescale, the same rejection rule, the same command.
//
// registerPath is the caller-supplied leader PREFIX both bindings share
// (e.g. []string{"s","f"} — deliberately not digit-led; see the caller's
// own doc comment for why a bare leading digit can never be reached
// through Feed's count-prefix rule, AC-5): the INCREASE binding is
// registered at registerPath+"+" and the DECREASE binding at
// registerPath+"-". Both registrations go through g.Register (never
// RegisterGlobal — this is a screen-scoped action, not a topbar-style
// global like chrome's "!"), mirroring the leader-tree convention every
// mnemonic path in this
// codebase already uses.
//
// Scope note, honestly recorded (mirrors doc.go's own even-handed
// treatment of partial landings, and mapScreen's "Subscribe issued, not
// yet served" precedent): this registers a REAL, callable, tested
// keys.KeyGrammar action — a genuine call site from a keystroke all the
// way to SetFunding — but cmd/metropolis/run.go does not yet feed live
// tcell key events into ANY KeyGrammar for ANY F-screen (services
// included): runInteractive's RenderLoop only ever draws mapScreen today
// (cmd/metropolis/boot.go's viewStore doc comment), so there is no
// "currently active F4 screen" concept this action could be gated
// behind. That is pre-existing screen-switching/render-focus
// infrastructure this pilot's rails do not cover — not invented here,
// flagged for the lead the same way BUG-058/SVC-3/SVC-6 are flagged
// above.
func (s *Screen) RegisterFundingAdjustKeys(g *keys.KeyGrammar, send SendCommandFunc, serviceID string, step float64, registerPath []string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RegisterFundingAdjustKeys"}); err != nil {
		return err
	}
	if len(registerPath) == 0 {
		return errs.New(ErrInvalidFundingRequest, s.correlationID, map[string]any{
			"reason": "RegisterFundingAdjustKeys requires a non-empty mnemonic path",
		})
	}
	if !(step > 0) || math.IsNaN(step) || math.IsInf(step, 0) {
		return errs.New(ErrInvalidFundingRequest, s.correlationID, map[string]any{
			"reason": "RegisterFundingAdjustKeys requires a finite, positive step",
			"step":   step,
		})
	}

	sl := ServiceSlider{ID: serviceID, Label: serviceID, Min: 0, Max: 1, Step: step}

	// adjust no longer owns any client-side tracker of its own (FEAT-208
	// increment 3 destructive round r1, finding F-B part 2's root cause: a
	// closure-private `current` variable that ApplyResult had no way to
	// reach and correct after a rejection). It reads Screen's own
	// fundingBaseline(serviceID) fresh on every fire instead — the SAME
	// state ApplyResult reverts on rejection (see fundingConfirmed's doc
	// comment) — so a rejected adjustment's correction is visible to the
	// very next press, not silently invisible to this closure forever.
	adjust := func(delta float64) error {
		next := s.fundingBaseline(serviceID) + delta
		if next < 0 {
			next = 0
		}
		if next > 1 {
			next = 1
		}
		// SetFunding's own rawValue domain is sl.Min..sl.Max, which this
		// degenerate slider defines as exactly [0,1] — so `next` is passed
		// straight through, not rescaled a second time. Its returned error
		// (finding F-B part 1) is NOT discarded here — Action.Run itself
		// cannot return an error (keys.Action's signature), but SetFunding
		// has already recorded any failure on FundingLocalFailureReason
		// before returning, and returning it here too keeps this closure
		// itself honest for anything that calls adjust directly (this
		// package's own tests).
		return s.SetFunding(send, sl, next)
	}

	// countAdjust honours ui.keys' count-prefix (AC-5, "5 s f +" == five
	// steps in one dispatch) — FEAT-208 increment 3 destructive round r1's
	// MINOR finding: Action.Run previously ignored ActionArgs entirely,
	// silently discarding any count prefix the player fed. Count is
	// documented to default to 1 when no digits were fed
	// (ActionArgs' own doc comment; consumeCountLocked, grammar.go), but
	// defended here too rather than trusted blindly (GR#1: never assume an
	// upstream invariant without a local check when the cost of checking
	// is one comparison).
	countAdjust := func(args keys.ActionArgs, sign float64) {
		count := args.Count
		if count < 1 {
			count = 1
		}
		_ = adjust(sign * step * float64(count))
	}

	increasePath := append(append([]string{}, registerPath...), "+")
	decreasePath := append(append([]string{}, registerPath...), "-")

	if err := g.Register(increasePath, keys.Action{
		Name: "Increase " + serviceID + " funding",
		Run:  func(args keys.ActionArgs) { countAdjust(args, 1) },
	}); err != nil {
		return err
	}
	if err := g.Register(decreasePath, keys.Action{
		Name: "Decrease " + serviceID + " funding",
		Run:  func(args keys.ActionArgs) { countAdjust(args, -1) },
	}); err != nil {
		return err
	}
	return nil
}

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
	_ = errs.New(ErrStaleSubscription, s.correlationID, map[string]any{
		"subscriptionId": string(id),
	})
}
