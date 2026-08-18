package refuse

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// unboundedCapacity is the logistics shelf capacity refuse provisions for
// disposal sites that never "fill" (incinerator and compost): they are
// throughput-bounded by engine.logistics' movement machinery, not
// capacity-bounded the way a landfill is. It is the only way to express
// "no finite fill ceiling" to engine.logistics.Provision, whose capacity
// field is int64.
const unboundedCapacity = int64(math.MaxInt64)

// RegisterLandfill registers a landfill disposal site (AC-8) with its
// total capacity (kg) and the surrounding cells that blight when the
// landfill is full. The landfill's fill state lives in engine.logistics'
// own stock shelf (district = siteID, commodity = Waste), provisioned
// lazily on first use — the landfill IS a logistics stock, reusing that
// machinery rather than a refuse-owned fill counter (AC-4/GR#3). The fill
// is ALSO recorded locally on the disposalSite's `used` field so a Wire
// re-provision with a different logistics instance re-seeds the new shelf
// rather than resetting a permanent fill to zero (AC-8).
//
// A siteID that is already registered is REJECTED with
// ErrDisposalSiteUnavailable rather than silently replacing the existing
// site — a re-register would otherwise destroy the site's durable state
// (backlog, permanent fill, energy, airshed, compost) and break AC-8/AC-9/
// AC-10/AC-11 (Destructive-MOD039 r4). Register a disposal site once; a
// landfill is capped-and-reclaimed via CapAndReclaim, never re-registered.
func (r *RefuseAPI) RegisterLandfill(siteID string, capacityKG int64, surrounding []string) error {
	if err := r.checkNotCopied("RegisterLandfill"); err != nil {
		return err
	}
	if siteID == "" || capacityKG <= 0 {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sites[siteID]; exists {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID, "reason": "already registered"})
	}
	r.sites[siteID] = &disposalSite{
		id:          siteID,
		kind:        DisposalLandfill,
		capacity:    capacityKG,
		surrounding: append([]string(nil), surrounding...),
	}
	return nil
}

// RegisterIncinerator registers an incinerator disposal site (AC-9). A
// siteID that is already registered is REJECTED with
// ErrDisposalSiteUnavailable, never silently replacing the site and zeroing
// its accumulated energy/airshed (Destructive-MOD039 r4).
func (r *RefuseAPI) RegisterIncinerator(siteID string) error {
	if err := r.checkNotCopied("RegisterIncinerator"); err != nil {
		return err
	}
	if siteID == "" {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sites[siteID]; exists {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID, "reason": "already registered"})
	}
	r.sites[siteID] = &disposalSite{id: siteID, kind: DisposalIncinerator}
	return nil
}

// RegisterCompostSite registers a compost disposal site (AC-10). A siteID
// that is already registered is REJECTED with ErrDisposalSiteUnavailable,
// never silently replacing the site and zeroing its accumulated compost
// output (Destructive-MOD039 r4).
func (r *RefuseAPI) RegisterCompostSite(siteID string) error {
	if err := r.checkNotCopied("RegisterCompostSite"); err != nil {
		return err
	}
	if siteID == "" {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sites[siteID]; exists {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID, "reason": "already registered"})
	}
	r.sites[siteID] = &disposalSite{id: siteID, kind: DisposalCompost}
	return nil
}

