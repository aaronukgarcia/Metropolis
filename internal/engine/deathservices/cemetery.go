package deathservices

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// cemetery.go implements AC-2/AC-3/AC-4/AC-15: graveyard plot allocation,
// data-driven reuse-horizon-gated recycling, and the "fills permanently --
// land pressure" saturation triage.

// plot is one fixed slot in a cemetery's plot array. A plot is either never
// used (occupied=false) or holds the most recent occupant's burial record
// (occupied=true, buriedMonth, bodyID) -- burying into an eligible-for-reuse
// plot OVERWRITES its occupant record. The evicted former occupant's Body
// record is NOT reverted or reclassified: a body's state, once BodyBuried,
// is a permanent lifetime classification ("this body was terminally
// disposed of by burial"), exactly like BodyCremated/BodyDispensed never
// reverting either -- AC-15's terminal-exclusivity guarantee applies to
// EVERY terminal state equally. conservation.go's BodiesBuried term is
// therefore this LIFETIME count (sum of every body ever classified
// BodyBuried), which is what keeps the AC-14 identity a true, permanent
// partition of every released body. Live PHYSICAL plot occupancy -- how
// many of a cemetery's plots are presently full, which shrinks as reuse
// recycles them -- is a separate, lower-level, capacity-only figure exposed
// by [DeathServicesAPI.CemeteryOccupancy]; it can be smaller than the
// lifetime BodiesBuried count once reuse has recycled at least one plot,
// and that is expected, not a conservation violation.
type plot struct {
	occupied    bool
	buriedMonth int64
	bodyID      uint64
}

// cemeteryState is one registered cemetery's plot pool (AC-2). capacity is
// fixed at registration time from data (or an explicit override, for a test
// that needs a small saturating pool without inventing a second data file
// per test -- see RegisterCemeteryWithCapacity's doc).
type cemeteryState struct {
	id       string
	capacity int64
	plots    []plot
}

// RegisterCemetery registers a graveyard building (consumed through the
// engine.build catalogue edge in the live composition, GR#20) with the
// data-sourced plot capacity (AC-2, spec seed 2k). Re-registering an
// existing ID is idempotent and does not reset its occupancy (mirrors
// refuse.RegisterDepot's idempotent-registration precedent).
func (d *DeathServicesAPI) RegisterCemetery(cemeteryID string, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "RegisterCemetery"); err != nil {
		return err
	}
	if cemeteryID == "" {
		return errs.New(ErrUnknownBuildingType, correlationID, map[string]any{"buildingType": "cemetery", "cemeteryId": cemeteryID})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.cemeteries[cemeteryID]; exists {
		return nil
	}
	d.cemeteries[cemeteryID] = &cemeteryState{
		id:       cemeteryID,
		capacity: d.cfg.GraveyardPlotCapacity(),
		plots:    make([]plot, d.cfg.GraveyardPlotCapacity()),
	}
	return nil
}

// RegisterCemeteryWithCapacity is [RegisterCemetery]'s test-facing sibling:
// it registers a cemetery whose plot capacity is an explicit override
// rather than data/deathservices.json's spec-seed 2k default. This exists
// so AC-4's saturation test can exercise "every plot full" without a
// 2000-body fixture -- the CAPACITY here still traces to a documented,
// caller-supplied number (never a magic literal baked into this package's
// production logic; production callers always go through
// [RegisterCemetery], which is 100% data-sourced, GR#15). capacity <= 0 is
// rejected -- a zero-or-negative plot pool has no allocatable meaning.
func (d *DeathServicesAPI) RegisterCemeteryWithCapacity(cemeteryID string, capacity int64, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "RegisterCemeteryWithCapacity"); err != nil {
		return err
	}
	if cemeteryID == "" {
		return errs.New(ErrUnknownBuildingType, correlationID, map[string]any{"buildingType": "cemetery", "cemeteryId": cemeteryID})
	}
	if capacity <= 0 {
		return errs.New(ErrUnknownBuildingType, correlationID, map[string]any{"buildingType": "cemetery", "cemeteryId": cemeteryID, "capacity": capacity})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cemeteries[cemeteryID] = &cemeteryState{
		id:       cemeteryID,
		capacity: capacity,
		plots:    make([]plot, capacity),
	}
	return nil
}

// isNoPlotAvailable reports whether err is [ErrNoPlotAvailable] (AC-4/H6),
// the ONE burial-rejection reason a batch caller (hearse.go's
// RunHearseTransport) treats as "skip and keep going" rather than an
// abort -- every other error from buryLocked (unknown body, already
// handled, unknown cemetery) is a real fault the caller must not silently
// swallow.
func isNoPlotAvailable(err error) bool {
	e, ok := err.(*errs.E)
	return ok && e.Code == ErrNoPlotAvailable
}

