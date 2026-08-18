package comms

import (
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FulfilmentCentre is the queryable record of one registered fulfilment
// centre (AC-7): the real firm it registered as (FirmID, queryable through
// engine.firms' FirmsAPI), its staff count (the "thousands of jobs" figure,
// from data/comms.json), and its premises zone class.
type FulfilmentCentre struct {
	FirmID   uint64
	Staff    int64
	Premises string
}

// LastMileDepot is the queryable record of one registered last-mile depot
// (AC-8): the real firm it registered as and the district it serves.
type LastMileDepot struct {
	FirmID   uint64
	District string
}

// fulfilmentRecord is the unexported runtime record of the (single)
// registered fulfilment centre. Reachable only through CommsAPI's exported
// methods (GR#20) — never a public field a caller could mutate.
type fulfilmentRecord struct {
	firmID   firms.FirmID
	staff    int64
	premises string
}

// fulfilmentView renders a fulfilmentRecord as its exported [FulfilmentCentre]
// view (AC-7) — the single place the record-to-view mapping lives (GR#3).
func fulfilmentView(r *fulfilmentRecord) FulfilmentCentre {
	return FulfilmentCentre{FirmID: uint64(r.firmID), Staff: r.staff, Premises: r.premises}
}

// CommsAPI is code.json's "engine.comms" inbound contract (CommsAPI,
// GUID f5f764ed-ee57-41bf-96d4-ccc82dda44ac): the §35 communications,
// internet & e-commerce module. It exposes two query surfaces:
//
//   - the ERA surface — [CommsAPI.Era] (the current connectivity era),
//     [CommsAPI.AdvanceEra] (the monotonic, one-step-at-a-time era command),
//     and [CommsAPI.CellularSubTier] (the 2G→5G overlay);
//   - the per-capability GATE surface — [CommsAPI.OfficeTierCeiling],
//     [CommsAPI.DataCentreEligible], [CommsAPI.ResearchRateModifier], and
//     [CommsAPI.RemoteWorkBaseCoefficient], each an independently-queryable
//     value per era (AC-3), plus the sector-aware [CommsAPI.RemoteWorkShare].
//
// plus the e-commerce/post surfaces ([CommsAPI.ECommerceShare],
// [CommsAPI.LetterVolume]/[CommsAPI.ParcelVolume], [CommsAPI.HighStreetDrain]/
// [CommsAPI.NetHighStreetDrain]) and the fulfilment-centre-as-firm /
// delivery-movement / postal-service surfaces.
//
// The zero value is not usable; construct via [Load] or [LoadDefault], then
// wire the stateful dependencies with [SetFirms]/[SetLogistics]/
// [SetServices] and (optionally) [SetMilestoneGate]. A *CommsAPI is safe
// for concurrent use: mutable state is guarded by mu, and checkNotCopied
// rejects a method call on a struct-copied value (SEC-020-class, mirroring
// engine.firms and engine.logistics).
type CommsAPI struct {
	mu            sync.RWMutex
	correlationID string

	cfg config

	firms     *firms.FirmsAPI
	logistics *logistics.LogisticsAPI
	services  *services.ServicesAPI
	eraGate   MilestoneGate

	era         Era
	wealth      float64
	counterplay float64
	baseLetters float64
	baseParcels float64

	fulfilment *fulfilmentRecord

	// fulfilmentPending / fulfilmentDone are the SEC-191 reservation state for
	// [CommsAPI.RegisterFulfilmentCentre]: while the firms.RegisterFirm seam runs
	// (c.mu released, SEC-173), fulfilmentPending marks the key as in-progress and
	// fulfilmentDone is closed when the registration settles, so a concurrent
	// caller observes the reservation and waits for the winner instead of passing
	// the idempotency check and minting a duplicate firm.
	fulfilmentPending bool
	fulfilmentDone    chan struct{}

	// depots is the district → registered last-mile depot index (AC-8). It is
	// the dedup/idempotency source of truth for [CommsAPI.RegisterLastMileDepot]
	// (SEC-172): a district is registered at most once, so the operating-depot
	// count is len(depots) and a duplicate registration can never mint a second
	// firm or re-provision the district's parcel shelf.
	depots map[string]LastMileDepot

	// depotsPending is the district → in-progress-registration signal index
	// (SEC-191): a district present here is mid-RegisterLastMileDepot, so a
	// concurrent caller waits on the channel rather than re-running the
	// firm/shelf seams and minting a second firm.
	depotsPending map[string]chan struct{}

	postalRegistered bool
	sortingOfficeID  services.ServiceID
	parcelHubID      services.ServiceID

	// postalPending / postalDone are the SEC-191 reservation state for
	// [CommsAPI.RegisterPostalServices], mirroring fulfilmentPending/fulfilmentDone:
	// while the services seams run, a concurrent caller waits rather than
	// re-running the kind/service registrations.
	postalPending bool
	postalDone    chan struct{}

	// self is the SEC-020 copy guard, stored exactly once in Load before
	// the value is returned to any caller (mirroring engine.firms/engine.logistics).
	self atomic.Pointer[CommsAPI]
}

// Load reads and validates data/comms.json from dir and returns a ready
// *CommsAPI with era=EraTelephoneExchange and no registered infrastructure.
// correlationID is attached to every error this call (and the returned API's
// methods) construct (GR#1). Every failure is a registry-sourced *errs.E —
// never a silent default substitution, never a panic. The firms/logistics/
// services dependencies are wired later via the Set* setters.
func Load(dir, correlationID string) (*CommsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	cfg, err := LoadConfig(filepath.Join(dir, fileComms), correlationID)
	if err != nil {
		return nil, err
	}
	c := &CommsAPI{
		correlationID: correlationID,
		cfg:           cfg,
		era:           EraTelephoneExchange,
		baseLetters:   cfg.post.BaseLetters,
		baseParcels:   cfg.post.BaseParcels,
		depots:        make(map[string]LastMileDepot),
	}
	c.self.Store(c)
	return c, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*CommsAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// commsErr builds a registry-sourced error under the API's correlation ID
// (GR#7/GR#1). It is a package-level function (not a method) so checkNotCopied
// can call it without recursing.
func commsErr(correlationID, code string, ctx map[string]any) *errs.E {
	return errs.New(code, correlationID, ctx)
}

// checkNotCopied rejects a method call on a struct-copied *CommsAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and safe to
// run before mu is ever touched.
func (c *CommsAPI) checkNotCopied(method string) error {
	if c.self.Load() != c {
		return commsErr(c.correlationID, ErrCopiedValue, map[string]any{"method": method})
	}
	return nil
}

// SetFirms wires the engine.firms dependency (AC-7's fulfilment-centre-as-
// real-firm registration).
func (c *CommsAPI) SetFirms(f *firms.FirmsAPI) error {
	if err := c.checkNotCopied("SetFirms"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.firms = f
	return nil
}

// SetLogistics wires the engine.logistics dependency (AC-8's delivery
// movements).
func (c *CommsAPI) SetLogistics(l *logistics.LogisticsAPI) error {
	if err := c.checkNotCopied("SetLogistics"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logistics = l
	return nil
}

// SetServices wires the engine.services dependency (US-5's postal service).
func (c *CommsAPI) SetServices(s *services.ServicesAPI) error {
	if err := c.checkNotCopied("SetServices"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services = s
	return nil
}

// SetMilestoneGate installs the §4 milestone gate AdvanceEra consults (the
// "era gates from unlock trees" pattern — engine.unlocks implements
// [MilestoneGate], so the composition root wires it here). A nil gate means
// "no milestone state available": AdvanceEra then fails closed for any era
// whose milestone tier is > 0 (AC-4/SEC-095 shape).
func (c *CommsAPI) SetMilestoneGate(g MilestoneGate) error {
	if err := c.checkNotCopied("SetMilestoneGate"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eraGate = g
	return nil
}

// Era returns the current connectivity era (AC-1's current-era query).
func (c *CommsAPI) Era() Era {
	if err := c.checkNotCopied("Era"); err != nil {
		return EraTelephoneExchange
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.era
}

// AdvanceEra advances the connectivity era to target. The ladder is
// monotonic and one-step-at-a-time (AC-2): target must be the era
// immediately after the current one — advancing from broadband directly to
// submarine cable (skipping fibre and cellular) is rejected with ErrEraSkip.
// A target outside the six-era enum is rejected with ErrInvalidEra (no
// zero-value era state is created). A target whose milestone tier is not
// reached (or no milestone gate is wired) is rejected with ErrNotUnlocked.
// Reaching the current era is an idempotent no-op.
func (c *CommsAPI) AdvanceEra(target Era) error {
	if err := c.checkNotCopied("AdvanceEra"); err != nil {
		return err
	}
	if !validEra(target) {
		return commsErr(c.correlationID, ErrInvalidEra, map[string]any{"era": uint8(target)})
	}

	// Snapshot comms' own state under the lock, then release before the
	// milestone-gate seam (FEAT-135: never hold c.mu across a callback that can
	// synchronously re-enter — a MilestoneGate that queries comms would deadlock
	// on RLock otherwise, SEC-173).
	c.mu.Lock()
	cur := c.era
	gate := c.eraGate
	c.mu.Unlock()

	// The idempotent no-op and era-skip checks are pure functions of (cur,
	// target); the milestone tier is a pure function of target (config is
	// immutable after Load). All three are evaluated with no lock held.
	if target == cur {
		return nil
	}
	if target != cur+1 {
		return commsErr(c.correlationID, ErrEraSkip, map[string]any{
			"target":  target.String(),
			"current": cur.String(),
		})
	}
	if tier := c.cfg.eras[target].Tier; tier > 0 {
		if gate == nil || !gate.MilestoneReached(tier) {
			return commsErr(c.correlationID, ErrNotUnlocked, map[string]any{
				"target": target.String(),
				"tier":   tier,
			})
		}
	}

	// Re-acquire to commit, re-validating the monotonic one-step ladder because
	// c.era may have moved while the gate callback ran (a concurrent advance can
	// overtake this one; the stale advance then reports the same ErrEraSkip a
	// fresh call would).
	c.mu.Lock()
	defer c.mu.Unlock()
	if target == c.era {
		return nil
	}
	if target != c.era+1 {
		return commsErr(c.correlationID, ErrEraSkip, map[string]any{
			"target":  target.String(),
			"current": c.era.String(),
		})
	}
	c.era = target
	return nil
}

// CellularSubTier returns the current era's 2G→5G cellular coverage
// sub-tier (0 for a pre-cellular era). It carries §35's "cellular masts
// (2G→5G coverage overlay)" refinement on the single EraCellular rung.
func (c *CommsAPI) CellularSubTier() int {
	if err := c.checkNotCopied("CellularSubTier"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.eras[c.era].CellularSubTier
}

// eraConfigFor resolves one era's immutable gate record, or ErrInvalidEra.
// Lock-free — the config is immutable after Load.
func (c *CommsAPI) eraConfigFor(era Era) (eraConfig, error) {
	if err := c.checkNotCopied("eraConfigFor"); err != nil {
		return eraConfig{}, err
	}
	if !validEra(era) {
		return eraConfig{}, commsErr(c.correlationID, ErrInvalidEra, map[string]any{"era": uint8(era)})
	}
	return c.cfg.eras[era], nil
}

// OfficeTierCeiling returns the office-tier ceiling gate for era (AC-3's
// first capability gate). An era outside the six-era ladder is rejected
// with ErrInvalidEra, never a silently-created zero-value gate.
func (c *CommsAPI) OfficeTierCeiling(era Era) (int, error) {
	if err := c.checkNotCopied("OfficeTierCeiling"); err != nil {
		return 0, err
	}
	cfg, err := c.eraConfigFor(era)
	if err != nil {
		return 0, err
	}
	return cfg.OfficeTierCeiling, nil
}

// DataCentreEligible returns the data-centre eligibility gate for era
// (AC-3's second capability gate) — a step function that flips true at
// fibre and stays true, independent of the continuously-rising research
// modifier. An era outside the ladder is rejected with ErrInvalidEra.
func (c *CommsAPI) DataCentreEligible(era Era) (bool, error) {
	if err := c.checkNotCopied("DataCentreEligible"); err != nil {
		return false, err
	}
	cfg, err := c.eraConfigFor(era)
	if err != nil {
		return false, err
	}
	return cfg.DataCentreEligible, nil
}

// ResearchRateModifier returns the university research-rate modifier gate
// for era (AC-3's third capability gate). An era outside the ladder is
// rejected with ErrInvalidEra.
func (c *CommsAPI) ResearchRateModifier(era Era) (float64, error) {
	if err := c.checkNotCopied("ResearchRateModifier"); err != nil {
		return 0, err
	}
	cfg, err := c.eraConfigFor(era)
	if err != nil {
		return 0, err
	}
	return cfg.ResearchRateModifier, nil
}

// RemoteWorkBaseCoefficient returns the remote-work-share coefficient gate
// for era (AC-3's fourth capability gate): the era-level base share before
// the sector affinity is applied. An era outside the ladder is rejected
// with ErrInvalidEra.
func (c *CommsAPI) RemoteWorkBaseCoefficient(era Era) (float64, error) {
	if err := c.checkNotCopied("RemoteWorkBaseCoefficient"); err != nil {
		return 0, err
	}
	cfg, err := c.eraConfigFor(era)
	if err != nil {
		return 0, err
	}
	return cfg.RemoteWorkBase, nil
}

// RemoteWorkShare returns the remote-work share for era and sector (AC-4):
// the era's remote-work base coefficient scaled by the sector's documented
// remote-work affinity (data/comms.json's "sectors" — §35's "personality/
// sector-dependent slice"), clamped to [0,1]. Two sectors with different
// affinities therefore produce different shares at the SAME era. An era
// outside the ladder is ErrInvalidEra; a sector outside the five buckets is
// ErrUnknownSector.
func (c *CommsAPI) RemoteWorkShare(era Era, sector Sector) (float64, error) {
	if err := c.checkNotCopied("RemoteWorkShare"); err != nil {
		return 0, err
	}
	cfg, err := c.eraConfigFor(era)
	if err != nil {
		return 0, err
	}
	if !validSector(sector) {
		return 0, commsErr(c.correlationID, ErrUnknownSector, map[string]any{"sector": uint8(sector)})
	}
	return clamp01(cfg.RemoteWorkBase * c.cfg.sectorAffinity[sector]), nil
}

// SetWealth sets the city wealth index driving e-commerce share and parcel
// volume. Domain [0,1] (0-100%); a non-finite value is ErrNonFinite, a
// value outside [0,1] is ErrOutOfRange — never silently clamped (AC-11).
func (c *CommsAPI) SetWealth(v float64) error {
	if err := c.checkNotCopied("SetWealth"); err != nil {
		return err
	}
	if !num.IsFinite(v) {
		return commsErr(c.correlationID, ErrNonFinite, map[string]any{"field": "wealth"})
	}
	if v < 0 || v > 1 {
		return commsErr(c.correlationID, ErrOutOfRange, map[string]any{"field": "wealth", "value": v})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wealth = v
	return nil
}

// SetCounterplayOffset sets the high-street-drain counterplay offset input
// (AC-9, §41 café-culture vitality / entertainment-zoning density /
// pedestrianisation). Domain [0,1]; non-finite is ErrNonFinite, outside
// [0,1] is ErrOutOfRange — never silently clamped (AC-11).
func (c *CommsAPI) SetCounterplayOffset(v float64) error {
	if err := c.checkNotCopied("SetCounterplayOffset"); err != nil {
		return err
	}
	if !num.IsFinite(v) {
		return commsErr(c.correlationID, ErrNonFinite, map[string]any{"field": "counterplayOffset"})
	}
	if v < 0 || v > 1 {
		return commsErr(c.correlationID, ErrOutOfRange, map[string]any{"field": "counterplayOffset", "value": v})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counterplay = v
	return nil
}

// SetPostalVolumes sets the external base letter and parcel volumes for the
// current period (the absolute postal scale comms does not derive from
// population — a consumer supplies it). Both must be non-negative; a
// negative value is ErrOutOfRange and a non-finite value is ErrNonFinite,
// never silently clamped or ignored (AC-11).
func (c *CommsAPI) SetPostalVolumes(letters, parcels float64) error {
	if err := c.checkNotCopied("SetPostalVolumes"); err != nil {
		return err
	}
	if !num.IsFinite(letters) {
		return commsErr(c.correlationID, ErrNonFinite, map[string]any{"field": "letters"})
	}
	if !num.IsFinite(parcels) {
		return commsErr(c.correlationID, ErrNonFinite, map[string]any{"field": "parcels"})
	}
	if letters < 0 {
		return commsErr(c.correlationID, ErrOutOfRange, map[string]any{"field": "letters", "value": letters})
	}
	if parcels < 0 {
		return commsErr(c.correlationID, ErrOutOfRange, map[string]any{"field": "parcels", "value": parcels})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseLetters = letters
	c.baseParcels = parcels
	return nil
}

// rawShareLocked computes the unconstrained e-commerce retail share from
// (era, wealth) — the §35 "share of retail demand shifts online as
// connectivity + wealth rise" curve, clamped to [0,1]. The caller holds
// c.mu (read or write).
func (c *CommsAPI) rawShareLocked() float64 {
	ec := c.cfg.eCommerce
	era := c.cfg.eras[c.era]
	return clamp01(ec.ShareBase + ec.ShareWealthWeight*era.Connectivity*c.wealth)
}

// effectiveShareLocked computes the infrastructure-capped e-commerce share:
// without a registered fulfilment centre AND at least one last-mile depot,
// the share cannot exceed the no-infrastructure floor (AC-6). The caller
// holds c.mu.
func (c *CommsAPI) effectiveShareLocked() float64 {
	raw := c.rawShareLocked()
	infra := c.fulfilment != nil && len(c.depots) >= 1
	if !infra && raw > c.cfg.eCommerce.NoInfrastructureFloor {
		return c.cfg.eCommerce.NoInfrastructureFloor
	}
	return raw
}

// ECommerceRawShare returns the unconstrained e-commerce retail share — the
// modelled coefficient from (era, wealth) BEFORE the fulfilment-centre
// infrastructure floor is applied. Exposed alongside [CommsAPI.ECommerceShare]
// so the infrastructure cap is provably binding, not a described-but-inert
// clamp.
func (c *CommsAPI) ECommerceRawShare() float64 {
	if err := c.checkNotCopied("ECommerceRawShare"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rawShareLocked()
}

// ECommerceShare returns the e-commerce share of retail demand (AC-6): the
// modelled (era, wealth) coefficient, capped at the no-infrastructure floor
// while no fulfilment centre and last-mile depot are operational. A share
// above the floor with no fulfilment centre registered is therefore
// impossible through this accessor — the cap is enforced, never silently
// bypassed.
func (c *CommsAPI) ECommerceShare() float64 {
	if err := c.checkNotCopied("ECommerceShare"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.effectiveShareLocked()
}

// LetterVolume returns the current letter volume: the base letter scale
// scaled by the current era's letter factor, which declines as era advances
// (§35's "managed-decline" story, AC-5). Letter volume does NOT respond to
// e-commerce share.
func (c *CommsAPI) LetterVolume() float64 {
	if err := c.checkNotCopied("LetterVolume"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return satFinite(c.baseLetters * c.cfg.eras[c.era].LetterFactor)
}

// ParcelVolume returns the current parcel volume: the base parcel scale
// scaled by the era's parcel factor, the wealth sensitivity, and the
// e-commerce-share sensitivity — so parcels grow with wealth, era, and
// e-commerce share (AC-5), independently of the letter series.
func (c *CommsAPI) ParcelVolume() float64 {
	if err := c.checkNotCopied("ParcelVolume"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	era := c.cfg.eras[c.era]
	share := c.effectiveShareLocked()
	po := c.cfg.post
	return satFinite(c.baseParcels * era.ParcelEraFactor *
		(1 + po.ParcelWealthSensitivity*c.wealth) *
		(1 + po.ParcelShareSensitivity*share))
}

// RegisterFulfilmentCentre registers a fulfilment centre as a REAL firm
// through engine.firms (AC-7) — thousands of jobs (data/comms.json's
// fulfilment.staff) and a real premises zone class, never a comms-owned
// pseudo-employer. It requires engine.firms to be wired (ErrNoFirmRef
// otherwise, AC-10), and rejects a duplicate with the firm already
// registered. On success the fulfilment centre is queryable through
// engine.firms' FirmsAPI and through [CommsAPI.FulfilmentCentre].
func (c *CommsAPI) RegisterFulfilmentCentre() (FulfilmentCentre, error) {
	if err := c.checkNotCopied("RegisterFulfilmentCentre"); err != nil {
		return FulfilmentCentre{}, err
	}

	// Loop because a concurrent registration may already be in flight: the
	// reservation below (SEC-191) makes a same-key caller wait for the winner
	// instead of passing the idempotency check and minting a duplicate firm.
	for {
		c.mu.Lock()
		if c.firms == nil {
			c.mu.Unlock()
			return FulfilmentCentre{}, commsErr(c.correlationID, ErrNoFirmRef, nil)
		}
		if c.fulfilment != nil {
			// Idempotent: a second registration returns the existing centre rather
			// than minting a duplicate firm (SEC-172).
			existing := c.fulfilment
			c.mu.Unlock()
			return fulfilmentView(existing), nil
		}
		if c.fulfilmentPending {
			// A concurrent caller has reserved the key and is running the seam;
			// wait for it to settle and re-read rather than minting a second firm
			// (SEC-191: the bare double-check is not atomic).
			done := c.fulfilmentDone
			c.mu.Unlock()
			<-done
			continue
		}
		// Reserve the key BEFORE releasing c.mu (SEC-191): the reservation is
		// visible to concurrent callers for the whole seam, so exactly one caller
		// ever reaches firms.RegisterFirm.
		done := make(chan struct{})
		c.fulfilmentPending = true
		c.fulfilmentDone = done
		f := c.firms
		fc := c.cfg.fulfilment
		c.mu.Unlock()

		// The registered firm snapshot carries the real firm ID and the staff
		// count (thousands, from data). The static type is freight.Firm, but
		// engine.comms never names engine.freight (not a registered outbound
		// edge, GR#20) — the fields are read by inference. Never hold c.mu across
		// the seam (FEAT-135, SEC-173).
		reg, err := f.RegisterFirm(fc.Name, fc.Staff, fc.Premises)

		c.mu.Lock()
		if err == nil {
			c.fulfilment = &fulfilmentRecord{
				firmID:   firms.FirmID(reg.ID),
				staff:    reg.Staff,
				premises: reg.Premises,
			}
		}
		existing := c.fulfilment
		// Promote on success / clear on failure, then release the waiters.
		c.fulfilmentPending = false
		c.fulfilmentDone = nil
		close(done)
		c.mu.Unlock()
		if err != nil {
			return FulfilmentCentre{}, err
		}
		return fulfilmentView(existing), nil
	}
}

// FulfilmentCentre returns the registered fulfilment-centre record, or
// ErrFulfilmentNotRegistered when none is registered (never a zero-value
// record masquerading as a fulfilment centre — AC-10).
func (c *CommsAPI) FulfilmentCentre() (FulfilmentCentre, error) {
	if err := c.checkNotCopied("FulfilmentCentre"); err != nil {
		return FulfilmentCentre{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.fulfilment == nil {
		return FulfilmentCentre{}, commsErr(c.correlationID, ErrFulfilmentNotRegistered, nil)
	}
	return fulfilmentView(c.fulfilment), nil
}

// RegisterLastMileDepot registers a last-mile depot serving district: it
// registers as a real firm through engine.firms AND provisions the district's
// parcel stock shelf through engine.logistics (AC-8's "vans on the same real
// roads"), and records it toward the e-commerce infrastructure requirement
// (AC-6 needs at least one depot operational). It requires engine.firms
// (ErrNoFirmRef) and engine.logistics (ErrLogisticsNotWired) to be wired.
func (c *CommsAPI) RegisterLastMileDepot(district string) (LastMileDepot, error) {
	if err := c.checkNotCopied("RegisterLastMileDepot"); err != nil {
		return LastMileDepot{}, err
	}
	if district == "" {
		return LastMileDepot{}, commsErr(c.correlationID, ErrOutOfRange, map[string]any{"field": "district", "value": district})
	}

	// Loop because a concurrent registration of the SAME district may be in
	// flight: the per-district reservation (SEC-191) makes a same-key caller wait
	// for the winner instead of passing the idempotency check and minting a second
	// firm or re-provisioning the shelf.
	for {
		c.mu.Lock()
		if c.firms == nil {
			c.mu.Unlock()
			return LastMileDepot{}, commsErr(c.correlationID, ErrNoFirmRef, nil)
		}
		if c.logistics == nil {
			c.mu.Unlock()
			return LastMileDepot{}, commsErr(c.correlationID, ErrLogisticsNotWired, nil)
		}
		if existing, ok := c.depots[district]; ok {
			// Idempotent: a second registration returns the existing depot rather
			// than minting a duplicate firm or re-provisioning the shelf (SEC-172).
			c.mu.Unlock()
			return existing, nil
		}
		if done, pending := c.depotsPending[district]; pending {
			// A concurrent caller has reserved this district and is running the
			// firm/shelf seams; wait for it to settle and re-read (SEC-191).
			c.mu.Unlock()
			<-done
			continue
		}
		// Reserve the district BEFORE releasing c.mu (SEC-191), so exactly one
		// caller ever runs the seams for it.
		done := make(chan struct{})
		if c.depotsPending == nil {
			c.depotsPending = make(map[string]chan struct{})
		}
		c.depotsPending[district] = done
		f := c.firms
		l := c.logistics
		ld := c.cfg.lastMileDepot
		c.mu.Unlock()

		// The parcel commodity is one of logistics' required §6 commodities
		// (always provisionable), and the depot staff is data-validated
		// non-negative. The firm is registered first (the facility's identity),
		// then its shelf is provisioned. Never hold c.mu across the seams
		// (FEAT-135, SEC-173).
		reg, err := f.RegisterFirm(ld.Name+" "+district, ld.Staff, ld.Premises)
		if err == nil {
			_, err = l.Provision(district, parcelCommodity, ld.ShelfCapacity, ld.ShelfCapacity)
		}

		c.mu.Lock()
		var depot LastMileDepot
		if err == nil {
			depot = LastMileDepot{FirmID: uint64(firms.FirmID(reg.ID)), District: district}
			c.depots[district] = depot
		}
		// Promote on success / clear on failure, then release the waiters.
		delete(c.depotsPending, district)
		close(done)
		c.mu.Unlock()
		if err != nil {
			return LastMileDepot{}, err
		}
		return depot, nil
	}
}

// LastMileDepotCount returns the number of registered last-mile depots.
func (c *CommsAPI) LastMileDepotCount() int {
	if err := c.checkNotCopied("LastMileDepotCount"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.depots)
}

// DeliverParcels resolves one e-commerce delivery movement through
// engine.logistics (AC-8): it draws the requested parcels from the district's
// last-mile shelf via [logistics.LogisticsAPI.Draw], reusing logistics' own
// stock-draw/shortfall machinery rather than a comms-owned parallel van
// router. A request exceeding the shelf returns a shortfall (the deferred,
// next-day load — logistics' documented shortfall semantics), never a silent
// partial success. Requires engine.logistics wired (ErrLogisticsNotWired)
// and a fulfilment centre registered (ErrFulfilmentNotRegistered); a negative
// parcel count is ErrOutOfRange.
func (c *CommsAPI) DeliverParcels(district string, parcels int64) (logistics.DrawResult, error) {
	if err := c.checkNotCopied("DeliverParcels"); err != nil {
		return logistics.DrawResult{}, err
	}
	if parcels < 0 {
		return logistics.DrawResult{}, commsErr(c.correlationID, ErrOutOfRange, map[string]any{"field": "parcels", "value": parcels})
	}

	// Snapshot comms' own state under the lock, then release before the
	// logistics.Draw seam (FEAT-135, SEC-173): Draw fires subscribed
	// ShortfallHandlers synchronously, and a handler that queries comms must not
	// deadlock on a re-entrant RLock held across the call. Mirroring
	// PostalDeliveryReliability's snapshot-release-call pattern.
	c.mu.Lock()
	l := c.logistics
	hasFulfilment := c.fulfilment != nil
	c.mu.Unlock()

	if l == nil {
		return logistics.DrawResult{}, commsErr(c.correlationID, ErrLogisticsNotWired, nil)
	}
	if !hasFulfilment {
		return logistics.DrawResult{}, commsErr(c.correlationID, ErrFulfilmentNotRegistered, nil)
	}
	// parcelCommodity is an untyped string constant, so it converts to
	// logistics' market.CommodityType parameter without engine.comms importing
	// engine.market (not a registered outbound edge, GR#20). The duplication
	// of market.ConsumerGoods is held by TestParcelCommodityMatchesMarket.
	return l.Draw(district, parcelCommodity, parcels, logistics.ConsumerFirm)
}

// HighStreetDrain returns the RAW high-street drain pressure (AC-9): the
// documented function of the current e-commerce share — the higher the share
// shifts retail online, the more the high street drains. It is the
// counterplay-OFFSET-independent figure a vacancy/blight consumer subscribes
// to once BUG-058's consumer edge lands.
func (c *CommsAPI) HighStreetDrain() float64 {
	if err := c.checkNotCopied("HighStreetDrain"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return satFinite(c.cfg.drain.DrainPerShare * c.effectiveShareLocked())
}

// NetHighStreetDrain returns the NET high-street drain pressure (AC-9): the
// raw drain reduced by the counterplay offset input (§41 café-culture /
// entertainment-zoning / pedestrianisation). The offset DAMPENS but never
// ZEROES the drain — the dampening is bounded by the data's
// maxCounterplayDampening (< 1), so "nothing is free" holds even at maximum
// counterplay while e-commerce share is nonzero.
func (c *CommsAPI) NetHighStreetDrain() float64 {
	if err := c.checkNotCopied("NetHighStreetDrain"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	raw := c.cfg.drain.DrainPerShare * c.effectiveShareLocked()
	return satFinite(raw * (1 - c.counterplay*c.cfg.drain.MaxCounterplayDampening))
}

// RegisterPostalServices registers the §35 post-and-parcel infrastructure
// (sorting office + parcel hub) through engine.services' generic service
// framework (US-5) — a new "postal" kind plus two instances — so postal
// delivery reliability draws from the same funding→quality/staffing pool as
// every other public service. Requires engine.services wired
// (ErrServicesNotWired).
func (c *CommsAPI) RegisterPostalServices() error {
	if err := c.checkNotCopied("RegisterPostalServices"); err != nil {
		return err
	}

	// Loop because a concurrent registration may already be in flight: the
	// reservation below (SEC-191) makes a same-key caller wait for the winner
	// instead of re-running the kind/service seams and surfacing ErrDuplicateService.
	for {
		c.mu.Lock()
		if c.services == nil {
			c.mu.Unlock()
			return commsErr(c.correlationID, ErrServicesNotWired, nil)
		}
		if c.postalRegistered {
			// Idempotent: a second registration is a no-op rather than re-running the
			// kind/service registrations (SEC-172 class — the services seam would
			// otherwise surface a confusing ErrDuplicateService).
			c.mu.Unlock()
			return nil
		}
		if c.postalPending {
			// A concurrent caller has reserved the key and is running the seams;
			// wait for it to settle and re-read (SEC-191).
			done := c.postalDone
			c.mu.Unlock()
			<-done
			continue
		}
		// Reserve the key BEFORE releasing c.mu (SEC-191), so exactly one caller
		// ever runs the kind/service registrations.
		done := make(chan struct{})
		c.postalPending = true
		c.postalDone = done
		s := c.services
		ps := c.cfg.postal
		c.mu.Unlock()

		// Never hold c.mu across the services seams (FEAT-135, SEC-173).
		kind := services.ServiceKind(ps.Kind)
		err := s.RegisterKind(kind, services.KindDef{Name: ps.KindName})
		if err == nil {
			err = s.RegisterService(services.ServiceSpec{
				ID:             services.ServiceID(ps.SortingOffice.ID),
				Kind:           kind,
				CapacityRaw:    ps.SortingOffice.CapacityRaw,
				CoverageRadius: ps.SortingOffice.CoverageRadius,
			})
		}
		if err == nil {
			err = s.RegisterService(services.ServiceSpec{
				ID:             services.ServiceID(ps.ParcelHub.ID),
				Kind:           kind,
				CapacityRaw:    ps.ParcelHub.CapacityRaw,
				CoverageRadius: ps.ParcelHub.CoverageRadius,
			})
		}

		c.mu.Lock()
		if err == nil {
			c.postalRegistered = true
			c.sortingOfficeID = services.ServiceID(ps.SortingOffice.ID)
			c.parcelHubID = services.ServiceID(ps.ParcelHub.ID)
		}
		// Promote on success / clear on failure, then release the waiters.
		c.postalPending = false
		c.postalDone = nil
		close(done)
		c.mu.Unlock()
		return err
	}
}

// PostalDeliveryReliability returns the current postal delivery reliability
// (US-5): the realised quality of the postal infrastructure, read through
// engine.services' Quality — the minimum of the sorting-office and parcel-hub
// quality, so a funding cut to either degrades delivery reliability exactly
// as it would any other service. Requires engine.services wired and the
// postal services registered (ErrServicesNotWired / ErrFulfilmentNotRegistered).
func (c *CommsAPI) PostalDeliveryReliability() (float64, error) {
	if err := c.checkNotCopied("PostalDeliveryReliability"); err != nil {
		return 0, err
	}
	c.mu.RLock()
	s := c.services
	registered := c.postalRegistered
	sortingID := c.sortingOfficeID
	hubID := c.parcelHubID
	c.mu.RUnlock()

	if s == nil || !registered {
		return 0, commsErr(c.correlationID, ErrServicesNotWired, nil)
	}
	sortingQ, err := s.Quality(sortingID)
	if err != nil {
		return 0, err
	}
	hubQ, err := s.Quality(hubID)
	if err != nil {
		return 0, err
	}
	if hubQ < sortingQ {
		return hubQ, nil
	}
	return sortingQ, nil
}

// satFinite collapses a non-finite float64 to the nearest finite extreme so a
// finite input can never leak ±Inf/NaN from a computed output (GR#16): NaN
// collapses to 0, +Inf to math.MaxFloat64, and -Inf to -math.MaxFloat64, while
// a finite value passes through unchanged. It is the float counterpart of the
// integer saturation in [num.SatAdd]/[num.SafeMul], applied to the package's
// multiplicative volume/drain outputs whose factors can exceed 1 (SEC-171).
func satFinite(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case math.IsInf(v, 1):
		return math.MaxFloat64
	case math.IsInf(v, -1):
		return -math.MaxFloat64
	default:
		return v
	}
}

// clamp01 clamps v into [0,1] (GR#16: a non-finite value collapses to 0,
// never leaks +Inf/NaN from a finite input — mirroring services.clamp01).
func clamp01(v float64) float64 {
	if !num.IsFinite(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