// SetGeneralSite sets the active general-waste disposal target (AC-8/AC-9):
// the site that rounds deliver general waste to. It must be a registered
// landfill or incinerator; a compost site (or an unknown site) is rejected.
func (r *RefuseAPI) SetGeneralSite(siteID string) error {
	if err := r.checkNotCopied("SetGeneralSite"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[siteID]
	if !ok || site.kind == DisposalCompost {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	r.generalSiteID = siteID
	return nil
}

// SetCompostSite sets the active food-waste compost target (AC-10). It
// must be a registered compost site.
func (r *RefuseAPI) SetCompostSite(siteID string) error {
	if err := r.checkNotCopied("SetCompostSite"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[siteID]
	if !ok || site.kind != DisposalCompost {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	r.compostSiteID = siteID
	return nil
}

// ensureSiteShelf provisions a logistics stock shelf for a disposal site
// exactly once (idempotent via the provisioned map). The shelf's capacity
// is the landfill's total capacity (so the landfill fills), or unbounded
// when no finite ceiling applies. When a re-provision is necessary (a Wire
// replaced the logistics instance), the landfill's permanent fill — held on
// the disposalSite's own `used` field, not only in the now-orphaned shelf —
// re-seeds the new instance's shelf, so RemainingCapacity stays monotone
// non-increasing across ANY re-wire, same or different instance (AC-8).
//
// The check + provision + flag-set run as ONE critical section under the
// write lock (AC-8/AC-17): a check-then-release-then-provision TOCTOU let
// concurrent [RefuseAPI.RouteGeneralToSite] calls on a fresh landfill both
// pass the provisioned-flag check and double-Provision, resetting the shelf
// so the landfill accepted over capacity and site.used diverged from
// shelf.Level. Provision is safe to call under r.mu — engine.logistics is a
// dependency, never a caller back into this package, so there is no lock
// order cycle.
func (r *RefuseAPI) ensureSiteShelf(lg *logistics.LogisticsAPI, siteID string, capacity int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.provisioned[siteID] {
		return
	}
	initial := int64(0)
	if site, ok := r.sites[siteID]; ok {
		initial = site.used
	}
	if capacity <= 0 {
		capacity = unboundedCapacity
	}
	_, _ = lg.Provision(siteID, market.Waste, capacity, initial)
	r.provisioned[siteID] = true
}

// RouteGeneralToSite routes general waste directly to a disposal site
// (AC-8/AC-9): a landfill consumes it permanently (its remaining capacity
// only ever decreases), an incinerator produces energy output at the cost
// of an airshed-pollution term. A compost site, a full landfill, or a
// reclaimed landfill is rejected with ErrDisposalSiteUnavailable (AC-8) —
// never a silently-dropped tonnage. Returns the tonnage actually accepted.
//
// The incinerator path is throughput-bounded: it accepts no more in one tick
// than engine.logistics' Deliverable throughput for the Waste commodity,
// exactly like the round path (AC-9) — an incinerator site is not an
// unbounded, instant-accept sink. The landfill path is capacity-bounded by
// its own Restock.
//
// Direct routing is the package's surface for externally-sourced waste, so
// the accepted tonnage is credited to `generated` as well as `collected`,
// keeping AC-11's mass-conservation identity balanced on this path (see
// [RefuseAPI.TonnesGenerated]).
func (r *RefuseAPI) RouteGeneralToSite(siteID string, tonnage int64) (int64, error) {
	if err := r.checkNotCopied("RouteGeneralToSite"); err != nil {
		return 0, err
	}
	if err := r.requireWired("RouteGeneralToSite"); err != nil {
		return 0, err
	}
	if tonnage <= 0 {
		return 0, nil
	}

	deps := r.snapshotDeps()

	// Snapshot the site's identity and the mutable reclaimed flag under ONE
	// RLock, then act on the snapshot (AC-17): CapAndReclaim writes
	// site.reclaimed under r.mu.Lock, so reading it after RUnlock races.
	r.mu.RLock()
	site, ok := r.sites[siteID]
	kind := DisposalKind("")
	reclaimed := false
	capacity := int64(0)
	if ok {
		kind = site.kind
		reclaimed = site.reclaimed
		capacity = site.capacity
	}
	r.mu.RUnlock()
	if !ok || kind == DisposalCompost {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}

	switch kind {
	case DisposalLandfill:
		if reclaimed {
			return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
		}
		r.ensureSiteShelf(deps.logistics, siteID, capacity)
		shelf, err := deps.logistics.Stock(siteID, market.Waste)
		if err != nil {
			return 0, err
		}
		if shelf.Level >= shelf.Capacity {
			return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
		}
		added, err := deps.logistics.Restock(siteID, market.Waste, tonnage)
		if err != nil {
			return 0, err
		}
		r.mu.Lock()
		// Direct routing introduces externally-sourced waste into the
		// accounting period, so credit `generated` by exactly the accepted
		// amount alongside `collected` (AC-11: the four-term identity stays
		// balanced on the direct-route surface). The permanent fill is also
		// recorded on the site's own `used` field, so a re-provision after a
		// Wire re-seeds the shelf from the local record (AC-8).
		r.generated[0] = num.SatAdd(r.generated[0], added)
		r.collected[0] = num.SatAdd(r.collected[0], added)
		if site, ok := r.sites[siteID]; ok {
			site.used = num.SatAdd(site.used, added)
		}
		r.mu.Unlock()
		return added, nil

	default: // incinerator
		accepted, err := r.throughputAccepted(deps.logistics, siteID, tonnage)
		if err != nil {
			return 0, err
		}
		if accepted <= 0 {
			return 0, nil
		}
		r.mu.Lock()
		if site, ok := r.sites[siteID]; ok {
			site.energy = num.SatAdd(site.energy, num.ClampInt64FromFloat(float64(accepted)*r.cfg.Incineration.EnergyPerKg))
			site.airshed += float64(accepted) * r.cfg.Incineration.AirshedPollutionPerKg
		}
		r.generated[0] = num.SatAdd(r.generated[0], accepted)
		r.collected[0] = num.SatAdd(r.collected[0], accepted)
		r.mu.Unlock()
		return accepted, nil
	}
}

// RouteFoodToCompost routes food waste to a compost site (AC-10), producing
// compost output at the data-sourced conversion ratio (GR#15). Returns the
// food tonnage accepted.
//
// Like [RefuseAPI.RouteGeneralToSite]'s incinerator path, this direct route
// is throughput-bounded: it accepts no more in one tick than
// engine.logistics' Deliverable throughput for the Waste commodity (AC-10),
// exactly like the round path. It introduces externally-sourced waste, so
// the accepted tonnage is credited to `generated` as well as `collected`
// (AC-11).
func (r *RefuseAPI) RouteFoodToCompost(siteID string, tonnage int64) (int64, error) {
	if err := r.checkNotCopied("RouteFoodToCompost"); err != nil {
		return 0, err
	}
	if err := r.requireWired("RouteFoodToCompost"); err != nil {
		return 0, err
	}
	if tonnage <= 0 {
		return 0, nil
	}
	deps := r.snapshotDeps()

	r.mu.RLock()
	site, ok := r.sites[siteID]
	kind := DisposalKind("")
	if ok {
		kind = site.kind
	}
	r.mu.RUnlock()
	if !ok || kind != DisposalCompost {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	accepted, err := r.throughputAccepted(deps.logistics, siteID, tonnage)
	if err != nil {
		return 0, err
	}
	if accepted <= 0 {
		return 0, nil
	}
	compost := num.ClampInt64FromFloat(math.Floor(float64(accepted) * r.cfg.Compost.ConversionRatio))
	r.mu.Lock()
	if site, ok := r.sites[siteID]; ok {
		site.compost = num.SatAdd(site.compost, compost)
	}
	// Direct routing introduces externally-sourced food waste, so credit
	// `generated` alongside `collected` (AC-11: the identity holds on the
	// direct-route surface).
	r.generated[2] = num.SatAdd(r.generated[2], accepted)
	r.collected[2] = num.SatAdd(r.collected[2], accepted)
	r.mu.Unlock()
	return accepted, nil
}

// throughputAccepted caps a direct-route tonnage at engine.logistics'
// per-tick Deliverable throughput for the Waste commodity (AC-8/AC-9/
// AC-10): incinerator and compost sites are throughput-bounded, not
// capacity-bounded, so a direct route can accept no more in one tick than
// the movement machinery can deliver — exactly like the round path. Returns
// the throughput-bounded tonnage actually accepted. lg is the caller's
// dependency snapshot (AC-17): it must never re-read r.logistics here.
func (r *RefuseAPI) throughputAccepted(lg *logistics.LogisticsAPI, siteID string, tonnage int64) (int64, error) {
	delivery, err := lg.Deliverable(siteID, market.Waste, tonnage)
	if err != nil {
		return 0, err
	}
	accepted := delivery.Delivered
	if accepted < 0 {
		accepted = 0
	}
	if accepted > tonnage {
		accepted = tonnage
	}
	return accepted, nil
}

// ProcessDisposal moves a disposal site's inbound backlog into its terminal
// form (landfill fill / incinerator energy+pollution / compost output),
// incrementing the collected accounting term (AC-11's "completed round
// deliveries"). Returns the tonnage processed. A landfill that cannot accept
// its full backlog (it is full, or has only partial headroom) returns the
// processed tonnage alongside ErrDisposalSiteUnavailable — the un-processed
// remainder stays queued rather than being silently dropped (AC-8).
func (r *RefuseAPI) ProcessDisposal(siteID string) (int64, error) {
	if err := r.checkNotCopied("ProcessDisposal"); err != nil {
		return 0, err
	}
	if err := r.requireWired("ProcessDisposal"); err != nil {
		return 0, err
	}
	deps := r.snapshotDeps()

	// Snapshot the site's identity and the mutable reclaimed flag under ONE
	// RLock (AC-17): CapAndReclaim writes site.reclaimed under r.mu.Lock.
	r.mu.RLock()
	site, ok := r.sites[siteID]
	kind := DisposalKind("")
	reclaimed := false
	capacity := int64(0)
	if ok {
		kind = site.kind
		reclaimed = site.reclaimed
		capacity = site.capacity
	}
	r.mu.RUnlock()
	if !ok {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}

	switch kind {
	case DisposalLandfill:
		if reclaimed {
			return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
		}
		r.ensureSiteShelf(deps.logistics, siteID, capacity)
		// Claim the whole backlog atomically BEFORE the external Restock, so
		// a concurrent ProcessDisposal cannot read the same tonnage and
		// re-process it (the AC-17/AC-11 lost-update). The un-accepted
		// remainder is returned to the queue afterwards.
		r.mu.Lock()
		general := site.backlog[0]
		site.backlog[0] = 0
		r.mu.Unlock()
		added, err := deps.logistics.Restock(siteID, market.Waste, general)
		if err != nil {
			// Restock failed: return the claimed tonnage to the backlog so it
			// is not lost (AC-8: never silently drop).
			r.mu.Lock()
			site.backlog[0] = num.SatAdd(site.backlog[0], general)
			r.mu.Unlock()
			return 0, err
		}
		shortfall := num.SatSub(general, added)
		r.mu.Lock()
		site.backlog[0] = num.SatAdd(site.backlog[0], shortfall)
		site.used = num.SatAdd(site.used, added)
		r.collected[0] = num.SatAdd(r.collected[0], added)
		r.mu.Unlock()
		if general > 0 && added < general {
			// The landfill is full (or has only partial headroom): the
			// un-processed backlog is NOT silently dropped — surface the
			// full-site error so the caller can route the shortfall
			// elsewhere (AC-8).
			return added, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{
				"site":      siteID,
				"shortfall": shortfall,
			})
		}
		return added, nil

	case DisposalIncinerator:
		r.mu.Lock()
		general := site.backlog[0]
		site.backlog[0] = 0
		site.energy = num.SatAdd(site.energy, num.ClampInt64FromFloat(float64(general)*r.cfg.Incineration.EnergyPerKg))
		site.airshed += float64(general) * r.cfg.Incineration.AirshedPollutionPerKg
		r.collected[0] = num.SatAdd(r.collected[0], general)
		r.mu.Unlock()
		return general, nil

	default: // compost
		r.mu.Lock()
		food := site.backlog[2]
		site.backlog[2] = 0
		compost := num.ClampInt64FromFloat(math.Floor(float64(food) * r.cfg.Compost.ConversionRatio))
		site.compost = num.SatAdd(site.compost, compost)
		r.collected[2] = num.SatAdd(r.collected[2], food)
		r.mu.Unlock()
		return food, nil
	}
}

// CapAndReclaim caps and reclaims an exhausted landfill as parkland (§32,
// AC-8): after reclaim it is no longer a valid disposal target. Only a
// landfill can be reclaimed.
func (r *RefuseAPI) CapAndReclaim(siteID string) error {
	if err := r.checkNotCopied("CapAndReclaim"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[siteID]
	if !ok || site.kind != DisposalLandfill {
		return errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	site.reclaimed = true
	return nil
}

// RemainingCapacity returns a landfill's remaining capacity (kg) — capacity
// minus the filled level read from engine.logistics' own stock shelf (the
// single source of the fill, AC-4/GR#3). It only ever decreases under load
// (AC-8): the shelf is only ever Restocked, never Drawn from. Only a
// landfill has a capacity.
func (r *RefuseAPI) RemainingCapacity(siteID string) (int64, error) {
	if err := r.checkNotCopied("RemainingCapacity"); err != nil {
		return 0, err
	}
	if err := r.requireWired("RemainingCapacity"); err != nil {
		return 0, err
	}
	deps := r.snapshotDeps()

	r.mu.RLock()
	site, ok := r.sites[siteID]
	kind := DisposalKind("")
	capacity := int64(0)
	if ok {
		kind = site.kind
		capacity = site.capacity
	}
	r.mu.RUnlock()
	if !ok || kind != DisposalLandfill {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	r.ensureSiteShelf(deps.logistics, siteID, capacity)
	shelf, err := deps.logistics.Stock(siteID, market.Waste)
	if err != nil {
		return 0, err
	}
	return num.SatSub(shelf.Capacity, shelf.Level), nil
}

// BlightedCells returns the surrounding cells a full, un-reclaimed landfill
// blights (AC-8/§32 reclamation mechanic). A reclaimed landfill blights
// nothing (it has become parkland).
func (r *RefuseAPI) BlightedCells(siteID string) ([]string, error) {
	if err := r.checkNotCopied("BlightedCells"); err != nil {
		return nil, err
	}
	if err := r.requireWired("BlightedCells"); err != nil {
		return nil, err
	}
	deps := r.snapshotDeps()

	// Snapshot the site identity, the mutable reclaimed flag, and the
	// surrounding cells under ONE RLock (AC-17): CapAndReclaim writes
	// site.reclaimed under r.mu.Lock.
	r.mu.RLock()
	site, ok := r.sites[siteID]
	kind := DisposalKind("")
	reclaimed := false
	capacity := int64(0)
	surrounding := []string(nil)
	if ok {
		kind = site.kind
		reclaimed = site.reclaimed
		capacity = site.capacity
		surrounding = append([]string(nil), site.surrounding...)
	}
	r.mu.RUnlock()
	if !ok || kind != DisposalLandfill {
		return nil, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	if reclaimed {
		return nil, nil
	}
	r.ensureSiteShelf(deps.logistics, siteID, capacity)
	shelf, err := deps.logistics.Stock(siteID, market.Waste)
	if err != nil {
		return nil, nil
	}
	if shelf.Level < shelf.Capacity {
		return nil, nil // not full — no blight
	}
	return surrounding, nil
}

// EnergyOutput returns an incinerator's cumulative energy output (AC-9),
// queryable by whatever energy-accounting module later consumes it.
func (r *RefuseAPI) EnergyOutput(siteID string) (int64, error) {
	if err := r.checkNotCopied("EnergyOutput"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	site, ok := r.sites[siteID]
	if !ok || site.kind != DisposalIncinerator {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	return site.energy, nil
}

// AirshedPollution returns an incinerator's cumulative airshed-pollution
// term (AC-9) — the cost of incineration, distinct from and in addition to
// the overflow-driven PollutionExposure term of AC-7, so incineration is a
// real trade-off rather than strictly dominant over landfill.
func (r *RefuseAPI) AirshedPollution(siteID string) (float64, error) {
	if err := r.checkNotCopied("AirshedPollution"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	site, ok := r.sites[siteID]
	if !ok || site.kind != DisposalIncinerator {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	return site.airshed, nil
}

// CompostOutput returns a compost site's cumulative compost output (kg)
// (AC-10) — the exported, independently queryable food-waste→compost value
// that engine.farming consumes through the registered edge.
func (r *RefuseAPI) CompostOutput(siteID string) (int64, error) {
	if err := r.checkNotCopied("CompostOutput"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	site, ok := r.sites[siteID]
	if !ok || site.kind != DisposalCompost {
		return 0, errs.New(ErrDisposalSiteUnavailable, r.correlationID, map[string]any{"site": siteID})
	}
	return site.compost, nil
}