// CemeteryOccupancy returns (occupied, capacity) for a registered cemetery
// (AC-2's occupancy accessor). Unknown cemeteryID returns
// [ErrUnknownCemetery].
func (d *DeathServicesAPI) CemeteryOccupancy(cemeteryID string, correlationID string) (occupied, capacity int64, err error) {
	if err := d.checkNotCopied(correlationID, "CemeteryOccupancy"); err != nil {
		return 0, 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cemeteries[cemeteryID]
	if !ok {
		return 0, 0, errs.New(ErrUnknownCemetery, correlationID, map[string]any{"cemeteryId": cemeteryID})
	}
	occ := int64(0)
	for _, p := range c.plots {
		if p.occupied {
			occ++
		}
	}
	return occ, c.capacity, nil
}

// findAllocatablePlotLocked returns the index of a plot Bury may use this
// month: preferring a never-used plot (index order, deterministic), and
// falling back to the OLDEST reuse-eligible occupied plot (buriedMonth,
// then bodyID tie-break, GR#21) when no never-used plot remains. Returns
// -1 when neither exists (AC-4's saturation condition). Caller must hold
// d.mu.
func findAllocatablePlotLocked(c *cemeteryState, month, horizon int64) int {
	for i := range c.plots {
		if !c.plots[i].occupied {
			return i
		}
	}
	best := -1
	for i := range c.plots {
		p := &c.plots[i]
		if !p.occupied {
			continue
		}
		if month-p.buriedMonth < horizon {
			continue
		}
		if best == -1 || p.buriedMonth < c.plots[best].buriedMonth ||
			(p.buriedMonth == c.plots[best].buriedMonth && p.bodyID < c.plots[best].bodyID) {
			best = i
		}
	}
	return best
}

// allocatableCountLocked returns how many plots in c are allocatable RIGHT
// NOW at month: every never-used plot, plus every occupied plot that has
// reached its reuse horizon. This is the SAME set findAllocatablePlotLocked
// searches, just counted rather than selected -- used by
// awaitingAheadCountLocked's admission rule (H6/AC-18) to decide whether a
// contending body is within the currently-available capacity, independent
// of which plot it will eventually receive. Caller must hold d.mu.
func allocatableCountLocked(c *cemeteryState, month, horizon int64) int64 {
	var n int64
	for i := range c.plots {
		p := &c.plots[i]
		if !p.occupied || month-p.buriedMonth >= horizon {
			n++
		}
	}
	return n
}

// awaitingAheadCountLocked counts every BodyAwaiting body (system-wide, not
// scoped to one cemetery -- inc1's single documented priority order, see
// doc.go) whose (deathMonth, citizenID) ranks STRICTLY before (deathMonth,
// citizenID). This is the H6/ATTACK-8 fix's admission rule (AC-18): plot
// admission under contention is decided by comparing this count against
// [allocatableCountLocked], NEVER by which concurrent caller's goroutine
// happened to reach the mutex first.
//
// Why this is worker-count-invariant even though it is evaluated once per
// Bury call, potentially interleaved with other concurrent Bury calls in
// any order: a body that has not yet been resolved (successfully buried)
// is STILL BodyAwaiting regardless of whether its own Bury call has run
// yet, so it is counted identically no matter which order concurrent
// calls happen to execute in. Writing out the algebra:
// admit(id) iff aheadCount(id) < allocatable(id), where aheadCount only
// DECREASES when an ahead-of-id body SUCCEEDS (never when one is merely
// rejected -- a rejected body stays Awaiting, AC-4) and allocatable only
// decreases by the SAME amount per success (one consumed plot). The two
// terms move in lockstep, so the comparison's truth value for any given
// id is invariant to the interleaving order of concurrent calls -- see
// attack_round_test.go's TestAttackDeterminismUnderPlotContention, which
// this rule is proven against at worker counts 1/4/20.
//
// Caller must hold d.mu. O(len(d.bodies)) per call -- acceptable at inc1
// scale (small cemeteries/backlogs); a future increment revisiting
// 100M-citizen throughput would want an indexed structure here (mirrors
// citizens.DeathQueue's own BUG-663 evolution from an O(n) snapshot to a
// sharded index once real load justified it -- flagged, not solved, here).
func (d *DeathServicesAPI) awaitingAheadCountLocked(deathMonth int64, citizenID uint64, correlationID string) int64 {
	// REDUNDANT with every current caller's own guard (buryLocked, Cremate)
	// -- kept anyway, matching this package's established double-check
	// convention (see api.go's awaitingSortedLocked doc for the identical
	// astgate-blind-spot reasoning).
	_ = d.checkNotCopied(correlationID, "awaitingAheadCountLocked")
	var n int64
	for id, b := range d.bodies {
		if b.state != BodyAwaiting {
			continue
		}
		if b.deathMonth < deathMonth || (b.deathMonth == deathMonth && id < citizenID) {
			n++
		}
	}
	return n
}

// PlotEligibleForReuse reports whether the plot currently occupied by
// bodyID in cemeteryID would be reuse-eligible AT month (AC-3's direct
// mechanism check) -- a pure query, no mutation. Returns false (not an
// error) for an unoccupied/unknown bodyID, since "not currently occupying a
// reuse-eligible plot" is the correct answer for a body that is not buried
// there at all.
func (d *DeathServicesAPI) PlotEligibleForReuse(cemeteryID string, bodyID uint64, month int64, correlationID string) (bool, error) {
	if err := d.checkNotCopied(correlationID, "PlotEligibleForReuse"); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cemeteries[cemeteryID]
	if !ok {
		return false, errs.New(ErrUnknownCemetery, correlationID, map[string]any{"cemeteryId": cemeteryID})
	}
	horizon := d.cfg.PlotReuseHorizonMonths()
	for i := range c.plots {
		p := &c.plots[i]
		if p.occupied && p.bodyID == bodyID {
			return month-p.buriedMonth >= horizon, nil
		}
	}
	return false, nil
}

// Bury disposes of bodyID by burial in cemeteryID at month (AC-2/AC-4/
// AC-15). Consumes exactly one plot -- a never-used plot if one exists,
// else the oldest reuse-eligible occupied plot (AC-3). When neither exists
// (AC-4's saturation condition -- every plot full and none reuse-eligible),
// Bury returns [ErrNoPlotAvailable] and makes NO state change: no plot is
// consumed, the body stays Awaiting, so the caller's documented fallback
// (cremation or emergency dispensation) can still claim it. A body already
// in a terminal state is rejected with [ErrBodyAlreadyHandled] (AC-15).
//
// Under contention (H6/AC-18, ATTACK-8): admission to a scarce plot is
// decided by [DeathServicesAPI.awaitingAheadCountLocked] against
// [allocatableCountLocked], NEVER by which concurrent caller's goroutine
// happened to acquire d.mu first -- see that method's doc for the
// worker-count-invariance proof.
func (d *DeathServicesAPI) Bury(bodyID uint64, cemeteryID string, month int64, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "Bury"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buryLocked(bodyID, cemeteryID, month, correlationID)
}

// buryLocked is Bury's lock-held core (H3 fix): callers that already hold
// d.mu for a larger atomic sequence (hearse.go's RunHearseTransport claims
// its monthly budget AND transports in one continuous lock hold) call this
// directly instead of the public Bury, which would deadlock re-acquiring
// d.mu. Caller must hold d.mu.
func (d *DeathServicesAPI) buryLocked(bodyID uint64, cemeteryID string, month int64, correlationID string) error {
	// REDUNDANT with both current callers' own guard (Bury, hearse.go's
	// RunHearseTransport) -- kept anyway, matching this package's
	// established double-check convention (see api.go's
	// awaitingSortedLocked doc for the identical astgate-blind-spot
	// reasoning).
	if err := d.checkNotCopied(correlationID, "buryLocked"); err != nil {
		return err
	}
	b, ok := d.bodies[bodyID]
	if !ok {
		return errs.New(ErrUnknownBody, correlationID, map[string]any{"bodyId": bodyID})
	}
	if b.state != BodyAwaiting && b.state != BodyEnRoute {
		return errs.New(ErrBodyAlreadyHandled, correlationID, map[string]any{"bodyId": bodyID, "state": string(b.state)})
	}
	c, ok := d.cemeteries[cemeteryID]
	if !ok {
		return errs.New(ErrUnknownCemetery, correlationID, map[string]any{"cemeteryId": cemeteryID})
	}

	horizon := d.cfg.PlotReuseHorizonMonths()

	// H6/AC-18 admission gate: deterministic, order-independent regardless
	// of how many OTHER Bury/buryLocked calls are concurrently in flight
	// for this (or any) cemetery.
	ahead := d.awaitingAheadCountLocked(b.deathMonth, bodyID, correlationID)
	allocatable := allocatableCountLocked(c, month, horizon)
	if ahead >= allocatable {
		return errs.New(ErrNoPlotAvailable, correlationID, map[string]any{
			"cemeteryId": cemeteryID,
			"capacity":   c.capacity,
			"occupied":   c.capacity,
		})
	}

	idx := findAllocatablePlotLocked(c, month, horizon)
	if idx == -1 {
		// M2 (round-2): [ErrPlotNotReusable] wired to a real call site --
		// the admission gate above already proved a plot is allocatable
		// (ahead < allocatable), so findAllocatablePlotLocked returning -1
		// here means [allocatableCountLocked] and
		// [findAllocatablePlotLocked] have drifted out of sync with each
		// other (an invariant violation between the two counting
		// functions, not a normal saturation condition -- that case is
		// already handled above via ErrNoPlotAvailable). A distinct code
		// makes this diagnosable rather than silently indistinguishable
		// from ordinary land pressure.
		return errs.New(ErrPlotNotReusable, correlationID, map[string]any{
			"cemeteryId": cemeteryID,
			"plotId":     "unresolved", // no plot index exists to name -- see the rule above
			"bodyId":     bodyID,
			"buriedAt":   month,
			"horizon":    horizon,
		})
	}

	c.plots[idx] = plot{occupied: true, buriedMonth: month, bodyID: bodyID}
	b.state = BodyBuried
	b.cemeteryID = cemeteryID
	return nil
}
